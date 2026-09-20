package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// WorkloadPrincipalState is deliberately smaller than a token lifecycle. The
// service stores hashes and scope metadata only; it never stores credentials.
type WorkloadPrincipalState string

const (
	WorkloadPrincipalActive    WorkloadPrincipalState = "ACTIVE"
	WorkloadPrincipalCandidate WorkloadPrincipalState = "CANDIDATE"
	WorkloadPrincipalDraining  WorkloadPrincipalState = "DRAINING"
	WorkloadPrincipalRevoked   WorkloadPrincipalState = "REVOKED"
)

const (
	RotationCreateCandidate = "CREATE_CANDIDATE"
	RotationDualAccept      = "DUAL_ACCEPT"
	RotationDrainOld        = "DRAIN_OLD"
	RotationRevokeOld       = "REVOKE_OLD"
	RotationEmergencyRevoke = "EMERGENCY_REVOKE"
	InFlightPolicy          = "DRAIN_BEFORE_REVOKE"
)

var (
	ErrRotationNotFound     = errors.New("workload principal rotation not found")
	ErrRotationConflict     = errors.New("workload principal rotation revision conflict")
	ErrRotationInvalidScope = errors.New("workload principal scope must be exact and non-wildcard")
	ErrRotationInvalidInput = errors.New("invalid workload principal rotation input")
	ErrRotationInvalidState = errors.New("invalid workload principal rotation state")
	ErrRotationInFlight     = errors.New("old workload principal still has in-flight work")
	ErrRotationAudit        = errors.New("workload principal rotation audit failed")
	ErrRotationUnknownOwner = errors.New("unknown workload principal owner")
)

var workloadOwners = map[string]struct{}{
	"approver":   {},
	"fluxion":    {},
	"bids":       {},
	"settlement": {},
}

// WorkloadScope is an exact resource boundary. Wildcards and omitted values
// are rejected before a principal enters the rotation state machine.
type WorkloadScope struct {
	TenantID     string `json:"tenantId"`
	WorkspaceID  string `json:"workspaceId"`
	OwnerSystem  string `json:"ownerSystem"`
	ResourceType string `json:"resourceType"`
}

// RotationAuditEntry is metadata-only evidence. Principal secrets and token
// material are intentionally not representable in this type.
type RotationAuditEntry struct {
	EventID          string    `json:"eventId"`
	Owner            string    `json:"owner"`
	OldPrincipalHash string    `json:"oldPrincipalHash"`
	NewPrincipalHash string    `json:"newPrincipalHash"`
	State            string    `json:"state"`
	Actor            string    `json:"actor"`
	Reason           string    `json:"reason"`
	OccurredAt       time.Time `json:"occurredAt"`
}

// RotationAuditWriter is intentionally narrower than the domain audit module
// so identity can remain independent of persistence and avoid import cycles.
type RotationAuditWriter interface {
	AppendRotation(context.Context, RotationAuditEntry) error
}

// PrincipalRotation is an owner-scoped rotation with separate old/new state.
// InFlight counts work accepted by the old principal while it drains.
type PrincipalRotation struct {
	Owner            string                 `json:"owner"`
	Scope            WorkloadScope          `json:"scope"`
	Audience         string                 `json:"audience"`
	OldPrincipalHash string                 `json:"oldPrincipalHash"`
	NewPrincipalHash string                 `json:"newPrincipalHash"`
	OldState         WorkloadPrincipalState `json:"oldState"`
	NewState         WorkloadPrincipalState `json:"newState"`
	DualAccepted     bool                   `json:"dualAccepted"`
	InFlight         int64                  `json:"inFlight"`
	Revision         int64                  `json:"revision"`
	UpdatedAt        time.Time              `json:"updatedAt"`
}

type CreateCandidateRequest struct {
	Owner            string
	Scope            WorkloadScope
	Audience         string
	OldPrincipalHash string
	NewPrincipalHash string
	Actor            string
	Reason           string
}

// HashPrincipal creates the only form accepted by this lifecycle service.
// Callers pass issuer+subject, never a bearer token or private credential.
func HashPrincipal(issuer, subject string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(issuer) + "\x00" + strings.TrimSpace(subject)))
	return "sha256:" + hex.EncodeToString(digest[:])
}

