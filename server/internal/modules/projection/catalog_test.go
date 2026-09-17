package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type catalogMembershipReader struct{ membership identity.WorkspaceMembership }

func (reader catalogMembershipReader) FindMembership(context.Context, identity.IdentityKey, string, string) (identity.WorkspaceMembership, error) {
	return reader.membership, nil
}

type catalogSchemaReader struct{ definitions map[string]schema.Definition }

func (reader catalogSchemaReader) Get(_ context.Context, tenantID, schemaID string, version int64) (schema.Definition, error) {
	definition, ok := reader.definitions[tenantID+"\x00"+schemaID+"\x00"+fmt.Sprint(version)]
	if !ok {
		return schema.Definition{}, schema.ErrNotFound
	}
	return definition, nil
}

type catalogAuditWriter struct{ entries []audit.Entry }

func (writer *catalogAuditWriter) Append(_ context.Context, entry audit.Entry) error {
	writer.entries = append(writer.entries, entry)
	return nil
}

func catalogTestPrincipal() identity.Principal {
	return identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
}

func catalogTestService(t *testing.T) (*CatalogService, *memoryCatalogStore, *memoryCatalogReceipts, identity.Principal) {
	t.Helper()
	principal := catalogTestPrincipal()
	store := newMemoryCatalogStore()
	receipts := newMemoryCatalogReceipts()
	auditWriter := &catalogAuditWriter{}
	authorizer := identity.NewAuthorizer(catalogMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}})
	rawSchema, err := bson.Marshal(bson.D{{Key: "$schema", Value: schema.Draft202012URI}, {Key: "type", Value: "object"}, {Key: "additionalProperties", Value: false}, {Key: "required", Value: bson.A{"applicationId", "status"}}, {Key: "properties", Value: bson.D{{Key: "applicationId", Value: bson.D{{Key: "type", Value: "string"}}}, {Key: "status", Value: bson.D{{Key: "type", Value: "string"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	schemaReader := catalogSchemaReader{definitions: map[string]schema.Definition{"tenant-1\x00schema.application\x001": {TenantID: "tenant-1", SchemaID: "schema.application", Version: 1, Status: schema.StatusPublished, JSONSchema: bson.Raw(rawSchema)}}}
	return NewCatalogService(store, store, schemaReader, authorizer, receipts, auditWriter), store, receipts, principal
}

func validCatalogSourceInput() SourceCreateInput {
	return SourceCreateInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", SourceID: "approver", EventType: "application.updated", EventVersion: 1, OwnerContact: "team@example.com", TenantResolution: TenantResolution{Mode: TenantResolutionMetadata, Field: "workspaceId"}, RequestID: "request-source-1", IdempotencyKey: "source-1"}
}

func validCatalogMappingInput() MappingCreateInput {
	document := []byte(`{"eventId":"00000000-0000-4000-8000-000000000001","kind":"event","eventType":"application.updated","schemaVersion":1,"sourceSystem":"approver","tenantId":"tenant-1","aggregateType":"Application","aggregateId":"application-1","aggregateVersion":1,"occurredAt":"2026-09-16T00:00:00Z","payload":{"applicationId":"application-1","status":"PENDING"},"metadata":{"workspaceId":"workspace-1"}}`)
	_, hash, _ := canonicalFixture(document)
	return MappingCreateInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", MappingID: "application-summary", SourceID: "approver", EventType: "application.updated", EventVersion: 1, TargetTableID: "applications", TargetSchemaID: "schema.application", TargetSchemaVersion: 1, FieldMap: map[string]string{"applicationId": "payload.applicationId", "status": "payload.status"}, Fixture: MappingFixture{EventRef: "fixture/application-updated.json", SHA256: hash}, FixtureDocument: document, RequestID: "request-mapping-1", IdempotencyKey: "mapping-1"}
}

func TestCatalogCanonicalHashIsStableAndFieldMapOrderIndependent(t *testing.T) {
	left := validCatalogMappingInput()
	right := validCatalogMappingInput()
	right.FieldMap = map[string]string{"status": "payload.status", "applicationId": "payload.applicationId"}
	service, _, _, principal := catalogTestService(t)
	if _, _, err := service.CreateSource(context.Background(), principal, validCatalogSourceInput()); err != nil {
		t.Fatal(err)
	}
	first, _, err := service.CreateMapping(context.Background(), catalogTestPrincipal(), left)
	if err != nil {
		t.Fatal(err)
	}
	// A separate service/store is used to ensure the comparison is not an
	// idempotency replay and therefore exercises canonicalization directly.
	other, _, _, otherPrincipal := catalogTestService(t)
	if _, _, err := other.CreateSource(context.Background(), otherPrincipal, validCatalogSourceInput()); err != nil {
		t.Fatal(err)
	}
	second, _, err := other.CreateMapping(context.Background(), catalogTestPrincipal(), right)
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalHash != second.CanonicalHash {
		t.Fatalf("canonical hash changed with map order: %s vs %s", first.CanonicalHash, second.CanonicalHash)
	}
}

func TestCatalogPublishesSourceAndMappingWithExactPrerequisites(t *testing.T) {
	service, _, receipts, principal := catalogTestService(t)
	source, replay, err := service.CreateSource(context.Background(), principal, validCatalogSourceInput())
	if err != nil || replay || source.Status != CatalogStatusDraft || source.Revision != 1 {
		t.Fatalf("create source = %#v replay=%v err=%v", source, replay, err)
	}
	publishedSource, _, err := service.PublishSource(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: "approver", ExpectedRevision: 1, RequestID: "request-source-publish-1", IdempotencyKey: "source-publish-1"})
	if err != nil || publishedSource.Status != CatalogStatusPublished || publishedSource.Revision != 2 {
		t.Fatalf("publish source = %#v err=%v", publishedSource, err)
	}
	mapping, _, err := service.CreateMapping(context.Background(), principal, validCatalogMappingInput())
	if err != nil || mapping.Status != CatalogStatusDraft {
		t.Fatalf("create mapping = %#v err=%v", mapping, err)
	}
	receipt, err := receipts.Find(context.Background(), "tenant-1", "workspace-1", "mapping.create", "mapping-1")
	if err != nil {
		t.Fatal(err)
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(receiptJSON), "PENDING") || strings.Contains(string(receiptJSON), "00000000-0000-4000-8000-000000000001") {
		t.Fatalf("catalog receipt leaked fixture body: %s", receiptJSON)
	}
	publishedMapping, _, err := service.PublishMapping(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: mapping.ID, ExpectedRevision: 1, RequestID: "request-mapping-publish-1", IdempotencyKey: "mapping-publish-1"})
	if err != nil || publishedMapping.Status != CatalogStatusPublished || publishedMapping.Revision != 2 {
		t.Fatalf("publish mapping = %#v err=%v", publishedMapping, err)
	}
	encoded, err := json.Marshal(publishedMapping)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fixtureDocument") || strings.Contains(string(encoded), "PENDING") || strings.Contains(string(encoded), "00000000-0000-4000-8000-000000000001") {
		t.Fatalf("mapping response leaked fixture body: %s", encoded)
	}
	revoked, _, err := service.RevokeMapping(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: mapping.ID, ExpectedRevision: 2, RequestID: "request-mapping-revoke-1", IdempotencyKey: "mapping-revoke-1"})
	if err != nil || revoked.Status != CatalogStatusRevoked || revoked.Revision != 3 {
		t.Fatalf("revoke mapping = %#v err=%v", revoked, err)
	}
}

func TestCatalogRejectsUnpublishedSourceAndIdempotencyReuse(t *testing.T) {
	service, _, _, principal := catalogTestService(t)
	if _, _, err := service.CreateSource(context.Background(), principal, validCatalogSourceInput()); err != nil {
		t.Fatal(err)
	}
	input := validCatalogMappingInput()
	if _, _, err := service.CreateMapping(context.Background(), principal, input); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.PublishMapping(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: input.MappingID, ExpectedRevision: 1, RequestID: "request-publish-before-source", IdempotencyKey: "publish-before-source"}); !errors.Is(err, ErrSourceNotPublished) {
		t.Fatalf("unpublished source error = %v", err)
	}
	if _, replay, err := service.CreateSource(context.Background(), principal, validCatalogSourceInput()); err != nil || !replay {
		t.Fatalf("same idempotency key should replay: replay=%v err=%v", replay, err)
	}
	conflict := validCatalogSourceInput()
	conflict.OwnerContact = "different@example.com"
	if _, _, err := service.CreateSource(context.Background(), principal, conflict); !errors.Is(err, ErrCatalogIdempotencyConflict) {
		t.Fatalf("idempotency conflict = %v", err)
	}
}

func TestCatalogRejectsTargetFieldsOutsidePublishedSchema(t *testing.T) {
	service, _, _, principal := catalogTestService(t)
	if _, _, err := service.CreateSource(context.Background(), principal, validCatalogSourceInput()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.PublishSource(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: "approver", ExpectedRevision: 1, RequestID: "request-source-publish-fields", IdempotencyKey: "source-publish-fields"}); err != nil {
		t.Fatal(err)
	}
	input := validCatalogMappingInput()
	input.MappingID = "unknown-field-summary"
	input.IdempotencyKey = "mapping-unknown-field"
	input.FieldMap = map[string]string{"secret": "payload.secret"}
	if _, _, err := service.CreateMapping(context.Background(), principal, input); err != nil {
		t.Fatal(err)
	}
	_, _, err := service.PublishMapping(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: input.MappingID, ExpectedRevision: 1, RequestID: "request-mapping-publish-fields", IdempotencyKey: "mapping-publish-fields"})
	if !errors.Is(err, ErrTargetFieldNotAllowed) {
		t.Fatalf("unknown target field error = %v", err)
	}
}

