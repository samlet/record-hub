package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var (
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
	ErrIdempotencyConflict    = errors.New("idempotency key was used with different input")
	ErrReceiptNotFound        = errors.New("idempotency receipt not found")
)

type Registry interface {
	Create(context.Context, Definition) error
	Get(context.Context, string, string, int64) (Definition, error)
	UpdateDraft(context.Context, Definition, int64) (Definition, error)
	PublishDefinition(context.Context, string, string, int64, int64, identity.IdentityKey, string) (Definition, error)
}

type TransactionalRegistry interface {
	Registry
	WithTransaction(context.Context, func(context.Context) error) error
}

type Receipt struct {
	TenantID       string     `bson:"tenantId"`
	WorkspaceID    string     `bson:"workspaceId"`
	Operation      string     `bson:"operation"`
	IdempotencyKey string     `bson:"idempotencyKey"`
	RequestHash    string     `bson:"requestHash"`
	Definition     Definition `bson:"definition"`
}

type ReceiptStore interface {
	Find(context.Context, string, string, string, string) (Receipt, error)
	Save(context.Context, Receipt) error
}

type Service struct {
	registry    Registry
	authorizer  *identity.Authorizer
	receipts    ReceiptStore
	auditWriter audit.Writer
	clock       func() time.Time
}

func NewService(registry Registry, authorizer *identity.Authorizer, receipts ReceiptStore, auditWriter audit.Writer) *Service {
	return &Service{registry: registry, authorizer: authorizer, receipts: receipts, auditWriter: auditWriter, clock: time.Now}
}

type DraftInput struct {
	TenantID       string
	WorkspaceID    string
	SchemaID       string
	Name           string
	Version        int64
	Revision       int64
	JSONSchema     bson.Raw
	SemanticTypes  []string
	RequestID      string
	IdempotencyKey string
}

func (service *Service) CreateDraft(ctx context.Context, principal identity.Principal, input DraftInput) (Definition, error) {
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID); err != nil {
		return Definition{}, err
	}
	if input.IdempotencyKey == "" {
		return Definition{}, ErrIdempotencyKeyRequired
	}
	input.Revision = 1
	normalized, err := NormalizeSemanticTypes(input.SemanticTypes)
	if err != nil {
		return Definition{}, err
	}
	if _, err := compileBSONSchema(input.SchemaID, input.JSONSchema); err != nil {
		return Definition{}, err
	}
	requestHash := draftHash(input, StatusDraft, normalized)
	if result, found, err := service.receipt(ctx, input, "schema.create", requestHash); err != nil || found {
		return result, err
	}
	now := service.clock().UTC()
	definition := Definition{
		TenantID: input.TenantID, SchemaID: input.SchemaID, Name: input.Name, Version: input.Version, Revision: 1,
		Status: StatusDraft, JSONSchema: input.JSONSchema, SemanticTypes: normalized,
		CreatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now,
	}
	var result Definition
	err = service.inTransaction(ctx, func(transactionContext context.Context) error {
		if err := service.registry.Create(transactionContext, definition); err != nil {
			return err
		}
		var completeErr error
		result, completeErr = service.complete(transactionContext, input, "schema.create", requestHash, definition, audit.Entry{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Action: "schema.create", Actor: principal.IdentityKey(), ResourceType: "SchemaDefinition", ResourceID: input.SchemaID, ResourceVersion: definition.Version, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, AfterHash: requestHash, CreatedAt: now})
		return completeErr
	})
	return result, err
}

func (service *Service) UpdateDraft(ctx context.Context, principal identity.Principal, input DraftInput, expectedRevision int64) (Definition, error) {
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID); err != nil {
		return Definition{}, err
	}
	if input.IdempotencyKey == "" {
		return Definition{}, ErrIdempotencyKeyRequired
	}
	normalized, err := NormalizeSemanticTypes(input.SemanticTypes)
	if err != nil {
		return Definition{}, err
	}
	if _, err := compileBSONSchema(input.SchemaID, input.JSONSchema); err != nil {
		return Definition{}, err
	}
	input.Revision = expectedRevision
	requestHash := draftHash(input, StatusDraft, normalized)
	if result, found, err := service.receipt(ctx, input, "schema.update", requestHash); err != nil || found {
		return result, err
	}
	existing, err := service.registry.Get(ctx, input.TenantID, input.SchemaID, input.Version)
	if err != nil {
		return Definition{}, err
	}
	definition := existing
	definition.Name = input.Name
	definition.JSONSchema = input.JSONSchema
	definition.SemanticTypes = normalized
	now := service.clock().UTC()
	var result Definition
	err = service.inTransaction(ctx, func(transactionContext context.Context) error {
		updated, updateErr := service.registry.UpdateDraft(transactionContext, definition, expectedRevision)
		if updateErr != nil {
			return updateErr
		}
		var completeErr error
		result, completeErr = service.complete(transactionContext, input, "schema.update", requestHash, updated, audit.Entry{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Action: "schema.update", Actor: principal.IdentityKey(), ResourceType: "SchemaDefinition", ResourceID: input.SchemaID, ResourceVersion: updated.Version, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, BeforeHash: hashDefinition(existing), AfterHash: requestHash, CreatedAt: now})
		return completeErr
	})
	return result, err
}

