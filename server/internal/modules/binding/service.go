package binding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
)

// RecordReader is deliberately narrower than the records module. Adapters
// expose only a scoped stable reference and the already-authorized data needed
// to build a workflow snapshot.
type RecordReader interface {
	Read(context.Context, string, string, string) (SourceRecord, error)
}

// SnapshotStore persists immutable snapshots and owns the idempotency unique
// key. Implementations must treat Snapshot fields as immutable after Create.
type SnapshotStore interface {
	FindByOperation(context.Context, string, string, string) (Snapshot, error)
	Create(context.Context, Snapshot) error
	Get(context.Context, string, string, string) (Snapshot, error)
}

// BindingAuthorization is the exact resource boundary a machine policy must
// approve. Empty/wildcard fields are never treated as a match.
type BindingAuthorization struct {
	TenantID       string
	WorkspaceID    string
	Purpose        string
	RecordRef      string
	ResourceSystem string
	ResourceType   string
}

// PolicyAuthorizer is separate from the human membership Authorizer because
// service principals do not inherit a user's workspace role.
type PolicyAuthorizer interface {
	Authorize(context.Context, identity.Principal, BindingAuthorization) error
}

// MachinePolicy is an exact allowlist entry for one service identity and one
// workflow binding purpose. It intentionally has no wildcard semantics.
type MachinePolicy struct {
	Identity       identity.IdentityKey
	Audience       string
	Scope          string
	TenantID       string
	WorkspaceID    string
	Purpose        string
	ResourceSystem string
	ResourceType   string
}

// StaticMachinePolicyAuthorizer is suitable for local configuration and tests.
// Production may replace it with a policy backed by a secret manager or
// control-plane store without changing the binding service.
type StaticMachinePolicyAuthorizer struct {
	policies []MachinePolicy
}

func NewStaticMachinePolicyAuthorizer(policies ...MachinePolicy) *StaticMachinePolicyAuthorizer {
	return &StaticMachinePolicyAuthorizer{policies: append([]MachinePolicy(nil), policies...)}
}

