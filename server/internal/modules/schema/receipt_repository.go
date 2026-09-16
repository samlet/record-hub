package schema

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const receiptCollectionName = "idempotency_receipts"

type MongoReceiptStore struct {
	collection *mongo.Collection
}

func NewMongoReceiptStore(database *mongo.Database) *MongoReceiptStore {
	return &MongoReceiptStore{collection: database.Collection(receiptCollectionName)}
}

func (store *MongoReceiptStore) EnsureIndexes(ctx context.Context) error {
	_, err := store.collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "operation", Value: 1}, {Key: "idempotencyKey", Value: 1}},
		Options: options.Index().SetName("receipt_scope_key_unique").SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create receipt index: %w", err)
	}
	return nil
}

func (store *MongoReceiptStore) Find(ctx context.Context, tenantID, workspaceID, operation, key string) (Receipt, error) {
	var receipt Receipt
	err := store.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "operation", Value: operation}, {Key: "idempotencyKey", Value: key}}).Decode(&receipt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Receipt{}, ErrReceiptNotFound
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("find idempotency receipt: %w", err)
	}
	return receipt, nil
}

func (store *MongoReceiptStore) Save(ctx context.Context, receipt Receipt) error {
	if _, err := store.collection.InsertOne(ctx, receipt); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			existing, findErr := store.Find(ctx, receipt.TenantID, receipt.WorkspaceID, receipt.Operation, receipt.IdempotencyKey)
			if findErr == nil && existing.RequestHash == receipt.RequestHash {
				return nil
			}
			return ErrIdempotencyConflict
		}
		return fmt.Errorf("insert idempotency receipt: %w", err)
	}
	return nil
}
