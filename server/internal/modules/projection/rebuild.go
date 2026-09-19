package projection

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	MaxRebuildOperations = 100
	RebuildPollInterval  = 500 * time.Millisecond
)

var (
	ErrRebuildUnavailable         = errors.New("projection rebuild is unavailable")
	ErrRebuildInvalid             = errors.New("invalid projection rebuild request")
	ErrRebuildNotFound            = errors.New("projection rebuild operation not found")
	ErrRebuildExists              = errors.New("projection rebuild operation already exists")
	ErrRebuildRevisionConflict    = errors.New("projection rebuild operation revision conflict")
	ErrRebuildImmutable           = errors.New("projection rebuild operation is immutable")
	ErrRebuildNotCancellable      = errors.New("projection rebuild operation cannot be cancelled")
	ErrRebuildReceiptNotFound     = errors.New("projection rebuild receipt not found")
	ErrRebuildIdempotencyConflict = errors.New("projection rebuild idempotency conflict")
	ErrRebuildCancellation        = errors.New("projection rebuild cancellation requested")
)

type RebuildStatus string

const (
	RebuildAccepted        RebuildStatus = "ACCEPTED"
	RebuildRunning         RebuildStatus = "RUNNING"
	RebuildCancelRequested RebuildStatus = "CANCEL_REQUESTED"
	RebuildCancelled       RebuildStatus = "CANCELLED"
	RebuildSucceeded       RebuildStatus = "SUCCEEDED"
	RebuildFailed          RebuildStatus = "FAILED"
)

// ProjectionRebuildOperation is the durable control-plane record for a
// generation rebuild. The active pointer is switched only after the staged
// generation has been fully compiled and validated.
type ProjectionRebuildOperation struct {
	ID                  string               `bson:"operationId" json:"operationId"`
	TenantID            string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID         string               `bson:"workspaceId" json:"workspaceId"`
	Status              RebuildStatus        `bson:"status" json:"status"`
	Revision            int64                `bson:"revision" json:"revision"`
	GenerationBefore    string               `bson:"generationBefore,omitempty" json:"generationBefore,omitempty"`
	StagingGenerationID string               `bson:"stagingGenerationId,omitempty" json:"stagingGenerationId,omitempty"`
	StagingCollection   string               `bson:"stagingCollection,omitempty" json:"stagingCollection,omitempty"`
	ActiveGenerationID  string               `bson:"activeGenerationId,omitempty" json:"activeGenerationId,omitempty"`
	MappingCount        int                  `bson:"mappingCount" json:"mappingCount"`
	CreatedBy           identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	UpdatedBy           identity.IdentityKey `bson:"updatedBy" json:"updatedBy"`
	CreatedAt           time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt           time.Time            `bson:"updatedAt" json:"updatedAt"`
	StartedAt           *time.Time           `bson:"startedAt,omitempty" json:"startedAt,omitempty"`
	FinishedAt          *time.Time           `bson:"finishedAt,omitempty" json:"finishedAt,omitempty"`
	SafeError           string               `bson:"safeError,omitempty" json:"safeError,omitempty"`
}

func (operation ProjectionRebuildOperation) Validate() error {
	if !nonBlankBounded(operation.ID, 128) || !nonBlankBounded(operation.TenantID, maxCatalogID) || !nonBlankBounded(operation.WorkspaceID, maxCatalogID) || operation.Revision < 1 || operation.MappingCount < 0 || operation.CreatedAt.IsZero() || operation.UpdatedAt.IsZero() || operation.CreatedBy.Issuer == "" || operation.CreatedBy.Subject == "" || operation.UpdatedBy.Issuer == "" || operation.UpdatedBy.Subject == "" {
		return ErrRebuildInvalid
	}
	if strings.TrimSpace(operation.SafeError) != operation.SafeError || len(operation.SafeError) > 512 {
		return ErrRebuildInvalid
	}
	switch operation.Status {
	case RebuildAccepted:
		if operation.StartedAt != nil || operation.FinishedAt != nil {
			return ErrRebuildInvalid
		}
	case RebuildRunning, RebuildCancelRequested:
		if operation.StartedAt == nil || operation.FinishedAt != nil {
			return ErrRebuildInvalid
		}
	case RebuildCancelled, RebuildSucceeded, RebuildFailed:
		if operation.FinishedAt == nil {
			return ErrRebuildInvalid
		}
	default:
		return ErrRebuildInvalid
	}
	return nil
}

