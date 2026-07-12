#!/usr/bin/bash -p
set +x
set -Eeuo pipefail

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  validator_environment_is_clean=1
  while IFS= read -r -d '' entry; do
    name="${entry%%=*}"
    case "$name" in
      PATH|LC_ALL|PWD|SHLVL|_|ASCENDANY_TRAINER_VALIDATOR_CLEAN_ENV|ASCENDANY_TRAINER_VALIDATION_PHASE)
        ;;
      *)
        validator_environment_is_clean=0
        ;;
    esac
  done < <(/usr/bin/env -0)
  if [[ "${ASCENDANY_TRAINER_VALIDATOR_CLEAN_ENV-}" != "1" ||
        "${PATH-}" != "/usr/bin:/bin" || "${LC_ALL-}" != "C" ||
        "$validator_environment_is_clean" != "1" ]]; then
    validation_phase_input="${ASCENDANY_TRAINER_VALIDATION_PHASE-}"
    exec /usr/bin/env -i \
      PATH=/usr/bin:/bin \
      LC_ALL=C \
      ASCENDANY_TRAINER_VALIDATOR_CLEAN_ENV=1 \
      "ASCENDANY_TRAINER_VALIDATION_PHASE=$validation_phase_input" \
      /usr/bin/bash -p "$0" "$@"
  fi
fi

umask 077

release_root="/opt/ascendany/v2"
unit="ascendany-trainer-agent.service"
environment_file="/etc/ascendany/v2/trainer-agent.env"
acceptance_evidence="/var/lib/ascendany-acceptance/trainer-latest.json"
trainer_endpoint="https://ascendany-trainer.kkkzbh.cn"
trainer_checkout_root="/opt/ascendany/Release"
trainer_runtime_parent="/opt/ascendany-trainer-runtime"
trainer_runtime_root="$trainer_runtime_parent/current"
trainer_python="$trainer_runtime_root/python/bin/python3.14"
trainer_runtime_lock_relative="trainers/recommendation/runtime-requirements-cu130.lock"
trainer_runtime_closure_relative="trainers/recommendation/runtime-closure-cu130.json"
trainer_runtime_wheels_relative="trainers/recommendation/runtime-wheels-cu130.json"
trainer_runtime_python_source_relative="trainers/recommendation/runtime-python-cu130.json"
trainer_runtime_installer_relative="scripts/install-trainer-runtime.sh"
trainer_runtime_tree_identity_relative="scripts/trainer-runtime-tree-identity.sh"
trainer_host_capability_identity_relative="scripts/trainer-host-capability-identity.sh"
trainer_runtime_inputs="$trainer_runtime_root/.ascendany-construction-inputs"
trainer_runtime_marker="$trainer_runtime_root/.ascendany-runtime-provenance.json"
trainer_runtime_source_manifest="$trainer_runtime_inputs/release-manifest.json"
trainer_runtime_captured_lock="$trainer_runtime_inputs/runtime-requirements-cu130.lock"
trainer_runtime_captured_closure="$trainer_runtime_inputs/runtime-closure-cu130.json"
trainer_runtime_captured_wheels="$trainer_runtime_inputs/runtime-wheels-cu130.json"
trainer_runtime_captured_python_source="$trainer_runtime_inputs/runtime-python-cu130.json"
trainer_runtime_captured_installer="$trainer_runtime_inputs/install-trainer-runtime.sh"
trainer_runtime_captured_tree_identity="$trainer_runtime_inputs/trainer-runtime-tree-identity.sh"
trainer_runtime_captured_host_capability="$trainer_runtime_inputs/trainer-host-capability-identity.sh"
trainer_runtime_captured_uv="$trainer_runtime_inputs/uv"
global_service_dropin="/usr/lib/systemd/system/service.d/10-timeout-abort.conf"
trainer_torch_version="2.13.0+cu130"
trainer_cuda_version="13.0"
trainer_python_version="3.14.6"
validation_phase="${ASCENDANY_TRAINER_VALIDATION_PHASE-}"
require_active="0"
require_quiesced_work_root="0"
require_acceptance_evidence="0"
require_empty_acceptance="0"
require_remote_release="0"
required_enablement=""
release_payload_verified="0"

failures=0

pass() {
  printf 'PASS %s\n' "$*"
}

fail() {
  printf 'FAIL %s\n' "$*" >&2
  failures=$((failures + 1))
}

validate_input_contract() {
  case "$validation_phase" in
    staged)
      require_active="0"
      require_quiesced_work_root="1"
      require_acceptance_evidence="0"
      require_empty_acceptance="1"
      require_remote_release="0"
      required_enablement="disabled"
      ;;
    production)
      require_active="1"
      require_quiesced_work_root="0"
      require_acceptance_evidence="1"
      require_empty_acceptance="0"
      require_remote_release="1"
      required_enablement="enabled"
      ;;
    quiesced)
      require_active="0"
      require_quiesced_work_root="1"
      require_acceptance_evidence="1"
      require_empty_acceptance="0"
      require_remote_release="1"
      required_enablement="enabled"
      ;;
    *)
      fail "ASCENDANY_TRAINER_VALIDATION_PHASE must be exactly staged, production, or quiesced"
      return 1
      ;;
  esac
}

unit_property() {
  local property="$1"
  systemctl show "$unit" --property="$property" --value 2>/dev/null
}

normalize_word_set() {
  tr '[:space:]' '\n' | sed '/^$/d' | LC_ALL=C sort
}

check_effective_value() {
  local property="$1" expected="$2" actual
  if ! actual="$(unit_property "$property")"; then
    fail "$unit effective $property cannot be read"
  elif [[ "$actual" != "$expected" ]]; then
    fail "$unit effective $property is ${actual:-<empty>}; expected ${expected:-<empty>}"
  else
    pass "$unit effective $property is ${expected:-empty}"
  fi
}

check_effective_word_set() {
  local property="$1"
  shift
  local actual expected
  if ! actual="$(unit_property "$property")"; then
    fail "$unit effective $property cannot be read"
    return
  fi
  expected="$(printf '%s\n' "$@")"
  if [[ "$(normalize_word_set <<<"$actual")" != "$(normalize_word_set <<<"$expected")" ]]; then
    fail "$unit effective $property set differs from the trainer contract"
  else
    pass "$unit effective $property set matches the trainer contract"
  fi
}

