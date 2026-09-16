package web

import (
	"net/http"
	"strings"
)

// NewResourceRouter composes the existing records and schema REST handlers
// behind one browser-facing /api/v1 boundary. The handlers remain owned by
// their domain modules; this router only performs deterministic path
// selection, so authorization cannot be bypassed by a second Web route.
//
// A nil handler is treated as an unavailable dependency and returns 503. This
// allows the API process to expose health/auth routes while repositories are
// still being assembled during a staged rollout.
func NewResourceRouter(recordsHandler, schemaHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSchemaPath(r.URL.Path) {
			serveResourceHandler(w, r, schemaHandler)
			return
		}
		serveResourceHandler(w, r, recordsHandler)
	})
}

func isSchemaPath(path string) bool {
	return path == "/api/v1/schemas" || strings.HasPrefix(path, "/api/v1/schemas/")
}

func serveResourceHandler(w http.ResponseWriter, r *http.Request, handler http.Handler) {
	if handler == nil {
		http.Error(w, "resource service unavailable", http.StatusServiceUnavailable)
		return
	}
	handler.ServeHTTP(w, r)
}
