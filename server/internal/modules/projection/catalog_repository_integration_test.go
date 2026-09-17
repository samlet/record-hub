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

func TestMongoCatalogRepositoryScopesIDsAndTransitions(t *testing.T) {
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
	database := client.Database("record_hub_catalog_test")
	repository := NewMongoCatalogRepository(database)
	receipts := NewMongoCatalogReceiptStore(database)
	defer func() { _ = database.Drop(context.Background()) }()
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := receipts.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	actor := identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}
	base := SourceRegistration{ID: "approver", TenantID: "tenant-1", WorkspaceID: "workspace-1", EventType: "application.updated", EventVersion: 1, OwnerContact: "team@example.com", TenantResolution: TenantResolution{Mode: TenantResolutionMetadata, Field: "workspaceId"}, Status: CatalogStatusDraft, Revision: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateSource(ctx, base); err != nil {
		t.Fatal(err)
	}
	otherTenant := base
	otherTenant.TenantID = "tenant-2"
	otherTenant.WorkspaceID = "workspace-2"
	if err := repository.CreateSource(ctx, otherTenant); err != nil {
		t.Fatalf("same sourceId in another scope: %v", err)
	}
	if err := repository.CreateSource(ctx, base); !errors.Is(err, ErrSourceExists) {
		t.Fatalf("same scoped source error = %v", err)
	}
	published, err := repository.TransitionSource(ctx, base.TenantID, base.WorkspaceID, base.ID, CatalogStatusPublished, actor, &now, 1)
	if err != nil || published.Status != CatalogStatusPublished || published.Revision != 2 {
		t.Fatalf("published source = %#v err=%v", published, err)
	}
	if _, err := repository.TransitionSource(ctx, base.TenantID, base.WorkspaceID, base.ID, CatalogStatusPublished, actor, &now, 1); !errors.Is(err, ErrSourceImmutable) {
		t.Fatalf("second source publish error = %v", err)
	}

	fixtureInput := validCatalogMappingInput()
	fixture, err := prepareMappingFixtureDocument(base.TenantID, base.WorkspaceID, fixtureInput.Fixture, fixtureInput.FixtureDocument, actor, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutFixture(ctx, fixture); err != nil {
		t.Fatal(err)
	}
	storedFixture, err := repository.GetFixture(ctx, fixture.TenantID, fixture.WorkspaceID, fixture.EventRef, fixture.SHA256)
	if err != nil || string(storedFixture.Document) != string(fixture.Document) {
		t.Fatalf("stored fixture = %#v err=%v", storedFixture, err)
	}
	mapping := MappingRegistration{ID: "application-summary", TenantID: base.TenantID, WorkspaceID: base.WorkspaceID, SourceID: base.ID, EventType: base.EventType, EventVersion: base.EventVersion, TargetTableID: "applications", TargetSchemaID: "urn:record-hub:schema:application", TargetSchemaVersion: 1, FieldMap: map[string]string{"status": "payload.status"}, Fixture: fixtureInput.Fixture, Status: CatalogStatusDraft, Revision: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	mapping.CanonicalHash, err = mapping.ComputeCanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	publishedMapping, err := repository.TransitionMapping(ctx, mapping.TenantID, mapping.WorkspaceID, mapping.ID, CatalogStatusPublished, actor, &now, 1)
	if err != nil || publishedMapping.Status != CatalogStatusPublished || publishedMapping.Revision != 2 {
		t.Fatalf("published mapping = %#v err=%v", publishedMapping, err)
	}
	publishedMappings, err := repository.ListPublishedMappings(ctx, maxRuntimeMappings)
	if err != nil || len(publishedMappings) != 1 || publishedMappings[0].ID != mapping.ID {
		t.Fatalf("published runtime mappings = %#v err=%v", publishedMappings, err)
	}
	revoked, err := repository.TransitionMapping(ctx, mapping.TenantID, mapping.WorkspaceID, mapping.ID, CatalogStatusRevoked, actor, &now, 2)
	if err != nil || revoked.Status != CatalogStatusRevoked || revoked.Revision != 3 || revoked.PublishedAt == nil {
		t.Fatalf("revoked mapping = %#v err=%v", revoked, err)
	}
	publishedMappings, err = repository.ListPublishedMappings(ctx, maxRuntimeMappings)
	if err != nil || len(publishedMappings) != 0 {
		t.Fatalf("runtime mappings after revoke = %#v err=%v", publishedMappings, err)
	}

	receipt := CatalogReceipt{TenantID: base.TenantID, WorkspaceID: base.WorkspaceID, Operation: "source.create", IdempotencyKey: "request-1", RequestHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Source: &base}
	if err := receipts.Save(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := receipts.Save(ctx, receipt); err != nil {
		t.Fatalf("same receipt replay: %v", err)
	}
	conflict := receipt
	conflict.RequestHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := receipts.Save(ctx, conflict); !errors.Is(err, ErrCatalogIdempotencyConflict) {
		t.Fatalf("conflicting receipt error = %v", err)
	}
}
