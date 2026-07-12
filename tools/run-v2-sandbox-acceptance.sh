#!/usr/bin/bash -p
if [[ "${BASH:-}" != "/usr/bin/bash" || "$-" != *p* || "$-" == *[cis]* ||
      -n "${BASH_EXECUTION_STRING:-}" || "${#BASH_SOURCE[@]}" -ne 1 ||
      "${BASH_SOURCE[0]}" != "$0" ]]; then
  /usr/bin/printf '%s\n' 'sandbox acceptance must run directly under /usr/bin/bash -p' >&2
  /usr/bin/kill -KILL "${BASHPID}"
fi
set +x
builtin unset BASH_ENV ENV CDPATH GLOBIGNORE
builtin export -n SHELLOPTS BASHOPTS
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly PATH=/usr/local/bin:/usr/bin:/bin
export PATH
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly REPOSITORY_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd -P)"
readonly BACKEND_ROOT="${REPOSITORY_ROOT}/backend"
readonly JUDGE_CONFIG="${REPOSITORY_ROOT}/deploy/v2/config/judge.env.example"
readonly JUDGE_CONTRACT="${REPOSITORY_ROOT}/deploy/v2/scripts/judge-image-contract.sh"
readonly JUDGE_ATTESTER="${REPOSITORY_ROOT}/deploy/v2/scripts/attest-judge-image.sh"
readonly CONFIRMATION='run-real-ascendany-v2-sandbox-acceptance'

usage() {
  /usr/bin/printf '%s\n' \
    "Usage: $0 --confirm ${CONFIRMATION} --clangd-sha256 64_LOWER_HEX" \
    '' \
    'Runs the exact real Podman and real /usr/bin/clangd security test manifest.' \
    'Any skipped or missing required test fails the gate.'
}

fail() {
  /usr/bin/printf '%s\n' "$1" >&2
  exit 2
}

