package records

import (
	"context"
	"errors"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type memoryViewRepository struct {
	views   map[string]ViewDefinition
	records []Record
}

func (repository *memoryViewRepository) CreateView(_ context.Context, view ViewDefinition) error {
	key := view.TenantID + ":" + view.WorkspaceID + ":" + view.TableID + ":" + view.ID
	if _, exists := repository.views[key]; exists {
		return ErrViewExists
	}
	repository.views[key] = view
	return nil
}

func (repository *memoryViewRepository) GetView(_ context.Context, tenantID, workspaceID, tableID, viewID string) (ViewDefinition, error) {
	view, ok := repository.views[tenantID+":"+workspaceID+":"+tableID+":"+viewID]
	if !ok {
		return ViewDefinition{}, ErrViewNotFound
	}
	return view, nil
}

func (repository *memoryViewRepository) UpdateView(_ context.Context, view ViewDefinition, expectedVersion int64) (ViewDefinition, error) {
	key := view.TenantID + ":" + view.WorkspaceID + ":" + view.TableID + ":" + view.ID
	current, ok := repository.views[key]
	if !ok {
		return ViewDefinition{}, ErrViewNotFound
	}
	if current.Version != expectedVersion {
		return ViewDefinition{}, ErrViewVersionConflict
	}
	view.Version = expectedVersion + 1
	repository.views[key] = view
	return view, nil
}

func (repository *memoryViewRepository) ListViews(_ context.Context, tenantID, workspaceID, tableID string) ([]ViewDefinition, error) {
	result := make([]ViewDefinition, 0)
	for _, view := range repository.views {
		if view.TenantID == tenantID && view.WorkspaceID == workspaceID && view.TableID == tableID {
			result = append(result, view)
		}
	}
	return result, nil
}

func (repository *memoryViewRepository) ListRecords(_ context.Context, tenantID, workspaceID, tableID string, _ ViewDefinition, cursor string, limit int) (RecordPage, error) {
	if cursor != "" && cursor != "cursor-1" {
		return RecordPage{}, ErrInvalidCursor
	}
	result := make([]Record, 0)
	for _, record := range repository.records {
		if record.TenantID == tenantID && record.WorkspaceID == workspaceID && record.TableID == tableID {
			result = append(result, record)
		}
	}
	if len(result) > limit {
		return RecordPage{Items: result[:limit], NextCursor: "cursor-1"}, nil
	}
	return RecordPage{Items: result}, nil
}

func TestViewDefinitionValidation(t *testing.T) {
	view := ViewDefinition{ID: "view-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", Name: "Open", Columns: []string{"title", "status"}, Filters: []ViewFilter{{Field: "status", Operator: FilterEqual, Value: "OPEN"}}, Sorts: []ViewSort{{Field: "updatedAt", Direction: SortDescending}}, Version: 1, CreatedBy: identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}, UpdatedBy: identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}, CreatedAt: timeNow(), UpdatedAt: timeNow()}
	if err := view.Validate(); err != nil {
		t.Fatal(err)
	}
	view.Filters[0].Operator = ViewFilterOperator("$where")
	if err := view.Validate(); err == nil {
		t.Fatal("unsafe filter operator should fail")
	}
	view.Filters[0] = ViewFilter{Field: "status", Operator: FilterContains, Value: 123}
	if err := view.Validate(); err == nil {
		t.Fatal("contains with non-string should fail")
	}
}

func TestViewServiceEnforcesSchemaFieldAllowlistAndCursorBounds(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	definition := recordSchemaDefinition(t)
	tables := &memoryTableRepository{values: map[string]TableDefinition{"tenant-1:workspace-1:table-1": {ID: "table-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Kind: TableKindCustom, SchemaID: definition.SchemaID, SchemaVersion: 1}}}
	views := &memoryViewRepository{views: make(map[string]ViewDefinition)}
	service := NewService(nil, tables, memorySchemaReader{definition: definition}, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}})).WithViewRepository(views)
	view, err := service.CreateView(context.Background(), principal, ViewInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "view-1", Name: "Open", Columns: []string{"title"}, Filters: []ViewFilter{{Field: "count", Operator: FilterAtLeast, Value: 1}}, Sorts: []ViewSort{{Field: "updatedAt", Direction: SortDescending}}})
	if err != nil || view.ID != "view-1" {
		t.Fatalf("create view = %#v, %v", view, err)
	}
	if _, err := service.CreateView(context.Background(), principal, ViewInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "view-2", Name: "Unsafe", Columns: []string{"secret"}}); err == nil {
		t.Fatal("field absent from published schema should fail")
	}
	updated, err := service.UpdateView(context.Background(), principal, ViewInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "view-1", Name: "Open first", Columns: []string{"title", "count"}, Sorts: []ViewSort{{Field: "count", Direction: SortDescending}}}, 1)
	if err != nil || updated.Version != 2 || updated.Name != "Open first" {
		t.Fatalf("update view = %#v, %v", updated, err)
	}
	if _, err := service.UpdateView(context.Background(), principal, ViewInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "view-1", Name: "Stale", Columns: []string{"title"}}, 1); !errors.Is(err, ErrViewVersionConflict) {
		t.Fatalf("stale view update = %v", err)
	}
	if _, err := service.ListRecords(context.Background(), principal, "tenant-1", "workspace-1", "table-1", "", "", 101); err == nil {
		t.Fatal("page limit above 100 should fail")
	}
}
