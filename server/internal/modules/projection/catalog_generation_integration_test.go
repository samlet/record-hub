package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestPublishedMappingJetStreamRecovery is the opt-in live gate for
// P2-1-003. It proves that a generation can be reconstructed from Mongo after
// a worker restart and that the same durable consumer resumes a queued event.
func TestPublishedMappingJetStreamRecovery(t *testing.T) {
	mongoURI := os.Getenv("RECORD_HUB_MONGODB_URI")
	natsURL := os.Getenv("RECORD_HUB_NATS_URL")
	if mongoURI == "" || natsURL == "" {
		t.Skip("RECORD_HUB_MONGODB_URI and RECORD_HUB_NATS_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	databaseName := "record_hub_mapping_" + token
	streamName := "P2_MAPPING_" + token
	durable := "p2-mapping-" + token
	subject := "p2.mapping." + token + ".approver"

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(mongoURI).SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mongoClient.Disconnect(context.Background()) }()
	database := mongoClient.Database(databaseName)
	defer func() { _ = database.Drop(context.Background()) }()

	natsClient, err := Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer natsClient.Close()
	if _, err := natsClient.jetstream.CreateStream(ctx, jetstream.StreamConfig{Name: streamName, Subjects: []string{subject}, Storage: jetstream.MemoryStorage, Retention: jetstream.LimitsPolicy, MaxAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = natsClient.jetstream.DeleteStream(context.Background(), streamName) }()
	if _, err := natsClient.jetstream.CreateConsumer(ctx, streamName, jetstream.ConsumerConfig{Name: durable, Durable: durable, FilterSubject: subject, DeliverPolicy: jetstream.DeliverAllPolicy, AckPolicy: jetstream.AckExplicitPolicy, AckWait: 5 * time.Second, MaxDeliver: 5}); err != nil {
		t.Fatal(err)
	}

	catalog := NewMongoCatalogRepository(database)
	schemas := schema.NewMongoRepository(database)
	inbox := NewMongoInboxRepository(database)
	projections := NewMongoProjectionRepository(database)
	for _, ensure := range []func(context.Context) error{catalog.EnsureIndexes, schemas.EnsureIndexes, inbox.EnsureIndexes, projections.EnsureIndexes} {
		if err := ensure(ctx); err != nil {
			t.Fatal(err)
		}
	}
	seedPublishedMapping(t, ctx, catalog, schemas)

	runWorker := func() (context.CancelFunc, <-chan error) {
		generation, buildErr := NewMappingGenerationBuilder(catalog, schemas).Build(ctx)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		generations := NewMappingGenerationRegistry()
		if activateErr := generations.Activate(generation); activateErr != nil {
			t.Fatal(activateErr)
		}
		handlers := NewHandlerRegistry()
		if registerErr := RegisterSummaryHandlers(handlers); registerErr != nil {
			t.Fatal(registerErr)
		}
		projector, projectorErr := NewSummaryProjector(handlers, inbox, projections, "")
		if projectorErr != nil {
			t.Fatal(projectorErr)
		}
		projector.WithMappingGenerations(generations)
		runner, runnerErr := NewPullRunner(natsClient, PullRunnerConfig{Stream: streamName, Durable: durable, BatchSize: 4, FetchTimeout: 100 * time.Millisecond, AckTimeout: time.Second, RetryDelay: 10 * time.Millisecond, DrainTimeout: time.Second}, projector.HandleMessage)
		if runnerErr != nil {
			t.Fatal(runnerErr)
		}
		workerCtx, workerCancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- runner.Run(workerCtx) }()
		return workerCancel, done
	}

	workerCancel, workerDone := runWorker()
	if _, err := natsClient.jetstream.Publish(ctx, subject, liveMappingEnvelope(t, 1)); err != nil {
		t.Fatal(err)
	}
	waitForMappedRecord(t, ctx, database, 1)
	workerCancel()
	if err := <-workerDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	// The second event remains in JetStream while the first worker is down.
	if _, err := natsClient.jetstream.Publish(ctx, subject, liveMappingEnvelope(t, 2)); err != nil {
		t.Fatal(err)
	}
	workerCancel, workerDone = runWorker()
	waitForMappedRecord(t, ctx, database, 2)
	workerCancel()
	if err := <-workerDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if count, err := database.Collection(inboxCollectionName).CountDocuments(ctx, bson.D{{Key: "consumer", Value: ApproverProjectionConsumer}, {Key: "status", Value: InboxApplied}}); err != nil || count != 2 {
		t.Fatalf("applied inbox count = %d, err=%v", count, err)
	}
}

func seedPublishedMapping(t *testing.T, ctx context.Context, catalog *MongoCatalogRepository, schemas *schema.MongoRepository) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	actor := identity.IdentityKey{Issuer: "record-hub", Subject: "p2-live-gate"}
	rawSchema, err := bson.Marshal(bson.D{
		{Key: "$schema", Value: schema.Draft202012URI}, {Key: "type", Value: "object"}, {Key: "additionalProperties", Value: false},
		{Key: "required", Value: bson.A{"applicationId", "status"}},
		{Key: "properties", Value: bson.D{{Key: "applicationId", Value: bson.D{{Key: "type", Value: "string"}}}, {Key: "status", Value: bson.D{{Key: "type", Value: "string"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	definition := schema.Definition{TenantID: "tenant-p2", SchemaID: "schema.application", Name: "P2 Application", Version: 1, Revision: 2, Status: schema.StatusPublished, JSONSchema: bson.Raw(rawSchema), ContentHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedBy: actor, PublishedBy: &actor, CreatedAt: now, UpdatedAt: now, PublishedAt: &now}
	if err := schemas.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	source := SourceRegistration{ID: "approver", TenantID: "tenant-p2", WorkspaceID: "workspace-p2", EventType: "application.updated", EventVersion: 1, OwnerContact: "team@example.test", TenantResolution: TenantResolution{Mode: TenantResolutionMetadata, Field: "workspaceId"}, Status: CatalogStatusDraft, Revision: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if err := catalog.CreateSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.TransitionSource(ctx, source.TenantID, source.WorkspaceID, source.ID, CatalogStatusPublished, actor, &now, 1); err != nil {
		t.Fatal(err)
	}
	mapping := MappingRegistration{ID: "application-live", TenantID: source.TenantID, WorkspaceID: source.WorkspaceID, SourceID: source.ID, EventType: source.EventType, EventVersion: 1, TargetTableID: "applications", TargetSchemaID: definition.SchemaID, TargetSchemaVersion: 1, FieldMap: map[string]string{"applicationId": "payload.applicationId", "status": "payload.status"}, Fixture: MappingFixture{EventRef: "fixtures/p2-live.json", SHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, Status: CatalogStatusDraft, Revision: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	mapping.CanonicalHash, err = mapping.ComputeCanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.CreateMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.TransitionMapping(ctx, mapping.TenantID, mapping.WorkspaceID, mapping.ID, CatalogStatusPublished, actor, &now, 1); err != nil {
		t.Fatal(err)
	}
}

func liveMappingEnvelope(t *testing.T, version int64) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{
		"eventId": fmt.Sprintf("00000000-0000-4000-8000-%012d", version), "kind": "event", "eventType": "application.updated", "schemaVersion": 1,
		"sourceSystem": "approver", "tenantId": "tenant-p2", "aggregateType": "Application", "aggregateId": "application-live", "aggregateVersion": version,
		"occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]interface{}{"applicationId": "application-live", "status": fmt.Sprintf("STATE-%d", version), "ignored": "not projected"},
		"metadata": map[string]string{"workspaceId": "workspace-p2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func waitForMappedRecord(t *testing.T, ctx context.Context, database *mongo.Database, version int64) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var record struct {
			RecordVersion int64    `bson:"recordVersion"`
			Data          bson.Raw `bson:"data"`
		}
		err := database.Collection("records").FindOne(ctx, bson.D{{Key: "tenantId", Value: "tenant-p2"}, {Key: "workspaceId", Value: "workspace-p2"}, {Key: "tableId", Value: "applications"}, {Key: "recordVersion", Value: version}}).Decode(&record)
		if err == nil {
			var data map[string]interface{}
			if err := bson.Unmarshal(record.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data["ignored"] != nil || data["status"] != fmt.Sprintf("STATE-%d", version) {
				t.Fatalf("mapped live document = %#v", data)
			}
			return
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for mapped record version %d: %v", version, ctx.Err())
		case <-ticker.C:
		}
	}
}
