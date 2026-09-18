package commands_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type manifest struct {
	Version        int    `json:"version"`
	ContractFamily string `json:"contractFamily"`
	Contracts      []struct {
		Kind                string   `json:"kind"`
		SchemaID            string   `json:"schemaId"`
		SchemaFile          string   `json:"schemaFile"`
		ValidFixtureFile    string   `json:"validFixtureFile"`
		InvalidFixtureFiles []string `json:"invalidFixtureFiles"`
		SchemaContentHash   string   `json:"schemaContentHash"`
	} `json:"contracts"`
	ErrorCodesFile        string `json:"errorCodesFile"`
	ErrorCodesContentHash string `json:"errorCodesContentHash"`
}

type identityKey struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

type commandEnvelope struct {
	OperationID     string                     `json:"operationId"`
	TenantID        string                     `json:"tenantId"`
	WorkspaceID     string                     `json:"workspaceId"`
	PolicyID        string                     `json:"policyId"`
	OwnerSystem     string                     `json:"ownerSystem"`
	ResourceType    string                     `json:"resourceType"`
	Action          string                     `json:"action"`
	Purpose         string                     `json:"purpose"`
	ResourceRef     string                     `json:"resourceRef"`
	ExpectedVersion *int64                     `json:"expectedVersion,omitempty"`
	PayloadHash     string                     `json:"payloadHash"`
	Payload         map[string]json.RawMessage `json:"payload"`
	RequestedBy     identityKey                `json:"requestedBy"`
	CreatedAt       string                     `json:"createdAt"`
}

type resultEnvelope struct {
	EventID       string `json:"eventId"`
	OperationID   string `json:"operationId"`
	TenantID      string `json:"tenantId"`
	WorkspaceID   string `json:"workspaceId"`
	OwnerSystem   string `json:"ownerSystem"`
	Action        string `json:"action"`
	Status        string `json:"status"`
	ResultHash    string `json:"resultHash,omitempty"`
	ErrorCode     string `json:"errorCode,omitempty"`
	SafeError     string `json:"safeError,omitempty"`
	ResultVersion *int64 `json:"resultVersion,omitempty"`
	OccurredAt    string `json:"occurredAt"`
}

func TestCommandResultManifestAndFixtures(t *testing.T) {
	root := "."
	rawManifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || m.ContractFamily != "record-hub-command-result" || len(m.Contracts) != 2 {
		t.Fatalf("unexpected manifest: %#v", m)
	}
	for _, contract := range m.Contracts {
		t.Run(contract.Kind, func(t *testing.T) {
			schema, err := os.ReadFile(filepath.Join(root, contract.SchemaFile))
			if err != nil {
				t.Fatal(err)
			}
			if got := sha256Hex(schema); got != contract.SchemaContentHash {
				t.Fatalf("schema hash mismatch: got %s want %s", got, contract.SchemaContentHash)
			}
			var schemaDoc map[string]any
			if err := json.Unmarshal(schema, &schemaDoc); err != nil {
				t.Fatal(err)
			}
			if schemaDoc["$id"] != contract.SchemaID || schemaDoc["additionalProperties"] != false {
				t.Fatalf("schema metadata is not canonical: %#v", schemaDoc)
			}
			valid, err := os.ReadFile(filepath.Join(root, contract.ValidFixtureFile))
			if err != nil {
				t.Fatal(err)
			}
			if contract.Kind == "command-envelope" {
				var envelope commandEnvelope
				decodeStrict(t, valid, &envelope)
				if envelope.OperationID == "" || envelope.PayloadHash == "" || len(envelope.Payload) == 0 || envelope.RequestedBy.Issuer == "" || envelope.RequestedBy.Subject == "" {
					t.Fatalf("valid command fixture is incomplete: %#v", envelope)
				}
				payload, err := json.Marshal(envelope.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if got := sha256Hex(payload); got != envelope.PayloadHash {
					t.Fatalf("payload hash mismatch: got %s want %s", got, envelope.PayloadHash)
				}
			} else {
				var result resultEnvelope
				decodeStrict(t, valid, &result)
				if result.EventID == "" || result.OperationID == "" || result.OccurredAt == "" || !validResultStatus(result.Status) {
					t.Fatalf("valid result fixture is incomplete: %#v", result)
				}
			}
			for _, invalidFile := range contract.InvalidFixtureFiles {
				invalid, err := os.ReadFile(filepath.Join(root, invalidFile))
				if err != nil {
					t.Fatal(err)
				}
				if contract.Kind == "command-envelope" {
					var envelope commandEnvelope
					if err := strictDecode(invalid, &envelope); err == nil {
						t.Fatalf("invalid command fixture unexpectedly decoded")
					}
				} else {
					var result resultEnvelope
					if err := strictDecode(invalid, &result); err != nil {
						t.Fatal(err)
					}
					if validResultStatus(result.Status) {
						t.Fatalf("invalid result status unexpectedly accepted: %s", result.Status)
					}
				}
			}
		})
	}
	codes, err := os.ReadFile(filepath.Join(root, m.ErrorCodesFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256Hex(codes); got != m.ErrorCodesContentHash {
		t.Fatalf("error code hash mismatch: got %s want %s", got, m.ErrorCodesContentHash)
	}
	var codeDoc struct {
		Version int `json:"version"`
		Codes   []struct {
			Code      string `json:"code"`
			Class     string `json:"class"`
			Retryable bool   `json:"retryable"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(codes, &codeDoc); err != nil || codeDoc.Version != 1 || len(codeDoc.Codes) < 8 {
		t.Fatalf("error code catalog is incomplete: %v %#v", err, codeDoc)
	}
}

func decodeStrict(t *testing.T, raw []byte, target any) {
	t.Helper()
	if err := strictDecode(raw, target); err != nil {
		t.Fatal(err)
	}
}

func strictDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validResultStatus(status string) bool {
	switch status {
	case "SUCCEEDED", "REJECTED", "FAILED", "EXPIRED":
		return true
	default:
		return false
	}
}
