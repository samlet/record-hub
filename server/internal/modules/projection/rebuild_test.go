package projection

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type memoryRebuildStore struct {
	values map[string]ProjectionRebuildOperation
	err    error
}

func newMemoryRebuildStore() *memoryRebuildStore {
	return &memoryRebuildStore{values: make(map[string]ProjectionRebuildOperation)}
}

func (store *memoryRebuildStore) Create(_ context.Context, operation ProjectionRebuildOperation) error {
	if store.err != nil {
		return store.err
	}
	key := operation.TenantID + "\x00" + operation.WorkspaceID + "\x00" + operation.ID
	if _, exists := store.values[key]; exists {
		return ErrRebuildExists
	}
	store.values[key] = operation
	return nil
}

func (store *memoryRebuildStore) Find(_ context.Context, tenantID, workspaceID, operationID string) (ProjectionRebuildOperation, error) {
	if store.err != nil {
		return ProjectionRebuildOperation{}, store.err
	}
	operation, ok := store.values[tenantID+"\x00"+workspaceID+"\x00"+operationID]
	if !ok {
		return ProjectionRebuildOperation{}, ErrRebuildNotFound
	}
	return operation, nil
}

func (store *memoryRebuildStore) ListRunnable(_ context.Context, _ int64) ([]ProjectionRebuildOperation, error) {
	if store.err != nil {
		return nil, store.err
	}
	result := make([]ProjectionRebuildOperation, 0)
	for _, operation := range store.values {
		switch operation.Status {
		case RebuildAccepted, RebuildRunning, RebuildCancelRequested:
			result = append(result, operation)
		}
	}
	return result, nil
}

func (store *memoryRebuildStore) SaveCAS(_ context.Context, operation ProjectionRebuildOperation, expectedRevision int64) (ProjectionRebuildOperation, error) {
	if store.err != nil {
		return ProjectionRebuildOperation{}, store.err
	}
	key := operation.TenantID + "\x00" + operation.WorkspaceID + "\x00" + operation.ID
	current, ok := store.values[key]
	if !ok {
		return ProjectionRebuildOperation{}, ErrRebuildNotFound
	}
	if current.Revision != expectedRevision {
		return ProjectionRebuildOperation{}, ErrRebuildRevisionConflict
	}
	operation.Revision = expectedRevision + 1
	if err := operation.Validate(); err != nil {
		return ProjectionRebuildOperation{}, err
	}
	store.values[key] = operation
	return operation, nil
}

type memoryRebuildReceipts struct{ values map[string]RebuildReceipt }

func newMemoryRebuildReceipts() *memoryRebuildReceipts {
	return &memoryRebuildReceipts{values: make(map[string]RebuildReceipt)}
}

func (store *memoryRebuildReceipts) Find(_ context.Context, tenantID, workspaceID, operation, key string) (RebuildReceipt, error) {
	receipt, ok := store.values[tenantID+"\x00"+workspaceID+"\x00"+operation+"\x00"+key]
	if !ok {
		return RebuildReceipt{}, ErrRebuildReceiptNotFound
	}
	return receipt, nil
}

func (store *memoryRebuildReceipts) Save(_ context.Context, receipt RebuildReceipt) error {
	identity := receipt.TenantID + "\x00" + receipt.WorkspaceID + "\x00" + receipt.Operation + "\x00" + receipt.IdempotencyKey
	if existing, ok := store.values[identity]; ok {
		if existing.RequestHash == receipt.RequestHash {
			return nil
		}
		return ErrRebuildIdempotencyConflict
	}
	store.values[identity] = receipt
	return nil
}

type memoryRebuildAudit struct{ entries []audit.Entry }

func (writer *memoryRebuildAudit) Append(_ context.Context, entry audit.Entry) error {
	writer.entries = append(writer.entries, entry)
	return nil
}

func rebuildTestService(t *testing.T) (*ProjectionRebuildService, *memoryRebuildStore, *generationCatalog, *MappingGenerationRegistry, identity.Principal) {
	t.Helper()
	catalog, schemas, _ := generationFixtures(t)
	store := newMemoryRebuildStore()
	registry := NewMappingGenerationRegistry()
	authorizer := identity.NewAuthorizer(catalogMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: catalogTestPrincipal().IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}})
	auditWriter := &memoryRebuildAudit{}
	service := NewProjectionRebuildService(store, NewMappingGenerationBuilder(catalog, schemas), registry, authorizer, newMemoryRebuildReceipts(), auditWriter)
	service.clock = func() time.Time { return time.Date(2026, time.September, 17, 3, 0, 0, 0, time.UTC) }
	nextID := 0
	service.id = func() (string, error) {
		nextID++
		return fmt.Sprintf("rebuild-test-%d", nextID), nil
	}
	return service, store, catalog, registry, catalogTestPrincipal()
}

