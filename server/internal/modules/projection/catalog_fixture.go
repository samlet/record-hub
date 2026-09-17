package projection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/samlet/record-hub/contracts/eventenvelope"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MappingFixtureDocument keeps the sample envelope in a dedicated collection.
// API responses expose only EventRef and SHA256 from MappingRegistration, so
// sample payloads cannot be returned accidentally by list/read endpoints.
type MappingFixtureDocument struct {
	TenantID    string               `bson:"tenantId"`
	WorkspaceID string               `bson:"workspaceId"`
	EventRef    string               `bson:"eventRef"`
	SHA256      string               `bson:"sha256"`
	Document    []byte               `bson:"document"`
	CreatedBy   identity.IdentityKey `bson:"createdBy"`
	CreatedAt   time.Time            `bson:"createdAt"`
}

func (fixture MappingFixtureDocument) Validate() error {
	if !nonBlankBounded(fixture.TenantID, maxCatalogID) || !nonBlankBounded(fixture.WorkspaceID, maxCatalogID) || !nonBlankBounded(fixture.EventRef, maxFixtureRef) || !sha256Pattern.MatchString(fixture.SHA256) {
		return ErrFixtureInvalid
	}
	if len(fixture.Document) == 0 || len(fixture.Document) > eventenvelope.MaxEnvelopeBytes || fixture.CreatedBy.Issuer == "" || fixture.CreatedBy.Subject == "" || fixture.CreatedAt.IsZero() {
		return ErrFixtureInvalid
	}
	canonical, hash, err := canonicalFixture(fixture.Document)
	if err != nil {
		return err
	}
	if hash != fixture.SHA256 || !bytes.Equal(canonical, fixture.Document) {
		return ErrFixtureHashMismatch
	}
	return nil
}

func prepareMappingFixtureDocument(tenantID, workspaceID string, metadata MappingFixture, raw []byte, actor identity.IdentityKey, now time.Time) (MappingFixtureDocument, error) {
	if len(raw) == 0 || len(raw) > eventenvelope.MaxEnvelopeBytes {
		return MappingFixtureDocument{}, ErrFixtureInvalid
	}
	canonical, hash, err := canonicalFixture(raw)
	if err != nil {
		return MappingFixtureDocument{}, err
	}
	if hash != metadata.SHA256 {
		return MappingFixtureDocument{}, ErrFixtureHashMismatch
	}
	fixture := MappingFixtureDocument{TenantID: strings.TrimSpace(tenantID), WorkspaceID: strings.TrimSpace(workspaceID), EventRef: strings.TrimSpace(metadata.EventRef), SHA256: hash, Document: canonical, CreatedBy: actor, CreatedAt: now.UTC()}
	if err := fixture.Validate(); err != nil {
		return MappingFixtureDocument{}, err
	}
	return fixture, nil
}

func canonicalFixture(raw []byte) ([]byte, string, error) {
	canonical, err := schema.CanonicalizeJSON(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%w: fixture must be one unambiguous JSON document", ErrFixtureInvalid)
	}
	digest := sha256.Sum256(canonical)
	return canonical, "sha256:" + hex.EncodeToString(digest[:]), nil
}

type fixtureEnvelope struct {
	Kind          string            `json:"kind"`
	EventType     string            `json:"eventType"`
	SchemaVersion int64             `json:"schemaVersion"`
	SourceSystem  string            `json:"sourceSystem"`
	TenantID      string            `json:"tenantId"`
	Metadata      map[string]string `json:"metadata"`
}

func validateMappingFixture(source SourceRegistration, mapping MappingRegistration, target schema.Definition, raw []byte) error {
	canonical, hash, err := canonicalFixture(raw)
	if err != nil {
		return err
	}
	if hash != mapping.Fixture.SHA256 {
		return ErrFixtureHashMismatch
	}
	if err := eventenvelope.NewVerifier().Validate(canonical); err != nil {
		return fmt.Errorf("%w: event envelope contract failed", ErrFixtureInvalid)
	}
	var envelope fixtureEnvelope
	if err := json.Unmarshal(canonical, &envelope); err != nil {
		return ErrFixtureInvalid
	}
	if envelope.Kind != "event" || envelope.SourceSystem != source.ID || envelope.EventType != mapping.EventType || envelope.SchemaVersion != mapping.EventVersion || envelope.TenantID != mapping.TenantID {
		return fmt.Errorf("%w: fixture identity does not match source registration", ErrFixtureInvalid)
	}
	if source.TenantID != mapping.TenantID || source.WorkspaceID != mapping.WorkspaceID {
		return fmt.Errorf("%w: mapping scope does not match source registration", ErrFixtureInvalid)
	}
	if !fixtureResolvesWorkspace(source.TenantResolution, envelope, mapping.TenantID, mapping.WorkspaceID) {
		return fmt.Errorf("%w: fixture workspace resolution failed", ErrFixtureInvalid)
	}

	instance, err := decodeFixtureJSON(canonical)
	if err != nil {
		return ErrFixtureInvalid
	}
	mapped := make(map[string]interface{}, len(mapping.FieldMap))
	for targetPath, sourcePath := range mapping.FieldMap {
		value, ok := resolveFixturePath(instance, sourcePath)
		if !ok {
			return fmt.Errorf("%w: a declared source path is absent", ErrFixtureInvalid)
		}
		if err := setMappedFixtureValue(mapped, targetPath, value); err != nil {
			return err
		}
	}
	mappedJSON, err := json.Marshal(mapped)
	if err != nil {
		return fmt.Errorf("%w: mapped fixture cannot be encoded", ErrFixtureInvalid)
	}
	targetSchema, err := bson.MarshalExtJSON(target.JSONSchema, false, false)
	if err != nil {
		return fmt.Errorf("%w: target schema cannot be encoded", ErrFixtureInvalid)
	}
	validator, err := schema.CompileValidator(target.SchemaID, targetSchema)
	if err != nil {
		return fmt.Errorf("%w: target schema cannot be compiled", ErrFixtureInvalid)
	}
	if err := validator.ValidateJSON(mappedJSON); err != nil {
		return fmt.Errorf("%w: mapped fixture does not match target schema", ErrFixtureInvalid)
	}
	return nil
}

