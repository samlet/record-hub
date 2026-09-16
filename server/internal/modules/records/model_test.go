package records

import (
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestTableDefinitionValidatesKindAndSourcePolicy(t *testing.T) {
	base := TableDefinition{ID: "table-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Name: "Custom", Kind: TableKindCustom, SchemaID: "urn:record-hub:schema:example", SchemaVersion: 1, Version: 1, CreatedBy: identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}, UpdatedBy: identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	projection := base
	projection.Kind = TableKindProjection
	if err := projection.Validate(); err == nil {
		t.Fatal("projection without source policy should fail")
	}
	projection.SourcePolicy = &SourcePolicy{System: "fluxion", Type: "PROJECT", AllowFields: []string{"id", "status"}}
	if err := projection.Validate(); err != nil {
		t.Fatal(err)
	}
	customWithPolicy := base
	customWithPolicy.SourcePolicy = projection.SourcePolicy
	if err := customWithPolicy.Validate(); err == nil {
		t.Fatal("custom table with source policy should fail")
	}
	badField := projection
	badField.SourcePolicy = &SourcePolicy{System: "fluxion", Type: "PROJECT", AllowFields: []string{""}}
	if err := badField.Validate(); err == nil || !strings.Contains(err.Error(), "fields") {
		t.Fatalf("empty allow field error = %v", err)
	}
}

func TestWorkspaceValidation(t *testing.T) {
	workspace := Workspace{ID: "workspace-1", TenantID: "tenant-1", Name: "Workspace", Version: 1, CreatedBy: identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := workspace.Validate(); err != nil {
		t.Fatal(err)
	}
	workspace.Version = 0
	if err := workspace.Validate(); err == nil {
		t.Fatal("zero workspace version should fail")
	}
}
