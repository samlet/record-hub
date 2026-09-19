package projection

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"net/http"
	"strconv"
)

type TenderApplicationAssociationHTTPService interface {
	List(context.Context, identity.Principal, TenderApplicationAssociationQuery) ([]TenderApplicationAssociation, error)
}
type TenderApplicationAssociationHTTPHandler struct {
	service TenderApplicationAssociationHTTPService
}

func NewCombinedAssociationHTTPHandler(project AssociationHTTPService, tender TenderApplicationAssociationHTTPService, settlement SettlementAssociationHTTPService) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/associations/project-applications", NewAssociationHTTPHandler(project))
	mux.Handle("/api/v1/associations/tender-applications", NewTenderApplicationAssociationHTTPHandler(tender))
	mux.Handle("/api/v1/associations/settlements", NewSettlementAssociationHTTPHandler(settlement))
	return mux
}
func NewTenderApplicationAssociationHTTPHandler(service TenderApplicationAssociationHTTPService) http.Handler {
	h := &TenderApplicationAssociationHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/associations/tender-applications", h.list)
	return mux
}
func (handler *TenderApplicationAssociationHTTPHandler) list(writer http.ResponseWriter, request *http.Request) {
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
	query := TenderApplicationAssociationQuery{TenantID: request.URL.Query().Get("tenantId"), WorkspaceID: request.URL.Query().Get("workspaceId"), TenderRef: request.URL.Query().Get("tenderRef"), ApplicationRef: request.URL.Query().Get("applicationRef"), Limit: limit}
	if handler == nil || handler.service == nil {
		writeAssociationError(writer, http.StatusNotImplemented, "ASSOCIATION_API_UNAVAILABLE", "The association API is not configured.")
		return
	}
	items, err := handler.service.List(request.Context(), principal, query)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrUnauthenticated):
			writeAssociationError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		case errors.Is(err, identity.ErrForbidden):
			writeAssociationError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
		default:
			writeAssociationError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The association query is invalid.")
		}
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(map[string]any{"items": items})
}
