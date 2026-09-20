package records

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/schema"
)

type memoryTagDictionaryRepository struct {
	values map[string]TagDictionary
}

func (repository *memoryTagDictionaryRepository) GetTagDictionary(_ context.Context, tenantID, workspaceID, tableID string) (TagDictionary, error) {
	dictionary, ok := repository.values[tagDictionaryScopeKey(tenantID, workspaceID, tableID)]
	if !ok {
		return TagDictionary{}, ErrTagDictionaryNotFound
	}
	return dictionary, nil
}

func (repository *memoryTagDictionaryRepository) ListTagDictionaries(_ context.Context, tenantID string) ([]TagDictionary, error) {
	result := make([]TagDictionary, 0)
	for _, dictionary := range repository.values {
		if dictionary.TenantID == tenantID {
			result = append(result, dictionary)
		}
	}
	if len(result) == 0 {
		return nil, ErrTagDictionaryNotFound
	}
	return result, nil
}

func (repository *memoryTagDictionaryRepository) SaveTagDictionary(_ context.Context, dictionary TagDictionary, expectedRevision int64) error {
	key := tagDictionaryScopeKey(dictionary.TenantID, dictionary.WorkspaceID, dictionary.TableID)
	current, exists := repository.values[key]
	if expectedRevision == 0 {
		if exists {
			return ErrTagDictionaryExists
		}
	} else if !exists || current.Revision != expectedRevision {
		return ErrTagDictionaryVersionConflict
	}
	repository.values[key] = dictionary
	return nil
}

func TestTagDictionaryHTTPUsesOwnerCASAndScopedRead(t *testing.T) {
	principal := identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "owner"}
	membership := identity.WorkspaceMembership{TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: identity.RoleOwner, Status: identity.MembershipActive}
	tables := &memoryTableRepository{values: make(map[string]TableDefinition)}
	tagStore := &memoryTagDictionaryRepository{values: make(map[string]TagDictionary)}
	service := NewRecordService(nil, tables, memorySchemaReader{definition: schema.Definition{TenantID: "tenant-1", SchemaID: "schema", Version: 1, Status: schema.StatusPublished}}, identity.NewAuthorizer(serviceMembershipReader{membership: membership}), &memoryRecordRepository{values: make(map[string]Record)}, &memoryRecordReceipts{values: make(map[string]RecordReceipt)}, &memoryRecordAudit{}).WithTagDictionaryRepository(tagStore)
	handler := NewHTTPHandler(service)
	create := doRecordsRequest(handler, &principal, http.MethodPut, "/api/v1/tag-dictionaries", `{"tenantId":"tenant-1","workspaceId":"workspace-1","entries":[{"id":"approval.pending","label":"Pending","group":"approval-state","scope":"workspace","active":true}]}`)
	if create.Code != http.StatusOK || create.Header().Get("ETag") != `"1"` {
		t.Fatalf("dictionary create status=%d etag=%q body=%s", create.Code, create.Header().Get("ETag"), create.Body.String())
	}
	listed := doRecordsRequest(handler, &principal, http.MethodGet, "/api/v1/tag-dictionaries?tenantId=tenant-1&workspaceId=workspace-1&tableId=table-1", "")
	if listed.Code != http.StatusOK || !containsBody(listed.Body.String(), "approval.pending") {
		t.Fatalf("dictionary list status=%d body=%s", listed.Code, listed.Body.String())
	}
	stale := doRecordsRequestWithHeaders(handler, &principal, http.MethodPut, "/api/v1/tag-dictionaries", `{"tenantId":"tenant-1","workspaceId":"workspace-1","entries":[]}`, map[string]string{"If-Match": `"9"`})
	if stale.Code != http.StatusConflict || !containsBody(stale.Body.String(), "TAG_DICTIONARY_VERSION_CONFLICT") {
		t.Fatalf("dictionary stale update status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func containsBody(body, value string) bool { return len(body) > 0 && strings.Contains(body, value) }