type RebuildStartInput struct {
	TenantID       string
	WorkspaceID    string
	RequestID      string
	IdempotencyKey string
}

type RebuildCancelInput struct {
	TenantID       string
	WorkspaceID    string
	OperationID    string
	RequestID      string
	IdempotencyKey string
}

type RebuildStore interface {
	Create(context.Context, ProjectionRebuildOperation) error
	Find(context.Context, string, string, string) (ProjectionRebuildOperation, error)
	ListRunnable(context.Context, int64) ([]ProjectionRebuildOperation, error)
	SaveCAS(context.Context, ProjectionRebuildOperation, int64) (ProjectionRebuildOperation, error)
}

type RebuildTransactional interface {
	WithTransaction(context.Context, func(context.Context) error) error
}

type RebuildReceipt struct {
	TenantID       string                      `bson:"tenantId"`
	WorkspaceID    string                      `bson:"workspaceId"`
	Operation      string                      `bson:"operation"`
	IdempotencyKey string                      `bson:"idempotencyKey"`
	RequestHash    string                      `bson:"requestHash"`
	Rebuild        *ProjectionRebuildOperation `bson:"rebuild,omitempty"`
}

type RebuildReceiptStore interface {
	Find(context.Context, string, string, string, string) (RebuildReceipt, error)
	Save(context.Context, RebuildReceipt) error
}

type ProjectionRebuildService struct {
	operations   RebuildStore
	builder      *MappingGenerationBuilder
	registry     *MappingGenerationRegistry
	authorizer   *identity.Authorizer
	receipts     RebuildReceiptStore
	audit        audit.Writer
	archive      ProjectionEventArchiveReader
	readPointers *MongoProjectionReadPointerRepository
	clock        func() time.Time
	id           func() (string, error)
}

func NewProjectionRebuildService(operations RebuildStore, builder *MappingGenerationBuilder, registry *MappingGenerationRegistry, authorizer *identity.Authorizer, receipts RebuildReceiptStore, auditWriter audit.Writer) *ProjectionRebuildService {
	return &ProjectionRebuildService{operations: operations, builder: builder, registry: registry, authorizer: authorizer, receipts: receipts, audit: auditWriter, clock: time.Now, id: newRebuildID}
}

// WithReplayDependencies enables the data-plane portion of rebuild. Keeping
// this opt-in preserves the in-memory control-plane service used by unit
// tests, while the production runtime always wires both durable stores.
func (service *ProjectionRebuildService) WithReplayDependencies(archive ProjectionEventArchiveReader, readPointers *MongoProjectionReadPointerRepository) *ProjectionRebuildService {
	if service != nil {
		service.archive = archive
		service.readPointers = readPointers
	}
	return service
}

func (service *ProjectionRebuildService) ready() error {
	if service == nil || service.operations == nil || service.builder == nil || service.registry == nil || service.authorizer == nil || service.receipts == nil || service.audit == nil || service.clock == nil || service.id == nil {
		return ErrRebuildUnavailable
	}
	return nil
}

