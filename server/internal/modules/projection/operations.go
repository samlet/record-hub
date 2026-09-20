package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/observability"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	DefaultOperationsLimit          = 50
	MaxOperationsLimit              = 100
	DefaultProjectionBacklogWarning = 100
	DefaultProjectionLagWarning     = 30 * time.Second
	DefaultProjectionFailureBudget  = 10
	ApproverProjectionConsumer      = "record-hub-approver-projection-v1"
	FluxionProjectionConsumer       = "record-hub-fluxion-projection-v1"
	BidsProjectionConsumer          = "record-hub-bids-projection-v1"
)

var supportedProjectionConsumers = map[string]struct{}{
	ApproverProjectionConsumer: {},
	FluxionProjectionConsumer:  {},
	BidsProjectionConsumer:     {},
	// Explicit migration aliases for pre-M8 local fixtures. No wildcard or
	// arbitrary consumer selector is accepted by the operations page.
	"record-hub-approver-v1": {},
	"record-hub-fluxion-v1":  {},
	"record-hub-bids-v1":     {},
}

var (
	ErrOperationsQueryInvalid = errors.New("invalid projection operations query")
	ErrOperationsUnavailable  = errors.New("projection operations are unavailable")
)

// OperationsQuery is deliberately scoped to one consumer and workspace. The
// Inbox claims may omit scope for backwards compatibility with pre-M4-047
// consumers. Scoped claims are counted only for this tenant/workspace; legacy
// unscoped claims are intentionally excluded instead of becoming a cross-
// tenant side channel. Checkpoints are always filtered by both scope fields.
type OperationsQuery struct {
	TenantID    string
	WorkspaceID string
	Consumer    string
	Limit       int
}

func (query OperationsQuery) normalized() (OperationsQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.WorkspaceID = strings.TrimSpace(query.WorkspaceID)
	query.Consumer = strings.TrimSpace(query.Consumer)
	if query.TenantID == "" || query.WorkspaceID == "" || query.Consumer == "" || len(query.TenantID) > 128 || len(query.WorkspaceID) > 128 || len(query.Consumer) > 256 {
		return OperationsQuery{}, fmt.Errorf("%w: tenantId, workspaceId, and consumer are required and bounded", ErrOperationsQueryInvalid)
	}
	if strings.ContainsAny(query.TenantID+query.WorkspaceID+query.Consumer, " \t\r\n") {
		return OperationsQuery{}, fmt.Errorf("%w: scope identifiers cannot contain whitespace", ErrOperationsQueryInvalid)
	}
	if _, ok := supportedProjectionConsumers[query.Consumer]; !ok {
		return OperationsQuery{}, fmt.Errorf("%w: consumer is not a registered projection", ErrOperationsQueryInvalid)
	}
	if query.Limit == 0 {
		query.Limit = DefaultOperationsLimit
	}
	if query.Limit < 1 || query.Limit > MaxOperationsLimit {
		return OperationsQuery{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrOperationsQueryInvalid, MaxOperationsLimit)
	}
	return query, nil
}

type InboxOperationsSummary struct {
	Processing         int64      `json:"processing"`
	Applied            int64      `json:"applied"`
	Rejected           int64      `json:"rejected"`
	Failed             int64      `json:"failed"`
	OldestProcessingAt *time.Time `json:"oldestProcessingAt,omitempty"`
}

type ProjectionSLOThresholds struct {
	BacklogWarning int64
	LagWarning     time.Duration
	FailureBudget  int64
}

func DefaultProjectionSLOThresholds() ProjectionSLOThresholds {
	return ProjectionSLOThresholds{BacklogWarning: DefaultProjectionBacklogWarning, LagWarning: DefaultProjectionLagWarning, FailureBudget: DefaultProjectionFailureBudget}
}

func (thresholds ProjectionSLOThresholds) Validate() error {
	if thresholds.BacklogWarning < 1 || thresholds.BacklogWarning > 1_000_000 || thresholds.LagWarning <= 0 || thresholds.LagWarning > 24*time.Hour || thresholds.FailureBudget < 1 || thresholds.FailureBudget > 1_000_000 {
		return ErrOperationsQueryInvalid
	}
	return nil
}

