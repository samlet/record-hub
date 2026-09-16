package records

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type memoryWorkspaceRepository struct {
	values map[string]Workspace
}

func (repository *memoryWorkspaceRepository) CreateWorkspace(_ context.Context, workspace Workspace) error {
	key := workspace.TenantID + ":" + workspace.ID
	if _, ok := repository.values[key]; ok {
		return ErrWorkspaceExists
	}
	repository.values[key] = workspace
	return nil
}

func (repository *memoryWorkspaceRepository) GetWorkspace(_ context.Context, tenantID, workspaceID string) (Workspace, error) {
	workspace, ok := repository.values[tenantID+":"+workspaceID]
	if !ok {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return workspace, nil
}

func (repository *memoryWorkspaceRepository) ListWorkspaces(_ context.Context, tenantID string, creator identity.IdentityKey) ([]Workspace, error) {
	var result []Workspace
	for _, workspace := range repository.values {
		if workspace.TenantID == tenantID && workspace.CreatedBy == creator {
			result = append(result, workspace)
		}
	}
	return result, nil
}

type memoryTableRepository struct {
	values map[string]TableDefinition
}

func (repository *memoryTableRepository) CreateTable(_ context.Context, table TableDefinition) error {
	key := table.TenantID + ":" + table.WorkspaceID + ":" + table.ID
	for _, existing := range repository.values {
		if existing.TenantID == table.TenantID && existing.WorkspaceID == table.WorkspaceID && existing.Name == table.Name {
			return ErrTableExists
		}
	}
	if _, ok := repository.values[key]; ok {
		return ErrTableExists
	}
	repository.values[key] = table
	return nil
}

func (repository *memoryTableRepository) GetTable(_ context.Context, tenantID, workspaceID, tableID string) (TableDefinition, error) {
	table, ok := repository.values[tenantID+":"+workspaceID+":"+tableID]
	if !ok {
		return TableDefinition{}, ErrTableNotFound
	}
	return table, nil
}

func (repository *memoryTableRepository) ListTables(_ context.Context, tenantID, workspaceID string) ([]TableDefinition, error) {
	var result []TableDefinition
	for _, table := range repository.values {
		if table.TenantID == tenantID && table.WorkspaceID == workspaceID {
			result = append(result, table)
		}
	}
	return result, nil
}

type memorySchemaReader struct {
	definition schema.Definition
}

func (reader memorySchemaReader) Get(context.Context, string, string, int64) (schema.Definition, error) {
	return reader.definition, nil
}

func TestRecordServiceEnforcesSchemaPublicationAndRoles(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	membership := identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}
	authorizer := identity.NewAuthorizer(serviceMembershipReader{membership: membership})
	published := schema.Definition{TenantID: "tenant-1", SchemaID: "urn:record-hub:schema:example", Version: 1, Status: schema.StatusPublished}
	workspaces := &memoryWorkspaceRepository{values: make(map[string]Workspace)}
	tables := &memoryTableRepository{values: make(map[string]TableDefinition)}
	service := NewService(workspaces, tables, memorySchemaReader{definition: published}, authorizer)
	ctx := context.Background()
	workspace, err := service.CreateWorkspace(ctx, principal, WorkspaceInput{TenantID: "tenant-1", ID: "workspace-1", Name: "Workspace"})
	if err != nil || workspace.ID != "workspace-1" {
		t.Fatalf("create workspace = %#v, %v", workspace, err)
	}
	table, err := service.CreateTable(ctx, principal, TableInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: "table-1", Name: "Custom", Kind: TableKindCustom, SchemaID: published.SchemaID, SchemaVersion: 1})
	if err != nil || table.Version != 1 {
		t.Fatalf("create table = %#v, %v", table, err)
	}
	viewer := principal
	viewer.Subject = "viewer"
	if _, err := service.ListTables(ctx, viewer, "tenant-1", "workspace-1"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("unmatched membership error = %v", err)
	}
	unpublished := service
	unpublished.schemas = memorySchemaReader{definition: schema.Definition{TenantID: "tenant-1", SchemaID: published.SchemaID, Version: 2, Status: schema.StatusDraft}}
	if _, err := unpublished.CreateTable(ctx, principal, TableInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: "table-2", Name: "Draft", Kind: TableKindCustom, SchemaID: published.SchemaID, SchemaVersion: 2}); !errors.Is(err, ErrSchemaUnavailable) {
		t.Fatalf("unpublished schema error = %v", err)
	}
}

type serviceMembershipReader struct {
	membership identity.WorkspaceMembership
}

func (reader serviceMembershipReader) FindMembership(_ context.Context, subject identity.IdentityKey, tenantID, workspaceID string) (identity.WorkspaceMembership, error) {
	if reader.membership.Identity != subject || reader.membership.TenantID != tenantID || reader.membership.WorkspaceID != workspaceID {
		return identity.WorkspaceMembership{}, identity.ErrMembershipGone
	}
	return reader.membership, nil
}

type memoryRecordRepository struct {
	values map[string]Record
}

func (repository *memoryRecordRepository) CreateRecord(_ context.Context, record Record) error {
	key := record.TenantID + ":" + record.WorkspaceID + ":" + record.ID
	if _, ok := repository.values[key]; ok {
		return ErrRecordExists
	}
	repository.values[key] = record
	return nil
}

func (repository *memoryRecordRepository) GetRecord(_ context.Context, tenantID, workspaceID, recordID string) (Record, error) {
	record, ok := repository.values[tenantID+":"+workspaceID+":"+recordID]
	if !ok {
		return Record{}, ErrRecordNotFound
	}
	return record, nil
}

