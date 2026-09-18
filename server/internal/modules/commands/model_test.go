package commands

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

func TestEnvelopeWirePayloadIsJSONObjectAndStrict(t *testing.T) {
	envelope := Envelope{
		OperationID: "cmd-wire-test", TenantID: "tenant-1", WorkspaceID: "workspace-1", PolicyID: "project.annotate",
		OwnerSystem: "fluxion", ResourceType: "PROJECT", Action: "project.annotate", Purpose: "project-annotation",
		ResourceRef: "fluxion:PROJECT:project-1", PayloadHash: "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
		Payload: []byte(`{"note":"safe"}`), RequestedBy: identity.IdentityKey{Issuer: "issuer", Subject: "subject"},
		CreatedAt: time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(wire["payload"], &payload); err != nil || payload["note"] != "safe" {
		t.Fatalf("payload is not a JSON object: %s", wire["payload"])
	}
	var decoded Envelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Payload, envelope.Payload) {
		t.Fatalf("payload changed across wire round trip: %s", decoded.Payload)
	}
	if err := json.Unmarshal(append(raw[:len(raw)-1], []byte(`,"unexpected":true}`)...), &decoded); err == nil {
		t.Fatal("unknown command field was accepted")
	}
}
