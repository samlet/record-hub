package schema

import (
	"errors"
	"testing"
)

const sixFieldSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:record-hub:test:six-fields",
  "type": "object",
  "additionalProperties": false,
  "required": ["text", "number", "enabled", "occurredAt", "state", "reference"],
  "properties": {
    "text": {"type": "string", "minLength": 1},
    "number": {"type": "number"},
    "enabled": {"type": "boolean"},
    "occurredAt": {"type": "string", "format": "date-time"},
    "state": {"type": "string", "enum": ["OPEN", "CLOSED"]},
    "reference": {
      "type": "object",
      "additionalProperties": false,
      "required": ["system", "type", "id"],
      "properties": {
        "system": {"type": "string", "enum": ["approver", "fluxion", "bids", "record-hub"]},
        "type": {"type": "string", "pattern": "^[A-Z][A-Z0-9_]{0,63}$"},
        "id": {"type": "string", "format": "uuid"}
      }
    }
  }
}`

func TestValidatorAcceptsSixMVPFieldTypes(t *testing.T) {
	validator, err := CompileValidator("urn:record-hub:test:six-fields", []byte(sixFieldSchema))
	if err != nil {
		t.Fatal(err)
	}
	valid := `{
      "text": "Tender review",
      "number": 12.50,
      "enabled": true,
      "occurredAt": "2026-09-16T12:30:00Z",
      "state": "OPEN",
      "reference": {"system":"bids","type":"TENDER","id":"ec7440e2-26ae-4f72-abf7-2f43afadd7b1"}
    }`
	if err := validator.ValidateJSON([]byte(valid)); err != nil {
		t.Fatalf("valid six-field document: %v", err)
	}
}

func TestValidatorRejectsEachInvalidMVPFieldType(t *testing.T) {
	validator, err := CompileValidator("urn:record-hub:test:six-fields", []byte(sixFieldSchema))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"text":      `{"text":1,"number":12.5,"enabled":true,"occurredAt":"2026-09-16T12:30:00Z","state":"OPEN","reference":{"system":"bids","type":"TENDER","id":"ec7440e2-26ae-4f72-abf7-2f43afadd7b1"}}`,
		"number":    `{"text":"x","number":"12.5","enabled":true,"occurredAt":"2026-09-16T12:30:00Z","state":"OPEN","reference":{"system":"bids","type":"TENDER","id":"ec7440e2-26ae-4f72-abf7-2f43afadd7b1"}}`,
		"boolean":   `{"text":"x","number":12.5,"enabled":"yes","occurredAt":"2026-09-16T12:30:00Z","state":"OPEN","reference":{"system":"bids","type":"TENDER","id":"ec7440e2-26ae-4f72-abf7-2f43afadd7b1"}}`,
		"date-time": `{"text":"x","number":12.5,"enabled":true,"occurredAt":"16/09/2026","state":"OPEN","reference":{"system":"bids","type":"TENDER","id":"ec7440e2-26ae-4f72-abf7-2f43afadd7b1"}}`,
		"enum":      `{"text":"x","number":12.5,"enabled":true,"occurredAt":"2026-09-16T12:30:00Z","state":"UNKNOWN","reference":{"system":"bids","type":"TENDER","id":"ec7440e2-26ae-4f72-abf7-2f43afadd7b1"}}`,
		"reference": `{"text":"x","number":12.5,"enabled":true,"occurredAt":"2026-09-16T12:30:00Z","state":"OPEN","reference":{"system":"bids","type":"tender","id":"not-a-uuid"}}`,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validator.ValidateJSON([]byte(document)); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("error = %v, want ErrInvalidDocument", err)
			}
		})
	}
}

func TestValidatorRejectsInvalidSchemaAndJSON(t *testing.T) {
	invalidSchemas := map[string]string{
		"wrong draft": `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`,
		"bad keyword": `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"not-a-type"}`,
		"trailing":    `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"} {}`,
	}
	for name, rawSchema := range invalidSchemas {
		t.Run(name, func(t *testing.T) {
			if _, err := CompileValidator("urn:record-hub:test:invalid", []byte(rawSchema)); !errors.Is(err, ErrInvalidSchema) {
				t.Fatalf("error = %v, want ErrInvalidSchema", err)
			}
		})
	}

	validator, err := CompileValidator("urn:record-hub:test:six-fields", []byte(sixFieldSchema))
	if err != nil {
		t.Fatal(err)
	}
	for name, document := range map[string][]byte{
		"invalid UTF-8": {0xff, 0xfe},
		"trailing JSON": []byte(`{} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validator.ValidateJSON(document); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("error = %v, want ErrInvalidDocument", err)
			}
		})
	}
}
