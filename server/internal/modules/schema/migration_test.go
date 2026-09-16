package schema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type memoryMigrationPlanStore struct {
	plans map[string]MigrationPlan
}

func (store *memoryMigrationPlanStore) CreateMigrationPlan(_ context.Context, plan MigrationPlan) error {
	if _, exists := store.plans[plan.ID]; exists {
		return ErrMigrationPlanExists
	}
	for _, existing := range store.plans {
		if existing.TenantID == plan.TenantID && existing.WorkspaceID == plan.WorkspaceID && existing.SchemaID == plan.SchemaID && existing.FromVersion == plan.FromVersion && existing.ToVersion == plan.ToVersion && existing.Mode == plan.Mode && existing.CompatibilityHash == plan.CompatibilityHash {
			return ErrMigrationPlanExists
		}
	}
	store.plans[plan.ID] = plan
	return nil
}

func (store *memoryMigrationPlanStore) FindMigrationPlan(_ context.Context, tenantID, workspaceID, schemaID, planID string) (MigrationPlan, error) {
	plan, ok := store.plans[planID]
	if !ok || plan.TenantID != tenantID || plan.WorkspaceID != workspaceID || plan.SchemaID != schemaID {
		return MigrationPlan{}, ErrMigrationPlanNotFound
	}
	return plan, nil
}

func (store *memoryMigrationPlanStore) FindMigrationPlanByFingerprint(_ context.Context, tenantID, workspaceID, schemaID string, fromVersion, toVersion int64, mode MigrationPlanMode, compatibilityHash string) (MigrationPlan, error) {
	for _, plan := range store.plans {
		if plan.TenantID == tenantID && plan.WorkspaceID == workspaceID && plan.SchemaID == schemaID && plan.FromVersion == fromVersion && plan.ToVersion == toVersion && plan.Mode == mode && plan.CompatibilityHash == compatibilityHash {
			return plan, nil
		}
	}
	return MigrationPlan{}, ErrMigrationPlanNotFound
}

func (store *memoryMigrationPlanStore) UpdateMigrationPlan(_ context.Context, plan MigrationPlan, expectedVersion int64) (MigrationPlan, error) {
	current, ok := store.plans[plan.ID]
	if !ok {
		return MigrationPlan{}, ErrMigrationPlanNotFound
	}
	if current.Version != expectedVersion {
		return MigrationPlan{}, ErrMigrationPlanConflict
	}
	store.plans[plan.ID] = plan
	return plan, nil
}

type memoryMigrationReceipts struct {
	values map[string]MigrationPlanReceipt
}

func (store *memoryMigrationReceipts) Find(_ context.Context, tenantID, workspaceID, operation, key string) (MigrationPlanReceipt, error) {
	receipt, ok := store.values[tenantID+"\x00"+workspaceID+"\x00"+operation+"\x00"+key]
	if !ok {
		return MigrationPlanReceipt{}, ErrReceiptNotFound
	}
	return receipt, nil
}

func (store *memoryMigrationReceipts) Save(_ context.Context, receipt MigrationPlanReceipt) error {
	key := receipt.TenantID + "\x00" + receipt.WorkspaceID + "\x00" + receipt.Operation + "\x00" + receipt.IdempotencyKey
	if existing, ok := store.values[key]; ok {
		if existing.RequestHash != receipt.RequestHash {
			return ErrIdempotencyConflict
		}
		return nil
	}
	store.values[key] = receipt
	return nil
}

func TestMigrationServiceCreateCancelAndIdempotency(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	membership := identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}
	registry := &memoryRegistry{definitions: map[string]Definition{}}
	now := time.Now().UTC()
	for _, definition := range []Definition{
		{TenantID: "tenant-1", SchemaID: "schema-1", Name: "v1", Version: 1, Revision: 2, Status: StatusPublished, JSONSchema: migrationSchemaRaw(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"name":{"type":"string"}}}`), CreatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now, ContentHash: "sha256:" + "a"},
		{TenantID: "tenant-1", SchemaID: "schema-1", Name: "v2", Version: 2, Revision: 1, Status: StatusDraft, JSONSchema: migrationSchemaRaw(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"name":{"type":"string"},"description":{"type":"string"}}}`), CreatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now},
	} {
		registry.definitions[schemaKey(definition.TenantID, definition.SchemaID, definition.Version)] = definition
	}
	plans := &memoryMigrationPlanStore{plans: map[string]MigrationPlan{}}
	receipts := &memoryMigrationReceipts{values: map[string]MigrationPlanReceipt{}}
	service := NewMigrationService(registry, plans, identity.NewAuthorizer(serviceMembershipReader{membership: membership}), receipts, &memoryAudit{})

	input := MigrationPlanInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", SchemaID: "schema-1", FromVersion: 1, ToVersion: 2, Mode: MigrationModeDryRun, FailureSampleLimit: 20, IdempotencyKey: "plan-1"}
	plan, reused, err := service.Create(context.Background(), principal, input)
	if err != nil || reused || plan.Status != MigrationStatusDraft || plan.CompatibilityHash == "" {
		t.Fatalf("create plan = %#v reused=%v err=%v", plan, reused, err)
	}
	repeated, reused, err := service.Create(context.Background(), principal, input)
	if err != nil || !reused || repeated.ID != plan.ID {
		t.Fatalf("repeat plan = %#v reused=%v err=%v", repeated, reused, err)
	}
	conflicting := input
	conflicting.Mode = MigrationModeApply
	if _, _, err := service.Create(context.Background(), principal, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}

	cancelled, reused, err := service.Cancel(context.Background(), principal, "tenant-1", "workspace-1", "schema-1", plan.ID, plan.Version, "cancel-1", "cancel-key-1")
	if err != nil || reused || cancelled.Status != MigrationStatusCancelled || cancelled.Version != 2 {
		t.Fatalf("cancel plan = %#v reused=%v err=%v", cancelled, reused, err)
	}
	repeatedCancel, reused, err := service.Cancel(context.Background(), principal, "tenant-1", "workspace-1", "schema-1", plan.ID, plan.Version, "cancel-1", "cancel-key-1")
	if err != nil || !reused || repeatedCancel.ID != plan.ID || repeatedCancel.Version != 2 {
		t.Fatalf("repeat cancel = %#v reused=%v err=%v", repeatedCancel, reused, err)
	}
	if _, _, err := service.Cancel(context.Background(), principal, "tenant-1", "workspace-1", "schema-1", plan.ID, cancelled.Version, "cancel-2", "cancel-key-2"); err != nil {
		t.Fatalf("second cancel should be idempotent by state: %v", err)
	}
}

func migrationSchemaRaw(t *testing.T, value string) bson.Raw {
	t.Helper()
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(value), false, &raw); err != nil {
		t.Fatalf("marshal migration schema: %v", err)
	}
	return raw
}
