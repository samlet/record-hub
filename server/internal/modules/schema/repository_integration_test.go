package schema

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestMongoRepositoryIndexesAndImmutability(t *testing.T) {
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
	database := client.Database("record_hub")
	repository := NewMongoRepository(database)
	defer func() { _ = repository.collection.Drop(context.Background()) }()
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}

	draft := schemaFixture(t, StatusDraft)
	if err := repository.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(ctx, draft); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate identity error = %v", err)
	}
	sameName := draft
	sameName.SchemaID = "urn:record-hub:schema:other"
	if err := repository.Create(ctx, sameName); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate tenant/name/version error = %v", err)
	}

	draft.SemanticTypes = []string{"https://schema.org/Thing"}
	updated, err := repository.UpdateDraft(ctx, draft, 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || len(updated.SemanticTypes) != 1 {
		t.Fatalf("unexpected updated draft: %#v", updated)
	}
	if _, err := repository.UpdateDraft(ctx, draft, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}

	published := schemaFixture(t, StatusPublished)
	published.SchemaID = "urn:record-hub:schema:published"
	published.Name = "PublishedSchema"
	if err := repository.Create(ctx, published); err != nil {
		t.Fatal(err)
	}
	listed, err := repository.List(ctx, draft.TenantID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed schema definitions = %d, want 2", len(listed))
	}
	maliciousDraft := published
	maliciousDraft.Status = StatusDraft
	maliciousDraft.ContentHash = ""
	maliciousDraft.PublishedBy = nil
	maliciousDraft.PublishedAt = nil
	if _, err := repository.UpdateDraft(ctx, maliciousDraft, 1); !errors.Is(err, ErrImmutable) {
		t.Fatalf("published update error = %v", err)
	}
}

func TestMongoPersistenceAdapters(t *testing.T) {
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
	database := client.Database("record_hub")
	registry := NewMongoRepository(database)
	receipts := NewMongoReceiptStore(database)
	auditWriter := audit.NewMongoWriter(database)
	defer func() {
		_ = registry.collection.Drop(context.Background())
		_ = receipts.collection.Drop(context.Background())
		_ = database.Collection("audit_entries").Drop(context.Background())
	}()
	if err := receipts.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := auditWriter.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	definition := schemaFixture(t, StatusDraft)
	receipt := Receipt{TenantID: "tenant-1", WorkspaceID: "workspace-1", Operation: "schema.create", IdempotencyKey: "request-1", RequestHash: "sha256:request", Definition: definition}
	if err := receipts.Save(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := receipts.Save(ctx, receipt); err != nil {
		t.Fatalf("same receipt should be idempotent: %v", err)
	}
	if _, err := receipts.Find(ctx, receipt.TenantID, receipt.WorkspaceID, receipt.Operation, receipt.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	conflict := receipt
	conflict.RequestHash = "sha256:other"
	if err := receipts.Save(ctx, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting receipt = %v", err)
	}

	entry := audit.Entry{TenantID: "tenant-1", WorkspaceID: "workspace-1", Action: "schema.create", Actor: definition.CreatedBy, ResourceType: "SchemaDefinition", ResourceID: definition.SchemaID, ResourceVersion: definition.Version, IdempotencyKey: receipt.IdempotencyKey, AfterHash: receipt.RequestHash, CreatedAt: time.Now().UTC()}
	if err := auditWriter.Append(ctx, entry); err != nil {
		t.Fatal(err)
	}

	if err := registry.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	service := NewService(registry, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive,
	}}), receipts, auditWriter)
	input := draftInput(t)
	input.SchemaID = "urn:record-hub:test:transaction"
	input.IdempotencyKey = "transaction-create"
	if _, err := service.CreateDraft(ctx, principal, input); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateDraft(ctx, principal, input); err != nil {
		t.Fatalf("transactional idempotent replay: %v", err)
	}

	failingService := NewService(registry, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive,
	}}), receipts, failingAuditWriter{})
	failedInput := input
	failedInput.SchemaID = "urn:record-hub:test:transaction-rollback"
	failedInput.IdempotencyKey = "transaction-rollback"
	if _, err := failingService.CreateDraft(ctx, principal, failedInput); err == nil {
		t.Fatal("audit failure should fail mutation")
	}
	if _, err := registry.Get(ctx, failedInput.TenantID, failedInput.SchemaID, failedInput.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("audit failure left schema behind: %v", err)
	}
}

type failingAuditWriter struct{}

func (failingAuditWriter) Append(context.Context, audit.Entry) error {
	return errors.New("audit unavailable")
}

func schemaFixture(t *testing.T, status Status) Definition {
	t.Helper()
	raw, err := bson.Marshal(bson.D{
		{Key: "$schema", Value: "https://json-schema.org/draft/2020-12/schema"},
		{Key: "type", Value: "object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	creator := identity.IdentityKey{Issuer: "https://issuer.example", Subject: "user-1"}
	definition := Definition{
		TenantID: "tenant-1", SchemaID: "urn:record-hub:schema:example", Name: "ExampleSchema",
		Version: 1, Revision: 1, Status: status, JSONSchema: bson.Raw(raw), SemanticTypes: []string{},
		CreatedBy: creator, CreatedAt: now, UpdatedAt: now,
	}
	if status == StatusPublished {
		definition.ContentHash = "sha256:fixture"
		definition.PublishedBy = &creator
		definition.PublishedAt = &now
	}
	return definition
}
