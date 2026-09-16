package records

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type SchemaReader interface {
	Get(context.Context, string, string, int64) (schema.Definition, error)
}

type Service struct {
	workspaces WorkspaceRepository
	tables     TableRepository
	schemas    SchemaReader
	authorizer *identity.Authorizer
	clock      func() time.Time
	records    RecordRepository
	receipts   RecordReceiptStore
	audit      audit.Writer
}

func NewService(workspaces WorkspaceRepository, tables TableRepository, schemas SchemaReader, authorizer *identity.Authorizer) *Service {
	return &Service{workspaces: workspaces, tables: tables, schemas: schemas, authorizer: authorizer, clock: time.Now}
}

func NewRecordService(workspaces WorkspaceRepository, tables TableRepository, schemas SchemaReader, authorizer *identity.Authorizer, records RecordRepository, receipts RecordReceiptStore, auditWriter audit.Writer) *Service {
	service := NewService(workspaces, tables, schemas, authorizer)
	service.records = records
	service.receipts = receipts
	service.audit = auditWriter
	return service
}

type WorkspaceInput struct {
	TenantID string
	ID       string
	Name     string
}

func (service *Service) CreateWorkspace(ctx context.Context, principal identity.Principal, input WorkspaceInput) (Workspace, error) {
	if err := requireUser(principal); err != nil {
		return Workspace{}, err
	}
	if service == nil || service.workspaces == nil || service.clock == nil {
		return Workspace{}, identity.ErrForbidden
	}
	workspace := Workspace{ID: strings.TrimSpace(input.ID), TenantID: strings.TrimSpace(input.TenantID), Name: strings.TrimSpace(input.Name), Version: 1, CreatedBy: principal.IdentityKey(), CreatedAt: service.clock().UTC(), UpdatedAt: service.clock().UTC()}
	if err := workspace.Validate(); err != nil {
		return Workspace{}, err
	}
	if err := service.workspaces.CreateWorkspace(ctx, workspace); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func (service *Service) ListWorkspaces(ctx context.Context, principal identity.Principal, tenantID string) ([]Workspace, error) {
	if err := requireUser(principal); err != nil {
		return nil, err
	}
	if service == nil || service.workspaces == nil || strings.TrimSpace(tenantID) == "" {
		return nil, identity.ErrForbidden
	}
	return service.workspaces.ListWorkspaces(ctx, tenantID, principal.IdentityKey())
}

type TableInput struct {
	TenantID      string
	WorkspaceID   string
	ID            string
	Name          string
	Kind          TableKind
	SchemaID      string
	SchemaVersion int64
	SourcePolicy  *SourcePolicy
}

func (service *Service) CreateTable(ctx context.Context, principal identity.Principal, input TableInput) (TableDefinition, error) {
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionWorkspaceManage); err != nil {
		return TableDefinition{}, err
	}
	if service.tables == nil || service.schemas == nil {
		return TableDefinition{}, identity.ErrForbidden
	}
	definition, err := service.schemas.Get(ctx, input.TenantID, input.SchemaID, input.SchemaVersion)
	if err != nil || definition.Status != schema.StatusPublished {
		return TableDefinition{}, ErrSchemaUnavailable
	}
	now := service.clock().UTC()
	table := TableDefinition{ID: strings.TrimSpace(input.ID), TenantID: strings.TrimSpace(input.TenantID), WorkspaceID: strings.TrimSpace(input.WorkspaceID), Name: strings.TrimSpace(input.Name), Kind: input.Kind, SchemaID: strings.TrimSpace(input.SchemaID), SchemaVersion: input.SchemaVersion, SourcePolicy: input.SourcePolicy, Version: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := table.Validate(); err != nil {
		return TableDefinition{}, err
	}
	if err := service.tables.CreateTable(ctx, table); err != nil {
		return TableDefinition{}, err
	}
	return table, nil
}

func (service *Service) ListTables(ctx context.Context, principal identity.Principal, tenantID, workspaceID string) ([]TableDefinition, error) {
	if err := service.authorize(ctx, principal, tenantID, workspaceID, identity.ActionWorkspaceRead); err != nil {
		return nil, err
	}
	if service.tables == nil {
		return nil, identity.ErrForbidden
	}
	return service.tables.ListTables(ctx, tenantID, workspaceID)
}

func (service *Service) GetTable(ctx context.Context, principal identity.Principal, tenantID, workspaceID, tableID string) (TableDefinition, error) {
	if err := service.authorize(ctx, principal, tenantID, workspaceID, identity.ActionWorkspaceRead); err != nil {
		return TableDefinition{}, err
	}
	if service.tables == nil {
		return TableDefinition{}, identity.ErrForbidden
	}
	return service.tables.GetTable(ctx, tenantID, workspaceID, tableID)
}

type RecordInput struct {
	TenantID       string
	WorkspaceID    string
	TableID        string
	ID             string
	Data           bson.Raw
	Tags           []string
	Relations      []RecordRelation
	RequestID      string
	IdempotencyKey string
}

type RecordDeleteInput struct {
	TenantID       string
	WorkspaceID    string
	TableID        string
	RecordID       string
	RequestID      string
	IdempotencyKey string
}

type TransactionalRecordRepository interface {
	RecordRepository
	WithTransaction(context.Context, func(context.Context) error) error
}

func (service *Service) CreateRecord(ctx context.Context, principal identity.Principal, input RecordInput) (Record, error) {
	if err := service.recordDependencies(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionRecordWrite); err != nil {
		return Record{}, err
	}
	if input.IdempotencyKey == "" {
		return Record{}, ErrIdempotencyKeyRequired
	}
	table, err := service.tables.GetTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return Record{}, err
	}
	if table.Kind == TableKindProjection {
		return Record{}, ErrProjectionReadOnly
	}
	definition, err := service.publishedSchema(ctx, input.TenantID, table.SchemaID, table.SchemaVersion)
	if err != nil {
		return Record{}, err
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return Record{}, err
	}
	if err := validateRecordData(definition, input.Data); err != nil {
		return Record{}, err
	}
	requestHash, err := recordRequestHash("record.create", input, 0, tags)
	if err != nil {
		return Record{}, err
	}
	if result, found, err := service.recordReceipt(ctx, input.TenantID, input.WorkspaceID, "record.create", input.IdempotencyKey, requestHash); err != nil || found {
		return result, err
	}
	now := service.clock().UTC()
	record := Record{ID: strings.TrimSpace(input.ID), TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: input.TableID, SchemaID: table.SchemaID, SchemaVersion: table.SchemaVersion, RecordVersion: 1, Tags: tags, Data: input.Data, Relations: input.Relations, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	var result Record
	err = service.recordTransaction(ctx, func(transactionContext context.Context) error {
		if err := service.records.CreateRecord(transactionContext, record); err != nil {
			return err
		}
		var completeErr error
		result, completeErr = service.completeRecord(transactionContext, input.TenantID, input.WorkspaceID, "record.create", input.IdempotencyKey, requestHash, record, audit.Entry{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Action: "record.create", Actor: principal.IdentityKey(), ResourceType: "Record", ResourceID: record.ID, ResourceVersion: record.RecordVersion, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, AfterHash: hashRecord(record), CreatedAt: now})
		return completeErr
	})
	return result, err
}

func (service *Service) GetRecord(ctx context.Context, principal identity.Principal, tenantID, workspaceID, recordID string) (Record, error) {
	if err := service.recordDependencies(ctx, principal, tenantID, workspaceID, identity.ActionRecordRead); err != nil {
		return Record{}, err
	}
	return service.records.GetRecord(ctx, tenantID, workspaceID, recordID)
}

func (service *Service) UpdateRecord(ctx context.Context, principal identity.Principal, input RecordInput, expectedVersion int64) (Record, error) {
	if err := service.recordDependencies(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionRecordWrite); err != nil {
		return Record{}, err
	}
	if input.IdempotencyKey == "" {
		return Record{}, ErrIdempotencyKeyRequired
	}
	if expectedVersion < 1 {
		return Record{}, ErrRecordVersionConflict
	}
	table, err := service.tables.GetTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return Record{}, err
	}
	if table.Kind == TableKindProjection {
		return Record{}, ErrProjectionReadOnly
	}
	definition, err := service.publishedSchema(ctx, input.TenantID, table.SchemaID, table.SchemaVersion)
	if err != nil {
		return Record{}, err
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return Record{}, err
	}
	if err := validateRecordData(definition, input.Data); err != nil {
		return Record{}, err
	}
	requestHash, err := recordRequestHash("record.update", input, expectedVersion, tags)
	if err != nil {
		return Record{}, err
	}
	if result, found, err := service.recordReceipt(ctx, input.TenantID, input.WorkspaceID, "record.update", input.IdempotencyKey, requestHash); err != nil || found {
		return result, err
	}
	existing, err := service.records.GetRecord(ctx, input.TenantID, input.WorkspaceID, input.ID)
	if err != nil {
		return Record{}, err
	}
	if existing.TableID != input.TableID {
		return Record{}, ErrRecordNotFound
	}
	updated := existing
	updated.Data = input.Data
	updated.Tags = tags
	updated.Relations = input.Relations
	updated.UpdatedBy = principal.IdentityKey()
	updated.UpdatedAt = service.clock().UTC()
	if err := updated.Validate(); err != nil {
		return Record{}, err
	}
	var result Record
	err = service.recordTransaction(ctx, func(transactionContext context.Context) error {
		updatedRecord, updateErr := service.records.UpdateRecord(transactionContext, updated, expectedVersion)
		if updateErr != nil {
			return updateErr
		}
		var completeErr error
		result, completeErr = service.completeRecord(transactionContext, input.TenantID, input.WorkspaceID, "record.update", input.IdempotencyKey, requestHash, updatedRecord, audit.Entry{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Action: "record.update", Actor: principal.IdentityKey(), ResourceType: "Record", ResourceID: updatedRecord.ID, ResourceVersion: updatedRecord.RecordVersion, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, BeforeHash: hashRecord(existing), AfterHash: hashRecord(updatedRecord), CreatedAt: updatedRecord.UpdatedAt})
		return completeErr
	})
	return result, err
}

