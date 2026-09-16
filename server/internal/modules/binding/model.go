package binding

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	ErrInvalidSnapshot        = errors.New("invalid binding snapshot")
	ErrInvalidSnapshotRequest = errors.New("invalid binding snapshot request")
	ErrSnapshotNotFound       = errors.New("binding snapshot not found")
	ErrSnapshotExists         = errors.New("binding snapshot already exists")
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
	ErrIdempotencyConflict    = errors.New("idempotency key was used with different input")
	ErrRecordNotFound         = errors.New("binding source record not found")
	ErrRecordVersionConflict  = errors.New("binding source record version is stale")
	ErrSourceVersionConflict  = errors.New("binding source version is stale")
	ErrSchemaMismatch         = errors.New("binding schema does not match source record")
	ErrMachinePolicyDenied    = errors.New("machine binding policy denied")
	ErrBindingUnavailable     = errors.New("binding service is unavailable")
	ErrInvalidRecordReference = errors.New("invalid record reference")
)

const (
	maxIdentifierLength = 256
	maxPurposeLength    = 128
	maxRecordRefLength  = 512
)

// SnapshotRequest is the immutable input contract for a workflow binding.
// OperationID is normally populated from the Idempotency-Key header.
type SnapshotRequest struct {
	TenantID              string
	WorkspaceID           string
	RecordRef             string
	SchemaID              string
	SchemaVersion         int64
	ExpectedRecordVersion int64
	ExpectedSourceVersion int64
	Purpose               string
	OperationID           string
}

func (request SnapshotRequest) normalized() (SnapshotRequest, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.RecordRef = strings.TrimSpace(request.RecordRef)
	request.SchemaID = strings.TrimSpace(request.SchemaID)
	request.Purpose = strings.TrimSpace(request.Purpose)
	request.OperationID = strings.TrimSpace(request.OperationID)
	if request.TenantID == "" || request.WorkspaceID == "" || request.RecordRef == "" || request.SchemaID == "" || request.Purpose == "" {
		return SnapshotRequest{}, fmt.Errorf("%w: tenantId, workspaceId, recordRef, schemaId, purpose, and operationId are required", ErrInvalidSnapshotRequest)
	}
	if request.OperationID == "" {
		return SnapshotRequest{}, ErrIdempotencyKeyRequired
	}
	for _, item := range []struct {
		name  string
		value string
		max   int
	}{
		{name: "tenantId", value: request.TenantID, max: maxIdentifierLength}, {name: "workspaceId", value: request.WorkspaceID, max: maxIdentifierLength}, {name: "schemaId", value: request.SchemaID, max: maxIdentifierLength}, {name: "purpose", value: request.Purpose, max: maxPurposeLength}, {name: "operationId", value: request.OperationID, max: maxIdentifierLength},
	} {
		name, value, max := item.name, item.value, item.max
		if len(value) > max {
			return SnapshotRequest{}, fmt.Errorf("%w: %s is too long", ErrInvalidSnapshotRequest, name)
		}
		if strings.ContainsAny(value, "\t\r\n") {
			return SnapshotRequest{}, fmt.Errorf("%w: %s contains control characters", ErrInvalidSnapshotRequest, name)
		}
	}
	if len(request.Purpose) > maxPurposeLength {
		return SnapshotRequest{}, fmt.Errorf("%w: purpose is too long", ErrInvalidSnapshotRequest)
	}
	if len(request.RecordRef) > maxRecordRefLength {
		return SnapshotRequest{}, fmt.Errorf("%w: recordRef is too long", ErrInvalidSnapshotRequest)
	}
	if request.SchemaVersion < 1 || request.ExpectedRecordVersion < 1 || request.ExpectedSourceVersion < 0 {
		return SnapshotRequest{}, fmt.Errorf("%w: schemaVersion and expectedRecordVersion must be positive; expectedSourceVersion cannot be negative", ErrInvalidSnapshotRequest)
	}
	if _, err := ParseRecordRef(request.RecordRef); err != nil {
		return SnapshotRequest{}, fmt.Errorf("%w: %v", ErrInvalidSnapshotRequest, err)
	}
	return request, nil
}

// RecordRef is the stable external identity used by workflow bindings.
// It deliberately has exactly three components: system:type:id.
type RecordRef struct {
	System string
	Type   string
	ID     string
}