func (service *ProjectionRebuildService) Start(ctx context.Context, principal identity.Principal, input RebuildStartInput) (ProjectionRebuildOperation, bool, error) {
	if err := service.ready(); err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	if err := validateRebuildHeaders(input.TenantID, input.WorkspaceID, input.RequestID, input.IdempotencyKey); err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionProjectionManage); err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	tenantID, workspaceID := strings.TrimSpace(input.TenantID), strings.TrimSpace(input.WorkspaceID)
	requestHash := rebuildHash("projection.rebuild.create", tenantID, workspaceID)
	if receipt, found, err := service.findReceipt(ctx, tenantID, workspaceID, "projection.rebuild.create", input.IdempotencyKey, requestHash); err != nil || found {
		if !found || receipt.Rebuild == nil {
			return ProjectionRebuildOperation{}, found, err
		}
		return *receipt.Rebuild, true, err
	}
	operationID, err := service.id()
	if err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	now := service.clock().UTC()
	operation := ProjectionRebuildOperation{ID: operationID, TenantID: tenantID, WorkspaceID: workspaceID, Status: RebuildAccepted, Revision: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := operation.Validate(); err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	err = service.inTransaction(ctx, func(tx context.Context) error {
		if err := service.operations.Create(tx, operation); err != nil {
			return err
		}
		if err := service.audit.Append(tx, audit.Entry{TenantID: tenantID, WorkspaceID: workspaceID, Action: "projection.rebuild.create", Actor: principal.IdentityKey(), ResourceType: "ProjectionRebuildOperation", ResourceID: operation.ID, ResourceVersion: operation.Revision, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, AfterHash: requestHash, CreatedAt: now}); err != nil {
			return err
		}
		return service.receipts.Save(tx, RebuildReceipt{TenantID: tenantID, WorkspaceID: workspaceID, Operation: "projection.rebuild.create", IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Rebuild: &operation})
	})
	return operation, false, err
}

func (service *ProjectionRebuildService) Get(ctx context.Context, principal identity.Principal, tenantID, workspaceID, operationID string) (ProjectionRebuildOperation, error) {
	if err := service.ready(); err != nil {
		return ProjectionRebuildOperation{}, err
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(operationID) == "" {
		return ProjectionRebuildOperation{}, ErrRebuildInvalid
	}
	if err := service.authorize(ctx, principal, tenantID, workspaceID, identity.ActionOperationsRead); err != nil {
		return ProjectionRebuildOperation{}, err
	}
	return service.operations.Find(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), strings.TrimSpace(operationID))
}

func (service *ProjectionRebuildService) Cancel(ctx context.Context, principal identity.Principal, input RebuildCancelInput) (ProjectionRebuildOperation, bool, error) {
	if err := service.ready(); err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	if err := validateRebuildHeaders(input.TenantID, input.WorkspaceID, input.RequestID, input.IdempotencyKey); err != nil || strings.TrimSpace(input.OperationID) == "" {
		if err != nil {
			return ProjectionRebuildOperation{}, false, err
		}
		return ProjectionRebuildOperation{}, false, ErrRebuildInvalid
	}
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionProjectionManage); err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	tenantID, workspaceID, operationID := strings.TrimSpace(input.TenantID), strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.OperationID)
	operation, err := service.operations.Find(ctx, tenantID, workspaceID, operationID)
	if err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	requestHash := rebuildHash("projection.rebuild.cancel", tenantID, workspaceID, operationID, fmt.Sprint(operation.Revision))
	if receipt, found, receiptErr := service.findReceipt(ctx, tenantID, workspaceID, "projection.rebuild.cancel", input.IdempotencyKey, requestHash); receiptErr != nil || found {
		if !found || receipt.Rebuild == nil {
			return ProjectionRebuildOperation{}, found, receiptErr
		}
		return *receipt.Rebuild, true, receiptErr
	}
	if operation.Status != RebuildAccepted && operation.Status != RebuildRunning && operation.Status != RebuildCancelRequested {
		return ProjectionRebuildOperation{}, false, ErrRebuildNotCancellable
	}
	if operation.Status == RebuildCancelRequested {
		return operation, false, service.saveCancelReceipt(ctx, principal, operation, input, requestHash)
	}
	now := service.clock().UTC()
	updated := operation
	if operation.Status == RebuildAccepted {
		updated.Status = RebuildCancelled
		updated.FinishedAt = &now
	} else {
		updated.Status = RebuildCancelRequested
	}
	updated.UpdatedBy = principal.IdentityKey()
	updated.UpdatedAt = now
	err = service.inTransaction(ctx, func(tx context.Context) error {
		var saveErr error
		updated, saveErr = service.operations.SaveCAS(tx, updated, operation.Revision)
		if saveErr != nil {
			return saveErr
		}
		if saveErr := service.audit.Append(tx, audit.Entry{TenantID: tenantID, WorkspaceID: workspaceID, Action: "projection.rebuild.cancel", Actor: principal.IdentityKey(), ResourceType: "ProjectionRebuildOperation", ResourceID: updated.ID, ResourceVersion: updated.Revision, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, AfterHash: requestHash, CreatedAt: now}); saveErr != nil {
			return saveErr
		}
		return service.receipts.Save(tx, RebuildReceipt{TenantID: tenantID, WorkspaceID: workspaceID, Operation: "projection.rebuild.cancel", IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Rebuild: &updated})
	})
	if err != nil {
		return ProjectionRebuildOperation{}, false, err
	}
	return updated, false, nil
}

