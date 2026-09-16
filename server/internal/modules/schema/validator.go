package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const Draft202012URI = "https://json-schema.org/draft/2020-12/schema"

var (
	ErrInvalidSchema   = errors.New("invalid JSON Schema")
	ErrInvalidDocument = errors.New("document does not match schema")
)

// Validator is an immutable, concurrency-safe compiled JSON Schema.
type Validator struct {
	compiled *jsonschema.Schema
}

func CompileValidator(schemaID string, rawSchema []byte) (*Validator, error) {
	document, err := decodeJSON(rawSchema)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	object, ok := document.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: root must be an object", ErrInvalidSchema)
	}
	if draft, _ := object["$schema"].(string); draft != Draft202012URI {
		return nil, fmt.Errorf("%w: $schema must be %q", ErrInvalidSchema, Draft202012URI)
	}
	if schemaID == "" {
		return nil, fmt.Errorf("%w: schema ID is required", ErrInvalidSchema)
	}

	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.AssertVocabs()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	return &Validator{compiled: compiled}, nil
}

func (validator *Validator) ValidateJSON(rawDocument []byte) error {
	if validator == nil || validator.compiled == nil {
		return fmt.Errorf("%w: validator is not initialized", ErrInvalidDocument)
	}
	document, err := decodeJSON(rawDocument)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDocument, err)
	}
	if err := validator.compiled.Validate(document); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	return nil
}

func decodeJSON(raw []byte) (interface{}, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("input is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return value, nil
}
