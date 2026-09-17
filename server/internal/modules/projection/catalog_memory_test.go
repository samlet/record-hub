package projection

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type memoryCatalogStore struct {
	mu       sync.Mutex
	sources  map[string]SourceRegistration
	mappings map[string]MappingRegistration
	fixtures map[string]MappingFixtureDocument
}

func newMemoryCatalogStore() *memoryCatalogStore {
	return &memoryCatalogStore{sources: map[string]SourceRegistration{}, mappings: map[string]MappingRegistration{}, fixtures: map[string]MappingFixtureDocument{}}
}

func catalogKey(tenant, workspace, id string) string {
	return tenant + "\x00" + workspace + "\x00" + id
}

func (store *memoryCatalogStore) CreateSource(_ context.Context, source SourceRegistration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := catalogKey(source.TenantID, source.WorkspaceID, source.ID)
	if _, ok := store.sources[key]; ok {
		return ErrSourceExists
	}
	store.sources[key] = source
	return nil
}

func (store *memoryCatalogStore) FindSource(_ context.Context, tenant, workspace, id string) (SourceRegistration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result, ok := store.sources[catalogKey(tenant, workspace, id)]
	if !ok {
		return SourceRegistration{}, ErrSourceNotFound
	}
	return result, nil
}

func (store *memoryCatalogStore) ListSources(_ context.Context, tenant, workspace string, limit int64) ([]SourceRegistration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]SourceRegistration, 0)
	for _, value := range store.sources {
		if value.TenantID == tenant && value.WorkspaceID == workspace {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if int64(len(result)) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (store *memoryCatalogStore) TransitionSource(_ context.Context, tenant, workspace, id string, status CatalogStatus, actor identity.IdentityKey, at *time.Time, expected int64) (SourceRegistration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := catalogKey(tenant, workspace, id)
	value, ok := store.sources[key]
	if !ok {
		return SourceRegistration{}, ErrSourceNotFound
	}
	if value.Status != CatalogStatusDraft {
		return SourceRegistration{}, ErrSourceImmutable
	}
	if value.Revision != expected {
		return SourceRegistration{}, ErrSourceRevisionConflict
	}
	value.Status = status
	value.Revision++
	value.UpdatedBy = actor
	value.UpdatedAt = *at
	if status == CatalogStatusPublished {
		value.PublishedAt = at
	}
	store.sources[key] = value
	return value, nil
}

func (store *memoryCatalogStore) CreateMapping(_ context.Context, mapping MappingRegistration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := catalogKey(mapping.TenantID, mapping.WorkspaceID, mapping.ID)
	if _, ok := store.mappings[key]; ok {
		return ErrMappingExists
	}
	store.mappings[key] = mapping
	return nil
}

func (store *memoryCatalogStore) FindMapping(_ context.Context, tenant, workspace, id string) (MappingRegistration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result, ok := store.mappings[catalogKey(tenant, workspace, id)]
	if !ok {
		return MappingRegistration{}, ErrMappingNotFound
	}
	return result, nil
}

func (store *memoryCatalogStore) ListMappings(_ context.Context, tenant, workspace, source string, limit int64) ([]MappingRegistration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]MappingRegistration, 0)
	for _, value := range store.mappings {
		if value.TenantID == tenant && value.WorkspaceID == workspace && (source == "" || value.SourceID == source) {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if int64(len(result)) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (store *memoryCatalogStore) TransitionMapping(_ context.Context, tenant, workspace, id string, status CatalogStatus, actor identity.IdentityKey, at *time.Time, expected int64) (MappingRegistration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := catalogKey(tenant, workspace, id)
	value, ok := store.mappings[key]
	if !ok {
		return MappingRegistration{}, ErrMappingNotFound
	}
	allowed := CatalogStatusDraft
	if status == CatalogStatusRevoked {
		allowed = CatalogStatusPublished
	}
	if value.Status != allowed {
		return MappingRegistration{}, ErrMappingImmutable
	}
	if value.Revision != expected {
		return MappingRegistration{}, ErrMappingRevisionConflict
	}
	value.Status = status
	value.Revision++
	value.UpdatedBy = actor
	value.UpdatedAt = *at
	if status == CatalogStatusPublished {
		value.PublishedAt = at
	}
	store.mappings[key] = value
	return value, nil
}

func (store *memoryCatalogStore) PutFixture(_ context.Context, fixture MappingFixtureDocument) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := catalogKey(fixture.TenantID, fixture.WorkspaceID, fixture.EventRef+"\x00"+fixture.SHA256)
	if existing, ok := store.fixtures[key]; ok {
		if string(existing.Document) == string(fixture.Document) {
			return nil
		}
		return ErrFixtureHashMismatch
	}
	store.fixtures[key] = fixture
	return nil
}

func (store *memoryCatalogStore) GetFixture(_ context.Context, tenantID, workspaceID, eventRef, hash string) (MappingFixtureDocument, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	fixture, ok := store.fixtures[catalogKey(tenantID, workspaceID, eventRef+"\x00"+hash)]
	if !ok {
		return MappingFixtureDocument{}, ErrFixtureNotFound
	}
	return fixture, nil
}

type memoryCatalogReceipts struct {
	mu     sync.Mutex
	values map[string]CatalogReceipt
}

func newMemoryCatalogReceipts() *memoryCatalogReceipts {
	return &memoryCatalogReceipts{values: map[string]CatalogReceipt{}}
}

func (store *memoryCatalogReceipts) Find(_ context.Context, tenant, workspace, operation, key string) (CatalogReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.values[catalogKey(tenant, workspace, operation+"\x00"+key)]
	if !ok {
		return CatalogReceipt{}, ErrCatalogReceiptNotFound
	}
	return value, nil
}

func (store *memoryCatalogReceipts) Save(_ context.Context, receipt CatalogReceipt) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := catalogKey(receipt.TenantID, receipt.WorkspaceID, receipt.Operation+"\x00"+receipt.IdempotencyKey)
	if value, ok := store.values[key]; ok {
		if value.RequestHash == receipt.RequestHash {
			return nil
		}
		return ErrCatalogIdempotencyConflict
	}
	store.values[key] = receipt
	return nil
}
