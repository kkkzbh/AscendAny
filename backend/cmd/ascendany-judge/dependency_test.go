package main

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestJudgeBinaryDependencyBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("dependency gate source path is unavailable")
	}
	command := exec.CommandContext(ctx, "go", "list", "-deps", "-f={{.ImportPath}}", ".")
	command.Dir = filepath.Dir(sourcePath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list judge dependencies: %v\n%s", err, output)
	}
	forbidden := []string{
		"github.com/jackc/pgx",
		"github.com/kkkzbh/AscendAny/backend/internal/database",
		"github.com/kkkzbh/AscendAny/backend/internal/artifact",
		"github.com/kkkzbh/AscendAny/backend/internal/auth",
		"github.com/kkkzbh/AscendAny/backend/internal/credential",
		"github.com/kkkzbh/AscendAny/backend/internal/principalguard",
		"github.com/kkkzbh/AscendAny/backend/internal/oj",
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		dependency := strings.TrimSpace(scanner.Text())
		for _, prefix := range forbidden {
			if dependency == prefix || strings.HasPrefix(dependency, prefix+"/") {
				t.Errorf("isolated judge depends on forbidden capability package %q", dependency)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
