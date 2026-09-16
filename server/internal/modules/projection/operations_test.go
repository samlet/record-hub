package projection

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/observability"
)

type memoryOperationsReader struct {
	snapshot OperationsSnapshot
	err      error
	query    OperationsQuery
}

func (reader *memoryOperationsReader) Snapshot(_ context.Context, query OperationsQuery) (OperationsSnapshot, error) {
	reader.query = query
	return reader.snapshot, reader.err
}

type operationsMembershipReader struct {
	membership identity.WorkspaceMembership
}

func (reader operationsMembershipReader) FindMembership(context.Context, identity.IdentityKey, string, string) (identity.WorkspaceMembership, error) {
	return reader.membership, nil
}

func operationsPrincipal(role identity.Role) identity.Principal {
	return identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "operator"}
}

func operationsService(t *testing.T, reader OperationsReader, role identity.Role) (*OperationsService, identity.Principal) {
	t.Helper()
	principal := operationsPrincipal(role)
	authorizer := identity.NewAuthorizer(operationsMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: role, Status: identity.MembershipActive,
	}})
	return NewOperationsService(reader, authorizer), principal
}

func validOperationsSnapshot() OperationsSnapshot {
	now := time.Now().UTC().Truncate(time.Second)
	return OperationsSnapshot{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1", GeneratedAt: now,
		Inbox: InboxOperationsSummary{Processing: 2, Applied: 8, Rejected: 1, Failed: 1},
		Checkpoints: []ProjectionCheckpoint{{
			TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1", SourceSystem: "approver", AggregateType: "Application", AggregateID: "app-123", SourceVersion: 8, LastEventID: "event-8", SyncedAt: now, Status: CheckpointGap,
		}},
	}
}