func (service *ProjectionRebuildService) saveCancelReceipt(ctx context.Context, principal identity.Principal, operation ProjectionRebuildOperation, input RebuildCancelInput, requestHash string) error {
	return service.receipts.Save(ctx, RebuildReceipt{TenantID: operation.TenantID, WorkspaceID: operation.WorkspaceID, Operation: "projection.rebuild.cancel", IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Rebuild: &operation})
}

func (service *ProjectionRebuildService) ProcessOne(ctx context.Context) error {
	if err := service.ready(); err != nil {
		return err
	}
	operations, err := service.operations.ListRunnable(ctx, 8)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if err := service.process(ctx, operation); err != nil {
			return err
		}
	}
	return nil
}

func (service *ProjectionRebuildService) process(ctx context.Context, operation ProjectionRebuildOperation) error {
	if operation.Status == RebuildCancelRequested {
		return service.finishCancelled(ctx, operation)
	}
	if operation.Status == RebuildAccepted {
		now := service.clock().UTC()
		operation.GenerationBefore = service.registry.ActiveGenerationID()
		operation.Status = RebuildRunning
		operation.StartedAt = &now
		operation.UpdatedAt = now
		operation = operationWithActor(operation, identity.IdentityKey{Issuer: "record-hub", Subject: "rebuild-worker"})
		var err error
		operation, err = service.operations.SaveCAS(ctx, operation, operation.Revision)
		if err != nil {
			if errors.Is(err, ErrRebuildRevisionConflict) || errors.Is(err, ErrRebuildNotFound) {
				return nil
			}
			return err
		}
	}
	generation, err := service.builder.Build(ctx)
	if err != nil {
		return service.fail(ctx, operation, "generation validation failed")
	}
	latest, err := service.operations.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.ID)
	if err != nil {
		return err
	}
	if latest.Status == RebuildCancelRequested {
		return service.finishCancelled(ctx, latest)
	}
	if latest.Status != RebuildRunning {
		return nil
	}
	now := service.clock().UTC()
	staged := latest
	staged.StagingGenerationID = generation.ID
	staged.StagingCollection = stagingRecordsCollectionName(staged.ID)
	staged.MappingCount = generation.EntryCount()
	staged.UpdatedAt = now
	staged = operationWithActor(staged, identity.IdentityKey{Issuer: "record-hub", Subject: "rebuild-worker"})
	staged, err = service.operations.SaveCAS(ctx, staged, latest.Revision)
	if err != nil {
		return err
	}
	latest, err = service.operations.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.ID)
	if err != nil {
		return err
	}
	if latest.Status == RebuildCancelRequested {
		return service.finishCancelled(ctx, latest)
	}
	if latest.Status != RebuildRunning {
		return nil
	}
	if service.archive != nil && service.readPointers != nil {
		if err := service.replay(ctx, latest, generation); err != nil {
			if errors.Is(err, ErrRebuildCancellation) {
				current, findErr := service.operations.Find(ctx, latest.TenantID, latest.WorkspaceID, latest.ID)
				if findErr != nil {
					return findErr
				}
				return service.finishCancelled(ctx, current)
			}
			return service.fail(ctx, latest, "historical projection replay failed")
		}
		latest, err = service.operations.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.ID)
		if err != nil {
			return err
		}
		if latest.Status == RebuildCancelRequested {
			return service.finishCancelled(ctx, latest)
		}
		if latest.Status != RebuildRunning {
			return nil
		}
	}
	// The in-memory generation pointer and durable per-table read pointers are
	// switched only after staging replay has completed successfully.
	if service.readPointers != nil {
		if err := service.activateReadPointers(ctx, latest, generation); err != nil {
			return service.fail(ctx, latest, "projection read pointer activation failed")
		}
	}
	if err := service.registry.Activate(generation); err != nil {
		return service.fail(ctx, latest, "generation activation failed")
	}
	now = service.clock().UTC()
	latest.Status = RebuildSucceeded
	latest.ActiveGenerationID = generation.ID
	latest.FinishedAt = &now
	latest.UpdatedAt = now
	latest = operationWithActor(latest, identity.IdentityKey{Issuer: "record-hub", Subject: "rebuild-worker"})
	_, err = service.operations.SaveCAS(ctx, latest, staged.Revision)
	return err
}

