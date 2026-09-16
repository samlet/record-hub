// Command workload-token-smoke obtains and verifies one client-credentials
// token against the same OIDC contract used by Record Hub.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	issuer := required("RECORD_HUB_WORKLOAD_ISSUER")
	audience := required("RECORD_HUB_WORKLOAD_AUDIENCE")
	clientID := required("RECORD_HUB_WORKLOAD_CLIENT_ID")
	clientSecret := required("RECORD_HUB_WORKLOAD_CLIENT_SECRET")
	scope := required("RECORD_HUB_WORKLOAD_SCOPE")
	if issuer == "" || audience == "" || clientID == "" || clientSecret == "" || scope == "" {
		return errors.New("workload issuer, audience, client ID, client secret, and scope are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {scope}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(issuer, "/")+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(clientID, clientSecret)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("request workload token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read token response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("token endpoint returned HTTP %d", response.StatusCode)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil || tokenResponse.AccessToken == "" {
		return errors.New("token endpoint returned an invalid response")
	}
	verifier, err := identity.NewOIDCVerifier(ctx, identity.OIDCVerifierConfig{Issuer: issuer, Audience: audience, PrincipalKind: identity.PrincipalService, AllowInsecureIssuer: strings.HasPrefix(issuer, "http://")})
	if err != nil {
		return fmt.Errorf("configure verifier: %w", err)
	}
	principal, err := verifier.Verify(ctx, tokenResponse.AccessToken)
	if err != nil {
		return fmt.Errorf("verify workload token: %w", err)
	}
	if principal.Kind != identity.PrincipalService || principal.Issuer != issuer || principal.Subject != clientID || !contains(principal.Audience, audience) || !contains(principal.Scopes, scope) {
		return fmt.Errorf("workload token claims do not match the requested contract")
	}
	if !strings.EqualFold(tokenResponse.TokenType, "bearer") || tokenResponse.ExpiresIn <= 0 || tokenResponse.ExpiresIn > 900 || !contains(strings.Fields(tokenResponse.Scope), scope) {
		return errors.New("workload token response metadata is outside accepted bounds")
	}
	fmt.Printf("workload token verified: subject=%s audience=%s scope=%s ttl=%ds\n", principal.Subject, audience, scope, tokenResponse.ExpiresIn)
	return nil
}

func required(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
