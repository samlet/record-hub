package projection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/records"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrProjectionRecordRequired = errors.New("projection record is required")
	ErrProjectionAlreadyApplied = errors.New("projection event is already applied")
	ErrProjectionVersionGap     = errors.New("projection aggregate version gap")
	ErrProjectionVersionStale   = errors.New("projection aggregate version is stale")
)

type CheckpointStatus string

const (
	CheckpointCurrent CheckpointStatus = "CURRENT"
	CheckpointGap     CheckpointStatus = "GAP"
	CheckpointFailed  CheckpointStatus = "FAILED"
)

type ProjectionCheckpoint struct {
	TenantID      string           `bson:"tenantId" json:"tenantId"`
	WorkspaceID   string           `bson:"workspaceId" json:"workspaceId"`
	Consumer      string           `bson:"consumer" json:"consumer"`
	SourceSystem  string           `bson:"sourceSystem" json:"sourceSystem"`
	AggregateType string           `bson:"aggregateType" json:"aggregateType"`
	AggregateID   string           `bson:"aggregateId" json:"aggregateId"`
	SourceVersion int64            `bson:"sourceVersion" json:"sourceVersion"`
	LastEventID   string           `bson:"lastEventId" json:"lastEventId"`
	SyncedAt      time.Time        `bson:"syncedAt" json:"syncedAt"`
	Status        CheckpointStatus `bson:"status" json:"status"`
}

func (checkpoint ProjectionCheckpoint) Validate() error {
	if strings.TrimSpace(checkpoint.TenantID) == "" || strings.TrimSpace(checkpoint.WorkspaceID) == "" || strings.TrimSpace(checkpoint.Consumer) == "" || strings.TrimSpace(checkpoint.SourceSystem) == "" || strings.TrimSpace(checkpoint.AggregateType) == "" || strings.TrimSpace(checkpoint.AggregateID) == "" || strings.TrimSpace(checkpoint.LastEventID) == "" {
		return errors.New("checkpoint scope, source identity, and lastEventId are required")
	}
	if checkpoint.SourceVersion < 0 {
		return errors.New("checkpoint sourceVersion cannot be negative")
	}
	if checkpoint.SyncedAt.IsZero() {
		return errors.New("checkpoint syncedAt is required")
	}
	switch checkpoint.Status {
	case CheckpointCurrent, CheckpointGap, CheckpointFailed:
	default:
		return errors.New("checkpoint status is invalid")
	}
	return nil
}

type ProjectionApply struct {
	InboxEvent InboxEvent
	Record     records.Record
	Checkpoint ProjectionCheckpoint
	Audit      audit.Entry
}

type ProjectionRepository interface {
	Apply(context.Context, ProjectionApply) error
}

type MongoProjectionRepository struct {
	database     *mongo.Database
	inbox        *mongo.Collection
	records      *mongo.Collection
	readPointers *mongo.Collection
	checkpoints  *mongo.Collection
	audit        *mongo.Collection
	staging      bool
	// afterCommit is a narrow fault-injection hook used by the live recovery
	// test to model a lost client response after Mongo has committed.
	afterCommit func() error
}

const checkpointCollectionName = "projection_checkpoints"

func NewMongoProjectionRepository(database *mongo.Database) *MongoProjectionRepository {
	if database == nil {
		return &MongoProjectionRepository{}
	}
	return &MongoProjectionRepository{database: database, inbox: database.Collection(inboxCollectionName), records: database.Collection("records"), readPointers: database.Collection(records.ProjectionReadPointerCollectionName), checkpoints: database.Collection(checkpointCollectionName), audit: database.Collection("audit_entries")}
}

// WithStaging returns an isolated repository used only during replay. It does
// not touch Inbox, checkpoints, or live audit state; staged records become
// visible only after the durable read-pointer transaction commits.
func (repository *MongoProjectionRepository) WithStaging(operationID string) *MongoProjectionRepository {
	if repository == nil || repository.database == nil {
		return repository
	}
	copy := *repository
	copy.records = repository.database.Collection(stagingRecordsCollectionName(operationID))
	copy.staging = true
	return &copy
}

func (repository *MongoProjectionRepository) EnsureStagingIndexes(ctx context.Context, operationID string) error {
	if repository == nil || repository.database == nil {
		return ErrProjectionReadPointerUnavailable
	}
	collection := repository.database.Collection(stagingRecordsCollectionName(operationID))
	if _, err := collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("projection_staging_record_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}, {Key: "recordVersion", Value: -1}}, Options: options.Index().SetName("projection_staging_record_version")},
	}); err != nil {
		return fmt.Errorf("create projection staging indexes: %w", err)
	}
	return nil
}

