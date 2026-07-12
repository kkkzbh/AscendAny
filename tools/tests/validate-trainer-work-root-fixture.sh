#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
fixture_parent="$(mktemp -d)"
trap 'rm -rf -- "$fixture_parent"' EXIT
validator="$repo_root/deploy/v2/scripts/validate-trainer-host.sh"
trainer_unit="$repo_root/deploy/v2/systemd/ascendany-trainer-agent.service"

[[ "$(head -n 1 "$validator")" == '#!/usr/bin/bash -p' ]]
[[ "$(sed -n '2p' "$validator")" == 'set +x' ]]
printf '%s\n' 'touch "$BASH_ENV_MARKER"' >"$fixture_parent/bash-env"
if env \
    BASH_ENV="$fixture_parent/bash-env" \
    BASH_ENV_MARKER="$fixture_parent/bash-env-executed" \
    SHELLOPTS=xtrace \
    ASCENDANY_TRAINER_VALIDATION_PHASE=DO_NOT_TRACE_THIS_VALUE \
    "$validator" >"$fixture_parent/clean-env.out" 2>"$fixture_parent/clean-env.err"; then
  printf 'invalid clean-environment trainer validation unexpectedly passed\n' >&2
  exit 1
fi
[[ ! -e "$fixture_parent/bash-env-executed" ]]
if grep -F 'DO_NOT_TRACE_THIS_VALUE' "$fixture_parent/clean-env.out" "$fixture_parent/clean-env.err" >/dev/null; then
  printf 'trainer validator leaked its phase input through inherited tracing\n' >&2
  exit 1
fi
grep -Fx 'FAIL ASCENDANY_TRAINER_VALIDATION_PHASE must be exactly staged, production, or quiesced' \
  "$fixture_parent/clean-env.err" >/dev/null
if ASCENDANY_TRAINER_VALIDATION_PHASE='' "$validator" \
    >"$fixture_parent/empty-phase.out" 2>"$fixture_parent/empty-phase.err"; then
  printf 'empty trainer validation phase unexpectedly passed\n' >&2
  exit 1
fi
grep -Fx 'FAIL ASCENDANY_TRAINER_VALIDATION_PHASE must be exactly staged, production, or quiesced' \
  "$fixture_parent/empty-phase.err" >/dev/null
grep -F -- \
  "curl --disable --fail --silent --show-error --max-time 10 --noproxy '*' --proto '=https'" \
  "$validator" >/dev/null
grep -F 'trainer_endpoint="https://ascendany-trainer.kkkzbh.cn"' "$validator" >/dev/null
grep -F 'check_remote_ingress_closure' "$validator" >/dev/null
grep -F 'check_empty_acceptance_state' "$validator" >/dev/null
grep -Fx 'AssertPathIsDirectory=/opt/ascendany/Release' "$trainer_unit" >/dev/null
grep -Fx 'SystemCallFilter=~@clock' "$trainer_unit" >/dev/null
grep -Fx '  check_effective_value ProtectClock no' "$validator" >/dev/null
grep -Fx '  check_effective_value TimeoutStopFailureMode abort' "$validator" >/dev/null
if grep -q '^ProtectClock=' "$trainer_unit"; then
  printf 'trainer unit still enables ProtectClock and its implicit RTC DeviceAllow\n' >&2
  exit 1
fi
if grep -E '^(release_root|unit|environment_file|acceptance_evidence)=.*\$\{' "$validator" >/dev/null; then
  printf 'trainer validator still accepts caller-controlled authority or runtime paths\n' >&2
  exit 1
fi

# shellcheck source=../../deploy/v2/scripts/validate-trainer-host.sh
source "$validator"

trainer_uid="$(id -u)"
trainer_gid="$(id -g)"
invocation_name='123e4567-e89b-42d3-a456-426614174000-123456789'
mock_active_state=active
mock_sub_state=running
mock_unit_rendered=''
mock_nvidia_smi_mode=success
mock_enabled_state=disabled
mock_ingress_status=404
mock_read_only_paths='/etc/ascendany/v2 /opt/ascendany-trainer-runtime /opt/ascendany/v2/trainers/recommendation'
mock_read_write_paths='/var/lib/ascendany-trainer'
mock_inaccessible_paths='/etc/ascendany/credentials /opt/ascendany/Release /var/lib/ascendany/artifacts'
mock_dropin_paths="$global_service_dropin"
mock_device_allow=$'/dev/nvidia0 rw\n/dev/nvidiactl rw\n/dev/nvidia-uvm rw'

