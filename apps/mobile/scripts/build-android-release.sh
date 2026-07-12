#!/usr/bin/bash -p

if [[ "${BASH:-}" != "/usr/bin/bash" || "$-" != *p* || "$-" == *[cis]* ||
      -n "${BASH_EXECUTION_STRING:-}" || "${#BASH_SOURCE[@]}" -ne 1 ||
      "${BASH_SOURCE[0]}" != "$0" ]]; then
  /usr/bin/printf '%s\n' 'Android release failed: wrapper must run under /usr/bin/bash -p' >&2
  /usr/bin/kill -KILL "${BASHPID}"
fi

set -Eeuo pipefail
set +x
if ! ulimit -c 0; then
  /usr/bin/printf '%s\n' 'Android release failed: core dumps must be disabled before reading signing credentials' >&2
  /usr/bin/kill -KILL "${BASHPID}"
fi

for credential_environment_name in \
  ASCENDANY_ANDROID_SIGNING_STORE_FILE \
  ASCENDANY_ANDROID_SIGNING_STORE_PASSWORD \
  ASCENDANY_ANDROID_SIGNING_KEY_ALIAS \
  ASCENDANY_ANDROID_SIGNING_KEY_PASSWORD \
  ASCENDANY_APKSIGNER_STORE_PASSWORD \
  ASCENDANY_APKSIGNER_KEY_PASSWORD \
  SIGNING_STORE_PASSWORD \
  SIGNING_KEY_PASSWORD; do
  if [[ -v "${credential_environment_name}" ]]; then
    printf 'Android release failed: ambient Android signing credential variable is forbidden: %s\n' \
      "${credential_environment_name}" >&2
    exit 2
  fi
done
unset credential_environment_name

export LC_ALL=C
umask 077
export -n BASHOPTS SHELLOPTS
readonly SYSTEM_PATH='/usr/bin:/bin'
PATH="${SYSTEM_PATH}"
export PATH
hash -r

