package binding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type memoryRecordReader struct {
	record SourceRecord
	err    error
}

func (reader memoryRecordReader) Read(context.Context, string, string, string) (SourceRecord, error) {
	if reader.err != nil {
		return SourceRecord{}, reader.err
	}
	return reader.record, nil
}

type memorySnapshotStore struct {
	byOperation map[string]Snapshot
	byID        map[string]Snapshot
}

func newMemorySnapshotStore() *memorySnapshotStore {
	return &memorySnapshotStore{byOperation: make(map[string]Snapshot), byID: make(map[string]Snapshot)}
}

func (store *memorySnapshotStore) FindByOperation(_ context.Context, tenantID, workspaceID, operationID string) (Snapshot, error) {
	snapshot, ok := store.byOperation[tenantID+"\x00"+workspaceID+"\x00"+operationID]
	if !ok {
		return Snapshot{}, ErrSnapshotNotFound
	}
	return snapshot, nil
}

func (store *memorySnapshotStore) Create(_ context.Context, snapshot Snapshot) error {
	key := snapshot.TenantID + "\x00" + snapshot.WorkspaceID + "\x00" + snapshot.OperationID
	if _, ok := store.byOperation[key]; ok {
		return ErrSnapshotExists
	}
	if _, ok := store.byID[snapshot.SnapshotID]; ok {
		return ErrSnapshotExists
	}
	store.byOperation[key], store.byID[snapshot.SnapshotID] = snapshot, snapshot
	return nil
}

func (store *memorySnapshotStore) Get(_ context.Context, tenantID, workspaceID, snapshotID string) (Snapshot, error) {
	snapshot, ok := store.byID[snapshotID]
	if !ok || snapshot.TenantID != tenantID || snapshot.WorkspaceID != workspaceID {
		return Snapshot{}, ErrSnapshotNotFound
	}
	return snapshot, nil
}

type membershipReaderFunc func(context.Context, identity.IdentityKey, string, string) (identity.WorkspaceMembership, error)

func (reader membershipReaderFunc) FindMembership(ctx context.Context, key identity.IdentityKey, tenantID, workspaceID string) (identity.WorkspaceMembership, error) {
	return reader(ctx, key, tenantID, workspaceID)
}

func bindingUserAuthorizer() *identity.Authorizer {
	return identity.NewAuthorizer(membershipReaderFunc(func(_ context.Context, key identity.IdentityKey, tenantID, workspaceID string) (identity.WorkspaceMembership, error) {
		if tenantID != "tenant-1" || workspaceID != "workspace-1" {
			return identity.WorkspaceMembership{}, errors.New("membership not found")
		}
		return identity.WorkspaceMembership{TenantID: tenantID, WorkspaceID: workspaceID, Identity: key, Role: identity.RoleViewer, Status: identity.MembershipActive}, nil
	}))
}

func bindingUser() identity.Principal {
	return identity.Principal{Kind: identity.PrincipalUser, Issuer: "https://issuer.example", Subject: "viewer"}
}

func bindingService() identity.Principal {
	return identity.Principal{Kind: identity.PrincipalService, Issuer: "https://issuer.example", Subject: "fluxion-to-record-hub", Audience: []string{"record-hub"}}
}

func validSourceRecord() SourceRecord {
	return SourceRecord{RecordRef: "fluxion:PROJECT:project-1", SchemaID: "urn:record-hub:summary:project:v1", SchemaVersion: 1, RecordVersion: 8, SourceVersion: 17, Data: json.RawMessage(`{"projectId":"project-1","status":"ACTIVE"}`)}
}

func newBindingService(reader RecordReader, store SnapshotStore) *Service {
	service := NewService(reader, store, bindingUserAuthorizer(), NewStaticMachinePolicyAuthorizer(MachinePolicy{Identity: bindingService().IdentityKey(), Audience: "record-hub", TenantID: "tenant-1", WorkspaceID: "workspace-1", Purpose: "diagnostic", ResourceSystem: "fluxion", ResourceType: "PROJECT"}))
	return service.WithClock(func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }).WithIDGenerator(func() (string, error) { return "snapshot-1", nil })
}

