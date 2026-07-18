#!/usr/bin/bash -p
set +x
set -euo pipefail

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  validator_environment_is_clean=1
  while IFS= read -r -d '' entry; do
    name="${entry%%=*}"
    case "$name" in
      PATH|LC_ALL|PWD|SHLVL|_|ASCENDANY_VALIDATOR_CLEAN_ENV|ASCENDANY_VALIDATION_PHASE|ASCENDANY_DEPLOYMENT_TRANSITION|ASCENDANY_EXPECTED_RUNTIME_PROVIDER_CREDENTIAL_BINDINGS|ASCENDANY_AGENT_ACCEPTANCE_RECEIPT_PATH|ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256|ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256|ASCENDANY_FORWARD_MODEL_HEAD_REVISION|ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256)
        ;;
      PGPASSFILE)
        [[ "${ASCENDANY_VALIDATION_PHASE-}" == "staged" ||
           "${ASCENDANY_VALIDATION_PHASE-}" == "catalog" ||
           "${ASCENDANY_VALIDATION_PHASE-}" == "activation" ]] || validator_environment_is_clean=0
        ;;
      *)
        validator_environment_is_clean=0
        ;;
    esac
  done < <(/usr/bin/env -0)
  if [[ "${ASCENDANY_VALIDATOR_CLEAN_ENV-}" != "1" ||
        "${PATH-}" != "/usr/bin:/bin" || "${LC_ALL-}" != "C" ||
        "$validator_environment_is_clean" != "1" ]]; then
    validation_phase_input="${ASCENDANY_VALIDATION_PHASE-}"
    deployment_transition_input="${ASCENDANY_DEPLOYMENT_TRANSITION-}"
    provider_bindings_input="${ASCENDANY_EXPECTED_RUNTIME_PROVIDER_CREDENTIAL_BINDINGS-}"
    agent_acceptance_receipt_input="${ASCENDANY_AGENT_ACCEPTANCE_RECEIPT_PATH-}"
    forward_database_fingerprint_input="${ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256-}"
    forward_business_fingerprint_input="${ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256-}"
    forward_model_head_revision_input="${ASCENDANY_FORWARD_MODEL_HEAD_REVISION-}"
    forward_model_artifact_sha256_input="${ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256-}"
    staged_pgpass_input="${PGPASSFILE-}"
    clean_environment=(
      /usr/bin/env -i
      PATH=/usr/bin:/bin
      LC_ALL=C
      ASCENDANY_VALIDATOR_CLEAN_ENV=1
      "ASCENDANY_VALIDATION_PHASE=$validation_phase_input"
      "ASCENDANY_DEPLOYMENT_TRANSITION=$deployment_transition_input"
      "ASCENDANY_EXPECTED_RUNTIME_PROVIDER_CREDENTIAL_BINDINGS=$provider_bindings_input"
      "ASCENDANY_AGENT_ACCEPTANCE_RECEIPT_PATH=$agent_acceptance_receipt_input"
      "ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256=$forward_database_fingerprint_input"
      "ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256=$forward_business_fingerprint_input"
      "ASCENDANY_FORWARD_MODEL_HEAD_REVISION=$forward_model_head_revision_input"
      "ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256=$forward_model_artifact_sha256_input"
    )
    if [[ ( "$validation_phase_input" == "staged" || "$validation_phase_input" == "catalog" ||
            "$validation_phase_input" == "activation" ) &&
          -n "$staged_pgpass_input" ]]; then
      clean_environment+=("PGPASSFILE=$staged_pgpass_input")
    fi
    exec "${clean_environment[@]}" /usr/bin/bash -p "$0" "$@"
  fi
fi

umask 077

release_root="/opt/ascendany/v2"
artifact_root="/var/lib/ascendany/artifacts"
catalog_publisher_state_root="/var/lib/ascendany-catalog-publisher"
catalog_publisher_config_root="/etc/ascendany-catalog-publisher"
catalog_publisher_pending_root="$catalog_publisher_state_root/pending"
catalog_publication_request_source="$catalog_publisher_pending_root/catalog_publication_request.cred"
catalog_publication_access_token_source="$catalog_publisher_pending_root/admin_access_token.cred"
catalog_receipt_root="$catalog_publisher_state_root/receipts"
restore_catalog_receipt_root="/var/lib/ascendany-restore/catalog-receipts"
backup_root="/var/backups/ascendany"
restore_evidence="/var/lib/ascendany-acceptance/restore-verify.json"
expected_db_user="ascendanyd_login"
runtime_pg_host="127.0.0.1"
runtime_pg_port="6432"
runtime_pg_database="ascendany_v2"
runtime_pg_connect_timeout="5"
postgres_network="podman"
postgres_gateway="10.88.0.1"
postgres_address="10.88.0.2"
postgres_subnet="10.88.0.0/16"
postgres_image_id="07f76768a0c956d6e9bddbcdb3c2be7fd9fd45ee6174a26873f8219fccbad65d"
postgres_image_reference="docker.io/library/postgres@sha256:5c855ad7b85e68e48a62f34662853f38b57c1c1d80f3a927ab58034fd6d31c5e"
postgres_data_volume="ascendany-postgres-data"
pgbouncer_config_root="/opt/ascendany/infra/pgbouncer"
pgbouncer_unit="ascendany-pgbouncer.service"
pgbouncer_package_unit="pgbouncer.service"
pgbouncer_binary="/usr/bin/pgbouncer"
pgbouncer_nevra="pgbouncer-1.25.2-1.fc44.x86_64"
pgbouncer_binary_sha256="42c722ab7352ccbb1eaba8dcc6d7fb9d28df11fbe1a73aa8b177c88dcd0bb318"
pgbouncer_binary_size="467960"
systemd_creds_binary="/usr/bin/systemd-creds"
initialization_node_binary="/usr/bin/node-22"
initialization_node_version="v22.22.2"
initialization_node_package="nodejs22-22.22.2-3.fc44.x86_64"
initialization_node_sha256="7ed75caca3ed639ebde926277e43ed04c67de55bfece9d56bd752159d96368f0"
pgbouncer_credential_source="/etc/ascendany/credentials/pgbouncer_userlist.cred"
pgbouncer_runtime_credential="/run/credentials/ascendany-pgbouncer.service/pgbouncer_userlist"
runtime_db_credential_source="/etc/ascendany/credentials/runtime_db_password.cred"
catalog_publisher_db_credential_source="$catalog_publisher_config_root/credentials/catalog_publisher_db_password.cred"
retired_api_unit="ascendany-api.service"
retired_api_port="8000"
retired_trainer_unit="ascendany-trainer-agent.service"
production_namespace_root="/opt/ascendany"
configuration_namespace_root="/etc/ascendany"
systemd_system_root="/etc/systemd/system"
systemd_runtime_root="/run/systemd/system"
systemd_local_root="/usr/local/lib/systemd/system"
systemd_vendor_root="/usr/lib/systemd/system"
retired_trainer_runtime_root="/opt/ascendany-trainer-runtime"
retired_trainer_state_root="/var/lib/ascendany-trainer"
retired_trainer_log_root="/var/log/ascendany-trainer"
retired_process_root="/proc"
acceptance_root="/var/lib/ascendany-acceptance"
managed_ports="5432 6432 18000"
required_ports=""
validation_phase="${ASCENDANY_VALIDATION_PHASE-}"
deployment_transition="${ASCENDANY_DEPLOYMENT_TRANSITION-}"
expected_forward_database_fingerprint="${ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256-}"
expected_forward_business_fingerprint="${ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256-}"
expected_forward_model_head_revision="${ASCENDANY_FORWARD_MODEL_HEAD_REVISION-}"
expected_forward_model_artifact_sha256="${ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256-}"
expected_write_mode=""
ascendanyd_active="0"
smoke_dropin="/etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf"
expected_runtime_provider_credential_bindings="${ASCENDANY_EXPECTED_RUNTIME_PROVIDER_CREDENTIAL_BINDINGS:-}"
agent_acceptance_receipt_path="${ASCENDANY_AGENT_ACCEPTANCE_RECEIPT_PATH-}"
agent_prompt_document_sha256="1e7fc27df0bedfb43126579204833750e36877940d921cbb01afeb116d9d59f2"
temporary_pgpass=""
release_manifest_commit=""
release_manifest_version=""
release_manifest_build_time=""
release_manifest_purpose=""
release_model_sha256=""
release_catalog_sha256=""
release_payload_verified="0"
observed_forward_database_fingerprint=""
observed_forward_business_fingerprint=""
observed_forward_model_head_revision=""
observed_forward_model_artifact_sha256=""

declare -a runtime_provider_bindings=()
declare -a runtime_provider_credential_ids=()
declare -a runtime_provider_environment=()

failures=0

cleanup() {
  if [[ -n "$temporary_pgpass" ]]; then
    rm -f -- "$temporary_pgpass"
  fi
}
trap cleanup EXIT

pass() {
  printf 'PASS %s\n' "$*"
}

fail() {
  printf 'FAIL %s\n' "$*" >&2
  failures=$((failures + 1))
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "required command is missing: $1"
    return 1
  fi
}

canonical_path() {
  realpath -m -- "$1"
}

