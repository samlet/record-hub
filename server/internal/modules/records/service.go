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
	"github.com/samlet/record-hub/server/internal/observability"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type SchemaReader interface {
	Get(context.Context, string, string, int64) (schema.Definition, error)
}

type Service struct {
	workspaces  WorkspaceRepository
	tables      TableRepository
	schemas     SchemaReader
	authorizer  *identity.Authorizer
	clock       func() time.Time
	records     RecordRepository
	receipts    RecordReceiptStore
	audit       audit.Writer
	views       ViewRepository
	indexes     IndexRepository
	metrics     *observability.Registry
	queryBudget observability.QueryBudget
}

func NewService(workspaces WorkspaceRepository, tables TableRepository, schemas SchemaReader, authorizer *identity.Authorizer) *Service {
	return &Service{workspaces: workspaces, tables: tables, schemas: schemas, authorizer: authorizer, clock: time.Now, queryBudget: observability.DefaultQueryBudget()}
}

func NewRecordService(workspaces WorkspaceRepository, tables TableRepository, schemas SchemaReader, authorizer *identity.Authorizer, records RecordRepository, receipts RecordReceiptStore, auditWriter audit.Writer) *Service {
	service := NewService(workspaces, tables, schemas, authorizer)
	service.records = records
	service.receipts = receipts
	service.audit = auditWriter
	return service
}

func (service *Service) WithViewRepository(views ViewRepository) *Service {
	if service != nil {
		service.views = views
	}
	return service
}

func (service *Service) WithIndexRepository(indexes IndexRepository) *Service {
	if service != nil {
		service.indexes = indexes
	}
	return service
}

// WithMetrics attaches low-cardinality query cost telemetry. Tenant, table,
// record and field identifiers are intentionally never metric labels.
func (service *Service) WithMetrics(registry *observability.Registry) *Service {
	if service != nil {
		service.metrics = registry
	}
	return service
}

func (service *Service) WithQueryBudget(budget observability.QueryBudget) *Service {
	if service != nil {
		if budget.Validate() == nil {
			service.queryBudget = budget
		}
	}
	return service
}

var ErrQueryCostExceeded = observability.ErrQueryBudgetExceeded

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

type ViewInput struct {
	TenantID    string
	WorkspaceID string
	TableID     string
	ID          string
	Name        string
	Columns     []string
	Filters     []ViewFilter
	Sorts       []ViewSort
}

func (service *Service) CreateView(ctx context.Context, principal identity.Principal, input ViewInput) (ViewDefinition, error) {
	if err := service.viewDependencies(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionViewWrite); err != nil {
		return ViewDefinition{}, err
	}
	table, err := service.tables.GetTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return ViewDefinition{}, err
	}
	definition, err := service.publishedSchema(ctx, input.TenantID, table.SchemaID, table.SchemaVersion)
	if err != nil {
		return ViewDefinition{}, err
	}
	if err := validateViewSchemaFields(input.Columns, input.Filters, input.Sorts, definition); err != nil {
		return ViewDefinition{}, err
	}
	now := service.clock().UTC()
	view := ViewDefinition{ID: strings.TrimSpace(input.ID), TenantID: strings.TrimSpace(input.TenantID), WorkspaceID: strings.TrimSpace(input.WorkspaceID), TableID: strings.TrimSpace(input.TableID), Name: strings.TrimSpace(input.Name), Columns: append([]string(nil), input.Columns...), Filters: append([]ViewFilter(nil), input.Filters...), Sorts: append([]ViewSort(nil), input.Sorts...), Version: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := view.Validate(); err != nil {
		return ViewDefinition{}, err
	}
	if err := service.views.CreateView(ctx, view); err != nil {
		return ViewDefinition{}, err
	}
	return view, nil
}

