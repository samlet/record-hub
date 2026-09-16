package records

import (
	"errors"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type TableKind string

const (
	TableKindCustom     TableKind = "CUSTOM"
	TableKindProjection TableKind = "PROJECTION"
)

type Workspace struct {
	ID        string               `bson:"_id" json:"id"`
	TenantID  string               `bson:"tenantId" json:"tenantId"`
	Name      string               `bson:"name" json:"name"`
	Version   int64                `bson:"version" json:"version"`
	CreatedBy identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	CreatedAt time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time            `bson:"updatedAt" json:"updatedAt"`
}

// SourcePolicy describes the safe, allowlisted source fields for a projection
// table. It is intentionally narrow: projection handlers never receive an
// arbitrary Mongo query or an unrestricted source payload.
type SourcePolicy struct {
	System      string   `bson:"system" json:"system"`
	Type        string   `bson:"type" json:"type"`
	AllowFields []string `bson:"allowFields" json:"allowFields"`
}

type TableDefinition struct {
	ID            string               `bson:"_id" json:"id"`
	TenantID      string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID   string               `bson:"workspaceId" json:"workspaceId"`
	Name          string               `bson:"name" json:"name"`
	Kind          TableKind            `bson:"kind" json:"kind"`
	SchemaID      string               `bson:"schemaId" json:"schemaId"`
	SchemaVersion int64                `bson:"schemaVersion" json:"schemaVersion"`
	SourcePolicy  *SourcePolicy        `bson:"sourcePolicy,omitempty" json:"sourcePolicy,omitempty"`
	Version       int64                `bson:"version" json:"version"`
	CreatedBy     identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	UpdatedBy     identity.IdentityKey `bson:"updatedBy" json:"updatedBy"`
	CreatedAt     time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt     time.Time            `bson:"updatedAt" json:"updatedAt"`
}

type RecordSource struct {
	System  string `bson:"system" json:"system"`
	Type    string `bson:"type" json:"type"`
	ID      string `bson:"id" json:"id"`
	Version int64  `bson:"version" json:"version"`
}

type RelationTarget struct {
	System string `bson:"system" json:"system"`
	Type   string `bson:"type" json:"type"`
	ID     string `bson:"id" json:"id"`
}

type RelationStatus string

const (
	RelationCurrent   RelationStatus = "CURRENT"
	RelationBroken    RelationStatus = "BROKEN"
	RelationForbidden RelationStatus = "FORBIDDEN"
)

type RecordRelation struct {
	Target           RelationTarget `bson:"target" json:"target"`
	RelationType     string         `bson:"relationType" json:"relationType"`
	ResolvedRecordID string         `bson:"resolvedRecordId,omitempty" json:"resolvedRecordId,omitempty"`
	Status           RelationStatus `bson:"status" json:"status"`
}

type ProjectionState struct {
	LastEventID string    `bson:"lastEventId" json:"lastEventId"`
	SyncedAt    time.Time `bson:"syncedAt" json:"syncedAt"`
	Status      string    `bson:"status" json:"status"`
}

// Record is the user-owned envelope. Dynamic business content is kept as a
// BSON document and is validated against the table's published schema before
// every write.
type Record struct {
	ID            string               `bson:"_id" json:"id"`
	TenantID      string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID   string               `bson:"workspaceId" json:"workspaceId"`
	TableID       string               `bson:"tableId" json:"tableId"`
	SchemaID      string               `bson:"schemaId" json:"schemaId"`
	SchemaVersion int64                `bson:"schemaVersion" json:"schemaVersion"`
	Source        *RecordSource        `bson:"source,omitempty" json:"source,omitempty"`
	RecordVersion int64                `bson:"recordVersion" json:"recordVersion"`
	Tags          []string             `bson:"tags" json:"tags"`
	Data          bson.Raw             `bson:"data" json:"data"`
	Relations     []RecordRelation     `bson:"relations" json:"relations"`
	Projection    *ProjectionState     `bson:"projection,omitempty" json:"projection,omitempty"`
	CreatedBy     identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	UpdatedBy     identity.IdentityKey `bson:"updatedBy" json:"updatedBy"`
	CreatedAt     time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt     time.Time            `bson:"updatedAt" json:"updatedAt"`
}

func (record Record) Validate() error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.WorkspaceID) == "" || strings.TrimSpace(record.TableID) == "" || strings.TrimSpace(record.SchemaID) == "" {
		return errors.New("record id, tenant, workspace, table, and schema are required")
	}
	if record.SchemaVersion < 1 || record.RecordVersion < 1 {
		return errors.New("schema version and record version must be positive")
	}
	var document bson.D
	if len(record.Data) == 0 || record.Data.Validate() != nil || bson.Unmarshal(record.Data, &document) != nil {
		return errors.New("record data must be an object")
	}
	if record.CreatedBy.Issuer == "" || record.CreatedBy.Subject == "" || record.UpdatedBy.Issuer == "" || record.UpdatedBy.Subject == "" || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return errors.New("record identities and timestamps are required")
	}
	if record.Source != nil {
		if strings.TrimSpace(record.Source.System) == "" || strings.TrimSpace(record.Source.Type) == "" || strings.TrimSpace(record.Source.ID) == "" || record.Source.Version < 1 {
			return errors.New("record source requires system, type, id, and positive version")
		}
	}
	for _, relation := range record.Relations {
		if err := relation.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (relation RecordRelation) Validate() error {
	if strings.TrimSpace(relation.Target.System) == "" || strings.TrimSpace(relation.Target.Type) == "" || strings.TrimSpace(relation.Target.ID) == "" || strings.TrimSpace(relation.RelationType) == "" {
		return errors.New("record relation requires target and relation type")
	}
	switch relation.Status {
	case "", RelationCurrent, RelationBroken:
	case RelationForbidden:
		if relation.ResolvedRecordID != "" {
			return errors.New("forbidden relation cannot expose a resolved record")
		}
	default:
		return errors.New("record relation status must be CURRENT, BROKEN, or FORBIDDEN")
	}
	return nil
}

func (workspace Workspace) Validate() error {
	if strings.TrimSpace(workspace.ID) == "" || strings.TrimSpace(workspace.TenantID) == "" || strings.TrimSpace(workspace.Name) == "" {
		return errors.New("workspace id, tenant, and name are required")
	}
	if workspace.Version < 1 {
		return errors.New("workspace version must be positive")
	}
	if workspace.CreatedBy.Issuer == "" || workspace.CreatedBy.Subject == "" || workspace.CreatedAt.IsZero() || workspace.UpdatedAt.IsZero() {
		return errors.New("workspace creator and timestamps are required")
	}
	return nil
}

func (table TableDefinition) Validate() error {
	if strings.TrimSpace(table.ID) == "" || strings.TrimSpace(table.TenantID) == "" || strings.TrimSpace(table.WorkspaceID) == "" || strings.TrimSpace(table.Name) == "" {
		return errors.New("table id, tenant, workspace, and name are required")
	}
	if table.Kind != TableKindCustom && table.Kind != TableKindProjection {
		return errors.New("table kind must be CUSTOM or PROJECTION")
	}
	if strings.TrimSpace(table.SchemaID) == "" || table.SchemaVersion < 1 {
		return errors.New("table schema and positive schema version are required")
	}
	if table.Kind == TableKindProjection {
		if table.SourcePolicy == nil || strings.TrimSpace(table.SourcePolicy.System) == "" || strings.TrimSpace(table.SourcePolicy.Type) == "" {
			return errors.New("projection table requires a source policy with system and type")
		}
		for _, field := range table.SourcePolicy.AllowFields {
			if strings.TrimSpace(field) == "" {
				return errors.New("projection source policy fields cannot be empty")
			}
		}
	} else if table.SourcePolicy != nil {
		return errors.New("custom table cannot define a source policy")
	}
	if table.Version < 1 {
		return errors.New("table version must be positive")
	}
	if table.CreatedBy.Issuer == "" || table.CreatedBy.Subject == "" || table.UpdatedBy.Issuer == "" || table.UpdatedBy.Subject == "" || table.CreatedAt.IsZero() || table.UpdatedAt.IsZero() {
		return errors.New("table identities and timestamps are required")
	}
	return nil
}
