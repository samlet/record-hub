package projection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
)

// CatalogHTTPService is the narrow transport contract for source/mapping
// registration. It keeps the HTTP surface independent of MongoDB.
type CatalogHTTPService interface {
	CreateSource(context.Context, identity.Principal, SourceCreateInput) (SourceRegistration, bool, error)
	ListSources(context.Context, identity.Principal, string, string, int64) ([]SourceRegistration, error)
	PublishSource(context.Context, identity.Principal, CatalogTransitionInput) (SourceRegistration, bool, error)
	CreateMapping(context.Context, identity.Principal, MappingCreateInput) (MappingRegistration, bool, error)
	ListMappings(context.Context, identity.Principal, string, string, string, int64) ([]MappingRegistration, error)
	PublishMapping(context.Context, identity.Principal, CatalogTransitionInput) (MappingRegistration, bool, error)
	RevokeMapping(context.Context, identity.Principal, CatalogTransitionInput) (MappingRegistration, bool, error)
}

type CatalogHTTPHandler struct{ service CatalogHTTPService }

func NewCatalogHTTPHandler(service CatalogHTTPService) http.Handler {
	handler := &CatalogHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sources", handler.createSource)
	mux.HandleFunc("GET /api/v1/sources", handler.listSources)
	mux.HandleFunc("POST /api/v1/sources/{sourceID}/publish", handler.publishSource)
	mux.HandleFunc("POST /api/v1/mappings", handler.createMapping)
	mux.HandleFunc("GET /api/v1/mappings", handler.listMappings)
	mux.HandleFunc("POST /api/v1/mappings/{mappingID}/publish", handler.publishMapping)
	mux.HandleFunc("POST /api/v1/mappings/{mappingID}/revoke", handler.revokeMapping)
	return mux
}

type sourceRequest struct {
	TenantID         string           `json:"tenantId"`
	WorkspaceID      string           `json:"workspaceId"`
	SourceID         string           `json:"sourceId"`
	EventType        string           `json:"eventType"`
	EventVersion     int64            `json:"eventVersion"`
	OwnerContact     string           `json:"ownerContact"`
	TenantResolution TenantResolution `json:"tenantResolution"`
}

type mappingRequest struct {
	TenantID            string            `json:"tenantId"`
	WorkspaceID         string            `json:"workspaceId"`
	MappingID           string            `json:"mappingId"`
	SourceID            string            `json:"sourceId"`
	EventType           string            `json:"eventType"`
	EventVersion        int64             `json:"eventVersion"`
	TargetTableID       string            `json:"targetTableId"`
	TargetSchemaID      string            `json:"targetSchemaId"`
	TargetSchemaVersion int64             `json:"targetSchemaVersion"`
	FieldMap            map[string]string `json:"fieldMap"`
	Fixture             MappingFixture    `json:"fixture"`
}

type transitionRequest struct {
	TenantID    string `json:"tenantId"`
	WorkspaceID string `json:"workspaceId"`
}

func (handler *CatalogHTTPHandler) createSource(writer http.ResponseWriter, request *http.Request) {
	principal, ok := catalogPrincipal(writer, request)
	if !ok {
		return
	}
	var input sourceRequest
	if !decodeCatalogRequest(writer, request, &input) {
		return
	}
	if handler == nil || handler.service == nil {
		writeCatalogError(writer, http.StatusNotImplemented, "CATALOG_API_UNAVAILABLE", "The projection catalog API is not configured.")
		return
	}
	source, _, err := handler.service.CreateSource(request.Context(), principal, SourceCreateInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, SourceID: input.SourceID, EventType: input.EventType, EventVersion: input.EventVersion, OwnerContact: input.OwnerContact, TenantResolution: input.TenantResolution, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		writeCatalogDomainError(writer, err)
		return
	}
	writeCatalogJSON(writer, http.StatusCreated, source)
}

func (handler *CatalogHTTPHandler) listSources(writer http.ResponseWriter, request *http.Request) {
	principal, ok := catalogPrincipal(writer, request)
	if !ok {
		return
	}
	if handler == nil || handler.service == nil {
		writeCatalogError(writer, http.StatusNotImplemented, "CATALOG_API_UNAVAILABLE", "The projection catalog API is not configured.")
		return
	}
	limit, err := catalogLimit(request.URL.Query().Get("limit"))
	if err != nil {
		writeCatalogError(writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be between 1 and 100.")
		return
	}
	items, err := handler.service.ListSources(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), limit)
	if err != nil {
		writeCatalogDomainError(writer, err)
		return
	}
	writeCatalogJSON(writer, http.StatusOK, map[string]interface{}{"items": items})
}

