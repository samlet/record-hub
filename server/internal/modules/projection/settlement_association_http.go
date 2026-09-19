package projection

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type SettlementAssociationHTTPService interface {
	List(context.Context, identity.Principal, SettlementAssociationQuery) ([]SettlementAssociation, error)
}

func NewSettlementAssociationHTTPHandler(service SettlementAssociationHTTPService) http.Handler {
	handler := &settlementAssociationHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/associations/settlements", handler.list)
	return mux
}

type settlementAssociationHTTPHandler struct {
	service SettlementAssociationHTTPService
}

func (handler *settlementAssociationHTTPHandler) list(writer http.ResponseWriter, request *http.Request) {
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
	query := SettlementAssociationQuery{TenantID: request.URL.Query().Get("tenantId"), WorkspaceID: request.URL.Query().Get("workspaceId"), SettlementRef: request.URL.Query().Get("settlementRef"), ApprovalRef: request.URL.Query().Get("approvalRef"), Limit: limit}
	if handler == nil || handler.service == nil {
		writeAssociationError(writer, http.StatusNotImplemented, "ASSOCIATION_API_UNAVAILABLE", "The settlement association API is not configured.")
		return
	}
	items, err := handler.service.List(request.Context(), principal, query)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrUnauthenticated):
			writeAssociationError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		case errors.Is(err, identity.ErrForbidden):
			writeAssociationError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
		case errors.Is(err, ErrSettlementAssociationQueryInvalid):
			writeAssociationError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The settlement association query is invalid.")
		default:
			writeAssociationError(writer, http.StatusInternalServerError, "ASSOCIATION_QUERY_FAILED", "The settlement association could not be loaded.")
		}
		return
	}
	writeAssociationJSON(writer, http.StatusOK, map[string]any{"items": items})
}
