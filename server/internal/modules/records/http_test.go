package records

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
)

func TestRecordsHTTPWorkspaceAndTableContract(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	service := NewService(
		&memoryWorkspaceRepository{values: make(map[string]Workspace)},
		&memoryTableRepository{values: make(map[string]TableDefinition)},
		memorySchemaReader{definition: schema.Definition{TenantID: "tenant-1", SchemaID: "urn:record-hub:schema:published", Version: 1, Status: schema.StatusPublished}},
		identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}}),
	)
	handler := NewHTTPHandler(service)
	if response := doRecordsRequest(handler, nil, http.MethodPost, "/api/v1/workspaces", `{"tenantId":"tenant-1","id":"workspace-1","name":"Workspace"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated workspace status = %d", response.Code)
	}
	if response := doRecordsRequest(handler, &principal, http.MethodPost, "/api/v1/workspaces", `{"tenantId":"tenant-1","id":"workspace-1","name":"Workspace"}`); response.Code != http.StatusCreated {
		t.Fatalf("workspace create status = %d body=%s", response.Code, response.Body.String())
	}
	body := `{"tenantId":"tenant-1","id":"table-1","name":"Projects","kind":"CUSTOM","schemaId":"urn:record-hub:schema:published","schemaVersion":1}`
	if response := doRecordsRequest(handler, &principal, http.MethodPost, "/api/v1/workspaces/workspace-1/tables", body); response.Code != http.StatusCreated {
		t.Fatalf("table create status = %d body=%s", response.Code, response.Body.String())
	}
	response := doRecordsRequest(handler, &principal, http.MethodGet, "/api/v1/workspaces/workspace-1/tables?tenantId=tenant-1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("table list status = %d body=%s", response.Code, response.Body.String())
	}
	var list struct {
		Items []TableDefinition `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != "table-1" {
		t.Fatalf("unexpected table list: %#v", list.Items)
	}
}

func TestRecordsHTTPRejectsProjectionWithoutPolicy(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	service := NewService(&memoryWorkspaceRepository{values: make(map[string]Workspace)}, &memoryTableRepository{values: make(map[string]TableDefinition)}, memorySchemaReader{definition: schema.Definition{TenantID: "tenant-1", SchemaID: "urn:record-hub:schema:published", Version: 1, Status: schema.StatusPublished}}, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}}))
	handler := NewHTTPHandler(service)
	body := `{"tenantId":"tenant-1","id":"table-1","name":"Projection","kind":"PROJECTION","schemaId":"urn:record-hub:schema:published","schemaVersion":1}`
	response := doRecordsRequest(handler, &principal, http.MethodPost, "/api/v1/workspaces/workspace-1/tables", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("projection validation status = %d body=%s", response.Code, response.Body.String())
	}
}

func doRecordsRequest(handler http.Handler, principal *identity.Principal, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if principal != nil {
		request = request.WithContext(identity.WithPrincipal(context.Background(), *principal))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
