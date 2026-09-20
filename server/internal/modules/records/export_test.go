package records

import (
	"context"
	"strings"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestExportRecordsRedactsSensitiveViewFieldsAndAuditsReceipt(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	membership := identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}
	now := timeNow()
	view := ViewDefinition{ID: "view-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", Name: "Export", Columns: []string{"data.title", "data.secret", "tags"}, Version: 1, CreatedBy: principal.IdentityKey(), UpdatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	viewStore := &memoryViewRepository{views: map[string]ViewDefinition{"tenant-1:workspace-1:table-1:view-1": view}, records: []Record{{ID: "record-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", Data: mustRecordData(t, `{"title":"hello","secret":"must-drop"}`), Tags: []string{"public"}, RecordVersion: 1, SchemaVersion: 1, CreatedAt: now, UpdatedAt: now}}}
	audits := &memoryRecordAudit{}
	service := NewRecordService(nil, &memoryTableRepository{values: map[string]TableDefinition{}}, memorySchemaReader{}, identity.NewAuthorizer(serviceMembershipReader{membership: membership}), &memoryRecordRepository{values: map[string]Record{}}, &memoryRecordReceipts{values: map[string]RecordReceipt{}}, audits).WithViewRepository(viewStore)
	result, err := service.ExportRecords(context.Background(), principal, ExportInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "table-1", ViewID: "view-1", Format: "jsonl", RequestID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 || result.ContentType != "application/x-ndjson" || len(result.RedactedFields) != 1 || result.RedactedFields[0] != "data.secret" {
		t.Fatalf("unexpected export metadata: %#v", result)
	}
	if !strings.Contains(string(result.Body), `"data.title":"hello"`) || strings.Contains(string(result.Body), "must-drop") {
		t.Fatalf("redaction failed: %s", result.Body)
	}
	if len(audits.entries) != 1 || audits.entries[0].ResourceType != "redacted_export" || len(audits.entries[0].RedactedFields) != 1 {
		t.Fatalf("export audit entry = %#v", audits.entries)
	}
}
