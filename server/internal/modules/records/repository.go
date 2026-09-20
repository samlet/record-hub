package records

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
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
	ErrViewNotFound           = errors.New("view not found")
	ErrViewExists             = errors.New("view already exists")
	ErrViewVersionConflict    = errors.New("view version conflict")
	ErrInvalidCursor          = errors.New("invalid record cursor")
	ErrIndexNotFound          = errors.New("index not found")
	ErrIndexExists            = errors.New("index already exists")
	ErrIndexLimit             = errors.New("index limit reached")
	ErrIndexFieldNotAllowed   = errors.New("index field is not allowed")
)

const (
	workspaceCollectionName     = "workspaces"
	tableCollectionName         = "table_definitions"
	recordCollectionName        = "records"
	viewCollectionName          = "view_definitions"
	indexCollectionName         = "index_definitions"
	tagDictionaryCollectionName = "tag_dictionaries"
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

type ViewRepository interface {
	CreateView(context.Context, ViewDefinition) error
	GetView(context.Context, string, string, string, string) (ViewDefinition, error)
	UpdateView(context.Context, ViewDefinition, int64) (ViewDefinition, error)
	ListViews(context.Context, string, string, string) ([]ViewDefinition, error)
	ListRecords(context.Context, string, string, string, ViewDefinition, string, int) (RecordPage, error)
}

type IndexRepository interface {
	CreateIndex(context.Context, IndexDefinition) error
	ListIndexes(context.Context, string, string, string) ([]IndexDefinition, error)
}

type TagDictionaryRepository interface {
	GetTagDictionary(context.Context, string, string, string) (TagDictionary, error)
	ListTagDictionaries(context.Context, string) ([]TagDictionary, error)
	SaveTagDictionary(context.Context, TagDictionary, int64) error
}

type MongoRepository struct {
	workspaces      *mongo.Collection
	tables          *mongo.Collection
	records         *mongo.Collection
	readPointers    *mongo.Collection
	views           *mongo.Collection
	indexes         *mongo.Collection
	tagDictionaries *mongo.Collection
	clock           func() time.Time
}

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{workspaces: database.Collection(workspaceCollectionName), tables: database.Collection(tableCollectionName), records: database.Collection(recordCollectionName), readPointers: database.Collection(ProjectionReadPointerCollectionName), views: database.Collection(viewCollectionName), indexes: database.Collection(indexCollectionName), tagDictionaries: database.Collection(tagDictionaryCollectionName), clock: time.Now}
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
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "source.system", Value: 1}, {Key: "source.type", Value: 1}, {Key: "source.id", Value: 1}}, Options: options.Index().SetName("record_source_unique").SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "source.system", Value: bson.D{{Key: "$exists", Value: true}}}, {Key: "source.type", Value: bson.D{{Key: "$exists", Value: true}}}, {Key: "source.id", Value: bson.D{{Key: "$exists", Value: true}}}})},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "schemaId", Value: 1}, {Key: "schemaVersion", Value: 1}}, Options: options.Index().SetName("record_tenant_schema")},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "tags", Value: 1}}, Options: options.Index().SetName("record_table_tags")},
	}); err != nil {
		return fmt.Errorf("create record indexes: %w", err)
	}
	if repository.readPointers != nil {
		if _, err := repository.readPointers.Indexes().CreateMany(ctx, []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "status", Value: 1}}, Options: options.Index().SetName("projection_read_pointer_active_unique").SetUnique(true)},
			{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "status", Value: 1}, {Key: "activatedAt", Value: -1}}, Options: options.Index().SetName("projection_read_pointer_scope_status")},
		}); err != nil {
			return fmt.Errorf("create projection read pointer indexes: %w", err)
		}
	}
	if _, err := repository.views.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("view_tenant_workspace_table_id_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetName("view_table_name_unique").SetUnique(true)},
	}); err != nil {
		return fmt.Errorf("create view indexes: %w", err)
	}
	if _, err := repository.indexes.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "field", Value: 1}, {Key: "direction", Value: 1}}, Options: options.Index().SetName("index_table_field_direction_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "updatedAt", Value: -1}}, Options: options.Index().SetName("index_table_updated")},
	}); err != nil {
		return fmt.Errorf("create index metadata indexes: %w", err)
	}
	if _, err := repository.tagDictionaries.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}}, Options: options.Index().SetName("tag_dictionary_scope")},
	}); err != nil {
		return fmt.Errorf("create tag dictionary indexes: %w", err)
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
	if repository.readPointers != nil {
		if record, lookupErr := repository.findAcrossActiveProjectionCollections(ctx, tenantID, workspaceID, bson.D{{Key: "_id", Value: recordID}}); lookupErr == nil {
			return record, nil
		}
	}
	collection, err := repository.activeRecordCollection(ctx, tenantID, workspaceID, "")
	if err != nil {
		return Record{}, err
	}
	err = collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "_id", Value: recordID}}).Decode(&record)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if record, lookupErr := repository.findAcrossActiveProjectionCollections(ctx, tenantID, workspaceID, bson.D{{Key: "_id", Value: recordID}}); lookupErr == nil {
			return record, nil
		}
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Record{}, ErrRecordNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("find record: %w", err)
	}
	return record, nil
}