for phase_contract in \
    'staged:0:1:0:1:0:disabled' \
    'production:1:0:1:0:1:enabled' \
    'quiesced:0:1:1:0:1:enabled'; do
  IFS=: read -r validation_phase expected_active expected_quiesced expected_acceptance \
    expected_empty_acceptance expected_remote expected_enablement <<<"$phase_contract"
  failures=0
  validate_input_contract
  [[ "$failures:$require_active:$require_quiesced_work_root:$require_acceptance_evidence:$require_empty_acceptance:$require_remote_release:$required_enablement" == \
     "0:$expected_active:$expected_quiesced:$expected_acceptance:$expected_empty_acceptance:$expected_remote:$expected_enablement" ]]
done
printf 'PASS fixture trainer-phase-contracts\n'

systemctl() {
  if [[ "${1:-}" == "cat" && "${2:-}" == "$unit" && "$#" == 2 ]]; then
    printf '%s\n' "$mock_unit_rendered"
    return 0
  fi
  if [[ "${1:-}" == "is-enabled" && "${2:-}" == "$unit" && "$#" == 2 ]]; then
    printf '%s\n' "$mock_enabled_state"
    [[ "$mock_enabled_state" == "enabled" ]]
    return
  fi
  return 1
}

curl() {
  printf '%s' "$mock_ingress_status"
}

unit_property() {
  case "$1" in
    ActiveState) printf '%s\n' "$mock_active_state" ;;
    SubState) printf '%s\n' "$mock_sub_state" ;;
    ReadOnlyPaths) printf '%s\n' "$mock_read_only_paths" ;;
    ReadWritePaths) printf '%s\n' "$mock_read_write_paths" ;;
    InaccessiblePaths) printf '%s\n' "$mock_inaccessible_paths" ;;
    DropInPaths) printf '%s\n' "$mock_dropin_paths" ;;
    DeviceAllow) printf '%s\n' "$mock_device_allow" ;;
    *) return 1 ;;
  esac
}

nvidia-smi() {
  [[ "$*" == '--id=0 --query-gpu=index --format=csv,noheader,nounits' ]] || return 91
  case "$mock_nvidia_smi_mode" in
    success) printf '0\n' ;;
    wrong-index) printf '1\n' ;;
    multiline) printf '0\n1\n' ;;
    failure) return 92 ;;
    *) return 93 ;;
  esac
}

