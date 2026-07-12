package traineragent

import (
	"os"
	"strings"
	"testing"
)

func TestTrainerAgentDeploymentPreservesRemoteWorkerBoundary(t *testing.T) {
	t.Parallel()
	unit := readTrainerAgentContractFile(t, "../../../deploy/v2/systemd/ascendany-trainer-agent.service")
	for _, required := range []string{
		"User=ascendany-trainer",
		"LoadCredentialEncrypted=trainer_agent_token:",
		"Environment=ASCENDANY_TRAINER_AGENT_TOKEN_FILE=%d/trainer_agent_token",
		"ExecStartPre=/opt/ascendany/v2/bin/ascendany-trainer-agent verify-runtime",
		"ExecStart=/opt/ascendany/v2/bin/ascendany-trainer-agent run",
		"RequiresMountsFor=/opt/ascendany-trainer-runtime/current",
		"AssertFileIsExecutable=/opt/ascendany-trainer-runtime/current/python/bin/python3.14",
		"AssertPathIsDirectory=/var/lib/ascendany-trainer/acceptance",
		"NoNewPrivileges=yes",
		"DevicePolicy=closed",
		"InaccessiblePaths=/etc/ascendany/credentials /opt/ascendany/Release /var/lib/ascendany/artifacts",
	} {
		if !strings.Contains(unit, required) {
			t.Fatalf("trainer-agent unit is missing %q", required)
		}
	}
	if strings.Count(unit, "LoadCredentialEncrypted=") != 1 {
		t.Fatalf("trainer-agent unit credential count = %d", strings.Count(unit, "LoadCredentialEncrypted="))
	}
	for _, forbidden := range []string{
		"ASCENDANY_DATABASE",
		"PGHOST",
		"PGPORT",
		"PrivateNetwork=yes",
		"ReadWritePaths=/var/lib/ascendany/artifacts",
		"SupplementaryGroups=",
	} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("trainer-agent unit contains forbidden boundary marker %q", forbidden)
		}
	}

	environment := readTrainerAgentContractFile(t, "../../../deploy/v2/config/trainer-agent.env.example")
	for _, forbidden := range []string{"DATABASE", "PGHOST", "PGPORT", "ARTIFACT_ROOT", "MODEL_API_KEY"} {
		if strings.Contains(environment, forbidden) {
			t.Fatalf("trainer-agent environment contains forbidden capability marker %q", forbidden)
		}
	}
	if strings.Contains(environment, "ASCENDANY_TRAINER_AGENT_TOKEN_FILE=") {
		t.Fatal("trainer token path must be injected by systemd, not stored in the env file")
	}
	if strings.Count(environment, "ASCENDANY_TRAINER_AGENT_ENDPOINT=") != 1 ||
		!strings.Contains(
			environment,
			"ASCENDANY_TRAINER_AGENT_ENDPOINT=https://ascendany-trainer.kkkzbh.cn",
		) || strings.Contains(environment, "ASCENDANY_TRAINER_AGENT_ENDPOINT=https://ascendany.kkkzbh.cn") {
		t.Fatal("trainer deployment must use one dedicated cutover-independent HTTPS origin")
	}
	if strings.Count(environment, "ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH=") != 1 ||
		!strings.Contains(
			environment,
			"ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH=/var/lib/ascendany-trainer/acceptance/trainer-latest.json",
		) {
		t.Fatal("trainer environment must bind one exact acceptance candidate path")
	}
	if !strings.Contains(environment, "ASCENDANY_TRAINER_AGENT_RUNTIME_ROOT=/opt/ascendany-trainer-runtime/current") ||
		!strings.Contains(environment, "ASCENDANY_TRAINER_AGENT_PYTHON=/opt/ascendany-trainer-runtime/current/python/bin/python3.14") ||
		!strings.Contains(environment, "ASCENDANY_TRAINER_AGENT_RUNTIME_PATHS=/lib,/lib64,/opt/ascendany-trainer-runtime/current,/sys") ||
		!strings.Contains(environment, "ASCENDANY_TRAINER_AGENT_NVIDIA_DEVICE_PATHS=/dev/nvidia-uvm,/dev/nvidia0,/dev/nvidiactl") ||
		strings.Contains(environment, ",/sys,/usr") {
		t.Fatal("trainer environment must select portable CPython and omit /usr from the child mount set")
	}

	sysusers := readTrainerAgentContractFile(t, "../../../deploy/v2/sysusers.d/ascendany-v2.conf")
	if strings.Contains(sysusers, "m ascendany-trainer") {
		t.Fatal("trainer identity must not inherit a runtime, database, or artifact group")
	}
	tmpfiles := readTrainerAgentContractFile(t, "../../../deploy/v2/tmpfiles.d/ascendany-v2.conf")
	if !strings.Contains(
		tmpfiles,
		"d /var/lib/ascendany-trainer/acceptance      0700 ascendany-trainer    ascendany-trainer    -   -",
	) {
		t.Fatal("tmpfiles must create the exact private trainer acceptance directory")
	}

	transportContract := readTrainerAgentContractFile(t, "../../../deploy/v2/TRAINER_AGENT_CONTRACT.md")
	for _, required := range []string{
		"`https://ascendany-trainer.kkkzbh.cn`",
		"path `^/version$`, service",
		"`^/api/v2/internal/recommendation/trainer-agent/claims(/.*)?$`",
		"`http://127.0.0.1:18000`",
		"`http_status:404`",
		"`staged` requires the unit to\nbe disabled, inactive, and dead",
		"`production` requires the unit to be enabled, active, and",
		"`quiesced`\nrequires the unit to remain enabled while inactive and dead",
	} {
		if !strings.Contains(transportContract, required) {
			t.Fatalf("trainer transport deployment contract is missing %q", required)
		}
	}
}