func TestOperationsServiceAuthorizesScopedReadAndReturnsSafeSnapshot(t *testing.T) {
	reader := &memoryOperationsReader{snapshot: validOperationsSnapshot()}
	service, principal := operationsService(t, reader, identity.RoleViewer)
	snapshot, err := service.Snapshot(context.Background(), principal, OperationsQuery{TenantID: " tenant-1 ", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Inbox.Processing != 2 || len(snapshot.Checkpoints) != 1 || snapshot.Checkpoints[0].Status != CheckpointGap {
		t.Fatalf("unexpected operations snapshot: %#v", snapshot)
	}
	if reader.query.TenantID != "tenant-1" || reader.query.Limit != 1 {
		t.Fatalf("query was not normalized: %#v", reader.query)
	}

	for _, role := range []identity.Role{"", identity.Role("OTHER")} {
		unauthorizedService, unauthorizedPrincipal := operationsService(t, reader, role)
		if _, err := unauthorizedService.Snapshot(context.Background(), unauthorizedPrincipal, OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1"}); !errors.Is(err, identity.ErrForbidden) {
			t.Fatalf("role %q operations access error = %v", role, err)
		}
	}
}

func TestOperationsServiceUpdatesBoundedBacklogMetrics(t *testing.T) {
	reader := &memoryOperationsReader{snapshot: validOperationsSnapshot()}
	service, principal := operationsService(t, reader, identity.RoleViewer)
	metrics := observability.NewRegistry()
	service.WithMetrics(metrics)
	if _, err := service.Snapshot(context.Background(), principal, OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1"}); err != nil {
		t.Fatal(err)
	}
	body := metrics.Render()
	if !strings.Contains(body, `record_hub_projection_backlog{consumer="record-hub-approver-v1"} 2.000000`) || !strings.Contains(body, `record_hub_projection_failures{consumer="record-hub-approver-v1"} 2.000000`) {
		t.Fatalf("backlog metrics missing: %s", body)
	}
}

func TestOperationsServiceRejectsUnboundedQueryAndReaderErrors(t *testing.T) {
	reader := &memoryOperationsReader{snapshot: validOperationsSnapshot(), err: errors.New("database unavailable")}
	service, principal := operationsService(t, reader, identity.RoleOwner)
	if _, err := service.Snapshot(context.Background(), principal, OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1", Limit: 101}); !errors.Is(err, ErrOperationsQueryInvalid) {
		t.Fatalf("unbounded query error = %v", err)
	}
	if _, err := service.Snapshot(context.Background(), principal, OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1"}); !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("reader error = %v", err)
	}
}

func TestOperationsServiceRejectsUnknownConsumerAndReaderScopeMismatch(t *testing.T) {
	reader := &memoryOperationsReader{snapshot: validOperationsSnapshot()}
	service, principal := operationsService(t, reader, identity.RoleViewer)
	if _, err := service.Snapshot(context.Background(), principal, OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "events.anything.>"}); !errors.Is(err, ErrOperationsQueryInvalid) {
		t.Fatalf("unknown consumer error = %v", err)
	}
	reader.snapshot = validOperationsSnapshot()
	reader.snapshot.WorkspaceID = "workspace-other"
	if _, err := service.Snapshot(context.Background(), principal, OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1"}); !errors.Is(err, ErrOperationsQueryInvalid) {
		t.Fatalf("scope mismatch error = %v", err)
	}
	reader.snapshot = validOperationsSnapshot()
	reader.snapshot.Checkpoints[0].Consumer = "record-hub-fluxion-v1"
	if _, err := service.Snapshot(context.Background(), principal, OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1"}); !errors.Is(err, ErrOperationsQueryInvalid) {
		t.Fatalf("checkpoint consumer mismatch error = %v", err)
	}
}

func TestOperationsHTTPReturnsSafeMetadataOnly(t *testing.T) {
	reader := &memoryOperationsReader{snapshot: validOperationsSnapshot()}
	service, principal := operationsService(t, reader, identity.RoleViewer)
	handler := NewOperationsHTTPHandler(service)

	if response := doOperationsRequest(handler, nil, http.MethodGet, "/api/v1/operations/events?tenantId=tenant-1&workspaceId=workspace-1&consumer=record-hub-approver-v1", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	if response := doOperationsRequest(handler, &principal, http.MethodGet, "/api/v1/operations/events?tenantId=tenant-1&workspaceId=workspace-1&consumer=record-hub-approver-v1&limit=101", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d body=%s", response.Code, response.Body.String())
	}
	response := doOperationsRequest(handler, &principal, http.MethodGet, "/api/v1/operations/events?tenantId=tenant-1&workspaceId=workspace-1&consumer=record-hub-approver-v1&limit=1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("operations status = %d body=%s", response.Code, response.Body.String())
	}
	var body OperationsSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Inbox.Processing != 2 || len(body.Checkpoints) != 1 || body.Checkpoints[0].Status != CheckpointGap {
		t.Fatalf("unexpected operations body: %#v", body)
	}
	if strings.Contains(response.Body.String(), "payload") || strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "quotation") {
		t.Fatalf("operations response leaked sensitive fields: %s", response.Body.String())
	}
}

func TestOperationsPageIsAuthenticatedAndPayloadFree(t *testing.T) {
	reader := &memoryOperationsReader{snapshot: validOperationsSnapshot()}
	service, principal := operationsService(t, reader, identity.RoleViewer)
	handler := NewOperationsHTTPHandler(service)
	if response := doOperationsRequest(handler, nil, http.MethodGet, "/operations/events", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated page status = %d", response.Code)
	}
	response := doOperationsRequest(handler, &principal, http.MethodGet, "/operations/events", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/api/v1/operations/events") || !strings.Contains(response.Body.String(), "record-hub-bids-projection-v1") || strings.Contains(response.Body.String(), "raw payload") {
		t.Fatalf("operations page = status %d body=%s", response.Code, response.Body.String())
	}
}

func TestMongoOperationsRepositoryWithoutDatabaseFailsClosed(t *testing.T) {
	if _, err := (&MongoProjectionRepository{}).Snapshot(context.Background(), OperationsQuery{TenantID: "tenant-1", WorkspaceID: "workspace-1", Consumer: "record-hub-approver-v1"}); !errors.Is(err, ErrOperationsUnavailable) {
		t.Fatalf("unconfigured repository error = %v", err)
	}
}

func doOperationsRequest(handler http.Handler, principal *identity.Principal, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if principal != nil {
		request = request.WithContext(identity.WithPrincipal(context.Background(), *principal))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