run_gpu_query_case() {
  local label="$1" mode="$2" expected="$3" actual=0
  mock_nvidia_smi_mode="$mode"
  if gpu_index_is_available 0; then
    actual=1
  fi
  if [[ "$actual" != "$expected" ]]; then
    printf 'fixture %s availability was %s; expected %s\n' "$label" "$actual" "$expected" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

new_work_root() {
  local name="$1"
  local root="$fixture_parent/$name"
  mkdir "$root"
  chmod 0700 "$root"
  printf '%s\n' "$root"
}

run_case() {
  local label="$1" expected_failures="$2" work_root="$3"
  failures=0
  check_work_root_structure "$work_root" "$trainer_uid" "$trainer_gid" \
    >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr"
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

run_command_case() {
  local label="$1" expected_failures="$2" rendered="$3"
  failures=0
  mock_unit_rendered="$rendered"
  check_effective_service_commands \
    >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr"
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

run_filesystem_path_case() {
  local label="$1" expected_failures="$2" read_only_paths="$3"
  failures=0
  mock_read_only_paths="$read_only_paths"
  check_effective_filesystem_paths \
    >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr"
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

run_dropin_set_case() {
  local label="$1" expected_failures="$2" dropin_paths="$3"
  failures=0
  mock_dropin_paths="$dropin_paths"
  check_effective_word_set DropInPaths "$global_service_dropin" \
    >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr"
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

run_global_dropin_bytes_case() {
  local label="$1" expected_failures="$2" contents="$3" path
  path="$fixture_parent/$label.conf"
  failures=0
  printf '%s' "$contents" >"$path"
  check_fedora_global_service_dropin_bytes "$path" \
    >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr"
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

run_device_allow_case() {
  local label="$1" expected_failures="$2" device_allow="$3"
  failures=0
  mock_device_allow="$device_allow"
  check_effective_gpu_device_allow \
    >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr" || true
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

run_enablement_case() {
  local label="$1" phase="$2" expected_enablement="$3" actual_enablement="$4" expected_failures="$5"
  failures=0
  validation_phase="$phase"
  required_enablement="$expected_enablement"
  mock_enabled_state="$actual_enablement"
  check_unit_enablement >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr"
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

run_ingress_case() {
  local label="$1" status="$2" expected_failures="$3"
  failures=0
  mock_ingress_status="$status"
  trainer_environment[ASCENDANY_TRAINER_AGENT_ENDPOINT]="$trainer_endpoint"
  check_remote_ingress_closure >"$fixture_parent/$label.stdout" 2>"$fixture_parent/$label.stderr"
  if [[ "$failures" != "$expected_failures" ]]; then
    printf 'fixture %s produced %d finding(s); expected %d\n' \
      "$label" "$failures" "$expected_failures" >&2
    return 1
  fi
  printf 'PASS fixture %s\n' "$label"
}

readonly exact_service_commands='[Service]
ExecStartPre=/usr/bin/test -s %d/trainer_agent_token
ExecStartPre=/usr/bin/test -c /dev/nvidia0
ExecStartPre=/usr/bin/test -c /dev/nvidiactl
ExecStartPre=/usr/bin/test -c /dev/nvidia-uvm
ExecStartPre=/opt/ascendany/v2/bin/ascendany-trainer-agent verify-runtime
ExecStart=/opt/ascendany/v2/bin/ascendany-trainer-agent run'
run_command_case exact-service-commands 0 "$exact_service_commands"
run_command_case replaced-exec-start 1 "${exact_service_commands%ExecStart=*}ExecStart=/tmp/unreviewed-trainer run"
run_command_case missing-runtime-attestation 1 '[Service]
ExecStartPre=/usr/bin/test -s %d/trainer_agent_token
ExecStartPre=/usr/bin/test -c /dev/nvidia0
ExecStartPre=/usr/bin/test -c /dev/nvidiactl
ExecStartPre=/usr/bin/test -c /dev/nvidia-uvm
ExecStart=/opt/ascendany/v2/bin/ascendany-trainer-agent run'
run_command_case reordered-exec-start-pre 1 '[Service]
ExecStartPre=/usr/bin/test -s %d/trainer_agent_token
ExecStartPre=/usr/bin/test -c /dev/nvidiactl
ExecStartPre=/usr/bin/test -c /dev/nvidia0
ExecStartPre=/usr/bin/test -c /dev/nvidia-uvm
ExecStartPre=/opt/ascendany/v2/bin/ascendany-trainer-agent verify-runtime
ExecStart=/opt/ascendany/v2/bin/ascendany-trainer-agent run'
run_filesystem_path_case exact-filesystem-paths 0 \
  '/etc/ascendany/v2 /opt/ascendany-trainer-runtime /opt/ascendany/v2/trainers/recommendation'
run_filesystem_path_case selector-only-read-only 1 \
  '/etc/ascendany/v2 /opt/ascendany-trainer-runtime/current /opt/ascendany/v2/trainers/recommendation'
run_dropin_set_case exact-global-service-dropin 0 "$global_service_dropin"
run_dropin_set_case missing-global-service-dropin 1 ''
run_dropin_set_case extra-service-dropin 1 \
  "$global_service_dropin /etc/systemd/system/ascendany-trainer-agent.service.d/override.conf"
run_global_dropin_bytes_case exact-global-service-dropin-bytes 0 \
  $'# Fedora package comment\n[Service]\nTimeoutStopFailureMode=abort\n'
run_global_dropin_bytes_case drifted-global-service-dropin-bytes 1 \
  $'[Service]\nTimeoutStopFailureMode=abort\nNoNewPrivileges=yes\n'

declare -A trainer_environment=()
trainer_environment[ASCENDANY_TRAINER_AGENT_NVIDIA_DEVICE_PATHS]='/dev/nvidia0,/dev/nvidiactl,/dev/nvidia-uvm'
run_device_allow_case exact-nvidia-device-allow 0 \
  $'/dev/nvidia0 rw\n/dev/nvidiactl rw\n/dev/nvidia-uvm rw'
run_device_allow_case implicit-rtc-device-allow 1 \
  $'char-rtc r\n/dev/nvidia0 rw\n/dev/nvidiactl rw\n/dev/nvidia-uvm rw'
run_enablement_case staged-disabled staged disabled disabled 0
run_enablement_case staged-enabled staged disabled enabled 1
run_enablement_case production-enabled production enabled enabled 0
run_enablement_case production-disabled production enabled disabled 1
run_enablement_case quiesced-enabled quiesced enabled enabled 0
run_enablement_case quiesced-disabled quiesced enabled disabled 1
run_ingress_case ingress-closed 404 0
run_ingress_case ingress-exposes-livez 200 1

empty_acceptance="$fixture_parent/empty-acceptance"
mkdir -m 0700 "$empty_acceptance"
acceptance_state_is_empty "$empty_acceptance" "$fixture_parent/promoted-evidence.json"
printf '{}\n' >"$empty_acceptance/trainer-latest.json"
if acceptance_state_is_empty "$empty_acceptance" "$fixture_parent/promoted-evidence.json"; then
  printf 'fixture candidate-present acceptance state unexpectedly passed\n' >&2
  exit 1
fi
rm -f -- "$empty_acceptance/trainer-latest.json"
ln -s "$fixture_parent/missing-evidence-target" "$fixture_parent/promoted-evidence.json"
if acceptance_state_is_empty "$empty_acceptance" "$fixture_parent/promoted-evidence.json"; then
  printf 'fixture linked promoted evidence unexpectedly passed\n' >&2
  exit 1
fi
printf 'PASS fixture staged-acceptance-absence\n'

run_gpu_query_case nvidia-query-success success 1
run_gpu_query_case nvidia-query-wrong-index wrong-index 0
run_gpu_query_case nvidia-query-multiline multiline 0
run_gpu_query_case nvidia-query-failure failure 0

require_quiesced_work_root=0
online_active="$(new_work_root online-active)"
mkdir -m 0700 "$online_active/$invocation_name"
mkdir -m 0700 "$online_active/$invocation_name/output"
printf '{}\n' >"$online_active/$invocation_name/output/output.json"
chmod 0600 "$online_active/$invocation_name/output/output.json"
run_case online-active-invocation 0 "$online_active"

online_empty="$(new_work_root online-empty)"
run_case online-empty 0 "$online_empty"

unsafe_mode="$(new_work_root unsafe-mode)"
mkdir -m 0755 "$unsafe_mode/$invocation_name"
run_case unsafe-invocation-mode 1 "$unsafe_mode"

linked_output="$(new_work_root linked-output)"
mkdir -m 0700 "$linked_output/$invocation_name"
mkdir -m 0700 "$linked_output/$invocation_name/output"
printf '{}\n' >"$fixture_parent/external-output.json"
chmod 0600 "$fixture_parent/external-output.json"
ln -s "$fixture_parent/external-output.json" "$linked_output/$invocation_name/output/output.json"
run_case linked-output 1 "$linked_output"

hardlinked_output="$(new_work_root hardlinked-output)"
mkdir -m 0700 "$hardlinked_output/$invocation_name"
mkdir -m 0700 "$hardlinked_output/$invocation_name/output"
printf '{}\n' >"$fixture_parent/hardlink-source.json"
chmod 0600 "$fixture_parent/hardlink-source.json"
ln "$fixture_parent/hardlink-source.json" "$hardlinked_output/$invocation_name/output/output.json"
run_case hardlinked-output 1 "$hardlinked_output"

require_quiesced_work_root=1
mock_active_state=inactive
mock_sub_state=dead
quiesced_empty="$(new_work_root quiesced-empty)"
run_case quiesced-empty 0 "$quiesced_empty"

quiesced_retained="$(new_work_root quiesced-retained)"
mkdir -m 0700 "$quiesced_retained/$invocation_name"
run_case quiesced-retained-invocation 1 "$quiesced_retained"

mock_active_state=active
mock_sub_state=running
quiesced_active="$(new_work_root quiesced-active)"
run_case quiesced-active-unit 1 "$quiesced_active"

printf 'trainer work-root fixtures passed.\n'
