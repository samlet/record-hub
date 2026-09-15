package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewWritesStructuredJSONAtConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)

	logger.Debug("hidden")
	logger.Info("started", "mode", "api")

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not JSON: %v", err)
	}
	if entry["msg"] != "started" || entry["mode"] != "api" {
		t.Fatalf("log entry = %#v", entry)
	}
}
