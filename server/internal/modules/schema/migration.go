package schema

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var (
	ErrMigrationPlanNotFound = errors.New("migration plan not found")
	ErrMigrationPlanExists   = errors.New("migration plan already exists")
	ErrMigrationPlanConflict = errors.New("migration plan version conflict")
	ErrMigrationPlanTerminal = errors.New("migration plan is already terminal")
	ErrMigrationPlanInvalid  = errors.New("invalid migration plan")
)

type MigrationPlanMode string

const (
	MigrationModeDryRun MigrationPlanMode = "DRY_RUN"
	MigrationModeApply  MigrationPlanMode = "APPLY"
)

type MigrationPlanStatus string

const (
	MigrationStatusDraft           MigrationPlanStatus = "DRAFT"
	MigrationStatusApproved        MigrationPlanStatus = "APPROVED"
	MigrationStatusRunning         MigrationPlanStatus = "RUNNING"
	MigrationStatusCancelRequested MigrationPlanStatus = "CANCEL_REQUESTED"
	MigrationStatusCancelled       MigrationPlanStatus = "CANCELLED"
	MigrationStatusSucceeded       MigrationPlanStatus = "SUCCEEDED"
	MigrationStatusFailed          MigrationPlanStatus = "FAILED"
)

// MigrationPlan is an immutable version pair plus a mutable, auditable
// execution state. It never contains record payloads or transformation code.
type MigrationPlan struct {
	ID                 string               `bson:"_id" json:"planId"`
	TenantID           string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID        string               `bson:"workspaceId" json:"workspaceId"`
	SchemaID           string               `bson:"schemaId" json:"schemaId"`
	FromVersion        int64                `bson:"fromVersion" json:"fromVersion"`
	ToVersion          int64                `bson:"toVersion" json:"toVersion"`
	Mode               MigrationPlanMode    `bson:"mode" json:"mode"`
	FailureSampleLimit int64                `bson:"failureSampleLimit" json:"failureSampleLimit"`
	Compatibility      CompatibilityReport  `bson:"compatibility" json:"compatibility"`
	CompatibilityHash  string               `bson:"compatibilityHash" json:"compatibilityHash"`
	Status             MigrationPlanStatus  `bson:"status" json:"status"`
	Version            int64                `bson:"version" json:"version"`
	CreatedBy          identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	UpdatedBy          identity.IdentityKey `bson:"updatedBy" json:"updatedBy"`
	CreatedAt          time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt          time.Time            `bson:"updatedAt" json:"updatedAt"`
	CancelRequestedAt  *time.Time           `bson:"cancelRequestedAt,omitempty" json:"cancelRequestedAt,omitempty"`
}

func (plan MigrationPlan) Validate() error {
	if strings.TrimSpace(plan.ID) == "" || strings.TrimSpace(plan.TenantID) == "" || strings.TrimSpace(plan.WorkspaceID) == "" || strings.TrimSpace(plan.SchemaID) == "" {
		return ErrMigrationPlanInvalid
	}
	if plan.FromVersion < 1 || plan.ToVersion <= plan.FromVersion || plan.FailureSampleLimit < 1 || plan.FailureSampleLimit > 100 || plan.Version < 1 {
		return ErrMigrationPlanInvalid
	}
	if plan.Mode != MigrationModeDryRun && plan.Mode != MigrationModeApply {
		return ErrMigrationPlanInvalid
	}
	switch plan.Status {
	case MigrationStatusDraft, MigrationStatusApproved, MigrationStatusRunning, MigrationStatusCancelRequested, MigrationStatusCancelled, MigrationStatusSucceeded, MigrationStatusFailed:
	default:
		return ErrMigrationPlanInvalid
	}
	if len(plan.CompatibilityHash) != len("sha256:")+64 || !strings.HasPrefix(plan.CompatibilityHash, "sha256:") {
		return ErrMigrationPlanInvalid
	}
	if plan.CreatedBy.Issuer == "" || plan.CreatedBy.Subject == "" || plan.UpdatedBy.Issuer == "" || plan.UpdatedBy.Subject == "" || plan.CreatedAt.IsZero() || plan.UpdatedAt.IsZero() {
		return ErrMigrationPlanInvalid
	}
	return nil
}

