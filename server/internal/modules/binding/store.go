package binding

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const snapshotCollectionName = "binding_snapshots"

// MongoSnapshotStore persists the canonical snapshot document. The data field
// is stored as a BSON document rather than binary JSON so it remains queryable
// only by internal code and can be returned as ordinary JSON safely.
type MongoSnapshotStore struct {
	collection *mongo.Collection
}

func NewMongoSnapshotStore(database *mongo.Database) *MongoSnapshotStore {
	if database == nil {
		return &MongoSnapshotStore{}
	}
	return &MongoSnapshotStore{collection: database.Collection(snapshotCollectionName)}
}

func (store *MongoSnapshotStore) EnsureIndexes(ctx context.Context) error {
	if store == nil || store.collection == nil {
		return ErrBindingUnavailable
	}
	_, err := store.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "operationId", Value: 1}}, Options: options.Index().SetName("binding_snapshot_operation_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "createdAt", Value: -1}}, Options: options.Index().SetName("binding_snapshot_created")},
	})
	if err != nil {
		return fmt.Errorf("create binding snapshot indexes: %w", err)
	}
	return nil
}

func (store *MongoSnapshotStore) FindByOperation(ctx context.Context, tenantID, workspaceID, operationID string) (Snapshot, error) {
	if store == nil || store.collection == nil {
		return Snapshot{}, ErrBindingUnavailable
	}
	var document snapshotDocument
	err := store.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "operationId", Value: operationID}}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Snapshot{}, ErrSnapshotNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("find binding snapshot operation: %w", err)
	}
	return document.snapshot()
}

func (store *MongoSnapshotStore) Create(ctx context.Context, snapshot Snapshot) error {
	if store == nil || store.collection == nil {
		return ErrBindingUnavailable
	}
	document, err := newSnapshotDocument(snapshot)
	if err != nil {
		return err
	}
	if _, err := store.collection.InsertOne(ctx, document); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrSnapshotExists
		}
		return fmt.Errorf("insert binding snapshot: %w", err)
	}
	return nil
}

func (store *MongoSnapshotStore) Get(ctx context.Context, tenantID, workspaceID, snapshotID string) (Snapshot, error) {
	if store == nil || store.collection == nil {
		return Snapshot{}, ErrBindingUnavailable
	}
	var document snapshotDocument
	err := store.collection.FindOne(ctx, bson.D{{Key: "_id", Value: snapshotID}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Snapshot{}, ErrSnapshotNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("find binding snapshot: %w", err)
	}
	return document.snapshot()
}

type snapshotDocument struct {
	SnapshotID    string    `bson:"_id"`
	TenantID      string    `bson:"tenantId"`
	WorkspaceID   string    `bson:"workspaceId"`
	OperationID   string    `bson:"operationId"`
	RecordRef     string    `bson:"recordRef"`
	SchemaID      string    `bson:"schemaId"`
	SchemaVersion int64     `bson:"schemaVersion"`
	RecordVersion int64     `bson:"recordVersion"`
	SourceVersion int64     `bson:"sourceVersion"`
	Purpose       string    `bson:"purpose"`
	SnapshotHash  string    `bson:"snapshotHash"`
	Data          bson.Raw  `bson:"data"`
	RequestHash   string    `bson:"requestHash"`
	CreatedAt     time.Time `bson:"createdAt"`
}

func newSnapshotDocument(snapshot Snapshot) (snapshotDocument, error) {
	if err := snapshot.Validate(); err != nil {
		return snapshotDocument{}, err
	}
	var data bson.Raw
	if err := bson.UnmarshalExtJSON(snapshot.Data, false, &data); err != nil {
		return snapshotDocument{}, fmt.Errorf("encode snapshot data: %w", err)
	}
	return snapshotDocument{SnapshotID: snapshot.SnapshotID, TenantID: snapshot.TenantID, WorkspaceID: snapshot.WorkspaceID, OperationID: snapshot.OperationID, RecordRef: snapshot.RecordRef, SchemaID: snapshot.SchemaID, SchemaVersion: snapshot.SchemaVersion, RecordVersion: snapshot.RecordVersion, SourceVersion: snapshot.SourceVersion, Purpose: snapshot.Purpose, SnapshotHash: snapshot.SnapshotHash, Data: data, RequestHash: snapshot.RequestHash, CreatedAt: snapshot.CreatedAt.UTC()}, nil
}

func (document snapshotDocument) snapshot() (Snapshot, error) {
	data, err := bson.MarshalExtJSON(document.Data, false, false)
	if err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot data: %w", err)
	}
	snapshot := Snapshot{SnapshotID: document.SnapshotID, TenantID: document.TenantID, WorkspaceID: document.WorkspaceID, OperationID: document.OperationID, RecordRef: document.RecordRef, SchemaID: document.SchemaID, SchemaVersion: document.SchemaVersion, RecordVersion: document.RecordVersion, SourceVersion: document.SourceVersion, Purpose: document.Purpose, SnapshotHash: document.SnapshotHash, Data: data, RequestHash: document.RequestHash, CreatedAt: document.CreatedAt}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}