type ProjectionFreshness struct {
	LastProjectedAt      *time.Time `json:"lastProjectedAt,omitempty"`
	LastEventID          string     `json:"lastEventId,omitempty"`
	LastProjectedVersion int64      `json:"lastProjectedVersion"`
	LagAgeSeconds        float64    `json:"lagAgeSeconds"`
	Backlog              int64      `json:"backlog"`
	Failures             int64      `json:"failures"`
	ErrorBudgetRemaining int64      `json:"errorBudgetRemaining"`
	FindingCount         int64      `json:"findingCount"`
	RecoveryAgeSeconds   float64    `json:"recoveryAgeSeconds"`
	Stale                bool       `json:"stale"`
	SLOBreached          bool       `json:"sloBreached"`
}

type OperationsSnapshot struct {
	TenantID    string                 `json:"tenantId"`
	WorkspaceID string                 `json:"workspaceId"`
	Consumer    string                 `json:"consumer"`
	Inbox       InboxOperationsSummary `json:"inbox"`
	Checkpoints []ProjectionCheckpoint `json:"checkpoints"`
	Freshness   ProjectionFreshness    `json:"freshness"`
	GeneratedAt time.Time              `json:"generatedAt"`
}

func (snapshot OperationsSnapshot) Validate() error {
	if strings.TrimSpace(snapshot.TenantID) == "" || strings.TrimSpace(snapshot.WorkspaceID) == "" || strings.TrimSpace(snapshot.Consumer) == "" || snapshot.GeneratedAt.IsZero() {
		return ErrOperationsQueryInvalid
	}
	if snapshot.Inbox.Processing < 0 || snapshot.Inbox.Applied < 0 || snapshot.Inbox.Rejected < 0 || snapshot.Inbox.Failed < 0 {
		return ErrOperationsQueryInvalid
	}
	if snapshot.Freshness.LastProjectedVersion < 0 || snapshot.Freshness.LagAgeSeconds < 0 || snapshot.Freshness.Backlog < 0 || snapshot.Freshness.Failures < 0 || snapshot.Freshness.ErrorBudgetRemaining < 0 || snapshot.Freshness.FindingCount < 0 || snapshot.Freshness.RecoveryAgeSeconds < 0 {
		return ErrOperationsQueryInvalid
	}
	if len(snapshot.Checkpoints) > MaxOperationsLimit {
		return ErrOperationsQueryInvalid
	}
	for _, checkpoint := range snapshot.Checkpoints {
		if err := checkpoint.Validate(); err != nil {
			return fmt.Errorf("checkpoint: %w", err)
		}
	}
	return nil
}

// OperationsReader is the read-only persistence boundary for the operations
// page. Implementations must return summaries only; raw inbox payloads are
// intentionally absent from this interface and response model.
type OperationsReader interface {
	Snapshot(context.Context, OperationsQuery) (OperationsSnapshot, error)
}

// OperationsService authorizes a workspace-scoped operations read before
// delegating to persistence.
type OperationsService struct {
	reader     OperationsReader
	authorizer *identity.Authorizer
	clock      func() time.Time
	metrics    *observability.Registry
	thresholds ProjectionSLOThresholds
}

// WithMetrics records bounded backlog gauges for the selected consumer.
func (service *OperationsService) WithMetrics(registry *observability.Registry) *OperationsService {
	if service != nil {
		service.metrics = registry
	}
	return service
}

func NewOperationsService(reader OperationsReader, authorizer *identity.Authorizer) *OperationsService {
	return &OperationsService{reader: reader, authorizer: authorizer, clock: time.Now, thresholds: DefaultProjectionSLOThresholds()}
}

func (service *OperationsService) WithSLOThresholds(thresholds ProjectionSLOThresholds) *OperationsService {
	if service != nil {
		if thresholds.Validate() == nil {
			service.thresholds = thresholds
		}
	}
	return service
}