func (service *Service) Publish(ctx context.Context, principal identity.Principal, input DraftInput, expectedRevision int64) (Definition, error) {
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID); err != nil {
		return Definition{}, err
	}
	if input.IdempotencyKey == "" {
		return Definition{}, ErrIdempotencyKeyRequired
	}
	requestHash := publishHash(input, expectedRevision)
	if result, found, err := service.receipt(ctx, input, "schema.publish", requestHash); err != nil || found {
		return result, err
	}
	existing, err := service.registry.Get(ctx, input.TenantID, input.SchemaID, input.Version)
	if err != nil {
		return Definition{}, err
	}
	if existing.Status != StatusDraft {
		return Definition{}, ErrImmutable
	}
	if _, err := compileBSONSchema(existing.SchemaID, existing.JSONSchema); err != nil {
		return Definition{}, err
	}
	contentHash, err := schemaContentHash(existing)
	if err != nil {
		return Definition{}, err
	}
	now := service.clock().UTC()
	var result Definition
	err = service.inTransaction(ctx, func(transactionContext context.Context) error {
		published, publishErr := service.registry.PublishDefinition(transactionContext, input.TenantID, input.SchemaID, input.Version, expectedRevision, principal.IdentityKey(), contentHash)
		if publishErr != nil {
			return publishErr
		}
		var completeErr error
		result, completeErr = service.complete(transactionContext, input, "schema.publish", requestHash, published, audit.Entry{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Action: "schema.publish", Actor: principal.IdentityKey(), ResourceType: "SchemaDefinition", ResourceID: input.SchemaID, ResourceVersion: published.Version, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, BeforeHash: hashDefinition(existing), AfterHash: contentHash, CreatedAt: now})
		return completeErr
	})
	return result, err
}

func (service *Service) authorize(ctx context.Context, principal identity.Principal, tenantID, workspaceID string) error {
	if service == nil || service.registry == nil || service.authorizer == nil || service.receipts == nil || service.auditWriter == nil {
		return identity.ErrForbidden
	}
	_, err := service.authorizer.Authorize(ctx, principal, tenantID, workspaceID, identity.ActionSchemaManage)
	return err
}

func (service *Service) inTransaction(ctx context.Context, fn func(context.Context) error) error {
	if transactional, ok := service.registry.(TransactionalRegistry); ok {
		return transactional.WithTransaction(ctx, fn)
	}
	return fn(ctx)
}

func (service *Service) receipt(ctx context.Context, input DraftInput, operation, requestHash string) (Definition, bool, error) {
	receipt, err := service.receipts.Find(ctx, input.TenantID, input.WorkspaceID, operation, input.IdempotencyKey)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return Definition{}, false, nil
		}
		return Definition{}, false, err
	}
	if receipt.RequestHash != requestHash {
		return Definition{}, false, ErrIdempotencyConflict
	}
	return receipt.Definition, true, nil
}

func (service *Service) complete(ctx context.Context, input DraftInput, operation, requestHash string, definition Definition, entry audit.Entry) (Definition, error) {
	if err := service.auditWriter.Append(ctx, entry); err != nil {
		return Definition{}, fmt.Errorf("append schema audit: %w", err)
	}
	if err := service.receipts.Save(ctx, Receipt{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Operation: operation, IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Definition: definition}); err != nil {
		return Definition{}, fmt.Errorf("save schema receipt: %w", err)
	}
	return definition, nil
}

func compileBSONSchema(schemaID string, raw bson.Raw) (*Validator, error) {
	jsonSchema, err := bson.MarshalExtJSON(raw, false, false)
	if err != nil {
		return nil, fmt.Errorf("decode schema document: %w", err)
	}
	return CompileValidator(schemaID, jsonSchema)
}

func draftHash(input DraftInput, status Status, semanticTypes []string) string {
	value := []byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\x00%s", input.TenantID, input.SchemaID, input.Name, input.Version, input.Revision, status, hex.EncodeToString(input.JSONSchema), strings.Join(semanticTypes, "\x1f")))
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func publishHash(input DraftInput, expectedRevision int64) string {
	value := []byte(fmt.Sprintf("publish\x00%s\x00%s\x00%d\x00%d", input.TenantID, input.SchemaID, input.Version, expectedRevision))
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func hashDefinition(definition Definition) string {
	content, _ := bson.Marshal(definition)
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func schemaContentHash(definition Definition) (string, error) {
	jsonSchema, err := bson.MarshalExtJSON(definition.JSONSchema, false, false)
	if err != nil {
		return "", fmt.Errorf("decode schema document: %w", err)
	}
	return SchemaContentHash(jsonSchema, definition.SemanticTypes)
}