func TestCreateSnapshotIsImmutableAndIdempotent(t *testing.T) {
	store := newMemorySnapshotStore()
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, store)
	request := SnapshotRequest{TenantID: "tenant-1", WorkspaceID: "workspace-1", RecordRef: "fluxion:PROJECT:project-1", SchemaID: "urn:record-hub:summary:project:v1", SchemaVersion: 1, ExpectedRecordVersion: 8, ExpectedSourceVersion: 17, Purpose: "diagnostic", OperationID: "op-1"}
	snapshot, replayed, err := service.CreateSnapshot(context.Background(), bindingUser(), request)
	if err != nil || replayed {
		t.Fatalf("create snapshot = %#v, replayed=%v, err=%v", snapshot, replayed, err)
	}
	if snapshot.SnapshotID != "snapshot-1" || snapshot.SnapshotHash == "" || snapshot.CreatedAt.IsZero() {
		t.Fatalf("snapshot metadata incomplete: %#v", snapshot)
	}
	replayedSnapshot, replayed, err := service.CreateSnapshot(context.Background(), bindingUser(), request)
	if err != nil || !replayed || replayedSnapshot.SnapshotID != snapshot.SnapshotID || string(replayedSnapshot.Data) != string(snapshot.Data) {
		t.Fatalf("replay = %#v, replayed=%v, err=%v", replayedSnapshot, replayed, err)
	}
	request.Purpose = "other-purpose"
	if _, _, err := service.CreateSnapshot(context.Background(), bindingUser(), request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed request error = %v", err)
	}
	loaded, err := service.GetSnapshot(context.Background(), bindingUser(), "tenant-1", "workspace-1", snapshot.SnapshotID)
	if err != nil || loaded.SnapshotHash != snapshot.SnapshotHash {
		t.Fatalf("loaded snapshot = %#v, err=%v", loaded, err)
	}
}

func TestSnapshotHashIsStableForPublicFixture(t *testing.T) {
	canonicalData := []byte(`{"projectId":"project-1","status":"ACTIVE"}`)
	hash, err := computeSnapshotHash("urn:record-hub:summary:project:v1", 1, 8, 17, canonicalData)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:2a080a9c320e6eb806bf7bf2dcd772feb3cf4989ad3537a82792accde0ed3594"; hash != want {
		t.Fatalf("snapshot hash = %s, want %s", hash, want)
	}
}

