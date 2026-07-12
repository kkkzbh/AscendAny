package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOnlineServerDependencyGraphExcludesTrainerProcess(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller path is unavailable")
	}
	command := exec.Command("go", "list", "-deps", ".")
	command.Dir = filepath.Dir(filename)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list -deps . failed: %v: %s", err, stderr.String())
	}
	const forbidden = "github.com/kkkzbh/AscendAny/backend/internal/trainerprocess"
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == forbidden {
			t.Fatalf("online server dependency graph contains %s", forbidden)
		}
	}
}
