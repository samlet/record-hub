// Command dex-smoke exercises the local Dex issuer and its four Web clients.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type clientConfig struct {
	id          string
	secretEnv   string
	redirectURI string
}

var clients = []clientConfig{
	{id: "approver-web-local", secretEnv: "DEX_APPROVER_WEB_SECRET", redirectURI: "http://127.0.0.1:3001/auth/callback"},
	{id: "fluxion-web-local", secretEnv: "DEX_FLUXION_WEB_SECRET", redirectURI: "http://127.0.0.1:3002/auth/callback"},
	{id: "bids-web-local", secretEnv: "DEX_BIDS_WEB_SECRET", redirectURI: "http://127.0.0.1:3003/auth/callback"},
	{id: "record-hub-web-local", secretEnv: "DEX_RECORD_HUB_WEB_SECRET", redirectURI: "http://127.0.0.1:3004/auth/callback"},
}

type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

type idTokenClaims struct {
	Issuer   string      `json:"iss"`
	Subject  string      `json:"sub"`
	Audience interface{} `json:"aud"`
	Nonce    string      `json:"nonce"`
}

var formActionPattern = regexp.MustCompile(`<form[^>]+action="([^"]+)"`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dex smoke failed:", err)
		os.Exit(1)
	}
	fmt.Println("dex smoke passed: discovery jwks four-client code+pkce redirect-isolation pkce-negative")
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	issuer := getenv("DEX_ISSUER", "http://127.0.0.1:5556/dex")
	discovery, err := fetchDiscovery(ctx, issuer)
	if err != nil {
		return err
	}
	if err := verifyJWKS(ctx, discovery.JWKSURI); err != nil {
		return err
	}

	for index, configuredClient := range clients {
		secret, err := requiredEnv(configuredClient.secretEnv)
		if err != nil {
			return err
		}
		verifier := randomURLSafe(48)
		nonce := randomURLSafe(24)
		code, err := authorize(ctx, discovery.AuthorizationEndpoint, configuredClient, verifier, nonce)
		if err != nil {
			return fmt.Errorf("authorize %s: %w", configuredClient.id, err)
		}
		tokens, err := exchange(ctx, discovery.TokenEndpoint, configuredClient, secret, code, verifier)
		if err != nil {
			return fmt.Errorf("exchange %s: %w", configuredClient.id, err)
		}
		if err := verifyTokens(tokens, issuer, configuredClient.id, nonce); err != nil {
			return fmt.Errorf("verify %s tokens: %w", configuredClient.id, err)
		}

		otherRedirect := clients[(index+1)%len(clients)].redirectURI
		if err := expectRedirectRejected(ctx, discovery.AuthorizationEndpoint, configuredClient.id, otherRedirect); err != nil {
			return err
		}
	}

	negativeClient := clients[len(clients)-1]
	secret, err := requiredEnv(negativeClient.secretEnv)
	if err != nil {
		return err
	}
	if err := expectPKCERejected(ctx, discovery, negativeClient, secret, "wrong"); err != nil {
		return err
	}
	if err := expectPKCERejected(ctx, discovery, negativeClient, secret, "missing"); err != nil {
		return err
	}
	return nil
}

func fetchDiscovery(ctx context.Context, issuer string) (discoveryDocument, error) {
	var document discoveryDocument
	if err := getJSON(ctx, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", &document); err != nil {
		return document, fmt.Errorf("discovery: %w", err)
	}
	if document.Issuer != issuer {
		return document, fmt.Errorf("discovery issuer %q, want %q", document.Issuer, issuer)
	}
	if document.AuthorizationEndpoint == "" || document.TokenEndpoint == "" || document.JWKSURI == "" {
		return document, errors.New("discovery is missing required endpoints")
	}
	return document, nil
}

func verifyJWKS(ctx context.Context, endpoint string) error {
	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := getJSON(ctx, endpoint, &jwks); err != nil {
		return fmt.Errorf("jwks: %w", err)
	}
	if len(jwks.Keys) == 0 {
		return errors.New("jwks has no signing keys")
	}
	return nil
}

