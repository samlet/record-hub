package records

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func projectionRecordService(t *testing.T) (*Service, identity.Principal) {
	t.Helper()
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	definition := recordSchemaDefinition(t)
	tables := &memoryTableRepository{values: map[string]TableDefinition{"tenant-1:workspace-1:projection-1": {
		ID: "projection-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Name: "Projects", Kind: TableKindProjection,
		SchemaID: definition.SchemaID, SchemaVersion: definition.Version,
		SourcePolicy: &SourcePolicy{System: "fluxion", Type: "PROJECT", AllowFields: []string{"title"}},
	}}}
	service := NewRecordService(nil, tables, memorySchemaReader{definition: definition}, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive,
	}}), &memoryRecordRepository{values: make(map[string]Record)}, &memoryRecordReceipts{values: make(map[string]RecordReceipt)}, &memoryRecordAudit{})
	return service, principal
}

func TestRecordServiceRejectsEveryGenericProjectionWrite(t *testing.T) {
	service, principal := projectionRecordService(t)
	ctx := context.Background()
	if _, err := service.CreateRecord(ctx, principal, RecordInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "projection-1", ID: "record-1", Data: mustRecordData(t, `{"title":"created"}`), IdempotencyKey: "projection-create"}); !errors.Is(err, ErrProjectionReadOnly) {
		t.Fatalf("projection create error = %v", err)
	}
	if _, err := service.UpdateRecord(ctx, principal, RecordInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "projection-1", ID: "record-1", Data: mustRecordData(t, `{"title":"updated"}`), IdempotencyKey: "projection-update"}, 1); !errors.Is(err, ErrProjectionReadOnly) {
		t.Fatalf("projection update error = %v", err)
	}
	if _, err := service.DeleteRecord(ctx, principal, RecordDeleteInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "projection-1", RecordID: "record-1", IdempotencyKey: "projection-delete"}, 1); !errors.Is(err, ErrProjectionReadOnly) {
		t.Fatalf("projection delete error = %v", err)
	}
}

func TestProjectionHTTPWriteMatrix(t *testing.T) {
	service, principal := projectionRecordService(t)
	handler := NewHTTPHandler(service)
	created := doRecordsRequestWithHeaders(handler, &principal, http.MethodPost, "/api/v1/tables/projection-1/records", `{"tenantId":"tenant-1","workspaceId":"workspace-1","id":"record-1","data":{"title":"created"}}`, map[string]string{"Idempotency-Key": "projection-create"})
	updated := doRecordsRequestWithHeaders(handler, &principal, http.MethodPatch, "/api/v1/records/record-1", `{"tenantId":"tenant-1","workspaceId":"workspace-1","tableId":"projection-1","data":{"title":"updated"}}`, map[string]string{"Idempotency-Key": "projection-update", "If-Match": `"1"`})
	deleted := doRecordsRequestWithHeaders(handler, &principal, http.MethodDelete, "/api/v1/records/record-1?tenantId=tenant-1&workspaceId=workspace-1&tableId=projection-1", "", map[string]string{"Idempotency-Key": "projection-delete", "If-Match": `"1"`})
	for operation, recorder := range map[string]*httptest.ResponseRecorder{"create": created, "update": updated, "delete": deleted} {
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"PROJECTION_READ_ONLY"`) {
			t.Fatalf("projection %s status=%d body=%s", operation, recorder.Code, recorder.Body.String())
		}
	}
}
