package eventenvelope

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, time.September, 16, 0, 0, 0, 0, time.UTC)

func TestValidateFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantErr error
	}{
		{name: "valid", fixture: "valid/event-envelope-v1.json"},
		{name: "unknown field", fixture: "invalid/event-envelope-v1-unknown-field.json", wantErr: ErrInvalid},
		{name: "unsupported version", fixture: "invalid/event-envelope-v1-unsupported-version.json", wantErr: ErrUnsupportedVersion},
		{name: "non UTC time", fixture: "invalid/event-envelope-v1-non-utc-time.json", wantErr: ErrInvalidTime},
		{name: "invalid schema", fixture: "invalid/event-envelope-v1-missing-event-id.json", wantErr: ErrInvalid},
	}

	verifier := newVerifier(func() time.Time { return fixedNow })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.fixture))
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			err = verifier.Validate(data)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRejectsOversizedEnvelope(t *testing.T) {
	data := []byte(strings.Repeat("x", MaxEnvelopeBytes+1))
	if err := NewVerifier().Validate(data); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Validate() error = %v, want ErrTooLarge", err)
	}
}

func TestValidateAcceptsMaximumSizedEnvelopeBeforeSchemaCheck(t *testing.T) {
	data := []byte(strings.Repeat("x", MaxEnvelopeBytes))
	if err := NewVerifier().Validate(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateRejectsFutureTime(t *testing.T) {
	data := fixture(t, "valid/event-envelope-v1.json")
	data = []byte(strings.Replace(string(data), "2026-09-15T15:30:00Z", "2026-09-16T00:05:01Z", 1))
	verifier := newVerifier(func() time.Time { return fixedNow })
	if err := verifier.Validate(data); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Validate() error = %v, want ErrInvalidTime", err)
	}
}

func TestValidateRejectsTrailingJSON(t *testing.T) {
	data := append(fixture(t, "valid/event-envelope-v1.json"), []byte("{}")...)
	if err := NewVerifier().Validate(data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return data
}
