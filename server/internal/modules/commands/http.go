package commands

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
	Submit(context.Context, identity.Principal, SubmitRequest) (Operation, bool, error)
	Get(context.Context, identity.Principal, string, string, string) (Operation, error)
}

type HTTPHandler struct{ service HTTPService }

func NewHTTPHandler(service HTTPService) http.Handler {
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/commands", handler.submit)
	mux.HandleFunc("GET /api/v1/commands/{operationID}", handler.get)
	return mux
}

type submitBody struct {
	TenantID        string          `json:"tenantId"`
	WorkspaceID     string          `json:"workspaceId"`
	PolicyID        string          `json:"policyId"`
	ResourceRef     string          `json:"resourceRef"`
	ExpectedVersion *int64          `json:"expectedVersion"`
	Payload         json.RawMessage `json:"payload"`
}

func (handler *HTTPHandler) submit(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.service == nil {
		writeCommandError(writer, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The command gateway is not configured.")
		return
	}
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeCommandError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	var body submitBody
	if !decodeCommandBody(writer, request, &body) {
		return
	}
	operation, replayed, err := handler.service.Submit(request.Context(), principal, SubmitRequest{TenantID: body.TenantID, WorkspaceID: body.WorkspaceID, PolicyID: body.PolicyID, ResourceRef: body.ResourceRef, ExpectedVersion: body.ExpectedVersion, Payload: body.Payload, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		writeCommandServiceError(writer, err)
		return
	}
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	writeCommandJSON(writer, status, operation)
}

func (handler *HTTPHandler) get(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.service == nil {
		writeCommandError(writer, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The command gateway is not configured.")
		return
	}
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeCommandError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	tenantID := strings.TrimSpace(request.URL.Query().Get("tenantId"))
	workspaceID := strings.TrimSpace(request.URL.Query().Get("workspaceId"))
	if tenantID == "" || workspaceID == "" {
		writeCommandError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId and workspaceId are required.")
		return
	}
	operation, err := handler.service.Get(request.Context(), principal, tenantID, workspaceID, request.PathValue("operationID"))
	if err != nil {
		writeCommandServiceError(writer, err)
		return
	}
	writeCommandJSON(writer, http.StatusOK, operation)
}

func decodeCommandBody(writer http.ResponseWriter, request *http.Request, target interface{}) bool {
	if request.ContentLength > MaxPayloadBytes+16<<10 {
		writeCommandError(writer, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "The command request exceeds the bounded payload limit.")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, MaxPayloadBytes+16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeCommandError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The command request is invalid.")
		return false
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeCommandError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The command request must contain one JSON object.")
		return false
	}
	return true
}

func writeCommandJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeCommandServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeCommandError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, ErrCommandPolicyDenied):
		writeCommandError(writer, http.StatusForbidden, "COMMAND_POLICY_DENIED", "The principal is not authorized for this command policy.")
	case errors.Is(err, ErrCommandPolicyNotFound):
		writeCommandError(writer, http.StatusForbidden, "COMMAND_POLICY_DENIED", "The command policy is not available.")
	case errors.Is(err, ErrCommandUnavailable):
		writeCommandError(writer, http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The command gateway is not configured.")
	case errors.Is(err, ErrCommandIdempotency):
		writeCommandError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different input.")
	case errors.Is(err, ErrCommandExpectedVersion):
		writeCommandError(writer, http.StatusBadRequest, "EXPECTED_VERSION_REQUIRED", "expectedVersion is required by the command policy.")
	case errors.Is(err, ErrCommandNotFound):
		writeCommandError(writer, http.StatusNotFound, "COMMAND_NOT_FOUND", "The command operation was not found.")
	case errors.Is(err, ErrCommandPayloadTooLarge):
		writeCommandError(writer, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The command payload exceeds the bounded limit.")
	default:
		writeCommandError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The command request is invalid.")
	}
}

func writeCommandError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
