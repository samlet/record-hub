package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/samlet/record-hub/contracts/eventenvelope"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const maxRuntimeMappings int64 = 10_000

var (
	ErrMappingGenerationInvalid = errors.New("invalid projection mapping generation")
	ErrRuntimeMappingNotFound   = errors.New("published runtime mapping not found")
	ErrRuntimeMappingAmbiguous  = errors.New("published runtime mapping is ambiguous")
	ErrBuiltInMappingReserved   = errors.New("built-in projection mapping key is reserved")
	ErrMappingSourceUnsupported = errors.New("projection mapping source has no JetStream consumer")
)

// RuntimeMappingKey is the complete isolation and event-contract key. No
// generation lookup drops tenant, workspace, type, or version from this key.
type RuntimeMappingKey struct {
	TenantID     string
	WorkspaceID  string
	SourceID     string
	EventType    string
	EventVersion int64
}

type runtimeEventKey struct {
	TenantID     string
	SourceID     string
	EventType    string
	EventVersion int64
}

// RuntimeMapping is a compiled, immutable mapping entry. Its maps and source
// resolution rules are copied while building the generation.
type RuntimeMapping struct {
	Key                 RuntimeMappingKey
	MappingID           string
	MappingRevision     int64
	CanonicalHash       string
	TargetTableID       string
	TargetSchemaID      string
	TargetSchemaVersion int64
	fieldMap            map[string]string
	tenantResolution    TenantResolution
	validator           *schema.Validator
}

// MappingGeneration is immutable after Build. A replacement is made visible
// with one atomic pointer swap, so an in-flight event always observes one
// complete catalog snapshot.
type MappingGeneration struct {
	ID         string
	BuiltAt    time.Time
	entries    map[RuntimeMappingKey]*RuntimeMapping
	byEvent    map[runtimeEventKey][]RuntimeMappingKey
	entryCount int
}

func (generation *MappingGeneration) EntryCount() int {
	if generation == nil {
		return 0
	}
	return generation.entryCount
}

// TargetTableIDs returns the immutable set of projection tables represented by
// a generation for one tenant/workspace scope. Built-in summary tables are
// included because rebuilds must switch reads for both catalog mappings and
// the three first-party projections.
func (generation *MappingGeneration) TargetTableIDs(tenantID, workspaceID string) []string {
	if generation == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for key, entry := range generation.entries {
		if key.TenantID == strings.TrimSpace(tenantID) && key.WorkspaceID == strings.TrimSpace(workspaceID) && entry != nil && entry.TargetTableID != "" {
			seen[entry.TargetTableID] = struct{}{}
		}
	}
	for _, source := range []string{"approver", "fluxion", "bids"} {
		seen[projectionTableID(source)] = struct{}{}
	}
	tables := make([]string, 0, len(seen))
	for tableID := range seen {
		tables = append(tables, tableID)
	}
	sort.Strings(tables)
	return tables
}

type MappingGenerationRegistry struct {
	active atomic.Pointer[MappingGeneration]
}

func NewMappingGenerationRegistry() *MappingGenerationRegistry {
	registry := &MappingGenerationRegistry{}
	registry.active.Store(&MappingGeneration{ID: emptyMappingGenerationID(), BuiltAt: time.Now().UTC(), entries: map[RuntimeMappingKey]*RuntimeMapping{}, byEvent: map[runtimeEventKey][]RuntimeMappingKey{}})
	return registry
}

func (registry *MappingGenerationRegistry) Activate(generation *MappingGeneration) error {
	if registry == nil || generation == nil || generation.ID == "" || generation.entries == nil || generation.byEvent == nil {
		return ErrMappingGenerationInvalid
	}
	registry.active.Store(generation)
	return nil
}

func (registry *MappingGenerationRegistry) ActiveGenerationID() string {
	if registry == nil || registry.active.Load() == nil {
		return ""
	}
	return registry.active.Load().ID
}

// Resolve uses an exact tenant/source/type/version index and then applies the
// source's declared workspace rule. It never falls back to a less-specific
// source or event version.
func (registry *MappingGenerationRegistry) Resolve(tenantID, sourceID, eventType string, eventVersion int64, metadata map[string]string) (RuntimeMapping, error) {
	if registry == nil || registry.active.Load() == nil {
		return RuntimeMapping{}, ErrRuntimeMappingNotFound
	}
	generation := registry.active.Load()
	eventKey := runtimeEventKey{TenantID: strings.TrimSpace(tenantID), SourceID: strings.TrimSpace(sourceID), EventType: strings.TrimSpace(eventType), EventVersion: eventVersion}
	var match *RuntimeMapping
	for _, key := range generation.byEvent[eventKey] {
		candidate := generation.entries[key]
		if candidate == nil || !candidate.resolvesWorkspace(eventKey.TenantID, metadata) {
			continue
		}
		if match != nil {
			return RuntimeMapping{}, ErrRuntimeMappingAmbiguous
		}
		match = candidate
	}
	if match == nil {
		return RuntimeMapping{}, ErrRuntimeMappingNotFound
	}
	return *match, nil
}