func (service *Service) UpdateView(ctx context.Context, principal identity.Principal, input ViewInput, expectedVersion int64) (ViewDefinition, error) {
	if err := service.viewDependencies(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionViewWrite); err != nil {
		return ViewDefinition{}, err
	}
	if expectedVersion < 1 {
		return ViewDefinition{}, ErrViewVersionConflict
	}
	table, err := service.tables.GetTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return ViewDefinition{}, err
	}
	definition, err := service.publishedSchema(ctx, input.TenantID, table.SchemaID, table.SchemaVersion)
	if err != nil {
		return ViewDefinition{}, err
	}
	if err := validateViewSchemaFields(input.Columns, input.Filters, input.Sorts, definition); err != nil {
		return ViewDefinition{}, err
	}
	existing, err := service.views.GetView(ctx, input.TenantID, input.WorkspaceID, input.TableID, input.ID)
	if err != nil {
		return ViewDefinition{}, err
	}
	existing.Name = strings.TrimSpace(input.Name)
	existing.Columns = append([]string(nil), input.Columns...)
	existing.Filters = append([]ViewFilter(nil), input.Filters...)
	existing.Sorts = append([]ViewSort(nil), input.Sorts...)
	existing.UpdatedBy = principal.IdentityKey()
	existing.UpdatedAt = service.clock().UTC()
	if err := existing.Validate(); err != nil {
		return ViewDefinition{}, err
	}
	return service.views.UpdateView(ctx, existing, expectedVersion)
}

func (service *Service) ListViews(ctx context.Context, principal identity.Principal, tenantID, workspaceID, tableID string) ([]ViewDefinition, error) {
	if err := service.viewDependencies(ctx, principal, tenantID, workspaceID, identity.ActionViewRead); err != nil {
		return nil, err
	}
	return service.views.ListViews(ctx, tenantID, workspaceID, tableID)
}

func (service *Service) ListRecords(ctx context.Context, principal identity.Principal, tenantID, workspaceID, tableID, viewID, cursor string, limit int) (RecordPage, error) {
	started := time.Now()
	outcome := "error"
	rows := 0
	defer func() {
		if service == nil || service.metrics == nil {
			return
		}
		labels := observability.Labels{"operation": "records", "outcome": outcome}
		service.metrics.IncCounter("record_hub_record_queries_total", labels)
		service.metrics.ObserveDuration("record_hub_record_query_duration_seconds", time.Since(started), labels)
		service.metrics.SetGauge("record_hub_record_query_rows", float64(rows), labels)
		if outcome == "rejected" {
			service.metrics.IncCounter("record_hub_record_query_budget_exceeded_total", nil)
		}
	}()
	if err := service.recordDependencies(ctx, principal, tenantID, workspaceID, identity.ActionRecordRead); err != nil {
		return RecordPage{}, err
	}
	if service.views == nil {
		return RecordPage{}, identity.ErrForbidden
	}
	budget := service.queryBudget
	if err := budget.Validate(); err != nil {
		return RecordPage{}, err
	}
	if limit < 1 || limit > budget.MaxPageRows {
		outcome = "rejected"
		return RecordPage{}, fmt.Errorf("%w: page rows must be between 1 and %d", ErrQueryCostExceeded, budget.MaxPageRows)
	}
	var view ViewDefinition
	var err error
	if viewID != "" {
		view, err = service.views.GetView(ctx, tenantID, workspaceID, tableID, viewID)
		if err != nil {
			return RecordPage{}, err
		}
	} else {
		view = ViewDefinition{TenantID: tenantID, WorkspaceID: workspaceID, TableID: tableID, Sorts: []ViewSort{{Field: "id", Direction: SortAscending}}}
	}
	queryContext, cancel := context.WithTimeout(ctx, budget.MaxDuration)
	defer cancel()
	page, err := service.views.ListRecords(queryContext, tenantID, workspaceID, tableID, view, cursor, limit)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			outcome = "rejected"
			return RecordPage{}, fmt.Errorf("%w: execution exceeded %s", ErrQueryCostExceeded, budget.MaxDuration)
		}
		return RecordPage{}, err
	}
	encoded, err := bson.Marshal(page)
	if err != nil {
		return RecordPage{}, fmt.Errorf("measure record page cost: %w", err)
	}
	if int64(len(encoded)) > budget.MaxResponseBytes {
		outcome = "rejected"
		return RecordPage{}, fmt.Errorf("%w: response exceeds %d bytes", ErrQueryCostExceeded, budget.MaxResponseBytes)
	}
	rows = len(page.Items)
	outcome = "success"
	return page, nil
}

type IndexInput struct {
	TenantID    string
	WorkspaceID string
	TableID     string
	ID          string
	Field       string
	Direction   IndexDirection
}

