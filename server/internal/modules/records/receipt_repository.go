package records

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const recordReceiptCollectionName = "record_idempotency_receipts"

type RecordReceipt struct {
	TenantID       string `bson:"tenantId"`
	WorkspaceID    string `bson:"workspaceId"`
	Operation      string `bson:"operation"`
	IdempotencyKey string `bson:"idempotencyKey"`
	RequestHash    string `bson:"requestHash"`
	Record         Record `bson:"record"`
}

type RecordReceiptStore interface {
	Find(context.Context, string, string, string, string) (RecordReceipt, error)
	Save(context.Context, RecordReceipt) error
}

type MongoRecordReceiptStore struct {
	collection *mongo.Collection
}

func NewMongoRecordReceiptStore(database *mongo.Database) *MongoRecordReceiptStore {
	return &MongoRecordReceiptStore{collection: database.Collection(recordReceiptCollectionName)}
}

func (store *MongoRecordReceiptStore) EnsureIndexes(ctx context.Context) error {
	_, err := store.collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "operation", Value: 1}, {Key: "idempotencyKey", Value: 1}}, Options: options.Index().SetName("record_receipt_identity_unique").SetUnique(true)})
	if err != nil {
		return fmt.Errorf("create record receipt indexes: %w", err)
	}
	return nil
}

func (store *MongoRecordReceiptStore) Find(ctx context.Context, tenantID, workspaceID, operation, key string) (RecordReceipt, error) {
	var receipt RecordReceipt
	err := store.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "operation", Value: operation}, {Key: "idempotencyKey", Value: key}}).Decode(&receipt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return RecordReceipt{}, ErrReceiptNotFound
	}
	if err != nil {
		return RecordReceipt{}, fmt.Errorf("find record receipt: %w", err)
	}
	return receipt, nil
}

func (store *MongoRecordReceiptStore) Save(ctx context.Context, receipt RecordReceipt) error {
	if receipt.TenantID == "" || receipt.WorkspaceID == "" || receipt.Operation == "" || receipt.IdempotencyKey == "" || receipt.RequestHash == "" {
		return errors.New("incomplete record receipt")
	}
	if _, err := store.collection.InsertOne(ctx, receipt); err != nil {
		if !mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("insert record receipt: %w", err)
		}
		existing, findErr := store.Find(ctx, receipt.TenantID, receipt.WorkspaceID, receipt.Operation, receipt.IdempotencyKey)
		if findErr != nil {
			return findErr
		}
		if existing.RequestHash != receipt.RequestHash {
			return ErrIdempotencyConflict
		}
		return nil
	}
	return nil
}
