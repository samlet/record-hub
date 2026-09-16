package records

import (
	"errors"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
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
