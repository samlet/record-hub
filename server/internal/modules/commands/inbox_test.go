package commands

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testInboxEnvelope() Envelope {
	return Envelope{OperationID: "cmd-inbox-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", PolicyID: "project.annotate", OwnerSystem: "fluxion", ResourceType: "PROJECT", Action: "project.annotate", Purpose: "project-annotation", ResourceRef: "fluxion:PROJECT:project-1", PayloadHash: "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a", Payload: []byte(`{}`), CreatedAt: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}
}

func TestInboxProcessClaimsCommitsAndReplays(t *testing.T) {
	store := NewMemoryInboxStore()
	service := NewInboxService(store).WithClock(func() time.Time { return time.Date(2026, 9, 17, 12, 1, 0, 0, time.UTC) })
	envelope := testInboxEnvelope()
	called := 0
	execute := func(_ context.Context, _ Envelope) (ResultEnvelope, error) {
		called++
		return ResultEnvelope{EventID: "owner-event-1", Status: StatusSucceeded, ResultHash: "sha256:business", OccurredAt: time.Date(2026, 9, 17, 12, 2, 0, 0, time.UTC)}, nil
	}
	result, replayed, err := service.Process(context.Background(), envelope, execute)
	if err != nil || replayed || result.Status != StatusSucceeded || called != 1 {
		t.Fatalf("process = %#v replayed=%v called=%d err=%v", result, replayed, called, err)
	}
	replay, replayed, err := service.Process(context.Background(), envelope, execute)
	if err != nil || !replayed || replay.EventID != result.EventID || called != 1 {
		t.Fatalf("replay = %#v replayed=%v called=%d err=%v", replay, replayed, called, err)
	}
}

func TestInboxProcessFailureCommitsSafeResult(t *testing.T) {
	store := NewMemoryInboxStore()
	service := NewInboxService(store).WithClock(func() time.Time { return time.Date(2026, 9, 17, 12, 1, 0, 0, time.UTC) })
	result, _, err := service.Process(context.Background(), testInboxEnvelope(), func(context.Context, Envelope) (ResultEnvelope, error) {
		return ResultEnvelope{}, errors.New("database password leaked")
	})
	if err == nil {
		// The owner error is returned for retry, while the safe terminal result is stored.
		t.Fatalf("expected owner error, got nil")
	}
	if result.Status != StatusFailed || result.SafeError != "owner execution failed" || result.ErrorCode != "OWNER_EXECUTION_FAILED" {
		t.Fatalf("failure result = %#v", result)
	}
	replay, replayed, replayErr := service.Process(context.Background(), testInboxEnvelope(), func(context.Context, Envelope) (ResultEnvelope, error) {
		t.Fatal("replayed inbox must not execute")
		return ResultEnvelope{}, nil
	})
	if replayErr != nil || !replayed || replay.EventID != result.EventID {
		t.Fatalf("failure replay = %#v replayed=%v err=%v", replay, replayed, replayErr)
	}
}

func TestInboxClaimRejectsPayloadConflict(t *testing.T) {
	store := NewMemoryInboxStore()
	first := testInboxEnvelope()
	if _, _, err := store.Claim(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	first.PayloadHash = "sha256:other"
	if _, _, err := store.Claim(context.Background(), first); !errors.Is(err, ErrCommandInboxConflict) {
		t.Fatalf("payload conflict error = %v", err)
	}
}
