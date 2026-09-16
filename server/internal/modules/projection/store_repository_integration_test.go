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

func TestMongoProjectionApplyTransaction(t *testing.T) {
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
	claim := InboxClaim{EventID: input.InboxEvent.EventID, Consumer: input.InboxEvent.Consumer, Subject: input.InboxEvent.Subject, Payload: []byte(`{"eventId":"event-1"}`), ReceivedAt: input.InboxEvent.ReceivedAt}
	claimed, err := (&MongoInboxRepository{collection: database.Collection(inboxCollectionName)}).Claim(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	input.InboxEvent = claimed.Event
	if err := repository.Apply(ctx, input); err != nil {
		t.Fatal(err)
	}
	var storedInbox InboxEvent
	err = repository.inbox.FindOne(ctx, bson.D{{Key: "eventId", Value: input.InboxEvent.EventID}, {Key: "consumer", Value: input.InboxEvent.Consumer}}).Decode(&storedInbox)
	if err != nil {
		t.Fatal(err)
	}
	if storedInbox.Status != InboxApplied {
		t.Fatalf("inbox status = %s", storedInbox.Status)
	}
	if err := repository.Apply(ctx, input); err != nil {
		t.Fatalf("idempotent applied projection = %v", err)
	}
	input.InboxEvent.PayloadHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := repository.Apply(ctx, input); !errors.Is(err, ErrInboxPayloadConflict) {
		t.Fatalf("applied payload conflict = %v", err)
	}
	// A future event is retained in PROCESSING while the checkpoint advertises
	// a gap; once the missing version arrives, the future event can be applied.
	gapInput := input
	gapInput.InboxEvent = InboxEvent{EventID: "event-3", Consumer: input.InboxEvent.Consumer, Subject: input.InboxEvent.Subject, PayloadHash: "", Status: InboxProcessing, ReceivedAt: time.Now().UTC()}
	gapInput.Record.ID = "record-3"
	gapInput.Record.Projection = &records.ProjectionState{LastEventID: "event-3", SyncedAt: gapInput.InboxEvent.ReceivedAt, Status: "CURRENT"}
	gapInput.Checkpoint.SourceVersion = 3
	gapInput.Checkpoint.LastEventID = "event-3"
	gapInput.Checkpoint.SyncedAt = gapInput.InboxEvent.ReceivedAt
	gapInput.Audit.ResourceID = gapInput.Record.ID
	gapPayload := []byte(`{"eventId":"event-3"}`)
	gapClaim, err := (&MongoInboxRepository{collection: database.Collection(inboxCollectionName)}).Claim(ctx, InboxClaim{EventID: "event-3", Consumer: input.InboxEvent.Consumer, Subject: input.InboxEvent.Subject, Payload: gapPayload, ReceivedAt: gapInput.InboxEvent.ReceivedAt})
	if err != nil {
		t.Fatal(err)
	}
	gapInput.InboxEvent = gapClaim.Event
	if err := repository.Apply(ctx, gapInput); !errors.Is(err, ErrProjectionVersionGap) {
		t.Fatalf("version gap = %v", err)
	}
	missingInput := input
	missingInput.InboxEvent = InboxEvent{EventID: "event-2", Consumer: input.InboxEvent.Consumer, Subject: input.InboxEvent.Subject, Status: InboxProcessing, ReceivedAt: time.Now().UTC()}
	missingInput.Record.ID = "record-2"
	missingInput.Record.Projection = &records.ProjectionState{LastEventID: "event-2", SyncedAt: missingInput.InboxEvent.ReceivedAt, Status: "CURRENT"}
	missingInput.Checkpoint.SourceVersion = 2
	missingInput.Checkpoint.LastEventID = "event-2"
	missingInput.Checkpoint.SyncedAt = missingInput.InboxEvent.ReceivedAt
	missingInput.Audit.ResourceID = missingInput.Record.ID
	missingClaim, err := (&MongoInboxRepository{collection: database.Collection(inboxCollectionName)}).Claim(ctx, InboxClaim{EventID: "event-2", Consumer: input.InboxEvent.Consumer, Subject: input.InboxEvent.Subject, Payload: []byte(`{"eventId":"event-2"}`), ReceivedAt: missingInput.InboxEvent.ReceivedAt})
	if err != nil {
		t.Fatal(err)
	}
	missingInput.InboxEvent = missingClaim.Event
	if err := repository.Apply(ctx, missingInput); err != nil {
		t.Fatalf("missing version apply = %v", err)
	}
	if err := repository.Apply(ctx, gapInput); err != nil {
		t.Fatalf("gap recovery apply = %v", err)
	}
}
