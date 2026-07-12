package logging

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewWritesStructuredJSONAtConfiguredLevel(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := New(&output, "warn")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Info("hidden")
	logger.Warn("database unavailable", "component", "readiness")

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not JSON: %v", err)
	}
	if entry["level"] != "WARN" || entry["msg"] != "database unavailable" {
		t.Fatalf("unexpected log entry: %#v", entry)
	}
	if entry["component"] != "readiness" {
		t.Fatalf("component = %#v", entry["component"])
	}
}