func (service *Service) DeleteRecord(ctx context.Context, principal identity.Principal, input RecordDeleteInput, expectedVersion int64) (Record, error) {
	if err := service.recordDependencies(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionRecordWrite); err != nil {
		return Record{}, err
	}
	if input.IdempotencyKey == "" {
		return Record{}, ErrIdempotencyKeyRequired
	}
	if expectedVersion < 1 {
		return Record{}, ErrRecordVersionConflict
	}
	hashInput := RecordInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: input.TableID, ID: input.RecordID, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey}
	requestHash, err := recordRequestHash("record.delete", hashInput, expectedVersion, nil)
	if err != nil {
		return Record{}, err
	}
	if result, found, err := service.recordReceipt(ctx, input.TenantID, input.WorkspaceID, "record.delete", input.IdempotencyKey, requestHash); err != nil || found {
		return result, err
	}
	table, err := service.tables.GetTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return Record{}, err
	}
	if table.Kind == TableKindProjection {
		return Record{}, ErrProjectionReadOnly
	}
	existing, err := service.records.GetRecord(ctx, input.TenantID, input.WorkspaceID, input.RecordID)
	if err != nil {
		return Record{}, err
	}
	if existing.TableID != input.TableID {
		return Record{}, ErrRecordNotFound
	}
	var result Record
	err = service.recordTransaction(ctx, func(transactionContext context.Context) error {
		if deleteErr := service.records.DeleteRecord(transactionContext, input.TenantID, input.WorkspaceID, input.RecordID, expectedVersion); deleteErr != nil {
			return deleteErr
		}
		var completeErr error
		result, completeErr = service.completeRecord(transactionContext, input.TenantID, input.WorkspaceID, "record.delete", input.IdempotencyKey, requestHash, existing, audit.Entry{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Action: "record.delete", Actor: principal.IdentityKey(), ResourceType: "Record", ResourceID: existing.ID, ResourceVersion: existing.RecordVersion, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, BeforeHash: hashRecord(existing), CreatedAt: service.clock().UTC()})
		return completeErr
	})
	return result, err
}

