package records

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
	CreateWorkspace(context.Context, identity.Principal, WorkspaceInput) (Workspace, error)
	ListWorkspaces(context.Context, identity.Principal, string) ([]Workspace, error)
	CreateTable(context.Context, identity.Principal, TableInput) (TableDefinition, error)
	ListTables(context.Context, identity.Principal, string, string) ([]TableDefinition, error)
	GetTable(context.Context, identity.Principal, string, string, string) (TableDefinition, error)
}

type HTTPHandler struct {
	service HTTPService
}

func NewHTTPHandler(service HTTPService) http.Handler {
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/workspaces", handler.createWorkspace)
	mux.HandleFunc("GET /api/v1/workspaces", handler.listWorkspaces)
	mux.HandleFunc("POST /api/v1/workspaces/{workspaceID}/tables", handler.createTable)
	mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/tables", handler.listTables)
	mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/tables/{tableID}", handler.getTable)
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
