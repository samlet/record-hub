package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samlet/record-hub/contracts/eventenvelope"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrInboxNotFound        = errors.New("inbox event not found")
	ErrInboxPayloadConflict = errors.New("inbox event payload hash conflict")
	ErrInboxStateConflict   = errors.New("inbox event state conflict")
)

type InboxStatus string

const (
	InboxProcessing InboxStatus = "PROCESSING"
	InboxApplied    InboxStatus = "APPLIED"
	InboxRejected   InboxStatus = "REJECTED"
	InboxFailed     InboxStatus = "FAILED"
)

type InboxEvent struct {
	EventID     string      `bson:"eventId" json:"eventId"`
	Consumer    string      `bson:"consumer" json:"consumer"`
	Subject     string      `bson:"subject" json:"subject"`
	PayloadHash string      `bson:"payloadHash" json:"payloadHash"`
	Status      InboxStatus `bson:"status" json:"status"`
	ReceivedAt  time.Time   `bson:"receivedAt" json:"receivedAt"`
	AppliedAt   *time.Time  `bson:"appliedAt,omitempty" json:"appliedAt,omitempty"`
	SafeError   string      `bson:"safeError,omitempty" json:"safeError,omitempty"`
}

type InboxClaim struct {
	EventID    string
	Consumer   string
	Subject    string
	Payload    []byte
	ReceivedAt time.Time
}

type InboxClaimResult struct {
	Event     InboxEvent
	Duplicate bool
}

func (event InboxEvent) Validate() error {
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.Consumer) == "" || strings.TrimSpace(event.Subject) == "" || strings.TrimSpace(event.PayloadHash) == "" {
		return errors.New("inbox event id, consumer, subject, and payload hash are required")
	}
	if event.Status != InboxProcessing && event.Status != InboxApplied && event.Status != InboxRejected && event.Status != InboxFailed {
		return errors.New("inbox event status is invalid")
	}
	if event.ReceivedAt.IsZero() {
		return errors.New("inbox event receivedAt is required")
	}
	if event.SafeError != "" && len(event.SafeError) > 512 {
		return errors.New("inbox event safeError cannot exceed 512 characters")
	}
	return nil
}

type InboxRepository interface {
	Claim(context.Context, InboxClaim) (InboxClaimResult, error)
	Get(context.Context, string, string) (InboxEvent, error)
	MarkApplied(context.Context, string, string, time.Time) error
	MarkRejected(context.Context, string, string, string, time.Time) error
}

type MongoInboxRepository struct {
	collection *mongo.Collection
}

const inboxCollectionName = "inbox_events"

func NewMongoInboxRepository(database *mongo.Database) *MongoInboxRepository {
	return &MongoInboxRepository{collection: database.Collection(inboxCollectionName)}
}

func (repository *MongoInboxRepository) EnsureIndexes(ctx context.Context) error {
	if _, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "consumer", Value: 1}, {Key: "eventId", Value: 1}}, Options: options.Index().SetName("inbox_consumer_event_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "consumer", Value: 1}, {Key: "status", Value: 1}, {Key: "receivedAt", Value: -1}}, Options: options.Index().SetName("inbox_consumer_status_received")},
	}); err != nil {
		return fmt.Errorf("create inbox indexes: %w", err)
	}
	return nil
}

func (repository *MongoInboxRepository) Claim(ctx context.Context, claim InboxClaim) (InboxClaimResult, error) {
	event, err := normalizeInboxClaim(claim)
	if err != nil {
		return InboxClaimResult{}, err
	}
	if _, err := repository.collection.InsertOne(ctx, event); err != nil {
		if !mongo.IsDuplicateKeyError(err) {
			return InboxClaimResult{}, fmt.Errorf("insert inbox event: %w", err)
		}
		existing, lookupErr := repository.Get(ctx, event.EventID, event.Consumer)
		if lookupErr != nil {
			return InboxClaimResult{}, lookupErr
		}
		if existing.PayloadHash != event.PayloadHash {
			return InboxClaimResult{}, ErrInboxPayloadConflict
		}
		return InboxClaimResult{Event: existing, Duplicate: true}, nil
	}
	return InboxClaimResult{Event: event}, nil
}

