package records

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
	ErrWorkspaceNotFound      = errors.New("workspace not found")
	ErrWorkspaceExists        = errors.New("workspace already exists")
	ErrTableNotFound          = errors.New("table not found")
	ErrTableExists            = errors.New("table already exists")
	ErrSchemaUnavailable      = errors.New("published schema is unavailable")
	ErrRecordNotFound         = errors.New("record not found")
	ErrRecordExists           = errors.New("record already exists")
	ErrRecordVersionConflict  = errors.New("record version conflict")
	ErrProjectionReadOnly     = errors.New("projection records are read-only")
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
	ErrIdempotencyConflict    = errors.New("idempotency key was used with different input")
	ErrReceiptNotFound        = errors.New("idempotency receipt not found")
)

const (
	workspaceCollectionName = "workspaces"
	tableCollectionName     = "table_definitions"
	recordCollectionName    = "records"
)

type WorkspaceRepository interface {
	CreateWorkspace(context.Context, Workspace) error
	GetWorkspace(context.Context, string, string) (Workspace, error)
	ListWorkspaces(context.Context, string, identity.IdentityKey) ([]Workspace, error)
}

type TableRepository interface {
	CreateTable(context.Context, TableDefinition) error
	GetTable(context.Context, string, string, string) (TableDefinition, error)
	ListTables(context.Context, string, string) ([]TableDefinition, error)
}

type RecordRepository interface {
	CreateRecord(context.Context, Record) error
	GetRecord(context.Context, string, string, string) (Record, error)
	UpdateRecord(context.Context, Record, int64) (Record, error)
	DeleteRecord(context.Context, string, string, string, int64) error
}

type MongoRepository struct {
	workspaces *mongo.Collection
	tables     *mongo.Collection
	records    *mongo.Collection
	clock      func() time.Time
}

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{workspaces: database.Collection(workspaceCollectionName), tables: database.Collection(tableCollectionName), records: database.Collection(recordCollectionName), clock: time.Now}
}

func (repository *MongoRepository) EnsureIndexes(ctx context.Context) error {
	if _, err := repository.workspaces.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("workspace_tenant_id_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "createdBy.issuer", Value: 1}, {Key: "createdBy.subject", Value: 1}}, Options: options.Index().SetName("workspace_tenant_creator")},
	}); err != nil {
		return fmt.Errorf("create workspace indexes: %w", err)
	}
	if _, err := repository.tables.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("table_tenant_workspace_id_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetName("table_workspace_name_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "kind", Value: 1}, {Key: "updatedAt", Value: -1}}, Options: options.Index().SetName("table_workspace_kind_updated")},
	}); err != nil {
		return fmt.Errorf("create table indexes: %w", err)
	}
	if _, err := repository.records.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("record_tenant_workspace_table_id_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "source.system", Value: 1}, {Key: "source.type", Value: 1}, {Key: "source.id", Value: 1}}, Options: options.Index().SetName("record_source_unique").SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "schemaId", Value: 1}, {Key: "schemaVersion", Value: 1}}, Options: options.Index().SetName("record_tenant_schema")},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "tags", Value: 1}}, Options: options.Index().SetName("record_table_tags")},
	}); err != nil {
		return fmt.Errorf("create record indexes: %w", err)
	}
	return nil
}

func (repository *MongoRepository) CreateWorkspace(ctx context.Context, workspace Workspace) error {
	if err := workspace.Validate(); err != nil {
		return err
	}
	if _, err := repository.workspaces.InsertOne(ctx, workspace); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrWorkspaceExists
		}
		return fmt.Errorf("insert workspace: %w", err)
	}
	return nil
}

func (repository *MongoRepository) GetWorkspace(ctx context.Context, tenantID, workspaceID string) (Workspace, error) {
	var workspace Workspace
	err := repository.workspaces.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "_id", Value: workspaceID}}).Decode(&workspace)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Workspace{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("find workspace: %w", err)
	}
	return workspace, nil
}

func (repository *MongoRepository) ListWorkspaces(ctx context.Context, tenantID string, creator identity.IdentityKey) ([]Workspace, error) {
	query := bson.D{{Key: "tenantId", Value: tenantID}}
	if creator.Issuer != "" || creator.Subject != "" {
		query = append(query, bson.E{Key: "createdBy", Value: creator})
	}
	cursor, err := repository.workspaces.Find(ctx, query, options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(100))
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer cursor.Close(ctx)
	var workspaces []Workspace
	if err := cursor.All(ctx, &workspaces); err != nil {
		return nil, fmt.Errorf("decode workspaces: %w", err)
	}
	return workspaces, nil
}

