package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	projectionEventArchiveCollectionName = "projection_event_archive"
	MaxArchivedReplayEvents              = 100_000
	archiveReplayPageSize                = MaxArchivedReplayEvents + 1
)

var (
	ErrEventArchiveInvalid         = errors.New("invalid projection event archive entry")
	ErrEventArchivePayloadConflict = errors.New("projection event archive payload conflict")
	ErrEventArchiveUnavailable     = errors.New("projection event archive is unavailable")
)

// ProjectionEventArchiveEntry is the immutable replay envelope. Raw is kept
// alongside normalized routing fields so replay never depends on a lossy
// reconstruction of the original event.
type ProjectionEventArchiveEntry struct {
	EventID          string    `bson:"eventId" json:"eventId"`
	Consumer         string    `bson:"consumer" json:"consumer"`
	Subject          string    `bson:"subject" json:"subject"`
	TenantID         string    `bson:"tenantId" json:"tenantId"`
	WorkspaceID      string    `bson:"workspaceId" json:"workspaceId"`
	SourceSystem     string    `bson:"sourceSystem" json:"sourceSystem"`
	EventType        string    `bson:"eventType" json:"eventType"`
	SchemaVersion    int64     `bson:"schemaVersion" json:"schemaVersion"`
	AggregateType    string    `bson:"aggregateType" json:"aggregateType"`
	AggregateID      string    `bson:"aggregateId" json:"aggregateId"`
	AggregateVersion int64     `bson:"aggregateVersion" json:"aggregateVersion"`
	OccurredAt       time.Time `bson:"occurredAt" json:"occurredAt"`
	ReceivedAt       time.Time `bson:"receivedAt" json:"receivedAt"`
	PayloadHash      string    `bson:"payloadHash" json:"payloadHash"`
	Raw              []byte    `bson:"raw" json:"-"`
}

func (entry ProjectionEventArchiveEntry) Validate() error {
	if strings.TrimSpace(entry.EventID) == "" || strings.TrimSpace(entry.Consumer) == "" || strings.TrimSpace(entry.Subject) == "" || strings.TrimSpace(entry.TenantID) == "" || strings.TrimSpace(entry.WorkspaceID) == "" || strings.TrimSpace(entry.SourceSystem) == "" || strings.TrimSpace(entry.EventType) == "" || strings.TrimSpace(entry.AggregateType) == "" || strings.TrimSpace(entry.AggregateID) == "" || entry.SchemaVersion < 1 || entry.AggregateVersion < 1 || entry.OccurredAt.IsZero() || entry.ReceivedAt.IsZero() || len(entry.Raw) == 0 {
		return ErrEventArchiveInvalid
	}
	hash := sha256.Sum256(entry.Raw)
	if entry.PayloadHash != "sha256:"+hex.EncodeToString(hash[:]) {
		return ErrEventArchiveInvalid
	}
	return nil
}

type ProjectionEventArchiveWriter interface {
	Archive(context.Context, ProjectionEventArchiveEntry) error
}

type ProjectionEventArchiveReader interface {
	List(context.Context, string, string, int64) ([]ProjectionEventArchiveEntry, error)
}

type MongoProjectionEventArchive struct {
	collection *mongo.Collection
}

func NewMongoProjectionEventArchive(database *mongo.Database) *MongoProjectionEventArchive {
	if database == nil {
		return &MongoProjectionEventArchive{}
	}
	return &MongoProjectionEventArchive{collection: database.Collection(projectionEventArchiveCollectionName)}
}

func (repository *MongoProjectionEventArchive) EnsureIndexes(ctx context.Context) error {
	if repository == nil || repository.collection == nil {
		return ErrEventArchiveUnavailable
	}
	if _, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "consumer", Value: 1}, {Key: "eventId", Value: 1}}, Options: options.Index().SetName("projection_event_archive_event_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "occurredAt", Value: 1}, {Key: "eventId", Value: 1}}, Options: options.Index().SetName("projection_event_archive_scope_order")},
	}); err != nil {
		return fmt.Errorf("create projection event archive indexes: %w", err)
	}
	return nil
}

func (repository *MongoProjectionEventArchive) Archive(ctx context.Context, entry ProjectionEventArchiveEntry) error {
	if repository == nil || repository.collection == nil {
		return ErrEventArchiveUnavailable
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	// The archive is append-only. A duplicate delivery is accepted only when
	// the raw event is byte-identical; a conflicting payload is a hard error.
	_, err := repository.collection.InsertOne(ctx, entry)
	if err == nil {
		return nil
	}
	if !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("insert projection event archive: %w", err)
	}
	var existing ProjectionEventArchiveEntry
	if findErr := repository.collection.FindOne(ctx, bson.D{{Key: "consumer", Value: entry.Consumer}, {Key: "eventId", Value: entry.EventID}}).Decode(&existing); findErr != nil {
		return fmt.Errorf("find projection event archive duplicate: %w", findErr)
	}
	if existing.PayloadHash != entry.PayloadHash || string(existing.Raw) != string(entry.Raw) {
		return ErrEventArchivePayloadConflict
	}
	return nil
}

func (repository *MongoProjectionEventArchive) List(ctx context.Context, tenantID, workspaceID string, limit int64) ([]ProjectionEventArchiveEntry, error) {
	if repository == nil || repository.collection == nil {
		return nil, ErrEventArchiveUnavailable
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workspaceID) == "" || limit < 1 || limit > archiveReplayPageSize {
		return nil, ErrEventArchiveInvalid
	}
	cursor, err := repository.collection.Find(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}, options.Find().SetSort(bson.D{{Key: "occurredAt", Value: 1}, {Key: "eventId", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list projection event archive: %w", err)
	}
	defer cursor.Close(ctx)
	var entries []ProjectionEventArchiveEntry
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, fmt.Errorf("decode projection event archive: %w", err)
	}
	return entries, nil
}

func archiveEntryFromEnvelope(raw []byte, consumer, subject, receivedAt string) (ProjectionEventArchiveEntry, error) {
	// Kept as a small helper for tests and adapters that already have a JSON
	// envelope. The live projector uses the typed envelope directly.
	var envelope summaryEventEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ProjectionEventArchiveEntry{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, receivedAt)
	if err != nil {
		return ProjectionEventArchiveEntry{}, err
	}
	hash := sha256.Sum256(raw)
	return ProjectionEventArchiveEntry{EventID: envelope.EventID, Consumer: consumer, Subject: subject, TenantID: envelope.TenantID, WorkspaceID: envelope.Metadata["workspaceId"], SourceSystem: envelope.SourceSystem, EventType: envelope.EventType, SchemaVersion: envelope.SchemaVersion, AggregateType: envelope.AggregateType, AggregateID: envelope.AggregateID, AggregateVersion: envelope.AggregateVersion, OccurredAt: envelope.OccurredAt, ReceivedAt: parsed, PayloadHash: "sha256:" + hex.EncodeToString(hash[:]), Raw: append([]byte(nil), raw...)}, nil
}