func (service *Service) CreateIndex(ctx context.Context, principal identity.Principal, input IndexInput) (IndexDefinition, error) {
	if err := service.indexDependencies(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionWorkspaceManage); err != nil {
		return IndexDefinition{}, err
	}
	table, err := service.tables.GetTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return IndexDefinition{}, err
	}
	definition, err := service.publishedSchema(ctx, input.TenantID, table.SchemaID, table.SchemaVersion)
	if err != nil {
		return IndexDefinition{}, err
	}
	field := normalizeIndexField(input.Field)
	if err := validateIndexSchemaField(field, definition); err != nil {
		return IndexDefinition{}, err
	}
	current, err := service.indexes.ListIndexes(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return IndexDefinition{}, err
	}
	requestedKey := indexKey(field, input.Direction)
	for _, existing := range current {
		if indexKey(existing.Field, existing.Direction) == requestedKey {
			return IndexDefinition{}, ErrIndexExists
		}
	}
	if len(current) >= maxIndexesPerTable {
		return IndexDefinition{}, ErrIndexLimit
	}
	now := service.clock().UTC()
	index := IndexDefinition{ID: strings.TrimSpace(input.ID), TenantID: strings.TrimSpace(input.TenantID), WorkspaceID: strings.TrimSpace(input.WorkspaceID), TableID: strings.TrimSpace(input.TableID), Field: field, Direction: input.Direction, Name: physicalIndexName(field, input.Direction), Version: 1, CreatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := index.Validate(); err != nil {
		return IndexDefinition{}, err
	}
	if err := service.indexes.CreateIndex(ctx, index); err != nil {
		return IndexDefinition{}, err
	}
	return index, nil
}

func (service *Service) ListIndexes(ctx context.Context, principal identity.Principal, tenantID, workspaceID, tableID string) ([]IndexDefinition, error) {
	if err := service.indexDependencies(ctx, principal, tenantID, workspaceID, identity.ActionWorkspaceRead); err != nil {
		return nil, err
	}
	return service.indexes.ListIndexes(ctx, tenantID, workspaceID, tableID)
}

func (service *Service) indexDependencies(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) error {
	if service == nil || service.indexes == nil || service.tables == nil || service.schemas == nil {
		return identity.ErrForbidden
	}
	return service.authorize(ctx, principal, tenantID, workspaceID, action)
}

func validateIndexSchemaField(field string, definition schema.Definition) error {
	field = normalizeIndexField(field)
	if isIndexEnvelopeField(field) {
		return fmt.Errorf("%w: %q is a record envelope field", ErrIndexFieldNotAllowed, field)
	}
	var document map[string]interface{}
	if err := json.Unmarshal(mustExtJSON(definition.JSONSchema), &document); err != nil {
		return fmt.Errorf("%w: published schema is invalid", ErrIndexFieldNotAllowed)
	}
	properties, ok := document["properties"].(map[string]interface{})
	if !ok || len(properties) == 0 {
		return fmt.Errorf("%w: schema has no top-level properties", ErrIndexFieldNotAllowed)
	}
	if _, ok := properties[field]; !ok {
		return fmt.Errorf("%w: %q is absent from the published schema", ErrIndexFieldNotAllowed, field)
	}
	return nil
}

func (service *Service) viewDependencies(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) error {
	if service == nil || service.views == nil {
		return identity.ErrForbidden
	}
	return service.authorize(ctx, principal, tenantID, workspaceID, action)
}

func validateViewSchemaFields(columns []string, filters []ViewFilter, sorts []ViewSort, definition schema.Definition) error {
	allowed := map[string]struct{}{"id": {}, "recordVersion": {}, "schemaVersion": {}, "createdAt": {}, "updatedAt": {}, "tags": {}}
	jsonSchema := mustExtJSON(definition.JSONSchema)
	var document map[string]interface{}
	if err := json.Unmarshal(jsonSchema, &document); err == nil {
		if properties, ok := document["properties"].(map[string]interface{}); ok && len(properties) > 0 {
			allowed = map[string]struct{}{"id": {}, "recordVersion": {}, "schemaVersion": {}, "createdAt": {}, "updatedAt": {}, "tags": {}}
			for field := range properties {
				allowed[field] = struct{}{}
			}
		}
	}
	for _, field := range columns {
		if err := assertViewFieldAllowed(field, allowed); err != nil {
			return fmt.Errorf("view column: %w", err)
		}
	}
	for _, filter := range filters {
		if err := assertViewFieldAllowed(filter.Field, allowed); err != nil {
			return fmt.Errorf("view filter: %w", err)
		}
	}
	for _, sort := range sorts {
		if err := assertViewFieldAllowed(sort.Field, allowed); err != nil {
			return fmt.Errorf("view sort: %w", err)
		}
	}
	return nil
}

