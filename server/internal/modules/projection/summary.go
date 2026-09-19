package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/samlet/record-hub/contracts/eventenvelope"
	"github.com/samlet/record-hub/contracts/summaries"
	"github.com/samlet/record-hub/server/internal/modules/schema"
)

type SummaryKind string

const (
	SummaryApplication SummaryKind = "application"
	SummaryProject     SummaryKind = "project"
	SummaryTender      SummaryKind = "tender"
	SummaryApproval    SummaryKind = "approval"
)

var (
	ErrSummaryRejected        = errors.New("summary event rejected")
	ErrSummaryKindUnknown     = errors.New("unknown summary handler kind")
	ErrSummarySourceMismatch  = errors.New("summary source system mismatch")
	ErrSummaryTypeMismatch    = errors.New("summary event type mismatch")
	ErrSummaryKindMismatch    = errors.New("summary event kind mismatch")
	ErrSummaryVersionMismatch = errors.New("summary schema version mismatch")
)

type summarySpec struct {
	kind          SummaryKind
	sourceSystem  string
	eventType     string
	schemaID      string
	summarySchema []byte
}

// SummaryHandler validates one safe summary event. It deliberately does not
// map arbitrary payload fields into a projection: the embedded schema is an
// allowlist and rejects PII, quotations, file URLs, and future fields until a
// new contract version is registered.
type SummaryHandler struct {
	spec      summarySpec
	verifier  *eventenvelope.Verifier
	validator *schema.Validator
}

type summaryEnvelope struct {
	Kind          string          `json:"kind"`
	EventType     string          `json:"eventType"`
	SchemaVersion int64           `json:"schemaVersion"`
	SourceSystem  string          `json:"sourceSystem"`
	Payload       json.RawMessage `json:"payload"`
}

func NewSummaryHandler(kind SummaryKind) (*SummaryHandler, error) {
	spec, err := summarySpecFor(kind)
	if err != nil {
		return nil, err
	}
	validator, err := schema.CompileValidator(spec.schemaID, spec.summarySchema)
	if err != nil {
		return nil, fmt.Errorf("compile %s summary schema: %w", kind, err)
	}
	return &SummaryHandler{
		spec:      spec,
		verifier:  eventenvelope.NewVerifier(),
		validator: validator,
	}, nil
}

func (handler *SummaryHandler) Handle(ctx context.Context, raw []byte) error {
	if handler == nil || handler.verifier == nil || handler.validator == nil {
		return DeterministicError(ErrSummaryRejected, "summary handler unavailable")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return TransientError(err, "summary handling canceled")
		}
	}
	if err := handler.verifier.Validate(raw); err != nil {
		return DeterministicError(fmt.Errorf("%w: %v", ErrSummaryRejected, err), "summary envelope rejected")
	}
	var envelope summaryEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DeterministicError(fmt.Errorf("%w: %v", ErrSummaryRejected, err), "summary envelope rejected")
	}
	if envelope.Kind != "event" {
		return DeterministicError(fmt.Errorf("%w: %w", ErrSummaryRejected, ErrSummaryKindMismatch), "summary event rejected")
	}
	if envelope.SourceSystem != handler.spec.sourceSystem {
		return DeterministicError(fmt.Errorf("%w: %w", ErrSummaryRejected, ErrSummarySourceMismatch), "summary event rejected")
	}
	if envelope.EventType != handler.spec.eventType {
		return DeterministicError(fmt.Errorf("%w: %w", ErrSummaryRejected, ErrSummaryTypeMismatch), "summary event rejected")
	}
	if envelope.SchemaVersion != eventenvelope.SchemaVersion {
		return DeterministicError(fmt.Errorf("%w: %w", ErrSummaryRejected, ErrSummaryVersionMismatch), "summary event rejected")
	}
	if err := handler.validator.ValidateJSON(envelope.Payload); err != nil {
		return DeterministicError(fmt.Errorf("%w: %v", ErrSummaryRejected, err), "summary payload rejected")
	}
	return nil
}

// RegisterSummaryHandlers installs the three v1 handlers under exact
// sourceSystem/eventType/schemaVersion keys. Registry duplicates are surfaced
// to the caller so startup cannot silently replace a projection contract.
func RegisterSummaryHandlers(registry *HandlerRegistry) error {
	if registry == nil {
		return ErrInvalidHandlerKey
	}
	entries := []struct {
		kind         SummaryKind
		sourceSystem string
		eventType    string
	}{
		{SummaryApplication, "approver", "approver.application.summary-changed"},
		{SummaryProject, "fluxion", "fluxion.project.summary-changed"},
		{SummaryTender, "bids", "bids.tender.summary-changed"},
		{SummaryApproval, "approver", "approver.dispatch-approval.summary-changed"},
	}
	for _, entry := range entries {
		handler, err := NewSummaryHandler(entry.kind)
		if err != nil {
			return err
		}
		if err := registry.Register(HandlerKey{
			SourceSystem:  entry.sourceSystem,
			EventType:     entry.eventType,
			SchemaVersion: eventenvelope.SchemaVersion,
		}, handler.Handle); err != nil {
			return err
		}
	}
	return nil
}

func summarySpecFor(kind SummaryKind) (summarySpec, error) {
	contractKind := summaries.Kind(kind)
	var sourceSystem, eventType string
	switch kind {
	case SummaryApplication:
		sourceSystem, eventType = "approver", "approver.application.summary-changed"
	case SummaryProject:
		sourceSystem, eventType = "fluxion", "fluxion.project.summary-changed"
	case SummaryTender:
		sourceSystem, eventType = "bids", "bids.tender.summary-changed"
	case SummaryApproval:
		sourceSystem, eventType = "approver", "approver.dispatch-approval.summary-changed"
	default:
		return summarySpec{}, ErrSummaryKindUnknown
	}
	schemaID, err := summaries.SchemaID(contractKind)
	if err != nil {
		return summarySpec{}, err
	}
	raw, err := summaries.Schema(contractKind)
	if err != nil {
		return summarySpec{}, err
	}
	return summarySpec{
		kind:          kind,
		sourceSystem:  sourceSystem,
		eventType:     eventType,
		schemaID:      schemaID,
		summarySchema: raw,
	}, nil
}

func (handler *SummaryHandler) String() string {
	if handler == nil {
		return "<nil>"
	}
	return strings.Join([]string{string(handler.spec.kind), handler.spec.sourceSystem, handler.spec.eventType}, "/")
}