// GetRecordBySource resolves a projection through its stable external
// reference. Binding adapters use this method instead of exposing Mongo
// queries or internal record IDs to workflow code.
func (repository *MongoRepository) GetRecordBySource(ctx context.Context, tenantID, workspaceID, system, recordType, sourceID string) (Record, error) {
	var record Record
	if repository.readPointers != nil {
		if record, lookupErr := repository.findAcrossActiveProjectionCollections(ctx, tenantID, workspaceID, bson.D{{Key: "source.system", Value: system}, {Key: "source.type", Value: recordType}, {Key: "source.id", Value: sourceID}}); lookupErr == nil {
			return record, nil
		}
	}
	collection, err := repository.activeRecordCollection(ctx, tenantID, workspaceID, "")
	if err != nil {
		return Record{}, err
	}
	err = collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "source.system", Value: system}, {Key: "source.type", Value: recordType}, {Key: "source.id", Value: sourceID}}).Decode(&record)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if record, lookupErr := repository.findAcrossActiveProjectionCollections(ctx, tenantID, workspaceID, bson.D{{Key: "source.system", Value: system}, {Key: "source.type", Value: recordType}, {Key: "source.id", Value: sourceID}}); lookupErr == nil {
			return record, nil
		}
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Record{}, ErrRecordNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("find record by source: %w", err)
	}
	return record, nil
}

func (repository *MongoRepository) UpdateRecord(ctx context.Context, record Record, expectedVersion int64) (Record, error) {
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	filter := bson.D{{Key: "tenantId", Value: record.TenantID}, {Key: "workspaceId", Value: record.WorkspaceID}, {Key: "_id", Value: record.ID}, {Key: "recordVersion", Value: expectedVersion}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: record.Data}, {Key: "tags", Value: record.Tags}, {Key: "relations", Value: record.Relations}, {Key: "updatedBy", Value: record.UpdatedBy}, {Key: "updatedAt", Value: record.UpdatedAt}}}, {Key: "$inc", Value: bson.D{{Key: "recordVersion", Value: 1}}}}
	collection, err := repository.activeRecordCollection(ctx, record.TenantID, record.WorkspaceID, record.TableID)
	if err != nil {
		return Record{}, err
	}
	var updated Record
	err = collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
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
	collection, err := repository.activeRecordCollection(ctx, tenantID, workspaceID, "")
	if err != nil {
		return err
	}
	result, err := collection.DeleteOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "_id", Value: recordID}, {Key: "recordVersion", Value: expectedVersion}})
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

func (repository *MongoRepository) CreateView(ctx context.Context, view ViewDefinition) error {
	if err := view.Validate(); err != nil {
		return err
	}
	if _, err := repository.views.InsertOne(ctx, view); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrViewExists
		}
		return fmt.Errorf("insert view: %w", err)
	}
	return nil
}

