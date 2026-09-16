package schema

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type memoryRegistry struct {
	definitions map[string]Definition
	creates     int
}

func (r *memoryRegistry) Create(_ context.Context, definition Definition) error {
	key := schemaKey(definition.TenantID, definition.SchemaID, definition.Version)
	if _, exists := r.definitions[key]; exists {
		return ErrDuplicate
	}
	r.definitions[key] = definition
	r.creates++
	return nil
}

func (r *memoryRegistry) Get(_ context.Context, tenantID, schemaID string, version int64) (Definition, error) {
	definition, ok := r.definitions[schemaKey(tenantID, schemaID, version)]
	if !ok {
		return Definition{}, ErrNotFound
	}
	return definition, nil
}

func (r *memoryRegistry) UpdateDraft(_ context.Context, definition Definition, expectedRevision int64) (Definition, error) {
	key := schemaKey(definition.TenantID, definition.SchemaID, definition.Version)
	current, ok := r.definitions[key]
	if !ok {
		return Definition{}, ErrNotFound
	}
	if current.Status != StatusDraft {
		return Definition{}, ErrImmutable
	}
	if current.Revision != expectedRevision {
		return Definition{}, ErrRevisionConflict
	}
	definition.Revision = expectedRevision + 1
	definition.UpdatedAt = time.Now().UTC()
	r.definitions[key] = definition
	return definition, nil
}

func (r *memoryRegistry) PublishDefinition(_ context.Context, tenantID, schemaID string, version, expectedRevision int64, publisher identity.IdentityKey, contentHash string) (Definition, error) {
	key := schemaKey(tenantID, schemaID, version)
	definition, ok := r.definitions[key]
	if !ok {
		return Definition{}, ErrNotFound
	}
	if definition.Status != StatusDraft {
		return Definition{}, ErrImmutable
	}
	if definition.Revision != expectedRevision {
		return Definition{}, ErrRevisionConflict
	}
	now := time.Now().UTC()
	definition.Status = StatusPublished
	definition.Revision++
	definition.ContentHash = contentHash
	definition.PublishedBy = &publisher
	definition.PublishedAt = &now
	definition.UpdatedAt = now
	r.definitions[key] = definition
	return definition, nil
}

type serviceMembershipReader struct{ membership identity.WorkspaceMembership }

func (r serviceMembershipReader) FindMembership(context.Context, identity.IdentityKey, string, string) (identity.WorkspaceMembership, error) {
	return r.membership, nil
}

type memoryReceipts struct{ values map[string]Receipt }

func (r *memoryReceipts) Find(_ context.Context, tenantID, workspaceID, operation, key string) (Receipt, error) {
	receipt, ok := r.values[receiptKey(tenantID, workspaceID, operation, key)]
	if !ok {
		return Receipt{}, ErrReceiptNotFound
	}
	return receipt, nil
}

func (r *memoryReceipts) Save(_ context.Context, receipt Receipt) error {
	r.values[receiptKey(receipt.TenantID, receipt.WorkspaceID, receipt.Operation, receipt.IdempotencyKey)] = receipt
	return nil
}

type memoryAudit struct{ entries []audit.Entry }

func (w *memoryAudit) Append(_ context.Context, entry audit.Entry) error {
	w.entries = append(w.entries, entry)
	return nil
}

func TestSchemaServiceCreateIdempotencyAndAuthorization(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	registry := &memoryRegistry{definitions: make(map[string]Definition)}
	receipts := &memoryReceipts{values: make(map[string]Receipt)}
	auditWriter := &memoryAudit{}
	service := NewService(registry, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive,
	}}), receipts, auditWriter)
	input := draftInput(t)
	created, err := service.CreateDraft(context.Background(), principal, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != StatusDraft || registry.creates != 1 || len(auditWriter.entries) != 1 {
		t.Fatalf("unexpected create result registry=%d audit=%d definition=%#v", registry.creates, len(auditWriter.entries), created)
	}
	if _, err := service.CreateDraft(context.Background(), principal, input); err != nil {
		t.Fatalf("same idempotency request should replay: %v", err)
	}
	if registry.creates != 1 || len(auditWriter.entries) != 1 {
		t.Fatalf("idempotent replay repeated side effects: registry=%d audit=%d", registry.creates, len(auditWriter.entries))
	}
	conflicting := input
	conflicting.Name = "DifferentName"
	if _, err := service.CreateDraft(context.Background(), principal, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different request with same key = %v", err)
	}

	editor := principal
	editor.Subject = "editor-1"
	if _, err := service.CreateDraft(context.Background(), editor, input); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("editor schema mutation = %v", err)
	}
}

func TestSchemaServiceUpdatePublishAndRevision(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner-1"}
	registry := &memoryRegistry{definitions: make(map[string]Definition)}
	receipts := &memoryReceipts{values: make(map[string]Receipt)}
	service := NewService(registry, identity.NewAuthorizer(serviceMembershipReader{membership: identity.WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive,
	}}), receipts, &memoryAudit{})
	input := draftInput(t)
	if _, err := service.CreateDraft(context.Background(), principal, input); err != nil {
		t.Fatal(err)
	}
	update := input
	update.IdempotencyKey = "update-1"
	update.SemanticTypes = []string{"https://schema.org/Action"}
	updated, err := service.UpdateDraft(context.Background(), principal, update, 1)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update result=%#v err=%v", updated, err)
	}
	stale := update
	stale.IdempotencyKey = "update-stale"
	if _, err := service.UpdateDraft(context.Background(), principal, stale, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update = %v", err)
	}
	publish := update
	publish.IdempotencyKey = "publish-1"
	published, err := service.Publish(context.Background(), principal, publish, 2)
	if err != nil || published.Status != StatusPublished || published.Revision != 3 || published.ContentHash == "" {
		t.Fatalf("publish result=%#v err=%v", published, err)
	}
	if replay, err := service.Publish(context.Background(), principal, publish, 2); err != nil || replay.Status != StatusPublished {
		t.Fatalf("publish replay result=%#v err=%v", replay, err)
	}
	afterPublish := update
	afterPublish.IdempotencyKey = "update-after-publish"
	if _, err := service.UpdateDraft(context.Background(), principal, afterPublish, 2); !errors.Is(err, ErrImmutable) {
		t.Fatalf("published update = %v", err)
	}
}

func draftInput(t *testing.T) DraftInput {
	t.Helper()
	raw, err := bson.Marshal(bson.D{{Key: "$schema", Value: Draft202012URI}, {Key: "type", Value: "object"}})
	if err != nil {
		t.Fatal(err)
	}
	return DraftInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", SchemaID: "urn:record-hub:test:service", Name: "ServiceSchema", Version: 1, JSONSchema: bson.Raw(raw), IdempotencyKey: "create-1"}
}

func schemaKey(tenantID, schemaID string, version int64) string {
	return fmt.Sprintf("%s\x00%s\x00%d", tenantID, schemaID, version)
}

func receiptKey(tenantID, workspaceID, operation, key string) string {
	return tenantID + "\x00" + workspaceID + "\x00" + operation + "\x00" + key
}
