package projection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/records"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func validProjectionApply(t *testing.T) ProjectionApply {
	t.Helper()
	creator := identity.IdentityKey{Issuer: "https://issuer.example", Subject: "projector"}
	now := time.Now().UTC()
	record := records.Record{ID: "record-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", TableID: "projection-1", SchemaID: "urn:record-hub:schema:project", SchemaVersion: 1, RecordVersion: 1, Data: mustProjectionData(t, `{"title":"project"}`), Projection: &records.ProjectionState{LastEventID: "event-1", SyncedAt: now, Status: "CURRENT"}, CreatedBy: creator, UpdatedBy: creator, CreatedAt: now, UpdatedAt: now}
	return ProjectionApply{InboxEvent: InboxEvent{EventID: "event-1", Consumer: "record-hub-fluxion-v1", Subject: "events.fluxion.project.changed.v1", PayloadHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: InboxProcessing, ReceivedAt: now}, Record: record, Checkpoint: ProjectionCheckpoint{TenantID: record.TenantID, WorkspaceID: record.WorkspaceID, Consumer: "record-hub-fluxion-v1", SourceSystem: "fluxion", AggregateType: "project", AggregateID: "project-1", SourceVersion: 1, LastEventID: "event-1", SyncedAt: now, Status: CheckpointCurrent}, Audit: audit.Entry{TenantID: record.TenantID, WorkspaceID: record.WorkspaceID, Action: "projection.apply", Actor: creator, ResourceType: "Record", ResourceID: record.ID, ResourceVersion: record.RecordVersion, CreatedAt: now}}
}

func mustProjectionData(t *testing.T, value string) bson.Raw {
	t.Helper()
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(value), false, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProjectionApplyValidationBindsInboxRecordCheckpointAndAudit(t *testing.T) {
	input := validProjectionApply(t)
	if err := validateProjectionApply(input); err != nil {
		t.Fatal(err)
	}
	input.Record.Projection = nil
	if !errors.Is(validateProjectionApply(input), ErrProjectionRecordRequired) {
		t.Fatalf("missing projection state error = %v", validateProjectionApply(input))
	}
	input = validProjectionApply(t)
	input.Checkpoint.LastEventID = "event-other"
	if err := validateProjectionApply(input); err == nil {
		t.Fatal("event identity mismatch should fail")
	}
	input = validProjectionApply(t)
	input.InboxEvent.PayloadHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := validateProjectionApply(input); err != nil {
		t.Fatalf("hash is part of the claimed identity, not validation: %v", err)
	}
}

func TestMongoProjectionRepositoryWithoutDatabaseFailsClosed(t *testing.T) {
	input := validProjectionApply(t)
	repository := &MongoProjectionRepository{}
	if err := repository.Apply(context.Background(), input); err == nil {
		t.Fatal("unconfigured projection repository should fail")
	}
}
