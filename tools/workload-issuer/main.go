// Command workload-issuer is an ephemeral OAuth/OIDC issuer for local and CI
// acceptance tests. It is deliberately not a production authorization server.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	tokenTTL          = 5 * time.Minute
	maxRequestBody    = 16 << 10
	defaultKeyOverlap = tokenTTL
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type clientConfig struct {
	ID     string   `json:"id"`
	Secret string   `json:"secret"`
	Scopes []string `json:"scopes"`
}

type issuerConfig struct {
	Address        string
	Issuer         string
	Audience       string
	Clients        map[string]clientConfig
	ClientsFile    string
	KeyOverlap     time.Duration
	RotateOnSIGHUP bool
}

type signingKey struct {
	signer    jose.Signer
	publicKey jose.JSONWebKey
	retireAt  time.Time
}

type issuerServer struct {
	config      issuerConfig
	mu          sync.RWMutex
	signer      jose.Signer
	publicKey   jose.JSONWebKey
	signingKeys []signingKey
	clients     map[string]clientConfig
	now         func() time.Time
}

func main() {
	config, err := loadConfig(os.LookupEnv)
	if err != nil {
		slog.Error("invalid workload issuer configuration", "error", err)
		os.Exit(2)
	}
	server, err := newIssuerServer(config)
	if err != nil {
		slog.Error("initialize workload issuer", "error", err)
		os.Exit(1)
	}
	slog.Info("starting ephemeral workload issuer", "address", config.Address, "issuer", config.Issuer, "audience", config.Audience, "clients", len(config.Clients))
	if config.RotateOnSIGHUP || config.ClientsFile != "" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGHUP)
		defer signal.Stop(signals)
		go func() {
			for range signals {
				if err := server.reload(); err != nil {
					slog.Error("reload workload issuer", "error", err)
					continue
				}
				slog.Info("reloaded workload issuer", "clients", server.clientCount(), "jwksKeys", server.keyCount())
			}
		}()
	}
	httpServer := &http.Server{Addr: config.Address, Handler: server.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("workload issuer stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig(lookup func(string) (string, bool)) (issuerConfig, error) {
	read := func(key string) string {
		value, _ := lookup(key)
		return strings.TrimSpace(value)
	}
	config := issuerConfig{
		Address:        read("RECORD_HUB_WORKLOAD_ISSUER_ADDRESS"),
		Issuer:         strings.TrimRight(read("RECORD_HUB_WORKLOAD_ISSUER"), "/"),
		Audience:       read("RECORD_HUB_WORKLOAD_AUDIENCE"),
		ClientsFile:    read("RECORD_HUB_WORKLOAD_CLIENTS_FILE"),
		KeyOverlap:     defaultKeyOverlap,
		RotateOnSIGHUP: true,
	}
	if config.Address == "" || config.Issuer == "" || config.Audience == "" {
		return issuerConfig{}, errors.New("RECORD_HUB_WORKLOAD_ISSUER_ADDRESS, RECORD_HUB_WORKLOAD_ISSUER, and RECORD_HUB_WORKLOAD_AUDIENCE are required")
	}
	if _, _, err := net.SplitHostPort(config.Address); err != nil {
		return issuerConfig{}, fmt.Errorf("invalid workload issuer address: %w", err)
	}
	parsedIssuer, err := url.Parse(config.Issuer)
	if err != nil || parsedIssuer.Host == "" || (parsedIssuer.Scheme != "http" && parsedIssuer.Scheme != "https") {
		return issuerConfig{}, errors.New("RECORD_HUB_WORKLOAD_ISSUER must be an absolute HTTP(S) URL")
	}
	if !safeName.MatchString(config.Audience) {
		return issuerConfig{}, errors.New("RECORD_HUB_WORKLOAD_AUDIENCE contains unsupported characters")
	}
	if rawOverlap := read("RECORD_HUB_WORKLOAD_ISSUER_KEY_OVERLAP"); rawOverlap != "" {
		overlap, err := time.ParseDuration(rawOverlap)
		if err != nil || overlap <= 0 {
			return issuerConfig{}, errors.New("RECORD_HUB_WORKLOAD_ISSUER_KEY_OVERLAP must be a positive duration")
		}
		config.KeyOverlap = overlap
	}
	if rawRotate := read("RECORD_HUB_WORKLOAD_ISSUER_ROTATE_ON_SIGHUP"); rawRotate != "" {
		value, err := strconv.ParseBool(rawRotate)
		if err != nil {
			return issuerConfig{}, errors.New("RECORD_HUB_WORKLOAD_ISSUER_ROTATE_ON_SIGHUP must be true or false")
		}
		config.RotateOnSIGHUP = value
	}
	rawClients := read("RECORD_HUB_WORKLOAD_CLIENTS")
	if config.ClientsFile != "" {
		contents, err := os.ReadFile(config.ClientsFile)
		if err != nil {
			return issuerConfig{}, fmt.Errorf("read RECORD_HUB_WORKLOAD_CLIENTS_FILE: %w", err)
		}
		rawClients = string(contents)
	}
	clients, err := parseClients(rawClients)
	if err != nil {
		return issuerConfig{}, err
	}
	config.Clients = clients
	return config, nil
}

func parseClients(rawClients string) (map[string]clientConfig, error) {
	decoder := json.NewDecoder(strings.NewReader(rawClients))
	decoder.DisallowUnknownFields()
	var clients []clientConfig
	if rawClients == "" || decoder.Decode(&clients) != nil || len(clients) == 0 || len(clients) > 32 {
		return nil, errors.New("RECORD_HUB_WORKLOAD_CLIENTS must be a JSON array containing 1-32 clients")
	}
	parsed := make(map[string]clientConfig, len(clients))
	for index, client := range clients {
		client.ID = strings.TrimSpace(client.ID)
		if !safeName.MatchString(client.ID) {
			return nil, fmt.Errorf("workload client %d has an invalid id", index)
		}
		if len(client.Secret) < 32 {
			return nil, fmt.Errorf("workload client %q secret must contain at least 32 bytes", client.ID)
		}
		if len(client.Scopes) == 0 || len(client.Scopes) > 16 {
			return nil, fmt.Errorf("workload client %q must have 1-16 scopes", client.ID)
		}
		scopeSet := make(map[string]struct{}, len(client.Scopes))
		for scopeIndex, scope := range client.Scopes {
			scope = strings.TrimSpace(scope)
			if !safeName.MatchString(scope) {
				return nil, fmt.Errorf("workload client %q scope %d is invalid", client.ID, scopeIndex)
			}
			if _, exists := scopeSet[scope]; exists {
				return nil, fmt.Errorf("workload client %q contains duplicate scope %q", client.ID, scope)
			}
			scopeSet[scope] = struct{}{}
			client.Scopes[scopeIndex] = scope
		}
		sort.Strings(client.Scopes)
		if _, exists := parsed[client.ID]; exists {
			return nil, fmt.Errorf("duplicate workload client %q", client.ID)
		}
		parsed[client.ID] = client
	}
	return parsed, nil
}

func newIssuerServer(config issuerConfig) (*issuerServer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	keyIDBytes := make([]byte, 12)
	if _, err := rand.Read(keyIDBytes); err != nil {
		return nil, fmt.Errorf("generate key id: %w", err)
	}
	keyID := hex.EncodeToString(keyIDBytes)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		return nil, fmt.Errorf("create signer: %w", err)
	}
	keyMaterial := signingKey{
		signer:    signer,
		publicKey: jose.JSONWebKey{Key: &key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig"},
	}
	return &issuerServer{
		config:      config,
		signer:      signer,
		publicKey:   keyMaterial.publicKey,
		signingKeys: []signingKey{keyMaterial},
		clients:     cloneClients(config.Clients),
		now:         time.Now,
	}, nil
}

func (server *issuerServer) reload() error {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.config.ClientsFile != "" {
		contents, err := os.ReadFile(server.config.ClientsFile)
		if err != nil {
			return fmt.Errorf("read workload clients file: %w", err)
		}
		clients, err := parseClients(string(contents))
		if err != nil {
			return err
		}
		server.clients = clients
	}
	if !server.config.RotateOnSIGHUP {
		return nil
	}
	key, err := generateSigningKey()
	if err != nil {
		return err
	}
	now := server.now().UTC()
	if len(server.signingKeys) > 0 {
		server.signingKeys[0].retireAt = now.Add(server.config.KeyOverlap)
	}
	server.signingKeys = append([]signingKey{key}, server.signingKeys...)
	server.signer = key.signer
	server.publicKey = key.publicKey
	server.pruneKeysLocked(now)
	return nil
}

func (server *issuerServer) pruneKeysLocked(now time.Time) {
	active := server.signingKeys[:0]
	for _, key := range server.signingKeys {
		if key.retireAt.IsZero() || now.Before(key.retireAt) {
			active = append(active, key)
		}
	}
	server.signingKeys = active
}

func (server *issuerServer) clientCount() int {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return len(server.clients)
}

func (server *issuerServer) keyCount() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.pruneKeysLocked(server.now().UTC())
	return len(server.signingKeys)
}

