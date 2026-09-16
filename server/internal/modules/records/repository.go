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
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrWorkspaceExists   = errors.New("workspace already exists")
	ErrTableNotFound     = errors.New("table not found")
	ErrTableExists       = errors.New("table already exists")
	ErrSchemaUnavailable = errors.New("published schema is unavailable")
)

const (
	workspaceCollectionName = "workspaces"
	tableCollectionName     = "table_definitions"
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

type MongoRepository struct {
	workspaces *mongo.Collection
	tables     *mongo.Collection
	clock      func() time.Time
}

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{workspaces: database.Collection(workspaceCollectionName), tables: database.Collection(tableCollectionName), clock: time.Now}
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
