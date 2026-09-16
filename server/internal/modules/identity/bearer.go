package identity

import (
	"net/http"
	"strings"
)

// BearerMiddleware verifies an Authorization bearer token and places the
// resulting principal in request context. Existing browser sessions remain
// authoritative when present, so this adapter can be composed outside the
// Web BFF middleware without changing the session contract.
func BearerMiddleware(verifier TokenVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFromContext(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		raw := strings.TrimSpace(r.Header.Get("Authorization"))
		if raw == "" {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.Fields(raw)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || verifier == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		principal, err := verifier.Verify(r.Context(), parts[1])
		if err != nil || (principal.Kind != PrincipalUser && principal.Kind != PrincipalService) || principal.Issuer == "" || principal.Subject == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}
