package binding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type HTTPService interface {
	CreateSnapshot(context.Context, identity.Principal, SnapshotRequest) (Snapshot, bool, error)
	GetSnapshot(context.Context, identity.Principal, string, string, string) (Snapshot, error)
}

type HTTPHandler struct {
	service HTTPService
}

func NewHTTPHandler(service HTTPService) http.Handler {
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/bindings/snapshots", handler.createSnapshot)
	mux.HandleFunc("GET /api/v1/bindings/snapshots/{snapshotID}", handler.getSnapshot)
	return mux
}

type snapshotRequestBody struct {
	TenantID              string `json:"tenantId"`
	WorkspaceID           string `json:"workspaceId"`
	RecordRef             string `json:"recordRef"`
	SchemaID              string `json:"schemaId"`
	SchemaVersion         int64  `json:"schemaVersion"`
	ExpectedRecordVersion int64  `json:"expectedRecordVersion"`
	ExpectedSourceVersion int64  `json:"expectedSourceVersion"`
	Purpose               string `json:"purpose"`
}

func (handler *HTTPHandler) createSnapshot(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.service == nil {
		writeBindingError(writer, http.StatusServiceUnavailable, "BINDING_UNAVAILABLE", "The binding service is not configured.")
		return
	}
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeBindingError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	var body snapshotRequestBody
	if !decodeSnapshotBody(writer, request, &body) {
		return
	}
	operationID := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	snapshot, replayed, err := handler.service.CreateSnapshot(request.Context(), principal, SnapshotRequest{TenantID: body.TenantID, WorkspaceID: body.WorkspaceID, RecordRef: body.RecordRef, SchemaID: body.SchemaID, SchemaVersion: body.SchemaVersion, ExpectedRecordVersion: body.ExpectedRecordVersion, ExpectedSourceVersion: body.ExpectedSourceVersion, Purpose: body.Purpose, OperationID: operationID})
	if err != nil {
		writeBindingServiceError(writer, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeSnapshot(writer, status, snapshot)
}

func (handler *HTTPHandler) getSnapshot(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.service == nil {
		writeBindingError(writer, http.StatusServiceUnavailable, "BINDING_UNAVAILABLE", "The binding service is not configured.")
		return
	}
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeBindingError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	tenantID, workspaceID := strings.TrimSpace(request.URL.Query().Get("tenantId")), strings.TrimSpace(request.URL.Query().Get("workspaceId"))
	if tenantID == "" || workspaceID == "" {
		writeBindingError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId and workspaceId are required.")
		return
	}
	snapshot, err := handler.service.GetSnapshot(request.Context(), principal, tenantID, workspaceID, request.PathValue("snapshotID"))
	if err != nil {
		writeBindingServiceError(writer, err)
		return
	}
	writeSnapshot(writer, http.StatusOK, snapshot)
}

func decodeSnapshotBody(writer http.ResponseWriter, request *http.Request, target interface{}) bool {
	if request.ContentLength > 2<<20 {
		writeBindingError(writer, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request body exceeds the 2 MiB limit.")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeBindingError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeBindingError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func writeSnapshot(writer http.ResponseWriter, status int, snapshot Snapshot) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(snapshot)
}

func writeBindingServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeBindingError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, ErrMachinePolicyDenied):
		writeBindingError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this binding.")
	case errors.Is(err, ErrBindingUnavailable):
		writeBindingError(writer, http.StatusServiceUnavailable, "BINDING_UNAVAILABLE", "The binding service is not configured.")
	case errors.Is(err, ErrIdempotencyKeyRequired):
		writeBindingError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required.")
	case errors.Is(err, ErrIdempotencyConflict):
		writeBindingError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different input.")
	case errors.Is(err, ErrSnapshotNotFound):
		writeBindingError(writer, http.StatusNotFound, "SNAPSHOT_NOT_FOUND", "The binding snapshot was not found.")
	case errors.Is(err, ErrRecordNotFound):
		writeBindingError(writer, http.StatusNotFound, "RECORD_NOT_FOUND", "The source record was not found.")
	case errors.Is(err, ErrRecordVersionConflict):
		writeBindingError(writer, http.StatusConflict, "RECORD_VERSION_CONFLICT", "The source record version is stale.")
	case errors.Is(err, ErrSourceVersionConflict):
		writeBindingError(writer, http.StatusConflict, "SOURCE_VERSION_CONFLICT", "The source version is stale.")
	case errors.Is(err, ErrSchemaMismatch):
		writeBindingError(writer, http.StatusConflict, "SCHEMA_MISMATCH", "The requested schema does not match the source record.")
	default:
		writeBindingError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The binding request is invalid.")
	}
}

func writeBindingError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
