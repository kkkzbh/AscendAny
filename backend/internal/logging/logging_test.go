package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
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
	timestamp, ok := entry["time"].(string)
	if !ok || !strings.HasSuffix(timestamp, "Z") {
		t.Fatalf("time = %#v, want canonical UTC", entry["time"])
	}
}

func TestCanonicalAttributeNormalizesTimeToUTC(t *testing.T) {
	t.Parallel()

	local := time.Date(2026, time.July, 16, 20, 42, 2, 345678901, time.FixedZone("UTC+8", 8*60*60))
	attribute := canonicalAttribute(nil, slog.Time(slog.TimeKey, local))

	if got := attribute.Value.Time(); got.Location() != time.UTC || !got.Equal(local) {
		t.Fatalf("canonical time = %s (%s), want same instant in UTC", got, got.Location())
	}
}
