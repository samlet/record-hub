package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const testAudience = "record-hub-web-local"

type testIssuer struct {
	server *httptest.Server
	mu     sync.RWMutex
	keys   []jose.JSONWebKey
	fail   bool
}

func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	issuer := &testIssuer{}
	mux := http.NewServeMux()
	issuer.server = httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"issuer":                                issuer.server.URL,
			"authorization_endpoint":                issuer.server.URL + "/auth",
			"token_endpoint":                        issuer.server.URL + "/token",
			"jwks_uri":                              issuer.server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(writer http.ResponseWriter, _ *http.Request) {
		issuer.mu.RLock()
		defer issuer.mu.RUnlock()
		if issuer.fail {
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(writer).Encode(jose.JSONWebKeySet{Keys: issuer.keys})
	})
	t.Cleanup(issuer.server.Close)
	return issuer
}

func (i *testIssuer) publish(key *rsa.PrivateKey, keyID string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.keys = []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig"}}
}

func (i *testIssuer) setFailure(fail bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fail = fail
}

func TestOIDCVerifierValidTokenAndRotation(t *testing.T) {
	issuer := newTestIssuer(t)
	firstKey := generateRSAKey(t)
	secondKey := generateRSAKey(t)
	issuer.publish(firstKey, "key-1")

	verifier, err := NewOIDCVerifier(context.Background(), OIDCVerifierConfig{
		Issuer:              issuer.server.URL,
		Audience:            testAudience,
		PrincipalKind:       PrincipalUser,
		AllowInsecureIssuer: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	firstToken := signToken(t, firstKey, "key-1", issuer.server.URL, testAudience, time.Now().Add(time.Hour))
	principal, err := verifier.Verify(context.Background(), firstToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.IdentityKey() != (IdentityKey{Issuer: issuer.server.URL, Subject: "user-123"}) {
		t.Fatalf("unexpected identity key: %#v", principal.IdentityKey())
	}
	if principal.Kind != PrincipalUser || principal.Email != "developer@example.test" || len(principal.Groups) != 1 {
		t.Fatalf("unexpected principal: %#v", principal)
	}

	issuer.publish(secondKey, "key-2")
	rotatedToken := signToken(t, secondKey, "key-2", issuer.server.URL, testAudience, time.Now().Add(time.Hour))
	if _, err := verifier.Verify(context.Background(), rotatedToken); err != nil {
		t.Fatalf("verify after JWKS rotation: %v", err)
	}

	issuer.setFailure(true)
	if _, err := verifier.Verify(context.Background(), rotatedToken); err != nil {
		t.Fatalf("cached key should survive JWKS outage: %v", err)
	}
	unknownKey := generateRSAKey(t)
	unknownToken := signToken(t, unknownKey, "key-3", issuer.server.URL, testAudience, time.Now().Add(time.Hour))
	if _, err := verifier.Verify(context.Background(), unknownToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown key during outage should fail closed, got %v", err)
	}
}

func TestOIDCVerifierRejectsInvalidClaims(t *testing.T) {
	issuer := newTestIssuer(t)
	key := generateRSAKey(t)
	issuer.publish(key, "key-1")
	verifier, err := NewOIDCVerifier(context.Background(), OIDCVerifierConfig{
		Issuer: issuer.server.URL, Audience: testAudience, PrincipalKind: PrincipalUser, AllowInsecureIssuer: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"wrong issuer":   signToken(t, key, "key-1", issuer.server.URL+"/other", testAudience, time.Now().Add(time.Hour)),
		"wrong audience": signToken(t, key, "key-1", issuer.server.URL, "another-client", time.Now().Add(time.Hour)),
		"expired":        signToken(t, key, "key-1", issuer.server.URL, testAudience, time.Now().Add(-time.Hour)),
	}
	for name, rawToken := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), rawToken); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expected ErrInvalidToken, got %v", err)
			}
		})
	}
}

func TestOIDCVerifierConfigFailsClosed(t *testing.T) {
	tests := []OIDCVerifierConfig{
		{},
		{Issuer: "not-a-url", Audience: testAudience, PrincipalKind: PrincipalUser},
		{Issuer: "http://issuer.example", Audience: testAudience, PrincipalKind: PrincipalUser},
		{Issuer: "https://issuer.example", PrincipalKind: PrincipalUser},
		{Issuer: "https://issuer.example", Audience: testAudience, PrincipalKind: "unknown"},
	}
	for _, cfg := range tests {
		if _, err := NewOIDCVerifier(context.Background(), cfg); err == nil {
			t.Fatalf("expected config error for %#v", cfg)
		}
	}
}

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signToken(t *testing.T, key *rsa.PrivateKey, keyID, issuer, audience string, expiry time.Time) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   issuer,
		Subject:  "user-123",
		Audience: jwt.Audience{audience},
		IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		Expiry:   jwt.NewNumericDate(expiry),
	}).Claims(map[string]interface{}{
		"email":              "developer@example.test",
		"email_verified":     true,
		"name":               "Developer",
		"preferred_username": "developer",
		"groups":             []string{"record-hub-developers"},
	}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
