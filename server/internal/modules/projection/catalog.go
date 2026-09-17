package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The catalog is the control plane for externally supplied event mappings.
// It intentionally lives beside the projection registry, while generation
// switching remains a separate concern owned by the projector.
type CatalogStatus string

const (
	CatalogStatusDraft     CatalogStatus = "DRAFT"
	CatalogStatusPublished CatalogStatus = "PUBLISHED"
	CatalogStatusRevoked   CatalogStatus = "REVOKED"
)

type TenantResolutionMode string

const (
	TenantResolutionMetadata  TenantResolutionMode = "metadata"
	TenantResolutionAllowlist TenantResolutionMode = "allowlist"
)

var (
	ErrCatalogUnavailable         = errors.New("projection catalog is unavailable")
	ErrCatalogInvalid             = errors.New("invalid projection catalog registration")
	ErrSourceExists               = errors.New("source registration already exists")
	ErrSourceNotFound             = errors.New("source registration not found")
	ErrSourceImmutable            = errors.New("published source registration is immutable")
	ErrSourceRevisionConflict     = errors.New("source registration revision conflict")
	ErrMappingExists              = errors.New("mapping registration already exists")
	ErrMappingNotFound            = errors.New("mapping registration not found")
	ErrMappingImmutable           = errors.New("published mapping registration is immutable")
	ErrMappingRevisionConflict    = errors.New("mapping registration revision conflict")
	ErrSourceNotPublished         = errors.New("source registration is not published")
	ErrTargetSchemaNotPublished   = errors.New("target schema is not published")
	ErrTargetFieldNotAllowed      = errors.New("mapping target field is not declared by the target schema")
	ErrCanonicalHashMismatch      = errors.New("mapping canonical hash mismatch")
	ErrFixtureNotFound            = errors.New("mapping fixture document not found")
	ErrFixtureHashMismatch        = errors.New("mapping fixture hash mismatch")
	ErrFixtureInvalid             = errors.New("mapping fixture is invalid")
	ErrIdempotencyKeyMissing      = errors.New("idempotency key is required")
	ErrRequestIDMissing           = errors.New("request ID is required")
	ErrCatalogIdempotencyConflict = errors.New("idempotency key was used with different input")
	ErrCatalogReceiptNotFound     = errors.New("catalog idempotency receipt not found")
)

const (
	maxCatalogID           = 128
	maxOwnerContact        = 256
	maxMappingFields       = 128
	maxAllowlist           = 128
	maxFixtureRef          = 512
	maxPathLength          = 256
	maxListLimit     int64 = 100
)

