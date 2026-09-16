package web

import (
	"context"
	"fmt"
	"sync"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

// LazyNonceVerifier defers OIDC discovery until the first callback. This keeps
// API startup independent from Dex availability while still caching the
// concurrency-safe JWKS verifier after discovery succeeds. A failed discovery
// is retried on the next callback instead of being permanently memoized.
type LazyNonceVerifier struct {
	cfg identity.OIDCVerifierConfig

	mu       sync.Mutex
	verifier identity.NonceVerifier
}

func NewLazyNonceVerifier(cfg identity.OIDCVerifierConfig) *LazyNonceVerifier {
	return &LazyNonceVerifier{cfg: cfg}
}

func (v *LazyNonceVerifier) VerifyNonce(ctx context.Context, rawToken, nonce string) (identity.Principal, error) {
	v.mu.Lock()
	verifier := v.verifier
	if verifier == nil {
		var err error
		verifier, err = identity.NewOIDCVerifier(ctx, v.cfg)
		if err != nil {
			v.mu.Unlock()
			return identity.Principal{}, fmt.Errorf("discover OIDC verifier: %w", err)
		}
		v.verifier = verifier
	}
	v.mu.Unlock()
	return verifier.VerifyNonce(ctx, rawToken, nonce)
}
