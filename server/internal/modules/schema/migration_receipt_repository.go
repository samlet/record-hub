package schema

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const migrationPlanReceiptCollectionName = "migration_plan_receipts"

type MongoMigrationPlanReceiptStore struct {
	collection *mongo.Collection
}

func NewMongoMigrationPlanReceiptStore(database *mongo.Database) *MongoMigrationPlanReceiptStore {
	return &MongoMigrationPlanReceiptStore{collection: database.Collection(migrationPlanReceiptCollectionName)}
}

func (store *MongoMigrationPlanReceiptStore) EnsureIndexes(ctx context.Context) error {
	if store == nil || store.collection == nil {
		return errors.New("migration plan receipt store is not configured")
	}
	_, err := store.collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "operation", Value: 1}, {Key: "idempotencyKey", Value: 1}},
		Options: options.Index().SetName("migration_receipt_scope_key_unique").SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create migration plan receipt index: %w", err)
	}
	return nil
}

func (store *MongoMigrationPlanReceiptStore) Find(ctx context.Context, tenantID, workspaceID, operation, key string) (MigrationPlanReceipt, error) {
	if store == nil || store.collection == nil {
		return MigrationPlanReceipt{}, errors.New("migration plan receipt store is not configured")
	}
	var receipt MigrationPlanReceipt
	err := store.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "operation", Value: operation}, {Key: "idempotencyKey", Value: key}}).Decode(&receipt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return MigrationPlanReceipt{}, ErrReceiptNotFound
	}
	if err != nil {
		return MigrationPlanReceipt{}, fmt.Errorf("find migration plan receipt: %w", err)
	}
	return receipt, nil
}

func (store *MongoMigrationPlanReceiptStore) Save(ctx context.Context, receipt MigrationPlanReceipt) error {
	if store == nil || store.collection == nil {
		return errors.New("migration plan receipt store is not configured")
	}
	if receipt.TenantID == "" || receipt.WorkspaceID == "" || receipt.Operation == "" || receipt.IdempotencyKey == "" || receipt.RequestHash == "" {
		return errors.New("incomplete migration plan receipt")
	}
	if _, err := store.collection.InsertOne(ctx, receipt); err != nil {
		if !mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("insert migration plan receipt: %w", err)
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
