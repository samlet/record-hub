package schema

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrNotFound         = errors.New("schema definition not found")
	ErrDuplicate        = errors.New("schema definition already exists")
	ErrImmutable        = errors.New("published schema is immutable")
	ErrRevisionConflict = errors.New("schema revision conflict")
)

const collectionName = "schema_definitions"
const migrationPlanCollectionName = "migration_plans"

type MongoRepository struct {
	collection     *mongo.Collection
	migrationPlans *mongo.Collection
	clock          func() time.Time
}

// WithTransaction lets mutation services atomically couple a registry change
// with its audit entry and idempotency receipt. Context passed to fn is a
// mongo.SessionContext and must be used by every participating adapter.
func (repository *MongoRepository) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	session, err := repository.collection.Database().Client().StartSession()
	if err != nil {
		return fmt.Errorf("start schema transaction: %w", err)
	}
	defer session.EndSession(context.Background())
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (interface{}, error) {
		return nil, fn(transactionContext)
	})
	return err
}

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{collection: database.Collection(collectionName), migrationPlans: database.Collection(migrationPlanCollectionName), clock: time.Now}
}

func (repository *MongoRepository) EnsureIndexes(ctx context.Context) error {
	_, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "name", Value: 1}, {Key: "version", Value: 1}},
			Options: options.Index().SetName("schema_tenant_name_version_unique").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "schemaId", Value: 1}, {Key: "version", Value: 1}},
			Options: options.Index().SetName("schema_tenant_id_version_unique").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "status", Value: 1}, {Key: "updatedAt", Value: -1}},
			Options: options.Index().SetName("schema_tenant_status_updated"),
		},
	})
	if err != nil {
		return fmt.Errorf("create schema indexes: %w", err)
	}
	_, err = repository.migrationPlans.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "schemaId", Value: 1}, {Key: "fromVersion", Value: 1}, {Key: "toVersion", Value: 1}, {Key: "mode", Value: 1}, {Key: "compatibilityHash", Value: 1}},
			Options: options.Index().SetName("migration_plan_fingerprint_unique").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "schemaId", Value: 1}, {Key: "updatedAt", Value: -1}},
			Options: options.Index().SetName("migration_plan_scope_updated"),
		},
	})
	if err != nil {
		return fmt.Errorf("create migration plan indexes: %w", err)
	}
	return nil
}

func (repository *MongoRepository) Create(ctx context.Context, definition Definition) error {
	if err := definition.Validate(); err != nil {
		return err
	}
	if _, err := repository.collection.InsertOne(ctx, definition); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert schema definition: %w", err)
	}
	return nil
}

func (repository *MongoRepository) Get(ctx context.Context, tenantID, schemaID string, version int64) (Definition, error) {
	var definition Definition
	err := repository.collection.FindOne(ctx, bson.D{
		{Key: "tenantId", Value: tenantID},
		{Key: "schemaId", Value: schemaID},
		{Key: "version", Value: version},
	}).Decode(&definition)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Definition{}, ErrNotFound
	}
	if err != nil {
		return Definition{}, fmt.Errorf("find schema definition: %w", err)
	}
	return definition, nil
}

func (repository *MongoRepository) List(ctx context.Context, tenantID string, limit int64) ([]Definition, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("schema list limit must be between 1 and 100")
	}
	cursor, err := repository.collection.Find(
		ctx,
		bson.D{{Key: "tenantId", Value: tenantID}},
		options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}, {Key: "schemaId", Value: 1}, {Key: "version", Value: -1}}).SetLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list schema definitions: %w", err)
	}
	defer cursor.Close(ctx)
	var definitions []Definition
	if err := cursor.All(ctx, &definitions); err != nil {
		return nil, fmt.Errorf("decode schema definitions: %w", err)
	}
	return definitions, nil
}

func (repository *MongoRepository) CreateMigrationPlan(ctx context.Context, plan MigrationPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if _, err := repository.migrationPlans.InsertOne(ctx, plan); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrMigrationPlanExists
		}
		return fmt.Errorf("insert migration plan: %w", err)
	}
	return nil
}

func (repository *MongoRepository) FindMigrationPlan(ctx context.Context, tenantID, workspaceID, schemaID, planID string) (MigrationPlan, error) {
	var plan MigrationPlan
	err := repository.migrationPlans.FindOne(ctx, bson.D{{Key: "_id", Value: planID}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "schemaId", Value: schemaID}}).Decode(&plan)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return MigrationPlan{}, ErrMigrationPlanNotFound
	}
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("find migration plan: %w", err)
	}
	return plan, nil
}