func (service *ProjectionRebuildService) replay(ctx context.Context, operation ProjectionRebuildOperation, generation *MappingGeneration) error {
	if service.archive == nil || service.readPointers == nil || service.readPointers.database == nil || generation == nil {
		return ErrRebuildUnavailable
	}
	if err := NewMongoProjectionRepository(service.readPointers.database).EnsureStagingIndexes(ctx, operation.ID); err != nil {
		return err
	}
	entries, err := service.archive.List(ctx, operation.TenantID, operation.WorkspaceID, archiveReplayPageSize)
	if err != nil {
		return err
	}
	if len(entries) > MaxArchivedReplayEvents {
		return fmt.Errorf("replay event limit of %d reached; archive window is incomplete", MaxArchivedReplayEvents)
	}
	registry := NewMappingGenerationRegistry()
	if err := registry.Activate(generation); err != nil {
		return err
	}
	handlers := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(handlers); err != nil {
		return err
	}
	projector, err := NewSummaryProjector(handlers, &rebuildInbox{}, NewMongoProjectionRepository(service.readPointers.database).WithStaging(operation.ID), operation.WorkspaceID)
	if err != nil {
		return err
	}
	projector.WithMappingGenerations(registry).WithWorkspaceMappings(map[string]string{operation.TenantID: operation.WorkspaceID})
	for _, entry := range entries {
		latest, findErr := service.operations.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.ID)
		if findErr != nil {
			return findErr
		}
		if latest.Status == RebuildCancelRequested {
			return ErrRebuildCancellation
		}
		if latest.Status != RebuildRunning {
			return ErrRebuildCancellation
		}
		if err := projector.Handle(ctx, entry.Subject, entry.Raw); err != nil {
			return err
		}
	}
	return nil
}

