package projection

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type AssociationHTTPService interface {
	List(context.Context, identity.Principal, AssociationQuery) ([]ProjectApplicationAssociation, error)
}

type AssociationHTTPHandler struct{ service AssociationHTTPService }

func NewAssociationHTTPHandler(service AssociationHTTPService) http.Handler {
	handler := &AssociationHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/associations/project-applications", handler.list)
	return mux
}

func (handler *AssociationHTTPHandler) list(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAssociationError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	limit := 0
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeAssociationError(writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be between 1 and 100.")
			return
		}
		limit = parsed
	}
	query := AssociationQuery{TenantID: request.URL.Query().Get("tenantId"), WorkspaceID: request.URL.Query().Get("workspaceId"), ProjectRef: request.URL.Query().Get("projectRef"), ApplicationRef: request.URL.Query().Get("applicationRef"), Limit: limit}
	if handler == nil || handler.service == nil {
		writeAssociationError(writer, http.StatusNotImplemented, "ASSOCIATION_API_UNAVAILABLE", "The association API is not configured.")
		return
	}
	items, err := handler.service.List(request.Context(), principal, query)
	if err != nil {
		writeAssociationServiceError(writer, err)
		return
	}
	writeAssociationJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func writeAssociationServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeAssociationError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeAssociationError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrAssociationQueryInvalid):
		writeAssociationError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The association query is invalid.")
	case errors.Is(err, ErrAssociationUnavailable):
		writeAssociationError(writer, http.StatusNotImplemented, "ASSOCIATION_API_UNAVAILABLE", "The association API is not configured.")
	case errors.Is(err, ErrAssociationConflict):
		writeAssociationError(writer, http.StatusConflict, "ASSOCIATION_CONFLICT", "The association projection is in conflict.")
	default:
		writeAssociationError(writer, http.StatusInternalServerError, "ASSOCIATION_QUERY_FAILED", "The association projection could not be loaded.")
	}
}

func writeAssociationError(writer http.ResponseWriter, status int, code, message string) {
	writeAssociationJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeAssociationJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
