package schema

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestMigrationPlanHTTPContract(t *testing.T) {
	owner := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	editor := owner
	editor.Subject = "editor-1"
	membership := identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: owner.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}
	now := time.Now().UTC()
	registry := &memoryRegistry{definitions: map[string]Definition{
		schemaKey("tenant-1", "schema-1", 1): {TenantID: "tenant-1", SchemaID: "schema-1", Name: "v1", Version: 1, Revision: 2, Status: StatusPublished, JSONSchema: migrationSchemaRaw(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"name":{"type":"string"}}}`), ContentHash: "sha256:published", CreatedBy: owner.IdentityKey(), CreatedAt: now, UpdatedAt: now},
		schemaKey("tenant-1", "schema-1", 2): {TenantID: "tenant-1", SchemaID: "schema-1", Name: "v2", Version: 2, Revision: 1, Status: StatusDraft, JSONSchema: migrationSchemaRaw(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"name":{"type":"string"},"description":{"type":"string"}}}`), CreatedBy: owner.IdentityKey(), CreatedAt: now, UpdatedAt: now},
	}}
	service := NewMigrationService(registry, &memoryMigrationPlanStore{plans: map[string]MigrationPlan{}}, identity.NewAuthorizer(serviceMembershipReader{membership: membership}), &memoryMigrationReceipts{values: map[string]MigrationPlanReceipt{}}, &memoryAudit{})
	handler := NewHTTPHandler(nil, service)

	if response := doSchemaRequest(handler, nil, http.MethodPost, "/api/v1/schemas/schema-1/migration-plans", `{}`, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	body := `{"tenantId":"tenant-1","workspaceId":"workspace-1","fromVersion":1,"toVersion":2,"mode":"DRY_RUN","failureSampleLimit":20}`
	if response := doSchemaRequest(handler, &owner, http.MethodPost, "/api/v1/schemas/schema-1/migration-plans", body, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency status = %d", response.Code)
	}
	created := doSchemaRequest(handler, &owner, http.MethodPost, "/api/v1/schemas/schema-1/migration-plans", body, map[string]string{"Idempotency-Key": "migration-1"})
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"1"` || !strings.Contains(created.Body.String(), `"status":"DRAFT"`) {
		t.Fatalf("create status=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	repeated := doSchemaRequest(handler, &owner, http.MethodPost, "/api/v1/schemas/schema-1/migration-plans", body, map[string]string{"Idempotency-Key": "migration-1"})
	if repeated.Code != http.StatusCreated || repeated.Body.String() == "" {
		t.Fatalf("repeat status=%d body=%s", repeated.Code, repeated.Body.String())
	}
	if response := doSchemaRequest(handler, &editor, http.MethodPost, "/api/v1/schemas/schema-1/migration-plans", body, map[string]string{"Idempotency-Key": "editor"}); response.Code != http.StatusForbidden {
		t.Fatalf("editor status = %d", response.Code)
	}

	var createdBody MigrationPlan
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	getPath := "/api/v1/schemas/schema-1/migration-plans/" + createdBody.ID + "?tenantId=tenant-1&workspaceId=workspace-1"
	if response := doSchemaRequest(handler, &owner, http.MethodGet, getPath, "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), createdBody.ID) {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
	cancelPath := "/api/v1/schemas/schema-1/migration-plans/" + createdBody.ID + "/cancel"
	if response := doSchemaRequest(handler, &owner, http.MethodPost, cancelPath, `{"tenantId":"tenant-1","workspaceId":"workspace-1"}`, map[string]string{"Idempotency-Key": "cancel-1"}); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d", response.Code)
	}
	cancelled := doSchemaRequest(handler, &owner, http.MethodPost, cancelPath, `{"tenantId":"tenant-1","workspaceId":"workspace-1"}`, map[string]string{"Idempotency-Key": "cancel-1", "If-Match": `"1"`})
	if cancelled.Code != http.StatusOK || cancelled.Header().Get("ETag") != `"2"` || !strings.Contains(cancelled.Body.String(), `"status":"CANCELLED"`) {
		t.Fatalf("cancel status=%d etag=%q body=%s", cancelled.Code, cancelled.Header().Get("ETag"), cancelled.Body.String())
	}
}
