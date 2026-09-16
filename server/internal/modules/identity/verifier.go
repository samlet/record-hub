package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

var ErrInvalidToken = errors.New("invalid identity token")

// TokenVerifier authenticates one bearer token and constructs a principal.
type TokenVerifier interface {
	Verify(context.Context, string) (Principal, error)
}

// NonceVerifier authenticates an OIDC ID token and binds it to the nonce that
// was generated for the browser's authorization request. The extra method is
// intentionally separate from TokenVerifier because bearer-token callers do
// not have an authorization-request nonce to validate.
type NonceVerifier interface {
	VerifyNonce(context.Context, string, string) (Principal, error)
}

type OIDCVerifierConfig struct {
	Issuer              string
	Audience            string
	PrincipalKind       PrincipalKind
	AllowInsecureIssuer bool
}

// OIDCVerifier performs discovery once and uses go-oidc's concurrency-safe
// remote key set. Known keys remain cached; an unknown kid triggers a JWKS
// refresh, which supports normal signing-key rotation.
type OIDCVerifier struct {
	issuer   string
	audience string
	kind     PrincipalKind
	verifier *oidc.IDTokenVerifier
}

func NewOIDCVerifier(ctx context.Context, cfg OIDCVerifierConfig) (*OIDCVerifier, error) {
	if err := validateVerifierConfig(cfg); err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer: %w", err)
	}
	if provider.Endpoint().AuthURL == "" || provider.Endpoint().TokenURL == "" {
		return nil, errors.New("OIDC discovery is missing required endpoints")
	}
	return &OIDCVerifier{
		issuer:   cfg.Issuer,
		audience: cfg.Audience,
		kind:     cfg.PrincipalKind,
		verifier: provider.Verifier(&oidc.Config{
			ClientID:             cfg.Audience,
			SupportedSigningAlgs: []string{oidc.RS256},
		}),
	}, nil
}

func (v *OIDCVerifier) Verify(ctx context.Context, rawToken string) (Principal, error) {
	return v.verify(ctx, rawToken, "")
}

// VerifyNonce verifies an ID token and requires the nonce claim to match the
// value generated for the corresponding authorization request.
func (v *OIDCVerifier) VerifyNonce(ctx context.Context, rawToken, expectedNonce string) (Principal, error) {
	if strings.TrimSpace(expectedNonce) == "" {
		return Principal{}, fmt.Errorf("%w: nonce is required", ErrInvalidToken)
	}
	return v.verify(ctx, rawToken, expectedNonce)
}

func (v *OIDCVerifier) verify(ctx context.Context, rawToken, expectedNonce string) (Principal, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Principal{}, ErrInvalidToken
	}
	verified, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if verified.Subject == "" {
		return Principal{}, fmt.Errorf("%w: subject is empty", ErrInvalidToken)
	}

	var claims struct {
		Email             string   `json:"email"`
		EmailVerified     bool     `json:"email_verified"`
		Name              string   `json:"name"`
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
		Nonce             string   `json:"nonce"`
		Scope             string   `json:"scope"`
	}
	if err := verified.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("%w: decode claims", ErrInvalidToken)
	}
	if expectedNonce != "" && claims.Nonce != expectedNonce {
		return Principal{}, fmt.Errorf("%w: nonce mismatch", ErrInvalidToken)
	}

	return Principal{
		Kind:              v.kind,
		Issuer:            verified.Issuer,
		Subject:           verified.Subject,
		Audience:          append([]string(nil), verified.Audience...),
		Email:             claims.Email,
		EmailVerified:     claims.EmailVerified,
		Name:              claims.Name,
		PreferredUsername: claims.PreferredUsername,
		Groups:            append([]string(nil), claims.Groups...),
		Scopes:            strings.Fields(claims.Scope),
	}, nil
}

func validateVerifierConfig(cfg OIDCVerifierConfig) error {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return errors.New("OIDC issuer is required")
	}
	issuerURL, err := url.Parse(cfg.Issuer)
	if err != nil || issuerURL.Host == "" || (issuerURL.Scheme != "https" && issuerURL.Scheme != "http") {
		return errors.New("OIDC issuer must be an absolute HTTP(S) URL")
	}
	if issuerURL.Scheme != "https" && !cfg.AllowInsecureIssuer {
		return errors.New("OIDC issuer must use HTTPS")
	}
	if strings.TrimSpace(cfg.Audience) == "" {
		return errors.New("OIDC audience is required")
	}
	if cfg.PrincipalKind != PrincipalUser && cfg.PrincipalKind != PrincipalService {
		return errors.New("OIDC principal kind must be user or service")
	}
	return nil
}
