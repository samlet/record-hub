package commands

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	commandInboxCollection = "command_inbox"
	ResultStreamName       = "COMMAND_RESULTS"
	ResultConsumerName     = "record-hub-command-results-v1"
)

var (
	ErrCommandInboxClaimed  = errors.New("command inbox entry is already claimed")
	ErrCommandInboxConflict = errors.New("command inbox entry conflicts")
)

type InboxStatus string

const (
	InboxClaimed   InboxStatus = "CLAIMED"
	InboxCommitted InboxStatus = "COMMITTED"
)

type InboxEntry struct {
	OperationID string          `bson:"operationId" json:"operationId"`
	TenantID    string          `bson:"tenantId" json:"tenantId"`
	WorkspaceID string          `bson:"workspaceId" json:"workspaceId"`
	OwnerSystem string          `bson:"ownerSystem" json:"ownerSystem"`
	Action      string          `bson:"action" json:"action"`
	PayloadHash string          `bson:"payloadHash" json:"payloadHash"`
	Status      InboxStatus     `bson:"status" json:"status"`
	Result      *ResultEnvelope `bson:"result,omitempty" json:"result,omitempty"`
	Revision    int64           `bson:"revision" json:"revision"`
	CreatedAt   time.Time       `bson:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time       `bson:"updatedAt" json:"updatedAt"`
}

type InboxStore interface {
	Claim(context.Context, Envelope) (InboxEntry, bool, error)
	Commit(context.Context, InboxEntry, ResultEnvelope) (InboxEntry, error)
}

type MemoryInboxStore struct {
	mu     sync.Mutex
	values map[string]InboxEntry
}

func NewMemoryInboxStore() *MemoryInboxStore {
	return &MemoryInboxStore{values: make(map[string]InboxEntry)}
}

func (store *MemoryInboxStore) Claim(_ context.Context, envelope Envelope) (InboxEntry, bool, error) {
	if store == nil {
		return InboxEntry{}, false, ErrCommandUnavailable
	}
	key := inboxKey(envelope)
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.values[key]; ok {
		if existing.PayloadHash != envelope.PayloadHash || existing.OwnerSystem != envelope.OwnerSystem || existing.Action != envelope.Action {
			return InboxEntry{}, false, ErrCommandInboxConflict
		}
		if existing.Status == InboxCommitted {
			return existing, true, nil
		}
		return existing, false, ErrCommandInboxClaimed
	}
	now := envelope.CreatedAt.UTC()
	entry := InboxEntry{OperationID: envelope.OperationID, TenantID: envelope.TenantID, WorkspaceID: envelope.WorkspaceID, OwnerSystem: envelope.OwnerSystem, Action: envelope.Action, PayloadHash: envelope.PayloadHash, Status: InboxClaimed, Revision: 1, CreatedAt: now, UpdatedAt: now}
	store.values[key] = entry
	return entry, false, nil
}

func (store *MemoryInboxStore) Commit(_ context.Context, claimed InboxEntry, result ResultEnvelope) (InboxEntry, error) {
	if store == nil {
		return InboxEntry{}, ErrCommandUnavailable
	}
	if err := result.Validate(); err != nil {
		return InboxEntry{}, err
	}
	key := inboxKeyValues(claimed.TenantID, claimed.WorkspaceID, claimed.OwnerSystem, claimed.OperationID)
	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.values[key]
	if !ok {
		return InboxEntry{}, ErrCommandNotFound
	}
	if current.Status == InboxCommitted {
		if current.Result != nil && current.Result.EventID == result.EventID {
			return current, nil
		}
		return InboxEntry{}, ErrCommandInboxConflict
	}
	if current.Revision != claimed.Revision || current.Status != InboxClaimed {
		return InboxEntry{}, ErrCommandInboxConflict
	}
	current.Status = InboxCommitted
	current.Result = &result
	current.Revision++
	current.UpdatedAt = result.OccurredAt.UTC()
	store.values[key] = current
	return current, nil
}

type MongoInboxStore struct{ collection *mongo.Collection }

func NewMongoInboxStore(database *mongo.Database) *MongoInboxStore {
	if database == nil {
		return &MongoInboxStore{}
	}
	return &MongoInboxStore{collection: database.Collection(commandInboxCollection)}
}

func (store *MongoInboxStore) EnsureIndexes(ctx context.Context) error {
	if store == nil || store.collection == nil {
		return ErrCommandUnavailable
	}
	_, err := store.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "operationId", Value: 1}, {Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}}, Options: options.Index().SetName("command_inbox_operation_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "updatedAt", Value: 1}}, Options: options.Index().SetName("command_inbox_status_updated")},
	})
	if err != nil {
		return fmt.Errorf("create command inbox indexes: %w", err)
	}
	return nil
}

func (store *MongoInboxStore) Claim(ctx context.Context, envelope Envelope) (InboxEntry, bool, error) {
	if store == nil || store.collection == nil {
		return InboxEntry{}, false, ErrCommandUnavailable
	}
	key := bson.D{{Key: "operationId", Value: envelope.OperationID}, {Key: "tenantId", Value: envelope.TenantID}, {Key: "workspaceId", Value: envelope.WorkspaceID}}
	var existing InboxEntry
	err := store.collection.FindOne(ctx, key).Decode(&existing)
	if err == nil {
		if existing.PayloadHash != envelope.PayloadHash || existing.OwnerSystem != envelope.OwnerSystem || existing.Action != envelope.Action {
			return InboxEntry{}, false, ErrCommandInboxConflict
		}
		if existing.Status == InboxCommitted {
			return existing, true, nil
		}
		return existing, false, ErrCommandInboxClaimed
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return InboxEntry{}, false, fmt.Errorf("find command inbox entry: %w", err)
	}
	now := time.Now().UTC()
	entry := InboxEntry{OperationID: envelope.OperationID, TenantID: envelope.TenantID, WorkspaceID: envelope.WorkspaceID, OwnerSystem: envelope.OwnerSystem, Action: envelope.Action, PayloadHash: envelope.PayloadHash, Status: InboxClaimed, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := store.collection.InsertOne(ctx, entry); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return store.Claim(ctx, envelope)
		}
		return InboxEntry{}, false, fmt.Errorf("insert command inbox entry: %w", err)
	}
	return entry, false, nil
}

func (store *MongoInboxStore) Commit(ctx context.Context, claimed InboxEntry, result ResultEnvelope) (InboxEntry, error) {
	if store == nil || store.collection == nil {
		return InboxEntry{}, ErrCommandUnavailable
	}
	if err := result.Validate(); err != nil {
		return InboxEntry{}, err
	}
	filter := bson.D{{Key: "operationId", Value: claimed.OperationID}, {Key: "tenantId", Value: claimed.TenantID}, {Key: "workspaceId", Value: claimed.WorkspaceID}, {Key: "revision", Value: claimed.Revision}, {Key: "status", Value: InboxClaimed}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: InboxCommitted}, {Key: "result", Value: result}, {Key: "revision", Value: claimed.Revision + 1}, {Key: "updatedAt", Value: result.OccurredAt.UTC()}}}}
	var saved InboxEntry
	err := store.collection.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&saved)
	if err == nil {
		return saved, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return InboxEntry{}, fmt.Errorf("commit command inbox entry: %w", err)
	}
	var current InboxEntry
	if findErr := store.collection.FindOne(ctx, bson.D{{Key: "operationId", Value: claimed.OperationID}, {Key: "tenantId", Value: claimed.TenantID}, {Key: "workspaceId", Value: claimed.WorkspaceID}}).Decode(&current); findErr != nil {
		if errors.Is(findErr, mongo.ErrNoDocuments) {
			return InboxEntry{}, ErrCommandNotFound
		}
		return InboxEntry{}, findErr
	}
	if current.Status == InboxCommitted && current.Result != nil && current.Result.EventID == result.EventID {
		return current, nil
	}
	return InboxEntry{}, ErrCommandInboxConflict
}

type InboxService struct {
	store InboxStore
	clock func() time.Time
}

func NewInboxService(store InboxStore) *InboxService {
	return &InboxService{store: store, clock: time.Now}
}

func (service *InboxService) WithClock(clock func() time.Time) *InboxService {
	if service != nil && clock != nil {
		service.clock = clock
	}
	return service
}

// Process claims an operation durably before invoking execute. The owner
// callback must commit its business transaction and outbox before returning a
// result; this abstraction intentionally does not pretend to span Mongo and
// the owner's database in one transaction.
func (service *InboxService) Process(ctx context.Context, envelope Envelope, execute func(context.Context, Envelope) (ResultEnvelope, error)) (ResultEnvelope, bool, error) {
	if service == nil || service.store == nil || service.clock == nil || execute == nil {
		return ResultEnvelope{}, false, ErrCommandUnavailable
	}
	if err := envelope.Validate(); err != nil {
		return ResultEnvelope{}, false, err
	}
	claimed, replayed, err := service.store.Claim(ctx, envelope)
	if err != nil {
		return ResultEnvelope{}, false, err
	}
	if replayed && claimed.Result != nil {
		return *claimed.Result, true, nil
	}
	result, executeErr := execute(ctx, envelope)
	if executeErr != nil {
		result = ResultEnvelope{EventID: "command-result-" + envelope.OperationID, OperationID: envelope.OperationID, TenantID: envelope.TenantID, WorkspaceID: envelope.WorkspaceID, OwnerSystem: envelope.OwnerSystem, Action: envelope.Action, Status: StatusFailed, ErrorCode: "OWNER_EXECUTION_FAILED", SafeError: "owner execution failed", OccurredAt: service.clock().UTC()}
	}
	if result.EventID == "" {
		result.EventID = "command-result-" + envelope.OperationID
	}
	if result.OperationID == "" {
		result.OperationID = envelope.OperationID
	}
	if result.TenantID == "" {
		result.TenantID = envelope.TenantID
	}
	if result.WorkspaceID == "" {
		result.WorkspaceID = envelope.WorkspaceID
	}
	if result.OwnerSystem == "" {
		result.OwnerSystem = envelope.OwnerSystem
	}
	if result.Action == "" {
		result.Action = envelope.Action
	}
	if result.OccurredAt.IsZero() {
		result.OccurredAt = service.clock().UTC()
	}
	if result.OperationID != envelope.OperationID || result.TenantID != envelope.TenantID || result.WorkspaceID != envelope.WorkspaceID || result.OwnerSystem != envelope.OwnerSystem || result.Action != envelope.Action {
		return ResultEnvelope{}, false, ErrCommandResultConflict
	}
	if err := result.Validate(); err != nil {
		return ResultEnvelope{}, false, err
	}
	if _, err := service.store.Commit(ctx, claimed, result); err != nil {
		return ResultEnvelope{}, false, err
	}
	if executeErr != nil {
		return result, false, executeErr
	}
	return result, false, nil
}

func inboxKey(envelope Envelope) string {
	return inboxKeyValues(envelope.TenantID, envelope.WorkspaceID, envelope.OwnerSystem, envelope.OperationID)
}

func inboxKeyValues(tenantID, workspaceID, ownerSystem, operationID string) string {
	return tenantID + "\x00" + workspaceID + "\x00" + ownerSystem + "\x00" + operationID
}
