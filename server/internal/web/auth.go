package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

// Config contains the browser-facing OIDC client settings. The redirect and
// post-logout targets are validated to prevent an open redirect.
type Config struct {
	AuthorizationEndpoint  string
	ClientID               string
	RedirectURL            string
	SuccessRedirectURL     string
	PostLogoutRedirectURL  string
	Scope                  string
	AllowInsecureEndpoints bool
	SecureCookies          bool
	SessionTTL             time.Duration
}

// CodeExchanger exchanges a one-time authorization code and verifies the
// returned ID token against the nonce. The production adapter should use the
// identity.OIDCVerifier implementation below.
type CodeExchanger func(context.Context, string, string, string) (identity.Principal, error)

// Handler owns the browser authentication routes. Mount Middleware around the
// API mux so successful sessions become identity principals in request context
// and state-changing endpoints receive CSRF protection.
type Handler struct {
	cfg      Config
	sessions *SessionManager
	csrf     *CSRFProtection
	exchange CodeExchanger
}

// NewHandler validates configuration and creates the reusable auth handler.
func NewHandler(cfg Config, sessions *SessionManager, exchange CodeExchanger) (*Handler, error) {
	if sessions == nil {
		return nil, errors.New("web session manager is required")
	}
	if exchange == nil {
		return nil, errors.New("OIDC code exchanger is required")
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = sessions.ttl
	}
	if cfg.SessionTTL != sessions.ttl {
		return nil, errors.New("web session TTL must match session manager TTL")
	}
	if cfg.Scope == "" {
		cfg.Scope = "openid profile email"
	}
	if cfg.SuccessRedirectURL == "" {
		cfg.SuccessRedirectURL = "/"
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	csrf := newCSRFProtection(sessions)
	return &Handler{cfg: cfg, sessions: sessions, csrf: csrf, exchange: exchange}, nil
}

// Routes mounts the authentication endpoints below /auth.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/auth/login", h.login)
	mux.HandleFunc("/auth/callback", h.callback)
	mux.HandleFunc("/auth/logout", h.logout)
	mux.HandleFunc("/auth/session", h.session)
}

// Middleware applies session context and CSRF checks to downstream handlers.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return h.SessionMiddleware(h.csrf.Middleware(next))
}