func (repository *MongoRepository) GetView(ctx context.Context, tenantID, workspaceID, tableID, viewID string) (ViewDefinition, error) {
	var view ViewDefinition
	err := repository.views.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}, {Key: "_id", Value: viewID}}).Decode(&view)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ViewDefinition{}, ErrViewNotFound
	}
	if err != nil {
		return ViewDefinition{}, fmt.Errorf("find view: %w", err)
	}
	return view, nil
}

func (repository *MongoRepository) UpdateView(ctx context.Context, view ViewDefinition, expectedVersion int64) (ViewDefinition, error) {
	if err := view.Validate(); err != nil {
		return ViewDefinition{}, err
	}
	filter := bson.D{{Key: "tenantId", Value: view.TenantID}, {Key: "workspaceId", Value: view.WorkspaceID}, {Key: "tableId", Value: view.TableID}, {Key: "_id", Value: view.ID}, {Key: "version", Value: expectedVersion}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: view.Name}, {Key: "columns", Value: view.Columns}, {Key: "filters", Value: view.Filters}, {Key: "sorts", Value: view.Sorts}, {Key: "updatedBy", Value: view.UpdatedBy}, {Key: "updatedAt", Value: view.UpdatedAt}}}, {Key: "$inc", Value: bson.D{{Key: "version", Value: 1}}}}
	var updated ViewDefinition
	err := repository.views.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updated)
	if err == nil {
		return updated, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return ViewDefinition{}, fmt.Errorf("update view: %w", err)
	}
	existing, lookupErr := repository.GetView(ctx, view.TenantID, view.WorkspaceID, view.TableID, view.ID)
	if lookupErr != nil {
		return ViewDefinition{}, lookupErr
	}
	if existing.Version != expectedVersion {
		return ViewDefinition{}, ErrViewVersionConflict
	}
	return ViewDefinition{}, ErrViewVersionConflict
}

func (repository *MongoRepository) ListViews(ctx context.Context, tenantID, workspaceID, tableID string) ([]ViewDefinition, error) {
	cursor, err := repository.views.Find(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(100))
	if err != nil {
		return nil, fmt.Errorf("list views: %w", err)
	}
	defer cursor.Close(ctx)
	var views []ViewDefinition
	if err := cursor.All(ctx, &views); err != nil {
		return nil, fmt.Errorf("decode views: %w", err)
	}
	return views, nil
}

func (repository *MongoRepository) CreateIndex(ctx context.Context, definition IndexDefinition) error {
	definition.Field = normalizeIndexField(definition.Field)
	if definition.Name == "" {
		definition.Name = physicalIndexName(definition.Field, definition.Direction)
	}
	if err := definition.Validate(); err != nil {
		return err
	}
	if _, err := repository.indexes.InsertOne(ctx, definition); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrIndexExists
		}
		return fmt.Errorf("insert index definition: %w", err)
	}
	direction := 1
	if definition.Direction == IndexDescending {
		direction = -1
	}
	if _, err := repository.records.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "data." + definition.Field, Value: direction}},
		Options: options.Index().SetName(definition.Name),
	}); err != nil {
		// Metadata and the physical index are not part of a Mongo transaction;
		// remove the metadata if DDL fails so a retry can safely repair it.
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = repository.indexes.DeleteOne(cleanupContext, bson.D{{Key: "_id", Value: definition.ID}, {Key: "tenantId", Value: definition.TenantID}, {Key: "workspaceId", Value: definition.WorkspaceID}, {Key: "tableId", Value: definition.TableID}})
		cancel()
		return fmt.Errorf("create record index: %w", err)
	}
	return nil
}

