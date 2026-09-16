package records

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
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
}