func (repository *memoryRecordRepository) UpdateRecord(_ context.Context, record Record, expectedVersion int64) (Record, error) {
	key := record.TenantID + ":" + record.WorkspaceID + ":" + record.ID
	existing, ok := repository.values[key]
	if !ok {
		return Record{}, ErrRecordNotFound
	}
	if existing.RecordVersion != expectedVersion {
		return Record{}, ErrRecordVersionConflict
	}
	record.RecordVersion = expectedVersion + 1
	repository.values[key] = record
	return record, nil
}

func (repository *memoryRecordRepository) DeleteRecord(_ context.Context, tenantID, workspaceID, recordID string, expectedVersion int64) error {
	key := tenantID + ":" + workspaceID + ":" + recordID
	record, ok := repository.values[key]
	if !ok {
		return ErrRecordNotFound
	}
	if record.RecordVersion != expectedVersion {
		return ErrRecordVersionConflict
	}
	delete(repository.values, key)
	return nil
}

type memoryRecordReceipts struct {
	values map[string]RecordReceipt
}

func (store *memoryRecordReceipts) Find(_ context.Context, tenantID, workspaceID, operation, key string) (RecordReceipt, error) {
	receipt, ok := store.values[tenantID+":"+workspaceID+":"+operation+":"+key]
	if !ok {
		return RecordReceipt{}, ErrReceiptNotFound
	}
	return receipt, nil
}

func (store *memoryRecordReceipts) Save(_ context.Context, receipt RecordReceipt) error {
	identity := receipt.TenantID + ":" + receipt.WorkspaceID + ":" + receipt.Operation + ":" + receipt.IdempotencyKey
	if existing, ok := store.values[identity]; ok {
		if existing.RequestHash != receipt.RequestHash {
			return ErrIdempotencyConflict
		}
		return nil
	}
	store.values[identity] = receipt
	return nil
}

type memoryRecordAudit struct {
	entries []audit.Entry
}

func (writer *memoryRecordAudit) Append(_ context.Context, entry audit.Entry) error {
	writer.entries = append(writer.entries, entry)
	return nil
}

func TestRecordServiceCRUDIdempotencyAndCAS(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	membership := identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}
	definition := recordSchemaDefinition(t)
	tables := &memoryTableRepository{values: map[string]TableDefinition{"tenant-1:workspace-1:table-1": {ID: "table-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Name: "Custom", Kind: TableKindCustom, SchemaID: definition.SchemaID, SchemaVersion: 1, Version: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: timeNow(), UpdatedAt: timeNow()}}}
	store := &memoryRecordRepository{values: make(map[string]Record)}
	receipts := &memoryRecordReceipts{values: make(map[string]RecordReceipt)}
	audits := &memoryRecordAudit{}
	service := NewRecordService(nil, tables, memorySchemaReader{definition: definition}, identity.NewAuthorizer(serviceMembershipReader{membership: membership}), store, receipts, audits)
	ctx := context.Background()
	data := mustRecordData(t, `{"title":"hello","count":1}`)
	input := RecordInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "record-1", Data: data, Tags: []string{"urgent", "urgent"}, Relations: []RecordRelation{{Target: RelationTarget{System: "fluxion", Type: "PROJECT", ID: "project-1"}, RelationType: "tracks"}, {Target: RelationTarget{System: "fluxion", Type: "PROJECT", ID: "project-1"}, RelationType: "tracks", Status: RelationCurrent}}, IdempotencyKey: "create-1"}
	created, err := service.CreateRecord(ctx, principal, input)
	if err != nil || created.RecordVersion != 1 || len(created.Tags) != 1 || len(created.Relations) != 1 || created.Relations[0].Status != RelationCurrent {
		t.Fatalf("create record = %#v, %v", created, err)
	}
	if _, err := service.CreateRecord(ctx, principal, input); err != nil {
		t.Fatalf("idempotent create = %v", err)
	}
	conflict := input
	conflict.Data = mustRecordData(t, `{"title":"different","count":1}`)
	if _, err := service.CreateRecord(ctx, principal, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("create idempotency conflict = %v", err)
	}
	updated, err := service.UpdateRecord(ctx, principal, RecordInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: input.TableID, ID: input.ID, Data: mustRecordData(t, `{"title":"updated","count":2}`), IdempotencyKey: "update-1"}, 1)
	if err != nil || updated.RecordVersion != 2 {
		t.Fatalf("update record = %#v, %v", updated, err)
	}
	if _, err := service.UpdateRecord(ctx, principal, RecordInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: input.TableID, ID: input.ID, Data: data, IdempotencyKey: "stale"}, 1); !errors.Is(err, ErrRecordVersionConflict) {
		t.Fatalf("stale update = %v", err)
	}
	deleted, err := service.DeleteRecord(ctx, principal, RecordDeleteInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: input.TableID, RecordID: input.ID, IdempotencyKey: "delete-1"}, 2)
	if err != nil || deleted.RecordVersion != 2 || len(audits.entries) != 3 {
		t.Fatalf("delete record = %#v, audits=%d, err=%v", deleted, len(audits.entries), err)
	}
	if _, err := service.DeleteRecord(ctx, principal, RecordDeleteInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: input.TableID, RecordID: input.ID, IdempotencyKey: "delete-1"}, 2); err != nil {
		t.Fatalf("idempotent delete = %v", err)
	}
}

func recordSchemaDefinition(t *testing.T) schema.Definition {
	t.Helper()
	jsonSchema := mustRecordData(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["title","count"],"properties":{"title":{"type":"string"},"count":{"type":"integer"}}}`)
	return schema.Definition{TenantID: "tenant-1", SchemaID: "urn:record-hub:schema:record", Version: 1, Status: schema.StatusPublished, JSONSchema: jsonSchema}
}

func mustRecordData(t *testing.T, value string) bson.Raw {
	t.Helper()
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(value), false, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func timeNow() time.Time { return time.Now().UTC() }