func (repository *MongoRepository) ListIndexes(ctx context.Context, tenantID, workspaceID, tableID string) ([]IndexDefinition, error) {
	cursor, err := repository.indexes.Find(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}}, options.Find().SetSort(bson.D{{Key: "field", Value: 1}, {Key: "direction", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(maxIndexesPerTable))
	if err != nil {
		return nil, fmt.Errorf("list indexes: %w", err)
	}
	defer cursor.Close(ctx)
	var definitions []IndexDefinition
	if err := cursor.All(ctx, &definitions); err != nil {
		return nil, fmt.Errorf("decode indexes: %w", err)
	}
	return definitions, nil
}

func (repository *MongoRepository) ListRecords(ctx context.Context, tenantID, workspaceID, tableID string, view ViewDefinition, cursorValue string, limit int) (RecordPage, error) {
	if limit < 1 || limit > 100 {
		return RecordPage{}, errors.New("record page limit must be between 1 and 100")
	}
	filter := bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}}
	viewFilters, err := mongoViewFilters(view.Filters)
	if err != nil {
		return RecordPage{}, err
	}
	filter = append(filter, viewFilters...)
	sorts := viewSorts(view.Sorts)
	if cursorValue != "" {
		decoded, err := decodeRecordCursor(cursorValue)
		if err != nil {
			return RecordPage{}, err
		}
		if len(decoded.Values) != len(sorts) {
			return RecordPage{}, ErrInvalidCursor
		}
		orConditions := bson.A{}
		for index := range sorts {
			andConditions := bson.D{}
			for previous := 0; previous < index; previous++ {
				andConditions = append(andConditions, bson.E{Key: viewMongoField(sorts[previous].Field), Value: decoded.Values[previous]})
			}
			op := "$gt"
			if sorts[index].Direction == SortDescending {
				op = "$lt"
			}
			andConditions = append(andConditions, bson.E{Key: viewMongoField(sorts[index].Field), Value: bson.D{{Key: op, Value: decoded.Values[index]}}})
			orConditions = append(orConditions, andConditions)
		}
		filter = append(filter, bson.E{Key: "$or", Value: orConditions})
	}
	collection, err := repository.activeRecordCollection(ctx, tenantID, workspaceID, tableID)
	if err != nil {
		return RecordPage{}, err
	}
	cursor, err := collection.Find(ctx, filter, options.Find().SetSort(mongoSort(sorts)).SetLimit(int64(limit+1)))
	if err != nil {
		return RecordPage{}, fmt.Errorf("list records: %w", err)
	}
	defer cursor.Close(ctx)
	var records []Record
	if err := cursor.All(ctx, &records); err != nil {
		return RecordPage{}, fmt.Errorf("decode records: %w", err)
	}
	page := RecordPage{Items: records}
	if len(records) > limit {
		page.Items = records[:limit]
		page.NextCursor, err = encodeRecordCursor(page.Items[len(page.Items)-1], sorts)
		if err != nil {
			return RecordPage{}, err
		}
	}
	return page, nil
}

func (repository *MongoRepository) activeRecordCollection(ctx context.Context, tenantID, workspaceID, tableID string) (*mongo.Collection, error) {
	if repository == nil || repository.records == nil {
		return nil, errors.New("record repository is not configured")
	}
	if repository.readPointers == nil || strings.TrimSpace(tableID) == "" {
		return repository.records, nil
	}
	var pointer ProjectionReadPointer
	err := repository.readPointers.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}, {Key: "status", Value: ProjectionReadPointerActive}}).Decode(&pointer)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return repository.records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find projection read pointer: %w", err)
	}
	if strings.TrimSpace(pointer.CollectionName) == "" {
		return nil, errors.New("projection read pointer has no collection")
	}
	return repository.records.Database().Collection(pointer.CollectionName), nil
}

func (repository *MongoRepository) findAcrossActiveProjectionCollections(ctx context.Context, tenantID, workspaceID string, extra bson.D) (Record, error) {
	if repository.readPointers == nil {
		return Record{}, ErrRecordNotFound
	}
	cursor, err := repository.readPointers.Find(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "status", Value: ProjectionReadPointerActive}})
	if err != nil {
		return Record{}, fmt.Errorf("list projection read pointers: %w", err)
	}
	defer cursor.Close(ctx)
	var pointers []ProjectionReadPointer
	if err := cursor.All(ctx, &pointers); err != nil {
		return Record{}, fmt.Errorf("decode projection read pointers: %w", err)
	}
	for _, pointer := range pointers {
		filter := bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}
		filter = append(filter, extra...)
		var record Record
		if err := repository.records.Database().Collection(pointer.CollectionName).FindOne(ctx, filter).Decode(&record); err == nil {
			return record, nil
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return Record{}, fmt.Errorf("find projection record in active collection: %w", err)
		}
	}
	return Record{}, ErrRecordNotFound
}

