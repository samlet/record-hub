package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type memoryOperationsReader struct {
	summary OperationsSummary
}

func (reader *memoryOperationsReader) Snapshot(_ context.Context, _ OperationsQuery) (OperationsSummary, error) {
	return reader.summary, nil
}

type operationsMembershipReader struct{}

func (operationsMembershipReader) FindMembership(_ context.Context, principal identity.IdentityKey, tenantID, workspaceID string) (identity.WorkspaceMembership, error) {
	return identity.WorkspaceMembership{TenantID: tenantID, WorkspaceID: workspaceID, Identity: principal, Role: identity.RoleOperator, Status: identity.MembershipActive}, nil
}

func commandOperationsPrincipal() identity.Principal {
	return identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "operator"}
}

func TestCommandOperationsReturnsScopedSafeSummary(t *testing.T) {
	reader := &memoryOperationsReader{summary: OperationsSummary{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Accepted: 1, Dispatched: 2, Succeeded: 8, Rejected: 1, Failed: 1, Expired: 0,
		GeneratedAt: time.Now().UTC(),
	}}
	service := NewOperationsService(reader, identity.NewAuthorizer(operationsMembershipReader{}))
	summary, err := service.Snapshot(context.Background(), commandOperationsPrincipal(), OperationsQuery{TenantID: " tenant-1 ", WorkspaceID: "workspace-1"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Accepted != 1 || summary.Dispatched != 2 || summary.Succeeded != 8 {
		t.Fatalf("unexpected command summary: %#v", summary)
	}
}

func TestCommandOperationsHTTPRejectsUnauthenticatedAndDoesNotExposePayload(t *testing.T) {
	reader := &memoryOperationsReader{summary: OperationsSummary{TenantID: "tenant-1", WorkspaceID: "workspace-1", Succeeded: 1, GeneratedAt: time.Now().UTC()}}
	handler := NewOperationsHTTPHandler(NewOperationsService(reader, identity.NewAuthorizer(operationsMembershipReader{})))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/operations/commands?tenantId=tenant-1&workspaceId=workspace-1", nil)
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, request)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}

	request = request.WithContext(identity.WithPrincipal(context.Background(), commandOperationsPrincipal()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("summary status = %d body=%s", response.Code, response.Body.String())
	}
	var summary OperationsSummary
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "payload") || strings.Contains(response.Body.String(), "operationId") || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("command summary leaked sensitive fields: %s", response.Body.String())
	}
}