func (service *OperationsService) Snapshot(ctx context.Context, principal identity.Principal, query OperationsQuery) (OperationsSnapshot, error) {
	started := time.Now()
	query, err := query.normalized()
	if err != nil {
		return OperationsSnapshot{}, err
	}
	if service == nil || service.reader == nil || service.authorizer == nil || service.clock == nil {
		return OperationsSnapshot{}, ErrOperationsUnavailable
	}
	if _, err := service.authorizer.Authorize(ctx, principal, query.TenantID, query.WorkspaceID, identity.ActionOperationsRead); err != nil {
		return OperationsSnapshot{}, err
	}
	snapshot, err := service.reader.Snapshot(ctx, query)
	if err != nil {
		return OperationsSnapshot{}, err
	}
	if snapshot.TenantID == "" {
		snapshot.TenantID = query.TenantID
	}
	if snapshot.WorkspaceID == "" {
		snapshot.WorkspaceID = query.WorkspaceID
	}
	if snapshot.Consumer == "" {
		snapshot.Consumer = query.Consumer
	}
	if snapshot.TenantID != query.TenantID || snapshot.WorkspaceID != query.WorkspaceID || snapshot.Consumer != query.Consumer {
		return OperationsSnapshot{}, fmt.Errorf("%w: reader returned a different scope", ErrOperationsQueryInvalid)
	}
	for _, checkpoint := range snapshot.Checkpoints {
		if checkpoint.TenantID != query.TenantID || checkpoint.WorkspaceID != query.WorkspaceID || checkpoint.Consumer != query.Consumer {
			return OperationsSnapshot{}, fmt.Errorf("%w: checkpoint scope differs from query", ErrOperationsQueryInvalid)
		}
	}
	if snapshot.GeneratedAt.IsZero() {
		snapshot.GeneratedAt = service.clock().UTC()
	}
	service.populateFreshness(&snapshot)
	if err := snapshot.Validate(); err != nil {
		return OperationsSnapshot{}, fmt.Errorf("operations snapshot: %w", err)
	}
	if service.metrics != nil {
		labels := observability.Labels{"consumer": query.Consumer}
		service.metrics.SetGauge("record_hub_projection_backlog", float64(snapshot.Inbox.Processing), labels)
		service.metrics.SetGauge("record_hub_projection_failures", float64(snapshot.Inbox.Failed+snapshot.Inbox.Rejected), labels)
		service.metrics.SetGauge("record_hub_projection_last_projected_version", float64(snapshot.Freshness.LastProjectedVersion), labels)
		if snapshot.Freshness.LastProjectedAt != nil {
			service.metrics.SetGauge("record_hub_projection_last_projected_timestamp_seconds", float64(snapshot.Freshness.LastProjectedAt.Unix()), labels)
		}
		service.metrics.SetGauge("record_hub_projection_lag_age_seconds", snapshot.Freshness.LagAgeSeconds, labels)
		service.metrics.SetGauge("record_hub_projection_error_budget_remaining", float64(snapshot.Freshness.ErrorBudgetRemaining), labels)
		service.metrics.SetGauge("record_hub_projection_finding_count", float64(snapshot.Freshness.FindingCount), labels)
		service.metrics.SetGauge("record_hub_projection_recovery_age_seconds", snapshot.Freshness.RecoveryAgeSeconds, labels)
		stale := 0.0
		if snapshot.Freshness.Stale {
			stale = 1
		}
		service.metrics.SetGauge("record_hub_projection_snapshot_stale", stale, labels)
		breach := 0.0
		if snapshot.Freshness.SLOBreached {
			breach = 1
		}
		service.metrics.SetGauge("record_hub_projection_slo_breach", breach, labels)
		service.metrics.SetGauge("record_hub_projection_backlog_warning", float64(service.thresholds.BacklogWarning), labels)
		service.metrics.SetGauge("record_hub_projection_lag_warning_seconds", service.thresholds.LagWarning.Seconds(), labels)
		service.metrics.ObserveDuration("record_hub_projection_snapshot_duration_seconds", time.Since(started), labels)
	}
	return snapshot, nil
}

func (service *OperationsService) populateFreshness(snapshot *OperationsSnapshot) {
	if service == nil || snapshot == nil {
		return
	}
	latest := snapshot.Freshness.LastProjectedAt
	findingCount := snapshot.Inbox.Rejected + snapshot.Inbox.Failed
	for _, checkpoint := range snapshot.Checkpoints {
		if checkpoint.Status == CheckpointGap || checkpoint.Status == CheckpointFailed {
			findingCount++
		}
		if latest == nil || checkpoint.SyncedAt.After(*latest) {
			value := checkpoint.SyncedAt.UTC()
			latest = &value
			snapshot.Freshness.LastEventID = checkpoint.LastEventID
		}
		if checkpoint.SourceVersion > snapshot.Freshness.LastProjectedVersion {
			snapshot.Freshness.LastProjectedVersion = checkpoint.SourceVersion
		}
	}
	snapshot.Freshness.LastProjectedAt = latest
	snapshot.Freshness.Backlog = snapshot.Inbox.Processing
	snapshot.Freshness.Failures = snapshot.Inbox.Failed + snapshot.Inbox.Rejected
	snapshot.Freshness.FindingCount = findingCount
	snapshot.Freshness.ErrorBudgetRemaining = service.thresholds.FailureBudget - snapshot.Freshness.Failures
	if snapshot.Freshness.ErrorBudgetRemaining < 0 {
		snapshot.Freshness.ErrorBudgetRemaining = 0
	}
	if latest != nil {
		age := snapshot.GeneratedAt.Sub(*latest).Seconds()
		if age > 0 {
			snapshot.Freshness.LagAgeSeconds = age
		}
	}
	if snapshot.Freshness.FindingCount > 0 {
		snapshot.Freshness.RecoveryAgeSeconds = snapshot.Freshness.LagAgeSeconds
	}
	if age := time.Since(snapshot.GeneratedAt).Seconds(); age > 60 {
		snapshot.Freshness.Stale = true
	}
	snapshot.Freshness.SLOBreached = snapshot.Freshness.Backlog >= service.thresholds.BacklogWarning || snapshot.Freshness.LagAgeSeconds >= service.thresholds.LagWarning.Seconds() || snapshot.Freshness.Failures >= service.thresholds.FailureBudget
}