var (
	catalogIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,127}$`)
	pathPattern       = regexp.MustCompile(`^(?:/|payload\.)[A-Za-z0-9_./~-]{1,255}$`)
	targetPathPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
	sha256Pattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type TenantResolutionRule struct {
	TenantID    string `bson:"tenantId" json:"tenantId"`
	WorkspaceID string `bson:"workspaceId" json:"workspaceId"`
}

type TenantResolution struct {
	Mode      TenantResolutionMode   `bson:"mode" json:"mode"`
	Field     string                 `bson:"field,omitempty" json:"field,omitempty"`
	Allowlist []TenantResolutionRule `bson:"allowlist,omitempty" json:"allowlist,omitempty"`
}

type SourceRegistration struct {
	ID               string               `bson:"sourceId" json:"sourceId"`
	TenantID         string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID      string               `bson:"workspaceId" json:"workspaceId"`
	EventType        string               `bson:"eventType" json:"eventType"`
	EventVersion     int64                `bson:"eventVersion" json:"eventVersion"`
	OwnerContact     string               `bson:"ownerContact" json:"ownerContact"`
	TenantResolution TenantResolution     `bson:"tenantResolution" json:"tenantResolution"`
	Status           CatalogStatus        `bson:"status" json:"status"`
	Revision         int64                `bson:"revision" json:"revision"`
	CreatedBy        identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	UpdatedBy        identity.IdentityKey `bson:"updatedBy" json:"updatedBy"`
	CreatedAt        time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt        time.Time            `bson:"updatedAt" json:"updatedAt"`
	PublishedAt      *time.Time           `bson:"publishedAt,omitempty" json:"publishedAt,omitempty"`
}

func (source SourceRegistration) Validate() error {
	if !catalogIDPattern.MatchString(strings.TrimSpace(source.ID)) || !nonBlankBounded(source.TenantID, maxCatalogID) || !nonBlankBounded(source.WorkspaceID, maxCatalogID) || !eventTypePattern.MatchString(strings.TrimSpace(source.EventType)) || source.EventVersion < 1 || source.EventVersion > 1_000_000 || !nonBlankBounded(source.OwnerContact, maxOwnerContact) {
		return ErrCatalogInvalid
	}
	if source.Revision < 1 || source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() || source.CreatedBy.Issuer == "" || source.CreatedBy.Subject == "" || source.UpdatedBy.Issuer == "" || source.UpdatedBy.Subject == "" {
		return ErrCatalogInvalid
	}
	switch source.Status {
	case CatalogStatusDraft:
		if source.PublishedAt != nil {
			return ErrCatalogInvalid
		}
	case CatalogStatusPublished, CatalogStatusRevoked:
		if source.PublishedAt == nil {
			return ErrCatalogInvalid
		}
	default:
		return ErrCatalogInvalid
	}
	return source.TenantResolution.Validate()
}

func (resolution TenantResolution) Validate() error {
	if resolution.Mode != TenantResolutionMetadata && resolution.Mode != TenantResolutionAllowlist {
		return ErrCatalogInvalid
	}
	switch resolution.Mode {
	case TenantResolutionMetadata:
		if resolution.Field != "workspaceId" || len(resolution.Allowlist) != 0 {
			return ErrCatalogInvalid
		}
	case TenantResolutionAllowlist:
		if resolution.Field != "" || len(resolution.Allowlist) == 0 || len(resolution.Allowlist) > maxAllowlist {
			return ErrCatalogInvalid
		}
		seen := make(map[string]struct{}, len(resolution.Allowlist))
		for _, rule := range resolution.Allowlist {
			if !nonBlankBounded(rule.TenantID, maxCatalogID) || !nonBlankBounded(rule.WorkspaceID, maxCatalogID) {
				return ErrCatalogInvalid
			}
			key := strings.TrimSpace(rule.TenantID) + "\x00" + strings.TrimSpace(rule.WorkspaceID)
			if _, exists := seen[key]; exists {
				return ErrCatalogInvalid
			}
			seen[key] = struct{}{}
		}
	}
	return nil
}

type MappingFixture struct {
	EventRef string `bson:"eventRef" json:"eventRef"`
	SHA256   string `bson:"sha256" json:"sha256"`
}

type MappingRegistration struct {
	ID                  string               `bson:"mappingId" json:"mappingId"`
	TenantID            string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID         string               `bson:"workspaceId" json:"workspaceId"`
	SourceID            string               `bson:"sourceId" json:"sourceId"`
	EventType           string               `bson:"eventType" json:"eventType"`
	EventVersion        int64                `bson:"eventVersion" json:"eventVersion"`
	TargetTableID       string               `bson:"targetTableId" json:"targetTableId"`
	TargetSchemaID      string               `bson:"targetSchemaId" json:"targetSchemaId"`
	TargetSchemaVersion int64                `bson:"targetSchemaVersion" json:"targetSchemaVersion"`
	FieldMap            map[string]string    `bson:"fieldMap" json:"fieldMap"`
	Fixture             MappingFixture       `bson:"fixture" json:"fixture"`
	CanonicalHash       string               `bson:"canonicalHash" json:"canonicalHash"`
	Status              CatalogStatus        `bson:"status" json:"status"`
	Revision            int64                `bson:"revision" json:"revision"`
	CreatedBy           identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	UpdatedBy           identity.IdentityKey `bson:"updatedBy" json:"updatedBy"`
	CreatedAt           time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt           time.Time            `bson:"updatedAt" json:"updatedAt"`
	PublishedAt         *time.Time           `bson:"publishedAt,omitempty" json:"publishedAt,omitempty"`
}

func (mapping MappingRegistration) Validate() error {
	if !catalogIDPattern.MatchString(strings.TrimSpace(mapping.ID)) || !nonBlankBounded(mapping.TenantID, maxCatalogID) || !nonBlankBounded(mapping.WorkspaceID, maxCatalogID) || !catalogIDPattern.MatchString(strings.TrimSpace(mapping.SourceID)) || !eventTypePattern.MatchString(strings.TrimSpace(mapping.EventType)) || mapping.EventVersion < 1 || mapping.EventVersion > 1_000_000 || !nonBlankBounded(mapping.TargetTableID, maxCatalogID) || !nonBlankBounded(mapping.TargetSchemaID, 256) || mapping.TargetSchemaVersion < 1 {
		return ErrCatalogInvalid
	}
	if mapping.Revision < 1 || mapping.CreatedAt.IsZero() || mapping.UpdatedAt.IsZero() || mapping.CreatedBy.Issuer == "" || mapping.CreatedBy.Subject == "" || mapping.UpdatedBy.Issuer == "" || mapping.UpdatedBy.Subject == "" {
		return ErrCatalogInvalid
	}
	if len(mapping.FieldMap) == 0 || len(mapping.FieldMap) > maxMappingFields {
		return ErrCatalogInvalid
	}
	for target, source := range mapping.FieldMap {
		if !targetPathPattern.MatchString(target) || len(target) > maxPathLength || !pathPattern.MatchString(source) || len(source) > maxPathLength {
			return ErrCatalogInvalid
		}
	}
	if !nonBlankBounded(mapping.Fixture.EventRef, maxFixtureRef) || !sha256Pattern.MatchString(mapping.Fixture.SHA256) || !sha256Pattern.MatchString(mapping.CanonicalHash) {
		return ErrCatalogInvalid
	}
	switch mapping.Status {
	case CatalogStatusDraft:
		if mapping.PublishedAt != nil {
			return ErrCatalogInvalid
		}
	case CatalogStatusPublished, CatalogStatusRevoked:
		if mapping.PublishedAt == nil {
			return ErrCatalogInvalid
		}
	default:
		return ErrCatalogInvalid
	}
	return nil
}

func (mapping MappingRegistration) ComputeCanonicalHash() (string, error) {
	fieldMap := make(map[string]string, len(mapping.FieldMap))
	for key, value := range mapping.FieldMap {
		fieldMap[key] = value
	}
	content := map[string]interface{}{
		"tenantId": mapping.TenantID, "workspaceId": mapping.WorkspaceID, "sourceId": mapping.SourceID,
		"eventType": mapping.EventType, "eventVersion": mapping.EventVersion, "targetTableId": mapping.TargetTableID,
		"targetSchemaId": mapping.TargetSchemaID, "targetSchemaVersion": mapping.TargetSchemaVersion,
		"fieldMap": fieldMap, "fixture": map[string]interface{}{"eventRef": mapping.Fixture.EventRef, "sha256": mapping.Fixture.SHA256},
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("encode mapping canonical content: %w", err)
	}
	canonical, err := schema.CanonicalizeJSON(raw)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func nonBlankBounded(value string, max int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= max && !strings.ContainsAny(trimmed, "\t\r\n")
}

type SourceStore interface {
	CreateSource(context.Context, SourceRegistration) error
	FindSource(context.Context, string, string, string) (SourceRegistration, error)
	ListSources(context.Context, string, string, int64) ([]SourceRegistration, error)
	TransitionSource(context.Context, string, string, string, CatalogStatus, identity.IdentityKey, *time.Time, int64) (SourceRegistration, error)
}

type MappingStore interface {
	CreateMapping(context.Context, MappingRegistration) error
	FindMapping(context.Context, string, string, string) (MappingRegistration, error)
	ListMappings(context.Context, string, string, string, int64) ([]MappingRegistration, error)
	TransitionMapping(context.Context, string, string, string, CatalogStatus, identity.IdentityKey, *time.Time, int64) (MappingRegistration, error)
}

type MappingFixtureStore interface {
	PutFixture(context.Context, MappingFixtureDocument) error
	GetFixture(context.Context, string, string, string, string) (MappingFixtureDocument, error)
}

type CatalogSchemaReader interface {
	Get(context.Context, string, string, int64) (schema.Definition, error)
}

type CatalogTransactional interface {
	WithTransaction(context.Context, func(context.Context) error) error
}

type CatalogReceipt struct {
	TenantID       string               `bson:"tenantId"`
	WorkspaceID    string               `bson:"workspaceId"`
	Operation      string               `bson:"operation"`
	IdempotencyKey string               `bson:"idempotencyKey"`
	RequestHash    string               `bson:"requestHash"`
	Source         *SourceRegistration  `bson:"source,omitempty"`
	Mapping        *MappingRegistration `bson:"mapping,omitempty"`
}

type CatalogReceiptStore interface {
	Find(context.Context, string, string, string, string) (CatalogReceipt, error)
	Save(context.Context, CatalogReceipt) error
}

type SourceCreateInput struct {
	TenantID         string
	WorkspaceID      string
	SourceID         string
	EventType        string
	EventVersion     int64
	OwnerContact     string
	TenantResolution TenantResolution
	RequestID        string
	IdempotencyKey   string
}

type CatalogTransitionInput struct {
	TenantID         string
	WorkspaceID      string
	ID               string
	ExpectedRevision int64
	RequestID        string
	IdempotencyKey   string
}

type MappingCreateInput struct {
	TenantID            string
	WorkspaceID         string
	MappingID           string
	SourceID            string
	EventType           string
	EventVersion        int64
	TargetTableID       string
	TargetSchemaID      string
	TargetSchemaVersion int64
	FieldMap            map[string]string
	Fixture             MappingFixture
	FixtureDocument     []byte
	RequestID           string
	IdempotencyKey      string
}

type CatalogService struct {
	sources     SourceStore
	mappings    MappingStore
	fixtures    MappingFixtureStore
	schemas     CatalogSchemaReader
	authorizer  *identity.Authorizer
	receipts    CatalogReceiptStore
	auditWriter audit.Writer
	clock       func() time.Time
}

func NewCatalogService(sources SourceStore, mappings MappingStore, schemas CatalogSchemaReader, authorizer *identity.Authorizer, receipts CatalogReceiptStore, auditWriter audit.Writer, fixtureStores ...MappingFixtureStore) *CatalogService {
	var fixtures MappingFixtureStore
	if len(fixtureStores) > 0 {
		fixtures = fixtureStores[0]
	} else if inferred, ok := sources.(MappingFixtureStore); ok {
		fixtures = inferred
	}
	return &CatalogService{sources: sources, mappings: mappings, fixtures: fixtures, schemas: schemas, authorizer: authorizer, receipts: receipts, auditWriter: auditWriter, clock: time.Now}
}

func (service *CatalogService) CreateSource(ctx context.Context, principal identity.Principal, input SourceCreateInput) (SourceRegistration, bool, error) {
	if err := service.ready(); err != nil {
		return SourceRegistration{}, false, err
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return SourceRegistration{}, false, ErrIdempotencyKeyMissing
	}
	if strings.TrimSpace(input.RequestID) == "" {
		return SourceRegistration{}, false, ErrRequestIDMissing
	}
	if len(strings.TrimSpace(input.IdempotencyKey)) > 256 || len(strings.TrimSpace(input.RequestID)) > 256 {
		return SourceRegistration{}, false, ErrCatalogInvalid
	}
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionSchemaManage); err != nil {
		return SourceRegistration{}, false, err
	}
	now := service.clock().UTC()
	source := SourceRegistration{ID: strings.TrimSpace(input.SourceID), TenantID: strings.TrimSpace(input.TenantID), WorkspaceID: strings.TrimSpace(input.WorkspaceID), EventType: strings.TrimSpace(input.EventType), EventVersion: input.EventVersion, OwnerContact: strings.TrimSpace(input.OwnerContact), TenantResolution: input.TenantResolution, Status: CatalogStatusDraft, Revision: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := source.Validate(); err != nil {
		return SourceRegistration{}, false, err
	}
	if source.TenantResolution.Mode == TenantResolutionAllowlist {
		for _, rule := range source.TenantResolution.Allowlist {
			if _, err := service.authorizer.Authorize(ctx, principal, rule.TenantID, rule.WorkspaceID, identity.ActionSchemaManage); err != nil {
				return SourceRegistration{}, false, err
			}
		}
	}
	requestHash := hashCatalogInput("source.create", map[string]interface{}{
		"tenantId": source.TenantID, "workspaceId": source.WorkspaceID, "sourceId": source.ID,
		"eventType": source.EventType, "eventVersion": source.EventVersion, "ownerContact": source.OwnerContact,
		"tenantResolution": source.TenantResolution,
	})
	if result, found, err := service.sourceReceipt(ctx, input.TenantID, input.WorkspaceID, "source.create", input.IdempotencyKey, requestHash); err != nil || found {
		return result, found, err
	}
	var result SourceRegistration
	err := service.inTransaction(ctx, func(tx context.Context) error {
		if err := service.sources.CreateSource(tx, source); err != nil {
			return err
		}
		result = source
		if err := service.auditWriter.Append(tx, audit.Entry{TenantID: source.TenantID, WorkspaceID: source.WorkspaceID, Action: "source.create", Actor: principal.IdentityKey(), ResourceType: "SourceRegistration", ResourceID: source.ID, ResourceVersion: source.Revision, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, AfterHash: requestHash, CreatedAt: now}); err != nil {
			return err
		}
		return service.receipts.Save(tx, CatalogReceipt{TenantID: source.TenantID, WorkspaceID: source.WorkspaceID, Operation: "source.create", IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Source: &source})
	})
	return result, false, err
}

func (service *CatalogService) ListSources(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, limit int64) ([]SourceRegistration, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > maxListLimit {
		return nil, ErrCatalogInvalid
	}
	if err := service.authorize(ctx, principal, tenantID, workspaceID, identity.ActionSchemaRead); err != nil {
		return nil, err
	}
	return service.sources.ListSources(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), limit)
}

func (service *CatalogService) PublishSource(ctx context.Context, principal identity.Principal, input CatalogTransitionInput) (SourceRegistration, bool, error) {
	return service.transitionSource(ctx, principal, input, CatalogStatusPublished)
}

func (service *CatalogService) transitionSource(ctx context.Context, principal identity.Principal, input CatalogTransitionInput, status CatalogStatus) (SourceRegistration, bool, error) {
	if err := service.ready(); err != nil {
		return SourceRegistration{}, false, err
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return SourceRegistration{}, false, ErrIdempotencyKeyMissing
	}
	if strings.TrimSpace(input.RequestID) == "" {
		return SourceRegistration{}, false, ErrRequestIDMissing
	}
	if len(strings.TrimSpace(input.IdempotencyKey)) > 256 || len(strings.TrimSpace(input.RequestID)) > 256 {
		return SourceRegistration{}, false, ErrCatalogInvalid
	}
	if input.ExpectedRevision < 1 {
		return SourceRegistration{}, false, ErrSourceRevisionConflict
	}
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionSchemaManage); err != nil {
		return SourceRegistration{}, false, err
	}
	source, err := service.sources.FindSource(ctx, input.TenantID, input.WorkspaceID, input.ID)
	if err != nil {
		return SourceRegistration{}, false, err
	}
	requestHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(fmt.Sprintf("source.%s\x00%s\x00%d", strings.ToLower(string(status)), source.ID, input.ExpectedRevision))))
	if result, found, err := service.sourceReceipt(ctx, input.TenantID, input.WorkspaceID, "source."+strings.ToLower(string(status)), input.IdempotencyKey, requestHash); err != nil || found {
		return result, found, err
	}
	if source.Status != CatalogStatusDraft {
		return SourceRegistration{}, false, ErrSourceImmutable
	}
	now := service.clock().UTC()
	var updated SourceRegistration
	err = service.inTransaction(ctx, func(tx context.Context) error {
		var transitionErr error
		updated, transitionErr = service.sources.TransitionSource(tx, source.TenantID, source.WorkspaceID, source.ID, status, principal.IdentityKey(), &now, input.ExpectedRevision)
		if transitionErr != nil {
			return transitionErr
		}
		if err := service.auditWriter.Append(tx, audit.Entry{TenantID: source.TenantID, WorkspaceID: source.WorkspaceID, Action: "source." + strings.ToLower(string(status)), Actor: principal.IdentityKey(), ResourceType: "SourceRegistration", ResourceID: source.ID, ResourceVersion: updated.Revision, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, BeforeHash: hashCatalogInput("source.before", source), AfterHash: requestHash, CreatedAt: now}); err != nil {
			return err
		}
		return service.receipts.Save(tx, CatalogReceipt{TenantID: source.TenantID, WorkspaceID: source.WorkspaceID, Operation: "source." + strings.ToLower(string(status)), IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Source: &updated})
	})
	return updated, false, err
}

func (service *CatalogService) CreateMapping(ctx context.Context, principal identity.Principal, input MappingCreateInput) (MappingRegistration, bool, error) {
	if err := service.ready(); err != nil {
		return MappingRegistration{}, false, err
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return MappingRegistration{}, false, ErrIdempotencyKeyMissing
	}
	if strings.TrimSpace(input.RequestID) == "" {
		return MappingRegistration{}, false, ErrRequestIDMissing
	}
	if len(strings.TrimSpace(input.IdempotencyKey)) > 256 || len(strings.TrimSpace(input.RequestID)) > 256 {
		return MappingRegistration{}, false, ErrCatalogInvalid
	}
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionSchemaManage); err != nil {
		return MappingRegistration{}, false, err
	}
	source, err := service.sources.FindSource(ctx, input.TenantID, input.WorkspaceID, input.SourceID)
	if err != nil {
		return MappingRegistration{}, false, err
	}
	if source.EventType != strings.TrimSpace(input.EventType) || source.EventVersion != input.EventVersion {
		return MappingRegistration{}, false, ErrCatalogInvalid
	}
	if service.schemas == nil {
		return MappingRegistration{}, false, ErrCatalogUnavailable
	}
	if _, err := service.schemas.Get(ctx, input.TenantID, input.TargetSchemaID, input.TargetSchemaVersion); err != nil {
		return MappingRegistration{}, false, err
	}
	now := service.clock().UTC()
	fixtureMetadata := MappingFixture{EventRef: strings.TrimSpace(input.Fixture.EventRef), SHA256: strings.TrimSpace(input.Fixture.SHA256)}
	fixtureDocument, err := prepareMappingFixtureDocument(input.TenantID, input.WorkspaceID, fixtureMetadata, input.FixtureDocument, principal.IdentityKey(), now)
	if err != nil {
		return MappingRegistration{}, false, err
	}
	mapping := MappingRegistration{ID: strings.TrimSpace(input.MappingID), TenantID: strings.TrimSpace(input.TenantID), WorkspaceID: strings.TrimSpace(input.WorkspaceID), SourceID: strings.TrimSpace(input.SourceID), EventType: strings.TrimSpace(input.EventType), EventVersion: input.EventVersion, TargetTableID: strings.TrimSpace(input.TargetTableID), TargetSchemaID: strings.TrimSpace(input.TargetSchemaID), TargetSchemaVersion: input.TargetSchemaVersion, FieldMap: input.FieldMap, Fixture: fixtureMetadata, Status: CatalogStatusDraft, Revision: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	hash, err := mapping.ComputeCanonicalHash()
	if err != nil {
		return MappingRegistration{}, false, err
	}
	mapping.CanonicalHash = hash
	if err := mapping.Validate(); err != nil {
		return MappingRegistration{}, false, err
	}
	requestHash := hashCatalogInput("mapping.create", map[string]interface{}{
		"tenantId": mapping.TenantID, "workspaceId": mapping.WorkspaceID, "mappingId": mapping.ID,
		"sourceId": mapping.SourceID, "eventType": mapping.EventType, "eventVersion": mapping.EventVersion,
		"targetTableId": mapping.TargetTableID, "targetSchemaId": mapping.TargetSchemaID,
		"targetSchemaVersion": mapping.TargetSchemaVersion, "fieldMap": mapping.FieldMap, "fixture": mapping.Fixture,
	})
	if result, found, err := service.mappingReceipt(ctx, input.TenantID, input.WorkspaceID, "mapping.create", input.IdempotencyKey, requestHash); err != nil || found {
		return result, found, err
	}
	var result MappingRegistration
	err = service.inTransaction(ctx, func(tx context.Context) error {
		if err := service.fixtures.PutFixture(tx, fixtureDocument); err != nil {
			return err
		}
		if err := service.mappings.CreateMapping(tx, mapping); err != nil {
			return err
		}
		result = mapping
		if err := service.auditWriter.Append(tx, audit.Entry{TenantID: mapping.TenantID, WorkspaceID: mapping.WorkspaceID, Action: "mapping.create", Actor: principal.IdentityKey(), ResourceType: "MappingRegistration", ResourceID: mapping.ID, ResourceVersion: mapping.Revision, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, AfterHash: requestHash, CreatedAt: now}); err != nil {
			return err
		}
		return service.receipts.Save(tx, CatalogReceipt{TenantID: mapping.TenantID, WorkspaceID: mapping.WorkspaceID, Operation: "mapping.create", IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Mapping: &mapping})
	})
	return result, false, err
}

func (service *CatalogService) ListMappings(ctx context.Context, principal identity.Principal, tenantID, workspaceID, sourceID string, limit int64) ([]MappingRegistration, error) {
	if err := service.ready(); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > maxListLimit {
		return nil, ErrCatalogInvalid
	}
	if err := service.authorize(ctx, principal, tenantID, workspaceID, identity.ActionSchemaRead); err != nil {
		return nil, err
	}
	return service.mappings.ListMappings(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), strings.TrimSpace(sourceID), limit)
}

func (service *CatalogService) PublishMapping(ctx context.Context, principal identity.Principal, input CatalogTransitionInput) (MappingRegistration, bool, error) {
	return service.transitionMapping(ctx, principal, input, CatalogStatusPublished)
}

func (service *CatalogService) RevokeMapping(ctx context.Context, principal identity.Principal, input CatalogTransitionInput) (MappingRegistration, bool, error) {
	return service.transitionMapping(ctx, principal, input, CatalogStatusRevoked)
}

func (service *CatalogService) transitionMapping(ctx context.Context, principal identity.Principal, input CatalogTransitionInput, status CatalogStatus) (MappingRegistration, bool, error) {
	if err := service.ready(); err != nil {
		return MappingRegistration{}, false, err
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return MappingRegistration{}, false, ErrIdempotencyKeyMissing
	}
	if strings.TrimSpace(input.RequestID) == "" {
		return MappingRegistration{}, false, ErrRequestIDMissing
	}
	if len(strings.TrimSpace(input.IdempotencyKey)) > 256 || len(strings.TrimSpace(input.RequestID)) > 256 {
		return MappingRegistration{}, false, ErrCatalogInvalid
	}
	if input.ExpectedRevision < 1 {
		return MappingRegistration{}, false, ErrMappingRevisionConflict
	}
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionSchemaManage); err != nil {
		return MappingRegistration{}, false, err
	}
	mapping, err := service.mappings.FindMapping(ctx, input.TenantID, input.WorkspaceID, input.ID)
	if err != nil {
		return MappingRegistration{}, false, err
	}
	action := "mapping." + strings.ToLower(string(status))
	requestHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", action, mapping.ID, input.ExpectedRevision))))
	if result, found, err := service.mappingReceipt(ctx, input.TenantID, input.WorkspaceID, action, input.IdempotencyKey, requestHash); err != nil || found {
		return result, found, err
	}
	if status == CatalogStatusPublished {
		if mapping.Status != CatalogStatusDraft {
			return MappingRegistration{}, false, ErrMappingImmutable
		}
		source, sourceErr := service.sources.FindSource(ctx, mapping.TenantID, mapping.WorkspaceID, mapping.SourceID)
		if sourceErr != nil {
			return MappingRegistration{}, false, sourceErr
		}
		if source.Status != CatalogStatusPublished {
			return MappingRegistration{}, false, ErrSourceNotPublished
		}
		target, schemaErr := service.schemas.Get(ctx, mapping.TenantID, mapping.TargetSchemaID, mapping.TargetSchemaVersion)
		if schemaErr != nil {
			return MappingRegistration{}, false, schemaErr
		}
		if target.Status != schema.StatusPublished {
			return MappingRegistration{}, false, ErrTargetSchemaNotPublished
		}
		if err := validateMappingTargetFields(target, mapping.FieldMap); err != nil {
			return MappingRegistration{}, false, err
		}
		fixture, fixtureErr := service.fixtures.GetFixture(ctx, mapping.TenantID, mapping.WorkspaceID, mapping.Fixture.EventRef, mapping.Fixture.SHA256)
		if fixtureErr != nil {
			return MappingRegistration{}, false, fixtureErr
		}
		if err := validateMappingFixture(source, mapping, target, fixture.Document); err != nil {
			return MappingRegistration{}, false, err
		}
		hash, hashErr := mapping.ComputeCanonicalHash()
		if hashErr != nil {
			return MappingRegistration{}, false, hashErr
		}
		if hash != mapping.CanonicalHash {
			return MappingRegistration{}, false, ErrCanonicalHashMismatch
		}
	} else if mapping.Status != CatalogStatusPublished {
		return MappingRegistration{}, false, ErrMappingImmutable
	}
	now := service.clock().UTC()
	var updated MappingRegistration
	err = service.inTransaction(ctx, func(tx context.Context) error {
		var transitionErr error
		updated, transitionErr = service.mappings.TransitionMapping(tx, mapping.TenantID, mapping.WorkspaceID, mapping.ID, status, principal.IdentityKey(), &now, input.ExpectedRevision)
		if transitionErr != nil {
			return transitionErr
		}
		if err := service.auditWriter.Append(tx, audit.Entry{TenantID: mapping.TenantID, WorkspaceID: mapping.WorkspaceID, Action: action, Actor: principal.IdentityKey(), ResourceType: "MappingRegistration", ResourceID: mapping.ID, ResourceVersion: updated.Revision, RequestID: input.RequestID, IdempotencyKey: input.IdempotencyKey, BeforeHash: mapping.CanonicalHash, AfterHash: requestHash, CreatedAt: now}); err != nil {
			return err
		}
		return service.receipts.Save(tx, CatalogReceipt{TenantID: mapping.TenantID, WorkspaceID: mapping.WorkspaceID, Operation: action, IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Mapping: &updated})
	})
	return updated, false, err
}

func validateMappingTargetFields(definition schema.Definition, fieldMap map[string]string) error {
	raw, err := bson.MarshalExtJSON(definition.JSONSchema, false, false)
	if err != nil {
		return fmt.Errorf("encode target schema: %w", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("decode target schema: %w", err)
	}
	for target := range fieldMap {
		current := root
		for _, segment := range strings.Split(target, ".") {
			properties, ok := current["properties"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("%w: %s", ErrTargetFieldNotAllowed, target)
			}
			next, ok := properties[segment].(map[string]interface{})
			if !ok {
				return fmt.Errorf("%w: %s", ErrTargetFieldNotAllowed, target)
			}
			current = next
		}
	}
	return nil
}

func (service *CatalogService) ready() error {
	if service == nil || service.sources == nil || service.mappings == nil || service.fixtures == nil || service.schemas == nil || service.authorizer == nil || service.receipts == nil || service.auditWriter == nil || service.clock == nil {
		return ErrCatalogUnavailable
	}
	return nil
}

func (service *CatalogService) authorize(ctx context.Context, principal identity.Principal, tenantID, workspaceID string, action identity.Action) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workspaceID) == "" {
		return ErrCatalogInvalid
	}
	_, err := service.authorizer.Authorize(ctx, principal, strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), action)
	return err
}

func (service *CatalogService) inTransaction(ctx context.Context, fn func(context.Context) error) error {
	if transactional, ok := service.sources.(CatalogTransactional); ok {
		return transactional.WithTransaction(ctx, fn)
	}
	return fn(ctx)
}

func hashCatalogInput(operation string, value interface{}) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(append([]byte(operation+"\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (service *CatalogService) sourceReceipt(ctx context.Context, tenantID, workspaceID, operation, key, requestHash string) (SourceRegistration, bool, error) {
	receipt, err := service.receipts.Find(ctx, tenantID, workspaceID, operation, key)
	if err != nil {
		if errors.Is(err, ErrCatalogReceiptNotFound) {
			return SourceRegistration{}, false, nil
		}
		return SourceRegistration{}, false, err
	}
	if receipt.RequestHash != requestHash || receipt.Source == nil {
		return SourceRegistration{}, false, ErrCatalogIdempotencyConflict
	}
	return *receipt.Source, true, nil
}

func (service *CatalogService) mappingReceipt(ctx context.Context, tenantID, workspaceID, operation, key, requestHash string) (MappingRegistration, bool, error) {
	receipt, err := service.receipts.Find(ctx, tenantID, workspaceID, operation, key)
	if err != nil {
		if errors.Is(err, ErrCatalogReceiptNotFound) {
			return MappingRegistration{}, false, nil
		}
		return MappingRegistration{}, false, err
	}
	if receipt.RequestHash != requestHash || receipt.Mapping == nil {
		return MappingRegistration{}, false, ErrCatalogIdempotencyConflict
	}
	return *receipt.Mapping, true, nil
}

// MongoCatalogRepository persists the source and mapping control plane. The
// transition methods predicate on status and revision to make publish/revoke
// safe under concurrent operators.
type MongoCatalogRepository struct {
	sources  *mongo.Collection
	mappings *mongo.Collection
	fixtures *mongo.Collection
}

const (
	sourceCollectionName         = "projection_sources"
	mappingCollectionName        = "projection_mappings"
	fixtureCollectionName        = "projection_mapping_fixtures"
	catalogReceiptCollectionName = "projection_catalog_receipts"
)

func NewMongoCatalogRepository(database *mongo.Database) *MongoCatalogRepository {
	return &MongoCatalogRepository{sources: database.Collection(sourceCollectionName), mappings: database.Collection(mappingCollectionName), fixtures: database.Collection(fixtureCollectionName)}
}

func (repository *MongoCatalogRepository) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	if repository == nil || repository.sources == nil {
		return ErrCatalogUnavailable
	}
	session, err := repository.sources.Database().Client().StartSession()
	if err != nil {
		return fmt.Errorf("start catalog transaction: %w", err)
	}
	defer session.EndSession(context.Background())
	_, err = session.WithTransaction(ctx, func(tx context.Context) (interface{}, error) { return nil, fn(tx) })
	return err
}

func (repository *MongoCatalogRepository) EnsureIndexes(ctx context.Context) error {
	if repository == nil || repository.sources == nil || repository.mappings == nil || repository.fixtures == nil {
		return ErrCatalogUnavailable
	}
	if _, err := repository.sources.Indexes().CreateMany(ctx, []mongo.IndexModel{{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "sourceId", Value: 1}}, Options: options.Index().SetName("projection_source_scope_id_unique").SetUnique(true)}, {Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "status", Value: 1}, {Key: "updatedAt", Value: -1}}, Options: options.Index().SetName("projection_source_scope_status_updated")}}); err != nil {
		return fmt.Errorf("create projection source indexes: %w", err)
	}
	if _, err := repository.mappings.Indexes().CreateMany(ctx, []mongo.IndexModel{{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "mappingId", Value: 1}}, Options: options.Index().SetName("projection_mapping_scope_id_unique").SetUnique(true)}, {Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "sourceId", Value: 1}, {Key: "eventType", Value: 1}, {Key: "eventVersion", Value: 1}, {Key: "status", Value: 1}}, Options: options.Index().SetName("projection_mapping_event_lookup")}}); err != nil {
		return fmt.Errorf("create projection mapping indexes: %w", err)
	}
	if err := repository.ensureFixtureIndexes(ctx); err != nil {
		return err
	}
	return nil
}

func (repository *MongoCatalogRepository) CreateSource(ctx context.Context, source SourceRegistration) error {
	if err := source.Validate(); err != nil {
		return err
	}
	_, err := repository.sources.InsertOne(ctx, source)
	if mongo.IsDuplicateKeyError(err) {
		return ErrSourceExists
	}
	if err != nil {
		return fmt.Errorf("insert source registration: %w", err)
	}
	return nil
}
func (repository *MongoCatalogRepository) FindSource(ctx context.Context, tenantID, workspaceID, id string) (SourceRegistration, error) {
	var source SourceRegistration
	err := repository.sources.FindOne(ctx, bson.D{{Key: "sourceId", Value: id}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}).Decode(&source)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return SourceRegistration{}, ErrSourceNotFound
	}
	if err != nil {
		return SourceRegistration{}, fmt.Errorf("find source registration: %w", err)
	}
	return source, nil
}
func (repository *MongoCatalogRepository) ListSources(ctx context.Context, tenantID, workspaceID string, limit int64) ([]SourceRegistration, error) {
	cursor, err := repository.sources.Find(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}, {Key: "sourceId", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list source registrations: %w", err)
	}
	defer cursor.Close(ctx)
	var result []SourceRegistration
	if err := cursor.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("decode source registrations: %w", err)
	}
	return result, nil
}

func (repository *MongoCatalogRepository) TransitionSource(ctx context.Context, tenantID, workspaceID, id string, status CatalogStatus, actor identity.IdentityKey, at *time.Time, expectedRevision int64) (SourceRegistration, error) {
	filter := bson.D{{Key: "sourceId", Value: id}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "status", Value: CatalogStatusDraft}, {Key: "revision", Value: expectedRevision}}
	set := bson.D{{Key: "status", Value: status}, {Key: "updatedBy", Value: actor}, {Key: "updatedAt", Value: at}}
	if status == CatalogStatusPublished {
		set = append(set, bson.E{Key: "publishedAt", Value: at})
	}
	update := bson.D{{Key: "$set", Value: set}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: 1}}}}
	var result SourceRegistration
	err := repository.sources.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return SourceRegistration{}, fmt.Errorf("transition source registration: %w", err)
	}
	current, lookupErr := repository.FindSource(ctx, tenantID, workspaceID, id)
	if lookupErr != nil {
		return SourceRegistration{}, lookupErr
	}
	if current.Status != CatalogStatusDraft {
		return SourceRegistration{}, ErrSourceImmutable
	}
	return SourceRegistration{}, ErrSourceRevisionConflict
}

func (repository *MongoCatalogRepository) CreateMapping(ctx context.Context, mapping MappingRegistration) error {
	if err := mapping.Validate(); err != nil {
		return err
	}
	_, err := repository.mappings.InsertOne(ctx, mapping)
	if mongo.IsDuplicateKeyError(err) {
		return ErrMappingExists
	}
	if err != nil {
		return fmt.Errorf("insert mapping registration: %w", err)
	}
	return nil
}
func (repository *MongoCatalogRepository) FindMapping(ctx context.Context, tenantID, workspaceID, id string) (MappingRegistration, error) {
	var mapping MappingRegistration
	err := repository.mappings.FindOne(ctx, bson.D{{Key: "mappingId", Value: id}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}).Decode(&mapping)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return MappingRegistration{}, ErrMappingNotFound
	}
	if err != nil {
		return MappingRegistration{}, fmt.Errorf("find mapping registration: %w", err)
	}
	return mapping, nil
}
func (repository *MongoCatalogRepository) ListMappings(ctx context.Context, tenantID, workspaceID, sourceID string, limit int64) ([]MappingRegistration, error) {
	filter := bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}}
	if sourceID != "" {
		filter = append(filter, bson.E{Key: "sourceId", Value: sourceID})
	}
	cursor, err := repository.mappings.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}, {Key: "mappingId", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("list mapping registrations: %w", err)
	}
	defer cursor.Close(ctx)
	var result []MappingRegistration
	if err := cursor.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("decode mapping registrations: %w", err)
	}
	return result, nil
}
func (repository *MongoCatalogRepository) TransitionMapping(ctx context.Context, tenantID, workspaceID, id string, status CatalogStatus, actor identity.IdentityKey, at *time.Time, expectedRevision int64) (MappingRegistration, error) {
	allowed := CatalogStatusDraft
	if status == CatalogStatusRevoked {
		allowed = CatalogStatusPublished
	}
	filter := bson.D{{Key: "mappingId", Value: id}, {Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "status", Value: allowed}, {Key: "revision", Value: expectedRevision}}
	set := bson.D{{Key: "status", Value: status}, {Key: "updatedBy", Value: actor}, {Key: "updatedAt", Value: at}}
	if status == CatalogStatusPublished {
		set = append(set, bson.E{Key: "publishedAt", Value: at})
	}
	update := bson.D{{Key: "$set", Value: set}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: 1}}}}
	var result MappingRegistration
	err := repository.mappings.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return MappingRegistration{}, fmt.Errorf("transition mapping registration: %w", err)
	}
	current, lookupErr := repository.FindMapping(ctx, tenantID, workspaceID, id)
	if lookupErr != nil {
		return MappingRegistration{}, lookupErr
	}
	if current.Status != allowed {
		return MappingRegistration{}, ErrMappingImmutable
	}
	return MappingRegistration{}, ErrMappingRevisionConflict
}

type MongoCatalogReceiptStore struct{ collection *mongo.Collection }

func NewMongoCatalogReceiptStore(database *mongo.Database) *MongoCatalogReceiptStore {
	return &MongoCatalogReceiptStore{collection: database.Collection(catalogReceiptCollectionName)}
}
func (store *MongoCatalogReceiptStore) EnsureIndexes(ctx context.Context) error {
	if store == nil || store.collection == nil {
		return ErrCatalogUnavailable
	}
	_, err := store.collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "operation", Value: 1}, {Key: "idempotencyKey", Value: 1}}, Options: options.Index().SetName("projection_catalog_receipt_scope_key_unique").SetUnique(true)})
	if err != nil {
		return fmt.Errorf("create projection catalog receipt index: %w", err)
	}
	return nil
}
func (store *MongoCatalogReceiptStore) Find(ctx context.Context, tenantID, workspaceID, operation, key string) (CatalogReceipt, error) {
	var receipt CatalogReceipt
	err := store.collection.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "operation", Value: operation}, {Key: "idempotencyKey", Value: key}}).Decode(&receipt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return CatalogReceipt{}, ErrCatalogReceiptNotFound
	}
	if err != nil {
		return CatalogReceipt{}, fmt.Errorf("find projection catalog receipt: %w", err)
	}
	return receipt, nil
}
func (store *MongoCatalogReceiptStore) Save(ctx context.Context, receipt CatalogReceipt) error {
	if _, err := store.collection.InsertOne(ctx, receipt); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			existing, findErr := store.Find(ctx, receipt.TenantID, receipt.WorkspaceID, receipt.Operation, receipt.IdempotencyKey)
			if findErr == nil && existing.RequestHash == receipt.RequestHash {
				return nil
			}
			return ErrCatalogIdempotencyConflict
		}
		return fmt.Errorf("save projection catalog receipt: %w", err)
	}
	return nil
}
