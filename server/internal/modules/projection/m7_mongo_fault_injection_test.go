package projection

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/records"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestMongoProjectionApplyAckLossReplayIsDuplicateFree models a committed
// Mongo transaction whose client response is lost before the caller can ACK
// the NATS message. The exact event is replayed and must not create another
// record, checkpoint, inbox row, or audit entry.
func TestMongoProjectionApplyAckLossReplayIsDuplicateFree(t *testing.T) {
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
	repository := NewMongoProjectionRepository(database)
	defer func() {
		_ = database.Collection(inboxCollectionName).Drop(context.Background())
		_ = database.Collection("records").Drop(context.Background())
		_ = database.Collection(checkpointCollectionName).Drop(context.Background())
		_ = database.Collection("audit_entries").Drop(context.Background())
	}()
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := records.NewMongoRepository(database).EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}

	input := validProjectionApply(t)
	input.InboxEvent.EventID = "m7-ack-loss-event"
	input.InboxEvent.Consumer = "record-hub-m7-ack-loss-v1"
	input.InboxEvent.Subject = "events.fluxion.project.summary-changed.v1"
	input.Record.ID = "m7-ack-loss-record"
	input.Record.Projection = &records.ProjectionState{LastEventID: input.InboxEvent.EventID, SyncedAt: input.InboxEvent.ReceivedAt, Status: "CURRENT"}
	input.Checkpoint.Consumer = input.InboxEvent.Consumer
	input.Checkpoint.LastEventID = input.InboxEvent.EventID
	input.Audit.ResourceID = input.Record.ID
	input.Audit.ResourceVersion = input.Record.RecordVersion
	payload := []byte(`{"eventId":"m7-ack-loss-event","payload":{"status":"ACTIVE"}}`)
	claim, err := (&MongoInboxRepository{collection: database.Collection(inboxCollectionName)}).Claim(ctx, InboxClaim{EventID: input.InboxEvent.EventID, Consumer: input.InboxEvent.Consumer, Subject: input.InboxEvent.Subject, Payload: payload, ReceivedAt: input.InboxEvent.ReceivedAt})
	if err != nil {
		t.Fatal(err)
	}
	input.InboxEvent = claim.Event
	simulatedLoss := errors.New("simulated committed response loss")
	repository.afterCommit = func() error { return simulatedLoss }
	if err := repository.Apply(ctx, input); !errors.Is(err, simulatedLoss) {
		t.Fatalf("first apply error = %v, want simulated response loss", err)
	}
	if err := repository.Apply(ctx, input); err != nil {
		t.Fatalf("replay after response loss = %v", err)
	}

	filters := map[string]bson.D{
		"inbox":      {{Key: "eventId", Value: input.InboxEvent.EventID}, {Key: "consumer", Value: input.InboxEvent.Consumer}},
		"record":     {{Key: "tenantId", Value: input.Record.TenantID}, {Key: "workspaceId", Value: input.Record.WorkspaceID}, {Key: "tableId", Value: input.Record.TableID}, {Key: "_id", Value: input.Record.ID}},
		"checkpoint": projectionCheckpointFilter(input.Checkpoint),
		"audit":      {{Key: "tenantId", Value: input.Record.TenantID}, {Key: "workspaceId", Value: input.Record.WorkspaceID}, {Key: "action", Value: input.Audit.Action}, {Key: "resourceId", Value: input.Record.ID}},
	}
	collections := map[string]*mongo.Collection{
		"inbox": database.Collection(inboxCollectionName), "record": database.Collection("records"),
		"checkpoint": database.Collection(checkpointCollectionName), "audit": database.Collection("audit_entries"),
	}
	for name, filter := range filters {
		count, err := collections[name].CountDocuments(ctx, filter)
		if err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want exactly one", name, count)
		}
	}
	var stored records.Record
	if err := database.Collection("records").FindOne(ctx, filters["record"]).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.RecordVersion != input.Record.RecordVersion || stored.Projection == nil || stored.Projection.LastEventID != input.InboxEvent.EventID {
		t.Fatalf("replay changed record version/state: %#v", stored)
	}
}
