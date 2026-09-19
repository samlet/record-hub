package connector

import (
	"errors"
	"testing"
)

func testManifest() Manifest {
	return Manifest{
		Key:              Key{Connector: "fluxion.approval", Event: "dispatch.result", SchemaVersion: 1},
		OwnerSystem:      "approver",
		SDKVersion:       "1.2.0",
		CompatibilityMin: "1.0.0",
		CompatibilityMax: "1.9.9",
		ContractHash:     "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		AllowedFields:    []string{"decision", "generation"},
	}
}

func TestRegistryRequiresExactEnabledVersion(t *testing.T) {
	registry := NewRegistry()
	manifest := testManifest()
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(manifest.Key, "1.2.0"); !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("draft connector resolve error = %v", err)
	}
	enabled, err := registry.Transition(manifest.Key, StatusEnabled, 1)
	if err != nil || enabled.Revision != 2 {
		t.Fatalf("enable = %#v, err=%v", enabled, err)
	}
	if _, err := registry.Resolve(manifest.Key, "1.10.0"); !errors.Is(err, ErrSDKIncompatible) {
		t.Fatalf("out-of-window SDK error = %v", err)
	}
	resolved, err := registry.Resolve(manifest.Key, "1.2.0")
	if err != nil || resolved.Key != manifest.Key {
		t.Fatalf("resolve = %#v, err=%v", resolved, err)
	}
	if _, err := registry.Resolve(Key{Connector: "unknown", Event: "dispatch.result", SchemaVersion: 1}, "1.2.0"); !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("unknown connector error = %v", err)
	}
}

func TestRegistryRejectsDuplicateUnsafeAndRevisionConflict(t *testing.T) {
	registry := NewRegistry()
	manifest := testManifest()
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(manifest); !errors.Is(err, ErrConnectorExists) {
		t.Fatalf("duplicate error = %v", err)
	}
	unsafe := testManifest()
	unsafe.Key.Event = "dispatch.*"
	if err := NewRegistry().Register(unsafe); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("unsafe event error = %v", err)
	}
	if _, err := registry.Transition(manifest.Key, StatusEnabled, 99); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale transition error = %v", err)
	}
}

func TestRegistryDisableAndSnapshotAreDeterministic(t *testing.T) {
	registry := NewRegistry()
	first := testManifest()
	second := testManifest()
	second.Key.Connector = "bids.approval"
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Transition(first.Key, StatusEnabled, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Transition(first.Key, StatusDisabled, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(first.Key, "1.2.0"); !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("disabled resolve error = %v", err)
	}
	snapshot := registry.Snapshot()
	if len(snapshot) != 2 || snapshot[0].Key.Connector != "bids.approval" || snapshot[1].Key.Connector != "fluxion.approval" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
