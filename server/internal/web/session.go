package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

const (
	// SessionCookieName is the browser session cookie used by the BFF.
	SessionCookieName = "record_hub_session"
	// PendingCookieName binds the browser to an in-flight OIDC authorization.
	PendingCookieName = "record_hub_oidc_state"
	// CSRFCookieName is readable by the browser so the UI can send the matching
	// X-CSRF-Token header on state-changing requests.
	CSRFCookieName = "record_hub_csrf"

	maxPendingStates = 1024
)

var (
	ErrInvalidSession = errors.New("invalid web session")
	ErrInvalidState   = errors.New("invalid OIDC authorization state")
	ErrCSRF           = errors.New("CSRF validation failed")
)

const (
	defaultSessionTTL = 8 * time.Hour
	defaultPendingTTL = 10 * time.Minute
)

// SessionManager signs browser state with an application secret. No access or
// refresh token is placed in the cookie. Pending OIDC requests are kept in a
// bounded in-memory map and are single-use; a multi-instance deployment must
// replace this map with a shared short-lived store before enabling it.
type SessionManager struct {
	secret []byte
	ttl    time.Duration
	secure bool
	now    func() time.Time
	rand   io.Reader

	pendingMu sync.Mutex
	pending   map[string]pendingState
}

type pendingState struct {
	nonce        string
	codeVerifier string
	expiresAt    time.Time
}

type pendingCookie struct {
	State     string `json:"state"`
	ExpiresAt int64  `json:"exp"`
}

type sessionCookie struct {
	Kind      identity.PrincipalKind `json:"kind"`
	Issuer    string                 `json:"iss"`
	Subject   string                 `json:"sub"`
	Audience  []string               `json:"aud,omitempty"`
	ExpiresAt int64                  `json:"exp"`
}

type csrfCookie struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"exp"`
}

// NewSessionManager validates the signing key and creates a session manager.
// Production callers should provide at least 32 random bytes and set secure
// to true when the browser reaches the service over HTTPS.
func NewSessionManager(secret []byte, ttl time.Duration, secure bool) (*SessionManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("web session secret must contain at least 32 bytes")
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		return nil, errors.New("web session TTL must be between 1 second and 24 hours")
	}
	return &SessionManager{
		secret:  append([]byte(nil), secret...),
		ttl:     ttl,
		secure:  secure,
		now:     time.Now,
		rand:    rand.Reader,
		pending: make(map[string]pendingState),
	}, nil
}

func (m *SessionManager) pendingTTL() time.Duration {
	if m.ttl < defaultPendingTTL {
		return m.ttl
	}
	return defaultPendingTTL
}

// beginPending creates the state, nonce and S256 PKCE verifier for one login
// attempt and sets a signed, HttpOnly state cookie.
func (m *SessionManager) beginPending(w http.ResponseWriter) (state, nonce, verifier, challenge string, err error) {
	state, err = randomURLValue(m.rand, 32)
	if err != nil {
		return "", "", "", "", fmt.Errorf("generate OIDC state: %w", err)
	}
	nonce, err = randomURLValue(m.rand, 32)
	if err != nil {
		return "", "", "", "", fmt.Errorf("generate OIDC nonce: %w", err)
	}
	verifier, err = randomURLValue(m.rand, 32)
	if err != nil {
		return "", "", "", "", fmt.Errorf("generate PKCE verifier: %w", err)
	}
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])

	expiresAt := m.now().Add(m.pendingTTL())
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	now := m.now()
	for key, value := range m.pending {
		if !value.expiresAt.After(now) {
			delete(m.pending, key)
		}
	}
	if len(m.pending) >= maxPendingStates {
		return "", "", "", "", errors.New("too many pending OIDC authorizations")
	}
	m.pending[state] = pendingState{nonce: nonce, codeVerifier: verifier, expiresAt: expiresAt}

	encoded, err := m.seal("oidc-state", pendingCookie{State: state, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		delete(m.pending, state)
		return "", "", "", "", err
	}
	setCookie(w, PendingCookieName, encoded, expiresAt, true, m.secure)
	return state, nonce, verifier, challenge, nil
}

func (m *SessionManager) consumePending(r *http.Request, state string) (pendingState, error) {
	cookie, err := r.Cookie(PendingCookieName)
	if err != nil {
		return pendingState{}, ErrInvalidState
	}
	var signed pendingCookie
	if err := m.unseal("oidc-state", cookie.Value, &signed); err != nil {
		return pendingState{}, ErrInvalidState
	}
	if signed.State == "" || state == "" || !hmac.Equal([]byte(signed.State), []byte(state)) || signed.ExpiresAt <= m.now().Unix() {
		return pendingState{}, ErrInvalidState
	}
	m.pendingMu.Lock()
	pending, ok := m.pending[state]
	if ok {
		delete(m.pending, state)
	}
	m.pendingMu.Unlock()
	if !ok || !pending.expiresAt.After(m.now()) {
		return pendingState{}, ErrInvalidState
	}
	return pending, nil
}

func (m *SessionManager) clearPending(w http.ResponseWriter) {
	clearCookie(w, PendingCookieName, m.secure)
}

// SetSession writes a new signed session cookie after a successful OIDC
// callback. Only the stable principal identity and audience are persisted.
func (m *SessionManager) SetSession(w http.ResponseWriter, principal identity.Principal) error {
	if principal.Kind != identity.PrincipalUser || strings.TrimSpace(principal.Issuer) == "" || strings.TrimSpace(principal.Subject) == "" {
		return ErrInvalidSession
	}
	expiresAt := m.now().Add(m.ttl)
	encoded, err := m.seal("session", sessionCookie{
		Kind:      principal.Kind,
		Issuer:    principal.Issuer,
		Subject:   principal.Subject,
		Audience:  append([]string(nil), principal.Audience...),
		ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		return fmt.Errorf("encode web session: %w", err)
	}
	setCookie(w, SessionCookieName, encoded, expiresAt, true, m.secure)
	return nil
}

// PrincipalFromRequest verifies and decodes the session cookie.
func (m *SessionManager) PrincipalFromRequest(r *http.Request) (identity.Principal, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return identity.Principal{}, false
	}
	var signed sessionCookie
	if err := m.unseal("session", cookie.Value, &signed); err != nil || signed.ExpiresAt <= m.now().Unix() || signed.Kind != identity.PrincipalUser || signed.Issuer == "" || signed.Subject == "" {
		return identity.Principal{}, false
	}
	return identity.Principal{
		Kind:     signed.Kind,
		Issuer:   signed.Issuer,
		Subject:  signed.Subject,
		Audience: append([]string(nil), signed.Audience...),
	}, true
}

// ClearSession invalidates the browser session cookie.
func (m *SessionManager) ClearSession(w http.ResponseWriter) {
	clearCookie(w, SessionCookieName, m.secure)
}

func (m *SessionManager) seal(kind string, value interface{}) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	message := kind + "." + body
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(message))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

func (m *SessionManager) unseal(kind, encoded string, destination interface{}) error {
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ErrInvalidSession
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidSession
	}
	expected, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidSession
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(kind + "." + parts[0]))
	if !hmac.Equal(expected, mac.Sum(nil)) {
		return ErrInvalidSession
	}
	if len(body) > 4096 {
		return ErrInvalidSession
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return ErrInvalidSession
	}
	return nil
}

func randomURLValue(reader io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func setCookie(w http.ResponseWriter, name, value string, expiresAt time.Time, httpOnly, secure bool) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
