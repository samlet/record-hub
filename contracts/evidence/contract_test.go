package evidence_test

import (
	"os"
	"path/filepath"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPhase4EvidenceManifestSchemaAndFixtures(t *testing.T) {
	root := "."
	schemaFile := filepath.Join(root, "phase4-evidence-manifest-v1.schema.json")
	schemaDocument, err := os.Open(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	defer schemaDocument.Close()
	document, err := jsonschema.UnmarshalJSON(schemaDocument)
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("urn:record-hub:phase4-evidence-manifest:v1", document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("urn:record-hub:phase4-evidence-manifest:v1")
	if err != nil {
		t.Fatal(err)
	}

	valid, err := decodeFixture(filepath.Join(root, "testdata/valid/phase4-evidence-manifest-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(valid); err != nil {
		t.Fatalf("valid evidence fixture rejected: %v", err)
	}
	invalid, err := decodeFixture(filepath.Join(root, "testdata/invalid/phase4-evidence-manifest-v1-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(invalid); err == nil {
		t.Fatal("invalid evidence fixture unexpectedly accepted")
	}
}

func decodeFixture(path string) (any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return jsonschema.UnmarshalJSON(file)
}
