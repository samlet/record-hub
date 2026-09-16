package records

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type MutationService interface {
	CreateWorkspace(context.Context, identity.Principal, WorkspaceInput) (Workspace, error)
	ListWorkspaces(context.Context, identity.Principal, string) ([]Workspace, error)
	CreateTable(context.Context, identity.Principal, TableInput) (TableDefinition, error)
	ListTables(context.Context, identity.Principal, string, string) ([]TableDefinition, error)
	GetTable(context.Context, identity.Principal, string, string, string) (TableDefinition, error)
}

type RecordHTTPService interface {
	CreateRecord(context.Context, identity.Principal, RecordInput) (Record, error)
	GetRecord(context.Context, identity.Principal, string, string, string) (Record, error)
	UpdateRecord(context.Context, identity.Principal, RecordInput, int64) (Record, error)
	DeleteRecord(context.Context, identity.Principal, RecordDeleteInput, int64) (Record, error)
}

type HTTPService interface {
	MutationService
	RecordHTTPService
}

type HTTPHandler struct {
	service MutationService
}

func NewHTTPHandler(service MutationService) http.Handler {
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/workspaces", handler.createWorkspace)
	mux.HandleFunc("GET /api/v1/workspaces", handler.listWorkspaces)
	mux.HandleFunc("POST /api/v1/workspaces/{workspaceID}/tables", handler.createTable)
	mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/tables", handler.listTables)
	mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/tables/{tableID}", handler.getTable)
	mux.HandleFunc("POST /api/v1/tables/{tableID}/records", handler.createRecord)
	mux.HandleFunc("GET /api/v1/records/{recordID}", handler.getRecord)
	mux.HandleFunc("PATCH /api/v1/records/{recordID}", handler.updateRecord)
	mux.HandleFunc("DELETE /api/v1/records/{recordID}", handler.deleteRecord)
	return mux
}

type workspaceRequest struct {
	TenantID string `json:"tenantId"`
	ID       string `json:"id"`
	Name     string `json:"name"`
}

