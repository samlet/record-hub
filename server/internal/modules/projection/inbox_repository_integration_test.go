package projection

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestMongoInboxClaimPersistence(t *testing.T) {
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
	repository := NewMongoInboxRepository(client.Database("record_hub"))
	defer func() { _ = repository.collection.Drop(context.Background()) }()
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	claim := InboxClaim{EventID: "event-mongo-1", Consumer: "record-hub-approver-v1", Subject: "events.approver.application.changed.v1", Payload: []byte(`{"eventId":"event-mongo-1","payload":{"status":"OPEN"}}`), ReceivedAt: time.Now().UTC()}
	first, err := repository.Claim(ctx, claim)
	if err != nil || first.Duplicate {
		t.Fatalf("first mongo claim = %#v, %v", first, err)
	}
	second, err := repository.Claim(ctx, claim)
	if err != nil || !second.Duplicate {
		t.Fatalf("duplicate mongo claim = %#v, %v", second, err)
	}
	claim.Payload = []byte(`{"eventId":"event-mongo-1","payload":{"status":"CLOSED"}}`)
	if _, err := repository.Claim(ctx, claim); !errors.Is(err, ErrInboxPayloadConflict) {
		t.Fatalf("mongo payload conflict = %v", err)
	}
	if err := repository.MarkApplied(ctx, first.Event.EventID, first.Event.Consumer, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Get(ctx, first.Event.EventID, first.Event.Consumer)
	if err != nil || stored.Status != InboxApplied || stored.AppliedAt == nil {
		t.Fatalf("stored applied inbox event = %#v, %v", stored, err)
	}
}