decode_upper_hex_ascii() {
  local hex="$1" output="" pair character decimal index
  [[ "$hex" =~ ^([0-9A-F]{2})+$ ]] || return 1
  for ((index = 0; index < ${#hex}; index += 2)); do
    pair="${hex:index:2}"
    decimal=$((16#$pair))
    if (( decimal < 33 || decimal > 126 )); then
      return 1
    fi
    printf -v character '%b' "\\x$pair"
    output+="$character"
  done
  printf '%s' "$output"
}

is_under() {
  local child parent
  child="$(canonical_path "$1")"
  parent="$(canonical_path "$2")"
  [[ "$child" == "$parent" || "$child" == "$parent"/* ]]
}

unit_property() {
  local unit="$1" property="$2"
  systemctl show "$unit" --property="$property" --value 2>/dev/null
}

normalize_word_set() {
  tr '[:space:]' '\n' | sed '/^$/d' | LC_ALL=C sort
}

parse_runtime_provider_bindings() {
  local binding variable credential_id
  local invalid=0
  local -a unsorted=()
  local -A seen_variables=() seen_ids=()
  runtime_provider_bindings=()
  runtime_provider_credential_ids=()
  runtime_provider_environment=()

  if [[ -n "$expected_runtime_provider_credential_bindings" ]]; then
    mapfile -t unsorted < <(
      printf '%s' "$expected_runtime_provider_credential_bindings" |
        tr '[:space:]' '\n' |
        sed '/^$/d'
    )
  fi
  for binding in "${unsorted[@]}"; do
    if [[ "$binding" != *=* || "${binding#*=}" == *"="* ]]; then
      fail "ASCENDANY_EXPECTED_RUNTIME_PROVIDER_CREDENTIAL_BINDINGS contains a malformed binding"
      invalid=1
      continue
    fi
    variable="${binding%%=*}"
    credential_id="${binding#*=}"
    if [[ ! "$variable" =~ ^ASCENDANY_CREDENTIAL_FILE_REF_HEX_([0-9A-F]{2})+_AUTHORITY_HEX_([0-9A-F]{2})+$ ]]; then
      fail "runtime provider credential binding has a noncanonical credential path variable: $variable"
      invalid=1
      continue
    fi
    if [[ ! "$credential_id" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]]; then
      fail "runtime provider credential binding has a noncanonical credential ID"
      invalid=1
      continue
    fi
    case "$credential_id" in
      admin_access_token|admin_password|catalog_publication_request|catalog_publisher_db_password|db_password|runtime_db_password|backup_db_password|migrator_db_password|restore_db_password|jwt_signing_private_key|jwt_verification_public_key|password_pepper)
        fail "runtime provider credential binding reuses a core runtime credential ID: $credential_id"
        invalid=1
        continue
        ;;
    esac
    if [[ -n "${seen_variables[$variable]:-}" ]]; then
      fail "runtime provider credential path variable is repeated: $variable"
      invalid=1
      continue
    fi
    if [[ -n "${seen_ids[$credential_id]:-}" ]]; then
      fail "runtime provider credential ID is bound more than once: $credential_id"
      invalid=1
      continue
    fi
    seen_variables["$variable"]=1
    seen_ids["$credential_id"]=1
    runtime_provider_bindings+=("$binding")
  done

  if (( ${#runtime_provider_bindings[@]} > 0 )); then
    mapfile -t runtime_provider_bindings < <(
      printf '%s\n' "${runtime_provider_bindings[@]}" | LC_ALL=C sort
    )
  fi
  for binding in "${runtime_provider_bindings[@]}"; do
    variable="${binding%%=*}"
    credential_id="${binding#*=}"
    runtime_provider_credential_ids+=("$credential_id")
    runtime_provider_environment+=("$variable=%d/$credential_id")
  done
  (( invalid == 0 ))
}

validate_input_contract() {
  local starting_failures="$failures"
  if [[ "$(id -u)" != 0 ]]; then
    fail "production validation must run as root"
  fi
  case "$validation_phase" in
    staged)
      required_ports="5432 6432"
      expected_write_mode="disabled"
      ascendanyd_active="0"
      ;;
    smoke)
      required_ports="5432 6432 18000"
      expected_write_mode="disabled"
      ascendanyd_active="1"
      ;;
    activation)
      required_ports="5432 6432"
      expected_write_mode="enabled"
      ascendanyd_active="0"
      ;;
    catalog)
      required_ports="5432 6432"
      expected_write_mode="enabled"
      ascendanyd_active="0"
      ;;
    production)
      required_ports="5432 6432 18000"
      expected_write_mode="enabled"
      ascendanyd_active="1"
      ;;
    *)
      fail "ASCENDANY_VALIDATION_PHASE must be exactly staged, smoke, activation, catalog, or production"
      ;;
  esac
  if [[ "$validation_phase" == production ]]; then
    if [[ -z "$agent_acceptance_receipt_path" ]]; then
      fail "production phase requires ASCENDANY_AGENT_ACCEPTANCE_RECEIPT_PATH"
    fi
  elif [[ -n "$agent_acceptance_receipt_path" ]]; then
    fail "staged, smoke, catalog, and activation phases forbid ASCENDANY_AGENT_ACCEPTANCE_RECEIPT_PATH"
  fi
  case "$deployment_transition" in
    initial)
      if [[ -n "$expected_forward_database_fingerprint" ||
            -n "$expected_forward_business_fingerprint" ||
            -n "$expected_forward_model_head_revision" ||
            -n "$expected_forward_model_artifact_sha256" ]]; then
        fail "initial deployment forbids forward-state trust inputs"
      fi
      ;;
    forward)
      if [[ "$validation_phase" == staged ]]; then
        if [[ -n "$expected_forward_database_fingerprint" ||
              -n "$expected_forward_business_fingerprint" ||
              -n "$expected_forward_model_head_revision" ||
              -n "$expected_forward_model_artifact_sha256" ]]; then
          fail "forward staged capture forbids pre-existing forward-state trust inputs"
        fi
      elif [[ ! "$expected_forward_database_fingerprint" =~ ^[0-9a-f]{64}$ ||
              ! "$expected_forward_business_fingerprint" =~ ^[0-9a-f]{64}$ ||
              ! "$expected_forward_model_head_revision" =~ ^[1-9][0-9]*$ ||
              ! "$expected_forward_model_artifact_sha256" =~ ^[0-9a-f]{64}$ ]]; then
        fail "forward smoke, catalog, activation, and production require canonical database, business, model-head, and model-artifact trust inputs"
      fi
      ;;
    *)
      fail "ASCENDANY_DEPLOYMENT_TRANSITION must be exactly initial or forward"
      ;;
  esac
  parse_runtime_provider_bindings || true
  (( failures == starting_failures ))
}

smoke_dropin_required() {
  [[ "$validation_phase" == "staged" || "$validation_phase" == "smoke" ||
     "$validation_phase" == "catalog" || "$validation_phase" == "activation" ]]
}

production_phase() {
  [[ "$validation_phase" == "production" ]]
}

activation_phase() {
  [[ "$validation_phase" == "activation" ]]
}

catalog_phase() {
  [[ "$validation_phase" == "catalog" ]]
}

initial_transition() {
  [[ "$deployment_transition" == initial ]]
}

initial_fresh_phase() {
  initial_transition && [[ "$validation_phase" == staged || "$validation_phase" == smoke ]]
}

forward_transition() {
  [[ "$deployment_transition" == forward ]]
}

forward_preactivation_phase() {
  forward_transition && [[ "$validation_phase" == staged || "$validation_phase" == smoke ]]
}

forward_retained_backup_phase() {
  forward_transition && [[ "$validation_phase" == staged || "$validation_phase" == smoke ]]
}

decimal_increment() {
  local value="$1" index digit carry=1 result=""
  for ((index = ${#value} - 1; index >= 0; index--)); do
    digit="${value:index:1}"
    if (( carry == 1 )); then
      if [[ "$digit" == 9 ]]; then
        digit=0
      else
        digit="$((digit + 1))"
        carry=0
      fi
    fi
    result="$digit$result"
  done
  if (( carry == 1 )); then
    result="1$result"
  fi
  printf '%s\n' "$result"
}

check_effective_value() {
  local unit="$1" property="$2" expected="$3" actual
  if ! actual="$(unit_property "$unit" "$property")"; then
    fail "$unit effective $property cannot be read"
  elif [[ "$actual" != "$expected" ]]; then
    fail "$unit effective $property is ${actual:-<empty>}; expected ${expected:-<empty>}"
  else
    pass "$unit effective $property is ${expected:-empty}"
  fi
}

check_effective_word_set() {
  local unit="$1" property="$2"
  shift 2
  local actual expected actual_normalized expected_normalized
  if ! actual="$(unit_property "$unit" "$property")"; then
    fail "$unit effective $property cannot be read"
    return
  fi
  expected="$(printf '%s\n' "$@")"
  actual_normalized="$(normalize_word_set <<<"$actual")"
  expected_normalized="$(normalize_word_set <<<"$expected")"
  if [[ "$actual_normalized" != "$expected_normalized" ]]; then
    fail "$unit effective $property set differs from the deployment contract"
  else
    pass "$unit effective $property set matches the deployment contract"
  fi
}

check_system_manager_environment() {
  local raw actual
  local expected=$'LANG=zh_CN.UTF-8\nPATH=/usr/local/bin:/usr/bin'
  if ! raw="$(LC_ALL=C systemctl show-environment 2>/dev/null)"; then
    fail "system manager global environment cannot be read"
    return
  fi
  actual="$(printf '%s\n' "$raw" | sed '/^$/d' | LC_ALL=C sort)"
  expected="$(printf '%s\n' "$expected" | LC_ALL=C sort)"
  if [[ "$actual" != "$expected" ]]; then
    fail "system manager global environment differs from the exact km6 production contract"
  else
    pass "system manager global environment contains only the reviewed LANG and PATH"
  fi
}

check_effective_directive_sequence() {
  local unit="$1" directive="$2" expected_text="$3" rendered
  local index
  local -a actual=() expected=()
  if ! rendered="$(systemctl cat "$unit" 2>/dev/null)" || [[ -z "$rendered" ]]; then
    fail "$unit configuration cannot be read for $directive validation"
    return
  fi
  collect_effective_directives "$rendered" "$directive" actual
  if [[ -n "$expected_text" ]]; then
    mapfile -t expected <<<"$expected_text"
  fi
  if (( ${#actual[@]} != ${#expected[@]} )); then
    fail "$unit effective $directive sequence differs from the reviewed raw unit contract"
    return
  fi
  for index in "${!expected[@]}"; do
    if [[ "${actual[$index]}" != "${expected[$index]}" ]]; then
      fail "$unit effective $directive sequence differs from the reviewed raw unit contract"
      return
    fi
  done
  pass "$unit effective $directive sequence matches the reviewed raw unit contract"
}

check_unit_effective_shape() {
  local unit="$1" fragment="$2" working_directory="$3" expected_dropins="$4"
  local expected_start="$5" expected_start_pre="$6" expected_environment="$7"
  check_effective_value "$unit" FragmentPath "$fragment"
  if [[ -n "$expected_dropins" ]]; then
    check_effective_word_set "$unit" DropInPaths "$expected_dropins"
  else
    check_effective_word_set "$unit" DropInPaths
  fi
  check_effective_value "$unit" WorkingDirectory "$working_directory"
  check_effective_directive_sequence "$unit" ExecStart "$expected_start"
  check_effective_directive_sequence "$unit" ExecStartPre "$expected_start_pre"
  check_effective_directive_sequence "$unit" Environment "$expected_environment"
}

render_runtime_provider_dropin() {
  local binding variable credential_id
  printf '[Service]\n'
  for binding in "${runtime_provider_bindings[@]}"; do
    variable="${binding%%=*}"
    credential_id="${binding#*=}"
    printf 'LoadCredentialEncrypted=%s:/etc/ascendany/credentials/%s.cred\n' \
      "$credential_id" "$credential_id"
    printf 'Environment=%s=%%d/%s\n' "$variable" "$credential_id"
  done
}

check_runtime_provider_dropin_bytes() {
  local dropin="$1"
  if ! cmp --silent -- "$dropin" <(render_runtime_provider_dropin); then
    fail "ascendanyd runtime provider credential drop-in bytes differ from the canonical binding contract"
  else
    pass "ascendanyd runtime provider credential drop-in has exact canonical bytes"
  fi
}

check_runtime_provider_dropin_file() {
  local dropin="$1"
  if [[ ! -f "$dropin" || -L "$dropin" ||
        "$dropin" != "$(realpath -m -- "$dropin")" ||
        "$dropin" != "$(realpath -e -- "$dropin" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a:%h' "$dropin" 2>/dev/null || true)" != "0:0:644:1" ]] ||
     ! check_root_owned_ancestry "$dropin" 1; then
    fail "ascendanyd runtime provider credential drop-in must be a canonical root:root 0644 single-link file with protected ancestry"
  else
    check_runtime_provider_dropin_bytes "$dropin"
  fi
}

check_runtime_provider_dropin() {
  local dropin="/etc/systemd/system/ascendanyd.service.d/50-runtime-provider-credentials.conf"
  if (( ${#runtime_provider_bindings[@]} == 0 )); then
    return
  fi
  check_runtime_provider_dropin_file "$dropin"
}

render_read_only_smoke_dropin() {
  printf '%s\n' \
    '[Service]' \
    'EnvironmentFile=' \
    'EnvironmentFile=/etc/ascendany/v2/ascendanyd.env' \
    'EnvironmentFile=/etc/ascendany/v2/ascendanyd-read-only-smoke.env'
}

check_read_only_smoke_dropin_bytes() {
  local dropin="$1"
  if ! cmp --silent -- "$dropin" <(render_read_only_smoke_dropin); then
    fail "read-only smoke drop-in bytes differ from the reviewed contract"
  else
    pass "read-only smoke drop-in has exact canonical bytes"
  fi
}

check_read_only_smoke_dropin() {
  if smoke_dropin_required; then
    if [[ ! -f "$smoke_dropin" || -L "$smoke_dropin" ||
          "$smoke_dropin" != "$(realpath -m -- "$smoke_dropin")" ||
          "$smoke_dropin" != "$(realpath -e -- "$smoke_dropin" 2>/dev/null || true)" ||
          "$(stat -Lc '%u:%g:%a:%h' "$smoke_dropin" 2>/dev/null || true)" != "0:0:644:1" ]] ||
       ! check_root_owned_ancestry "$smoke_dropin" 1; then
      fail "read-only smoke drop-in must be a canonical root:root 0644 single-link file with protected ancestry"
    else
      check_read_only_smoke_dropin_bytes "$smoke_dropin"
    fi
  elif [[ -e "$smoke_dropin" || -L "$smoke_dropin" ]]; then
    fail "production phase forbids the read-only smoke drop-in"
  else
    pass "production phase has no read-only smoke drop-in"
  fi
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
  local dropin="/usr/lib/systemd/system/service.d/10-timeout-abort.conf"
  if [[ ! -f "$dropin" || -L "$dropin" ||
        "$dropin" != "$(realpath -m -- "$dropin")" ||
        "$dropin" != "$(realpath -e -- "$dropin" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a:%h' "$dropin" 2>/dev/null || true)" != "0:0:644:1" ]] ||
     ! check_root_owned_ancestry "$dropin" 1; then
    fail "Fedora global service drop-in must be a canonical root:root 0644 single-link file with protected ancestry"
  else
    check_fedora_global_service_dropin_bytes "$dropin"
  fi
}

check_backup_timer_effective_shape() {
  local unit="ascendany-backup.timer"
  local raw calendar
  check_effective_value "$unit" LoadState loaded
  check_effective_value "$unit" NeedDaemonReload no
  check_effective_value "$unit" FragmentPath /etc/systemd/system/ascendany-backup.timer
  check_effective_word_set "$unit" DropInPaths
  check_effective_value "$unit" Unit ascendany-backup.service
  check_effective_value "$unit" AccuracyUSec 1min
  check_effective_value "$unit" RandomizedDelayUSec 20min
  check_effective_value "$unit" FixedRandomDelay no
  check_effective_value "$unit" Persistent yes
  if ! raw="$(unit_property "$unit" TimersCalendar)"; then
    fail "$unit effective TimersCalendar cannot be read"
  elif ! calendar="$(
    LC_ALL=C sed -nE \
      's/^\{ OnCalendar=(.*) ; next_elapse=.* \}$/\1/p' \
      <<<"$raw"
  )" || [[ "$calendar" != "*-*-* 03:20:00" ]]; then
    fail "$unit effective OnCalendar differs from the reviewed schedule"
  else
    pass "$unit effective OnCalendar matches the reviewed schedule"
  fi
}

check_all_unit_effective_shapes() {
  local global_service_dropin="/usr/lib/systemd/system/service.d/10-timeout-abort.conf"
  local ascendanyd_dropins="$global_service_dropin"
  local ascendanyd_start ascendanyd_pre model_activate_start model_activate_pre model_activate_environment
  local model_register_start model_register_pre model_register_environment
  local catalog_publish_start catalog_publish_pre catalog_publish_environment
  local admin_start admin_pre admin_environment
  local backup_start backup_pre ascendanyd_environment judge_environment backup_environment
  local judge_start lsp_start migrate_start migrate_pre restore_start restore_pre restore_environment environment
  if smoke_dropin_required; then
    ascendanyd_dropins+=$'\n'"$smoke_dropin"
  fi
  if (( ${#runtime_provider_bindings[@]} > 0 )); then
    ascendanyd_dropins+=$'\n/etc/systemd/system/ascendanyd.service.d/50-runtime-provider-credentials.conf'
  fi

  ascendanyd_start='/opt/ascendany/v2/bin/ascendanyd serve'
  ascendanyd_pre=$'/usr/bin/test -s %d/db_password\n/usr/bin/test -s %d/jwt_signing_private_key\n/usr/bin/test -s %d/password_pepper\n/opt/ascendany/v2/bin/ascendany-model verify-catalog --catalog /opt/ascendany/v2/models/recommendation-knowledge-catalog.json --catalog-sha256 ${ASCENDANY_KNOWLEDGE_CATALOG_SHA256} --model /opt/ascendany/v2/models/recommendation-model.json --model-sha256 ${ASCENDANY_RECOMMENDATION_MODEL_SHA256} --expected-purpose ${ASCENDANY_RECOMMENDATION_MODEL_PURPOSE}'
  ascendanyd_environment=$'SHELL=/usr/sbin/nologin\nASCENDANY_DATABASE_PASSWORD_FILE=%d/db_password\nASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE=%d/jwt_signing_private_key\nASCENDANY_PASSWORD_PEPPER_FILE=%d/password_pepper'
  for environment in "${runtime_provider_environment[@]}"; do
    ascendanyd_environment+=$'\n'"$environment"
  done
  model_activate_start='/opt/ascendany/v2/bin/ascendanyd activate-model'
  model_activate_pre=$'/usr/bin/test -s %d/db_password\n/opt/ascendany/v2/bin/ascendany-model verify-catalog --catalog /opt/ascendany/v2/models/recommendation-knowledge-catalog.json --catalog-sha256 ${ASCENDANY_KNOWLEDGE_CATALOG_SHA256} --model /opt/ascendany/v2/models/recommendation-model.json --model-sha256 ${ASCENDANY_RECOMMENDATION_MODEL_SHA256} --expected-purpose ${ASCENDANY_RECOMMENDATION_MODEL_PURPOSE}'
  model_activate_environment=$'SHELL=/usr/sbin/nologin\nASCENDANY_DATABASE_PASSWORD_FILE=%d/db_password'
  model_register_start='/opt/ascendany/v2/bin/ascendanyd register-model'
  model_register_pre="$model_activate_pre"
  model_register_environment="$model_activate_environment"
  catalog_publish_start='/opt/ascendany/v2/bin/ascendany-catalog-publish publish'
  catalog_publish_pre=$'+/usr/bin/test -x /opt/ascendany/v2/bin/ascendany-catalog-publish\n+/usr/bin/test -r /etc/ascendany-catalog-publisher/catalog-publish.env\n+/usr/bin/test -f /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred\n+/usr/bin/test ! -L /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred\n+/usr/bin/test -s /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred\n+/usr/bin/test -f /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred\n+/usr/bin/test ! -L /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred\n+/usr/bin/test -s /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred\n/usr/bin/test -s %d/catalog_publisher_db_password\n/usr/bin/test -s %d/jwt_verification_public_key\n/usr/bin/test -s %d/catalog_publication_request\n/usr/bin/test -s %d/admin_access_token'
  catalog_publish_environment=$'SHELL=/usr/sbin/nologin\nASCENDANY_DATABASE_PASSWORD_FILE=%d/catalog_publisher_db_password\nASCENDANY_JWT_VERIFICATION_PUBLIC_KEY_FILE=%d/jwt_verification_public_key'
  backup_start='/opt/ascendany/v2/bin/ascendany-backup create'
  backup_pre='/usr/bin/test -s %d/backup_db_password'
  backup_environment=$'ASCENDANY_DATABASE_PASSWORD_FILE=%d/backup_db_password\nASCENDANY_BACKUP_RUNTIME_ROOT=/run/ascendany-backup'
  judge_start='/opt/ascendany/v2/bin/ascendany-judge run --job-id %i --control-socket /run/ascendany-judge/%i.sock --work-root /var/lib/ascendany-judge/jobs/%i --allowed-client-user ascendany --compiler-image ${ASCENDANY_JUDGE_COMPILER_IMAGE} --runtime-image ${ASCENDANY_JUDGE_RUNTIME_IMAGE} --podman-binary /usr/bin/podman --delegated-cgroup-root /sys/fs/cgroup'
  judge_environment=$'HOME=/var/lib/ascendany-judge\nXDG_RUNTIME_DIR=/run/ascendany-judge-podman/%i\nXDG_DATA_HOME=/var/lib/ascendany-judge/.local/share\nXDG_CONFIG_HOME=/var/lib/ascendany-judge/.config\nXDG_CACHE_HOME=/var/lib/ascendany-judge/.cache'
  lsp_start='/opt/ascendany/v2/bin/ascendany-lsp serve --session-id %i --control-socket /run/ascendany-lsp-control/control.sock --workspace /tmp/ascendany-lsp-sessions/%i'
  migrate_start='/opt/ascendany/v2/bin/ascendany-migrate up'
  migrate_pre='/usr/bin/test -s %d/migrator_db_password'
  admin_start='/opt/ascendany/v2/bin/ascendany-admin-bootstrap create --username admin --display-name admin'
  admin_pre=$'+/usr/bin/test -x /opt/ascendany/v2/bin/ascendany-admin-bootstrap\n+/usr/bin/test -r /etc/ascendany/v2/ascendanyd.env\n+/usr/bin/test -s /run/ascendany-admin-bootstrap-input/admin_password.cred\n/usr/bin/test -s %d/db_password\n/usr/bin/test -s %d/password_pepper\n/usr/bin/test -s %d/admin_password'
  admin_environment=$'ASCENDANY_DATABASE_PASSWORD_FILE=%d/db_password\nASCENDANY_PASSWORD_PEPPER_FILE=%d/password_pepper'
  restore_start='/opt/ascendany/v2/scripts/restore-verify-operator.sh run %i'
  restore_pre=$'/usr/bin/test -s %d/restore_db_password\n/usr/bin/test -f /run/ascendany-restore-operator/operator.lock\n/usr/bin/test -f /run/ascendany-restore-operator/publication.lock'
  restore_environment=$'ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE=%d/restore_db_password\nASCENDANY_RESTORE_RUNTIME_ROOT=%t/ascendany-restore-verify-%i'

  check_unit_effective_shape \
    ascendanyd.service \
    /etc/systemd/system/ascendanyd.service \
    /var/lib/ascendany \
    "$ascendanyd_dropins" \
    "$ascendanyd_start" \
    "$ascendanyd_pre" \
    "$ascendanyd_environment"
  check_unit_effective_shape \
    ascendany-model-activate.service \
    /etc/systemd/system/ascendany-model-activate.service \
    /var/lib/ascendany \
    "$global_service_dropin" \
    "$model_activate_start" \
    "$model_activate_pre" \
    "$model_activate_environment"
  check_unit_effective_shape \
    ascendany-model-register.service \
    /etc/systemd/system/ascendany-model-register.service \
    /var/lib/ascendany \
    "$global_service_dropin" \
    "$model_register_start" \
    "$model_register_pre" \
    "$model_register_environment"
  check_unit_effective_shape \
    ascendany-catalog-publish.service \
    /etc/systemd/system/ascendany-catalog-publish.service \
    /var/lib/ascendany-catalog-publisher \
    "$global_service_dropin" \
    "$catalog_publish_start" \
    "$catalog_publish_pre" \
    "$catalog_publish_environment"
  check_effective_directive_sequence \
    ascendany-catalog-publish.service \
    ExecStartPost \
    $'/usr/bin/test -d /var/lib/ascendany-catalog-publisher/receipts\n+/usr/bin/rm -f -- /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred'
  check_effective_directive_sequence \
    ascendany-catalog-publish.service \
    ExecStopPost \
    ''
  check_unit_effective_shape \
    ascendany-judge@validation.service \
    /etc/systemd/system/ascendany-judge@.service \
    /var/lib/ascendany-judge \
    "$global_service_dropin" \
    "$judge_start" \
    "" \
    "$judge_environment"
  check_unit_effective_shape \
    ascendany-lsp@validation.service \
    /etc/systemd/system/ascendany-lsp@.service \
    /tmp \
    "$global_service_dropin" \
    "$lsp_start" \
    "" \
    ""
  check_unit_effective_shape \
    ascendany-admin-bootstrap.service \
    /etc/systemd/system/ascendany-admin-bootstrap.service \
    /var/lib/ascendany \
    "$global_service_dropin" \
    "$admin_start" \
    "$admin_pre" \
    "$admin_environment"
  check_effective_directive_sequence \
    ascendany-admin-bootstrap.service \
    ExecStopPost \
    '+/usr/bin/rm -f -- /run/ascendany-admin-bootstrap-input/admin_password.cred'
  check_unit_effective_shape \
    ascendany-backup.service \
    /etc/systemd/system/ascendany-backup.service \
    /var/backups/ascendany \
    "$global_service_dropin" \
    "$backup_start" \
    "$backup_pre" \
    "$backup_environment"
  check_unit_effective_shape \
    ascendany-migrate.service \
    /etc/systemd/system/ascendany-migrate.service \
    /var/lib/ascendany-migrate \
    "$global_service_dropin" \
    "$migrate_start" \
    "$migrate_pre" \
    'ASCENDANY_DATABASE_PASSWORD_FILE=%d/migrator_db_password'
  check_unit_effective_shape \
    ascendany-restore-verify@validation.service \
    /etc/systemd/system/ascendany-restore-verify@.service \
    /var/lib/ascendany-restore \
    "$global_service_dropin" \
    "$restore_start" \
    "$restore_pre" \
    "$restore_environment"
  check_effective_directive_sequence \
    ascendany-restore-verify@validation.service \
    ExecStartPost \
    '+/opt/ascendany/v2/scripts/publish-restore-evidence.sh %i'
  check_effective_value ascendanyd.service StandardOutput journal
  check_effective_value ascendanyd.service StandardError journal
  check_effective_value ascendanyd.service MemoryPressureWatch yes
  check_effective_value ascendanyd.service MemoryPressureThresholdUSec 200ms
  check_effective_value ascendany-model-activate.service Type oneshot
  check_effective_value ascendany-model-register.service Type oneshot
  check_effective_value ascendany-catalog-publish.service Type oneshot
  check_effective_value ascendany-catalog-publish.service NoNewPrivileges yes
  check_effective_value ascendany-catalog-publish.service ProtectSystem strict
  check_effective_value ascendany-catalog-publish.service ProtectHome yes
  check_effective_value ascendany-catalog-publish.service ProtectProc invisible
  check_effective_value ascendany-catalog-publish.service ProcSubset pid
  check_effective_value ascendany-catalog-publish.service PrivateDevices yes
  check_effective_value ascendany-catalog-publish.service DevicePolicy closed
  check_effective_value ascendany-catalog-publish.service RestrictNamespaces yes
  check_effective_value ascendany-catalog-publish.service MemoryDenyWriteExecute yes
  check_effective_word_set ascendany-catalog-publish.service RestrictAddressFamilies AF_UNIX AF_INET AF_INET6
  check_effective_word_set ascendany-catalog-publish.service ReadWritePaths \
    /var/lib/ascendany-catalog-publisher/pending /var/lib/ascendany-catalog-publisher/receipts
  check_effective_word_set ascendany-catalog-publish.service InaccessiblePaths \
    /etc/ascendany/credentials /etc/ascendany/v2 /var/backups/ascendany \
    /var/lib/ascendany
  check_effective_value ascendany-model-activate.service NoNewPrivileges yes
  check_effective_value ascendany-model-register.service NoNewPrivileges yes
  check_effective_value ascendany-model-activate.service ProtectSystem strict
  check_effective_value ascendany-model-register.service ProtectSystem strict
  check_effective_value ascendany-model-activate.service ProtectHome yes
  check_effective_value ascendany-model-register.service ProtectHome yes
  check_effective_value ascendany-model-activate.service ProtectProc invisible
  check_effective_value ascendany-model-register.service ProtectProc invisible
  check_effective_value ascendany-model-activate.service ProcSubset pid
  check_effective_value ascendany-model-register.service ProcSubset pid
  check_effective_value ascendany-model-activate.service PrivateDevices yes
  check_effective_value ascendany-model-register.service PrivateDevices yes
  check_effective_value ascendany-model-activate.service DevicePolicy closed
  check_effective_value ascendany-model-register.service DevicePolicy closed
  check_effective_value ascendany-model-activate.service RestrictNamespaces yes
  check_effective_value ascendany-model-register.service RestrictNamespaces yes
  check_effective_value ascendany-model-activate.service MemoryDenyWriteExecute yes
  check_effective_value ascendany-model-register.service MemoryDenyWriteExecute yes
  check_effective_word_set ascendany-model-activate.service RestrictAddressFamilies AF_UNIX AF_INET AF_INET6
  check_effective_word_set ascendany-model-register.service RestrictAddressFamilies AF_UNIX AF_INET AF_INET6
  check_effective_word_set ascendany-model-activate.service ReadWritePaths /var/lib/ascendany
  check_effective_word_set ascendany-model-register.service ReadWritePaths /var/lib/ascendany
  check_effective_word_set ascendany-model-activate.service InaccessiblePaths \
    /var/lib/ascendany/artifacts /var/backups/ascendany /var/lib/ascendany-catalog-publisher
  check_effective_word_set ascendany-model-register.service InaccessiblePaths \
    /var/lib/ascendany/artifacts /var/backups/ascendany /var/lib/ascendany-catalog-publisher
  check_effective_word_set ascendanyd.service InaccessiblePaths \
    /var/lib/ascendany-catalog-publisher
  check_effective_value ascendanyd.service TimeoutStopFailureMode abort
  check_effective_value ascendany-model-activate.service TimeoutStopFailureMode abort
  check_effective_value ascendany-model-register.service TimeoutStopFailureMode abort
  check_effective_value ascendany-catalog-publish.service TimeoutStopFailureMode abort
  check_effective_value ascendany-judge@validation.service TimeoutStopFailureMode abort
  check_effective_value ascendany-lsp@validation.service TimeoutStopFailureMode abort
  check_effective_value ascendany-backup.service TimeoutStopFailureMode abort
  check_effective_value ascendany-admin-bootstrap.service TimeoutStopFailureMode abort
  check_effective_value ascendany-migrate.service TimeoutStopFailureMode abort
  check_effective_value ascendany-restore-verify@validation.service TimeoutStopFailureMode abort
  check_backup_timer_effective_shape
  check_fedora_global_service_dropin
  check_read_only_smoke_dropin
  check_runtime_provider_dropin
}

check_unit_identity() {
  local unit="$1" expected_user="$2" expected_group="$3"
  shift 3
  local load_state need_reload user group supplementary expected_supplementary
  load_state="$(unit_property "$unit" LoadState || true)"
  if [[ "$load_state" != "loaded" ]]; then
    fail "$unit is not loaded"
    return
  fi
  need_reload="$(unit_property "$unit" NeedDaemonReload || true)"
  if [[ "$need_reload" != "no" ]]; then
    fail "$unit requires daemon-reload before its effective configuration can be validated"
  fi
  user="$(unit_property "$unit" User || true)"
  group="$(unit_property "$unit" Group || true)"
  supplementary="$(unit_property "$unit" SupplementaryGroups || true)"
  expected_supplementary="$(printf '%s\n' "$@" | normalize_word_set)"
  if [[ "$user" != "$expected_user" || "$group" != "$expected_group" ||
        "$(normalize_word_set <<<"$supplementary")" != "$expected_supplementary" ]]; then
    fail "$unit effective User/Group/SupplementaryGroups differ from the capability identity"
  else
    pass "$unit uses the exact $expected_user:$expected_group capability identity"
  fi
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

check_credential_source() {
  local unit="$1" credential_id="$2" source="$3"
  if [[ ! -s "$source" || ! -f "$source" || -L "$source" ||
        "$(stat -c '%u:%g:%a:%h' "$source" 2>/dev/null || true)" != "0:0:400:1" ||
        ! "$source" =~ ^/ || "$source" != "$(realpath -m -- "$source")" ||
        "$source" != "$(realpath -e -- "$source" 2>/dev/null || true)" ]] ||
     ! check_root_owned_ancestry "$source" 0; then
    fail "$unit encrypted credential $credential_id must be a real root:root 0400 single-link file with root-owned non-writable ancestry"
    return 1
  elif is_under "$source" "$release_root"; then
    fail "$unit encrypted credential $credential_id is stored under the release root: $source"
    return 1
  else
    pass "$unit encrypted credential $credential_id has a protected external source"
    return 0
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

check_unit_credentials() {
  local unit="$1"
  shift
  local rendered entry credential_id source expected actual
  local invalid=0
  local -a plaintext=() encrypted=()
  local -A seen=()
  if ! rendered="$(systemctl cat "$unit" 2>/dev/null)" || [[ -z "$rendered" ]]; then
    fail "$unit configuration cannot be read for credential validation"
    return
  fi
  collect_effective_directives "$rendered" LoadCredential plaintext
  collect_effective_directives "$rendered" LoadCredentialEncrypted encrypted
  if (( ${#plaintext[@]} != 0 )); then
    fail "$unit has an effective plaintext LoadCredential directive"
    invalid=1
  else
    pass "$unit has no effective plaintext LoadCredential directive"
  fi
  for entry in "${encrypted[@]}"; do
    credential_id="${entry%%:*}"
    source="${entry#*:}"
    if [[ "$source" == "$entry" || ! "$credential_id" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ||
          "$source" != /* || -n "${seen[$credential_id]:-}" ]]; then
      fail "$unit has a duplicate or noncanonical LoadCredentialEncrypted entry"
      invalid=1
      continue
    fi
    seen["$credential_id"]=1
    if ! check_credential_source "$unit" "$credential_id" "$source"; then
      invalid=1
    fi
  done
  actual="$(printf '%s\n' "${!seen[@]}" | normalize_word_set)"
  expected="$(printf '%s\n' "$@" | normalize_word_set)"
  if [[ "$actual" != "$expected" ]]; then
    fail "$unit effective LoadCredentialEncrypted IDs differ from the exact expected set"
    invalid=1
  elif (( invalid == 0 )); then
    pass "$unit effective encrypted credential ID set is exact"
  fi
}

check_environment_file() {
  local unit="$1" path="$2" metadata owner mode
  if [[ ! -f "$path" || -L "$path" || "$path" != "$(realpath -m -- "$path")" ||
        "$path" != "$(realpath -e -- "$path" 2>/dev/null || true)" ]] ||
     ! check_root_owned_ancestry "$path" 0; then
    fail "$unit EnvironmentFile is missing, linked, or has unsafe ancestry: $path"
    return
  fi
  metadata="$(stat -c '%u:%a' "$path" 2>/dev/null || true)"
  IFS=: read -r owner mode <<<"$metadata"
  if [[ "$owner" != "0" ]] || (( 8#$mode & 8#022 )); then
    fail "$unit EnvironmentFile must be root-owned and non-writable by group/other: $path"
  else
    pass "$unit EnvironmentFile is root-owned and immutable to service identities"
  fi
}

check_unit_environment_files_with_policy() {
  local unit="$1" ignore_errors="$2"
  shift 2
  local raw actual expected path
  [[ "$ignore_errors" == "yes" || "$ignore_errors" == "no" ]] || {
    fail "$unit EnvironmentFiles validation policy is invalid"
    return
  }
  if ! raw="$(unit_property "$unit" EnvironmentFiles)"; then
    fail "$unit effective EnvironmentFiles cannot be read"
    return
  fi
  actual="$(sed '/^$/d' <<<"$raw" | LC_ALL=C sort)"
  expected="$(printf '%s\n' "$@" | sed "/^$/d; s/\$/ (ignore_errors=${ignore_errors})/" | LC_ALL=C sort)"
  if [[ "$actual" != "$expected" ]]; then
    fail "$unit effective EnvironmentFiles set differs from the exact required set"
    return
  fi
  for path in "$@"; do
    check_environment_file "$unit" "$path"
  done
  pass "$unit effective EnvironmentFiles set is exact"
}

check_unit_environment_files() {
  local unit="$1"
  shift
  check_unit_environment_files_with_policy "$unit" no "$@"
}

check_unit_optional_environment_files() {
  local unit="$1"
  shift
  check_unit_environment_files_with_policy "$unit" yes "$@"
}

check_ascendanyd_config_contract() {
  local path="${1:-/etc/ascendany/v2/ascendanyd.env}"
  local smoke_path="${2:-/etc/ascendany/v2/ascendanyd-read-only-smoke.env}"
  local configured_model_sha256="" configured_catalog_sha256=""
  local -a write_lines=() listen_lines=() model_path_lines=() model_sha_lines=() model_purpose_lines=()
  local -a catalog_path_lines=() catalog_sha_lines=() smoke_entries=()
  mapfile -t write_lines < <(grep -E '^ASCENDANY_WRITE_MODE=' "$path" 2>/dev/null || true)
  mapfile -t listen_lines < <(grep -E '^ASCENDANY_HTTP_LISTEN=' "$path" 2>/dev/null || true)
  mapfile -t model_path_lines < <(grep -E '^ASCENDANY_RECOMMENDATION_MODEL_PATH=' "$path" 2>/dev/null || true)
  mapfile -t model_sha_lines < <(grep -E '^ASCENDANY_RECOMMENDATION_MODEL_SHA256=' "$path" 2>/dev/null || true)
  mapfile -t model_purpose_lines < <(grep -E '^ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=' "$path" 2>/dev/null || true)
  mapfile -t catalog_path_lines < <(grep -E '^ASCENDANY_KNOWLEDGE_CATALOG_PATH=' "$path" 2>/dev/null || true)
  mapfile -t catalog_sha_lines < <(grep -E '^ASCENDANY_KNOWLEDGE_CATALOG_SHA256=' "$path" 2>/dev/null || true)
  mapfile -t smoke_entries < <(sed '/^#/d; /^$/d' "$smoke_path" 2>/dev/null || true)
  if (( ${#model_sha_lines[@]} == 1 )) &&
     [[ "${model_sha_lines[0]}" =~ ^ASCENDANY_RECOMMENDATION_MODEL_SHA256=([0-9a-f]{64})$ ]]; then
    configured_model_sha256="${BASH_REMATCH[1]}"
  fi
  if (( ${#catalog_sha_lines[@]} == 1 )) &&
     [[ "${catalog_sha_lines[0]}" =~ ^ASCENDANY_KNOWLEDGE_CATALOG_SHA256=([0-9a-f]{64})$ ]]; then
    configured_catalog_sha256="${BASH_REMATCH[1]}"
  fi
  if (( ${#write_lines[@]} != 1 )) || [[ "${write_lines[0]:-}" != "ASCENDANY_WRITE_MODE=enabled" ]]; then
    fail "ascendanyd.env must contain one production write-mode value: enabled"
  elif (( ${#listen_lines[@]} != 1 )) || [[ "${listen_lines[0]:-}" != "ASCENDANY_HTTP_LISTEN=127.0.0.1:18000" ]]; then
    fail "ascendanyd.env must contain the fixed v2 loopback listener 127.0.0.1:18000"
  elif (( ${#model_path_lines[@]} != 1 )) ||
       [[ "${model_path_lines[0]:-}" != "ASCENDANY_RECOMMENDATION_MODEL_PATH=/opt/ascendany/v2/models/recommendation-model.json" ]]; then
    fail "ascendanyd.env must bind the immutable release recommendation model path"
  elif [[ -z "$configured_model_sha256" || -z "$release_model_sha256" ||
          "$configured_model_sha256" != "$release_model_sha256" ]]; then
    fail "ascendanyd.env recommendation model digest differs from the release manifest"
  elif (( ${#model_purpose_lines[@]} != 1 )) ||
       [[ "${model_purpose_lines[0]:-}" != "ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=production" ||
          "$release_manifest_purpose" != production ]]; then
    fail "ascendanyd.env and release manifest must authorize only a production recommendation model"
  elif (( ${#catalog_path_lines[@]} != 1 )) ||
       [[ "${catalog_path_lines[0]:-}" != "ASCENDANY_KNOWLEDGE_CATALOG_PATH=/opt/ascendany/v2/models/recommendation-knowledge-catalog.json" ]]; then
    fail "ascendanyd.env must bind the immutable release knowledge catalog path"
  elif [[ -z "$configured_catalog_sha256" || -z "$release_catalog_sha256" ||
          "$configured_catalog_sha256" != "$release_catalog_sha256" ]]; then
    fail "ascendanyd.env knowledge catalog digest differs from the release manifest"
  elif (( ${#smoke_entries[@]} != 1 )) || [[ "${smoke_entries[0]:-}" != "ASCENDANY_WRITE_MODE=disabled" ]]; then
    fail "ascendanyd read-only smoke environment must contain only the disabled write mode"
  else
    pass "ascendanyd production and read-only smoke environments own exact write modes on loopback port 18000"
  fi
}

check_ascendanyd_phase_state() {
  local active_state enabled_state
  active_state="$(unit_property ascendanyd.service ActiveState || true)"
  enabled_state="$(systemctl is-enabled ascendanyd.service 2>/dev/null || true)"
  if [[ "$validation_phase" == "staged" || "$validation_phase" == "catalog" ||
        "$validation_phase" == "activation" ]]; then
    if [[ "$active_state" != "inactive" ]]; then
      fail "$validation_phase phase requires ascendanyd.service to be inactive"
    else
      pass "$validation_phase phase keeps ascendanyd.service inactive"
    fi
  elif [[ "$active_state" != "active" ]]; then
    fail "$validation_phase phase requires ascendanyd.service to be active"
  else
    pass "$validation_phase phase has an active ascendanyd.service"
  fi

  if production_phase; then
    if [[ "$enabled_state" != "enabled" ]]; then
      fail "production phase requires ascendanyd.service to be enabled"
    else
      pass "production phase enables ascendanyd.service"
    fi
  elif [[ "$enabled_state" != "disabled" ]]; then
    fail "$validation_phase phase requires ascendanyd.service to remain disabled"
  else
    pass "$validation_phase phase keeps ascendanyd.service disabled"
  fi
}

check_model_release_oneshot_unit_state() {
  local unit="$1" capability="$2" require_success="$3"
  local active_state enabled_state
  active_state="$(unit_property "$unit" ActiveState || true)"
  enabled_state="$(systemctl is-enabled "$unit" 2>/dev/null || true)"
  if [[ "$active_state" != inactive || "$enabled_state" != static ]]; then
    fail "$unit must remain inactive and static outside its explicit one-shot window"
    return
  fi
  if [[ "$require_success" == 1 ]]; then
    pass "$unit is inactive; the phase database contract owns durable model $capability evidence"
  else
    pass "$unit is inactive and cannot be enabled for boot"
  fi
}

check_model_registration_unit_state() {
  local require_success=0
  if activation_phase || catalog_phase || production_phase; then
    require_success=1
  fi
  check_model_release_oneshot_unit_state \
    ascendany-model-register.service registration "$require_success"
}

check_model_activation_unit_state() {
  local require_success=0
  if activation_phase || production_phase; then
    require_success=1
  fi
  check_model_release_oneshot_unit_state \
    ascendany-model-activate.service activation "$require_success"
}

check_inactive_backup_timer() {
  local active_state enabled_state
  active_state="$(unit_property ascendany-backup.timer ActiveState || true)"
  enabled_state="$(systemctl is-enabled ascendany-backup.timer 2>/dev/null || true)"
  if [[ "$active_state" != "inactive" || "$enabled_state" != "disabled" ]]; then
    fail "$validation_phase phase requires ascendany-backup.timer to be disabled and inactive"
  else
    pass "$validation_phase phase keeps ascendany-backup.timer disabled and inactive"
  fi
}

check_worker_isolation() {
  local unit="$1" rendered effective_environment
  local -a environment=()
  check_unit_credentials "$unit"
  if ! rendered="$(systemctl cat "$unit" 2>/dev/null)" || [[ -z "$rendered" ]]; then
    fail "$unit configuration cannot be read for secret environment validation"
  else
    collect_effective_directives "$rendered" Environment environment
    effective_environment="$(printf '%s\n' "${environment[@]}")"
  fi
  if [[ -z "${rendered:-}" ]]; then
    :
  elif grep -Eq '(^|[[:space:]])[^=]*(DB_|DATABASE|PASSWORD|JWT|SECRET|TOKEN)[^=]*=' <<<"$effective_environment"; then
    fail "$unit receives a database or secret environment variable"
  else
    pass "$unit has no database/secret environment"
  fi
  check_effective_value "$unit" PrivateNetwork yes
  check_effective_value "$unit" DevicePolicy closed
  check_effective_value "$unit" ProtectSystem strict
  if [[ "$unit" == "ascendany-judge@validation.service" ]]; then
    check_effective_value "$unit" ProtectControlGroupsEx private
    check_effective_word_set "$unit" ReadWritePaths \
      /var/lib/ascendany-judge \
      /run/ascendany-judge \
      /run/ascendany-judge-podman
  fi
}

run_as_judge() {
  (
    cd /var/lib/ascendany-judge || exit 1
    exec /usr/bin/runuser -u ascendany-judge -- /usr/bin/env -i \
      PATH=/usr/bin:/bin \
      LANG=C.UTF-8 \
      HOME=/var/lib/ascendany-judge \
      XDG_RUNTIME_DIR=/run/ascendany-judge-image-podman \
      XDG_DATA_HOME=/var/lib/ascendany-judge/.local/share \
      XDG_CONFIG_HOME=/var/lib/ascendany-judge/.config \
      XDG_CACHE_HOME=/var/lib/ascendany-judge/.cache \
      "$@"
  )
}

check_judge_runtime() {
  local unit="ascendany-judge@validation.service"
  local no_new_privileges ambient bounding compiler_image runtime_image locked_compiler locked_runtime env_file polkit_rule judge_uid judge_gid runtime_gid
  no_new_privileges="$(unit_property "$unit" NoNewPrivileges || true)"
  ambient="$(unit_property "$unit" AmbientCapabilities || true)"
  bounding="$(unit_property "$unit" CapabilityBoundingSet || true)"
  if [[ "$no_new_privileges" != "no" ]]; then
    fail "$unit blocks the rootless newuidmap/newgidmap helpers"
  elif [[ -n "$ambient" ]]; then
    fail "$unit grants ambient capabilities: $ambient"
  elif [[ " $bounding " != *" cap_setuid "* || " $bounding " != *" cap_setgid "* ]]; then
    fail "$unit lacks the two rootless user-namespace helper capabilities: $bounding"
  else
    local remaining=" ${bounding//cap_setuid/} "
    remaining=" ${remaining//cap_setgid/} "
    if [[ -n "${remaining//[[:space:]]/}" ]]; then
      fail "$unit capability boundary is broader than CAP_SETUID/CAP_SETGID: $bounding"
    else
      pass "$unit grants no ambient capability and bounds helpers to CAP_SETUID/CAP_SETGID"
    fi
  fi
  check_effective_value "$unit" Delegate yes
  check_effective_word_set "$unit" DelegateControllers cpu memory pids
  check_effective_value "$unit" DelegateSubgroup supervisor
  check_effective_value "$unit" RuntimeDirectory ascendany-judge-podman/validation
  check_effective_value "$unit" RuntimeDirectoryMode 0700
  check_effective_value "$unit" RuntimeDirectoryPreserve no
  check_effective_value "$unit" ProtectHostname no
  check_effective_value "$unit" PrivateTmp no
  check_effective_word_set "$unit" TemporaryFileSystem \
    /tmp:rw,nosuid,nodev,noexec \
    /var/tmp:rw,nosuid,nodev,noexec
  check_effective_value "$unit" ProtectKernelTunables no
  check_effective_value "$unit" ProtectKernelLogs no
  check_effective_value "$unit" ProtectProc invisible
  check_effective_value "$unit" ProcSubset all
  check_effective_value "$unit" RemoveIPC no

  if [[ ! -d /var/empty || -n "$(find /var/empty -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ||
        "$(stat -c '%u:%a' /var/empty 2>/dev/null || true)" != "0:755" ]]; then
    fail "/var/empty is not an exact root-owned empty 0755 OCI hooks directory"
  else
    pass "rootless Judge OCI hooks directory is empty and root-owned"
  fi

  judge_uid="$(id -u ascendany-judge 2>/dev/null || true)"
  judge_gid="$(id -g ascendany-judge 2>/dev/null || true)"
  runtime_gid="$(getent group ascendany-runtime 2>/dev/null | cut -d: -f3 || true)"
  if [[ -z "$judge_uid" || -z "$runtime_gid" ||
        ! -d /run/ascendany-judge || -L /run/ascendany-judge ||
        "/run/ascendany-judge" != "$(realpath -e -- /run/ascendany-judge 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a' /run/ascendany-judge 2>/dev/null || true)" != "$judge_uid:$runtime_gid:2770" ]]; then
    fail "Judge socket directory is not the exact persistent setgid 2770 boundary"
  else
    pass "Judge socket directory has one persistent tmpfiles owner"
  fi

  if [[ -z "$judge_uid" || -z "$judge_gid" ||
        ! -d /run/ascendany-judge-podman || -L /run/ascendany-judge-podman ||
        "/run/ascendany-judge-podman" != "$(realpath -e -- /run/ascendany-judge-podman 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a' /run/ascendany-judge-podman 2>/dev/null || true)" != "$judge_uid:$judge_gid:700" ||
        -n "$(find /run/ascendany-judge-podman -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
    fail "rootless Judge per-job runtime parent is not an empty dedicated 0700 boundary"
  else
    pass "rootless Judge per-job runtime parent is empty and private"
  fi

  if [[ -z "$judge_uid" || -z "$judge_gid" ||
        ! -d /run/ascendany-judge-image-podman || -L /run/ascendany-judge-image-podman ||
        "/run/ascendany-judge-image-podman" != "$(realpath -e -- /run/ascendany-judge-image-podman 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a' /run/ascendany-judge-image-podman 2>/dev/null || true)" != "$judge_uid:$judge_gid:700" ]]; then
    fail "rootless Judge image-operator runtime is not the exact dedicated 0700 boundary"
  else
    pass "rootless Judge image-operator runtime is dedicated and private"
  fi

  polkit_rule="/etc/polkit-1/rules.d/60-ascendany-judge.rules"
  if [[ ! -f "$polkit_rule" || "$(stat -c '%u:%g:%a' "$polkit_rule" 2>/dev/null || true)" != "0:0:644" ]]; then
    fail "Judge polkit rule is missing or not root:root 0644"
  else
    pass "Judge systemd authorization rule is root-owned"
  fi

  env_file="/etc/ascendany/v2/judge.env"
  compiler_image="$(grep -E '^ASCENDANY_JUDGE_COMPILER_IMAGE=[a-z0-9][a-z0-9._:/-]{0,255}@sha256:[0-9a-f]{64}$' "$env_file" 2>/dev/null | cut -d= -f2- || true)"
  runtime_image="$(grep -E '^ASCENDANY_JUDGE_RUNTIME_IMAGE=[a-z0-9][a-z0-9._:/-]{0,255}@sha256:[0-9a-f]{64}$' "$env_file" 2>/dev/null | cut -d= -f2- || true)"
  locked_compiler="$(jq -er '.compiler.identity' /opt/ascendany/v2/config/judge-image-lock.json 2>/dev/null || true)"
  locked_runtime="$(jq -er '.runtime.identity' /opt/ascendany/v2/config/judge-image-lock.json 2>/dev/null || true)"
  if [[ -z "$compiler_image" || -z "$runtime_image" ]]; then
    fail "judge.env lacks one of its two digest-pinned images"
  elif [[ "$compiler_image" != "$locked_compiler" || "$runtime_image" != "$locked_runtime" || "$compiler_image" == "$runtime_image" ]]; then
    fail "judge.env does not select the two release-bound image identities"
  elif ! run_as_judge /usr/bin/podman --cgroup-manager=cgroupfs \
      --runroot=/run/ascendany-judge-image-podman/containers image exists "$compiler_image"; then
    fail "digest-pinned Judge compiler image is not preloaded for ascendany-judge: $compiler_image"
  elif ! run_as_judge /usr/bin/podman --cgroup-manager=cgroupfs \
      --runroot=/run/ascendany-judge-image-podman/containers image exists "$runtime_image"; then
    fail "digest-pinned Judge runtime image is not preloaded for ascendany-judge: $runtime_image"
  elif ! run_as_judge /opt/ascendany/v2/scripts/attest-judge-image.sh >/dev/null; then
    fail "preloaded Judge images failed release-bound rootfs and static-execution attestation"
  else
    pass "release-bound Judge compiler and empty runtime images are attested for ascendany-judge"
  fi
}

check_lsp_runtime() {
  local unit="ascendany-lsp@validation.service"
  local polkit_rule root actual expected metadata groups
  check_effective_value "$unit" NoNewPrivileges yes
  check_effective_value "$unit" PrivateTmp yes
  check_effective_value "$unit" PrivateTmpEx disconnected
  check_effective_value "$unit" PrivatePIDs yes
  check_effective_value "$unit" PrivateDevices yes
  check_effective_value "$unit" LimitFSIZE 33554432
  check_effective_value "$unit" StateDirectory ""
  check_effective_value "$unit" ReadWritePaths ""
  check_effective_value "$unit" RootDirectory /var/lib/ascendany-lsp-root
  check_effective_value "$unit" MountAPIVFS yes

  groups="$(id -nG ascendany-lsp 2>/dev/null | normalize_word_set)"
  expected="$(printf '%s\n' ascendany-lsp ascendany-lsp-control | normalize_word_set)"
  if [[ "$groups" != "$expected" ]]; then
    fail "ascendany-lsp OS identity has a group outside the dedicated control boundary"
  else
    pass "ascendany-lsp belongs only to its primary and dedicated control groups"
  fi

  check_lsp_control_socket

  root=/var/lib/ascendany-lsp-root
  expected=$'bin\ndev\netc\nhome\nlib\nlib64\nopt\nproc\nrun\nsys\ntmp\nusr\nvar'
  actual="$(find "$root" -mindepth 1 -maxdepth 1 -printf '%f\n' 2>/dev/null | sort || true)"
  if [[ ! -d "$root" || -L "$root" || "$(stat -c '%u:%g:%a' "$root" 2>/dev/null || true)" != '0:0:755' || "$actual" != "$expected" ]]; then
    fail "LSP RootDirectory top-level skeleton is not the exact root-owned closure"
  else
    pass "LSP RootDirectory top-level skeleton is exact and root-owned"
  fi
  for entry in bin:usr/bin lib:usr/lib lib64:usr/lib64; do
    local name="${entry%%:*}" target="${entry#*:}"
    if [[ ! -L "$root/$name" || "$(readlink -- "$root/$name" 2>/dev/null || true)" != "$target" ||
          "$(stat -c '%u:%g:%a' "$root/$name" 2>/dev/null || true)" != '0:0:777' ]]; then
      fail "LSP RootDirectory $name link differs from the reviewed /usr closure"
    else
      pass "LSP RootDirectory $name link is exact"
    fi
  done
  for directory in dev etc home opt opt/ascendany opt/ascendany/v2 opt/ascendany/v2/bin proc run run/ascendany-lsp-control sys usr var; do
    metadata="$(stat -c '%u:%g:%a' "$root/$directory" 2>/dev/null || true)"
    if [[ ! -d "$root/$directory" || -L "$root/$directory" || "$metadata" != '0:0:755' ]]; then
      fail "LSP RootDirectory mount skeleton directory is invalid: $directory"
    fi
  done
  if [[ "$(stat -c '%u:%g:%a' "$root/tmp" 2>/dev/null || true)" != '0:0:1777' ]]; then
    fail "LSP RootDirectory /tmp mountpoint is not root:root mode 1777"
  fi
  for placeholder in opt/ascendany/v2/bin/ascendany-lsp run/ascendany-lsp-control/control.sock; do
    if [[ ! -f "$root/$placeholder" || -L "$root/$placeholder" ||
          "$(stat -c '%u:%g:%a:%s:%h' "$root/$placeholder" 2>/dev/null || true)" != '0:0:0:0:1' ]]; then
      fail "LSP RootDirectory bind mount placeholder is invalid: $placeholder"
    fi
  done
  for empty in dev etc home proc sys usr var; do
    if [[ -n "$(find "$root/$empty" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
      fail "LSP RootDirectory host skeleton unexpectedly exposes content under /$empty"
    fi
  done

  polkit_rule="/etc/polkit-1/rules.d/61-ascendany-lsp.rules"
  if [[ ! -f "$polkit_rule" || "$(stat -c '%u:%g:%a' "$polkit_rule" 2>/dev/null || true)" != "0:0:644" ]]; then
    fail "LSP polkit rule is missing or not root:root 0644"
  else
    pass "LSP systemd authorization rule is root-owned"
  fi
}

check_lsp_control_socket() {
  local control_socket="${1:-/run/ascendany-lsp-control/control.sock}"
  local metadata
  if [[ "$ascendanyd_active" == "1" && "$expected_write_mode" == "enabled" ]]; then
    metadata="$(stat -Lc '%U:%G:%a' "$control_socket" 2>/dev/null || true)"
    if [[ ! -S "$control_socket" || -L "$control_socket" ||
          "$metadata" != 'ascendany:ascendany-lsp-control:660' ]]; then
      fail "active write runtime LSP control socket lacks the exact non-root server identity boundary"
    else
      pass "active write runtime LSP control socket binds its non-root inode owner to peer authentication"
    fi
  elif [[ "$ascendanyd_active" == "1" &&
          ( -e "$control_socket" || -L "$control_socket" ) ]]; then
    fail "read-only runtime exposes an LSP control socket"
  elif [[ "$ascendanyd_active" == "1" ]]; then
    pass "read-only runtime exposes no LSP control socket"
  fi
}

check_credentials() {
  local unit="ascendanyd.service" credential_id
  local -a expected_ids=(
    db_password
    jwt_signing_private_key
    password_pepper
  )
  for credential_id in "${runtime_provider_credential_ids[@]}"; do
    expected_ids+=("$credential_id")
  done
  check_unit_credentials "$unit" "${expected_ids[@]}"
  if smoke_dropin_required; then
    check_unit_environment_files "$unit" \
      /etc/ascendany/v2/ascendanyd.env \
      /etc/ascendany/v2/ascendanyd-read-only-smoke.env
  else
    check_unit_environment_files "$unit" /etc/ascendany/v2/ascendanyd.env
  fi
  check_credential_source \
    "$unit" jwt_signing_private_key \
    /etc/ascendany/credentials/jwt_signing_private_key.cred || true

  if [[ "$ascendanyd_active" == "1" ]]; then
    local active_credential="/run/credentials/${unit}/jwt_signing_private_key"
    if [[ ! -s "$active_credential" ]]; then
      fail "active JWT private-key credential is missing: $active_credential"
    elif ! /usr/bin/openssl pkey -in "$active_credential" -noout -check >/dev/null 2>&1; then
      fail "active JWT private-key credential is not a valid Ed25519 private key"
    elif [[ "$(/usr/bin/openssl pkey -in "$active_credential" -text -noout 2>/dev/null | /usr/bin/sed -n '1p')" != 'ED25519 Private-Key:' ]]; then
      fail "active JWT private-key credential uses a non-Ed25519 key"
    else
      pass "active JWT private-key credential is Ed25519"
    fi
  fi

  if [[ "$ascendanyd_active" == "1" ]]; then
    local active_pepper="/run/credentials/${unit}/password_pepper"
    if [[ ! -s "$active_pepper" ]]; then
      fail "active password pepper credential is missing: $active_pepper"
    elif (( $(stat -c '%s' "$active_pepper") < 32 )); then
      fail "active password pepper credential is shorter than 32 bytes"
    else
      pass "active password pepper credential exists and is at least 32 bytes"
    fi
  fi
}

check_jwt_keypair_credentials() {
  local private_source=/etc/ascendany/credentials/jwt_signing_private_key.cred
  local public_source=/etc/ascendany/credentials/jwt_verification_public_key.cred
  local private_raw_sha private_canonical_sha public_raw_sha public_canonical_sha
  local private_type public_type derived_public configured_public

  if ! private_raw_sha="$({
      /usr/bin/systemd-creds --name=jwt_signing_private_key decrypt "$private_source" - 2>/dev/null
    } | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')" ||
     ! private_canonical_sha="$({
      /usr/bin/systemd-creds --name=jwt_signing_private_key decrypt "$private_source" - 2>/dev/null
    } | /usr/bin/openssl pkey -outform PEM 2>/dev/null | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')" ||
     [[ ! "$private_raw_sha" =~ ^[0-9a-f]{64}$ || "$private_raw_sha" != "$private_canonical_sha" ]]; then
    fail "JWT signing credential is not one canonical PKCS#8 Ed25519 private-key PEM"
    return
  fi
  if ! private_type="$({
      /usr/bin/systemd-creds --name=jwt_signing_private_key decrypt "$private_source" - 2>/dev/null
    } | /usr/bin/openssl pkey -text -noout 2>/dev/null | /usr/bin/sed -n '1p')" ||
     [[ "$private_type" != 'ED25519 Private-Key:' ]]; then
    fail "JWT signing credential contains a non-Ed25519 private key"
    return
  fi
  if ! derived_public="$({
      /usr/bin/systemd-creds --name=jwt_signing_private_key decrypt "$private_source" - 2>/dev/null
    } | /usr/bin/openssl pkey -pubout 2>/dev/null)" ||
     [[ "$derived_public" != '-----BEGIN PUBLIC KEY-----'* ]]; then
    fail "JWT signing credential cannot derive an Ed25519 public key"
    return
  fi
  if ! public_raw_sha="$({
      /usr/bin/systemd-creds --name=jwt_verification_public_key decrypt "$public_source" - 2>/dev/null
    } | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')" ||
     ! public_canonical_sha="$({
      /usr/bin/systemd-creds --name=jwt_verification_public_key decrypt "$public_source" - 2>/dev/null
    } | /usr/bin/openssl pkey -pubin -pubout -outform PEM 2>/dev/null | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')" ||
     [[ ! "$public_raw_sha" =~ ^[0-9a-f]{64}$ || "$public_raw_sha" != "$public_canonical_sha" ]]; then
    fail "JWT verification credential is not one canonical PKIX Ed25519 public-key PEM"
    return
  fi
  if ! public_type="$({
      /usr/bin/systemd-creds --name=jwt_verification_public_key decrypt "$public_source" - 2>/dev/null
    } | /usr/bin/openssl pkey -pubin -text -noout 2>/dev/null | /usr/bin/sed -n '1p')" ||
     [[ "$public_type" != 'ED25519 Public-Key:' ]]; then
    fail "JWT verification credential contains a non-Ed25519 public key"
    return
  fi
  if ! configured_public="$({
      /usr/bin/systemd-creds --name=jwt_verification_public_key decrypt "$public_source" - 2>/dev/null
    } | /usr/bin/openssl pkey -pubin -pubout 2>/dev/null)" ||
     [[ "$configured_public" != "$derived_public" ]]; then
    fail "JWT private signing credential and public verification credential do not form one Ed25519 keypair"
    return
  fi
  pass "JWT Ed25519 signing and verification credentials are canonical and capability-separated"
}

check_admin_bootstrap_unit() {
  local unit="ascendany-admin-bootstrap.service"
  local one_time_source="/run/ascendany-admin-bootstrap-input/admin_password.cred"
  local input_directory="/run/ascendany-admin-bootstrap-input"
  local rendered actual expected active_state enabled_state
  local -a plaintext=() encrypted=()

  if ! rendered="$(systemctl cat "$unit" 2>/dev/null)" || [[ -z "$rendered" ]]; then
    fail "$unit configuration cannot be read"
    return
  fi
  collect_effective_directives "$rendered" LoadCredential plaintext
  collect_effective_directives "$rendered" LoadCredentialEncrypted encrypted
  if (( ${#plaintext[@]} != 0 )); then
    fail "$unit has an effective plaintext LoadCredential directive"
  fi
  actual="$(printf '%s\n' "${encrypted[@]}" | normalize_word_set)"
  expected="$(printf '%s\n' \
    'admin_password:/run/ascendany-admin-bootstrap-input/admin_password.cred' \
    'db_password:/etc/ascendany/credentials/runtime_db_password.cred' \
    'password_pepper:/etc/ascendany/credentials/password_pepper.cred' |
    normalize_word_set)"
  if [[ "$actual" != "$expected" ]]; then
    fail "$unit encrypted credential declarations differ from the exact bootstrap contract"
  else
    pass "$unit encrypted credential declarations are exact"
  fi
  check_credential_source "$unit" db_password /etc/ascendany/credentials/runtime_db_password.cred || true
  check_credential_source "$unit" password_pepper /etc/ascendany/credentials/password_pepper.cred || true

  if [[ ! -d "$input_directory" || -L "$input_directory" ||
        "$(stat -Lc '%u:%g:%a' "$input_directory" 2>/dev/null || true)" != "0:0:700" ]] ||
     ! check_root_owned_ancestry "$input_directory" 1; then
    fail "administrator bootstrap input directory must be root:root mode 0700"
  elif [[ -e "$one_time_source" || -L "$one_time_source" ]]; then
    fail "one-time administrator password credential remains after the bootstrap window"
  else
    pass "one-time administrator password credential is absent"
  fi

  active_state="$(unit_property "$unit" ActiveState || true)"
  enabled_state="$(systemctl is-enabled "$unit" 2>/dev/null || true)"
  if [[ "$active_state" != "inactive" || "$enabled_state" != "static" ]]; then
    fail "$unit must remain inactive and static outside its one-shot bootstrap window"
  else
    pass "$unit is inactive; durable administrator state is verified from the database"
  fi
}

check_catalog_publisher_config_contract() {
  local path="${1:-$catalog_publisher_config_root/catalog-publish.env}"
  local expected actual
  expected="$(printf '%s\n' \
    'ASCENDANY_DATABASE_URL=postgresql://ascendany_catalog_publisher_login@127.0.0.1:6432/ascendany_v2' \
    'ASCENDANY_DATABASE_POOL_MODE=transaction' \
    'ASCENDANY_DATABASE_SCHEMA_VERSION=10' \
    'ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s' \
    'ASCENDANY_DATABASE_HEALTH_TIMEOUT=3s' \
    'ASCENDANY_AUTH_ISSUER=ascendany' \
    'ASCENDANY_AUTH_AUDIENCE=ascendany-v2' \
    'ASCENDANY_RECOMMENDATION_MODEL_PATH=/opt/ascendany/v2/models/recommendation-model.json' \
    "ASCENDANY_RECOMMENDATION_MODEL_SHA256=$release_model_sha256" \
    'ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=production' \
    'ASCENDANY_KNOWLEDGE_CATALOG_PATH=/opt/ascendany/v2/models/recommendation-knowledge-catalog.json' \
    "ASCENDANY_KNOWLEDGE_CATALOG_SHA256=$release_catalog_sha256" \
    'ASCENDANY_LOG_LEVEL=info')"
  actual="$(sed '/^#/d; /^$/d' "$path" 2>/dev/null || true)"
  if [[ ! "$release_model_sha256" =~ ^[0-9a-f]{64}$ ||
        ! "$release_catalog_sha256" =~ ^[0-9a-f]{64}$ ||
        "$actual" != "$expected" ]]; then
    fail "catalog publisher environment differs from the exact release-bound stopped-runtime contract"
  else
    pass "catalog publisher environment exactly binds its DB role, model, and catalog contract"
  fi
}

check_catalog_publisher_unit() {
  local unit="ascendany-catalog-publish.service"
  local request_source="$catalog_publication_request_source"
  local access_token_source="$catalog_publication_access_token_source"
  local rendered actual expected active_state enabled_state
  local -a plaintext=() encrypted=()

  if ! rendered="$(systemctl cat "$unit" 2>/dev/null)" || [[ -z "$rendered" ]]; then
    fail "$unit configuration cannot be read"
    return
  fi
  collect_effective_directives "$rendered" LoadCredential plaintext
  collect_effective_directives "$rendered" LoadCredentialEncrypted encrypted
  if (( ${#plaintext[@]} != 0 )); then
    fail "$unit has an effective plaintext LoadCredential directive"
  fi
  actual="$(printf '%s\n' "${encrypted[@]}" | normalize_word_set)"
  expected="$(printf '%s\n' \
    'admin_access_token:/var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred' \
    'catalog_publication_request:/var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred' \
    'catalog_publisher_db_password:/etc/ascendany-catalog-publisher/credentials/catalog_publisher_db_password.cred' \
    'jwt_verification_public_key:/etc/ascendany/credentials/jwt_verification_public_key.cred' |
    normalize_word_set)"
  if [[ "$actual" != "$expected" ]]; then
    fail "$unit encrypted credential declarations differ from the exact publisher contract"
  else
    pass "$unit encrypted credential declarations are exact"
  fi
  check_credential_source \
    "$unit" catalog_publisher_db_password \
    /etc/ascendany-catalog-publisher/credentials/catalog_publisher_db_password.cred || true
  check_credential_source \
    "$unit" jwt_verification_public_key \
    /etc/ascendany/credentials/jwt_verification_public_key.cred || true

  if [[ -e "$request_source" || -L "$request_source" ||
        -e "$access_token_source" || -L "$access_token_source" ]]; then
    fail "catalog publication request or access token remains outside the operator window"
  else
    pass "catalog publication request and access token are absent outside the operator window"
  fi

  active_state="$(unit_property "$unit" ActiveState || true)"
  enabled_state="$(systemctl is-enabled "$unit" 2>/dev/null || true)"
  if [[ "$active_state" != inactive || "$enabled_state" != static ]]; then
    fail "$unit must remain inactive and static outside its explicit one-shot window"
  else
    pass "$unit is inactive; durable catalog publication state is verified from its receipt and database transaction"
  fi
}

check_active_ascendanyd_environment() {
  local pid="$1" environ_path="$2" environment_file="$3"
  local line name value entry generated_invalid
  local invalid=0
  local -A expected=() seen=() required_generated=()

  if [[ ! -r "$environment_file" ]]; then
    fail "active ascendanyd environment contract cannot read $environment_file"
    return
  fi
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    if [[ ! "$line" =~ ^([A-Z][A-Z0-9_]*)=(.*)$ ]]; then
      fail "ascendanyd.env contains a noncanonical environment entry"
      invalid=1
      continue
    fi
    name="${BASH_REMATCH[1]}"
    value="${BASH_REMATCH[2]}"
    case "$name" in
      LANG|PATH|USER|LOGNAME|HOME|SHELL|INVOCATION_ID|JOURNAL_STREAM|SYSTEMD_EXEC_PID|MEMORY_PRESSURE_WATCH|MEMORY_PRESSURE_WRITE|CREDENTIALS_DIRECTORY|RUNTIME_DIRECTORY|STATE_DIRECTORY|LOGS_DIRECTORY|ASCENDANY_DATABASE_PASSWORD_FILE|ASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE|ASCENDANY_PASSWORD_PEPPER_FILE|ASCENDANY_CREDENTIAL_FILE_REF_HEX_*)
        fail "ascendanyd.env attempts to own reserved environment name $name"
        invalid=1
        continue
        ;;
    esac
    if [[ -n "${expected[$name]+present}" ]]; then
      fail "ascendanyd.env repeats environment name $name"
      invalid=1
      continue
    fi
    expected["$name"]="$value"
  done <"$environment_file"

  expected[ASCENDANY_WRITE_MODE]="$expected_write_mode"

  expected[LANG]='zh_CN.UTF-8'
  expected[PATH]='/usr/local/bin:/usr/bin'
  expected[USER]='ascendany'
  expected[LOGNAME]='ascendany'
  expected[HOME]='/var/lib/ascendany'
  expected[SHELL]='/usr/sbin/nologin'
  expected[ASCENDANY_DATABASE_PASSWORD_FILE]='/run/credentials/ascendanyd.service/db_password'
  expected[ASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE]='/run/credentials/ascendanyd.service/jwt_signing_private_key'
  expected[ASCENDANY_PASSWORD_PEPPER_FILE]='/run/credentials/ascendanyd.service/password_pepper'
  for entry in "${runtime_provider_bindings[@]}"; do
    name="${entry%%=*}"
    value="${entry#*=}"
    expected["$name"]="/run/credentials/ascendanyd.service/$value"
  done

  required_generated[INVOCATION_ID]=1
  required_generated[JOURNAL_STREAM]=1
  required_generated[SYSTEMD_EXEC_PID]=1
  required_generated[MEMORY_PRESSURE_WATCH]=1
  required_generated[MEMORY_PRESSURE_WRITE]=1
  required_generated[CREDENTIALS_DIRECTORY]=1
  required_generated[RUNTIME_DIRECTORY]=1
  required_generated[STATE_DIRECTORY]=1
  required_generated[LOGS_DIRECTORY]=1

  if [[ ! -r "$environ_path" ]]; then
    fail "active ascendanyd process environment cannot be read"
    return
  fi
  while IFS= read -r -d '' entry; do
    if [[ ! "$entry" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]]; then
      fail "active ascendanyd process has a malformed environment entry"
      invalid=1
      continue
    fi
    name="${BASH_REMATCH[1]}"
    value="${BASH_REMATCH[2]}"
    if [[ -n "${seen[$name]+present}" ]]; then
      fail "active ascendanyd process repeats environment name $name"
      invalid=1
      continue
    fi
    seen["$name"]=1
    if [[ -n "${expected[$name]+present}" ]]; then
      if [[ "$value" != "${expected[$name]}" ]]; then
        fail "active ascendanyd process environment value drifted for $name"
        invalid=1
      fi
      continue
    fi
    generated_invalid=0
    case "$name" in
      INVOCATION_ID)
        [[ "$value" =~ ^[0-9a-f]{32}$ ]] || generated_invalid=1
        ;;
      JOURNAL_STREAM)
        [[ "$value" =~ ^[0-9]+:[0-9]+$ ]] || generated_invalid=1
        ;;
      SYSTEMD_EXEC_PID)
        [[ "$value" == "$pid" ]] || generated_invalid=1
        ;;
      MEMORY_PRESSURE_WATCH)
        [[ "$value" == "/sys/fs/cgroup/system.slice/ascendanyd.service/memory.pressure" ]] || generated_invalid=1
        ;;
      MEMORY_PRESSURE_WRITE)
        [[ "$value" == 'c29tZSAyMDAwMDAgMjAwMDAwMAA=' ]] || generated_invalid=1
        ;;
      CREDENTIALS_DIRECTORY)
        [[ "$value" == "/run/credentials/ascendanyd.service" ]] || generated_invalid=1
        ;;
      RUNTIME_DIRECTORY)
        [[ "$value" == "/run/ascendany" ]] || generated_invalid=1
        ;;
      STATE_DIRECTORY)
        [[ "$value" == "/var/lib/ascendany" ]] || generated_invalid=1
        ;;
      LOGS_DIRECTORY)
        [[ "$value" == "/var/log/ascendany" ]] || generated_invalid=1
        ;;
      *)
        fail "active ascendanyd process has an undeclared environment name: $name"
        invalid=1
        continue
        ;;
    esac
    if (( generated_invalid != 0 )); then
      fail "active ascendanyd process has an invalid systemd-generated value for $name"
      invalid=1
    fi
  done <"$environ_path"

  for name in "${!expected[@]}" "${!required_generated[@]}"; do
    if [[ -z "${seen[$name]+present}" ]]; then
      fail "active ascendanyd process is missing required environment name $name"
      invalid=1
    fi
  done
  if (( invalid == 0 )); then
    pass "active ascendanyd process environment is the exact reviewed and systemd-generated closed set"
  fi
}

check_active_ascendanyd_process() {
  local pid executable
  local -a argv=()
  [[ "$ascendanyd_active" == "1" ]] || return 0
  pid="$(unit_property ascendanyd.service MainPID || true)"
  if [[ ! "$pid" =~ ^[1-9][0-9]*$ || ! -r "/proc/$pid/cmdline" ]]; then
    fail "ascendanyd.service has no readable positive MainPID"
    return
  fi
  executable="$(realpath -e -- "/proc/$pid/exe" 2>/dev/null || true)"
  mapfile -d '' -t argv <"/proc/$pid/cmdline" || true
  if [[ "$executable" != "$release_root/bin/ascendanyd" ]]; then
    fail "active ascendanyd executable does not match the staged release binary"
  elif (( ${#argv[@]} != 2 )) ||
       [[ "${argv[0]}" != "$release_root/bin/ascendanyd" || "${argv[1]}" != "serve" ]]; then
    fail "active ascendanyd argv differs from the exact release binary/serve contract"
  else
    pass "active ascendanyd executable and argv match the staged release"
  fi
  check_active_ascendanyd_environment \
    "$pid" "/proc/$pid/environ" /etc/ascendany/v2/ascendanyd.env
}

check_active_ascendanyd_health() {
  local liveness readiness
  [[ "$ascendanyd_active" == "1" ]] || return 0
  if ! liveness="$(curl --disable --fail --silent --show-error --max-time 5 --noproxy '*' --proto '=http' http://127.0.0.1:18000/livez)"; then
    fail "active ascendanyd liveness cannot be read"
  elif ! jq -e '
      type == "object" and keys == ["status"] and .status == "alive"
    ' <<<"$liveness" >/dev/null 2>&1; then
    fail "active ascendanyd liveness violates the closed response contract"
  else
    pass "active ascendanyd liveness is healthy"
  fi

  if ! readiness="$(curl --disable --fail --silent --show-error --max-time 5 --noproxy '*' --proto '=http' http://127.0.0.1:18000/readyz)"; then
    fail "active ascendanyd readiness cannot be read"
  elif ! jq -e '
      type == "object" and
      keys == ["checks", "status"] and
      .status == "ready" and
      (.checks | type == "object" and keys == ["database", "migrations"]) and
      (.checks.database | type == "object" and keys == ["status"] and .status == "pass") and
      (.checks.migrations | type == "object" and
        keys == ["currentVersion", "expectedVersion", "status"] and
        .status == "pass" and .currentVersion == 10 and .expectedVersion == 10)
    ' <<<"$readiness" >/dev/null 2>&1; then
    fail "active ascendanyd readiness violates the schema-v10 closed response contract"
  else
    pass "active ascendanyd database and migration readiness are healthy at schema v10"
  fi
}

check_release_for_secret_files() {
  local -a found=()
  if [[ ! -d "$release_root" ]]; then
    fail "release root is missing: $release_root"
    return
  fi
  mapfile -t found < <(
    find "$release_root" -xdev -type f \
      \( -name '.env' -o -name '.env.*' -o -name '*.key' -o -name '*.pem' \
         -o -name '*.cred' -o -name '.pgpass' -o -iname '*password*' \
         -o -iname '*secret*' -o -iname '*token*' \) -print
  )
  if (( ${#found[@]} > 0 )); then
    printf 'Secret-like files under release root:\n' >&2
    printf '  %s\n' "${found[@]}" >&2
    fail "release root contains secret-like files"
  else
    pass "release root contains no secret-like files"
  fi
}

check_release_directory_metadata() {
  local path="$1" description="$2"
  local metadata

  metadata="$(stat -Lc '%u:%g:%a' -- "$path" 2>/dev/null || true)"
  if [[ "$path" != /* || "$path" != "$(realpath -m -- "$path")" ||
        ! -d "$path" || -L "$path" ||
        "$path" != "$(realpath -e -- "$path" 2>/dev/null || true)" ||
        "$metadata" != "0:0:755" ]]; then
    fail "$description must be a canonical non-symbolic-link root:root mode 0755 directory"
    return 1
  fi
  pass "$description is a canonical root:root mode 0755 directory"
}

check_release_payload() {
  local manifest="$release_root/release-manifest.json"
  local payload_failures_before="$failures"
  local relative path expected_sha expected_size expected_mode
  local actual_sha actual_size actual_mode owner_group runtime_metadata runtime_capabilities catalog_metadata
  local expected_writes_json
  local expected_build_time manifest_go_version manifest_goos manifest_goarch
  local manifest_goamd64 manifest_go_experiment manifest_go_fips manifest_cgo_enabled
  local -a required_paths=(
    bin/ascendanyd
    bin/ascendany-admin-bootstrap
    bin/ascendany-backup
    bin/ascendany-catalog-publish
    bin/ascendany-judge
    bin/ascendany-lsp
    bin/ascendany-migrate
    bin/ascendany-model
    bin/ascendany-release-ops
    models/recommendation-model.json
    models/recommendation-knowledge-catalog.json
    operators/ascendany-production-initialize.mjs
    README.md
    OJ_JUDGE_CONTRACT.md
    LSP_CONTROL_CONTRACT.md
    contracts/openapi/ascendany-v2.yaml
    contracts/pintia/ascendany.pintia.snapshot.v2.schema.json
    db/roles/README.md
    db/roles/001_v2_roles.sql
    db/roles/verify_v2_roles.sql
    config/analytics.json
    config/ascendanyd.env
    config/ascendanyd-read-only-smoke.env
    config/backup.env
    config/catalog-publish.env
    config/cloudflared.yaml
    config/fedora-runtime-packages.json
    config/judge.env
    config/judge-compiler-rootfs.inventory
    config/judge-image-lock.json
    config/judge-images.Containerfile
    config/migrate.env
    config/pgbouncer-hba.conf
    config/pgbouncer.ini
    config/postgresql-hba.conf
    config/postgresql-ident.conf
    config/restore.env
    systemd/ascendanyd.service
    systemd/ascendany-model-register.service
    systemd/ascendany-model-activate.service
    systemd/ascendany-catalog-publish.service
    systemd/ascendanyd.service.d/40-read-only-smoke.conf
    systemd/ascendany-admin-bootstrap.service
    systemd/ascendany-backup.service
    systemd/ascendany-backup.timer
    systemd/ascendany-judge@.service
    systemd/ascendany-lsp@.service
    systemd/ascendany-migrate.service
    systemd/ascendany-pgbouncer.service
    systemd/ascendany-restore-verify@.service
    systemd/ascendany-cloudflared.service
    polkit-1/rules.d/60-ascendany-judge.rules
    polkit-1/rules.d/61-ascendany-lsp.rules
    sysusers.d/ascendany-v2.conf
    tmpfiles.d/ascendany-v2.conf
    scripts/publish-restore-evidence.sh
    scripts/restore-verify-operator.sh
    scripts/install-v2-release.sh
    scripts/acquire-pgbouncer-rpm.sh
    scripts/acquire-judge-image.sh
    scripts/attest-pgbouncer-rpm.sh
    scripts/attest-judge-image.sh
    scripts/judge-image-contract.sh
    scripts/preload-judge-image.sh
    scripts/provision-postgres-pgbouncer.sh
    scripts/postgres-schema-fingerprint.sh
    scripts/validate-cloudflared.sh
    scripts/validate-production.sh
  )
  local -a required_directories=(
    bin
    models
    operators
    config
    contracts
    contracts/openapi
    contracts/pintia
    db
    db/roles
    polkit-1
    polkit-1/rules.d
    scripts
    systemd
    systemd/ascendanyd.service.d
    sysusers.d
    tmpfiles.d
  )
  local -a actual_files=()
  local -a declared_files=()
  local -a actual_directories=()
  local -a expected_directories=()
  declare -A declared=()

  if ! check_release_directory_metadata "$release_root" "release root"; then
    return
  fi
  if find "$release_root" -xdev -type l -print -quit | grep -q .; then
    fail "release payload contains a symbolic link"
  fi
  if find "$release_root" -xdev ! -type d ! -type f -print -quit | grep -q .; then
    fail "release payload contains a special filesystem node"
  fi
  if find "$release_root" -xdev \( ! -user root -o ! -group root -o -perm /022 \) -print -quit | grep -q .; then
    fail "release payload contains an entry that is not root:root or is writable by group/other"
  else
    pass "release payload is root-owned and immutable to service identities"
  fi

  if [[ ! -f "$manifest" || -L "$manifest" ]]; then
    fail "release manifest is missing or is a symbolic link: $manifest"
    return
  fi
  if ! jq -e '
      type == "object" and
      (keys == ["build", "commit", "files", "purpose", "schema", "sourceDateEpoch", "version"]) and
      .schema == "ascendany.release.v2" and
      .purpose == "production" and
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
      (.files | type == "array" and length == 68) and
      (all(.files[];
        type == "object" and
        (keys == ["mode", "path", "sha256", "size"]) and
        (.path | type == "string" and test("^[0-9A-Za-z][0-9A-Za-z._@/-]*$") and
          (startswith("/") | not) and
          (contains("//") | not) and
          (contains("/../") | not) and
          (endswith("/..") | not) and
          (contains("/./") | not) and
          (endswith("/.") | not)) and
        (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
        (.size | type == "number" and floor == . and . > 0) and
        (.mode | type == "string" and test("^0[0-7]{3}$")))) and
      (([.files[].path] | length) == ([.files[].path] | unique | length))
    ' "$manifest" >/dev/null; then
    fail "release manifest violates ascendany.release.v2"
    return
  fi

  release_manifest_commit="$(jq -r '.commit' "$manifest")"
  release_manifest_version="$(jq -r '.version' "$manifest")"
  release_manifest_purpose="$(jq -r '.purpose' "$manifest")"
  manifest_go_version="$(jq -r '.build.goVersion' "$manifest")"
  manifest_goos="$(jq -r '.build.goos' "$manifest")"
  manifest_goarch="$(jq -r '.build.goarch' "$manifest")"
  manifest_goamd64="$(jq -r '.build.goamd64' "$manifest")"
  manifest_go_experiment="$(jq -r '.build.goExperiment' "$manifest")"
  manifest_go_fips="$(jq -r '.build.gofips140' "$manifest")"
  manifest_cgo_enabled="$(jq -r '.build.cgoEnabled' "$manifest")"
  expected_build_time="$(date -u -d "@$(jq -r '.sourceDateEpoch' "$manifest")" +%FT%TZ 2>/dev/null || true)"
  release_manifest_build_time="$expected_build_time"
  if [[ -z "$expected_build_time" ]]; then
    fail "release sourceDateEpoch cannot be rendered as UTC build time"
  fi
  while IFS=$'\t' read -r relative expected_sha expected_size expected_mode; do
    if [[ -n "${declared[$relative]:-}" ]]; then
      fail "release manifest repeats path: $relative"
      continue
    fi
    declared["$relative"]=1
    declared_files+=("$relative")
    path="$release_root/$relative"
    if ! is_under "$path" "$release_root" || [[ ! -f "$path" || -L "$path" ]]; then
      fail "release manifest path is missing, outside the root, or a symbolic link: $relative"
      continue
    fi
    owner_group="$(stat -Lc '%u:%g' "$path" 2>/dev/null || true)"
    actual_mode="$(stat -Lc '%a' "$path" 2>/dev/null || true)"
    actual_size="$(stat -Lc '%s' "$path" 2>/dev/null || true)"
    actual_sha="$(sha256sum -- "$path" 2>/dev/null | awk '{print $1}')"
    if [[ "$owner_group" != "0:0" ]]; then
      fail "release file is not root:root: $relative"
    elif [[ "$actual_mode" != "${expected_mode#0}" ]]; then
      fail "release file mode drifted for $relative: $actual_mode != ${expected_mode#0}"
    elif [[ "$actual_size" != "$expected_size" ]]; then
      fail "release file size drifted for $relative"
    elif [[ "$actual_sha" != "$expected_sha" ]]; then
      fail "release file digest drifted for $relative"
    fi
  done < <(jq -r '.files[] | [.path, .sha256, (.size | tostring), .mode] | @tsv' "$manifest")

  for relative in "${required_paths[@]}"; do
    if [[ -z "${declared[$relative]:-}" ]]; then
      fail "release manifest omits required payload: $relative"
    elif [[ "$relative" == bin/* && ! -x "$release_root/$relative" ]]; then
      fail "release binary is not executable: $relative"
    elif [[ "$relative" == operators/ascendany-production-initialize.mjs &&
            "$(jq -r --arg path "$relative" '.files[] | select(.path == $path) | .mode' "$manifest")" != 0555 ]]; then
      fail "production initialization operator must be immutable mode 0555"
    elif [[ "$relative" == "models/recommendation-model.json" ]]; then
      release_model_sha256="$(jq -r --arg path "$relative" '.files[] | select(.path == $path) | .sha256' "$manifest")"
    elif [[ "$relative" == "models/recommendation-knowledge-catalog.json" ]]; then
      release_catalog_sha256="$(jq -r --arg path "$relative" '.files[] | select(.path == $path) | .sha256' "$manifest")"
    fi
  done

  if [[ ! "$release_catalog_sha256" =~ ^[0-9a-f]{64}$ ]] ||
     ! catalog_metadata="$(/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
       "$release_root/bin/ascendany-model" verify-catalog \
       --catalog "$release_root/models/recommendation-knowledge-catalog.json" \
       --catalog-sha256 "$release_catalog_sha256" \
       --model "$release_root/models/recommendation-model.json" \
       --model-sha256 "$release_model_sha256" \
       --expected-purpose production)"; then
    fail "release knowledge catalog failed canonical model-bound verification"
  elif ! jq -e \
      --arg catalogSHA "$release_catalog_sha256" \
      --arg modelSHA "$release_model_sha256" \
      --arg modelID "$(jq -r '.manifest.modelId' "$release_root/models/recommendation-model.json")" '
        type == "object" and
        (keys == [
          "artifactMode", "artifactSizeBytes", "catalogSha256", "knowledgePointIds",
          "modelArtifactSha256", "modelId", "problemAssignmentCount", "schema", "taxonomyId"
        ]) and
        .schema == "ascendany.knowledge_catalog.recommendation.v1" and
        .catalogSha256 == $catalogSHA and .modelArtifactSha256 == $modelSHA and .modelId == $modelID and
        .artifactMode == 420 and (.artifactSizeBytes | type == "number" and . > 0 and . <= 262144) and
        (.taxonomyId | type == "string" and length > 0) and
        (.knowledgePointIds | type == "array" and length > 0) and
        (.problemAssignmentCount | type == "number" and floor == . and . >= 0)
      ' <<<"$catalog_metadata" >/dev/null; then
    fail "release knowledge catalog verifier returned noncanonical provenance"
  else
    pass "release knowledge catalog is canonical and binds the immutable inference model"
  fi

  mapfile -t expected_directories < <(printf '%s\n' "${required_directories[@]}" | LC_ALL=C sort)
  mapfile -t actual_directories < <(find "$release_root" -mindepth 1 -type d -printf '%P\n' | LC_ALL=C sort)
  if [[ "$(printf '%s\n' "${expected_directories[@]}")" != "$(printf '%s\n' "${actual_directories[@]}")" ]]; then
    fail "release root directory set differs from the exact deployment contract"
  else
    pass "release root directory set is exact"
  fi
  for relative in "${required_directories[@]}"; do
    check_release_directory_metadata "$release_root/$relative" "release directory $relative" || true
  done

  declared_files+=("release-manifest.json")
  mapfile -t declared_files < <(printf '%s\n' "${declared_files[@]}" | LC_ALL=C sort)
  mapfile -t actual_files < <(find "$release_root" -xdev -type f -printf '%P\n' | LC_ALL=C sort)
  if [[ "$(printf '%s\n' "${declared_files[@]}")" != "$(printf '%s\n' "${actual_files[@]}")" ]]; then
    fail "release root contains an unmanifested file or omits a declared file"
  else
    pass "release manifest closes the complete staged file set"
  fi

  if [[ "$ascendanyd_active" == "1" ]]; then
    if ! runtime_metadata="$(curl --disable --fail --silent --show-error --max-time 5 --noproxy '*' --proto '=http' http://127.0.0.1:18000/version)"; then
      fail "active ascendanyd version metadata cannot be read"
    elif ! jq -e \
        --arg version "$release_manifest_version" \
        --arg commit "$release_manifest_commit" \
        --arg buildTime "$expected_build_time" \
        --arg goVersion "$manifest_go_version" \
        --arg goos "$manifest_goos" \
        --arg goarch "$manifest_goarch" \
        --arg goamd64 "$manifest_goamd64" \
        --arg goExperiment "$manifest_go_experiment" \
        --arg gofips140 "$manifest_go_fips" \
        --argjson cgoEnabled "$manifest_cgo_enabled" '
          type == "object" and
          (keys == ["buildTime", "cgoEnabled", "commit", "goExperiment", "goVersion", "goamd64", "goarch", "gofips140", "goos", "version"]) and
          .version == $version and .commit == $commit and .buildTime == $buildTime and
          .goVersion == $goVersion and .goos == $goos and .goarch == $goarch and
          .goamd64 == $goamd64 and .goExperiment == $goExperiment and
          .gofips140 == $gofips140 and .cgoEnabled == $cgoEnabled
        ' <<<"$runtime_metadata" >/dev/null 2>&1; then
      fail "active ascendanyd was not built from the staged release manifest"
    else
      pass "active ascendanyd matches release source, build time, toolchain, and linux/amd64 target"
    fi
    expected_writes_json=false
    [[ "$expected_write_mode" != "enabled" ]] || expected_writes_json=true
    if ! runtime_capabilities="$(curl --disable --fail --silent --show-error --max-time 5 --noproxy '*' --proto '=http' --header 'CF-Connecting-IP: 127.0.0.1' http://127.0.0.1:18000/api/v2/capabilities)"; then
      fail "active ascendanyd capability metadata cannot be read"
    elif ! jq -e --argjson expected "$expected_writes_json" \
        'type == "object" and .writesEnabled == $expected' \
        <<<"$runtime_capabilities" >/dev/null 2>&1; then
      fail "active ascendanyd write capability differs from the validation phase"
    else
      pass "active ascendanyd write capability matches the $validation_phase phase"
    fi
  fi
  if (( failures == payload_failures_before )); then
    release_payload_verified="1"
  fi
}

check_initialization_operator_runtime() {
  [[ "$validation_phase" == staged ]] || return 0
  local bundle="$release_root/operators/ascendany-production-initialize.mjs"
  local resolved metadata installed_package
  resolved="$(realpath -e -- "$initialization_node_binary" 2>/dev/null || true)"
  metadata="$(stat -Lc '%u:%g:%a:%h' -- "$resolved" 2>/dev/null || true)"
  installed_package="$(rpm -qf --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' \
    "$resolved" 2>/dev/null || true)"
  if [[ "$resolved" != "$initialization_node_binary" || ! -f "$resolved" || -L "$resolved" ||
        "$metadata" != 0:0:755:1 ]] || ! check_root_owned_ancestry "$resolved" 1; then
    fail "production initialization Node must be the canonical root-owned /usr/bin/node-22 binary"
  elif [[ "$installed_package" != "$initialization_node_package" ||
          "$(sha256sum -- "$resolved" | awk '{print $1}')" != "$initialization_node_sha256" ||
          "$($resolved --version 2>/dev/null || true)" != "$initialization_node_version" ]]; then
    fail "production initialization Node package, digest, or version differs from the pinned operator contract"
  elif ! /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
      "$resolved" --check "$bundle" >/dev/null 2>&1; then
    fail "manifest-bound production initialization operator is not valid pinned-Node JavaScript"
  else
    pass "pinned Node v22.22.2 parses the manifest-bound production initialization operator"
  fi
}

check_installed_release_copy() {
  local relative="$1" installed="$2" preserve_mode="$3"
  local source="$release_root/$relative" source_mode installed_mode metadata owner group
  if [[ ! -f "$source" || -L "$source" || ! -f "$installed" || -L "$installed" ||
        "$installed" != "$(realpath -m -- "$installed")" ||
        "$installed" != "$(realpath -e -- "$installed" 2>/dev/null || true)" ]] ||
     ! check_root_owned_ancestry "$installed" 0; then
    fail "installed release input is missing, linked, or has unsafe ancestry: $installed"
    return
  fi
  metadata="$(stat -Lc '%u:%g:%a' "$installed" 2>/dev/null || true)"
  IFS=: read -r owner group installed_mode <<<"$metadata"
  source_mode="$(stat -Lc '%a' "$source" 2>/dev/null || true)"
  if [[ ! "$installed_mode" =~ ^[0-7]{3,4}$ || ! "$source_mode" =~ ^[0-7]{3,4}$ ]]; then
    fail "installed release input metadata cannot be read: $installed"
  elif [[ "$owner" != "0" ]] || (( 8#$installed_mode & 8#022 )); then
    fail "installed release input is writable by a service identity: $installed"
  elif [[ "$preserve_mode" == "1" && ( "$group" != "0" || "$installed_mode" != "$source_mode" ) ]]; then
    fail "installed immutable release input changed owner group or mode: $installed"
  elif ! cmp --silent -- "$source" "$installed"; then
    fail "installed release input bytes differ from the reviewed release: $installed"
  else
    pass "installed release input matches $relative"
  fi
}

check_installed_release_inputs() {
  local -a immutable_relatives=(
    systemd/ascendany-cloudflared.service
    systemd/ascendanyd.service
    systemd/ascendany-model-register.service
    systemd/ascendany-model-activate.service
    systemd/ascendany-catalog-publish.service
    systemd/ascendany-admin-bootstrap.service
    systemd/ascendany-backup.service
    systemd/ascendany-backup.timer
    systemd/ascendany-judge@.service
    systemd/ascendany-lsp@.service
    systemd/ascendany-migrate.service
    systemd/ascendany-pgbouncer.service
    systemd/ascendany-restore-verify@.service
    polkit-1/rules.d/60-ascendany-judge.rules
    polkit-1/rules.d/61-ascendany-lsp.rules
    sysusers.d/ascendany-v2.conf
    tmpfiles.d/ascendany-v2.conf
  )
  local -a immutable_targets=(
    /etc/systemd/system/ascendany-cloudflared.service
    /etc/systemd/system/ascendanyd.service
    /etc/systemd/system/ascendany-model-register.service
    /etc/systemd/system/ascendany-model-activate.service
    /etc/systemd/system/ascendany-catalog-publish.service
    /etc/systemd/system/ascendany-admin-bootstrap.service
    /etc/systemd/system/ascendany-backup.service
    /etc/systemd/system/ascendany-backup.timer
    /etc/systemd/system/ascendany-judge@.service
    /etc/systemd/system/ascendany-lsp@.service
    /etc/systemd/system/ascendany-migrate.service
    /etc/systemd/system/ascendany-pgbouncer.service
    /etc/systemd/system/ascendany-restore-verify@.service
    /etc/polkit-1/rules.d/60-ascendany-judge.rules
    /etc/polkit-1/rules.d/61-ascendany-lsp.rules
    /etc/sysusers.d/ascendany-v2.conf
    /etc/tmpfiles.d/ascendany-v2.conf
  )
  local -a config_relatives=(
    config/analytics.json
    config/ascendanyd.env
    config/ascendanyd-read-only-smoke.env
    config/backup.env
    config/catalog-publish.env
    config/judge.env
    config/migrate.env
    config/pgbouncer-hba.conf
    config/pgbouncer.ini
    config/restore.env
  )
  local -a config_targets=(
    /etc/ascendany/v2/analytics.json
    /etc/ascendany/v2/ascendanyd.env
    /etc/ascendany/v2/ascendanyd-read-only-smoke.env
    /etc/ascendany/v2/backup.env
    /etc/ascendany-catalog-publisher/catalog-publish.env
    /etc/ascendany/v2/judge.env
    /etc/ascendany/v2/migrate.env
    /opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf
    /opt/ascendany/infra/pgbouncer/pgbouncer.ini
    /etc/ascendany/v2/restore.env
  )
  local index
  if [[ "${#immutable_relatives[@]}" != "${#immutable_targets[@]}" ||
        "${#config_relatives[@]}" != "${#config_targets[@]}" ]]; then
    fail "installed release input mapping contract is internally inconsistent"
    return
  fi
  for index in "${!immutable_relatives[@]}"; do
    check_installed_release_copy "${immutable_relatives[$index]}" "${immutable_targets[$index]}" 1
  done
  for index in "${!config_relatives[@]}"; do
    check_installed_release_copy "${config_relatives[$index]}" "${config_targets[$index]}" 0
  done
  if smoke_dropin_required; then
    check_installed_release_copy \
      systemd/ascendanyd.service.d/40-read-only-smoke.conf \
      "$smoke_dropin" \
      1
  fi
}

run_pgbouncer_rejection_psql() {
  local user="$1" database="$2"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    PGHOST="$runtime_pg_host" \
    PGPORT="$runtime_pg_port" \
    PGDATABASE="$database" \
    PGUSER="$user" \
    PGCONNECT_TIMEOUT="$runtime_pg_connect_timeout" \
    /usr/bin/psql -X --no-password -c 'SELECT 1'
}

probe_pgbouncer_hba_rejection() {
  local user="$1" database="$2" output expected
  expected="psql: error: connection to server at \"127.0.0.1\", port 6432 failed: FATAL:  login rejected"
  if output="$(run_pgbouncer_rejection_psql "$user" "$database" 2>&1)"; then
    fail "PgBouncer accepted forbidden $user access to $database"
  elif [[ "$output" != "$expected" ]]; then
    fail "PgBouncer did not return the exact HBA rejection for $user on $database"
  else
    pass "PgBouncer HBA rejects $user on $database before password authentication"
  fi
}

check_pgbouncer_service_ownership() {
  local package_active package_enabled package_main_pid

  package_enabled="$(systemctl is-enabled "$pgbouncer_package_unit" 2>/dev/null || true)"
  package_active="$(systemctl is-active "$pgbouncer_package_unit" 2>/dev/null || true)"
  package_main_pid="$(unit_property "$pgbouncer_package_unit" MainPID 2>/dev/null || true)"
  if [[ "$package_enabled" != masked || "$package_active" != inactive || "$package_main_pid" != 0 ]]; then
    fail "package-owned PgBouncer unit must be masked, inactive, and process-free"
  else
    pass "release-owned PgBouncer has exclusive service ownership"
  fi
}

check_retired_runtime_boundary() {
  local retired_enabled retired_active retired_main_pid retired_listeners

  retired_enabled="$(systemctl is-enabled "$retired_api_unit" 2>/dev/null || true)"
  retired_active="$(systemctl is-active "$retired_api_unit" 2>/dev/null || true)"
  retired_main_pid="$(unit_property "$retired_api_unit" MainPID 2>/dev/null || true)"
  if [[ "$retired_enabled" != masked || "$retired_active" != inactive || "$retired_main_pid" != 0 ]]; then
    fail "retired API unit must be masked, inactive, and process-free"
  else
    pass "retired API unit is permanently unavailable"
  fi

  retired_listeners="$(ss -H -ltn "sport = :$retired_api_port" 2>/dev/null || true)"
  if [[ -n "$retired_listeners" ]]; then
    fail "retired API TCP port $retired_api_port must have no listener"
  else
    pass "retired API TCP port $retired_api_port is unused"
  fi
}

check_exact_directory_entry_set() {
  local path="$1" metadata="$2" label="$3" expected="$4" actual observed_metadata
  if [[ ! -d "$path" || -L "$path" ]]; then
    fail "$label is missing or is not a real directory"
    return
  fi
  observed_metadata="$(stat -Lc '%U:%G:%a' -- "$path" 2>/dev/null || true)"
  if [[ "$observed_metadata" != "$metadata" ]]; then
    fail "$label metadata is $observed_metadata; expected $metadata"
  fi
  if ! actual="$(find "$path" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C sort)"; then
    fail "$label entry inventory cannot be read"
    return
  fi
  if [[ "$actual" != "$expected" ]]; then
    fail "$label entry set differs from the closed generation-v2 namespace"
  fi
}

check_retired_unit_mask() {
  local unit="$1" mask="$systemd_system_root/$1" enabled active main_pid load_state
  enabled="$(systemctl is-enabled "$unit" 2>/dev/null || true)"
  active="$(systemctl is-active "$unit" 2>/dev/null || true)"
  main_pid="$(unit_property "$unit" MainPID 2>/dev/null || true)"
  load_state="$(unit_property "$unit" LoadState 2>/dev/null || true)"
  if [[ "$enabled" != masked || "$active" != inactive || "$main_pid" != 0 || "$load_state" != masked ]]; then
    fail "$unit must be persistently masked, inactive, process-free, and loaded only as a mask"
  fi
  if [[ ! -L "$mask" || "$(readlink -- "$mask" 2>/dev/null || true)" != /dev/null ||
        "$(stat -c '%U:%G' -- "$mask" 2>/dev/null || true)" != root:root ]]; then
    fail "$unit retirement mask must be one root-owned /etc systemd link to /dev/null"
  fi
  if [[ -e "$systemd_system_root/$unit.d" || -L "$systemd_system_root/$unit.d" ||
        -e "$systemd_runtime_root/$unit" || -L "$systemd_runtime_root/$unit" ||
        -e "$systemd_runtime_root/$unit.d" || -L "$systemd_runtime_root/$unit.d" ||
        -e "$systemd_local_root/$unit" || -L "$systemd_local_root/$unit" ||
        -e "$systemd_local_root/$unit.d" || -L "$systemd_local_root/$unit.d" ||
        -e "$systemd_vendor_root/$unit" || -L "$systemd_vendor_root/$unit" ||
        -e "$systemd_vendor_root/$unit.d" || -L "$systemd_vendor_root/$unit.d" ]]; then
    fail "$unit retains a unit fragment or drop-in outside its permanent retirement mask"
  fi
}

contains_retired_generation_reference() {
  local value="$1" marker
  local -a markers=(
    "$production_namespace_root/Release"
    "$production_namespace_root/.venv"
    "$production_namespace_root/data"
    "$retired_trainer_runtime_root"
    "$retired_trainer_state_root"
    "$production_namespace_root/v2/bin/ascendany-trainer-agent"
  )
  for marker in "${markers[@]}"; do
    [[ "$value" != *"$marker"* ]] || return 0
  done
  return 1
}

check_retired_generation_processes() {
  local process_path process_pid executable working_directory command_line maps reference fd target
  local -a process_paths=() fd_paths=()
  shopt -s nullglob
  process_paths=("$retired_process_root"/[1-9]*)
  shopt -u nullglob
  for process_path in "${process_paths[@]}"; do
    [[ -d "$process_path" ]] || continue
    process_pid="${process_path##*/}"
    if [[ "$retired_process_root" == /proc && "$process_pid" == "$BASHPID" ]]; then
      continue
    fi
    executable="$(readlink -- "$process_path/exe" 2>/dev/null || true)"
    working_directory="$(readlink -- "$process_path/cwd" 2>/dev/null || true)"
    command_line="$(tr '\0' ' ' <"$process_path/cmdline" 2>/dev/null || true)"
    maps="$(sed -n '1,$p' "$process_path/maps" 2>/dev/null || true)"
    reference="$executable"$'\n'"$working_directory"$'\n'"$command_line"$'\n'"$maps"
    if contains_retired_generation_reference "$reference"; then
      fail "process $process_pid retains a retired Python/trainer generation reference"
      continue
    fi
    fd_paths=()
    shopt -s nullglob
    fd_paths=("$process_path"/fd/*)
    shopt -u nullglob
    for fd in "${fd_paths[@]}"; do
      target="$(readlink -- "$fd" 2>/dev/null || true)"
      if contains_retired_generation_reference "$target"; then
        fail "process $process_pid retains an open descriptor into the retired generation"
        break
      fi
    done
  done
}

check_retired_generation_closure() {
  local failures_before="$failures" expected_credentials actual_containers passwd_entries group_entries
  local -a credential_entries=(
    backup_db_password.cred
    cloudflare_tunnel_credentials.cred
    jwt_signing_private_key.cred
    jwt_verification_public_key.cred
    migrator_db_password.cred
    password_pepper.cred
    pgbouncer_userlist.cred
    restore_db_password.cred
    runtime_db_password.cred
  )
  local credential_id path

  check_retired_unit_mask "$retired_api_unit"
  check_retired_unit_mask "$retired_trainer_unit"

  check_exact_directory_entry_set \
    "$production_namespace_root" root:root:755 \
    '/opt/ascendany production namespace' \
    $'.install-v2-release.lock|f\ninfra|d\nv2|d'
  if [[ "$(stat -Lc '%U:%G:%a:%h' -- "$production_namespace_root/.install-v2-release.lock" 2>/dev/null || true)" != root:root:600:1 ]]; then
    fail "generation-v2 installation lock metadata differs from root:root mode 0600 single-link"
  fi
  check_exact_directory_entry_set \
    "$production_namespace_root/infra" root:root:755 \
    '/opt/ascendany/infra production namespace' \
    'pgbouncer|d'
  check_exact_directory_entry_set \
    "$configuration_namespace_root" root:root:755 \
    '/etc/ascendany production namespace' \
    $'credentials|d\nv2|d'
  check_exact_directory_entry_set \
    "$configuration_namespace_root/v2" root:ascendany-runtime:750 \
    '/etc/ascendany/v2 configuration namespace' \
    $'analytics.json|f\nascendanyd-read-only-smoke.env|f\nascendanyd.env|f\nbackup.env|f\njudge.env|f\nmigrate.env|f\nrestore.env|f'

  for credential_id in "${runtime_provider_credential_ids[@]}"; do
    credential_entries+=("$credential_id.cred")
  done
  expected_credentials="$(printf '%s|f\n' "${credential_entries[@]}" | LC_ALL=C sort)"
  check_exact_directory_entry_set \
    "$configuration_namespace_root/credentials" root:root:700 \
    '/etc/ascendany/credentials capability namespace' \
    "$expected_credentials"
  check_exact_directory_entry_set \
    "$catalog_publisher_config_root" root:ascendany-catalog-publisher:750 \
    '/etc/ascendany-catalog-publisher capability namespace' \
    $'catalog-publish.env|f\ncredentials|d'
  check_exact_directory_entry_set \
    "$catalog_publisher_config_root/credentials" root:root:700 \
    '/etc/ascendany-catalog-publisher/credentials capability namespace' \
    'catalog_publisher_db_password.cred|f'
  for path in \
    "$catalog_publisher_config_root/credentials/catalog_publisher_db_password.cred"; do
    if [[ ! -s "$path" || ! -f "$path" || -L "$path" ||
          "$(stat -Lc '%U:%G:%a:%h' -- "$path" 2>/dev/null || true)" != root:root:400:1 ]]; then
      fail "catalog publisher encrypted credential source violates the root-owned 0400 single-link contract: $path"
    fi
  done
  if [[ ! -f "$catalog_publisher_config_root/catalog-publish.env" ||
        -L "$catalog_publisher_config_root/catalog-publish.env" ||
        "$(stat -Lc '%U:%G:%a:%h' -- "$catalog_publisher_config_root/catalog-publish.env" 2>/dev/null || true)" != root:ascendany-catalog-publisher:640:1 ]]; then
    fail "catalog publisher configuration must be a root-owned publisher-readable 0640 single-link file"
  fi

  for path in \
    "$retired_trainer_runtime_root" \
    "$retired_trainer_state_root" \
    "$retired_trainer_log_root"; do
    if [[ -e "$path" || -L "$path" ]]; then
      fail "retired trainer runtime/state path remains: $path"
    fi
  done

  if ! passwd_entries="$(getent passwd)" || ! group_entries="$(getent group)"; then
    fail "local account databases cannot be enumerated for trainer identity retirement"
  else
    if grep -Eq '^ascendany-trainer:' <<<"$passwd_entries"; then
      fail "retired ascendany-trainer OS identity remains"
    fi
    if grep -Eq '^ascendany-trainer:' <<<"$group_entries"; then
      fail "retired ascendany-trainer group remains"
    fi
  fi

  if ! actual_containers="$(podman ps -a --format '{{.Names}}')"; then
    fail "Podman container namespace cannot be enumerated for generation retirement"
  elif grep -Eq '^(ascendany-cloudflared|ascendany-trainer($|-))' <<<"$actual_containers"; then
    fail "a retired Cloudflared/trainer container remains in the production namespace"
  fi

  check_retired_generation_processes
  if (( failures == failures_before )); then
    pass "retired Python/trainer release, runtime, identity, unit, process, config, credential, and container closure is absent"
  fi
}

encrypted_credential_sha256() {
  local credential_name="$1" source="$2" digest
  if ! digest="$({
      "$systemd_creds_binary" --name="$credential_name" decrypt "$source" - 2>/dev/null
    } | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')" ||
     [[ ! "$digest" =~ ^[0-9a-f]{64}$ ]]; then
    return 1
  fi
  /usr/bin/printf '%s' "$digest"
}

check_pgbouncer_plaintext_userlist_contract() {
  local runtime_metadata catalog_pattern runtime_pattern
  local catalog_password runtime_password catalog_source_sha runtime_source_sha
  local catalog_userlist_sha runtime_userlist_sha
  local -a userlist_lines=()

  runtime_metadata="$(stat -Lc '%u:%g:%a:%h' "$pgbouncer_runtime_credential" 2>/dev/null || true)"
  if [[ ! -s "$pgbouncer_runtime_credential" || -L "$pgbouncer_runtime_credential" ||
        "$runtime_metadata" != "0:0:440:1" ||
        -n "$(tail -c 1 -- "$pgbouncer_runtime_credential" 2>/dev/null || true)" ]]; then
    fail "decrypted PgBouncer plaintext userlist violates the protected runtime-file contract"
    return
  fi

  mapfile -t userlist_lines <"$pgbouncer_runtime_credential"
  catalog_pattern='^"ascendany_catalog_publisher_login" "([A-Za-z0-9._~+/@%=-]{32,128})"$'
  runtime_pattern='^"ascendanyd_login" "([A-Za-z0-9._~+/@%=-]{32,128})"$'
  if (( ${#userlist_lines[@]} != 2 )) ||
     [[ ! "${userlist_lines[0]:-}" =~ $catalog_pattern ]]; then
    fail "decrypted PgBouncer userlist violates the fixed-order two-record plaintext contract"
    return
  fi
  catalog_password="${BASH_REMATCH[1]}"
  if [[ ! "${userlist_lines[1]:-}" =~ $runtime_pattern ]]; then
    fail "decrypted PgBouncer userlist violates the fixed-order two-record plaintext contract"
    return
  fi
  runtime_password="${BASH_REMATCH[1]}"

  if ! catalog_source_sha="$(encrypted_credential_sha256 \
      catalog_publisher_db_password "$catalog_publisher_db_credential_source")" ||
     ! runtime_source_sha="$(encrypted_credential_sha256 \
      db_password "$runtime_db_credential_source")"; then
    fail "PgBouncer plaintext userlist cannot be bound to the encrypted database credentials"
    return
  fi
  catalog_userlist_sha="$({
      /usr/bin/printf '%s' "$catalog_password"
    } | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')"
  runtime_userlist_sha="$({
      /usr/bin/printf '%s' "$runtime_password"
    } | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')"
  if [[ "$catalog_userlist_sha" != "$catalog_source_sha" ||
        "$runtime_userlist_sha" != "$runtime_source_sha" ]]; then
    fail "PgBouncer plaintext userlist differs from the encrypted database credentials"
    return
  fi

  pass "native PgBouncer runs with the exact encrypted runtime and catalog publisher plaintext auth entries"
}

check_pgbouncer_contract() {
  local failures_before="$failures" entries metadata installed_nevra binary_sha verify_output verify_status=0
  local active enabled fragment dropins reload dynamic user group main_pid executable
  local uid_line gid_line uid_real uid_effective uid_saved uid_fs
  local gid_real gid_effective gid_saved gid_fs field value
  local relative path conflicting_containers
  local -a argv=()

  check_pgbouncer_service_ownership

  installed_nevra="$(rpm -q --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' pgbouncer 2>/dev/null || true)"
  metadata="$(stat -Lc '%u:%g:%a:%s:%h' "$pgbouncer_binary" 2>/dev/null || true)"
  binary_sha="$(sha256sum "$pgbouncer_binary" 2>/dev/null | awk '{print $1}' || true)"
  verify_output="$(rpm --verify pgbouncer 2>&1)" || verify_status=$?
  if [[ "$installed_nevra" != "$pgbouncer_nevra" ||
        "$metadata" != "0:0:755:$pgbouncer_binary_size:1" ||
        "$binary_sha" != "$pgbouncer_binary_sha256" || "$verify_status" != 0 ||
        -n "$verify_output" ]] ||
     [[ "$($pgbouncer_binary --version 2>&1 | head -n 1)" != "PgBouncer 1.25.2" ]] ||
     ! check_root_owned_ancestry "$pgbouncer_binary" 1; then
    fail "installed PgBouncer package differs from the signed Fedora runtime lock"
  else
    pass "installed PgBouncer package matches the signed Fedora runtime lock"
  fi

  conflicting_containers="$(podman ps -a --format '{{.Names}}' 2>/dev/null |
    grep -E '^ascendany-pgbouncer($|-)' || true)"
  if [[ -n "$conflicting_containers" ]]; then
    fail "a container conflicts with the native PgBouncer ownership boundary"
  else
    pass "the native PgBouncer ownership boundary has no container conflict"
  fi

  if [[ ! -d "$pgbouncer_config_root" || -L "$pgbouncer_config_root" ||
        "$pgbouncer_config_root" != "$(realpath -m -- "$pgbouncer_config_root")" ||
        "$pgbouncer_config_root" != "$(realpath -e -- "$pgbouncer_config_root" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a' "$pgbouncer_config_root" 2>/dev/null || true)" != "0:0:755" ]] ||
     ! check_root_owned_ancestry "$pgbouncer_config_root/pgbouncer.ini" 1; then
    fail "PgBouncer configuration root must be canonical root:root mode 0755"
    return
  fi
  entries="$(find "$pgbouncer_config_root" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C sort)"
  if [[ "$entries" != $'pgbouncer-hba.conf|f\npgbouncer.ini|f' ]]; then
    fail "PgBouncer configuration root differs from the exact two-file closure"
  else
    pass "PgBouncer configuration root has the exact two-file closure"
  fi
  for relative in pgbouncer-hba.conf pgbouncer.ini; do
    path="$pgbouncer_config_root/$relative"
    metadata="$(stat -Lc '%u:%g:%a:%h' "$path" 2>/dev/null || true)"
    if [[ ! -f "$path" || -L "$path" || "$metadata" != "0:0:644:1" ]]; then
      fail "PgBouncer input $relative must be a root:root mode 0644 single-link file"
    fi
  done

  active="$(systemctl is-active "$pgbouncer_unit" 2>/dev/null || true)"
  enabled="$(systemctl is-enabled "$pgbouncer_unit" 2>/dev/null || true)"
  fragment="$(unit_property "$pgbouncer_unit" FragmentPath || true)"
  dropins="$(unit_property "$pgbouncer_unit" DropInPaths || true)"
  reload="$(unit_property "$pgbouncer_unit" NeedDaemonReload || true)"
  dynamic="$(unit_property "$pgbouncer_unit" DynamicUser || true)"
  user="$(unit_property "$pgbouncer_unit" User || true)"
  group="$(unit_property "$pgbouncer_unit" Group || true)"
  main_pid="$(unit_property "$pgbouncer_unit" MainPID || true)"
  if [[ "$active" != active || "$enabled" != enabled ||
        "$fragment" != /etc/systemd/system/ascendany-pgbouncer.service ||
        "$dropins" != /usr/lib/systemd/system/service.d/10-timeout-abort.conf ||
        "$reload" != no || "$dynamic" != yes ||
        "$user" != ascendany-pgbouncer || "$group" != ascendany-pgbouncer ||
        ! "$main_pid" =~ ^[1-9][0-9]*$ ]]; then
    fail "native PgBouncer systemd state or dynamic capability identity differs from the contract"
    return
  fi
  check_unit_effective_shape \
    "$pgbouncer_unit" \
    /etc/systemd/system/ascendany-pgbouncer.service \
    '' \
    /usr/lib/systemd/system/service.d/10-timeout-abort.conf \
    '/usr/bin/pgbouncer -q /opt/ascendany/infra/pgbouncer/pgbouncer.ini' \
    $'/usr/bin/test -x /usr/bin/pgbouncer\n/usr/bin/test -r /opt/ascendany/infra/pgbouncer/pgbouncer.ini\n/usr/bin/test -r /opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf\n/usr/bin/test -s %d/pgbouncer_userlist' \
    ''
  check_unit_credentials "$pgbouncer_unit" pgbouncer_userlist
  check_unit_environment_files "$pgbouncer_unit"
  for property_value in \
    'Type notify-reload' \
    'KillSignal 2' \
    'NoNewPrivileges yes' \
    'PrivateTmp yes' \
    'PrivateDevices yes' \
    'ProtectSystem strict' \
    'ProtectHome yes' \
    'ProtectControlGroups yes' \
    'ProtectKernelTunables yes' \
    'ProtectKernelModules yes' \
    'ProtectKernelLogs yes' \
    'ProtectClock yes' \
    'ProtectHostname yes' \
    'ProtectProc invisible' \
    'ProcSubset pid' \
    'RestrictNamespaces yes' \
    'RestrictRealtime yes' \
    'RestrictSUIDSGID yes' \
    'LockPersonality yes' \
    'MemoryDenyWriteExecute yes' \
    'RemoveIPC yes' \
    'KeyringMode private' \
    'DevicePolicy closed'; do
    check_effective_value "$pgbouncer_unit" "${property_value%% *}" "${property_value#* }"
  done
  check_effective_word_set "$pgbouncer_unit" RestrictAddressFamilies AF_UNIX AF_INET
  check_effective_word_set "$pgbouncer_unit" CapabilityBoundingSet
  check_effective_word_set "$pgbouncer_unit" AmbientCapabilities

  executable="$(readlink -e -- "/proc/$main_pid/exe" 2>/dev/null || true)"
  mapfile -d '' -t argv <"/proc/$main_pid/cmdline" || true
  if [[ "$executable" != "$pgbouncer_binary" || "${#argv[@]}" != 3 ||
        "${argv[0]:-}" != "$pgbouncer_binary" || "${argv[1]:-}" != -q ||
        "${argv[2]:-}" != "$pgbouncer_config_root/pgbouncer.ini" ]]; then
    fail "native PgBouncer process executable or argv differs from the reviewed unit"
  fi
  uid_line="$(awk '$1 == "Uid:" {print $2, $3, $4, $5}' "/proc/$main_pid/status" 2>/dev/null || true)"
  gid_line="$(awk '$1 == "Gid:" {print $2, $3, $4, $5}' "/proc/$main_pid/status" 2>/dev/null || true)"
  read -r uid_real uid_effective uid_saved uid_fs <<<"$uid_line"
  read -r gid_real gid_effective gid_saved gid_fs <<<"$gid_line"
  if [[ -z "$uid_real" || "$uid_real" == 0 || "$uid_real" != "$uid_effective" ||
        "$uid_real" != "$uid_saved" || "$uid_real" != "$uid_fs" ||
        -z "$gid_real" || "$gid_real" == 0 || "$gid_real" != "$gid_effective" ||
        "$gid_real" != "$gid_saved" || "$gid_real" != "$gid_fs" ]]; then
    fail "native PgBouncer process does not use one non-root dynamic UID/GID"
  fi
  if [[ "$(awk '$1 == "NoNewPrivs:" {print $2}' "/proc/$main_pid/status")" != 1 ||
        "$(awk '$1 == "Seccomp:" {print $2}' "/proc/$main_pid/status")" != 2 ]]; then
    fail "native PgBouncer process lacks no-new-privileges or seccomp enforcement"
  fi
  for field in CapInh CapPrm CapEff CapBnd CapAmb; do
    value="$(awk -v field="$field:" '$1 == field {print $2; exit}' "/proc/$main_pid/status")"
    [[ "$value" == 0000000000000000 ]] || fail "native PgBouncer process $field is not empty"
  done
  if tr '\0' '\n' <"/proc/$main_pid/environ" |
      grep -E '(^|_)(HTTP|HTTPS|ALL|NO)_PROXY=|PASSWORD=|TOKEN=|SECRET=|CREDENTIAL=' >/dev/null; then
    fail "native PgBouncer process inherited proxy or plaintext secret environment"
  fi

  check_pgbouncer_plaintext_userlist_contract

  if (( failures == failures_before )); then
    probe_pgbouncer_hba_rejection ascendanyd_login postgres
    probe_pgbouncer_hba_rejection postgres ascendany_v2
  fi
}

postgres_admin_psql() {
  podman exec -i --user postgres ascendany-postgres \
    /usr/bin/env -i \
      HOME=/var/lib/postgresql \
      PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
      LC_ALL=C \
      /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
        --username=postgres "$@"
}

check_postgres_schema_fingerprint() {
  local helper="$release_root/scripts/postgres-schema-fingerprint.sh"
  local failures_before="$failures" expected actual

  if ! expected="$(/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
      "$helper" --expected-sha256)" || [[ ! "$expected" =~ ^[0-9a-f]{64}$ ]]; then
    fail "release PostgreSQL schema helper has no canonical expected SHA-256"
    return
  fi
  if ! actual="$(
      /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C "$helper" --emit-sql |
        postgres_admin_psql --dbname=ascendany_v2 --tuples-only --no-align --quiet |
        LC_ALL=C sort |
        sha256sum |
        awk '{print $1}'
    )"; then
    fail "PostgreSQL schema fingerprint query failed"
    return
  fi
  if [[ ! "$actual" =~ ^[0-9a-f]{64}$ || "$actual" != "$expected" ]] ||
     ! /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
       "$helper" --verify-sha256 "$actual"; then
    fail "PostgreSQL schema fingerprint differs from the canonical schema-v10 contract"
  elif (( failures == failures_before )); then
    pass "PostgreSQL columns, constraints, indexes, triggers, and routines match the canonical schema-v10 fingerprint"
  fi
}

database_fingerprint_sha256() {
  local scope="$1"
  case "$scope" in
    full)
      postgres_admin_psql --dbname=ascendany_v2 --tuples-only --no-align <<'SQL' |
SELECT format(
  'SELECT %L || ''|'' || COALESCE(jsonb_agg(to_jsonb(row_value) ORDER BY to_jsonb(row_value)::text)::text, ''[]'') FROM %I.%I AS row_value;',
  'table:' || table_name, table_schema, table_name
)
FROM information_schema.tables
WHERE table_schema = 'ascendany' AND table_type = 'BASE TABLE'
ORDER BY table_name
\gexec
SELECT format(
  'SELECT %L || ''|'' || jsonb_build_object(''lastValue'', last_value, ''isCalled'', is_called)::text FROM %I.%I;',
  'sequence:' || relation.relname, namespace.nspname, relation.relname
)
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany' AND relation.relkind = 'S'
ORDER BY relation.relname
\gexec
SQL
        LC_ALL=C sort | sha256sum | awk '{print $1}'
      ;;
    business)
      postgres_admin_psql --dbname=ascendany_v2 --tuples-only --no-align <<'SQL' |
SELECT format(
  'SELECT %L || ''|'' || COALESCE(jsonb_agg(to_jsonb(row_value) ORDER BY to_jsonb(row_value)::text)::text, ''[]'') FROM %I.%I AS row_value;',
  'table:' || table_name, table_schema, table_name
)
FROM information_schema.tables
WHERE table_schema = 'ascendany'
  AND table_type = 'BASE TABLE'
  AND table_name NOT IN (
    'recommendation_model_activation_events',
    'recommendation_model_head',
    'recommendation_model_releases'
  )
ORDER BY table_name
\gexec
SELECT format(
  'SELECT %L || ''|'' || jsonb_build_object(''lastValue'', last_value, ''isCalled'', is_called)::text FROM %I.%I;',
  'sequence:' || relation.relname, namespace.nspname, relation.relname
)
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany'
  AND relation.relkind = 'S'
  AND relation.relname <> 'recommendation_model_release_ids_seq'
ORDER BY relation.relname
\gexec
SQL
        LC_ALL=C sort | sha256sum | awk '{print $1}'
      ;;
    *)
      return 2
      ;;
  esac
}

expected_initial_table_names() {
  cat <<'TABLES'
achievement_rule_head
achievement_rule_sets
achievement_rules
agent_note_revisions
agent_notes
agent_run_events
agent_runs
agent_tool_calls
analytics_generation_events
analytics_generation_snapshots
analytics_generations
analytics_head
artifacts
audit_events
auth_accounts
auth_enrollment_events
auth_enrollment_grants
auth_refresh_tokens
auth_sessions
chat_messages
chat_threads
configuration_items
configuration_versions
exam_snapshots
feedback_attachments
feedback_delivery_events
feedback_delivery_jobs
feedback_submissions
import_job_events
import_jobs
knowledge_catalog_publication_authorizations
knowledge_catalog_publications
logical_exams
oj_judge_job_events
oj_judge_jobs
oj_judge_results
oj_problem_versions
oj_problems
oj_submissions
pintia_actor_identifiers
pintia_actors
pintia_ranking_problem_results
pintia_rankings
pintia_snapshot_participants
pintia_snapshot_problems
pintia_snapshot_submissions
pintia_submission_case_results
pintia_submission_identities
problem_analytics
recommendation_model_activation_events
recommendation_model_head
recommendation_model_releases
schema_migrations_v2
student_analytics
TABLES
}

expected_initial_sequence_names() {
  cat <<'SEQUENCES'
achievement_rule_sets_achievement_rule_set_id_seq
agent_note_revisions_agent_note_revision_id_seq
agent_notes_agent_note_id_seq
agent_runs_agent_run_id_seq
agent_tool_calls_agent_tool_call_id_seq
analytics_generations_analytics_generation_id_seq
artifacts_artifact_id_seq
audit_events_audit_event_id_seq
auth_accounts_account_id_seq
auth_enrollment_events_enrollment_event_id_seq
auth_enrollment_grants_enrollment_grant_id_seq
auth_refresh_tokens_refresh_token_id_seq
auth_sessions_session_id_seq
chat_messages_chat_message_id_seq
chat_threads_chat_thread_id_seq
configuration_items_configuration_item_id_seq
configuration_versions_configuration_version_id_seq
exam_snapshots_snapshot_id_seq
feedback_delivery_jobs_feedback_delivery_job_id_seq
feedback_submissions_feedback_id_seq
import_jobs_import_job_id_seq
knowledge_catalog_publication_ids_seq
logical_exams_exam_id_seq
oj_judge_jobs_judge_job_id_seq
oj_judge_results_judge_result_id_seq
oj_problem_versions_oj_problem_version_id_seq
oj_problems_oj_problem_id_seq
oj_submissions_oj_submission_id_seq
pintia_actors_actor_id_seq
pintia_submission_identities_submission_identity_id_seq
recommendation_model_release_ids_seq
SEQUENCES
}

expected_initial_migration_rows() {
  cat <<'MIGRATIONS'
migration|1|fresh_schema|0cffdb00acefd37c049a654bad76d8fac79727ed7c54cc3fa9234d54964ce0cf
migration|2|product_domains|1762304608ed3f93d62c01ad494a2b6110b07737cc652f38a2581392985fdd36
migration|3|recommendation_catalog_contract|6fa4a81fbe3440fc4b149a5b77d6c3860031e285bafef50b5a881e8783f36267
migration|4|achievement_rules|3242ddfbdee0911d961ebe0f46237f6e2b8a6e7c5e09cf1d94f6ae98c4caaccb
migration|5|auto_analysis_once|40fed038bc7773f45e940de2880ca18427573e10555937afa202e684aecdaa17
migration|6|inference_model_runtime|330bd7bebdd6e67572a76fcb0c1e84c897df2a766f6e821312c46ecfc18e39ea
migration|7|catalog_publication_provenance|a69c081d1b0eaa31df8490773d3feed355fdb4053925f84087552df9b5fc940b
migration|8|auto_analysis_frontend_context|117d0eff2231d23929e91dda1f463d766b0d2dd7c8ff381266b5431f25cc4ed9
migration|9|auth_pta_nickname|6ec2def4d4e433fd6d1dc915b582d724a445b79f5fa023260bb841a66e2e630e
migration|10|feedback_duplicate_attachments|08cd0e1437ffa16c41ef4de0d1857acff38e15626770cd1dc2ec80dc2e7855e5
MIGRATIONS
}

expected_initial_achievement_rows() {
  cat <<'ACHIEVEMENTS'
achievement-set|1|1
achievement-rule|1|accuracy_max|准确进阶|准确单维最高分达到 60 / 75 / 90。|accuracy_max|60|75|90|7
achievement-rule|1|ai_dialogue_count|AI陪练|与 AI 成功对话次数达到 3 / 15 / 40 次。|ai_dialogue_count|3|15|40|5
achievement-rule|1|best_positive_streak|稳定连涨|最佳连涨场次达到 2 / 4 / 6 场。|best_positive_streak|2|4|6|4
achievement-rule|1|current_min_metric|全能王者|当前五维最低分达到 70 / 80 / 90。|current_min_metric|70|80|90|18
achievement-rule|1|exam_count_first|初试锋芒|累计参赛次数达到 1 / 3 / 8 场。|exam_count|1|3|8|1
achievement-rule|1|exam_count_veteran|久经赛场|累计参赛次数达到 5 / 12 / 20 场。|exam_count|5|12|20|2
achievement-rule|1|flexibility_max|灵活进阶|灵活单维最高分达到 60 / 75 / 90。|flexibility_max|60|75|90|9
achievement-rule|1|knowledge_max|知识进阶|知识单维最高分达到 60 / 75 / 90。|knowledge_max|60|75|90|6
achievement-rule|1|max_of_exam_min_metric|均衡发展|单场五维最低分的历史最高值达到 55 / 65 / 75。|max_of_exam_min_metric|55|65|75|15
achievement-rule|1|max_rating|评级起飞|历史最高 rating 达到 900 / 1000 / 1200。|max_rating|900|1000|1200|11
achievement-rule|1|max_rating_delta|单场爆发|历史单场涨分达到 15 / 30 / 50。|max_rating_delta|15|30|50|12
achievement-rule|1|positive_delta_count|首次上分|rating 正增长次数达到 1 / 3 / 8 次。|positive_delta_count|1|3|8|3
achievement-rule|1|proficiency_max|熟练进阶|熟练单维最高分达到 60 / 75 / 90。|proficiency_max|60|75|90|10
achievement-rule|1|quality_max|质量进阶|质量单维最高分达到 60 / 75 / 90。|quality_max|60|75|90|8
achievement-rule|1|rank1_count|冠军时刻|总排名第 1 次数达到 1 / 2 / 3 次。|rank1_count|1|2|3|17
achievement-rule|1|top10_count|前十常客|排名前十次数达到 1 / 3 / 6 次。|top10_count|1|3|6|13
achievement-rule|1|top3_count|三甲选手|排名前三次数达到 1 / 2 / 4 次。|top3_count|1|2|4|14
achievement-head|true|1|1
ACHIEVEMENTS
}

initial_database_state_snapshot() {
  postgres_admin_psql --dbname=ascendany_v2 --tuples-only --no-align <<'SQL'
SELECT format(
  'SELECT %L || count(*)::text FROM %I.%I;',
  'table:' || table_name || '|', table_schema, table_name
)
FROM information_schema.tables
WHERE table_schema = 'ascendany' AND table_type = 'BASE TABLE'
ORDER BY table_name
\gexec
SELECT format(
  'SELECT %L || last_value::text || ''|'' || is_called::text FROM %I.%I;',
  'sequence:' || relation.relname || '|', namespace.nspname, relation.relname
)
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany' AND relation.relkind = 'S'
ORDER BY relation.relname
\gexec
SELECT concat_ws('|', 'migration', version::text, name, sha256)
FROM ascendany.schema_migrations_v2
ORDER BY version;
SELECT concat_ws(
  '|', 'analytics-head', singleton::text,
  COALESCE(current_generation_id::text, ''), head_revision::text
)
FROM ascendany.analytics_head;
SELECT concat_ws('|', 'achievement-set', achievement_rule_set_id::text, version::text)
FROM ascendany.achievement_rule_sets
ORDER BY achievement_rule_set_id;
SELECT concat_ws(
  '|', 'achievement-rule', achievement_rule_set_id::text, achievement_code,
  title, description, progress_key, bronze_target::text, silver_target::text,
  gold_target::text, sort_order::text
)
FROM ascendany.achievement_rules
ORDER BY achievement_code COLLATE "C";
SELECT concat_ws(
  '|', 'achievement-head', singleton::text, current_rule_set_id::text,
  head_revision::text
)
FROM ascendany.achievement_rule_head;
SQL
}

check_initial_database_state() {
  initial_transition && [[ "$validation_phase" == staged || "$validation_phase" == smoke ||
    "$validation_phase" == activation ]] || return 0
  local failures_before="$failures" snapshot actual expected label count table sequence last_value is_called
  local unknown migration_rows analytics_rows achievement_rows
  local model_activated=0
  activation_phase && model_activated=1
  if ! snapshot="$(initial_database_state_snapshot)"; then
    fail "initial fresh database inventory query failed"
    return
  fi
  unknown="$(grep -Ev '^(table:|sequence:|migration\||analytics-head\||achievement-(set|rule|head)\|)' <<<"$snapshot" || true)"
  if [[ -n "$unknown" ]]; then
    fail "initial fresh database inventory contains an unclassified or malformed record"
  fi

  actual="$(sed -n 's/^table:\([^|]*\)|.*$/\1/p' <<<"$snapshot" | LC_ALL=C sort)"
  expected="$(expected_initial_table_names)"
  if [[ "$actual" != "$expected" ]]; then
    fail "initial fresh database base-table set differs from the schema-v10 contract"
  fi
  while IFS='|' read -r label count; do
    [[ -n "$label" ]] || continue
    table="${label#table:}"
    if [[ ! "$count" =~ ^[0-9]+$ ]]; then
      fail "initial fresh database has a noncanonical row count for $table"
      continue
    fi
    case "$table" in
      schema_migrations_v2) expected=10 ;;
      achievement_rule_sets|achievement_rule_head|analytics_head) expected=1 ;;
      achievement_rules) expected=17 ;;
      recommendation_model_activation_events|recommendation_model_head|recommendation_model_releases)
        expected="$model_activated"
        ;;
      *) expected=0 ;;
    esac
    if [[ "$count" != "$expected" ]]; then
      fail "initial fresh database table $table has $count rows; expected $expected"
    fi
  done < <(grep '^table:' <<<"$snapshot" || true)

  actual="$(sed -n 's/^sequence:\([^|]*\)|.*$/\1/p' <<<"$snapshot" | LC_ALL=C sort)"
  expected="$(expected_initial_sequence_names)"
  if [[ "$actual" != "$expected" ]]; then
    fail "initial fresh database sequence set differs from the schema-v10 contract"
  fi
  while IFS='|' read -r label last_value is_called; do
    [[ -n "$label" ]] || continue
    sequence="${label#sequence:}"
    if [[ "$sequence" == achievement_rule_sets_achievement_rule_set_id_seq ]]; then
      expected='1|true'
    elif [[ "$sequence" == recommendation_model_release_ids_seq && "$model_activated" == 1 ]]; then
      expected='1|true'
    else
      expected='1|false'
    fi
    if [[ "$last_value|$is_called" != "$expected" ]]; then
      fail "initial fresh database sequence $sequence is $last_value/$is_called; expected ${expected//|//}"
    fi
  done < <(grep '^sequence:' <<<"$snapshot" || true)

  migration_rows="$(grep '^migration|' <<<"$snapshot" || true)"
  if [[ "$migration_rows" != "$(expected_initial_migration_rows)" ]]; then
    fail "initial fresh database migration manifest differs from the embedded schema-v10 manifest"
  fi
  analytics_rows="$(grep '^analytics-head|' <<<"$snapshot" || true)"
  if [[ "$analytics_rows" != 'analytics-head|true||0' ]]; then
    fail "initial fresh database analytics singleton differs from the zero-head seed"
  fi
  achievement_rows="$(grep '^achievement-' <<<"$snapshot" || true)"
  if [[ "$achievement_rows" != "$(expected_initial_achievement_rows)" ]]; then
    fail "initial fresh database achievement seed rows differ from migration v4"
  fi
  if (( failures == failures_before )); then
    pass "initial $validation_phase database exactly matches all 54 base tables, 31 sequences, migrations, permitted seeds, and phase-owned model state"
  fi
}

check_forward_database_state() {
  local full_fingerprint business_fingerprint
  forward_transition || return 0
  production_phase && return 0
  if ! full_fingerprint="$(database_fingerprint_sha256 full)" ||
     ! business_fingerprint="$(database_fingerprint_sha256 business)"; then
    fail "forward database fingerprint query failed"
    return
  fi
  if [[ ! "$full_fingerprint" =~ ^[0-9a-f]{64}$ ||
        ! "$business_fingerprint" =~ ^[0-9a-f]{64}$ ]]; then
    fail "forward database fingerprint query returned a noncanonical digest"
    return
  fi
  observed_forward_database_fingerprint="$full_fingerprint"
  observed_forward_business_fingerprint="$business_fingerprint"
  if [[ "$validation_phase" == staged ]]; then
    [[ "$observed_forward_model_head_revision" =~ ^[1-9][0-9]*$ &&
       "$observed_forward_model_artifact_sha256" =~ ^[0-9a-f]{64}$ ]] || {
      fail "forward staged capture lacks a retained model-head revision or artifact digest"
      return
    }
    pass "forward staged capture bound every AscendAny base table and sequence"
  elif [[ "$validation_phase" == smoke ]]; then
    if [[ "$full_fingerprint" != "$expected_forward_database_fingerprint" ||
          "$business_fingerprint" != "$expected_forward_business_fingerprint" ]]; then
      fail "forward read-only smoke changed an AscendAny base table or sequence"
    else
      pass "forward read-only smoke preserved every AscendAny base table and sequence exactly"
    fi
  elif catalog_phase && [[ "$full_fingerprint" == "$expected_forward_database_fingerprint" ||
      "$business_fingerprint" == "$expected_forward_business_fingerprint" ]]; then
    fail "forward catalog publication did not advance both the full and business database fingerprints"
  elif catalog_phase; then
    pass "forward catalog publication advanced stopped-runtime configuration/audit state while retaining the prior model head"
  elif activation_phase && [[ "$business_fingerprint" != "$expected_forward_business_fingerprint" ]]; then
    fail "forward activation changed retained business or durable-job database state"
  elif activation_phase && [[ "$full_fingerprint" == "$expected_forward_database_fingerprint" ]]; then
    fail "forward activation did not advance immutable recommendation model state"
  elif activation_phase; then
    pass "forward activation changed only recommendation model state and preserved all retained business data"
  else
    fail "forward database fingerprint verification reached an unsupported phase"
  fi
}

check_provisioning_terminal_state() {
  local receipt=/var/lib/ascendany-v2-provision/receipt entries expected actual
  local retained_pool_paths system_identifier
  retained_pool_paths="$(find /opt/ascendany/infra -mindepth 1 -maxdepth 1 \
    -name '.pgbouncer.stage.*' \
    -printf '%f\n' | LC_ALL=C sort)"
  entries="$(find /var/lib/ascendany-v2-provision -mindepth 1 -maxdepth 1 -printf '%f|%y\n' 2>/dev/null | LC_ALL=C sort)"
  if [[ ! -d /var/lib/ascendany-v2-provision ||
        -L /var/lib/ascendany-v2-provision ||
        "$(stat -Lc '%u:%g:%a' /var/lib/ascendany-v2-provision 2>/dev/null || true)" != 0:0:700 ||
        "$entries" != 'receipt|f' ||
        ! -f "$receipt" || -L "$receipt" ||
        "$(stat -Lc '%u:%g:%a:%h' "$receipt" 2>/dev/null || true)" != 0:0:400:1 ||
        -e /run/ascendany-v2-provision ||
        -L /run/ascendany-v2-provision ||
        -n "$retained_pool_paths" ]]; then
    fail "PostgreSQL/PgBouncer provisioning receipt or consumed-input boundary differs"
    return
  fi
  system_identifier="$(postgres_admin_psql --dbname=postgres --tuples-only --no-align --command='SELECT system_identifier FROM pg_control_system()' 2>/dev/null || true)"
  expected="$(printf '%s\n' \
    'schema=ascendany.postgres-pgbouncer.provision.v2' \
    'database=ascendany_v2' \
    "postgresSystemIdentifier=$system_identifier" \
    "roleBootstrapSHA256=$(sha256sum "$release_root/db/roles/001_v2_roles.sql" | awk '{print $1}')" \
    "postgresHBASHA256=$(sha256sum "$release_root/config/postgresql-hba.conf" | awk '{print $1}')" \
    "postgresIdentSHA256=$(sha256sum "$release_root/config/postgresql-ident.conf" | awk '{print $1}')" \
    "pgbouncerConfigSHA256=$(sha256sum "$release_root/config/pgbouncer.ini" | awk '{print $1}')" \
    "pgbouncerHBASHA256=$(sha256sum "$release_root/config/pgbouncer-hba.conf" | awk '{print $1}')")"
  actual="$(<"$receipt")"
  if [[ ! "$system_identifier" =~ ^[0-9]{10,20}$ || "$actual" != "$expected" ]]; then
    fail "PostgreSQL/PgBouncer provisioning receipt differs from live/release provenance"
  else
    pass "PostgreSQL/PgBouncer provisioning receipt binds the live cluster and release inputs"
  fi
}

postgres_container_generation_contract() {
  jq -e \
    --arg imageId "$postgres_image_id" \
    --arg imageReference "$postgres_image_reference" \
    --arg volume "$postgres_data_volume" '
      type == "array" and length == 1 and
      .[0].Image == $imageId and
      .[0].Config.Image == $imageReference and
      .[0].Config.Cmd == ["postgres", "-c", "password_encryption=scram-sha-256"] and
      .[0].HostConfig.RestartPolicy == {Name: "always", MaximumRetryCount: 0} and
      .[0].HostConfig.PortBindings == {"5432/tcp": [{HostIp: "127.0.0.1", HostPort: "5432"}]} and
      (.[0].Mounts | length) == 1 and
      .[0].Mounts[0].Type == "volume" and
      .[0].Mounts[0].Name == $volume and
      .[0].Mounts[0].Destination == "/var/lib/postgresql/data" and
      .[0].Mounts[0].RW == true and
      (.[0].Mounts[0].Options | sort) == ["nodev", "nosuid", "rbind"] and
      (.[0].Config.Env | type) == "array" and
      ((.[0].Config.Env | map(capture("^(?<key>[^=]+)=(?<value>.*)$")) |
        map({key: .key, value: .value}) | from_entries) as $environment |
      ($environment | keys) == [
        "GOSU_VERSION", "HOME", "HOSTNAME", "LANG", "PATH", "PGDATA",
        "PG_MAJOR", "PG_VERSION", "container"
      ] and
      $environment.GOSU_VERSION == "1.19" and
      $environment.HOME == "/root" and
      ($environment.HOSTNAME | test("^[0-9a-f]{12}$")) and
      $environment.LANG == "en_US.utf8" and
      $environment.PATH == "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/lib/postgresql/17/bin" and
      $environment.PGDATA == "/var/lib/postgresql/data" and
      $environment.PG_MAJOR == "17" and
      $environment.PG_VERSION == "17.10-1.pgdg13+1" and
      $environment.container == "podman")
    ' >/dev/null
}

check_postgresql_access_contract() {
  local inspect_json network_json network_contract result relative source target expected_sha actual_sha
  local expected_size metadata file_mtime hba_mtime='' ident_mtime=''

  if ! podman container exists ascendany-postgres >/dev/null 2>&1 ||
     [[ "$(podman inspect --format '{{.State.Running}}' ascendany-postgres 2>/dev/null || true)" != true ]]; then
    fail "PostgreSQL 17 production container is missing or inactive"
    return
  fi
  inspect_json="$(podman inspect ascendany-postgres 2>/dev/null || true)"
  network_json="$(podman network inspect "$postgres_network" 2>/dev/null || true)"
  if ! postgres_container_generation_contract <<<"$inspect_json"; then
    fail "PostgreSQL container image, command, restart, volume, port, or secret-free environment differs from the fresh generation contract"
  else
    pass "PostgreSQL runs from the pinned PG17 image with one volume and no retained bootstrap secret/proxy environment"
  fi
  network_contract="$(jq -r \
    --arg network "$postgres_network" \
    --arg gateway "$postgres_gateway" \
    --arg address "$postgres_address" '
      if type == "array" and length == 1 and
         (.[0].NetworkSettings.Networks | keys) == [$network] and
         .[0].NetworkSettings.Networks[$network].Gateway == $gateway and
         .[0].NetworkSettings.Networks[$network].IPAddress == $address and
         .[0].NetworkSettings.Networks[$network].IPPrefixLen == 16
      then "exact" else "" end
    ' <<<"$inspect_json" 2>/dev/null || true)"
  if [[ "$network_contract" != exact ]] || ! jq -e \
      --arg network "$postgres_network" \
      --arg gateway "$postgres_gateway" \
      --arg subnet "$postgres_subnet" '
        type == "array" and length == 1 and
        .[0].name == $network and
        .[0].driver == "bridge" and
        .[0].network_interface == "podman0" and
        .[0].internal == false and
        .[0].ipv6_enabled == false and
        .[0].subnets == [{"subnet": $subnet, "gateway": $gateway}]
      ' <<<"$network_json" >/dev/null 2>&1; then
    fail "PostgreSQL container network differs from the release-owned native service boundary"
  else
    pass "PostgreSQL container network matches the release-owned native service boundary"
  fi

  for relative in postgresql-hba.conf postgresql-ident.conf; do
    source="$release_root/config/$relative"
    case "$relative" in
      postgresql-hba.conf) target=/var/lib/postgresql/data/pg_hba.conf ;;
      postgresql-ident.conf) target=/var/lib/postgresql/data/pg_ident.conf ;;
    esac
    expected_sha="$(sha256sum "$source" | awk '{print $1}')"
    expected_size="$(stat -Lc '%s' "$source")"
    actual_sha="$(podman exec ascendany-postgres /usr/bin/sha256sum "$target" 2>/dev/null |
      awk '{print $1}' || true)"
    metadata="$(podman exec ascendany-postgres /usr/bin/stat -Lc '%u:%g:%a:%h:%s' "$target" 2>/dev/null || true)"
    file_mtime="$(podman exec ascendany-postgres /usr/bin/stat -Lc '%y' "$target" 2>/dev/null || true)"
    case "$relative" in
      postgresql-hba.conf) hba_mtime="$file_mtime" ;;
      postgresql-ident.conf) ident_mtime="$file_mtime" ;;
    esac
    if [[ "$actual_sha" != "$expected_sha" || "$metadata" != "999:999:600:1:$expected_size" ]]; then
      fail "live PostgreSQL $relative differs from the immutable release bytes or metadata"
    else
      pass "live PostgreSQL $relative matches the immutable release bytes and metadata"
    fi
  done

  if ! result="$(postgres_admin_psql --dbname=postgres --tuples-only --no-align --field-separator='|' \
    --set=hba_mtime="$hba_mtime" --set=ident_mtime="$ident_mtime" <<'SQL'
SELECT
  current_user = 'postgres',
  current_setting('server_version_num')::int / 10000 = 17,
  current_setting('password_encryption') = 'scram-sha-256',
  current_setting('fsync') = 'on',
  current_setting('synchronous_commit') = 'on',
  current_setting('full_page_writes') = 'on',
  current_setting('hba_file') = '/var/lib/postgresql/data/pg_hba.conf',
  current_setting('ident_file') = '/var/lib/postgresql/data/pg_ident.conf',
  :'hba_mtime'::timestamptz <= pg_conf_load_time(),
  :'ident_mtime'::timestamptz <= pg_conf_load_time(),
  NOT EXISTS (SELECT 1 FROM pg_hba_file_rules WHERE error IS NOT NULL),
  NOT EXISTS (SELECT 1 FROM pg_ident_file_mappings WHERE error IS NOT NULL),
  EXISTS (
    SELECT 1 FROM pg_authid AS auth
    WHERE auth.rolname = 'postgres'
      AND auth.rolcanlogin AND auth.rolsuper AND NOT auth.rolinherit
      AND NOT auth.rolcreatedb AND NOT auth.rolcreaterole
      AND NOT auth.rolreplication AND NOT auth.rolbypassrls
      AND auth.rolconnlimit = -1 AND auth.rolpassword IS NULL
      AND (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = auth.oid) IS NULL
      AND shobj_description(auth.oid, 'pg_authid') = 'ascendany.postgres.dba.v2'
  ),
  (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'ascendany_v2') = 'ascendany_database_owner',
  (SELECT string_agg(rolname, ',' ORDER BY rolname) FROM pg_roles WHERE rolname !~ '^pg_') =
    'ascendany_backup,ascendany_backup_login,ascendany_catalog_publisher,ascendany_catalog_publisher_login,ascendany_database_owner,ascendany_migrator,ascendany_migrator_login,ascendany_owner,ascendany_restore_login,ascendany_runtime,ascendanyd_login,postgres',
  (SELECT string_agg(datname, ',' ORDER BY datname) FROM pg_database) =
    'ascendany_v2,postgres,template0,template1',
  NOT EXISTS (SELECT 1 FROM pg_db_role_setting),
  NOT EXISTS (SELECT 1 FROM pg_replication_slots),
  (SELECT count(*) = 5 AND count(DISTINCT rolpassword) = 5
   FROM pg_authid
   WHERE rolname = ANY(ARRAY[
     'ascendanyd_login', 'ascendany_migrator_login',
     'ascendany_backup_login', 'ascendany_restore_login',
     'ascendany_catalog_publisher_login'
   ])
     AND rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$');
SQL
)"; then
    fail "PostgreSQL peer-admin access or role/HBA catalog verification failed"
  elif [[ "$result" != 't|t|t|t|t|t|t|t|t|t|t|t|t|t|t|t|t|t|t' ]]; then
    fail "PostgreSQL durability, loaded access-file receipt, DBA role or v2 ownership differs from the closed contract"
  else
    pass "PostgreSQL durability, loaded access-file receipt, DBA role and v2 ownership match the closed contract"
  fi
}

check_backup_model_provenance() {
  local backup_id="$1" manifest_path="$2" evidence_path="$3"
  local model_path="$release_root/models/recommendation-model.json"
  local actual_manifest_sha installed_model_sha installed_model_size installed_model_manifest_sha

  actual_manifest_sha="$(sha256sum -- "$manifest_path" 2>/dev/null | awk '{print $1}')"
  installed_model_sha="$(sha256sum -- "$model_path" 2>/dev/null | awk '{print $1}')"
  installed_model_size="$(stat -Lc '%s' -- "$model_path" 2>/dev/null || true)"
  if ! installed_model_manifest_sha="$(jq -jSc '{
      schema: .schema,
      modelId: .manifest.modelId,
      purpose: .manifest.purpose,
      trainedAt: .manifest.trainedAt,
      algorithm: .manifest.algorithm,
      inferenceContract: .manifest.inferenceContract,
      trainingProvenanceSha256: .manifest.trainingProvenanceSha256,
      featureSchemaSha256: .manifest.featureSchemaSha256,
      knowledgeCatalogSha256: .manifest.knowledgeCatalogSha256,
      parameterSha256: .manifest.parameterSha256,
      goldenVectorsSha256: .manifest.goldenVectorsSha256,
      actorFeatureIds: .manifest.actorFeatureIds,
      problemFeatureIds: .manifest.problemFeatureIds,
      knowledgePointIds: .manifest.knowledgePointIds
    }' "$model_path" | sha256sum | awk '{print $1}')"; then
    fail "installed recommendation model manifest cannot be canonicalized for backup validation"
    return 1
  fi
  if [[ ! "$actual_manifest_sha" =~ ^[0-9a-f]{64}$ ||
        "$installed_model_sha" != "$release_model_sha256" ||
        ! "$installed_model_size" =~ ^[1-9][0-9]*$ ||
        ! "$installed_model_manifest_sha" =~ ^[0-9a-f]{64}$ ]]; then
    fail "backup model validation lacks an intact current release/model trust anchor"
    return 1
  fi

  if ! jq -e \
      --arg backupId "$backup_id" \
      --arg manifestSHA256 "$actual_manifest_sha" \
      --arg releaseCommit "$release_manifest_commit" \
      --arg releaseVersion "$release_manifest_version" \
      --arg releaseBuildTime "$release_manifest_build_time" \
      --arg releasePurpose "$release_manifest_purpose" \
      --arg releaseModelSHA256 "$release_model_sha256" \
      --arg installedModelManifestSHA256 "$installed_model_manifest_sha" \
      --arg catalogReceiptRoot "$restore_catalog_receipt_root" \
      --argjson installedModelSize "$installed_model_size" \
      --slurpfile evidence "$evidence_path" \
      --slurpfile model "$model_path" '
        def sha256: type == "string" and test("^[0-9a-f]{64}$");
        def utc_timestamp:
          type == "string" and
          test("^[0-9]{4}-(0[1-9]|1[0-2])-([0-2][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\\.[0-9]{1,9})?Z$");
        . as $bundle |
        ($model | length == 1) and ($evidence | length == 1) and
        (keys == ["artifacts", "backupId", "catalogPublicationReceipts", "createdAt", "database", "schema"]) and
        .schema == "ascendany.backup.bundle.v2" and
        .backupId == $backupId and
        (.createdAt | utc_timestamp) and
        (.database | type == "object" and
          keys == [
            "databaseName", "file", "knowledgeCatalogPublicationIds",
            "knowledgeCatalogPublications", "migrations", "recommendationModel"
          ]) and
        .database.databaseName == "ascendany_v2" and
        (.catalogPublicationReceipts | type == "object" and
          keys == ["count", "entries", "file", "totalBytes"] and
          (.count | type == "number" and floor == . and . > 0) and
          (.totalBytes | type == "number" and floor == . and . > 0) and
          (.file | type == "object" and keys == ["filename", "format", "sha256", "sizeBytes"] and
            .filename == "catalog-receipts.tar.zst" and .format == "tar+zstd" and
            (.sha256 | sha256) and (.sizeBytes | type == "number" and floor == . and . > 0)) and
          (.entries | type == "array" and length == $bundle.catalogPublicationReceipts.count) and
          ([.entries[].publicationId] == $bundle.database.knowledgeCatalogPublicationIds) and
          ([.entries[].publicationId] == [$bundle.database.knowledgeCatalogPublications[].knowledgeCatalogPublicationId])) and
        (.database.recommendationModel | type == "object" and
          keys == [
            "activatedAt", "algorithm", "applicationBuildTime", "applicationCommit",
            "applicationVersion", "artifactMode", "artifactSha256", "artifactSizeBytes",
            "featureSchemaSha256", "goldenVectorsSha256", "headRevision", "headUpdatedAt",
            "inferenceContract", "knowledgeCatalogSha256", "manifest", "manifestSha256",
            "modelId", "modelPurpose", "modelSchema", "parameterSha256", "releaseCreatedAt", "releaseId",
            "trainedAt", "trainingProvenanceSha256"
          ]) and
        (.database.recommendationModel as $binding |
          ($binding.releaseId | type == "number" and floor == . and . > 0) and
          ($binding.headRevision | type == "number" and floor == . and . > 0) and
          ($binding.modelId | type == "string" and test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
          $binding.artifactSha256 == $releaseModelSHA256 and
          $binding.artifactSizeBytes == $installedModelSize and
          $binding.artifactMode == 420 and
          $binding.modelSchema == $model[0].schema and
          $binding.modelPurpose == $releasePurpose and $releasePurpose == "production" and
          $binding.modelPurpose == $model[0].manifest.purpose and
          $binding.algorithm == $model[0].manifest.algorithm and
          $binding.inferenceContract == $model[0].manifest.inferenceContract and
          $binding.trainedAt == $model[0].manifest.trainedAt and
          ($binding.trainedAt | utc_timestamp) and
          $binding.trainingProvenanceSha256 == $model[0].manifest.trainingProvenanceSha256 and
          $binding.featureSchemaSha256 == $model[0].manifest.featureSchemaSha256 and
          $binding.knowledgeCatalogSha256 == $model[0].manifest.knowledgeCatalogSha256 and
          $binding.parameterSha256 == $model[0].manifest.parameterSha256 and
          $binding.goldenVectorsSha256 == $model[0].manifest.goldenVectorsSha256 and
          ($binding.trainingProvenanceSha256 | sha256) and
          ($binding.featureSchemaSha256 | sha256) and
          ($binding.knowledgeCatalogSha256 | sha256) and
          ($binding.parameterSha256 | sha256) and
          ($binding.goldenVectorsSha256 | sha256) and
          ($binding.manifestSha256 | sha256) and
          ($binding.releaseCreatedAt | utc_timestamp) and
          ($binding.activatedAt | utc_timestamp) and
          ($binding.headUpdatedAt | utc_timestamp) and
          $binding.applicationVersion == $releaseVersion and
          $binding.applicationCommit == $releaseCommit and
          $binding.applicationBuildTime == $releaseBuildTime and
          ($binding.manifest | type == "object" and keys == [
            "actorFeatureIds", "algorithm", "featureSchemaSha256", "goldenVectorsSha256",
            "inferenceContract", "knowledgeCatalogSha256", "knowledgePointIds", "modelId",
            "parameterSha256", "problemFeatureIds", "purpose", "schema", "trainedAt",
            "trainingProvenanceSha256"
          ]) and
          $binding.manifest == ($model[0] | {
            schema: .schema,
            modelId: .manifest.modelId,
            purpose: .manifest.purpose,
            trainedAt: .manifest.trainedAt,
            algorithm: .manifest.algorithm,
            inferenceContract: .manifest.inferenceContract,
            trainingProvenanceSha256: .manifest.trainingProvenanceSha256,
            featureSchemaSha256: .manifest.featureSchemaSha256,
            knowledgeCatalogSha256: .manifest.knowledgeCatalogSha256,
            parameterSha256: .manifest.parameterSha256,
            goldenVectorsSha256: .manifest.goldenVectorsSha256,
            actorFeatureIds: .manifest.actorFeatureIds,
            problemFeatureIds: .manifest.problemFeatureIds,
            knowledgePointIds: .manifest.knowledgePointIds
          }) and
          $binding.manifestSha256 == $installedModelManifestSHA256 and
          ($evidence[0] as $proof |
            ($proof | type == "object" and keys == [
              "artifactCount", "backupId", "catalogReceiptCount", "catalogReceiptRoot", "databaseName", "level", "manifestSHA256",
              "modelApplicationBuildTime", "modelApplicationCommit", "modelApplicationVersion",
              "modelArtifactSHA256", "modelFeatureSchemaSHA256", "modelHeadRevision", "modelId",
              "modelKnowledgeCatalogSHA256", "modelManifestSHA256", "modelPurpose", "msg", "releaseCommit",
              "releaseVersion", "time"
            ]) and
            $proof.level == "INFO" and $proof.msg == "backup restore verified" and
            $proof.backupId == $backupId and $proof.manifestSHA256 == $manifestSHA256 and
            $proof.databaseName == "ascendany_v2_restore_verify" and
            $proof.catalogReceiptRoot == $catalogReceiptRoot and
            $proof.releaseCommit == $releaseCommit and $proof.releaseVersion == $releaseVersion and
            $proof.artifactCount == $bundle.artifacts.count and
            $proof.catalogReceiptCount == $bundle.catalogPublicationReceipts.count and
            $proof.catalogReceiptCount == ($bundle.database.knowledgeCatalogPublicationIds | length) and
            $proof.catalogReceiptCount == ($bundle.database.knowledgeCatalogPublications | length) and
            $proof.modelId == $binding.modelId and
            $proof.modelArtifactSHA256 == $binding.artifactSha256 and
            $proof.modelPurpose == $binding.modelPurpose and
            $proof.modelHeadRevision == $binding.headRevision and
            $proof.modelApplicationVersion == $binding.applicationVersion and
            $proof.modelApplicationCommit == $binding.applicationCommit and
            $proof.modelApplicationBuildTime == $binding.applicationBuildTime and
            $proof.modelFeatureSchemaSHA256 == $binding.featureSchemaSha256 and
            $proof.modelKnowledgeCatalogSHA256 == $binding.knowledgeCatalogSha256 and
            $proof.modelManifestSHA256 == $binding.manifestSha256 and
            ($proof.time | utc_timestamp)
          )
        )
      ' "$manifest_path" >/dev/null 2>&1; then
    fail "latest backup and restore evidence do not bind the complete current recommendation model provenance"
    return 1
  fi
  pass "latest backup and restore evidence bind the complete current recommendation model provenance"
}

check_retained_backup_model_provenance() {
  local backup_id="$1" manifest_path="$2" evidence_path="$3"
  local actual_manifest_sha expected live
  actual_manifest_sha="$(sha256sum -- "$manifest_path" 2>/dev/null | awk '{print $1}')"
  if [[ ! "$actual_manifest_sha" =~ ^[0-9a-f]{64}$ ]]; then
    fail "retained backup manifest has no canonical SHA-256"
    return 1
  fi
  if ! expected="$(jq -er \
      --arg backupId "$backup_id" \
      --arg manifestSHA256 "$actual_manifest_sha" \
      --arg catalogReceiptRoot "$restore_catalog_receipt_root" \
      --slurpfile evidence "$evidence_path" '
        def sha256: type == "string" and test("^[0-9a-f]{64}$");
        def utc_timestamp:
          type == "string" and
          test("^[0-9]{4}-(0[1-9]|1[0-2])-([0-2][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\\.[0-9]{1,9})?Z$");
        . as $bundle |
        $bundle.database.recommendationModel as $model |
        $evidence[0] as $proof |
        select(
          ($evidence | length == 1) and
          ($bundle | type == "object" and keys == ["artifacts", "backupId", "catalogPublicationReceipts", "createdAt", "database", "schema"]) and
          $bundle.schema == "ascendany.backup.bundle.v2" and $bundle.backupId == $backupId and
          ($bundle.createdAt | utc_timestamp) and
          ($bundle.database | type == "object" and keys == [
            "databaseName", "file", "knowledgeCatalogPublicationIds",
            "knowledgeCatalogPublications", "migrations", "recommendationModel"
          ]) and
          $bundle.database.databaseName == "ascendany_v2" and
	          ($bundle.database.migrations | type == "array" and length == 10 and .[-1].version == 10) and
          ($bundle.catalogPublicationReceipts | type == "object" and
            keys == ["count", "entries", "file", "totalBytes"] and
            (.count | type == "number" and floor == . and . > 0) and
            (.totalBytes | type == "number" and floor == . and . > 0) and
            (.file | type == "object" and keys == ["filename", "format", "sha256", "sizeBytes"] and
              .filename == "catalog-receipts.tar.zst" and .format == "tar+zstd" and
              (.sha256 | sha256) and (.sizeBytes | type == "number" and floor == . and . > 0)) and
            (.entries | type == "array" and length == $bundle.catalogPublicationReceipts.count) and
            ([.entries[].publicationId] == $bundle.database.knowledgeCatalogPublicationIds) and
            ([.entries[].publicationId] == [$bundle.database.knowledgeCatalogPublications[].knowledgeCatalogPublicationId])) and
          ($model | type == "object" and keys == [
            "activatedAt", "algorithm", "applicationBuildTime", "applicationCommit",
            "applicationVersion", "artifactMode", "artifactSha256", "artifactSizeBytes",
            "featureSchemaSha256", "goldenVectorsSha256", "headRevision", "headUpdatedAt",
            "inferenceContract", "knowledgeCatalogSha256", "manifest", "manifestSha256",
            "modelId", "modelPurpose", "modelSchema", "parameterSha256", "releaseCreatedAt", "releaseId",
            "trainedAt", "trainingProvenanceSha256"
          ]) and
          ($model.releaseId | type == "number" and floor == . and . > 0) and
          ($model.headRevision | type == "number" and floor == . and . > 0) and
          ($model.modelId | type == "string" and test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
          ($model.artifactSha256 | sha256) and
          ($model.artifactSizeBytes | type == "number" and floor == . and . > 0 and . <= 16777216) and
          $model.artifactMode == 420 and
          $model.modelSchema == "ascendany.recommendation.inference-model.v1" and
          $model.modelPurpose == "production" and
          $model.algorithm == "knowledge_mirt_feature_v1" and
          $model.inferenceContract == "ascendany.recommendation.inference.v1" and
          ($model.trainingProvenanceSha256 | sha256) and
          ($model.featureSchemaSha256 | sha256) and
          ($model.knowledgeCatalogSha256 | sha256) and
          ($model.parameterSha256 | sha256) and
          ($model.goldenVectorsSha256 | sha256) and
          ($model.manifestSha256 | sha256) and
          ($model.applicationVersion | type == "string" and length > 0 and length <= 128) and
          ($model.applicationCommit | type == "string" and test("^[0-9a-f]{40}$")) and
          ($model.applicationBuildTime | utc_timestamp) and
          ($model.trainedAt | utc_timestamp) and
          ($model.releaseCreatedAt | utc_timestamp) and
          ($model.activatedAt | utc_timestamp) and
          ($model.headUpdatedAt | utc_timestamp) and
          ($model.manifest | type == "object" and
            .schema == $model.modelSchema and
            .modelId == $model.modelId and
            .purpose == $model.modelPurpose and
            .trainedAt == $model.trainedAt and
            .algorithm == $model.algorithm and
            .inferenceContract == $model.inferenceContract and
            .trainingProvenanceSha256 == $model.trainingProvenanceSha256 and
            .featureSchemaSha256 == $model.featureSchemaSha256 and
            .knowledgeCatalogSha256 == $model.knowledgeCatalogSha256 and
            .parameterSha256 == $model.parameterSha256 and
            .goldenVectorsSha256 == $model.goldenVectorsSha256) and
          ($proof | type == "object" and keys == [
            "artifactCount", "backupId", "catalogReceiptCount", "catalogReceiptRoot", "databaseName", "level", "manifestSHA256",
            "modelApplicationBuildTime", "modelApplicationCommit", "modelApplicationVersion",
            "modelArtifactSHA256", "modelFeatureSchemaSHA256", "modelHeadRevision", "modelId",
            "modelKnowledgeCatalogSHA256", "modelManifestSHA256", "modelPurpose", "msg", "releaseCommit",
            "releaseVersion", "time"
          ]) and
          $proof.level == "INFO" and $proof.msg == "backup restore verified" and
          $proof.backupId == $backupId and $proof.manifestSHA256 == $manifestSHA256 and
          $proof.databaseName == "ascendany_v2_restore_verify" and
          $proof.catalogReceiptRoot == $catalogReceiptRoot and
          $proof.releaseCommit == $model.applicationCommit and
          $proof.releaseVersion == $model.applicationVersion and
          $proof.modelId == $model.modelId and
          $proof.modelPurpose == $model.modelPurpose and
          $proof.modelArtifactSHA256 == $model.artifactSha256 and
          $proof.modelHeadRevision == $model.headRevision and
          $proof.modelApplicationVersion == $model.applicationVersion and
          $proof.modelApplicationCommit == $model.applicationCommit and
          $proof.modelApplicationBuildTime == $model.applicationBuildTime and
          $proof.modelFeatureSchemaSHA256 == $model.featureSchemaSha256 and
          $proof.modelKnowledgeCatalogSHA256 == $model.knowledgeCatalogSha256 and
          $proof.modelManifestSHA256 == $model.manifestSha256 and
          $proof.artifactCount == $bundle.artifacts.count and
          $proof.catalogReceiptCount == $bundle.catalogPublicationReceipts.count and
          $proof.catalogReceiptCount == ($bundle.database.knowledgeCatalogPublicationIds | length) and
          $proof.catalogReceiptCount == ($bundle.database.knowledgeCatalogPublications | length) and
          ($proof.time | utc_timestamp)
        ) |
        [
          $model.modelId, $model.artifactSha256, ($model.headRevision | tostring),
          $model.applicationVersion, $model.applicationCommit, $model.applicationBuildTime,
          $model.knowledgeCatalogSha256, $model.manifestSha256
        ] | join("|")
      ' "$manifest_path" 2>/dev/null)"; then
    fail "retained backup and restore evidence violate the closed prior-production provenance contract"
    return 1
  fi
  if ! live="$(run_runtime_psql -A -t -F '|' -v ON_ERROR_STOP=1 <<'SQL'
SELECT model.model_id::text,
       model.artifact_sha256,
       head.head_revision,
       event.application_version,
       event.application_commit,
       event.application_build_time,
       model.knowledge_catalog_sha256,
       model.manifest_sha256
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS model
  ON model.recommendation_model_release_id = head.current_release_id
JOIN ascendany.recommendation_model_activation_events AS event
  ON event.head_revision = head.head_revision
 AND event.recommendation_model_release_id = head.current_release_id
 AND event.artifact_sha256 = model.artifact_sha256
WHERE head.singleton
SQL
  )"; then
    fail "retained backup provenance cannot read the live prior model head"
    return 1
  fi
  if [[ "$live" != "$expected" ]]; then
    fail "retained backup and restore evidence differ from the live prior model head"
    return 1
  fi
  pass "retained backup and restore evidence bind the live prior model head"
}

check_backup_schedule() {
  local timer="ascendany-backup.timer"
  local latest_backup latest_manifest evidence_time bundle_entries
  local evidence_epoch now_epoch evidence_parent
  local next_elapse next_elapse_epoch scratch_database_count restore_state provenance_valid=0
  local backup_binary="$release_root/bin/ascendany-backup"
  local restore_lock_directory="/run/ascendany-restore-operator"

  if [[ "$(unit_property "$timer" LoadState || true)" != "loaded" ]]; then
    fail "$timer is not loaded"
    return
  fi
  if production_phase; then
    if ! systemctl is-enabled --quiet "$timer"; then
      fail "$timer is not enabled"
    elif [[ "$(unit_property "$timer" ActiveState || true)" != "active" ]]; then
      fail "$timer is not active"
    else
      pass "$timer is enabled and active"
    fi
    now_epoch="$(date -u +%s)"
    next_elapse="$(unit_property "$timer" NextElapseUSecRealtime || true)"
    next_elapse_epoch="$(date -u -d "$next_elapse" +%s 2>/dev/null || true)"
    if [[ -z "$next_elapse_epoch" || "$next_elapse_epoch" -le "$now_epoch" ]]; then
      fail "$timer has no valid future realtime elapse"
    else
      pass "$timer has a valid future realtime elapse"
    fi
  fi

  if [[ ! -d "$backup_root" || -L "$backup_root" ||
        "$(stat -Lc '%U:%G:%a' "$backup_root" 2>/dev/null || true)" != "ascendany-backup:ascendany-backup-readers:750" ]]; then
    fail "backup root must be a real ascendany-backup:ascendany-backup-readers mode 0750 directory"
    return
  fi
  if find "$backup_root" -mindepth 1 -maxdepth 1 \
       \( -name '.incoming-backup-*' -o -name '*.pgpass' -o -name '.pgpass' \) \
       -print -quit | grep -q .; then
    fail "backup root retains an unpublished staging tree or plaintext pgpass"
  else
    pass "backup root contains no unpublished staging tree or plaintext pgpass"
  fi
  latest_backup="$(find "$backup_root" -mindepth 1 -maxdepth 1 -type d -name 'backup-*' -printf '%f\n' | LC_ALL=C sort | tail -n 1)"
  if [[ -z "$latest_backup" ]]; then
    fail "backup root contains no published bundle"
    return
  fi
  latest_manifest="$backup_root/$latest_backup/manifest.json"
  if ! bundle_entries="$(find "$backup_root/$latest_backup" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' 2>/dev/null | LC_ALL=C sort)" ||
     [[ "$bundle_entries" != $'artifacts.tar.zst|f\ncatalog-receipts.tar.zst|f\ndatabase.dump|f\nmanifest.json|f\nmanifest.sha256|f' ]]; then
    fail "latest backup bundle differs from the exact five-file publication contract"
    return
  fi
  if [[ ! -d "$backup_root/$latest_backup" || -L "$backup_root/$latest_backup" ||
        "$(stat -Lc '%U:%G:%a' "$backup_root/$latest_backup" 2>/dev/null || true)" != "ascendany-backup:ascendany-backup-readers:750" ]] ||
     find "$backup_root/$latest_backup" -mindepth 1 -maxdepth 1 \
       \( ! -type f -o ! -user ascendany-backup -o ! -group ascendany-backup-readers -o ! -perm 0640 -o ! -links 1 \) \
       -print -quit | grep -q .; then
    fail "latest backup bundle violates the 0750/0640 reader-group publication contract"
  elif ! runuser -u ascendany-restore -- test -r "$latest_manifest"; then
    fail "restore verifier identity cannot read the published backup bundle"
  elif ! jq -e --arg id "$latest_backup" '
      .schema == "ascendany.backup.bundle.v2" and
      .backupId == $id and
      .database.databaseName == "ascendany_v2" and
      (.database.migrations | length == 10 and .[-1].version == 10)
    ' "$latest_manifest" >/dev/null 2>&1; then
    fail "latest backup manifest is missing, malformed, or not schema v10: $latest_backup"
  elif ! runuser -u ascendany-backup -- env -i \
      PATH=/usr/bin:/bin \
      ASCENDANY_BACKUP_ROOT="$backup_root" \
      ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_and_catalog_receipt_tar_zstd \
      ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
      ASCENDANY_BACKUP_COMMAND_TIMEOUT=2h \
      ASCENDANY_PG_DUMP_PATH=/usr/bin/pg_dump \
      ASCENDANY_PG_RESTORE_PATH=/usr/bin/pg_restore \
      ASCENDANY_ZSTD_PATH=/usr/bin/zstd \
      "$backup_binary" verify "$latest_backup" >/dev/null 2>&1; then
    fail "latest backup bundle failed live verification: $latest_backup"
  else
    pass "latest schema-v10 backup passed live verification: $latest_backup"
  fi
  evidence_parent="$(dirname -- "$restore_evidence")"
  if [[ "$restore_evidence" != /* || "$restore_evidence" != "$(realpath -m -- "$restore_evidence")" ||
        ! -f "$restore_evidence" || -L "$restore_evidence" ||
        "$restore_evidence" != "$(realpath -e -- "$restore_evidence" 2>/dev/null || true)" ||
        "$(stat -Lc '%u:%g:%a' "$restore_evidence" 2>/dev/null || true)" != "0:0:600" ||
        ! -d "$evidence_parent" || -L "$evidence_parent" ||
        "$(stat -Lc '%u:%g:%a' "$evidence_parent" 2>/dev/null || true)" != "0:0:700" ]] ||
     ! check_root_owned_ancestry "$restore_evidence" 1; then
    fail "restore verification evidence must be a canonical root:root 0600 file in a root:root 0700 directory"
    return
  fi
  if forward_preactivation_phase; then
    check_retained_backup_model_provenance "$latest_backup" "$latest_manifest" "$restore_evidence" &&
      provenance_valid=1
  else
    check_backup_model_provenance "$latest_backup" "$latest_manifest" "$restore_evidence" &&
      provenance_valid=1
  fi
  if (( provenance_valid == 1 )); then
    evidence_time="$(jq -er '.time' "$restore_evidence" 2>/dev/null || true)"
    evidence_epoch="$(date -u -d "$evidence_time" +%s 2>/dev/null || true)"
    now_epoch="$(date -u +%s)"
    if [[ -z "$evidence_epoch" || "$evidence_epoch" -gt "$now_epoch" || $((now_epoch - evidence_epoch)) -gt 2678400 ]]; then
      fail "restore verification evidence is invalid or older than 31 days"
    else
      pass "recent destructive restore verification evidence binds the latest schema-v10 backup"
    fi
  fi

  restore_state="$(unit_property ascendany-restore-verify@validation.service ActiveState || true)"
  if [[ "$restore_state" != "inactive" ||
        "$(systemctl is-enabled ascendany-restore-verify@.service 2>/dev/null || true)" != "static" ]]; then
    fail "restore verification template must remain inactive and static outside an operator run"
  fi
  if [[ ! -d /var/lib/ascendany-restore || -L /var/lib/ascendany-restore ||
        "$(stat -Lc '%U:%G:%a' /var/lib/ascendany-restore 2>/dev/null || true)" != "ascendany-restore:ascendany-restore:700" ]] ||
     find /var/lib/ascendany-restore -mindepth 1 -maxdepth 1 \
       \( -name 'artifacts' -o -name '.restore-*' -o -name '*.pgpass' -o \
          -name 'restore-verify.*.pending.json' -o -name '.restore-verify.*.pending.tmp' -o \
          -name 'restore-verify.log' \) \
       -print -quit | grep -q .; then
    fail "restore verifier left scratch artifacts or private credentials behind"
  else
    pass "restore verifier left no scratch artifact or credential state"
  fi
  if [[ ! -d "$restore_lock_directory" || -L "$restore_lock_directory" ||
        "$(stat -Lc '%U:%G:%a' "$restore_lock_directory" 2>/dev/null || true)" != "root:ascendany-restore:750" ||
        ! -f "$restore_lock_directory/operator.lock" || -L "$restore_lock_directory/operator.lock" ||
        "$(stat -Lc '%U:%G:%a:%h' "$restore_lock_directory/operator.lock" 2>/dev/null || true)" != "ascendany-restore:ascendany-restore:600:1" ||
        ! -f "$restore_lock_directory/publication.lock" || -L "$restore_lock_directory/publication.lock" ||
        "$(stat -Lc '%U:%G:%a:%h' "$restore_lock_directory/publication.lock" 2>/dev/null || true)" != "root:root:600:1" ]]; then
    fail "restore verifier stable operator/publication lock inodes violate the tmpfiles contract"
  elif find /run -mindepth 1 -maxdepth 1 -type d -name 'ascendany-restore-verify-*' -print -quit | grep -q .; then
    fail "inactive restore verifier retains a per-instance RuntimeDirectory"
  else
    pass "restore locks have stable tmpfiles-owned inodes and no instance runtime remains"
  fi
  scratch_database_count="$(run_runtime_psql -A -t -v ON_ERROR_STOP=1 -c \
    "SELECT count(*) FROM pg_database WHERE datname = 'ascendany_v2_restore_verify'" 2>/dev/null || true)"
  if [[ "$scratch_database_count" != "0" ]]; then
    fail "restore verifier left its scratch database behind"
  else
    pass "restore verifier removed its scratch database"
  fi
}

check_artifact_root() {
  local path expected_mode actual_mode actual_owner actual_group groups
  if is_under "$artifact_root" "$release_root"; then
    fail "artifact root is inside the release root: $artifact_root"
    return
  fi
  if [[ ! -d "$artifact_root" || -L "$artifact_root" ]]; then
    fail "artifact root is missing: $artifact_root"
    return
  fi
  for path in "$artifact_root" "$artifact_root/sha256"; do
    expected_mode="750"
    if [[ ! -d "$path" || -L "$path" ]]; then
      fail "published artifact directory is missing: $path"
      continue
    fi
    actual_mode="$(stat -c '%a' "$path")"
    actual_owner="$(stat -c '%U' "$path")"
    actual_group="$(stat -c '%G' "$path")"
    if [[ "$actual_mode" != "$expected_mode" || "$actual_owner" != "ascendany" || "$actual_group" != "ascendany" ]]; then
      fail "$path must be mode 0750 and owned by ascendany:ascendany"
    fi
  done
  for path in "$artifact_root/incoming" "$artifact_root/.locks"; do
    if [[ ! -d "$path" || -L "$path" ||
          "$(stat -c '%U:%G:%a' "$path" 2>/dev/null || true)" != "ascendany:ascendany:700" ]]; then
      fail "$path must be a real ascendany:ascendany mode 0700 private directory"
    fi
  done
  groups="$(id -nG ascendany-backup 2>/dev/null || true)"
  if [[ " $groups " != *" ascendany "* ]]; then
    fail "ascendany-backup is not a member of the published-artifact read group"
  elif ! runuser -u ascendany-backup -- test -x "$artifact_root/sha256"; then
    fail "ascendany-backup cannot traverse the published artifact tree"
  elif runuser -u ascendany-backup -- test -x "$artifact_root/incoming" || runuser -u ascendany-backup -- test -x "$artifact_root/.locks"; then
    fail "ascendany-backup can traverse private upload or lock state"
  else
    pass "backup can read only the published artifact namespace"
  fi
  if find "$artifact_root/sha256" -mindepth 1 ! -type d ! -type f -print -quit | grep -q .; then
    fail "published artifact tree contains a symbolic link or special node"
  elif find "$artifact_root/sha256" -type f \
      \( ! -perm 0640 -o ! -user ascendany -o ! -group ascendany \) -print -quit | grep -q .; then
    fail "published artifact tree contains a file whose mode is not 0640"
  elif find "$artifact_root/sha256" -type f ! -links 1 -print -quit | grep -q .; then
    fail "published artifact tree contains a multiply-linked file"
  elif find "$artifact_root/sha256" -type d \
      \( ! -perm 0750 -o ! -user ascendany -o ! -group ascendany \) -print -quit | grep -q .; then
    fail "published artifact tree contains a directory with invalid mode or ownership"
  else
    pass "published artifact modes and ownership are exact"
  fi
  pass "artifact root is durable and external to the release: $artifact_root"
}

check_catalog_publisher_state_root() {
  check_exact_directory_entry_set \
    "$catalog_publisher_state_root" \
    ascendany-catalog-publisher:ascendany-catalog-readers:750 \
    'catalog publisher durable state root' \
    $'pending|d\nreceipts|d'
  if [[ ! -d "$catalog_receipt_root" || -L "$catalog_receipt_root" ||
        "$(stat -Lc '%U:%G:%a' -- "$catalog_receipt_root" 2>/dev/null || true)" != ascendany-catalog-publisher:ascendany-catalog-readers:750 ]]; then
    fail "catalog publisher receipt root must be a real publisher-owned reader-group mode 0750 directory"
  else
    pass "catalog publisher owns one isolated mode 0750 backup-readable receipt namespace"
  fi
  if [[ ! -d "$catalog_publisher_pending_root" || -L "$catalog_publisher_pending_root" ||
        "$(stat -Lc '%U:%G:%a' -- "$catalog_publisher_pending_root" 2>/dev/null || true)" != root:root:700 ]]; then
    fail "catalog publisher pending input root must be a real root-owned mode 0700 directory"
  elif [[ -e "$catalog_publication_request_source" || -L "$catalog_publication_request_source" ||
          -e "$catalog_publication_access_token_source" || -L "$catalog_publication_access_token_source" ]]; then
    fail "catalog publisher pending request or access token remains outside the operator window"
  else
    pass "catalog publisher pending input root is empty outside the operator window"
  fi
}

check_catalog_publisher_capabilities() {
  local publisher_groups backup_groups reader_group members path unreadable=0
  publisher_groups="$(id -nG ascendany-catalog-publisher 2>/dev/null | normalize_word_set)"
  backup_groups="$(id -nG ascendany-backup 2>/dev/null | normalize_word_set)"
  if [[ "$publisher_groups" != ascendany-catalog-publisher ]]; then
    fail "catalog publisher must have no supplementary group capability"
  else
    pass "catalog publisher has only its primary capability group"
  fi
  if [[ "$backup_groups" != "$(printf '%s\n' \
      ascendany \
      ascendany-backup \
      ascendany-backup-readers \
      ascendany-catalog-readers | LC_ALL=C sort)" ]]; then
    fail "backup identity supplementary groups differ from the exact artifact/catalog reader contract"
  else
    pass "backup identity has the exact artifact and catalog reader capabilities"
  fi
  reader_group="$(getent group ascendany-catalog-readers 2>/dev/null || true)"
  members="${reader_group##*:}"
  if [[ "$reader_group" != ascendany-catalog-readers:* || "$members" != ascendany-backup ]]; then
    fail "catalog backup reader group must contain only ascendany-backup"
  else
    pass "catalog backup reader group has one exact member"
  fi
  if ! runuser -u ascendany-catalog-publisher -- test -w "$catalog_receipt_root" ||
     ! runuser -u ascendany-backup -- test -x "$catalog_publisher_state_root" ||
     ! runuser -u ascendany-backup -- test -x "$catalog_receipt_root" ||
     runuser -u ascendany-backup -- test -w "$catalog_receipt_root" ||
     runuser -u ascendany-catalog-publisher -- test -x "$catalog_publisher_pending_root" ||
     runuser -u ascendany-backup -- test -x "$catalog_publisher_pending_root" ||
     runuser -u ascendany -- test -x "$catalog_publisher_state_root" ||
     runuser -u ascendany-restore -- test -x "$catalog_publisher_state_root"; then
    fail "catalog publisher, backup reader, runtime, or restore live-state capability differs from the contract"
    return
  fi
  while IFS= read -r path; do
    if ! runuser -u ascendany-backup -- test -r "$path" ||
       runuser -u ascendany-backup -- test -w "$path"; then
      unreadable=1
    fi
  done < <(find "$catalog_receipt_root" -mindepth 1 -maxdepth 1 -type f -print | LC_ALL=C sort)
  if (( unreadable != 0 )); then
    fail "backup identity lacks read-only access to an immutable catalog receipt"
  else
    pass "backup has read-only access to every immutable catalog receipt"
  fi
}

check_initial_empty_durable_state() {
  initial_transition && [[ "$validation_phase" == staged || "$validation_phase" == smoke ||
    "$validation_phase" == activation ]] || return 0
  local failures_before="$failures" path metadata entry
  for path in "$artifact_root/sha256" "$artifact_root/incoming" "$artifact_root/.locks"; do
    if [[ ! -d "$path" || -L "$path" ]]; then
      fail "initial durable namespace is missing a real directory: $path"
    elif ! entry="$(find "$path" -mindepth 1 -print -quit)"; then
      fail "initial durable namespace cannot be enumerated: $path"
    elif [[ -n "$entry" ]]; then
      fail "initial pre-catalog artifact namespace is not empty: $path"
    fi
  done

  metadata="$(stat -Lc '%U:%G:%a' -- "$catalog_receipt_root" 2>/dev/null || true)"
  if [[ ! -d "$catalog_receipt_root" || -L "$catalog_receipt_root" ||
        "$metadata" != ascendany-catalog-publisher:ascendany-catalog-readers:750 ]]; then
    fail "initial catalog publication receipt root must be a real publisher-owned reader-group mode 0750 directory"
  elif ! entry="$(find "$catalog_receipt_root" -mindepth 1 -print -quit)"; then
    fail "initial catalog publication receipt root cannot be enumerated"
  elif [[ -n "$entry" ]]; then
    fail "initial pre-catalog publication receipt root must be empty"
  fi

  metadata="$(stat -Lc '%U:%G:%a' -- "$backup_root" 2>/dev/null || true)"
  if [[ ! -d "$backup_root" || -L "$backup_root" ||
        "$metadata" != ascendany-backup:ascendany-backup-readers:750 ]]; then
    fail "initial backup root must be a real ascendany-backup:ascendany-backup-readers mode 0750 directory"
  elif ! entry="$(find "$backup_root" -mindepth 1 -print -quit)"; then
    fail "initial backup root cannot be enumerated"
  elif [[ -n "$entry" ]]; then
    fail "initial pre-catalog backup root must be completely empty"
  fi

  metadata="$(stat -Lc '%U:%G:%a' -- "$acceptance_root" 2>/dev/null || true)"
  if [[ ! -d "$acceptance_root" || -L "$acceptance_root" || "$metadata" != root:root:700 ]]; then
    fail "initial acceptance root must be a real root:root mode 0700 directory"
  elif ! entry="$(find "$acceptance_root" -mindepth 1 -print -quit)"; then
    fail "initial acceptance root cannot be enumerated"
  elif [[ -n "$entry" ]]; then
    fail "initial pre-catalog acceptance root must contain no restore or prior-production evidence"
  elif [[ -e "$restore_evidence" || -L "$restore_evidence" ]]; then
    fail "initial pre-catalog restore verification evidence must be absent"
  fi

  if (( failures == failures_before )); then
    pass "initial pre-catalog artifact, catalog receipt, backup, and restore-evidence namespaces are canonical and empty"
  fi
}

run_runtime_psql() {
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    PGHOST="$runtime_pg_host" \
    PGPORT="$runtime_pg_port" \
    PGDATABASE="$runtime_pg_database" \
    PGUSER="$expected_db_user" \
    PGCONNECT_TIMEOUT="$runtime_pg_connect_timeout" \
    PGPASSFILE="$PGPASSFILE" \
    /usr/bin/psql -X "$@"
}

run_runtime_psql_with_variables() {
  local argument_count="$#"
  local command_index command
  local -a arguments=("$@")
  if (( argument_count < 2 )); then
    return 2
  fi
  command_index=$((argument_count - 2))
  if [[ "${arguments[$command_index]}" != -c ]]; then
    return 2
  fi
  command="${arguments[$((argument_count - 1))]}"
  unset 'arguments[command_index]' 'arguments[argument_count - 1]'
  printf '%s\n' "$command" | run_runtime_psql "${arguments[@]}"
}

check_admin_bootstrap_database() {
  local result admin_count active_admin_count canonical_admin_count
  local bootstrap_audit_count canonical_bootstrap_audit_count
  if [[ -z "${PGPASSFILE:-}" ]]; then
    fail "administrator bootstrap verification has no authenticated database channel"
    return
  fi
  if ! result="$(
    run_runtime_psql -A -t -F '|' -v ON_ERROR_STOP=1 -c "
SELECT
  count(*) FILTER (WHERE account.role = 'admin'),
  count(*) FILTER (WHERE account.role = 'admin' AND account.disabled_at IS NULL),
  count(*) FILTER (
    WHERE account.role = 'admin'
      AND account.username = 'admin'
      AND account.display_name = 'admin'
      AND account.actor_id IS NULL
      AND account.student_number IS NULL
      AND account.auth_revision = 1
      AND account.disabled_at IS NULL
      AND account.password_phc LIKE '\$argon2id\$%'
      AND account.created_at = account.updated_at
  ),
  (SELECT count(*) FROM ascendany.audit_events WHERE event_type = 'auth.admin_bootstrap'),
  (SELECT count(*)
   FROM ascendany.audit_events AS audit
   JOIN ascendany.auth_accounts AS bootstrap_account
     ON bootstrap_account.account_id = audit.account_id
   WHERE audit.event_type = 'auth.admin_bootstrap'
     AND audit.session_id IS NULL
     AND audit.payload = '{}'::jsonb
     AND audit.occurred_at = bootstrap_account.created_at
     AND bootstrap_account.role = 'admin'
     AND bootstrap_account.username = 'admin')
FROM ascendany.auth_accounts AS account"
  )"; then
    fail "administrator bootstrap database evidence query failed"
    return
  fi
  IFS='|' read -r admin_count active_admin_count canonical_admin_count \
    bootstrap_audit_count canonical_bootstrap_audit_count <<<"$result"
  if [[ ! "$admin_count" =~ ^[0-9]+$ || ! "$active_admin_count" =~ ^[0-9]+$ ||
        ! "$canonical_admin_count" =~ ^[0-9]+$ || ! "$bootstrap_audit_count" =~ ^[0-9]+$ ||
        ! "$canonical_bootstrap_audit_count" =~ ^[0-9]+$ ]]; then
    fail "administrator bootstrap database evidence is noncanonical"
    return
  fi
  if initial_transition && [[ "$validation_phase" == staged || "$validation_phase" == smoke ||
      "$validation_phase" == activation ]]; then
    if [[ "$admin_count:$active_admin_count:$canonical_admin_count:$bootstrap_audit_count:$canonical_bootstrap_audit_count" != "0:0:0:0:0" ]]; then
      fail "initial preactivation database must contain no administrator or bootstrap audit"
    else
      pass "initial preactivation database contains no administrator before bootstrap"
    fi
  elif (( admin_count < 1 || active_admin_count < 1 )) ||
       [[ "$canonical_admin_count:$bootstrap_audit_count:$canonical_bootstrap_audit_count" != "1:1:1" ]]; then
    fail "$deployment_transition $validation_phase database lacks the canonical active bootstrap administrator or exact bootstrap audit"
  else
    pass "$deployment_transition $validation_phase database retains the canonical active bootstrap administrator and exact audit"
  fi
}

check_database_role() {
  local result db_user superuser createdb createrole replication bypassrls owner_member
  local runtime_v2_connect
  local credential_file roles_verifier

  if [[ "$release_payload_verified" != "1" ]]; then
    fail "database role verification requires an intact release payload"
    return
  fi

  if [[ "$validation_phase" == "staged" || "$validation_phase" == "catalog" ||
        "$validation_phase" == "activation" ]]; then
    if [[ -z "${PGPASSFILE:-}" || ! -f "$PGPASSFILE" || -L "$PGPASSFILE" ||
          "$PGPASSFILE" != "$(realpath -m -- "$PGPASSFILE" 2>/dev/null || true)" ||
          "$PGPASSFILE" != "$(realpath -e -- "$PGPASSFILE" 2>/dev/null || true)" ||
          "$(stat -Lc '%u:%g:%a:%h' "$PGPASSFILE" 2>/dev/null || true)" != "0:0:600:1" ]] ||
       ! check_root_owned_ancestry "$PGPASSFILE" 1; then
      fail "$validation_phase phase requires an explicit canonical root:root 0600 single-link operator PGPASSFILE"
      return
    fi
    pass "$validation_phase phase uses an explicit protected operator PGPASSFILE"
  fi

  credential_file="/run/credentials/ascendanyd.service/db_password"
  if [[ -z "${PGPASSFILE:-}" ]]; then
    if [[ ! -s "$credential_file" ]]; then
      fail "$validation_phase phase has no readable runtime DB credential for role validation"
      return
    fi
    temporary_pgpass="$(mktemp /run/ascendany-validate-pgpass.XXXXXX)"
    chmod 0600 "$temporary_pgpass"
    if ! /usr/bin/awk \
        -v host="$runtime_pg_host" \
        -v port="$runtime_pg_port" \
        -v database="$runtime_pg_database" \
        -v user="$expected_db_user" '
          NR == 1 {
            password = $0
            gsub(/\\/, "\\\\", password)
            gsub(/:/, "\\:", password)
            printf "%s:%s:%s:%s:%s\n", host, port, database, user, password
            next
          }
          { exit 2 }
          END { if (NR != 1 || length(password) == 0) exit 2 }
        ' "$credential_file" >"$temporary_pgpass"; then
      fail "$validation_phase phase runtime DB credential is not one non-empty line"
      return
    fi
    export PGPASSFILE="$temporary_pgpass"
  fi

  if ! result="$(
    run_runtime_psql -A -t -F '|' -v ON_ERROR_STOP=1 -c \
      "SELECT current_user,
              rolsuper,
              rolcreatedb,
              rolcreaterole,
              rolreplication,
              rolbypassrls,
              pg_has_role(current_user, 'ascendany_owner', 'MEMBER'),
              has_database_privilege(current_user, 'ascendany_v2', 'CONNECT')
       FROM pg_roles
       WHERE rolname = current_user"
  )"; then
    fail "database role query failed on ${runtime_pg_host}:${runtime_pg_port}/${runtime_pg_database}"
    return
  fi
  IFS='|' read -r db_user superuser createdb createrole replication bypassrls owner_member \
    runtime_v2_connect <<<"$result"
  if [[ "$db_user" != "$expected_db_user" ]]; then
    fail "database authenticated as $db_user; expected $expected_db_user"
  elif [[ "$superuser" == "t" || "$createdb" == "t" || "$createrole" == "t" || "$replication" == "t" || "$bypassrls" == "t" ]]; then
    fail "runtime database role owns cluster-level privilege"
  elif [[ "$owner_member" == "t" ]]; then
    fail "runtime database role is a member of ascendany_owner"
  elif [[ "$runtime_v2_connect" != t ]]; then
    fail "runtime database role cannot connect to ascendany_v2"
  else
    pass "runtime database role is non-superuser, outside owner membership, and limited to ascendany_v2"
  fi

  roles_verifier="$release_root/db/roles/verify_v2_roles.sql"
  if ! postgres_admin_psql --dbname=ascendany_v2 <"$roles_verifier" >/dev/null; then
    fail "release-bound v2 database role and grant verification failed"
  else
    pass "release-bound v2 database role and grant contract is exact"
  fi
}

check_agent_acceptance_receipt() {
  production_phase || return 0

  local failures_before="$failures"
  local path="$agent_acceptance_receipt_path" metadata size canonical_receipt
  local receipt_values accepted_at accepted_epoch probe_checked_at probe_epoch now_epoch
  local administrator_account_id student_account_id student_username student_number
  local target_application_version target_application_commit target_application_build_time
  local provider_credential_sha256
  local prompt_configuration_id prompt_head_revision prompt_version_id prompt_version_number
  local prompt_schema_id prompt_document_sha256
  local model_configuration_id model_head_revision model_version_id model_version_number
  local model_schema_id model_document_sha256 model_credential_ref
  local probe_authority probe_model reply_marker database_match
  local reply_run_id reply_thread_id reply_input_message_id reply_output_message_id
  local reply_sha256 reply_event_count
  local auto_run_id auto_thread_id auto_input_message_id auto_output_message_id
  local auto_created auto_sha256 auto_event_count
  local binding variable encoded reference_hex authority_hex binding_reference binding_authority
  local provider_credential_id provider_credential_source provider_source_sha256
  local matching_provider_bindings=0

  metadata="$(stat -Lc '%u:%g:%a:%h' -- "$path" 2>/dev/null || true)"
  if [[ "$path" != /* || "$path" != "$(realpath -m -- "$path" 2>/dev/null || true)" ||
        ! -f "$path" || -L "$path" ||
        "$path" != "$(realpath -e -- "$path" 2>/dev/null || true)" ||
        "$metadata" != 0:0:400:1 ]] || ! check_root_owned_ancestry "$path" 1; then
    fail "Agent acceptance receipt must be one canonical root:root 0400 single-link file with protected root-owned ancestry"
    return
  fi
  size="$(stat -Lc '%s' -- "$path" 2>/dev/null || true)"
  if [[ ! "$size" =~ ^[1-9][0-9]*$ || "$size" -gt 32768 ]]; then
    fail "Agent acceptance receipt violates the nonempty 32768-byte limit"
    return
  fi

  canonical_receipt="$(mktemp)"
  if ! jq -Scs 'if length == 1 then .[0] else empty end' -- "$path" >"$canonical_receipt" 2>/dev/null ||
     ! cmp --silent -- "$path" "$canonical_receipt"; then
    rm -f -- "$canonical_receipt"
    fail "Agent acceptance receipt must contain exactly one canonical JSON object and one trailing newline"
    return
  fi
  rm -f -- "$canonical_receipt"

  if ! jq -e --arg promptDocumentSHA256 "$agent_prompt_document_sha256" \
      --arg targetApplicationVersion "$release_manifest_version" \
      --arg targetApplicationCommit "$release_manifest_commit" \
      --arg targetApplicationBuildTime "$release_manifest_build_time" '
      def safe_positive_integer:
        type == "number" and floor == . and . >= 1 and . <= 9007199254740991;
      def safe_nonnegative_integer:
        type == "number" and floor == . and . >= 0 and . <= 9007199254740991;
      def positive_int64_string:
        type == "string" and test("^[1-9][0-9]{0,18}$") and
        (length < 19 or (length == 19 and . <= "9223372036854775807"));
      def sha256: type == "string" and test("^[0-9a-f]{64}$");
      def uuid_v4:
        type == "string" and test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$");
      def timestamp:
        type == "string" and
        test("^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]([.][0-9]{1,9})?Z$");
      def printable_nonempty($maximum):
        type == "string" and length >= 1 and length <= $maximum and
        all(explode[]; . >= 32 and . != 127);
      def semver:
        type == "string" and length <= 128 and
        test("^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)([.]((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?([+][0-9A-Za-z-]+([.][0-9A-Za-z-]+)*)?$");
      def configuration($key; $schema; $credential):
        type == "object" and
        keys == [
          "configurationId", "credentialRef", "documentSha256", "headRevision",
          "key", "schemaId", "state", "versionId", "versionNumber"
        ] and
        .key == $key and (.configurationId | uuid_v4) and
        (.headRevision | safe_positive_integer) and (.versionId | positive_int64_string) and
        (.versionNumber | safe_positive_integer) and .versionNumber == .headRevision and .schemaId == $schema and
        (.documentSha256 | sha256) and .credentialRef == $credential and
        (.state == "advanced" or .state == "created" or .state == "matched");
      def acceptance:
        type == "object" and keys == [
          "created", "eventCount", "inputMessageId", "outputMessageId", "replySha256",
          "runId", "terminalDoneCount", "threadId"
        ] and
        (.created | type == "boolean") and (.runId | uuid_v4) and (.threadId | uuid_v4) and
        (.inputMessageId | uuid_v4) and (.outputMessageId | uuid_v4) and
        ([.runId, .threadId, .inputMessageId, .outputMessageId] | unique | length == 4) and
        (.replySha256 | sha256) and (.eventCount | safe_positive_integer) and
        .terminalDoneCount == 1;
      type == "object" and
      keys == [
        "acceptanceStudentAccountId", "acceptanceStudentNumber", "acceptanceStudentUsername",
        "acceptedAt", "administratorAccountId", "autoAnalysisAcceptance", "modelConfiguration",
        "modelProbe", "promptConfiguration", "providerCredentialSha256", "replyAcceptance", "schema",
        "targetApplicationBuildTime", "targetApplicationCommit", "targetApplicationVersion"
      ] and
      .schema == "ascendany.production-agent-acceptance-receipt.v1" and
      (.acceptedAt | timestamp) and (.administratorAccountId | uuid_v4) and
      (.acceptanceStudentAccountId | uuid_v4) and
      (.acceptanceStudentUsername | type == "string" and test("^[a-z0-9_]{3,32}$") and . != "admin") and
      (.acceptanceStudentNumber | printable_nonempty(64)) and
      (.targetApplicationVersion | semver) and
      (.targetApplicationCommit | type == "string" and test("^[0-9a-f]{40}$")) and
      (.targetApplicationBuildTime | timestamp) and
      .targetApplicationVersion == $targetApplicationVersion and
      .targetApplicationCommit == $targetApplicationCommit and
      .targetApplicationBuildTime == $targetApplicationBuildTime and
      (.providerCredentialSha256 | sha256) and
      (.promptConfiguration |
        configuration("agent.prompt.default"; "ascendany.prompt.chat.v1"; null)) and
      .promptConfiguration.documentSha256 == $promptDocumentSHA256 and
      (.modelConfiguration.credentialRef |
        type == "string" and test("^[a-z][a-z0-9_.-]{0,127}$")) and
      (.modelConfiguration |
        configuration(
          "agent.model.default";
          "ascendany.model_connection.openai_compatible.v1";
          .credentialRef
        )) and
      (.modelProbe |
        type == "object" and
        keys == [
          "authority", "checkedAt", "configurationHeadRevision", "configurationKey",
          "configurationSha256", "configurationVersion", "latencyMilliseconds", "model"
        ] and
        .configurationKey == "agent.model.default" and
        (.configurationHeadRevision | safe_positive_integer) and
        (.configurationVersion | safe_positive_integer) and
        (.configurationSha256 | sha256) and
        (.authority | printable_nonempty(512)) and
        (.authority | ascii_downcase == .) and
        (.authority | test("^([^:/\\?#@[:space:]]+|\\[[0-9a-f:.]+\\]):[1-9][0-9]{0,4}$")) and
        ((.authority | capture(":(?<port>[1-9][0-9]{0,4})$").port | tonumber) <= 65535) and
        (.model | printable_nonempty(256)) and
        (.checkedAt | timestamp) and (.latencyMilliseconds | safe_nonnegative_integer)) and
      .modelProbe.configurationKey == .modelConfiguration.key and
      .modelProbe.configurationHeadRevision == .modelConfiguration.headRevision and
      .modelProbe.configurationVersion == .modelConfiguration.versionNumber and
      .modelProbe.configurationSha256 == .modelConfiguration.documentSha256 and
      (.replyAcceptance | acceptance) and .replyAcceptance.created == true and
      (.autoAnalysisAcceptance | acceptance) and
      ([
        .replyAcceptance.runId, .replyAcceptance.threadId,
        .replyAcceptance.inputMessageId, .replyAcceptance.outputMessageId,
        .autoAnalysisAcceptance.runId, .autoAnalysisAcceptance.threadId,
        .autoAnalysisAcceptance.inputMessageId, .autoAnalysisAcceptance.outputMessageId
      ] | unique | length == 8)
    ' -- "$path" >/dev/null 2>&1; then
    fail "Agent acceptance receipt has a noncanonical, open, or non-production provenance schema"
    return
  fi

  receipt_values="$(jq -er '[
      .acceptedAt, .modelProbe.checkedAt,
      .administratorAccountId, .acceptanceStudentAccountId,
      .acceptanceStudentUsername, .acceptanceStudentNumber,
      .targetApplicationVersion, .targetApplicationCommit, .targetApplicationBuildTime,
      .providerCredentialSha256,
      .promptConfiguration.configurationId, (.promptConfiguration.headRevision | tostring),
      .promptConfiguration.versionId, (.promptConfiguration.versionNumber | tostring),
      .promptConfiguration.schemaId, .promptConfiguration.documentSha256,
      .modelConfiguration.configurationId, (.modelConfiguration.headRevision | tostring),
      .modelConfiguration.versionId, (.modelConfiguration.versionNumber | tostring),
      .modelConfiguration.schemaId, .modelConfiguration.documentSha256,
      .modelConfiguration.credentialRef, .modelProbe.authority, .modelProbe.model,
      .replyAcceptance.runId, .replyAcceptance.threadId,
      .replyAcceptance.inputMessageId, .replyAcceptance.outputMessageId,
      .replyAcceptance.replySha256, (.replyAcceptance.eventCount | tostring),
      .autoAnalysisAcceptance.runId, .autoAnalysisAcceptance.threadId,
      .autoAnalysisAcceptance.inputMessageId, .autoAnalysisAcceptance.outputMessageId,
      (.autoAnalysisAcceptance.created | tostring), .autoAnalysisAcceptance.replySha256,
      (.autoAnalysisAcceptance.eventCount | tostring)
    ] | @tsv' -- "$path")"
  IFS=$'\t' read -r accepted_at probe_checked_at \
    administrator_account_id student_account_id student_username student_number \
    target_application_version target_application_commit target_application_build_time \
    provider_credential_sha256 \
    prompt_configuration_id prompt_head_revision prompt_version_id prompt_version_number \
    prompt_schema_id prompt_document_sha256 model_configuration_id model_head_revision \
    model_version_id model_version_number model_schema_id model_document_sha256 \
    model_credential_ref probe_authority probe_model \
    reply_run_id reply_thread_id reply_input_message_id reply_output_message_id \
    reply_sha256 reply_event_count \
    auto_run_id auto_thread_id auto_input_message_id auto_output_message_id \
    auto_created auto_sha256 auto_event_count <<<"$receipt_values"

  accepted_epoch="$(date -u -d "$accepted_at" +%s 2>/dev/null || true)"
  probe_epoch="$(date -u -d "$probe_checked_at" +%s 2>/dev/null || true)"
  now_epoch="$(date -u +%s)"
  if [[ ! "$accepted_epoch" =~ ^[0-9]+$ || ! "$probe_epoch" =~ ^[0-9]+$ ||
        "$probe_epoch" -gt "$accepted_epoch" ||
        $((accepted_epoch - probe_epoch)) -gt 900 ||
        "$accepted_epoch" -gt $((now_epoch + 300)) ||
        $((now_epoch - accepted_epoch)) -gt 86400 ]]; then
    fail "Agent acceptance receipt probe/acceptance times are unordered, stale, or implausibly future-dated"
    return
  fi

  for binding in "${runtime_provider_bindings[@]}"; do
    variable="${binding%%=*}"
    encoded="${variable#ASCENDANY_CREDENTIAL_FILE_REF_HEX_}"
    reference_hex="${encoded%%_AUTHORITY_HEX_*}"
    authority_hex="${encoded#*_AUTHORITY_HEX_}"
    binding_reference="$(decode_upper_hex_ascii "$reference_hex" 2>/dev/null || true)"
    binding_authority="$(decode_upper_hex_ascii "$authority_hex" 2>/dev/null || true)"
    if [[ "$binding_reference" == "$model_credential_ref" &&
          "$binding_authority" == "$probe_authority" ]]; then
      matching_provider_bindings=$((matching_provider_bindings + 1))
      provider_credential_id="${binding#*=}"
    fi
  done
  if [[ "$matching_provider_bindings" != 1 ]]; then
    fail "Agent model receipt credential reference and probe authority do not resolve to one expected runtime provider binding"
    return
  fi

  provider_credential_source="/etc/ascendany/credentials/${provider_credential_id}.cred"
  if ! provider_source_sha256="$(encrypted_credential_sha256 \
      "$provider_credential_id" "$provider_credential_source")" ||
     [[ "$provider_source_sha256" != "$provider_credential_sha256" ]]; then
    fail "Agent acceptance receipt credential SHA-256 differs from the current host-encrypted provider credential"
    return
  fi

  reply_marker="$(jq -Scn \
    --arg schema 'ascendany.production-agent-reply-acceptance.v1' \
    --arg instruction 'Read my current learning data, update my current notes with a concise progress summary by calling update_notes, and briefly explain my learning progress.' \
    --arg targetApplicationVersion "$target_application_version" \
    --arg targetApplicationCommit "$target_application_commit" \
    --arg targetApplicationBuildTime "$target_application_build_time" \
    '{schema: $schema, instruction: $instruction,
      targetApplicationBuildTime: $targetApplicationBuildTime,
      targetApplicationCommit: $targetApplicationCommit,
      targetApplicationVersion: $targetApplicationVersion}')"

  if ! database_match="$({
    run_runtime_psql -A -t -v ON_ERROR_STOP=1 \
      -v prompt_configuration_id="$prompt_configuration_id" \
      -v prompt_head_revision="$prompt_head_revision" \
      -v prompt_version_id="$prompt_version_id" \
      -v prompt_version_number="$prompt_version_number" \
      -v prompt_schema_id="$prompt_schema_id" \
      -v prompt_document_sha256="$prompt_document_sha256" \
      -v model_configuration_id="$model_configuration_id" \
      -v model_head_revision="$model_head_revision" \
      -v model_version_id="$model_version_id" \
      -v model_version_number="$model_version_number" \
      -v model_schema_id="$model_schema_id" \
      -v model_document_sha256="$model_document_sha256" \
      -v model_credential_ref="$model_credential_ref" \
      -v probe_authority="$probe_authority" \
      -v probe_model="$probe_model" \
      -v administrator_account_id="$administrator_account_id" \
      -v student_account_id="$student_account_id" \
      -v student_username="$student_username" \
      -v student_number="$student_number" \
      -v probe_checked_at="$probe_checked_at" \
      -v accepted_at="$accepted_at" \
      -v reply_marker="$reply_marker" \
      -v reply_run_id="$reply_run_id" \
      -v reply_thread_id="$reply_thread_id" \
      -v reply_input_message_id="$reply_input_message_id" \
      -v reply_output_message_id="$reply_output_message_id" \
      -v reply_sha256="$reply_sha256" \
      -v reply_event_count="$reply_event_count" \
      -v auto_run_id="$auto_run_id" \
      -v auto_thread_id="$auto_thread_id" \
      -v auto_input_message_id="$auto_input_message_id" \
      -v auto_output_message_id="$auto_output_message_id" \
      -v auto_created="$auto_created" \
      -v auto_sha256="$auto_sha256" \
      -v auto_event_count="$auto_event_count" <<'SQL'
/* ascendany-validator:agent-acceptance-receipt */
WITH prompt_configuration AS (
SELECT prompt_version.configuration_version_id
FROM ascendany.configuration_items AS prompt_item
JOIN ascendany.configuration_versions AS prompt_version
  ON prompt_version.configuration_item_id = prompt_item.configuration_item_id
 AND prompt_version.configuration_version_id = prompt_item.active_version_id
 AND prompt_version.configuration_kind = prompt_item.configuration_kind
WHERE prompt_item.configuration_key = 'agent.prompt.default'
  AND prompt_item.configuration_kind = 'prompt'
  AND prompt_item.public_id = :'prompt_configuration_id'::uuid
  AND prompt_item.head_revision = :'prompt_head_revision'::bigint
  AND prompt_version.configuration_version_id = :'prompt_version_id'::bigint
  AND prompt_version.version_number = :'prompt_version_number'::bigint
  AND prompt_version.schema_id = :'prompt_schema_id'
  AND prompt_version.schema_id = 'ascendany.prompt.chat.v1'
  AND prompt_version.document_sha256 = :'prompt_document_sha256'
  AND prompt_version.document_sha256 = '1e7fc27df0bedfb43126579204833750e36877940d921cbb01afeb116d9d59f2'
  AND prompt_version.credential_ref IS NULL
  AND prompt_version.created_by_role = 'admin'
), model_configuration AS (
SELECT model_version.configuration_version_id
FROM ascendany.configuration_items AS model_item
JOIN ascendany.configuration_versions AS model_version
  ON model_version.configuration_item_id = model_item.configuration_item_id
 AND model_version.configuration_version_id = model_item.active_version_id
 AND model_version.configuration_kind = model_item.configuration_kind
CROSS JOIN LATERAL regexp_match(
  model_version.document ->> 'endpoint',
  '^https://([a-z0-9][a-z0-9.-]*[a-z0-9]|[a-z0-9]|\[[0-9a-f:.]+\])(:([1-9][0-9]{0,4}))?(/[^?#]*)$'
) AS endpoint_match(parts)
WHERE model_item.configuration_key = 'agent.model.default'
  AND model_item.configuration_kind = 'model_connection'
  AND model_item.public_id = :'model_configuration_id'::uuid
  AND model_item.head_revision = :'model_head_revision'::bigint
  AND model_version.configuration_version_id = :'model_version_id'::bigint
  AND model_version.version_number = :'model_version_number'::bigint
  AND model_version.schema_id = :'model_schema_id'
  AND model_version.schema_id = 'ascendany.model_connection.openai_compatible.v1'
  AND model_version.document_sha256 = :'model_document_sha256'
  AND model_version.credential_ref = :'model_credential_ref'
  AND model_version.created_by_role = 'admin'
  AND model_version.document = jsonb_build_object(
    'endpoint', model_version.document ->> 'endpoint',
    'maxCompletionTokens', 4096,
    'model', :'probe_model'::text,
    'timeoutMilliseconds', 120000
  )
  AND (endpoint_match.parts)[4] <> '/'
  AND strpos((endpoint_match.parts)[4], '//') = 0
  AND COALESCE((endpoint_match.parts)[3], '443')::integer BETWEEN 1 AND 65535
  AND :'probe_authority' =
    (endpoint_match.parts)[1] || ':' || COALESCE((endpoint_match.parts)[3], '443')
), acceptance_identity AS (
SELECT administrator.account_id AS administrator_account_id,
       student.account_id AS student_account_id
FROM ascendany.auth_accounts AS administrator
CROSS JOIN ascendany.auth_accounts AS student
WHERE administrator.public_id = :'administrator_account_id'::uuid
  AND administrator.username = 'admin'
  AND administrator.role = 'admin'
  AND administrator.student_number IS NULL
  AND administrator.disabled_at IS NULL
  AND student.public_id = :'student_account_id'::uuid
  AND student.username = :'student_username'
  AND student.student_number = :'student_number'
  AND student.role = 'student'
  AND student.disabled_at IS NULL
), current_analytics AS (
SELECT generation.analytics_generation_id, head.head_revision
FROM ascendany.analytics_head AS head
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
WHERE head.singleton
  AND head.head_revision > 0
  AND generation.status = 'succeeded'
), reply_acceptance AS (
SELECT run.agent_run_id, run.finished_at
FROM acceptance_identity AS identity
CROSS JOIN current_analytics AS analytics
CROSS JOIN prompt_configuration AS prompt
CROSS JOIN model_configuration AS model
JOIN ascendany.agent_runs AS run
  ON run.owner_account_id = identity.student_account_id
JOIN ascendany.auth_sessions AS session
  ON session.session_id = run.request_session_id
 AND session.account_id = run.owner_account_id
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = run.chat_thread_id
 AND thread.owner_account_id = run.owner_account_id
JOIN ascendany.chat_messages AS input
  ON input.chat_message_id = run.input_message_id
 AND input.chat_thread_id = run.chat_thread_id
 AND input.owner_account_id = run.owner_account_id
JOIN ascendany.chat_messages AS output
  ON output.chat_message_id = run.output_message_id
 AND output.agent_run_id = run.agent_run_id
 AND output.chat_thread_id = run.chat_thread_id
 AND output.owner_account_id = run.owner_account_id
WHERE run.public_id = :'reply_run_id'::uuid
  AND thread.public_id = :'reply_thread_id'::uuid
  AND input.public_id = :'reply_input_message_id'::uuid
  AND output.public_id = :'reply_output_message_id'::uuid
  AND thread.thread_kind = 'conversation'
  AND run.run_kind = 'reply'
  AND run.input_message_kind = 'user'
  AND input.message_kind = 'user'
  AND input.author_session_id = run.request_session_id
  AND output.message_kind = 'assistant'
  AND run.status = 'succeeded'
  AND run.error_code IS NULL
  AND run.error_detail IS NULL
  AND run.prompt_configuration_version_id = prompt.configuration_version_id
  AND run.model_configuration_version_id = model.configuration_version_id
  AND run.analytics_generation_id = analytics.analytics_generation_id
  AND run.created_at = input.created_at
  AND run.created_at >= :'probe_checked_at'::timestamptz
  AND run.started_at >= run.created_at
  AND run.finished_at >= run.started_at
  AND run.finished_at = output.created_at
  AND run.finished_at <= :'accepted_at'::timestamptz
  AND input.content::jsonb = jsonb_build_object(
    'currentUser', jsonb_build_object(
      'content', :'reply_marker'::text, 'messageIndex', 0,
      'ptaNickname', '', 'studentId', ''
    ),
    'messages', jsonb_build_array(jsonb_build_object(
      'content', :'reply_marker'::text, 'reasoningContent', NULL, 'role', 'user'
    )),
    'notes', jsonb_build_object(
      'content', E'# Production acceptance\n\nAwaiting the current learning-progress summary.',
      'locked', false,
      'title', 'Production acceptance'
    ),
    'role', jsonb_build_object('id', '', 'name', '', 'systemPrompt', ''),
    'schema', 'ascendany.agent.frontend-context.v1',
    'summary', ''
  )
  AND encode(sha256(convert_to(output.content, 'UTF8')), 'hex') = :'reply_sha256'
  AND :'reply_event_count'::bigint = 8 +
    CASE WHEN COALESCE(output.reasoning_content, '') = '' THEN 0 ELSE 1 END
  AND (SELECT count(*) FROM ascendany.agent_tool_calls AS tool
       WHERE tool.agent_run_id = run.agent_run_id) = 2
  AND EXISTS (
    SELECT 1
    FROM ascendany.agent_tool_calls AS tool
    WHERE tool.agent_run_id = run.agent_run_id
      AND tool.tool_sequence = 1
      AND tool.tool_name = 'analytics.get_self'
      AND tool.arguments_schema = 'ascendany.agent_tool.analytics_get_self_arguments.v1'
      AND tool.arguments = '{"historyLimit":50}'::jsonb
      AND tool.arguments_sha256 = '6f074b108ee51e8bf0b7ef1bbbe4bab2ca6bd4b01b5817dc49a8348f99d4f09b'
      AND tool.result_schema = 'ascendany.agent_tool.analytics_get_self_result.v1'
      AND tool.result ->> 'state' = 'ready'
      AND (tool.result ->> 'headRevision')::bigint = analytics.head_revision
      AND tool.outcome = 'succeeded'
      AND tool.error_code IS NULL
      AND tool.started_at >= run.started_at
      AND tool.finished_at >= tool.started_at
      AND tool.finished_at <= run.finished_at
  )
  AND EXISTS (
    SELECT 1
    FROM ascendany.agent_tool_calls AS tool
    WHERE tool.agent_run_id = run.agent_run_id
      AND tool.tool_sequence = 2
      AND tool.tool_name = 'update_notes'
      AND tool.arguments_schema = 'ascendany.agent_tool.update_notes_arguments.v1'
      AND tool.arguments ->> 'mode' IN ('patch', 'replace')
      AND (
        (tool.arguments ->> 'mode' = 'patch' AND
         tool.arguments = jsonb_build_object(
           'mode', 'patch',
           'patch', tool.arguments -> 'patch'
         ) AND
         jsonb_typeof(tool.arguments -> 'patch') = 'string')
        OR
        (tool.arguments ->> 'mode' = 'replace' AND
         tool.arguments = jsonb_build_object(
           'mode', 'replace',
           'content', tool.arguments -> 'content'
         ) AND
         jsonb_typeof(tool.arguments -> 'content') = 'string')
      )
      AND tool.result_schema = 'ascendany.agent_tool.update_notes_result.v1'
      AND tool.result ->> 'ok' = 'true'
      AND tool.result ->> 'mode' = tool.arguments ->> 'mode'
      AND (tool.result ->> 'length')::bigint >= 0
      AND tool.result ->> 'previousSha256' = encode(
        sha256(convert_to(E'# Production acceptance\n\nAwaiting the current learning-progress summary.', 'UTF8')),
        'hex'
      )
      AND tool.result ->> 'nextSha256' = encode(
        sha256(convert_to(tool.result ->> 'updatedNotes', 'UTF8')),
        'hex'
      )
      AND tool.outcome = 'succeeded'
      AND tool.error_code IS NULL
      AND tool.started_at >= run.started_at
      AND tool.finished_at >= tool.started_at
      AND tool.finished_at <= run.finished_at
  )
  AND (SELECT count(*) FROM ascendany.agent_run_events AS event
       WHERE event.agent_run_id = run.agent_run_id) =
      (SELECT max(event.event_sequence) FROM ascendany.agent_run_events AS event
       WHERE event.agent_run_id = run.agent_run_id)
  AND (SELECT count(*) FROM ascendany.agent_run_events AS event
       WHERE event.agent_run_id = run.agent_run_id
         AND event.event_type IN ('claimed', 'reclaimed')) = run.attempt_count
  AND EXISTS (
    SELECT 1 FROM ascendany.agent_run_events AS event
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_sequence = 1
      AND event.event_type = 'queued'
      AND event.payload = jsonb_build_object(
        'analyticsHeadRevision', analytics.head_revision,
        'messageSequence', input.message_sequence,
        'model', :'probe_model'::text,
        'provider', 'openai_compatible',
        'requestMode', 'chat_completions',
        'runKind', 'reply'
      )
  )
  AND (SELECT count(*)
       FROM ascendany.agent_run_events AS event
       JOIN ascendany.agent_tool_calls AS tool
         ON tool.agent_run_id = event.agent_run_id
        AND event.payload = jsonb_build_object(
          'toolCallKey', tool.tool_call_key,
          'toolName', tool.tool_name,
          'toolSequence', tool.tool_sequence
        )
       WHERE event.agent_run_id = run.agent_run_id
         AND event.event_type = 'tool.succeeded') = 2
  AND EXISTS (
    SELECT 1
    FROM ascendany.agent_run_events AS event
    JOIN ascendany.agent_tool_calls AS tool
      ON tool.agent_run_id = event.agent_run_id
     AND tool.tool_sequence = 2
     AND tool.tool_name = 'update_notes'
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_type = 'notes_update'
      AND event.payload = jsonb_build_object(
        'mode', tool.result ->> 'mode',
        'next', tool.result ->> 'updatedNotes',
        'patch', CASE WHEN tool.result ->> 'mode' = 'patch' THEN tool.arguments -> 'patch' ELSE 'null'::jsonb END,
        'previous', E'# Production acceptance\n\nAwaiting the current learning-progress summary.',
        'toolCallKey', tool.tool_call_key,
        'toolName', tool.tool_name,
        'toolSequence', tool.tool_sequence
      )
  )
  AND EXISTS (
    SELECT 1 FROM ascendany.agent_run_events AS event
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_sequence = (
        SELECT max(terminal.event_sequence)
        FROM ascendany.agent_run_events AS terminal
        WHERE terminal.agent_run_id = run.agent_run_id
      )
      AND event.event_type = 'completed'
      AND event.payload = jsonb_build_object(
        'messageId', output.public_id::text,
        'messageSequence', output.message_sequence
      )
  )
  AND NOT EXISTS (
    SELECT 1 FROM ascendany.agent_run_events AS event
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_type NOT IN ('queued', 'claimed', 'reclaimed', 'notes_update', 'tool.succeeded', 'completed')
  )
), auto_acceptance AS (
SELECT run.agent_run_id
FROM acceptance_identity AS identity
CROSS JOIN current_analytics AS analytics
CROSS JOIN prompt_configuration AS prompt
CROSS JOIN model_configuration AS model
CROSS JOIN reply_acceptance AS reply
JOIN ascendany.agent_runs AS run
  ON run.owner_account_id = identity.student_account_id
JOIN ascendany.auth_sessions AS session
  ON session.session_id = run.request_session_id
 AND session.account_id = run.owner_account_id
JOIN ascendany.chat_threads AS thread
  ON thread.chat_thread_id = run.chat_thread_id
 AND thread.owner_account_id = run.owner_account_id
JOIN ascendany.chat_messages AS input
  ON input.chat_message_id = run.input_message_id
 AND input.chat_thread_id = run.chat_thread_id
 AND input.owner_account_id = run.owner_account_id
JOIN ascendany.chat_messages AS output
  ON output.chat_message_id = run.output_message_id
 AND output.agent_run_id = run.agent_run_id
 AND output.chat_thread_id = run.chat_thread_id
 AND output.owner_account_id = run.owner_account_id
WHERE run.public_id = :'auto_run_id'::uuid
  AND thread.public_id = :'auto_thread_id'::uuid
  AND input.public_id = :'auto_input_message_id'::uuid
  AND output.public_id = :'auto_output_message_id'::uuid
  AND thread.thread_kind = 'auto_analysis'
  AND run.run_kind = 'auto_analysis'
  AND run.input_message_kind = 'auto_analysis_request'
  AND run.auto_analysis_exam_id IS NOT NULL
  AND run.auto_analysis_role_id = 'xiaoD'
  AND input.message_kind = 'auto_analysis_request'
  AND input.author_session_id = run.request_session_id
  AND output.message_kind = 'assistant'
  AND run.status = 'succeeded'
  AND run.error_code IS NULL
  AND run.error_detail IS NULL
  AND run.prompt_configuration_version_id = prompt.configuration_version_id
  AND run.model_configuration_version_id = model.configuration_version_id
  AND run.analytics_generation_id = analytics.analytics_generation_id
  AND run.created_at = input.created_at
  AND run.started_at >= run.created_at
  AND run.finished_at >= run.started_at
  AND run.finished_at = output.created_at
  AND run.finished_at <= :'accepted_at'::timestamptz
  AND (
    (:'auto_created'::boolean AND run.created_at >= reply.finished_at)
    OR (NOT :'auto_created'::boolean AND run.created_at < :'probe_checked_at'::timestamptz)
  )
  AND input.content::jsonb = jsonb_build_object(
    'context', jsonb_build_object(
      'latestExamId', run.auto_analysis_exam_id::text,
      'notes', '', 'notesLocked', false, 'notesTitle', '',
      'ptaNickname', '', 'roleId', run.auto_analysis_role_id,
      'roleName', '', 'roleSystemPrompt', '', 'studentId', ''
    ),
    'instruction', 'Analyze the student''s current published analytics snapshot and provide a concise, actionable progress review.',
    'schema', 'ascendany.agent.auto-analysis.frontend-context.v1'
  )
  AND encode(sha256(convert_to(output.content, 'UTF8')), 'hex') = :'auto_sha256'
  AND :'auto_event_count'::bigint = 5 +
    CASE WHEN COALESCE(output.reasoning_content, '') = '' THEN 0 ELSE 1 END
  AND (SELECT count(*) FROM ascendany.agent_tool_calls AS tool
       WHERE tool.agent_run_id = run.agent_run_id) = 1
  AND EXISTS (
    SELECT 1
    FROM ascendany.agent_tool_calls AS tool
    WHERE tool.agent_run_id = run.agent_run_id
      AND tool.tool_sequence = 1
      AND tool.tool_name = 'analytics.get_self'
      AND tool.arguments_schema = 'ascendany.agent_tool.analytics_get_self_arguments.v1'
      AND tool.arguments = '{"historyLimit":50}'::jsonb
      AND tool.arguments_sha256 = '6f074b108ee51e8bf0b7ef1bbbe4bab2ca6bd4b01b5817dc49a8348f99d4f09b'
      AND tool.result_schema = 'ascendany.agent_tool.analytics_get_self_result.v1'
      AND tool.result ->> 'state' = 'ready'
      AND (tool.result ->> 'headRevision')::bigint = analytics.head_revision
      AND tool.outcome = 'succeeded'
      AND tool.error_code IS NULL
      AND tool.started_at >= run.started_at
      AND tool.finished_at >= tool.started_at
      AND tool.finished_at <= run.finished_at
  )
  AND (SELECT count(*) FROM ascendany.agent_run_events AS event
       WHERE event.agent_run_id = run.agent_run_id) =
      (SELECT max(event.event_sequence) FROM ascendany.agent_run_events AS event
       WHERE event.agent_run_id = run.agent_run_id)
  AND (SELECT count(*) FROM ascendany.agent_run_events AS event
       WHERE event.agent_run_id = run.agent_run_id
         AND event.event_type IN ('claimed', 'reclaimed')) = run.attempt_count
  AND EXISTS (
    SELECT 1 FROM ascendany.agent_run_events AS event
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_sequence = 1
      AND event.event_type = 'queued'
      AND event.payload = jsonb_build_object(
        'analyticsHeadRevision', analytics.head_revision,
        'autoAnalysisExamId', run.auto_analysis_exam_id::text,
        'autoAnalysisRoleId', run.auto_analysis_role_id,
        'messageSequence', input.message_sequence,
        'model', :'probe_model'::text,
        'provider', 'openai_compatible',
        'requestMode', 'chat_completions',
        'runKind', 'auto_analysis'
      )
  )
  AND EXISTS (
    SELECT 1 FROM ascendany.agent_run_events AS event
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_type = 'tool.succeeded'
      AND event.payload = (
        SELECT jsonb_build_object(
          'toolCallKey', tool.tool_call_key,
          'toolName', tool.tool_name,
          'toolSequence', tool.tool_sequence
        )
        FROM ascendany.agent_tool_calls AS tool
        WHERE tool.agent_run_id = run.agent_run_id
      )
  )
  AND EXISTS (
    SELECT 1 FROM ascendany.agent_run_events AS event
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_sequence = (
        SELECT max(terminal.event_sequence)
        FROM ascendany.agent_run_events AS terminal
        WHERE terminal.agent_run_id = run.agent_run_id
      )
      AND event.event_type = 'completed'
      AND event.payload = jsonb_build_object(
        'messageId', output.public_id::text,
        'messageSequence', output.message_sequence
      )
  )
  AND NOT EXISTS (
    SELECT 1 FROM ascendany.agent_run_events AS event
    WHERE event.agent_run_id = run.agent_run_id
      AND event.event_type NOT IN ('queued', 'claimed', 'reclaimed', 'tool.succeeded', 'completed')
  )
)
SELECT count(*)
FROM prompt_configuration
CROSS JOIN model_configuration
CROSS JOIN acceptance_identity
CROSS JOIN current_analytics
CROSS JOIN reply_acceptance
CROSS JOIN auto_acceptance
SQL
  } 2>/dev/null)"; then
    database_match=""
  fi
  if [[ "$database_match" != 1 ]]; then
    fail "Agent acceptance receipt does not match active configuration, account, analytics, run, message, tool, and event provenance"
    return
  fi

  if (( failures == failures_before )); then
    pass "Agent acceptance receipt is fresh, release/provider-bound, and matches immutable database execution provenance"
  fi
}

check_catalog_publication_binding() {
  if ! catalog_phase && ! production_phase &&
     ! { forward_transition && activation_phase; }; then
    return 0
  fi

  local failures_before="$failures"
  local metadata entries filename node_type path canonical_receipt receipt_values
  local publication_id authorization_id target_model_release_id catalog_sha model_artifact_sha model_id
  local target_application_version target_application_commit target_application_build_time
  local configuration_key configuration_id expected_configuration_head_revision
  local configuration_head_revision configuration_version_id configuration_version_number
  local analytics_generation_id analytics_head_revision input_manifest_sha
  local current_model_head_revision current_model_artifact_sha published_account_id
  local published_session_id published_at audit_event_id configuration_mutated
  local database_match filesystem_ids database_ids
  local target_model_id catalog_document_base64
  local expected_prior_revision expected_prior_sha target_state target_count target_publication_id
  local target_release_id target_prior_revision target_prior_sha target_configuration_head_revision
  local target_consumption_count target_consumption_revision expected_consumption_count
  local expected_consumption_revision activation_state active_head_revision activation_count
  local activation_min_revision activation_max_revision activation_distinct_revision_count
  local activation_invalid_count initial_publication_count publication_count
  local unexpected_unconsumed_count expected_active_head_revision receipt_count=0
  declare -A receipt_current_revision=()
  declare -A receipt_current_sha=()
  declare -A receipt_configuration_revision=()
  declare -A receipt_catalog_sha=()
  declare -A receipt_model_sha=()
  declare -A receipt_model_id=()
  declare -A receipt_target_release_id=()
  declare -A receipt_target_version=()
  declare -A receipt_target_commit=()
  declare -A receipt_target_build_time=()

  metadata="$(stat -Lc '%U:%G:%a' -- "$catalog_receipt_root" 2>/dev/null || true)"
  if [[ ! -d "$catalog_receipt_root" || -L "$catalog_receipt_root" ||
        "$metadata" != ascendany-catalog-publisher:ascendany-catalog-readers:750 ]]; then
    fail "catalog publication receipt root must be a real publisher-owned reader-group mode 0750 directory"
    return
  fi
  if ! entries="$(find "$catalog_receipt_root" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C sort -t '|' -k1,1n)"; then
    fail "catalog publication receipt root cannot be enumerated"
    return
  fi
  if [[ -z "$entries" ]]; then
    fail "catalog publication receipt root is empty after the catalog commit point"
    return
  fi

  canonical_receipt="$(mktemp)"
  while IFS='|' read -r filename node_type; do
    [[ -n "$filename" ]] || continue
    path="$catalog_receipt_root/$filename"
    if [[ "$node_type" != f || ! "$filename" =~ ^[1-9][0-9]*[.]json$ ||
          "$(stat -Lc '%U:%G:%a:%h' -- "$path" 2>/dev/null || true)" != ascendany-catalog-publisher:ascendany-catalog-readers:640:1 ||
          ! -s "$path" || "$(stat -Lc '%s' -- "$path" 2>/dev/null || true)" -gt 4096 ]]; then
      fail "catalog publication receipt entry violates the immutable publication-ID file contract: $filename"
      continue
    fi
    if ! jq -jScs 'if length == 1 then .[0] else empty end' -- "$path" >"$canonical_receipt" 2>/dev/null ||
       ! cmp --silent -- "$path" "$canonical_receipt"; then
      fail "catalog publication receipt is not exactly one canonical JSON object without trailing bytes: $filename"
      continue
    fi
    if ! jq -e -s --arg publicationId "${filename%.json}" '
        def positive_int64_string:
          type == "string" and test("^[1-9][0-9]{0,18}$") and
          (length < 19 or (length == 19 and . <= "9223372036854775807"));
        def safe_positive_integer:
          type == "number" and floor == . and . >= 1 and . <= 9007199254740991;
        def safe_nonnegative_integer:
          type == "number" and floor == . and . >= 0 and . <= 9007199254740991;
        def sha256: type == "string" and test("^[0-9a-f]{64}$");
        def uuid_v4:
          type == "string" and test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$");
        length == 1 and (.[0] |
          type == "object" and
          keys == [
            "analyticsGenerationId", "analyticsHeadRevision", "auditEventId", "authorizationId", "catalogSha256",
            "configurationHeadRevision", "configurationId", "configurationKey", "configurationMutated",
            "configurationVersionId", "configurationVersionNumber", "currentModelArtifactSha256",
            "currentModelHeadRevision", "expectedConfigurationHeadRevision",
            "inputManifestSha256", "knowledgeCatalogPublicationId", "modelArtifactSha256",
            "modelId", "publishedAt", "publishedByAccountId", "publishedBySessionId", "schema",
            "targetApplicationBuildTime", "targetApplicationCommit", "targetApplicationVersion",
            "targetModelReleaseId"
          ] and
          .schema == "ascendany.knowledge_catalog.publication-receipt.v1" and
          (.authorizationId | uuid_v4) and
          (.knowledgeCatalogPublicationId | positive_int64_string) and
          .knowledgeCatalogPublicationId == $publicationId and
          (.targetModelReleaseId | positive_int64_string) and
          (.catalogSha256 | sha256) and (.modelArtifactSha256 | sha256) and
          (.modelId | uuid_v4) and
          .configurationKey == "recommendation.catalog.active" and
          (.configurationId | uuid_v4) and
          (.expectedConfigurationHeadRevision | safe_nonnegative_integer) and
          (.configurationHeadRevision | safe_positive_integer) and
          (.configurationVersionId | positive_int64_string) and
          (.configurationVersionNumber | safe_positive_integer) and
          (.analyticsGenerationId | positive_int64_string) and
          (.analyticsHeadRevision | safe_positive_integer) and
          (.inputManifestSha256 | sha256) and
          (.currentModelHeadRevision | safe_positive_integer) and
          (.currentModelArtifactSha256 | sha256) and
          (.targetApplicationVersion | type == "string" and length <= 128 and
            test("^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)([.]((0|[1-9][0-9]*)|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?([+][0-9A-Za-z-]+([.][0-9A-Za-z-]+)*)?$")) and
          (.targetApplicationCommit | type == "string" and test("^[0-9a-f]{40}$")) and
          (.targetApplicationBuildTime | type == "string" and
            test("^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]([.][0-9]{1,9})?Z$")) and
          (.publishedByAccountId | uuid_v4) and (.publishedBySessionId | uuid_v4) and
          (.publishedAt | type == "string" and
            test("^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]([.][0-9]{1,9})?Z$")) and
          (.auditEventId | positive_int64_string) and (.configurationMutated | type == "boolean") and
          .configurationHeadRevision ==
            (.expectedConfigurationHeadRevision + (if .configurationMutated then 1 else 0 end))
        )
      ' -- "$path" >/dev/null 2>&1; then
      fail "catalog publication receipt has a noncanonical or open schema: $filename"
      continue
    fi
    receipt_values="$(jq -er -s '.[0] | [
        .knowledgeCatalogPublicationId, .authorizationId, .targetModelReleaseId,
        .catalogSha256, .modelArtifactSha256, .modelId,
        .targetApplicationVersion, .targetApplicationCommit, .targetApplicationBuildTime,
        .configurationKey, .configurationId, (.expectedConfigurationHeadRevision | tostring),
        (.configurationHeadRevision | tostring), .configurationVersionId,
        (.configurationVersionNumber | tostring), .analyticsGenerationId,
        (.analyticsHeadRevision | tostring), .inputManifestSha256,
        (.currentModelHeadRevision | tostring), .currentModelArtifactSha256,
        .publishedByAccountId, .publishedBySessionId, .publishedAt, .auditEventId,
        (.configurationMutated | tostring)
      ] | @tsv' -- "$path")"
    IFS=$'\t' read -r publication_id authorization_id target_model_release_id catalog_sha model_artifact_sha model_id \
      target_application_version target_application_commit target_application_build_time \
      configuration_key configuration_id expected_configuration_head_revision \
      configuration_head_revision configuration_version_id configuration_version_number \
      analytics_generation_id analytics_head_revision input_manifest_sha \
      current_model_head_revision current_model_artifact_sha published_account_id \
      published_session_id published_at audit_event_id configuration_mutated <<<"$receipt_values"
    database_match="$(run_runtime_psql_with_variables -A -t -v ON_ERROR_STOP=1 \
      -v knowledge_catalog_publication_id="$publication_id" \
      -v publication_authorization_id="$authorization_id" \
      -v target_model_release_id="$target_model_release_id" \
      -v catalog_sha="$catalog_sha" \
      -v model_artifact_sha="$model_artifact_sha" \
      -v model_id="$model_id" \
      -v target_application_version="$target_application_version" \
      -v target_application_commit="$target_application_commit" \
      -v target_application_build_time="$target_application_build_time" \
      -v configuration_id="$configuration_id" \
      -v expected_configuration_head_revision="$expected_configuration_head_revision" \
      -v configuration_head_revision="$configuration_head_revision" \
      -v configuration_mutated="$configuration_mutated" \
      -v configuration_version_id="$configuration_version_id" \
      -v configuration_version_number="$configuration_version_number" \
      -v analytics_generation_id="$analytics_generation_id" \
      -v analytics_head_revision="$analytics_head_revision" \
      -v input_manifest_sha="$input_manifest_sha" \
      -v current_model_head_revision="$current_model_head_revision" \
      -v current_model_artifact_sha="$current_model_artifact_sha" \
      -v published_account_id="$published_account_id" \
      -v published_session_id="$published_session_id" \
      -v published_at="$published_at" \
      -v audit_event_id="$audit_event_id" -c '
/* ascendany-validator:catalog-publication-receipt */
SELECT count(*)
FROM ascendany.knowledge_catalog_publications AS publication
JOIN ascendany.knowledge_catalog_publication_authorizations AS capability
  ON capability.public_id = publication.publication_authorization_id
 AND capability.consumed_publication_id = publication.knowledge_catalog_publication_id
 AND capability.consumed_at = publication.published_at
JOIN ascendany.recommendation_model_releases AS target_release
  ON target_release.recommendation_model_release_id = publication.target_model_release_id
 AND target_release.model_id = publication.target_model_id
 AND target_release.artifact_sha256 = publication.target_model_artifact_sha256
 AND target_release.knowledge_catalog_sha256 = publication.catalog_sha256
JOIN ascendany.configuration_items AS item
  ON item.configuration_item_id = publication.configuration_item_id
 AND item.configuration_kind = '\''knowledge_catalog'\''
JOIN ascendany.configuration_versions AS version
  ON version.configuration_item_id = publication.configuration_item_id
 AND version.configuration_version_id = publication.configuration_version_id
 AND version.configuration_kind = '\''knowledge_catalog'\''
JOIN ascendany.auth_accounts AS account
  ON account.account_id = publication.published_by_account_id
JOIN ascendany.auth_sessions AS session
  ON session.session_id = publication.published_by_session_id
 AND session.account_id = publication.published_by_account_id
JOIN ascendany.audit_events AS audit
  ON audit.audit_event_id = publication.audit_event_id
 AND audit.account_id = publication.published_by_account_id
 AND audit.session_id = publication.published_by_session_id
 AND audit.occurred_at = publication.published_at
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = publication.analytics_generation_id
 AND generation.status = '\''succeeded'\''
 AND generation.input_manifest_sha256 = publication.input_manifest_sha256
WHERE publication.knowledge_catalog_publication_id = :'\''knowledge_catalog_publication_id'\''::bigint
  AND publication.publication_authorization_id = :'\''publication_authorization_id'\''::uuid
  AND publication.target_model_release_id = :'\''target_model_release_id'\''::bigint
  AND publication.catalog_sha256 = :'\''catalog_sha'\''
  AND publication.target_model_artifact_sha256 = :'\''model_artifact_sha'\''
  AND publication.target_model_id = :'\''model_id'\''::uuid
  AND publication.target_application_version = :'\''target_application_version'\''
  AND publication.target_application_commit = :'\''target_application_commit'\''
  AND publication.target_application_build_time = :'\''target_application_build_time'\''
  AND item.configuration_key = '\''recommendation.catalog.active'\''
  AND item.public_id = :'\''configuration_id'\''::uuid
  AND publication.expected_configuration_head_revision = :'\''expected_configuration_head_revision'\''::bigint
  AND publication.configuration_head_revision = :'\''configuration_head_revision'\''::bigint
  AND publication.configuration_mutated = :'\''configuration_mutated'\''::boolean
  AND version.configuration_version_id = :'\''configuration_version_id'\''::bigint
  AND version.version_number = :'\''configuration_version_number'\''::bigint
  AND version.schema_id = '\''ascendany.knowledge_catalog.recommendation.v1'\''
  AND version.document_sha256 = publication.catalog_sha256
  AND version.credential_ref IS NULL
  AND version.created_by_role = '\''admin'\''
  AND publication.analytics_generation_id = :'\''analytics_generation_id'\''::bigint
  AND publication.analytics_head_revision = :'\''analytics_head_revision'\''::bigint
  AND publication.input_manifest_sha256 = :'\''input_manifest_sha'\''
  AND publication.current_model_head_revision = :'\''current_model_head_revision'\''::bigint
  AND publication.current_model_artifact_sha256 = :'\''current_model_artifact_sha'\''
  AND account.public_id = :'\''published_account_id'\''::uuid
  AND session.public_id = :'\''published_session_id'\''::uuid
  AND publication.published_at = :'\''published_at'\''::timestamptz
  AND publication.audit_event_id = :'\''audit_event_id'\''::bigint
  AND (
    NOT publication.configuration_mutated OR (
      version.created_by_account_id = publication.published_by_account_id
      AND version.created_by_session_id = publication.published_by_session_id
      AND version.created_at = publication.published_at
    )
  )
  AND audit.event_type = CASE
    WHEN publication.configuration_mutated THEN '\''admin.configuration_version_created'\''
    ELSE '\''admin.knowledge_catalog_release_bound'\''
  END
  AND audit.payload = jsonb_build_object(
    '\''authorizationId'\'', publication.publication_authorization_id::text,
    '\''configurationId'\'', item.public_id::text,
    '\''key'\'', item.configuration_key,
    '\''kind'\'', item.configuration_kind,
    '\''versionNumber'\'', version.version_number,
    '\''schemaId'\'', version.schema_id,
    '\''documentSha256'\'', version.document_sha256,
    '\''headRevision'\'', publication.configuration_head_revision,
    '\''credentialRef'\'', version.credential_ref,
    '\''expectedConfigurationHeadRevision'\'', publication.expected_configuration_head_revision,
    '\''configurationMutated'\'', publication.configuration_mutated,
    '\''analyticsGenerationId'\'', publication.analytics_generation_id::text,
    '\''analyticsHeadRevision'\'', publication.analytics_head_revision,
    '\''inputManifestSha256'\'', publication.input_manifest_sha256,
    '\''currentModelHeadRevision'\'', publication.current_model_head_revision,
    '\''currentModelArtifactSha256'\'', publication.current_model_artifact_sha256,
    '\''targetApplicationVersion'\'', publication.target_application_version,
    '\''targetApplicationCommit'\'', publication.target_application_commit,
    '\''targetApplicationBuildTime'\'', publication.target_application_build_time,
    '\''targetCatalogSha256'\'', publication.catalog_sha256,
    '\''targetModelId'\'', publication.target_model_id::text,
    '\''targetModelArtifactSha256'\'', publication.target_model_artifact_sha256,
    '\''targetModelReleaseId'\'', publication.target_model_release_id::text
  )')" || database_match=""
    if [[ "$database_match" != 1 ]]; then
      fail "catalog publication receipt does not match one exact immutable database publication: $filename"
    fi
    receipt_current_revision["$publication_id"]="$current_model_head_revision"
    receipt_current_sha["$publication_id"]="$current_model_artifact_sha"
    receipt_configuration_revision["$publication_id"]="$configuration_head_revision"
    receipt_catalog_sha["$publication_id"]="$catalog_sha"
    receipt_model_sha["$publication_id"]="$model_artifact_sha"
    receipt_model_id["$publication_id"]="$model_id"
    receipt_target_release_id["$publication_id"]="$target_model_release_id"
    receipt_target_version["$publication_id"]="$target_application_version"
    receipt_target_commit["$publication_id"]="$target_application_commit"
    receipt_target_build_time["$publication_id"]="$target_application_build_time"
    receipt_count=$((receipt_count + 1))
  done <<<"$entries"
  rm -f -- "$canonical_receipt"

  filesystem_ids="$(printf '%s\n' "${!receipt_current_revision[@]}" | LC_ALL=C sort -n)"
  database_ids="$(run_runtime_psql -A -t -v ON_ERROR_STOP=1 -c '
/* ascendany-validator:catalog-publication-ids */
SELECT knowledge_catalog_publication_id::text
FROM ascendany.knowledge_catalog_publications
ORDER BY knowledge_catalog_publication_id')" || database_ids=""
  if [[ "$receipt_count" == 0 || "$filesystem_ids" != "$database_ids" ]]; then
    fail "catalog receipt publication-ID set differs from the immutable database publication-ID set"
    return
  fi
  target_model_id="$(jq -er '.manifest.modelId | select(type == "string")' "$release_root/models/recommendation-model.json" 2>/dev/null || true)"
  catalog_document_base64="$(base64 -w0 -- "$release_root/models/recommendation-knowledge-catalog.json")"
  if forward_transition; then
    expected_prior_revision="$expected_forward_model_head_revision"
    expected_prior_sha="$expected_forward_model_artifact_sha256"
  elif initial_transition && production_phase; then
    expected_prior_revision=1
    expected_prior_sha="$observed_forward_model_artifact_sha256"
  else
    expected_prior_revision="$observed_forward_model_head_revision"
    expected_prior_sha="$observed_forward_model_artifact_sha256"
  fi
  if [[ ! "$expected_prior_revision" =~ ^[1-9][0-9]*$ ||
        ! "$expected_prior_sha" =~ ^[0-9a-f]{64}$ ||
        ! "$target_model_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]; then
    fail "catalog publication target has no canonical prior model or release model identity"
    return
  fi
  target_state="$(run_runtime_psql_with_variables -A -t -F '|' -v ON_ERROR_STOP=1 \
    -v release_model_id="$target_model_id" \
    -v release_model_sha="$release_model_sha256" \
    -v release_catalog_sha="$release_catalog_sha256" \
    -v release_version="$release_manifest_version" \
    -v release_commit="$release_manifest_commit" \
    -v release_build_time="$release_manifest_build_time" \
    -v expected_prior_revision="$expected_prior_revision" \
    -v expected_prior_sha="$expected_prior_sha" \
    -v catalog_document_base64="$catalog_document_base64" -c '
/* ascendany-validator:catalog-publication-target */
SELECT count(*),
       COALESCE(min(publication.knowledge_catalog_publication_id), 0),
       COALESCE(min(publication.target_model_release_id), 0),
       COALESCE(min(publication.current_model_head_revision), 0),
       COALESCE(min(publication.current_model_artifact_sha256), '\'''\''),
       COALESCE(min(publication.configuration_head_revision), 0),
       count(activation.knowledge_catalog_publication_id),
       COALESCE(min(activation.head_revision), 0)
FROM ascendany.knowledge_catalog_publications AS publication
JOIN ascendany.recommendation_model_releases AS target_release
  ON target_release.recommendation_model_release_id = publication.target_model_release_id
 AND target_release.model_id = publication.target_model_id
 AND target_release.artifact_sha256 = publication.target_model_artifact_sha256
 AND target_release.knowledge_catalog_sha256 = publication.catalog_sha256
JOIN ascendany.configuration_items AS item
  ON item.configuration_item_id = publication.configuration_item_id
 AND item.active_version_id = publication.configuration_version_id
 AND item.head_revision = publication.configuration_head_revision
 AND item.configuration_key = '\''recommendation.catalog.active'\''
 AND item.configuration_kind = '\''knowledge_catalog'\''
JOIN ascendany.configuration_versions AS version
  ON version.configuration_item_id = publication.configuration_item_id
 AND version.configuration_version_id = publication.configuration_version_id
 AND version.configuration_kind = '\''knowledge_catalog'\''
 AND version.schema_id = '\''ascendany.knowledge_catalog.recommendation.v1'\''
 AND version.document_sha256 = publication.catalog_sha256
 AND version.document = convert_from(decode(:'\''catalog_document_base64'\'', '\''base64'\''), '\''UTF8'\'')::jsonb
 AND version.credential_ref IS NULL
LEFT JOIN ascendany.recommendation_model_activation_events AS activation
  ON activation.knowledge_catalog_publication_id = publication.knowledge_catalog_publication_id
WHERE publication.target_model_id = :'\''release_model_id'\''::uuid
  AND publication.target_model_artifact_sha256 = :'\''release_model_sha'\''
  AND publication.catalog_sha256 = :'\''release_catalog_sha'\''
  AND publication.target_application_version = :'\''release_version'\''
  AND publication.target_application_commit = :'\''release_commit'\''
  AND publication.target_application_build_time = :'\''release_build_time'\''
  AND publication.current_model_head_revision = :'\''expected_prior_revision'\''::bigint
  AND publication.current_model_artifact_sha256 = :'\''expected_prior_sha'\''')" || target_state=""
  IFS='|' read -r target_count target_publication_id target_release_id target_prior_revision target_prior_sha \
    target_configuration_head_revision target_consumption_count target_consumption_revision <<<"$target_state"
  if [[ "$target_count" != 1 || ! -v "receipt_current_revision[$target_publication_id]" ||
        "${receipt_current_revision[$target_publication_id]:-}" != "$target_prior_revision" ||
        "${receipt_current_sha[$target_publication_id]:-}" != "$target_prior_sha" ||
        "${receipt_configuration_revision[$target_publication_id]:-}" != "$target_configuration_head_revision" ||
        "${receipt_catalog_sha[$target_publication_id]:-}" != "$release_catalog_sha256" ||
        "${receipt_model_sha[$target_publication_id]:-}" != "$release_model_sha256" ||
        "${receipt_model_id[$target_publication_id]:-}" != "$target_model_id" ||
        "${receipt_target_release_id[$target_publication_id]:-}" != "$target_release_id" ||
        "${receipt_target_version[$target_publication_id]:-}" != "$release_manifest_version" ||
        "${receipt_target_commit[$target_publication_id]:-}" != "$release_manifest_commit" ||
        "${receipt_target_build_time[$target_publication_id]:-}" != "$release_manifest_build_time" ]]; then
    fail "release model/catalog/application target does not resolve to exactly one receipt-backed active publication"
    return
  fi

  expected_consumption_count=0
  expected_consumption_revision=0
  if { forward_transition && { activation_phase || production_phase; }; } ||
     { initial_transition && production_phase; }; then
    expected_consumption_count=1
    expected_consumption_revision=$((expected_prior_revision + 1))
  fi
  if [[ "$target_consumption_count" != "$expected_consumption_count" ||
        "$target_consumption_revision" != "$expected_consumption_revision" ]]; then
    fail "target catalog publication consumption differs from the selected model activation phase"
  fi

  activation_state="$(run_runtime_psql_with_variables -A -t -F '|' -v ON_ERROR_STOP=1 \
    -v target_publication_id="$target_publication_id" -c '
/* ascendany-validator:catalog-publication-activation-state */
WITH initial_publication AS (
  SELECT publication.knowledge_catalog_publication_id
  FROM ascendany.knowledge_catalog_publications AS publication
  JOIN ascendany.recommendation_model_activation_events AS initial_activation
    ON initial_activation.head_revision = 1
   AND initial_activation.recommendation_model_release_id = publication.target_model_release_id
   AND initial_activation.artifact_sha256 = publication.target_model_artifact_sha256
   AND initial_activation.application_version = publication.target_application_version
   AND initial_activation.application_commit = publication.target_application_commit
   AND initial_activation.application_build_time = publication.target_application_build_time
  WHERE publication.current_model_head_revision = 1
    AND publication.current_model_artifact_sha256 = initial_activation.artifact_sha256
), unexpected_unconsumed AS (
  SELECT publication.knowledge_catalog_publication_id
  FROM ascendany.knowledge_catalog_publications AS publication
  WHERE publication.knowledge_catalog_publication_id <> :'\''target_publication_id'\''::bigint
    AND NOT EXISTS (
      SELECT 1 FROM initial_publication AS initial
      WHERE initial.knowledge_catalog_publication_id = publication.knowledge_catalog_publication_id
    )
    AND NOT EXISTS (
      SELECT 1 FROM ascendany.recommendation_model_activation_events AS activation
      WHERE activation.knowledge_catalog_publication_id = publication.knowledge_catalog_publication_id
    )
)
SELECT (SELECT head_revision FROM ascendany.recommendation_model_head WHERE singleton),
       (SELECT COALESCE(pending_catalog_publication_id::text, '\'''\'') FROM ascendany.recommendation_model_head WHERE singleton),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events),
       (SELECT COALESCE(min(head_revision), 0) FROM ascendany.recommendation_model_activation_events),
       (SELECT COALESCE(max(head_revision), 0) FROM ascendany.recommendation_model_activation_events),
       (SELECT count(DISTINCT head_revision) FROM ascendany.recommendation_model_activation_events),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events
         WHERE (head_revision = 1 AND knowledge_catalog_publication_id IS NOT NULL)
            OR (head_revision > 1 AND knowledge_catalog_publication_id IS NULL)),
       (SELECT count(*) FROM initial_publication),
       (SELECT count(*) FROM ascendany.knowledge_catalog_publications),
       (SELECT count(*) FROM unexpected_unconsumed)')" || activation_state=""
  IFS='|' read -r active_head_revision pending_publication_id activation_count activation_min_revision \
    activation_max_revision activation_distinct_revision_count activation_invalid_count \
    initial_publication_count publication_count unexpected_unconsumed_count <<<"$activation_state"
  if forward_transition && catalog_phase; then
    expected_active_head_revision="$expected_prior_revision"
  elif forward_transition; then
    expected_active_head_revision=$((expected_prior_revision + 1))
  elif catalog_phase; then
    expected_active_head_revision="$expected_prior_revision"
  else
    expected_active_head_revision=$((expected_prior_revision + 1))
  fi
  if [[ "$active_head_revision" != "$expected_active_head_revision" ||
        "$activation_count" != "$active_head_revision" ||
        "$activation_min_revision" != 1 || "$activation_max_revision" != "$active_head_revision" ||
        "$activation_distinct_revision_count" != "$active_head_revision" ||
        "$activation_invalid_count" != 0 || "$initial_publication_count" != 1 ||
        "$publication_count" != "$receipt_count" || "$unexpected_unconsumed_count" != 0 ]]; then
    fail "catalog publications and model activation events violate the continuous one-publication-per-forward-activation state machine"
  fi
  if catalog_phase; then
    if [[ "$pending_publication_id" != "$target_publication_id" ]]; then
      fail "catalog phase model head does not reserve the exact target publication"
    fi
  elif [[ -n "$pending_publication_id" ]]; then
    fail "activated model head retains a pending catalog publication"
  fi
  if (( failures == failures_before )); then
    pass "every catalog publication has one canonical receipt, exact database/audit provenance, and deterministic activation ownership"
  fi
}

check_recommendation_model_binding() {
  local model_path="$release_root/models/recommendation-model.json"
  local model_binary="$release_root/bin/ascendany-model"
  local model_id model_size result expected_next_revision=""
  local stored_model_id stored_sha stored_size stored_mode stored_schema stored_purpose stored_algorithm stored_contract
  local stored_catalog_sha stored_revision pending_publication_id event_sha event_version event_commit event_build_time
  local catalog_kind_count catalog_key_count catalog_key catalog_kind catalog_head_revision
  local catalog_version_number catalog_schema catalog_document_sha catalog_credential_ref

  if [[ ! "$release_model_sha256" =~ ^[0-9a-f]{64}$ ]]; then
    fail "release manifest has no canonical recommendation model digest"
    return
  fi
  if ! /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
      "$model_binary" verify --model "$model_path" --sha256 "$release_model_sha256" --expected-purpose production; then
    fail "release recommendation model failed canonical semantic verification"
    return
  fi
  model_id="$(jq -er '.manifest.modelId | select(type == "string" and test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"))' "$model_path" 2>/dev/null || true)"
  model_size="$(stat -Lc '%s' -- "$model_path" 2>/dev/null || true)"
  if [[ -z "$model_id" || ! "$model_size" =~ ^[1-9][0-9]*$ || "$model_size" -gt 16777216 ]]; then
    fail "release recommendation model identity or bounded size is invalid"
    return
  fi
  pass "release recommendation model is canonical and bound to its manifest SHA-256"

  if initial_fresh_phase; then
    if ! result="$(run_runtime_psql -A -t -F '|' -v ON_ERROR_STOP=1 <<'SQL'
SELECT (SELECT count(*) FROM ascendany.recommendation_model_releases),
       (SELECT count(*) FROM ascendany.recommendation_model_head),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events);
SQL
    )"; then
      fail "initial preactivation recommendation model database evidence query failed"
      return
    fi
    if [[ "$result" != "0|0|0" ]]; then
      fail "initial preactivation database contains recommendation model release, head, or activation state"
      return
    fi
    pass "initial preactivation database contains no recommendation model release, head, or activation state"
    return
  fi
  if ! result="$(run_runtime_psql -A -t -F '|' -v ON_ERROR_STOP=1 <<'SQL'
SELECT model.model_id::text,
       model.artifact_sha256,
       model.artifact_size_bytes,
       model.artifact_mode,
       model.model_schema,
       model.model_purpose,
       model.algorithm,
       model.inference_contract,
       model.knowledge_catalog_sha256,
       head.head_revision,
       COALESCE(head.pending_catalog_publication_id::text, ''),
       event.artifact_sha256,
       event.application_version,
       event.application_commit,
       event.application_build_time,
       (SELECT count(*) FROM ascendany.configuration_items WHERE configuration_kind = 'knowledge_catalog'),
       (SELECT count(*) FROM ascendany.configuration_items WHERE configuration_key = 'recommendation.catalog.active'),
       catalog_item.configuration_key,
       catalog_item.configuration_kind,
       catalog_item.head_revision,
       catalog_version.version_number,
       catalog_version.schema_id,
       catalog_version.document_sha256,
       COALESCE(catalog_version.credential_ref, '')
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS model
  ON model.recommendation_model_release_id = head.current_release_id
JOIN ascendany.recommendation_model_activation_events AS event
  ON event.head_revision = head.head_revision
 AND event.recommendation_model_release_id = head.current_release_id
LEFT JOIN ascendany.configuration_items AS catalog_item
  ON catalog_item.configuration_key = 'recommendation.catalog.active'
LEFT JOIN ascendany.configuration_versions AS catalog_version
  ON catalog_version.configuration_item_id = catalog_item.configuration_item_id
 AND catalog_version.configuration_version_id = catalog_item.active_version_id
WHERE head.singleton
SQL
  )"; then
    fail "durable recommendation model binding query failed; schema v10 model activation is required"
    return
  fi
  IFS='|' read -r stored_model_id stored_sha stored_size stored_mode stored_schema stored_purpose stored_algorithm stored_contract \
    stored_catalog_sha stored_revision pending_publication_id event_sha event_version event_commit event_build_time \
    catalog_kind_count catalog_key_count catalog_key catalog_kind catalog_head_revision \
    catalog_version_number catalog_schema catalog_document_sha catalog_credential_ref <<<"$result"
  if catalog_phase; then
    if [[ ! "$pending_publication_id" =~ ^[1-9][0-9]*$ ]]; then
      fail "catalog phase model head has no canonical pending publication identity"
      return
    fi
  elif [[ -n "$pending_publication_id" ]]; then
    fail "non-catalog validation phase found a pending model-head publication"
    return
  fi
  if initial_transition && [[ "$validation_phase" == activation ]] &&
     [[ "$catalog_kind_count:$catalog_key_count" == "0:0" &&
        -z "$catalog_key$catalog_kind$catalog_head_revision$catalog_version_number$catalog_schema$catalog_document_sha$catalog_credential_ref" ]]; then
    pass "initial activation records the release model before the isolated knowledge-catalog initialization window"
  elif catalog_phase; then
    if [[ "$catalog_kind_count:$catalog_key_count" != "1:1" ||
          "$catalog_key" != "recommendation.catalog.active" || "$catalog_kind" != knowledge_catalog ||
          ! "$catalog_head_revision" =~ ^[1-9][0-9]*$ ||
          "$catalog_version_number" != "$catalog_head_revision" ||
          "$catalog_schema" != "ascendany.knowledge_catalog.recommendation.v1" ||
          -n "$catalog_credential_ref" || ! "$catalog_document_sha" =~ ^[0-9a-f]{64}$ ||
          "$catalog_document_sha" != "$release_catalog_sha256" ]]; then
      fail "catalog phase active knowledge catalog differs from the immutable release catalog"
      return
    fi
    pass "catalog phase active knowledge catalog binds the immutable release catalog"
  else
    if [[ "$catalog_kind_count:$catalog_key_count" != "1:1" ||
          "$catalog_key" != "recommendation.catalog.active" || "$catalog_kind" != knowledge_catalog ||
          ! "$catalog_head_revision" =~ ^[1-9][0-9]*$ ||
          "$catalog_version_number" != "$catalog_head_revision" ||
          "$catalog_schema" != "ascendany.knowledge_catalog.recommendation.v1" ||
          -n "$catalog_credential_ref" || ! "$catalog_document_sha" =~ ^[0-9a-f]{64}$ ||
          "$catalog_document_sha" != "$stored_catalog_sha" ]]; then
      fail "active knowledge catalog identity, provenance, or digest differs from the active recommendation model"
      return
    fi
    pass "one fixed active knowledge catalog binds the active recommendation model digest"
  fi

  if forward_preactivation_phase || { forward_transition && catalog_phase; }; then
    if [[ ! "$stored_model_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ||
          ! "$stored_sha" =~ ^[0-9a-f]{64}$ || ! "$stored_size" =~ ^[1-9][0-9]*$ ||
          "$stored_mode" != 420 || "$stored_schema" != "ascendany.recommendation.inference-model.v1" ||
          "$stored_purpose" != production || "$stored_algorithm" != "knowledge_mirt_feature_v1" ||
          "$stored_contract" != "ascendany.recommendation.inference.v1" ||
          ! "$stored_catalog_sha" =~ ^[0-9a-f]{64}$ || ! "$stored_revision" =~ ^[1-9][0-9]*$ ||
          "$event_sha" != "$stored_sha" || ! "$event_commit" =~ ^[0-9a-f]{40}$ ||
          -z "$event_version" || -z "$event_build_time" ]]; then
      fail "forward preactivation database lacks one internally consistent retained model head and activation"
      return
    fi
    if [[ "$event_version" == "$release_manifest_version" &&
          "$event_commit" == "$release_manifest_commit" &&
          "$event_build_time" == "$release_manifest_build_time" ]]; then
      fail "forward preactivation database already contains the replacement application activation"
      return
    fi
    observed_forward_model_head_revision="$stored_revision"
    observed_forward_model_artifact_sha256="$stored_sha"
    if [[ ( "$validation_phase" == smoke || "$validation_phase" == catalog ) &&
          ( "$stored_revision" != "$expected_forward_model_head_revision" ||
            "$stored_sha" != "$expected_forward_model_artifact_sha256" ) ]]; then
      fail "forward $validation_phase changed the retained recommendation model head or artifact"
      return
    fi
    pass "forward preactivation retains the prior internally consistent model head and activation"
    return
  fi

  if forward_transition; then
    expected_next_revision="$(decimal_increment "$expected_forward_model_head_revision")"
  fi
  if [[ "$stored_model_id" != "$model_id" || "$stored_sha" != "$release_model_sha256" ||
        "$stored_size" != "$model_size" || "$stored_mode" != 420 ||
        "$stored_schema" != "ascendany.recommendation.inference-model.v1" ||
        "$stored_purpose" != production || "$stored_purpose" != "$release_manifest_purpose" ||
        "$stored_algorithm" != "knowledge_mirt_feature_v1" ||
        "$stored_contract" != "ascendany.recommendation.inference.v1" ||
        ! "$stored_revision" =~ ^[1-9][0-9]*$ ||
        "$event_sha" != "$stored_sha" || "$event_version" != "$release_manifest_version" ||
        "$event_commit" != "$release_manifest_commit" || "$event_build_time" != "$release_manifest_build_time" ||
        ( -n "$expected_next_revision" && "$stored_revision" != "$expected_next_revision" ) ]]; then
    fail "active database recommendation model head differs from the immutable release model and activation event"
  else
    observed_forward_model_head_revision="$stored_revision"
    observed_forward_model_artifact_sha256="$stored_sha"
    pass "active database recommendation model head binds the immutable release model and activation event"
  fi
}

