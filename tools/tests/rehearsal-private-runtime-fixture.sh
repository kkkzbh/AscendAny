#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly postgres_rehearsal="${repository_root}/tools/run-v2-postgres-podman-rehearsal.sh"
readonly postgres_integration="${repository_root}/tools/run-v2-postgres-integration.sh"
readonly backup_rehearsal="${repository_root}/tools/run-v2-backup-restore-podman-rehearsal.sh"
readonly recommendation_catalog_fixture="${repository_root}/contracts/recommendation/fixtures/synthetic-test-only.knowledge-catalog.v1.json"
readonly recommendation_catalog_sha256="a58370ec66def22b13a0bd64acf195e9fa28530e81481e7ade2545aaaa9bfe3c"
readonly recommendation_model_fixture="${repository_root}/contracts/recommendation/fixtures/synthetic-test-only.inference-model.v1.json"
readonly recommendation_model_sha256="5182ed451d74a4e10d8384f3a4d9fcb2a8d2ad7d043e3721f2247e10c029bf58"

fixture_root=""
private_fixture_root=""
runtime_fixture_roots=()

cleanup_fixture() {
  local path
  for path in "${runtime_fixture_roots[@]}"; do
    if [[ -n "${path}" ]]; then
      rm -rf -- "${path}"
    fi
  done
  if [[ -n "${private_fixture_root}" ]]; then
    rm -rf -- "${private_fixture_root}"
  fi
  if [[ -n "${fixture_root}" ]]; then
    rm -rf -- "${fixture_root}"
  fi
}
trap cleanup_fixture EXIT

fail() {
  printf 'fixture failure: %s\n' "$1" >&2
  exit 1
}

require_fixed() {
  local file="$1"
  local expected="$2"
  grep -Fq -- "${expected}" "${file}" ||
    fail "${file} does not contain the required contract marker: ${expected}"
}

require_regex() {
  local file="$1"
  local expected="$2"
  grep -Eq -- "${expected}" "${file}" ||
    fail "${file} does not implement the required contract pattern: ${expected}"
}

line_number() {
  local file="$1"
  local marker="$2"
  local -a matches=()
  mapfile -t matches < <(grep -nF -- "${marker}" "${file}")
  [[ "${#matches[@]}" == "1" ]] ||
    fail "${file} does not contain exactly one ordered marker: ${marker}"
  printf '%s\n' "${matches[0]%%:*}"
}

cleanup_body() {
  local file="$1"
  awk '
    /^cleanup\(\) \{$/ { capture = 1 }
    capture { print }
    capture && /^}$/ { exit }
  ' "${file}"
}

