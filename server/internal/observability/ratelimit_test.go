package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterResetsWithoutTrackingClientIdentifiers(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	start := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if allowed, _ := limiter.Allow(start); !allowed {
		t.Fatal("first request was unexpectedly limited")
	}
	if allowed, _ := limiter.Allow(start.Add(time.Second)); !allowed {
		t.Fatal("second request was unexpectedly limited")
	}
	if allowed, retry := limiter.Allow(start.Add(2 * time.Second)); allowed || retry <= 0 {
		t.Fatalf("third request allowed=%v retry=%s", allowed, retry)
	}
	if allowed, _ := limiter.Allow(start.Add(time.Minute)); !allowed {
		t.Fatal("window did not reset")
	}
}

func TestRateLimitMiddlewareReturnsSafe429(t *testing.T) {
	limiter := NewRateLimiter(1, time.Minute)
	handler := RateLimitMiddleware(limiter, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/tenant-123/records/secret-row", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/tenant-123/records/secret-row", nil))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" {
		t.Fatalf("unexpected rate-limit statuses: first=%d second=%d headers=%v", first.Code, second.Code, second.Header())
	}
	if strings.Contains(second.Body.String(), "tenant-123") || strings.Contains(second.Body.String(), "secret-row") {
		t.Fatalf("rate-limit response leaked request data: %s", second.Body.String())
	}
}
