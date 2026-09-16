package projection

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryInboxRepository struct {
	values map[string]InboxEvent
}

func (repository *memoryInboxRepository) Claim(_ context.Context, claim InboxClaim) (InboxClaimResult, error) {
	event, err := normalizeInboxClaim(claim)
	if err != nil {
		return InboxClaimResult{}, err
	}
	key := event.Consumer + ":" + event.EventID
	if existing, ok := repository.values[key]; ok {
		if existing.PayloadHash != event.PayloadHash {
			return InboxClaimResult{}, ErrInboxPayloadConflict
		}
		return InboxClaimResult{Event: existing, Duplicate: true}, nil
	}
	repository.values[key] = event
	return InboxClaimResult{Event: event}, nil
}

func (repository *memoryInboxRepository) Get(_ context.Context, eventID, consumer string) (InboxEvent, error) {
	event, ok := repository.values[consumer+":"+eventID]
	if !ok {
		return InboxEvent{}, ErrInboxNotFound
	}
	return event, nil
}

func (repository *memoryInboxRepository) MarkApplied(_ context.Context, eventID, consumer string, appliedAt time.Time) error {
	event, err := repository.Get(context.Background(), eventID, consumer)
	if err != nil {
		return err
	}
	if event.Status != InboxProcessing && event.Status != InboxApplied {
		return ErrInboxStateConflict
	}
	event.Status = InboxApplied
	event.AppliedAt = &appliedAt
	repository.values[consumer+":"+eventID] = event
	return nil
}

func (repository *memoryInboxRepository) MarkRejected(_ context.Context, eventID, consumer, safeError string, rejectedAt time.Time) error {
	event, err := repository.Get(context.Background(), eventID, consumer)
	if err != nil {
		return err
	}
	if event.Status != InboxProcessing && event.Status != InboxRejected {
		return ErrInboxStateConflict
	}
	event.Status = InboxRejected
	event.SafeError = safeError
	event.AppliedAt = &rejectedAt
	repository.values[consumer+":"+eventID] = event
	return nil
}

func TestInboxClaimIsIdempotentAndDetectsPayloadConflict(t *testing.T) {
	repository := &memoryInboxRepository{values: make(map[string]InboxEvent)}
	claim := InboxClaim{EventID: "event-1", Consumer: "record-hub-approver-v1", Subject: "events.approver.application.changed.v1", Payload: []byte(`{"eventId":"event-1","payload":{"state":"OPEN"}}`), ReceivedAt: time.Now()}
	first, err := repository.Claim(context.Background(), claim)
	if err != nil || first.Duplicate || first.Event.Status != InboxProcessing || first.Event.PayloadHash == "" {
		t.Fatalf("first claim = %#v, %v", first, err)
	}
	second, err := repository.Claim(context.Background(), claim)
	if err != nil || !second.Duplicate || second.Event.PayloadHash != first.Event.PayloadHash {
		t.Fatalf("duplicate claim = %#v, %v", second, err)
	}
	claim.Payload = []byte(`{"eventId":"event-1","payload":{"state":"CLOSED"}}`)
	if _, err := repository.Claim(context.Background(), claim); !errors.Is(err, ErrInboxPayloadConflict) {
		t.Fatalf("payload conflict = %v", err)
	}
}

func TestInboxStatusTransitionsAreIdempotentButNotReversible(t *testing.T) {
	repository := &memoryInboxRepository{values: make(map[string]InboxEvent)}
	claim := InboxClaim{EventID: "event-2", Consumer: "record-hub-fluxion-v1", Subject: "events.fluxion.project.changed.v1", Payload: []byte(`{"eventId":"event-2"}`), ReceivedAt: time.Now()}
	if _, err := repository.Claim(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkApplied(context.Background(), claim.EventID, claim.Consumer, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkApplied(context.Background(), claim.EventID, claim.Consumer, time.Now()); err != nil {
		t.Fatalf("idempotent applied transition = %v", err)
	}
	if err := repository.MarkRejected(context.Background(), claim.EventID, claim.Consumer, "should not reverse", time.Now()); !errors.Is(err, ErrInboxStateConflict) {
		t.Fatalf("reversible inbox transition = %v", err)
	}
}

func TestInboxClaimBoundsPayloadAndIdentifiers(t *testing.T) {
	repository := &memoryInboxRepository{values: make(map[string]InboxEvent)}
	claim := InboxClaim{EventID: "event 1", Consumer: "consumer", Subject: "subject", Payload: []byte("{}"), ReceivedAt: time.Now()}
	if _, err := repository.Claim(context.Background(), claim); err == nil {
		t.Fatal("whitespace event identifier should fail")
	}
	claim.EventID = "event-3"
	claim.Payload = make([]byte, 256*1024+1)
	if _, err := repository.Claim(context.Background(), claim); err == nil {
		t.Fatal("oversized inbox payload should fail")
	}
}
