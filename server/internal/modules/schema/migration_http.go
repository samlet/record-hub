package schema

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type MigrationHTTPService interface {
	Create(context.Context, identity.Principal, MigrationPlanInput) (MigrationPlan, bool, error)
	Get(context.Context, identity.Principal, string, string, string, string) (MigrationPlan, error)
	Cancel(context.Context, identity.Principal, string, string, string, string, int64, string, string) (MigrationPlan, bool, error)
}

type migrationPlanRequest struct {
	TenantID           string            `json:"tenantId"`
	WorkspaceID        string            `json:"workspaceId"`
	FromVersion        int64             `json:"fromVersion"`
	ToVersion          int64             `json:"toVersion"`
	Mode               MigrationPlanMode `json:"mode"`
	FailureSampleLimit int64             `json:"failureSampleLimit"`
}

type migrationCancelRequest struct {
	TenantID    string `json:"tenantId"`
	WorkspaceID string `json:"workspaceId"`
}

func (handler *HTTPHandler) createMigrationPlan(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	if handler.migrations == nil {
		writeAPIError(writer, http.StatusNotImplemented, "MIGRATION_PLAN_UNAVAILABLE", "The migration plan API is not configured.")
		return
	}
	var input migrationPlanRequest
	if !decodeMutationRequest(writer, request, &input) {
		return
	}
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.WorkspaceID) == "" || input.FromVersion < 1 || input.ToVersion <= input.FromVersion || input.FailureSampleLimit < 1 || input.FailureSampleLimit > 100 {
		writeMigrationError(writer, ErrMigrationPlanInvalid)
		return
	}
	plan, _, err := handler.migrations.Create(request.Context(), principal, MigrationPlanInput{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, SchemaID: request.PathValue("schemaID"), FromVersion: input.FromVersion, ToVersion: input.ToVersion, Mode: input.Mode, FailureSampleLimit: input.FailureSampleLimit, RequestID: request.Header.Get("X-Request-ID"), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		writeMigrationError(writer, err)
		return
	}
	writeMigrationPlan(writer, http.StatusCreated, plan)
}

func (handler *HTTPHandler) getMigrationPlan(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	if handler.migrations == nil {
		writeAPIError(writer, http.StatusNotImplemented, "MIGRATION_PLAN_UNAVAILABLE", "The migration plan API is not configured.")
		return
	}
	plan, err := handler.migrations.Get(request.Context(), principal, request.URL.Query().Get("tenantId"), request.URL.Query().Get("workspaceId"), request.PathValue("schemaID"), request.PathValue("planID"))
	if err != nil {
		writeMigrationError(writer, err)
		return
	}
	writeMigrationPlan(writer, http.StatusOK, plan)
}

func (handler *HTTPHandler) cancelMigrationPlan(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	if handler.migrations == nil {
		writeAPIError(writer, http.StatusNotImplemented, "MIGRATION_PLAN_UNAVAILABLE", "The migration plan API is not configured.")
		return
	}
	expectedVersion, ok := parseIfMatch(request.Header.Get("If-Match"))
	if !ok {
		writeAPIError(writer, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "If-Match must contain the expected migration plan version.")
		return
	}
	var input migrationCancelRequest
	if !decodeMutationRequest(writer, request, &input) {
		return
	}
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.WorkspaceID) == "" {
		writeMigrationError(writer, ErrMigrationPlanInvalid)
		return
	}
	plan, _, err := handler.migrations.Cancel(request.Context(), principal, input.TenantID, input.WorkspaceID, request.PathValue("schemaID"), request.PathValue("planID"), expectedVersion, request.Header.Get("X-Request-ID"), request.Header.Get("Idempotency-Key"))
	if err != nil {
		writeMigrationError(writer, err)
		return
	}
	writeMigrationPlan(writer, http.StatusOK, plan)
}

func writeMigrationPlan(writer http.ResponseWriter, status int, plan MigrationPlan) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(plan.Version, 10)))
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(plan)
}

func writeMigrationError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeAPIError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeAPIError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrIdempotencyKeyRequired):
		writeAPIError(writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required.")
	case errors.Is(err, ErrIdempotencyConflict):
		writeAPIError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with different input.")
	case errors.Is(err, ErrReceiptNotFound):
		writeAPIError(writer, http.StatusNotFound, "MIGRATION_PLAN_RECEIPT_NOT_FOUND", "The migration plan receipt was not found.")
	case errors.Is(err, ErrMigrationPlanNotFound), errors.Is(err, ErrNotFound):
		writeAPIError(writer, http.StatusNotFound, "MIGRATION_PLAN_NOT_FOUND", "The migration plan or schema version was not found.")
	case errors.Is(err, ErrMigrationPlanExists):
		writeAPIError(writer, http.StatusConflict, "MIGRATION_PLAN_EXISTS", "An equivalent migration plan already exists.")
	case errors.Is(err, ErrMigrationPlanConflict):
		writeAPIError(writer, http.StatusConflict, "MIGRATION_PLAN_VERSION_CONFLICT", "The migration plan version is stale.")
	case errors.Is(err, ErrMigrationPlanTerminal):
		writeAPIError(writer, http.StatusConflict, "MIGRATION_PLAN_TERMINAL", "The migration plan is already terminal.")
	case errors.Is(err, ErrMigrationPlanInvalid), errors.Is(err, ErrInvalidSchema):
		writeAPIError(writer, http.StatusBadRequest, "INVALID_MIGRATION_PLAN", "The migration plan or referenced schema is invalid.")
	default:
		writeAPIError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The migration plan request is invalid.")
	}
}
