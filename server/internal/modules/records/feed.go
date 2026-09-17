package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

var (
	ErrFeedCursorExpired = errors.New("record feed cursor is no longer retained")
	ErrFeedCursorInvalid = errors.New("record feed cursor is invalid")
	ErrFeedSlowConsumer  = errors.New("record feed subscriber buffer is full")
	ErrFeedScopeRequired = errors.New("record feed scope is required")
)

const (
	DefaultFeedHistoryLimit = 2048
	DefaultFeedBufferSize   = 64
	DefaultFeedMaxEventSize = 64 << 10
)

// RecordChange is the deliberately small public event emitted by the read
// feed. It contains enough information for a client to invalidate or refetch
// a record, but never carries record data.
type RecordChange struct {
	ID            uint64    `json:"id"`
	TenantID      string    `json:"tenantId"`
	WorkspaceID   string    `json:"workspaceId"`
	TableID       string    `json:"tableId"`
	RecordID      string    `json:"recordId"`
	RecordVersion int64     `json:"recordVersion"`
	ChangeType    string    `json:"changeType"`
	LastEventID   string    `json:"lastEventId,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
	// DedupKey is internal and is not serialized. Projection retries can call
	// Publish twice after a lost client response; the feed must remain stable.
	DedupKey string `json:"-"`
}

func (change RecordChange) validate() error {
	if strings.TrimSpace(change.TenantID) == "" || strings.TrimSpace(change.WorkspaceID) == "" || strings.TrimSpace(change.TableID) == "" || strings.TrimSpace(change.RecordID) == "" {
		return ErrFeedScopeRequired
	}
	if change.RecordVersion < 0 || (change.ChangeType != "upsert" && change.ChangeType != "delete") {
		return ErrFeedCursorInvalid
	}
	return nil
}

type feedScope struct {
	tenantID    string
	workspaceID string
	tableID     string
}

func (scope feedScope) matches(change RecordChange) bool {
	return scope.tenantID == change.TenantID && scope.workspaceID == change.WorkspaceID && scope.tableID == change.TableID
}

type feedSubscriber struct {
	scope  feedScope
	events chan RecordChange
	done   chan error
	once   sync.Once
}

func (subscriber *feedSubscriber) close(reason error) {
	subscriber.once.Do(func() {
		if reason != nil {
			select {
			case subscriber.done <- reason:
			default:
			}
		}
		close(subscriber.done)
		close(subscriber.events)
	})
}

// RecordFeedSubscription is a bounded live subscription. Replay contains
// retained events that precede live delivery and is ordered before Events.
type RecordFeedSubscription struct {
	Replay []RecordChange
	Events <-chan RecordChange
	Done   <-chan error
	close  func()
}

func (subscription *RecordFeedSubscription) Close() {
	if subscription != nil && subscription.close != nil {
		subscription.close()
	}
}

// RecordFeed is an in-process bounded broker. Mongo/NATS remain the durable
// systems of record; this broker only provides low-latency fan-out and a
// bounded recovery window for connected clients.
type RecordFeed struct {
	mu             sync.Mutex
	history        []RecordChange
	historyLimit   int
	subscriberBuf  int
	nextID         uint64
	nextSubscriber uint64
	subscribers    map[uint64]*feedSubscriber
	dedup          map[string]uint64
	dropped        atomic.Uint64
}

func NewRecordFeed(historyLimit, subscriberBuffer int) *RecordFeed {
	if historyLimit <= 0 {
		historyLimit = DefaultFeedHistoryLimit
	}
	if subscriberBuffer <= 0 {
		subscriberBuffer = DefaultFeedBufferSize
	}
	return &RecordFeed{historyLimit: historyLimit, subscriberBuf: subscriberBuffer, subscribers: make(map[uint64]*feedSubscriber), dedup: make(map[string]uint64)}
}

func (feed *RecordFeed) Publish(change RecordChange) error {
	if feed == nil {
		return errors.New("record feed is not configured")
	}
	if err := change.validate(); err != nil {
		return err
	}
	if change.OccurredAt.IsZero() {
		change.OccurredAt = time.Now().UTC()
	} else {
		change.OccurredAt = change.OccurredAt.UTC()
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if change.DedupKey != "" {
		if _, exists := feed.dedup[change.DedupKey]; exists {
			return nil
		}
	}
	feed.nextID++
	change.ID = feed.nextID
	feed.history = append(feed.history, change)
	if len(feed.history) > feed.historyLimit {
		expired := feed.history[0]
		feed.history = feed.history[1:]
		if expired.DedupKey != "" {
			delete(feed.dedup, expired.DedupKey)
		}
	}
	if change.DedupKey != "" {
		feed.dedup[change.DedupKey] = change.ID
	}
	for key, subscriber := range feed.subscribers {
		if !subscriber.scope.matches(change) {
			continue
		}
		select {
		case subscriber.events <- change:
		default:
			delete(feed.subscribers, key)
			feed.dropped.Add(1)
			subscriber.close(ErrFeedSlowConsumer)
		}
	}
	return nil
}

func (feed *RecordFeed) DroppedCount() uint64 {
	if feed == nil {
		return 0
	}
	return feed.dropped.Load()
}

func parseFeedCursor(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, ErrFeedCursorInvalid
	}
	return value, nil
}

func (feed *RecordFeed) Subscribe(tenantID, workspaceID, tableID string, afterID uint64, buffer int) (*RecordFeedSubscription, error) {
	if feed == nil {
		return nil, errors.New("record feed is not configured")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(tableID) == "" {
		return nil, ErrFeedScopeRequired
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if afterID > 0 && len(feed.history) > 0 && afterID < feed.history[0].ID-1 {
		return nil, ErrFeedCursorExpired
	}
	replay := make([]RecordChange, 0)
	if afterID > 0 {
		for _, change := range feed.history {
			if change.ID > afterID && change.TenantID == tenantID && change.WorkspaceID == workspaceID && change.TableID == tableID {
				replay = append(replay, change)
			}
		}
	}
	if buffer <= 0 {
		buffer = feed.subscriberBuf
	}
	if buffer < len(replay)+1 {
		buffer = len(replay) + 1
	}
	if buffer > feed.historyLimit+feed.subscriberBuf {
		buffer = feed.historyLimit + feed.subscriberBuf
	}
	subscriber := &feedSubscriber{scope: feedScope{tenantID: tenantID, workspaceID: workspaceID, tableID: tableID}, events: make(chan RecordChange, buffer), done: make(chan error, 1)}
	feed.nextSubscriber++
	key := feed.nextSubscriber
	feed.subscribers[key] = subscriber
	return &RecordFeedSubscription{Replay: replay, Events: subscriber.events, Done: subscriber.done, close: func() {
		feed.mu.Lock()
		if current, ok := feed.subscribers[key]; ok && current == subscriber {
			delete(feed.subscribers, key)
		}
		feed.mu.Unlock()
		subscriber.close(nil)
	}}, nil
}

// FeedAuthorizer is intentionally small so the stream can be tested without
// Mongo while production uses identity.Authorizer.
type FeedAuthorizer interface {
	Authorize(context.Context, identity.Principal, string, string, identity.Action) (identity.WorkspaceMembership, error)
}

type FeedHTTPOptions struct {
	BufferSize             int
	MaxEventBytes          int
	AuthCheckInterval      time.Duration
	HeartbeatInterval      time.Duration
	MaxConnections         int
	MaxConnectionsPerScope int
}

func (options FeedHTTPOptions) withDefaults() FeedHTTPOptions {
	if options.BufferSize <= 0 {
		options.BufferSize = DefaultFeedBufferSize
	}
	if options.MaxEventBytes <= 0 {
		options.MaxEventBytes = DefaultFeedMaxEventSize
	}
	if options.AuthCheckInterval <= 0 {
		options.AuthCheckInterval = 5 * time.Second
	}
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = 15 * time.Second
	}
	if options.MaxConnections <= 0 {
		options.MaxConnections = 256
	}
	if options.MaxConnectionsPerScope <= 0 {
		options.MaxConnectionsPerScope = 16
	}
	return options
}

type FeedHTTPHandler struct {
	feed         *RecordFeed
	authorizer   FeedAuthorizer
	options      FeedHTTPOptions
	connections  atomic.Int64
	connectionMu sync.Mutex
	byScope      map[string]int
}

func NewFeedHTTPHandler(feed *RecordFeed, authorizer FeedAuthorizer, options FeedHTTPOptions) http.Handler {
	return &FeedHTTPHandler{feed: feed, authorizer: authorizer, options: options.withDefaults(), byScope: make(map[string]int)}
}

type feedEvent struct {
	RecordID      string    `json:"recordId"`
	RecordVersion int64     `json:"recordVersion"`
	TenantID      string    `json:"tenantId"`
	WorkspaceID   string    `json:"workspaceId"`
	TableID       string    `json:"tableId"`
	ChangeType    string    `json:"changeType"`
	LastEventID   string    `json:"lastEventId,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
}

