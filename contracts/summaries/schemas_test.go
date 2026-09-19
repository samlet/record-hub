package summaries

import (
	"encoding/json"
	"testing"
)

func TestSummaryContractAssetsAreAvailableAndSafe(t *testing.T) {
	for _, kind := range []Kind{Application, Project, Tender, Approval} {
		t.Run(string(kind), func(t *testing.T) {
			rawSchema, err := Schema(kind)
			if err != nil {
				t.Fatal(err)
			}
			rawFixture, err := Fixture(kind)
			if err != nil {
				t.Fatal(err)
			}
			var schemaDocument map[string]any
			if err := json.Unmarshal(rawSchema, &schemaDocument); err != nil {
				t.Fatalf("schema JSON: %v", err)
			}
			var fixture map[string]any
			if err := json.Unmarshal(rawFixture, &fixture); err != nil {
				t.Fatalf("fixture JSON: %v", err)
			}
			if schemaDocument["$id"] != mustSchemaID(t, kind) || schemaDocument["additionalProperties"] != false {
				t.Fatalf("schema metadata is not canonical: %#v", schemaDocument)
			}
			for _, forbidden := range []string{"contactEmail", "phone", "bidAmount", "quotation", "fileUrl", "documentUrl"} {
				if _, present := fixture[forbidden]; present {
					t.Fatalf("fixture contains forbidden field %q", forbidden)
				}
			}
		})
	}
}

func mustSchemaID(t *testing.T, kind Kind) string {
	t.Helper()
	id, err := SchemaID(kind)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
