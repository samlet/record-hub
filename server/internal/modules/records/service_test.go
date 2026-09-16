package records

import (
	"context"
	"errors"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
)

type memoryWorkspaceRepository struct {
	values map[string]Workspace
}

func (repository *memoryWorkspaceRepository) CreateWorkspace(_ context.Context, workspace Workspace) error {
	key := workspace.TenantID + ":" + workspace.ID
	if _, ok := repository.values[key]; ok {
		return ErrWorkspaceExists
	}
	repository.values[key] = workspace
	return nil
}

func (repository *memoryWorkspaceRepository) GetWorkspace(_ context.Context, tenantID, workspaceID string) (Workspace, error) {
	workspace, ok := repository.values[tenantID+":"+workspaceID]
	if !ok {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return workspace, nil
}

func (repository *memoryWorkspaceRepository) ListWorkspaces(_ context.Context, tenantID string, creator identity.IdentityKey) ([]Workspace, error) {
	var result []Workspace
	for _, workspace := range repository.values {
		if workspace.TenantID == tenantID && workspace.CreatedBy == creator {
			result = append(result, workspace)
		}
	}
	return result, nil
}

type memoryTableRepository struct {
	values map[string]TableDefinition
}

func (repository *memoryTableRepository) CreateTable(_ context.Context, table TableDefinition) error {
	key := table.TenantID + ":" + table.WorkspaceID + ":" + table.ID
	for _, existing := range repository.values {
		if existing.TenantID == table.TenantID && existing.WorkspaceID == table.WorkspaceID && existing.Name == table.Name {
			return ErrTableExists
		}
	}
	if _, ok := repository.values[key]; ok {
		return ErrTableExists
	}
	repository.values[key] = table
	return nil
}

func (repository *memoryTableRepository) GetTable(_ context.Context, tenantID, workspaceID, tableID string) (TableDefinition, error) {
	table, ok := repository.values[tenantID+":"+workspaceID+":"+tableID]
	if !ok {
		return TableDefinition{}, ErrTableNotFound
	}
	return table, nil
}

func (repository *memoryTableRepository) ListTables(_ context.Context, tenantID, workspaceID string) ([]TableDefinition, error) {
	var result []TableDefinition
	for _, table := range repository.values {
		if table.TenantID == tenantID && table.WorkspaceID == workspaceID {
			result = append(result, table)
		}
	}
	return result, nil
}

type memorySchemaReader struct {
	definition schema.Definition
}

func (reader memorySchemaReader) Get(context.Context, string, string, int64) (schema.Definition, error) {
	return reader.definition, nil
}

func TestRecordServiceEnforcesSchemaPublicationAndRoles(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	membership := identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}
	authorizer := identity.NewAuthorizer(serviceMembershipReader{membership: membership})
	published := schema.Definition{TenantID: "tenant-1", SchemaID: "urn:record-hub:schema:example", Version: 1, Status: schema.StatusPublished}
	workspaces := &memoryWorkspaceRepository{values: make(map[string]Workspace)}
	tables := &memoryTableRepository{values: make(map[string]TableDefinition)}
	service := NewService(workspaces, tables, memorySchemaReader{definition: published}, authorizer)
	ctx := context.Background()
	workspace, err := service.CreateWorkspace(ctx, principal, WorkspaceInput{TenantID: "tenant-1", ID: "workspace-1", Name: "Workspace"})
	if err != nil || workspace.ID != "workspace-1" {
		t.Fatalf("create workspace = %#v, %v", workspace, err)
	}
	table, err := service.CreateTable(ctx, principal, TableInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: "table-1", Name: "Custom", Kind: TableKindCustom, SchemaID: published.SchemaID, SchemaVersion: 1})
	if err != nil || table.Version != 1 {
		t.Fatalf("create table = %#v, %v", table, err)
	}
	viewer := principal
	viewer.Subject = "viewer"
	if _, err := service.ListTables(ctx, viewer, "tenant-1", "workspace-1"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("unmatched membership error = %v", err)
	}
	unpublished := service
	unpublished.schemas = memorySchemaReader{definition: schema.Definition{TenantID: "tenant-1", SchemaID: published.SchemaID, Version: 2, Status: schema.StatusDraft}}
	if _, err := unpublished.CreateTable(ctx, principal, TableInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: "table-2", Name: "Draft", Kind: TableKindCustom, SchemaID: published.SchemaID, SchemaVersion: 2}); !errors.Is(err, ErrSchemaUnavailable) {
		t.Fatalf("unpublished schema error = %v", err)
	}
}

type serviceMembershipReader struct {
	membership identity.WorkspaceMembership
}

func (reader serviceMembershipReader) FindMembership(_ context.Context, subject identity.IdentityKey, tenantID, workspaceID string) (identity.WorkspaceMembership, error) {
	if reader.membership.Identity != subject || reader.membership.TenantID != tenantID || reader.membership.WorkspaceID != workspaceID {
		return identity.WorkspaceMembership{}, identity.ErrMembershipGone
	}
	return reader.membership, nil
}