func cloneClients(clients map[string]clientConfig) map[string]clientConfig {
	clone := make(map[string]clientConfig, len(clients))
	for id, client := range clients {
		client.Scopes = append([]string(nil), client.Scopes...)
		clone[id] = client
	}
	return clone
}

func generateSigningKey() (signingKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return signingKey{}, fmt.Errorf("generate signing key: %w", err)
	}
	keyIDBytes := make([]byte, 12)
	if _, err := rand.Read(keyIDBytes); err != nil {
		return signingKey{}, fmt.Errorf("generate key id: %w", err)
	}
	keyID := hex.EncodeToString(keyIDBytes)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		return signingKey{}, fmt.Errorf("create signer: %w", err)
	}
	return signingKey{
		signer:    signer,
		publicKey: jose.JSONWebKey{Key: &key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig"},
	}, nil
}

func (server *issuerServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+server.config.IssuerPath("/.well-known/openid-configuration"), server.discovery)
	mux.HandleFunc("GET "+server.config.IssuerPath("/keys"), server.keys)
	mux.HandleFunc("POST "+server.config.IssuerPath("/token"), server.token)
	mux.HandleFunc("GET "+server.config.IssuerPath("/authorize"), func(writer http.ResponseWriter, _ *http.Request) {
		writeOAuthError(writer, http.StatusBadRequest, "unsupported_response_type", "This issuer supports client_credentials only.")
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(writer, request)
	})
}