func (repository *MongoRepository) FindMigrationPlanByFingerprint(ctx context.Context, tenantID, workspaceID, schemaID string, fromVersion, toVersion int64, mode MigrationPlanMode, compatibilityHash string) (MigrationPlan, error) {
	var plan MigrationPlan
	err := repository.migrationPlans.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "schemaId", Value: schemaID}, {Key: "fromVersion", Value: fromVersion}, {Key: "toVersion", Value: toVersion}, {Key: "mode", Value: mode}, {Key: "compatibilityHash", Value: compatibilityHash}}).Decode(&plan)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return MigrationPlan{}, ErrMigrationPlanNotFound
	}
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("find migration plan by fingerprint: %w", err)
	}
	return plan, nil
}

func (repository *MongoRepository) UpdateMigrationPlan(ctx context.Context, plan MigrationPlan, expectedVersion int64) (MigrationPlan, error) {
	if err := plan.Validate(); err != nil {
		return MigrationPlan{}, err
	}
	filter := bson.D{{Key: "_id", Value: plan.ID}, {Key: "tenantId", Value: plan.TenantID}, {Key: "workspaceId", Value: plan.WorkspaceID}, {Key: "schemaId", Value: plan.SchemaID}, {Key: "version", Value: expectedVersion}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: plan.Status}, {Key: "updatedBy", Value: plan.UpdatedBy}, {Key: "updatedAt", Value: plan.UpdatedAt}, {Key: "cancelRequestedAt", Value: plan.CancelRequestedAt}}}, {Key: "$inc", Value: bson.D{{Key: "version", Value: 1}}}}
	var updated MigrationPlan
	err := repository.migrationPlans.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err == nil {
		return updated, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return MigrationPlan{}, fmt.Errorf("update migration plan: %w", err)
	}
	if _, lookupErr := repository.FindMigrationPlan(ctx, plan.TenantID, plan.WorkspaceID, plan.SchemaID, plan.ID); errors.Is(lookupErr, ErrMigrationPlanNotFound) {
		return MigrationPlan{}, ErrMigrationPlanNotFound
	}
	return MigrationPlan{}, ErrMigrationPlanConflict
}

// UpdateDraft is the only general mutation path. Filtering on DRAFT makes
// published and deprecated documents immutable even under concurrent callers.
func (repository *MongoRepository) UpdateDraft(ctx context.Context, definition Definition, expectedRevision int64) (Definition, error) {
	if definition.Status != StatusDraft {
		return Definition{}, ErrImmutable
	}
	if err := definition.Validate(); err != nil {
		return Definition{}, err
	}
	now := repository.clock().UTC()
	filter := bson.D{
		{Key: "tenantId", Value: definition.TenantID},
		{Key: "schemaId", Value: definition.SchemaID},
		{Key: "version", Value: definition.Version},
		{Key: "status", Value: StatusDraft},
		{Key: "revision", Value: expectedRevision},
	}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "name", Value: definition.Name},
		{Key: "jsonSchema", Value: definition.JSONSchema},
		{Key: "semanticTypes", Value: definition.SemanticTypes},
		{Key: "updatedAt", Value: now},
	}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: 1}}}}
	var updated Definition
	err := repository.collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err == nil {
		return updated, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		if mongo.IsDuplicateKeyError(err) {
			return Definition{}, ErrDuplicate
		}
		return Definition{}, fmt.Errorf("update schema draft: %w", err)
	}

	existing, lookupErr := repository.Get(ctx, definition.TenantID, definition.SchemaID, definition.Version)
	if lookupErr != nil {
		return Definition{}, lookupErr
	}
	if existing.Status != StatusDraft {
		return Definition{}, ErrImmutable
	}
	return Definition{}, ErrRevisionConflict
}

// PublishDefinition atomically transitions one draft to PUBLISHED. The status
// and revision predicates prevent a second publisher from changing the record.
func (repository *MongoRepository) PublishDefinition(ctx context.Context, tenantID, schemaID string, version, expectedRevision int64, publisher identity.IdentityKey, contentHash string) (Definition, error) {
	now := repository.clock().UTC()
	filter := bson.D{
		{Key: "tenantId", Value: tenantID},
		{Key: "schemaId", Value: schemaID},
		{Key: "version", Value: version},
		{Key: "status", Value: StatusDraft},
		{Key: "revision", Value: expectedRevision},
	}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: StatusPublished},
		{Key: "contentHash", Value: contentHash},
		{Key: "publishedBy", Value: publisher},
		{Key: "publishedAt", Value: now},
		{Key: "updatedAt", Value: now},
	}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: 1}}}}
	var published Definition
	err := repository.collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&published)
	if err == nil {
		return published, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return Definition{}, fmt.Errorf("publish schema definition: %w", err)
	}
	existing, lookupErr := repository.Get(ctx, tenantID, schemaID, version)
	if lookupErr != nil {
		return Definition{}, lookupErr
	}
	if existing.Status != StatusDraft {
		return Definition{}, ErrImmutable
	}
	return Definition{}, ErrRevisionConflict
}
