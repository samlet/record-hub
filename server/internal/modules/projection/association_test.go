package projection

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

func testAssociationEvent(version int64, hash string) AssociationEvent {
	return AssociationEvent{TenantID: "tenant-1", WorkspaceID: "workspace-1", ProjectRef: "fluxion:PROJECT:11111111-1111-1111-1111-111111111111", ApplicationRef: "approver:APPLICATION:app-1", WorkflowID: "project-1", WorkflowRunID: "run-1", ApprovalStatus: "PENDING", ProposalHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DispatchGeneration: 1, DecisionVersion: 1, SourceVersion: version, EventID: "event-" + hash, PayloadHash: "sha256:" + hash, ObservedAt: time.Date(2026, time.September, 20, 1, 0, 0, 0, time.UTC)}
}

func TestAssociationProjectionIsMonotonicAndRepairsGaps(t *testing.T) {
	current := associationFromEvent("association-1", testAssociationEvent(1, "1111111111111111111111111111111111111111111111111111111111111111"), time.Now().UTC())
	var changed bool
	var err error
	current, changed, err = applyAssociationEvent(current, testAssociationEvent(3, "3333333333333333333333333333333333333333333333333333333333333333"), time.Now().UTC())
	if err != nil || !changed || current.ProjectionState != AssociationGap || current.SourceVersion != 1 || current.ObservedVersion != 3 {
		t.Fatalf("future event=%+v changed=%v err=%v", current, changed, err)
	}
	current, changed, err = applyAssociationEvent(current, testAssociationEvent(2, "2222222222222222222222222222222222222222222222222222222222222222"), time.Now().UTC())
	if err != nil || !changed || current.ProjectionState != AssociationGap || current.SourceVersion != 2 {
		t.Fatalf("version 2=%+v changed=%v err=%v", current, changed, err)
	}
	current, changed, err = applyAssociationEvent(current, testAssociationEvent(3, "3333333333333333333333333333333333333333333333333333333333333333"), time.Now().UTC())
	if err != nil || !changed || current.ProjectionState != AssociationCurrent || current.SourceVersion != 3 {
		t.Fatalf("repaired association=%+v changed=%v err=%v", current, changed, err)
	}
}

func TestAssociationProjectionDetectsSameVersionConflict(t *testing.T) {
	model := associationFromEvent("association-1", testAssociationEvent(1, "1111111111111111111111111111111111111111111111111111111111111111"), time.Now().UTC())
	var updated bool
	model, updated, err := applyAssociationEvent(model, testAssociationEvent(1, "2222222222222222222222222222222222222222222222222222222222222222"), time.Now().UTC())
	if err != nil || !updated || model.ProjectionState != AssociationConflict {
		t.Fatalf("conflict transition update=%v err=%v model=%+v", updated, err, model)
	}
	if _, _, err := applyAssociationEvent(model, testAssociationEvent(2, "3333333333333333333333333333333333333333333333333333333333333333"), time.Now().UTC()); err != ErrAssociationConflict {
		t.Fatalf("conflict follow-up err=%v", err)
	}
}

type associationMemoryRepository struct {
	values []ProjectApplicationAssociation
}

func (repository *associationMemoryRepository) Apply(_ context.Context, event AssociationEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	for index := range repository.values {
		if repository.values[index].ID == associationID(event.TenantID, event.WorkspaceID, event.ProjectRef, event.ApplicationRef) {
			updatedAssociation, updated, err := applyAssociationEvent(repository.values[index], event, event.ObservedAt)
			if err != nil {
				return err
			}
			if updated {
				repository.values[index] = updatedAssociation
			}
			return nil
		}
	}
	repository.values = append(repository.values, associationFromEvent(associationID(event.TenantID, event.WorkspaceID, event.ProjectRef, event.ApplicationRef), event, event.ObservedAt))
	return nil
}

func (repository *associationMemoryRepository) List(_ context.Context, query AssociationQuery) ([]ProjectApplicationAssociation, error) {
	query, err := query.normalized()
	if err != nil {
		return nil, err
	}
	items := make([]ProjectApplicationAssociation, 0, len(repository.values))
	for _, item := range repository.values {
		if item.TenantID == query.TenantID && item.WorkspaceID == query.WorkspaceID && (query.ProjectRef == "" || item.ProjectRef == query.ProjectRef) && (query.ApplicationRef == "" || item.ApplicationRef == query.ApplicationRef) {
			items = append(items, item)
		}
	}
	if len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return items, nil
}