func (service *ProjectionRebuildService) activateReadPointers(ctx context.Context, operation ProjectionRebuildOperation, generation *MappingGeneration) error {
	if service.readPointers == nil {
		return nil
	}
	var checkpoints []ProjectionCheckpoint
	if service.archive != nil {
		entries, err := service.archive.List(ctx, operation.TenantID, operation.WorkspaceID, archiveReplayPageSize)
		if err != nil {
			return err
		}
		latest := make(map[string]ProjectionCheckpoint, len(entries))
		for _, entry := range entries {
			key := strings.Join([]string{entry.Consumer, entry.SourceSystem, entry.AggregateType, entry.AggregateID}, "\x00")
			candidate := ProjectionCheckpoint{TenantID: entry.TenantID, WorkspaceID: entry.WorkspaceID, Consumer: entry.Consumer, SourceSystem: entry.SourceSystem, AggregateType: entry.AggregateType, AggregateID: entry.AggregateID, SourceVersion: entry.AggregateVersion, LastEventID: entry.EventID, SyncedAt: entry.ReceivedAt.UTC(), Status: CheckpointCurrent}
			if current, ok := latest[key]; !ok || candidate.SourceVersion > current.SourceVersion || (candidate.SourceVersion == current.SourceVersion && candidate.SyncedAt.After(current.SyncedAt)) {
				latest[key] = candidate
			}
		}
		checkpoints = make([]ProjectionCheckpoint, 0, len(latest))
		for _, checkpoint := range latest {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	return service.readPointers.ActivateManyWithCheckpoints(ctx, operation.TenantID, operation.WorkspaceID, operation.ID, generation.ID, stagingRecordsCollectionName(operation.ID), generation.TargetTableIDs(operation.TenantID, operation.WorkspaceID), checkpoints)
}

// rebuildInbox is intentionally ephemeral. Replaying archived events must
// never mutate the live inbox/checkpoint stream; staging repository writes are
// protected by record-version monotonicity instead.
type rebuildInbox struct{}

func (inbox *rebuildInbox) Claim(_ context.Context, claim InboxClaim) (InboxClaimResult, error) {
	hash := sha256.Sum256(claim.Payload)
	return InboxClaimResult{Event: InboxEvent{EventID: claim.EventID, Consumer: claim.Consumer, Subject: claim.Subject, TenantID: claim.TenantID, WorkspaceID: claim.WorkspaceID, PayloadHash: "sha256:" + hex.EncodeToString(hash[:]), Status: InboxProcessing, ReceivedAt: claim.ReceivedAt}}, nil
}

func (inbox *rebuildInbox) Get(context.Context, string, string) (InboxEvent, error) {
	return InboxEvent{}, ErrInboxNotFound
}

func (inbox *rebuildInbox) MarkApplied(context.Context, string, string, time.Time) error { return nil }

func (inbox *rebuildInbox) MarkRejected(context.Context, string, string, string, time.Time) error {
	return nil
}

func (service *ProjectionRebuildService) finishCancelled(ctx context.Context, operation ProjectionRebuildOperation) error {
	if operation.Status == RebuildCancelled {
		return nil
	}
	now := service.clock().UTC()
	operation.Status = RebuildCancelled
	operation.FinishedAt = &now
	operation.UpdatedAt = now
	operation = operationWithActor(operation, identity.IdentityKey{Issuer: "record-hub", Subject: "rebuild-worker"})
	_, err := service.operations.SaveCAS(ctx, operation, operation.Revision)
	if errors.Is(err, ErrRebuildRevisionConflict) || errors.Is(err, ErrRebuildNotFound) {
		return nil
	}
	return err
}

func (service *ProjectionRebuildService) fail(ctx context.Context, operation ProjectionRebuildOperation, safeError string) error {
	latest, err := service.operations.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.ID)
	if err != nil {
		return err
	}
	if latest.Status == RebuildCancelRequested {
		return service.finishCancelled(ctx, latest)
	}
	if latest.Status != RebuildRunning {
		return nil
	}
	now := service.clock().UTC()
	latest.Status = RebuildFailed
	latest.SafeError = safeError
	latest.FinishedAt = &now
	latest.UpdatedAt = now
	latest = operationWithActor(latest, identity.IdentityKey{Issuer: "record-hub", Subject: "rebuild-worker"})
	_, err = service.operations.SaveCAS(ctx, latest, latest.Revision)
	return err
}

func (service *ProjectionRebuildService) Run(ctx context.Context) error {
	if err := service.ready(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(RebuildPollInterval)
	defer ticker.Stop()
	for {
		if err := service.ProcessOne(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func operationWithActor(operation ProjectionRebuildOperation, actor identity.IdentityKey) ProjectionRebuildOperation {
	operation.UpdatedBy = actor
	return operation
}

func (service *ProjectionRebuildService) authorize(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workspaceID) == "" {
		return ErrRebuildInvalid
	}
	_, err := service.authorizer.Authorize(ctx, principal, strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), action)
	return err
}

func (service *ProjectionRebuildService) inTransaction(ctx context.Context, fn func(context.Context) error) error {
	if transactional, ok := service.operations.(RebuildTransactional); ok {
		return transactional.WithTransaction(ctx, fn)
	}
	return fn(ctx)
}

func (service *ProjectionRebuildService) findReceipt(ctx context.Context, tenantID, workspaceID, operation, key, requestHash string) (RebuildReceipt, bool, error) {
	receipt, err := service.receipts.Find(ctx, tenantID, workspaceID, operation, key)
	if err != nil {
		if errors.Is(err, ErrRebuildReceiptNotFound) {
			return RebuildReceipt{}, false, nil
		}
		return RebuildReceipt{}, false, err
	}
	if receipt.RequestHash != requestHash || receipt.Rebuild == nil {
		return RebuildReceipt{}, false, ErrRebuildIdempotencyConflict
	}
	return receipt, true, nil
}

func validateRebuildHeaders(tenantID, workspaceID, requestID, idempotencyKey string) error {
	if !nonBlankBounded(tenantID, maxCatalogID) || !nonBlankBounded(workspaceID, maxCatalogID) {
		return ErrRebuildInvalid
	}
	if strings.TrimSpace(requestID) == "" {
		return ErrRequestIDMissing
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ErrIdempotencyKeyMissing
	}
	if len(strings.TrimSpace(requestID)) > 256 || len(strings.TrimSpace(idempotencyKey)) > 256 {
		return ErrRebuildInvalid
	}
	return nil
}

func rebuildHash(operation string, values ...string) string {
	return hashCatalogInput(operation, values)
}

func newRebuildID() (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "rebuild-" + hex.EncodeToString(random[:]), nil
}

const rebuildCollectionName = "projection_rebuild_operations"
const rebuildReceiptCollectionName = "projection_rebuild_receipts"

type MongoRebuildRepository struct{ collection *mongo.Collection }

func NewMongoRebuildRepository(database *mongo.Database) *MongoRebuildRepository {
	return &MongoRebuildRepository{collection: database.Collection(rebuildCollectionName)}
}

func (repository *MongoRebuildRepository) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	if repository == nil || repository.collection == nil {
		return ErrRebuildUnavailable
	}
	session, err := repository.collection.Database().Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(context.Background())
	_, err = session.WithTransaction(ctx, func(tx context.Context) (interface{}, error) { return nil, fn(tx) })
	return err
}

func (repository *MongoRebuildRepository) EnsureIndexes(ctx context.Context) error {
	if repository == nil || repository.collection == nil {
		return ErrRebuildUnavailable
	}
	_, err := repository.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "operationId", Value: 1}, {Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}}, Options: options.Index().SetName("projection_rebuild_scope_id_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "updatedAt", Value: -1}}, Options: options.Index().SetName("projection_rebuild_status_updated")},
	})
	if err != nil {
		return fmt.Errorf("create projection rebuild indexes: %w", err)
	}
	return nil
}

