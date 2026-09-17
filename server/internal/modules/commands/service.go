package commands

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
	"unicode/utf8"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/binding"
	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type PolicyRegistry interface {
	Find(context.Context, string, string, string) (Policy, error)
}

type StaticPolicyRegistry struct{ policies map[string]Policy }

func NewStaticPolicyRegistry(policies ...Policy) (*StaticPolicyRegistry, error) {
	registry := &StaticPolicyRegistry{policies: make(map[string]Policy, len(policies))}
	for _, policy := range policies {
		if err := policy.Validate(); err != nil {
			return nil, err
		}
		if policy.MaxPayloadBytes <= 0 {
			policy.MaxPayloadBytes = MaxPayloadBytes
		}
		key := policy.TenantID + "\x00" + policy.WorkspaceID + "\x00" + policy.ID
		if _, exists := registry.policies[key]; exists {
			return nil, fmt.Errorf("%w: duplicate policy %s", ErrCommandInvalidRequest, policy.ID)
		}
		registry.policies[key] = policy
	}
	return registry, nil
}

func (registry *StaticPolicyRegistry) Find(_ context.Context, tenantID, workspaceID, policyID string) (Policy, error) {
	if registry == nil {
		return Policy{}, ErrCommandPolicyNotFound
	}
	policy, ok := registry.policies[strings.TrimSpace(tenantID)+"\x00"+strings.TrimSpace(workspaceID)+"\x00"+strings.TrimSpace(policyID)]
	if !ok {
		return Policy{}, ErrCommandPolicyNotFound
	}
	return policy, nil
}

type Store interface {
	FindByIdempotency(context.Context, string, string, string, string) (Operation, error)
	Find(context.Context, string, string, string) (Operation, error)
	Create(context.Context, Operation) error
	SaveCAS(context.Context, Operation, int64) (Operation, error)
}

type Publisher interface {
	Publish(context.Context, string, Envelope) error
}

type Service struct {
	policies   PolicyRegistry
	store      Store
	authorizer binding.PolicyAuthorizer
	publisher  Publisher
	audit      audit.Writer
	clock      func() time.Time
	id         func() (string, error)
}

func NewService(policies PolicyRegistry, store Store, authorizer binding.PolicyAuthorizer, auditWriter audit.Writer) *Service {
	return &Service{policies: policies, store: store, authorizer: authorizer, audit: auditWriter, clock: time.Now, id: newOperationID}
}

func (service *Service) WithPublisher(publisher Publisher) *Service {
	if service != nil {
		service.publisher = publisher
	}
	return service
}

func (service *Service) WithClock(clock func() time.Time) *Service {
	if service != nil && clock != nil {
		service.clock = clock
	}
	return service
}

func (service *Service) WithIDGenerator(generator func() (string, error)) *Service {
	if service != nil && generator != nil {
		service.id = generator
	}
	return service
}

