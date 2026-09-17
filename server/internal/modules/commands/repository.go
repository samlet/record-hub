package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const operationCollectionName = "command_operations"

type MongoStore struct{ collection *mongo.Collection }

func NewMongoStore(database *mongo.Database) *MongoStore {
	if database == nil {
		return &MongoStore{}
	}
	return &MongoStore{collection: database.Collection(operationCollectionName)}
}

func (store *MongoStore) EnsureIndexes(ctx context.Context) error {
	if store == nil || store.collection == nil {
		return ErrCommandUnavailable
	}
	if _, err := store.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "policyId", Value: 1}, {Key: "idempotencyKey", Value: 1}}, Options: options.Index().SetName("command_scope_idempotency_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "operationId", Value: 1}, {Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}}, Options: options.Index().SetName("command_operation_scope_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "updatedAt", Value: 1}}, Options: options.Index().SetName("command_status_updated")},
	}); err != nil {
		return fmt.Errorf("create command indexes: %w", err)
	}
	return nil
}

func (store *MongoStore) FindByIdempotency(ctx context.Context, tenantID, workspaceID, policyID, key string) (Operation, error) {
	if store == nil || store.collection == nil {
		return Operation{}, ErrCommandUnavailable
	}
	var operation Operation
	err := store.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "policyId", Value: policyID}, {Key: "idempotencyKey", Value: key}}).Decode(&operation)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Operation{}, ErrCommandNotFound
	}
	if err != nil {
		return Operation{}, fmt.Errorf("find command idempotency receipt: %w", err)
	}
	return operation, nil
}

func (store *MongoStore) Find(ctx context.Context, tenantID, workspaceID, operationID string) (Operation, error) {
	if store == nil || store.collection == nil {
		return Operation{}, ErrCommandUnavailable
	}
	var operation Operation
	err := store.collection.FindOne(ctx, bson.D{{Key: "operationId", Value: operationID}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}).Decode(&operation)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Operation{}, ErrCommandNotFound
	}
	if err != nil {
		return Operation{}, fmt.Errorf("find command operation: %w", err)
	}
	return operation, nil
}

func (store *MongoStore) Create(ctx context.Context, operation Operation) error {
	if store == nil || store.collection == nil {
		return ErrCommandUnavailable
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	if _, err := store.collection.InsertOne(ctx, operation); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrCommandExists
		}
		return fmt.Errorf("insert command operation: %w", err)
	}
	return nil
}

func (store *MongoStore) SaveCAS(ctx context.Context, operation Operation, expectedRevision int64) (Operation, error) {
	if store == nil || store.collection == nil {
		return Operation{}, ErrCommandUnavailable
	}
	if err := operation.Validate(); err != nil {
		return Operation{}, err
	}
	var result Operation
	err := store.collection.FindOneAndUpdate(ctx, bson.D{{Key: "operationId", Value: operation.ID}, {Key: "tenantId", Value: operation.TenantID}, {Key: "workspaceId", Value: operation.WorkspaceID}, {Key: "revision", Value: expectedRevision}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: operation.Status}, {Key: "revision", Value: operation.Revision}, {Key: "safeError", Value: operation.SafeError}, {Key: "resultEventId", Value: operation.ResultEventID}, {Key: "resultHash", Value: operation.ResultHash}, {Key: "resultVersion", Value: operation.ResultVersion}, {Key: "updatedAt", Value: operation.UpdatedAt}}}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) {
		current, findErr := store.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.ID)
		if findErr != nil {
			return Operation{}, findErr
		}
		if current.Revision != expectedRevision {
			return Operation{}, ErrCommandIdempotency
		}
		return Operation{}, ErrCommandNotFound
	}
	if err != nil {
		return Operation{}, fmt.Errorf("save command operation: %w", err)
	}
	return result, nil
}

type MemoryStore struct {
	values map[string]Operation
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{values: make(map[string]Operation)} }

func (store *MemoryStore) FindByIdempotency(_ context.Context, tenantID, workspaceID, policyID, key string) (Operation, error) {
	for _, operation := range store.values {
		if operation.TenantID == tenantID && operation.WorkspaceID == workspaceID && operation.PolicyID == policyID && operation.IdempotencyKey == key {
			return operation, nil
		}
	}
	return Operation{}, ErrCommandNotFound
}

func (store *MemoryStore) Find(_ context.Context, tenantID, workspaceID, operationID string) (Operation, error) {
	operation, ok := store.values[tenantID+"\x00"+workspaceID+"\x00"+operationID]
	if !ok {
		return Operation{}, ErrCommandNotFound
	}
	return operation, nil
}

func (store *MemoryStore) Create(_ context.Context, operation Operation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	key := operation.TenantID + "\x00" + operation.WorkspaceID + "\x00" + operation.ID
	if _, ok := store.values[key]; ok {
		return ErrCommandExists
	}
	if existing, err := store.FindByIdempotency(context.Background(), operation.TenantID, operation.WorkspaceID, operation.PolicyID, operation.IdempotencyKey); err == nil && existing.ID != operation.ID {
		return ErrCommandExists
	}
	store.values[key] = operation
	return nil
}

func (store *MemoryStore) SaveCAS(_ context.Context, operation Operation, expectedRevision int64) (Operation, error) {
	current, err := store.Find(context.Background(), operation.TenantID, operation.WorkspaceID, operation.ID)
	if err != nil {
		return Operation{}, err
	}
	if current.Revision != expectedRevision {
		return Operation{}, ErrCommandIdempotency
	}
	store.values[operation.TenantID+"\x00"+operation.WorkspaceID+"\x00"+operation.ID] = operation
	return operation, nil
}

func normalizeOperationID(operationID string) string { return strings.TrimSpace(operationID) }
