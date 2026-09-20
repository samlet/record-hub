package connector

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
)

var (
	ErrOnboardingNotFound = errors.New("connector onboarding record not found")
	ErrOnboardingExists   = errors.New("connector onboarding record already exists")
	ErrOnboardingState    = errors.New("connector onboarding state transition is invalid")
	ErrOnboardingEvidence = errors.New("connector onboarding evidence is incomplete")
)

var sourceCommitPattern = regexp.MustCompile(`^[a-f0-9]{7,64}$`)

type OnboardingEvidence struct {
	FixtureDigest         string `json:"fixtureDigest"`
	RedactionPolicyDigest string `json:"redactionPolicyDigest"`
	SourceCommit          string `json:"sourceCommit"`
	CompatibilityPass     bool   `json:"compatibilityPass"`
	FixturePass           bool   `json:"fixturePass"`
	RedactionPass         bool   `json:"redactionPass"`
	OperatorAudit         bool   `json:"operatorAudit"`
	OwnerSignoff          bool   `json:"ownerSignoff"`
	Reviewer              string `json:"reviewer,omitempty"`
	Approver              string `json:"approver,omitempty"`
}

type OnboardingRecord struct {
	TenantID    string               `json:"tenantId"`
	WorkspaceID string               `json:"workspaceId"`
	Manifest    Manifest             `json:"manifest"`
	Evidence    OnboardingEvidence   `json:"evidence"`
	Submitter   identity.IdentityKey `json:"submitter"`
	CreatedAt   time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
}

type OnboardingService struct {
	mu         sync.RWMutex
	entries    map[string]OnboardingRecord
	authorizer *identity.Authorizer
	audit      audit.Writer
	clock      func() time.Time
}

func NewOnboardingService(authorizer *identity.Authorizer, auditWriter audit.Writer) *OnboardingService {
	return &OnboardingService{entries: make(map[string]OnboardingRecord), authorizer: authorizer, audit: auditWriter, clock: time.Now}
}

func (service *OnboardingService) Submit(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, manifest Manifest, evidence OnboardingEvidence) (OnboardingRecord, error) {
	if err := service.authorize(ctx, principal, tenantID, workspaceID, identity.ActionConnectorOnboard); err != nil {
		return OnboardingRecord{}, err
	}
	if service == nil || service.audit == nil {
		return OnboardingRecord{}, identity.ErrForbidden
	}
	normalized, err := validateOnboardingManifest(manifest, evidence)
	if err != nil {
		return OnboardingRecord{}, err
	}
	key := onboardingKey(tenantID, workspaceID, normalized.Key)
	service.mu.Lock()
	if _, exists := service.entries[key]; exists {
		service.mu.Unlock()
		return OnboardingRecord{}, ErrOnboardingExists
	}
	now := service.clock().UTC()
	record := OnboardingRecord{TenantID: strings.TrimSpace(tenantID), WorkspaceID: strings.TrimSpace(workspaceID), Manifest: normalized, Evidence: evidence, Submitter: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	service.entries[key] = record
	service.mu.Unlock()
	if err := service.appendAudit(ctx, principal, record, "connector.onboarding.submit", ""); err != nil {
		return OnboardingRecord{}, err
	}
	return record, nil
}

func (service *OnboardingService) SubmitForReview(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, key Key, expectedRevision int64) (OnboardingRecord, error) {
	return service.transition(ctx, principal, tenantID, workspaceID, key, expectedRevision, StatusInReview, identity.ActionConnectorOnboard, func(record *OnboardingRecord) error {
		if record.Manifest.Status != StatusDraft {
			return ErrOnboardingState
		}
		return nil
	})
}

func (service *OnboardingService) Review(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, key Key, expectedRevision int64, approved bool) (OnboardingRecord, error) {
	status := StatusRejected
	if approved {
		status = StatusApproved
	}
	return service.transition(ctx, principal, tenantID, workspaceID, key, expectedRevision, status, identity.ActionConnectorReview, func(record *OnboardingRecord) error {
		if record.Manifest.Status != StatusInReview || record.Submitter == principal.IdentityKey() {
			return ErrOnboardingState
		}
		record.Evidence.Reviewer = principal.IdentityKey().Subject
		return nil
	})
}

func (service *OnboardingService) Enable(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, key Key, expectedRevision int64) (OnboardingRecord, error) {
	return service.transition(ctx, principal, tenantID, workspaceID, key, expectedRevision, StatusEnabled, identity.ActionConnectorApprove, func(record *OnboardingRecord) error {
		if record.Manifest.Status != StatusApproved || record.Submitter == principal.IdentityKey() || record.Evidence.Reviewer == principal.IdentityKey().Subject || !record.Evidence.CompatibilityPass || !record.Evidence.FixturePass || !record.Evidence.RedactionPass || !record.Evidence.OperatorAudit {
			return ErrOnboardingEvidence
		}
		record.Evidence.OwnerSignoff = true
		record.Evidence.Approver = principal.IdentityKey().Subject
		return nil
	})
}

func (service *OnboardingService) Disable(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, key Key, expectedRevision int64) (OnboardingRecord, error) {
	return service.transition(ctx, principal, tenantID, workspaceID, key, expectedRevision, StatusDisabled, identity.ActionConnectorApprove, func(record *OnboardingRecord) error {
		if record.Manifest.Status != StatusEnabled {
			return ErrOnboardingState
		}
		return nil
	})
}

func (service *OnboardingService) List(ctx context.Context, principal identity.Principal, tenantID, workspaceID string) ([]OnboardingRecord, error) {
	if err := service.authorize(ctx, principal, tenantID, workspaceID, identity.ActionConnectorReview); err != nil {
		return nil, err
	}
	service.mu.RLock()
	result := make([]OnboardingRecord, 0)
	for _, record := range service.entries {
		if record.TenantID == tenantID && record.WorkspaceID == workspaceID {
			result = append(result, record)
		}
	}
	service.mu.RUnlock()
	sort.Slice(result, func(left, right int) bool {
		return result[left].Manifest.Key.Connector < result[right].Manifest.Key.Connector
	})
	return result, nil
}

func (service *OnboardingService) transition(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, key Key, expectedRevision int64, status Status, action identity.Action, check func(*OnboardingRecord) error) (OnboardingRecord, error) {
	if err := service.authorize(ctx, principal, tenantID, workspaceID, action); err != nil {
		return OnboardingRecord{}, err
	}
	normalized, err := key.normalized()
	if err != nil {
		return OnboardingRecord{}, err
	}
	lookup := onboardingKey(tenantID, workspaceID, normalized)
	service.mu.Lock()
	record, exists := service.entries[lookup]
	if !exists {
		service.mu.Unlock()
		return OnboardingRecord{}, ErrOnboardingNotFound
	}
	if record.Manifest.Revision != expectedRevision {
		service.mu.Unlock()
		return OnboardingRecord{}, ErrRevisionConflict
	}
	if err := check(&record); err != nil {
		service.mu.Unlock()
		return OnboardingRecord{}, err
	}
	record.Manifest.Status = status
	record.Manifest.Revision++
	record.UpdatedAt = service.clock().UTC()
	service.entries[lookup] = record
	service.mu.Unlock()
	if err := service.appendAudit(ctx, principal, record, "connector.onboarding."+strings.ToLower(string(status)), ""); err != nil {
		return OnboardingRecord{}, err
	}
	return record, nil
}

func (service *OnboardingService) authorize(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) error {
	if service == nil || service.authorizer == nil || service.clock == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workspaceID) == "" {
		return identity.ErrForbidden
	}
	_, err := service.authorizer.Authorize(ctx, principal, tenantID, workspaceID, action)
	return err
}

