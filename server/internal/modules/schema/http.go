package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type MutationService interface {
	CreateDraft(context.Context, identity.Principal, DraftInput) (Definition, error)
	UpdateDraft(context.Context, identity.Principal, DraftInput, int64) (Definition, error)
	Publish(context.Context, identity.Principal, DraftInput, int64) (Definition, error)
}

type ReadService interface {
	GetDefinition(context.Context, identity.Principal, string, string, string, int64) (Definition, error)
}

type ListService interface {
	ListDefinitions(context.Context, identity.Principal, string, string, int64) ([]Definition, error)
}

type HTTPHandler struct {
	service MutationService
}

func NewHTTPHandler(service MutationService) http.Handler {
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/schemas", handler.create)
	mux.HandleFunc("GET /api/v1/schemas", handler.list)
	mux.HandleFunc("GET /api/v1/schemas/{schemaID}", handler.get)
	mux.HandleFunc("PUT /api/v1/schemas/{schemaID}/draft", handler.update)
	mux.HandleFunc("POST /api/v1/schemas/{schemaID}/publish", handler.publish)
	return mux
}

func (handler *HTTPHandler) list(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	limit := int64(50)
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 || parsed > 100 {
			writeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be between 1 and 100.")
			return
		}
		limit = parsed
	}
	service, ok := handler.service.(ListService)
	if !ok || service == nil {
		writeAPIError(writer, http.StatusNotImplemented, "SCHEMA_LIST_UNAVAILABLE", "The schema list API is not configured.")
		return
	}
	definitions, err := service.ListDefinitions(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), limit)
	if err != nil {
		writeSchemaError(writer, err)
		return
	}
	items := make([]definitionSummaryResponse, 0, len(definitions))
	for _, definition := range definitions {
		items = append(items, definitionSummary(definition))
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{"items": items})
}

func (handler *HTTPHandler) get(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	version, err := strconv.ParseInt(strings.TrimSpace(request.URL.Query().Get("version")), 10, 64)
	if err != nil || version < 1 {
		writeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "version must be a positive integer.")
		return
	}
	service, ok := handler.service.(ReadService)
	if !ok || service == nil {
		writeAPIError(writer, http.StatusNotImplemented, "SCHEMA_READ_UNAVAILABLE", "The schema read API is not configured.")
		return
	}
	definition, err := service.GetDefinition(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), request.PathValue("schemaID"), version)
	if err != nil {
		writeSchemaError(writer, err)
		return
	}
	writeDefinition(writer, http.StatusOK, definition)
}

type mutationRequest struct {
	TenantID      string          `json:"tenantId"`
	WorkspaceID   string          `json:"workspaceId"`
	SchemaID      string          `json:"schemaId"`
	Name          string          `json:"name"`
	Version       int64           `json:"version"`
	JSONSchema    json.RawMessage `json:"jsonSchema"`
	SemanticTypes []string        `json:"semanticTypes"`
}

type publishRequest struct {
	TenantID    string `json:"tenantId"`
	WorkspaceID string `json:"workspaceId"`
	Version     int64  `json:"version"`
}

func (handler *HTTPHandler) create(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	var input mutationRequest
	if !decodeMutationRequest(writer, request, &input) {
		return
	}
	draft, err := input.draft(request)
	if err != nil {
		writeSchemaError(writer, err)
		return
	}
	definition, err := handler.service.CreateDraft(request.Context(), principal, draft)
	if err != nil {
		writeSchemaError(writer, err)
		return
	}
	writeDefinition(writer, http.StatusCreated, definition)
}

func (handler *HTTPHandler) update(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	expectedRevision, ok := parseIfMatch(request.Header.Get("If-Match"))
	if !ok {
		writeAPIError(writer, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "If-Match must contain the expected schema revision.")
		return
	}
	var input mutationRequest
	if !decodeMutationRequest(writer, request, &input) {
		return
	}
	input.SchemaID = request.PathValue("schemaID")
	draft, err := input.draft(request)
	if err != nil {
		writeSchemaError(writer, err)
		return
	}
	definition, err := handler.service.UpdateDraft(request.Context(), principal, draft, expectedRevision)
	if err != nil {
		writeSchemaError(writer, err)
		return
	}
	writeDefinition(writer, http.StatusOK, definition)
}

func (handler *HTTPHandler) publish(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	expectedRevision, ok := parseIfMatch(request.Header.Get("If-Match"))
	if !ok {
		writeAPIError(writer, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "If-Match must contain the expected schema revision.")
		return
	}
	var input publishRequest
	if !decodeMutationRequest(writer, request, &input) {
		return
	}
	draft := DraftInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, SchemaID: request.PathValue("schemaID"), Version: input.Version, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")}
	if draft.TenantID == "" || draft.WorkspaceID == "" || draft.Version < 1 {
		writeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId, workspaceId, and positive version are required.")
		return
	}
	definition, err := handler.service.Publish(request.Context(), principal, draft, expectedRevision)
	if err != nil {
		writeSchemaError(writer, err)
		return
	}
	writeDefinition(writer, http.StatusOK, definition)
}