check_cloudflared_connector() {
  if ASCENDANY_VALIDATION_PHASE="$validation_phase" \
      "$release_root/scripts/validate-cloudflared.sh"; then
    pass "cloudflared connector matches the release-owned $validation_phase gate"
  else
    fail "cloudflared connector failed the release-owned $validation_phase gate"
  fi
}

port_is_managed() {
  local candidate="$1" port
  for port in $managed_ports; do
    [[ "$candidate" == "$port" ]] && return 0
  done
  return 1
}

check_loopback_ports() {
  local state recvq sendq endpoint peer rest port address required
  declare -A seen=()
  while read -r state recvq sendq endpoint peer rest; do
    [[ -n "${endpoint:-}" ]] || continue
    port="${endpoint##*:}"
    port_is_managed "$port" || continue
    address="${endpoint%:*}"
    address="${address#[}"
    address="${address%]}"
    address="${address%%%*}"
    seen["$port"]=1
    if [[ "$address" == "::1" || "$address" == 127.* ]]; then
      pass "TCP $port listens on loopback $address"
    else
      fail "managed TCP $port listens on non-loopback address $address"
    fi
  done < <(ss -H -ltn)

  for required in $required_ports; do
    if [[ -z "${seen[$required]:-}" ]]; then
      fail "required loopback TCP port is not listening: $required"
    fi
  done
  if [[ ( "$validation_phase" == "staged" || "$validation_phase" == "catalog" ||
          "$validation_phase" == "activation" ) &&
        -n "${seen[18000]:-}" ]]; then
    fail "$validation_phase phase requires v2 TCP port 18000 to be unused"
  fi
}

