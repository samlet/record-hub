package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/samlet/record-hub/contracts/eventenvelope"
	"github.com/samlet/record-hub/contracts/summaries"
	"github.com/samlet/record-hub/server/internal/modules/schema"
)

func TestSummaryHandlersAcceptOnlyTheirV1SafePayloads(t *testing.T) {
	tests := []struct {
		name         string
		kind         SummaryKind
		sourceSystem string
		eventType    string
		payload      map[string]any
	}{
		{
			name:         "application",
			kind:         SummaryApplication,
			sourceSystem: "approver",
			eventType:    "approver.application.summary-changed",
			payload: map[string]any{
				"applicationId": "app-123",
				"title":         "Safety review",
				"status":        "IN_REVIEW",
				"processRef":    "workflow-456",
				"updatedAt":     "2026-09-15T15:30:00Z",
				"version":       7,
			},
		},
		{
			name:         "project",
			kind:         SummaryProject,
			sourceSystem: "fluxion",
			eventType:    "fluxion.project.summary-changed",
			payload: map[string]any{
				"projectId":    "project-123",
				"type":         "engineering",
				"status":       "ACTIVE",
				"currentStage": "IMPLEMENTATION",
				"workflowRef":  "workflow-789",
				"updatedAt":    "2026-09-15T15:30:00Z",
				"version":      3,
			},
		},
		{
			name:         "tender",
			kind:         SummaryTender,
			sourceSystem: "bids",
			eventType:    "bids.tender.summary-changed",
			payload: map[string]any{
				"tenderId":          "tender-123",
				"buyerOrganization": "Example Buyer",
				"name":              "Cloud services",
				"status":            "OPEN",
				"template":          "standard-v2",
				"updatedAt":         "2026-09-15T15:30:00Z",
				"version":           4,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, err := NewSummaryHandler(test.kind)
			if err != nil {
				t.Fatal(err)
			}
			if err := handler.Handle(context.Background(), summaryEnvelopeJSON(test.sourceSystem, test.eventType, test.payload)); err != nil {
				t.Fatalf("valid summary rejected: %v", err)
			}
		})
	}
}

func TestSummaryHandlersRejectWrongIdentityAndSensitiveFields(t *testing.T) {
	handler, err := NewSummaryHandler(SummaryApplication)
	if err != nil {
		t.Fatal(err)
	}
	validPayload := map[string]any{
		"applicationId": "app-123",
		"title":         "Safety review",
		"status":        "IN_REVIEW",
		"processRef":    "workflow-456",
		"updatedAt":     "2026-09-15T15:30:00Z",
		"version":       7,
	}

	tests := []struct {
		name        string
		source      string
		eventType   string
		payload     map[string]any
		wantMessage string
	}{
		{
			name:        "wrong source",
			source:      "fluxion",
			eventType:   "approver.application.summary-changed",
			payload:     validPayload,
			wantMessage: "summary event rejected",
		},
		{
			name:        "wrong event type",
			source:      "approver",
			eventType:   "approver.application.deleted",
			payload:     validPayload,
			wantMessage: "summary event rejected",
		},
		{
			name:      "contact email",
			source:    "approver",
			eventType: "approver.application.summary-changed",
			payload: map[string]any{
				"applicationId": "app-123",
				"title":         "Safety review",
				"status":        "IN_REVIEW",
				"processRef":    "workflow-456",
				"updatedAt":     "2026-09-15T15:30:00Z",
				"version":       7,
				"contactEmail":  "person@example.com",
			},
			wantMessage: "summary payload rejected",
		},
		{
			name:      "quotation",
			source:    "approver",
			eventType: "approver.application.summary-changed",
			payload: map[string]any{
				"applicationId": "app-123",
				"title":         "Safety review",
				"status":        "IN_REVIEW",
				"processRef":    "workflow-456",
				"updatedAt":     "2026-09-15T15:30:00Z",
				"version":       7,
				"bidAmount":     "1000000",
			},
			wantMessage: "summary payload rejected",
		},
		{
			name:      "file URL",
			source:    "approver",
			eventType: "approver.application.summary-changed",
			payload: map[string]any{
				"applicationId": "app-123",
				"title":         "Safety review",
				"status":        "IN_REVIEW",
				"processRef":    "workflow-456",
				"updatedAt":     "2026-09-15T15:30:00Z",
				"version":       7,
				"fileUrl":       "https://files.example.test/a.pdf",
			},
			wantMessage: "summary payload rejected",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := handler.Handle(context.Background(), summaryEnvelopeJSON(test.source, test.eventType, test.payload))
			if err == nil {
				t.Fatal("expected rejection")
			}
			var retryErr RetryError
			if !errors.As(err, &retryErr) || retryErr.Class != ErrorDeterministic {
				t.Fatalf("error class = %v", err)
			}
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("error = %q, want %q", err, test.wantMessage)
			}
			if strings.Contains(err.Error(), "person@example.com") || strings.Contains(err.Error(), "1000000") || strings.Contains(err.Error(), "files.example.test") {
				t.Fatalf("error leaked sensitive payload: %q", err)
			}
		})
	}
}