func TestCatalogFixtureHashAndPublishValidation(t *testing.T) {
	service, _, _, principal := catalogTestService(t)
	if _, _, err := service.CreateSource(context.Background(), principal, validCatalogSourceInput()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.PublishSource(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: "approver", ExpectedRevision: 1, RequestID: "request-source-publish-fixtures", IdempotencyKey: "source-publish-fixtures"}); err != nil {
		t.Fatal(err)
	}

	badHash := validCatalogMappingInput()
	badHash.MappingID = "bad-hash"
	badHash.IdempotencyKey = "mapping-bad-hash"
	badHash.Fixture.SHA256 = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, err := service.CreateMapping(context.Background(), principal, badHash); !errors.Is(err, ErrFixtureHashMismatch) {
		t.Fatalf("fixture hash error = %v", err)
	}

	tests := []struct {
		name    string
		replace func(string) string
	}{
		{name: "workspace", replace: func(value string) string {
			return strings.Replace(value, `"workspaceId":"workspace-1"`, `"workspaceId":"workspace-other"`, 1)
		}},
		{name: "source", replace: func(value string) string {
			return strings.Replace(value, `"sourceSystem":"approver"`, `"sourceSystem":"fluxion"`, 1)
		}},
		{name: "missing-path", replace: func(value string) string { return strings.Replace(value, `"applicationId":"application-1",`, "", 1) }},
		{name: "schema", replace: func(value string) string { return strings.Replace(value, `"status":"PENDING"`, `"status":42`, 1) }},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validCatalogMappingInput()
			input.MappingID = "invalid-fixture-" + strconv.Itoa(index)
			input.IdempotencyKey = "mapping-invalid-fixture-" + strconv.Itoa(index)
			input.RequestID = "request-invalid-fixture-" + strconv.Itoa(index)
			input.Fixture.EventRef = "fixture/invalid-" + strconv.Itoa(index) + ".json"
			input.FixtureDocument = []byte(test.replace(string(input.FixtureDocument)))
			_, hash, err := canonicalFixture(input.FixtureDocument)
			if err != nil {
				t.Fatal(err)
			}
			input.Fixture.SHA256 = hash
			mapping, _, err := service.CreateMapping(context.Background(), principal, input)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = service.PublishMapping(context.Background(), principal, CatalogTransitionInput{TenantID: "tenant-1", WorkspaceID: "workspace-1", ID: mapping.ID, ExpectedRevision: 1, RequestID: "request-publish-invalid-" + strconv.Itoa(index), IdempotencyKey: "publish-invalid-" + strconv.Itoa(index)})
			if !errors.Is(err, ErrFixtureInvalid) {
				t.Fatalf("invalid fixture publish error = %v", err)
			}
		})
	}
}