func (handler *CatalogHTTPHandler) publishSource(writer http.ResponseWriter, request *http.Request) {
	principal, ok := catalogPrincipal(writer, request)
	if !ok {
		return
	}
	transition, ok := decodeTransition(writer, request)
	if !ok {
		return
	}
	expected, ok := parseCatalogIfMatch(request.Header.Get("If-Match"))
	if !ok {
		writeCatalogError(writer, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "If-Match must contain the expected source revision.")
		return
	}
	if handler == nil || handler.service == nil {
		writeCatalogError(writer, http.StatusNotImplemented, "CATALOG_API_UNAVAILABLE", "The projection catalog API is not configured.")
		return
	}
	source, _, err := handler.service.PublishSource(request.Context(), principal, CatalogTransitionInput{TenantID: transition.TenantID, WorkspaceID: transition.WorkspaceID, ID: request.PathValue("sourceID"), ExpectedRevision: expected, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		writeCatalogDomainError(writer, err)
		return
	}
	writeCatalogJSON(writer, http.StatusOK, source)
}

func (handler *CatalogHTTPHandler) createMapping(writer http.ResponseWriter, request *http.Request) {
	principal, ok := catalogPrincipal(writer, request)
	if !ok {
		return
	}
	var input mappingRequest
	if !decodeCatalogRequest(writer, request, &input) {
		return
	}
	if handler == nil || handler.service == nil {
		writeCatalogError(writer, http.StatusNotImplemented, "CATALOG_API_UNAVAILABLE", "The projection catalog API is not configured.")
		return
	}
	mapping, _, err := handler.service.CreateMapping(request.Context(), principal, MappingCreateInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, MappingID: input.MappingID, SourceID: input.SourceID, EventType: input.EventType, EventVersion: input.EventVersion, TargetTableID: input.TargetTableID, TargetSchemaID: input.TargetSchemaID, TargetSchemaVersion: input.TargetSchemaVersion, FieldMap: input.FieldMap, Fixture: input.Fixture, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		writeCatalogDomainError(writer, err)
		return
	}
	writeCatalogJSON(writer, http.StatusCreated, mapping)
}

func (handler *CatalogHTTPHandler) listMappings(writer http.ResponseWriter, request *http.Request) {
	principal, ok := catalogPrincipal(writer, request)
	if !ok {
		return
	}
	if handler == nil || handler.service == nil {
		writeCatalogError(writer, http.StatusNotImplemented, "CATALOG_API_UNAVAILABLE", "The projection catalog API is not configured.")
		return
	}
	limit, err := catalogLimit(request.URL.Query().Get("limit"))
	if err != nil {
		writeCatalogError(writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be between 1 and 100.")
		return
	}
	items, err := handler.service.ListMappings(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), request.URL.Query().Get("sourceId"), limit)
	if err != nil {
		writeCatalogDomainError(writer, err)
		return
	}
	writeCatalogJSON(writer, http.StatusOK, map[string]interface{}{"items": items})
}

func (handler *CatalogHTTPHandler) publishMapping(writer http.ResponseWriter, request *http.Request) {
	handler.transitionMapping(writer, request, false)
}

func (handler *CatalogHTTPHandler) revokeMapping(writer http.ResponseWriter, request *http.Request) {
	handler.transitionMapping(writer, request, true)
}

func (handler *CatalogHTTPHandler) transitionMapping(writer http.ResponseWriter, request *http.Request, revoke bool) {
	principal, ok := catalogPrincipal(writer, request)
	if !ok {
		return
	}
	transition, ok := decodeTransition(writer, request)
	if !ok {
		return
	}
	expected, ok := parseCatalogIfMatch(request.Header.Get("If-Match"))
	if !ok {
		writeCatalogError(writer, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "If-Match must contain the expected mapping revision.")
		return
	}
	if handler == nil || handler.service == nil {
		writeCatalogError(writer, http.StatusNotImplemented, "CATALOG_API_UNAVAILABLE", "The projection catalog API is not configured.")
		return
	}
	input := CatalogTransitionInput{TenantID: transition.TenantID, WorkspaceID: transition.WorkspaceID, ID: request.PathValue("mappingID"), ExpectedRevision: expected, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")}
	var mapping MappingRegistration
	var err error
	if revoke {
		mapping, _, err = handler.service.RevokeMapping(request.Context(), principal, input)
	} else {
		mapping, _, err = handler.service.PublishMapping(request.Context(), principal, input)
	}
	if err != nil {
		writeCatalogDomainError(writer, err)
		return
	}
	writeCatalogJSON(writer, http.StatusOK, mapping)
}

func catalogPrincipal(writer http.ResponseWriter, request *http.Request) (identity.Principal, bool) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeCatalogError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return identity.Principal{}, false
	}
	return principal, true
}

