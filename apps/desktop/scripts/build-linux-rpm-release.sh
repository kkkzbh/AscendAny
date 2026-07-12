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
declare -a environment_removals=()
while IFS= read -r -d '' environment_entry; do
  environment_name="${environment_entry%%=*}"
  case "$environment_name" in
    BASH_FUNC_*%%|INIT_CWD|NODE_OPTIONS|NODE_PATH|npm_*|NPM_*|pnpm_*|PNPM_*|corepack_*|COREPACK_*|ELECTRON_*|CSC_*|WIN_CSC_*)
      environment_removals+=( -u "$environment_name" )
      ;;
  esac
done < <(/usr/bin/env -0)
if (( ${#environment_removals[@]} > 0 )); then
  script_entry="${BASH_SOURCE[0]}"
  [[ "$script_entry" == /* ]] || script_entry="$PWD/$script_entry"
  exec /usr/bin/env "${environment_removals[@]}" /usr/bin/bash -p "$script_entry" "$@"
fi
unset environment_removals environment_entry environment_name
builtin unset BASH_ENV ENV CDPATH GLOBIGNORE
builtin export -n SHELLOPTS BASHOPTS
set -Eeuo pipefail

export LC_ALL=C

script_source="${BASH_SOURCE[0]}"
if [[ "${script_source}" != /* ]]; then
  script_source="${PWD}/${script_source}"
fi
readonly BUILDER_PATH="${script_source}"
readonly SCRIPT_DIR="$(cd -- "${script_source%/*}" && pwd -P)"
readonly DESKTOP_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd -P)"
readonly REPOSITORY_ROOT="$(cd -- "${DESKTOP_ROOT}/../.." && pwd -P)"
unset script_source
readonly VERSION="${ASCENDANY_DESKTOP_VERSION:-}"
readonly REQUESTED_COMMIT="${ASCENDANY_DESKTOP_RELEASE_COMMIT:-}"
readonly OUTPUT_DIRECTORY="${ASCENDANY_DESKTOP_OUTPUT_DIRECTORY:-}"
readonly REQUESTED_SIGNING_FINGERPRINT="${ASCENDANY_RPM_SIGNING_FINGERPRINT:-}"
readonly NODE_BINARY="${ASCENDANY_DESKTOP_NODE_PATH:-}"
readonly PNPM_CLI="${ASCENDANY_DESKTOP_PNPM_CLI_PATH:-}"
readonly BWRAP_BINARY="${ASCENDANY_DESKTOP_BWRAP_PATH:-}"
readonly BUILD_TOOL_ROOT="${ASCENDANY_DESKTOP_BUILD_TOOL_ROOT:-}"
readonly PNPM_STORE_SEED="${ASCENDANY_DESKTOP_PNPM_STORE_PATH:-}"
readonly BUILD_CACHE_SEED="${ASCENDANY_DESKTOP_BUILD_CACHE_PATH:-}"
readonly GPG_BINARY="${ASCENDANY_DESKTOP_GPG_PATH:-}"
readonly RPM_BINARY="${ASCENDANY_DESKTOP_RPM_PATH:-}"
readonly RPMKEYS_BINARY="${ASCENDANY_DESKTOP_RPMKEYS_PATH:-}"
readonly RPMSIGN_BINARY="${ASCENDANY_DESKTOP_RPMSIGN_PATH:-}"
readonly RELEASE_HOME="${HOME:-}"
readonly EXPECTED_GNUPG_HOME="${RELEASE_HOME%/}/.gnupg"
readonly GNUPG_HOME="${GNUPGHOME:-${EXPECTED_GNUPG_HOME}}"
readonly API_ORIGIN="${VITE_API_BASE_URL:-}"
readonly PROMPT_KEY="${VITE_CHAT_PROMPT_CONFIGURATION_KEY:-}"
readonly MODEL_KEY="${VITE_CHAT_MODEL_CONFIGURATION_KEY:-}"
readonly RPM_RCFILES='/usr/lib/rpm/rpmrc:/usr/lib/rpm/redhat/rpmrc'
readonly RPM_MACRO_PATH='/usr/lib/rpm/macros:/usr/lib/rpm/macros.d/macros.*:/usr/lib/rpm/platform/%{_target}/macros:/usr/lib/rpm/fileattrs/*.attr:/usr/lib/rpm/redhat/macros:/etc/rpm/macros.*:/etc/rpm/macros:/etc/rpm/%{_target}/macros'

fail() {
  printf '%s\n' "$1" >&2
  exit 2
}

(( EUID != 0 )) || fail 'desktop release builder requires a dedicated non-root release identity'

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
  local fixed_path='apps/desktop/scripts/build-linux-rpm-release.sh'
  local expected_path="${REPOSITORY_ROOT}/${fixed_path}"
  local captured_path="${SOURCE_ROOT}/${fixed_path}"
  local owner live_mode

  [[ "${BUILDER_PATH}" == "${expected_path}" &&
     -f "${BUILDER_PATH}" && ! -L "${BUILDER_PATH}" &&
     "${BUILDER_PATH}" == "$(/usr/bin/realpath -e -- "${BUILDER_PATH}")" ]] ||
    fail 'RPM release builder must be the canonical fixed repository file'
  owner="$(/usr/bin/stat -Lc '%u' -- "${BUILDER_PATH}")"
  live_mode="$(/usr/bin/stat -Lc '%a' -- "${BUILDER_PATH}")"
  [[ "${owner}" == 0 || "${owner}" == "${EUID}" ]] ||
    fail 'RPM release builder must be owned by root or the release user'
  [[ "${live_mode}" == 755 ]] || fail 'RPM release builder must be mode 0755'
  validate_protected_file_ancestry 'RPM release builder' "${BUILDER_PATH}"

  [[ -f "${captured_path}" && ! -L "${captured_path}" &&
     "$(stat -Lc '%a' -- "${captured_path}")" == 755 ]] ||
    fail 'captured reviewed commit RPM release builder must be one mode 100755 regular file'
  /usr/bin/cmp -s -- "${BUILDER_PATH}" "${captured_path}" ||
    fail 'running RPM release builder bytes differ from the reviewed commit'
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
    /^$/ { headers_done = 1; exit }
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
  captured_tree="$(run_captured_git write-tree)" ||
    fail 'captured desktop tree could not be written'
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
  materialized_tree="$(run_captured_git write-tree)" ||
    fail 'materialized desktop tree could not be written'
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
      --bind "${SOURCE_ROOT}" "${SOURCE_ROOT}" \
      --bind "${BUILDER_OUTPUT}" "${BUILDER_OUTPUT}" \
      --bind "${SANDBOX_HOME}" "${SANDBOX_HOME}" \
      --bind "${SANDBOX_TMPDIR}" "${SANDBOX_TMPDIR}" \
      --bind "${PRIVATE_PNPM_STORE}" "${PRIVATE_PNPM_STORE}" \
      --bind "${PRIVATE_CACHE}" "${PRIVATE_CACHE}" \
      --clearenv \
      --setenv PATH /usr/bin:/bin \
      --setenv HOME "${SANDBOX_HOME}" \
      --setenv TMPDIR "${SANDBOX_TMPDIR}" \
      --setenv XDG_CACHE_HOME "${PRIVATE_CACHE}" \
      --setenv npm_config_store_dir "${PRIVATE_PNPM_STORE}" \
      --setenv NPM_CONFIG_USERCONFIG /dev/null \
      --setenv NPM_CONFIG_GLOBALCONFIG /dev/null \
      --setenv ELECTRON_BUILDER_OFFLINE true \
      --setenv VITE_API_BASE_URL "${API_ORIGIN}" \
      --setenv VITE_CHAT_PROMPT_CONFIGURATION_KEY "${PROMPT_KEY}" \
      --setenv VITE_CHAT_MODEL_CONFIGURATION_KEY "${MODEL_KEY}" \
      --chdir "${SOURCE_ROOT}" \
      "${NODE_BINARY}" "${PNPM_CLI}" "$@"
)

run_gpg() (
  close_inherited_fds_except
  exec /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    HOME="${TOOL_HOME}" \
    GNUPGHOME="${GNUPG_HOME}" \
    LC_ALL=C \
    "${GPG_BINARY}" "$@"
)

run_rpm() (
  close_inherited_fds_except
  exec /usr/bin/env -i PATH=/usr/bin:/bin HOME="${TOOL_HOME}" LC_ALL=C \
    "${RPM_BINARY}" --rcfile="${RPM_RCFILES}" --macros="${RPM_MACRO_PATH}" "$@"
)

run_rpmkeys() (
  close_inherited_fds_except
  exec /usr/bin/env -i PATH=/usr/bin:/bin HOME="${TOOL_HOME}" LC_ALL=C \
    "${RPMKEYS_BINARY}" --rcfile="${RPM_RCFILES}" --macros="${RPM_MACRO_PATH}" "$@"
)

run_rpmsign() (
  close_inherited_fds_except
  exec /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    HOME="${TOOL_HOME}" \
    GNUPGHOME="${GNUPG_HOME}" \
    LC_ALL=C \
    "${RPMSIGN_BINARY}" --rcfile="${RPM_RCFILES}" --macros="${RPM_MACRO_PATH}" "$@"
)

[[ "${DESKTOP_ROOT}" == "${REPOSITORY_ROOT}/apps/desktop" ]] ||
  fail 'desktop release script must run from the repository apps/desktop tree'
PATH=/usr/bin:/bin
export PATH
hash -r

for command_name in awk base64 chmod cp diff dirname env find git grep install mktemp mv realpath rm sha256sum sha512sum sort stat sync tr; do
  command -v "${command_name}" >/dev/null 2>&1 ||
    fail "required command is unavailable: ${command_name}"
done
unset command_name

validate_release_tool ASCENDANY_DESKTOP_NODE_PATH "${NODE_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_PNPM_CLI_PATH "${PNPM_CLI}"
validate_release_tool ASCENDANY_DESKTOP_BWRAP_PATH "${BWRAP_BINARY}"
readonly BWRAP_IDENTITY="$(release_tool_identity "${BWRAP_BINARY}")"
validate_release_tool ASCENDANY_DESKTOP_GPG_PATH "${GPG_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_RPM_PATH "${RPM_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_RPMKEYS_PATH "${RPMKEYS_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_RPMSIGN_PATH "${RPMSIGN_BINARY}"
readonly RPMSIGN_IDENTITY="$(release_tool_identity "${RPMSIGN_BINARY}")"
readonly GPG_IDENTITY="$(release_tool_identity "${GPG_BINARY}")"
readonly RPM_IDENTITY="$(release_tool_identity "${RPM_BINARY}")"
readonly RPMKEYS_IDENTITY="$(release_tool_identity "${RPMKEYS_BINARY}")"
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
[[ "${GNUPG_HOME}" == "${EXPECTED_GNUPG_HOME}" ]] ||
  fail 'GNUPGHOME must equal the canonical HOME/.gnupg release keyring'
[[ "${GNUPG_HOME}" = /* && -d "${GNUPG_HOME}" && ! -L "${GNUPG_HOME}" ]] ||
  fail 'GNUPGHOME must name an absolute real keyring directory'
[[ "${GNUPG_HOME}" == "$(/usr/bin/realpath -e -- "${GNUPG_HOME}")" ]] ||
  fail 'GNUPGHOME must be one canonical path without symlink ancestry'
[[ "$(/usr/bin/stat -Lc '%u' -- "${GNUPG_HOME}")" == "${EUID}" ]] ||
  fail 'GNUPGHOME must be owned by the release user'
gnupg_mode="$((8#$(/usr/bin/stat -Lc '%a' -- "${GNUPG_HOME}")))"
(( (gnupg_mode & 8#077) == 0 )) ||
  fail 'GNUPGHOME must not grant group or other permissions'
unset gnupg_mode
validate_protected_directory_ancestry GNUPGHOME "${GNUPG_HOME}"
if path_contains "${PNPM_STORE_SEED}" "${GNUPG_HOME}" ||
   path_contains "${BUILD_CACHE_SEED}" "${GNUPG_HOME}"; then
  fail 'offline desktop build seed paths must not contain the RPM release keyring'
fi
if path_contains "${REPOSITORY_ROOT}" "${GNUPG_HOME}" ||
   path_contains "${REPOSITORY_ROOT}" "${PNPM_STORE_SEED}" ||
   path_contains "${REPOSITORY_ROOT}" "${BUILD_CACHE_SEED}" ||
   path_contains "${REPOSITORY_ROOT}" "${BUILD_TOOL_ROOT}" ||
   path_contains "${REPOSITORY_ROOT}" "${NODE_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${PNPM_CLI}" ||
   path_contains "${REPOSITORY_ROOT}" "${BWRAP_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${GPG_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${RPM_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${RPMKEYS_BINARY}" ||
   path_contains "${REPOSITORY_ROOT}" "${RPMSIGN_BINARY}"; then
  fail 'release inputs and tools must remain outside the live repository mount mask'
fi
for external_path in "${PNPM_STORE_SEED}" "${BUILD_CACHE_SEED}" "${BUILD_TOOL_ROOT}"; do
  if path_contains "${RELEASE_HOME}" "${external_path}"; then
    fail 'RPM offline seeds and build tools must remain outside the masked release HOME'
  fi
done
unset external_path
for rpm_policy_file in /usr/lib/rpm/rpmrc /usr/lib/rpm/redhat/rpmrc; do
  [[ -f "${rpm_policy_file}" && ! -L "${rpm_policy_file}" &&
     "$(stat -Lc '%u' -- "${rpm_policy_file}")" == 0 ]] ||
    fail 'RPM fixed rc policy must be a root-owned regular file'
  (( (8#$(stat -Lc '%a' -- "${rpm_policy_file}") & 8#022) == 0 )) ||
    fail 'RPM fixed rc policy must not be group- or other-writable'
  validate_protected_file_ancestry 'RPM fixed rc policy' "${rpm_policy_file}"
done
unset rpm_policy_file

(( ${#VERSION} <= 128 )) ||
  fail 'ASCENDANY_DESKTOP_VERSION must be no longer than 128 ASCII bytes'
is_canonical_semver "${VERSION}" ||
  fail 'ASCENDANY_DESKTOP_VERSION must be one canonical SemVer value'
[[ "${REQUESTED_COMMIT}" =~ ^[0-9a-f]{40}$ ]] ||
  fail 'ASCENDANY_DESKTOP_RELEASE_COMMIT must be one explicit lowercase 40-hex commit ID'
[[ "$(run_repository_git rev-parse --show-toplevel)" == "${REPOSITORY_ROOT}" ]] ||
  fail 'desktop release repository root is invalid'
commit="$(run_repository_git rev-parse --verify "${REQUESTED_COMMIT}^{commit}" 2>/dev/null)" ||
  fail 'desktop release commit does not identify an available commit object'
[[ "${commit}" == "${REQUESTED_COMMIT}" ]] ||
  fail 'desktop release commit did not resolve to the exact requested object ID'
readonly COMMIT="${commit}"
unset commit
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
[[ "${REQUESTED_SIGNING_FINGERPRINT}" =~ ^[0-9A-Fa-f]{40}$ ]] ||
  fail 'ASCENDANY_RPM_SIGNING_FINGERPRINT must be one full 40-hex signing fingerprint'
readonly SIGNING_FINGERPRINT="$(
  printf '%s' "${REQUESTED_SIGNING_FINGERPRINT}" | tr '[:lower:]' '[:upper:]'
)"

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
WORKSPACE="$(mktemp -d "${OUTPUT_PARENT}/.ascendany-desktop-rpm.XXXXXXXX")"
chmod 0700 -- "${WORKSPACE}"
readonly WORKSPACE
readonly WORKSPACE_BASENAME="${WORKSPACE##*/}"
readonly SOURCE_ROOT="${WORKSPACE}/source"
readonly BUILDER_OUTPUT="${WORKSPACE}/builder-output"
readonly STAGING_OUTPUT="${WORKSPACE}/published-output"
readonly SIGNATURE_PACKET="${WORKSPACE}/rpm-signature.pgp"
readonly RPM_PUBLIC_KEY="${WORKSPACE}/rpm-signing-key.asc"
readonly RPM_DATABASE="${WORKSPACE}/rpm-database"
readonly TOOL_TMPDIR="${WORKSPACE}/tool-tmp"
readonly TOOL_HOME="${WORKSPACE}/tool-home"
readonly SANDBOX_HOME="${WORKSPACE}/sandbox-home"
readonly SANDBOX_TMPDIR="${WORKSPACE}/sandbox-tmp"
readonly PRIVATE_PNPM_STORE="${WORKSPACE}/pnpm-store"
readonly PRIVATE_CACHE="${WORKSPACE}/cache"
readonly CAPTURED_OBJECTS="${WORKSPACE}/captured-objects"
readonly CAPTURED_INDEX="${WORKSPACE}/captured-index"
readonly CAPTURED_INDEX_INFO="${WORKSPACE}/captured-index-info"
readonly CAPTURED_COMMIT_FILE="${WORKSPACE}/captured-commit"
readonly CAPTURED_BLOB="${WORKSPACE}/captured-blob"

cleanup() {
  rm -rf -- "${WORKSPACE}"
  rm -rf -- "/proc/self/fd/${OUTPUT_PARENT_FD}/${WORKSPACE_BASENAME}" 2>/dev/null || true
  exec {OUTPUT_PARENT_FD}<&- 2>/dev/null || true
}
trap cleanup EXIT

install -d -m 0700 -- \
  "${SOURCE_ROOT}" \
  "${BUILDER_OUTPUT}" \
  "${STAGING_OUTPUT}" \
  "${RPM_DATABASE}" \
  "${TOOL_TMPDIR}" \
  "${TOOL_HOME}" \
  "${SANDBOX_HOME}" \
  "${SANDBOX_TMPDIR}" \
  "${PRIVATE_PNPM_STORE}" \
  "${PRIVATE_CACHE}" \
  "${CAPTURED_OBJECTS}"
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

key_listing="$(
  run_gpg --batch --with-colons --list-secret-keys "${SIGNING_FINGERPRINT}" 2>/dev/null
)" || fail 'the requested RPM signing secret key is unavailable'
printf '%s\n' "${key_listing}" |
  awk -F: -v expected="${SIGNING_FINGERPRINT}" '
    $1 == "sec" {
      primary_trusted = ($2 == "u" || $2 == "f")
      candidate_capabilities = tolower($12)
      next
    }
    $1 == "ssb" {
      candidate_capabilities = tolower($12)
      next
    }
    $1 == "fpr" {
      fingerprint = toupper($10)
      if (fingerprint == expected && primary_trusted && candidate_capabilities ~ /s/) {
        matches += 1
      }
      candidate_capabilities = ""
    }
    END { exit(matches == 1 ? 0 : 1) }
  ' || fail 'RPM signing fingerprint must identify exactly one trusted secret signing key'
unset key_listing

(
  cd -- "${SOURCE_ROOT}"
  run_snapshot_pnpm install --frozen-lockfile --offline
  run_snapshot_pnpm --filter @ascendany/desktop build
  run_snapshot_pnpm --filter @ascendany/desktop exec electron-builder \
    --linux rpm \
    --x64 \
    --publish never \
    --config.directories.output="${BUILDER_OUTPUT}" \
    --config.artifactName="AscendAny-linux-x64-${VERSION}.rpm" \
    --config.extraMetadata.ascendanyReleaseCommit="${COMMIT}" \
    --config.extraMetadata.version="${VERSION}"
)
assert_output_parent_identity 'RPM verification'

readonly PACKAGE_NAME="AscendAny-linux-x64-${VERSION}.rpm"
readonly BUILT_PACKAGE="${BUILDER_OUTPUT}/${PACKAGE_NAME}"
[[ -f "${BUILT_PACKAGE}" && ! -L "${BUILT_PACKAGE}" &&
   "$(stat -Lc '%h:%u' -- "${BUILT_PACKAGE}")" == "1:${EUID}" ]] ||
  fail 'expected RPM package is missing'
validate_release_tool ASCENDANY_DESKTOP_RPMSIGN_PATH "${RPMSIGN_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_GPG_PATH "${GPG_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_RPM_PATH "${RPM_BINARY}"
validate_release_tool ASCENDANY_DESKTOP_RPMKEYS_PATH "${RPMKEYS_BINARY}"
[[ "$(release_tool_identity "${RPMSIGN_BINARY}")" == "${RPMSIGN_IDENTITY}" &&
   "$(release_tool_identity "${GPG_BINARY}")" == "${GPG_IDENTITY}" &&
   "$(release_tool_identity "${RPM_BINARY}")" == "${RPM_IDENTITY}" &&
   "$(release_tool_identity "${RPMKEYS_BINARY}")" == "${RPMKEYS_IDENTITY}" ]] ||
  fail 'RPM signing or verification tool identity changed after the isolated build'
validate_protected_directory_ancestry GNUPGHOME "${GNUPG_HOME}"
run_rpmsign \
  --addsign \
  --key-id="${SIGNING_FINGERPRINT}" \
  --define "__gpg ${GPG_BINARY}" \
  "${BUILT_PACKAGE}"

signature_base64="$(run_rpm -qp --queryformat '%{OPENPGP}' "${BUILT_PACKAGE}")" ||
  fail 'RPM OpenPGP signature packet is unavailable'
[[ "${signature_base64}" =~ ^[A-Za-z0-9+/]+={0,2}$ ]] ||
  fail 'RPM OpenPGP signature packet is not canonical base64'
printf '%s' "${signature_base64}" | base64 --decode >"${SIGNATURE_PACKET}" ||
  fail 'RPM OpenPGP signature packet could not be decoded'
unset signature_base64
packet_listing="$(run_gpg --batch --list-packets "${SIGNATURE_PACKET}" 2>&1)" ||
  fail 'RPM OpenPGP signature packet could not be inspected'
actual_signing_fingerprint="$(
  printf '%s\n' "${packet_listing}" |
    awk '
      /issuer fpr v4 / {
        value = $0
        sub(/^.*issuer fpr v4 /, "", value)
        sub(/[^0-9A-Fa-f].*$/, "", value)
        print toupper(value)
        matches += 1
      }
      END { if (matches != 1) exit 1 }
    '
)" || fail 'RPM signature must contain exactly one full v4 issuer fingerprint'
[[ "${actual_signing_fingerprint}" =~ ^[0-9A-F]{40}$ ]] ||
  fail 'RPM artifact signer fingerprint is not one full 40-hex fingerprint'
[[ "${actual_signing_fingerprint}" == "${SIGNING_FINGERPRINT}" ]] ||
  fail 'RPM artifact signer fingerprint does not match the requested signing fingerprint'
unset actual_signing_fingerprint packet_listing

run_gpg --batch --armor --export "${SIGNING_FINGERPRINT}" >"${RPM_PUBLIC_KEY}"
[[ -s "${RPM_PUBLIC_KEY}" && -f "${RPM_PUBLIC_KEY}" && ! -L "${RPM_PUBLIC_KEY}" ]] ||
  fail 'RPM signing public key export failed'
run_rpmkeys --dbpath "${RPM_DATABASE}" --import "${RPM_PUBLIC_KEY}"
run_rpmkeys --dbpath "${RPM_DATABASE}" --checksig --verbose "${BUILT_PACKAGE}"

install -m 0644 -- "${BUILT_PACKAGE}" "${STAGING_OUTPUT}/${PACKAGE_NAME}"
package_hash="$(sha512sum -- "${STAGING_OUTPUT}/${PACKAGE_NAME}" | awk '{ print $1 }')"
[[ "${package_hash}" =~ ^[0-9a-f]{128}$ ]] || fail 'RPM package SHA-512 digest is invalid'
printf '%s  %s\n' "${package_hash}" "${PACKAGE_NAME}" > \
  "${STAGING_OUTPUT}/${PACKAGE_NAME}.sha512"
chmod 0644 -- "${STAGING_OUTPUT}/${PACKAGE_NAME}.sha512"
unset package_hash
/usr/bin/sync -f -- "${STAGING_OUTPUT}/${PACKAGE_NAME}" \
  "${STAGING_OUTPUT}/${PACKAGE_NAME}.sha512" \
  "${STAGING_OUTPUT}"

printf '%s\n' "${PACKAGE_NAME}" "${PACKAGE_NAME}.sha512" | sort > \
  "${WORKSPACE}/expected-output-paths"
find "${STAGING_OUTPUT}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort > \
  "${WORKSPACE}/actual-output-paths"
diff -u "${WORKSPACE}/expected-output-paths" "${WORKSPACE}/actual-output-paths" ||
  fail 'Linux RPM desktop release output differs from its exact two-file contract'
if find "${STAGING_OUTPUT}" -mindepth 1 ! -type f -print -quit | grep -q .; then
  fail 'Linux RPM desktop release output contains a directory, symlink, or special node'
fi

assert_output_parent_identity 'Linux RPM desktop release publication'
chmod 0755 -- "${STAGING_OUTPUT}"
/usr/bin/sync -f -- "${STAGING_OUTPUT}"
if ! mv --no-target-directory --no-clobber -- "${STAGING_OUTPUT}" "${OUTPUT_DIRECTORY}"; then
  fail 'Linux RPM desktop release publication failed without replacing the target'
fi
/usr/bin/sync -f -- "${OUTPUT_DIRECTORY}" "${OUTPUT_PARENT}"
[[ ! -e "${STAGING_OUTPUT}" && ! -L "${STAGING_OUTPUT}" ]] ||
  fail 'Linux RPM desktop release target appeared during publication'
assert_output_parent_identity 'published Linux RPM desktop release verification'
[[ -d "${OUTPUT_DIRECTORY}" && ! -L "${OUTPUT_DIRECTORY}" ]] ||
  fail 'published Linux RPM desktop release is not one real directory'
[[ "$(/usr/bin/stat -Lc '%u:%a' -- "${OUTPUT_DIRECTORY}")" == "${EUID}:755" ]] ||
  fail 'published Linux RPM desktop release directory ownership or mode drifted'
find "${OUTPUT_DIRECTORY}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort > \
  "${WORKSPACE}/published-output-paths"
diff -u "${WORKSPACE}/expected-output-paths" "${WORKSPACE}/published-output-paths" ||
  fail 'published Linux RPM desktop release differs from its verified staged tree'
for published_path in "${PACKAGE_NAME}" "${PACKAGE_NAME}.sha512"; do
  [[ "$(stat -Lc '%h:%u:%a' -- "${OUTPUT_DIRECTORY}/${published_path}")" == "1:${EUID}:644" ]] ||
    fail 'published Linux RPM desktop release file ownership, mode, or link count drifted'
done
unset published_path
if find "${OUTPUT_DIRECTORY}" -mindepth 1 ! -type f -print -quit | grep -q .; then
  fail 'published Linux RPM desktop release contains a directory, symlink, or special node'
fi
(
  cd -- "${OUTPUT_DIRECTORY}"
  /usr/bin/sha512sum --check -- "${PACKAGE_NAME}.sha512" >/dev/null
) || fail 'published Linux RPM checksum differs from the verified staged artifact'
printf '%s\n' "${OUTPUT_DIRECTORY}"