func TestSnapshotDocumentRoundTripKeepsJSONObjectData(t *testing.T) {
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, newMemorySnapshotStore())
	snapshot, _, err := service.CreateSnapshot(context.Background(), bindingUser(), SnapshotRequest{TenantID: "tenant-1", WorkspaceID: "workspace-1", RecordRef: "fluxion:PROJECT:project-1", SchemaID: "urn:record-hub:summary:project:v1", SchemaVersion: 1, ExpectedRecordVersion: 8, ExpectedSourceVersion: 17, Purpose: "diagnostic", OperationID: "round-trip"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := newSnapshotDocument(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := document.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Data) != string(snapshot.Data) || loaded.SnapshotHash != snapshot.SnapshotHash {
		t.Fatalf("round trip changed snapshot: loaded=%#v original=%#v", loaded, snapshot)
	}
}

func TestCreateSnapshotRejectsStaleAndMismatchedInputs(t *testing.T) {
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, newMemorySnapshotStore())
	base := SnapshotRequest{TenantID: "tenant-1", WorkspaceID: "workspace-1", RecordRef: "fluxion:PROJECT:project-1", SchemaID: "urn:record-hub:summary:project:v1", SchemaVersion: 1, ExpectedRecordVersion: 8, ExpectedSourceVersion: 17, Purpose: "diagnostic", OperationID: "op-1"}
	tests := []struct {
		name string
		edit func(*SnapshotRequest)
		want error
	}{
		{name: "record version", edit: func(request *SnapshotRequest) { request.ExpectedRecordVersion = 7 }, want: ErrRecordVersionConflict},
		{name: "source version", edit: func(request *SnapshotRequest) { request.ExpectedSourceVersion = 16 }, want: ErrSourceVersionConflict},
		{name: "schema", edit: func(request *SnapshotRequest) { request.SchemaID = "urn:other" }, want: ErrSchemaMismatch},
		{name: "operation required", edit: func(request *SnapshotRequest) { request.OperationID = "" }, want: ErrIdempotencyKeyRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.edit(&request)
			if _, _, err := service.CreateSnapshot(context.Background(), bindingUser(), request); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMachinePolicyIsExactAcrossScopePurposeAndResource(t *testing.T) {
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, newMemorySnapshotStore())
	request := SnapshotRequest{TenantID: "tenant-1", WorkspaceID: "workspace-1", RecordRef: "fluxion:PROJECT:project-1", SchemaID: "urn:record-hub:summary:project:v1", SchemaVersion: 1, ExpectedRecordVersion: 8, ExpectedSourceVersion: 17, Purpose: "diagnostic", OperationID: "op-1"}
	if _, _, err := service.CreateSnapshot(context.Background(), bindingService(), request); err != nil {
		t.Fatalf("allowed machine request failed: %v", err)
	}
	for name, edit := range map[string]func(*SnapshotRequest){
		"tenant":    func(request *SnapshotRequest) { request.TenantID = "tenant-2" },
		"workspace": func(request *SnapshotRequest) { request.WorkspaceID = "workspace-2" },
		"purpose":   func(request *SnapshotRequest) { request.Purpose = "write-command" },
		"resource":  func(request *SnapshotRequest) { request.RecordRef = "bids:TENDER:tender-1" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			changed.OperationID = "op-" + name
			edit(&changed)
			if _, _, err := service.CreateSnapshot(context.Background(), bindingService(), changed); !errors.Is(err, ErrMachinePolicyDenied) {
				t.Fatalf("error = %v, want machine policy denial", err)
			}
		})
	}
	wrongAudience := bindingService()
	wrongAudience.Audience = []string{"other-service"}
	request.OperationID = "op-audience"
	if _, _, err := service.CreateSnapshot(context.Background(), wrongAudience, request); !errors.Is(err, ErrMachinePolicyDenied) {
		t.Fatalf("wrong audience error = %v", err)
	}
}

func TestBindingHTTPHandlerMapsAuthIdempotencyAndSnapshotResponses(t *testing.T) {
	store := newMemorySnapshotStore()
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, store)
	handler := NewHTTPHandler(service)
	body := `{"tenantId":"tenant-1","workspaceId":"workspace-1","recordRef":"fluxion:PROJECT:project-1","schemaId":"urn:record-hub:summary:project:v1","schemaVersion":1,"expectedRecordVersion":8,"expectedSourceVersion":17,"purpose":"diagnostic"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/bindings/snapshots", strings.NewReader(body)).WithContext(identity.WithPrincipal(context.Background(), bindingUser()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Fatalf("missing idempotency = %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/bindings/snapshots", strings.NewReader(body)).WithContext(identity.WithPrincipal(context.Background(), bindingUser()))
	request.Header.Set("Idempotency-Key", "op-http")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	var snapshot Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/bindings/snapshots", strings.NewReader(body)).WithContext(identity.WithPrincipal(context.Background(), bindingUser()))
	request.Header.Set("Idempotency-Key", "op-http")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("replay status = %d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/bindings/snapshots/"+snapshot.SnapshotID+"?tenantId=tenant-1&workspaceId=workspace-1", nil).WithContext(identity.WithPrincipal(context.Background(), bindingUser()))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), snapshot.SnapshotHash) {
		t.Fatalf("get status = %d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/bindings/snapshots/"+snapshot.SnapshotID+"?tenantId=tenant-1&workspaceId=workspace-2", nil).WithContext(identity.WithPrincipal(context.Background(), bindingUser()))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross workspace status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBindingHTTPHandlerFailsClosedWhenServiceIsMissing(t *testing.T) {
	handler := NewHTTPHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/bindings/snapshots/snapshot-1?tenantId=tenant-1&workspaceId=workspace-1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "BINDING_UNAVAILABLE") {
		t.Fatalf("missing service = %d %s", response.Code, response.Body.String())
	}
}
