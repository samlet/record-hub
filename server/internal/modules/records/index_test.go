package records

import (
	"context"
	"errors"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type memoryIndexRepository struct {
	values map[string]IndexDefinition
}

func (repository *memoryIndexRepository) CreateIndex(_ context.Context, definition IndexDefinition) error {
	if _, exists := repository.values[definition.TenantID+":"+definition.WorkspaceID+":"+definition.TableID+":"+definition.ID]; exists {
		return ErrIndexExists
	}
	for _, existing := range repository.values {
		if existing.TenantID == definition.TenantID && existing.WorkspaceID == definition.WorkspaceID && existing.TableID == definition.TableID && indexKey(existing.Field, existing.Direction) == indexKey(definition.Field, definition.Direction) {
			return ErrIndexExists
		}
	}
	repository.values[definition.TenantID+":"+definition.WorkspaceID+":"+definition.TableID+":"+definition.ID] = definition
	return nil
}

func (repository *memoryIndexRepository) ListIndexes(_ context.Context, tenantID, workspaceID, tableID string) ([]IndexDefinition, error) {
	result := make([]IndexDefinition, 0)
	for _, definition := range repository.values {
		if definition.TenantID == tenantID && definition.WorkspaceID == workspaceID && definition.TableID == tableID {
			result = append(result, definition)
		}
	}
	return result, nil
}

func TestIndexDefinitionValidation(t *testing.T) {
	definition := IndexDefinition{ID: "index-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", Field: "title", Direction: IndexAscending, Name: physicalIndexName("title", IndexAscending), Version: 1, CreatedBy: identity.IdentityKey{Issuer: "https://issuer.example", Subject: "owner"}, CreatedAt: timeNow(), UpdatedAt: timeNow()}
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	definition.Direction = IndexDirection("$where")
	if err := definition.Validate(); err == nil {
		t.Fatal("unsafe index direction should fail")
	}
	definition.Direction = IndexAscending
	definition.Field = "recordVersion"
	if err := validateIndexSchemaField(definition.Field, recordSchemaDefinition(t)); !errors.Is(err, ErrIndexFieldNotAllowed) {
		t.Fatalf("envelope index field error = %v", err)
	}
	if physicalIndexName("title", IndexAscending) == physicalIndexName("title", IndexDescending) {
		t.Fatal("different index directions must have different physical names")
	}
}

func TestIndexServiceRestrictsFieldsRolesAndCount(t *testing.T) {
	owner := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	definition := recordSchemaDefinition(t)
	tables := &memoryTableRepository{values: map[string]TableDefinition{"tenant-1:workspace-1:table-1": {ID: "table-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Kind: TableKindCustom, SchemaID: definition.SchemaID, SchemaVersion: 1}}}
	indexes := &memoryIndexRepository{values: make(map[string]IndexDefinition)}
	authorizer := identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: owner.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}})
	service := NewService(nil, tables, memorySchemaReader{definition: definition}, authorizer).WithIndexRepository(indexes)
	created, err := service.CreateIndex(context.Background(), owner, IndexInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "index-1", Field: "title", Direction: IndexAscending})
	if err != nil || created.Name == "" {
		t.Fatalf("create index = %#v, %v", created, err)
	}
	if _, err := service.CreateIndex(context.Background(), owner, IndexInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "index-2", Field: "secret", Direction: IndexAscending}); !errors.Is(err, ErrIndexFieldNotAllowed) {
		t.Fatalf("unpublished field error = %v", err)
	}
	editor := owner
	editor.Subject = "editor"
	if _, err := service.CreateIndex(context.Background(), editor, IndexInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "index-3", Field: "count", Direction: IndexAscending}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("editor index authorization error = %v", err)
	}
	for number := 0; number < maxIndexesPerTable-1; number++ {
		indexes.values["tenant-1:workspace-1:table-1:seed-"+string(rune('a'+number))] = IndexDefinition{ID: "seed", TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", Field: "seed", Direction: IndexAscending}
	}
	if _, err := service.CreateIndex(context.Background(), owner, IndexInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ID: "index-limit", Field: "count", Direction: IndexDescending}); !errors.Is(err, ErrIndexLimit) {
		t.Fatalf("index limit error = %v", err)
	}
}