func TestProjectionRebuildStartIsIdempotentAndWorkerAtomicallyActivates(t *testing.T) {
	service, store, _, registry, principal := rebuildTestService(t)
	input := RebuildStartInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", RequestID: "request-1", IdempotencyKey: "rebuild-1"}
	first, replay, err := service.Start(context.Background(), principal, input)
	if err != nil || replay || first.Status != RebuildAccepted || first.Revision != 1 {
		t.Fatalf("Start() = %#v replay=%v err=%v", first, replay, err)
	}
	replayed, replay, err := service.Start(context.Background(), principal, input)
	if err != nil || !replay || replayed.ID != first.ID {
		t.Fatalf("idempotent Start() = %#v replay=%v err=%v", replayed, replay, err)
	}
	if err := service.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed, err := store.Find(context.Background(), "tenant-1", "workspace-1", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != RebuildSucceeded || completed.GenerationBefore == "" || completed.StagingGenerationID == "" || completed.ActiveGenerationID != registry.ActiveGenerationID() || completed.MappingCount != 1 {
		t.Fatalf("completed operation = %#v active=%s", completed, registry.ActiveGenerationID())
	}
}

func TestProjectionRebuildFailureDoesNotSwitchLastKnownGoodGeneration(t *testing.T) {
	service, store, catalog, registry, principal := rebuildTestService(t)
	initial := registry.ActiveGenerationID()
	catalog.err = errors.New("catalog unavailable")
	operation, _, err := service.Start(context.Background(), principal, RebuildStartInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", RequestID: "request-2", IdempotencyKey: "rebuild-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Find(context.Background(), operation.TenantID, operation.WorkspaceID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != RebuildFailed || registry.ActiveGenerationID() != initial || failed.SafeError == "" {
		t.Fatalf("failed operation = %#v active=%s", failed, registry.ActiveGenerationID())
	}
}

func TestProjectionRebuildCancellationBeforeAndDuringRun(t *testing.T) {
	service, store, _, _, principal := rebuildTestService(t)
	accepted, _, err := service.Start(context.Background(), principal, RebuildStartInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", RequestID: "request-3", IdempotencyKey: "rebuild-3"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, replay, err := service.Cancel(context.Background(), principal, RebuildCancelInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", OperationID: accepted.ID, RequestID: "cancel-3", IdempotencyKey: "cancel-3"})
	if err != nil || replay || cancelled.Status != RebuildCancelled {
		t.Fatalf("accepted Cancel() = %#v replay=%v err=%v", cancelled, replay, err)
	}
	if err := service.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Find(context.Background(), accepted.TenantID, accepted.WorkspaceID, accepted.ID); err != nil {
		t.Fatal(err)
	}

	running, _, err := service.Start(context.Background(), principal, RebuildStartInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", RequestID: "request-4", IdempotencyKey: "rebuild-4"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 17, 3, 0, 0, 0, time.UTC)
	running.Status = RebuildRunning
	running.StartedAt = &now
	running.UpdatedAt = now
	if _, err := store.SaveCAS(context.Background(), running, running.Revision); err != nil {
		t.Fatal(err)
	}
	requested, _, err := service.Cancel(context.Background(), principal, RebuildCancelInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", OperationID: running.ID, RequestID: "cancel-4", IdempotencyKey: "cancel-4"})
	if err != nil || requested.Status != RebuildCancelRequested {
		t.Fatalf("running Cancel() = %#v err=%v", requested, err)
	}
	if err := service.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Find(context.Background(), running.TenantID, running.WorkspaceID, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != RebuildCancelled || finished.FinishedAt == nil {
		t.Fatalf("cancelled running operation = %#v", finished)
	}
}

func TestProjectionRebuildRejectsConflictingIdempotency(t *testing.T) {
	service, _, _, _, principal := rebuildTestService(t)
	started, _, err := service.Start(context.Background(), principal, RebuildStartInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", RequestID: "request-5", IdempotencyKey: "rebuild-5"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Cancel(context.Background(), principal, RebuildCancelInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", OperationID: started.ID, RequestID: "cancel-5", IdempotencyKey: "cancel-5"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Cancel(context.Background(), principal, RebuildCancelInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", OperationID: started.ID, RequestID: "cancel-5-retry", IdempotencyKey: "cancel-5"}); !errors.Is(err, ErrRebuildIdempotencyConflict) {
		t.Fatalf("conflicting idempotency error = %v", err)
	}
}
