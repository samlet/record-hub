package schema

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestSchemaHTTPMutationContract(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	registry := &memoryRegistry{definitions: make(map[string]Definition)}
	service := NewService(registry, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive,
	}}), &memoryReceipts{values: make(map[string]Receipt)}, &memoryAudit{})
	handler := NewHTTPHandler(service)

	unauthenticated := doSchemaRequest(handler, nil, http.MethodPost, "/api/v1/schemas", `{"tenantId":"tenant-1"}`, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}

	body := `{"tenantId":"tenant-1","workspaceId":"workspace-1","schemaId":"urn:record-hub:test:http","name":"HTTP Schema","version":1,"jsonSchema":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"},"semanticTypes":[]}`
	created := doSchemaRequest(handler, &principal, http.MethodPost, "/api/v1/schemas", body, map[string]string{"Idempotency-Key": "create-http-1"})
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"1"` {
		t.Fatalf("create response status=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	if repeat := doSchemaRequest(handler, &principal, http.MethodPost, "/api/v1/schemas", body, map[string]string{"Idempotency-Key": "create-http-1"}); repeat.Code != http.StatusCreated {
		t.Fatalf("idempotent repeat status = %d", repeat.Code)
	}
	conflictingBody := strings.Replace(body, `"HTTP Schema"`, `"Different"`, 1)
	if conflict := doSchemaRequest(handler, &principal, http.MethodPost, "/api/v1/schemas", conflictingBody, map[string]string{"Idempotency-Key": "create-http-1"}); conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status = %d", conflict.Code)
	}

	if missingIfMatch := doSchemaRequest(handler, &principal, http.MethodPut, "/api/v1/schemas/urn%3Arecord-hub%3Atest%3Ahttp/draft", body, map[string]string{"Idempotency-Key": "update-missing-if-match"}); missingIfMatch.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d", missingIfMatch.Code)
	}
	updateBody := strings.Replace(body, `"HTTP Schema"`, `"Updated HTTP Schema"`, 1)
	updated := doSchemaRequest(handler, &principal, http.MethodPut, "/api/v1/schemas/urn%3Arecord-hub%3Atest%3Ahttp/draft", updateBody, map[string]string{"Idempotency-Key": "update-http-1", "If-Match": `"1"`})
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != `"2"` {
		t.Fatalf("update response status=%d etag=%q body=%s", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
	publishBody := `{"tenantId":"tenant-1","workspaceId":"workspace-1","version":1}`
	published := doSchemaRequest(handler, &principal, http.MethodPost, "/api/v1/schemas/urn%3Arecord-hub%3Atest%3Ahttp/publish", publishBody, map[string]string{"Idempotency-Key": "publish-http-1", "If-Match": `"2"`})
	if published.Code != http.StatusOK || published.Header().Get("ETag") != `"3"` {
		t.Fatalf("publish response status=%d etag=%q body=%s", published.Code, published.Header().Get("ETag"), published.Body.String())
	}
	if afterPublish := doSchemaRequest(handler, &principal, http.MethodPut, "/api/v1/schemas/urn%3Arecord-hub%3Atest%3Ahttp/draft", updateBody, map[string]string{"Idempotency-Key": "update-after-publish-http", "If-Match": `"3"`}); afterPublish.Code != http.StatusConflict {
		t.Fatalf("published update status = %d", afterPublish.Code)
	}
}

func TestSchemaHTTPRejectsMalformedAndUnauthorizedRequests(t *testing.T) {
	owner := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	editor := owner
	editor.Subject = "editor-1"
	registry := &memoryRegistry{definitions: make(map[string]Definition)}
	service := NewService(registry, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: owner.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive,
	}}), &memoryReceipts{values: make(map[string]Receipt)}, &memoryAudit{})
	handler := NewHTTPHandler(service)
	if response := doSchemaRequest(handler, &owner, http.MethodPost, "/api/v1/schemas", `{"tenantId":"tenant-1","workspaceId":"workspace-1","schemaId":"x","name":"x","version":1,"jsonSchema":{},"unexpected":true}`, map[string]string{"Idempotency-Key": "bad-unknown"}); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", response.Code)
	}
	if response := doSchemaRequest(handler, &owner, http.MethodPost, "/api/v1/schemas", `{"tenantId":"tenant-1","workspaceId":"workspace-1","schemaId":"x","name":"x","version":1,"jsonSchema":{}}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency status = %d", response.Code)
	}
	if response := doSchemaRequest(handler, &editor, http.MethodPost, "/api/v1/schemas", `{"tenantId":"tenant-1","workspaceId":"workspace-1","schemaId":"x","name":"x","version":1,"jsonSchema":{}}`, map[string]string{"Idempotency-Key": "editor"}); response.Code != http.StatusForbidden {
		t.Fatalf("editor status = %d", response.Code)
	}
}

func doSchemaRequest(handler http.Handler, principal *identity.Principal, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
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

func TestSchemaHTTPErrorShape(t *testing.T) {
	response := doSchemaRequest(NewHTTPHandler(nil), nil, http.MethodPost, "/api/v1/schemas", `{}`, nil)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnauthorized || body.Error.Code != "AUTHENTICATION_REQUIRED" || body.Error.Message == "" {
		t.Fatalf("unexpected error response status=%d body=%s", response.Code, response.Body.String())
	}
}
