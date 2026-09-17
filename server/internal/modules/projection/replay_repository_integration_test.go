package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/records"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestMongoProjectionReplayStagingReadPointerAndLiveContinuation(t *testing.T) {
	uri := os.Getenv("RECORD_HUB_MONGODB_URI")
	if uri == "" {
		t.Skip("RECORD_HUB_MONGODB_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	database := client.Database("record_hub")
	operationID := "replay-integration-" + time.Now().UTC().Format("20060102150405.000000000")
	stagingCollection := stagingRecordsCollectionName(operationID)
	cleanup := []string{projectionEventArchiveCollectionName, records.ProjectionReadPointerCollectionName, stagingCollection, "records", inboxCollectionName, checkpointCollectionName, "audit_entries"}
	defer func() {
		for _, name := range cleanup {
			_ = database.Collection(name).Drop(context.Background())
		}
	}()

	archive := NewMongoProjectionEventArchive(database)
	pointers := NewMongoProjectionReadPointerRepository(database)
	if err := archive.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pointers.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := records.NewMongoRepository(database).EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := NewMongoProjectionRepository(database).EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}

	first := replayFixtureEnvelope(t, "018f47a5-8b77-7c5a-9c56-38db8aa4b181", 1, 1)
	if err := archive.Archive(ctx, first); err != nil {
		t.Fatal(err)
	}
	entries, err := archive.List(ctx, "tenant-replay", "workspace-replay", 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("archive entries = %#v, %v", entries, err)
	}

	stagingRepository := NewMongoProjectionRepository(database)
	if err := stagingRepository.EnsureStagingIndexes(ctx, operationID); err != nil {
		t.Fatal(err)
	}
	handlers := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(handlers); err != nil {
		t.Fatal(err)
	}
	projector, err := NewSummaryProjector(handlers, &rebuildInbox{}, stagingRepository.WithStaging(operationID), "workspace-replay")
	if err != nil {
		t.Fatal(err)
	}
	projector.WithWorkspaceMappings(map[string]string{"tenant-replay": "workspace-replay"})
	if err := projector.Handle(ctx, first.Subject, first.Raw); err != nil {
		t.Fatalf("replay staged event: %v", err)
	}
	checkpoint := ProjectionCheckpoint{TenantID: "tenant-replay", WorkspaceID: "workspace-replay", Consumer: ApproverProjectionConsumer, SourceSystem: "approver", AggregateType: "Application", AggregateID: "application-1", SourceVersion: 1, LastEventID: first.EventID, SyncedAt: first.ReceivedAt, Status: CheckpointCurrent}
	if err := pointers.ActivateManyWithCheckpoints(ctx, "tenant-replay", "workspace-replay", operationID, "generation-replay-1", stagingCollection, []string{projectionTableID("approver")}, []ProjectionCheckpoint{checkpoint}); err != nil {
		t.Fatal(err)
	}

	readRepository := records.NewMongoRepository(database)
	staged, err := readRepository.GetRecord(ctx, "tenant-replay", "workspace-replay", projectionRecordID("tenant-replay", "workspace-replay", "approver", "Application", "application-1"))
	if err != nil || staged.RecordVersion != 1 {
		t.Fatalf("staged record = %#v, %v", staged, err)
	}

	second := replayFixtureEnvelope(t, "018f47a5-8b77-7c5a-9c56-38db8aa4b182", 2, 2)
	liveHandlers := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(liveHandlers); err != nil {
		t.Fatal(err)
	}
	liveProjector, err := NewSummaryProjector(liveHandlers, NewMongoInboxRepository(database), NewMongoProjectionRepository(database), "workspace-replay")
	if err != nil {
		t.Fatal(err)
	}
	liveProjector.WithWorkspaceMappings(map[string]string{"tenant-replay": "workspace-replay"})
	if err := liveProjector.Handle(ctx, second.Subject, second.Raw); err != nil {
		t.Fatalf("continue live event after pointer switch: %v", err)
	}
	continued, err := readRepository.GetRecord(ctx, "tenant-replay", "workspace-replay", staged.ID)
	if err != nil || continued.RecordVersion != 2 || continued.Projection == nil || continued.Projection.LastEventID != second.EventID {
		t.Fatalf("continued record = %#v, %v", continued, err)
	}
}

func replayFixtureEnvelope(t *testing.T, eventID string, aggregateVersion, payloadVersion int64) ProjectionEventArchiveEntry {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"eventId": eventID, "kind": "event", "eventType": "approver.application.summary-changed", "schemaVersion": 1,
		"sourceSystem": "approver", "tenantId": "tenant-replay", "aggregateType": "Application", "aggregateId": "application-1", "aggregateVersion": aggregateVersion,
		"occurredAt": time.Unix(1700000000+aggregateVersion, 0).UTC(), "payload": map[string]any{"applicationId": "application-1", "title": "Replay", "status": "OPEN", "processRef": "workflow-1", "updatedAt": "2026-09-15T15:30:00Z", "version": payloadVersion},
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	now := time.Now().UTC()
	return ProjectionEventArchiveEntry{EventID: eventID, Consumer: ApproverProjectionConsumer, Subject: "events.approver.application.summary-changed.v1", TenantID: "tenant-replay", WorkspaceID: "workspace-replay", SourceSystem: "approver", EventType: "approver.application.summary-changed", SchemaVersion: 1, AggregateType: "Application", AggregateID: "application-1", AggregateVersion: aggregateVersion, OccurredAt: time.Unix(1700000000+aggregateVersion, 0).UTC(), ReceivedAt: now, PayloadHash: "sha256:" + hex.EncodeToString(hash[:]), Raw: raw}
}
