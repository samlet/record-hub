package identity

import (
	"context"
	"errors"
	"testing"
)

func TestPrincipalRotationLifecycleAuditsHashesOnly(t *testing.T) {
	audit := &MemoryRotationAudit{}
	service := NewPrincipalRotationService(audit)
	oldHash := HashPrincipal("https://dex.example", "approver-old")
	newHash := HashPrincipal("https://dex.example", "approver-new")
	scope := WorkloadScope{TenantID: "tenant-1", WorkspaceID: "workspace-1", OwnerSystem: "approver", ResourceType: "Application"}

	rotation, err := service.CreateCandidate(context.Background(), CreateCandidateRequest{Owner: "approver", Scope: scope, Audience: "record-hub", OldPrincipalHash: oldHash, NewPrincipalHash: newHash, Actor: "operator-1", Reason: "scheduled rotation"})
	if err != nil || rotation.NewState != WorkloadPrincipalCandidate || rotation.Revision != 1 {
		t.Fatalf("create candidate = %+v, err=%v", rotation, err)
	}
	if _, err := service.DualAccept(context.Background(), "approver", "operator-1", "dual acceptance"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DrainOld(context.Background(), "approver", "operator-1", "drain old principal"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetInFlight(context.Background(), "approver", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RevokeOld(context.Background(), "approver", "operator-1", "revoke old principal"); !errors.Is(err, ErrRotationInFlight) {
		t.Fatalf("revoke with in-flight err=%v", err)
	}
	if _, err := service.SetInFlight(context.Background(), "approver", 0); err != nil {
		t.Fatal(err)
	}
	rotation, err = service.RevokeOld(context.Background(), "approver", "operator-1", "revoke old principal")
	if err != nil || rotation.OldState != WorkloadPrincipalRevoked || rotation.NewState != WorkloadPrincipalActive {
		t.Fatalf("revoke = %+v, err=%v", rotation, err)
	}

	entries := audit.Entries()
	if len(entries) != 4 {
		t.Fatalf("audit entries = %d, want 4", len(entries))
	}
	for _, entry := range entries {
		if entry.OldPrincipalHash != oldHash || entry.NewPrincipalHash != newHash || entry.OccurredAt.IsZero() || entry.Actor == "" || entry.Reason == "" {
			t.Fatalf("unsafe/incomplete audit entry: %+v", entry)
		}
	}
}

func TestPrincipalRotationRejectsWildcardScopeAndAuditFailure(t *testing.T) {
	audit := &MemoryRotationAudit{}
	service := NewPrincipalRotationService(audit)
	request := CreateCandidateRequest{Owner: "fluxion", Scope: WorkloadScope{TenantID: "tenant-*", WorkspaceID: "workspace-1", OwnerSystem: "fluxion", ResourceType: "Project"}, Audience: "record-hub", OldPrincipalHash: HashPrincipal("issuer", "old"), NewPrincipalHash: HashPrincipal("issuer", "new"), Actor: "operator-1", Reason: "test"}
	if _, err := service.CreateCandidate(context.Background(), request); !errors.Is(err, ErrRotationInvalidScope) && !errors.Is(err, ErrRotationInvalidInput) {
		t.Fatalf("wildcard scope err=%v", err)
	}
	audit.SetError(errors.New("immutable store unavailable"))
	request.Scope.TenantID = "tenant-1"
	if _, err := service.CreateCandidate(context.Background(), request); !errors.Is(err, ErrRotationAudit) {
		t.Fatalf("audit failure err=%v", err)
	}
	if _, err := service.Get("fluxion"); !errors.Is(err, ErrRotationNotFound) {
		t.Fatalf("failed audit must not persist rotation, err=%v", err)
	}
}
