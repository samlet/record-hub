package projection

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type generationCatalog struct {
	mappings []MappingRegistration
	sources  map[string]SourceRegistration
	err      error
}

func (catalog *generationCatalog) ListPublishedMappings(context.Context, int64) ([]MappingRegistration, error) {
	if catalog.err != nil {
		return nil, catalog.err
	}
	return append([]MappingRegistration(nil), catalog.mappings...), nil
}

func (catalog *generationCatalog) FindSource(_ context.Context, tenantID, workspaceID, sourceID string) (SourceRegistration, error) {
	source, ok := catalog.sources[tenantID+"\x00"+workspaceID+"\x00"+sourceID]
	if !ok {
		return SourceRegistration{}, ErrSourceNotFound
	}
	return source, nil
}

func generationFixtures(t *testing.T) (*generationCatalog, catalogSchemaReader, []byte) {
	t.Helper()
	now := time.Date(2026, time.September, 17, 1, 0, 0, 0, time.UTC)
	actor := identity.IdentityKey{Issuer: "record-hub", Subject: "catalog-test"}
	source := SourceRegistration{
		ID: "approver", TenantID: "tenant-1", WorkspaceID: "workspace-1", EventType: "application.updated", EventVersion: 1,
		OwnerContact: "team@example.test", TenantResolution: TenantResolution{Mode: TenantResolutionMetadata, Field: "workspaceId"},
		Status: CatalogStatusPublished, Revision: 2, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now, PublishedAt: &now,
	}
	mapping := MappingRegistration{
		ID: "application-summary", TenantID: "tenant-1", WorkspaceID: "workspace-1", SourceID: "approver", EventType: "application.updated", EventVersion: 1,
		TargetTableID: "applications", TargetSchemaID: "schema.application", TargetSchemaVersion: 1,
		FieldMap: map[string]string{"applicationId": "payload.applicationId", "status": "/payload/status"},
		Fixture:  MappingFixture{EventRef: "fixtures/application-updated.json", SHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Status:   CatalogStatusPublished, Revision: 2, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now, PublishedAt: &now,
	}
	hash, err := mapping.ComputeCanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	mapping.CanonicalHash = hash
	rawSchema, err := bson.Marshal(bson.D{
		{Key: "$schema", Value: schema.Draft202012URI}, {Key: "type", Value: "object"}, {Key: "additionalProperties", Value: false},
		{Key: "required", Value: bson.A{"applicationId", "status"}},
		{Key: "properties", Value: bson.D{{Key: "applicationId", Value: bson.D{{Key: "type", Value: "string"}}}, {Key: "status", Value: bson.D{{Key: "type", Value: "string"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	schemas := catalogSchemaReader{definitions: map[string]schema.Definition{
		"tenant-1\x00schema.application\x001": {TenantID: "tenant-1", SchemaID: "schema.application", Version: 1, Status: schema.StatusPublished, JSONSchema: bson.Raw(rawSchema), ContentHash: "sha256:schema"},
	}}
	raw := []byte(`{"eventId":"00000000-0000-4000-8000-000000000002","kind":"event","eventType":"application.updated","schemaVersion":1,"sourceSystem":"approver","tenantId":"tenant-1","aggregateType":"Application","aggregateId":"application-2","aggregateVersion":7,"occurredAt":"2026-09-17T01:01:00Z","payload":{"applicationId":"application-2","status":"APPROVED","ignored":"not-projected"},"metadata":{"workspaceId":"workspace-1"}}`)
	return &generationCatalog{mappings: []MappingRegistration{mapping}, sources: map[string]SourceRegistration{"tenant-1\x00workspace-1\x00approver": source}}, schemas, raw
}

func TestMappingGenerationBuildResolveAndMapExactPublishedContract(t *testing.T) {
	catalog, schemas, raw := generationFixtures(t)
	builder := NewMappingGenerationBuilder(catalog, schemas)
	builder.clock = func() time.Time { return time.Date(2026, time.September, 17, 2, 0, 0, 0, time.UTC) }
	first, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.EntryCount() != 1 {
		t.Fatalf("generation IDs/count = %q %q %d", first.ID, second.ID, first.EntryCount())
	}
	registry := NewMappingGenerationRegistry()
	if err := registry.Activate(first); err != nil {
		t.Fatal(err)
	}
	mapping, err := registry.Resolve("tenant-1", "approver", "application.updated", 1, map[string]string{"workspaceId": "workspace-1"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := mapping.Map(raw)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := bson.Unmarshal(document, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["applicationId"] != "application-2" || decoded["status"] != "APPROVED" || decoded["ignored"] != nil {
		t.Fatalf("mapped document = %#v", decoded)
	}
	for _, lookup := range []struct {
		tenant, source, eventType, workspace string
		version                              int64
	}{
		{tenant: "tenant-2", source: "approver", eventType: "application.updated", version: 1, workspace: "workspace-1"},
		{tenant: "tenant-1", source: "approver", eventType: "application.updated", version: 2, workspace: "workspace-1"},
		{tenant: "tenant-1", source: "approver", eventType: "application.changed", version: 1, workspace: "workspace-1"},
		{tenant: "tenant-1", source: "approver", eventType: "application.updated", version: 1, workspace: "workspace-2"},
	} {
		if _, err := registry.Resolve(lookup.tenant, lookup.source, lookup.eventType, lookup.version, map[string]string{"workspaceId": lookup.workspace}); !errors.Is(err, ErrRuntimeMappingNotFound) {
			t.Fatalf("Resolve(%+v) error = %v", lookup, err)
		}
	}
}

func TestMappingGenerationActivationRemovesRevokedEntryAtomically(t *testing.T) {
	catalog, schemas, _ := generationFixtures(t)
	builder := NewMappingGenerationBuilder(catalog, schemas)
	active, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	registry := NewMappingGenerationRegistry()
	if err := registry.Activate(active); err != nil {
		t.Fatal(err)
	}
	oldID := registry.ActiveGenerationID()
	catalog.mappings = nil
	replacement, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(replacement); err != nil {
		t.Fatal(err)
	}
	if registry.ActiveGenerationID() == oldID || replacement.EntryCount() != 0 {
		t.Fatalf("generation did not switch: old=%s new=%s count=%d", oldID, registry.ActiveGenerationID(), replacement.EntryCount())
	}
	if _, err := registry.Resolve("tenant-1", "approver", "application.updated", 1, map[string]string{"workspaceId": "workspace-1"}); !errors.Is(err, ErrRuntimeMappingNotFound) {
		t.Fatalf("revoked lookup error = %v", err)
	}
}

func TestMappingGenerationRejectsBuiltInOverrideAndRetainsLastGoodOnRefreshFailure(t *testing.T) {
	catalog, schemas, _ := generationFixtures(t)
	builder := NewMappingGenerationBuilder(catalog, schemas)
	registry := NewMappingGenerationRegistry()
	refresher := NewMappingGenerationRefresher(builder, registry, time.Second, nil)
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	activeID := registry.ActiveGenerationID()
	catalog.err = errors.New("mongo unavailable")
	if _, err := refresher.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() error = nil")
	}
	if registry.ActiveGenerationID() != activeID {
		t.Fatalf("failed refresh changed active generation: %s -> %s", activeID, registry.ActiveGenerationID())
	}
	catalog.err = nil
	catalog.mappings[0].EventType = "approver.application.summary-changed"
	catalog.mappings[0].CanonicalHash, _ = catalog.mappings[0].ComputeCanonicalHash()
	catalog.sources["tenant-1\x00workspace-1\x00approver"] = func() SourceRegistration {
		source := catalog.sources["tenant-1\x00workspace-1\x00approver"]
		source.EventType = "approver.application.summary-changed"
		return source
	}()
	if _, err := builder.Build(context.Background()); !errors.Is(err, ErrBuiltInMappingReserved) {
		t.Fatalf("built-in override error = %v", err)
	}
}

func TestSummaryProjectorAppliesCatalogMappingAndRejectsAfterGenerationSwitch(t *testing.T) {
	catalog, schemas, raw := generationFixtures(t)
	generation, err := NewMappingGenerationBuilder(catalog, schemas).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	generations := NewMappingGenerationRegistry()
	if err := generations.Activate(generation); err != nil {
		t.Fatal(err)
	}
	handlers := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(handlers); err != nil {
		t.Fatal(err)
	}
	repository := &projectorRepository{}
	projector, err := NewSummaryProjector(handlers, &projectorInbox{}, repository, "")
	if err != nil {
		t.Fatal(err)
	}
	projector.WithMappingGenerations(generations)
	projector.now = func() time.Time { return time.Date(2026, time.September, 17, 2, 0, 0, 0, time.UTC) }
	if err := projector.Handle(context.Background(), "events.approver.application.updated.v1", raw); err != nil {
		t.Fatal(err)
	}
	apply := repository.applied
	if apply.Record.WorkspaceID != "workspace-1" || apply.Record.TableID != "applications" || apply.Record.SchemaID != "schema.application" || apply.Record.RecordVersion != 7 || apply.Checkpoint.SourceVersion != 7 {
		t.Fatalf("dynamic projection = %+v", apply)
	}
	if apply.InboxEvent.Consumer != ApproverProjectionConsumer || apply.Audit.ResourceVersion != 7 {
		t.Fatalf("dynamic inbox/audit = %+v / %+v", apply.InboxEvent, apply.Audit)
	}
	empty := &MappingGeneration{ID: fmt.Sprintf("sha256:%064d", 0), BuiltAt: time.Now(), entries: map[RuntimeMappingKey]*RuntimeMapping{}, byEvent: map[runtimeEventKey][]RuntimeMappingKey{}}
	if err := generations.Activate(empty); err != nil {
		t.Fatal(err)
	}
	if err := projector.Handle(context.Background(), "events.approver.application.updated.v1", raw); err == nil || !errors.Is(err, ErrHandlerNotFound) {
		t.Fatalf("projector after revoke error = %v", err)
	}
}