func (mapping *RuntimeMapping) resolvesWorkspace(tenantID string, metadata map[string]string) bool {
	if mapping == nil || mapping.Key.TenantID != tenantID {
		return false
	}
	switch mapping.tenantResolution.Mode {
	case TenantResolutionMetadata:
		return strings.TrimSpace(metadata[mapping.tenantResolution.Field]) == mapping.Key.WorkspaceID
	case TenantResolutionAllowlist:
		for _, rule := range mapping.tenantResolution.Allowlist {
			if rule.TenantID == tenantID && rule.WorkspaceID == mapping.Key.WorkspaceID {
				return true
			}
		}
	}
	return false
}

// Map validates the real event again, extracts only declared fields, and
// checks the result against the compiled published target schema.
func (mapping *RuntimeMapping) Map(raw []byte) (bson.Raw, error) {
	if mapping == nil || mapping.validator == nil {
		return nil, ErrMappingGenerationInvalid
	}
	if err := eventenvelope.NewVerifier().Validate(raw); err != nil {
		return nil, fmt.Errorf("event envelope: %w", err)
	}
	instance, err := decodeFixtureJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}
	mapped := make(map[string]interface{}, len(mapping.fieldMap))
	targets := make([]string, 0, len(mapping.fieldMap))
	for target := range mapping.fieldMap {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		value, found := resolveFixturePath(instance, mapping.fieldMap[target])
		if !found {
			return nil, fmt.Errorf("source path %q is absent", mapping.fieldMap[target])
		}
		if err := setMappedFixtureValue(mapped, target, value); err != nil {
			return nil, err
		}
	}
	mappedJSON, err := json.Marshal(mapped)
	if err != nil {
		return nil, fmt.Errorf("encode mapped event: %w", err)
	}
	if err := mapping.validator.ValidateJSON(mappedJSON); err != nil {
		return nil, fmt.Errorf("validate mapped event: %w", err)
	}
	var document bson.Raw
	if err := bson.UnmarshalExtJSON(mappedJSON, false, &document); err != nil {
		return nil, fmt.Errorf("encode mapped BSON: %w", err)
	}
	return document, nil
}

type MappingGenerationBuilder struct {
	catalog PublishedMappingReader
	schemas CatalogSchemaReader
	clock   func() time.Time
	limit   int64
}

func NewMappingGenerationBuilder(catalog PublishedMappingReader, schemas CatalogSchemaReader) *MappingGenerationBuilder {
	return &MappingGenerationBuilder{catalog: catalog, schemas: schemas, clock: time.Now, limit: maxRuntimeMappings}
}

func (builder *MappingGenerationBuilder) Build(ctx context.Context) (*MappingGeneration, error) {
	if builder == nil || builder.catalog == nil || builder.schemas == nil || builder.clock == nil || builder.limit < 1 || builder.limit > maxRuntimeMappings {
		return nil, ErrMappingGenerationInvalid
	}
	mappings, err := builder.catalog.ListPublishedMappings(ctx, builder.limit+1)
	if err != nil {
		return nil, err
	}
	if int64(len(mappings)) > builder.limit {
		return nil, fmt.Errorf("%w: published mapping count exceeds %d", ErrMappingGenerationInvalid, builder.limit)
	}
	generation := &MappingGeneration{BuiltAt: builder.clock().UTC(), entries: make(map[RuntimeMappingKey]*RuntimeMapping, len(mappings)), byEvent: make(map[runtimeEventKey][]RuntimeMappingKey), entryCount: len(mappings)}
	fingerprints := make([]string, 0, len(mappings))
	for _, registration := range mappings {
		entry, fingerprint, err := builder.compile(ctx, registration)
		if err != nil {
			return nil, fmt.Errorf("compile mapping %s: %w", registration.ID, err)
		}
		if _, exists := generation.entries[entry.Key]; exists {
			return nil, fmt.Errorf("%w: duplicate key %+v", ErrRuntimeMappingAmbiguous, entry.Key)
		}
		generation.entries[entry.Key] = entry
		eventKey := runtimeEventKey{TenantID: entry.Key.TenantID, SourceID: entry.Key.SourceID, EventType: entry.Key.EventType, EventVersion: entry.Key.EventVersion}
		generation.byEvent[eventKey] = append(generation.byEvent[eventKey], entry.Key)
		fingerprints = append(fingerprints, fingerprint)
	}
	sort.Strings(fingerprints)
	digest := sha256.Sum256([]byte(strings.Join(fingerprints, "\n")))
	generation.ID = "sha256:" + hex.EncodeToString(digest[:])
	return generation, nil
}