type MigrationPlanStore interface {
	CreateMigrationPlan(context.Context, MigrationPlan) error
	FindMigrationPlan(context.Context, string, string, string, string) (MigrationPlan, error)
	FindMigrationPlanByFingerprint(context.Context, string, string, string, int64, int64, MigrationPlanMode, string) (MigrationPlan, error)
	UpdateMigrationPlan(context.Context, MigrationPlan, int64) (MigrationPlan, error)
}

type MigrationPlanReceipt struct {
	TenantID       string        `bson:"tenantId"`
	WorkspaceID    string        `bson:"workspaceId"`
	Operation      string        `bson:"operation"`
	IdempotencyKey string        `bson:"idempotencyKey"`
	RequestHash    string        `bson:"requestHash"`
	Plan           MigrationPlan `bson:"plan"`
}

type MigrationPlanReceiptStore interface {
	Find(context.Context, string, string, string, string) (MigrationPlanReceipt, error)
	Save(context.Context, MigrationPlanReceipt) error
}

type MigrationPlanInput struct {
	TenantID           string
	WorkspaceID        string
	SchemaID           string
	FromVersion        int64
	ToVersion          int64
	Mode               MigrationPlanMode
	FailureSampleLimit int64
	RequestID          string
	IdempotencyKey     string
}

type MigrationService struct {
	registry    Registry
	plans       MigrationPlanStore
	receipts    MigrationPlanReceiptStore
	authorizer  *identity.Authorizer
	auditWriter audit.Writer
	clock       func() time.Time
}

func NewMigrationService(registry Registry, plans MigrationPlanStore, authorizer *identity.Authorizer, receipts MigrationPlanReceiptStore, auditWriter audit.Writer) *MigrationService {
	return &MigrationService{registry: registry, plans: plans, receipts: receipts, authorizer: authorizer, auditWriter: auditWriter, clock: time.Now}
}