func (config issuerConfig) IssuerPath(suffix string) string {
	parsed, _ := url.Parse(config.Issuer)
	return strings.TrimRight(parsed.Path, "/") + suffix
}

func (server *issuerServer) discovery(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]interface{}{
		"issuer":                                server.config.Issuer,
		"authorization_endpoint":                server.config.Issuer + "/authorize",
		"token_endpoint":                        server.config.Issuer + "/token",
		"jwks_uri":                              server.config.Issuer + "/keys",
		"grant_types_supported":                 []string{"client_credentials"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"recordhub.binding.snapshot"},
	})
}

func (server *issuerServer) keys(writer http.ResponseWriter, _ *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.pruneKeysLocked(server.now().UTC())
	keys := make([]jose.JSONWebKey, 0, len(server.signingKeys))
	for _, key := range server.signingKeys {
		keys = append(keys, key.publicKey)
	}
	writeJSON(writer, http.StatusOK, jose.JSONWebKeySet{Keys: keys})
}

func (server *issuerServer) token(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBody)
	clientID, clientSecret, ok := request.BasicAuth()
	server.mu.RLock()
	client, exists := server.clients[clientID]
	signer := server.signer
	server.mu.RUnlock()
	if !ok || !exists || subtle.ConstantTimeCompare([]byte(client.Secret), []byte(clientSecret)) != 1 {
		writer.Header().Set("WWW-Authenticate", `Basic realm="workload-token"`)
		writeOAuthError(writer, http.StatusUnauthorized, "invalid_client", "Client authentication failed.")
		return
	}
	if err := request.ParseForm(); err != nil {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request", "The token request is invalid.")
		return
	}
	if request.Form.Get("grant_type") != "client_credentials" {
		writeOAuthError(writer, http.StatusBadRequest, "unsupported_grant_type", "Only client_credentials is supported.")
		return
	}
	requestedScopes := strings.Fields(request.Form.Get("scope"))
	if len(requestedScopes) == 0 || !scopesAllowed(requestedScopes, client.Scopes) {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_scope", "The requested scope is not allowed.")
		return
	}
	sort.Strings(requestedScopes)
	now := server.now().UTC()
	rawToken, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   server.config.Issuer,
		Subject:  client.ID,
		Audience: jwt.Audience{server.config.Audience},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(tokenTTL)),
	}).Claims(map[string]interface{}{"scope": strings.Join(requestedScopes, " ")}).Serialize()
	if err != nil {
		writeOAuthError(writer, http.StatusInternalServerError, "server_error", "Token issuance failed.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]interface{}{
		"access_token": rawToken,
		"token_type":   "Bearer",
		"expires_in":   int(tokenTTL.Seconds()),
		"scope":        strings.Join(requestedScopes, " "),
	})
}

func scopesAllowed(requested, allowed []string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, scope := range allowed {
		allowedSet[scope] = struct{}{}
	}
	for _, scope := range requested {
		if _, ok := allowedSet[scope]; !ok {
			return false
		}
	}
	return true
}

func writeOAuthError(writer http.ResponseWriter, status int, code, description string) {
	writeJSON(writer, status, map[string]string{"error": code, "error_description": description})
}

func writeJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
