package lsprunner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lspwire"
)

const testSessionID = "11111111-1111-4111-8111-111111111111"

type controlSocketFileInfo struct {
	mode os.FileMode
	uid  uint32
}

func (controlSocketFileInfo) Name() string            { return "control.sock" }
func (controlSocketFileInfo) Size() int64             { return 0 }
func (value controlSocketFileInfo) Mode() os.FileMode { return value.mode }
func (controlSocketFileInfo) ModTime() time.Time      { return time.Time{} }
func (controlSocketFileInfo) IsDir() bool             { return false }
func (value controlSocketFileInfo) Sys() any          { return &syscall.Stat_t{Uid: value.uid} }

type invalidControlSocketMetadata struct{ controlSocketFileInfo }

func (invalidControlSocketMetadata) Sys() any { return nil }

func TestControlSocketOwnerUIDRequiresExactBoundSocketContract(t *testing.T) {
	valid := controlSocketFileInfo{mode: os.ModeSocket | 0o660, uid: 1001}
	uid, err := controlSocketOwnerUID(valid, nil)
	if err != nil || uid != 1001 {
		t.Fatalf("controlSocketOwnerUID(valid) = %d, %v", uid, err)
	}
	for name, input := range map[string]controlSocketFileInfo{
		"root owner":     {mode: os.ModeSocket | 0o660, uid: 0},
		"world readable": {mode: os.ModeSocket | 0o664, uid: 1001},
		"regular file":   {mode: 0o660, uid: 1001},
		"symlink":        {mode: os.ModeSymlink | 0o660, uid: 1001},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := controlSocketOwnerUID(input, nil); err == nil {
				t.Fatal("invalid control socket identity was accepted")
			}
		})
	}
	if _, err := controlSocketOwnerUID(valid, os.ErrNotExist); err == nil {
		t.Fatal("control socket stat failure was accepted")
	}
	if _, err := controlSocketOwnerUID(nil, nil); err == nil {
		t.Fatal("missing control socket metadata was accepted")
	}
	if _, err := controlSocketOwnerUID(invalidControlSocketMetadata{valid}, nil); err == nil {
		t.Fatal("non-stat control socket metadata was accepted")
	}
}

func TestWorkspaceLifecycleRejectsReuseAndSymlinks(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "sessions", testSessionID)
	if err := prepareWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	if err := prepareWorkspace(workspace); err == nil {
		t.Fatal("existing workspace was accepted")
	}
	if err := cleanupWorkspace(workspace, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains after cleanup: %v", err)
	}
	linkParent := filepath.Join(root, "linked-sessions")
	if err := os.Symlink(filepath.Join(root, "sessions"), linkParent); err != nil {
		t.Fatal(err)
	}
	if err := prepareWorkspace(filepath.Join(linkParent, testSessionID)); err == nil {
		t.Fatal("symlinked workspace parent was accepted")
	}
}

func TestWorkspaceInspectionEnforcesFileByteAndTypeLimits(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "sessions", testSessionID)
	if err := prepareWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	policy := lsp.DefaultPolicy()
	policy.MaximumWorkspaceFiles = 8
	policy.MaximumWorkspaceBytes = 1 << 20
	if err := inspectWorkspace(workspace, policy); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := inspectWorkspace(workspace, policy); err == nil {
		t.Fatal("workspace symlink was accepted")
	}
	if err := os.Remove(filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "large.cpp"), make([]byte, policy.MaximumWorkspaceBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inspectWorkspace(workspace, policy); err == nil {
		t.Fatal("workspace byte excess was accepted")
	}
}

func TestRealClangdSessionRoundTripAndDisconnectCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("real clangd smoke test")
	}
	if os.Getuid() == 0 {
		t.Skip("production worker refuses root and peer UIDs must be non-root")
	}
	if _, err := os.Stat("/usr/bin/clangd"); err != nil {
		t.Skip("/usr/bin/clangd is unavailable")
	}
	runRealClangdSession(t, "int square(int x) { return x * x; }\nint main() { return square(3); }\n", "")
}

