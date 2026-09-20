package connector

import (
	"context"
	"errors"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type onboardingMembershipReader struct {
	memberships map[identity.IdentityKey]identity.WorkspaceMembership
}

func (reader onboardingMembershipReader) FindMembership(_ context.Context, principal identity.IdentityKey, tenantID, workspaceID string) (identity.WorkspaceMembership, error) {
	membership, ok := reader.memberships[principal]
	if !ok || membership.TenantID != tenantID || membership.WorkspaceID != workspaceID {
		return identity.WorkspaceMembership{}, identity.ErrMembershipGone
	}
	return membership, nil
}

type onboardingAuditWriter struct{ entries []audit.Entry }

func (writer *onboardingAuditWriter) Append(_ context.Context, entry audit.Entry) error {
	writer.entries = append(writer.entries, entry)
	return nil
}

func TestOnboardingLifecycleRequiresIndependentReviewAndCAS(t *testing.T) {
	owner := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	secondOwner := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-2"}
	operator := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "operator"}
	reader := onboardingMembershipReader{memberships: map[identity.IdentityKey]identity.WorkspaceMembership{}}
	for _, item := range []struct {
		principal identity.Principal
		role      identity.Role
	}{
		{owner, identity.RoleOwner}, {secondOwner, identity.RoleOwner}, {operator, identity.RoleOperator},
	} {
		reader.memberships[item.principal.IdentityKey()] = identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: item.principal.IdentityKey(), Role: item.role, Status: identity.MembershipActive}
	}
	audits := &onboardingAuditWriter{}
	service := NewOnboardingService(identity.NewAuthorizer(reader), audits)
	manifest := Manifest{Key: Key{Connector: "fluxion.approval", Event: "dispatch.result", SchemaVersion: 1}, OwnerSystem: "approver", SDKVersion: "1.2.0", CompatibilityMin: "1.0.0", CompatibilityMax: "1.9.9", ContractHash: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", AllowedFields: []string{"decision"}}
	evidence := OnboardingEvidence{FixtureDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RedactionPolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceCommit: "0123456789abcdef0123456789abcdef01234567", CompatibilityPass: true, FixturePass: true, RedactionPass: true, OperatorAudit: true}
	ctx := context.Background()
	record, err := service.Submit(ctx, owner, "tenant-1", "workspace-1", manifest, evidence)
	if err != nil || record.Manifest.Status != StatusDraft || record.Manifest.Revision != 1 {
		t.Fatalf("submit = %#v, %v", record, err)
	}
	if _, err := service.SubmitForReview(ctx, owner, "tenant-1", "workspace-1", manifest.Key, 99); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale submit-for-review = %v", err)
	}
	record, err = service.SubmitForReview(ctx, owner, "tenant-1", "workspace-1", manifest.Key, 1)
	if err != nil || record.Manifest.Status != StatusInReview || record.Manifest.Revision != 2 {
		t.Fatalf("submit-for-review = %#v, %v", record, err)
	}
	if _, err := service.Enable(ctx, owner, "tenant-1", "workspace-1", manifest.Key, 2); !errors.Is(err, ErrOnboardingEvidence) {
		t.Fatalf("same owner enable = %v", err)
	}
	record, err = service.Review(ctx, operator, "tenant-1", "workspace-1", manifest.Key, 2, true)
	if err != nil || record.Manifest.Status != StatusApproved || record.Manifest.Revision != 3 {
		t.Fatalf("review = %#v, %v", record, err)
	}
	record, err = service.Enable(ctx, secondOwner, "tenant-1", "workspace-1", manifest.Key, 3)
	if err != nil || record.Manifest.Status != StatusEnabled || record.Manifest.Revision != 4 || len(audits.entries) != 4 {
		t.Fatalf("enable = %#v audits=%d err=%v", record, len(audits.entries), err)
	}
}