script_source="${BASH_SOURCE[0]}"
if [[ "${script_source}" != /* ]]; then
  script_source="${PWD}/${script_source}"
fi
readonly SCRIPT_DIR="$(cd -- "${script_source%/*}" && pwd -P)"
readonly REPOSITORY_ROOT="$(cd -- "${SCRIPT_DIR}/../../.." && pwd -P)"
unset script_source

VERSION=""
VERSION_CODE=""
GRADLE_MAX_WORKERS=""
COMMIT=""
OUTPUT_DIRECTORY=""
API_ORIGIN=""
PROMPT_KEY=""
MODEL_KEY=""
KEYSTORE_FILE=""
KEY_ALIAS=""
EXPECTED_SIGNER_SHA256=""
NODE_BINARY=""
PNPM_ENTRY=""
APKSIGNER_BINARY=""
STORE_PASSWORD_FD=""
KEY_PASSWORD_FD=""
STORE_PASSWORD_VALUE=""
KEY_PASSWORD_VALUE=""
JAVA_HOME_INPUT="${JAVA_HOME:-}"
ANDROID_HOME_INPUT="${ANDROID_HOME:-}"
WORK_ROOT=""
PUBLISHED_OUTPUT_CLEANUP_ARMED=0
PUBLISHED_OUTPUT_DIRECTORY_FD=""
PUBLISHED_OUTPUT_IDENTITY=""

export -n STORE_PASSWORD_VALUE KEY_PASSWORD_VALUE JAVA_HOME_INPUT ANDROID_HOME_INPUT
usage() {
  printf '%s\n' \
    'Usage: build-android-release.sh \' \
    '  --version <canonical-semver> --version-code <positive-int> \' \
    '  --gradle-max-workers <positive-int> \' \
    '  --commit <reviewed-40hex-commit> --output-dir <absolute-new-directory> \' \
    '  --api-origin <canonical-https-origin> \' \
    '  --prompt-key <configuration-key> --model-key <configuration-key> \' \
    '  --node-bin <absolute-canonical-node> --pnpm-entry <absolute-canonical-pnpm-cjs> \' \
    '  --apksigner-bin <absolute-canonical-apksigner> \' \
    '  --keystore <absolute-canonical-file> --key-alias <alias> \' \
    '  --store-password-fd <fd> --key-password-fd <different-fd> \' \
    '  --signer-sha256 <64-lowercase-hex>' \
    '' \
    'Each password FD must contain exactly one non-empty NUL-terminated value.'
}

fail() {
  printf 'Android release failed: %s\n' "$1" >&2
  exit 2
}

reject_ambient_injections() {
  local environment_entry environment_name environment_entry_count=0
  [[ -x /usr/bin/env ]] || fail 'trusted /usr/bin/env is unavailable'
  while IFS= read -r -d '' environment_entry; do
    ((environment_entry_count += 1))
    environment_name="${environment_entry%%=*}"
    case "${environment_name}" in
      BASH_ENV|ENV|BASH_FUNC_*)
        fail "ambient shell injection variable is forbidden: ${environment_name}"
        ;;
      ORG_GRADLE_PROJECT_*)
        fail "ambient Gradle project property is forbidden: ${environment_name}"
        ;;
      GRADLE_OPTS|JAVA_OPTS|JAVA_TOOL_OPTIONS|JDK_JAVA_OPTIONS|_JAVA_OPTIONS)
        fail "ambient Gradle/JVM option injection is forbidden: ${environment_name}"
        ;;
      JAVA_*)
        [[ "${environment_name}" == "JAVA_HOME" ]] ||
          fail "ambient JAVA_* variable is forbidden: ${environment_name}"
        ;;
      ANDROID_SDK_ROOT)
        fail 'ANDROID_SDK_ROOT is forbidden; use one validated canonical ANDROID_HOME'
        ;;
      ASCENDANY_ANDROID_SIGNING_STORE_FILE|ASCENDANY_ANDROID_SIGNING_STORE_PASSWORD|\
      ASCENDANY_ANDROID_SIGNING_KEY_ALIAS|ASCENDANY_ANDROID_SIGNING_KEY_PASSWORD|\
      ASCENDANY_APKSIGNER_STORE_PASSWORD|ASCENDANY_APKSIGNER_KEY_PASSWORD|\
      SIGNING_STORE_PASSWORD|SIGNING_KEY_PASSWORD)
        fail "ambient Android signing credential variable is forbidden: ${environment_name}"
        ;;
    esac
  done < <(/usr/bin/env -0)
  (( environment_entry_count > 0 )) || fail 'ambient process environment could not be enumerated'
}

run_snapshot_pnpm() {
  local network_policy="$1"
  local working_directory="${PWD}"
  shift
  local -a snapshot_environment=(
    /usr/bin/env -i
    HOME="${BUILD_HOME}"
    XDG_CONFIG_HOME="${BUILD_XDG_CONFIG_HOME}"
    XDG_CACHE_HOME="${BUILD_XDG_CACHE_HOME}"
    XDG_DATA_HOME="${BUILD_XDG_DATA_HOME}"
    TMPDIR="${BUILD_TMPDIR}"
    PATH="${SNAPSHOT_TOOL_PATH}"
    LC_ALL=C
    TZ=UTC
    CI=1
  )
  [[ -z "${VITE_API_BASE_URL:-}" ]] || snapshot_environment+=("VITE_API_BASE_URL=${VITE_API_BASE_URL}")
  [[ -z "${VITE_CHAT_PROMPT_CONFIGURATION_KEY:-}" ]] ||
    snapshot_environment+=("VITE_CHAT_PROMPT_CONFIGURATION_KEY=${VITE_CHAT_PROMPT_CONFIGURATION_KEY}")
  [[ -z "${VITE_CHAT_MODEL_CONFIGURATION_KEY:-}" ]] ||
    snapshot_environment+=("VITE_CHAT_MODEL_CONFIGURATION_KEY=${VITE_CHAT_MODEL_CONFIGURATION_KEY}")
  local -a namespace=(
    "${TRUSTED_BWRAP_BINARY}"
    --die-with-parent
    --new-session
    --unshare-pid
    --unshare-ipc
    --unshare-uts
    --ro-bind /usr /usr
    --symlink usr/bin /bin
    --symlink usr/sbin /sbin
    --symlink usr/lib /lib
    --symlink usr/lib64 /lib64
    --ro-bind /etc /etc
    --ro-bind /sys /sys
    --ro-bind "${TRUSTED_RESOLV_CONF}" "${TRUSTED_RESOLV_CONF}"
    --ro-bind "${TRUSTED_NODE_BINARY}" "${TRUSTED_NODE_BINARY}"
    --ro-bind "${TRUSTED_PNPM_ENTRY}" "${TRUSTED_PNPM_ENTRY}"
    --proc /proc
    --dev /dev
    --ro-bind /dev/null "${CANONICAL_KEYSTORE}"
    --bind "${SOURCE_ROOT}" "${SOURCE_ROOT}"
    --bind "${BUILD_HOME}" "${BUILD_HOME}"
    --bind "${BUILD_XDG_CONFIG_HOME}" "${BUILD_XDG_CONFIG_HOME}"
    --bind "${BUILD_XDG_CACHE_HOME}" "${BUILD_XDG_CACHE_HOME}"
    --bind "${BUILD_XDG_DATA_HOME}" "${BUILD_XDG_DATA_HOME}"
    --bind "${BUILD_TMPDIR}" "${BUILD_TMPDIR}"
    --bind "${PNPM_STORE_DIRECTORY}" "${PNPM_STORE_DIRECTORY}"
    --ro-bind "${TRUSTED_TOOL_BIN}" "${TRUSTED_TOOL_BIN}"
    --chdir "${working_directory}"
  )
  case "${network_policy}" in
    fetch) ;;
    offline) namespace+=(--unshare-net) ;;
    *) fail 'internal pnpm namespace policy is invalid' ;;
  esac
  /usr/bin/env -i PATH="${SYSTEM_PATH}" LC_ALL=C "${namespace[@]}" -- \
    "${snapshot_environment[@]}" \
    "${TRUSTED_NODE_BINARY}" \
    "${TRUSTED_PNPM_ENTRY}" \
    "$@"
}

cleanup_published_output() {
  local target_identity="" descriptor_identity=""
  [[ "${PUBLISHED_OUTPUT_CLEANUP_ARMED:-0}" == "1" ]] || return 0
  if [[ -n "${OUTPUT_DIRECTORY:-}" && -n "${PUBLISHED_OUTPUT_IDENTITY:-}" &&
        -d "${OUTPUT_DIRECTORY}" && ! -L "${OUTPUT_DIRECTORY}" &&
        -n "${PUBLISHED_OUTPUT_DIRECTORY_FD:-}" ]]; then
    target_identity="$(/usr/bin/stat -Lc '%d:%i' -- "${OUTPUT_DIRECTORY}" 2>/dev/null)"
    descriptor_identity="$(/usr/bin/stat -Lc '%d:%i' -- "/proc/self/fd/${PUBLISHED_OUTPUT_DIRECTORY_FD}" 2>/dev/null)"
    if [[ "${target_identity}" == "${PUBLISHED_OUTPUT_IDENTITY}" &&
          "${descriptor_identity}" == "${PUBLISHED_OUTPUT_IDENTITY}" ]]; then
      /usr/bin/rm -rf --one-file-system -- "${OUTPUT_DIRECTORY}"
      [[ -z "${OUTPUT_PARENT:-}" ]] || /usr/bin/sync -f -- "${OUTPUT_PARENT}" 2>/dev/null || true
    fi
  fi
  if [[ "${PUBLISHED_OUTPUT_DIRECTORY_FD:-}" =~ ^[0-9]+$ ]]; then
    exec {PUBLISHED_OUTPUT_DIRECTORY_FD}<&-
  fi
  PUBLISHED_OUTPUT_CLEANUP_ARMED=0
}

cleanup() {
  local status=$?
  set +e
  cleanup_published_output
  if [[ -n "${WORK_ROOT}" && -d "${WORK_ROOT}" ]]; then
    rm -rf -- "${WORK_ROOT}"
  fi
  exit "${status}"
}
trap cleanup EXIT

reject_ambient_injections
unset BASH_ENV ENV CDPATH GLOBIGNORE
unset ASCENDANY_ANDROID_SIGNING_STORE_FILE ASCENDANY_ANDROID_SIGNING_STORE_PASSWORD
unset ASCENDANY_ANDROID_SIGNING_KEY_ALIAS ASCENDANY_ANDROID_SIGNING_KEY_PASSWORD
unset ASCENDANY_APKSIGNER_STORE_PASSWORD ASCENDANY_APKSIGNER_KEY_PASSWORD
unset SIGNING_STORE_PASSWORD SIGNING_KEY_PASSWORD
unset GRADLE_USER_HOME GRADLE_OPTS JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT
unset JAVA_OPTS JAVA_TOOL_OPTIONS JDK_JAVA_OPTIONS _JAVA_OPTIONS
unset VITE_API_BASE_URL VITE_CHAT_PROMPT_CONFIGURATION_KEY VITE_CHAT_MODEL_CONFIGURATION_KEY

require_option_value() {
  local option="$1"
  local value="${2:-}"
  [[ -n "${value}" ]] || fail "${option} requires a non-empty value"
}

set_once() {
  local variable_name="$1"
  local option="$2"
  local value="$3"
  [[ -z "${!variable_name}" ]] || fail "${option} may be specified only once"
  printf -v "${variable_name}" '%s' "${value}"
}

while (( $# > 0 )); do
  case "$1" in
    --version|--version-code|--gradle-max-workers|--commit|--output-dir|--api-origin|--prompt-key|--model-key|--node-bin|--pnpm-entry|--apksigner-bin|--keystore|--key-alias|--store-password-fd|--key-password-fd|--signer-sha256)
      require_option_value "$1" "${2:-}"
      case "$1" in
        --version) set_once VERSION "$1" "$2" ;;
        --version-code) set_once VERSION_CODE "$1" "$2" ;;
        --gradle-max-workers) set_once GRADLE_MAX_WORKERS "$1" "$2" ;;
        --commit) set_once COMMIT "$1" "$2" ;;
        --output-dir) set_once OUTPUT_DIRECTORY "$1" "$2" ;;
        --api-origin) set_once API_ORIGIN "$1" "$2" ;;
        --prompt-key) set_once PROMPT_KEY "$1" "$2" ;;
        --model-key) set_once MODEL_KEY "$1" "$2" ;;
        --node-bin) set_once NODE_BINARY "$1" "$2" ;;
        --pnpm-entry) set_once PNPM_ENTRY "$1" "$2" ;;
        --apksigner-bin) set_once APKSIGNER_BINARY "$1" "$2" ;;
        --keystore) set_once KEYSTORE_FILE "$1" "$2" ;;
        --key-alias) set_once KEY_ALIAS "$1" "$2" ;;
        --store-password-fd) set_once STORE_PASSWORD_FD "$1" "$2" ;;
        --key-password-fd) set_once KEY_PASSWORD_FD "$1" "$2" ;;
        --signer-sha256) set_once EXPECTED_SIGNER_SHA256 "$1" "$2" ;;
      esac
      shift 2
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

for required_variable in \
  VERSION VERSION_CODE GRADLE_MAX_WORKERS COMMIT OUTPUT_DIRECTORY API_ORIGIN PROMPT_KEY MODEL_KEY \
  NODE_BINARY PNPM_ENTRY APKSIGNER_BINARY \
  KEYSTORE_FILE KEY_ALIAS STORE_PASSWORD_FD KEY_PASSWORD_FD EXPECTED_SIGNER_SHA256; do
  [[ -n "${!required_variable}" ]] || fail "missing required release option: ${required_variable}"
done
unset required_variable

readonly SEMVER_PATTERN='^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-([0-9A-Za-z-]+([.][0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+([.][0-9A-Za-z-]+)*))?$'
(( ${#VERSION} <= 128 )) || fail '--version must be at most 128 ASCII bytes'
[[ "${VERSION}" =~ ${SEMVER_PATTERN} ]] || fail '--version must be canonical SemVer'
version_without_build="${VERSION%%+*}"
if [[ "${version_without_build}" == *-* ]]; then
  prerelease="${version_without_build#*-}"
  IFS='.' read -r -a prerelease_identifiers <<<"${prerelease}"
  for identifier in "${prerelease_identifiers[@]}"; do
    if [[ "${identifier}" =~ ^[0-9]+$ && "${identifier}" != "0" && "${identifier}" == 0* ]]; then
      fail '--version must be canonical SemVer'
    fi
  done
  unset prerelease prerelease_identifiers identifier
fi
unset version_without_build

[[ "${VERSION_CODE}" =~ ^[1-9][0-9]{0,9}$ ]] || fail '--version-code must be a canonical positive integer'
(( VERSION_CODE <= 2147483647 )) || fail '--version-code exceeds the Android signed 32-bit limit'
[[ "${GRADLE_MAX_WORKERS}" =~ ^[1-9][0-9]{0,2}$ ]] ||
  fail '--gradle-max-workers must be a canonical integer from 1 through 256'
(( GRADLE_MAX_WORKERS <= 256 )) ||
  fail '--gradle-max-workers must be a canonical integer from 1 through 256'
[[ "${COMMIT}" =~ ^[0-9a-f]{40}$ ]] || fail '--commit must be a reviewed lowercase 40-hex commit ID'
[[ "${PROMPT_KEY}" =~ ^[a-z][a-z0-9_.-]{0,127}$ ]] || fail '--prompt-key is invalid'
[[ "${MODEL_KEY}" =~ ^[a-z][a-z0-9_.-]{0,127}$ ]] || fail '--model-key is invalid'
[[ "${KEY_ALIAS}" =~ ^[A-Za-z0-9._-]{1,128}$ ]] || fail '--key-alias is invalid'
[[ "${EXPECTED_SIGNER_SHA256}" =~ ^[0-9a-f]{64}$ ]] || fail '--signer-sha256 must be exactly 64 lowercase hexadecimal digits'
[[ "${STORE_PASSWORD_FD}" =~ ^[1-9][0-9]{0,3}$ ]] &&
  (( 10#${STORE_PASSWORD_FD} >= 3 && 10#${STORE_PASSWORD_FD} <= 1023 )) ||
  fail '--store-password-fd must be an integer from 3 through 1023'
[[ "${KEY_PASSWORD_FD}" =~ ^[1-9][0-9]{0,3}$ ]] &&
  (( 10#${KEY_PASSWORD_FD} >= 3 && 10#${KEY_PASSWORD_FD} <= 1023 )) ||
  fail '--key-password-fd must be an integer from 3 through 1023'
[[ "${STORE_PASSWORD_FD}" != "${KEY_PASSWORD_FD}" ]] ||
  fail '--store-password-fd and --key-password-fd must be different'

read_secret_fd() {
  local label="$1"
  local fd="$2"
  local variable_name="$3"
  local value="" trailing=""
  if ! IFS= read -r -d '' -u "${fd}" value; then
    exec {fd}<&-
    fail "${label} must contain one NUL-terminated value"
  fi
  if IFS= read -r -d '' -u "${fd}" trailing || [[ -n "${trailing}" ]]; then
    exec {fd}<&-
    fail "${label} must contain exactly one NUL-terminated value"
  fi
  exec {fd}<&-
  [[ -n "${value}" ]] || fail "${label} must contain a non-empty value"
  printf -v "${variable_name}" '%s' "${value}"
}

read_secret_fd '--store-password-fd' "${STORE_PASSWORD_FD}" STORE_PASSWORD_VALUE
read_secret_fd '--key-password-fd' "${KEY_PASSWORD_FD}" KEY_PASSWORD_VALUE
unset STORE_PASSWORD_FD KEY_PASSWORD_FD

for command_name in awk basename chmod cp diff dirname find git grep id install ln mkdir mktemp mv realpath rm sed sort stat; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is unavailable: ${command_name}"
done
unset command_name

stat_uid() {
  /usr/bin/stat -c '%u' -- "$1"
}

stat_mode() {
  /usr/bin/stat -c '%a' -- "$1"
}

validate_toolchain_root() {
  local label="$1"
  local value="$2"
  local canonical owner mode_text mode
  [[ -n "${value}" ]] || fail "${label} is required"
  [[ "${value}" == /* && -d "${value}" && ! -L "${value}" ]] ||
    fail "${label} must name an absolute directory and may not be a symlink"
  canonical="$(realpath -e -- "${value}")"
  [[ "${canonical}" == "${value}" ]] ||
    fail "${label} must use its canonical path and may not traverse symlinked ancestors"
  owner="$(stat_uid "${value}")"
  [[ "${owner}" == "0" || "${owner}" == "$(id -u)" ]] ||
    fail "${label} must be owned by root or the release user"
  mode_text="$(stat_mode "${value}")"
  mode=$((8#${mode_text}))
  (( (mode & 8#022) == 0 )) || fail "${label} must not be group- or other-writable"
  validate_protected_ancestry "${label}" "${value}/.ascendany-toolchain-root"
}

validate_release_tool_file() {
  local label="$1"
  local value="$2"
  local canonical owner mode_text mode
  [[ "${value}" == /* && -f "${value}" && -r "${value}" && -x "${value}" && ! -L "${value}" ]] ||
    fail "${label} must name an absolute readable executable regular file and may not be a symlink"
  canonical="$(realpath -e -- "${value}")"
  [[ "${canonical}" == "${value}" ]] ||
    fail "${label} must use its canonical path and may not traverse symlinked ancestors"
  owner="$(stat_uid "${value}")"
  [[ "${owner}" == "0" || "${owner}" == "$(id -u)" ]] ||
    fail "${label} must be owned by root or the release user"
  mode_text="$(stat_mode "${value}")"
  mode=$((8#${mode_text}))
  (( (mode & 8#022) == 0 )) || fail "${label} must not be group- or other-writable"
}

validate_release_data_file() {
  local label="$1"
  local value="$2"
  local canonical owner mode_text mode
  [[ "${value}" == /* && -f "${value}" && -r "${value}" && ! -L "${value}" ]] ||
    fail "${label} must name an absolute readable regular file and may not be a symlink"
  canonical="$(realpath -e -- "${value}")"
  [[ "${canonical}" == "${value}" ]] ||
    fail "${label} must use its canonical path and may not traverse symlinked ancestors"
  owner="$(stat_uid "${value}")"
  [[ "${owner}" == "0" || "${owner}" == "$(id -u)" ]] ||
    fail "${label} must be owned by root or the release user"
  mode_text="$(stat_mode "${value}")"
  mode=$((8#${mode_text}))
  (( (mode & 8#022) == 0 )) || fail "${label} must not be group- or other-writable"
}

validate_protected_ancestry() {
  local label="$1"
  local file_path="$2"
  local ancestor owner mode_text mode parent
  ancestor="$(dirname -- "${file_path}")"
  while :; do
    [[ -d "${ancestor}" && ! -L "${ancestor}" ]] ||
      fail "${label} has a non-directory or symlinked ancestor"
    [[ "$(realpath -e -- "${ancestor}")" == "${ancestor}" ]] ||
      fail "${label} has a non-canonical ancestor"
    owner="$(stat_uid "${ancestor}")"
    [[ "${owner}" == "0" || "${owner}" == "$(id -u)" ]] ||
      fail "${label} ancestry must be owned by root or the release user"
    mode_text="$(stat_mode "${ancestor}")"
    mode=$((8#${mode_text}))
    if (( (mode & 8#022) != 0 )); then
      (( owner == 0 && (mode & 8#1000) != 0 )) ||
        fail "${label} has an unprotected writable ancestor"
    fi
    [[ "${ancestor}" != "/" ]] || break
    parent="$(dirname -- "${ancestor}")"
    [[ "${parent}" != "${ancestor}" ]] || fail "${label} ancestry did not terminate at root"
    ancestor="${parent}"
  done
}

validate_toolchain_tree() {
  local label="$1"
  local tree_root="$2"
  local listing="$3"
  local node owner mode_text mode canonical node_count=0
  [[ -d "${tree_root}" && ! -L "${tree_root}" ]] ||
    fail "${label} must be one regular directory tree"
  canonical="$(realpath -e -- "${tree_root}")"
  [[ "${canonical}" == "${tree_root}" ]] || fail "${label} must use one canonical root"
  find "${tree_root}" -print0 >"${listing}" || fail "${label} could not be enumerated completely"
  while IFS= read -r -d '' node; do
    if [[ -L "${node}" ]]; then
      canonical="$(realpath -e -- "${node}")" || fail "${label} contains a dangling symlink"
      [[ -f "${canonical}" ]] || fail "${label} may contain only regular-file symlinks"
      validate_protected_ancestry "${label} symlink target" "${canonical}"
      owner="$(stat_uid "${canonical}")"
      [[ "${owner}" == "0" || "${owner}" == "$(id -u)" ]] ||
        fail "${label} symlink target must be owned by root or the release user"
      mode_text="$(stat_mode "${canonical}")"
      mode=$((8#${mode_text}))
      (( (mode & 8#022) == 0 )) || fail "${label} symlink target must not be group- or other-writable"
    elif [[ -f "${node}" || -d "${node}" ]]; then
      owner="$(stat_uid "${node}")"
      [[ "${owner}" == "0" || "${owner}" == "$(id -u)" ]] ||
        fail "${label} descendants must be owned by root or the release user"
      mode_text="$(stat_mode "${node}")"
      mode=$((8#${mode_text}))
      (( (mode & 8#022) == 0 )) || fail "${label} descendants must not be group- or other-writable"
    else
      fail "${label} contains a special node"
    fi
    ((node_count += 1))
  done <"${listing}"
  (( node_count > 0 )) || fail "${label} tree is empty"
}

validate_release_tool_file '--node-bin' "${NODE_BINARY}"
validate_release_tool_file '--pnpm-entry' "${PNPM_ENTRY}"
validate_release_tool_file '--apksigner-bin' "${APKSIGNER_BINARY}"
readonly TRUSTED_NODE_BINARY="${NODE_BINARY}"
readonly TRUSTED_PNPM_ENTRY="${PNPM_ENTRY}"
readonly TRUSTED_APKSIGNER_BINARY="${APKSIGNER_BINARY}"
readonly TRUSTED_APKSIGNER_JAR="${TRUSTED_APKSIGNER_BINARY%/*}/lib/apksigner.jar"
readonly PINNED_APKSIGNER_BINARY_SHA256='b47549e373b895ce6ca620d0c7887e674d9615ffa837a86ac601dcfd04adb0f0'
readonly PINNED_APKSIGNER_JAR_SHA256='3716d9311e55d2b0918a2fd9d54ba9e406c5f6abeea700b287f11259bc163dec'
readonly TRUSTED_SHA512SUM_BINARY='/usr/bin/sha512sum'
readonly TRUSTED_SHA256SUM_BINARY='/usr/bin/sha256sum'
readonly TRUSTED_BWRAP_BINARY='/usr/bin/bwrap'
readonly TRUSTED_RESOLV_CONF="$(realpath -e -- /etc/resolv.conf)"
readonly EXECUTING_BUILDER="${SCRIPT_DIR}/build-android-release.sh"
[[ "$(realpath -e -- "$0")" == "${EXECUTING_BUILDER}" ]] ||
  fail 'executing Android release wrapper must use its canonical repository path'
validate_release_tool_file 'executing Android release wrapper' "${EXECUTING_BUILDER}"
validate_protected_ancestry 'executing Android release wrapper' "${EXECUTING_BUILDER}"
validate_release_tool_file 'trusted /usr/bin/sha512sum' "${TRUSTED_SHA512SUM_BINARY}"
[[ "$(stat_uid "${TRUSTED_SHA512SUM_BINARY}")" == "0" ]] ||
  fail 'trusted /usr/bin/sha512sum must be owned by root'
validate_release_tool_file 'trusted /usr/bin/sha256sum' "${TRUSTED_SHA256SUM_BINARY}"
[[ "$(stat_uid "${TRUSTED_SHA256SUM_BINARY}")" == "0" ]] ||
  fail 'trusted /usr/bin/sha256sum must be owned by root'
validate_release_tool_file 'trusted /usr/bin/bwrap' "${TRUSTED_BWRAP_BINARY}"
[[ "$(stat_uid "${TRUSTED_BWRAP_BINARY}")" == "0" ]] ||
  fail 'trusted /usr/bin/bwrap must be owned by root'
validate_protected_ancestry 'trusted /usr/bin/bwrap' "${TRUSTED_BWRAP_BINARY}"
[[ -f "${TRUSTED_RESOLV_CONF}" && ! -L "${TRUSTED_RESOLV_CONF}" ]] ||
  fail 'trusted resolver configuration must be a regular file'
resolver_owner="$(stat_uid "${TRUSTED_RESOLV_CONF}")"
resolver_mode_text="$(stat_mode "${TRUSTED_RESOLV_CONF}")"
resolver_mode=$((8#${resolver_mode_text}))
(( resolver_owner >= 0 && resolver_owner < 1000 && (resolver_mode & 8#022) == 0 )) ||
  fail 'trusted resolver configuration must be protected by a system identity'
unset resolver_owner resolver_mode_text resolver_mode
validate_protected_ancestry '--node-bin' "${TRUSTED_NODE_BINARY}"
validate_protected_ancestry '--pnpm-entry' "${TRUSTED_PNPM_ENTRY}"
validate_protected_ancestry '--apksigner-bin' "${TRUSTED_APKSIGNER_BINARY}"
validate_release_data_file 'apksigner sibling lib/apksigner.jar' "${TRUSTED_APKSIGNER_JAR}"
validate_protected_ancestry 'apksigner sibling lib/apksigner.jar' "${TRUSTED_APKSIGNER_JAR}"

trusted_sha256_digest() {
  local file_path="$1"
  local digest_output digest
  digest_output="$(/usr/bin/env -i LC_ALL=C "${TRUSTED_SHA256SUM_BINARY}" -- "${file_path}")" || return 1
  digest="${digest_output%% *}"
  [[ "${digest}" =~ ^[0-9a-f]{64}$ ]] || return 1
  printf '%s\n' "${digest}"
}

validate_trusted_apksigner_bundle() {
  local trusted_apksigner_binary_sha256 trusted_apksigner_jar_sha256
  validate_release_tool_file '--apksigner-bin' "${TRUSTED_APKSIGNER_BINARY}"
  validate_protected_ancestry '--apksigner-bin' "${TRUSTED_APKSIGNER_BINARY}"
  validate_release_data_file 'apksigner sibling lib/apksigner.jar' "${TRUSTED_APKSIGNER_JAR}"
  validate_protected_ancestry 'apksigner sibling lib/apksigner.jar' "${TRUSTED_APKSIGNER_JAR}"
  trusted_apksigner_binary_sha256="$(trusted_sha256_digest "${TRUSTED_APKSIGNER_BINARY}")" ||
    fail 'apksigner launcher SHA-256 could not be calculated'
  [[ "${trusted_apksigner_binary_sha256}" == "${PINNED_APKSIGNER_BINARY_SHA256}" ]] ||
    fail '--apksigner-bin differs from the pinned Android Build Tools 36.0.0 launcher'
  trusted_apksigner_jar_sha256="$(trusted_sha256_digest "${TRUSTED_APKSIGNER_JAR}")" ||
    fail 'apksigner sibling lib/apksigner.jar SHA-256 could not be calculated'
  [[ "${trusted_apksigner_jar_sha256}" == "${PINNED_APKSIGNER_JAR_SHA256}" ]] ||
    fail 'apksigner sibling lib/apksigner.jar differs from the pinned Android Build Tools 36.0.0 JAR'
}
validate_trusted_apksigner_bundle
readonly EXECUTING_BUILDER_SHA256="$(trusted_sha256_digest "${EXECUTING_BUILDER}")"
if ! /usr/bin/env -i LC_ALL=C TZ=UTC "${TRUSTED_NODE_BINARY}" - <<'NODE'
const [major, minor] = process.versions.node.split(".").map(Number);
if (major !== 22 || minor < 18) process.exit(1);
NODE
then
  fail '--node-bin must provide Node.js >=22.18.0 and <23'
fi
if ! /usr/bin/env -i LC_ALL=C TZ=UTC "${TRUSTED_NODE_BINARY}" - "${API_ORIGIN}" <<'NODE'
const input = process.argv[2];
let url;
try {
  url = new URL(input);
} catch {
  process.exit(1);
}
if (
  url.protocol !== "https:"
  || url.username !== ""
  || url.password !== ""
  || url.pathname !== "/"
  || url.search !== ""
  || url.hash !== ""
  || url.origin !== input
) {
  process.exit(1);
}
NODE
then
  fail '--api-origin must be one canonical HTTPS origin'
fi

[[ "${KEYSTORE_FILE}" = /* && -f "${KEYSTORE_FILE}" && -r "${KEYSTORE_FILE}" && ! -L "${KEYSTORE_FILE}" ]] ||
  fail '--keystore must name an absolute readable regular file and may not be a symlink'
readonly CANONICAL_KEYSTORE="$(realpath -e -- "${KEYSTORE_FILE}")"
[[ "${CANONICAL_KEYSTORE}" == "${KEYSTORE_FILE}" ]] ||
  fail '--keystore must use its canonical path and may not traverse symlinked ancestors'
[[ "$(stat_uid "${KEYSTORE_FILE}")" == "$(id -u)" ]] || fail '--keystore must be owned by the release user'
[[ "$(stat -c '%h' -- "${KEYSTORE_FILE}")" == "1" ]] || fail '--keystore must have exactly one hard link'
keystore_mode_text="$(stat_mode "${KEYSTORE_FILE}")"
keystore_mode=$((8#${keystore_mode_text}))
(( (keystore_mode & 8#7177) == 0 )) || fail '--keystore must be mode 0600 or stricter'
unset keystore_mode_text keystore_mode
validate_protected_ancestry '--keystore' "${CANONICAL_KEYSTORE}"

[[ "${OUTPUT_DIRECTORY}" = /* && ! -e "${OUTPUT_DIRECTORY}" && ! -L "${OUTPUT_DIRECTORY}" ]] ||
  fail '--output-dir must be an absolute path that does not exist'
readonly OUTPUT_PARENT_INPUT="$(dirname -- "${OUTPUT_DIRECTORY}")"
readonly OUTPUT_BASENAME="$(basename -- "${OUTPUT_DIRECTORY}")"
[[ "${OUTPUT_BASENAME}" != "." && "${OUTPUT_BASENAME}" != ".." ]] || fail '--output-dir has an invalid basename'
readonly OUTPUT_PARENT="$(realpath -e -- "${OUTPUT_PARENT_INPUT}")"
[[ "${OUTPUT_PARENT_INPUT}" == "${OUTPUT_PARENT}" ]] ||
  fail '--output-dir parent must use its canonical path and may not traverse symlinks'
[[ -d "${OUTPUT_PARENT}" && ! -L "${OUTPUT_PARENT}" ]] || fail '--output-dir parent must be a regular directory'
[[ "$(stat_uid "${OUTPUT_PARENT}")" == "$(id -u)" ]] || fail '--output-dir parent must be owned by the release user'
output_parent_mode_text="$(stat_mode "${OUTPUT_PARENT}")"
output_parent_mode=$((8#${output_parent_mode_text}))
(( (output_parent_mode & 8#022) == 0 )) || fail '--output-dir parent must not be group- or other-writable'
unset output_parent_mode_text output_parent_mode
[[ "${OUTPUT_DIRECTORY}" == "${OUTPUT_PARENT}/${OUTPUT_BASENAME}" ]] || fail '--output-dir must be canonical'
validate_protected_ancestry '--output-dir parent' "${OUTPUT_DIRECTORY}"

validate_toolchain_root 'JAVA_HOME' "${JAVA_HOME_INPUT}"
validate_toolchain_root 'ANDROID_HOME' "${ANDROID_HOME_INPUT}"

readonly REPOSITORY_GIT_DIR="${REPOSITORY_ROOT}/.git"
readonly REPOSITORY_OBJECTS_DIRECTORY="${REPOSITORY_GIT_DIR}/objects"
[[ -d "${REPOSITORY_GIT_DIR}" && ! -L "${REPOSITORY_GIT_DIR}" ]] ||
  fail 'release repository must use a canonical in-tree .git directory'
[[ "$(realpath -e -- "${REPOSITORY_GIT_DIR}")" == "${REPOSITORY_GIT_DIR}" ]] ||
  fail 'release repository .git directory must be canonical'
[[ -d "${REPOSITORY_OBJECTS_DIRECTORY}" && ! -L "${REPOSITORY_OBJECTS_DIRECTORY}" ]] ||
  fail 'release repository object store is unavailable'
validate_protected_ancestry \
  'release repository .git directory' \
  "${REPOSITORY_GIT_DIR}/.ascendany-repository-root"
validate_protected_ancestry \
  'release repository object store' \
  "${REPOSITORY_OBJECTS_DIRECTORY}/.ascendany-object-store"

WORK_ROOT="$(mktemp -d "${OUTPUT_PARENT}/.ascendany-android-release.XXXXXX")"
chmod 0700 -- "${WORK_ROOT}"
readonly SOURCE_ROOT="${WORK_ROOT}/source"
readonly PREFETCH_SOURCE_ROOT="${WORK_ROOT}/prefetch-source"
readonly ISOLATED_GIT_DIR="${WORK_ROOT}/repository.git"
readonly PROVENANCE_GIT_DIR="${WORK_ROOT}/provenance.git"
readonly PROVENANCE_INDEX="${WORK_ROOT}/provenance.index"
readonly ISOLATED_GIT_HOME="${WORK_ROOT}/git-home"
readonly ISOLATED_GIT_XDG_HOME="${WORK_ROOT}/git-xdg"
readonly BUILD_HOME="${WORK_ROOT}/build-home"
readonly BUILD_XDG_CONFIG_HOME="${WORK_ROOT}/xdg-config"
readonly BUILD_XDG_CACHE_HOME="${WORK_ROOT}/xdg-cache"
readonly BUILD_XDG_DATA_HOME="${WORK_ROOT}/xdg-data"
readonly GRADLE_USER_HOME_PRIVATE="${WORK_ROOT}/gradle-user-home"
readonly BUILD_TMPDIR="${WORK_ROOT}/tmp"
readonly PNPM_STORE_DIRECTORY="${WORK_ROOT}/pnpm-store"
readonly TRUSTED_TOOL_BIN="${WORK_ROOT}/trusted-tool-bin"
readonly SNAPSHOT_TOOL_PATH="${TRUSTED_TOOL_BIN}:/usr/bin:/bin"
readonly JAVA_TREE_LISTING="${WORK_ROOT}/java-tree-listing"
readonly ANDROID_PLATFORM_TREE_LISTING="${WORK_ROOT}/android-platform-tree-listing"
readonly ANDROID_BUILD_TOOLS_TREE_LISTING="${WORK_ROOT}/android-build-tools-tree-listing"
readonly ANDROID_PLATFORM_TOOLS_TREE_LISTING="${WORK_ROOT}/android-platform-tools-tree-listing"
install -d -m 0700 -- \
  "${SOURCE_ROOT}" \
  "${ISOLATED_GIT_DIR}/objects/info" \
  "${ISOLATED_GIT_DIR}/refs/heads" \
  "${ISOLATED_GIT_HOME}" \
  "${ISOLATED_GIT_XDG_HOME}" \
  "${BUILD_HOME}" \
  "${BUILD_XDG_CONFIG_HOME}" \
  "${BUILD_XDG_CACHE_HOME}" \
  "${BUILD_XDG_DATA_HOME}" \
  "${GRADLE_USER_HOME_PRIVATE}" \
  "${BUILD_TMPDIR}" \
  "${PNPM_STORE_DIRECTORY}" \
  "${TRUSTED_TOOL_BIN}"
validate_toolchain_tree 'JAVA_HOME' "${JAVA_HOME_INPUT}" "${JAVA_TREE_LISTING}"
validate_release_tool_file 'JAVA_HOME/bin/java' "${JAVA_HOME_INPUT}/bin/java"
validate_protected_ancestry 'JAVA_HOME/bin/java' "${JAVA_HOME_INPUT}/bin/java"
validate_toolchain_tree \
  'ANDROID_HOME/platforms/android-36' \
  "${ANDROID_HOME_INPUT}/platforms/android-36" \
  "${ANDROID_PLATFORM_TREE_LISTING}"
validate_release_data_file \
  'ANDROID_HOME/platforms/android-36/android.jar' \
  "${ANDROID_HOME_INPUT}/platforms/android-36/android.jar"
validate_toolchain_tree \
  'ANDROID_HOME/build-tools/36.0.0' \
  "${ANDROID_HOME_INPUT}/build-tools/36.0.0" \
  "${ANDROID_BUILD_TOOLS_TREE_LISTING}"
for android_build_tool in aapt2 d8 zipalign; do
  validate_release_tool_file \
    "ANDROID_HOME/build-tools/36.0.0/${android_build_tool}" \
    "${ANDROID_HOME_INPUT}/build-tools/36.0.0/${android_build_tool}"
done
unset android_build_tool
validate_toolchain_tree \
  'ANDROID_HOME/platform-tools' \
  "${ANDROID_HOME_INPUT}/platform-tools" \
  "${ANDROID_PLATFORM_TOOLS_TREE_LISTING}"
validate_release_tool_file \
  'ANDROID_HOME/platform-tools/adb' \
  "${ANDROID_HOME_INPUT}/platform-tools/adb"
ln -s -- "${TRUSTED_NODE_BINARY}" "${TRUSTED_TOOL_BIN}/node"
ln -s -- "${TRUSTED_NODE_BINARY}" "${TRUSTED_TOOL_BIN}/nodejs"
ln -s -- "${TRUSTED_PNPM_ENTRY}" "${TRUSTED_TOOL_BIN}/pnpm"
printf '%s\n' "${REPOSITORY_OBJECTS_DIRECTORY}" >"${ISOLATED_GIT_DIR}/objects/info/alternates"
printf '%s\n' \
  '[core]' \
  '    repositoryformatversion = 0' \
  '    bare = true' \
  >"${ISOLATED_GIT_DIR}/config"
printf '%s\n' 'ref: refs/heads/placeholder' >"${ISOLATED_GIT_DIR}/HEAD"
chmod 0600 -- \
  "${ISOLATED_GIT_DIR}/objects/info/alternates" \
  "${ISOLATED_GIT_DIR}/config" \
  "${ISOLATED_GIT_DIR}/HEAD"

if ! /usr/bin/env -i \
  PATH="${SYSTEM_PATH}" \
  HOME="${ISOLATED_GIT_HOME}" \
  XDG_CONFIG_HOME="${ISOLATED_GIT_XDG_HOME}" \
  LC_ALL=C \
  GIT_CONFIG_NOSYSTEM=1 \
  GIT_CONFIG_GLOBAL=/dev/null \
  GIT_ATTR_NOSYSTEM=1 \
  GIT_NO_REPLACE_OBJECTS=1 \
  GIT_NO_LAZY_FETCH=1 \
  GIT_TERMINAL_PROMPT=0 \
  /usr/bin/git \
    -c core.attributesFile=/dev/null \
    -c core.hooksPath=/dev/null \
    init --bare --object-format=sha1 --quiet "${PROVENANCE_GIT_DIR}"; then
  fail 'private Git provenance repository could not be initialized'
fi

run_isolated_git() {
  /usr/bin/env -i \
    PATH="${SYSTEM_PATH}" \
    HOME="${ISOLATED_GIT_HOME}" \
    XDG_CONFIG_HOME="${ISOLATED_GIT_XDG_HOME}" \
    LC_ALL=C \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_NO_REPLACE_OBJECTS=1 \
    GIT_NO_LAZY_FETCH=1 \
    GIT_TERMINAL_PROMPT=0 \
    /usr/bin/git \
      -c core.attributesFile=/dev/null \
      -c core.hooksPath=/dev/null \
      --git-dir="${ISOLATED_GIT_DIR}" \
      "$@"
}

run_provenance_git() {
  /usr/bin/env -i \
    PATH="${SYSTEM_PATH}" \
    HOME="${ISOLATED_GIT_HOME}" \
    XDG_CONFIG_HOME="${ISOLATED_GIT_XDG_HOME}" \
    LC_ALL=C \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_NO_REPLACE_OBJECTS=1 \
    GIT_NO_LAZY_FETCH=1 \
    GIT_TERMINAL_PROMPT=0 \
    GIT_DIR="${PROVENANCE_GIT_DIR}" \
    GIT_INDEX_FILE="${PROVENANCE_INDEX}" \
    /usr/bin/git \
      -c core.attributesFile=/dev/null \
      -c core.hooksPath=/dev/null \
      "$@"
}

validate_reviewed_path() {
  local relative="$1"
  local remaining component

  if [[ -z "${relative}" || "${relative}" == /* || "${relative}" == */ ||
        "${relative}" == *//* ]]; then
    return 1
  fi
  remaining="${relative}"
  while :; do
    component="${remaining%%/*}"
    if [[ -z "${component}" || "${component}" == . || "${component}" == .. ||
          "${component,,}" == .git ]]; then
      return 1
    fi
    if [[ "${remaining}" != */* ]]; then
      break
    fi
    remaining="${remaining#*/}"
  done
}

[[ "$(run_isolated_git rev-parse --show-object-format 2>/dev/null)" == sha1 ]] ||
  fail 'release repository must use the SHA-1 Git object format'
[[ "$(run_provenance_git rev-parse --show-object-format 2>/dev/null)" == sha1 ]] ||
  fail 'private Git provenance repository must use the SHA-1 object format'

readonly COMMIT_PAYLOAD="${WORK_ROOT}/commit.payload"
if ! run_isolated_git cat-file commit "${COMMIT}" >"${COMMIT_PAYLOAD}"; then
  fail 'reviewed commit payload could not be captured from the repository object store'
fi
chmod 0600 -- "${COMMIT_PAYLOAD}"
verified_commit="$(run_provenance_git hash-object -t commit -w --stdin <"${COMMIT_PAYLOAD}")" ||
  fail 'reviewed commit payload could not be hashed in the private provenance repository'
[[ "${verified_commit}" =~ ^[0-9a-f]{40}$ && "${verified_commit}" == "${COMMIT}" ]] ||
  fail 'reviewed commit payload failed isolated SHA-1 identity verification'
IFS= read -r commit_tree_header <"${COMMIT_PAYLOAD}" ||
  fail 'verified reviewed commit payload has no root tree header'
[[ "${commit_tree_header}" =~ ^tree\ ([0-9a-f]{40})$ ]] ||
  fail 'verified reviewed commit payload has an invalid root tree header'
readonly VERIFIED_COMMIT="${verified_commit}"
readonly ROOT_TREE="${BASH_REMATCH[1]}"
unset verified_commit commit_tree_header

readonly TREE_LISTING="${WORK_ROOT}/tree-listing"
readonly PROVENANCE_INDEX_INFORMATION="${WORK_ROOT}/provenance-index-information"
run_isolated_git ls-tree -rz --full-tree "${ROOT_TREE}" >"${TREE_LISTING}" ||
  fail 'reviewed commit tree could not be enumerated'
: >"${PROVENANCE_INDEX_INFORMATION}"
chmod 0600 -- "${PROVENANCE_INDEX_INFORMATION}"

materialized_entry_count=0
while IFS= read -r -d '' tree_entry; do
  tree_metadata="${tree_entry%%$'\t'*}"
  tree_path="${tree_entry#*$'\t'}"
  [[ "${tree_metadata}" != "${tree_entry}" && -n "${tree_path}" ]] ||
    fail 'reviewed commit contains a malformed tree entry'
  read -r tree_mode tree_type tree_object extra_metadata <<<"${tree_metadata}"
  [[ -z "${extra_metadata:-}" && "${tree_type}" == "blob" &&
     "${tree_object}" =~ ^[0-9a-f]{40}$ ]] ||
    fail 'reviewed commit contains a non-blob recursive tree entry'
  [[ "${tree_mode}" == "100644" || "${tree_mode}" == "100755" ]] ||
    fail '--commit contains a symlink, submodule, or unsupported file mode'
  validate_reviewed_path "${tree_path}" || fail 'reviewed commit contains an unsafe path'
  materialized_path="${SOURCE_ROOT}/${tree_path}"
  [[ ! -e "${materialized_path}" && ! -L "${materialized_path}" ]] ||
    fail 'reviewed commit contains a duplicate materialized path'
  install -d -m 0700 -- "$(dirname -- "${materialized_path}")"
  run_isolated_git cat-file blob "${tree_object}" >"${materialized_path}" ||
    fail 'reviewed commit blob failed integrity verification during materialization'
  materialized_object_id="$(
    run_provenance_git hash-object -w --no-filters -- "${materialized_path}"
  )" || fail 'materialized reviewed commit blob could not be hashed'
  [[ "${materialized_object_id}" == "${tree_object}" ]] ||
    fail 'reviewed commit blob failed integrity verification after materialization'
  if [[ "${tree_mode}" == "100755" ]]; then
    chmod 0755 -- "${materialized_path}"
  else
    chmod 0644 -- "${materialized_path}"
  fi
  printf '%s %s\t%s\0' \
    "${tree_mode}" \
    "${tree_object}" \
    "${tree_path}" \
    >>"${PROVENANCE_INDEX_INFORMATION}"
  ((materialized_entry_count += 1))
done <"${TREE_LISTING}"
(( materialized_entry_count > 0 )) || fail 'reviewed commit tree is empty'
run_provenance_git read-tree --empty ||
  fail 'private Git provenance index could not be initialized'
run_provenance_git update-index -z --index-info <"${PROVENANCE_INDEX_INFORMATION}" ||
  fail 'reviewed commit tree could not be reconstructed from verified blobs'
reconstructed_tree="$(run_provenance_git write-tree)" ||
  fail 'reviewed commit root tree could not be reconstructed'
[[ "${reconstructed_tree}" =~ ^[0-9a-f]{40}$ && "${reconstructed_tree}" == "${ROOT_TREE}" ]] ||
  fail 'reconstructed reviewed commit root tree differs from the verified commit payload'
unset materialized_entry_count tree_entry tree_metadata tree_path tree_mode tree_type tree_object
unset extra_metadata materialized_path materialized_object_id reconstructed_tree

readonly MATERIALIZED_GRADLEW="${SOURCE_ROOT}/apps/mobile/android/gradlew"
readonly MATERIALIZED_GRADLE_WRAPPER_PROPERTIES="${SOURCE_ROOT}/apps/mobile/android/gradle/wrapper/gradle-wrapper.properties"
readonly MATERIALIZED_GRADLE_WRAPPER_JAR="${SOURCE_ROOT}/apps/mobile/android/gradle/wrapper/gradle-wrapper.jar"
readonly MATERIALIZED_GRADLE_VERIFICATION_METADATA="${SOURCE_ROOT}/apps/mobile/android/gradle/verification-metadata.xml"
readonly MATERIALIZED_BUILDER="${SOURCE_ROOT}/apps/mobile/scripts/build-android-release.sh"

validate_materialized_gradle_wrapper() {
  local properties_sha256 gradlew_sha256 wrapper_jar_sha256 verification_metadata_sha256
  [[ -f "${MATERIALIZED_GRADLEW}" && ! -L "${MATERIALIZED_GRADLEW}" && \
     "$(stat_mode "${MATERIALIZED_GRADLEW}")" == "755" ]] ||
    fail 'materialized Gradle launcher must be one regular mode 0755 file'
  [[ -f "${MATERIALIZED_GRADLE_WRAPPER_PROPERTIES}" && ! -L "${MATERIALIZED_GRADLE_WRAPPER_PROPERTIES}" && \
     "$(stat_mode "${MATERIALIZED_GRADLE_WRAPPER_PROPERTIES}")" == "644" ]] ||
    fail 'materialized Gradle wrapper properties must be one regular mode 0644 file'
  [[ -f "${MATERIALIZED_GRADLE_WRAPPER_JAR}" && ! -L "${MATERIALIZED_GRADLE_WRAPPER_JAR}" && \
     "$(stat_mode "${MATERIALIZED_GRADLE_WRAPPER_JAR}")" == "644" ]] ||
    fail 'materialized Gradle wrapper JAR must be one regular mode 0644 file'
  [[ -f "${MATERIALIZED_GRADLE_VERIFICATION_METADATA}" && \
     ! -L "${MATERIALIZED_GRADLE_VERIFICATION_METADATA}" && \
     "$(stat_mode "${MATERIALIZED_GRADLE_VERIFICATION_METADATA}")" == "644" ]] ||
    fail 'materialized Gradle verification metadata must be one regular mode 0644 file'
  properties_sha256="$(trusted_sha256_digest "${MATERIALIZED_GRADLE_WRAPPER_PROPERTIES}")" ||
    fail 'materialized Gradle wrapper properties SHA-256 could not be calculated'
  [[ "${properties_sha256}" == "bbdad274aefbd87a4306aaeb0e4399a72dd841cbdbe3e84257cbb7a6c83e4ef3" ]] ||
    fail 'materialized Gradle wrapper properties differ from the exact 8.14.3 release contract'
  gradlew_sha256="$(trusted_sha256_digest "${MATERIALIZED_GRADLEW}")" ||
    fail 'materialized Gradle launcher SHA-256 could not be calculated'
  [[ "${gradlew_sha256}" == "b187b4c52e749f5760afdd6fadc31b2a98ad35fb249bf0dff03b72650f320409" ]] ||
    fail 'materialized Gradle launcher differs from the pinned release launcher'
  wrapper_jar_sha256="$(trusted_sha256_digest "${MATERIALIZED_GRADLE_WRAPPER_JAR}")" ||
    fail 'materialized Gradle wrapper JAR SHA-256 could not be calculated'
  [[ "${wrapper_jar_sha256}" == "7d3a4ac4de1c32b59bc6a4eb8ecb8e612ccd0cf1ae1e99f66902da64df296172" ]] ||
    fail 'materialized Gradle wrapper JAR differs from the official 8.14.3 wrapper JAR'
  verification_metadata_sha256="$(trusted_sha256_digest "${MATERIALIZED_GRADLE_VERIFICATION_METADATA}")" ||
    fail 'materialized Gradle verification metadata SHA-256 could not be calculated'
  [[ "${verification_metadata_sha256}" == "c8dedffdec0c4c3eac03c6ac9b97fac9efb5ea1c08687462098214e80032775e" ]] ||
    fail 'materialized Gradle verification metadata differs from the pinned dependency contract'
}

validate_materialized_gradle_wrapper
[[ -f "${MATERIALIZED_BUILDER}" && ! -L "${MATERIALIZED_BUILDER}" && \
   "$(stat_mode "${MATERIALIZED_BUILDER}")" == "755" ]] ||
  fail 'materialized Android release wrapper must be one regular mode 0755 file'
[[ "$(trusted_sha256_digest "${MATERIALIZED_BUILDER}")" == "${EXECUTING_BUILDER_SHA256}" ]] &&
  diff --brief -- "${EXECUTING_BUILDER}" "${MATERIALIZED_BUILDER}" >/dev/null ||
  fail 'executing Android release wrapper differs from reviewed commit'

if ! /usr/bin/env -i LC_ALL=C TZ=UTC "${TRUSTED_NODE_BINARY}" - "${SOURCE_ROOT}/package.json" <<'NODE'
const fs = require("node:fs");
const packageJson = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
if (packageJson.packageManager !== "pnpm@9.15.4") process.exit(1);
NODE
then
  fail 'materialized package.json must pin packageManager exactly to pnpm@9.15.4'
fi
pnpm_version=""
cd -- "${SOURCE_ROOT}"
if ! pnpm_version="$(run_snapshot_pnpm offline --version)"; then
  fail '--pnpm-entry failed its closed-environment version check'
fi
[[ "${pnpm_version}" == "9.15.4" ]] || fail '--pnpm-entry must report exactly pnpm 9.15.4'
unset pnpm_version

run_snapshot_pnpm \
  fetch \
  --filter '@ascendany/mobile...' \
  fetch \
  --frozen-lockfile \
  --store-dir "${PNPM_STORE_DIRECTORY}"
run_snapshot_pnpm \
  offline \
  --filter '@ascendany/mobile...' \
  install \
  --offline \
  --ignore-scripts \
  --frozen-lockfile \
  --store-dir "${PNPM_STORE_DIRECTORY}"
VITE_API_BASE_URL="${API_ORIGIN}" \
VITE_CHAT_PROMPT_CONFIGURATION_KEY="${PROMPT_KEY}" \
VITE_CHAT_MODEL_CONFIGURATION_KEY="${MODEL_KEY}" \
  run_snapshot_pnpm offline --filter @ascendany/mobile sync:android

validate_materialized_gradle_wrapper
[[ "$(trusted_sha256_digest "${MATERIALIZED_BUILDER}")" == "${EXECUTING_BUILDER_SHA256}" ]] &&
  diff --brief -- "${EXECUTING_BUILDER}" "${MATERIALIZED_BUILDER}" >/dev/null ||
  fail 'materialized Android release wrapper changed during pnpm execution'

gradle_environment=(
  /usr/bin/env -i
  HOME="${BUILD_HOME}"
  GRADLE_USER_HOME="${GRADLE_USER_HOME_PRIVATE}"
  TMPDIR="${BUILD_TMPDIR}"
  PATH="${SNAPSHOT_TOOL_PATH}"
  LC_ALL=C
  ASCENDANY_ANDROID_RELEASE_WRAPPER=1
  ASCENDANY_ANDROID_VERSION_NAME="${VERSION}"
  ASCENDANY_ANDROID_VERSION_CODE="${VERSION_CODE}"
)
[[ -z "${JAVA_HOME_INPUT}" ]] || gradle_environment+=("JAVA_HOME=${JAVA_HOME_INPUT}")
[[ -z "${ANDROID_HOME_INPUT}" ]] || gradle_environment+=("ANDROID_HOME=${ANDROID_HOME_INPUT}")

run_gradle_namespace() {
  local source_root="$1"
  local network_policy="$2"
  shift 2
  local -a namespace=(
    "${TRUSTED_BWRAP_BINARY}"
    --die-with-parent
    --new-session
    --unshare-pid
    --unshare-ipc
    --unshare-uts
    --ro-bind /usr /usr
    --symlink usr/bin /bin
    --symlink usr/sbin /sbin
    --symlink usr/lib /lib
    --symlink usr/lib64 /lib64
    --ro-bind /etc /etc
    --ro-bind /sys /sys
    --ro-bind "${TRUSTED_RESOLV_CONF}" "${TRUSTED_RESOLV_CONF}"
    --ro-bind "${JAVA_HOME_INPUT}" "${JAVA_HOME_INPUT}"
    --ro-bind
      "${ANDROID_HOME_INPUT}/platforms/android-36"
      "${ANDROID_HOME_INPUT}/platforms/android-36"
    --ro-bind
      "${ANDROID_HOME_INPUT}/build-tools/36.0.0"
      "${ANDROID_HOME_INPUT}/build-tools/36.0.0"
    --ro-bind
      "${ANDROID_HOME_INPUT}/platform-tools"
      "${ANDROID_HOME_INPUT}/platform-tools"
    --proc /proc
    --dev /dev
    --ro-bind /dev/null "${CANONICAL_KEYSTORE}"
    --bind "${source_root}" "${source_root}"
    --bind "${BUILD_HOME}" "${BUILD_HOME}"
    --bind "${GRADLE_USER_HOME_PRIVATE}" "${GRADLE_USER_HOME_PRIVATE}"
    --bind "${BUILD_TMPDIR}" "${BUILD_TMPDIR}"
    --ro-bind "${TRUSTED_TOOL_BIN}" "${TRUSTED_TOOL_BIN}"
    --chdir "${source_root}/apps/mobile/android"
  )
  case "${network_policy}" in
    fetch) ;;
    offline) namespace+=(--unshare-net) ;;
    *) fail 'internal Gradle namespace policy is invalid' ;;
  esac
  /usr/bin/env -i PATH="${SYSTEM_PATH}" LC_ALL=C "${namespace[@]}" -- \
    "${gradle_environment[@]}" \
    "${source_root}/apps/mobile/android/gradlew" \
    "$@"
}

