package projection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type projectorInbox struct{ event InboxEvent }

func (inbox *projectorInbox) Claim(_ context.Context, claim InboxClaim) (InboxClaimResult, error) {
	if inbox.event.EventID != "" {
		return InboxClaimResult{Event: inbox.event, Duplicate: true}, nil
	}
	inbox.event = InboxEvent{EventID: claim.EventID, Consumer: claim.Consumer, Subject: claim.Subject, TenantID: claim.TenantID, WorkspaceID: claim.WorkspaceID, PayloadHash: payloadHash(claim.Payload), Status: InboxProcessing, ReceivedAt: claim.ReceivedAt}
	return InboxClaimResult{Event: inbox.event}, nil
}
func (*projectorInbox) Get(context.Context, string, string) (InboxEvent, error) {
	return InboxEvent{}, ErrInboxNotFound
}
func (*projectorInbox) MarkApplied(context.Context, string, string, time.Time) error { return nil }
func (*projectorInbox) MarkRejected(context.Context, string, string, string, time.Time) error {
	return nil
}

type projectorRepository struct{ applied ProjectionApply }

func (repository *projectorRepository) Apply(_ context.Context, input ProjectionApply) error {
	repository.applied = input
	return nil
}

func TestSummaryProjectorBuildsScopedProjectionApply(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(registry); err != nil {
		t.Fatal(err)
	}
	inbox := &projectorInbox{}
	repository := &projectorRepository{}
	projector, err := NewSummaryProjector(registry, inbox, repository, "workspace-fallback")
	if err != nil {
		t.Fatal(err)
	}
	projector.now = func() time.Time { return time.Date(2026, time.September, 16, 1, 0, 0, 0, time.UTC) }
	raw := summaryProjectorEnvelope(t, map[string]any{"applicationId": "app-1", "title": "Review", "status": "OPEN", "processRef": "wf-1", "updatedAt": "2026-09-15T15:30:00Z", "version": 3})
	if err := projector.Handle(context.Background(), "events.approver.application.summary-changed.v1", raw); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	apply := repository.applied
	if apply.Record.TenantID != "tenant-1" || apply.Record.WorkspaceID != "workspace-fallback" || apply.Checkpoint.SourceVersion != 3 || apply.InboxEvent.Status != InboxProcessing {
		t.Fatalf("projection apply = %+v", apply)
	}
	if apply.Record.Source == nil || apply.Record.Source.System != "approver" || apply.Record.Source.ID != "app-1" {
		t.Fatalf("record source = %+v", apply.Record.Source)
	}
	if apply.Record.ID == "app-1" || apply.Audit.ResourceID != apply.Record.ID {
		t.Fatalf("projection record identity = %q audit=%q", apply.Record.ID, apply.Audit.ResourceID)
	}
}

func TestSummaryProjectorRequiresExplicitWorkspaceScope(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(registry); err != nil {
		t.Fatal(err)
	}
	projector, err := NewSummaryProjector(registry, &projectorInbox{}, &projectorRepository{}, "")
	if err != nil {
		t.Fatal(err)
	}
	raw := summaryProjectorEnvelope(t, map[string]any{"applicationId": "app-1", "title": "Review", "status": "OPEN", "processRef": "wf-1", "updatedAt": "2026-09-15T15:30:00Z", "version": 1})
	if err := projector.Handle(context.Background(), "events.approver.application.summary-changed.v1", raw); err == nil || !strings.Contains(err.Error(), "workspace scope is missing") {
		t.Fatalf("Handle() error = %v, want deterministic workspace rejection", err)
	}
}

func summaryProjectorEnvelope(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"eventId": "018f47a5-8b77-7c5a-9c56-38db8aa4b180", "kind": "event", "eventType": "approver.application.summary-changed", "schemaVersion": 1,
		"sourceSystem": "approver", "tenantId": "tenant-1", "aggregateType": "Application", "aggregateId": "app-1", "aggregateVersion": payload["version"],
		"occurredAt": "2026-09-15T15:30:00Z", "payload": payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func payloadHash(raw []byte) string {
	// The inbox implementation computes the same hash; this fixture helper
	// only needs a stable non-empty value for the fake repository.
	return "sha256:projector-test"
}
