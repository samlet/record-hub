package schema

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestCanonicalizeJSON(t *testing.T) {
	raw := []byte("{\"z\":1.2300,\"a\":1e3,\"nested\":{\"b\":-0.00,\"a\":1e-3},\"text\":\"<中文>\"}")
	got, err := CanonicalizeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":1000,"nested":{"a":0.001,"b":0},"text":"<中文>","z":1.23}`
	if string(got) != want {
		t.Fatalf("canonical JSON = %s, want %s", got, want)
	}
}

func TestCanonicalizeJSONRejectsAmbiguousInput(t *testing.T) {
	for name, raw := range map[string][]byte{
		"duplicate key": []byte(`{"a":1,"a":2}`),
		"trailing":      []byte(`{} {}`),
		"invalid utf8":  {0xff},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalizeJSON(raw); !errors.Is(err, ErrCanonicalJSON) {
				t.Fatalf("error = %v, want ErrCanonicalJSON", err)
			}
		})
	}
}

func TestSchemaContentHashFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/schema-content-hash.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema        json.RawMessage `json:"schema"`
		SemanticTypes []string        `json:"semanticTypes"`
		ExpectedHash  string          `json:"expectedHash"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	got, err := SchemaContentHash(fixture.Schema, fixture.SemanticTypes)
	if err != nil {
		t.Fatal(err)
	}
	if got != fixture.ExpectedHash {
		t.Fatalf("schema content hash = %s, want %s", got, fixture.ExpectedHash)
	}
}