func fixtureResolvesWorkspace(resolution TenantResolution, envelope fixtureEnvelope, tenantID, workspaceID string) bool {
	switch resolution.Mode {
	case TenantResolutionMetadata:
		return envelope.TenantID == tenantID && strings.TrimSpace(envelope.Metadata[resolution.Field]) == workspaceID
	case TenantResolutionAllowlist:
		for _, rule := range resolution.Allowlist {
			if envelope.TenantID == rule.TenantID && tenantID == rule.TenantID && workspaceID == rule.WorkspaceID {
				return true
			}
		}
	}
	return false
}

func decodeFixtureJSON(raw []byte) (interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func resolveFixturePath(root interface{}, path string) (interface{}, bool) {
	if strings.HasPrefix(path, "payload.") {
		object, ok := root.(map[string]interface{})
		if !ok {
			return nil, false
		}
		value, ok := object["payload"]
		if !ok {
			return nil, false
		}
		return traverseFixturePath(value, strings.Split(strings.TrimPrefix(path, "payload."), "."))
	}
	if strings.HasPrefix(path, "/") {
		segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
		for index, segment := range segments {
			segments[index] = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		}
		return traverseFixturePath(root, segments)
	}
	return nil, false
}

func traverseFixturePath(value interface{}, segments []string) (interface{}, bool) {
	current := value
	for _, segment := range segments {
		switch typed := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = typed[segment]
			if !ok {
				return nil, false
			}
		case []interface{}:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func setMappedFixtureValue(root map[string]interface{}, path string, value interface{}) error {
	segments := strings.Split(path, ".")
	current := root
	for index, segment := range segments {
		if index == len(segments)-1 {
			if _, exists := current[segment]; exists {
				return fmt.Errorf("%w: duplicate target path", ErrFixtureInvalid)
			}
			current[segment] = value
			return nil
		}
		next, exists := current[segment]
		if !exists {
			child := make(map[string]interface{})
			current[segment] = child
			current = child
			continue
		}
		child, ok := next.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%w: conflicting target paths", ErrFixtureInvalid)
		}
		current = child
	}
	return ErrFixtureInvalid
}

func (repository *MongoCatalogRepository) ensureFixtureIndexes(ctx context.Context) error {
	_, err := repository.fixtures.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "eventRef", Value: 1}, {Key: "sha256", Value: 1}}, Options: options.Index().SetName("projection_fixture_scope_ref_hash_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "createdAt", Value: -1}}, Options: options.Index().SetName("projection_fixture_scope_created")},
	})
	if err != nil {
		return fmt.Errorf("create projection fixture indexes: %w", err)
	}
	return nil
}

func (repository *MongoCatalogRepository) PutFixture(ctx context.Context, fixture MappingFixtureDocument) error {
	if repository == nil || repository.fixtures == nil {
		return ErrCatalogUnavailable
	}
	if err := fixture.Validate(); err != nil {
		return err
	}
	if _, err := repository.fixtures.InsertOne(ctx, fixture); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			existing, findErr := repository.GetFixture(ctx, fixture.TenantID, fixture.WorkspaceID, fixture.EventRef, fixture.SHA256)
			if findErr == nil && bytes.Equal(existing.Document, fixture.Document) {
				return nil
			}
			return ErrFixtureHashMismatch
		}
		return fmt.Errorf("insert projection fixture: %w", err)
	}
	return nil
}

func (repository *MongoCatalogRepository) GetFixture(ctx context.Context, tenantID, workspaceID, eventRef, hash string) (MappingFixtureDocument, error) {
	if repository == nil || repository.fixtures == nil {
		return MappingFixtureDocument{}, ErrCatalogUnavailable
	}
	var fixture MappingFixtureDocument
	err := repository.fixtures.FindOne(ctx, bson.D{{Key: "tenantId", Value: tenantID}, {Key: "workspaceId", Value: workspaceID}, {Key: "eventRef", Value: eventRef}, {Key: "sha256", Value: hash}}).Decode(&fixture)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return MappingFixtureDocument{}, ErrFixtureNotFound
	}
	if err != nil {
		return MappingFixtureDocument{}, fmt.Errorf("find projection fixture: %w", err)
	}
	if err := fixture.Validate(); err != nil {
		return MappingFixtureDocument{}, err
	}
	return fixture, nil
}
