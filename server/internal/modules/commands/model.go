package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

var (
	ErrCommandUnavailable       = errors.New("command gateway is unavailable")
	ErrCommandPolicyDenied      = errors.New("command policy denied")
	ErrCommandPolicyNotFound    = errors.New("command policy not found")
	ErrCommandNotFound          = errors.New("command operation not found")
	ErrCommandExists            = errors.New("command operation already exists")
	ErrCommandIdempotency       = errors.New("command idempotency conflict")
	ErrCommandInvalidRequest    = errors.New("invalid command request")
	ErrCommandExpectedVersion   = errors.New("expected record version is required")
	ErrCommandPayloadTooLarge   = errors.New("command payload is too large")
	ErrCommandPublicationFailed = errors.New("command publication failed")
	ErrCommandResultConflict    = errors.New("command result conflicts with operation")
	ErrCommandResultInvalid     = errors.New("invalid command result")
)

type Status string

const (
	StatusAccepted   Status = "ACCEPTED"
	StatusDispatched Status = "DISPATCHED"
	StatusSucceeded  Status = "SUCCEEDED"
	StatusRejected   Status = "REJECTED"
	StatusFailed     Status = "FAILED"
	StatusExpired    Status = "EXPIRED"
)

const MaxPayloadBytes = 256 << 10

// Policy is an explicit allowlist entry. It deliberately contains no
// wildcard semantics and is scoped to one owner system/resource/action.
type Policy struct {
	ID                      string
	TenantID                string
	WorkspaceID             string
	Purpose                 string
	OwnerSystem             string
	ResourceType            string
	Action                  string
	ExpectedVersionRequired bool
	MaxPayloadBytes         int
}

func (policy Policy) Validate() error {
	fields := []struct{ name, value string }{
		{"id", policy.ID}, {"tenantId", policy.TenantID}, {"workspaceId", policy.WorkspaceID},
		{"purpose", policy.Purpose}, {"ownerSystem", policy.OwnerSystem}, {"resourceType", policy.ResourceType}, {"action", policy.Action},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" || strings.ContainsAny(field.value, "*> \t\r\n") {
			return fmt.Errorf("%w: %s must be non-empty and exact", ErrCommandInvalidRequest, field.name)
		}
	}
	if len(policy.ID) > 128 || len(policy.Purpose) > 128 || len(policy.OwnerSystem) > 64 || len(policy.ResourceType) > 128 || len(policy.Action) > 128 {
		return fmt.Errorf("%w: policy identifier is too long", ErrCommandInvalidRequest)
	}
	if policy.MaxPayloadBytes <= 0 {
		policy.MaxPayloadBytes = MaxPayloadBytes
	}
	if policy.MaxPayloadBytes > MaxPayloadBytes {
		return fmt.Errorf("%w: maxPayloadBytes exceeds hard limit", ErrCommandInvalidRequest)
	}
	return nil
}

type SubmitRequest struct {
	TenantID        string
	WorkspaceID     string
	PolicyID        string
	ResourceRef     string
	ExpectedVersion *int64
	Payload         []byte
	RequestID       string
	IdempotencyKey  string
}

type Operation struct {
	ID              string               `bson:"operationId" json:"operationId"`
	TenantID        string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID     string               `bson:"workspaceId" json:"workspaceId"`
	PolicyID        string               `bson:"policyId" json:"policyId"`
	OwnerSystem     string               `bson:"ownerSystem" json:"ownerSystem"`
	ResourceType    string               `bson:"resourceType" json:"resourceType"`
	Action          string               `bson:"action" json:"action"`
	Purpose         string               `bson:"purpose" json:"purpose"`
	ResourceRef     string               `bson:"resourceRef" json:"resourceRef"`
	ExpectedVersion *int64               `bson:"expectedVersion,omitempty" json:"expectedVersion,omitempty"`
	PayloadHash     string               `bson:"payloadHash" json:"payloadHash"`
	Payload         []byte               `bson:"payload" json:"-"`
	IdempotencyKey  string               `bson:"idempotencyKey" json:"-"`
	RequestID       string               `bson:"requestId,omitempty" json:"requestId,omitempty"`
	Status          Status               `bson:"status" json:"status"`
	Revision        int64                `bson:"revision" json:"revision"`
	SafeError       string               `bson:"safeError,omitempty" json:"safeError,omitempty"`
	ResultEventID   string               `bson:"resultEventId,omitempty" json:"resultEventId,omitempty"`
	ResultHash      string               `bson:"resultHash,omitempty" json:"resultHash,omitempty"`
	ResultVersion   *int64               `bson:"resultVersion,omitempty" json:"resultVersion,omitempty"`
	CreatedBy       identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	CreatedAt       time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt       time.Time            `bson:"updatedAt" json:"updatedAt"`
}

