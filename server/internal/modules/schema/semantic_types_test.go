package schema

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeSemanticTypes(t *testing.T) {
	got, err := NormalizeSemanticTypes([]string{
		" http://www.schema.org/Action/ ",
		"https://schema.org/Action",
		"urn:record-hub:bids:TenderApproval",
		"https://EXAMPLE.com:443/types/Tender",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://example.com/types/Tender",
		"https://schema.org/Action",
		"urn:record-hub:bids:TenderApproval",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeSemanticTypes() = %#v, want %#v", got, want)
	}
}

func TestNormalizeSemanticTypesRejectsUnsafeOrAmbiguousURI(t *testing.T) {
	for name, value := range map[string]string{
		"relative":        "schema.org/Action",
		"query":           "https://schema.org/Action?version=1",
		"fragment":        "https://schema.org/Action#part",
		"userinfo":        "https://user@example.com/types/Tender",
		"insecure custom": "http://example.com/types/Tender",
		"missing path":    "https://schema.org/",
		"unsupported":     "ftp://example.com/types/Tender",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeSemanticTypes([]string{value}); !errors.Is(err, ErrInvalidSemanticType) {
				t.Fatalf("error = %v, want ErrInvalidSemanticType", err)
			}
		})
	}
}

func TestSemanticTypesDoNotChangeStructuralValidation(t *testing.T) {
	validator, err := CompileValidator("urn:record-hub:test:six-fields", []byte(sixFieldSchema))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeSemanticTypes([]string{"https://schema.org/Action"}); err != nil {
		t.Fatal(err)
	}
	invalidDocument := []byte(`{"text":42}`)
	if err := validator.ValidateJSON(invalidDocument); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("semantic annotation changed validation result: %v", err)
	}
}
