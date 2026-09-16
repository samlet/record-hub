package binding

import (
	"context"
	"errors"
	"testing"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestM7SnapshotScopeMatrixDeniesUserCrossTenantAndWorkspace(t *testing.T) {
	base := SnapshotRequest{
		TenantID:              "tenant-1",
		WorkspaceID:           "workspace-1",
		RecordRef:             "fluxion:PROJECT:project-1",
		SchemaID:              "urn:record-hub:summary:project:v1",
		SchemaVersion:         1,
		ExpectedRecordVersion: 8,
		ExpectedSourceVersion: 17,
		Purpose:               "diagnostic",
	}
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, newMemorySnapshotStore())
	for _, test := range []struct {
		name string
		edit func(*SnapshotRequest)
	}{
		{name: "cross tenant", edit: func(request *SnapshotRequest) { request.TenantID = "tenant-2" }},
		{name: "cross workspace", edit: func(request *SnapshotRequest) { request.WorkspaceID = "workspace-2" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.OperationID = "m7-user-" + test.name
			test.edit(&request)
			if _, _, err := service.CreateSnapshot(context.Background(), bindingUser(), request); !errors.Is(err, identity.ErrForbidden) {
				t.Fatalf("cross-scope create error = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestM7SnapshotScopeMatrixDeniesMachineCrossScopeAndIdentity(t *testing.T) {
	base := SnapshotRequest{
		TenantID:              "tenant-1",
		WorkspaceID:           "workspace-1",
		RecordRef:             "fluxion:PROJECT:project-1",
		SchemaID:              "urn:record-hub:summary:project:v1",
		SchemaVersion:         1,
		ExpectedRecordVersion: 8,
		ExpectedSourceVersion: 17,
		Purpose:               "diagnostic",
	}
	for _, test := range []struct {
		name          string
		editRequest   func(*SnapshotRequest)
		editPrincipal func(*identity.Principal)
	}{
		{name: "cross tenant", editRequest: func(request *SnapshotRequest) { request.TenantID = "tenant-2" }},
		{name: "cross workspace", editRequest: func(request *SnapshotRequest) { request.WorkspaceID = "workspace-2" }},
		{name: "wrong purpose", editRequest: func(request *SnapshotRequest) { request.Purpose = "write-command" }},
		{name: "wrong resource type", editRequest: func(request *SnapshotRequest) { request.RecordRef = "fluxion:TENDER:tender-1" }},
		{name: "wrong audience", editPrincipal: func(principal *identity.Principal) { principal.Audience = []string{"other-service"} }},
		{name: "wrong scope", editPrincipal: func(principal *identity.Principal) { principal.Scopes = []string{"recordhub.command.submit"} }},
		{name: "wrong identity", editPrincipal: func(principal *identity.Principal) { principal.Subject = "other-service" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newBindingService(memoryRecordReader{record: validSourceRecord()}, newMemorySnapshotStore())
			request := base
			request.OperationID = "m7-machine-" + test.name
			principal := bindingService()
			if test.editRequest != nil {
				test.editRequest(&request)
			}
			if test.editPrincipal != nil {
				test.editPrincipal(&principal)
			}
			if _, _, err := service.CreateSnapshot(context.Background(), principal, request); !errors.Is(err, ErrMachinePolicyDenied) {
				t.Fatalf("cross-boundary create error = %v, want ErrMachinePolicyDenied", err)
			}
		})
	}
}

func TestM7SnapshotRowReferenceMismatchFailsClosed(t *testing.T) {
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, newMemorySnapshotStore())
	request := SnapshotRequest{
		TenantID:              "tenant-1",
		WorkspaceID:           "workspace-1",
		RecordRef:             "fluxion:PROJECT:project-2",
		SchemaID:              "urn:record-hub:summary:project:v1",
		SchemaVersion:         1,
		ExpectedRecordVersion: 8,
		ExpectedSourceVersion: 17,
		Purpose:               "diagnostic",
		OperationID:           "m7-row-mismatch",
	}
	// The adapter is expected to return the exact stable row reference it read.
	// A mismatched reference must never be rebound to the requested row.
	if _, _, err := service.CreateSnapshot(context.Background(), bindingService(), request); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("row mismatch error = %v, want ErrRecordNotFound", err)
	}
}

func TestM7SnapshotReadScopeMatrixDoesNotExposeExistingSnapshot(t *testing.T) {
	store := newMemorySnapshotStore()
	service := newBindingService(memoryRecordReader{record: validSourceRecord()}, store)
	request := SnapshotRequest{
		TenantID:              "tenant-1",
		WorkspaceID:           "workspace-1",
		RecordRef:             "fluxion:PROJECT:project-1",
		SchemaID:              "urn:record-hub:summary:project:v1",
		SchemaVersion:         1,
		ExpectedRecordVersion: 8,
		ExpectedSourceVersion: 17,
		Purpose:               "diagnostic",
		OperationID:           "m7-read-scope",
	}
	snapshot, _, err := service.CreateSnapshot(context.Background(), bindingUser(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		tenantID    string
		workspaceID string
	}{
		{name: "cross tenant", tenantID: "tenant-2", workspaceID: "workspace-1"},
		{name: "cross workspace", tenantID: "tenant-1", workspaceID: "workspace-2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.GetSnapshot(context.Background(), bindingUser(), test.tenantID, test.workspaceID, snapshot.SnapshotID); !errors.Is(err, identity.ErrForbidden) {
				t.Fatalf("cross-scope read error = %v, want ErrForbidden", err)
			}
		})
	}
}
