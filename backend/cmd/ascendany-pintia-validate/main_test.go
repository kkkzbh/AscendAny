package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunValidatesAuthoritativeSnapshot(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "pintia", "fixtures", "valid", "complete.json"))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	status := run(context.Background(), []string{"/fixture.json"}, func(path string) (io.ReadCloser, error) {
		if path != "/fixture.json" {
			t.Fatalf("path = %q", path)
		}
		return io.NopCloser(bytes.NewReader(payload)), nil
	}, &logs)
	if status != 0 {
		t.Fatalf("status = %d, logs = %s", status, logs.String())
	}
	if !strings.Contains(logs.String(), `"msg":"snapshot accepted"`) {
		t.Fatalf("missing acceptance log: %s", logs.String())
	}
}

func TestRunRejectsInvalidCommandAndSnapshot(t *testing.T) {
	t.Run("command", func(t *testing.T) {
		if status := run(context.Background(), nil, nil, io.Discard); status != 2 {
			t.Fatalf("status = %d", status)
		}
	})
	t.Run("snapshot", func(t *testing.T) {
		status := run(context.Background(), []string{"/fixture.json"}, func(string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(`{"schema":"wrong"}`)), nil
		}, io.Discard)
		if status != 1 {
			t.Fatalf("status = %d", status)
		}
	})
}
