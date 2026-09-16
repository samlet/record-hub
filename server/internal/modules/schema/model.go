package schema

import (
	"errors"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Status string

const (
	StatusDraft      Status = "DRAFT"
	StatusPublished  Status = "PUBLISHED"
	StatusDeprecated Status = "DEPRECATED"
)

// Definition is one immutable-version slot in the tenant schema registry.
// JSONSchema is raw BSON so dynamic content remains a document in MongoDB
// without leaking map[string]any through the domain boundary.
type Definition struct {
	TenantID      string                `bson:"tenantId"`
	SchemaID      string                `bson:"schemaId"`
	Name          string                `bson:"name"`
	Version       int64                 `bson:"version"`
	Revision      int64                 `bson:"revision"`
	Status        Status                `bson:"status"`
	JSONSchema    bson.Raw              `bson:"jsonSchema"`
	SemanticTypes []string              `bson:"semanticTypes"`
	ContentHash   string                `bson:"contentHash,omitempty"`
	CreatedBy     identity.IdentityKey  `bson:"createdBy"`
	PublishedBy   *identity.IdentityKey `bson:"publishedBy,omitempty"`
	CreatedAt     time.Time             `bson:"createdAt"`
	UpdatedAt     time.Time             `bson:"updatedAt"`
	PublishedAt   *time.Time            `bson:"publishedAt,omitempty"`
}

func (definition Definition) Validate() error {
	if strings.TrimSpace(definition.TenantID) == "" || strings.TrimSpace(definition.SchemaID) == "" || strings.TrimSpace(definition.Name) == "" {
		return errors.New("tenant, schema ID, and name are required")
	}
	if definition.Version < 1 || definition.Revision < 1 {
		return errors.New("version and revision must be positive")
	}
	if definition.Status != StatusDraft && definition.Status != StatusPublished && definition.Status != StatusDeprecated {
		return errors.New("invalid schema status")
	}
	if len(definition.JSONSchema) == 0 {
		return errors.New("JSON Schema document is required")
	}
	if definition.CreatedBy.Issuer == "" || definition.CreatedBy.Subject == "" || definition.CreatedAt.IsZero() || definition.UpdatedAt.IsZero() {
		return errors.New("creator and timestamps are required")
	}
	if definition.Status != StatusDraft && (definition.ContentHash == "" || definition.PublishedBy == nil || definition.PublishedAt == nil) {
		return errors.New("non-draft schema requires hash, publisher, and timestamp")
	}
	return nil
}