// MemoryRotationAudit stores immutable entries for tests and local preflight.
// Production adapters may persist the same metadata to the audit boundary.
type MemoryRotationAudit struct {
	mu      sync.Mutex
	entries []RotationAuditEntry
	err     error
}

func (writer *MemoryRotationAudit) AppendRotation(_ context.Context, entry RotationAuditEntry) error {
	if writer == nil {
		return ErrRotationAudit
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.err != nil {
		return writer.err
	}
	writer.entries = append(writer.entries, entry)
	return nil
}

func (writer *MemoryRotationAudit) Entries() []RotationAuditEntry {
	if writer == nil {
		return nil
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]RotationAuditEntry(nil), writer.entries...)
}

func (writer *MemoryRotationAudit) SetError(err error) {
	if writer == nil {
		return
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.err = err
}

// PrincipalRotationService implements candidate -> dual accept -> drain ->
// revoke with fail-closed scope and audit boundaries.
type PrincipalRotationService struct {
	mu     sync.Mutex
	values map[string]PrincipalRotation
	audit  RotationAuditWriter
	now    func() time.Time
}

func NewPrincipalRotationService(audit RotationAuditWriter) *PrincipalRotationService {
	return &PrincipalRotationService{values: make(map[string]PrincipalRotation), audit: audit, now: func() time.Time { return time.Now().UTC() }}
}

func (service *PrincipalRotationService) CreateCandidate(ctx context.Context, request CreateCandidateRequest) (PrincipalRotation, error) {
	if service == nil {
		return PrincipalRotation{}, ErrRotationInvalidInput
	}
	if err := validateCreateCandidate(request); err != nil {
		return PrincipalRotation{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if _, exists := service.values[request.Owner]; exists {
		return PrincipalRotation{}, ErrRotationConflict
	}
	rotation := PrincipalRotation{Owner: request.Owner, Scope: request.Scope, Audience: request.Audience, OldPrincipalHash: request.OldPrincipalHash, NewPrincipalHash: request.NewPrincipalHash, OldState: WorkloadPrincipalActive, NewState: WorkloadPrincipalCandidate, Revision: 1, UpdatedAt: service.now().UTC()}
	if err := service.auditLocked(ctx, rotation, RotationCreateCandidate, request.Actor, request.Reason); err != nil {
		return PrincipalRotation{}, err
	}
	service.values[request.Owner] = rotation
	return rotation, nil
}

func (service *PrincipalRotationService) DualAccept(ctx context.Context, owner, actor, reason string) (PrincipalRotation, error) {
	return service.transition(ctx, owner, RotationDualAccept, actor, reason, func(rotation *PrincipalRotation) error {
		if rotation.OldState != WorkloadPrincipalActive || rotation.NewState != WorkloadPrincipalCandidate {
			return ErrRotationInvalidState
		}
		rotation.DualAccepted = true
		return nil
	})
}

func (service *PrincipalRotationService) DrainOld(ctx context.Context, owner, actor, reason string) (PrincipalRotation, error) {
	return service.transition(ctx, owner, RotationDrainOld, actor, reason, func(rotation *PrincipalRotation) error {
		if !rotation.DualAccepted || rotation.OldState != WorkloadPrincipalActive || rotation.NewState != WorkloadPrincipalCandidate {
			return ErrRotationInvalidState
		}
		rotation.OldState = WorkloadPrincipalDraining
		return nil
	})
}

func (service *PrincipalRotationService) SetInFlight(ctx context.Context, owner string, count int64) (PrincipalRotation, error) {
	if service == nil || count < 0 {
		return PrincipalRotation{}, ErrRotationInvalidInput
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	rotation, ok := service.values[owner]
	if !ok {
		return PrincipalRotation{}, ErrRotationNotFound
	}
	rotation.InFlight = count
	rotation.Revision++
	rotation.UpdatedAt = service.now().UTC()
	service.values[owner] = rotation
	return rotation, nil
}

func (service *PrincipalRotationService) RevokeOld(ctx context.Context, owner, actor, reason string) (PrincipalRotation, error) {
	return service.transition(ctx, owner, RotationRevokeOld, actor, reason, func(rotation *PrincipalRotation) error {
		if rotation.OldState != WorkloadPrincipalDraining || rotation.NewState != WorkloadPrincipalCandidate {
			return ErrRotationInvalidState
		}
		if rotation.InFlight != 0 {
			return ErrRotationInFlight
		}
		rotation.OldState = WorkloadPrincipalRevoked
		rotation.NewState = WorkloadPrincipalActive
		return nil
	})
}

// EmergencyRevoke is the audited stop path. It intentionally does not clear
// in-flight work; callers must reconcile/stop it before resuming traffic.
func (service *PrincipalRotationService) EmergencyRevoke(ctx context.Context, owner, actor, reason string) (PrincipalRotation, error) {
	return service.transition(ctx, owner, RotationEmergencyRevoke, actor, reason, func(rotation *PrincipalRotation) error {
		if rotation.OldState == WorkloadPrincipalRevoked {
			return ErrRotationInvalidState
		}
		rotation.OldState = WorkloadPrincipalRevoked
		return nil
	})
}

func (service *PrincipalRotationService) Get(owner string) (PrincipalRotation, error) {
	if service == nil {
		return PrincipalRotation{}, ErrRotationInvalidInput
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	rotation, ok := service.values[owner]
	if !ok {
		return PrincipalRotation{}, ErrRotationNotFound
	}
	return rotation, nil
}

func (service *PrincipalRotationService) transition(ctx context.Context, owner, action, actor, reason string, mutate func(*PrincipalRotation) error) (PrincipalRotation, error) {
	if service == nil || strings.TrimSpace(owner) == "" || strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return PrincipalRotation{}, ErrRotationInvalidInput
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	rotation, ok := service.values[owner]
	if !ok {
		return PrincipalRotation{}, ErrRotationNotFound
	}
	updated := rotation
	if err := mutate(&updated); err != nil {
		return PrincipalRotation{}, err
	}
	updated.Revision++
	updated.UpdatedAt = service.now().UTC()
	if err := service.auditLocked(ctx, updated, action, actor, reason); err != nil {
		return PrincipalRotation{}, err
	}
	service.values[owner] = updated
	return updated, nil
}

func (service *PrincipalRotationService) auditLocked(ctx context.Context, rotation PrincipalRotation, action, actor, reason string) error {
	if service.audit == nil {
		return ErrRotationAudit
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return ErrRotationInvalidInput
	}
	entry := RotationAuditEntry{EventID: fmt.Sprintf("%s-%d", rotation.Owner, rotation.Revision), Owner: rotation.Owner, OldPrincipalHash: rotation.OldPrincipalHash, NewPrincipalHash: rotation.NewPrincipalHash, State: action + ":" + string(rotation.OldState) + ":" + string(rotation.NewState), Actor: actor, Reason: reason, OccurredAt: rotation.UpdatedAt}
	if err := service.audit.AppendRotation(ctx, entry); err != nil {
		return fmt.Errorf("%w: %v", ErrRotationAudit, err)
	}
	return nil
}

func validateCreateCandidate(request CreateCandidateRequest) error {
	if _, ok := workloadOwners[strings.TrimSpace(request.Owner)]; !ok {
		return ErrRotationUnknownOwner
	}
	if !validScope(request.Scope) {
		return ErrRotationInvalidScope
	}
	if strings.TrimSpace(request.Audience) == "" || !validPrincipalHash(request.OldPrincipalHash) || !validPrincipalHash(request.NewPrincipalHash) || request.OldPrincipalHash == request.NewPrincipalHash || strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Reason) == "" {
		return ErrRotationInvalidInput
	}
	return nil
}

func validScope(scope WorkloadScope) bool {
	for _, value := range []string{scope.TenantID, scope.WorkspaceID, scope.OwnerSystem, scope.ResourceType} {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, "*?") {
			return false
		}
	}
	return scope.OwnerSystem == "approver" || scope.OwnerSystem == "fluxion" || scope.OwnerSystem == "bids" || scope.OwnerSystem == "settlement"
}

func validPrincipalHash(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