func (repository *MongoRebuildRepository) Create(ctx context.Context, operation ProjectionRebuildOperation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	if _, err := repository.collection.InsertOne(ctx, operation); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrRebuildExists
		}
		return fmt.Errorf("insert projection rebuild operation: %w", err)
	}
	return nil
}

func (repository *MongoRebuildRepository) Find(ctx context.Context, tenantID, workspaceID, operationID string) (ProjectionRebuildOperation, error) {
	var operation ProjectionRebuildOperation
	err := repository.collection.FindOne(ctx, bson.D{{Key: "operationId", Value: operationID}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}).Decode(&operation)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ProjectionRebuildOperation{}, ErrRebuildNotFound
	}
	if err != nil {
		return ProjectionRebuildOperation{}, fmt.Errorf("find projection rebuild operation: %w", err)
	}
	return operation, nil
}

func (repository *MongoRebuildRepository) ListRunnable(ctx context.Context, limit int64) ([]ProjectionRebuildOperation, error) {
	if limit < 1 || limit > MaxRebuildOperations {
		return nil, ErrRebuildInvalid
	}
	cursor, err := repository.collection.Find(ctx, bson.D{{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{RebuildAccepted, RebuildRunning, RebuildCancelRequested}}}}}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "operationId", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list runnable rebuild operations: %w", err)
	}
	defer cursor.Close(ctx)
	var operations []ProjectionRebuildOperation
	if err := cursor.All(ctx, &operations); err != nil {
		return nil, fmt.Errorf("decode runnable rebuild operations: %w", err)
	}
	return operations, nil
}

