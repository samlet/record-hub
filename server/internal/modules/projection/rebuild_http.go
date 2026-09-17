package projection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type ProjectionRebuildHTTPService interface {
	Start(context.Context, identity.Principal, RebuildStartInput) (ProjectionRebuildOperation, bool, error)
	Get(context.Context, identity.Principal, string, string, string) (ProjectionRebuildOperation, error)
	Cancel(context.Context, identity.Principal, RebuildCancelInput) (ProjectionRebuildOperation, bool, error)
}

type ProjectionRebuildHTTPHandler struct{ service ProjectionRebuildHTTPService }

func NewProjectionRebuildHTTPHandler(service ProjectionRebuildHTTPService) http.Handler {
	handler := &ProjectionRebuildHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projection/rebuilds", handler.start)
	mux.HandleFunc("GET /api/v1/projection/rebuilds/{operationID}", handler.get)
	mux.HandleFunc("POST /api/v1/projection/rebuilds/{operationID}/cancel", handler.cancel)
	return mux
}

type rebuildScopeRequest struct {
	TenantID    string `json:"tenantId"`
	WorkspaceID string `json:"workspaceId"`
}

func (handler *ProjectionRebuildHTTPHandler) start(writer http.ResponseWriter, request *http.Request) {
	principal, ok := rebuildPrincipal(writer, request)
	if !ok {
		return
	}
	var input rebuildScopeRequest
	if !decodeRebuildRequest(writer, request, &input) {
		return
	}
	if handler == nil || handler.service == nil {
		writeRebuildError(writer, http.StatusNotImplemented, "REBUILD_API_UNAVAILABLE", "The projection rebuild API is not configured.")
		return
	}
	operation, replay, err := handler.service.Start(request.Context(), principal, RebuildStartInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		writeRebuildDomainError(writer, err)
		return
	}
	status := http.StatusAccepted
	if replay {
		status = http.StatusOK
	}
	writeRebuildJSON(writer, status, operation)
}

func (handler *ProjectionRebuildHTTPHandler) get(writer http.ResponseWriter, request *http.Request) {
	principal, ok := rebuildPrincipal(writer, request)
	if !ok {
		return
	}
	if handler == nil || handler.service == nil {
		writeRebuildError(writer, http.StatusNotImplemented, "REBUILD_API_UNAVAILABLE", "The projection rebuild API is not configured.")
		return
	}
	operation, err := handler.service.Get(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), request.PathValue("operationID"))
	if err != nil {
		writeRebuildDomainError(writer, err)
		return
	}
	writeRebuildJSON(writer, http.StatusOK, operation)
}

func (handler *ProjectionRebuildHTTPHandler) cancel(writer http.ResponseWriter, request *http.Request) {
	principal, ok := rebuildPrincipal(writer, request)
	if !ok {
		return
	}
	var input rebuildScopeRequest
	if !decodeRebuildRequest(writer, request, &input) {
		return
	}
	if handler == nil || handler.service == nil {
		writeRebuildError(writer, http.StatusNotImplemented, "REBUILD_API_UNAVAILABLE", "The projection rebuild API is not configured.")
		return
	}
	operation, replay, err := handler.service.Cancel(request.Context(), principal, RebuildCancelInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, OperationID: request.PathValue("operationID"), RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		writeRebuildDomainError(writer, err)
		return
	}
	status := http.StatusAccepted
	if replay {
		status = http.StatusOK
	}
	writeRebuildJSON(writer, status, operation)
}

func rebuildPrincipal(writer http.ResponseWriter, request *http.Request) (identity.Principal, bool) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeRebuildError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return identity.Principal{}, false
	}
	return principal, true
}

func decodeRebuildRequest(writer http.ResponseWriter, request *http.Request, target interface{}) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeRebuildError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		writeRebuildError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func writeRebuildJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeRebuildDomainError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeRebuildError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeRebuildError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrRebuildNotFound):
		writeRebuildError(writer, http.StatusNotFound, "REBUILD_NOT_FOUND", "The projection rebuild operation was not found.")
	case errors.Is(err, ErrRebuildExists), errors.Is(err, ErrRebuildRevisionConflict), errors.Is(err, ErrRebuildNotCancellable), errors.Is(err, ErrRebuildIdempotencyConflict):
		writeRebuildError(writer, http.StatusConflict, "REBUILD_CONFLICT", "The projection rebuild operation conflicts with its current state.")
	case errors.Is(err, ErrIdempotencyKeyMissing):
		writeRebuildError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required.")
	case errors.Is(err, ErrRequestIDMissing):
		writeRebuildError(writer, http.StatusBadRequest, "REQUEST_ID_REQUIRED", "X-Request-ID is required.")
	case errors.Is(err, ErrRebuildInvalid):
		writeRebuildError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The projection rebuild request is invalid.")
	case errors.Is(err, ErrRebuildUnavailable):
		writeRebuildError(writer, http.StatusServiceUnavailable, "REBUILD_UNAVAILABLE", "The projection rebuild API is not configured.")
	default:
		writeRebuildError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The projection rebuild request could not be completed.")
	}
}

func writeRebuildError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
