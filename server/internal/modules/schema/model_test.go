package schema

import "testing"

func TestDefinitionValidation(t *testing.T) {
	valid := schemaFixture(t, StatusDraft)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid draft: %v", err)
	}

	tests := map[string]Definition{
		"missing tenant": func() Definition { value := valid; value.TenantID = ""; return value }(),
		"zero version":   func() Definition { value := valid; value.Version = 0; return value }(),
		"unknown status": func() Definition { value := valid; value.Status = "UNKNOWN"; return value }(),
		"missing schema": func() Definition { value := valid; value.JSONSchema = nil; return value }(),
		"incomplete published": func() Definition {
			value := valid
			value.Status = StatusPublished
			return value
		}(),
	}
	for name, definition := range tests {
		t.Run(name, func(t *testing.T) {
			if err := definition.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