func (service *Service) Submit(ctx context.Context, principal identity.Principal, request SubmitRequest) (Operation, bool, error) {
	if service == nil || service.policies == nil || service.store == nil || service.authorizer == nil || service.clock == nil || service.id == nil {
		return Operation{}, false, ErrCommandUnavailable
	}
	request, payload, payloadHash, err := normalizeRequest(request)
	if err != nil {
		return Operation{}, false, err
	}
	if principal.Kind != identity.PrincipalService || principal.Issuer == "" || principal.Subject == "" {
		return Operation{}, false, ErrCommandPolicyDenied
	}
	policy, err := service.policies.Find(ctx, request.TenantID, request.WorkspaceID, request.PolicyID)
	if err != nil {
		return Operation{}, false, err
	}
	if policy.MaxPayloadBytes > 0 && len(payload) > policy.MaxPayloadBytes {
		return Operation{}, false, ErrCommandPayloadTooLarge
	}
	reference, err := binding.ParseRecordRef(request.ResourceRef)
	if err != nil || reference.System != policy.OwnerSystem || reference.Type != policy.ResourceType {
		return Operation{}, false, ErrCommandPolicyDenied
	}
	if policy.ExpectedVersionRequired && request.ExpectedVersion == nil {
		return Operation{}, false, ErrCommandExpectedVersion
	}
	if service.authorizer == nil {
		return Operation{}, false, ErrCommandPolicyDenied
	}
	if err := service.authorizer.Authorize(ctx, principal, binding.BindingAuthorization{TenantID: request.TenantID, WorkspaceID: request.WorkspaceID, Purpose: policy.Purpose, RecordRef: request.ResourceRef, ResourceSystem: policy.OwnerSystem, ResourceType: policy.ResourceType}); err != nil {
		return Operation{}, false, ErrCommandPolicyDenied
	}
	if existing, findErr := service.store.FindByIdempotency(ctx, request.TenantID, request.WorkspaceID, policy.ID, request.IdempotencyKey); findErr == nil {
		if existing.PayloadHash != payloadHash {
			return Operation{}, false, ErrCommandIdempotency
		}
		if existing.Status == StatusAccepted {
			existing = service.dispatch(ctx, existing)
		}
		return existing, true, nil
	} else if !errors.Is(findErr, ErrCommandNotFound) {
		return Operation{}, false, findErr
	}
	operationID, err := service.id()
	if err != nil {
		return Operation{}, false, ErrCommandUnavailable
	}
	now := service.clock().UTC()
	operation := Operation{ID: operationID, TenantID: request.TenantID, WorkspaceID: request.WorkspaceID, PolicyID: policy.ID, OwnerSystem: policy.OwnerSystem, ResourceType: policy.ResourceType, Action: policy.Action, Purpose: policy.Purpose, ResourceRef: request.ResourceRef, ExpectedVersion: request.ExpectedVersion, PayloadHash: payloadHash, Payload: payload, IdempotencyKey: request.IdempotencyKey, RequestID: request.RequestID, Status: StatusAccepted, Revision: 1, CreatedBy: principal.IdentityKey(), CreatedAt: now, UpdatedAt: now}
	if err := operation.Validate(); err != nil {
		return Operation{}, false, err
	}
	if err := service.store.Create(ctx, operation); err != nil {
		if errors.Is(err, ErrCommandExists) {
			existing, findErr := service.store.FindByIdempotency(ctx, request.TenantID, request.WorkspaceID, policy.ID, request.IdempotencyKey)
			if findErr == nil && existing.PayloadHash == payloadHash {
				return existing, true, nil
			}
			return Operation{}, false, ErrCommandIdempotency
		}
		return Operation{}, false, err
	}
	if service.audit != nil {
		if err := service.audit.Append(ctx, audit.Entry{TenantID: operation.TenantID, WorkspaceID: operation.WorkspaceID, Action: "command.submit", Actor: principal.IdentityKey(), ResourceType: "CommandOperation", ResourceID: operation.ID, ResourceVersion: operation.Revision, RequestID: operation.RequestID, IdempotencyKey: operation.IdempotencyKey, AfterHash: operation.PayloadHash, CreatedAt: operation.CreatedAt}); err != nil {
			operation.SafeError = "audit unavailable"
		}
	}
	operation = service.dispatch(ctx, operation)
	return operation, false, nil
}

func (service *Service) dispatch(ctx context.Context, operation Operation) Operation {
	if service == nil || service.publisher == nil || operation.Status != StatusAccepted {
		return operation
	}
	policy := Envelope{OperationID: operation.ID, TenantID: operation.TenantID, WorkspaceID: operation.WorkspaceID, PolicyID: operation.PolicyID, OwnerSystem: operation.OwnerSystem, ResourceType: operation.ResourceType, Action: operation.Action, Purpose: operation.Purpose, ResourceRef: operation.ResourceRef, ExpectedVersion: operation.ExpectedVersion, PayloadHash: operation.PayloadHash, Payload: operation.Payload, RequestedBy: operation.CreatedBy, CreatedAt: operation.CreatedAt}
	if err := service.publisher.Publish(ctx, commandSubject(operation.OwnerSystem, operation.Action), policy); err != nil {
		return operation
	}
	updated := operation
	updated.Status = StatusDispatched
	updated.Revision++
	updated.UpdatedAt = service.clock().UTC()
	if saved, err := service.store.SaveCAS(ctx, updated, operation.Revision); err == nil {
		return saved
	}
	return operation
}

func (service *Service) Get(ctx context.Context, principal identity.Principal, tenantID, workspaceID, operationID string) (Operation, error) {
	if service == nil || service.store == nil || service.policies == nil || service.authorizer == nil {
		return Operation{}, ErrCommandUnavailable
	}
	operation, err := service.store.Find(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID), strings.TrimSpace(operationID))
	if err != nil {
		return Operation{}, err
	}
	if principal.Kind != identity.PrincipalService || principal.Issuer == "" || principal.Subject == "" {
		return Operation{}, ErrCommandPolicyDenied
	}
	policy, err := service.policies.Find(ctx, operation.TenantID, operation.WorkspaceID, operation.PolicyID)
	if err != nil {
		return Operation{}, ErrCommandPolicyDenied
	}
	if err := service.authorizer.Authorize(ctx, principal, binding.BindingAuthorization{TenantID: operation.TenantID, WorkspaceID: operation.WorkspaceID, Purpose: policy.Purpose, RecordRef: operation.ResourceRef, ResourceSystem: policy.OwnerSystem, ResourceType: policy.ResourceType}); err != nil {
		return Operation{}, ErrCommandPolicyDenied
	}
	return operation, nil
}