func (service *OnboardingService) appendAudit(ctx context.Context, principal identity.Principal, record OnboardingRecord, action, beforeHash string) error {
	data, _ := json.Marshal(record)
	digest := sha256.Sum256(data)
	return service.audit.Append(ctx, audit.Entry{TenantID: record.TenantID, WorkspaceID: record.WorkspaceID, Action: action, Actor: principal.IdentityKey(), ResourceType: "connector_manifest", ResourceID: onboardingKey(record.TenantID, record.WorkspaceID, record.Manifest.Key), ResourceVersion: record.Manifest.Revision, BeforeHash: beforeHash, AfterHash: fmt.Sprintf("sha256:%x", digest[:]), CreatedAt: service.clock().UTC()})
}

func validateOnboardingManifest(manifest Manifest, evidence OnboardingEvidence) (Manifest, error) {
	if manifest.Status != "" && manifest.Status != StatusDraft {
		return Manifest{}, fmt.Errorf("%w: onboarding upload must start as draft", ErrInvalidManifest)
	}
	if manifest.Revision != 0 {
		return Manifest{}, fmt.Errorf("%w: onboarding revision is server-assigned", ErrInvalidManifest)
	}
	if strings.TrimSpace(evidence.FixtureDigest) == "" {
		evidence.FixtureDigest = manifest.FixtureDigest
	}
	if strings.TrimSpace(evidence.RedactionPolicyDigest) == "" {
		evidence.RedactionPolicyDigest = manifest.RedactionPolicyDigest
	}
	if strings.TrimSpace(evidence.SourceCommit) == "" {
		evidence.SourceCommit = manifest.SourceCommit
	}
	for _, digest := range []string{evidence.FixtureDigest, evidence.RedactionPolicyDigest} {
		if !hashPattern.MatchString(strings.ToLower(strings.TrimSpace(digest))) {
			return Manifest{}, fmt.Errorf("%w: evidence digests must be sha256", ErrInvalidManifest)
		}
	}
	if !sourceCommitPattern.MatchString(strings.ToLower(strings.TrimSpace(evidence.SourceCommit))) {
		return Manifest{}, fmt.Errorf("%w: sourceCommit must be a git SHA", ErrInvalidManifest)
	}
	normalized, err := manifest.normalized()
	if err != nil {
		return Manifest{}, err
	}
	normalized.FixtureDigest = strings.ToLower(strings.TrimSpace(evidence.FixtureDigest))
	normalized.RedactionPolicyDigest = strings.ToLower(strings.TrimSpace(evidence.RedactionPolicyDigest))
	normalized.SourceCommit = strings.ToLower(strings.TrimSpace(evidence.SourceCommit))
	normalized.Status = StatusDraft
	normalized.Revision = 1
	return normalized, nil
}

func onboardingKey(tenantID, workspaceID string, key Key) string {
	return strings.Join([]string{tenantID, workspaceID, key.Connector, key.Event, fmt.Sprint(key.SchemaVersion)}, "\x00")
}