func (input mutationRequest) draft(request *http.Request) (DraftInput, error) {
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.SchemaID) == "" || strings.TrimSpace(input.Name) == "" || input.Version < 1 {
		return DraftInput{}, errors.New("tenantId, workspaceId, schemaId, name, and positive version are required")
	}
	if len(input.JSONSchema) == 0 {
		return DraftInput{}, errors.New("jsonSchema is required")
	}
	var schemaDocument bson.Raw
	if err := bson.UnmarshalExtJSON(input.JSONSchema, false, &schemaDocument); err != nil {
		return DraftInput{}, fmt.Errorf("jsonSchema must be an object: %w", err)
	}
	return DraftInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, SchemaID: input.SchemaID, Name: input.Name, Version: input.Version, JSONSchema: schemaDocument, SemanticTypes: input.SemanticTypes, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")}, nil
}

func decodeMutationRequest(writer http.ResponseWriter, request *http.Request, target interface{}) bool {
	if request.ContentLength > 2<<20 {
		writeAPIError(writer, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request body exceeds the 2 MiB limit.")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func parseIfMatch(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	return revision, err == nil && revision > 0
}

type definitionResponse struct {
	TenantID      string                `json:"tenantId"`
	SchemaID      string                `json:"schemaId"`
	Name          string                `json:"name"`
	Version       int64                 `json:"version"`
	Revision      int64                 `json:"revision"`
	Status        Status                `json:"status"`
	JSONSchema    json.RawMessage       `json:"jsonSchema"`
	SemanticTypes []string              `json:"semanticTypes"`
	ContentHash   string                `json:"contentHash,omitempty"`
	CreatedBy     identity.IdentityKey  `json:"createdBy"`
	PublishedBy   *identity.IdentityKey `json:"publishedBy,omitempty"`
	CreatedAt     string                `json:"createdAt"`
	UpdatedAt     string                `json:"updatedAt"`
	PublishedAt   *string               `json:"publishedAt,omitempty"`
}

type definitionSummaryResponse struct {
	TenantID      string   `json:"tenantId"`
	SchemaID      string   `json:"schemaId"`
	Name          string   `json:"name"`
	Version       int64    `json:"version"`
	Revision      int64    `json:"revision"`
	Status        Status   `json:"status"`
	SemanticTypes []string `json:"semanticTypes"`
	ContentHash   string   `json:"contentHash,omitempty"`
	UpdatedAt     string   `json:"updatedAt"`
}

func definitionSummary(definition Definition) definitionSummaryResponse {
	return definitionSummaryResponse{
		TenantID: definition.TenantID, SchemaID: definition.SchemaID, Name: definition.Name,
		Version: definition.Version, Revision: definition.Revision, Status: definition.Status,
		SemanticTypes: definition.SemanticTypes, ContentHash: definition.ContentHash,
		UpdatedAt: definition.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeDefinition(writer http.ResponseWriter, status int, definition Definition) {
	jsonSchema, err := bson.MarshalExtJSON(definition.JSONSchema, false, false)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "RESPONSE_ENCODING_FAILED", "Response could not be encoded.")
		return
	}
	response := definitionResponse{TenantID: definition.TenantID, SchemaID: definition.SchemaID, Name: definition.Name, Version: definition.Version, Revision: definition.Revision, Status: definition.Status, JSONSchema: json.RawMessage(jsonSchema), SemanticTypes: definition.SemanticTypes, ContentHash: definition.ContentHash, CreatedBy: definition.CreatedBy, PublishedBy: definition.PublishedBy, CreatedAt: definition.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: definition.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	if definition.PublishedAt != nil {
		publishedAt := definition.PublishedAt.UTC().Format(time.RFC3339Nano)
		response.PublishedAt = &publishedAt
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(definition.Revision, 10)))
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}

func writeSchemaError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeAPIError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrNotFound):
		writeAPIError(writer, http.StatusNotFound, "SCHEMA_NOT_FOUND", "The schema version was not found.")
	case errors.Is(err, ErrIdempotencyKeyRequired):
		writeAPIError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required.")
	case errors.Is(err, ErrIdempotencyConflict):
		writeAPIError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different input.")
	case errors.Is(err, ErrRevisionConflict):
		writeAPIError(writer, http.StatusConflict, "SCHEMA_REVISION_CONFLICT", "The schema revision is stale.")
	case errors.Is(err, ErrImmutable):
		writeAPIError(writer, http.StatusConflict, "SCHEMA_IMMUTABLE", "Published schemas cannot be changed.")
	case errors.Is(err, ErrDuplicate):
		writeAPIError(writer, http.StatusConflict, "SCHEMA_ALREADY_EXISTS", "The schema version already exists.")
	case errors.Is(err, ErrInvalidSchema), errors.Is(err, ErrInvalidSemanticType):
		writeAPIError(writer, http.StatusBadRequest, "INVALID_SCHEMA", "The schema or semantic type URI is invalid.")
	default:
		writeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The schema mutation request is invalid.")
	}
}

func writeAPIError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
