package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegistryRendersBoundedDeterministicMetrics(t *testing.T) {
	registry := NewRegistry()
	registry.IncCounter("record_hub_auth_failures_total", Labels{"status": "403"})
	registry.IncCounter("record_hub_auth_failures_total", Labels{"status": "403"})
	registry.SetGauge("record_hub_projection_backlog", 4, Labels{"consumer": "approver-v1"})
	registry.ObserveDuration("record_hub_http_request_duration_seconds", 1500*time.Millisecond, Labels{"method": "GET", "status": "403"})

	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		`record_hub_auth_failures_total{status="403"} 2.000000`,
		`record_hub_projection_backlog{consumer="approver-v1"} 4.000000`,
		`record_hub_http_request_duration_seconds_sum{method="GET",status="403"} 1.500000`,
		`record_hub_http_request_duration_seconds_count{method="GET",status="403"} 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q in %s", expected, body)
		}
	}
	if strings.Contains(body, "tenant-") || strings.Contains(body, "record-") || strings.Contains(body, "token") {
		t.Fatalf("metrics contain unbounded or secret data: %s", body)
	}
}

func TestHTTPMiddlewareCountsStatusAndAuthFailuresWithoutPathLabels(t *testing.T) {
	registry := NewRegistry()
	handler := HTTPMiddleware(registry, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tenant-123/records/secret-row", nil))
	body := registry.Render()
	if response.Code != http.StatusForbidden || !strings.Contains(body, `record_hub_auth_failures_total{status="403"} 1.000000`) {
		t.Fatalf("status/auth metrics missing: %s", body)
	}
	if strings.Contains(body, "tenant-123") || strings.Contains(body, "secret-row") || strings.Contains(body, "path") {
		t.Fatalf("middleware emitted high-cardinality path data: %s", body)
	}
}