func (authorizer *StaticMachinePolicyAuthorizer) Authorize(_ context.Context, principal identity.Principal, request BindingAuthorization) error {
	if authorizer == nil || principal.Kind != identity.PrincipalService || principal.Issuer == "" || principal.Subject == "" {
		return ErrMachinePolicyDenied
	}
	for _, policy := range authorizer.policies {
		if policy.Identity != principal.IdentityKey() || policy.TenantID != request.TenantID || policy.WorkspaceID != request.WorkspaceID || policy.Purpose != request.Purpose || policy.ResourceSystem != request.ResourceSystem || policy.ResourceType != request.ResourceType {
			continue
		}
		if policy.Audience == "" || !contains(principal.Audience, policy.Audience) || policy.Scope == "" || !contains(principal.Scopes, policy.Scope) {
			continue
		}
		return nil
	}
	return ErrMachinePolicyDenied
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type Service struct {
	records     RecordReader
	snapshots   SnapshotStore
	users       *identity.Authorizer
	machines    PolicyAuthorizer
	clock       func() time.Time
	idGenerator func() (string, error)
}

func NewService(records RecordReader, snapshots SnapshotStore, users *identity.Authorizer, machines PolicyAuthorizer) *Service {
	return &Service{records: records, snapshots: snapshots, users: users, machines: machines, clock: time.Now, idGenerator: newUUID}
}

func (service *Service) WithClock(clock func() time.Time) *Service {
	if service != nil && clock != nil {
		service.clock = clock
	}
	return service
}

func (service *Service) WithIDGenerator(generator func() (string, error)) *Service {
	if service != nil && generator != nil {
		service.idGenerator = generator
	}
	return service
}

func (service *Service) CreateSnapshot(ctx context.Context, principal identity.Principal, request SnapshotRequest) (Snapshot, bool, error) {
	normalized, err := request.normalized()
	if err != nil {
		return Snapshot{}, false, err
	}
	if service == nil || service.records == nil || service.snapshots == nil || service.clock == nil || service.idGenerator == nil {
		return Snapshot{}, false, ErrBindingUnavailable
	}
	if err := requirePrincipal(principal); err != nil {
		return Snapshot{}, false, err
	}
	reference, _ := ParseRecordRef(normalized.RecordRef)
	if principal.Kind == identity.PrincipalUser {
		if service.users == nil {
			return Snapshot{}, false, identity.ErrForbidden
		}
		if _, err := service.users.Authorize(ctx, principal, normalized.TenantID, normalized.WorkspaceID, identity.ActionRecordRead); err != nil {
			return Snapshot{}, false, err
		}
	} else {
		if service.machines == nil {
			return Snapshot{}, false, ErrMachinePolicyDenied
		}
		if err := service.machines.Authorize(ctx, principal, BindingAuthorization{TenantID: normalized.TenantID, WorkspaceID: normalized.WorkspaceID, Purpose: normalized.Purpose, RecordRef: normalized.RecordRef, ResourceSystem: reference.System, ResourceType: reference.Type}); err != nil {
			return Snapshot{}, false, err
		}
	}
	requestHash, err := snapshotRequestHash(normalized)
	if err != nil {
		return Snapshot{}, false, err
	}
	if existing, findErr := service.snapshots.FindByOperation(ctx, normalized.TenantID, normalized.WorkspaceID, normalized.OperationID); findErr == nil {
		if existing.RequestHash != requestHash {
			return Snapshot{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(findErr, ErrSnapshotNotFound) {
		return Snapshot{}, false, findErr
	}

	record, err := service.records.Read(ctx, normalized.TenantID, normalized.WorkspaceID, normalized.RecordRef)
	if err != nil {
		return Snapshot{}, false, err
	}
	if err := record.Validate(); err != nil {
		return Snapshot{}, false, err
	}
	if record.RecordRef != normalized.RecordRef {
		return Snapshot{}, false, ErrRecordNotFound
	}
	if record.SchemaID != normalized.SchemaID || record.SchemaVersion != normalized.SchemaVersion {
		return Snapshot{}, false, ErrSchemaMismatch
	}
	if record.RecordVersion != normalized.ExpectedRecordVersion {
		return Snapshot{}, false, ErrRecordVersionConflict
	}
	if normalized.ExpectedSourceVersion > 0 && record.SourceVersion != normalized.ExpectedSourceVersion {
		return Snapshot{}, false, ErrSourceVersionConflict
	}

	canonicalData, err := schema.CanonicalizeJSON(record.Data)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("canonicalize snapshot data: %w", err)
	}
	snapshotHash, err := computeSnapshotHash(normalized.SchemaID, normalized.SchemaVersion, record.RecordVersion, record.SourceVersion, canonicalData)
	if err != nil {
		return Snapshot{}, false, err
	}
	snapshotID, err := service.idGenerator()
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("generate snapshot ID: %w", err)
	}
	now := service.clock().UTC()
	snapshot := Snapshot{SnapshotID: snapshotID, TenantID: normalized.TenantID, WorkspaceID: normalized.WorkspaceID, OperationID: normalized.OperationID, RecordRef: normalized.RecordRef, SchemaID: normalized.SchemaID, SchemaVersion: normalized.SchemaVersion, RecordVersion: record.RecordVersion, SourceVersion: record.SourceVersion, Purpose: normalized.Purpose, SnapshotHash: snapshotHash, Data: append([]byte(nil), canonicalData...), RequestHash: requestHash, CreatedAt: now}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, false, err
	}
	if err := service.snapshots.Create(ctx, snapshot); err != nil {
		if errors.Is(err, ErrSnapshotExists) {
			existing, findErr := service.snapshots.FindByOperation(ctx, normalized.TenantID, normalized.WorkspaceID, normalized.OperationID)
			if findErr != nil {
				return Snapshot{}, false, findErr
			}
			if existing.RequestHash != requestHash {
				return Snapshot{}, false, ErrIdempotencyConflict
			}
			return existing, true, nil
		}
		return Snapshot{}, false, err
	}
	return snapshot, false, nil
}

func (service *Service) GetSnapshot(ctx context.Context, principal identity.Principal, tenantID, workspaceID, snapshotID string) (Snapshot, error) {
	tenantID, workspaceID, snapshotID = strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), strings.TrimSpace(snapshotID)
	if tenantID == "" || workspaceID == "" || snapshotID == "" {
		return Snapshot{}, ErrInvalidSnapshotRequest
	}
	if service == nil || service.snapshots == nil {
		return Snapshot{}, ErrBindingUnavailable
	}
	if err := requirePrincipal(principal); err != nil {
		return Snapshot{}, err
	}
	if principal.Kind == identity.PrincipalUser {
		if service.users == nil {
			return Snapshot{}, identity.ErrForbidden
		}
		if _, err := service.users.Authorize(ctx, principal, tenantID, workspaceID, identity.ActionRecordRead); err != nil {
			return Snapshot{}, err
		}
	}
	snapshot, err := service.snapshots.Get(ctx, tenantID, workspaceID, snapshotID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	if principal.Kind == identity.PrincipalService {
		reference, parseErr := ParseRecordRef(snapshot.RecordRef)
		if parseErr != nil {
			return Snapshot{}, ErrInvalidSnapshot
		}
		if service.machines == nil {
			return Snapshot{}, ErrMachinePolicyDenied
		}
		if err := service.machines.Authorize(ctx, principal, BindingAuthorization{TenantID: snapshot.TenantID, WorkspaceID: snapshot.WorkspaceID, Purpose: snapshot.Purpose, RecordRef: snapshot.RecordRef, ResourceSystem: reference.System, ResourceType: reference.Type}); err != nil {
			return Snapshot{}, err
		}
	}
	return snapshot, nil
}

func requirePrincipal(principal identity.Principal) error {
	if (principal.Kind != identity.PrincipalUser && principal.Kind != identity.PrincipalService) || principal.Issuer == "" || principal.Subject == "" {
		return identity.ErrUnauthenticated
	}
	return nil
}

func snapshotRequestHash(request SnapshotRequest) (string, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"tenantId": request.TenantID, "workspaceId": request.WorkspaceID, "recordRef": request.RecordRef, "schemaId": request.SchemaID, "schemaVersion": request.SchemaVersion, "expectedRecordVersion": request.ExpectedRecordVersion, "expectedSourceVersion": request.ExpectedSourceVersion, "purpose": request.Purpose,
	})
	if err != nil {
		return "", fmt.Errorf("encode snapshot request: %w", err)
	}
	canonical, err := schema.CanonicalizeJSON(payload)
	if err != nil {
		return "", fmt.Errorf("canonicalize snapshot request: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func computeSnapshotHash(schemaID string, schemaVersion, recordVersion, sourceVersion int64, canonicalData []byte) (string, error) {
	var data interface{}
	decoder := json.NewDecoder(strings.NewReader(string(canonicalData)))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return "", fmt.Errorf("decode canonical snapshot data: %w", err)
	}
	content, err := json.Marshal(map[string]interface{}{"data": data, "recordVersion": recordVersion, "schemaId": schemaID, "schemaVersion": schemaVersion, "sourceVersion": sourceVersion})
	if err != nil {
		return "", fmt.Errorf("encode snapshot hash content: %w", err)
	}
	canonical, err := schema.CanonicalizeJSON(content)
	if err != nil {
		return "", fmt.Errorf("canonicalize snapshot hash content: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func newUUID() (string, error) {
	var bytes [16]byte
	if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}
