package projection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/records"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrProjectionReadPointerUnavailable = errors.New("projection read pointer is unavailable")

const defaultProjectionRecordsCollection = "records"

// MongoProjectionReadPointerRepository owns the durable switch that makes a
// staged collection visible to both API reads and live projector writes.
type MongoProjectionReadPointerRepository struct {
	database    *mongo.Database
	collection  *mongo.Collection
	checkpoints *mongo.Collection
}

func NewMongoProjectionReadPointerRepository(database *mongo.Database) *MongoProjectionReadPointerRepository {
	if database == nil {
		return &MongoProjectionReadPointerRepository{}
	}
	return &MongoProjectionReadPointerRepository{database: database, collection: database.Collection(records.ProjectionReadPointerCollectionName), checkpoints: database.Collection(checkpointCollectionName)}
}

func (repository *MongoProjectionReadPointerRepository) EnsureIndexes(ctx context.Context) error {
	if repository == nil || repository.collection == nil {
		return ErrProjectionReadPointerUnavailable
	}
	if _, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "status", Value: 1}}, Options: options.Index().SetName("projection_read_pointer_active_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "status", Value: 1}, {Key: "activatedAt", Value: -1}}, Options: options.Index().SetName("projection_read_pointer_scope_status")},
	}); err != nil {
		return fmt.Errorf("create projection read pointer indexes: %w", err)
	}
	return nil
}

func (repository *MongoProjectionReadPointerRepository) ActiveCollection(ctx context.Context, tenantID, workspaceID, tableID string) (string, error) {
	if repository == nil || repository.collection == nil {
		return "", ErrProjectionReadPointerUnavailable
	}
	var pointer records.ProjectionReadPointer
	err := repository.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}, {Key: "status", Value: records.ProjectionReadPointerActive}}).Decode(&pointer)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return defaultProjectionRecordsCollection, nil
	}
	if err != nil {
		return "", fmt.Errorf("find projection read pointer: %w", err)
	}
	if strings.TrimSpace(pointer.CollectionName) == "" {
		return "", ErrProjectionReadPointerUnavailable
	}
	return pointer.CollectionName, nil
}

func (repository *MongoProjectionReadPointerRepository) ActivateMany(ctx context.Context, tenantID, workspaceID, operationID, generationID, collectionName string, tableIDs []string) error {
	return repository.ActivateManyWithCheckpoints(ctx, tenantID, workspaceID, operationID, generationID, collectionName, tableIDs, nil)
}

func (repository *MongoProjectionReadPointerRepository) ActivateManyWithCheckpoints(ctx context.Context, tenantID, workspaceID, operationID, generationID, collectionName string, tableIDs []string, checkpoints []ProjectionCheckpoint) error {
	if repository == nil || repository.database == nil || repository.collection == nil {
		return ErrProjectionReadPointerUnavailable
	}
	tenantID, workspaceID, operationID, generationID, collectionName = strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), strings.TrimSpace(operationID), strings.TrimSpace(generationID), strings.TrimSpace(collectionName)
	if tenantID == "" || workspaceID == "" || operationID == "" || generationID == "" || collectionName == "" {
		return ErrProjectionReadPointerUnavailable
	}
	seen := make(map[string]struct{}, len(tableIDs))
	uniqueTables := make([]string, 0, len(tableIDs))
	for _, tableID := range tableIDs {
		tableID = strings.TrimSpace(tableID)
		if tableID == "" {
			continue
		}
		if _, ok := seen[tableID]; ok {
			continue
		}
		seen[tableID] = struct{}{}
		uniqueTables = append(uniqueTables, tableID)
	}
	if len(uniqueTables) == 0 {
		return nil
	}
	session, err := repository.database.Client().StartSession()
	if err != nil {
		return fmt.Errorf("start projection read pointer transaction: %w", err)
	}
	defer session.EndSession(context.Background())
	_, err = session.WithTransaction(ctx, func(tx context.Context) (interface{}, error) {
		now := time.Now().UTC()
		for _, tableID := range uniqueTables {
			filter := bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}}
			if _, updateErr := repository.collection.UpdateOne(tx, append(filter, bson.E{Key: "status", Value: records.ProjectionReadPointerActive}), bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: records.ProjectionReadPointerRetired}}}}); updateErr != nil {
				return nil, fmt.Errorf("retire projection read pointer: %w", updateErr)
			}
			pointer := records.ProjectionReadPointer{TenantID: tenantID, WorkspaceID: workspaceID, TableID: tableID, GenerationID: generationID, CollectionName: collectionName, OperationID: operationID, Revision: 1, Status: records.ProjectionReadPointerActive, CreatedAt: now, ActivatedAt: now}
			if _, insertErr := repository.collection.InsertOne(tx, pointer); insertErr != nil {
				return nil, fmt.Errorf("activate projection read pointer: %w", insertErr)
			}
		}
		if repository.checkpoints != nil {
			for _, checkpoint := range checkpoints {
				if err := checkpoint.Validate(); err != nil {
					return nil, err
				}
				filter := projectionCheckpointFilter(checkpoint)
				var current ProjectionCheckpoint
				findErr := repository.checkpoints.FindOne(tx, filter).Decode(&current)
				if errors.Is(findErr, mongo.ErrNoDocuments) {
					if _, insertErr := repository.checkpoints.InsertOne(tx, checkpoint); insertErr != nil {
						return nil, fmt.Errorf("insert replay projection checkpoint: %w", insertErr)
					}
					continue
				}
				if findErr != nil {
					return nil, fmt.Errorf("load replay projection checkpoint: %w", findErr)
				}
				if current.SourceVersion < checkpoint.SourceVersion {
					if _, updateErr := repository.checkpoints.UpdateOne(tx, filter, bson.D{{Key: "$set", Value: checkpoint}}); updateErr != nil {
						return nil, fmt.Errorf("update replay projection checkpoint: %w", updateErr)
					}
				}
			}
		}
		return nil, nil
	})
	return err
}

func stagingRecordsCollectionName(operationID string) string {
	return "projection_staging_records_" + strings.TrimPrefix(strings.TrimSpace(operationID), "projection_staging_records_")
}