// ApplyResult advances an accepted/dispatched operation from an owner result
// event. Event IDs are the durable idempotency key: an identical replay is a
// no-op, while a different terminal result is rejected as a conflict.
func (service *Service) ApplyResult(ctx context.Context, result ResultEnvelope) (Operation, bool, error) {
	if service == nil || service.store == nil || service.clock == nil {
		return Operation{}, false, ErrCommandUnavailable
	}
	if err := result.Validate(); err != nil {
		return Operation{}, false, err
	}
	operation, err := service.store.Find(ctx, result.TenantID, result.WorkspaceID, result.OperationID)
	if err != nil {
		return Operation{}, false, err
	}
	if operation.OwnerSystem != result.OwnerSystem || operation.Action != result.Action {
		return Operation{}, false, ErrCommandResultConflict
	}
	if operation.Status != StatusAccepted && operation.Status != StatusDispatched {
		if operation.ResultEventID == result.EventID && operation.Status == result.Status && operation.ResultHash == result.ResultHash && operation.SafeError == result.SafeError && sameOptionalVersion(operation.ResultVersion, result.ResultVersion) {
			return operation, true, nil
		}
		return Operation{}, false, ErrCommandResultConflict
	}
	updated := operation
	updated.Status = result.Status
	updated.SafeError = result.SafeError
	updated.ResultEventID = result.EventID
	updated.ResultHash = result.ResultHash
	updated.ResultVersion = result.ResultVersion
	updated.Revision++
	updated.UpdatedAt = result.OccurredAt.UTC()
	if updated.UpdatedAt.Before(operation.UpdatedAt) {
		updated.UpdatedAt = service.clock().UTC()
	}
	saved, err := service.store.SaveCAS(ctx, updated, operation.Revision)
	if err != nil {
		return Operation{}, false, err
	}
	if service.audit != nil {
		_ = service.audit.Append(ctx, audit.Entry{TenantID: saved.TenantID, WorkspaceID: saved.WorkspaceID, Action: "command.result", Actor: identity.IdentityKey{Issuer: saved.OwnerSystem, Subject: saved.OwnerSystem}, ResourceType: "CommandOperation", ResourceID: saved.ID, ResourceVersion: saved.Revision, AfterHash: saved.ResultHash, CreatedAt: saved.UpdatedAt})
	}
	return saved, false, nil
}

func sameOptionalVersion(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// HandleResultMessage is the durable JetStream consumer handler. It bounds
// input before decoding and rejects unknown/trailing JSON without logging the
// raw message body.
func (service *Service) HandleResultMessage(ctx context.Context, message jetstream.Msg) error {
	if message == nil || len(message.Data()) == 0 || len(message.Data()) > MaxPayloadBytes || !utf8.Valid(message.Data()) {
		return ErrCommandResultInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(message.Data())))
	decoder.DisallowUnknownFields()
	var result ResultEnvelope
	if err := decoder.Decode(&result); err != nil {
		return ErrCommandResultInvalid
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrCommandResultInvalid
	}
	_, _, err := service.ApplyResult(ctx, result)
	return err
}

func normalizeRequest(request SubmitRequest) (SubmitRequest, []byte, string, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.PolicyID = strings.TrimSpace(request.PolicyID)
	request.ResourceRef = strings.TrimSpace(request.ResourceRef)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.TenantID == "" || request.WorkspaceID == "" || request.PolicyID == "" || request.ResourceRef == "" || request.IdempotencyKey == "" {
		return SubmitRequest{}, nil, "", ErrCommandInvalidRequest
	}
	if len(request.IdempotencyKey) > 128 || len(request.RequestID) > 256 {
		return SubmitRequest{}, nil, "", ErrCommandInvalidRequest
	}
	if request.ExpectedVersion != nil && *request.ExpectedVersion < 1 {
		return SubmitRequest{}, nil, "", ErrCommandInvalidRequest
	}
	if _, err := binding.ParseRecordRef(request.ResourceRef); err != nil {
		return SubmitRequest{}, nil, "", ErrCommandInvalidRequest
	}
	payload, err := canonicalPayload(request.Payload)
	if err != nil {
		return SubmitRequest{}, nil, "", err
	}
	hash := sha256.Sum256(payload)
	return request, payload, "sha256:" + hex.EncodeToString(hash[:]), nil
}

func canonicalPayload(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes || !utf8.Valid(raw) {
		return nil, ErrCommandPayloadTooLarge
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value map[string]interface{}
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, ErrCommandInvalidRequest
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrCommandInvalidRequest
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > MaxPayloadBytes {
		return nil, ErrCommandPayloadTooLarge
	}
	return canonical, nil
}

func commandSubject(ownerSystem, action string) string {
	return "commands." + ownerSystem + "." + action + ".v1"
}

func ResultSubject(ownerSystem, action string) string {
	return "results." + ownerSystem + "." + action + ".v1"
}

func newOperationID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "cmd-" + hex.EncodeToString(value), nil
}
