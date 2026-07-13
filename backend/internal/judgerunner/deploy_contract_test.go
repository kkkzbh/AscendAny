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
	Schema string `json:"schema"`
	Build  struct {
		BuildahVersion      string `json:"buildahVersion"`
		ContainerfilePath   string `json:"containerfilePath"`
		ContainerfileSHA256 string `json:"containerfileSHA256"`
		Format              string `json:"format"`
		PodmanVersion       string `json:"podmanVersion"`
		SourceDateEpoch     int64  `json:"sourceDateEpoch"`
	} `json:"build"`
	Compiler struct {
		Architecture      string   `json:"architecture"`
		ConfigDigest      string   `json:"configDigest"`
		ConfigSize        int64    `json:"configSize"`
		Identity          string   `json:"identity"`
		ManifestMediaType string   `json:"manifestMediaType"`
		ManifestSize      int64    `json:"manifestSize"`
		OS                string   `json:"os"`
		Packages          []string `json:"packages"`
		RootFS            struct {
			EntryCount      int64  `json:"entryCount"`
			InventoryPath   string `json:"inventoryPath"`
			InventorySHA256 string `json:"inventorySHA256"`
			LayerDigest     string `json:"layerDigest"`
			LayerMediaType  string `json:"layerMediaType"`
			LayerSize       int64  `json:"layerSize"`
		} `json:"rootfs"`
		Toolchain struct {
			Compiler       string `json:"compiler"`
			Package        string `json:"package"`
			PackageVersion string `json:"packageVersion"`
			Version        string `json:"version"`
		} `json:"toolchain"`
	} `json:"compiler"`
	Runtime struct {
		Architecture      string `json:"architecture"`
		ConfigDigest      string `json:"configDigest"`
		ConfigSize        int64  `json:"configSize"`
		Identity          string `json:"identity"`
		ManifestMediaType string `json:"manifestMediaType"`
		ManifestSize      int64  `json:"manifestSize"`
		OS                string `json:"os"`
		RootFS            struct {
			EntryCount      int64  `json:"entryCount"`
			InventorySHA256 string `json:"inventorySHA256"`
			LayerDigest     string `json:"layerDigest"`
			LayerMediaType  string `json:"layerMediaType"`
			LayerSize       int64  `json:"layerSize"`
		} `json:"rootfs"`
	} `json:"runtime"`
	Source struct {
		Architecture         string `json:"architecture"`
		ConfigDigest         string `json:"configDigest"`
		ConfigSize           int64  `json:"configSize"`
		Index                string `json:"index"`
		IndexMediaType       string `json:"indexMediaType"`
		IndexSize            int64  `json:"indexSize"`
		Leaf                 string `json:"leaf"`
		ManifestMediaType    string `json:"manifestMediaType"`
		ManifestSize         int64  `json:"manifestSize"`
		OS                   string `json:"os"`
		Release              string `json:"release"`
		RootFSLayerDigest    string `json:"rootfsLayerDigest"`
		RootFSLayerMediaType string `json:"rootfsLayerMediaType"`
		RootFSLayerSize      int64  `json:"rootfsLayerSize"`
	} `json:"source"`
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
	digest := regexp.MustCompile(`^localhost/ascendany-judge-(compiler|runtime)@sha256:[0-9a-f]{64}$`)
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if lock.Schema != "ascendany.judge-image-lock.v2" ||
		lock.Build.ContainerfilePath != "config/judge-images.Containerfile" || !hex.MatchString(lock.Build.ContainerfileSHA256) ||
		lock.Build.Format != "oci" || lock.Build.SourceDateEpoch != 0 ||
		!digest.MatchString(lock.Compiler.Identity) || !digest.MatchString(lock.Runtime.Identity) ||
		lock.Compiler.Identity == lock.Runtime.Identity || lock.Compiler.OS != "linux" || lock.Runtime.OS != "linux" ||
		lock.Compiler.Architecture != "amd64" || lock.Runtime.Architecture != "amd64" ||
		lock.Compiler.Toolchain.Compiler != cpp20Compiler || lock.Compiler.Toolchain.PackageVersion != "15.2.0-r2" ||
		lock.Compiler.Toolchain.Version != "15.2.0" || lock.Compiler.RootFS.EntryCount != 2681 ||
		!hex.MatchString(lock.Compiler.RootFS.InventorySHA256) || lock.Runtime.RootFS.EntryCount != 0 ||
		lock.Runtime.RootFS.InventorySHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" ||
		lock.Source.Release != "3.23.5" || !strings.Contains(lock.Source.Leaf, "docker.io/library/alpine@sha256:") {
		t.Fatalf("judge image lock differs from the reviewed two-image closure: %#v", lock)
	}
	environment, err := os.ReadFile("../../../deploy/v2/config/judge.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(environment), "ASCENDANY_JUDGE_COMPILER_IMAGE="+lock.Compiler.Identity+"\n") ||
		!strings.Contains(string(environment), "ASCENDANY_JUDGE_RUNTIME_IMAGE="+lock.Runtime.Identity+"\n") {
		t.Fatal("production Judge environment does not select both locked image identities")
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