install -d -m 0700 -- "${PREFETCH_SOURCE_ROOT}"
cp -a --reflink=auto -- "${SOURCE_ROOT}/." "${PREFETCH_SOURCE_ROOT}/"
run_gradle_namespace \
  "${PREFETCH_SOURCE_ROOT}" \
  fetch \
  --dependency-verification=strict \
  --no-daemon \
  --max-workers "${GRADLE_MAX_WORKERS}" \
  :app:assembleRelease
rm -rf -- "${PREFETCH_SOURCE_ROOT}"
[[ ! -e "${PREFETCH_SOURCE_ROOT}" && ! -L "${PREFETCH_SOURCE_ROOT}" ]] ||
  fail 'Gradle dependency prefetch source could not be removed'
validate_materialized_gradle_wrapper
run_gradle_namespace \
  "${SOURCE_ROOT}" \
  offline \
  --dependency-verification=strict \
  --offline \
  --no-build-cache \
  --no-daemon \
  --max-workers "${GRADLE_MAX_WORKERS}" \
  :app:assembleRelease
unset gradle_environment

readonly UNSIGNED_OUTPUT_DIRECTORY="${SOURCE_ROOT}/apps/mobile/android/app/build/outputs/apk/release"
readonly BUILT_UNSIGNED_APK="${UNSIGNED_OUTPUT_DIRECTORY}/app-release-unsigned.apk"
readonly SIGNING_ROOT="$(mktemp -d "${WORK_ROOT}/signing.XXXXXX")"
chmod 0700 -- "${SIGNING_ROOT}"
readonly SIGNING_HOME="${SIGNING_ROOT}/home"
readonly SIGNING_TMPDIR="${SIGNING_ROOT}/tmp"
readonly SIGNING_TOOL_BIN="${SIGNING_ROOT}/tool-bin"
readonly SNAPSHOT_APKSIGNER_ROOT="${SIGNING_ROOT}/apksigner-bundle"
readonly SNAPSHOT_APKSIGNER_LIB="${SNAPSHOT_APKSIGNER_ROOT}/lib"
snapshot_apksigner_basename="$(basename -- "${TRUSTED_APKSIGNER_BINARY}")"
[[ "${snapshot_apksigner_basename}" =~ ^[A-Za-z0-9._-]{1,128}$ ]] ||
  fail '--apksigner-bin basename is invalid'