func (reference RecordRef) String() string {
	return strings.Join([]string{reference.System, reference.Type, reference.ID}, ":")
}

func ParseRecordRef(raw string) (RecordRef, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return RecordRef{}, ErrInvalidRecordReference
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || strings.ContainsAny(part, " \t\r\n") {
			return RecordRef{}, ErrInvalidRecordReference
		}
	}
	return RecordRef{System: parts[0], Type: parts[1], ID: parts[2]}, nil
}

// SourceRecord is the narrow boundary required from the Record Hub record
// store. Data must be one JSON object already filtered by the projection or
// record owner; binding never accepts an arbitrary database model.
type SourceRecord struct {
	RecordRef     string
	SchemaID      string
	SchemaVersion int64
	RecordVersion int64
	SourceVersion int64
	Data          json.RawMessage
}

func (record SourceRecord) Validate() error {
	if strings.TrimSpace(record.RecordRef) == "" || strings.TrimSpace(record.SchemaID) == "" || record.SchemaVersion < 1 || record.RecordVersion < 1 || record.SourceVersion < 0 || len(record.Data) == 0 {
		return fmt.Errorf("%w: source record identity, versions, and data are required", ErrRecordNotFound)
	}
	parsed, err := ParseRecordRef(record.RecordRef)
	if err != nil {
		return err
	}
	if parsed.String() != record.RecordRef {
		return ErrInvalidRecordReference
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(record.Data)))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return fmt.Errorf("%w: source record data must be an object", ErrInvalidSnapshot)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: source record data must contain one JSON object", ErrInvalidSnapshot)
	}
	return nil
}

// Snapshot is immutable after creation. RequestHash is persisted only for
// idempotency comparison and is not exposed by the HTTP response.
type Snapshot struct {
	SnapshotID    string          `bson:"_id" json:"snapshotId"`
	TenantID      string          `bson:"tenantId" json:"tenantId"`
	WorkspaceID   string          `bson:"workspaceId" json:"workspaceId"`
	OperationID   string          `bson:"operationId" json:"-"`
	RecordRef     string          `bson:"recordRef" json:"recordRef"`
	SchemaID      string          `bson:"schemaId" json:"schemaId"`
	SchemaVersion int64           `bson:"schemaVersion" json:"schemaVersion"`
	RecordVersion int64           `bson:"recordVersion" json:"recordVersion"`
	SourceVersion int64           `bson:"sourceVersion" json:"sourceVersion"`
	Purpose       string          `bson:"purpose" json:"purpose"`
	SnapshotHash  string          `bson:"snapshotHash" json:"snapshotHash"`
	Data          json.RawMessage `bson:"data" json:"data"`
	RequestHash   string          `bson:"requestHash" json:"-"`
	CreatedAt     time.Time       `bson:"createdAt" json:"createdAt"`
}

func (snapshot Snapshot) Validate() error {
	if strings.TrimSpace(snapshot.SnapshotID) == "" || strings.TrimSpace(snapshot.TenantID) == "" || strings.TrimSpace(snapshot.WorkspaceID) == "" || strings.TrimSpace(snapshot.OperationID) == "" || strings.TrimSpace(snapshot.RecordRef) == "" || strings.TrimSpace(snapshot.SchemaID) == "" || strings.TrimSpace(snapshot.Purpose) == "" || strings.TrimSpace(snapshot.SnapshotHash) == "" || strings.TrimSpace(snapshot.RequestHash) == "" || snapshot.SchemaVersion < 1 || snapshot.RecordVersion < 1 || snapshot.SourceVersion < 0 || snapshot.CreatedAt.IsZero() || len(snapshot.Data) == 0 {
		return ErrInvalidSnapshot
	}
	if _, err := ParseRecordRef(snapshot.RecordRef); err != nil {
		return err
	}
	if len(snapshot.Purpose) > maxPurposeLength || len(snapshot.OperationID) > maxIdentifierLength || len(snapshot.TenantID) > maxIdentifierLength || len(snapshot.WorkspaceID) > maxIdentifierLength || len(snapshot.SchemaID) > maxIdentifierLength {
		return ErrInvalidSnapshot
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(snapshot.Data)))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return ErrInvalidSnapshot
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidSnapshot
	}
	return nil
}