func (service *MigrationService) Create(ctx context.Context, principal identity.Principal, input MigrationPlanInput) (MigrationPlan, bool, error) {
	if service == nil || service.registry == nil || service.plans == nil || service.receipts == nil || service.authorizer == nil || service.auditWriter == nil || service.clock == nil {
		return MigrationPlan{}, false, identity.ErrForbidden
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return MigrationPlan{}, false, ErrIdempotencyKeyRequired
	}
	if err := authorizeMigration(ctx, service.authorizer, principal, input.TenantID, input.WorkspaceID); err != nil {
		return MigrationPlan{}, false, err
	}
	if input.FromVersion < 1 || input.ToVersion <= input.FromVersion || input.FailureSampleLimit < 1 || input.FailureSampleLimit > 100 || (input.Mode != MigrationModeDryRun && input.Mode != MigrationModeApply) {
		return MigrationPlan{}, false, ErrMigrationPlanInvalid
	}
	from, err := service.registry.Get(ctx, input.TenantID, input.SchemaID, input.FromVersion)
	if err != nil {
		return MigrationPlan{}, false, err
	}
	to, err := service.registry.Get(ctx, input.TenantID, input.SchemaID, input.ToVersion)
	if err != nil {
		return MigrationPlan{}, false, err
	}
	if from.Status != StatusPublished || (to.Status != StatusDraft && to.Status != StatusPublished) {
		return MigrationPlan{}, false, ErrMigrationPlanInvalid
	}
	fromJSON, err := bson.MarshalExtJSON(from.JSONSchema, false, false)
	if err != nil {
		return MigrationPlan{}, false, fmt.Errorf("encode source schema: %w", err)
	}
	toJSON, err := bson.MarshalExtJSON(to.JSONSchema, false, false)
	if err != nil {
		return MigrationPlan{}, false, fmt.Errorf("encode target schema: %w", err)
	}
	report, err := CheckBackwardCompatibility(fromJSON, toJSON)
	if err != nil {
		return MigrationPlan{}, false, err
	}
	compatibilityHash, err := hashCompatibility(report)
	if err != nil {
		return MigrationPlan{}, false, err
	}
	requestHash := migrationPlanRequestHash(input, compatibilityHash)
	if receipt, found, receiptErr := service.receipt(ctx, input.TenantID, input.WorkspaceID, "migration.plan.create", input.IdempotencyKey, requestHash); receiptErr != nil || found {
		return receipt, found, receiptErr
	}
	if existing, findErr := service.plans.FindMigrationPlanByFingerprint(ctx, input.TenantID, input.WorkspaceID, input.SchemaID, input.FromVersion, input.ToVersion, input.Mode, compatibilityHash); findErr == nil {
		return existing, false, nil
	} else if !errors.Is(findErr, ErrMigrationPlanNotFound) {
		return MigrationPlan{}, false, findErr
	}
	planID, err := newMigrationPlanID()
	if err != nil {
		return MigrationPlan{}, false, err
	}
	now := service.clock().UTC()
	plan := MigrationPlan{ID: planID, TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, SchemaID: input.SchemaID, FromVersion: input.FromVersion, ToVersion: input.ToVersion, Mode: input.Mode, FailureSampleLimit: input.FailureSampleLimit, Compatibility: report, CompatibilityHash: compatibilityHash, Status: MigrationStatusDraft, Version: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := plan.Validate(); err != nil {
		return MigrationPlan{}, false, err
	}
	var result MigrationPlan
	err = service.inTransaction(ctx, func(transactionContext context.Context) error {
		if err := service.plans.CreateMigrationPlan(transactionContext, plan); err != nil {
			return err
		}
		result = plan
		if err := service.auditWriter.Append(transactionContext, audit.Entry{TenantID: plan.TenantID, WorkspaceID: plan.WorkspaceID, Action: "migration.plan.create", Actor: principal.IdentityKey(), ResourceType: "MigrationPlan", ResourceID: plan.ID, ResourceVersion: plan.Version, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, AfterHash: requestHash, CreatedAt: now}); err != nil {
			return fmt.Errorf("append migration audit: %w", err)
		}
		return service.receipts.Save(transactionContext, MigrationPlanReceipt{TenantID: plan.TenantID, WorkspaceID: plan.WorkspaceID, Operation: "migration.plan.create", IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Plan: plan})
	})
	return result, false, err
}

func (service *MigrationService) Get(ctx context.Context, principal identity.Principal, tenantID, workspaceID, schemaID, planID string) (MigrationPlan, error) {
	if service == nil || service.plans == nil || service.authorizer == nil {
		return MigrationPlan{}, identity.ErrForbidden
	}
	if err := authorizeMigration(ctx, service.authorizer, principal, tenantID, workspaceID); err != nil {
		return MigrationPlan{}, err
	}
	return service.plans.FindMigrationPlan(ctx, tenantID, workspaceID, schemaID, planID)
}

func (service *MigrationService) Cancel(ctx context.Context, principal identity.Principal, tenantID, workspaceID, schemaID, planID string, expectedVersion int64, requestID, idempotencyKey string) (MigrationPlan, bool, error) {
	if service == nil || service.plans == nil || service.receipts == nil || service.authorizer == nil || service.auditWriter == nil || service.clock == nil {
		return MigrationPlan{}, false, identity.ErrForbidden
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return MigrationPlan{}, false, ErrIdempotencyKeyRequired
	}
	if expectedVersion < 1 {
		return MigrationPlan{}, false, ErrMigrationPlanConflict
	}
	if err := authorizeMigration(ctx, service.authorizer, principal, tenantID, workspaceID); err != nil {
		return MigrationPlan{}, false, err
	}
	plan, err := service.plans.FindMigrationPlan(ctx, tenantID, workspaceID, schemaID, planID)
	if err != nil {
		return MigrationPlan{}, false, err
	}
	requestHash := migrationCancelRequestHash(plan.ID, expectedVersion)
	if receipt, found, receiptErr := service.receipt(ctx, tenantID, workspaceID, "migration.plan.cancel", idempotencyKey, requestHash); receiptErr != nil || found {
		return receipt, found, receiptErr
	}
	if plan.Version != expectedVersion {
		return MigrationPlan{}, false, ErrMigrationPlanConflict
	}
	nextStatus := MigrationPlanStatus("")
	switch plan.Status {
	case MigrationStatusDraft, MigrationStatusApproved:
		nextStatus = MigrationStatusCancelled
	case MigrationStatusRunning:
		nextStatus = MigrationStatusCancelRequested
	case MigrationStatusCancelRequested, MigrationStatusCancelled:
		return plan, false, nil
	case MigrationStatusSucceeded, MigrationStatusFailed:
		return MigrationPlan{}, false, ErrMigrationPlanTerminal
	default:
		return MigrationPlan{}, false, ErrMigrationPlanInvalid
	}
	now := service.clock().UTC()
	updated := plan
	updated.Status = nextStatus
	updated.Version++
	updated.UpdatedBy = principal.IdentityKey()
	updated.UpdatedAt = now
	if nextStatus == MigrationStatusCancelRequested {
		updated.CancelRequestedAt = &now
	}
	var result MigrationPlan
	err = service.inTransaction(ctx, func(transactionContext context.Context) error {
		var updateErr error
		result, updateErr = service.plans.UpdateMigrationPlan(transactionContext, updated, expectedVersion)
		if updateErr != nil {
			return updateErr
		}
		if err := service.auditWriter.Append(transactionContext, audit.Entry{TenantID: plan.TenantID, WorkspaceID: plan.WorkspaceID, Action: "migration.plan.cancel", Actor: principal.IdentityKey(), ResourceType: "MigrationPlan", ResourceID: plan.ID, ResourceVersion: result.Version, RequestID: requestID, IdempotencyKey: idempotencyKey, BeforeHash: plan.CompatibilityHash, AfterHash: requestHash, CreatedAt: now}); err != nil {
			return fmt.Errorf("append migration cancel audit: %w", err)
		}
		return service.receipts.Save(transactionContext, MigrationPlanReceipt{TenantID: plan.TenantID, WorkspaceID: plan.WorkspaceID, Operation: "migration.plan.cancel", IdempotencyKey: idempotencyKey, RequestHash: requestHash, Plan: result})
	})
	return result, false, err
}

func (service *MigrationService) inTransaction(ctx context.Context, fn func(context.Context) error) error {
	if transactional, ok := service.registry.(TransactionalRegistry); ok {
		return transactional.WithTransaction(ctx, fn)
	}
	return fn(ctx)
}

func (service *MigrationService) receipt(ctx context.Context, tenantID, workspaceID, operation, key, requestHash string) (MigrationPlan, bool, error) {
	receipt, err := service.receipts.Find(ctx, tenantID, workspaceID, operation, key)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return MigrationPlan{}, false, nil
		}
		return MigrationPlan{}, false, err
	}
	if receipt.RequestHash != requestHash {
		return MigrationPlan{}, false, ErrIdempotencyConflict
	}
	return receipt.Plan, true, nil
}

func authorizeMigration(ctx context.Context, authorizer *identity.Authorizer, principal identity.Principal, tenantID, workspaceID string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workspaceID) == "" {
		return ErrMigrationPlanInvalid
	}
	_, err := authorizer.Authorize(ctx, principal, tenantID, workspaceID, identity.ActionSchemaManage)
	return err
}

func hashCompatibility(report CompatibilityReport) (string, error) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("encode compatibility report: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func migrationPlanRequestHash(input MigrationPlanInput, compatibilityHash string) string {
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%d\x00%s", input.TenantID, input.WorkspaceID, input.SchemaID, input.FromVersion, input.ToVersion, input.Mode, input.FailureSampleLimit, compatibilityHash)
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func migrationCancelRequestHash(planID string, expectedVersion int64) string {
	value := fmt.Sprintf("cancel\x00%s\x00%d", planID, expectedVersion)
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func newMigrationPlanID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate migration plan ID: %w", err)
	}
	return "migration-" + hex.EncodeToString(random[:]), nil
}
