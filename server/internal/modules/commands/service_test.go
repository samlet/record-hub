package commands

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/binding"
	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type commandPolicyAuthorizer struct{ allow bool }

func (authorizer commandPolicyAuthorizer) Authorize(context.Context, identity.Principal, binding.BindingAuthorization) error {
	if authorizer.allow {
		return nil
	}
	return binding.ErrMachinePolicyDenied
}

type commandPublisher struct {
	subject  string
	envelope Envelope
	err      error
}

func (publisher *commandPublisher) Publish(_ context.Context, subject string, envelope Envelope) error {
	publisher.subject, publisher.envelope = subject, envelope
	return publisher.err
}

type commandAudit struct{ entries []audit.Entry }

func (writer *commandAudit) Append(_ context.Context, entry audit.Entry) error {
	writer.entries = append(writer.entries, entry)
	return nil
}

func commandPrincipal() identity.Principal {
	return identity.Principal{Kind: identity.PrincipalService, Issuer: "issuer", Subject: "fluxion", Audience: []string{"record-hub"}, Scopes: []string{"command.submit"}}
}

func commandService(t *testing.T, publisher Publisher) (*Service, *MemoryStore) {
	t.Helper()
	registry, err := NewStaticPolicyRegistry(Policy{ID: "project.annotate", TenantID: "tenant-1", WorkspaceID: "workspace-1", Purpose: "project-annotation", OwnerSystem: "fluxion", ResourceType: "PROJECT", Action: "project.annotate", ExpectedVersionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	service := NewService(registry, store, commandPolicyAuthorizer{allow: true}, &commandAudit{}).WithPublisher(publisher).WithClock(func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }).WithIDGenerator(func() (string, error) { return "cmd-fixed", nil })
	return service, store
}

func TestCommandSubmitDispatchesAndReplaysIdempotently(t *testing.T) {
	publisher := &commandPublisher{}
	service, store := commandService(t, publisher)
	version := int64(4)
	request := SubmitRequest{TenantID: "tenant-1", WorkspaceID: "workspace-1", PolicyID: "project.annotate", ResourceRef: "fluxion:PROJECT:project-1", ExpectedVersion: &version, Payload: []byte(`{"note":"safe"}`), IdempotencyKey: "op-1"}
	operation, replayed, err := service.Submit(context.Background(), commandPrincipal(), request)
	if err != nil || replayed {
		t.Fatalf("submit = %#v replayed=%v err=%v", operation, replayed, err)
	}
	if operation.Status != StatusDispatched || publisher.subject != "commands.fluxion.project.annotate.v1" || publisher.envelope.OperationID != "cmd-fixed" {
		t.Fatalf("dispatch = %#v subject=%s", publisher.envelope, publisher.subject)
	}
	replayedOperation, replayed, err := service.Submit(context.Background(), commandPrincipal(), request)
	if err != nil || !replayed || replayedOperation.ID != operation.ID {
		t.Fatalf("replay = %#v replayed=%v err=%v", replayedOperation, replayed, err)
	}
	if len(store.values) != 1 {
		t.Fatalf("stored operations = %d, want 1", len(store.values))
	}
	request.Payload = []byte(`{"note":"changed"}`)
	if _, _, err := service.Submit(context.Background(), commandPrincipal(), request); !errors.Is(err, ErrCommandIdempotency) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestCommandRejectsPolicyBoundaryAndExpectedVersion(t *testing.T) {
	service, _ := commandService(t, nil)
	request := SubmitRequest{TenantID: "tenant-1", WorkspaceID: "workspace-1", PolicyID: "project.annotate", ResourceRef: "bids:PROJECT:project-1", Payload: []byte(`{"note":"safe"}`), IdempotencyKey: "op-2"}
	if _, _, err := service.Submit(context.Background(), commandPrincipal(), request); !errors.Is(err, ErrCommandPolicyDenied) {
		t.Fatalf("cross-owner resource error = %v", err)
	}
	request.ResourceRef = "fluxion:PROJECT:project-1"
	if _, _, err := service.Submit(context.Background(), commandPrincipal(), request); !errors.Is(err, ErrCommandExpectedVersion) {
		t.Fatalf("missing expected version error = %v", err)
	}
}

func TestCommandHTTPMapsReplayAndPolicyErrors(t *testing.T) {
	service, _ := commandService(t, nil)
	handler := NewHTTPHandler(service)
	principal := commandPrincipal()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(`{"tenantId":"tenant-1","workspaceId":"workspace-1","policyId":"project.annotate","resourceRef":"fluxion:PROJECT:project-1","expectedVersion":4,"payload":{"note":"safe"}}`))
	request.Header.Set("Idempotency-Key", "http-op")
	request = request.WithContext(identity.WithPrincipal(request.Context(), principal))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), "ACCEPTED") {
		t.Fatalf("submit status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(`{"tenantId":"tenant-1","workspaceId":"workspace-1","policyId":"project.annotate","resourceRef":"bids:PROJECT:project-1","expectedVersion":4,"payload":{"note":"safe"}}`))
	request.Header.Set("Idempotency-Key", "http-op-2")
	request = request.WithContext(identity.WithPrincipal(request.Context(), principal))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "COMMAND_POLICY_DENIED") {
		t.Fatalf("policy status=%d body=%s", response.Code, response.Body.String())
	}
}