func TestFixtureJSONPointerResolution(t *testing.T) {
	input := validCatalogMappingInput()
	var document interface{}
	if err := json.Unmarshal(input.FixtureDocument, &document); err != nil {
		t.Fatal(err)
	}
	value, ok := resolveFixturePath(document, "/payload/applicationId")
	if !ok || value != "application-1" {
		t.Fatalf("JSON pointer value = %#v ok=%v", value, ok)
	}
}

func TestCatalogMutationsRequireOwnerAndExactWorkspace(t *testing.T) {
	service, _, _, principal := catalogTestService(t)
	service.authorizer = identity.NewAuthorizer(catalogMembershipReader{membership: identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleEditor, Status: identity.MembershipActive}})
	if _, _, err := service.CreateSource(context.Background(), principal, validCatalogSourceInput()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("editor mutation error = %v", err)
	}
	service, _, _, principal = catalogTestService(t)
	crossTenant := validCatalogSourceInput()
	crossTenant.TenantID = "tenant-2"
	crossTenant.WorkspaceID = "workspace-2"
	crossTenant.IdempotencyKey = "source-cross-tenant"
	if _, _, err := service.CreateSource(context.Background(), principal, crossTenant); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("cross-tenant mutation error = %v", err)
	}
	allowlist := validCatalogSourceInput()
	allowlist.SourceID = "approver-allowlist"
	allowlist.IdempotencyKey = "source-cross-tenant-allowlist"
	allowlist.TenantResolution = TenantResolution{Mode: TenantResolutionAllowlist, Allowlist: []TenantResolutionRule{{TenantID: "tenant-2", WorkspaceID: "workspace-2"}}}
	if _, _, err := service.CreateSource(context.Background(), principal, allowlist); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("unauthorized allowlist scope error = %v", err)
	}
}

