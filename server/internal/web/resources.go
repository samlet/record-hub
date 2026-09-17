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
func NewResourceRouter(recordsHandler, schemaHandler http.Handler, catalogHandlers ...http.Handler) http.Handler {
	var catalogHandler http.Handler
	if len(catalogHandlers) > 0 {
		catalogHandler = catalogHandlers[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSchemaPath(r.URL.Path) {
			serveResourceHandler(w, r, schemaHandler)
			return
		}
		if isCatalogPath(r.URL.Path) {
			serveResourceHandler(w, r, catalogHandler)
			return
		}
		serveResourceHandler(w, r, recordsHandler)
	})
}

// NewConsoleRouter adds the read-only Operations page/API to the resource
// router. Projection handlers are passed in rather than constructed here so
// Mongo repositories and membership policy remain owned by their modules.
func NewConsoleRouter(recordsHandler, schemaHandler, operationsHandler http.Handler, catalogHandlers ...http.Handler) http.Handler {
	resources := NewResourceRouter(recordsHandler, schemaHandler, catalogHandlers...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/operations/events" || r.URL.Path == "/api/v1/operations/events" {
			serveResourceHandler(w, r, operationsHandler)
			return
		}
		resources.ServeHTTP(w, r)
	})
}

func isSchemaPath(path string) bool {
	return path == "/api/v1/schemas" || strings.HasPrefix(path, "/api/v1/schemas/")
}

func isCatalogPath(path string) bool {
	return path == "/api/v1/sources" || strings.HasPrefix(path, "/api/v1/sources/") || path == "/api/v1/mappings" || strings.HasPrefix(path, "/api/v1/mappings/")
}

func serveResourceHandler(w http.ResponseWriter, r *http.Request, handler http.Handler) {
	if handler == nil {
		http.Error(w, "resource service unavailable", http.StatusServiceUnavailable)
		return
	}
	handler.ServeHTTP(w, r)
}