is_under() {
  local child parent
  child="$(realpath -m -- "$1")"
  parent="$(realpath -m -- "$2")"
  [[ "$child" == "$parent" || "$child" == "$parent"/* ]]
}

check_root_owned_ancestry() {
  local path="$1" require_root_group="$2" current metadata owner group mode
  if [[ "$path" != /* || "$path" != "$(realpath -m -- "$path")" ||
        ! -e "$path" || "$path" != "$(realpath -e -- "$path" 2>/dev/null || true)" ]]; then
    return 1
  fi
  current="$(dirname -- "$path")"
  while :; do
    [[ ! -L "$current" && -d "$current" ]] || return 1
    metadata="$(stat -c '%u:%g:%a' "$current" 2>/dev/null)" || return 1
    IFS=: read -r owner group mode <<<"$metadata"
    [[ "$owner" == "0" ]] || return 1
    if [[ "$require_root_group" == "1" && "$group" != "0" ]]; then
      return 1
    fi
    if (( 8#$mode & 8#022 )); then
      return 1
    fi
    [[ "$current" == "/" ]] && break
    current="$(dirname -- "$current")"
  done
}

check_fedora_global_service_dropin_bytes() {
  local dropin="$1" directives
  directives="$(
    sed -E \
      -e '/^[[:space:]]*#/d' \
      -e '/^[[:space:]]*$/d' \
      -e 's/^[[:space:]]+//' \
      -e 's/[[:space:]]+$//' \
      "$dropin" 2>/dev/null
  )"
  if [[ "$directives" != $'[Service]\nTimeoutStopFailureMode=abort' ]]; then
    fail "Fedora global service drop-in has directives outside the reviewed timeout-abort contract"
  else
    pass "Fedora global service drop-in has only the reviewed timeout-abort directive"
  fi
}

check_fedora_global_service_dropin() {
  if [[ ! -f "$global_service_dropin" || -L "$global_service_dropin" ||
        "$global_service_dropin" != "$(realpath -m -- "$global_service_dropin")" ||
        "$global_service_dropin" != "$(realpath -e -- "$global_service_dropin" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a:%h' "$global_service_dropin" 2>/dev/null || true)" != "0:0:644:1" ]] ||
     ! check_root_owned_ancestry "$global_service_dropin" 1; then
    fail "Fedora global service drop-in must be a canonical root:root 0644 single-link file with protected ancestry"
  else
    check_fedora_global_service_dropin_bytes "$global_service_dropin"
  fi
}

check_protected_trainer_checkout() {
  if [[ ! -d "$trainer_checkout_root" || -L "$trainer_checkout_root" ||
        "$trainer_checkout_root" != "$(realpath -m -- "$trainer_checkout_root")" ||
        "$trainer_checkout_root" != "$(realpath -e -- "$trainer_checkout_root" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a' "$trainer_checkout_root" 2>/dev/null || true)" != "0:0:755" ]] ||
     ! check_root_owned_ancestry "$trainer_checkout_root" 1; then
    fail "trainer reviewed checkout must remain a canonical protected root:root 0755 directory"
  else
    pass "trainer reviewed checkout is present at the fixed protected path"
  fi
}

collect_effective_directives() {
  local rendered="$1" directive="$2" output_name="$3" line value section=""
  local -n output="$output_name"
  output=()
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    if [[ "$line" =~ ^\[([A-Za-z]+)\]$ ]]; then
      section="${BASH_REMATCH[1]}"
      continue
    fi
    [[ "$section" == "Service" ]] || continue
    [[ "$line" =~ ^${directive}[[:space:]]*=(.*)$ ]] || continue
    value="${BASH_REMATCH[1]}"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    if [[ -z "$value" ]]; then
      output=()
    else
      output+=("$value")
    fi
  done <<<"$rendered"
}

check_unit_credential() {
  local rendered entry credential_id source
  local -a plaintext=() encrypted=()
  if ! rendered="$(systemctl cat "$unit" 2>/dev/null)" || [[ -z "$rendered" ]]; then
    fail "$unit configuration cannot be read for credential validation"
    return
  fi
  collect_effective_directives "$rendered" LoadCredential plaintext
  collect_effective_directives "$rendered" LoadCredentialEncrypted encrypted
  if (( ${#plaintext[@]} != 0 )); then
    fail "$unit has an effective plaintext LoadCredential directive"
  else
    pass "$unit has no effective plaintext LoadCredential directive"
  fi
  if (( ${#encrypted[@]} != 1 )); then
    fail "$unit must load exactly one encrypted trainer_agent_token credential"
    return
  fi
  entry="${encrypted[0]}"
  credential_id="${entry%%:*}"
  source="${entry#*:}"
  if [[ "$credential_id" != "trainer_agent_token" || "$source" == "$entry" ||
        "$source" != /* || ! -s "$source" || ! -f "$source" || -L "$source" ||
        "$(stat -c '%u:%g:%a' "$source" 2>/dev/null || true)" != "0:0:400" ||
        "$source" != "$(realpath -m -- "$source")" ||
        "$source" != "$(realpath -e -- "$source" 2>/dev/null || true)" ]] ||
     ! check_root_owned_ancestry "$source" 1 || is_under "$source" "$release_root"; then
    fail "$unit encrypted trainer credential must be a protected external root:root 0400 file"
  else
    pass "$unit effective encrypted credential ID and protected source are exact"
  fi
}

check_effective_service_commands() {
  local rendered
  local -a exec_start=() exec_start_pre=()
  local -a expected_exec_start=(
    "/opt/ascendany/v2/bin/ascendany-trainer-agent run"
  )
  local -a expected_exec_start_pre=(
    "/usr/bin/test -s %d/trainer_agent_token"
    "/usr/bin/test -c /dev/nvidia0"
    "/usr/bin/test -c /dev/nvidiactl"
    "/usr/bin/test -c /dev/nvidia-uvm"
    "/opt/ascendany/v2/bin/ascendany-trainer-agent verify-runtime"
  )

  if ! rendered="$(systemctl cat "$unit" 2>/dev/null)" || [[ -z "$rendered" ]]; then
    fail "$unit configuration cannot be read for command validation"
    return
  fi
  collect_effective_directives "$rendered" ExecStart exec_start
  collect_effective_directives "$rendered" ExecStartPre exec_start_pre
  if (( ${#exec_start[@]} != ${#expected_exec_start[@]} )) ||
     [[ "$(printf '%s\n' "${exec_start[@]}")" != "$(printf '%s\n' "${expected_exec_start[@]}")" ]]; then
    fail "$unit effective ExecStart differs from the exact trainer-agent run command"
  else
    pass "$unit effective ExecStart is exact"
  fi
  if (( ${#exec_start_pre[@]} != ${#expected_exec_start_pre[@]} )) ||
     [[ "$(printf '%s\n' "${exec_start_pre[@]}")" != "$(printf '%s\n' "${expected_exec_start_pre[@]}")" ]]; then
    fail "$unit effective ExecStartPre sequence differs from the exact credential and GPU gates"
  else
    pass "$unit effective ExecStartPre sequence is exact"
  fi
}

check_effective_environment_file() {
  local raw expected
  expected="$environment_file (ignore_errors=no)"
  if ! raw="$(unit_property EnvironmentFiles)"; then
    fail "$unit effective EnvironmentFiles cannot be read"
  elif [[ "$(sed '/^$/d' <<<"$raw")" != "$expected" ]]; then
    fail "$unit effective EnvironmentFiles set differs from the exact trainer contract"
  else
    pass "$unit effective EnvironmentFiles set is exact"
  fi
}

check_release_contract() {
  local manifest="$release_root/release-manifest.json"
  local -a required_paths=(
    bin/ascendanyd
    bin/ascendany-admin-bootstrap
    bin/ascendany-backup
    bin/ascendany-judge
    bin/ascendany-lsp
    bin/ascendany-migrate
    bin/ascendany-release-ops
    bin/ascendany-trainer-agent
    trainers/recommendation/ascendany_recommendation_trainer/__init__.py
    trainers/recommendation/ascendany_recommendation_trainer/__main__.py
    trainers/recommendation/ascendany_recommendation_trainer/attestation.py
    trainers/recommendation/ascendany_recommendation_trainer/cli.py
    trainers/recommendation/ascendany_recommendation_trainer/contract.py
    trainers/recommendation/ascendany_recommendation_trainer/model.py
    trainers/recommendation/ascendany_recommendation_trainer/train.py
    trainers/recommendation/runtime-closure-cu130.json
    trainers/recommendation/runtime-python-cu130.json
    trainers/recommendation/runtime-requirements-cu130.lock
    trainers/recommendation/runtime-wheels-cu130.json
    README.md
    OJ_JUDGE_CONTRACT.md
    LSP_CONTROL_CONTRACT.md
    TRAINER_AGENT_CONTRACT.md
    contracts/openapi/ascendany-v2.yaml
    contracts/pintia/ascendany.pintia.snapshot.v2.schema.json
    db/roles/README.md
    db/roles/001_v2_roles.sql
    db/roles/verify_v2_roles.sql
    config/analytics.json
    config/ascendanyd.env
    config/ascendanyd-read-only-smoke.env
    config/backup.env
    config/cloudflared.yaml
    config/fedora-runtime-packages.json
    config/judge.env
    config/judge-image-lock.json
    config/migrate.env
    config/pgbouncer-hba.conf
    config/pgbouncer.ini
    config/postgresql-hba-bootstrap.conf
    config/postgresql-hba.conf
    config/postgresql-ident-bootstrap.conf
    config/postgresql-ident.conf
    config/restore.env
    config/trainer-agent.env
    systemd/ascendanyd.service
    systemd/ascendanyd.service.d/40-read-only-smoke.conf
    systemd/ascendany-admin-bootstrap.service
    systemd/ascendany-backup.service
    systemd/ascendany-backup.timer
    systemd/ascendany-cloudflared.service
    systemd/ascendany-judge@.service
    systemd/ascendany-lsp@.service
    systemd/ascendany-migrate.service
    systemd/ascendany-pgbouncer.service
    systemd/ascendany-restore-verify@.service
    systemd/ascendany-trainer-agent.service
    polkit-1/rules.d/60-ascendany-judge.rules
    polkit-1/rules.d/61-ascendany-lsp.rules
    sysusers.d/ascendany-v2.conf
    tmpfiles.d/ascendany-v2.conf
    scripts/publish-restore-evidence.sh
    scripts/restore-verify-operator.sh
    scripts/install-trainer-runtime.sh
    scripts/install-v2-release.sh
    scripts/acquire-judge-image.sh
    scripts/attest-judge-image.sh
    scripts/judge-image-contract.sh
    scripts/preload-judge-image.sh
    scripts/acquire-pgbouncer-rpm.sh
    scripts/attest-pgbouncer-rpm.sh
    scripts/provision-postgres-pgbouncer.sh
    scripts/trainer-host-capability-identity.sh
    scripts/trainer-runtime-tree-identity.sh
    scripts/validate-cloudflared.sh
    scripts/validate-production.sh
    scripts/validate-trainer-host.sh
  )
  local -a required_directories=(
    bin config contracts contracts/openapi contracts/pintia db db/roles polkit-1
    polkit-1/rules.d scripts systemd systemd/ascendanyd.service.d sysusers.d tmpfiles.d trainers
    trainers/recommendation trainers/recommendation/ascendany_recommendation_trainer
  )
  local actual_paths expected_paths actual_directories expected_directories
  if [[ ! -f "$manifest" || -L "$manifest" ||
        "$(stat -Lc '%u:%g:%a' "$manifest" 2>/dev/null || true)" != "0:0:644" ]]; then
    fail "release manifest must be a root:root 0644 regular file"
    return
  fi
  if ! jq -e '
      type == "object" and
      (keys == ["build", "commit", "files", "schema", "sourceDateEpoch", "version"]) and
      .schema == "ascendany.release.v2" and
      (.commit | type == "string" and test("^[0-9a-f]{40}$")) and
      (.version | type == "string" and length <= 128 and test("^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)([.]((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?([+][0-9A-Za-z-]+([.][0-9A-Za-z-]+)*)?$")) and
      (.sourceDateEpoch | type == "number" and floor == . and . >= 0) and
      (.build | type == "object" and
        (keys == ["cgoEnabled", "goExperiment", "goVersion", "goamd64", "goarch", "gofips140", "goos"]) and
        (.goVersion | type == "string" and test("^go[0-9]+[.][0-9]+([.][0-9]+)?[0-9A-Za-z.:_+~-]*$")) and
        .goos == "linux" and
        .goarch == "amd64" and
        .goamd64 == "v1" and
        (.goExperiment | type == "string" and test("^(none|[0-9A-Za-z_,.-]+)$")) and
        .gofips140 == "off" and
        .cgoEnabled == false) and
      (.files | type == "array" and length == 77) and
      (all(.files[];
        type == "object" and
        (keys == ["mode", "path", "sha256", "size"]) and
        (.path | type == "string") and
        (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
        (.size | type == "number" and floor == . and . > 0) and
        (.mode | type == "string" and test("^0[0-7]{3}$")))) and
      (([.files[].path] | length) == ([.files[].path] | unique | length))
    ' "$manifest" >/dev/null 2>&1; then
    fail "release manifest violates the exact v2 build contract"
    return
  fi
  expected_paths="$(printf '%s\n' "${required_paths[@]}" | LC_ALL=C sort)"
  actual_paths="$(jq -r '.files[].path' "$manifest" | LC_ALL=C sort)"
  if [[ "$actual_paths" != "$expected_paths" ]]; then
    fail "release manifest path set differs from the exact 77-path contract"
  else
    pass "release manifest path set and build provenance are exact"
  fi
  expected_paths="$(printf '%s\n' "${required_paths[@]}" release-manifest.json | LC_ALL=C sort)"
  actual_paths="$(find "$release_root" -mindepth 1 -type f -printf '%P\n' | LC_ALL=C sort)"
  if [[ "$actual_paths" != "$expected_paths" ]]; then
    fail "trainer release tree contains an unmanifested file or omits a declared file"
  fi
  expected_directories="$(printf '%s\n' "${required_directories[@]}" | LC_ALL=C sort)"
  actual_directories="$(find "$release_root" -mindepth 1 -type d -printf '%P\n' | LC_ALL=C sort)"
  if [[ "$actual_directories" != "$expected_directories" ]]; then
    fail "trainer release directory set differs from the exact contract"
  fi
  local relative executable
  for relative in "${required_paths[@]}"; do
    executable=0
    if [[ "$relative" == bin/* || "$relative" == scripts/* ]]; then
      executable=1
    fi
    check_release_file "$relative" "$executable"
  done
}

check_release_file() {
  local relative="$1" executable="$2"
  local manifest="$release_root/release-manifest.json"
  local path="$release_root/$relative"
  local expected_sha expected_size expected_mode actual_sha actual_size actual_mode

  if [[ ! -f "$manifest" || -L "$manifest" ||
        "$(stat -Lc '%u:%g' "$manifest" 2>/dev/null || true)" != "0:0" ||
        "$((8#$(stat -Lc '%a' "$manifest" 2>/dev/null || printf 777) & 8#22))" != "0" ]]; then
    fail "release manifest is missing, linked, non-root, or service-writable: $manifest"
    return
  fi
  if ! jq -e '
      .schema == "ascendany.release.v2" and
      (.commit | type == "string" and test("^[0-9a-f]{40}$"))
    ' "$manifest" >/dev/null 2>&1; then
    fail "release manifest schema or commit is invalid"
    return
  fi
  if ! expected_sha="$(jq -er --arg path "$relative" '
      [.files[] | select(.path == $path)] as $matches |
      if ($matches | length) == 1 then $matches[0].sha256 else error("missing path") end
    ' "$manifest")" ||
     ! expected_size="$(jq -er --arg path "$relative" '.files[] | select(.path == $path) | .size' "$manifest")" ||
     ! expected_mode="$(jq -er --arg path "$relative" '.files[] | select(.path == $path) | .mode' "$manifest")"; then
    fail "release manifest does not bind exactly one $relative"
    return
  fi
  if [[ ! -f "$path" || -L "$path" ]] || ! is_under "$path" "$release_root"; then
    fail "release payload is missing, linked, or outside the root: $relative"
    return
  fi
  actual_sha="$(sha256sum -- "$path" | awk '{print $1}')"
  actual_size="$(stat -Lc '%s' "$path")"
  actual_mode="$(stat -Lc '%a' "$path")"
  if [[ "$(stat -Lc '%u:%g' "$path")" != "0:0" ]]; then
    fail "release payload is not root:root: $relative"
  elif [[ "$actual_sha" != "$expected_sha" || "$actual_size" != "$expected_size" || "$actual_mode" != "${expected_mode#0}" ]]; then
    fail "release payload differs from its manifest: $relative"
  elif [[ "$executable" == "1" && ! -x "$path" ]]; then
    fail "release binary is not executable: $relative"
  else
    pass "release manifest verifies $relative"
  fi
}

check_release_root() {
  if [[ ! -d "$release_root" || -L "$release_root" ]]; then
    fail "release root is missing or linked: $release_root"
  elif find "$release_root" -xdev \( -type l -o ! -user root -o ! -group root -o -perm /022 \) -print -quit | grep -q .; then
    fail "release root contains a link, non-root entry, or service-writable path"
  elif find "$release_root" -xdev ! -type d ! -type f -print -quit | grep -q .; then
    fail "release root contains a special filesystem node"
  else
    pass "release root is immutable to the trainer identity"
  fi
}

read_environment_file() {
  local line key value
  declare -gA trainer_environment=()
  local -A allowed=()
  local -a required=(
    ASCENDANY_TRAINER_AGENT_ENDPOINT
    ASCENDANY_TRAINER_AGENT_ID
    ASCENDANY_TRAINER_AGENT_LEASE_DURATION
    ASCENDANY_TRAINER_AGENT_POLL_INTERVAL
    ASCENDANY_TRAINER_AGENT_REQUEST_TIMEOUT
    ASCENDANY_TRAINER_AGENT_MAX_INPUT_BYTES
    ASCENDANY_TRAINER_AGENT_MAX_OUTPUT_BYTES
    ASCENDANY_TRAINER_AGENT_MAX_STDERR_BYTES
    ASCENDANY_TRAINER_AGENT_BWRAP
    ASCENDANY_TRAINER_AGENT_RUNTIME_ROOT
    ASCENDANY_TRAINER_AGENT_PYTHON
    ASCENDANY_TRAINER_AGENT_PACKAGE_ROOT
    ASCENDANY_TRAINER_AGENT_WORK_ROOT
    ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH
    ASCENDANY_TRAINER_AGENT_RUNTIME_PATHS
    ASCENDANY_TRAINER_AGENT_NVIDIA_DEVICE_PATHS
    ASCENDANY_TRAINER_AGENT_TRAINER_TIMEOUT
    ASCENDANY_TRAINER_AGENT_CUBLAS_WORKSPACE_CONFIG
    ASCENDANY_TRAINER_AGENT_CUDA_VISIBLE_DEVICES
    ASCENDANY_TRAINER_AGENT_MKL_NUM_THREADS
    ASCENDANY_TRAINER_AGENT_OMP_NUM_THREADS
    ASCENDANY_TRAINER_AGENT_OPENBLAS_NUM_THREADS
    ASCENDANY_TRAINER_AGENT_LOG_LEVEL
  )

  if [[ ! -f "$environment_file" || -L "$environment_file" ||
        "$(stat -Lc '%u:%g' "$environment_file" 2>/dev/null || true)" != "0:0" ||
        "$(stat -Lc '%a' "$environment_file" 2>/dev/null || true)" != "644" ||
        "$environment_file" != "$(realpath -m -- "$environment_file")" ||
        "$environment_file" != "$(realpath -e -- "$environment_file" 2>/dev/null || true)" ]] ||
     ! check_root_owned_ancestry "$environment_file" 0; then
    fail "trainer environment file must be a real root:root 0644 file: $environment_file"
    return
  fi
  for key in "${required[@]}"; do
    allowed["$key"]=1
  done
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    if [[ ! "$line" =~ ^([A-Z][A-Z0-9_]*)=([^[:space:]]+)$ ]]; then
      fail "trainer environment contains a noncanonical line"
      continue
    fi
    key="${BASH_REMATCH[1]}"
    value="${BASH_REMATCH[2]}"
    if [[ -z "${allowed[$key]:-}" ]]; then
      fail "trainer environment contains an unknown key: $key"
    elif [[ -n "${trainer_environment[$key]:-}" ]]; then
      fail "trainer environment repeats key: $key"
    else
      trainer_environment["$key"]="$value"
    fi
  done <"$environment_file"
  for key in "${required[@]}"; do
    if [[ -z "${trainer_environment[$key]:-}" ]]; then
      fail "trainer environment omits required key: $key"
    fi
  done
  if grep -Eiq '(^|_)(database|db|password|jwt|secret|token|http_proxy|https_proxy|all_proxy|no_proxy)(_|=)' "$environment_file"; then
    fail "trainer environment contains a credential, database, or proxy capability"
  else
    pass "trainer environment contains only the closed non-secret key set"
  fi
}

check_endpoint_and_paths() {
  local endpoint="${trainer_environment[ASCENDANY_TRAINER_AGENT_ENDPOINT]:-}"
  local port="" thread_key thread_value
  if [[ ! "$endpoint" =~ ^https://[a-z0-9][a-z0-9.-]*[a-z0-9](:([1-9][0-9]{0,4}))?$ ]]; then
    fail "trainer endpoint is not one lowercase canonical HTTPS DNS origin"
  else
    port="${BASH_REMATCH[2]:-}"
    if [[ "$port" == "443" || ( -n "$port" && 10#$port -gt 65535 ) ]]; then
      fail "trainer endpoint uses a default, padded, or out-of-range port"
    else
      pass "trainer endpoint is one canonical HTTPS origin"
    fi
  fi
  if [[ "$endpoint" != "$trainer_endpoint" ]]; then
    fail "trainer endpoint must be the dedicated cutover-independent origin $trainer_endpoint"
  else
    pass "trainer endpoint is the dedicated cutover-independent origin"
  fi
  if [[ "${trainer_environment[ASCENDANY_TRAINER_AGENT_BWRAP]:-}" != "/usr/bin/bwrap" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_RUNTIME_ROOT]:-}" != "$trainer_runtime_root" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_PYTHON]:-}" != "$trainer_python" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_PACKAGE_ROOT]:-}" != "$release_root/trainers/recommendation" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_WORK_ROOT]:-}" != "/var/lib/ascendany-trainer/work" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH]:-}" != "/var/lib/ascendany-trainer/acceptance/trainer-latest.json" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_RUNTIME_PATHS]:-}" != "/lib,/lib64,$trainer_runtime_root,/sys" ]]; then
    fail "trainer executable, script, state, or runtime mount contract drifted"
  else
    pass "trainer executable and filesystem paths match the isolated contract"
  fi
  if [[ "${trainer_environment[ASCENDANY_TRAINER_AGENT_MAX_INPUT_BYTES]:-}" != "134217728" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_MAX_OUTPUT_BYTES]:-}" != "134217728" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_MAX_STDERR_BYTES]:-}" != "16384" ]]; then
    fail "trainer input, output, or stderr byte bound drifted"
  else
    pass "trainer transport and child byte bounds are exact"
  fi
  if [[ "${trainer_environment[ASCENDANY_TRAINER_AGENT_CUDA_VISIBLE_DEVICES]:-}" != "0" ||
        "${trainer_environment[ASCENDANY_TRAINER_AGENT_CUBLAS_WORKSPACE_CONFIG]:-}" != ":4096:8" ]]; then
    fail "trainer CUDA device or deterministic cuBLAS workspace configuration drifted"
  else
    pass "trainer CUDA device and deterministic cuBLAS workspace configuration are exact"
  fi
  for thread_key in \
    ASCENDANY_TRAINER_AGENT_MKL_NUM_THREADS \
    ASCENDANY_TRAINER_AGENT_OMP_NUM_THREADS \
    ASCENDANY_TRAINER_AGENT_OPENBLAS_NUM_THREADS; do
    thread_value="${trainer_environment[$thread_key]:-}"
    if [[ ! "$thread_value" =~ ^[1-9][0-9]*$ ]] || (( 10#$thread_value > 256 )); then
      fail "$thread_key must be a canonical integer from 1 to 256"
    fi
  done
}

check_trainer_runtime() {
  local selector_target resolved_root resolved_python marker_raw marker_canonical
  local python_tree_identity host_capabilities marker_host_capabilities
  local construction_sha provenance_sha tree_sha host_sha attestation attestation_canonical
  local runtime_entry runtime_device mount_target
  local -a runtime_entries=() root_entries=() input_entries=()

  if [[ ! -d "$trainer_runtime_parent" || -L "$trainer_runtime_parent" ||
        "$trainer_runtime_parent" != "$(realpath -e -- "$trainer_runtime_parent" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a' -- "$trainer_runtime_parent" 2>/dev/null || true)" != "0:0:755" ]] ||
     ! check_root_owned_ancestry "$trainer_runtime_parent" 1; then
    fail "trainer runtime parent must be a canonical root:root 0755 directory"
    return
  fi
  if [[ ! -L "$trainer_runtime_root" ]]; then
    fail "trainer runtime current selector must be a relative symbolic link"
    return
  fi
  selector_target="$(readlink -- "$trainer_runtime_root")"
  if [[ ! "$selector_target" =~ ^torch-2[.]13[.]0-cu130-[0-9a-f]{64}$ ]]; then
    fail "trainer runtime selector target is not construction-addressed"
    return
  fi
  resolved_root="$(realpath -e -- "$trainer_runtime_root" 2>/dev/null || true)"
  if [[ "$resolved_root" != "$trainer_runtime_parent/$selector_target" ||
        ! -d "$resolved_root" || -L "$resolved_root" ||
        "$(stat -Lc '%u:%g:%a' -- "$resolved_root" 2>/dev/null || true)" != "0:0:755" ]]; then
    fail "trainer runtime selector does not resolve to one immutable construction"
    return
  fi
  list_directory_entries "$trainer_runtime_parent" runtime_entries
  if (( ${#runtime_entries[@]} < 2 )); then
    fail "trainer runtime parent omits the selector or selected construction"
    return
  fi
  for runtime_entry in "${runtime_entries[@]}"; do
    if [[ "$runtime_entry" == "$trainer_runtime_root" ]]; then
      [[ -L "$runtime_entry" ]] || {
        fail "trainer runtime current entry is not a symbolic link"
        return
      }
    elif [[ "${runtime_entry##*/}" =~ ^torch-2[.]13[.]0-cu130-[0-9a-f]{64}$ &&
            -d "$runtime_entry" && ! -L "$runtime_entry" &&
            "$(stat -Lc '%u:%g:%a' -- "$runtime_entry")" == "0:0:755" ]]; then
      :
    else
      fail "trainer runtime parent contains an unowned publication entry: ${runtime_entry##*/}"
      return
    fi
  done

  trainer_runtime_inputs="$resolved_root/.ascendany-construction-inputs"
  trainer_runtime_marker="$resolved_root/.ascendany-runtime-provenance.json"
  trainer_runtime_source_manifest="$trainer_runtime_inputs/release-manifest.json"
  trainer_runtime_captured_lock="$trainer_runtime_inputs/runtime-requirements-cu130.lock"
  trainer_runtime_captured_closure="$trainer_runtime_inputs/runtime-closure-cu130.json"
  trainer_runtime_captured_wheels="$trainer_runtime_inputs/runtime-wheels-cu130.json"
  trainer_runtime_captured_python_source="$trainer_runtime_inputs/runtime-python-cu130.json"
  trainer_runtime_captured_installer="$trainer_runtime_inputs/install-trainer-runtime.sh"
  trainer_runtime_captured_tree_identity="$trainer_runtime_inputs/trainer-runtime-tree-identity.sh"
  trainer_runtime_captured_host_capability="$trainer_runtime_inputs/trainer-host-capability-identity.sh"
  trainer_runtime_captured_uv="$trainer_runtime_inputs/uv"
  resolved_python="$resolved_root/python/bin/python3.14"

  list_directory_entries "$resolved_root" root_entries
  if (( ${#root_entries[@]} != 3 )) ||
     [[ "${root_entries[0]}" != "$trainer_runtime_inputs" ||
        "${root_entries[1]}" != "$trainer_runtime_marker" ||
        "${root_entries[2]}" != "$resolved_root/python" ]]; then
    fail "selected trainer runtime root entry set differs from the closed contract"
    return
  fi
  list_directory_entries "$trainer_runtime_inputs" input_entries
  if (( ${#input_entries[@]} != 9 )) ||
     [[ "${input_entries[0]}" != "$trainer_runtime_captured_installer" ||
        "${input_entries[1]}" != "$trainer_runtime_source_manifest" ||
        "${input_entries[2]}" != "$trainer_runtime_captured_closure" ||
        "${input_entries[3]}" != "$trainer_runtime_captured_python_source" ||
        "${input_entries[4]}" != "$trainer_runtime_captured_lock" ||
        "${input_entries[5]}" != "$trainer_runtime_captured_wheels" ||
        "${input_entries[6]}" != "$trainer_runtime_captured_host_capability" ||
        "${input_entries[7]}" != "$trainer_runtime_captured_tree_identity" ||
        "${input_entries[8]}" != "$trainer_runtime_captured_uv" ]]; then
    fail "selected trainer runtime construction-input set differs from the closed contract"
    return
  fi

  runtime_device="$(stat -Lc '%d' -- "$resolved_root")"
  while IFS= read -r mount_target; do
    if [[ "$mount_target" != "$resolved_root" && "$mount_target" == "$resolved_root"/* ]]; then
      fail "selected trainer runtime contains a descendant mount"
      return
    fi
  done < <(findmnt -rn -R -o TARGET --target "$resolved_root")
  while IFS= read -r -d '' runtime_entry; do
    if [[ "$(stat -c '%d' -- "$runtime_entry")" != "$runtime_device" ]]; then
      fail "selected trainer runtime crosses its publication filesystem"
      return
    fi
  done < <(find -P "$resolved_root" -mindepth 1 -print0)
  if find "$resolved_root" \( ! -user root -o ! -group root \) -print -quit | grep -q . ||
     find "$resolved_root" ! -type l -perm /022 -print -quit | grep -q . ||
     find "$resolved_root" ! -type d ! -type f ! -type l -print -quit | grep -q . ||
     find "$resolved_root" -type f -links +1 -print -quit | grep -q . ||
     find "$resolved_root" -type l ! -path "$resolved_root/python/*" -print -quit | grep -q .; then
    fail "selected trainer runtime contains an unsafe entry"
    return
  fi
  if [[ ! -f "$resolved_python" || -L "$resolved_python" || ! -x "$resolved_python" ||
        "$(stat -Lc '%u:%g:%a:%h' -- "$resolved_python" 2>/dev/null || true)" != "0:0:755:1" ]]; then
    fail "selected trainer Python executable has unsafe identity"
    return
  fi
  if [[ ! -f "$trainer_runtime_marker" || -L "$trainer_runtime_marker" ||
        "$(stat -Lc '%u:%g:%a:%h' -- "$trainer_runtime_marker" 2>/dev/null || true)" != "0:0:644:1" ]]; then
    fail "selected trainer runtime provenance marker has unsafe identity"
    return
  fi
  marker_raw="$(<"$trainer_runtime_marker")"
  marker_canonical="$(jq -cS . "$trainer_runtime_marker" 2>/dev/null || true)"
  if [[ -z "$marker_canonical" || "$marker_raw" != "$marker_canonical" ]]; then
    fail "selected trainer runtime provenance marker is not exact canonical JSON"
    return
  fi
  construction_sha="$(jq -er '.constructionDigest | select(test("^[0-9a-f]{64}$"))' "$trainer_runtime_marker" 2>/dev/null || true)"
  provenance_sha="$(sha256sum -- "$trainer_runtime_marker" | awk '{print $1}')"
  tree_sha="$(jq -er '.pythonTree.sha256 | select(test("^[0-9a-f]{64}$"))' "$trainer_runtime_marker" 2>/dev/null || true)"
  if [[ -z "$construction_sha" || "${resolved_root##*/}" != "torch-2.13.0-cu130-$construction_sha" ||
        -z "$tree_sha" ]]; then
    fail "selected trainer runtime path is not bound to marker construction provenance"
    return
  fi

  if ! python_tree_identity="$(
      /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
        "$release_root/$trainer_runtime_tree_identity_relative" "$resolved_root/python"
    )" ||
     ! jq -e --argjson identity "$python_tree_identity" '.pythonTree == $identity' \
       "$trainer_runtime_marker" >/dev/null 2>&1; then
    fail "selected portable Python tree differs from marker identity"
    return
  fi
  if ! host_capabilities="$(
      /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
        "$release_root/$trainer_host_capability_identity_relative" "$resolved_root" "$resolved_python"
    )"; then
    fail "selected trainer host capability identity cannot be reproduced"
    return
  fi
  host_capabilities="$(jq -cS . <<<"$host_capabilities")"
  marker_host_capabilities="$(jq -cS '.hostCapabilities' "$trainer_runtime_marker")"
  if [[ "$host_capabilities" != "$marker_host_capabilities" ]]; then
    fail "trainer host capability identity differs from runtime publication"
    return
  fi
  host_sha="$(printf '%s' "$host_capabilities" | sha256sum | awk '{print $1}')"

  source "$release_root/$trainer_runtime_installer_relative"
  if ! attestation="$(
      run_runtime_attestation \
        "$resolved_root" \
        "$release_root/trainers/recommendation" \
        "$construction_sha" \
        "$provenance_sha" \
        "$tree_sha" \
        "$host_sha"
    )"; then
    fail "selected trainer runtime failed the production child attestation"
    return
  fi
  attestation_canonical="$(jq -cS . <<<"$attestation" 2>/dev/null || true)"
  if [[ -z "$attestation_canonical" ]] || ! jq -e \
      --arg construction "$construction_sha" \
      --arg provenance "$provenance_sha" \
      --arg tree "$tree_sha" \
      --arg host "$host_sha" '
      type == "object" and
      keys == ["hostCapabilitySha256", "runtimeAttestationSha256", "runtimeConstructionSha256", "runtimeProvenanceSha256", "runtimeTreeSha256"] and
      .hostCapabilitySha256 == $host and
      .runtimeConstructionSha256 == $construction and
      .runtimeProvenanceSha256 == $provenance and
      .runtimeTreeSha256 == $tree and
      (.runtimeAttestationSha256 | type == "string" and test("^[0-9a-f]{64}$"))
    ' <<<"$attestation_canonical" >/dev/null; then
    fail "production child attestation result differs from selected runtime provenance"
    return
  fi
  pass "selected construction, closed mount namespace, host capability, portable tree, torch, CUDA, and source package passed one production attestation contract"
}

check_installed_trainer_inputs() {
  local -a relatives=(
    config/trainer-agent.env
    systemd/ascendany-trainer-agent.service
    sysusers.d/ascendany-v2.conf
    tmpfiles.d/ascendany-v2.conf
  )
  local -a targets=(
    /etc/ascendany/v2/trainer-agent.env
    /etc/systemd/system/ascendany-trainer-agent.service
    /etc/sysusers.d/ascendany-v2.conf
    /etc/tmpfiles.d/ascendany-v2.conf
  )
  local index source target
  for index in "${!relatives[@]}"; do
    source="$release_root/${relatives[$index]}"
    target="${targets[$index]}"
    if [[ ! -f "$source" || -L "$source" || ! -f "$target" || -L "$target" ||
          "$target" != "$(realpath -m -- "$target")" ||
          "$target" != "$(realpath -e -- "$target" 2>/dev/null || true)" ||
          "$(stat -Lc '%u:%g:%a' "$target" 2>/dev/null || true)" != "0:0:644" ]] ||
       ! check_root_owned_ancestry "$target" 0; then
      fail "installed trainer input must be a canonical root:root 0644 file: $target"
    elif ! cmp --silent -- "$source" "$target"; then
      fail "installed trainer input differs from the reviewed release: $target"
    else
      pass "installed trainer input matches ${relatives[$index]}"
    fi
  done
}

check_remote_release() {
  local manifest="$release_root/release-manifest.json"
  local endpoint="${trainer_environment[ASCENDANY_TRAINER_AGENT_ENDPOINT]:-}"
  local metadata expected_build_time manifest_cgo_enabled
  if ! metadata="$(curl --disable --fail --silent --show-error --max-time 10 --noproxy '*' --proto '=https' "$endpoint/version")"; then
    fail "trainer endpoint version metadata cannot be read directly over HTTPS"
    return
  fi
  expected_build_time="$(date -u -d "@$(jq -r '.sourceDateEpoch' "$manifest")" +%FT%TZ 2>/dev/null || true)"
  manifest_cgo_enabled="$(jq -r '.build.cgoEnabled' "$manifest")"
  if ! jq -e \
      --arg version "$(jq -r '.version' "$manifest")" \
      --arg commit "$(jq -r '.commit' "$manifest")" \
      --arg buildTime "$expected_build_time" \
      --arg goVersion "$(jq -r '.build.goVersion' "$manifest")" \
      --arg goos "$(jq -r '.build.goos' "$manifest")" \
      --arg goarch "$(jq -r '.build.goarch' "$manifest")" \
      --arg goamd64 "$(jq -r '.build.goamd64' "$manifest")" \
      --arg goExperiment "$(jq -r '.build.goExperiment' "$manifest")" \
      --arg gofips140 "$(jq -r '.build.gofips140' "$manifest")" \
      --argjson cgoEnabled "$manifest_cgo_enabled" '
        type == "object" and
        (keys == ["buildTime", "cgoEnabled", "commit", "goExperiment", "goVersion", "goamd64", "goarch", "gofips140", "goos", "version"]) and
        .version == $version and .commit == $commit and .buildTime == $buildTime and
        .goVersion == $goVersion and .goos == $goos and .goarch == $goarch and
        .goamd64 == $goamd64 and .goExperiment == $goExperiment and
        .gofips140 == $gofips140 and .cgoEnabled == $cgoEnabled
      ' <<<"$metadata" >/dev/null 2>&1; then
    fail "trainer endpoint does not run the exact staged release toolchain and target"
  else
    pass "trainer endpoint matches the staged release source, toolchain, and target"
  fi
}

check_remote_ingress_closure() {
  local endpoint="${trainer_environment[ASCENDANY_TRAINER_AGENT_ENDPOINT]:-}"
  local status
  if ! status="$(
    curl --disable --silent --show-error --max-time 10 --noproxy '*' --proto '=https' \
      --output /dev/null --write-out '%{http_code}' "$endpoint/livez"
  )"; then
    fail "dedicated trainer endpoint negative route probe failed"
  elif [[ "$status" != "404" ]]; then
    fail "dedicated trainer endpoint exposes a path outside /version and the trainer transport"
  else
    pass "dedicated trainer endpoint rejects paths outside the closed ingress route set"
  fi
}

