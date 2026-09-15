// Package eventenvelope validates the shared v1 event envelope before a
// message reaches a projection handler.
package eventenvelope

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	MaxEnvelopeBytes = 256 * 1024
	SchemaVersion    = int64(1)
	maxFutureSkew    = 5 * time.Minute
	schemaID         = "urn:record-hub:contract:event-envelope:v1"
)

var (
	ErrInvalid            = errors.New("invalid event envelope")
	ErrTooLarge           = errors.New("event envelope exceeds maximum size")
	ErrUnsupportedVersion = errors.New("unsupported event envelope schema version")
	ErrInvalidTime        = errors.New("event envelope occurredAt must be RFC3339 UTC and not in the future")
)

//go:embed event-envelope-v1.schema.json
var schemaBytes []byte

var (
	compiledSchema     *jsonschema.Schema
	compiledSchemaErr  error
	compiledSchemaOnce sync.Once
)

// Verifier applies structural and transport-level envelope policy.
type Verifier struct {
	now func() time.Time
}

// NewVerifier uses the wall clock for occurredAt skew validation.
func NewVerifier() *Verifier {
	return &Verifier{now: time.Now}
}

func newVerifier(now func() time.Time) *Verifier {
	return &Verifier{now: now}
}

// Validate rejects oversized, malformed, structurally invalid, unsupported,
// and non-canonical envelopes. Errors intentionally exclude payload content.
func (v *Verifier) Validate(data []byte) error {
	if len(data) > MaxEnvelopeBytes {
		return ErrTooLarge
	}
	if len(data) == 0 || !utf8.Valid(data) {
		return ErrInvalid
	}

	instance, err := decode(data)
	if err != nil {
		return ErrInvalid
	}
	schema, err := schema()
	if err != nil {
		return fmt.Errorf("compile event envelope schema: %w", err)
	}
	if err := schema.Validate(instance); err != nil {
		return ErrInvalid
	}

	object, ok := instance.(map[string]any)
	if !ok {
		return ErrInvalid
	}
	version, ok := object["schemaVersion"].(json.Number)
	if !ok {
		return ErrInvalid
	}
	parsedVersion, err := version.Int64()
	if err != nil || parsedVersion != SchemaVersion {
		return ErrUnsupportedVersion
	}

	occurredAt, ok := object["occurredAt"].(string)
	if !ok || !strings.HasSuffix(occurredAt, "Z") {
		return ErrInvalidTime
	}
	parsedTime, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil || parsedTime.After(v.now().UTC().Add(maxFutureSkew)) {
		return ErrInvalidTime
	}
	return nil
}

func decode(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("event envelope contains trailing data")
	}
	return value, nil
}

func schema() (*jsonschema.Schema, error) {
	compiledSchemaOnce.Do(func() {
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
		if err != nil {
			compiledSchemaErr = err
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		if err := compiler.AddResource(schemaID, document); err != nil {
			compiledSchemaErr = err
			return
		}
		compiledSchema, compiledSchemaErr = compiler.Compile(schemaID)
	})
	return compiledSchema, compiledSchemaErr
}