func (operation Operation) Validate() error {
	if operation.ID == "" || operation.TenantID == "" || operation.WorkspaceID == "" || operation.PolicyID == "" || operation.OwnerSystem == "" || operation.ResourceType == "" || operation.Action == "" || operation.Purpose == "" || operation.ResourceRef == "" || operation.PayloadHash == "" || operation.IdempotencyKey == "" || operation.Revision < 1 || operation.CreatedAt.IsZero() || operation.UpdatedAt.IsZero() {
		return ErrCommandInvalidRequest
	}
	switch operation.Status {
	case StatusAccepted, StatusDispatched, StatusSucceeded, StatusRejected, StatusFailed, StatusExpired:
	default:
		return ErrCommandInvalidRequest
	}
	if len(operation.SafeError) > 512 || strings.ContainsAny(operation.SafeError, "\r\n") {
		return ErrCommandInvalidRequest
	}
	return nil
}

type Envelope struct {
	OperationID     string               `json:"operationId"`
	TenantID        string               `json:"tenantId"`
	WorkspaceID     string               `json:"workspaceId"`
	PolicyID        string               `json:"policyId"`
	OwnerSystem     string               `json:"ownerSystem"`
	ResourceType    string               `json:"resourceType"`
	Action          string               `json:"action"`
	Purpose         string               `json:"purpose"`
	ResourceRef     string               `json:"resourceRef"`
	ExpectedVersion *int64               `json:"expectedVersion,omitempty"`
	PayloadHash     string               `json:"payloadHash"`
	Payload         []byte               `json:"payload"`
	RequestedBy     identity.IdentityKey `json:"requestedBy"`
	CreatedAt       time.Time            `json:"createdAt"`
}

// MarshalJSON keeps the public v1 contract faithful to the command API: the
// canonical payload is a JSON object on the wire, rather than encoding the
// internal []byte representation as base64.
func (envelope Envelope) MarshalJSON() ([]byte, error) {
	type wireEnvelope struct {
		OperationID     string               `json:"operationId"`
		TenantID        string               `json:"tenantId"`
		WorkspaceID     string               `json:"workspaceId"`
		PolicyID        string               `json:"policyId"`
		OwnerSystem     string               `json:"ownerSystem"`
		ResourceType    string               `json:"resourceType"`
		Action          string               `json:"action"`
		Purpose         string               `json:"purpose"`
		ResourceRef     string               `json:"resourceRef"`
		ExpectedVersion *int64               `json:"expectedVersion,omitempty"`
		PayloadHash     string               `json:"payloadHash"`
		Payload         json.RawMessage      `json:"payload"`
		RequestedBy     identity.IdentityKey `json:"requestedBy"`
		CreatedAt       time.Time            `json:"createdAt"`
	}
	return json.Marshal(wireEnvelope{
		OperationID: envelope.OperationID, TenantID: envelope.TenantID, WorkspaceID: envelope.WorkspaceID,
		PolicyID: envelope.PolicyID, OwnerSystem: envelope.OwnerSystem, ResourceType: envelope.ResourceType,
		Action: envelope.Action, Purpose: envelope.Purpose, ResourceRef: envelope.ResourceRef,
		ExpectedVersion: envelope.ExpectedVersion, PayloadHash: envelope.PayloadHash,
		Payload: json.RawMessage(envelope.Payload), RequestedBy: envelope.RequestedBy, CreatedAt: envelope.CreatedAt,
	})
}

