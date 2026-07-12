package judgerunner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testImage = "localhost/ascendany-cpp20@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestPodmanArgumentsEnforceIsolationWithoutShell(t *testing.T) {
	binary, err := filepath.Abs("/usr/bin/podman")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewPodmanEngine(binary, testImage, "/var/empty")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := makeRuntimeRoot(t)
	arguments, err := engine.buildRunArguments(ContainerCommand{
		Name: "ascendany-job-compile", Workspace: workspace, RuntimeRoot: runtimeRoot, Executable: cpp20Compiler,
		Arguments: []string{"-std=c++20", "-o", "/workspace/program", "/workspace/main.cpp"},
		Timeout:   10 * time.Second, MemoryLimitBytes: 256 << 20, OutputLimitBytes: 1 << 20,
		PIDsLimit: 64, CPUs: 1, TemporaryLimitBytes: 64 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"--cgroup-manager=cgroupfs", "--events-backend=none", "--hooks-dir=/var/empty", "--interactive", "--pull=never", "--network=none", "--http-proxy=false", "--read-only",
		"--cap-drop=all", "--security-opt=no-new-privileges", "--cgroups=enabled", "--cgroup-parent=/containers", "--pids-limit=64",
		"--memory=268435456b", "--memory-swap=268435456b", "--userns=host",
		testImage, cpp20Compiler, "-std=c++20",
	} {
		if !slices.Contains(arguments, required) {
			t.Fatalf("arguments missing %q: %v", required, arguments)
		}
	}
	joined := strings.Join(arguments, "\n")
	if strings.Contains(joined, "/bin/sh") || strings.Contains(joined, "sh -c") ||
		strings.Contains(joined, "--userns=keep-id") || strings.Contains(joined, "--user=") {
		t.Fatalf("arguments contain shell execution or a shifted user mapping: %v", arguments)
	}
}

func TestPodmanChildUsesParentDeathSignalWithoutProcessGroupMutation(t *testing.T) {
	attributes := podmanProcessAttributes()
	if attributes.Pdeathsig != syscall.SIGKILL || attributes.Setpgid {
		t.Fatalf("unexpected Podman child process attributes: %#v", attributes)
	}
}

func TestMinimalPodmanEnvironmentExcludesOperatorBusAndNetworkState(t *testing.T) {
	for name, value := range map[string]string{
		"HOME": "/var/lib/ascendany-judge", "XDG_RUNTIME_DIR": "/run/ascendany-judge-podman/job",
		"XDG_DATA_HOME": "/var/lib/ascendany-judge/.local/share", "XDG_CONFIG_HOME": "/var/lib/ascendany-judge/.config",
		"XDG_CACHE_HOME": "/var/lib/ascendany-judge/.cache", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/operator-bus",
		"HTTP_PROXY": "http://operator-proxy.example", "CONTAINER_HOST": "unix:///tmp/operator-podman.sock",
	} {
		t.Setenv(name, value)
	}
	expected := []string{
		"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=/var/lib/ascendany-judge",
		"XDG_RUNTIME_DIR=/run/ascendany-judge-podman/job",
		"XDG_DATA_HOME=/var/lib/ascendany-judge/.local/share",
		"XDG_CONFIG_HOME=/var/lib/ascendany-judge/.config",
		"XDG_CACHE_HOME=/var/lib/ascendany-judge/.cache",
	}
	if actual := minimalPodmanEnvironment(); !slices.Equal(actual, expected) {
		t.Fatalf("minimalPodmanEnvironment() = %#v", actual)
	}
}

func TestPodmanRejectsUnpinnedImageAndUnsafeWorkspace(t *testing.T) {
	if _, err := NewPodmanEngine("/usr/bin/podman", "docker.io/library/gcc:latest", "/var/empty"); err == nil {
		t.Fatal("NewPodmanEngine() error = nil")
	}
	engine, err := NewPodmanEngine("/usr/bin/podman", testImage, "/var/empty")
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.buildRunArguments(ContainerCommand{
		Name: "job", Workspace: "/tmp/path,escape", Executable: "/workspace/program",
		RuntimeRoot: "/tmp/missing-runroot",
		Timeout:     time.Second, MemoryLimitBytes: 64 << 20, OutputLimitBytes: 1024,
		PIDsLimit: 1, CPUs: 1, TemporaryLimitBytes: 1 << 20,
	})
	if err == nil {
		t.Fatal("buildRunArguments() error = nil")
	}
}

func TestCombinedCaptureCancelsAtAggregateLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	capture := newCombinedCapture(5, cancel)
	if _, err := capture.writer(true).Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.writer(false).Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exceeded := capture.snapshot()
	if string(stdout) != "1234" || string(stderr) != "a" || !exceeded || ctx.Err() == nil {
		t.Fatalf("stdout=%q stderr=%q exceeded=%v context=%v", stdout, stderr, exceeded, ctx.Err())
	}
}

func TestPodmanEngineTimeoutRemovesContainerBeforeWaitingForClientExit(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "podman")
	if err := os.WriteFile(binary, []byte(`#!/usr/bin/bash
set -euo pipefail
runroot=''
operation=''
for argument in "$@"; do
  case "$argument" in
    --runroot=*) runroot="${argument#--runroot=}" ;;
    run|rm) operation="$argument" ;;
  esac
done
[[ -n "$runroot" && -n "$operation" ]]
case "$operation" in
  run)
    : >"${runroot}/run-started"
    while [[ ! -e "${runroot}/remove-requested" ]]; do
      /usr/bin/sleep 0.01
    done
    : >"${runroot}/remove-observed-by-run"
    exit 137
    ;;
  rm)
    : >"${runroot}/remove-requested"
    ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}
	engine, err := NewPodmanEngine(binary, testImage, "/var/empty")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := makeRuntimeRoot(t)
	result, err := engine.Run(context.Background(), ContainerCommand{
		Name: "ascendany-timeout-ownership", Workspace: workspace, RuntimeRoot: runtimeRoot,
		Executable: "/workspace/program", Timeout: 50 * time.Millisecond,
		MemoryLimitBytes: 64 << 20, OutputLimitBytes: 1024,
		PIDsLimit: 16, CPUs: 1, TemporaryLimitBytes: 16 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot, "remove-observed-by-run")); err != nil {
		t.Fatalf("running container client did not observe removal before exit: %v", err)
	}
}

func TestPodmanEngineCompilesAndRunsWhenImageConfigured(t *testing.T) {
	image := os.Getenv("ASCENDANY_TEST_JUDGE_IMAGE")
	if image == "" {
		t.Skip("ASCENDANY_TEST_JUDGE_IMAGE is not configured")
	}
	engine, err := NewPodmanEngine("/usr/bin/podman", image, "/var/empty")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := makeRuntimeRoot(t)
	if err := os.WriteFile(filepath.Join(workspace, "main.cpp"), []byte(`#include <iostream>
int main() { std::string value; std::getline(std::cin, value); std::cout << value << "\n"; }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	compileResult, err := engine.Run(context.Background(), ContainerCommand{
		Name: "ascendany-integration-compile", Workspace: workspace, RuntimeRoot: runtimeRoot,
		Executable: cpp20Compiler, Arguments: []string{"-std=c++20", "-o", "/workspace/program", "/workspace/main.cpp"},
		Timeout: 30 * time.Second, MemoryLimitBytes: 512 << 20, OutputLimitBytes: 1 << 20,
		PIDsLimit: 64, CPUs: 1, TemporaryLimitBytes: 64 << 20,
	})
	if err != nil || compileResult.ExitCode != 0 {
		t.Fatalf("compile result=%#v error=%v stderr=%s", compileResult, err, compileResult.Stderr)
	}
	runResult, err := engine.Run(context.Background(), ContainerCommand{
		Name: "ascendany-integration-run", Workspace: workspace, RuntimeRoot: runtimeRoot, ReadOnlyWorkspace: true,
		Executable: "/workspace/program", Stdin: bytes.NewBufferString("isolated\n"),
		Timeout: 5 * time.Second, MemoryLimitBytes: 64 << 20, OutputLimitBytes: 1024,
		PIDsLimit: 16, CPUs: 1, TemporaryLimitBytes: 16 << 20,
	})
	if err != nil || runResult.ExitCode != 0 || string(runResult.Stdout) != "isolated\n" {
		t.Fatalf("run result=%#v error=%v", runResult, err)
	}
}

func TestPodmanAttackCorpusWhenImageConfigured(t *testing.T) {
	image := os.Getenv("ASCENDANY_TEST_JUDGE_IMAGE")
	if image == "" {
		t.Skip("ASCENDANY_TEST_JUDGE_IMAGE is not configured")
	}
	engine, err := NewPodmanEngine("/usr/bin/podman", image, "/var/empty")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("network credentials and host artifacts", func(t *testing.T) {
		result := compileAndRunCPP(t, engine, "isolation", `
#include <arpa/inet.h>
#include <cstdlib>
#include <iostream>
#include <sys/socket.h>
#include <unistd.h>
int main() {
  if (std::getenv("ASCENDANY_DATABASE_PASSWORD_FILE") != nullptr) return 10;
  if (access("/var/lib/ascendany/artifacts", F_OK) == 0) return 11;
  int descriptor = socket(AF_INET, SOCK_STREAM, 0);
  sockaddr_in address{};
  address.sin_family = AF_INET;
  address.sin_port = htons(80);
  inet_pton(AF_INET, "1.1.1.1", &address.sin_addr);
  if (descriptor >= 0 && connect(descriptor, reinterpret_cast<sockaddr*>(&address), sizeof(address)) == 0) return 12;
  std::cout << "isolated\n";
}
`, 64<<20, 1024, 2*time.Second)
		if result.ExitCode != 0 || string(result.Stdout) != "isolated\n" {
			t.Fatalf("isolation result = %#v", result)
		}
	})
	t.Run("CPU timeout", func(t *testing.T) {
		result := compileAndRunCPP(t, engine, "timeout", `int main() { for (;;) {} }`, 64<<20, 1024, 250*time.Millisecond)
		if !result.TimedOut {
			t.Fatalf("timeout result = %#v", result)
		}
	})
	t.Run("output limit", func(t *testing.T) {
		result := compileAndRunCPP(t, engine, "output", `
#include <iostream>
int main() { for (;;) std::cout << "0123456789"; }
`, 64<<20, 1024, 2*time.Second)
		if !result.OutputLimitExceeded || len(result.Stdout)+len(result.Stderr) > 1024 {
			t.Fatalf("output result = %#v", result)
		}
	})
	t.Run("memory limit", func(t *testing.T) {
		result := compileAndRunCPP(t, engine, "memory", `
#include <cstddef>
int main() {
  constexpr std::size_t size = 256UL * 1024UL * 1024UL;
  volatile char* memory = new char[size];
  for (std::size_t index = 0; index < size; index += 4096) memory[index] = 1;
}
`, 64<<20, 1024, 3*time.Second)
		if result.ExitCode != 137 {
			t.Fatalf("memory result = %#v", result)
		}
	})
	t.Run("process limit", func(t *testing.T) {
		result := compileAndRunCPP(t, engine, "pids", `
#include <csignal>
#include <iostream>
#include <sys/wait.h>
#include <unistd.h>
#include <vector>
int main() {
  std::vector<pid_t> children;
  bool blocked = false;
  for (int index = 0; index < 128; ++index) {
    pid_t child = fork();
    if (child == 0) { pause(); _exit(0); }
    if (child < 0) { blocked = true; break; }
    children.push_back(child);
  }
  for (pid_t child : children) kill(child, SIGKILL);
  for (pid_t child : children) waitpid(child, nullptr, 0);
  std::cout << (blocked ? "blocked\n" : "unbounded\n");
  return blocked ? 0 : 1;
}
`, 64<<20, 1024, 3*time.Second)
		if result.ExitCode != 0 || string(result.Stdout) != "blocked\n" {
			t.Fatalf("PID result = %#v", result)
		}
	})
}

func compileAndRunCPP(t *testing.T, engine *PodmanEngine, name, source string, memory, output int64, timeout time.Duration) ContainerResult {
	t.Helper()
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := makeRuntimeRoot(t)
	if err := os.WriteFile(filepath.Join(workspace, "main.cpp"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	compileResult, err := engine.Run(context.Background(), ContainerCommand{
		Name: "ascendany-attack-" + name + "-compile", Workspace: workspace, RuntimeRoot: runtimeRoot,
		Executable: cpp20Compiler, Arguments: []string{"-std=c++20", "-O2", "-o", "/workspace/program", "/workspace/main.cpp"},
		Timeout: 30 * time.Second, MemoryLimitBytes: 512 << 20, OutputLimitBytes: 1 << 20,
		PIDsLimit: 64, CPUs: 1, TemporaryLimitBytes: 64 << 20,
	})
	if err != nil || compileResult.ExitCode != 0 {
		t.Fatalf("compile result=%#v error=%v stderr=%s", compileResult, err, compileResult.Stderr)
	}
	runResult, err := engine.Run(context.Background(), ContainerCommand{
		Name: "ascendany-attack-" + name + "-run", Workspace: workspace, RuntimeRoot: runtimeRoot, ReadOnlyWorkspace: true,
		Executable: "/workspace/program", Timeout: timeout, MemoryLimitBytes: memory,
		OutputLimitBytes: output, PIDsLimit: 16, CPUs: 1, TemporaryLimitBytes: 16 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runResult
}

func makeRuntimeRoot(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	path := filepath.Join(base, "runroot")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanupWorkRoot(base); err != nil {
			t.Error(err)
		}
	})
	return path
}
