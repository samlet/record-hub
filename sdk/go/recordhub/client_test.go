package recordhub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCreateAndGetSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" || (request.Method == http.MethodPost && request.Header.Get("Idempotency-Key") != "operation-1") {
			t.Fatalf("missing binding headers: %#v", request.Header)
		}
		if request.Method == http.MethodPost && request.URL.Path == "/api/v1/bindings/snapshots" {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"snapshotId":"snapshot-1","tenantId":"tenant-1","workspaceId":"workspace-1","recordRef":"fluxion:PROJECT:project-1","schemaId":"urn:summary","schemaVersion":1,"recordVersion":8,"sourceVersion":17,"purpose":"diagnostic","snapshotHash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","data":{"status":"ACTIVE"},"createdAt":"2026-09-16T12:00:00Z"}`))
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/api/v1/bindings/snapshots/snapshot-1" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"snapshotId":"snapshot-1","tenantId":"tenant-1","workspaceId":"workspace-1","recordRef":"fluxion:PROJECT:project-1","schemaId":"urn:summary","schemaVersion":1,"recordVersion":8,"sourceVersion":17,"purpose":"diagnostic","snapshotHash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","data":{"status":"ACTIVE"},"createdAt":"2026-09-16T12:00:00Z"}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.WithBearerToken("token")
	snapshot, replayed, err := client.CreateSnapshot(context.Background(), SnapshotRequest{TenantID: "tenant-1", WorkspaceID: "workspace-1", RecordRef: "fluxion:PROJECT:project-1", SchemaID: "urn:summary", SchemaVersion: 1, ExpectedRecordVersion: 8, ExpectedSourceVersion: 17, Purpose: "diagnostic"}, "operation-1")
	if err != nil || replayed || snapshot.SnapshotID != "snapshot-1" {
		t.Fatalf("create = %#v, replayed=%v, err=%v", snapshot, replayed, err)
	}
	loaded, err := client.GetSnapshot(context.Background(), "tenant-1", "workspace-1", "snapshot-1")
	if err != nil || loaded.SnapshotHash != snapshot.SnapshotHash {
		t.Fatalf("get = %#v, err=%v", loaded, err)
	}
}

func TestClientMapsAPIErrorAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/cancel" {
			<-request.Context().Done()
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":{"code":"SCHEMA_MISMATCH","message":"stale"}}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.do(context.Background(), http.MethodGet, "/error", "", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.Code != "SCHEMA_MISMATCH" {
		t.Fatalf("api error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _, err = client.do(ctx, http.MethodGet, "/cancel", "", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation error = %v", err)
	}
	if _, err := NewClient(strings.TrimSpace("not a URL"), nil); err == nil {
		t.Fatal("invalid base URL should fail")
	}
}
