package projection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
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

func TestSummaryProjectorBuildsSafeApprovalAssociationProjection(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(registry); err != nil {
		t.Fatal(err)
	}
	repository := &projectorRepository{}
	projector, err := NewSummaryProjector(registry, &projectorInbox{}, repository, "workspace-approval")
	if err != nil {
		t.Fatal(err)
	}
	projector.now = func() time.Time { return time.Date(2026, time.September, 19, 1, 0, 0, 0, time.UTC) }
	payload := map[string]any{
		"applicationRef":     "approver:APPLICATION:apr-p3-fluxion-0001",
		"projectRef":         "fluxion:PROJECT:11111111-1111-1111-1111-111111111111",
		"status":             "APPROVED",
		"workflowRef":        map[string]any{"workflowId": "project-11111111", "runId": "run-p3-0001"},
		"proposalHash":       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"dispatchGeneration": 3,
		"decisionVersion":    1,
		"freshness":          map[string]any{"sourceEventId": "apr-result-p3-fluxion-0001", "sourceVersion": 1, "observedAt": "2026-09-19T00:00:00Z"},
		"updatedAt":          "2026-09-19T00:00:00Z",
		"version":            1,
	}
	raw, err := json.Marshal(map[string]any{
		"eventId": "018f47a5-8b77-7c5a-9c56-38db8aa4b181", "kind": "event", "eventType": "approver.dispatch-approval.summary-changed", "schemaVersion": 1,
		"sourceSystem": "approver", "tenantId": "tenant-1", "aggregateType": "Approval", "aggregateId": "apr-p3-fluxion-0001", "aggregateVersion": 1,
		"occurredAt": "2026-09-19T00:00:00Z", "payload": payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := projector.Handle(context.Background(), "events.approver.dispatch-approval.summary-changed.v1", raw); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	apply := repository.applied
	if apply.Record.TableID != "projection-approver-approval-summary" || apply.Record.SchemaID != "urn:record-hub:summary:approval:v1" {
		t.Fatalf("approval projection target = table %q schema %q", apply.Record.TableID, apply.Record.SchemaID)
	}
	if apply.Record.Source == nil || apply.Record.Source.ID != "apr-p3-fluxion-0001" || apply.Record.Source.Type != "approval" {
		t.Fatalf("approval projection source = %+v", apply.Record.Source)
	}
	var document map[string]any
	if err := bson.Unmarshal(apply.Record.Data, &document); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"customer", "candidate", "workerRef", "address", "phone", "bidAmount", "fileUrl"} {
		if _, present := document[forbidden]; present {
			t.Fatalf("approval projection contains forbidden field %q: %#v", forbidden, document)
		}
	}
}

func TestSummaryProjectorPrefersTenantWorkspaceMappingOverGlobalFallback(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(registry); err != nil {
		t.Fatal(err)
	}
	repository := &projectorRepository{}
	projector, err := NewSummaryProjector(registry, &projectorInbox{}, repository, "workspace-global")
	if err != nil {
		t.Fatal(err)
	}
	projector.WithWorkspaceMappings(map[string]string{"tenant-1": "workspace-mapped"})
	raw := summaryProjectorEnvelope(t, map[string]any{"applicationId": "app-1", "title": "Review", "status": "OPEN", "processRef": "wf-1", "updatedAt": "2026-09-15T15:30:00Z", "version": 1})
	if err := projector.Handle(context.Background(), "events.approver.application.summary-changed.v1", raw); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if repository.applied.Record.WorkspaceID != "workspace-mapped" {
		t.Fatalf("workspace mapping = %q, want workspace-mapped", repository.applied.Record.WorkspaceID)
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