func (repository *MongoRebuildRepository) SaveCAS(ctx context.Context, operation ProjectionRebuildOperation, expectedRevision int64) (ProjectionRebuildOperation, error) {
	if err := operation.Validate(); err != nil {
		return ProjectionRebuildOperation{}, err
	}
	if expectedRevision < 1 {
		return ProjectionRebuildOperation{}, ErrRebuildRevisionConflict
	}
	set := bson.D{{Key: "status", Value: operation.Status}, {Key: "updatedBy", Value: operation.UpdatedBy}, {Key: "updatedAt", Value: operation.UpdatedAt}, {Key: "generationBefore", Value: operation.GenerationBefore}, {Key: "stagingGenerationId", Value: operation.StagingGenerationID}, {Key: "stagingCollection", Value: operation.StagingCollection}, {Key: "activeGenerationId", Value: operation.ActiveGenerationID}, {Key: "mappingCount", Value: operation.MappingCount}, {Key: "startedAt", Value: operation.StartedAt}, {Key: "finishedAt", Value: operation.FinishedAt}, {Key: "safeError", Value: operation.SafeError}}
	update := bson.D{{Key: "$set", Value: set}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: 1}}}}
	var result ProjectionRebuildOperation
	err := repository.collection.FindOneAndUpdate(ctx, bson.D{{Key: "operationId", Value: operation.ID}, {Key: "tenantId", Value: operation.TenantID}, {Key: "workspaceId", Value: operation.WorkspaceID}, {Key: "revision", Value: expectedRevision}}, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return ProjectionRebuildOperation{}, fmt.Errorf("save projection rebuild operation: %w", err)
	}
	if _, findErr := repository.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.ID); findErr != nil {
		return ProjectionRebuildOperation{}, findErr
	}
	return ProjectionRebuildOperation{}, ErrRebuildRevisionConflict
}

type MongoRebuildReceiptStore struct{ collection *mongo.Collection }

func NewMongoRebuildReceiptStore(database *mongo.Database) *MongoRebuildReceiptStore {
	return &MongoRebuildReceiptStore{collection: database.Collection(rebuildReceiptCollectionName)}
}

func (store *MongoRebuildReceiptStore) EnsureIndexes(ctx context.Context) error {
	if store == nil || store.collection == nil {
		return ErrRebuildUnavailable
	}
	_, err := store.collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "operation", Value: 1}, {Key: "idempotencyKey", Value: 1}}, Options: options.Index().SetName("projection_rebuild_receipt_scope_key_unique").SetUnique(true)})
	if err != nil {
		return fmt.Errorf("create projection rebuild receipt index: %w", err)
	}
	return nil
}

func (store *MongoRebuildReceiptStore) Find(ctx context.Context, tenantID, workspaceID, operation, key string) (RebuildReceipt, error) {
	var receipt RebuildReceipt
	err := store.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "operation", Value: operation}, {Key: "idempotencyKey", Value: key}}).Decode(&receipt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return RebuildReceipt{}, ErrRebuildReceiptNotFound
	}
	if err != nil {
		return RebuildReceipt{}, fmt.Errorf("find projection rebuild receipt: %w", err)
	}
	return receipt, nil
}

func (store *MongoRebuildReceiptStore) Save(ctx context.Context, receipt RebuildReceipt) error {
	if receipt.TenantID == "" || receipt.WorkspaceID == "" || receipt.Operation == "" || receipt.IdempotencyKey == "" || receipt.RequestHash == "" || receipt.Rebuild == nil {
		return ErrRebuildInvalid
	}
	if _, err := store.collection.InsertOne(ctx, receipt); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			existing, findErr := store.Find(ctx, receipt.TenantID, receipt.WorkspaceID, receipt.Operation, receipt.IdempotencyKey)
			if findErr == nil && existing.RequestHash == receipt.RequestHash {
				return nil
			}
			return ErrRebuildIdempotencyConflict
		}
		return fmt.Errorf("save projection rebuild receipt: %w", err)
	}
	return nil
}

type ProjectionRebuildWorker struct {
	service *ProjectionRebuildService
	logger  *slog.Logger
}

func NewProjectionRebuildWorker(service *ProjectionRebuildService, logger *slog.Logger) *ProjectionRebuildWorker {
	return &ProjectionRebuildWorker{service: service, logger: logger}
}

func (worker *ProjectionRebuildWorker) Name() string { return "projection-rebuild" }

func (worker *ProjectionRebuildWorker) Run(ctx context.Context) error {
	if worker == nil || worker.service == nil {
		return ErrRebuildUnavailable
	}
	if err := worker.service.ready(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(RebuildPollInterval)
	defer ticker.Stop()
	for {
		if err := worker.service.ProcessOne(ctx); err != nil && !errors.Is(err, context.Canceled) && worker.logger != nil {
			worker.logger.Warn("projection rebuild processing failed; retrying", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