// Snapshot reads bounded operator metadata from MongoDB. It never selects or
// decodes an inbox payload and only returns checkpoints in the requested
// tenant/workspace scope.
func (repository *MongoProjectionRepository) Snapshot(ctx context.Context, query OperationsQuery) (OperationsSnapshot, error) {
	query, err := query.normalized()
	if err != nil {
		return OperationsSnapshot{}, err
	}
	if repository == nil || repository.inbox == nil || repository.checkpoints == nil {
		return OperationsSnapshot{}, ErrOperationsUnavailable
	}
	counts := InboxOperationsSummary{}
	inboxFilter := bson.D{{Key: "consumer", Value: query.Consumer}, {Key: "tenantId", Value: query.TenantID}, {Key: "workspaceId", Value: query.WorkspaceID}}
	for status, target := range map[InboxStatus]*int64{
		InboxProcessing: &counts.Processing,
		InboxApplied:    &counts.Applied,
		InboxRejected:   &counts.Rejected,
		InboxFailed:     &counts.Failed,
	} {
		countFilter := append(append(bson.D(nil), inboxFilter...), bson.E{Key: "status", Value: status})
		count, countErr := repository.inbox.CountDocuments(ctx, countFilter)
		if countErr != nil {
			return OperationsSnapshot{}, fmt.Errorf("count inbox %s: %w", status, countErr)
		}
		*target = count
	}
	var oldest struct {
		ReceivedAt time.Time `bson:"receivedAt"`
	}
	oldestFilter := append(append(bson.D(nil), inboxFilter...), bson.E{Key: "status", Value: InboxProcessing})
	oldestErr := repository.inbox.FindOne(ctx, oldestFilter, options.FindOne().SetProjection(bson.D{{Key: "receivedAt", Value: 1}}).SetSort(bson.D{{Key: "receivedAt", Value: 1}})).Decode(&oldest)
	if oldestErr == nil {
		value := oldest.ReceivedAt.UTC()
		counts.OldestProcessingAt = &value
	} else if !errors.Is(oldestErr, mongo.ErrNoDocuments) {
		return OperationsSnapshot{}, fmt.Errorf("find oldest processing inbox event: %w", oldestErr)
	}

	filter := bson.D{{Key: "tenantId", Value: query.TenantID}, {Key: "workspaceId", Value: query.WorkspaceID}, {Key: "consumer", Value: query.Consumer}}
	cursor, err := repository.checkpoints.Find(ctx, filter, options.Find().SetProjection(bson.D{
		{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "consumer", Value: 1},
		{Key: "sourceSystem", Value: 1}, {Key: "aggregateType", Value: 1}, {Key: "aggregateId", Value: 1},
		{Key: "sourceVersion", Value: 1}, {Key: "lastEventId", Value: 1}, {Key: "syncedAt", Value: 1}, {Key: "status", Value: 1},
	}).SetSort(bson.D{{Key: "status", Value: 1}, {Key: "syncedAt", Value: -1}, {Key: "aggregateId", Value: 1}}).SetLimit(int64(query.Limit)))
	if err != nil {
		return OperationsSnapshot{}, fmt.Errorf("list projection checkpoints: %w", err)
	}
	defer cursor.Close(ctx)
	checkpoints := make([]ProjectionCheckpoint, 0, query.Limit)
	if err := cursor.All(ctx, &checkpoints); err != nil {
		return OperationsSnapshot{}, fmt.Errorf("decode projection checkpoints: %w", err)
	}
	snapshot := OperationsSnapshot{TenantID: query.TenantID, WorkspaceID: query.WorkspaceID, Consumer: query.Consumer, Inbox: counts, Checkpoints: checkpoints, GeneratedAt: time.Now().UTC()}
	if err := snapshot.Validate(); err != nil {
		return OperationsSnapshot{}, err
	}
	return snapshot, nil
}

