package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestResourceRouterDispatchesSchemaAndRecordsPaths(t *testing.T) {
	records := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Resource", "records")
		w.WriteHeader(http.StatusAccepted)
	})
	schemas := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Resource", "schemas")
		w.WriteHeader(http.StatusCreated)
	})
	router := NewResourceRouter(records, schemas)

	for _, test := range []struct {
		path       string
		wantHeader string
		wantStatus int
	}{
		{path: "/api/v1/workspaces", wantHeader: "records", wantStatus: http.StatusAccepted},
		{path: "/api/v1/tables/table-1/records", wantHeader: "records", wantStatus: http.StatusAccepted},
		{path: "/api/v1/schemas", wantHeader: "schemas", wantStatus: http.StatusCreated},
		{path: "/api/v1/schemas/schema-1/publish", wantHeader: "schemas", wantStatus: http.StatusCreated},
		// Similar prefixes must not be mistaken for the schema resource.
		{path: "/api/v1/schemas-export", wantHeader: "records", wantStatus: http.StatusAccepted},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus || response.Header().Get("X-Resource") != test.wantHeader {
				t.Fatalf("response = %d/%q, want %d/%q", response.Code, response.Header().Get("X-Resource"), test.wantStatus, test.wantHeader)
			}
		})
	}
}

func TestResourceRouterFailsClosedForMissingHandler(t *testing.T) {
	response := httptest.NewRecorder()
	NewResourceRouter(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing records handler status = %d, want 503", response.Code)
	}
	response = httptest.NewRecorder()
	NewResourceRouter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing schema handler status = %d, want 503", response.Code)
	}
}

func TestConsoleRouterDispatchesOperationsPageWithoutShadowingResources(t *testing.T) {
	marker := func(name string, status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Resource", name)
			w.WriteHeader(status)
		})
	}
	router := NewConsoleRouter(marker("records", http.StatusAccepted), marker("schemas", http.StatusCreated), marker("operations", http.StatusNoContent))
	for _, test := range []struct {
		path       string
		wantHeader string
		wantStatus int
	}{
		{path: "/operations/events", wantHeader: "operations", wantStatus: http.StatusNoContent},
		{path: "/api/v1/operations/events", wantHeader: "operations", wantStatus: http.StatusNoContent},
		{path: "/api/v1/workspaces", wantHeader: "records", wantStatus: http.StatusAccepted},
		{path: "/api/v1/schemas", wantHeader: "schemas", wantStatus: http.StatusCreated},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus || response.Header().Get("X-Resource") != test.wantHeader {
				t.Fatalf("response = %d/%q, want %d/%q", response.Code, response.Header().Get("X-Resource"), test.wantStatus, test.wantHeader)
			}
		})
	}
}

func TestResourceRouterReceivesSessionPrincipalAndCSRFBoundary(t *testing.T) {
	sessions, err := NewSessionManager([]byte(strings.Repeat("r", 32)), time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "viewer"}
	marker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := identity.PrincipalFromContext(r.Context()); !ok {
			http.Error(w, "missing principal", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	routes := NewResourceRouter(marker, marker)
	protected := (&Handler{sessions: sessions, csrf: newCSRFProtection(sessions)}).Middleware(routes)

	sessionResponse := httptest.NewRecorder()
	if err := sessions.SetSession(sessionResponse, principal); err != nil {
		t.Fatal(err)
	}
	sessionCookie := responseCookie(sessionResponse, SessionCookieName)

	unauthenticated := httptest.NewRecorder()
	protected.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated resource status = %d, want 401", unauthenticated.Code)
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil)
	authenticatedRequest.AddCookie(sessionCookie)
	authenticated := httptest.NewRecorder()
	protected.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("authenticated resource status = %d, want 204", authenticated.Code)
	}

	csrfIssue := httptest.NewRecorder()
	csrfRequest := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	csrfRequest.AddCookie(sessionCookie)
	protected.ServeHTTP(csrfIssue, csrfRequest)
	csrfTokenCookie := responseCookie(csrfIssue, CSRFCookieName)
	if csrfTokenCookie == nil {
		t.Fatal("resource middleware did not issue CSRF cookie")
	}
	mutating := httptest.NewRecorder()
	mutatingRequest := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", nil)
	mutatingRequest.Host = "app.test"
	mutatingRequest.Header.Set("Origin", "http://app.test")
	mutatingRequest.AddCookie(sessionCookie)
	mutatingRequest.AddCookie(csrfTokenCookie)
	protected.ServeHTTP(mutating, mutatingRequest)
	if mutating.Code != http.StatusForbidden {
		t.Fatalf("mutating resource without CSRF status = %d, want 403", mutating.Code)
	}

	var signed csrfCookie
	if err := sessions.unseal("csrf", csrfTokenCookie.Value, &signed); err != nil {
		t.Fatal(err)
	}
	mutatingRequest.Header.Set("X-CSRF-Token", signed.Token)
	allowed := httptest.NewRecorder()
	protected.ServeHTTP(allowed, mutatingRequest)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("mutating resource with CSRF status = %d, want 204", allowed.Code)
	}
}

func TestServiceBearerDoesNotRequireBrowserCSRF(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	sessions, err := NewSessionManager(secret, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	handler := (&Handler{sessions: sessions, csrf: newCSRFProtection(sessions)}).Middleware(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "http://app.test/api/v1/bindings/snapshots", strings.NewReader(`{}`))
	request = request.WithContext(identity.WithPrincipal(request.Context(), identity.Principal{Kind: identity.PrincipalService, Issuer: "https://workload.example", Subject: "fluxion-to-record-hub"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("service bearer status = %d, want 204", response.Code)
	}
}

func TestSessionMiddlewareDoesNotTrustMalformedCookie(t *testing.T) {
	sessions, err := NewSessionManager([]byte(strings.Repeat("m", 32)), time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	route := (&Handler{sessions: sessions, csrf: newCSRFProtection(sessions)}).SessionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := identity.PrincipalFromContext(r.Context()); ok {
			t.Fatal("malformed session became a principal")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/workspaces", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "forged"})
	response := httptest.NewRecorder()
	route.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("malformed cookie status = %d", response.Code)
	}
}
