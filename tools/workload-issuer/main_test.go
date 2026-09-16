package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func testIssuerConfig() issuerConfig {
	return issuerConfig{
		Address:  "127.0.0.1:15557",
		Issuer:   "http://127.0.0.1:15557/workload",
		Audience: "record-hub-api-local",
		Clients: map[string]clientConfig{
			"fluxion-to-record-hub": {ID: "fluxion-to-record-hub", Secret: strings.Repeat("a", 32), Scopes: []string{"recordhub.binding.snapshot"}},
		},
	}
}

func TestLoadConfigRejectsWeakOrWildcardClients(t *testing.T) {
	base := map[string]string{
		"RECORD_HUB_WORKLOAD_ISSUER_ADDRESS": "127.0.0.1:15557",
		"RECORD_HUB_WORKLOAD_ISSUER":         "http://127.0.0.1:15557/workload",
		"RECORD_HUB_WORKLOAD_AUDIENCE":       "record-hub-api-local",
	}
	for name, clients := range map[string]string{
		"weak secret": `[{"id":"fluxion","secret":"short","scopes":["recordhub.binding.snapshot"]}]`,
		"wildcard id": `[{"id":"*","secret":"01234567890123456789012345678901","scopes":["recordhub.binding.snapshot"]}]`,
		"empty scope": `[{"id":"fluxion","secret":"01234567890123456789012345678901","scopes":[]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			env := make(map[string]string, len(base)+1)
			for key, value := range base {
				env[key] = value
			}
			env["RECORD_HUB_WORKLOAD_CLIENTS"] = clients
			if _, err := loadConfig(func(key string) (string, bool) { value, ok := env[key]; return value, ok }); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestClientCredentialsTokenContract(t *testing.T) {
	server, err := newIssuerServer(testIssuerConfig())
	if err != nil {
		t.Fatal(err)
	}
	server.now = func() time.Time { return time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC) }
	request := httptest.NewRequest(http.MethodPost, "/workload/token", strings.NewReader(url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {"recordhub.binding.snapshot"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth("fluxion-to-record-hub", strings.Repeat("a", 32))
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("token status = %d body=%s", response.Code, response.Body.String())
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &tokenResponse); err != nil {
		t.Fatal(err)
	}
	var standardClaims jwt.Claims
	var customClaims struct {
		Scope string `json:"scope"`
	}
	parsed, err := jwt.ParseSigned(tokenResponse.AccessToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.Claims(server.publicKey.Key, &standardClaims, &customClaims); err != nil {
		t.Fatal(err)
	}
	if standardClaims.Issuer != server.config.Issuer || standardClaims.Subject != "fluxion-to-record-hub" || len(standardClaims.Audience) != 1 || standardClaims.Audience[0] != server.config.Audience || customClaims.Scope != "recordhub.binding.snapshot" {
		t.Fatalf("unexpected claims: standard=%+v custom=%+v", standardClaims, customClaims)
	}
	if tokenResponse.ExpiresIn != 300 || standardClaims.Expiry.Time().Sub(standardClaims.IssuedAt.Time()) != 5*time.Minute {
		t.Fatalf("unexpected ttl: response=%d claims=%s", tokenResponse.ExpiresIn, standardClaims.Expiry.Time().Sub(standardClaims.IssuedAt.Time()))
	}
}

func TestTokenEndpointRejectsBadCredentialGrantAndScope(t *testing.T) {
	server, err := newIssuerServer(testIssuerConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		secret     string
		grant      string
		scope      string
		wantStatus int
		wantCode   string
	}{
		{name: "bad secret", secret: strings.Repeat("b", 32), grant: "client_credentials", scope: "recordhub.binding.snapshot", wantStatus: http.StatusUnauthorized, wantCode: "invalid_client"},
		{name: "bad grant", secret: strings.Repeat("a", 32), grant: "password", scope: "recordhub.binding.snapshot", wantStatus: http.StatusBadRequest, wantCode: "unsupported_grant_type"},
		{name: "bad scope", secret: strings.Repeat("a", 32), grant: "client_credentials", scope: "recordhub.command.submit", wantStatus: http.StatusBadRequest, wantCode: "invalid_scope"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/workload/token", strings.NewReader(url.Values{"grant_type": {test.grant}, "scope": {test.scope}}.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetBasicAuth("fluxion-to-record-hub", test.secret)
			response := httptest.NewRecorder()
			server.handler().ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantCode) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}
