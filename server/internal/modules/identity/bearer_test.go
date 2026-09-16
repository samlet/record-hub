package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type bearerVerifierFunc func(context.Context, string) (Principal, error)

func (f bearerVerifierFunc) Verify(ctx context.Context, token string) (Principal, error) {
	return f(ctx, token)
}

func TestBearerMiddlewareVerifiesAndPreservesPrincipal(t *testing.T) {
	principal := Principal{Kind: PrincipalUser, Issuer: "issuer", Subject: "subject"}
	called := ""
	handler := BearerMiddleware(bearerVerifierFunc(func(_ context.Context, token string) (Principal, error) {
		called = token
		return principal, nil
	}), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := PrincipalFromContext(r.Context())
		if !ok || !reflect.DeepEqual(got, principal) {
			t.Fatalf("principal = %+v, ok=%v", got, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	request.Header.Set("Authorization", "Bearer token-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || called != "token-1" {
		t.Fatalf("status=%d token=%q", response.Code, called)
	}
}

func TestBearerMiddlewareRejectsMalformedOrInvalidToken(t *testing.T) {
	handler := BearerMiddleware(bearerVerifierFunc(func(context.Context, string) (Principal, error) {
		return Principal{}, ErrInvalidToken
	}), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("downstream handler should not run")
	}))
	for _, authorization := range []string{"Basic abc", "Bearer", "Bearer abc extra", "Bearer abc"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
		request.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status=%d", authorization, response.Code)
		}
	}
}

func TestBearerMiddlewareDoesNotOverrideExistingSessionPrincipal(t *testing.T) {
	existing := Principal{Kind: PrincipalUser, Issuer: "session-issuer", Subject: "session-subject"}
	handler := BearerMiddleware(bearerVerifierFunc(func(context.Context, string) (Principal, error) {
		t.Fatal("bearer verifier should not run")
		return Principal{}, nil
	}), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := PrincipalFromContext(r.Context())
		if !ok || !reflect.DeepEqual(got, existing) {
			t.Fatalf("principal = %+v, ok=%v", got, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(WithPrincipal(context.Background(), existing))
	request.Header.Set("Authorization", "Bearer ignored")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d", response.Code)
	}
}