func (repository *MongoInboxRepository) Get(ctx context.Context, eventID, consumer string) (InboxEvent, error) {
	var event InboxEvent
	err := repository.collection.FindOne(ctx, bson.D{{Key: "eventId", Value: eventID}, {Key: "consumer", Value: consumer}}).Decode(&event)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return InboxEvent{}, ErrInboxNotFound
	}
	if err != nil {
		return InboxEvent{}, fmt.Errorf("find inbox event: %w", err)
	}
	return event, nil
}

func (repository *MongoInboxRepository) MarkApplied(ctx context.Context, eventID, consumer string, appliedAt time.Time) error {
	if appliedAt.IsZero() {
		return errors.New("inbox appliedAt is required")
	}
	appliedAt = appliedAt.UTC()
	result, err := repository.collection.UpdateOne(ctx, bson.D{{Key: "eventId", Value: eventID}, {Key: "consumer", Value: consumer}, {Key: "status", Value: InboxProcessing}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: InboxApplied}, {Key: "appliedAt", Value: appliedAt}}}})
	if err != nil {
		return fmt.Errorf("mark inbox event applied: %w", err)
	}
	if result.ModifiedCount == 1 {
		return nil
	}
	return repository.checkInboxState(ctx, eventID, consumer, InboxApplied)
}

func (repository *MongoInboxRepository) MarkRejected(ctx context.Context, eventID, consumer, safeError string, rejectedAt time.Time) error {
	safeError = strings.TrimSpace(safeError)
	if safeError == "" || len(safeError) > 512 {
		return errors.New("safe inbox error must be between 1 and 512 characters")
	}
	if rejectedAt.IsZero() {
		return errors.New("inbox rejectedAt is required")
	}
	rejectedAt = rejectedAt.UTC()
	result, err := repository.collection.UpdateOne(ctx, bson.D{{Key: "eventId", Value: eventID}, {Key: "consumer", Value: consumer}, {Key: "status", Value: InboxProcessing}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: InboxRejected}, {Key: "safeError", Value: safeError}, {Key: "appliedAt", Value: rejectedAt}}}})
	if err != nil {
		return fmt.Errorf("mark inbox event rejected: %w", err)
	}
	if result.ModifiedCount == 1 {
		return nil
	}
	return repository.checkInboxState(ctx, eventID, consumer, InboxRejected)
}

func (repository *MongoInboxRepository) checkInboxState(ctx context.Context, eventID, consumer string, want InboxStatus) error {
	event, err := repository.Get(ctx, eventID, consumer)
	if err != nil {
		return err
	}
	if event.Status == want {
		return nil
	}
	return ErrInboxStateConflict
}

func normalizeInboxClaim(claim InboxClaim) (InboxEvent, error) {
	eventID := strings.TrimSpace(claim.EventID)
	consumer := strings.TrimSpace(claim.Consumer)
	subject := strings.TrimSpace(claim.Subject)
	if eventID == "" || consumer == "" || subject == "" || len(eventID) > 256 || len(consumer) > 256 || len(subject) > 256 {
		return InboxEvent{}, errors.New("inbox event id, consumer, and subject are required and bounded")
	}
	if strings.IndexFunc(eventID+consumer+subject, func(r rune) bool { return r == '\n' || r == '\r' || r == '\t' || r == ' ' }) >= 0 {
		return InboxEvent{}, errors.New("inbox event identifiers cannot contain whitespace")
	}
	if len(claim.Payload) == 0 || len(claim.Payload) > eventenvelope.MaxEnvelopeBytes {
		return InboxEvent{}, errors.New("inbox event payload must be between 1 byte and 256 KiB")
	}
	if claim.ReceivedAt.IsZero() {
		return InboxEvent{}, errors.New("inbox event receivedAt is required")
	}
	digest := sha256.Sum256(claim.Payload)
	event := InboxEvent{EventID: eventID, Consumer: consumer, Subject: subject, PayloadHash: "sha256:" + hex.EncodeToString(digest[:]), Status: InboxProcessing, ReceivedAt: claim.ReceivedAt.UTC()}
	return event, event.Validate()
}