func TestRealClangdSandboxRejectsHostFileReadsAndCleansWorkspace(t *testing.T) {
	if os.Getenv("ASCENDANY_TEST_LSP_ROOTFS") != "1" {
		t.Skip("real LSP root filesystem acceptance is disabled")
	}
	if os.Getenv("ASCENDANY_TEST_LSP_ROOTFS_CHILD") == "1" {
		runRealClangdSandboxChild(t)
		return
	}
	if os.Getuid() == 0 {
		t.Fatal("real LSP root filesystem acceptance requires a non-root identity")
	}
	for _, path := range []string{"/usr/bin/bwrap", "/usr/bin/clangd"} {
		info, err := os.Lstat(path)
		stat, ok := infoSysStat(info)
		if err != nil || !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("required root-owned executable is unavailable: %s", path)
		}
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	testBinary, err = filepath.EvalSymlinks(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	hostRoot := t.TempDir()
	hostCredential := filepath.Join(hostRoot, "host-credential")
	credentialSentinel := "ASCENDANY_LSP_HOST_CREDENTIAL_SENTINEL_7f52af6e"
	if err := os.WriteFile(hostCredential, []byte(credentialSentinel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostname, err := os.ReadFile("/etc/hostname")
	if err != nil || len(strings.TrimSpace(string(hostname))) == 0 {
		t.Fatalf("read host hostname identity: %v", err)
	}
	hostnameDigest := sha256.Sum256(hostname)
	credentialDigest := sha256.Sum256([]byte(credentialSentinel + "\n"))
	rootfsTestBinary := "/opt/ascendany/v2/bin/lsprunner.test"
	arguments := []string{
		"--die-with-parent", "--new-session",
		"--unshare-ipc", "--unshare-net", "--unshare-pid", "--unshare-uts",
		"--ro-bind", "/usr", "/usr",
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/lib", "/lib",
		"--symlink", "usr/lib64", "/lib64",
		"--dev", "/dev",
		"--dir", "/etc",
		"--dir", "/home",
		"--dir", "/opt",
		"--dir", "/opt/ascendany",
		"--dir", "/opt/ascendany/v2",
		"--dir", "/opt/ascendany/v2/bin",
		"--ro-bind", testBinary, rootfsTestBinary,
		"--proc", "/proc",
		"--dir", "/run",
		"--dir", "/sys",
		"--tmpfs", "/tmp",
		"--dir", "/var",
		"--clearenv",
		"--setenv", "HOME", "/tmp",
		"--setenv", "LANG", "C.UTF-8",
		"--setenv", "LC_ALL", "C.UTF-8",
		"--setenv", "PATH", "/usr/bin:/bin",
		"--setenv", "ASCENDANY_TEST_LSP_ROOTFS", "1",
		"--setenv", "ASCENDANY_TEST_LSP_ROOTFS_CHILD", "1",
		"--setenv", "ASCENDANY_TEST_LSP_HOST_CREDENTIAL", hostCredential,
		"--setenv", "ASCENDANY_TEST_LSP_HOSTNAME_SHA256", fmt.Sprintf("%x", hostnameDigest),
		"--setenv", "ASCENDANY_TEST_LSP_CREDENTIAL_SHA256", fmt.Sprintf("%x", credentialDigest),
		"--setenv", "ASCENDANY_TEST_LSP_HOSTNAME_CONTENT", strings.TrimSpace(string(hostname)),
		"--setenv", "ASCENDANY_TEST_LSP_CREDENTIAL_CONTENT", credentialSentinel,
		rootfsTestBinary,
		"-test.run", "^TestRealClangdSandboxRejectsHostFileReadsAndCleansWorkspace$",
		"-test.count=1",
		"-test.v",
	}
	command := exec.Command("/usr/bin/bwrap", arguments...)
	command.Env = []string{"HOME=" + hostRoot, "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "PATH=/usr/bin:/bin"}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("minimal-root clangd acceptance failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "--- PASS: TestRealClangdSandboxRejectsHostFileReadsAndCleansWorkspace") {
		t.Fatalf("minimal-root child did not emit exact pass evidence:\n%s", output)
	}
}

func runRealClangdSandboxChild(t *testing.T) {
	for _, path := range []string{"/etc/hostname", "/etc/ascendany/credentials"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("minimal root exposes host path %s: %v", path, err)
		}
	}
	hostCredential := os.Getenv("ASCENDANY_TEST_LSP_HOST_CREDENTIAL")
	if hostCredential == "" || !filepath.IsAbs(hostCredential) {
		t.Fatal("canonical host credential acceptance path is required")
	}
	if _, err := os.Lstat(hostCredential); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("minimal root exposes host credential path: %v", err)
	}
	for name, target := range map[string]string{"/bin": "usr/bin", "/lib": "usr/lib", "/lib64": "usr/lib64"} {
		actual, err := os.Readlink(name)
		if err != nil || actual != target {
			t.Fatalf("minimal root link %s = %q, %v", name, actual, err)
		}
	}
	candidates := []struct {
		name      string
		path      string
		content   string
		forbidden string
	}{
		{
			name:      "hostname",
			path:      "/etc/hostname",
			content:   os.Getenv("ASCENDANY_TEST_LSP_HOSTNAME_CONTENT"),
			forbidden: os.Getenv("ASCENDANY_TEST_LSP_HOSTNAME_SHA256"),
		},
		{
			name:      "production credential",
			path:      "/etc/ascendany/credentials/runtime_db_password.cred",
			content:   os.Getenv("ASCENDANY_TEST_LSP_CREDENTIAL_CONTENT"),
			forbidden: os.Getenv("ASCENDANY_TEST_LSP_CREDENTIAL_SHA256"),
		},
		{
			name:      "host credential",
			path:      hostCredential,
			content:   os.Getenv("ASCENDANY_TEST_LSP_CREDENTIAL_CONTENT"),
			forbidden: os.Getenv("ASCENDANY_TEST_LSP_CREDENTIAL_SHA256"),
		},
	}
	for _, candidate := range candidates {
		t.Run(candidate.name, func(t *testing.T) {
			if candidate.content == "" || candidate.forbidden == "" {
				t.Fatal("host identity digest is required")
			}
			source := "#include \"" + candidate.path + "\"\nint marker = 1;\n"
			diagnostics := runRealClangdSession(t, source, candidate.path)
			lowerDiagnostics := strings.ToLower(string(diagnostics))
			if strings.Contains(lowerDiagnostics, strings.ToLower(candidate.content)) ||
				strings.Contains(lowerDiagnostics, strings.ToLower(candidate.forbidden)) {
				t.Fatal("clangd diagnostics contain a host identity value")
			}
		})
	}
}

func infoSysStat(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return stat, ok
}

func runRealClangdSession(t *testing.T, source, diagnosticPath string) []byte {
	t.Helper()
	root := t.TempDir()
	socketDirectory := filepath.Join(root, "control")
	if err := os.Mkdir(socketDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDirectory, "lsp-control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	workspace := filepath.Join(root, "sessions", testSessionID)
	if err := os.Chmod(socket, 0o660); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(testSessionID, socket, workspace)
	config.Policy.MaximumSessionDuration = 15 * time.Second
	var runner *Runner
	if os.Getenv("ASCENDANY_TEST_LSP_ROOTFS_CHILD") == "1" {
		// Unprivileged bubblewrap deliberately leaves host UID 0 unmapped, so the
		// already root-owned /usr/bin/clangd appears as nobody inside this test
		// namespace. The outer test verifies its host ownership and exact bytes;
		// production construction continues to enforce root ownership in New.
		runner = &Runner{config: config, start: startClangd}
	} else {
		runner, err = New(config)
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- runner.Serve(ctx) }()
	connection, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := lspwire.NewReader(connection, config.Policy)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := lspwire.ReadHello(reader)
	if err != nil {
		t.Fatal(err)
	}
	if hello.SessionID != testSessionID {
		t.Fatalf("hello = %#v", hello)
	}
	writer, err := lspwire.NewWriter(connection, config.Policy)
	if err != nil {
		t.Fatal(err)
	}
	initialize := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":"file:///workspace","capabilities":{},"workspaceFolders":[{"uri":"file:///workspace","name":"workspace"}]}}`)
	if err := writer.Write(initialize); err != nil {
		t.Fatal(err)
	}
	if _, err := readResponseID(connection, reader, 1); err != nil {
		t.Fatalf("initialize response: %v", err)
	}
	for _, body := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`),
		mustJSONMessage(t, map[string]any{
			"jsonrpc": "2.0", "method": "textDocument/didOpen",
			"params": map[string]any{"textDocument": map[string]any{
				"uri": "file:///workspace/main.cpp", "languageId": "cpp", "version": 1, "text": source,
			}},
		}),
	} {
		if err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	var diagnostics []byte
	if diagnosticPath != "" {
		diagnostics, err = readDiagnosticsContaining(connection, reader, diagnosticPath)
		if err != nil {
			t.Fatalf("diagnostics for %s: %v", diagnosticPath, err)
		}
		if !strings.Contains(strings.ToLower(string(diagnostics)), "file not found") {
			t.Fatalf("clangd did not reject inaccessible path %s: %s", diagnosticPath, diagnostics)
		}
	}
	hover := []byte(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///workspace/main.cpp"},"position":{"line":1,"character":4}}}`)
	if err := writer.Write(hover); err != nil {
		t.Fatal(err)
	}
	response, err := readResponseID(connection, reader, 2)
	if err != nil {
		t.Fatalf("hover response: %v", err)
	}
	if !strings.Contains(string(response), `"id":2`) {
		t.Fatalf("hover response = %s", response)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("LSP worker did not terminate after disconnect")
	}
	if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains after disconnect: %v", err)
	}
	return diagnostics
}

func mustJSONMessage(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func readDiagnosticsContaining(connection *net.UnixConn, reader *lspwire.Reader, path string) ([]byte, error) {
	if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	defer connection.SetReadDeadline(time.Time{})
	for index := 0; index < 64; index++ {
		body, err := reader.Read()
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, err
		}
		if envelope.Method == "textDocument/publishDiagnostics" && strings.Contains(string(body), path) {
			return body, nil
		}
	}
	return nil, errors.New("expected clangd diagnostics were not observed")
}

func readResponseID(connection *net.UnixConn, reader *lspwire.Reader, expected int) ([]byte, error) {
	if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	defer connection.SetReadDeadline(time.Time{})
	for index := 0; index < 32; index++ {
		body, err := reader.Read()
		if err != nil {
			return nil, err
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, err
		}
		if string(envelope.ID) == string(rune('0'+expected)) {
			return body, nil
		}
	}
	return nil, errors.New("expected clangd response ID was not observed")
}
