package audit

import (
	"context"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

// Entry records the security-relevant fact without copying a dynamic payload.
type Entry struct {
	TenantID        string               `bson:"tenantId"`
	WorkspaceID     string               `bson:"workspaceId"`
	Action          string               `bson:"action"`
	Actor           identity.IdentityKey `bson:"actor"`
	ResourceType    string               `bson:"resourceType"`
	ResourceID      string               `bson:"resourceId"`
	ResourceVersion int64                `bson:"resourceVersion"`
	RequestID       string               `bson:"requestId,omitempty"`
	IdempotencyKey  string               `bson:"idempotencyKey,omitempty"`
	BeforeHash      string               `bson:"beforeHash,omitempty"`
	AfterHash       string               `bson:"afterHash,omitempty"`
	ViewID          string               `bson:"viewId,omitempty"`
	FieldSet        []string             `bson:"fieldSet,omitempty"`
	RedactedFields  []string             `bson:"redactedFields,omitempty"`
	CreatedAt       time.Time            `bson:"createdAt"`
}

type Writer interface {
	Append(context.Context, Entry) error
}