func (handler *FeedHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET is supported for record feeds.")
		return
	}
	if handler.feed == nil || handler.authorizer == nil {
		http.Error(writer, "record feed unavailable", http.StatusServiceUnavailable)
		return
	}
	principal, ok := identity.PrincipalFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		return
	}
	tenantID := strings.TrimSpace(request.URL.Query().Get("tenantId"))
	workspaceID := strings.TrimSpace(request.URL.Query().Get("workspaceId"))
	tableID := strings.TrimSpace(request.PathValue("tableID"))
	if tenantID == "" || workspaceID == "" || tableID == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_FEED_SCOPE", "tenantId, workspaceId, and tableID are required.")
		return
	}
	if _, err := handler.authorizer.Authorize(request.Context(), principal, tenantID, workspaceID, identity.ActionRecordRead); err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) {
			writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authentication is required.")
		} else {
			writeError(writer, http.StatusForbidden, "FORBIDDEN", "The principal is not authorized for this workspace.")
		}
		return
	}
	lastEventID := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	queryCursor := strings.TrimSpace(request.URL.Query().Get("cursor"))
	if lastEventID != "" && queryCursor != "" && lastEventID != queryCursor {
		writeError(writer, http.StatusBadRequest, "CURSOR_CONFLICT", "Last-Event-ID and cursor must match when both are provided.")
		return
	}
	if queryCursor != "" {
		lastEventID = queryCursor
	}
	afterID, err := parseFeedCursor(lastEventID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_CURSOR", "The feed cursor must be a positive decimal event id.")
		return
	}
	connectionKey := principal.Issuer + "\x00" + principal.Subject + "\x00" + tenantID + "\x00" + workspaceID + "\x00" + tableID
	handler.connectionMu.Lock()
	if handler.connections.Load() >= int64(handler.options.MaxConnections) || handler.byScope[connectionKey] >= handler.options.MaxConnectionsPerScope {
		handler.connectionMu.Unlock()
		writeError(writer, http.StatusTooManyRequests, "FEED_CONNECTION_LIMIT", "The record feed connection limit has been reached.")
		return
	}
	handler.byScope[connectionKey]++
	handler.connections.Add(1)
	handler.connectionMu.Unlock()
	defer func() {
		handler.connectionMu.Lock()
		if handler.byScope[connectionKey] <= 1 {
			delete(handler.byScope, connectionKey)
		} else {
			handler.byScope[connectionKey]--
		}
		handler.connections.Add(-1)
		handler.connectionMu.Unlock()
	}()
	subscription, err := handler.feed.Subscribe(tenantID, workspaceID, tableID, afterID, handler.options.BufferSize)
	if err != nil {
		if errors.Is(err, ErrFeedCursorExpired) {
			writeError(writer, http.StatusConflict, "CURSOR_RESET_REQUIRED", "The feed cursor is older than the retained recovery window.")
			return
		}
		writeError(writer, http.StatusBadRequest, "INVALID_CURSOR", "The feed cursor is invalid.")
		return
	}
	defer subscription.Close()
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	for _, change := range subscription.Replay {
		if !writeFeedEvent(writer, flusher, change, handler.options.MaxEventBytes) {
			return
		}
	}
	authTicker := time.NewTicker(handler.options.AuthCheckInterval)
	defer authTicker.Stop()
	heartbeatTicker := time.NewTicker(handler.options.HeartbeatInterval)
	defer heartbeatTicker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-authTicker.C:
			if _, authErr := handler.authorizer.Authorize(request.Context(), principal, tenantID, workspaceID, identity.ActionRecordRead); authErr != nil {
				return
			}
		case <-heartbeatTicker.C:
			if _, err := writer.Write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case change, open := <-subscription.Events:
			if !open {
				return
			}
			if !writeFeedEvent(writer, flusher, change, handler.options.MaxEventBytes) {
				return
			}
		case <-subscription.Done:
			return
		}
	}
}

func writeFeedEvent(writer http.ResponseWriter, flusher http.Flusher, change RecordChange, maxBytes int) bool {
	payload, err := json.Marshal(feedEvent{RecordID: change.RecordID, RecordVersion: change.RecordVersion, TenantID: change.TenantID, WorkspaceID: change.WorkspaceID, TableID: change.TableID, ChangeType: change.ChangeType, LastEventID: change.LastEventID, OccurredAt: change.OccurredAt})
	if err != nil || len(payload) > maxBytes {
		return false
	}
	if _, err := fmt.Fprintf(writer, "id: %d\nevent: record.%s\ndata: %s\n\n", change.ID, change.ChangeType, payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