contains_ambient_work_root_fallback() {
  local file="$1"
  local boundary_start
  local boundary_end
  boundary_start="$(line_number "${file}" 'readonly PRIVATE_RUNTIME_ROOT=')"
  boundary_end="$(line_number "${file}" 'WORK_ROOT="$(mktemp')"
  ((boundary_start < boundary_end)) ||
    fail "${file} defines WORK_ROOT before validating the private runtime root"
  sed -n "${boundary_start},${boundary_end}p" "${file}" |
    grep -Fq -- '${TMPDIR:-/tmp}'
}

append_required_rehearsal_inputs() {
  local script="$1"
  local arguments_name="$2"
  local -n target_arguments="${arguments_name}"
  if [[ "${script}" == "${backup_rehearsal}" ]]; then
    target_arguments+=(
      --recommendation-model "${recommendation_model_fixture}"
      --recommendation-model-sha256 "${recommendation_model_sha256}"
      --recommendation-catalog "${recommendation_catalog_fixture}"
      --recommendation-catalog-sha256 "${recommendation_catalog_sha256}"
    )
  fi
}

for command_name in awk chmod env grep id ln mapfile mktemp realpath rm sed stat; do
  command -v "${command_name}" >/dev/null 2>&1 ||
    fail "required command is unavailable: ${command_name}"
done
unset command_name

((EUID != 0)) || fail 'the executable fixture must run as a non-root user'
[[ -f "${postgres_rehearsal}" && -f "${postgres_integration}" &&
  -f "${backup_rehearsal}" && -f "${recommendation_catalog_fixture}" &&
  -f "${recommendation_model_fixture}" ]] ||
  fail 'one or more rehearsal scripts are unavailable'

static_boundary_fixture_root="$(mktemp -d "${repository_root}/.ascendany-work-root-boundary-fixture.XXXXXX")"
runtime_fixture_roots+=("${static_boundary_fixture_root}")
readonly safe_boundary_fixture="${static_boundary_fixture_root}/safe-isolated-tmpdir.sh"
readonly unsafe_boundary_fixture="${static_boundary_fixture_root}/unsafe-work-root-fallback.sh"
printf '%s\n' \
  'readonly PRIVATE_RUNTIME_ROOT="$(realpath -e -- "${XDG_RUNTIME_DIR}")"' \
  'readonly WORK_ROOT_PREFIX="${PRIVATE_RUNTIME_ROOT}/ascendany-v2-safe."' \
  'WORK_ROOT="$(mktemp -d "${WORK_ROOT_PREFIX}XXXXXX")"' \
  '/usr/bin/env -i \' \
  '  TMPDIR="${TMPDIR:-/tmp}" \' \
  '  go test ./...' \
  >"${safe_boundary_fixture}"
printf '%s\n' \
  'readonly PRIVATE_RUNTIME_ROOT="$(realpath -e -- "${XDG_RUNTIME_DIR}")"' \
  'readonly WORK_ROOT_PREFIX="${TMPDIR:-/tmp}/ascendany-v2-unsafe."' \
  'WORK_ROOT="$(mktemp -d "${WORK_ROOT_PREFIX}XXXXXX")"' \
  >"${unsafe_boundary_fixture}"
if contains_ambient_work_root_fallback "${safe_boundary_fixture}"; then
  fail 'isolated Go TMPDIR was misclassified as an ambient work-root fallback'
fi
printf 'PASS fixture static-work-root-boundary-safe-tmpdir-env\n'
if ! contains_ambient_work_root_fallback "${unsafe_boundary_fixture}"; then
  fail 'ambient TMPDIR work-root fallback escaped the scoped static check'
fi
printf 'PASS fixture static-work-root-boundary-rejects-ambient-fallback\n'

readonly expected_canonical_error='XDG_RUNTIME_DIR must identify an absolute canonical directory'
readonly expected_owner_mode_error='XDG_RUNTIME_DIR must be owned by the rehearsal user with mode 0700'
readonly expected_tmpfs_error='XDG_RUNTIME_DIR must use tmpfs'

for rehearsal in "${postgres_rehearsal}" "${backup_rehearsal}"; do
  if contains_ambient_work_root_fallback "${rehearsal}"; then
    fail "${rehearsal} reintroduced the ambient TMPDIR work-root fallback"
  fi

  require_fixed "${rehearsal}" 'PRIVATE_RUNTIME_ROOT'
  require_fixed "${rehearsal}" 'WORK_ROOT_PREFIX'
  require_fixed "${rehearsal}" "${expected_canonical_error}"
  require_fixed "${rehearsal}" "${expected_owner_mode_error}"
  require_fixed "${rehearsal}" "${expected_tmpfs_error}"
  require_regex "${rehearsal}" 'realpath -e -- "\$\{(XDG_RUNTIME_DIR|PRIVATE_RUNTIME_ROOT)\}"'
  require_fixed "${rehearsal}" 'stat -Lc '\''%u:%a'\'' -- "${PRIVATE_RUNTIME_ROOT}"'
  require_regex "${rehearsal}" 'stat -f -L?c '\''%T'\'' -- "\$\{PRIVATE_RUNTIME_ROOT\}"'
  require_regex "${rehearsal}" 'WORK_ROOT_PREFIX=.*\$\{PRIVATE_RUNTIME_ROOT\}/ascendany-v2-'
  require_regex "${rehearsal}" 'WORK_ROOT=.*mktemp -d "\$\{WORK_ROOT_PREFIX\}XXXXXX"'
  require_fixed "${rehearsal}" 'POD_CREATE_ATTEMPTED=0'

  trap_line="$(line_number "${rehearsal}" 'trap cleanup EXIT')"
  allocation_line="$(line_number "${rehearsal}" 'WORK_ROOT="$(mktemp')"
  ((trap_line < allocation_line)) ||
    fail "${rehearsal} installs cleanup after private work-root allocation"

  body="$(cleanup_body "${rehearsal}")"
  [[ -n "${body}" ]] || fail "${rehearsal} has no inspectable cleanup function"
  grep -Fq -- 'realpath -e -- "${WORK_ROOT}"' <<<"${body}" ||
    fail "${rehearsal} cleanup does not canonicalize the owned work root"
  grep -Fq -- 'stat -Lc '\''%u'\'' -- "${WORK_ROOT}"' <<<"${body}" ||
    fail "${rehearsal} cleanup does not re-check work-root ownership"
  grep -Fq -- '"${WORK_ROOT}" != "${WORK_ROOT_PREFIX}"*' <<<"${body}" ||
    fail "${rehearsal} cleanup does not enforce the exact randomized-name prefix boundary"
  grep -Fq -- 'rm -rf --one-file-system -- "${WORK_ROOT}"' <<<"${body}" ||
    fail "${rehearsal} cleanup does not remove the verified owned work root"
  grep -Fq -- 'POD_CREATE_ATTEMPTED == 1' <<<"${body}" ||
    fail "${rehearsal} cleanup does not reconcile a partial pod-create attempt"

  create_attempt_line="$(line_number "${rehearsal}" 'POD_CREATE_ATTEMPTED=1')"
  pod_create_line="$(line_number "${rehearsal}" 'if podman pod create')"
  ((create_attempt_line < pod_create_line)) ||
    fail "${rehearsal} records the pod-create attempt after the mutating call"

  printf 'PASS fixture static-private-runtime-contract script=%s\n' "$(basename -- "${rehearsal}")"
done
unset rehearsal trap_line allocation_line body create_attempt_line pod_create_line

require_fixed \
  "${postgres_rehearsal}" \
  'ASCENDANY_CI_RECOMMENDATION_CATALOG_SHA256="${RECOMMENDATION_CATALOG_SHA256}"'
require_fixed \
  "${postgres_integration}" \
  'readonly RECOMMENDATION_CATALOG_SHA256="$(required_environment ASCENDANY_CI_RECOMMENDATION_CATALOG_SHA256)"'
require_fixed \
  "${postgres_integration}" \
  'ASCENDANY_TEST_RECOMMENDATION_CATALOG_SHA256="${RECOMMENDATION_CATALOG_SHA256}"'
require_fixed \
  "${postgres_integration}" \
  'runtime|./internal/catalogartifact|TestPostgresCatalogArtifactLoadBoundary|none'
printf 'PASS fixture recommendation-catalog-trust-anchor-propagation\n'

backup_run_tmpfs_line="$(line_number "${backup_rehearsal}" '--tmpfs /run \')"
backup_work_root_bind_line="$(line_number \
  "${backup_rehearsal}" \
  '--bind "${WORK_ROOT}" "${WORK_ROOT}" \')"
((backup_run_tmpfs_line < backup_work_root_bind_line)) ||
  fail 'backup private-runtime bwrap masks WORK_ROOT with the later /run tmpfs mount'
printf 'PASS fixture backup-bwrap-runtime-mount-order\n'
unset backup_run_tmpfs_line backup_work_root_bind_line

# Use the real per-user runtime only as fixture input. Every rejected execution
# replaces Podman with an exported probe function. The probe permits only the
# preflight rootless/image reads and rejects every resource operation, so no
# daemon or container is started by this fixture.
readonly host_runtime="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
[[ "${host_runtime}" == /* && -d "${host_runtime}" && ! -L "${host_runtime}" ]] ||
  fail 'the host does not provide a canonical per-user runtime directory'
[[ "$(realpath -e -- "${host_runtime}")" == "${host_runtime}" ]] ||
  fail 'the host per-user runtime directory is not canonical'
[[ "$(stat -Lc '%u:%a' -- "${host_runtime}")" == "${EUID}:700" ]] ||
  fail 'the host per-user runtime directory does not have the expected owner and mode'
[[ "$(stat -f -Lc '%T' -- "${host_runtime}")" == 'tmpfs' ]] ||
  fail 'the host per-user runtime directory is not backed by tmpfs'

fixture_root="$(mktemp -d "${repository_root}/.ascendany-rehearsal-runtime-fixture.XXXXXX")"
chmod 0700 -- "${fixture_root}"
private_fixture_root="$(mktemp -d "${host_runtime}/ascendany-rehearsal-runtime-fixture.XXXXXX")"
chmod 0750 -- "${private_fixture_root}"

readonly non_tmpfs_runtime="${fixture_root}/non-tmpfs-runtime"
readonly symlink_runtime="${fixture_root}/runtime-link"
mkdir -m 0700 -- "${non_tmpfs_runtime}"
ln -s -- "${host_runtime}" "${symlink_runtime}"

run_rejected_runtime() {
  local script="$1"
  local confirmation="$2"
  local runtime_kind="$3"
  local runtime_value="$4"
  local expected_error="$5"
  local probe_marker="${fixture_root}/podman-probe-$(basename -- "${script}")-${runtime_kind}"
  local output
  local status
  local -a arguments=(--confirm-reset "${confirmation}")
  append_required_rehearsal_inputs "${script}" arguments

  set +e
  if [[ "${runtime_kind}" == 'unset' ]]; then
    output="$({
      env -u XDG_RUNTIME_DIR \
        PODMAN_PROBE_MARKER="${probe_marker}" \
        /usr/bin/bash -c '
          ss() { return 0; }
          podman() {
            if [[ "$1" == "info" && "$2" == "--format" ]]; then
              printf true
              return 0
            fi
            if [[ "$1" == "image" && "$2" == "exists" ]]; then
              return 0
            fi
            : >"${PODMAN_PROBE_MARKER}"
            return 97
          }
          export -f podman ss
          exec /usr/bin/bash "$@"
        ' _ "${script}" "${arguments[@]}"
    } 2>&1)"
    status=$?
  else
    output="$({
      env \
        XDG_RUNTIME_DIR="${runtime_value}" \
        PODMAN_PROBE_MARKER="${probe_marker}" \
        /usr/bin/bash -c '
          ss() { return 0; }
          podman() {
            if [[ "$1" == "info" && "$2" == "--format" ]]; then
              printf true
              return 0
            fi
            if [[ "$1" == "image" && "$2" == "exists" ]]; then
              return 0
            fi
            : >"${PODMAN_PROBE_MARKER}"
            return 97
          }
          export -f podman ss
          exec /usr/bin/bash "$@"
        ' _ "${script}" "${arguments[@]}"
    } 2>&1)"
    status=$?
  fi
  set -e

  [[ "${status}" == '2' ]] ||
    fail "$(basename -- "${script}") ${runtime_kind} runtime failed with status ${status}: ${output}"
  grep -Fqx -- "${expected_error}" <<<"${output}" ||
    fail "$(basename -- "${script}") ${runtime_kind} runtime did not fail with the exact contract error: ${output}"
  [[ ! -e "${probe_marker}" ]] ||
    fail "$(basename -- "${script}") attempted a Podman resource operation while rejecting ${runtime_kind} runtime"

  printf 'PASS fixture rejected-private-runtime script=%s case=%s\n' \
    "$(basename -- "${script}")" "${runtime_kind}"
}

run_rejected_catalog_contract() {
  local case_name="$1"
  local expected_error="$2"
  shift 2
  local probe_marker="${fixture_root}/podman-probe-catalog-${case_name}"
  local output
  local status

  set +e
  output="$({
    env \
      XDG_RUNTIME_DIR="${host_runtime}" \
      PODMAN_PROBE_MARKER="${probe_marker}" \
      /usr/bin/bash -c '
        ss() { return 0; }
        podman() {
          if [[ "$1" == "info" && "$2" == "--format" ]]; then
            printf true
            return 0
          fi
          if [[ "$1" == "image" && "$2" == "exists" ]]; then
            return 0
          fi
          : >"${PODMAN_PROBE_MARKER}"
          return 97
        }
        export -f podman ss
        exec /usr/bin/bash "$@"
      ' _ \
        "${postgres_rehearsal}" \
        --confirm-reset drop-disposable-ascendany-v2 \
        "$@"
  } 2>&1)"
  status=$?
  set -e

  [[ "${status}" == '2' ]] ||
    fail "catalog ${case_name} failed with status ${status}: ${output}"
  grep -Fqx -- "${expected_error}" <<<"${output}" ||
    fail "catalog ${case_name} did not fail with the exact contract error: ${output}"
  [[ ! -e "${probe_marker}" ]] ||
    fail "catalog ${case_name} attempted a Podman resource operation"

  printf 'PASS fixture rejected-recommendation-catalog case=%s\n' "${case_name}"
}

run_failed_allocation() {
  local script="$1"
  local confirmation="$2"
  local cleanup_marker="$3"
  local tag="$4"
  local mktemp_marker="${fixture_root}/mktemp-failure-${tag}"
  local podman_marker="${fixture_root}/podman-resource-${tag}"
  local output
  local status
  local -a arguments=(--confirm-reset "${confirmation}")
  append_required_rehearsal_inputs "${script}" arguments

  set +e
  output="$({
    env \
      XDG_RUNTIME_DIR="${host_runtime}" \
      MKTEMP_FAILURE_MARKER="${mktemp_marker}" \
      PODMAN_RESOURCE_MARKER="${podman_marker}" \
      /usr/bin/bash -c '
        ss() { return 0; }
        mktemp() {
          : >"${MKTEMP_FAILURE_MARKER}"
          return 73
        }
        podman() {
          if [[ "$1" == "info" && "$2" == "--format" ]]; then
            printf true
            return 0
          fi
          if [[ "$1" == "image" && "$2" == "exists" ]]; then
            return 0
          fi
          : >"${PODMAN_RESOURCE_MARKER}"
          return 97
        }
        export -f mktemp podman ss
        exec /usr/bin/bash "$@"
      ' _ "${script}" "${arguments[@]}"
  } 2>&1)"
  status=$?
  set -e

  [[ "${status}" == '73' ]] ||
    fail "$(basename -- "${script}") did not preserve the failed allocation status: ${status}: ${output}"
  [[ -f "${mktemp_marker}" ]] ||
    fail "$(basename -- "${script}") allocation failure was not exercised"
  [[ ! -e "${podman_marker}" ]] ||
    fail "$(basename -- "${script}") attempted a Podman resource operation after failed allocation"
  grep -Fq -- "${cleanup_marker}" <<<"${output}" ||
    fail "$(basename -- "${script}") failed allocation did not execute its pre-installed cleanup trap"

  printf 'PASS fixture failed-allocation-cleanup script=%s\n' "$(basename -- "${script}")"
}

run_cleanup_boundary_case() {
  local script="$1"
  local confirmation="$2"
  local cleanup_marker="$3"
  local work_root_prefix_basename="$4"
  local case_name="$5"
  local expected_action="$6"
  local work_root
  local unsupported_marker="${fixture_root}/unsupported-podman-${case_name}"
  local output
  local status
  local -a arguments=(--confirm-reset "${confirmation}")
  append_required_rehearsal_inputs "${script}" arguments

  if [[ "${expected_action}" == 'remove' ]]; then
    work_root="$(mktemp -d "${host_runtime}/${work_root_prefix_basename}XXXXXX")"
    runtime_fixture_roots+=("${work_root}")
  else
    work_root="$(mktemp -d "${fixture_root}/outside-${case_name}.XXXXXX")"
  fi
  printf 'controlled fixture sentinel\n' >"${work_root}/sentinel"

  set +e
  output="$({
    env \
      XDG_RUNTIME_DIR="${host_runtime}" \
      FAKE_WORK_ROOT="${work_root}" \
      UNSUPPORTED_PODMAN_MARKER="${unsupported_marker}" \
      /usr/bin/bash -c '
        ss() { return 0; }
        mktemp() {
          printf "%s\n" "${FAKE_WORK_ROOT}"
        }
        podman() {
          if [[ "$1" == "info" && "$2" == "--format" ]]; then
            printf true
            return 0
          fi
          if [[ "$1" == "image" && "$2" == "exists" ]]; then
            return 0
          fi
          if [[ "$1" == "ps" ]]; then
            return 97
          fi
          : >"${UNSUPPORTED_PODMAN_MARKER}"
          return 98
        }
        export -f mktemp podman ss
        exec /usr/bin/bash "$@"
      ' _ "${script}" "${arguments[@]}"
  } 2>&1)"
  status=$?
  set -e

  [[ "${status}" == '97' ]] ||
    fail "$(basename -- "${script}") ${case_name} did not stop at the injected post-allocation failure: ${status}: ${output}"
  [[ ! -e "${unsupported_marker}" ]] ||
    fail "$(basename -- "${script}") ${case_name} attempted an unexpected Podman operation"
  grep -Fq -- "${cleanup_marker}" <<<"${output}" ||
    fail "$(basename -- "${script}") ${case_name} did not execute cleanup"

  if [[ "${expected_action}" == 'remove' ]]; then
    [[ ! -e "${work_root}" ]] ||
      fail "$(basename -- "${script}") cleanup left its canonical owned work root behind"
  else
    [[ -f "${work_root}/sentinel" ]] ||
      fail "$(basename -- "${script}") cleanup removed a work root outside its exact prefix"
    grep -Fq -- 'refusing to remove an unowned' <<<"${output}" ||
      fail "$(basename -- "${script}") cleanup did not report the rejected outside-prefix work root"
  fi

  printf 'PASS fixture cleanup-prefix-boundary script=%s case=%s\n' \
    "$(basename -- "${script}")" "${case_name}"
}

run_partial_pod_create_case() {
  local script="$1"
  local confirmation="$2"
  local cleanup_marker="$3"
  local work_root_prefix_basename="$4"
  local case_name="$5"
  local label_mode="$6"
  local work_root
  local output
  local status
  local state
  local state_file="${fixture_root}/partial-pod-state-${case_name}"
  local remove_marker="${fixture_root}/partial-pod-remove-${case_name}"
  local exists_marker="${fixture_root}/partial-pod-exists-${case_name}"
  local inspect_marker="${fixture_root}/partial-pod-inspect-${case_name}"
  local unexpected_marker="${fixture_root}/partial-pod-unexpected-${case_name}"
  local -a arguments=(--confirm-reset "${confirmation}")
  append_required_rehearsal_inputs "${script}" arguments

  work_root="$(mktemp -d "${host_runtime}/${work_root_prefix_basename}XXXXXX")"
  runtime_fixture_roots+=("${work_root}")
  printf 'absent\n' >"${state_file}"

  set +e
  output="$({
    env \
      XDG_RUNTIME_DIR="${host_runtime}" \
      FAKE_WORK_ROOT="${work_root}" \
      POD_STATE_FILE="${state_file}" \
      POD_REMOVE_MARKER="${remove_marker}" \
      POD_EXISTS_MARKER="${exists_marker}" \
      POD_INSPECT_MARKER="${inspect_marker}" \
      POD_UNEXPECTED_MARKER="${unexpected_marker}" \
      STUB_LABEL_MODE="${label_mode}" \
      /usr/bin/bash -c '
        ss() { return 0; }

        mktemp() {
          printf "%s\n" "${FAKE_WORK_ROOT}"
        }

        go() {
          local output_path=""
          while (($# > 0)); do
            if [[ "$1" == "-o" && $# -ge 2 ]]; then
              output_path="$2"
              shift 2
            else
              shift
            fi
          done
          [[ -n "${output_path}" ]] || return 91
          printf "#!/usr/bin/bash\nexit 0\n" >"${output_path}"
          chmod 0700 -- "${output_path}"
        }

        podman() {
          local command_name="${1:-}"
          local subcommand=""
          local filter=""
          local name=""
          local parsed_label=""
          local actual_label=""
          local state=""
          local state_rest=""
          local state_name=""
          local state_label=""
          shift || true

          case "${command_name}" in
            info)
              printf true
              return 0
              ;;
            image)
              [[ "${1:-}" == "exists" ]] || return 98
              return 0
              ;;
            container)
              [[ "${1:-}" == "exists" ]] || return 98
              return 1
              ;;
            ps)
              while (($# > 0)); do
                if [[ "$1" == "--filter" && $# -ge 2 ]]; then
                  filter="$2"
                  shift 2
                else
                  shift
                fi
              done
              if [[ -z "${filter}" ]]; then
                printf "baseline-container-id\n"
              fi
              return 0
              ;;
            pod)
              subcommand="${1:-}"
              shift || true
              case "${subcommand}" in
                exists)
                  name="${1:-}"
                  state="$(<"${POD_STATE_FILE}")"
                  if [[ "${state}" == present\|* ]]; then
                    state_rest="${state#present|}"
                    state_name="${state_rest%%|*}"
                    if [[ "${state_name}" == "${name}" ]]; then
                      : >"${POD_EXISTS_MARKER}"
                      return 0
                    fi
                  fi
                  return 1
                  ;;
                create)
                  while (($# > 0)); do
                    case "$1" in
                      --name)
                        name="$2"
                        shift 2
                        ;;
                      --label)
                        parsed_label="${2#*=}"
                        shift 2
                        ;;
                      *)
                        shift
                        ;;
                    esac
                  done
                  [[ -n "${name}" && -n "${parsed_label}" ]] || return 96
                  actual_label="${parsed_label}"
                  if [[ "${STUB_LABEL_MODE}" == "mismatch" ]]; then
                    actual_label="mismatch-${parsed_label}"
                  fi
                  printf "present|%s|%s\n" "${name}" "${actual_label}" >"${POD_STATE_FILE}"
                  return 42
                  ;;
                inspect)
                  state="$(<"${POD_STATE_FILE}")"
                  [[ "${state}" == present\|* ]] || return 1
                  state_rest="${state#present|}"
                  state_label="${state_rest#*|}"
                  : >"${POD_INSPECT_MARKER}"
                  printf "%s\n" "${state_label}"
                  return 0
                  ;;
                rm)
                  state="$(<"${POD_STATE_FILE}")"
                  [[ "${state}" == present\|* ]] || return 1
                  printf "removed\n" >"${POD_REMOVE_MARKER}"
                  printf "absent\n" >"${POD_STATE_FILE}"
                  return 0
                  ;;
                ps)
                  while (($# > 0)); do
                    if [[ "$1" == "--filter" && $# -ge 2 ]]; then
                      filter="$2"
                      shift 2
                    else
                      shift
                    fi
                  done
                  state="$(<"${POD_STATE_FILE}")"
                  if [[ -n "${filter}" ]]; then
                    if [[ "${state}" == present\|* ]]; then
                      state_rest="${state#present|}"
                      state_name="${state_rest%%|*}"
                      state_label="${state_rest#*|}"
                      if [[ "${filter}" == *"=${state_label}" ]]; then
                        printf "%s\n" "${state_name}"
                      fi
                    fi
                    return 0
                  fi
                  printf "baseline-pod-id\n"
                  if [[ "${state}" == present\|* ]]; then
                    state_rest="${state#present|}"
                    printf "%s\n" "${state_rest%%|*}"
                  fi
                  return 0
                  ;;
              esac
              ;;
          esac

          : >"${POD_UNEXPECTED_MARKER}"
          return 98
        }

        export -f go mktemp podman ss
        exec /usr/bin/bash "$@"
      ' _ "${script}" "${arguments[@]}"
  } 2>&1)"
  status=$?
  set -e

  [[ "${status}" != '0' ]] ||
    fail "$(basename -- "${script}") ${case_name} accepted a failed partial pod creation"
  [[ ! -e "${unexpected_marker}" ]] ||
    fail "$(basename -- "${script}") ${case_name} reached an unexpected Podman operation"
  [[ -f "${exists_marker}" && -f "${inspect_marker}" ]] ||
    fail "$(basename -- "${script}") ${case_name} did not reconcile existence and ownership"
  [[ ! -e "${work_root}" ]] ||
    fail "$(basename -- "${script}") ${case_name} left its private work root behind"
  grep -Fq -- "${cleanup_marker}" <<<"${output}" ||
    fail "$(basename -- "${script}") ${case_name} did not execute cleanup"
  grep -Fq -- 'labeled_containers=0 labeled_pods=0' <<<"${output}" ||
    fail "$(basename -- "${script}") ${case_name} label queries did not converge"

  state="$(<"${state_file}")"
  if [[ "${label_mode}" == 'exact' ]]; then
    [[ "${status}" == '2' ]] ||
      fail "$(basename -- "${script}") exact-label partial create returned ${status}: ${output}"
    [[ -f "${remove_marker}" && "${state}" == 'absent' ]] ||
      fail "$(basename -- "${script}") did not remove the exact-labeled partial pod"
    grep -Fq -- 'preexisting_identities_unchanged=true' <<<"${output}" ||
      fail "$(basename -- "${script}") exact-label cleanup did not restore the baseline"
  else
    [[ ! -e "${remove_marker}" && "${state}" == present\|* ]] ||
      fail "$(basename -- "${script}") removed the mismatched-label partial pod"
    grep -Fq -- 'refusing to remove' <<<"${output}" ||
      fail "$(basename -- "${script}") did not report the mismatched ownership label"
    grep -Fq -- 'preexisting_identities_unchanged=false' <<<"${output}" ||
      fail "$(basename -- "${script}") mismatch cleanup did not remain non-converged"
  fi

  printf 'PASS fixture partial-pod-create-cleanup script=%s label=%s\n' \
    "$(basename -- "${script}")" "${label_mode}"
}

readonly noncanonical_runtime="${host_runtime}/../$(basename -- "${host_runtime}")"

run_rejected_catalog_contract \
  'missing-sha256' \
  '--knowledge-catalog and --knowledge-catalog-sha256 must be supplied together' \
  --knowledge-catalog "${recommendation_catalog_fixture}"
run_rejected_catalog_contract \
  'digest-mismatch' \
  'the recommendation knowledge catalog is noncanonical or differs from its pinned SHA-256' \
  --knowledge-catalog "${recommendation_catalog_fixture}" \
  --knowledge-catalog-sha256 'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'

for rehearsal_spec in \
  "${postgres_rehearsal}|drop-disposable-ascendany-v2" \
  "${backup_rehearsal}|drop-disposable-ascendany-v2-backup-restore"; do
  script="${rehearsal_spec%%|*}"
  confirmation="${rehearsal_spec#*|}"

  run_rejected_runtime "${script}" "${confirmation}" unset '' "${expected_canonical_error}"
  run_rejected_runtime "${script}" "${confirmation}" relative 'relative/runtime' "${expected_canonical_error}"
  run_rejected_runtime "${script}" "${confirmation}" symlink "${symlink_runtime}" "${expected_canonical_error}"
  run_rejected_runtime "${script}" "${confirmation}" noncanonical "${noncanonical_runtime}" "${expected_canonical_error}"
  run_rejected_runtime "${script}" "${confirmation}" wrong-mode "${private_fixture_root}" "${expected_owner_mode_error}"
  run_rejected_runtime "${script}" "${confirmation}" non-tmpfs "${non_tmpfs_runtime}" "${expected_tmpfs_error}"
done
unset rehearsal_spec script confirmation

run_failed_allocation \
  "${postgres_rehearsal}" \
  'drop-disposable-ascendany-v2' \
  'REHEARSAL_CLEANUP' \
  'postgres'
run_failed_allocation \
  "${backup_rehearsal}" \
  'drop-disposable-ascendany-v2-backup-restore' \
  'BACKUP_RESTORE_REHEARSAL_CLEANUP' \
  'backup'

run_cleanup_boundary_case \
  "${postgres_rehearsal}" \
  'drop-disposable-ascendany-v2' \
  'REHEARSAL_CLEANUP' \
  'ascendany-v2-postgres-podman.' \
  'postgres-owned-prefix' \
  'remove'
run_cleanup_boundary_case \
  "${postgres_rehearsal}" \
  'drop-disposable-ascendany-v2' \
  'REHEARSAL_CLEANUP' \
  'ascendany-v2-postgres-podman.' \
  'postgres-outside-prefix' \
  'preserve'
run_cleanup_boundary_case \
  "${backup_rehearsal}" \
  'drop-disposable-ascendany-v2-backup-restore' \
  'BACKUP_RESTORE_REHEARSAL_CLEANUP' \
  'ascendany-v2-backup-restore.' \
  'backup-owned-prefix' \
  'remove'
run_cleanup_boundary_case \
  "${backup_rehearsal}" \
  'drop-disposable-ascendany-v2-backup-restore' \
  'BACKUP_RESTORE_REHEARSAL_CLEANUP' \
  'ascendany-v2-backup-restore.' \
  'backup-outside-prefix' \
  'preserve'

run_partial_pod_create_case \
  "${postgres_rehearsal}" \
  'drop-disposable-ascendany-v2' \
  'REHEARSAL_CLEANUP' \
  'ascendany-v2-postgres-podman.' \
  'postgres-partial-exact' \
  'exact'
run_partial_pod_create_case \
  "${postgres_rehearsal}" \
  'drop-disposable-ascendany-v2' \
  'REHEARSAL_CLEANUP' \
  'ascendany-v2-postgres-podman.' \
  'postgres-partial-mismatch' \
  'mismatch'
run_partial_pod_create_case \
  "${backup_rehearsal}" \
  'drop-disposable-ascendany-v2-backup-restore' \
  'BACKUP_RESTORE_REHEARSAL_CLEANUP' \
  'ascendany-v2-backup-restore.' \
  'backup-partial-exact' \
  'exact'
run_partial_pod_create_case \
  "${backup_rehearsal}" \
  'drop-disposable-ascendany-v2-backup-restore' \
  'BACKUP_RESTORE_REHEARSAL_CLEANUP' \
  'ascendany-v2-backup-restore.' \
  'backup-partial-mismatch' \
  'mismatch'

printf 'PASS fixture rehearsal-private-runtime-lifecycle\n'