func (service *Service) recordDependencies(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) error {
	if service == nil || service.records == nil || service.tables == nil || service.schemas == nil || service.receipts == nil || service.audit == nil {
		return identity.ErrForbidden
	}
	return service.authorize(ctx, principal, tenantID, workspaceID, action)
}

func (service *Service) publishedSchema(ctx context.Context, tenantID, schemaID string, version int64) (schema.Definition, error) {
	definition, err := service.schemas.Get(ctx, tenantID, schemaID, version)
	if err != nil || definition.Status != schema.StatusPublished || definition.TenantID != tenantID || definition.SchemaID != schemaID || definition.Version != version {
		return schema.Definition{}, ErrSchemaUnavailable
	}
	return definition, nil
}

func (service *Service) recordTransaction(ctx context.Context, fn func(context.Context) error) error {
	if transactional, ok := service.records.(TransactionalRecordRepository); ok {
		return transactional.WithTransaction(ctx, fn)
	}
	return fn(ctx)
}

func (service *Service) recordReceipt(ctx context.Context, tenantID, workspaceID, operation, key, requestHash string) (Record, bool, error) {
	receipt, err := service.receipts.Find(ctx, tenantID, workspaceID, operation, key)
	if err != nil {
		if errors.Is(err, ErrReceiptNotFound) {
			return Record{}, false, nil
		}
		return Record{}, false, err
	}
	if receipt.RequestHash != requestHash {
		return Record{}, false, ErrIdempotencyConflict
	}
	return receipt.Record, true, nil
}

