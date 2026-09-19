package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/samlet/record-hub/contracts/summaries"
	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"github.com/samlet/record-hub/server/internal/modules/records"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// SummaryProjector is the production message adapter for the three v1
// summary contracts. Validation remains owned by HandlerRegistry; this
// adapter only converts an allowlisted payload into the Record Hub envelope
// and atomically claims/applies it through Inbox and Projection repositories.
// It never copies arbitrary envelope metadata into the record.
type SummaryProjector struct {
	registry          *HandlerRegistry
	mappings          *MappingGenerationRegistry
	inbox             InboxRepository
	projection        ProjectionRepository
	archive           ProjectionEventArchiveWriter
	association       AssociationWriter
	workspaceID       string
	workspaceByTenant map[string]string
	projectorKey      identity.IdentityKey
	now               func() time.Time
}

type summaryEventEnvelope struct {
	EventID          string            `json:"eventId"`
	Kind             string            `json:"kind"`
	EventType        string            `json:"eventType"`
	SchemaVersion    int64             `json:"schemaVersion"`
	SourceSystem     string            `json:"sourceSystem"`
	TenantID         string            `json:"tenantId"`
	AggregateType    string            `json:"aggregateType"`
	AggregateID      string            `json:"aggregateId"`
	AggregateVersion int64             `json:"aggregateVersion"`
	OccurredAt       time.Time         `json:"occurredAt"`
	Payload          json.RawMessage   `json:"payload"`
	Metadata         map[string]string `json:"metadata"`
}

func NewSummaryProjector(registry *HandlerRegistry, inbox InboxRepository, repository ProjectionRepository, workspaceID string) (*SummaryProjector, error) {
	if registry == nil || inbox == nil || repository == nil {
		return nil, errors.New("summary projector requires registry, inbox, and projection repository")
	}
	return &SummaryProjector{
		registry:     registry,
		inbox:        inbox,
		projection:   repository,
		workspaceID:  strings.TrimSpace(workspaceID),
		projectorKey: identity.IdentityKey{Issuer: "record-hub", Subject: "projector"},
		now:          time.Now,
	}, nil
}

// WithWorkspaceMappings supplies an explicit tenant-to-workspace allowlist
// for legacy envelopes. The map is copied so caller mutations cannot change
// routing after the projector has started.
func (projector *SummaryProjector) WithWorkspaceMappings(mappings map[string]string) *SummaryProjector {
	if projector == nil {
		return projector
	}
	if len(mappings) == 0 {
		projector.workspaceByTenant = nil
		return projector
	}
	projector.workspaceByTenant = make(map[string]string, len(mappings))
	for tenantID, workspaceID := range mappings {
		projector.workspaceByTenant[strings.TrimSpace(tenantID)] = strings.TrimSpace(workspaceID)
	}
	return projector
}

// WithMappingGenerations enables catalog-backed mappings. Built-in summary
// handlers remain an explicit exact-key path; a catalog entry can neither
// shadow them nor cause a fuzzy fallback.
func (projector *SummaryProjector) WithMappingGenerations(registry *MappingGenerationRegistry) *SummaryProjector {
	if projector != nil {
		projector.mappings = registry
	}
	return projector
}

// WithEventArchive enables durable raw-event retention for future rebuilds.
// It is optional so deterministic projector tests and embedded adapters can
// keep using the small Inbox/Projection interfaces without Mongo.
func (projector *SummaryProjector) WithEventArchive(archive ProjectionEventArchiveWriter) *SummaryProjector {
	if projector != nil {
		projector.archive = archive
	}
	return projector
}

// WithAssociationWriter enables the typed Project↔Application read model.
// The writer is fed only the allowlisted approval summary payload; it never
// receives arbitrary source metadata or the full workflow input.
func (projector *SummaryProjector) WithAssociationWriter(writer AssociationWriter) *SummaryProjector {
	if projector != nil {
		projector.association = writer
	}
	return projector
}

// HandleMessage is suitable for PullRunner. Deterministic contract or scope
// failures are classified for bounded retry/DLQ; Mongo/NATS errors remain
// transient and are NAKed by the runner.
func (projector *SummaryProjector) HandleMessage(ctx context.Context, message jetstream.Msg) error {
	return projector.Handle(ctx, message.Subject(), message.Data())
}