readonly SNAPSHOT_APKSIGNER_BINARY="${SNAPSHOT_APKSIGNER_ROOT}/${snapshot_apksigner_basename}"
readonly SNAPSHOT_APKSIGNER_JAR="${SNAPSHOT_APKSIGNER_LIB}/apksigner.jar"
unset snapshot_apksigner_basename
install -d -m 0700 -- \
  "${SIGNING_HOME}" \
  "${SIGNING_TMPDIR}" \
  "${SIGNING_TOOL_BIN}" \
  "${SNAPSHOT_APKSIGNER_ROOT}" \
  "${SNAPSHOT_APKSIGNER_LIB}"
readonly SIGNED_APK="${SIGNING_ROOT}/app-release-signed.apk"
[[ ! -e "${SIGNED_APK}" && ! -L "${SIGNED_APK}" ]] ||
  fail 'private signing output path already exists'
[[ -d "${UNSIGNED_OUTPUT_DIRECTORY}" && ! -L "${UNSIGNED_OUTPUT_DIRECTORY}" ]] ||
  fail 'Gradle must produce one regular APK output directory'
readonly APK_NODE_LISTING="${WORK_ROOT}/apk-node-listing"
readonly SORTED_APK_NODE_LISTING="${WORK_ROOT}/apk-node-listing.sorted"
find "${UNSIGNED_OUTPUT_DIRECTORY}" -mindepth 1 -name '*.apk' -printf '%P\0' >"${APK_NODE_LISTING}" ||
  fail 'Gradle APK output subtree could not be enumerated completely'
