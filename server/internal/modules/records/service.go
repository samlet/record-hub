package records

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
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
}

func NewService(workspaces WorkspaceRepository, tables TableRepository, schemas SchemaReader, authorizer *identity.Authorizer) *Service {
	return &Service{workspaces: workspaces, tables: tables, schemas: schemas, authorizer: authorizer, clock: time.Now}
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