func TestTrainerRuntimeProvisioningIsOfflineAttestedAndAtomicallySelected(t *testing.T) {
	t.Parallel()
	installer := readTrainerAgentContractFile(t, "../../../deploy/v2/scripts/install-trainer-runtime.sh")
	for _, required := range []string{
		"readonly uv_version='uv 0.9.26'",
		"readonly uv_archive_url='https://github.com/astral-sh/uv/releases/download/0.9.26/uv-x86_64-unknown-linux-gnu.tar.gz'",
		"readonly uv_archive_sha256=30ccbf0a66dc8727a02b0e245c583ee970bdafecf3a443c1686e1b30ec4939e8",
		"readonly uv_binary_sha256=0650696de7f403348e9dd617e1f65dc32147c106c40129138017efd8f0f01cc8",
		"readonly sandbox_bin=/usr/bin/bwrap",
		"readonly runtime_selector=\"$runtime_parent/current\"",
		"runtime-wheels-cu130.json",
		"ascendany.trainer-runtime.wheels.v1",
		"/usr/bin/flock --exclusive --nonblock",
		"ascendany.trainer-runtime.stage-owner.v1",
		"--unshare-all",
		"--offline",
		"--no-index",
		"--find-links /wheelhouse",
		"--no-managed-python",
		"--no-python-downloads",
		"private wheelhouse has a missing, extra, or special entry",
		"ascendany.trainer-runtime.provenance.v3",
		"wheelsSha256",
		"run_runtime_attestation",
		"runtimeAttestationSha256",
		"mv --no-target-directory --no-clobber -- \"$runtime_stage\" \"$runtime_root\"",
		"ln -s -- \"${runtime_root##*/}\" \"$selector_stage\"",
		"mv --no-target-directory -- \"$selector_stage\" \"$runtime_selector\"",
	} {
		if !strings.Contains(installer, required) {
			t.Fatalf("trainer runtime provisioning is missing %q", required)
		}
	}
	if strings.Count(installer, "run_runtime_attestation") < 3 {
		t.Fatal("runtime must be attested as staged, published, and through the selected construction")
	}
	helpersEnd := strings.Index(installer, "if [[ \"${BASH_SOURCE[0]}\" != \"$0\" ]]")
	if helpersEnd < 0 {
		t.Fatal("runtime installer helper boundary is missing")
	}
	helpers := installer[:helpersEnd]
	if strings.Contains(helpers, "--ro-bind /usr /usr") || strings.Contains(helpers, "/run/credentials") {
		t.Fatal("portable Python and attestation helpers expose a forbidden host capability")
	}
	formalSyncStart := strings.Index(installer, "/usr/bin/bwrap \\\n  --unshare-all")
	formalSyncEnd := strings.Index(installer, "rm -rf --one-file-system -- \"$wheelhouse\"")
	if formalSyncStart < 0 || formalSyncEnd <= formalSyncStart {
		t.Fatal("offline wheel sync boundary is missing")
	}
	formalSync := installer[formalSyncStart:formalSyncEnd]
	if strings.Contains(formalSync, "--ro-bind /usr /usr") || !strings.Contains(formalSync, "--clearenv") {
		t.Fatal("offline wheel sync does not use the closed no-/usr namespace")
	}
	if strings.Contains(installer, "/usr/local/bin/uv") || strings.Contains(installer, "pip sync --index") {
		t.Fatal("runtime installer retains an unreviewed host uv or online sync path")
	}

	hostIdentity := readTrainerAgentContractFile(t, "../../../deploy/v2/scripts/trainer-host-capability-identity.sh")
	for _, required := range []string{
		"readonly sandbox_runtime_root=/opt/ascendany-trainer-runtime/current",
		"--ro-bind \"$runtime_root\" \"$sandbox_runtime_root\"",
		"--dev-bind /dev/nvidia-uvm /dev/nvidia-uvm",
		"readonly driver_version_path=/sys/module/nvidia/version",
	} {
		if !strings.Contains(hostIdentity, required) {
			t.Fatalf("trainer host capability identity is missing %q", required)
		}
	}
	if strings.Contains(hostIdentity, "--ro-bind /usr /usr") || strings.Contains(hostIdentity, "nvidia-smi") {
		t.Fatal("trainer host capability identity exposes an unreviewed host runtime path")
	}

	validator := readTrainerAgentContractFile(t, "../../../deploy/v2/scripts/validate-trainer-host.sh")
	if strings.Count(validator, "check_trainer_runtime()") != 1 {
		t.Fatal("trainer host validator must own one runtime-validation implementation")
	}
	for _, required := range []string{
		"runtime-wheels-cu130.json",
		"run_runtime_attestation",
		"runtimeAttestationSha256",
		"trainer runtime selector target is not construction-addressed",
		"selected trainer runtime failed the production child attestation",
	} {
		if !strings.Contains(validator, required) {
			t.Fatalf("trainer host validator is missing %q", required)
		}
	}

	readme := readTrainerAgentContractFile(t, "../../../deploy/v2/README.md")
	if !strings.Contains(readme, "/opt/ascendany/v2/scripts/install-trainer-runtime.sh") {
		t.Fatal("deployment runbook does not select the reviewed runtime installer")
	}
}

func readTrainerAgentContractFile(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