CONFIRM_VALUE=''
EXPECTED_CLANGD_SHA256=''
while (($# > 0)); do
  case "$1" in
    --confirm)
      (($# >= 2)) || fail '--confirm requires a value'
      CONFIRM_VALUE="$2"
      shift 2
      ;;
    --clangd-sha256)
      (($# >= 2)) || fail '--clangd-sha256 requires a value'
      EXPECTED_CLANGD_SHA256="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) fail "unknown argument: $1" ;;
  esac
done

[[ "${CONFIRM_VALUE}" == "${CONFIRMATION}" ]] || fail "--confirm must equal ${CONFIRMATION}"
[[ "${EXPECTED_CLANGD_SHA256}" =~ ^[0-9a-f]{64}$ ]] ||
  fail '--clangd-sha256 must be an externally reviewed 64-character SHA-256'
((EUID != 0)) || fail 'sandbox acceptance must run as a rootless user'
[[ -n "${HOME:-}" && "${HOME}" == /* && -d "${HOME}" && ! -L "${HOME}" &&
   "$(/usr/bin/realpath -e -- "${HOME}")" == "${HOME}" &&
   "$(/usr/bin/stat -Lc '%u' -- "${HOME}")" == "${EUID}" ]] ||
  fail 'HOME must be one canonical real directory owned by the sandbox user'
for command_name in bwrap go grep jq podman realpath sha256sum stat; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is unavailable: ${command_name}"
done
unset command_name
[[ -f "${JUDGE_CONTRACT}" && ! -L "${JUDGE_CONTRACT}" &&
   -x "${JUDGE_ATTESTER}" && ! -L "${JUDGE_ATTESTER}" ]] ||
  fail 'release-bound Judge image contract or attester is unavailable'
# shellcheck source=../deploy/v2/scripts/judge-image-contract.sh
source "${JUDGE_CONTRACT}"
load_judge_image_contract
[[ "$(podman info --format '{{.Host.Security.Rootless}}')" == true ]] ||
  fail 'Podman is not operating in rootless mode'
[[ -f /usr/bin/clangd && ! -L /usr/bin/clangd && -x /usr/bin/clangd ]] ||
  fail '/usr/bin/clangd must be one executable non-symlink regular file'
[[ "$(stat -Lc '%U:%G:%a' /usr/bin/clangd)" == 'root:root:755' ]] ||
  fail '/usr/bin/clangd must be root:root mode 0755'
[[ "$(sha256sum /usr/bin/clangd | awk '{print $1}')" == "${EXPECTED_CLANGD_SHA256}" ]] ||
  fail '/usr/bin/clangd differs from the externally reviewed digest'

mapfile -t judge_image_lines < <(sed -n 's/^ASCENDANY_JUDGE_CPP20_IMAGE=//p' "${JUDGE_CONFIG}")
[[ "${#judge_image_lines[@]}" == 1 ]] || fail 'production judge config must define exactly one image'
readonly JUDGE_IMAGE="${judge_image_lines[0]}"
[[ "${JUDGE_IMAGE}" == "${JUDGE_IMAGE_LEAF}" ]] ||
  fail 'production judge config must select the release-bound linux/amd64 leaf digest'

readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-v2-sandbox-acceptance.XXXXXX")"
readonly JUDGE_LOG="${WORK_ROOT}/judge.jsonl"
readonly LSP_LOG="${WORK_ROOT}/lsp.jsonl"
readonly JUDGE_TEST_LIST="${WORK_ROOT}/judge-tests.txt"
readonly LSP_TEST_LIST="${WORK_ROOT}/lsp-tests.txt"
readonly JUDGE_ATTESTATION="${WORK_ROOT}/judge-attestation.json"
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  rm -rf --one-file-system "${WORK_ROOT}"
  exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

readonly -a REQUIRED_JUDGE_TESTS=(
  TestPodmanEngineCompilesAndRunsWhenImageConfigured
  TestPodmanAttackCorpusWhenImageConfigured
)
readonly -a REQUIRED_LSP_TESTS=(
  TestRealClangdSessionRoundTripAndDisconnectCleanup
  TestRealClangdSandboxRejectsHostFileReadsAndCleansWorkspace
)

join_tests() {
  local separator=''
  local test_name
  printf '^('
  for test_name in "$@"; do
    printf '%s%s' "${separator}" "${test_name}"
    separator='|'
  done
  printf ')$'
}

assert_test_manifest_exists() {
  local label="$1"
  local package="$2"
  local output="$3"
  shift 3
  local test_name
  if ! /usr/bin/env -i \
      PATH="${PATH}" LC_ALL=C HOME="${HOME}" GOTOOLCHAIN=local GOENV=off GOWORK=off \
      go -C "${BACKEND_ROOT}" test -list '^Test' "${package}" >"${output}"; then
    fail "${label} test inventory could not be compiled"
  fi
  for test_name in "$@"; do
    [[ "$(grep -Fxc -- "${test_name}" "${output}")" == 1 ]] ||
      fail "${label} required test is missing or duplicated: ${test_name}"
  done
}

assert_test_evidence() {
  local label="$1"
  local log="$2"
  shift 2
  local test_name
  if jq -e 'select(.Action == "skip")' "${log}" >/dev/null; then
    fail "${label} emitted a skipped test"
  fi
  for test_name in "$@"; do
    jq -e --arg test "${test_name}" \
      'select(.Action == "pass" and .Test == $test)' "${log}" >/dev/null ||
      fail "${label} did not pass required test ${test_name}"
  done
  jq -e 'select(.Action == "pass" and (.Test // "") == "")' "${log}" >/dev/null ||
    fail "${label} package did not pass"
}

assert_test_manifest_exists \
  'real Podman judge acceptance' ./internal/judgerunner "${JUDGE_TEST_LIST}" \
  "${REQUIRED_JUDGE_TESTS[@]}"
assert_test_manifest_exists \
  'real clangd LSP acceptance' ./internal/lsprunner "${LSP_TEST_LIST}" \
  "${REQUIRED_LSP_TESTS[@]}"
podman image exists "${JUDGE_IMAGE}" || fail "preload the production judge image: ${JUDGE_IMAGE}"
"${JUDGE_ATTESTER}" >"${JUDGE_ATTESTATION}" || fail 'production judge image attestation failed'
jq -e \
  --arg image "${JUDGE_IMAGE}" \
  --arg config_digest "${JUDGE_IMAGE_CONFIG_DIGEST}" \
  --arg os "${JUDGE_IMAGE_OS}" \
  --arg architecture "${JUDGE_IMAGE_ARCHITECTURE}" \
  --arg compiler "${JUDGE_IMAGE_COMPILER}" \
  --arg version "${JUDGE_IMAGE_TOOLCHAIN_VERSION}" '
  type == "object" and .schema == "ascendany.judge-image-attestation.v1" and
  .image == $image and .configDigest == $config_digest and
  .os == $os and .architecture == $architecture and
  .compiler == $compiler and .version == $version
' "${JUDGE_ATTESTATION}" >/dev/null || fail 'production judge image attestation evidence is invalid'

if ! /usr/bin/env -i \
    PATH="${PATH}" LC_ALL=C HOME="${HOME}" GOTOOLCHAIN=local GOENV=off GOWORK=off \
    ASCENDANY_TEST_JUDGE_IMAGE="${JUDGE_IMAGE}" \
    go -C "${BACKEND_ROOT}" test -json -count=1 \
      -run "$(join_tests "${REQUIRED_JUDGE_TESTS[@]}")" ./internal/judgerunner \
      >"${JUDGE_LOG}"; then
  fail 'real Podman judge acceptance failed'
fi
assert_test_evidence 'real Podman judge acceptance' "${JUDGE_LOG}" "${REQUIRED_JUDGE_TESTS[@]}"

if ! /usr/bin/env -i \
    PATH="${PATH}" LC_ALL=C HOME="${HOME}" GOTOOLCHAIN=local GOENV=off GOWORK=off \
    ASCENDANY_TEST_LSP_ROOTFS=1 \
    go -C "${BACKEND_ROOT}" test -json -count=1 \
      -run "$(join_tests "${REQUIRED_LSP_TESTS[@]}")" ./internal/lsprunner \
      >"${LSP_LOG}"; then
  fail 'real clangd LSP acceptance failed'
fi
assert_test_evidence 'real clangd LSP acceptance' "${LSP_LOG}" "${REQUIRED_LSP_TESTS[@]}"

/usr/bin/printf \
  'SANDBOX_ACCEPTANCE_RESULT judge_image_pinned=true judge_image_attested=true judge_required_tests=%s judge_skipped=0 clangd_sha256_verified=true lsp_rootfs=bwrap lsp_required_tests=%s lsp_skipped=0 passed=true\n' \
  "${#REQUIRED_JUDGE_TESTS[@]}" "${#REQUIRED_LSP_TESTS[@]}"
