package records

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestMongoWorkspaceAndTablePersistence(t *testing.T) {
	uri := os.Getenv("RECORD_HUB_MONGODB_URI")
	if uri == "" {
		t.Skip("RECORD_HUB_MONGODB_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	repository := NewMongoRepository(client.Database("record_hub"))
	defer func() {
		_ = repository.workspaces.Drop(context.Background())
		_ = repository.tables.Drop(context.Background())
		_ = repository.records.Drop(context.Background())
		_ = client.Database("record_hub").Collection(recordReceiptCollectionName).Drop(context.Background())
		_ = client.Database("record_hub").Collection("audit_entries").Drop(context.Background())
	}()
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	creator := identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}
	now := time.Now().UTC()
	workspace := Workspace{ID: "workspace-mongo", TenantID: "tenant-mongo", Name: "Mongo workspace", Version: 1, CreatedBy: creator, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWorkspace(ctx, workspace); !errors.Is(err, ErrWorkspaceExists) {
		t.Fatalf("duplicate workspace error = %v", err)
	}
	if got, err := repository.GetWorkspace(ctx, workspace.TenantID, workspace.ID); err != nil || got.Name != workspace.Name {
		t.Fatalf("get workspace = %#v, %v", got, err)
	}
	table := TableDefinition{ID: "table-mongo", TenantID: workspace.TenantID, WorkspaceID: workspace.ID, Name: "Projects", Kind: TableKindProjection, SchemaID: "urn:record-hub:schema:project", SchemaVersion: 1, SourcePolicy: &SourcePolicy{System: "fluxion", Type: "PROJECT", AllowFields: []string{"id", "status"}}, Version: 1, CreatedBy: creator, UpdatedBy: creator, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateTable(ctx, table); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateTable(ctx, table); !errors.Is(err, ErrTableExists) {
		t.Fatalf("duplicate table error = %v", err)
	}
	if got, err := repository.GetTable(ctx, table.TenantID, table.WorkspaceID, table.ID); err != nil || got.SourcePolicy == nil || got.SourcePolicy.System != "fluxion" {
		t.Fatalf("get table = %#v, %v", got, err)
	}
	tables, err := repository.ListTables(ctx, table.TenantID, table.WorkspaceID)
	if err != nil || len(tables) != 1 {
		t.Fatalf("list tables = %#v, %v", tables, err)
	}
	if _, err := repository.GetTable(ctx, "other-tenant", table.WorkspaceID, table.ID); !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("cross-tenant table lookup = %v", err)
	}
	recordTable := table
	recordTable.ID = "table-custom-mongo"
	recordTable.Name = "Custom records"
	recordTable.Kind = TableKindCustom
	recordTable.SourcePolicy = nil
	if err := repository.CreateTable(ctx, recordTable); err != nil {
		t.Fatal(err)
	}
	receipts := NewMongoRecordReceiptStore(client.Database("record_hub"))
	if err := receipts.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	auditWriter := audit.NewMongoWriter(client.Database("record_hub"))
	if err := auditWriter.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: creator.Issuer, Subject: creator.Subject}
	definition := schema.Definition{TenantID: workspace.TenantID, SchemaID: recordTable.SchemaID, Version: 1, Status: schema.StatusPublished, JSONSchema: mustRecordData(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["title"],"properties":{"title":{"type":"string"}}}`)}
	service := NewRecordService(repository, repository, memorySchemaReader{definition: definition}, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: workspace.TenantID, WorkspaceID: workspace.ID, Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}}), repository, receipts, auditWriter)
	recordInput := RecordInput{TenantID: workspace.TenantID, WorkspaceID: workspace.ID, TableID: recordTable.ID, ID: "record-mongo", Data: mustRecordData(t, `{"title":"hello"}`), IdempotencyKey: "record-create-mongo"}
	if _, err := service.CreateRecord(ctx, principal, recordInput); err != nil {
		t.Fatal(err)
	}
	secondInput := recordInput
	secondInput.ID = "record-mongo-2"
	secondInput.IdempotencyKey = "record-create-mongo-2"
	secondInput.Data = mustRecordData(t, `{"title":"hello again"}`)
	if _, err := service.CreateRecord(ctx, principal, secondInput); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateRecord(ctx, principal, recordInput); err != nil {
		t.Fatalf("record idempotent replay: %v", err)
	}
	updated, err := service.UpdateRecord(ctx, principal, RecordInput{TenantID: workspace.TenantID, WorkspaceID: workspace.ID, TableID: recordTable.ID, ID: recordInput.ID, Data: mustRecordData(t, `{"title":"hello updated"}`), IdempotencyKey: "record-update-mongo"}, 1)
	if err != nil || updated.RecordVersion != 2 {
		t.Fatalf("record update = %#v, %v", updated, err)
	}
	if _, err := service.UpdateRecord(ctx, principal, RecordInput{TenantID: workspace.TenantID, WorkspaceID: workspace.ID, TableID: recordTable.ID, ID: recordInput.ID, Data: recordInput.Data, IdempotencyKey: "record-stale-mongo"}, 1); !errors.Is(err, ErrRecordVersionConflict) {
		t.Fatalf("record stale update = %v", err)
	}
	failingService := NewRecordService(repository, repository, memorySchemaReader{definition: definition}, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: workspace.TenantID, WorkspaceID: workspace.ID, Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}}), repository, receipts, failingRecordAuditWriter{})
	failingInput := recordInput
	failingInput.ID = "record-rollback-mongo"
	failingInput.IdempotencyKey = "record-rollback-mongo"
	if _, err := failingService.CreateRecord(ctx, principal, failingInput); err == nil {
		t.Fatal("record audit failure should fail mutation")
	}
	if _, err := repository.GetRecord(ctx, workspace.TenantID, workspace.ID, failingInput.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("record audit failure left record behind: %v", err)
	}
	view := ViewDefinition{ID: "view-mongo", TenantID: workspace.TenantID, WorkspaceID: workspace.ID, TableID: recordTable.ID, Name: "Hello", Filters: []ViewFilter{{Field: "title", Operator: FilterContains, Value: "hello"}}, Sorts: []ViewSort{{Field: "title", Direction: SortAscending}}, Version: 1, CreatedBy: creator, UpdatedBy: creator, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateView(ctx, view); err != nil {
		t.Fatal(err)
	}
	service.WithViewRepository(repository)
	page, err := service.ListRecords(ctx, principal, workspace.TenantID, workspace.ID, recordTable.ID, view.ID, "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first view page = %#v, %v", page, err)
	}
	nextPage, err := service.ListRecords(ctx, principal, workspace.TenantID, workspace.ID, recordTable.ID, view.ID, page.NextCursor, 1)
	if err != nil || len(nextPage.Items) != 1 || nextPage.Items[0].ID == page.Items[0].ID {
		t.Fatalf("second view page = %#v, %v", nextPage, err)
	}
	if _, err := service.DeleteRecord(ctx, principal, RecordDeleteInput{TenantID: workspace.TenantID, WorkspaceID: workspace.ID, TableID: recordTable.ID, RecordID: recordInput.ID, IdempotencyKey: "record-delete-mongo"}, 2); err != nil {
		t.Fatal(err)
	}
}

type failingRecordAuditWriter struct{}

func (failingRecordAuditWriter) Append(context.Context, audit.Entry) error {
	return errors.New("audit unavailable")
}