func authorize(ctx context.Context, endpoint string, configuredClient clientConfig, verifier, nonce string) (string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", err
	}
	httpClient := &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.String() == configuredClient.redirectURI || strings.HasPrefix(request.URL.String(), configuredClient.redirectURI+"?") {
				return http.ErrUseLastResponse
			}
			if len(via) > 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}

	authorizationURL, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	state := randomURLSafe(24)
	query := authorizationURL.Query()
	query.Set("client_id", configuredClient.id)
	query.Set("redirect_uri", configuredClient.redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "openid profile email groups offline_access")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	authorizationURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login page status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	match := formActionPattern.FindSubmatch(body)
	if len(match) != 2 {
		return "", errors.New("login form action not found")
	}
	loginURL, err := url.Parse(html.UnescapeString(string(match[1])))
	if err != nil {
		return "", err
	}
	loginURL = response.Request.URL.ResolveReference(loginURL)
	form := url.Values{
		"login":    {getenv("DEX_LOCAL_EMAIL", "record-hub-dev@example.test")},
		"password": {os.Getenv("DEX_LOCAL_PASSWORD")},
	}
	loginRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse, err := httpClient.Do(loginRequest)
	if err != nil {
		return "", err
	}
	defer loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusFound && loginResponse.StatusCode != http.StatusSeeOther {
		failureBody, _ := io.ReadAll(io.LimitReader(loginResponse.Body, 4096))
		return "", fmt.Errorf("callback status %d: %s", loginResponse.StatusCode, strings.TrimSpace(string(failureBody)))
	}
	callback := loginResponse.Header.Get("Location")
	callbackURL, err := url.Parse(callback)
	if err != nil {
		return "", err
	}
	if callbackURL.Query().Get("state") != state {
		return "", errors.New("callback state mismatch")
	}
	if oauthError := callbackURL.Query().Get("error"); oauthError != "" {
		return "", fmt.Errorf("callback error: %s", oauthError)
	}
	code := callbackURL.Query().Get("code")
	if code == "" {
		return "", errors.New("callback code missing")
	}
	return code, nil
}

func exchange(ctx context.Context, endpoint string, configuredClient clientConfig, secret, code, verifier string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {configuredClient.redirectURI},
	}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(configuredClient.id, secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return tokenResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, err
	}
	if response.StatusCode != http.StatusOK {
		return tokenResponse{}, fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var tokens tokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return tokenResponse{}, err
	}
	return tokens, nil
}

func verifyTokens(tokens tokenResponse, issuer, audience, nonce string) error {
	if tokens.AccessToken == "" || tokens.IDToken == "" || tokens.RefreshToken == "" || !strings.EqualFold(tokens.TokenType, "bearer") {
		return errors.New("token response is incomplete")
	}
	parts := strings.Split(tokens.IDToken, ".")
	if len(parts) != 3 {
		return errors.New("ID token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return err
	}
	if claims.Issuer != issuer || claims.Subject == "" || claims.Nonce != nonce || !audienceContains(claims.Audience, audience) {
		return fmt.Errorf("unexpected claims iss=%q sub=%q aud=%v nonce=%q", claims.Issuer, claims.Subject, claims.Audience, claims.Nonce)
	}
	return nil
}

func expectRedirectRejected(ctx context.Context, endpoint, clientID, redirectURI string) error {
	authorizationURL, _ := url.Parse(endpoint)
	query := authorizationURL.Query()
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "openid")
	authorizationURL.RawQuery = query.Encode()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL.String(), nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("redirect negative %s: %w", clientID, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 400 || response.StatusCode >= 500 {
		return fmt.Errorf("redirect negative %s returned status %d", clientID, response.StatusCode)
	}
	return nil
}

func expectPKCERejected(ctx context.Context, discovery discoveryDocument, configuredClient clientConfig, secret, mode string) error {
	verifier := randomURLSafe(48)
	code, err := authorize(ctx, discovery.AuthorizationEndpoint, configuredClient, verifier, randomURLSafe(24))
	if err != nil {
		return fmt.Errorf("pkce %s setup: %w", mode, err)
	}
	suppliedVerifier := ""
	if mode == "wrong" {
		suppliedVerifier = randomURLSafe(48)
	}
	_, err = exchange(ctx, discovery.TokenEndpoint, configuredClient, secret, code, suppliedVerifier)
	if err == nil {
		return fmt.Errorf("token endpoint accepted %s PKCE verifier", mode)
	}
	return nil
}

func getJSON(ctx context.Context, endpoint string, target interface{}) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func randomURLSafe(size int) string {
	random := make([]byte, size)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(random)
}

func audienceContains(raw interface{}, expected string) bool {
	switch value := raw.(type) {
	case string:
		return value == expected
	case []interface{}:
		for _, item := range value {
			if item == expected {
				return true
			}
		}
	}
	return false
}

func requiredEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
