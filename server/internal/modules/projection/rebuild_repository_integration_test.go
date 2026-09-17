package projection

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

func TestMongoRebuildRepositoryCASAndReceipt(t *testing.T) {
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
	database := client.Database("record_hub_rebuild_test")
	defer func() { _ = database.Drop(context.Background()) }()
	repository := NewMongoRebuildRepository(database)
	receipts := NewMongoRebuildReceiptStore(database)
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := receipts.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	actor := identity.IdentityKey{Issuer: "record-hub", Subject: "rebuild-integration"}
	operation := ProjectionRebuildOperation{ID: "rebuild-integration-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Status: RebuildAccepted, Revision: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if err := repository.Create(ctx, operation); err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListRunnable(ctx, 10)
	if err != nil || len(items) != 1 || items[0].ID != operation.ID {
		t.Fatalf("runnable operations = %#v err=%v", items, err)
	}
	started := operation
	started.Status = RebuildRunning
	started.StartedAt = &now
	started.UpdatedAt = now
	started.UpdatedBy = actor
	started, err = repository.SaveCAS(ctx, started, operation.Revision)
	if err != nil || started.Revision != 2 {
		t.Fatalf("SaveCAS() = %#v err=%v", started, err)
	}
	if _, err := repository.SaveCAS(ctx, started, operation.Revision); !errors.Is(err, ErrRebuildRevisionConflict) {
		t.Fatalf("stale SaveCAS() error = %v", err)
	}
	receipt := RebuildReceipt{TenantID: operation.TenantID, WorkspaceID: operation.WorkspaceID, Operation: "projection.rebuild.create", IdempotencyKey: "integration-1", RequestHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Rebuild: &operation}
	if err := receipts.Save(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := receipts.Save(ctx, receipt); err != nil {
		t.Fatalf("receipt replay: %v", err)
	}
	conflict := receipt
	conflict.RequestHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := receipts.Save(ctx, conflict); !errors.Is(err, ErrRebuildIdempotencyConflict) {
		t.Fatalf("receipt conflict error = %v", err)
	}
}