// Handle processes one subject/payload pair. It is exported for deterministic
// contract tests and for transports that adapt JetStream messages elsewhere.
func (projector *SummaryProjector) Handle(ctx context.Context, subject string, raw []byte) error {
	if projector == nil || projector.registry == nil || projector.inbox == nil || projector.projection == nil || projector.now == nil {
		return DeterministicError(errors.New("summary projector is not configured"), "summary projector unavailable")
	}
	var envelope summaryEventEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DeterministicError(err, "summary envelope rejected")
	}
	if envelope.AggregateVersion < 1 {
		return DeterministicError(errors.New("aggregateVersion must be positive for projection"), "summary aggregate version rejected")
	}
	if strings.TrimSpace(envelope.EventID) == "" || strings.TrimSpace(envelope.TenantID) == "" || strings.TrimSpace(envelope.SourceSystem) == "" || strings.TrimSpace(envelope.AggregateType) == "" || strings.TrimSpace(envelope.AggregateID) == "" {
		return DeterministicError(errors.New("summary envelope identity is incomplete"), "summary envelope identity rejected")
	}

	key := HandlerKey{SourceSystem: envelope.SourceSystem, EventType: envelope.EventType, SchemaVersion: envelope.SchemaVersion}
	workspaceID := ""
	tableID := ""
	schemaID := ""
	schemaVersion := int64(1)
	recordVersion := envelope.AggregateVersion
	now := projector.now().UTC()
	payloadHash := sha256.Sum256(raw)
	archived := false
	var data bson.Raw
	var runtimeMapping *RuntimeMapping
	if projector.mappings != nil {
		mapping, resolveErr := projector.mappings.Resolve(envelope.TenantID, envelope.SourceSystem, envelope.EventType, envelope.SchemaVersion, envelope.Metadata)
		switch {
		case resolveErr == nil:
			runtimeMapping = &mapping
		case errors.Is(resolveErr, ErrRuntimeMappingNotFound):
		case errors.Is(resolveErr, ErrRuntimeMappingAmbiguous):
			return DeterministicError(resolveErr, "projection mapping scope is ambiguous")
		default:
			return DeterministicError(resolveErr, "projection mapping lookup rejected")
		}
	}
	if runtimeMapping != nil {
		workspaceID = runtimeMapping.Key.WorkspaceID
		tableID = runtimeMapping.TargetTableID
		schemaID = runtimeMapping.TargetSchemaID
		schemaVersion = runtimeMapping.TargetSchemaVersion
		if projector.archive != nil {
			consumer := consumerForSource(envelope.SourceSystem)
			if consumer != "" {
				if err := projector.archive.Archive(ctx, ProjectionEventArchiveEntry{
					EventID: envelope.EventID, Consumer: consumer, Subject: subject, TenantID: envelope.TenantID, WorkspaceID: workspaceID,
					SourceSystem: envelope.SourceSystem, EventType: envelope.EventType, SchemaVersion: envelope.SchemaVersion,
					AggregateType: envelope.AggregateType, AggregateID: envelope.AggregateID, AggregateVersion: envelope.AggregateVersion,
					OccurredAt: envelope.OccurredAt.UTC(), ReceivedAt: now, PayloadHash: "sha256:" + hex.EncodeToString(payloadHash[:]), Raw: append([]byte(nil), raw...),
				}); err != nil {
					return TransientError(err, "archive projection event failed")
				}
				archived = true
			}
		}
		mapped, err := runtimeMapping.Map(raw)
		if err != nil {
			return DeterministicError(err, "projection mapping rejected")
		}
		data = mapped
	} else {
		if err := projector.registry.Dispatch(ctx, key, raw); err != nil {
			if errors.Is(err, ErrHandlerNotFound) {
				return DeterministicError(err, "projection mapping not found")
			}
			return err
		}
		workspaceID = strings.TrimSpace(envelope.Metadata["workspaceId"])
		if workspaceID == "" {
			workspaceID = strings.TrimSpace(projector.workspaceByTenant[envelope.TenantID])
		}
		if workspaceID == "" {
			workspaceID = projector.workspaceID
		}
		if workspaceID == "" {
			return DeterministicError(errors.New("workspaceId is required for projection"), "summary workspace scope is missing")
		}
		if err := bson.UnmarshalExtJSON(envelope.Payload, false, &data); err != nil {
			return DeterministicError(err, "summary payload encoding rejected")
		}
		var payload struct {
			Version int64 `json:"version"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.Version < 1 {
			return DeterministicError(errors.New("summary payload version is invalid"), "summary payload version rejected")
		}
		recordVersion = payload.Version
		tableID = projectionTableIDForEvent(envelope.SourceSystem, envelope.EventType)
		schemaID = summarySchemaIDForEvent(envelope.SourceSystem, envelope.EventType)
	}
	consumer := consumerForSource(envelope.SourceSystem)
	if consumer == "" {
		return DeterministicError(errors.New("summary source is not registered"), "summary source rejected")
	}
	if projector.archive != nil && !archived {
		if err := projector.archive.Archive(ctx, ProjectionEventArchiveEntry{
			EventID: envelope.EventID, Consumer: consumer, Subject: subject, TenantID: envelope.TenantID, WorkspaceID: workspaceID,
			SourceSystem: envelope.SourceSystem, EventType: envelope.EventType, SchemaVersion: envelope.SchemaVersion,
			AggregateType: envelope.AggregateType, AggregateID: envelope.AggregateID, AggregateVersion: envelope.AggregateVersion,
			OccurredAt: envelope.OccurredAt.UTC(), ReceivedAt: now, PayloadHash: "sha256:" + hex.EncodeToString(payloadHash[:]), Raw: append([]byte(nil), raw...),
		}); err != nil {
			return TransientError(err, "archive projection event failed")
		}
	}
	claim, err := projector.inbox.Claim(ctx, InboxClaim{
		EventID: envelope.EventID, Consumer: consumer, Subject: subject, TenantID: envelope.TenantID, WorkspaceID: workspaceID,
		Payload: raw, ReceivedAt: now,
	})
	if err != nil {
		return TransientError(err, "claim projection inbox failed")
	}
	if claim.Duplicate && claim.Event.Status == InboxApplied {
		return nil
	}
	if projector.association != nil && envelope.EventType == "approver.dispatch-approval.summary-changed" {
		associationEvent, associationErr := associationEventFromSummary(envelope, envelope.Payload, now)
		if associationErr != nil {
			return DeterministicError(associationErr, "approval association rejected")
		}
		if associationEvent.WorkspaceID == "" {
			associationEvent.WorkspaceID = workspaceID
		}
		if err := projector.association.Apply(ctx, associationEvent); err != nil {
			return err
		}
	}
	recordID := projectionRecordID(envelope.TenantID, workspaceID, envelope.SourceSystem, envelope.AggregateType, envelope.AggregateID)
	if runtimeMapping != nil {
		recordID = projectionRecordIDForTable(envelope.TenantID, workspaceID, tableID, envelope.SourceSystem, envelope.AggregateType, envelope.AggregateID)
	}
	record := records.Record{
		ID: recordID, TenantID: envelope.TenantID, WorkspaceID: workspaceID,
		TableID: tableID, SchemaID: schemaID, SchemaVersion: schemaVersion,
		Source:        &records.RecordSource{System: envelope.SourceSystem, Type: strings.ToLower(envelope.AggregateType), ID: envelope.AggregateID, Version: envelope.AggregateVersion},
		RecordVersion: recordVersion, Tags: []string{"projection", envelope.SourceSystem}, Data: data,
		Projection: &records.ProjectionState{LastEventID: envelope.EventID, SyncedAt: now, Status: string(CheckpointCurrent)},
		CreatedBy:  projector.projectorKey, UpdatedBy: projector.projectorKey, CreatedAt: envelope.OccurredAt.UTC(), UpdatedAt: now,
	}
	checkpoint := ProjectionCheckpoint{TenantID: envelope.TenantID, WorkspaceID: workspaceID, Consumer: consumer, SourceSystem: envelope.SourceSystem, AggregateType: envelope.AggregateType, AggregateID: envelope.AggregateID, SourceVersion: envelope.AggregateVersion, LastEventID: envelope.EventID, SyncedAt: now, Status: CheckpointCurrent}
	return projector.projection.Apply(ctx, ProjectionApply{InboxEvent: claim.Event, Record: record, Checkpoint: checkpoint, Audit: audit.Entry{TenantID: envelope.TenantID, WorkspaceID: workspaceID, Action: "projection.apply", Actor: projector.projectorKey, ResourceType: "Record", ResourceID: record.ID, ResourceVersion: recordVersion, AfterHash: "sha256:" + hex.EncodeToString(payloadHash[:]), CreatedAt: now}})
}

func consumerForSource(source string) string {
	switch source {
	case "approver":
		return ApproverProjectionConsumer
	case "fluxion":
		return FluxionProjectionConsumer
	case "bids":
		return BidsProjectionConsumer
	default:
		return ""
	}
}

func projectionTableID(source string) string { return "projection-" + source + "-summary" }

func projectionTableIDForEvent(source, eventType string) string {
	if source == "approver" && eventType == "approver.dispatch-approval.summary-changed" {
		return "projection-approver-approval-summary"
	}
	return projectionTableID(source)
}

func projectionRecordID(tenantID, workspaceID, sourceSystem, aggregateType, aggregateID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{tenantID, workspaceID, sourceSystem, aggregateType, aggregateID}, "\x00")))
	return "projection-" + hex.EncodeToString(digest[:])[:40]
}

func projectionRecordIDForTable(tenantID, workspaceID, tableID, sourceSystem, aggregateType, aggregateID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{tenantID, workspaceID, tableID, sourceSystem, aggregateType, aggregateID}, "\x00")))
	return "projection-" + hex.EncodeToString(digest[:])[:40]
}

func summarySchemaID(source string) string {
	switch source {
	case "approver":
		return summaries.ApplicationSchemaID
	case "fluxion":
		return summaries.ProjectSchemaID
	case "bids":
		return summaries.TenderSchemaID
	default:
		return "urn:record-hub:summary:unknown:v1"
	}
}

func summarySchemaIDForEvent(source, eventType string) string {
	if source == "approver" && eventType == "approver.dispatch-approval.summary-changed" {
		return summaries.ApprovalSchemaID
	}
	return summarySchemaID(source)
}

func (envelope summaryEventEnvelope) String() string {
	return fmt.Sprintf("%s/%s/%s", envelope.SourceSystem, envelope.EventType, envelope.EventID)
}
