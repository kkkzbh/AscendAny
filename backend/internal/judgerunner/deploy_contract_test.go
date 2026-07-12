package judgerunner

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type judgeImageLock struct {
	Schema     string `json:"schema"`
	Dockerfile struct {
		Repository string `json:"repository"`
		Revision   string `json:"revision"`
		Path       string `json:"path"`
		SHA256     string `json:"sha256"`
	} `json:"dockerfile"`
	Image struct {
		Index             string `json:"index"`
		IndexMediaType    string `json:"indexMediaType"`
		Leaf              string `json:"leaf"`
		ManifestMediaType string `json:"manifestMediaType"`
		ManifestSize      int64  `json:"manifestSize"`
		ConfigDigest      string `json:"configDigest"`
		ConfigSize        int64  `json:"configSize"`
	} `json:"image"`
	Platform struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
	Toolchain struct {
		Compiler string `json:"compiler"`
		Version  string `json:"version"`
	} `json:"toolchain"`
}

func TestJudgeReleasePinsReviewedImageAndCompilerClosure(t *testing.T) {
	lockPath := "../../../deploy/v2/config/judge-image-lock.json"
	file, err := os.Open(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var lock judgeImageLock
	if err := decoder.Decode(&lock); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("judge image lock has trailing JSON: %v", err)
	}
	digest := regexp.MustCompile(`^docker[.]io/library/gcc@sha256:[0-9a-f]{64}$`)
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if lock.Schema != "ascendany.judge-image-lock.v1" ||
		lock.Dockerfile.Repository != "https://github.com/docker-library/gcc.git" ||
		lock.Dockerfile.Path != "15/Dockerfile" || len(lock.Dockerfile.Revision) != 40 ||
		!hex.MatchString(lock.Dockerfile.SHA256) || !digest.MatchString(lock.Image.Index) ||
		!digest.MatchString(lock.Image.Leaf) || lock.Image.Index == lock.Image.Leaf ||
		lock.Image.IndexMediaType != "application/vnd.oci.image.index.v1+json" ||
		lock.Image.ManifestMediaType != "application/vnd.oci.image.manifest.v1+json" ||
		lock.Image.ManifestSize < 1 || lock.Image.ConfigSize < 1 ||
		!strings.HasPrefix(lock.Image.ConfigDigest, "sha256:") ||
		lock.Platform.OS != "linux" || lock.Platform.Architecture != "amd64" ||
		lock.Toolchain.Compiler != cpp20Compiler || lock.Toolchain.Version != "15.2.0" {
		t.Fatalf("judge image lock differs from the reviewed GCC closure: %#v", lock)
	}
	environment, err := os.ReadFile("../../../deploy/v2/config/judge.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(environment), "ASCENDANY_JUDGE_CPP20_IMAGE="+lock.Image.Leaf+"\n") ||
		strings.Contains(string(environment), "ASCENDANY_JUDGE_CPP20_IMAGE="+lock.Image.Index+"\n") {
		t.Fatal("production Judge environment does not select the locked linux/amd64 leaf")
	}
	for _, relative := range []string{
		"acquire-judge-image.sh",
		"attest-judge-image.sh",
		"judge-image-contract.sh",
		"preload-judge-image.sh",
	} {
		info, err := os.Lstat("../../../deploy/v2/scripts/" + relative)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o755 {
			t.Fatalf("release Judge image script is not one mode-0755 regular file: %s", relative)
		}
	}
}

func TestJudgeOperatorCommandsUseClosedRootlessEnvironment(t *testing.T) {
	preloader, err := os.ReadFile("../../../deploy/v2/scripts/preload-judge-image.sh")
	if err != nil {
		t.Fatal(err)
	}
	preloaderText := string(preloader)
	for _, required := range []string{
		"#!/usr/bin/bash -p\n\nset +x\nset -Eeuo pipefail",
		"ASCENDANY_JUDGE_PRELOADER_CLEAN_ENV=1",
		`[[ "$target_user" == "ascendany-judge" ]]`,
		`cd "$target_home" || exit 1`,
		`exec /usr/bin/runuser -u "$target_user" -- /usr/bin/env -i`,
		`HOME="$target_home"`,
		`target_runtime="/run/ascendany-judge-image-podman"`,
		`XDG_RUNTIME_DIR="$target_runtime"`,
		`XDG_DATA_HOME="$target_home/.local/share"`,
		`XDG_CONFIG_HOME="$target_home/.config"`,
		`XDG_CACHE_HOME="$target_home/.cache"`,
		`run_as_target /usr/bin/podman --cgroup-manager=cgroupfs --runroot="$target_runtime/containers" load`,
		`run_as_target "${script_directory}/attest-judge-image.sh"`,
	} {
		if !strings.Contains(preloaderText, required) {
			t.Fatalf("Judge image preloader lacks closed rootless contract: %s", required)
		}
	}
	if strings.Contains(preloaderText, `runuser -u "$target_user" -- podman`) {
		t.Fatal("Judge image preloader still invokes Podman with inherited operator state")
	}
	attester, err := os.ReadFile("../../../deploy/v2/scripts/attest-judge-image.sh")
	if err != nil {
		t.Fatal(err)
	}
	attesterText := string(attester)
	for _, required := range []string{
		`operator_runtime="/run/ascendany-judge-image-podman"`,
		`podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" image inspect`,
		`podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" run --userns=host`,
	} {
		if !strings.Contains(attesterText, required) {
			t.Fatalf("Judge image attester lacks deterministic cgroup ownership: %s", required)
		}
	}

	validator, err := os.ReadFile("../../../deploy/v2/scripts/validate-production.sh")
	if err != nil {
		t.Fatal(err)
	}
	validatorText := string(validator)
	for _, required := range []string{
		"run_as_judge() {",
		"exec /usr/bin/runuser -u ascendany-judge -- /usr/bin/env -i",
		"XDG_RUNTIME_DIR=/run/ascendany-judge-image-podman",
		"--runroot=/run/ascendany-judge-image-podman/containers image exists",
		"run_as_judge /opt/ascendany/v2/scripts/attest-judge-image.sh",
	} {
		if !strings.Contains(validatorText, required) {
			t.Fatalf("production validator lacks closed Judge rootless contract: %s", required)
		}
	}
}

func TestJudgeSocketDirectoryHasSinglePersistentOwner(t *testing.T) {
	unit, err := os.ReadFile("../../../deploy/v2/systemd/ascendany-judge@.service")
	if err != nil {
		t.Fatal(err)
	}
	unitText := string(unit)
	if !strings.Contains(unitText, "AssertPathIsDirectory=/run/ascendany-judge\n") {
		t.Fatal("Judge unit does not require the persistent socket directory")
	}
	if strings.Contains(unitText, "\nRuntimeDirectory=ascendany-judge\n") {
		t.Fatal("Judge unit competes with tmpfiles for the shared socket directory")
	}
	for _, required := range []string{
		"Environment=XDG_RUNTIME_DIR=/run/ascendany-judge-podman/%i\n",
		"RuntimeDirectory=ascendany-judge-podman/%i\n",
		"RuntimeDirectoryMode=0700\n",
		"RuntimeDirectoryPreserve=no\n",
		"PrivateTmp=no\n",
		"TemporaryFileSystem=/tmp:rw,nosuid,nodev,noexec /var/tmp:rw,nosuid,nodev,noexec\n",
		"ProtectKernelTunables=no\n",
		"ProtectKernelLogs=no\n",
		"ProtectProc=invisible\n",
		"ProcSubset=all\n",
		"RemoveIPC=no\n",
	} {
		if !strings.Contains(unitText, required) {
			t.Fatalf("Judge unit lacks its fresh rootless runtime contract: %s", required)
		}
	}
	if !strings.Contains(unitText, "\nGroup=ascendany-judge\nSupplementaryGroups=ascendany-runtime\n") ||
		strings.Contains(unitText, "\nGroup=ascendany-runtime\n") {
		t.Fatal("Judge unit does not preserve the rootless Podman primary GID")
	}
	if !strings.Contains(unitText, "\nProtectHostname=no\n") {
		t.Fatal("Judge unit blocks crun from configuring its private UTS namespace")
	}

	tmpfiles, err := os.ReadFile("../../../deploy/v2/tmpfiles.d/ascendany-v2.conf")
	if err != nil {
		t.Fatal(err)
	}
	const socketDirectory = "d /run/ascendany-judge                       2770 ascendany-judge      ascendany-runtime    -   -"
	if strings.Count(string(tmpfiles), socketDirectory) != 1 {
		t.Fatal("Judge socket directory does not have one exact tmpfiles owner")
	}
	const imageRuntime = "d /run/ascendany-judge-image-podman          0700 ascendany-judge      ascendany-judge      -   -"
	if strings.Count(string(tmpfiles), imageRuntime) != 1 {
		t.Fatal("Judge image-operator runtime does not have one exact tmpfiles owner")
	}

	validator, err := os.ReadFile("../../../deploy/v2/scripts/validate-production.sh")
	if err != nil {
		t.Fatal(err)
	}
	validatorText := string(validator)
	for _, required := range []string{
		`check_effective_value "$unit" RuntimeDirectory ascendany-judge-podman/validation`,
		`check_effective_value "$unit" ProtectHostname no`,
		`stat -Lc '%u:%g:%a' /run/ascendany-judge`,
		`"$judge_uid:$runtime_gid:2770"`,
	} {
		if !strings.Contains(validatorText, required) {
			t.Fatalf("production validator does not enforce Judge socket ownership: %s", required)
		}
	}
}

func TestJudgeImagePreloaderIgnoresInheritedBashEnvironment(t *testing.T) {
	temporary := t.TempDir()
	marker := filepath.Join(temporary, "bash-env-executed")
	bashEnvironment := filepath.Join(temporary, "bash-env")
	if err := os.WriteFile(
		bashEnvironment,
		[]byte(`printf poisoned >"$ASCENDANY_TEST_BASH_ENV_MARKER"`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("../../../deploy/v2/scripts/preload-judge-image.sh")
	command.Env = append(
		os.Environ(),
		"BASH_ENV="+bashEnvironment,
		"ASCENDANY_TEST_BASH_ENV_MARKER="+marker,
		"SHELLOPTS=xtrace",
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("Judge image preloader unexpectedly accepted an empty invocation: %s", output)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("Judge image preloader executed inherited BASH_ENV: %v", err)
	}
}
