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

func TestRecordsHTTPRecordCRUDContract(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	definition := recordSchemaDefinition(t)
	tables := &memoryTableRepository{values: map[string]TableDefinition{"tenant-1:workspace-1:table-1": {ID: "table-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Name: "Custom", Kind: TableKindCustom, SchemaID: definition.SchemaID, SchemaVersion: 1}}}
	service := NewRecordService(nil, tables, memorySchemaReader{definition: definition}, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}}), &memoryRecordRepository{values: make(map[string]Record)}, &memoryRecordReceipts{values: make(map[string]RecordReceipt)}, &memoryRecordAudit{})
	handler := NewHTTPHandler(service)
	createBody := `{"tenantId":"tenant-1","workspaceId":"workspace-1","id":"record-1","data":{"title":"hello","count":1},"tags":["urgent"]}`
	created := doRecordsRequestWithHeaders(handler, &principal, http.MethodPost, "/api/v1/tables/table-1/records", createBody, map[string]string{"Idempotency-Key": "record-create-1"})
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"1"` || !strings.Contains(created.Body.String(), `"title":"hello"`) {
		t.Fatalf("record create status=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	updateBody := `{"tenantId":"tenant-1","workspaceId":"workspace-1","tableId":"table-1","data":{"title":"updated","count":2},"tags":[]}`
	updated := doRecordsRequestWithHeaders(handler, &principal, http.MethodPatch, "/api/v1/records/record-1", updateBody, map[string]string{"Idempotency-Key": "record-update-1", "If-Match": `"1"`})
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != `"2"` {
		t.Fatalf("record update status=%d etag=%q body=%s", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
	read := doRecordsRequest(handler, &principal, http.MethodGet, "/api/v1/records/record-1?tenantId=tenant-1&workspaceId=workspace-1", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"recordVersion":2`) {
		t.Fatalf("record get status=%d body=%s", read.Code, read.Body.String())
	}
	deleted := doRecordsRequestWithHeaders(handler, &principal, http.MethodDelete, "/api/v1/records/record-1?tenantId=tenant-1&workspaceId=workspace-1&tableId=table-1", "", map[string]string{"Idempotency-Key": "record-delete-1", "If-Match": `"2"`})
	if deleted.Code != http.StatusOK {
		t.Fatalf("record delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestRecordsHTTPViewAndQueryContract(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	definition := recordSchemaDefinition(t)
	tables := &memoryTableRepository{values: map[string]TableDefinition{"tenant-1:workspace-1:table-1": {ID: "table-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Name: "Custom", Kind: TableKindCustom, SchemaID: definition.SchemaID, SchemaVersion: 1}}}
	recordStore := &memoryRecordRepository{values: make(map[string]Record)}
	viewStore := &memoryViewRepository{views: make(map[string]ViewDefinition)}
	indexStore := &memoryIndexRepository{values: make(map[string]IndexDefinition)}
	service := NewRecordService(nil, tables, memorySchemaReader{definition: definition}, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}}), recordStore, &memoryRecordReceipts{values: make(map[string]RecordReceipt)}, &memoryRecordAudit{}).WithViewRepository(viewStore)
	service.WithIndexRepository(indexStore)
	handler := NewHTTPHandler(service)
	createRecord := doRecordsRequestWithHeaders(handler, &principal, http.MethodPost, "/api/v1/tables/table-1/records", `{"tenantId":"tenant-1","workspaceId":"workspace-1","id":"record-1","data":{"title":"hello","count":1}}`, map[string]string{"Idempotency-Key": "view-record-1"})
	if createRecord.Code != http.StatusCreated {
		t.Fatalf("record setup status=%d body=%s", createRecord.Code, createRecord.Body.String())
	}
	viewStore.records = []Record{recordStore.values["tenant-1:workspace-1:record-1"]}
	viewBody := `{"tenantId":"tenant-1","workspaceId":"workspace-1","id":"view-1","name":"Hello","columns":["title"],"filters":[{"field":"title","operator":"contains","value":"hello"}],"sorts":[{"field":"updatedAt","direction":"desc"}]}`
	created := doRecordsRequest(handler, &principal, http.MethodPost, "/api/v1/tables/table-1/views", viewBody)
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"1"` {
		t.Fatalf("view create status=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	updateViewBody := `{"tenantId":"tenant-1","workspaceId":"workspace-1","name":"Hello updated","columns":["title","count"],"filters":[{"field":"title","operator":"contains","value":"hello"}],"sorts":[{"field":"count","direction":"desc"}]}`
	updatedView := doRecordsRequestWithHeaders(handler, &principal, http.MethodPatch, "/api/v1/tables/table-1/views/view-1", updateViewBody, map[string]string{"If-Match": `"1"`})
	if updatedView.Code != http.StatusOK || updatedView.Header().Get("ETag") != `"2"` || !strings.Contains(updatedView.Body.String(), `"Hello updated"`) {
		t.Fatalf("view update status=%d etag=%q body=%s", updatedView.Code, updatedView.Header().Get("ETag"), updatedView.Body.String())
	}
	staleView := doRecordsRequestWithHeaders(handler, &principal, http.MethodPatch, "/api/v1/tables/table-1/views/view-1", updateViewBody, map[string]string{"If-Match": `"1"`})
	if staleView.Code != http.StatusConflict || !strings.Contains(staleView.Body.String(), `VIEW_VERSION_CONFLICT`) {
		t.Fatalf("stale view update status=%d body=%s", staleView.Code, staleView.Body.String())
	}
	listed := doRecordsRequest(handler, &principal, http.MethodGet, "/api/v1/tables/table-1/views?tenantId=tenant-1&workspaceId=workspace-1", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"view-1"`) {
		t.Fatalf("view list status=%d body=%s", listed.Code, listed.Body.String())
	}
	page := doRecordsRequest(handler, &principal, http.MethodGet, "/api/v1/tables/table-1/records?tenantId=tenant-1&workspaceId=workspace-1&viewId=view-1&limit=1", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"record-1"`) {
		t.Fatalf("view records status=%d body=%s", page.Code, page.Body.String())
	}
	indexBody := `{"tenantId":"tenant-1","workspaceId":"workspace-1","id":"index-1","field":"title","direction":"asc"}`
	createdIndex := doRecordsRequest(handler, &principal, http.MethodPost, "/api/v1/tables/table-1/indexes", indexBody)
	if createdIndex.Code != http.StatusCreated || !strings.Contains(createdIndex.Body.String(), `"field":"title"`) {
		t.Fatalf("index create status=%d body=%s", createdIndex.Code, createdIndex.Body.String())
	}
	listedIndexes := doRecordsRequest(handler, &principal, http.MethodGet, "/api/v1/tables/table-1/indexes?tenantId=tenant-1&workspaceId=workspace-1", "")
	if listedIndexes.Code != http.StatusOK || !strings.Contains(listedIndexes.Body.String(), `"index-1"`) {
		t.Fatalf("index list status=%d body=%s", listedIndexes.Code, listedIndexes.Body.String())
	}
}

func doRecordsRequest(handler http.Handler, principal *identity.Principal, method, path, body string) *httptest.ResponseRecorder {
	return doRecordsRequestWithHeaders(handler, principal, method, path, body, nil)
}

func doRecordsRequestWithHeaders(handler http.Handler, principal *identity.Principal, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if principal != nil {
		request = request.WithContext(identity.WithPrincipal(context.Background(), *principal))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