sort -z <"${APK_NODE_LISTING}" >"${SORTED_APK_NODE_LISTING}" ||
  fail 'Gradle APK output inventory could not be sorted'
mapfile -d '' -t release_apks <"${SORTED_APK_NODE_LISTING}"
if (( ${#release_apks[@]} != 1 )) || [[ "${release_apks[0]:-}" != "app-release-unsigned.apk" ]] ||
   [[ ! -f "${BUILT_UNSIGNED_APK}" || -L "${BUILT_UNSIGNED_APK}" ]]; then
  fail 'Gradle must produce exactly one app-release-unsigned.apk'
fi
unset release_apks

validate_toolchain_tree 'JAVA_HOME' "${JAVA_HOME_INPUT}" "${JAVA_TREE_LISTING}"
validate_release_tool_file 'JAVA_HOME/bin/java' "${JAVA_HOME_INPUT}/bin/java"
validate_protected_ancestry 'JAVA_HOME/bin/java' "${JAVA_HOME_INPUT}/bin/java"
validate_trusted_apksigner_bundle
ln -s -- "${JAVA_HOME_INPUT}/bin/java" "${SIGNING_TOOL_BIN}/java"
[[ "$(realpath -e -- "${SIGNING_TOOL_BIN}/java")" == "${JAVA_HOME_INPUT}/bin/java" ]] ||
  fail 'private signer java command does not resolve to validated JAVA_HOME/bin/java'
install -m 0500 -- "${TRUSTED_APKSIGNER_BINARY}" "${SNAPSHOT_APKSIGNER_BINARY}"
install -m 0400 -- "${TRUSTED_APKSIGNER_JAR}" "${SNAPSHOT_APKSIGNER_JAR}"
[[ "$(trusted_sha256_digest "${SNAPSHOT_APKSIGNER_BINARY}")" == "${PINNED_APKSIGNER_BINARY_SHA256}" ]] ||
  fail 'private apksigner launcher snapshot differs from the pinned Android Build Tools 36.0.0 launcher'
[[ "$(trusted_sha256_digest "${SNAPSHOT_APKSIGNER_JAR}")" == "${PINNED_APKSIGNER_JAR_SHA256}" ]] ||
  fail 'private apksigner JAR snapshot differs from the pinned Android Build Tools 36.0.0 JAR'

run_apksigner_sign() (
  set +x
  signer_environment=(
    /usr/bin/env -i
    HOME="${SIGNING_HOME}"
    TMPDIR="${SIGNING_TMPDIR}"
    PATH="${SIGNING_TOOL_BIN}:${SYSTEM_PATH}"
    LC_ALL=C
  )
  [[ -z "${JAVA_HOME_INPUT}" ]] || signer_environment+=("JAVA_HOME=${JAVA_HOME_INPUT}")
  "${signer_environment[@]}" /usr/bin/bash -p -c '
    set -Eeuo pipefail
    set +x
    IFS= read -r -d "" ASCENDANY_APKSIGNER_STORE_PASSWORD <&3
    IFS= read -r -d "" ASCENDANY_APKSIGNER_KEY_PASSWORD <&4
    exec 3<&- 4<&-
    export ASCENDANY_APKSIGNER_STORE_PASSWORD ASCENDANY_APKSIGNER_KEY_PASSWORD
    exec "$@"
  ' ascendany-apksigner-sign \
    "${SNAPSHOT_APKSIGNER_BINARY}" sign \
      --ks "${CANONICAL_KEYSTORE}" \
      --ks-key-alias "${KEY_ALIAS}" \
      --ks-pass env:ASCENDANY_APKSIGNER_STORE_PASSWORD \
      --key-pass env:ASCENDANY_APKSIGNER_KEY_PASSWORD \
      --out "${SIGNED_APK}" \
      "${BUILT_UNSIGNED_APK}" \
    3< <(printf '%s\0' "${STORE_PASSWORD_VALUE}") \
    4< <(printf '%s\0' "${KEY_PASSWORD_VALUE}")
)
run_apksigner_sign
unset STORE_PASSWORD_VALUE KEY_PASSWORD_VALUE
[[ -f "${SIGNED_APK}" && ! -L "${SIGNED_APK}" ]] || fail 'isolated apksigner did not produce the signed APK'

run_apksigner_verify() (
  verifier_environment=(
    /usr/bin/env -i
    HOME="${SIGNING_HOME}"
    TMPDIR="${SIGNING_TMPDIR}"
    PATH="${SIGNING_TOOL_BIN}:${SYSTEM_PATH}"
    LC_ALL=C
  )
  [[ -z "${JAVA_HOME_INPUT}" ]] || verifier_environment+=("JAVA_HOME=${JAVA_HOME_INPUT}")
  exec "${verifier_environment[@]}" \
    "${SNAPSHOT_APKSIGNER_BINARY}" verify -Werr --verbose --print-certs "${SIGNED_APK}"
)
verification_output=""
if ! verification_output="$(run_apksigner_verify 2>&1)"; then
  printf '%s\n' "${verification_output}" >&2
  fail 'apksigner rejected the release APK'
fi
mapfile -t signer_fingerprints < <(
  printf '%s\n' "${verification_output}" |
    sed -n -E 's/^Signer #[0-9]+ certificate SHA-256 digest: ([0-9A-Fa-f]{64})$/\1/p'
)
(( ${#signer_fingerprints[@]} == 1 )) || fail 'the release APK must contain exactly one signer SHA-256 certificate digest'
actual_signer_sha256="${signer_fingerprints[0],,}"
[[ "${actual_signer_sha256}" == "${EXPECTED_SIGNER_SHA256}" ]] || fail 'the release APK signer SHA-256 fingerprint does not match --signer-sha256'
unset verification_output signer_fingerprints actual_signer_sha256
unset JAVA_HOME_INPUT ANDROID_HOME_INPUT

sha512_digest() {
  local file_path="$1"
  local digest_output
  digest_output="$(/usr/bin/env -i LC_ALL=C "${TRUSTED_SHA512SUM_BINARY}" -- "${file_path}")" ||
    fail 'trusted /usr/bin/sha512sum failed'
  printf '%s\n' "${digest_output%% *}"
}

readonly ARTIFACT_NAME="AscendAny-Android-${VERSION}.apk"
readonly STAGED_ARTIFACT="${WORK_ROOT}/${ARTIFACT_NAME}"
readonly STAGED_CHECKSUM="${STAGED_ARTIFACT}.sha512"
readonly PUBLISHED_OUTPUT="${WORK_ROOT}/published-output"
[[ ! -e "${STAGED_ARTIFACT}" && ! -L "${STAGED_ARTIFACT}" && \
   ! -e "${STAGED_CHECKSUM}" && ! -L "${STAGED_CHECKSUM}" && \
   ! -e "${PUBLISHED_OUTPUT}" && ! -L "${PUBLISHED_OUTPUT}" ]] ||
  fail 'private artifact staging path already exists'
install -m 0644 -- "${SIGNED_APK}" "${STAGED_ARTIFACT}"
artifact_digest="$(sha512_digest "${STAGED_ARTIFACT}")"
[[ "${artifact_digest}" =~ ^[0-9a-fA-F]{128}$ ]] || fail 'SHA-512 implementation returned an invalid digest'
readonly ARTIFACT_SHA512="${artifact_digest,,}"
readonly ARTIFACT_SIZE="$(stat -c '%s' -- "${STAGED_ARTIFACT}")"
[[ "${ARTIFACT_SIZE}" =~ ^[1-9][0-9]*$ ]] || fail 'signed Android artifact has an invalid size'
printf '%s  %s\n' "${ARTIFACT_SHA512}" "${ARTIFACT_NAME}" >"${STAGED_CHECKSUM}"
chmod 0644 -- "${STAGED_CHECKSUM}"
unset artifact_digest

verify_android_output() {
  local output_root="$1"
  local label="$2"
  local output_artifact="${output_root}/${ARTIFACT_NAME}"
  local output_checksum="${output_artifact}.sha512"
  local output_path actual_mode actual_owner actual_links
  [[ -d "${output_root}" && ! -L "${output_root}" ]] ||
    fail "${label} output root must be one regular directory"
  [[ "$(stat_uid "${output_root}")" == "$(id -u)" && "$(stat_mode "${output_root}")" == "700" ]] ||
    fail "${label} output root has invalid ownership or mode"
  printf '%s\n' "${ARTIFACT_NAME}" "${ARTIFACT_NAME}.sha512" | sort >"${WORK_ROOT}/expected-output-paths"
  find "${output_root}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort >"${WORK_ROOT}/actual-output-paths"
  diff -u "${WORK_ROOT}/expected-output-paths" "${WORK_ROOT}/actual-output-paths" ||
    fail "${label} Android release output differs from its exact two-file contract"
  find "${output_root}" -mindepth 1 ! -type f -print -quit >"${WORK_ROOT}/nonregular-output-nodes" ||
    fail "${label} Android release output node inventory could not be enumerated"
  [[ ! -s "${WORK_ROOT}/nonregular-output-nodes" ]] ||
    fail "${label} Android release output contains a directory, symlink, or special node"
  for output_path in "${output_artifact}" "${output_checksum}"; do
    [[ -f "${output_path}" && ! -L "${output_path}" ]] ||
      fail "${label} Android release output contains a non-regular file"
    actual_mode="$(stat_mode "${output_path}")"
    actual_owner="$(stat_uid "${output_path}")"
    actual_links="$(stat -c '%h' -- "${output_path}")"
    [[ "${actual_mode}" == "644" && "${actual_owner}" == "$(id -u)" && "${actual_links}" == "1" ]] ||
      fail "${label} Android release file has invalid ownership, mode, or link count"
  done
  [[ "$(stat -c '%s' -- "${output_artifact}")" == "${ARTIFACT_SIZE}" ]] ||
    fail "${label} Android release artifact size differs from the signed artifact"
  [[ "$(sha512_digest "${output_artifact}")" == "${ARTIFACT_SHA512}" ]] ||
    fail "${label} Android release artifact digest differs from the signed artifact"
  diff -u "${STAGED_CHECKSUM}" "${output_checksum}" ||
    fail "${label} Android release checksum sidecar differs from the staged sidecar"
  (
    cd -- "${output_root}"
    /usr/bin/env -i LC_ALL=C "${TRUSTED_SHA512SUM_BINARY}" --check --strict -- "${ARTIFACT_NAME}.sha512" >/dev/null
  ) || fail "${label} Android release checksum verification failed"
}

install -d -m 0700 -- "${PUBLISHED_OUTPUT}"
install -m 0644 -- "${STAGED_ARTIFACT}" "${PUBLISHED_OUTPUT}/${ARTIFACT_NAME}"
install -m 0644 -- "${STAGED_CHECKSUM}" "${PUBLISHED_OUTPUT}/${ARTIFACT_NAME}.sha512"
verify_android_output "${PUBLISHED_OUTPUT}" staged
/usr/bin/sync -f -- \
  "${PUBLISHED_OUTPUT}/${ARTIFACT_NAME}" \
  "${PUBLISHED_OUTPUT}/${ARTIFACT_NAME}.sha512" \
  "${PUBLISHED_OUTPUT}"
exec {PUBLISHED_OUTPUT_DIRECTORY_FD}<"${PUBLISHED_OUTPUT}"
PUBLISHED_OUTPUT_IDENTITY="$(stat -Lc '%d:%i' -- "/proc/self/fd/${PUBLISHED_OUTPUT_DIRECTORY_FD}")"
[[ "${PUBLISHED_OUTPUT_IDENTITY}" =~ ^[0-9]+:[0-9]+$ ]] ||
  fail 'staged Android release directory identity could not be captured'
PUBLISHED_OUTPUT_CLEANUP_ARMED=1
if ! mv --no-target-directory --no-clobber -- "${PUBLISHED_OUTPUT}" "${OUTPUT_DIRECTORY}"; then
  fail 'Android release publication failed without replacing the target'
fi
[[ ! -e "${PUBLISHED_OUTPUT}" && ! -L "${PUBLISHED_OUTPUT}" ]] ||
  fail 'Android release publication target appeared during the no-replace rename'
/usr/bin/sync -f -- "${OUTPUT_DIRECTORY}" "${OUTPUT_PARENT}"
verify_android_output "${OUTPUT_DIRECTORY}" published
[[ "$(stat -Lc '%d:%i' -- "${OUTPUT_DIRECTORY}")" == "${PUBLISHED_OUTPUT_IDENTITY}" &&
   "$(stat -Lc '%d:%i' -- "/proc/self/fd/${PUBLISHED_OUTPUT_DIRECTORY_FD}")" == "${PUBLISHED_OUTPUT_IDENTITY}" ]] ||
  fail 'published Android release directory identity changed during final verification'
PUBLISHED_OUTPUT_CLEANUP_ARMED=0
exec {PUBLISHED_OUTPUT_DIRECTORY_FD}<&-
PUBLISHED_OUTPUT_IDENTITY=""

printf 'Android release published from commit %s\n' "${COMMIT}"
printf 'Artifact: %s\n' "${OUTPUT_DIRECTORY}/${ARTIFACT_NAME}"
printf 'Signer SHA-256: %s\n' "${EXPECTED_SIGNER_SHA256}"