type recordCursor struct {
	Values bson.A `bson:"values"`
}

func encodeRecordCursor(record Record, sorts []ViewSort) (string, error) {
	values := make(bson.A, len(sorts))
	for index, sort := range sorts {
		values[index] = recordSortValue(record, sort.Field)
	}
	payload, err := bson.Marshal(recordCursor{Values: values})
	if err != nil {
		return "", fmt.Errorf("encode record cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeRecordCursor(value string) (recordCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return recordCursor{}, ErrInvalidCursor
	}
	var cursor recordCursor
	if err := bson.Unmarshal(payload, &cursor); err != nil || len(cursor.Values) == 0 {
		return recordCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func viewSorts(sorts []ViewSort) []ViewSort {
	result := append([]ViewSort(nil), sorts...)
	for _, sort := range result {
		if sort.Field == "id" || sort.Field == "_id" {
			return result
		}
	}
	result = append(result, ViewSort{Field: "id", Direction: SortAscending})
	return result
}

func mongoSort(sorts []ViewSort) bson.D {
	result := make(bson.D, 0, len(sorts))
	for _, sort := range sorts {
		direction := 1
		if sort.Direction == SortDescending {
			direction = -1
		}
		result = append(result, bson.E{Key: viewMongoField(sort.Field), Value: direction})
	}
	return result
}

func mongoViewFilters(filters []ViewFilter) (bson.D, error) {
	conditions := make(map[string]bson.D)
	for _, filter := range filters {
		if err := validateFilter(filter); err != nil {
			return nil, err
		}
		path := viewMongoField(filter.Field)
		operator := "$eq"
		value := filter.Value
		switch filter.Operator {
		case FilterEqual:
		case FilterNotEqual:
			operator = "$ne"
		case FilterContains:
			operator = "$regex"
			value = regexp.QuoteMeta(value.(string))
		case FilterIn:
			operator = "$in"
		case FilterGreater:
			operator = "$gt"
		case FilterAtLeast:
			operator = "$gte"
		case FilterLess:
			operator = "$lt"
		case FilterAtMost:
			operator = "$lte"
		default:
			return nil, fmt.Errorf("unsupported view filter operator %q", filter.Operator)
		}
		if _, ok := conditions[path]; !ok {
			conditions[path] = bson.D{}
		}
		conditions[path] = append(conditions[path], bson.E{Key: operator, Value: value})
	}
	result := make(bson.D, 0, len(conditions))
	for path, operators := range conditions {
		if len(operators) == 1 && operators[0].Key == "$eq" {
			result = append(result, bson.E{Key: path, Value: operators[0].Value})
		} else {
			result = append(result, bson.E{Key: path, Value: operators})
		}
	}
	return result, nil
}

func viewMongoField(field string) string {
	switch field {
	case "id", "_id":
		return "_id"
	case "recordVersion", "schemaVersion", "createdAt", "updatedAt", "tags":
		return field
	default:
		return "data." + strings.TrimPrefix(field, "data.")
	}
}

func recordSortValue(record Record, field string) interface{} {
	switch field {
	case "id", "_id":
		return record.ID
	case "recordVersion":
		return record.RecordVersion
	case "schemaVersion":
		return record.SchemaVersion
	case "createdAt":
		return record.CreatedAt
	case "updatedAt":
		return record.UpdatedAt
	case "tags":
		return record.Tags
	default:
		parts := strings.Split(strings.TrimPrefix(field, "data."), ".")
		value := record.Data.Lookup(parts...)
		if value.IsZero() {
			return nil
		}
		var result interface{}
		if err := value.Unmarshal(&result); err != nil {
			return nil
		}
		return result
	}
}