func (repository *MongoProjectionRepository) EnsureIndexes(ctx context.Context) error {
	if _, err := repository.inbox.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "consumer", Value: 1}, {Key: "eventId", Value: 1}}, Options: options.Index().SetName("inbox_consumer_event_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "consumer", Value: 1}, {Key: "status", Value: 1}, {Key: "receivedAt", Value: -1}}, Options: options.Index().SetName("inbox_consumer_status_received")},
	}); err != nil {
		return fmt.Errorf("create inbox indexes: %w", err)
	}
	if _, err := repository.checkpoints.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "consumer", Value: 1}, {Key: "sourceSystem", Value: 1}, {Key: "aggregateType", Value: 1}, {Key: "aggregateId", Value: 1}}, Options: options.Index().SetName("checkpoint_aggregate_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "status", Value: 1}, {Key: "syncedAt", Value: -1}}, Options: options.Index().SetName("checkpoint_status_synced")},
	}); err != nil {
		return fmt.Errorf("create checkpoint indexes: %w", err)
	}
	return nil
}

func (repository *MongoProjectionRepository) Apply(ctx context.Context, input ProjectionApply) error {
	if err := validateProjectionApply(input); err != nil {
		return err
	}
	if repository == nil || repository.database == nil || repository.records == nil {
		return errors.New("projection repository is not configured")
	}
	if repository.staging {
		return repository.applyStaging(ctx, input)
	}
	if repository.inbox == nil || repository.checkpoints == nil || repository.audit == nil {
		return errors.New("projection repository is not configured")
	}
	session, err := repository.database.Client().StartSession()
	if err != nil {
		return fmt.Errorf("start projection transaction: %w", err)
	}
	defer session.EndSession(context.Background())
	gapDetected := false
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (interface{}, error) {
		var existing InboxEvent
		if findErr := repository.inbox.FindOne(transactionContext, bson.D{{Key: "eventId", Value: input.InboxEvent.EventID}, {Key: "consumer", Value: input.InboxEvent.Consumer}}).Decode(&existing); findErr != nil {
			if errors.Is(findErr, mongo.ErrNoDocuments) {
				return nil, ErrInboxNotFound
			}
			return nil, fmt.Errorf("load inbox event: %w", findErr)
		}
		if existing.PayloadHash != input.InboxEvent.PayloadHash {
			return nil, ErrInboxPayloadConflict
		}
		if existing.Status == InboxApplied {
			return nil, ErrProjectionAlreadyApplied
		}
		if existing.Status != InboxProcessing {
			return nil, ErrInboxStateConflict
		}
		checkpointFilter := projectionCheckpointFilter(input.Checkpoint)
		var current ProjectionCheckpoint
		checkpointErr := repository.checkpoints.FindOne(transactionContext, checkpointFilter).Decode(&current)
		if checkpointErr != nil && !errors.Is(checkpointErr, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("load projection checkpoint: %w", checkpointErr)
		}
		decision := classifyCheckpoint(current, checkpointErr == nil, input.Checkpoint.SourceVersion)
		appliedAt := input.Checkpoint.SyncedAt.UTC()
		if decision == projectionVersionStale {
			if _, updateErr := repository.inbox.UpdateOne(transactionContext, bson.D{{Key: "eventId", Value: input.InboxEvent.EventID}, {Key: "consumer", Value: input.InboxEvent.Consumer}, {Key: "status", Value: InboxProcessing}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: InboxApplied}, {Key: "appliedAt", Value: appliedAt}}}}); updateErr != nil {
				return nil, fmt.Errorf("mark stale inbox event applied: %w", updateErr)
			}
			return nil, nil
		}
		if decision == projectionVersionGap {
			if checkpointErr == nil {
				if _, updateErr := repository.checkpoints.UpdateOne(transactionContext, checkpointFilter, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: CheckpointGap}}}}); updateErr != nil {
					return nil, fmt.Errorf("mark projection checkpoint gap: %w", updateErr)
				}
			}
			gapDetected = true
			return nil, nil
		}
		if _, updateErr := repository.inbox.UpdateOne(transactionContext, bson.D{{Key: "eventId", Value: input.InboxEvent.EventID}, {Key: "consumer", Value: input.InboxEvent.Consumer}, {Key: "status", Value: InboxProcessing}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: InboxApplied}, {Key: "appliedAt", Value: appliedAt}}}}); updateErr != nil {
			return nil, fmt.Errorf("mark inbox event applied: %w", updateErr)
		}
		recordsCollection, resolveErr := repository.activeRecordsCollection(transactionContext, input.Record.TenantID, input.Record.WorkspaceID, input.Record.TableID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if _, replaceErr := recordsCollection.ReplaceOne(transactionContext, bson.D{{Key: "tenantId", Value: input.Record.TenantID}, {Key: "workspaceId", Value: input.Record.WorkspaceID}, {Key: "tableId", Value: input.Record.TableID}, {Key: "_id", Value: input.Record.ID}}, input.Record, options.Replace().SetUpsert(true)); replaceErr != nil {
			return nil, fmt.Errorf("upsert projection record: %w", replaceErr)
		}
		if _, checkpointErr := repository.checkpoints.UpdateOne(transactionContext, checkpointFilter, bson.D{{Key: "$set", Value: input.Checkpoint}}, options.UpdateOne().SetUpsert(true)); checkpointErr != nil {
			return nil, fmt.Errorf("upsert projection checkpoint: %w", checkpointErr)
		}
		if _, auditErr := repository.audit.InsertOne(transactionContext, input.Audit); auditErr != nil {
			return nil, fmt.Errorf("append projection audit: %w", auditErr)
		}
		return nil, nil
	})
	if errors.Is(err, ErrProjectionAlreadyApplied) {
		return nil
	}
	if err != nil {
		return err
	}
	if repository.afterCommit != nil {
		hook := repository.afterCommit
		repository.afterCommit = nil
		if hookErr := hook(); hookErr != nil {
			return hookErr
		}
	}
	if err == nil && gapDetected {
		return ErrProjectionVersionGap
	}
	return err
}