func TestCatalogHTTPRequiresPrincipalAndRevisionHeader(t *testing.T) {
	service, _, _, principal := catalogTestService(t)
	handler := NewCatalogHTTPHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sources", strings.NewReader(`{"tenantId":"tenant-1"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	body, _ := json.Marshal(map[string]interface{}{"tenantId": "tenant-1", "workspaceId": "workspace-1", "sourceId": "approver", "eventType": "application.updated", "eventVersion": 1, "ownerContact": "team@example.com", "tenantResolution": map[string]string{"mode": "metadata", "field": "workspaceId"}})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/sources", strings.NewReader(string(body)))
	request = request.WithContext(identity.WithPrincipal(context.Background(), principal))
	request.Header.Set("Idempotency-Key", "source-http-1")
	request.Header.Set("X-Request-ID", "request-source-http-1")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create source status = %d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/sources/approver/publish", strings.NewReader(`{"tenantId":"tenant-1","workspaceId":"workspace-1"}`))
	request = request.WithContext(identity.WithPrincipal(context.Background(), principal))
	request.Header.Set("Idempotency-Key", "source-http-publish-1")
	request.Header.Set("X-Request-ID", "request-source-http-publish-1")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d", response.Code)
	}
}

func TestCatalogValidationRejectsUnsafePathsAndAllowlistDuplicates(t *testing.T) {
	input := validCatalogMappingInput()
	input.FieldMap["status"] = "javascript:alert(1)"
	now := time.Now().UTC()
	mapping := MappingRegistration{ID: input.MappingID, TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, SourceID: input.SourceID, EventType: input.EventType, EventVersion: input.EventVersion, TargetTableID: input.TargetTableID, TargetSchemaID: input.TargetSchemaID, TargetSchemaVersion: input.TargetSchemaVersion, FieldMap: input.FieldMap, Fixture: input.Fixture, CanonicalHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: CatalogStatusDraft, Revision: 1, CreatedBy: identity.IdentityKey{Issuer: "i", Subject: "s"}, UpdatedBy: identity.IdentityKey{Issuer: "i", Subject: "s"}, CreatedAt: now, UpdatedAt: now}
	if !errors.Is(mapping.Validate(), ErrCatalogInvalid) {
		t.Fatal("unsafe mapping path was accepted")
	}
	mapping.FieldMap["status"] = "payload.status"
	mapping.Fixture.SHA256 = "not-a-hash"
	if !errors.Is(mapping.Validate(), ErrCatalogInvalid) {
		t.Fatal("invalid fixture hash was accepted")
	}
	source := SourceRegistration{ID: "approver", TenantID: "tenant-1", WorkspaceID: "workspace-1", EventType: "application.updated", EventVersion: 1, OwnerContact: "team@example.com", TenantResolution: TenantResolution{Mode: TenantResolutionAllowlist, Allowlist: []TenantResolutionRule{{TenantID: "tenant-1", WorkspaceID: "workspace-1"}, {TenantID: "tenant-1", WorkspaceID: "workspace-1"}}}, Status: CatalogStatusDraft, Revision: 1, CreatedBy: identity.IdentityKey{Issuer: "i", Subject: "s"}, UpdatedBy: identity.IdentityKey{Issuer: "i", Subject: "s"}, CreatedAt: now, UpdatedAt: now}
	if !errors.Is(source.Validate(), ErrCatalogInvalid) {
		t.Fatal("duplicate allowlist rule was accepted")
	}
	source.TenantResolution = TenantResolution{Mode: TenantResolutionMetadata, Field: "payload.workspaceId"}
	if !errors.Is(source.Validate(), ErrCatalogInvalid) {
		t.Fatal("arbitrary tenant resolution field was accepted")
	}
}