func TestAssociationHTTPAuthorizesTenantAndReturnsSafeTypedRefs(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	repository := &associationMemoryRepository{}
	if err := repository.Apply(context.Background(), testAssociationEvent(1, "1111111111111111111111111111111111111111111111111111111111111111")); err != nil {
		t.Fatal(err)
	}
	authorizer := identity.NewAuthorizer(associationMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleViewer, Status: identity.MembershipActive}})
	handler := NewAssociationHTTPHandler(NewAssociationService(repository, authorizer))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/associations/project-applications?tenantId=tenant-1&workspaceId=workspace-1", nil).WithContext(identity.WithPrincipal(context.Background(), principal))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"projectRef":"fluxion:PROJECT:`) || !strings.Contains(response.Body.String(), `"applicationRef":"approver:APPLICATION:app-1"`) {
		t.Fatalf("association response status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/associations/project-applications?tenantId=tenant-2&workspaceId=workspace-1", nil).WithContext(identity.WithPrincipal(context.Background(), principal))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant status=%d body=%s", response.Code, response.Body.String())
	}
}

type associationMembershipReader struct{ membership identity.WorkspaceMembership }

func (reader associationMembershipReader) FindMembership(context.Context, identity.IdentityKey, string, string) (identity.WorkspaceMembership, error) {
	return reader.membership, nil
}

func TestAssociationEventFromApprovalSummaryIsAllowlisted(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"applicationRef": "approver:APPLICATION:app-1", "projectRef": "fluxion:PROJECT:11111111-1111-1111-1111-111111111111", "status": "WITHDRAWN", "workflowRef": map[string]string{"workflowId": "project-1", "runId": "run-1"}, "proposalHash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "dispatchGeneration": 1, "decisionVersion": 1})
	event, err := associationEventFromSummary(summaryEventEnvelope{EventID: "event-1", TenantID: "tenant-1", AggregateVersion: 1, Metadata: map[string]string{"workspaceId": "workspace-1"}}, payload, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err != nil || event.ApprovalStatus != "WITHDRAWN" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
}

type captureAssociationWriter struct{ event AssociationEvent }

func (writer *captureAssociationWriter) Apply(_ context.Context, event AssociationEvent) error {
	writer.event = event
	return event.Validate()
}

func TestSummaryProjectorFeedsTypedAssociationWriter(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(registry); err != nil {
		t.Fatal(err)
	}
	writer := &captureAssociationWriter{}
	repository := &projectorRepository{}
	projector, err := NewSummaryProjector(registry, &projectorInbox{}, repository, "workspace-approval")
	if err != nil {
		t.Fatal(err)
	}
	projector.WithAssociationWriter(writer)
	payload := map[string]any{"applicationRef": "approver:APPLICATION:app-1", "projectRef": "fluxion:PROJECT:11111111-1111-1111-1111-111111111111", "status": "APPROVED", "workflowRef": map[string]string{"workflowId": "project-1", "runId": "run-1"}, "proposalHash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "dispatchGeneration": 1, "decisionVersion": 1, "freshness": map[string]any{"sourceEventId": "apr-result-12345678", "sourceVersion": 1, "observedAt": "2026-09-19T00:00:00Z"}, "updatedAt": "2026-09-19T00:00:00Z", "version": 1}
	raw, err := json.Marshal(map[string]any{"eventId": "018f47a5-8b77-7c5a-9c56-38db8aa4b181", "kind": "event", "eventType": "approver.dispatch-approval.summary-changed", "schemaVersion": 1, "sourceSystem": "approver", "tenantId": "tenant-1", "aggregateType": "Approval", "aggregateId": "app-1", "aggregateVersion": 1, "occurredAt": "2026-09-19T00:00:00Z", "metadata": map[string]string{"workspaceId": "workspace-approval"}, "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := projector.Handle(context.Background(), "events.approver.dispatch-approval.summary-changed.v1", raw); err != nil {
		t.Fatal(err)
	}
	if writer.event.ProjectRef == "" || writer.event.ApplicationRef == "" || writer.event.WorkspaceID != "workspace-approval" || writer.event.ApprovalStatus != "APPROVED" {
		t.Fatalf("association event=%+v", writer.event)
	}
}