func decodeTransition(writer http.ResponseWriter, request *http.Request) (transitionRequest, bool) {
	var input transitionRequest
	if !decodeCatalogRequest(writer, request, &input) {
		return transitionRequest{}, false
	}
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.WorkspaceID) == "" {
		writeCatalogError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId and workspaceId are required.")
		return transitionRequest{}, false
	}
	return input, true
}

func decodeCatalogRequest(writer http.ResponseWriter, request *http.Request, target interface{}) bool {
	if request.ContentLength > 2<<20 {
		writeCatalogError(writer, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request body exceeds the 2 MiB limit.")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeCatalogError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeCatalogError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func catalogLimit(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 50, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 1 || value > maxListLimit {
		return 0, ErrCatalogInvalid
	}
	return value, nil
}

func parseCatalogIfMatch(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	return revision, err == nil && revision > 0
}

func writeCatalogJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	if revision := catalogRevision(value); revision != "" {
		writer.Header().Set("ETag", strconv.Quote(revision))
	}
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
func catalogRevision(value interface{}) string {
	switch typed := value.(type) {
	case SourceRegistration:
		return strconv.FormatInt(typed.Revision, 10)
	case MappingRegistration:
		return strconv.FormatInt(typed.Revision, 10)
	}
	return ""
}

func writeCatalogDomainError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeCatalogError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeCatalogError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrSourceNotFound), errors.Is(err, ErrMappingNotFound), errors.Is(err, schema.ErrNotFound):
		writeCatalogError(writer, http.StatusNotFound, "CATALOG_NOT_FOUND", "The catalog registration was not found.")
	case errors.Is(err, ErrSourceExists), errors.Is(err, ErrMappingExists):
		writeCatalogError(writer, http.StatusConflict, "CATALOG_ALREADY_EXISTS", "The catalog registration already exists.")
	case errors.Is(err, ErrCatalogIdempotencyConflict):
		writeCatalogError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different input.")
	case errors.Is(err, ErrIdempotencyKeyMissing):
		writeCatalogError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required.")
	case errors.Is(err, ErrRequestIDMissing):
		writeCatalogError(writer, http.StatusBadRequest, "REQUEST_ID_REQUIRED", "X-Request-ID is required.")
	case errors.Is(err, ErrSourceRevisionConflict), errors.Is(err, ErrMappingRevisionConflict):
		writeCatalogError(writer, http.StatusConflict, "CATALOG_REVISION_CONFLICT", "The catalog revision is stale.")
	case errors.Is(err, ErrSourceImmutable), errors.Is(err, ErrMappingImmutable):
		writeCatalogError(writer, http.StatusConflict, "CATALOG_IMMUTABLE", "The catalog registration cannot be changed in its current state.")
	case errors.Is(err, ErrSourceNotPublished):
		writeCatalogError(writer, http.StatusConflict, "SOURCE_NOT_PUBLISHED", "The source registration must be published first.")
	case errors.Is(err, ErrTargetSchemaNotPublished):
		writeCatalogError(writer, http.StatusConflict, "TARGET_SCHEMA_NOT_PUBLISHED", "The target schema must be published first.")
	case errors.Is(err, ErrTargetFieldNotAllowed):
		writeCatalogError(writer, http.StatusConflict, "TARGET_FIELD_NOT_ALLOWED", "Every mapped target field must be declared by the published target schema.")
	case errors.Is(err, ErrCanonicalHashMismatch):
		writeCatalogError(writer, http.StatusConflict, "CANONICAL_HASH_MISMATCH", "The mapping canonical hash does not match its content.")
	case errors.Is(err, ErrCatalogInvalid):
		writeCatalogError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The catalog registration is invalid.")
	case errors.Is(err, ErrCatalogUnavailable):
		writeCatalogError(writer, http.StatusServiceUnavailable, "CATALOG_UNAVAILABLE", "The projection catalog is unavailable.")
	default:
		writeCatalogError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "The catalog request could not be completed.")
	}
}

func writeCatalogError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
