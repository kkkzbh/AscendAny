#!/usr/bin/bash -p
if [[ "${BASH:-}" != "/usr/bin/bash" ||
      "$-" != *p* ||
      "$-" == *[cis]* ||
      -n "${BASH_EXECUTION_STRING:-}" ||
      "${#BASH_SOURCE[@]}" -ne 1 ||
      "${BASH_SOURCE[0]}" != "$0" ]]; then
  /usr/bin/printf '%s\n' 'desktop release builder must run directly under /usr/bin/bash -p' >&2
  /usr/bin/kill -KILL "${BASHPID}"
fi
set +x

early_fail() {
  builtin printf '%s\n' "$1" >&2
  builtin kill -KILL "${BASHPID}"
}

(( EUID != 0 )) || early_fail 'desktop release builder requires a dedicated non-root release identity'

if [[ -v CSC_KEY_PASSWORD || -v WIN_CSC_KEY_PASSWORD || -v CERTIFICATE_PASSWORD ]]; then
  early_fail 'desktop release builder rejects signing passwords in process environment'
fi
certificate_password_fd="${ASCENDANY_DESKTOP_CSC_PASSWORD_FD:-}"
[[ "${certificate_password_fd}" =~ ^[1-9][0-9]{0,3}$ ]] &&
  (( 10#${certificate_password_fd} >= 3 && 10#${certificate_password_fd} <= 1023 )) ||
  early_fail 'ASCENDANY_DESKTOP_CSC_PASSWORD_FD must be one canonical decimal descriptor from 3 through 1023'
[[ -e "/proc/self/fd/${certificate_password_fd}" &&
   ( -f "/proc/self/fd/${certificate_password_fd}" || -p "/proc/self/fd/${certificate_password_fd}" ) ]] ||
  early_fail 'ASCENDANY_DESKTOP_CSC_PASSWORD_FD must name one inherited readable file or pipe descriptor'
CERTIFICATE_FILE="${CSC_LINK:-}"
builtin export -n ASCENDANY_DESKTOP_CSC_PASSWORD_FD CSC_LINK CERTIFICATE_FILE
builtin unset ASCENDANY_DESKTOP_CSC_PASSWORD_FD CSC_LINK

declare -a environment_removals=()
invalid_environment_name=0
while IFS= read -r -d '' environment_entry; do
  environment_name="${environment_entry%%=*}"
  if [[ ! "${environment_name}" =~ ^[A-Za-z_][0-9A-Za-z_]*$ ]]; then
    invalid_environment_name=1
    continue
  fi
  case "$environment_name" in
    INIT_CWD|NODE_OPTIONS|NODE_PATH|npm_*|NPM_*|pnpm_*|PNPM_*|corepack_*|COREPACK_*|ELECTRON_*|CSC_*|WIN_CSC_*|CERTIFICATE_FILE)
      environment_removals+=( "$environment_name" )
      ;;
  esac
done < <(
  exec {certificate_password_fd}<&-
  /usr/bin/env -0
)
if (( invalid_environment_name != 0 )); then
  early_fail 'desktop release builder rejects environment names outside the shell variable contract'
fi
for environment_name in "${environment_removals[@]}"; do
  builtin unset "${environment_name}"
done
unset environment_removals environment_entry environment_name invalid_environment_name
builtin unset BASH_ENV ENV CDPATH GLOBIGNORE
builtin export -n SHELLOPTS BASHOPTS
set -Eeuo pipefail

export LC_ALL=C

script_source="${BASH_SOURCE[0]}"
if [[ "${script_source}" != /* ]]; then
  script_source="${PWD}/${script_source}"
fi
readonly BUILDER_PATH="${script_source}"
readonly SCRIPT_DIR="${script_source%/*}"
readonly DESKTOP_ROOT="${SCRIPT_DIR%/*}"
apps_root="${DESKTOP_ROOT%/*}"
readonly REPOSITORY_ROOT="${apps_root%/*}"
unset apps_root
unset script_source
readonly VERSION="${ASCENDANY_DESKTOP_VERSION:-}"
readonly REQUESTED_COMMIT="${ASCENDANY_DESKTOP_RELEASE_COMMIT:-}"
readonly OUTPUT_DIRECTORY="${ASCENDANY_DESKTOP_OUTPUT_DIRECTORY:-}"
readonly NODE_BINARY="${ASCENDANY_DESKTOP_NODE_PATH:-}"
readonly PNPM_CLI="${ASCENDANY_DESKTOP_PNPM_CLI_PATH:-}"
readonly BWRAP_BINARY="${ASCENDANY_DESKTOP_BWRAP_PATH:-}"
readonly BUILD_TOOL_ROOT="${ASCENDANY_DESKTOP_BUILD_TOOL_ROOT:-}"
readonly PNPM_STORE_SEED="${ASCENDANY_DESKTOP_PNPM_STORE_PATH:-}"
readonly BUILD_CACHE_SEED="${ASCENDANY_DESKTOP_BUILD_CACHE_PATH:-}"
readonly OPENSSL_BINARY="${ASCENDANY_DESKTOP_OPENSSL_PATH:-}"
readonly OSSLSIGNCODE_BINARY="${ASCENDANY_DESKTOP_OSSLSIGNCODE_PATH:-}"
readonly RELEASE_HOME="${HOME:-}"
readonly API_ORIGIN="${VITE_API_BASE_URL:-}"
readonly PROMPT_KEY="${VITE_CHAT_PROMPT_CONFIGURATION_KEY:-}"
readonly MODEL_KEY="${VITE_CHAT_MODEL_CONFIGURATION_KEY:-}"

fail() {
  printf '%s\n' "$1" >&2
  exit 2
}

validate_protected_directory_ancestry() {
  local label="$1"
  local directory="$2"
  local current=/
  local component owner mode_text mode
  local -a components=()

  [[ "${directory}" = /* && ! "${directory}" =~ [[:cntrl:]] ]] ||
    fail "${label} must use one absolute path without control characters"
  IFS=/ read -r -a components <<<"${directory#/}"
  for component in '' "${components[@]}"; do
    if [[ -n "${component}" ]]; then
      current="${current%/}/${component}"
    fi
    [[ -d "${current}" && ! -L "${current}" ]] ||
      fail "${label} has a missing, non-directory, or symlink ancestor: ${current}"
    owner="$(/usr/bin/stat -c '%u' -- "${current}")"
    mode_text="$(/usr/bin/stat -c '%a' -- "${current}")"
    mode="$((8#${mode_text}))"
    [[ "${owner}" == 0 || "${owner}" == "${EUID}" ]] ||
      fail "${label} has an ancestor outside the root/release-user ownership boundary: ${current}"
    (( (mode & 8#022) == 0 || (owner == 0 && (mode & 8#1000) != 0) )) ||
      fail "${label} has an unprotected writable ancestor: ${current}"
  done
}

validate_protected_file_ancestry() {
  local label="$1"
  local file="$2"
  validate_protected_directory_ancestry "${label}" "$(/usr/bin/dirname -- "${file}")"
}

path_contains() {
  local container="$1"
  local path="$2"
  [[ "${path}" == "${container}" || "${path}" == "${container}/"* ]]
}

close_inherited_fds_except() {
  local descriptor_path descriptor keep_descriptor keep=0

  for descriptor_path in /proc/self/fd/[0-9]*; do
    descriptor="${descriptor_path##*/}"
    (( descriptor > 2 )) || continue
    keep=0
    for keep_descriptor in "$@"; do
      [[ "${descriptor}" != "${keep_descriptor}" ]] || keep=1
    done
    if (( keep == 0 )); then
      exec {descriptor}>&- || true
    fi
  done
}

validate_seed_tree() {
  local label="$1"
  local root="$2"
  local entry owner mode

  while IFS= read -r -d '' entry; do
    [[ ! -L "${entry}" && ( -d "${entry}" || -f "${entry}" ) ]] ||
      fail "${label} contains a symlink or special node"
    owner="$(stat -Lc '%u' -- "${entry}")"
    mode="$((8#$(stat -Lc '%a' -- "${entry}")))"
    [[ "${owner}" == 0 || "${owner}" == "${EUID}" ]] ||
      fail "${label} contains an entry outside the root/release-user ownership boundary"
    (( (mode & 8#022) == 0 )) ||
      fail "${label} contains a group- or other-writable entry"
  done < <(find "${root}" -mindepth 1 -print0)
}

validate_release_tool() {
  local label="$1"
  local path="$2"
  local mode owner

  [[ "${path}" =~ ^/[0-9A-Za-z_./:+-]+$ ]] ||
    fail "${label} contains a character outside the release-tool path contract"
  [[ "${path}" = /* && -f "${path}" && ! -L "${path}" && -x "${path}" ]] ||
    fail "${label} must name an absolute executable regular file"
  [[ "${path}" == "$(/usr/bin/realpath -e -- "${path}")" ]] ||
    fail "${label} must be one canonical path without symlink ancestry"
  owner="$(/usr/bin/stat -Lc '%u' -- "${path}")"
  [[ "${owner}" == 0 || "${owner}" == "${EUID}" ]] ||
    fail "${label} must be owned by root or the release user"
  mode="$((8#$(/usr/bin/stat -Lc '%a' -- "${path}")))"
  (( (mode & 8#022) == 0 )) ||
    fail "${label} must not be group- or other-writable"
  validate_protected_file_ancestry "${label}" "${path}"
}

release_tool_identity() {
  local path="$1"
  printf '%s:%s\n' "$(/usr/bin/stat -Lc '%d:%i:%s:%u:%a' -- "${path}")" \
    "$(/usr/bin/sha256sum -- "${path}" | /usr/bin/awk '{ print $1 }')"
}

run_repository_git() {
  env -i \
    PATH="${PATH}" \
    LC_ALL=C \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_NO_REPLACE_OBJECTS=1 \
    git \
      -c core.attributesFile=/dev/null \
      -c core.hooksPath=/dev/null \
      -C "${REPOSITORY_ROOT}" \
      "$@"
}

run_captured_git() {
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_NO_REPLACE_OBJECTS=1 \
    GIT_OBJECT_DIRECTORY="${CAPTURED_OBJECTS}" \
    GIT_INDEX_FILE="${CAPTURED_INDEX}" \
    git \
      -c core.attributesFile=/dev/null \
      -c core.hooksPath=/dev/null \
      "$@"
}

verify_running_builder() {
  local fixed_path='apps/desktop/scripts/build-windows-release.sh'
  local expected_path="${REPOSITORY_ROOT}/${fixed_path}"
  local captured_path="${SOURCE_ROOT}/${fixed_path}"
  local owner live_mode

  [[ "${BUILDER_PATH}" == "${expected_path}" &&
     -f "${BUILDER_PATH}" && ! -L "${BUILDER_PATH}" &&
     "${BUILDER_PATH}" == "$(/usr/bin/realpath -e -- "${BUILDER_PATH}")" ]] ||
    fail 'Windows release builder must be the canonical fixed repository file'
  owner="$(/usr/bin/stat -Lc '%u' -- "${BUILDER_PATH}")"
  live_mode="$(/usr/bin/stat -Lc '%a' -- "${BUILDER_PATH}")"
  [[ "${owner}" == 0 || "${owner}" == "${EUID}" ]] ||
    fail 'Windows release builder must be owned by root or the release user'
  [[ "${live_mode}" == 755 ]] || fail 'Windows release builder must be mode 0755'
  validate_protected_file_ancestry 'Windows release builder' "${BUILDER_PATH}"

  [[ -f "${captured_path}" && ! -L "${captured_path}" &&
     "$(stat -Lc '%a' -- "${captured_path}")" == 755 ]] ||
    fail 'captured reviewed commit Windows release builder must be one mode 100755 regular file'
  /usr/bin/cmp -s -- "${BUILDER_PATH}" "${captured_path}" ||
    fail 'running Windows release builder bytes differ from the reviewed commit'
}

capture_and_materialize_reviewed_commit() {
  local destination="$1"
  local listing="$2"
  local record metadata mode type object_id captured_object_id relative parent file_mode extra
  local commit_tree captured_commit captured_tree materialized_tree
  local entry_count=0

  if find "${destination}" -mindepth 1 -print -quit | grep -q .; then
    fail 'detached desktop release source destination is not empty'
  fi
  run_repository_git cat-file commit "${COMMIT}" >"${CAPTURED_COMMIT_FILE}" ||
    fail 'reviewed desktop commit object could not be captured'
  captured_commit="$(run_captured_git hash-object --no-filters -t commit -w --stdin <"${CAPTURED_COMMIT_FILE}")" ||
    fail 'captured desktop commit object could not be re-hashed'
  [[ "${captured_commit}" == "${COMMIT}" ]] ||
    fail 'captured desktop commit object hash differs from the requested commit'
  commit_tree="$(awk '
    /^tree [0-9a-f]+$/ { value = $2; count += 1 }
    /^$/ { exit }
    END { if (count != 1) exit 1; print value }
  ' "${CAPTURED_COMMIT_FILE}")" || fail 'captured desktop commit has no unique root tree'
  [[ "${commit_tree}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] ||
    fail 'captured desktop commit root tree is invalid'
  run_repository_git ls-tree -rz --full-tree "${commit_tree}" >"${listing}" ||
    fail 'reviewed desktop commit tree could not be enumerated'
  : >"${CAPTURED_INDEX_INFO}"

  while IFS= read -r -d '' record; do
    [[ "${record}" == *$'\t'* ]] || fail 'reviewed desktop commit contains an invalid tree record'
    metadata="${record%%$'\t'*}"
    relative="${record#*$'\t'}"
    IFS=' ' read -r mode type object_id extra <<<"${metadata}"
    if [[ -n "${extra:-}" || "${type}" != blob ||
          ! "${object_id}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ||
          ( "${mode}" != 100644 && "${mode}" != 100755 ) ]]; then
      fail "reviewed desktop commit contains a non-regular or invalid tree entry: ${relative}"
    fi
    if [[ -z "${relative}" || "${relative}" == /* || "${relative}" =~ [[:cntrl:]] ||
          "/${relative}/" == *'/./'* || "/${relative}/" == *'/../'* ||
          "/${relative}/" == *'//'* ]]; then
      fail 'reviewed desktop commit contains an unsafe path'
    fi
    parent="${relative%/*}"
    [[ "${parent}" != "${relative}" ]] || parent=.
    install -d -m 0700 -- "${destination}/${parent}"
    if [[ -e "${destination}/${relative}" || -L "${destination}/${relative}" ]]; then
      fail "reviewed desktop commit path collides during materialization: ${relative}"
    fi
    run_repository_git cat-file blob "${object_id}" >"${CAPTURED_BLOB}" ||
      fail "reviewed desktop commit blob could not be materialized: ${relative}"
    captured_object_id="$(run_captured_git hash-object --no-filters -t blob -w --stdin <"${CAPTURED_BLOB}")" ||
      fail "reviewed desktop commit blob could not be re-hashed: ${relative}"
    [[ "${captured_object_id}" == "${object_id}" ]] ||
      fail "reviewed desktop commit blob hash differs during capture: ${relative}"
    /usr/bin/cp --reflink=never --preserve=mode,timestamps -- "${CAPTURED_BLOB}" "${destination}/${relative}"
    file_mode=0644
    [[ "${mode}" != 100755 ]] || file_mode=0755
    chmod "${file_mode}" -- "${destination}/${relative}"
    printf '%s %s\t%s\0' "${mode}" "${captured_object_id}" "${relative}" >>"${CAPTURED_INDEX_INFO}"
    (( entry_count += 1 ))
  done <"${listing}"

  (( entry_count > 0 )) || fail 'reviewed desktop commit tree is empty'
  rm -f -- "${CAPTURED_INDEX}"
  run_captured_git update-index -z --index-info <"${CAPTURED_INDEX_INFO}" ||
    fail 'captured desktop tree could not be reconstructed through a NUL-safe index'
  captured_tree="$(run_captured_git write-tree)" || fail 'captured desktop tree could not be written'
  [[ "${captured_tree}" == "${commit_tree}" ]] ||
    fail 'captured desktop root tree differs from the reviewed commit root tree'
  : >"${CAPTURED_INDEX_INFO}"
  while IFS= read -r -d '' relative; do
    [[ -f "${destination}/${relative}" && ! -L "${destination}/${relative}" ]] ||
      fail "materialized desktop source contains a non-regular entry: ${relative}"
    file_mode=100644
    [[ "$((8#$(stat -Lc '%a' -- "${destination}/${relative}")))" == "$((8#755))" ]] && file_mode=100755
    captured_object_id="$(run_captured_git hash-object --no-filters -t blob -w -- "${destination}/${relative}")" ||
      fail "materialized desktop source could not be hashed: ${relative}"
    printf '%s %s\t%s\0' "${file_mode}" "${captured_object_id}" "${relative}" >>"${CAPTURED_INDEX_INFO}"
  done < <(find "${destination}" -type f -printf '%P\0' | sort -z)
  rm -f -- "${CAPTURED_INDEX}"
  run_captured_git update-index -z --index-info <"${CAPTURED_INDEX_INFO}" ||
    fail 'materialized desktop tree could not be reconstructed through a NUL-safe index'
  materialized_tree="$(run_captured_git write-tree)" || fail 'materialized desktop tree could not be written'
  [[ "${materialized_tree}" == "${commit_tree}" ]] ||
    fail 'materialized desktop root tree differs from the reviewed commit root tree'
}

is_canonical_semver() {
  local value="$1"
  local numeric='(0|[1-9][0-9]*)'
  local prerelease_identifier='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
  local build_identifier='[0-9A-Za-z-]+'
  [[ "${value}" =~ ^${numeric}[.]${numeric}[.]${numeric}(-${prerelease_identifier}([.]${prerelease_identifier})*)?(\+${build_identifier}([.]${build_identifier})*)?$ ]]
}

is_canonical_https_origin() {
  local value="$1"
  local authority host port

  [[ "${value}" == https://* ]] || return 1
  authority="${value#https://}"
  [[ -n "${authority}" && "${authority}" != *[/?#@]* &&
     "${authority}" != *[[:upper:][:cntrl:][:space:]]* ]] || return 1
  host="${authority}"
  if [[ "${authority}" == *:* ]]; then
    host="${authority%:*}"
    port="${authority##*:}"
    [[ "${port}" =~ ^[1-9][0-9]{0,4}$ ]] || return 1
    (( 10#${port} <= 65535 )) || return 1
  fi
  [[ "${host}" =~ ^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$ &&
     "${host}" != *..* && "${host}" != .* && "${host}" != *. ]] || return 1
}

certificate_sha256_fingerprint() {
  local certificate="$1"
  local certificate_count
  local fingerprint_output
  local fingerprint

  certificate_count="$(
    awk '/-----BEGIN CERTIFICATE-----/ { count += 1 } END { print count + 0 }' \
      "${certificate}"
  )"
  [[ "${certificate_count}" == 1 ]] || return 1
  fingerprint_output="$(run_openssl x509 -in "${certificate}" -noout -fingerprint -sha256)" || return 1
  fingerprint="${fingerprint_output#*=}"
  fingerprint="${fingerprint//:/}"
  fingerprint="$(printf '%s' "${fingerprint}" | tr '[:upper:]' '[:lower:]')"
  [[ "${fingerprint}" =~ ^[0-9a-f]{64}$ ]] || return 1
  printf '%s\n' "${fingerprint}"
}

run_snapshot_pnpm() (
  close_inherited_fds_except 255
  [[ "$(release_tool_identity "${BWRAP_BINARY}")" == "${BWRAP_IDENTITY}" ]] ||
    fail 'bubblewrap tool identity changed before an isolated desktop build phase'
  exec /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    "${BWRAP_BINARY}" \
      --die-with-parent \
      --new-session \
      --unshare-pid \
      --unshare-net \
      --unshare-ipc \
      --unshare-uts \
      --tmpfs / \
      --ro-bind /usr /usr \
      --symlink usr/bin /bin \
      --symlink usr/sbin /sbin \
      --symlink usr/lib /lib \
      --symlink usr/lib64 /lib64 \
      --dir /etc \
      --symlink ../usr/lib/os-release /etc/os-release \
      --proc /proc \
      --dev /dev \
      --tmpfs /tmp \
      --tmpfs /run \
      --tmpfs "${RELEASE_HOME}" \
      --tmpfs "${REPOSITORY_ROOT}" \
      --ro-bind "${BUILD_TOOL_ROOT}" "${BUILD_TOOL_ROOT}" \
      --ro-bind /dev/null "${CERTIFICATE_FILE}" \
      --bind "${SOURCE_ROOT}" "${SOURCE_ROOT}" \
      --bind "${BUILDER_OUTPUT}" "${BUILDER_OUTPUT}" \
      --bind "${SANDBOX_HOME}" "${SANDBOX_HOME}" \
      --bind "${SANDBOX_TMPDIR}" "${SANDBOX_TMPDIR}" \
      --bind "${PRIVATE_PNPM_STORE}" "${PRIVATE_PNPM_STORE}" \
      --bind "${PRIVATE_CACHE}" "${PRIVATE_CACHE}" \
      --bind "${BROKER_REQUEST_FIFO}" "${BROKER_REQUEST_FIFO}" \
      --bind "${BROKER_RESPONSE_FIFO}" "${BROKER_RESPONSE_FIFO}" \
      --bind "${BROKER_LOCK}" "${BROKER_LOCK}" \
      --ro-bind "${BROKER_CLIENT}" "${BROKER_CLIENT}" \
      --ro-bind "${SIGN_HOOK}" "${SIGN_HOOK}" \
      --clearenv \
      --setenv PATH /usr/bin:/bin \
      --setenv HOME "${SANDBOX_HOME}" \
      --setenv TMPDIR "${SANDBOX_TMPDIR}" \
      --setenv XDG_CACHE_HOME "${PRIVATE_CACHE}" \
      --setenv npm_config_store_dir "${PRIVATE_PNPM_STORE}" \
      --setenv NPM_CONFIG_USERCONFIG /dev/null \
      --setenv NPM_CONFIG_GLOBALCONFIG /dev/null \
      --setenv ELECTRON_BUILDER_OFFLINE true \
      --setenv CSC_IDENTITY_AUTO_DISCOVERY false \
      --setenv VITE_API_BASE_URL "${API_ORIGIN}" \
      --setenv VITE_CHAT_PROMPT_CONFIGURATION_KEY "${PROMPT_KEY}" \
      --setenv VITE_CHAT_MODEL_CONFIGURATION_KEY "${MODEL_KEY}" \
      --chdir "${SOURCE_ROOT}" \
      "${NODE_BINARY}" "${PNPM_CLI}" "$@"
)

run_openssl() (
  local argument
  local -a retained_fds=()
  for argument in "$@"; do
    if [[ "${argument}" =~ ^/proc/self/fd/([0-9]+)$ ]]; then
      retained_fds+=( "${BASH_REMATCH[1]}" )
    elif [[ "${argument}" =~ ^fd:([0-9]+)$ ]]; then
      retained_fds+=( "${BASH_REMATCH[1]}" )
    fi
  done
  close_inherited_fds_except "${retained_fds[@]}"
  exec /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C /usr/bin/timeout --signal=KILL 60 \
    "${OPENSSL_BINARY}" "$@"
)

run_osslsigncode() (
  local argument
  local -a retained_fds=()
  for argument in "$@"; do
    if [[ "${argument}" =~ ^/proc/self/fd/([0-9]+)$ ]]; then
      retained_fds+=( "${BASH_REMATCH[1]}" )
    fi
  done
  close_inherited_fds_except "${retained_fds[@]}"
  exec /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C /usr/bin/timeout --signal=KILL 60 \
    "${OSSLSIGNCODE_BINARY}" "$@"
)

validate_sign_request_parent() {
  local request_path="$1"
  local relative current component
  local -a components=()

  [[ "${request_path}" == "${BUILDER_OUTPUT}/"* ]] || return 1
  relative="${request_path#"${BUILDER_OUTPUT}/"}"
  [[ -n "${relative}" && "${relative}" != */../* && "${relative}" != ../* &&
     "${relative}" != */./* && "${relative}" != ./* && "${relative}" != *//* ]] || return 1
  IFS=/ read -r -a components <<<"${relative}"
  current="${BUILDER_OUTPUT}"
  for component in "${components[@]:0:${#components[@]}-1}"; do
    current="${current}/${component}"
    [[ -d "${current}" && ! -L "${current}" &&
       "$(stat -Lc '%u' -- "${current}")" == "${EUID}" ]] || return 1
  done
}

write_signing_broker_client() {
  cat >"${BROKER_CLIENT}" <<'CLIENT'
#!/usr/bin/bash -p
set -Eeuo pipefail
for descriptor_path in /proc/self/fd/[0-9]*; do
  descriptor="${descriptor_path##*/}"
  (( descriptor <= 2 || descriptor == 255 )) || exec {descriptor}>&- || true
done
unset descriptor descriptor_path
[[ "$#" == 3 ]] || exit 64
readonly request_path="$1"
readonly request_hash="$2"
readonly request_nested="$3"
readonly request_fifo="${ASCENDANY_SIGN_BROKER_REQUEST:?}"
readonly response_fifo="${ASCENDANY_SIGN_BROKER_RESPONSE:?}"
readonly lock_file="${ASCENDANY_SIGN_BROKER_LOCK:?}"
exec {lock_fd}>"${lock_file}"
/usr/bin/flock -w 60 -x "${lock_fd}"
exec {request_fd}>"${request_fifo}"
builtin printf '%s\0%s\0%s\0' "${request_path}" "${request_hash}" "${request_nested}" >&"${request_fd}"
exec {request_fd}>&-
exec {response_fd}<"${response_fifo}"
status=''
IFS= read -r -d '' status <&"${response_fd}" || exit 65
exec {response_fd}<&-
[[ "${status}" == ok ]] || {
  builtin printf '%s\n' "${status}" >&2
  exit 65
}
CLIENT
  chmod 0755 -- "${BROKER_CLIENT}"

  cat >"${SIGN_HOOK}" <<HOOK
"use strict";
const { execFileSync } = require("node:child_process");
const allowed = new Set(${SIGNING_ALLOWLIST_JSON});
exports.default = async function sign(configuration) {
  if (configuration.cscInfo !== null || configuration.hash !== "sha256" || configuration.isNest !== false) {
    throw new Error("AscendAny signing hook received an invalid signing contract");
  }
  if (!allowed.has(configuration.path)) {
    throw new Error("AscendAny signing hook rejected path: " + configuration.path);
  }
  execFileSync(${BROKER_CLIENT_JSON}, [configuration.path, configuration.hash, "0"], {
    env: {
      PATH: "/usr/bin:/bin",
      LC_ALL: "C",
      ASCENDANY_SIGN_BROKER_REQUEST: ${BROKER_REQUEST_FIFO_JSON},
      ASCENDANY_SIGN_BROKER_RESPONSE: ${BROKER_RESPONSE_FIFO_JSON},
      ASCENDANY_SIGN_BROKER_LOCK: ${BROKER_LOCK_JSON},
    },
    stdio: "inherit",
    timeout: 60000,
    killSignal: "SIGKILL",
  });
};
HOOK
  chmod 0444 -- "${SIGN_HOOK}"
}

signing_broker() {
  local password='' read_status=0 material_path password_path
  local request_path request_hash request_nested signed_tmp unsigned_tmp response
  local request_parent request_basename request_parent_identity pinned_request
  local signed_identity signed_digest
  local material_fd password_write_fd password_read_fd request_fd response_fd
  local request_parent_fd signed_file_fd
  local expected_fingerprint expected_path request_allowed signed_count=0
  declare -A signed_paths=()

  close_inherited_fds_except "${certificate_password_fd}" "${CERTIFICATE_FD}" 255
  [[ -e "/proc/self/fd/${certificate_password_fd}" ]] ||
    fail 'Windows signing broker lost the inherited password descriptor'
  [[ -e "/proc/self/fd/${CERTIFICATE_FD}" ]] ||
    fail 'Windows signing broker lost the captured PKCS#12 descriptor'

  IFS= read -r -d '' -n 4097 password <&"${certificate_password_fd}" || read_status=$?
  exec {certificate_password_fd}<&-
  if (( read_status == 0 )); then
    fail 'Windows signing password descriptor must reach EOF within 4096 bytes and contain no NUL byte'
  fi
  (( read_status == 1 && ${#password} > 0 && ${#password} <= 4096 )) ||
    fail 'Windows signing password descriptor must contain 1..4096 bytes'
  [[ "${password}" != *$'\n'* && "${password}" != *$'\r'* ]] ||
    fail 'Windows signing password descriptor must not contain a line break'

  password_path="$(mktemp "${BROKER_PRIVATE}/password.XXXXXXXX")"
  exec {password_write_fd}<>"${password_path}"
  rm -f -- "${password_path}"
  printf '%s' "${password}" >&"${password_write_fd}"
  unset password
  exec {password_read_fd}<"/proc/self/fd/${password_write_fd}"

  material_path="$(mktemp "${BROKER_PRIVATE}/material.XXXXXXXX")"
  exec {material_fd}<>"${material_path}"
  rm -f -- "${material_path}"
  [[ "$(release_tool_identity "${OPENSSL_BINARY}")" == "${OPENSSL_IDENTITY}" ]] ||
    fail 'OpenSSL tool identity changed before PKCS#12 extraction'
  [[ "$(stat -Lc '%d:%i:%s:%u:%h:%a:%f' -- "/proc/self/fd/${CERTIFICATE_FD}")" == \
     "${CERTIFICATE_IDENTITY}" ]] ||
    fail 'captured PKCS#12 descriptor identity changed before extraction'
  [[ "$(sha256sum -- "/proc/self/fd/${CERTIFICATE_FD}" | awk '{ print $1 }')" == \
     "${CERTIFICATE_DIGEST}" ]] ||
    fail 'captured PKCS#12 bytes changed before extraction'
  run_openssl pkcs12 \
    -in "/proc/self/fd/${CERTIFICATE_FD}" \
    -passin "fd:${password_read_fd}" \
    -nodes \
    -clcerts \
    -out "/proc/self/fd/${material_fd}" ||
    fail 'PKCS#12 signing material could not be extracted through the password descriptor'
  exec {CERTIFICATE_FD}<&-
  exec {password_read_fd}<&-
  exec {password_write_fd}>&-
  expected_fingerprint="$(certificate_sha256_fingerprint "/proc/self/fd/${material_fd}")" ||
    fail 'PKCS#12 must contain exactly one readable leaf certificate'

  exec {request_fd}<>"${BROKER_REQUEST_FIFO}"
  exec {response_fd}<>"${BROKER_RESPONSE_FIFO}"
  printf '%s\n' "${expected_fingerprint}" >"${BROKER_READY}"
  /usr/bin/sync -f -- "${BROKER_READY}"

  while true; do
    request_path=''
    request_hash=''
    request_nested=''
    IFS= read -r -d '' request_path <&"${request_fd}" || break
    IFS= read -r -d '' request_hash <&"${request_fd}" || break
    IFS= read -r -d '' request_nested <&"${request_fd}" || break
    response='error: signing broker rejected the request'
    if [[ "${request_path}" == __finish__ ]]; then
      if [[ "${request_hash}" == finish && "${request_nested}" == 0 &&
            "${signed_count}" == "${#EXPECTED_SIGN_PATHS[@]}" ]]; then
        response=ok
        for expected_path in "${EXPECTED_SIGN_PATHS[@]}"; do
          [[ "${signed_paths["${expected_path}"]:-}" == 1 ]] || response='error: signing broker request log is incomplete'
        done
      fi
      printf '%s\0' "${response}" >&"${response_fd}"
      [[ "${response}" == ok ]] || return 1
      break
    fi
    request_allowed=0
    for expected_path in "${EXPECTED_SIGN_PATHS[@]}"; do
      [[ "${request_path}" != "${expected_path}" ]] || request_allowed=1
    done
    if [[ "${request_hash}" == sha256 && "${request_nested}" == 0 &&
          "${request_allowed}" == 1 &&
          -z "${signed_paths["${request_path}"]+x}" ]] &&
       validate_sign_request_parent "${request_path}"; then
      request_parent="${request_path%/*}"
      request_basename="${request_path##*/}"
      request_parent_identity="$(stat -Lc '%d:%i' -- "${request_parent}")" || request_parent_identity=''
      request_parent_fd=''
      if [[ -n "${request_parent_identity}" ]] &&
         exec {request_parent_fd}<"${request_parent}" &&
         [[ "$(stat -Lc '%d:%i' -- "/proc/self/fd/${request_parent_fd}")" == \
            "${request_parent_identity}" ]]; then
        pinned_request="/proc/self/fd/${request_parent_fd}/${request_basename}"
        if [[ -f "${pinned_request}" && ! -L "${pinned_request}" &&
              "$(stat -Lc '%h:%u' -- "${pinned_request}")" == "1:${EUID}" ]]; then
          unsigned_tmp="$(mktemp "${BROKER_PRIVATE}/unsigned.XXXXXXXX.exe")"
          rm -f -- "${unsigned_tmp}"
          if mv --no-target-directory --no-clobber -- "${pinned_request}" "${unsigned_tmp}" &&
             [[ -f "${unsigned_tmp}" && ! -L "${unsigned_tmp}" &&
                "$(stat -Lc '%h:%u' -- "${unsigned_tmp}")" == "1:${EUID}" ]]; then
            signed_tmp="$(mktemp "${BROKER_PRIVATE}/signed.XXXXXXXX.exe")"
            rm -f -- "${signed_tmp}"
            if [[ "$(release_tool_identity "${OSSLSIGNCODE_BINARY}")" == "${OSSLSIGNCODE_IDENTITY}" ]] &&
               [[ ! -e "${pinned_request}" && ! -L "${pinned_request}" ]] &&
               run_osslsigncode sign \
                -certs "/proc/self/fd/${material_fd}" \
                -key "/proc/self/fd/${material_fd}" \
                -h sha256 \
                -n AscendAny \
                -in "${unsigned_tmp}" \
                -out "${signed_tmp}" &&
               [[ -f "${signed_tmp}" && ! -L "${signed_tmp}" &&
                  "$(stat -Lc '%h:%u' -- "${signed_tmp}")" == "1:${EUID}" ]] &&
               run_osslsigncode verify \
                 -in "${signed_tmp}" \
                 -require-leaf-hash "sha256:${expected_fingerprint}" &&
               signed_identity="$(stat -Lc '%d:%i:%s:%u:%h:%f' -- "${signed_tmp}")" &&
               signed_digest="$(sha256sum -- "${signed_tmp}" | awk '{ print $1 }')" &&
               validate_sign_request_parent "${request_path}" &&
               [[ "$(stat -Lc '%d:%i' -- "${request_parent}")" == \
                  "${request_parent_identity}" &&
                  ! -e "${pinned_request}" && ! -L "${pinned_request}" ]] &&
               mv --no-target-directory --no-clobber -- "${signed_tmp}" "${pinned_request}" &&
               [[ -f "${pinned_request}" && ! -L "${pinned_request}" &&
                  "$(stat -Lc '%h:%u' -- "${pinned_request}")" == "1:${EUID}" ]]; then
              exec {signed_file_fd}<"${pinned_request}"
              [[ "$(stat -Lc '%d:%i:%s:%u:%h:%f' -- "/proc/self/fd/${signed_file_fd}")" == \
                 "${signed_identity}" &&
                 "$(sha256sum -- "/proc/self/fd/${signed_file_fd}" | awk '{ print $1 }')" == \
                 "${signed_digest}" ]] ||
                fail 'signed Windows artifact descriptor did not bind the broker output'
              signed_paths["${request_path}"]=1
              (( signed_count += 1 ))
              printf 'signed\t%s\n' "${request_path}" >>"${BROKER_LOG}"
              /usr/bin/sync -f -- "/proc/self/fd/${signed_file_fd}" "${BROKER_LOG}"
              exec {signed_file_fd}<&-
              response=ok
            else
              rm -f -- "${signed_tmp}"
              [[ -e "${pinned_request}" || -L "${pinned_request}" ]] ||
                mv --no-target-directory --no-clobber -- "${unsigned_tmp}" "${pinned_request}" 2>/dev/null || true
            fi
            rm -f -- "${unsigned_tmp}"
          fi
        fi
        exec {request_parent_fd}<&-
      fi
    fi
    printf '%s\0' "${response}" >&"${response_fd}"
  done
  exec {material_fd}>&-
}

[[ "${DESKTOP_ROOT}" == "${REPOSITORY_ROOT}/apps/desktop" ]] ||
  fail 'desktop release script must run from the repository apps/desktop tree'
PATH=/usr/bin:/bin
export PATH
hash -r

for command_name in awk cat chmod cp diff dirname env find flock git grep install mkfifo mktemp mv realpath rm sha256sum sha512sum sort stat sync timeout tr; do
  command -v "${command_name}" >/dev/null 2>&1 ||
    fail "required command is unavailable: ${command_name}"
done
unset command_name

[[ "${REQUESTED_COMMIT}" =~ ^[0-9a-f]{40}$ ]] ||
  fail 'ASCENDANY_DESKTOP_RELEASE_COMMIT must be one explicit lowercase 40-hex commit ID'
repository_root="$(
  exec {certificate_password_fd}<&-
  run_repository_git rev-parse --show-toplevel
)" || fail 'desktop release repository root could not be resolved'
[[ "${repository_root}" == "${REPOSITORY_ROOT}" ]] ||
  fail 'desktop release repository root is invalid'
unset repository_root
commit="$(
  exec {certificate_password_fd}<&-
  run_repository_git rev-parse --verify "${REQUESTED_COMMIT}^{commit}" 2>/dev/null
)" ||
  fail 'desktop release commit does not identify an available commit object'
[[ "${commit}" == "${REQUESTED_COMMIT}" ]] ||
  fail 'desktop release commit did not resolve to the exact requested object ID'
readonly COMMIT="${commit}"
unset commit
(( ${#VERSION} <= 128 )) ||
  fail 'ASCENDANY_DESKTOP_VERSION must be no longer than 128 ASCII bytes'
is_canonical_semver "${VERSION}" ||
  fail 'ASCENDANY_DESKTOP_VERSION must be one canonical SemVer value'

validate_release_tool ASCENDANY_DESKTOP_NODE_PATH "${NODE_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_PNPM_CLI_PATH "${PNPM_CLI}"
validate_release_tool ASCENDANY_DESKTOP_BWRAP_PATH "${BWRAP_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_OPENSSL_PATH "${OPENSSL_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_OSSLSIGNCODE_PATH "${OSSLSIGNCODE_BINARY}"
readonly BWRAP_IDENTITY="$(release_tool_identity "${BWRAP_BINARY}")"
readonly OPENSSL_IDENTITY="$(release_tool_identity "${OPENSSL_BINARY}")"
readonly OSSLSIGNCODE_IDENTITY="$(release_tool_identity "${OSSLSIGNCODE_BINARY}")"
[[ "${RELEASE_HOME}" = /* && -d "${RELEASE_HOME}" && ! -L "${RELEASE_HOME}" ]] ||
  fail 'HOME must name an absolute real release-user directory'
[[ "${RELEASE_HOME}" == "$(/usr/bin/realpath -e -- "${RELEASE_HOME}")" ]] ||
  fail 'HOME must be one canonical path without symlink ancestry'
[[ "$(/usr/bin/stat -Lc '%u' -- "${RELEASE_HOME}")" == "${EUID}" ]] ||
  fail 'HOME must be owned by the release user'
validate_protected_directory_ancestry HOME "${RELEASE_HOME}"
[[ "${PNPM_STORE_SEED}" = /* && -d "${PNPM_STORE_SEED}" && ! -L "${PNPM_STORE_SEED}" ]] ||
  fail 'ASCENDANY_DESKTOP_PNPM_STORE_PATH must name an absolute real pnpm store seed directory'
[[ "${PNPM_STORE_SEED}" == "$(/usr/bin/realpath -e -- "${PNPM_STORE_SEED}")" ]] ||
  fail 'ASCENDANY_DESKTOP_PNPM_STORE_PATH must be one canonical path without symlink ancestry'
validate_protected_directory_ancestry ASCENDANY_DESKTOP_PNPM_STORE_PATH "${PNPM_STORE_SEED}"
validate_seed_tree ASCENDANY_DESKTOP_PNPM_STORE_PATH "${PNPM_STORE_SEED}"
[[ "${BUILD_CACHE_SEED}" = /* && -d "${BUILD_CACHE_SEED}" && ! -L "${BUILD_CACHE_SEED}" ]] ||
  fail 'ASCENDANY_DESKTOP_BUILD_CACHE_PATH must name an absolute real offline build cache seed directory'
[[ "${BUILD_CACHE_SEED}" == "$(/usr/bin/realpath -e -- "${BUILD_CACHE_SEED}")" ]] ||
  fail 'ASCENDANY_DESKTOP_BUILD_CACHE_PATH must be one canonical path without symlink ancestry'
validate_protected_directory_ancestry ASCENDANY_DESKTOP_BUILD_CACHE_PATH "${BUILD_CACHE_SEED}"
validate_seed_tree ASCENDANY_DESKTOP_BUILD_CACHE_PATH "${BUILD_CACHE_SEED}"
[[ "${BUILD_TOOL_ROOT}" = /* && -d "${BUILD_TOOL_ROOT}" && ! -L "${BUILD_TOOL_ROOT}" ]] ||
  fail 'ASCENDANY_DESKTOP_BUILD_TOOL_ROOT must name an absolute real build-tool directory'
[[ "${BUILD_TOOL_ROOT}" == "$(realpath -e -- "${BUILD_TOOL_ROOT}")" ]] ||
  fail 'ASCENDANY_DESKTOP_BUILD_TOOL_ROOT must be one canonical path without symlink ancestry'
validate_protected_directory_ancestry ASCENDANY_DESKTOP_BUILD_TOOL_ROOT "${BUILD_TOOL_ROOT}"
validate_seed_tree ASCENDANY_DESKTOP_BUILD_TOOL_ROOT "${BUILD_TOOL_ROOT}"
if ! path_contains /usr "${NODE_BINARY}" &&
   ! path_contains "${BUILD_TOOL_ROOT}" "${NODE_BINARY}"; then
  fail 'ASCENDANY_DESKTOP_NODE_PATH must reside under /usr or ASCENDANY_DESKTOP_BUILD_TOOL_ROOT'
fi
if ! path_contains /usr "${PNPM_CLI}" &&
   ! path_contains "${BUILD_TOOL_ROOT}" "${PNPM_CLI}"; then
  fail 'ASCENDANY_DESKTOP_PNPM_CLI_PATH must reside under /usr or ASCENDANY_DESKTOP_BUILD_TOOL_ROOT'
fi

[[ "${OUTPUT_DIRECTORY}" = /* ]] ||
  fail 'ASCENDANY_DESKTOP_OUTPUT_DIRECTORY must be an explicit absolute path'
[[ "${OUTPUT_DIRECTORY}" =~ ^/[0-9A-Za-z_./:+-]+$ ]] ||
  fail 'ASCENDANY_DESKTOP_OUTPUT_DIRECTORY contains a character outside the release path contract'
[[ "${OUTPUT_DIRECTORY}" == "$(realpath -m -- "${OUTPUT_DIRECTORY}")" ]] ||
  fail 'ASCENDANY_DESKTOP_OUTPUT_DIRECTORY must be a canonical path'
[[ ! -e "${OUTPUT_DIRECTORY}" && ! -L "${OUTPUT_DIRECTORY}" ]] ||
  fail 'desktop release output already exists'
if ! is_canonical_https_origin "${API_ORIGIN}"; then
  fail 'VITE_API_BASE_URL must be one canonical HTTPS origin'
fi
[[ "${PROMPT_KEY}" =~ ^[a-z][a-z0-9_.-]{0,127}$ ]] ||
  fail 'VITE_CHAT_PROMPT_CONFIGURATION_KEY is invalid'
[[ "${MODEL_KEY}" =~ ^[a-z][a-z0-9_.-]{0,127}$ ]] ||
  fail 'VITE_CHAT_MODEL_CONFIGURATION_KEY is invalid'
[[ "${CERTIFICATE_FILE}" = /* && -f "${CERTIFICATE_FILE}" && ! -L "${CERTIFICATE_FILE}" ]] ||
  fail 'CSC_LINK must name an absolute regular PKCS#12 file and may not be a symlink'
[[ "${CERTIFICATE_FILE}" == "$(realpath -e -- "${CERTIFICATE_FILE}")" ]] ||
  fail 'CSC_LINK must be one canonical path without symlink ancestry'
[[ "$(stat -Lc '%u:%h' -- "${CERTIFICATE_FILE}")" == "${EUID}:1" ]] ||
  fail 'CSC_LINK must be owned by the release user'
certificate_mode="$((8#$(stat -Lc '%a' -- "${CERTIFICATE_FILE}")))"
(( (certificate_mode & 8#077) == 0 )) ||
  fail 'CSC_LINK must not grant group or other permissions'
unset certificate_mode
validate_protected_file_ancestry CSC_LINK "${CERTIFICATE_FILE}"
certificate_identity="$(stat -Lc '%d:%i:%s:%u:%h:%a:%f' -- "${CERTIFICATE_FILE}")"
exec {CERTIFICATE_FD}<"${CERTIFICATE_FILE}"
readonly CERTIFICATE_FD
[[ "$(stat -Lc '%d:%i:%s:%u:%h:%a:%f' -- "/proc/self/fd/${CERTIFICATE_FD}")" == \
   "${certificate_identity}" ]] ||
  fail 'CSC_LINK changed while its validated descriptor was captured'
readonly CERTIFICATE_IDENTITY="${certificate_identity}"
readonly CERTIFICATE_DIGEST="$(sha256sum -- "/proc/self/fd/${CERTIFICATE_FD}" | awk '{ print $1 }')"
[[ "${CERTIFICATE_DIGEST}" =~ ^[0-9a-f]{64}$ ]] ||
  fail 'captured PKCS#12 descriptor has an invalid SHA-256 digest'
unset certificate_identity
if path_contains "${PNPM_STORE_SEED}" "${CERTIFICATE_FILE}" ||
   path_contains "${BUILD_CACHE_SEED}" "${CERTIFICATE_FILE}" ||
   path_contains "${PNPM_STORE_SEED}" "${RELEASE_HOME}/.gnupg" ||
   path_contains "${BUILD_CACHE_SEED}" "${RELEASE_HOME}/.gnupg"; then
  fail 'offline desktop build seed paths must not contain signing material or a release keyring'
fi
if path_contains "${REPOSITORY_ROOT}" "${CERTIFICATE_FILE}" ||
   path_contains "${REPOSITORY_ROOT}" "${PNPM_STORE_SEED}" ||
   path_contains "${REPOSITORY_ROOT}" "${BUILD_CACHE_SEED}" ||
   path_contains "${REPOSITORY_ROOT}" "${BUILD_TOOL_ROOT}" ||
   path_contains "${REPOSITORY_ROOT}" "${NODE_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${PNPM_CLI}" ||
   path_contains "${REPOSITORY_ROOT}" "${BWRAP_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${OPENSSL_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${OSSLSIGNCODE_BINARY}"; then
  fail 'release inputs and tools must remain outside the live repository mount mask'
fi
for external_path in \
  "${CERTIFICATE_FILE}" \
  "${PNPM_STORE_SEED}" \
  "${BUILD_CACHE_SEED}" \
  "${BUILD_TOOL_ROOT}" \
  "${NODE_BINARY}" \
  "${PNPM_CLI}" \
  "${BWRAP_BINARY}" \
  "${OPENSSL_BINARY}" \
  "${OSSLSIGNCODE_BINARY}"; do
  if path_contains "${RELEASE_HOME}" "${external_path}"; then
    fail 'Windows release tools, signing material, and offline seeds must remain outside the masked release HOME'
  fi
done
unset external_path

readonly OUTPUT_PARENT="$(dirname -- "${OUTPUT_DIRECTORY}")"
[[ -d "${OUTPUT_PARENT}" && ! -L "${OUTPUT_PARENT}" ]] ||
  fail 'desktop release output parent must be one existing real directory'
[[ "${OUTPUT_PARENT}" == "$(realpath -e -- "${OUTPUT_PARENT}")" ]] ||
  fail 'desktop release output parent must be one canonical directory'
[[ "$(stat -Lc '%u' -- "${OUTPUT_PARENT}")" == "${EUID}" ]] ||
  fail 'desktop release output parent must be owned by the release user'
output_parent_mode="$((8#$(stat -Lc '%a' -- "${OUTPUT_PARENT}")))"
(( (output_parent_mode & 8#022) == 0 )) ||
  fail 'desktop release output parent must not be group- or other-writable'
unset output_parent_mode
validate_protected_directory_ancestry 'desktop release output parent' "${OUTPUT_PARENT}"
if path_contains "${RELEASE_HOME}" "${OUTPUT_PARENT}"; then
  fail 'desktop release output parent must remain outside the masked release HOME'
fi
if path_contains "${PNPM_STORE_SEED}" "${OUTPUT_PARENT}" ||
   path_contains "${BUILD_CACHE_SEED}" "${OUTPUT_PARENT}" ||
   path_contains "${BUILD_TOOL_ROOT}" "${OUTPUT_PARENT}" ||
   path_contains "${REPOSITORY_ROOT}" "${OUTPUT_PARENT}"; then
  fail 'release output parent must remain outside build seed and live repository paths'
fi
readonly OUTPUT_PARENT_IDENTITY="$(/usr/bin/stat -Lc '%d:%i' -- "${OUTPUT_PARENT}")"
exec {OUTPUT_PARENT_FD}<"${OUTPUT_PARENT}"
readonly OUTPUT_PARENT_FD
[[ "$(/usr/bin/stat -Lc '%d:%i' -- "/proc/self/fd/${OUTPUT_PARENT_FD}")" == "${OUTPUT_PARENT_IDENTITY}" ]] ||
  fail 'desktop release output parent descriptor did not bind the validated directory'

assert_output_parent_identity() {
  local phase="$1"
  local actual_identity

  validate_protected_directory_ancestry 'desktop release output parent' "${OUTPUT_PARENT}"
  actual_identity="$(/usr/bin/stat -Lc '%d:%i' -- "${OUTPUT_PARENT}")" ||
    fail "desktop release output parent disappeared before ${phase}"
  [[ "${actual_identity}" == "${OUTPUT_PARENT_IDENTITY}" ]] ||
    fail "desktop release output parent identity changed before ${phase}"
}

umask 077
assert_output_parent_identity 'workspace creation'
WORKSPACE="$(mktemp -d "${OUTPUT_PARENT}/.ascendany-desktop-windows.XXXXXXXX")"
chmod 0700 -- "${WORKSPACE}"
readonly WORKSPACE
readonly WORKSPACE_BASENAME="${WORKSPACE##*/}"
readonly SOURCE_ROOT="${WORKSPACE}/source"
readonly BUILDER_OUTPUT="${WORKSPACE}/builder-output"
readonly STAGING_OUTPUT="${WORKSPACE}/published-output"
readonly EXTRACTED_SIGNATURE="${WORKSPACE}/installer-signature.pem"
readonly INSTALLER_SIGNER_CERTIFICATE="${WORKSPACE}/installer-signer.pem"
readonly TOOL_TMPDIR="${WORKSPACE}/tool-tmp"
readonly SANDBOX_HOME="${WORKSPACE}/sandbox-home"
readonly SANDBOX_TMPDIR="${WORKSPACE}/sandbox-tmp"
readonly PRIVATE_PNPM_STORE="${WORKSPACE}/pnpm-store"
readonly PRIVATE_CACHE="${WORKSPACE}/cache"
readonly CAPTURED_OBJECTS="${WORKSPACE}/captured-objects"
readonly CAPTURED_INDEX="${WORKSPACE}/captured-index"
readonly CAPTURED_INDEX_INFO="${WORKSPACE}/captured-index-info"
readonly CAPTURED_COMMIT_FILE="${WORKSPACE}/captured-commit"
readonly CAPTURED_BLOB="${WORKSPACE}/captured-blob"
readonly BROKER_PRIVATE="${WORKSPACE}/sign-broker-private"
readonly BROKER_REQUEST_FIFO="${WORKSPACE}/sign-request"
readonly BROKER_RESPONSE_FIFO="${WORKSPACE}/sign-response"
readonly BROKER_LOCK="${WORKSPACE}/sign.lock"
readonly BROKER_CLIENT="${WORKSPACE}/sign-client"
readonly SIGN_HOOK="${WORKSPACE}/sign-hook.cjs"
readonly BROKER_READY="${WORKSPACE}/sign-ready"
readonly BROKER_LOG="${WORKSPACE}/sign-requests.log"
readonly INSTALLER_NAME="AscendAny-win-x64-${VERSION}.exe"
readonly BUILT_INSTALLER="${BUILDER_OUTPUT}/${INSTALLER_NAME}"
readonly APP_EXECUTABLE="${BUILDER_OUTPUT}/win-unpacked/AscendAny.exe"
readonly ELEVATE_EXECUTABLE="${BUILDER_OUTPUT}/win-unpacked/resources/elevate.exe"
readonly UNINSTALLER_EXECUTABLE="${BUILDER_OUTPUT}/__uninstaller-nsis-AscendAny.exe"
readonly -a EXPECTED_SIGN_PATHS=(
  "${APP_EXECUTABLE}"
  "${ELEVATE_EXECUTABLE}"
  "${UNINSTALLER_EXECUTABLE}"
  "${BUILT_INSTALLER}"
)
BROKER_PID=''

cleanup() {
  if [[ -n "${BROKER_PID:-}" ]] && /usr/bin/kill -0 "${BROKER_PID}" 2>/dev/null; then
    /usr/bin/kill -TERM "${BROKER_PID}" 2>/dev/null || true
    wait "${BROKER_PID}" 2>/dev/null || true
  fi
  rm -rf -- "${WORKSPACE}"
  rm -rf -- "/proc/self/fd/${OUTPUT_PARENT_FD}/${WORKSPACE_BASENAME}" 2>/dev/null || true
  exec {OUTPUT_PARENT_FD}<&- 2>/dev/null || true
}
trap cleanup EXIT

install -d -m 0700 -- \
  "${SOURCE_ROOT}" \
  "${BUILDER_OUTPUT}" \
  "${STAGING_OUTPUT}" \
  "${TOOL_TMPDIR}" \
  "${SANDBOX_HOME}" \
  "${SANDBOX_TMPDIR}" \
  "${PRIVATE_PNPM_STORE}" \
  "${PRIVATE_CACHE}" \
  "${CAPTURED_OBJECTS}" \
  "${BROKER_PRIVATE}"
mkfifo -m 0600 -- "${BROKER_REQUEST_FIFO}" "${BROKER_RESPONSE_FIFO}"
install -m 0600 /dev/null "${BROKER_LOCK}"
install -m 0600 /dev/null "${BROKER_LOG}"
/usr/bin/cp -a --reflink=auto -- "${PNPM_STORE_SEED}/." "${PRIVATE_PNPM_STORE}/"
/usr/bin/cp -a --reflink=auto -- "${BUILD_CACHE_SEED}/." "${PRIVATE_CACHE}/"
capture_and_materialize_reviewed_commit "${SOURCE_ROOT}" "${WORKSPACE}/reviewed-tree"
[[ "$(stat -Lc '%u:%a' -- "${SOURCE_ROOT}")" == "${EUID}:700" ]] ||
  fail 'detached desktop release source is not private to the release user'
if find "${SOURCE_ROOT}" -mindepth 1 ! -type d ! -type f -print -quit | grep -q .; then
  fail 'detached desktop release source contains a symlink or special node'
fi
[[ -f "${SOURCE_ROOT}/apps/desktop/package.json" ]] ||
  fail 'reviewed commit has no desktop package source'
verify_running_builder

printf -v SIGNING_ALLOWLIST_JSON '["%s","%s","%s","%s"]' \
  "${APP_EXECUTABLE}" \
  "${ELEVATE_EXECUTABLE}" \
  "${UNINSTALLER_EXECUTABLE}" \
  "${BUILT_INSTALLER}"
printf -v BROKER_CLIENT_JSON '"%s"' "${BROKER_CLIENT}"
printf -v BROKER_REQUEST_FIFO_JSON '"%s"' "${BROKER_REQUEST_FIFO}"
printf -v BROKER_RESPONSE_FIFO_JSON '"%s"' "${BROKER_RESPONSE_FIFO}"
printf -v BROKER_LOCK_JSON '"%s"' "${BROKER_LOCK}"
readonly SIGNING_ALLOWLIST_JSON BROKER_CLIENT_JSON BROKER_REQUEST_FIFO_JSON \
  BROKER_RESPONSE_FIFO_JSON BROKER_LOCK_JSON
write_signing_broker_client

signing_broker &
BROKER_PID=$!
exec {certificate_password_fd}<&-
exec {CERTIFICATE_FD}<&-
unset certificate_password_fd
broker_ready_deadline=$((SECONDS + 60))
while [[ ! -s "${BROKER_READY}" ]]; do
  /usr/bin/kill -0 "${BROKER_PID}" 2>/dev/null || {
    wait "${BROKER_PID}" 2>/dev/null || true
    fail 'Windows signing broker failed before accepting requests'
  }
  (( SECONDS < broker_ready_deadline )) ||
    fail 'Windows signing broker did not become ready within 60 seconds'
  /usr/bin/sleep 0.01
done
unset broker_ready_deadline
expected_certificate_fingerprint="$(<"${BROKER_READY}")"
[[ "${expected_certificate_fingerprint}" =~ ^[0-9a-f]{64}$ ]] ||
  fail 'Windows signing broker returned an invalid leaf certificate fingerprint'
readonly EXPECTED_CERTIFICATE_FINGERPRINT="${expected_certificate_fingerprint}"
unset expected_certificate_fingerprint

(
  cd -- "${SOURCE_ROOT}"
  run_snapshot_pnpm install --frozen-lockfile --offline
  run_snapshot_pnpm --filter @ascendany/desktop build
  run_snapshot_pnpm --filter @ascendany/desktop exec electron-builder \
    --win nsis \
    --x64 \
    --publish never \
    --config.directories.output="${BUILDER_OUTPUT}" \
    --config.artifactName="AscendAny-win-x64-${VERSION}.exe" \
    --config.forceCodeSigning=true \
    --config.win.signAndEditExecutable=true \
    --config.win.signtoolOptions.sign="${SIGN_HOOK}" \
    --config.win.signtoolOptions.signingHashAlgorithms=sha256 \
    --config.extraMetadata.ascendanyReleaseCommit="${COMMIT}" \
    --config.extraMetadata.version="${VERSION}"
)
assert_output_parent_identity 'installer verification'
/usr/bin/env -i \
  PATH=/usr/bin:/bin \
  LC_ALL=C \
  ASCENDANY_SIGN_BROKER_REQUEST="${BROKER_REQUEST_FIFO}" \
  ASCENDANY_SIGN_BROKER_RESPONSE="${BROKER_RESPONSE_FIFO}" \
  ASCENDANY_SIGN_BROKER_LOCK="${BROKER_LOCK}" \
  /usr/bin/timeout --signal=KILL 60 "${BROKER_CLIENT}" __finish__ finish 0 ||
  fail 'Windows signing broker rejected the completed request log'
wait "${BROKER_PID}" || fail 'Windows signing broker failed'
BROKER_PID=''

[[ -f "${BUILT_INSTALLER}" && ! -L "${BUILT_INSTALLER}" &&
   "$(stat -Lc '%h:%u' -- "${BUILT_INSTALLER}")" == "1:${EUID}" ]] ||
  fail 'expected signed NSIS installer is missing'
validate_release_tool ASCENDANY_DESKTOP_OPENSSL_PATH "${OPENSSL_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_OSSLSIGNCODE_PATH "${OSSLSIGNCODE_BINARY}"
[[ "$(release_tool_identity "${OPENSSL_BINARY}")" == "${OPENSSL_IDENTITY}" &&
   "$(release_tool_identity "${OSSLSIGNCODE_BINARY}")" == "${OSSLSIGNCODE_IDENTITY}" ]] ||
  fail 'Windows signing or verification tool identity changed during the isolated build'
printf '%s\n' "${EXPECTED_SIGN_PATHS[@]}" | sort >"${WORKSPACE}/expected-sign-paths"
awk -F '\t' '$1 == "signed" { print $2 }' "${BROKER_LOG}" | sort >"${WORKSPACE}/actual-sign-paths"
diff -u "${WORKSPACE}/expected-sign-paths" "${WORKSPACE}/actual-sign-paths" ||
  fail 'Windows signing broker log differs from the exact four-file signing contract'
run_osslsigncode verify \
  -in "${BUILT_INSTALLER}" \
  -require-leaf-hash "sha256:${EXPECTED_CERTIFICATE_FINGERPRINT}"
run_osslsigncode extract-signature \
  -pem \
  -in "${BUILT_INSTALLER}" \
  -out "${EXTRACTED_SIGNATURE}"
run_openssl cms -verify \
  -inform PEM \
  -in "${EXTRACTED_SIGNATURE}" \
  -noverify \
  -no_content_verify \
  -no_attr_verify \
  -nosigs \
  -out /dev/null \
  -signer "${INSTALLER_SIGNER_CERTIFICATE}"
actual_certificate_fingerprint="$(
  certificate_sha256_fingerprint "${INSTALLER_SIGNER_CERTIFICATE}"
)" || fail 'signed installer must expose exactly one leaf signer certificate'
readonly ACTUAL_CERTIFICATE_FINGERPRINT="${actual_certificate_fingerprint}"
unset actual_certificate_fingerprint
[[ "${ACTUAL_CERTIFICATE_FINGERPRINT}" == "${EXPECTED_CERTIFICATE_FINGERPRINT}" ]] ||
  fail 'signed installer leaf certificate fingerprint does not match CSC_LINK'

install -m 0644 -- "${BUILT_INSTALLER}" "${STAGING_OUTPUT}/${INSTALLER_NAME}"
installer_hash="$(sha512sum -- "${STAGING_OUTPUT}/${INSTALLER_NAME}" | awk '{ print $1 }')"
[[ "${installer_hash}" =~ ^[0-9a-f]{128}$ ]] || fail 'installer SHA-512 digest is invalid'
printf '%s  %s\n' "${installer_hash}" "${INSTALLER_NAME}" > \
  "${STAGING_OUTPUT}/${INSTALLER_NAME}.sha512"
chmod 0644 -- "${STAGING_OUTPUT}/${INSTALLER_NAME}.sha512"
unset installer_hash
/usr/bin/sync -f -- "${STAGING_OUTPUT}/${INSTALLER_NAME}" \
  "${STAGING_OUTPUT}/${INSTALLER_NAME}.sha512" \
  "${STAGING_OUTPUT}"

printf '%s\n' "${INSTALLER_NAME}" "${INSTALLER_NAME}.sha512" | sort > \
  "${WORKSPACE}/expected-output-paths"
find "${STAGING_OUTPUT}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort > \
  "${WORKSPACE}/actual-output-paths"
diff -u "${WORKSPACE}/expected-output-paths" "${WORKSPACE}/actual-output-paths" ||
  fail 'Windows desktop release output differs from its exact two-file contract'
if find "${STAGING_OUTPUT}" -mindepth 1 ! -type f -print -quit | grep -q .; then
  fail 'Windows desktop release output contains a directory, symlink, or special node'
fi

assert_output_parent_identity 'Windows desktop release publication'
chmod 0755 -- "${STAGING_OUTPUT}"
/usr/bin/sync -f -- "${STAGING_OUTPUT}"
if ! mv --no-target-directory --no-clobber -- "${STAGING_OUTPUT}" "${OUTPUT_DIRECTORY}"; then
  fail 'Windows desktop release publication failed without replacing the target'
fi
/usr/bin/sync -f -- "${OUTPUT_DIRECTORY}" "${OUTPUT_PARENT}"
[[ ! -e "${STAGING_OUTPUT}" && ! -L "${STAGING_OUTPUT}" ]] ||
  fail 'Windows desktop release target appeared during publication'
assert_output_parent_identity 'published Windows desktop release verification'
[[ -d "${OUTPUT_DIRECTORY}" && ! -L "${OUTPUT_DIRECTORY}" ]] ||
  fail 'published Windows desktop release is not one real directory'
[[ "$(/usr/bin/stat -Lc '%u:%a' -- "${OUTPUT_DIRECTORY}")" == "${EUID}:755" ]] ||
  fail 'published Windows desktop release directory ownership or mode drifted'
find "${OUTPUT_DIRECTORY}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort > \
  "${WORKSPACE}/published-output-paths"
diff -u "${WORKSPACE}/expected-output-paths" "${WORKSPACE}/published-output-paths" ||
  fail 'published Windows desktop release differs from its verified staged tree'
for published_path in "${INSTALLER_NAME}" "${INSTALLER_NAME}.sha512"; do
  [[ "$(stat -Lc '%h:%u:%a' -- "${OUTPUT_DIRECTORY}/${published_path}")" == "1:${EUID}:644" ]] ||
    fail 'published Windows desktop release file ownership, mode, or link count drifted'
done
unset published_path
if find "${OUTPUT_DIRECTORY}" -mindepth 1 ! -type f -print -quit | grep -q .; then
  fail 'published Windows desktop release contains a directory, symlink, or special node'
fi
(
  cd -- "${OUTPUT_DIRECTORY}"
  /usr/bin/sha512sum --check -- "${INSTALLER_NAME}.sha512" >/dev/null
) || fail 'published Windows desktop installer checksum differs from the verified staged artifact'
printf '%s\n' "${OUTPUT_DIRECTORY}"
