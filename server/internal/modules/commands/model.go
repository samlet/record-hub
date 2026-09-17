package commands

import (
	"errors"
	"fmt"
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
