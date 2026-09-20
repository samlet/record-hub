package recordhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 2 << 20

type SnapshotRequest struct {
	TenantID              string `json:"tenantId"`
	WorkspaceID           string `json:"workspaceId"`
	RecordRef             string `json:"recordRef"`
	SchemaID              string `json:"schemaId"`
	SchemaVersion         int64  `json:"schemaVersion"`
	ExpectedRecordVersion int64  `json:"expectedRecordVersion"`
	ExpectedSourceVersion int64  `json:"expectedSourceVersion,omitempty"`
	Purpose               string `json:"purpose"`
}

type Snapshot struct {
	SnapshotID    string          `json:"snapshotId"`
	TenantID      string          `json:"tenantId"`
	WorkspaceID   string          `json:"workspaceId"`
	RecordRef     string          `json:"recordRef"`
	SchemaID      string          `json:"schemaId"`
	SchemaVersion int64           `json:"schemaVersion"`
	RecordVersion int64           `json:"recordVersion"`
	SourceVersion int64           `json:"sourceVersion"`
	Purpose       string          `json:"purpose"`
	SnapshotHash  string          `json:"snapshotHash"`
	Data          json.RawMessage `json:"data"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// RecordRef is the stable owner/type/id reference used by command and
// snapshot contracts. SnapshotRequest keeps its string field for backwards
// compatibility; new callers may use RecordRef(value) at the boundary.
type RecordRef string

// Receipt is the idempotent acknowledgement shared by command clients.
// It intentionally contains metadata only and never a business payload.
type Receipt struct {
	OperationID string    `json:"operationId"`
	Status      string    `json:"status"`
	Replayed    bool      `json:"replayed"`
	OccurredAt  time.Time `json:"occurredAt"`
}

// CommandResult is the framework-neutral command/result envelope. Owners
// remain responsible for applying side effects; Record Hub only carries the
// bounded result and receipt metadata.
type CommandResult struct {
	OperationID string          `json:"operationId"`
	Accepted    bool            `json:"accepted"`
	Receipt     Receipt         `json:"receipt"`
	Error       *APIError       `json:"error,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

type APIError struct {
	StatusCode int
	Code       string `json:"code"`
	Message    string `json:"message"`
}

// ErrorCode is the portable error-code marker used by generated and hand-
// written clients. Unknown values must remain forward-compatible strings.
type ErrorCode string

const (
	ErrorCodeInvalidArgument ErrorCode = "INVALID_ARGUMENT"
	ErrorCodeUnauthorized    ErrorCode = "UNAUTHORIZED"
	ErrorCodeForbidden       ErrorCode = "FORBIDDEN"
	ErrorCodeNotFound        ErrorCode = "NOT_FOUND"
	ErrorCodeConflict        ErrorCode = "CONFLICT"
	ErrorCodeRateLimited     ErrorCode = "RATE_LIMITED"
)

func (err *APIError) Error() string {
	if err == nil {
		return "record hub API error"
	}
	if err.Code == "" {
		return fmt.Sprintf("record hub API error: HTTP %d", err.StatusCode)
	}
	return fmt.Sprintf("record hub API error: HTTP %d %s: %s", err.StatusCode, err.Code, err.Message)
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

type Client struct {
	baseURL     *url.URL
	httpClient  *http.Client
	bearerToken string
}

func NewClient(rawBaseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("Record Hub base URL must be an absolute HTTP(S) URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: parsed, httpClient: httpClient}, nil
}

func (client *Client) WithBearerToken(token string) *Client {
	if client != nil {
		client.bearerToken = strings.TrimSpace(token)
	}
	return client
}

func (client *Client) CreateSnapshot(ctx context.Context, request SnapshotRequest, operationID string) (Snapshot, bool, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Snapshot{}, false, err
	}
	status, body, err := client.do(ctx, http.MethodPost, "/api/v1/bindings/snapshots", operationID, payload)
	if err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return Snapshot{}, false, fmt.Errorf("decode Record Hub snapshot: %w", err)
	}
	return snapshot, status == http.StatusOK, nil
}

func (client *Client) GetSnapshot(ctx context.Context, tenantID, workspaceID, snapshotID string) (Snapshot, error) {
	path := "/api/v1/bindings/snapshots/" + url.PathEscape(snapshotID) + "?tenantId=" + url.QueryEscape(tenantID) + "&workspaceId=" + url.QueryEscape(workspaceID)
	_, body, err := client.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode Record Hub snapshot: %w", err)
	}
	return snapshot, nil
}

func (client *Client) do(ctx context.Context, method, path, operationID string, body []byte) (int, []byte, error) {
	if client == nil || client.baseURL == nil || client.httpClient == nil {
		return 0, nil, errors.New("Record Hub client is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	relative, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(relative.Path, "/") {
		return 0, nil, errors.New("Record Hub request path is invalid")
	}
	requestURL := *client.baseURL
	requestURL.Path = strings.TrimRight(client.baseURL.Path, "/") + relative.Path
	requestURL.RawQuery = relative.RawQuery
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), strings.NewReader(string(body)))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if operationID != "" {
		request.Header.Set("Idempotency-Key", operationID)
	}
	if client.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+client.bearerToken)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return response.StatusCode, nil, err
	}
	if len(data) > maxResponseBytes {
		return response.StatusCode, nil, errors.New("Record Hub response exceeds the 2 MiB limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var envelope errorEnvelope
		if json.Unmarshal(data, &envelope) == nil {
			envelope.Error.StatusCode = response.StatusCode
			return response.StatusCode, nil, &envelope.Error
		}
		return response.StatusCode, nil, &APIError{StatusCode: response.StatusCode, Message: "Record Hub returned an invalid error response"}
	}
	return response.StatusCode, data, nil
}