func TestSummaryHandlersRejectOversizedEnvelope(t *testing.T) {
	handler, err := NewSummaryHandler(SummaryProject)
	if err != nil {
		t.Fatal(err)
	}
	raw := summaryEnvelopeJSON("fluxion", "fluxion.project.summary-changed", map[string]any{
		"projectId":    "project-123",
		"type":         "engineering",
		"status":       "ACTIVE",
		"currentStage": "IMPLEMENTATION",
		"workflowRef":  "workflow-789",
		"updatedAt":    "2026-09-15T15:30:00Z",
		"version":      3,
	})
	raw = append(raw, bytes.Repeat([]byte(" "), eventenvelope.MaxEnvelopeBytes)...)
	err = handler.Handle(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "summary envelope rejected") {
		t.Fatalf("oversized envelope error = %v", err)
	}
}

func TestRegisterSummaryHandlersUsesExactKeys(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := RegisterSummaryHandlers(registry); err != nil {
		t.Fatal(err)
	}
	for _, key := range []HandlerKey{
		{SourceSystem: "approver", EventType: "approver.application.summary-changed", SchemaVersion: 1},
		{SourceSystem: "fluxion", EventType: "fluxion.project.summary-changed", SchemaVersion: 1},
		{SourceSystem: "bids", EventType: "bids.tender.summary-changed", SchemaVersion: 1},
	} {
		if _, err := registry.Lookup(key); err != nil {
			t.Fatalf("lookup %v: %v", key, err)
		}
	}
	if _, err := registry.Lookup(HandlerKey{SourceSystem: "approver", EventType: "approver.application.summary-changed", SchemaVersion: 2}); !errors.Is(err, ErrHandlerNotFound) {
		t.Fatalf("unknown version lookup = %v", err)
	}
	if err := RegisterSummaryHandlers(registry); !errors.Is(err, ErrHandlerExists) {
		t.Fatalf("duplicate registration = %v", err)
	}
}

func TestSummaryHandlerRejectsUnknownKind(t *testing.T) {
	if _, err := NewSummaryHandler("unknown"); !errors.Is(err, ErrSummaryKindUnknown) {
		t.Fatalf("unknown kind error = %v", err)
	}
}

func TestSummarySchemaContentHashesMatchManifest(t *testing.T) {
	expected := map[summaries.Kind]string{
		summaries.Application: "sha256:77b957960d1fd71a75d8659febb42c1e941a120ba71212a01562d60f17b0b0bf",
		summaries.Project:     "sha256:07a245eb4c472cef26dd0e7ce32ec15d3278a4f6f633221157d97372659d17b4",
		summaries.Tender:      "sha256:b4b314d9f83d5ce4ab7d2a882ea677b2765adca2dfc29798930e61567bbf79a3",
	}
	for kind, want := range expected {
		raw, err := summaries.Schema(kind)
		if err != nil {
			t.Fatal(err)
		}
		got, err := schema.SchemaContentHash(raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s schema content hash = %s, want %s", kind, got, want)
		}
	}
}

func summaryEnvelopeJSON(sourceSystem, eventType string, payload map[string]any) []byte {
	envelope := map[string]any{
		"eventId":          "018f47a5-8b77-7c5a-9c56-38db8aa4b180",
		"kind":             "event",
		"eventType":        eventType,
		"schemaVersion":    1,
		"sourceSystem":     sourceSystem,
		"tenantId":         "tenant-local",
		"aggregateType":    "Summary",
		"aggregateId":      "summary-123",
		"aggregateVersion": 1,
		"occurredAt":       "2026-09-15T15:30:00Z",
		"payload":          payload,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return raw
}