func (repository *MongoProjectionRepository) applyStaging(ctx context.Context, input ProjectionApply) error {
	if err := validateProjectionApply(input); err != nil {
		return err
	}
	filter := bson.D{{Key: "tenantId", Value: input.Record.TenantID}, {Key: "workspaceId", Value: input.Record.WorkspaceID}, {Key: "tableId", Value: input.Record.TableID}, {Key: "_id", Value: input.Record.ID}}
	var current records.Record
	err := repository.records.FindOne(ctx, filter).Decode(&current)
	if err == nil {
		if current.RecordVersion >= input.Record.RecordVersion {
			return nil
		}
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("load staged projection record: %w", err)
	}
	if _, err := repository.records.ReplaceOne(ctx, filter, input.Record, options.Replace().SetUpsert(true)); err != nil {
		return fmt.Errorf("upsert staged projection record: %w", err)
	}
	return nil
}

func (repository *MongoProjectionRepository) activeRecordsCollection(ctx context.Context, tenantID, workspaceID, tableID string) (*mongo.Collection, error) {
	if repository.readPointers == nil || strings.TrimSpace(tableID) == "" {
		return repository.records, nil
	}
	var pointer records.ProjectionReadPointer
	err := repository.readPointers.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "tableId", Value: tableID}, {Key: "status", Value: records.ProjectionReadPointerActive}}).Decode(&pointer)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return repository.records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find projection read pointer: %w", err)
	}
	if strings.TrimSpace(pointer.CollectionName) == "" {
		return nil, ErrProjectionReadPointerUnavailable
	}
	return repository.database.Collection(pointer.CollectionName), nil
}

type projectionVersionDecision uint8

const (
	projectionVersionApply projectionVersionDecision = iota
	projectionVersionStale
	projectionVersionGap
)

func classifyCheckpoint(current ProjectionCheckpoint, exists bool, incomingVersion int64) projectionVersionDecision {
	currentVersion := int64(0)
	if exists {
		currentVersion = current.SourceVersion
	}
	if incomingVersion <= currentVersion {
		return projectionVersionStale
	}
	if incomingVersion != currentVersion+1 {
		return projectionVersionGap
	}
	return projectionVersionApply
}

func projectionCheckpointFilter(checkpoint ProjectionCheckpoint) bson.D {
	return bson.D{{Key: "tenantId", Value: checkpoint.TenantID}, {Key: "workspaceId", Value: checkpoint.WorkspaceID}, {Key: "consumer", Value: checkpoint.Consumer}, {Key: "sourceSystem", Value: checkpoint.SourceSystem}, {Key: "aggregateType", Value: checkpoint.AggregateType}, {Key: "aggregateId", Value: checkpoint.AggregateID}}
}

func validateProjectionApply(input ProjectionApply) error {
	if err := input.InboxEvent.Validate(); err != nil {
		return fmt.Errorf("inbox event: %w", err)
	}
	if err := input.Checkpoint.Validate(); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	if err := input.Record.Validate(); err != nil {
		return fmt.Errorf("projection record: %w", err)
	}
	if input.Record.Projection == nil {
		return ErrProjectionRecordRequired
	}
	if input.Checkpoint.LastEventID != input.InboxEvent.EventID || input.Checkpoint.TenantID != input.Record.TenantID || input.Checkpoint.WorkspaceID != input.Record.WorkspaceID || input.Checkpoint.Consumer != input.InboxEvent.Consumer || input.Audit.TenantID != input.Record.TenantID || input.Audit.WorkspaceID != input.Record.WorkspaceID || input.Audit.ResourceID != input.Record.ID || (input.InboxEvent.TenantID != "" && (input.InboxEvent.TenantID != input.Record.TenantID || input.InboxEvent.WorkspaceID != input.Record.WorkspaceID)) {
		return errors.New("projection apply scope and event identity do not match")
	}
	if input.Audit.Action == "" || input.Audit.Actor.Issuer == "" || input.Audit.Actor.Subject == "" || input.Audit.ResourceType == "" || input.Audit.CreatedAt.IsZero() {
		return errors.New("projection audit entry is incomplete")
	}
	return nil
}
