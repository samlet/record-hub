package projection

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type tenderAssociationMemory struct {
	items []TenderApplicationAssociation
}

func (m *tenderAssociationMemory) Apply(_ context.Context, event TenderApplicationAssociationEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	id := tenderApplicationAssociationID(event.TenantID, event.WorkspaceID, event.TenderRef, event.ApplicationRef)
	for i, current := range m.items {
		if current.ID == id {
			updated, changed, err := applyTenderAssociation(current, event, event.ObservedAt)
			if err != nil {
				return err
			}
			if changed {
				m.items[i] = updated
			}
			return nil
		}
	}
	m.items = append(m.items, tenderAssociationFromEvent(id, event, event.ObservedAt))
	return nil
}
func (m *tenderAssociationMemory) List(_ context.Context, query TenderApplicationAssociationQuery) ([]TenderApplicationAssociation, error) {
	q, err := query.normalized()
	if err != nil {
		return nil, err
	}
	out := []TenderApplicationAssociation{}
	for _, item := range m.items {
		if item.TenantID == q.TenantID && item.WorkspaceID == q.WorkspaceID && (q.TenderRef == "" || q.TenderRef == item.TenderRef) {
			out = append(out, item)
		}
	}
	return out, nil
}

func TestTenderApplicationAssociationAPIIsScopedAndSafe(t *testing.T) {
	memory := &tenderAssociationMemory{}
	now := time.Now().UTC()
	if err := memory.Apply(context.Background(), TenderApplicationAssociationEvent{TenantID: "tenant-1", WorkspaceID: "workspace-1", TenderRef: "bids:TENDER:11111111-1111-4111-8111-111111111111", ApplicationRef: "approver:APPLICATION:bapr-12345678", WorkflowID: "wf-1", WorkflowRunID: "run-1", ApprovalStatus: "APPROVED", ProposalHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ApprovalGeneration: 1, DecisionVersion: 1, SourceVersion: 1, EventID: "event-1", PayloadHash: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "issuer", Subject: "owner"}
	authorizer := identity.NewAuthorizer(tenderAssociationMembership{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleViewer, Status: identity.MembershipActive}})
	handler := NewTenderApplicationAssociationHTTPHandler(NewTenderApplicationAssociationService(memory, authorizer))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/associations/tender-applications?tenantId=tenant-1&workspaceId=workspace-1", nil).WithContext(identity.WithPrincipal(context.Background(), principal))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tenderRef":"bids:TENDER:`) || !strings.Contains(response.Body.String(), `"applicationRef":"approver:APPLICATION:`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type tenderAssociationMembership struct{ membership identity.WorkspaceMembership }

func (r tenderAssociationMembership) FindMembership(context.Context, identity.IdentityKey, string, string) (identity.WorkspaceMembership, error) {
	return r.membership, nil
}