func (builder *MappingGenerationBuilder) compile(ctx context.Context, registration MappingRegistration) (*RuntimeMapping, string, error) {
	if err := registration.Validate(); err != nil || registration.Status != CatalogStatusPublished {
		return nil, "", ErrMappingGenerationInvalid
	}
	hash, err := registration.ComputeCanonicalHash()
	if err != nil || hash != registration.CanonicalHash {
		return nil, "", ErrCanonicalHashMismatch
	}
	key := RuntimeMappingKey{TenantID: registration.TenantID, WorkspaceID: registration.WorkspaceID, SourceID: registration.SourceID, EventType: registration.EventType, EventVersion: registration.EventVersion}
	if isBuiltInHandlerKey(HandlerKey{SourceSystem: key.SourceID, EventType: key.EventType, SchemaVersion: key.EventVersion}) {
		return nil, "", ErrBuiltInMappingReserved
	}
	if consumerForSource(key.SourceID) == "" {
		return nil, "", ErrMappingSourceUnsupported
	}
	source, err := builder.catalog.FindSource(ctx, key.TenantID, key.WorkspaceID, key.SourceID)
	if err != nil {
		return nil, "", err
	}
	if source.Status != CatalogStatusPublished || source.EventType != key.EventType || source.EventVersion != key.EventVersion {
		return nil, "", ErrSourceNotPublished
	}
	definition, err := builder.schemas.Get(ctx, key.TenantID, registration.TargetSchemaID, registration.TargetSchemaVersion)
	if err != nil {
		return nil, "", err
	}
	if definition.Status != schema.StatusPublished {
		return nil, "", ErrTargetSchemaNotPublished
	}
	if err := validateMappingTargetFields(definition, registration.FieldMap); err != nil {
		return nil, "", err
	}
	rawSchema, err := bson.MarshalExtJSON(definition.JSONSchema, false, false)
	if err != nil {
		return nil, "", err
	}
	validator, err := schema.CompileValidator(definition.SchemaID, rawSchema)
	if err != nil {
		return nil, "", err
	}
	entry := &RuntimeMapping{Key: key, MappingID: registration.ID, MappingRevision: registration.Revision, CanonicalHash: registration.CanonicalHash, TargetTableID: registration.TargetTableID, TargetSchemaID: registration.TargetSchemaID, TargetSchemaVersion: registration.TargetSchemaVersion, fieldMap: cloneStringMap(registration.FieldMap), tenantResolution: cloneTenantResolution(source.TenantResolution), validator: validator}
	fingerprint := strings.Join([]string{key.TenantID, key.WorkspaceID, key.SourceID, key.EventType, fmt.Sprint(key.EventVersion), registration.ID, fmt.Sprint(registration.Revision), registration.CanonicalHash, fmt.Sprint(source.Revision), definition.ContentHash}, "\x00")
	return entry, fingerprint, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneTenantResolution(input TenantResolution) TenantResolution {
	output := TenantResolution{Mode: input.Mode, Field: input.Field}
	output.Allowlist = append([]TenantResolutionRule(nil), input.Allowlist...)
	return output
}

func emptyMappingGenerationID() string {
	digest := sha256.Sum256(nil)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func isBuiltInHandlerKey(key HandlerKey) bool {
	return (key.SourceSystem == "approver" && key.EventType == "approver.application.summary-changed" && key.SchemaVersion == eventenvelope.SchemaVersion) ||
		(key.SourceSystem == "fluxion" && key.EventType == "fluxion.project.summary-changed" && key.SchemaVersion == eventenvelope.SchemaVersion) ||
		(key.SourceSystem == "bids" && key.EventType == "bids.tender.summary-changed" && key.SchemaVersion == eventenvelope.SchemaVersion)
}

type MappingGenerationRefresher struct {
	builder  *MappingGenerationBuilder
	registry *MappingGenerationRegistry
	interval time.Duration
	logger   *slog.Logger
}

func NewMappingGenerationRefresher(builder *MappingGenerationBuilder, registry *MappingGenerationRegistry, interval time.Duration, logger *slog.Logger) *MappingGenerationRefresher {
	return &MappingGenerationRefresher{builder: builder, registry: registry, interval: interval, logger: logger}
}

func (refresher *MappingGenerationRefresher) Name() string { return "projection-mapping-generation" }

func (refresher *MappingGenerationRefresher) Refresh(ctx context.Context) (string, error) {
	if refresher == nil || refresher.builder == nil || refresher.registry == nil {
		return "", ErrMappingGenerationInvalid
	}
	generation, err := refresher.builder.Build(ctx)
	if err != nil {
		return "", err
	}
	if err := refresher.registry.Activate(generation); err != nil {
		return "", err
	}
	return generation.ID, nil
}

func (refresher *MappingGenerationRefresher) Run(ctx context.Context) error {
	if refresher == nil || refresher.interval <= 0 {
		return ErrMappingGenerationInvalid
	}
	ticker := time.NewTicker(refresher.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			generationID, err := refresher.Refresh(ctx)
			if err != nil {
				if refresher.logger != nil {
					refresher.logger.Warn("projection mapping generation refresh failed; retaining active generation", "error", err)
				}
				continue
			}
			if refresher.logger != nil {
				refresher.logger.Debug("projection mapping generation active", "generation", generationID)
			}
		}
	}
}
