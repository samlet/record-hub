package projection

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestHandlerRegistryRequiresExactUniqueKey(t *testing.T) {
	registry := NewHandlerRegistry()
	key := HandlerKey{SourceSystem: "approver", EventType: "application.summary-changed.v1", SchemaVersion: 1}
	var calls atomic.Int64
	if err := registry.Register(key, func(context.Context, []byte) error { calls.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(key, func(context.Context, []byte) error { return nil }); !errors.Is(err, ErrHandlerExists) {
		t.Fatalf("duplicate handler registration = %v", err)
	}
	if err := registry.Dispatch(context.Background(), key, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d", calls.Load())
	}
	if _, err := registry.Lookup(HandlerKey{SourceSystem: "approver", EventType: "application.summary-changed.v1", SchemaVersion: 2}); !errors.Is(err, ErrHandlerNotFound) {
		t.Fatalf("different schema version lookup = %v", err)
	}
	if err := registry.Dispatch(context.Background(), HandlerKey{SourceSystem: "approver", EventType: "application.unknown.v1", SchemaVersion: 1}, nil); !errors.Is(err, ErrHandlerNotFound) {
		t.Fatalf("unknown event dispatch = %v", err)
	}
}

func TestHandlerRegistryRejectsUnsafeKeysAndNilHandlers(t *testing.T) {
	registry := NewHandlerRegistry()
	if err := registry.Register(HandlerKey{SourceSystem: "approver", EventType: "$where", SchemaVersion: 1}, func(context.Context, []byte) error { return nil }); !errors.Is(err, ErrInvalidHandlerKey) {
		t.Fatalf("unsafe event type registration = %v", err)
	}
	if err := registry.Register(HandlerKey{SourceSystem: "approver", EventType: "application.changed.v1", SchemaVersion: 1}, nil); !errors.Is(err, ErrInvalidHandlerKey) {
		t.Fatalf("nil handler registration = %v", err)
	}
	if _, err := registry.Lookup(HandlerKey{SourceSystem: "approver", EventType: "application.changed.v1", SchemaVersion: 0}); !errors.Is(err, ErrInvalidHandlerKey) {
		t.Fatalf("zero schema version lookup = %v", err)
	}
}

func TestHandlerRegistrySupportsConcurrentDispatch(t *testing.T) {
	registry := NewHandlerRegistry()
	key := HandlerKey{SourceSystem: "fluxion", EventType: "project.changed.v1", SchemaVersion: 1}
	var calls atomic.Int64
	if err := registry.Register(key, func(context.Context, []byte) error { calls.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			if err := registry.Dispatch(context.Background(), key, nil); err != nil {
				t.Errorf("concurrent dispatch = %v", err)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 16; i++ {
		<-done
	}
	if calls.Load() != 16 {
		t.Fatalf("concurrent handler calls = %d", calls.Load())
	}
}
