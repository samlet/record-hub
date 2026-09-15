package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLivenessDoesNotIncludeDependencies(t *testing.T) {
	handler := newTestHandler(map[Dependency]Checker{
		MongoDB: CheckFunc(func(context.Context) error { return errors.New("mongodb://user:secret@example") }),
	})

	response := request(t, handler, "/healthz")
	if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("healthz = %d %s", response.Code, response.Body.String())
	}
}

func TestReadinessDistinguishesDependenciesWithoutLeakingErrors(t *testing.T) {
	handler := newTestHandler(map[Dependency]Checker{
		MongoDB: CheckFunc(func(context.Context) error { return nil }),
		NATS:    CheckFunc(func(context.Context) error { return errors.New("nats://token@example") }),
		Dex:     CheckFunc(func(context.Context) error { return nil }),
	})

	response := request(t, handler, "/readyz")
	body := response.Body.String()
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	for _, want := range []string{`"mongodb":{"status":"ready"}`, `"nats":{"status":"not_ready"}`, `"dex":{"status":"ready"}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("readyz body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "token") || strings.Contains(body, "example") {
		t.Fatalf("readyz leaked dependency error: %s", body)
	}
}

func TestReadinessSucceedsWhenAllChecksPass(t *testing.T) {
	handler := newTestHandler(map[Dependency]Checker{
		MongoDB: CheckFunc(func(context.Context) error { return nil }),
	})

	response := request(t, handler, "/readyz")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("readyz = %d %s", response.Code, response.Body.String())
	}
}

func newTestHandler(checks map[Dependency]Checker) http.Handler {
	mux := http.NewServeMux()
	NewHandler(time.Second, checks).Routes(mux)
	return mux
}

func request(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