// OperationsHTTPService keeps the HTTP layer independent of MongoDB while
// preserving principal-aware authorization in the service boundary.
type OperationsHTTPService interface {
	Snapshot(context.Context, identity.Principal, OperationsQuery) (OperationsSnapshot, error)
}

type OperationsHTTPHandler struct {
	service OperationsHTTPService
}

func NewOperationsHTTPHandler(service OperationsHTTPService) http.Handler {
	handler := &OperationsHTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/operations/events", handler.events)
	mux.HandleFunc("GET /operations/events", handler.page)
	return mux
}

func (handler *OperationsHTTPHandler) events(writer http.ResponseWriter, request *http.Request) {
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeOperationsError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	query, err := operationsQueryFromRequest(request)
	if err != nil {
		writeOperationsError(writer, http.StatusBadRequest, "INVALID_REQUEST", "tenantId, workspaceId, consumer, and a bounded limit are required.")
		return
	}
	if handler == nil || handler.service == nil {
		writeOperationsError(writer, http.StatusNotImplemented, "OPERATIONS_API_UNAVAILABLE", "The operations API is not configured.")
		return
	}
	snapshot, err := handler.service.Snapshot(request.Context(), principal, query)
	if err != nil {
		writeOperationsServiceError(writer, err)
		return
	}
	writeOperationsJSON(writer, http.StatusOK, snapshot)
}

func (handler *OperationsHTTPHandler) page(writer http.ResponseWriter, request *http.Request) {
	if _, ok := identity.PrincipalFromContext(request.Context()); !ok {
		writeOperationsError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(operationsPageHTML))
}

func operationsQueryFromRequest(request *http.Request) (OperationsQuery, error) {
	limit := 0
	if value := strings.TrimSpace(request.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return OperationsQuery{}, ErrOperationsQueryInvalid
		}
		limit = parsed
	}
	return (OperationsQuery{TenantID: request.URL.Query().Get("tenantId"), WorkspaceID: request.URL.Query().Get("workspaceId"), Consumer: request.URL.Query().Get("consumer"), Limit: limit}).normalized()
}

func writeOperationsServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeOperationsError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
	case errors.Is(err, identity.ErrForbidden):
		writeOperationsError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
	case errors.Is(err, ErrOperationsQueryInvalid):
		writeOperationsError(writer, http.StatusBadRequest, "INVALID_REQUEST", "The operations query is invalid.")
	case errors.Is(err, ErrOperationsUnavailable):
		writeOperationsError(writer, http.StatusNotImplemented, "OPERATIONS_API_UNAVAILABLE", "The operations API is not configured.")
	default:
		writeOperationsError(writer, http.StatusInternalServerError, "OPERATIONS_QUERY_FAILED", "The operations snapshot could not be loaded.")
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

const operationsPageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Record Hub Operations</title>
<style>body{font:15px system-ui,sans-serif;max-width:980px;margin:2rem auto;padding:0 1rem}label{display:inline-flex;flex-direction:column;margin-right:.75rem}input{padding:.35rem}button{padding:.4rem .8rem}pre{background:#f5f5f5;padding:1rem;overflow:auto}.status-GAP,.status-FAILED{color:#b00020;font-weight:600}</style></head>
<body><h1>Projection operations</h1><p>This page shows bounded counts and checkpoint metadata only. Event payloads and secrets are never displayed.</p>
<form id="query"><label>Tenant <input name="tenantId" required maxlength="128"></label><label>Workspace <input name="workspaceId" required maxlength="128"></label><label>Projection <select name="consumer" required><option value="record-hub-approver-projection-v1">Approver</option><option value="record-hub-fluxion-projection-v1">Fluxion</option><option value="record-hub-bids-projection-v1">Bids</option></select></label><label>Limit <input name="limit" type="number" min="1" max="100" value="50"></label><button>Refresh</button></form>
<pre id="result" aria-live="polite">Enter a scope and refresh.</pre>
<script>const form=document.querySelector('#query'),out=document.querySelector('#result');form.addEventListener('submit',async e=>{e.preventDefault();const q=new URLSearchParams(new FormData(form));const r=await fetch('/api/v1/operations/events?'+q,{headers:{accept:'application/json'}});const body=await r.json();out.textContent=JSON.stringify(body,null,2)});</script></body></html>`