// SessionMiddleware exposes a verified browser principal through the same
// context contract used by bearer-token API middleware.
func (h *Handler) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal, ok := h.sessions.PrincipalFromRequest(r); ok {
			r = r.WithContext(identity.WithPrincipal(r.Context(), principal))
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state, nonce, _, challenge, err := h.sessions.beginPending(w)
	if err != nil {
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	endpoint, _ := url.Parse(h.cfg.AuthorizationEndpoint)
	query := endpoint.Query()
	query.Set("client_id", h.cfg.ClientID)
	query.Set("redirect_uri", h.cfg.RedirectURL)
	query.Set("response_type", "code")
	query.Set("scope", h.cfg.Scope)
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	endpoint.RawQuery = query.Encode()
	http.Redirect(w, r, endpoint.String(), http.StatusFound)
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state := r.URL.Query().Get("state")
	pending, err := h.sessions.consumePending(r, state)
	if err != nil {
		http.Error(w, "invalid authentication state", http.StatusBadRequest)
		return
	}
	defer h.sessions.clearPending(w)
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}
	principal, err := h.exchange(r.Context(), code, pending.codeVerifier, pending.nonce)
	if err != nil || principal.Kind != identity.PrincipalUser || principal.Issuer == "" || principal.Subject == "" {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	if err := h.sessions.SetSession(w, principal); err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, h.cfg.SuccessRedirectURL, http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.sessions.ClearSession(w)
	if h.cfg.PostLogoutRedirectURL != "" {
		http.Redirect(w, r, h.cfg.PostLogoutRedirectURL, http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	principal, ok := h.sessions.PrincipalFromRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Authenticated bool   `json:"authenticated"`
		Issuer        string `json:"issuer"`
		Subject       string `json:"subject"`
	}{true, principal.Issuer, principal.Subject})
}

// HTTPCodeExchanger is the production adapter for a Dex token endpoint. It
// never returns access or refresh tokens to the browser and requires an
// identity.NonceVerifier so the callback's nonce cannot be skipped.
type HTTPCodeExchanger struct {
	TokenEndpoint         string
	ClientID              string
	ClientSecret          string
	RedirectURL           string
	AllowInsecureEndpoint bool
	HTTPClient            *http.Client
	Verifier              identity.NonceVerifier
}

func (e HTTPCodeExchanger) Exchange(ctx context.Context, code, codeVerifier, nonce string) (identity.Principal, error) {
	if err := validateTokenExchange(e, code, codeVerifier, nonce); err != nil {
		return identity.Principal{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {e.RedirectURL},
		"client_id":     {e.ClientID},
		"client_secret": {e.ClientSecret},
		"code_verifier": {codeVerifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return identity.Principal{}, fmt.Errorf("create token exchange: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	client := e.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return identity.Principal{}, fmt.Errorf("token exchange request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		return identity.Principal{}, fmt.Errorf("token exchange returned HTTP %d", response.StatusCode)
	}
	var tokenResponse struct {
		IDToken string `json:"id_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&tokenResponse); err != nil || strings.TrimSpace(tokenResponse.IDToken) == "" {
		return identity.Principal{}, errors.New("token exchange response did not contain an ID token")
	}
	principal, err := e.Verifier.VerifyNonce(ctx, tokenResponse.IDToken, nonce)
	if err != nil {
		return identity.Principal{}, fmt.Errorf("verify OIDC ID token: %w", err)
	}
	return principal, nil
}

func validateTokenExchange(e HTTPCodeExchanger, code, verifier, nonce string) error {
	if strings.TrimSpace(e.TokenEndpoint) == "" || strings.TrimSpace(e.ClientID) == "" || strings.TrimSpace(e.ClientSecret) == "" || strings.TrimSpace(e.RedirectURL) == "" {
		return errors.New("OIDC token exchange configuration is incomplete")
	}
	if e.Verifier == nil {
		return errors.New("OIDC nonce verifier is required")
	}
	if strings.TrimSpace(code) == "" || strings.TrimSpace(verifier) == "" || strings.TrimSpace(nonce) == "" {
		return errors.New("OIDC code, PKCE verifier and nonce are required")
	}
	if err := validateAbsoluteHTTPURL(e.TokenEndpoint, e.AllowInsecureEndpoint); err != nil {
		return fmt.Errorf("token endpoint: %w", err)
	}
	if err := validateRedirectURL(e.RedirectURL, e.AllowInsecureEndpoint); err != nil {
		return fmt.Errorf("redirect URL: %w", err)
	}
	return nil
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return errors.New("OIDC client ID is required")
	}
	if !cfg.AllowInsecureEndpoints && !cfg.SecureCookies {
		return errors.New("secure cookies are required when insecure endpoints are disabled")
	}
	if err := validateAbsoluteHTTPURL(cfg.AuthorizationEndpoint, cfg.AllowInsecureEndpoints); err != nil {
		return fmt.Errorf("authorization endpoint: %w", err)
	}
	if err := validateRedirectURL(cfg.RedirectURL, cfg.AllowInsecureEndpoints); err != nil {
		return fmt.Errorf("redirect URL: %w", err)
	}
	if cfg.SuccessRedirectURL == "" {
		cfg.SuccessRedirectURL = "/"
	}
	if err := validateRelativeRedirect(cfg.SuccessRedirectURL); err != nil {
		return fmt.Errorf("success redirect: %w", err)
	}
	if cfg.PostLogoutRedirectURL != "" {
		if err := validateRelativeRedirect(cfg.PostLogoutRedirectURL); err != nil {
			return fmt.Errorf("post-logout redirect: %w", err)
		}
	}
	return nil
}

func validateAbsoluteHTTPURL(raw string, allowInsecure bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("must be an absolute HTTP(S) URL without userinfo")
	}
	if parsed.Scheme != "https" && !allowInsecure {
		return errors.New("must use HTTPS")
	}
	return nil
}

func validateRedirectURL(raw string, allowInsecure bool) error {
	if err := validateAbsoluteHTTPURL(raw, allowInsecure); err != nil {
		return err
	}
	parsed, _ := url.Parse(raw)
	if parsed.Fragment != "" {
		return errors.New("must not contain a fragment")
	}
	return nil
}

func validateRelativeRedirect(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") || parsed.Fragment != "" {
		return errors.New("must be a same-origin path")
	}
	return nil
}

// CSRFProtection implements a signed double-submit token. The browser may
// read the CSRF cookie, but cannot forge a valid value without the session
// signing key. Origin is checked when a browser supplies it.
type CSRFProtection struct {
	sessions *SessionManager
}

func newCSRFProtection(sessions *SessionManager) *CSRFProtection {
	return &CSRFProtection{sessions: sessions}
}

func (p *CSRFProtection) Ensure(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(CSRFCookieName); err == nil {
		var signed csrfCookie
		if p.sessions.unseal("csrf", cookie.Value, &signed) == nil && signed.Token != "" && signed.ExpiresAt > p.sessions.now().Unix() {
			return
		}
	}
	token, err := randomURLValue(p.sessions.rand, 32)
	if err != nil {
		return
	}
	expiresAt := p.sessions.now().Add(p.sessions.ttl)
	encoded, err := p.sessions.seal("csrf", csrfCookie{Token: token, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		return
	}
	setCookie(w, CSRFCookieName, encoded, expiresAt, false, p.sessions.secure)
}

func (p *CSRFProtection) Validate(r *http.Request) error {
	if !sameOrigin(r) {
		return ErrCSRF
	}
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ErrCSRF
	}
	var signed csrfCookie
	if err := p.sessions.unseal("csrf", cookie.Value, &signed); err != nil || signed.Token == "" || signed.ExpiresAt <= p.sessions.now().Unix() {
		return ErrCSRF
	}
	header := r.Header.Get("X-CSRF-Token")
	if header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(signed.Token)) != 1 {
		return ErrCSRF
	}
	return nil
}

func (p *CSRFProtection) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.Ensure(w, r)
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.Method != http.MethodTrace {
			if err := p.Validate(r); err != nil {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	for _, header := range []string{"Origin", "Referer"} {
		value := strings.TrimSpace(r.Header.Get(header))
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || parsed.Scheme == "" {
			return false
		}
		requestHost := r.Host
		if parsed.Host != requestHost {
			return false
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return false
		}
		return true
	}
	return true
}
