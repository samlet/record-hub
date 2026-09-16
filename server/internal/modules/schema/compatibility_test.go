package schema

import (
	"fmt"
	"testing"
)

func TestCompatibilityAllowsOptionalAdditiveChange(t *testing.T) {
	published := compatibilitySchema(`
      "required": ["name"],
      "properties": {"name":{"type":"string"}}
    `)
	candidate := compatibilitySchema(`
      "required": ["name"],
      "properties": {
        "name":{"type":"string"},
        "description":{"type":"string"},
        "priority":{"type":"string","enum":["LOW","HIGH"]}
      }
    `)
	report, err := CheckBackwardCompatibility([]byte(published), []byte(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compatible || len(report.Changes) != 0 {
		t.Fatalf("optional additive change should be compatible: %#v", report)
	}
}

func TestCompatibilityRejectsBreakingChanges(t *testing.T) {
	published := compatibilitySchema(`
      "required": ["name"],
      "properties": {
        "name":{"type":"string"},
        "count":{"type":"integer"},
        "state":{"type":"string","enum":["OPEN","CLOSED"]},
        "metadata":{"type":"object","properties":{"label":{"type":"string"}}}
      }
    `)
	tests := map[string]string{
		"property deletion or rename": compatibilitySchema(`
          "required": ["displayName"],
          "properties": {
            "displayName":{"type":"string"},
            "count":{"type":"integer"},
            "state":{"type":"string","enum":["OPEN","CLOSED"]},
            "metadata":{"type":"object","properties":{"label":{"type":"string"}}}
          }
        `),
		"type narrowed": compatibilitySchema(`
          "required": ["name"],
          "properties": {
            "name":{"type":"string"},
            "count":{"type":"string"},
            "state":{"type":"string","enum":["OPEN","CLOSED"]},
            "metadata":{"type":"object","properties":{"label":{"type":"string"}}}
          }
        `),
		"enum narrowed": compatibilitySchema(`
          "required": ["name"],
          "properties": {
            "name":{"type":"string"},
            "count":{"type":"integer"},
            "state":{"type":"string","enum":["OPEN"]},
            "metadata":{"type":"object","properties":{"label":{"type":"string"}}}
          }
        `),
		"required added": compatibilitySchema(`
          "required": ["name","count"],
          "properties": {
            "name":{"type":"string"},
            "count":{"type":"integer"},
            "state":{"type":"string","enum":["OPEN","CLOSED"]},
            "metadata":{"type":"object","properties":{"label":{"type":"string"}}}
          }
        `),
		"nested deletion": compatibilitySchema(`
          "required": ["name"],
          "properties": {
            "name":{"type":"string"},
            "count":{"type":"integer"},
            "state":{"type":"string","enum":["OPEN","CLOSED"]},
            "metadata":{"type":"object","properties":{}}
          }
        `),
		"constraint changed": compatibilitySchema(`
          "required": ["name"],
          "properties": {
            "name":{"type":"string","maxLength":10},
            "count":{"type":"integer"},
            "state":{"type":"string","enum":["OPEN","CLOSED"]},
            "metadata":{"type":"object","properties":{"label":{"type":"string"}}}
          }
        `),
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			report, err := CheckBackwardCompatibility([]byte(published), []byte(candidate))
			if err != nil {
				t.Fatal(err)
			}
			if report.Compatible || len(report.Changes) == 0 {
				t.Fatalf("breaking change accepted: %#v", report)
			}
		})
	}
}

func TestCompatibilityAllowsWidening(t *testing.T) {
	published := compatibilitySchema(`
      "required": ["count","state"],
      "properties": {
        "count":{"type":"integer"},
        "state":{"type":"string","enum":["OPEN"]}
      }
    `)
	candidate := compatibilitySchema(`
      "properties": {
        "count":{"type":"number"},
        "state":{"type":"string","enum":["OPEN","CLOSED"]}
      }
    `)
	report, err := CheckBackwardCompatibility([]byte(published), []byte(candidate))
	if err != nil || !report.Compatible {
		t.Fatalf("widening should be compatible, report=%#v err=%v", report, err)
	}
}

func compatibilitySchema(body string) string {
	return fmt.Sprintf(`{
      "$schema":"https://json-schema.org/draft/2020-12/schema",
      "type":"object",
      "additionalProperties":false,
      %s
    }`, body)
}