func (service *Service) completeRecord(ctx context.Context, tenantID, workspaceID, operation, key, requestHash string, record Record, entry audit.Entry) (Record, error) {
	if err := service.audit.Append(ctx, entry); err != nil {
		return Record{}, fmt.Errorf("append record audit: %w", err)
	}
	if err := service.receipts.Save(ctx, RecordReceipt{TenantID: tenantID, WorkspaceID: workspaceID, Operation: operation, IdempotencyKey: key, RequestHash: requestHash, Record: record}); err != nil {
		return Record{}, fmt.Errorf("save record receipt: %w", err)
	}
	return record, nil
}

func normalizeTags(tags []string) ([]string, error) {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return nil, errors.New("record tags cannot be empty")
		}
		if len(tag) > 64 {
			return nil, errors.New("record tags cannot exceed 64 characters")
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	if len(result) > 64 {
		return nil, errors.New("record cannot have more than 64 tags")
	}
	sort.Strings(result)
	return result, nil
}

func validateRecordData(definition schema.Definition, data bson.Raw) error {
	jsonData, err := bson.MarshalExtJSON(data, false, false)
	if err != nil {
		return fmt.Errorf("record data must be an object: %w", err)
	}
	if _, err := schema.CanonicalizeJSON(jsonData); err != nil {
		return err
	}
	validator, err := schema.CompileValidator(definition.SchemaID, mustExtJSON(definition.JSONSchema))
	if err != nil {
		return err
	}
	if err := validator.ValidateJSON(jsonData); err != nil {
		return err
	}
	return nil
}

func mustExtJSON(raw bson.Raw) []byte {
	value, _ := bson.MarshalExtJSON(raw, false, false)
	return value
}

func recordRequestHash(operation string, input RecordInput, expectedVersion int64, tags []string) (string, error) {
	data := []byte("null")
	if len(input.Data) > 0 {
		var err error
		data, err = bson.MarshalExtJSON(input.Data, false, false)
		if err != nil {
			return "", err
		}
	}
	canonical, err := schema.CanonicalizeJSON(data)
	if err != nil {
		return "", err
	}
	relations, err := json.Marshal(input.Relations)
	if err != nil {
		return "", err
	}
	value := []byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%s", operation, input.TenantID, input.WorkspaceID, input.TableID, input.ID, expectedVersion, hex.EncodeToString(canonical), strings.Join(tags, "\x1f"), hex.EncodeToString(relations)))
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func hashRecord(record Record) string {
	data, _ := bson.Marshal(record)
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (service *Service) authorize(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) error {
	if service == nil || service.authorizer == nil || service.tables == nil || service.clock == nil {
		return identity.ErrForbidden
	}
	_, err := service.authorizer.Authorize(ctx, principal, tenantID, workspaceID, action)
	return err
}

func requireUser(principal identity.Principal) error {
	if principal.Kind != identity.PrincipalUser || principal.Issuer == "" || principal.Subject == "" {
		return errors.New("authenticated user is required")
	}
	return nil
}