main() {
  local command
  if ! validate_input_contract; then
    printf 'Production validation failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  for command in systemctl systemd-creds realpath find stat psql ss grep getent id runuser jq curl sha256sum base64 awk cmp date dirname sed sort tail tr mktemp chmod readlink podman rpm; do
    require_command "$command" || true
  done

  check_release_for_secret_files
  check_release_payload
  if (( failures > 0 )) || [[ "$release_payload_verified" != "1" ]]; then
    printf 'Production validation stopped before executing release-owned code because release verification failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  check_initialization_operator_runtime

  check_cloudflared_connector

  check_system_manager_environment
  check_retired_runtime_boundary
  check_retired_generation_closure
  check_unit_identity ascendanyd.service ascendany ascendany ascendany-runtime ascendany-lsp-control
  check_unit_identity ascendany-model-register.service ascendany ascendany ascendany-runtime
  check_unit_identity ascendany-model-activate.service ascendany ascendany ascendany-runtime
  check_unit_identity ascendany-catalog-publish.service ascendany-catalog-publisher ascendany-catalog-readers
  check_unit_identity ascendany-admin-bootstrap.service ascendany ascendany ascendany-runtime
  check_unit_identity ascendany-judge@validation.service ascendany-judge ascendany-judge ascendany-runtime
  check_unit_identity ascendany-lsp@validation.service ascendany-lsp ascendany-lsp ascendany-lsp-control
  check_unit_identity ascendany-backup.service ascendany-backup ascendany-backup-readers ascendany
  check_unit_identity ascendany-migrate.service ascendany-migrator ascendany-migrator
  check_unit_identity ascendany-restore-verify@validation.service ascendany-restore ascendany-restore ascendany-backup-readers
  check_all_unit_effective_shapes

  check_ascendanyd_phase_state
  check_model_registration_unit_state
  check_model_activation_unit_state

  check_worker_isolation ascendany-judge@validation.service
  check_worker_isolation ascendany-lsp@validation.service
  check_unit_environment_files ascendany-judge@validation.service /etc/ascendany/v2/judge.env
  check_unit_environment_files ascendany-lsp@validation.service
  check_unit_credentials ascendany-backup.service backup_db_password
  check_unit_environment_files ascendany-backup.service /etc/ascendany/v2/backup.env
  check_unit_credentials ascendany-model-register.service db_password
  check_unit_environment_files ascendany-model-register.service /etc/ascendany/v2/ascendanyd.env
  check_unit_credentials ascendany-model-activate.service db_password
  check_unit_environment_files ascendany-model-activate.service /etc/ascendany/v2/ascendanyd.env
  check_catalog_publisher_unit
  check_unit_environment_files ascendany-catalog-publish.service /etc/ascendany-catalog-publisher/catalog-publish.env
  check_admin_bootstrap_unit
  check_unit_optional_environment_files ascendany-admin-bootstrap.service /etc/ascendany/v2/ascendanyd.env
  check_unit_credentials ascendany-migrate.service migrator_db_password
  check_unit_environment_files ascendany-migrate.service /etc/ascendany/v2/migrate.env
  check_unit_credentials ascendany-restore-verify@validation.service restore_db_password
  check_unit_environment_files ascendany-restore-verify@validation.service /etc/ascendany/v2/restore.env
  check_lsp_runtime
  check_judge_runtime
  check_credentials
  check_jwt_keypair_credentials
  check_ascendanyd_config_contract
  check_catalog_publisher_config_contract
  check_active_ascendanyd_process
  check_active_ascendanyd_health
  check_installed_release_inputs
  check_provisioning_terminal_state
  check_pgbouncer_contract
  check_artifact_root
  check_catalog_publisher_state_root
  check_catalog_publisher_capabilities
  check_initial_empty_durable_state
  check_database_role
  check_postgresql_access_contract
  check_postgres_schema_fingerprint
  check_initial_database_state
  check_admin_bootstrap_database
  check_agent_acceptance_receipt
  check_recommendation_model_binding
  check_catalog_publication_binding
  check_forward_database_state
  if production_phase; then
    check_backup_schedule
  elif forward_retained_backup_phase; then
    check_inactive_backup_timer
    check_backup_schedule
  else
    check_inactive_backup_timer
  fi
  check_loopback_ports

  if (( failures > 0 )); then
    printf 'Production validation failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi

  if forward_transition && [[ "$validation_phase" == staged || "$validation_phase" == catalog ]]; then
    printf 'ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256=%s\n' "$observed_forward_database_fingerprint"
    printf 'ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256=%s\n' "$observed_forward_business_fingerprint"
    printf 'ASCENDANY_FORWARD_MODEL_HEAD_REVISION=%s\n' "$observed_forward_model_head_revision"
    printf 'ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256=%s\n' "$observed_forward_model_artifact_sha256"
  fi
  printf 'AscendAny %s %s validation passed.\n' "$deployment_transition" "$validation_phase"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