func assertViewFieldAllowed(field string, allowed map[string]struct{}) error {
	field = strings.TrimSpace(field)
	if _, ok := allowed[field]; ok {
		return nil
	}
	if strings.HasPrefix(field, "data.") {
		field = strings.TrimPrefix(field, "data.")
	}
	if _, ok := allowed[field]; !ok {
		return fmt.Errorf("field %q is not allowed by the published schema", field)
	}
	return nil
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
	table, err := service.writableTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return Record{}, err
	}
	if input.IdempotencyKey == "" {
		return Record{}, ErrIdempotencyKeyRequired
	}
	definition, err := service.publishedSchema(ctx, input.TenantID, table.SchemaID, table.SchemaVersion)
	if err != nil {
		return Record{}, err
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return Record{}, err
	}
	relations, err := normalizeRelations(input.Relations)
	if err != nil {
		return Record{}, err
	}
	input.Relations = relations
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
	record := Record{ID: strings.TrimSpace(input.ID), TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: input.TableID, SchemaID: table.SchemaID, SchemaVersion: table.SchemaVersion, RecordVersion: 1, Tags: tags, Data: input.Data, Relations: relations, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
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
	table, err := service.writableTable(ctx, input.TenantID, input.WorkspaceID, input.TableID)
	if err != nil {
		return Record{}, err
	}
	if input.IdempotencyKey == "" {
		return Record{}, ErrIdempotencyKeyRequired
	}
	if expectedVersion < 1 {
		return Record{}, ErrRecordVersionConflict
	}
	definition, err := service.publishedSchema(ctx, input.TenantID, table.SchemaID, table.SchemaVersion)
	if err != nil {
		return Record{}, err
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return Record{}, err
	}
	relations, err := normalizeRelations(input.Relations)
	if err != nil {
		return Record{}, err
	}
	input.Relations = relations
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
	updated.Relations = relations
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
	if _, err := service.writableTable(ctx, input.TenantID, input.WorkspaceID, input.TableID); err != nil {
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

func (service *Service) writableTable(ctx context.Context, tenantID, workspaceID, tableID string) (TableDefinition, error) {
	table, err := service.tables.GetTable(ctx, tenantID, workspaceID, tableID)
	if err != nil {
		return TableDefinition{}, err
	}
	if table.Kind == TableKindProjection {
		return TableDefinition{}, ErrProjectionReadOnly
	}
	return table, nil
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

func normalizeRelations(relations []RecordRelation) ([]RecordRelation, error) {
	seen := make(map[string]struct{}, len(relations))
	result := make([]RecordRelation, 0, len(relations))
	for _, relation := range relations {
		relation.Target.System = strings.TrimSpace(relation.Target.System)
		relation.Target.Type = strings.TrimSpace(relation.Target.Type)
		relation.Target.ID = strings.TrimSpace(relation.Target.ID)
		relation.RelationType = strings.TrimSpace(relation.RelationType)
		relation.ResolvedRecordID = strings.TrimSpace(relation.ResolvedRecordID)
		if relation.Status == "" {
			relation.Status = RelationCurrent
		}
		if relation.Status == RelationForbidden {
			relation.ResolvedRecordID = ""
		}
		if err := relation.Validate(); err != nil {
			return nil, err
		}
		key := relation.Target.System + "\x00" + relation.Target.Type + "\x00" + relation.Target.ID + "\x00" + relation.RelationType
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, relation)
	}
	if len(result) > 64 {
		return nil, errors.New("record cannot have more than 64 relations")
	}
	sort.Slice(result, func(left, right int) bool {
		return relationKey(result[left]) < relationKey(result[right])
	})
	return result, nil
}

func relationKey(relation RecordRelation) string {
	return relation.Target.System + "\x00" + relation.Target.Type + "\x00" + relation.Target.ID + "\x00" + relation.RelationType
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