check_unit_enablement() {
  local actual
  actual="$(systemctl is-enabled "$unit" 2>/dev/null || true)"
  if [[ "$actual" != "$required_enablement" ]]; then
    fail "$validation_phase phase requires $unit to be $required_enablement"
  else
    pass "$validation_phase phase keeps $unit $required_enablement"
  fi
}

check_effective_filesystem_paths() {
  check_effective_word_set ReadOnlyPaths \
    /etc/ascendany/v2 \
    "$trainer_runtime_parent" \
    /opt/ascendany/v2/trainers/recommendation
  check_effective_word_set ReadWritePaths /var/lib/ascendany-trainer
  check_effective_word_set InaccessiblePaths \
    /etc/ascendany/credentials \
    /opt/ascendany/Release \
    /var/lib/ascendany/artifacts
}

check_unit() {
  local effective_environment main_pid executable
  local -a process_argv=()
  if [[ "$(unit_property LoadState || true)" != "loaded" ]]; then
    fail "$unit is not loaded"
    return
  fi
  if [[ "$(unit_property NeedDaemonReload || true)" != "no" ]]; then
    fail "$unit requires daemon-reload before its effective configuration can be validated"
  fi
  check_unit_enablement
  if [[ "$(unit_property User || true)" != "ascendany-trainer" ||
        "$(unit_property Group || true)" != "ascendany-trainer" ||
        -n "$(unit_property SupplementaryGroups || true)" ||
        "$(id -nG ascendany-trainer 2>/dev/null || true)" != "ascendany-trainer" ]]; then
    fail "$unit identity is broader than ascendany-trainer:ascendany-trainer"
  else
    pass "$unit uses the dedicated trainer identity"
  fi
  if [[ "$require_active" == "1" ]]; then
    if [[ "$(unit_property ActiveState || true)" != "active" || "$(unit_property SubState || true)" != "running" ]]; then
      fail "$unit is not active and running"
    else
      pass "$unit is active"
    fi
  elif [[ "$(unit_property ActiveState || true)" != "inactive" || "$(unit_property SubState || true)" != "dead" ]]; then
    fail "$validation_phase phase requires $unit to be inactive and dead"
  else
    pass "$validation_phase phase keeps $unit inactive and dead"
  fi

  check_effective_environment_file
  check_unit_credential
  check_effective_service_commands
  if [[ "$require_active" == "1" && ! -s "/run/credentials/$unit/trainer_agent_token" ]]; then
    fail "active scoped trainer credential is missing"
  fi

  effective_environment="$(unit_property Environment || true)"
  if grep -Eo '(^|[[:space:]])[A-Z][A-Z0-9_]*=' <<<"$effective_environment" |
     sed -E 's/^[[:space:]]*//; s/=$//' |
     grep -E '(DATABASE|DB_|PASSWORD|JWT|SECRET|TOKEN|HTTP_PROXY|HTTPS_PROXY|ALL_PROXY|NO_PROXY)' |
     grep -vx 'ASCENDANY_TRAINER_AGENT_TOKEN_FILE' |
     grep -q .; then
    fail "$unit injects a forbidden database, credential, or proxy environment value"
  fi
  check_effective_value NoNewPrivileges yes
  check_effective_value FragmentPath "/etc/systemd/system/$unit"
  check_effective_word_set DropInPaths "$global_service_dropin"
  check_effective_value TimeoutStopFailureMode abort
  check_effective_value WorkingDirectory /var/lib/ascendany-trainer
  check_effective_word_set Environment \
    "ASCENDANY_TRAINER_AGENT_TOKEN_FILE=/run/credentials/$unit/trainer_agent_token"
  check_effective_value PrivateTmp yes
  check_effective_value PrivateNetwork no
  check_effective_value PrivateDevices no
  check_effective_value DevicePolicy closed
  check_effective_value ProtectClock no
  check_effective_value ProtectSystem strict
  check_effective_value CapabilityBoundingSet ""
  check_effective_value AmbientCapabilities ""
  check_effective_value StateDirectory ascendany-trainer
  check_effective_filesystem_paths

  if [[ "$require_active" == "1" ]]; then
    main_pid="$(unit_property MainPID || true)"
    executable="$(realpath -e -- "/proc/$main_pid/exe" 2>/dev/null || true)"
    if [[ ! "$main_pid" =~ ^[1-9][0-9]*$ || "$executable" != "$release_root/bin/ascendany-trainer-agent" ]]; then
      fail "$unit main process does not execute the staged trainer-agent binary"
    elif ! mapfile -d '' -t process_argv <"/proc/$main_pid/cmdline" 2>/dev/null ||
         (( ${#process_argv[@]} != 2 )) ||
         [[ "${process_argv[0]}" != "$release_root/bin/ascendany-trainer-agent" ||
            "${process_argv[1]}" != "run" ]]; then
      fail "$unit main process argv differs from the exact trainer-agent run command"
    else
      pass "$unit main process executable and argv match the staged release"
    fi
  fi
}

gpu_index_is_available() {
  local index="$1" reported
  if ! reported="$(
    LC_ALL=C nvidia-smi \
      --id="$index" \
      --query-gpu=index \
      --format=csv,noheader,nounits 2>/dev/null
  )"; then
    return 1
  fi
  [[ "$reported" == "$index" ]]
}

check_effective_gpu_device_allow() {
  local configured="${trainer_environment[ASCENDANY_TRAINER_AGENT_NVIDIA_DEVICE_PATHS]:-}"
  local effective_device_allow expected_device_allow
  local -a devices=()
  IFS=',' read -r -a devices <<<"$configured"
  mapfile -t devices < <(printf '%s\n' "${devices[@]}" | LC_ALL=C sort)
  effective_device_allow="$(unit_property DeviceAllow || true)"
  expected_device_allow="$(printf '%s rw\n' "${devices[@]}" | LC_ALL=C sort)"
  if [[ "$(printf '%s\n' "$effective_device_allow" | sed '/^$/d' | LC_ALL=C sort)" != "$expected_device_allow" ||
        " $configured " != *"/dev/nvidiactl"* || " $configured " != *"/dev/nvidia-uvm"* ]]; then
    fail "unit effective DeviceAllow set differs from the configured NVIDIA device set"
    return 1
  fi
  return 0
}

check_gpu_devices() {
  local configured="${trainer_environment[ASCENDANY_TRAINER_AGENT_NVIDIA_DEVICE_PATHS]:-}"
  local device index
  local -a devices=()
  if ! check_effective_gpu_device_allow; then
    return
  fi
  IFS=',' read -r -a devices <<<"$configured"
  mapfile -t devices < <(printf '%s\n' "${devices[@]}" | LC_ALL=C sort)
  for device in "${devices[@]}"; do
    if [[ ! -c "$device" ]] || ! runuser -u ascendany-trainer -- test -r "$device" || ! runuser -u ascendany-trainer -- test -w "$device"; then
      fail "trainer identity cannot read/write configured character device: $device"
    fi
  done
  index="${trainer_environment[ASCENDANY_TRAINER_AGENT_CUDA_VISIBLE_DEVICES]:-}"
  if [[ ! "$index" =~ ^(0|[1-9][0-9]*)$ || ! -c "/dev/nvidia$index" ]] ||
     ! gpu_index_is_available "$index"; then
    fail "CUDA_VISIBLE_DEVICES does not bind one available configured GPU"
  elif [[ "$(printf '%s\n' "${devices[@]}")" != "$(printf '%s\n' "/dev/nvidia-uvm" "/dev/nvidia$index" "/dev/nvidiactl" | LC_ALL=C sort)" ]]; then
    fail "trainer unit exposes NVIDIA devices beyond the one CUDA-visible GPU and control devices"
  else
    pass "unit, trainer identity, CUDA index, and NVIDIA character devices agree"
  fi
}

list_directory_entries() {
  local directory="$1" output_name="$2"
  local restore_dotglob=0 restore_nullglob=0
  local -n output="$output_name"
  if ! shopt -q dotglob; then
    shopt -s dotglob
    restore_dotglob=1
  fi
  if ! shopt -q nullglob; then
    shopt -s nullglob
    restore_nullglob=1
  fi
  output=("$directory"/*)
  if [[ "$restore_nullglob" == "1" ]]; then
    shopt -u nullglob
  fi
  if [[ "$restore_dotglob" == "1" ]]; then
    shopt -u dotglob
  fi
}

is_exact_trainer_directory() {
  local path="$1" expected_uid="$2" expected_gid="$3"
  [[ -d "$path" && ! -L "$path" &&
     "$path" == "$(realpath -m -- "$path")" &&
     "$path" == "$(realpath -e -- "$path" 2>/dev/null || true)" &&
     "$(stat -c '%u:%g:%a' "$path" 2>/dev/null || true)" == "$expected_uid:$expected_gid:700" ]]
}

is_exact_trainer_output() {
  local path="$1" expected_uid="$2" expected_gid="$3"
  [[ -f "$path" && ! -L "$path" &&
     "$(stat -c '%u:%g:%a:%h' "$path" 2>/dev/null || true)" == "$expected_uid:$expected_gid:600:1" ]]
}

check_work_root_structure() {
  local work_root="$1" trainer_uid="$2" trainer_gid="$3"
  local invocation invocation_name output_root output_file
  local -a work_entries=() invocation_entries=() output_entries=()
  list_directory_entries "$work_root" work_entries
  if [[ "$require_quiesced_work_root" == "1" ]]; then
    if [[ "$(unit_property ActiveState || true)" != "inactive" ||
          "$(unit_property SubState || true)" != "dead" ]]; then
      fail "quiesced work-root validation requires an inactive, dead trainer unit"
    elif (( ${#work_entries[@]} != 0 )); then
      fail "quiesced trainer work root retains an invocation directory"
    else
      pass "quiesced trainer unit has an empty private work root"
    fi
    return
  fi

  if (( ${#work_entries[@]} > 1 )); then
    fail "online trainer work root contains more than one invocation directory"
    return
  fi
  if (( ${#work_entries[@]} == 0 )); then
    pass "online trainer work root is private and has no active invocation"
    return
  fi

  invocation="${work_entries[0]}"
  invocation_name="${invocation##*/}"
  if [[ ! "$invocation_name" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}-[0-9]+$ ]] ||
     ! is_exact_trainer_directory "$invocation" "$trainer_uid" "$trainer_gid"; then
    fail "online trainer invocation must be one canonical UUIDv4 staging directory with exact owner and mode 0700"
    return
  fi

  list_directory_entries "$invocation" invocation_entries
  if (( ${#invocation_entries[@]} > 1 )); then
    fail "online trainer invocation contains entries outside its single output directory"
    return
  fi
  if (( ${#invocation_entries[@]} == 0 )); then
    pass "online trainer work root contains one safely initialized invocation"
    return
  fi

  output_root="${invocation_entries[0]}"
  if [[ "${output_root##*/}" != "output" ]] ||
     ! is_exact_trainer_directory "$output_root" "$trainer_uid" "$trainer_gid"; then
    fail "online trainer invocation output must be an exact owner-mode 0700 directory"
    return
  fi

  list_directory_entries "$output_root" output_entries
  if (( ${#output_entries[@]} > 1 )); then
    fail "online trainer output directory contains entries outside output.json"
    return
  fi
  if (( ${#output_entries[@]} == 0 )); then
    pass "online trainer work root contains one safe invocation awaiting output"
    return
  fi

  output_file="${output_entries[0]}"
  if [[ "${output_file##*/}" != "output.json" ]] ||
     ! is_exact_trainer_output "$output_file" "$trainer_uid" "$trainer_gid"; then
    fail "online trainer output must be the exact owner-mode 0600 output.json regular file"
  else
    pass "online trainer work root contains one exact active invocation tree"
  fi
}

check_work_root() {
  local work_root="${trainer_environment[ASCENDANY_TRAINER_AGENT_WORK_ROOT]:-}"
  local acceptance_root="/var/lib/ascendany-trainer/acceptance"
  local state_root="${work_root%/work}" trainer_uid trainer_gid
  local -a state_entries=()
  trainer_uid="$(id -u ascendany-trainer 2>/dev/null || true)"
  trainer_gid="$(id -g ascendany-trainer 2>/dev/null || true)"
  if [[ -z "$trainer_uid" || -z "$trainer_gid" ||
        "$work_root" != "/var/lib/ascendany-trainer/work" ||
        "$state_root" != "/var/lib/ascendany-trainer" ||
        ! -d "$state_root" || -L "$state_root" ||
        "$state_root" != "$(realpath -e -- "$state_root" 2>/dev/null || true)" ||
        "$(stat -c '%u:%g:%a' "$state_root" 2>/dev/null || true)" != "$trainer_uid:$trainer_gid:700" ]] ||
     ! check_root_owned_ancestry "$state_root" 0 ||
     ! is_exact_trainer_directory "$work_root" "$trainer_uid" "$trainer_gid" ||
     ! is_exact_trainer_directory "$acceptance_root" "$trainer_uid" "$trainer_gid"; then
    fail "trainer state, work, and acceptance roots must be canonical ascendany-trainer-owned 0700 directories below protected /var/lib"
    return
  fi
  list_directory_entries "$state_root" state_entries
  if (( ${#state_entries[@]} != 2 )) ||
     [[ "$(printf '%s\n' "${state_entries[@]}" | LC_ALL=C sort)" != "$(printf '%s\n' "$acceptance_root" "$work_root" | LC_ALL=C sort)" ]]; then
    fail "trainer state root must contain only its exact acceptance and work directories"
    return
  fi
  check_work_root_structure "$work_root" "$trainer_uid" "$trainer_gid"
}

check_acceptance_evidence() {
  local commit version agent_id endpoint evidence_parent
  local candidate candidate_parent trainer_uid trainer_gid
  local -a candidate_entries=()
  if [[ "$release_payload_verified" != "1" ]]; then
    fail "trainer acceptance verification requires an intact release payload"
    return
  fi
  candidate="${trainer_environment[ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH]:-}"
  candidate_parent="$(dirname -- "$candidate")"
  trainer_uid="$(id -u ascendany-trainer 2>/dev/null || true)"
  trainer_gid="$(id -g ascendany-trainer 2>/dev/null || true)"
  if [[ "$candidate" != "/var/lib/ascendany-trainer/acceptance/trainer-latest.json" ||
        ! -f "$candidate" || -L "$candidate" ||
        "$candidate" != "$(realpath -e -- "$candidate" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a:%h' "$candidate" 2>/dev/null || true)" != "$trainer_uid:$trainer_gid:600:1" ]] ||
     ! is_exact_trainer_directory "$candidate_parent" "$trainer_uid" "$trainer_gid"; then
    fail "trainer acceptance candidate must be the exact trainer-owned mode 0600 single-link file"
    return
  fi
  list_directory_entries "$candidate_parent" candidate_entries
  if (( ${#candidate_entries[@]} != 1 )) || [[ "${candidate_entries[0]:-}" != "$candidate" ]]; then
    fail "trainer acceptance directory must contain only the exact candidate file"
    return
  fi
  evidence_parent="$(dirname -- "$acceptance_evidence")"
  if [[ "$acceptance_evidence" != /* || "$acceptance_evidence" != "$(realpath -m -- "$acceptance_evidence")" ||
        ! -f "$acceptance_evidence" || -L "$acceptance_evidence" ||
        "$acceptance_evidence" != "$(realpath -e -- "$acceptance_evidence" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a:%h' "$acceptance_evidence" 2>/dev/null || true)" != "0:0:600:1" ||
        ! -d "$evidence_parent" || -L "$evidence_parent" ||
        "$(stat -Lc '%u:%g:%a' "$evidence_parent" 2>/dev/null || true)" != "0:0:700" ]] ||
     ! check_root_owned_ancestry "$acceptance_evidence" 1; then
    fail "trainer acceptance evidence must be a canonical root:root 0600 file in a root:root 0700 directory"
    return
  fi
  if ! cmp --silent -- "$candidate" "$acceptance_evidence"; then
    fail "root trainer acceptance evidence differs from the agent-written candidate"
    return
  fi
  if ! "$release_root/bin/ascendany-trainer-agent" verify-acceptance <"$candidate" >/dev/null 2>&1 ||
     ! "$release_root/bin/ascendany-trainer-agent" verify-acceptance <"$acceptance_evidence" >/dev/null 2>&1; then
    fail "trainer acceptance candidate or promoted evidence is not exact canonical v3 JSON"
    return
  fi
  if ! jq -e '
      type == "object" and
      (keys == ["agentId", "attemptToken", "claimAt", "disposition", "heartbeatAt", "hostCapabilitySha256", "inputManifestSHA256", "modelId", "origin", "outputBundleSHA256", "releaseCommit", "releaseVersion", "requestSHA256", "runId", "runtimeAttestationSha256", "runtimeConstructionSha256", "runtimeProvenanceSha256", "runtimeTreeSha256", "schema", "uploadAt"]) and
      .schema == "ascendany.trainer.acceptance.v3" and
      (.releaseCommit | test("^[0-9a-f]{40}$")) and
      (.releaseVersion | type == "string") and
      (.agentId | test("^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")) and
      (.runId | test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
      (.attemptToken | test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
      (.modelId | test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
      (.inputManifestSHA256 | test("^[0-9a-f]{64}$")) and
      (.outputBundleSHA256 | test("^[0-9a-f]{64}$")) and
      (.requestSHA256 | test("^[0-9a-f]{64}$")) and
      (.runtimeConstructionSha256 | test("^[0-9a-f]{64}$")) and
      (.runtimeProvenanceSha256 | test("^[0-9a-f]{64}$")) and
      (.runtimeTreeSha256 | test("^[0-9a-f]{64}$")) and
      (.hostCapabilitySha256 | test("^[0-9a-f]{64}$")) and
      (.runtimeAttestationSha256 | test("^[0-9a-f]{64}$")) and
      (.origin | type == "string") and
      (.disposition == "activated" or .disposition == "superseded") and
      (all(.claimAt, .heartbeatAt, .uploadAt; type == "string"))
    ' "$acceptance_evidence" >/dev/null; then
    fail "trainer acceptance evidence violates ascendany.trainer.acceptance.v3"
    return
  fi
  commit="$(jq -r '.releaseCommit' "$acceptance_evidence")"
  version="$(jq -r '.releaseVersion' "$acceptance_evidence")"
  agent_id="$(jq -r '.agentId' "$acceptance_evidence")"
  endpoint="$(jq -r '.origin' "$acceptance_evidence")"
  if [[ "$commit" != "$(jq -r '.commit' "$release_root/release-manifest.json")" ||
        "$version" != "$(jq -r '.version' "$release_root/release-manifest.json")" ||
        "$agent_id" != "${trainer_environment[ASCENDANY_TRAINER_AGENT_ID]:-}" ||
        "$endpoint" != "${trainer_environment[ASCENDANY_TRAINER_AGENT_ENDPOINT]:-}" ]]; then
    fail "trainer acceptance evidence belongs to a different release, agent, or origin"
  else
    pass "sealed claim-heartbeat-upload evidence matches the active trainer release"
  fi
}

acceptance_state_is_empty() {
  local candidate_parent="$1" evidence="$2"
  local -a candidate_entries=()
  list_directory_entries "$candidate_parent" candidate_entries
  (( ${#candidate_entries[@]} == 0 )) && [[ ! -e "$evidence" && ! -L "$evidence" ]]
}

check_empty_acceptance_state() {
  local candidate candidate_parent trainer_uid trainer_gid
  candidate="${trainer_environment[ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH]:-}"
  candidate_parent="$(dirname -- "$candidate")"
  trainer_uid="$(id -u ascendany-trainer 2>/dev/null || true)"
  trainer_gid="$(id -g ascendany-trainer 2>/dev/null || true)"
  if [[ "$candidate" != "/var/lib/ascendany-trainer/acceptance/trainer-latest.json" ||
        -z "$trainer_uid" || -z "$trainer_gid" ]] ||
     ! is_exact_trainer_directory "$candidate_parent" "$trainer_uid" "$trainer_gid"; then
    fail "staged trainer acceptance directory does not match the exact private contract"
    return
  fi
  if ! acceptance_state_is_empty "$candidate_parent" "$acceptance_evidence"; then
    fail "staged trainer host must have no candidate or promoted acceptance evidence"
  else
    pass "staged trainer host has no candidate or promoted acceptance evidence"
  fi
}

main() {
  local command
  if (( $# != 0 )); then
    fail "validate-trainer-host.sh accepts no positional arguments"
  fi
  validate_input_contract || true
  if (( failures > 0 )); then
    printf 'Trainer host validation rejected its execution contract with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  if [[ "$EUID" != "0" ]]; then
    printf 'validate-trainer-host.sh must run as root\n' >&2
    return 2
  fi
  for command in awk cmp curl date dirname find findmnt grep id jq mktemp nvidia-smi readlink realpath runuser sed sha256sum sort stat systemctl tr; do
    if ! command -v "$command" >/dev/null 2>&1; then
      fail "required command is missing: $command"
    fi
  done
  if (( failures > 0 )); then
    printf 'Trainer host validation rejected its execution contract with %d finding(s).\n' "$failures" >&2
    return 1
  fi

  check_release_root
  check_release_contract
  if (( failures > 0 )); then
    printf 'Trainer host validation stopped before executing release-owned code because release verification failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  release_payload_verified="1"

  check_installed_trainer_inputs
  check_fedora_global_service_dropin
  check_protected_trainer_checkout
  read_environment_file
  check_endpoint_and_paths
  check_trainer_runtime
  if [[ "$require_remote_release" == "1" ]]; then
    check_remote_release
    check_remote_ingress_closure
  fi
  check_unit
  check_gpu_devices
  check_work_root
  if [[ "$require_acceptance_evidence" == "1" ]]; then
    check_acceptance_evidence
  elif [[ "$require_empty_acceptance" == "1" ]]; then
    check_empty_acceptance_state
  fi

  if (( failures > 0 )); then
    printf 'Trainer host validation failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  printf 'Trainer host validation passed.\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
