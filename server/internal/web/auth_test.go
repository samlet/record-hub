package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestOIDCLoginCallbackBindsStateNonceAndPKCE(t *testing.T) {
	sessions := testSessions(t, false)
	var exchangeCode, exchangeVerifier, exchangeNonce string
	handler, err := NewHandler(Config{
		AuthorizationEndpoint:  "http://dex.test/auth",
		ClientID:               "record-hub-web-local",
		RedirectURL:            "http://app.test/auth/callback",
		AllowInsecureEndpoints: true,
	}, sessions, func(_ context.Context, code, verifier, nonce string) (identity.Principal, error) {
		exchangeCode, exchangeVerifier, exchangeNonce = code, verifier, nonce
		return identity.Principal{Kind: identity.PrincipalUser, Issuer: "http://dex.test", Subject: "user-123", Audience: []string{"record-hub-web-local"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Routes(mux)
	routes := handler.Middleware(mux)

	loginRequest := httptest.NewRequest(http.MethodGet, "http://app.test/auth/login", nil)
	loginRequest.Host = "app.test"
	loginResponse := httptest.NewRecorder()
	routes.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusFound {
		t.Fatalf("login status = %d, want 302", loginResponse.Code)
	}
	authorizationURL, err := url.Parse(loginResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorizationURL.Query()
	for _, key := range []string{"client_id", "redirect_uri", "response_type", "scope", "state", "nonce", "code_challenge", "code_challenge_method"} {
		if query.Get(key) == "" {
			t.Fatalf("authorization query missing %s: %s", key, authorizationURL)
		}
	}
	if query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected authorization query: %s", authorizationURL)
	}
	stateCookie := responseCookie(loginResponse, PendingCookieName)
	if stateCookie == nil || stateCookie.HttpOnly != true || stateCookie.SameSite != http.SameSiteLaxMode || stateCookie.Secure {
		t.Fatalf("state cookie flags = %#v", stateCookie)
	}
	csrfCookie := responseCookie(loginResponse, CSRFCookieName)
	if csrfCookie == nil || csrfCookie.HttpOnly || csrfCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("CSRF cookie flags = %#v", csrfCookie)
	}

	callbackRequest := httptest.NewRequest(http.MethodGet, "http://app.test/auth/callback?code=one-time-code&state="+url.QueryEscape(query.Get("state")), nil)
	callbackRequest.Host = "app.test"
	callbackRequest.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	routes.ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusSeeOther || callbackResponse.Header().Get("Location") != "/" {
		t.Fatalf("callback response = %d Location=%q", callbackResponse.Code, callbackResponse.Header().Get("Location"))
	}
	if exchangeCode != "one-time-code" || exchangeVerifier == "" || exchangeNonce != query.Get("nonce") {
		t.Fatalf("exchange arguments = code %q verifier %q nonce %q", exchangeCode, exchangeVerifier, exchangeNonce)
	}
	hash := sha256.Sum256([]byte(exchangeVerifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(hash[:])
	if query.Get("code_challenge") != wantChallenge {
		t.Fatalf("PKCE challenge = %q, want %q", query.Get("code_challenge"), wantChallenge)
	}
	sessionCookie := responseCookie(callbackResponse, SessionCookieName)
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie flags = %#v", sessionCookie)
	}
	sessionRequest := httptest.NewRequest(http.MethodGet, "http://app.test/auth/session", nil)
	sessionRequest.Host = "app.test"
	sessionRequest.AddCookie(sessionCookie)
	sessionResponse := httptest.NewRecorder()
	routes.ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK || !strings.Contains(sessionResponse.Body.String(), `"subject":"user-123"`) {
		t.Fatalf("session response = %d %s", sessionResponse.Code, sessionResponse.Body.String())
	}

	// The signed state is also single-use in the in-memory pending store.
	replay := httptest.NewRecorder()
	routes.ServeHTTP(replay, callbackRequest)
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback status = %d, want 400", replay.Code)
	}
}

func TestOIDCRejectsInvalidStateBeforeExchange(t *testing.T) {
	sessions := testSessions(t, false)
	called := false
	handler, err := NewHandler(Config{
		AuthorizationEndpoint:  "http://dex.test/auth",
		ClientID:               "client",
		RedirectURL:            "http://app.test/auth/callback",
		AllowInsecureEndpoints: true,
	}, sessions, func(context.Context, string, string, string) (identity.Principal, error) {
		called = true
		return identity.Principal{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://app.test/auth/callback?code=code&state=wrong", nil)
	request.Host = "app.test"
	response := httptest.NewRecorder()
	handler.Routes(http.NewServeMux())
	handler.callback(response, request)
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("invalid state response = %d, exchange called=%t", response.Code, called)
	}
}

func TestCSRFProtectsLogoutAndClearsSession(t *testing.T) {
	sessions := testSessions(t, false)
	handler, err := NewHandler(Config{
		AuthorizationEndpoint:  "http://dex.test/auth",
		ClientID:               "client",
		RedirectURL:            "http://app.test/auth/callback",
		AllowInsecureEndpoints: true,
	}, sessions, func(context.Context, string, string, string) (identity.Principal, error) {
		return identity.Principal{Kind: identity.PrincipalUser, Issuer: "issuer", Subject: "subject"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Routes(mux)
	routes := handler.Middleware(mux)

	// Issue a session directly; login flow is covered above.
	loginResponse := httptest.NewRecorder()
	if err := sessions.SetSession(loginResponse, identity.Principal{Kind: identity.PrincipalUser, Issuer: "issuer", Subject: "subject"}); err != nil {
		t.Fatal(err)
	}
	sessionCookie := responseCookie(loginResponse, SessionCookieName)
	csrfResponse := httptest.NewRecorder()
	csrfRequest := httptest.NewRequest(http.MethodGet, "http://app.test/auth/session", nil)
	csrfRequest.Host = "app.test"
	routes.ServeHTTP(csrfResponse, csrfRequest)
	csrfTokenCookie := responseCookie(csrfResponse, CSRFCookieName)
	if csrfTokenCookie == nil {
		t.Fatal("middleware did not issue CSRF cookie")
	}

	withoutHeader := httptest.NewRequest(http.MethodPost, "http://app.test/auth/logout", nil)
	withoutHeader.Host = "app.test"
	withoutHeader.Header.Set("Origin", "http://app.test")
	withoutHeader.AddCookie(sessionCookie)
	withoutHeader.AddCookie(csrfTokenCookie)
	forbidden := httptest.NewRecorder()
	routes.ServeHTTP(forbidden, withoutHeader)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF header = %d, want 403", forbidden.Code)
	}

	var signed csrfCookie
	if err := sessions.unseal("csrf", csrfTokenCookie.Value, &signed); err != nil {
		t.Fatal(err)
	}
	logout := httptest.NewRequest(http.MethodPost, "http://app.test/auth/logout", nil)
	logout.Host = "app.test"
	logout.Header.Set("Origin", "http://app.test")
	logout.Header.Set("X-CSRF-Token", signed.Token)
	logout.AddCookie(sessionCookie)
	logout.AddCookie(csrfTokenCookie)
	loggedOut := httptest.NewRecorder()
	routes.ServeHTTP(loggedOut, logout)
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", loggedOut.Code)
	}
	cleared := responseCookie(loggedOut, SessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 || cleared.HttpOnly != true {
		t.Fatalf("cleared session cookie = %#v", cleared)
	}
}

func TestSecureCookies(t *testing.T) {
	sessions := testSessions(t, true)
	response := httptest.NewRecorder()
	if err := sessions.SetSession(response, identity.Principal{Kind: identity.PrincipalUser, Issuer: "issuer", Subject: "subject"}); err != nil {
		t.Fatal(err)
	}
	cookie := responseCookie(response, SessionCookieName)
	if cookie == nil || !cookie.Secure {
		t.Fatalf("secure session cookie = %#v", cookie)
	}
}

func TestHTTPCodeExchangerVerifiesNonce(t *testing.T) {
	type capturedVerifier struct {
		idToken string
		nonce   string
	}
	captured := &capturedVerifier{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("token request method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") != "verifier" {
			t.Fatalf("token request form = %#v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id_token":"signed-id-token"}`)
	}))
	defer server.Close()
	exchanger := HTTPCodeExchanger{
		TokenEndpoint:         server.URL,
		ClientID:              "client",
		ClientSecret:          "secret",
		RedirectURL:           "http://app.test/auth/callback",
		AllowInsecureEndpoint: true,
		Verifier: nonceVerifierFunc(func(_ context.Context, raw, nonce string) (identity.Principal, error) {
			captured.idToken, captured.nonce = raw, nonce
			return identity.Principal{Kind: identity.PrincipalUser, Issuer: "issuer", Subject: "subject"}, nil
		}),
	}
	principal, err := exchanger.Exchange(context.Background(), "code", "verifier", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "subject" || captured.idToken != "signed-id-token" || captured.nonce != "nonce" {
		t.Fatalf("exchange principal/capture = %#v %#v", principal, captured)
	}
}

func TestHTTPCodeExchangerRequiresNonceVerifier(t *testing.T) {
	_, err := (HTTPCodeExchanger{
		TokenEndpoint: "https://dex.test/token",
		ClientID:      "client",
		ClientSecret:  "secret",
		RedirectURL:   "https://app.test/auth/callback",
	}).Exchange(context.Background(), "code", "verifier", "nonce")
	if err == nil || !strings.Contains(err.Error(), "nonce verifier") {
		t.Fatalf("error = %v", err)
	}
}

type nonceVerifierFunc func(context.Context, string, string) (identity.Principal, error)

func (f nonceVerifierFunc) VerifyNonce(ctx context.Context, raw, nonce string) (identity.Principal, error) {
	return f(ctx, raw, nonce)
}

func testSessions(t *testing.T, secure bool) *SessionManager {
	t.Helper()
	sessions, err := NewSessionManager([]byte(strings.Repeat("s", 32)), time.Hour, secure)
	if err != nil {
		t.Fatal(err)
	}
	return sessions
}

func responseCookie(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