type tableRequest struct {
	TenantID      string        `json:"tenantId"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Kind          TableKind     `json:"kind"`
	SchemaID      string        `json:"schemaId"`
	SchemaVersion int64         `json:"schemaVersion"`
	SourcePolicy  *SourcePolicy `json:"sourcePolicy,omitempty"`
}

type recordRequest struct {
	TenantID    string           `json:"tenantId"`
	WorkspaceID string           `json:"workspaceId"`
	ID          string           `json:"id"`
	TableID     string           `json:"tableId"`
	Data        json.RawMessage  `json:"data"`
	Tags        []string         `json:"tags"`
	Relations   []RecordRelation `json:"relations"`
}

func (handler *HTTPHandler) createWorkspace(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	var input workspaceRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	workspace, err := handler.service.CreateWorkspace(request.Context(), principal, WorkspaceInput{TenantID: input.TenantID, ID: input.ID, Name: input.Name})
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, workspace)
}

func (handler *HTTPHandler) listWorkspaces(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	workspaces, err := handler.service.ListWorkspaces(request.Context(), principal, request.URL.Query().Get("tenantId"))
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]interface{}{"items": workspaces})
}

func (handler *HTTPHandler) createTable(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	var input tableRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	table, err := handler.service.CreateTable(request.Context(), principal, TableInput{TenantID: input.TenantID, WorkspaceID: request.PathValue("workspaceID"), ID: input.ID, Name: input.Name, Kind: input.Kind, SchemaID: input.SchemaID, SchemaVersion: input.SchemaVersion, SourcePolicy: input.SourcePolicy})
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, table)
}

func (handler *HTTPHandler) listTables(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	tenantID := request.URL.Query().Get("tenantId")
	tables, err := handler.service.ListTables(request.Context(), principal, tenantID, request.PathValue("workspaceID"))
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]interface{}{"items": tables})
}

func (handler *HTTPHandler) getTable(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	table, err := handler.service.GetTable(request.Context(), principal, request.URL.Query().Get("tenantId"), request.PathValue("workspaceID"), request.PathValue("tableID"))
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, table)
}

func (handler *HTTPHandler) createRecord(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	var input recordRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	recordInput, err := input.recordInput(request, request.PathValue("tableID"), "")
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	service, ok := handler.recordService(writer)
	if !ok {
		return
	}
	record, err := service.CreateRecord(request.Context(), principal, recordInput)
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeRecord(writer, http.StatusCreated, record)
}

func (handler *HTTPHandler) getRecord(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	tenantID, workspaceID := request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId")
	if tenantID == "" || workspaceID == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId and workspaceId are required.")
		return
	}
	service, ok := handler.recordService(writer)
	if !ok {
		return
	}
	record, err := service.GetRecord(request.Context(), principal, tenantID, workspaceID, request.PathValue("recordID"))
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeRecord(writer, http.StatusOK, record)
}

func (handler *HTTPHandler) updateRecord(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	expectedVersion, ok := parseRecordIfMatch(request.Header.Get("If-Match"))
	if !ok {
		writeError(writer, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "If-Match must contain the expected record version.")
		return
	}
	var input recordRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	recordInput, err := input.recordInput(request, input.TableID, request.PathValue("recordID"))
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	service, ok := handler.recordService(writer)
	if !ok {
		return
	}
	record, err := service.UpdateRecord(request.Context(), principal, recordInput, expectedVersion)
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeRecord(writer, http.StatusOK, record)
}

func (handler *HTTPHandler) deleteRecord(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	expectedVersion, ok := parseRecordIfMatch(request.Header.Get("If-Match"))
	if !ok {
		writeError(writer, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "If-Match must contain the expected record version.")
		return
	}
	query := request.URL.Query()
	input := RecordDeleteInput{TenantID: query.Get("tenantId"), WorkspaceID: query.Get("workspaceId"), TableID: query.Get("tableId"), RecordID: request.PathValue("recordID"), RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")}
	if input.TenantID == "" || input.WorkspaceID == "" || input.TableID == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId, workspaceId, and tableId are required.")
		return
	}
	service, ok := handler.recordService(writer)
	if !ok {
		return
	}
	record, err := service.DeleteRecord(request.Context(), principal, input, expectedVersion)
	if err != nil {
		writeRecordsError(writer, err)
		return
	}
	writeRecord(writer, http.StatusOK, record)
}

func (input recordRequest) recordInput(request *http.Request, tableID, recordID string) (RecordInput, error) {
	if recordID == "" {
		recordID = input.ID
	}
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(tableID) == "" || len(input.Data) == 0 {
		return RecordInput{}, errors.New("tenantId, workspaceId, tableId, and data are required")
	}
	if strings.TrimSpace(recordID) == "" {
		return RecordInput{}, errors.New("record id is required")
	}
	var data bson.Raw
	if err := bson.UnmarshalExtJSON(input.Data, false, &data); err != nil {
		return RecordInput{}, errors.New("data must be a JSON object")
	}
	return RecordInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, TableID: tableID, ID: recordID, Data: data, Tags: input.Tags, Relations: input.Relations, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")}, nil
}

func (handler *HTTPHandler) recordService(writer http.ResponseWriter) (RecordHTTPService, bool) {
	service, ok := handler.service.(RecordHTTPService)
	if !ok || service == nil {
		writeError(writer, http.StatusNotImplemented, "RECORD_API_UNAVAILABLE", "The record API is not configured.")
		return nil, false
	}
	return service, true
}

func parseRecordIfMatch(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	version, err := strconv.ParseInt(value, 10, 64)
	return version, err == nil && version > 0
}

func writeRecord(writer http.ResponseWriter, status int, record Record) {
	data, err := bson.MarshalExtJSON(record.Data, false, false)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "RESPONSE_ENCODING_FAILED", "Response could not be encoded.")
		return
	}
	response := recordResponse{ID: record.ID, TenantID: record.TenantID, WorkspaceID: record.WorkspaceID, TableID: record.TableID, SchemaID: record.SchemaID, SchemaVersion: record.SchemaVersion, Source: record.Source, RecordVersion: record.RecordVersion, Tags: record.Tags, Data: json.RawMessage(data), Relations: record.Relations, Projection: record.Projection, CreatedBy: record.CreatedBy, UpdatedBy: record.UpdatedBy, CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: record.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(record.RecordVersion, 10)))
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}

type recordResponse struct {
	ID            string               `json:"id"`
	TenantID      string               `json:"tenantId"`
	WorkspaceID   string               `json:"workspaceId"`
	TableID       string               `json:"tableId"`
	SchemaID      string               `json:"schemaId"`
	SchemaVersion int64                `json:"schemaVersion"`
	Source        *RecordSource        `json:"source,omitempty"`
	RecordVersion int64                `json:"recordVersion"`
	Tags          []string             `json:"tags"`
	Data          json.RawMessage      `json:"data"`
	Relations     []RecordRelation     `json:"relations"`
	Projection    *ProjectionState     `json:"projection,omitempty"`
	CreatedBy     identity.IdentityKey `json:"createdBy"`
	UpdatedBy     identity.IdentityKey `json:"updatedBy"`
	CreatedAt     string               `json:"createdAt"`
	UpdatedAt     string               `json:"updatedAt"`
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target interface{}) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body is invalid.")
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func writeRecordsError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrWorkspaceExists):
		writeError(writer, http.StatusConflict, "WORKSPACE_ALREADY_EXISTS", "The workspace already exists.")
	case errors.Is(err, ErrTableExists):
		writeError(writer, http.StatusConflict, "TABLE_ALREADY_EXISTS", "The table already exists.")
	case errors.Is(err, ErrWorkspaceNotFound):
		writeError(writer, http.StatusNotFound, "WORKSPACE_NOT_FOUND", "The workspace was not found.")
	case errors.Is(err, ErrTableNotFound):
		writeError(writer, http.StatusNotFound, "TABLE_NOT_FOUND", "The table was not found.")
	case errors.Is(err, ErrSchemaUnavailable):
		writeError(writer, http.StatusConflict, "SCHEMA_UNAVAILABLE", "The table must reference a published schema.")
	case errors.Is(err, ErrProjectionReadOnly):
		writeError(writer, http.StatusConflict, "PROJECTION_READ_ONLY", "Projection records cannot be changed through the generic record API.")
	case errors.Is(err, ErrRecordNotFound):
		writeError(writer, http.StatusNotFound, "RECORD_NOT_FOUND", "The record was not found.")
	case errors.Is(err, ErrRecordVersionConflict):
		writeError(writer, http.StatusConflict, "RECORD_VERSION_CONFLICT", "The record version is stale.")
	case errors.Is(err, ErrIdempotencyKeyRequired):
		writeError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required.")
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different input.")
	case errors.Is(err, schema.ErrInvalidDocument):
		writeError(writer, http.StatusBadRequest, "RECORD_SCHEMA_INVALID", "Record data does not match the published schema.")
	default:
		message := "The request is invalid."
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "must") || strings.Contains(err.Error(), "cannot") {
			message = err.Error()
		}
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", message)
	}
}

func writeJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
