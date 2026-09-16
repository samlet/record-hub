package projection

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestM5SummaryProducerEnvelopes exercises the same envelope/summary handlers
// used by the NATS consumer for all three producer contracts. It is the
// deterministic cross-language gate; the live Mongo/JetStream gate is exposed
// by scripts/verify-m5-producers.sh when RECORD_HUB_M5_LIVE=1.
func TestM5SummaryProducerEnvelopes(t *testing.T) {
	cases := []struct {
		name    string
		kind    SummaryKind
		source  string
		event   string
		payload map[string]interface{}
	}{
		{
			name: "approver", kind: SummaryApplication, source: "approver",
			event:   "approver.application.summary-changed",
			payload: map[string]interface{}{"applicationId": "app-123", "title": "Safety review", "status": "IN_REVIEW", "processRef": "workflow-456", "updatedAt": "2026-09-15T15:30:00Z", "version": 7},
		},
		{
			name: "fluxion", kind: SummaryProject, source: "fluxion",
			event:   "fluxion.project.summary-changed",
			payload: map[string]interface{}{"projectId": "project-123", "type": "engineering", "status": "ACTIVE", "currentStage": "IMPLEMENTATION", "workflowRef": "workflow-789", "updatedAt": "2026-09-15T15:30:00Z", "version": 3},
		},
		{
			name: "bids", kind: SummaryTender, source: "bids",
			event:   "bids.tender.summary-changed",
			payload: map[string]interface{}{"tenderId": "tender-123", "buyerOrganization": "Example Buyer", "name": "Cloud services", "status": "OPEN", "template": "standard-v2", "updatedAt": "2026-09-15T15:30:00Z", "version": 4},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler, err := NewSummaryHandler(testCase.kind)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]interface{}{
				"eventId": "018f47a5-8b77-7c5a-9c56-38db8aa4b180", "kind": "event", "eventType": testCase.event,
				"schemaVersion": 1, "sourceSystem": testCase.source, "tenantId": "tenant-m5",
				"aggregateType": testCase.name, "aggregateId": testCase.name + "-123", "aggregateVersion": 1,
				"correlationId": "workflow-m5", "occurredAt": time.Now().UTC().Format(time.RFC3339Nano),
				"payload": testCase.payload,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := handler.Handle(t.Context(), raw); err != nil {
				t.Fatalf("producer envelope rejected: %v; raw=%s", fmt.Errorf("%w", err), raw)
			}
		})
	}
}