func (repository *MongoRepository) CreateTable(ctx context.Context, table TableDefinition) error {
	if err := table.Validate(); err != nil {
		return err
	}
	if _, err := repository.tables.InsertOne(ctx, table); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrTableExists
		}
		return fmt.Errorf("insert table: %w", err)
	}
	return nil
}

func (repository *MongoRepository) GetTable(ctx context.Context, tenantID, workspaceID, tableID string) (TableDefinition, error) {
	var table TableDefinition
	err := repository.tables.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "_id", Value: tableID}}).Decode(&table)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return TableDefinition{}, ErrTableNotFound
	}
	if err != nil {
		return TableDefinition{}, fmt.Errorf("find table: %w", err)
	}
	return table, nil
}

func (repository *MongoRepository) ListTables(ctx context.Context, tenantID, workspaceID string) ([]TableDefinition, error) {
	cursor, err := repository.tables.Find(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(100))
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer cursor.Close(ctx)
	var tables []TableDefinition
	if err := cursor.All(ctx, &tables); err != nil {
		return nil, fmt.Errorf("decode tables: %w", err)
	}
	return tables, nil
}

// WithTransaction atomically couples a record mutation with its audit entry
// and idempotency receipt. Every adapter in the callback must use the
// callback context so MongoDB binds the writes to the same session.
func (repository *MongoRepository) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	session, err := repository.records.Database().Client().StartSession()
	if err != nil {
		return fmt.Errorf("start record transaction: %w", err)
	}
	defer session.EndSession(context.Background())
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (interface{}, error) {
		return nil, fn(transactionContext)
	})
	return err
}

func (repository *MongoRepository) CreateRecord(ctx context.Context, record Record) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if _, err := repository.records.InsertOne(ctx, record); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrRecordExists
		}
		return fmt.Errorf("insert record: %w", err)
	}
	return nil
}

func (repository *MongoRepository) GetRecord(ctx context.Context, tenantID, workspaceID, recordID string) (Record, error) {
	var record Record
	err := repository.records.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "_id", Value: recordID}}).Decode(&record)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Record{}, ErrRecordNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("find record: %w", err)
	}
	return record, nil
}

func (repository *MongoRepository) UpdateRecord(ctx context.Context, record Record, expectedVersion int64) (Record, error) {
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	filter := bson.D{{Key: "tenantId", Value: record.TenantID}, {Key: "workspaceId", Value: record.WorkspaceID}, {Key: "_id", Value: record.ID}, {Key: "recordVersion", Value: expectedVersion}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: record.Data}, {Key: "tags", Value: record.Tags}, {Key: "relations", Value: record.Relations}, {Key: "updatedBy", Value: record.UpdatedBy}, {Key: "updatedAt", Value: record.UpdatedAt}}}, {Key: "$inc", Value: bson.D{{Key: "recordVersion", Value: 1}}}}
	var updated Record
	err := repository.records.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err == nil {
		return updated, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		if mongo.IsDuplicateKeyError(err) {
			return Record{}, ErrRecordExists
		}
		return Record{}, fmt.Errorf("update record: %w", err)
	}
	existing, lookupErr := repository.GetRecord(ctx, record.TenantID, record.WorkspaceID, record.ID)
	if lookupErr != nil {
		return Record{}, lookupErr
	}
	if existing.RecordVersion != expectedVersion {
		return Record{}, ErrRecordVersionConflict
	}
	return Record{}, ErrRecordVersionConflict
}

func (repository *MongoRepository) DeleteRecord(ctx context.Context, tenantID, workspaceID, recordID string, expectedVersion int64) error {
	result, err := repository.records.DeleteOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "_id", Value: recordID}, {Key: "recordVersion", Value: expectedVersion}})
	if err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	if result.DeletedCount == 1 {
		return nil
	}
	existing, lookupErr := repository.GetRecord(ctx, tenantID, workspaceID, recordID)
	if lookupErr != nil {
		return lookupErr
	}
	if existing.RecordVersion != expectedVersion {
		return ErrRecordVersionConflict
	}
	return ErrRecordVersionConflict
}
