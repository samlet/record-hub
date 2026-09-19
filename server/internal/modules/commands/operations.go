package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrCommandOperationsQueryInvalid = errors.New("invalid command operations query")
	ErrCommandOperationsUnavailable  = errors.New("command operations are unavailable")
)

type OperationsQuery struct {
	TenantID    string
	WorkspaceID string
}

func (query OperationsQuery) normalized() (OperationsQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.WorkspaceID = strings.TrimSpace(query.WorkspaceID)
	if query.TenantID == "" || query.WorkspaceID == "" || len(query.TenantID) > 128 || len(query.WorkspaceID) > 128 || strings.ContainsAny(query.TenantID+query.WorkspaceID, " \t\r\n") {
		return OperationsQuery{}, ErrCommandOperationsQueryInvalid
	}
	return query, nil
}

type OperationsSummary struct {
	TenantID        string     `json:"tenantId"`
	WorkspaceID     string     `json:"workspaceId"`
	Accepted        int64      `json:"accepted"`
	Dispatched      int64      `json:"dispatched"`
	Succeeded       int64      `json:"succeeded"`
	Rejected        int64      `json:"rejected"`
	Failed          int64      `json:"failed"`
	Expired         int64      `json:"expired"`
	OldestPendingAt *time.Time `json:"oldestPendingAt,omitempty"`
	GeneratedAt     time.Time  `json:"generatedAt"`
}

func (summary OperationsSummary) Validate() error {
	if summary.TenantID == "" || summary.WorkspaceID == "" || summary.GeneratedAt.IsZero() {
		return ErrCommandOperationsQueryInvalid
	}
	for _, value := range []int64{summary.Accepted, summary.Dispatched, summary.Succeeded, summary.Rejected, summary.Failed, summary.Expired} {
		if value < 0 {
			return ErrCommandOperationsQueryInvalid
		}
	}
	return nil
}

type OperationsReader interface {
	Snapshot(context.Context, OperationsQuery) (OperationsSummary, error)
}

type OperationsService struct {
	reader     OperationsReader
	authorizer *identity.Authorizer
	clock      func() time.Time
}

func NewOperationsService(reader OperationsReader, authorizer *identity.Authorizer) *OperationsService {
	return &OperationsService{reader: reader, authorizer: authorizer, clock: time.Now}
}

func (service *OperationsService) Snapshot(ctx context.Context, principal identity.Principal, query OperationsQuery) (OperationsSummary, error) {
	query, err := query.normalized()
	if err != nil {
		return OperationsSummary{}, err
	}
	if service == nil || service.reader == nil || service.authorizer == nil || service.clock == nil {
		return OperationsSummary{}, ErrCommandOperationsUnavailable
	}
	if _, err := service.authorizer.Authorize(ctx, principal, query.TenantID, query.WorkspaceID, identity.ActionWorkspaceRead); err != nil {
		return OperationsSummary{}, err
	}
	summary, err := service.reader.Snapshot(ctx, query)
	if err != nil {
		return OperationsSummary{}, err
	}
	if summary.TenantID == "" {
		summary.TenantID = query.TenantID
	}
	if summary.WorkspaceID == "" {
		summary.WorkspaceID = query.WorkspaceID
	}
	if summary.GeneratedAt.IsZero() {
		summary.GeneratedAt = service.clock().UTC()
	}
	if summary.TenantID != query.TenantID || summary.WorkspaceID != query.WorkspaceID {
		return OperationsSummary{}, fmt.Errorf("%w: reader returned a different scope", ErrCommandOperationsQueryInvalid)
	}
	if err := summary.Validate(); err != nil {
		return OperationsSummary{}, err
	}
	return summary, nil
}

// Snapshot is a safe receipt summary. It intentionally never selects payload,
// idempotency keys, requester identity, or operation IDs.
func (store *MongoStore) Snapshot(ctx context.Context, query OperationsQuery) (OperationsSummary, error) {
	query, err := query.normalized()
	if err != nil {
		return OperationsSummary{}, err
	}
	if store == nil || store.collection == nil {
		return OperationsSummary{}, ErrCommandOperationsUnavailable
	}
	filter := bson.D{{Key: "tenantId", Value: query.TenantID}, {Key: "workspaceId", Value: query.WorkspaceID}}
	summary := OperationsSummary{TenantID: query.TenantID, WorkspaceID: query.WorkspaceID, GeneratedAt: time.Now().UTC()}
	for status, target := range map[Status]*int64{
		StatusAccepted: &summary.Accepted, StatusDispatched: &summary.Dispatched, StatusSucceeded: &summary.Succeeded,
		StatusRejected: &summary.Rejected, StatusFailed: &summary.Failed, StatusExpired: &summary.Expired,
	} {
		statusFilter := append(append(bson.D(nil), filter...), bson.E{Key: "status", Value: status})
		count, countErr := store.collection.CountDocuments(ctx, statusFilter)
		if countErr != nil {
			return OperationsSummary{}, fmt.Errorf("count command receipts %s: %w", status, countErr)
		}
		*target = count
	}
	var oldest struct {
		UpdatedAt time.Time `bson:"updatedAt"`
	}
	oldestFilter := append(append(bson.D(nil), filter...), bson.E{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{StatusAccepted, StatusDispatched}}}})
	if err := store.collection.FindOne(ctx, oldestFilter, options.FindOne().SetProjection(bson.D{{Key: "updatedAt", Value: 1}}).SetSort(bson.D{{Key: "updatedAt", Value: 1}})).Decode(&oldest); err == nil {
		value := oldest.UpdatedAt.UTC()
		summary.OldestPendingAt = &value
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return OperationsSummary{}, fmt.Errorf("find oldest pending command receipt: %w", err)
	}
	return summary, nil
}

type OperationsHTTPService interface {
	Snapshot(context.Context, identity.Principal, OperationsQuery) (OperationsSummary, error)
}

type OperationsHTTPHandler struct{ service OperationsHTTPService }

func NewOperationsHTTPHandler(service OperationsHTTPService) http.Handler {
	handler := &OperationsHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/operations/commands", handler.summary)
	return mux
}

func (handler *OperationsHTTPHandler) summary(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeOperationsError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	query, err := (OperationsQuery{TenantID: request.URL.Query().Get("tenantId"), WorkspaceID: request.URL.Query().Get("workspaceId")}).normalized()
	if err != nil {
		writeOperationsError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId and workspaceId are required.")
		return
	}
	if handler == nil || handler.service == nil {
		writeOperationsError(writer, http.StatusNotImplemented, "OPERATIONS_API_UNAVAILABLE", "The command operations API is not configured.")
		return
	}
	summary, err := handler.service.Snapshot(request.Context(), principal, query)
	if err != nil {
		writeOperationsServiceError(writer, err)
		return
	}
	writeOperationsJSON(writer, http.StatusOK, summary)
}

func writeOperationsServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeOperationsError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeOperationsError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrCommandOperationsQueryInvalid):
		writeOperationsError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The command operations query is invalid.")
	case errors.Is(err, ErrCommandOperationsUnavailable):
		writeOperationsError(writer, http.StatusNotImplemented, "OPERATIONS_API_UNAVAILABLE", "The command operations API is not configured.")
	default:
		writeOperationsError(writer, http.StatusInternalServerError, "OPERATIONS_QUERY_FAILED", "The command receipt summary could not be loaded.")
	}
}

func writeOperationsError(writer http.ResponseWriter, status int, code, message string) {
	writeOperationsJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeOperationsJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