// UnmarshalJSON mirrors MarshalJSON and rejects unknown/trailing fields before
// an owner can claim the command in its Inbox.
func (envelope *Envelope) UnmarshalJSON(data []byte) error {
	if envelope == nil {
		return ErrCommandInvalidRequest
	}
	type wireEnvelope struct {
		OperationID     string               `json:"operationId"`
		TenantID        string               `json:"tenantId"`
		WorkspaceID     string               `json:"workspaceId"`
		PolicyID        string               `json:"policyId"`
		OwnerSystem     string               `json:"ownerSystem"`
		ResourceType    string               `json:"resourceType"`
		Action          string               `json:"action"`
		Purpose         string               `json:"purpose"`
		ResourceRef     string               `json:"resourceRef"`
		ExpectedVersion *int64               `json:"expectedVersion,omitempty"`
		PayloadHash     string               `json:"payloadHash"`
		Payload         json.RawMessage      `json:"payload"`
		RequestedBy     identity.IdentityKey `json:"requestedBy"`
		CreatedAt       time.Time            `json:"createdAt"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire wireEnvelope
	if err := decoder.Decode(&wire); err != nil {
		return ErrCommandInvalidRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrCommandInvalidRequest
	}
	var payload map[string]json.RawMessage
	if len(wire.Payload) == 0 || json.Unmarshal(wire.Payload, &payload) != nil || payload == nil {
		return ErrCommandInvalidRequest
	}
	*envelope = Envelope{
		OperationID: wire.OperationID, TenantID: wire.TenantID, WorkspaceID: wire.WorkspaceID,
		PolicyID: wire.PolicyID, OwnerSystem: wire.OwnerSystem, ResourceType: wire.ResourceType,
		Action: wire.Action, Purpose: wire.Purpose, ResourceRef: wire.ResourceRef,
		ExpectedVersion: wire.ExpectedVersion, PayloadHash: wire.PayloadHash,
		Payload: append([]byte(nil), wire.Payload...), RequestedBy: wire.RequestedBy, CreatedAt: wire.CreatedAt,
	}
	return nil
}

// ResultEnvelope is published by the owner system after its own transaction
// and outbox have committed. It carries safe metadata only; the owner keeps
// the authoritative business result.
type ResultEnvelope struct {
	EventID       string    `json:"eventId"`
	OperationID   string    `json:"operationId"`
	TenantID      string    `json:"tenantId"`
	WorkspaceID   string    `json:"workspaceId"`
	OwnerSystem   string    `json:"ownerSystem"`
	Action        string    `json:"action"`
	Status        Status    `json:"status"`
	ResultHash    string    `json:"resultHash,omitempty"`
	ErrorCode     string    `json:"errorCode,omitempty"`
	SafeError     string    `json:"safeError,omitempty"`
	ResultVersion *int64    `json:"resultVersion,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
}

func (result ResultEnvelope) Validate() error {
	if result.EventID == "" || result.OperationID == "" || result.TenantID == "" || result.WorkspaceID == "" || result.OwnerSystem == "" || result.Action == "" || result.OccurredAt.IsZero() {
		return ErrCommandResultInvalid
	}
	switch result.Status {
	case StatusSucceeded, StatusRejected, StatusFailed, StatusExpired:
	default:
		return ErrCommandResultInvalid
	}
	if len(result.EventID) > 256 || len(result.ErrorCode) > 128 || len(result.SafeError) > 512 || len(result.ResultHash) > 256 || strings.ContainsAny(result.EventID+result.ErrorCode+result.ResultHash+result.SafeError, "\r\n") {
		return ErrCommandResultInvalid
	}
	if result.ResultVersion != nil && *result.ResultVersion < 1 {
		return ErrCommandResultInvalid
	}
	return nil
}

func (envelope Envelope) Validate() error {
	if envelope.OperationID == "" || envelope.TenantID == "" || envelope.WorkspaceID == "" || envelope.PolicyID == "" || envelope.OwnerSystem == "" || envelope.ResourceType == "" || envelope.Action == "" || envelope.Purpose == "" || envelope.ResourceRef == "" || envelope.PayloadHash == "" || envelope.CreatedAt.IsZero() {
		return ErrCommandInvalidRequest
	}
	if len(envelope.OperationID) > 256 || len(envelope.Payload) == 0 || len(envelope.Payload) > MaxPayloadBytes {
		return ErrCommandInvalidRequest
	}
	if envelope.ExpectedVersion != nil && *envelope.ExpectedVersion < 1 {
		return ErrCommandInvalidRequest
	}
	hash := sha256.Sum256(envelope.Payload)
	if envelope.PayloadHash != "sha256:"+hex.EncodeToString(hash[:]) {
		return ErrCommandInvalidRequest
	}
	return nil
}
