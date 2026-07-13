#!/usr/bin/bash -p
if [[ "${BASH:-}" != "/usr/bin/bash" ||
      "$-" != *p* ||
      "$-" == *[cis]* ||
      -n "${BASH_EXECUTION_STRING:-}" ||
      "${#BASH_SOURCE[@]}" -ne 1 ||
      "${BASH_SOURCE[0]}" != "$0" ]]; then
  /usr/bin/printf '%s\n' 'release installer must run directly under /usr/bin/bash -p' >&2
  /usr/bin/kill -KILL "${BASHPID}"
fi
installer_environment_is_clean=1
while IFS= read -r environment_name; do
  case "$environment_name" in
    PATH|LC_ALL|PWD|SHLVL|_|ASCENDANY_RELEASE_INSTALLER_CLEAN_ENV)
      ;;
    *)
      installer_environment_is_clean=0
      ;;
  esac
done < <(builtin compgen -e)
if [[ "${ASCENDANY_RELEASE_INSTALLER_CLEAN_ENV-}" != "1" ||
      "${PATH-}" != "/usr/bin:/bin" || "${LC_ALL-}" != "C" ||
      "$installer_environment_is_clean" != 1 ]]; then
  script_entry="${BASH_SOURCE[0]}"
  [[ "$script_entry" == /* ]] || script_entry="$PWD/$script_entry"
  exec -c /usr/bin/env \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_RELEASE_INSTALLER_CLEAN_ENV=1 \
    /usr/bin/bash -p "$script_entry" "$@"
fi
unset installer_environment_is_clean environment_name script_entry
builtin unset BASH_ENV ENV CDPATH GLOBIGNORE POSIXLY_CORRECT TMPDIR
builtin export -n SHELLOPTS BASHOPTS
set -Eeuo pipefail

export PATH=/usr/bin:/bin
export LC_ALL=C
umask 077

usage() {
  /usr/bin/printf 'usage: %s --source /absolute/root-owned/release --manifest-sha256 64_LOWER_HEX [--replace-installed-manifest-sha256 64_LOWER_HEX --replace-installed-identity DEVICE:INODE] [--expected-purpose production|acceptance_test]\n' "$0" >&2
}

die() {
  case "${release_state:-preflight}" in
    staged)
      /usr/bin/printf \
        'release installation state: pre-commit-staging-retained parent-identity=%s basename=%s expected-path=%s/%s\n' \
        "${install_parent_identity:-unknown}" \
        "${stage_name:-unknown}" \
        "${install_parent:-/opt/ascendany}" \
        "${stage_name:-unknown}" >&2
      ;;
    committed)
      if [[ "${release_operation:-promote}" == replace ]]; then
        /usr/bin/printf \
          'release installation state: committed-unverified target=%s identity=%s retired-stage=%s/%s removal-tombstone=%s/%s expected-retired-identity=%s\n' \
          "${install_target:-/opt/ascendany/v2}" \
          "${published_target_identity:-unknown}" \
          "${install_parent:-/opt/ascendany}" \
          "${stage_name:-unknown}" \
          "${install_parent:-/opt/ascendany}" \
          "${removal_name:-unknown}" \
          "${installed_identity:-unknown}" >&2
      else
        /usr/bin/printf \
          'release installation state: committed-unverified target=%s identity=%s\n' \
          "${install_target:-/opt/ascendany/v2}" \
          "${published_target_identity:-unknown}" >&2
      fi
      ;;
  esac
  /usr/bin/printf 'release installation failed: %s\n' "$*" >&2
  exit 1
}

release_state=preflight

for required_command in \
  awk chmod cmp dd diff env find findmnt flock grep jq kill mkdir mktemp mv \
  printf realpath sha256sum sort stat sync systemctl; do
  [[ -x "/usr/bin/$required_command" ]] || die "required command is missing: /usr/bin/$required_command"
done
unset required_command

[[ "$EUID" == 0 ]] || die 'installer must run as root'
if (( $# < 4 )) || [[ "$1" != '--source' || "$3" != '--manifest-sha256' ]]; then
  usage
  exit 2
fi
source_root="$2"
expected_manifest_sha256="$4"
release_operation=promote
expected_installed_manifest_sha256=''
installed_identity=''
expected_release_purpose=production
case "$#" in
  4)
    ;;
  6)
    [[ "$5" == '--expected-purpose' ]] || { usage; exit 2; }
    expected_release_purpose="$6"
    ;;
  8)
    [[ "$5" == '--replace-installed-manifest-sha256' && "$7" == '--replace-installed-identity' ]] || {
      usage
      exit 2
    }
    release_operation=replace
    expected_installed_manifest_sha256="$6"
    installed_identity="$8"
    ;;
  10)
    [[ "$5" == '--replace-installed-manifest-sha256' && "$7" == '--replace-installed-identity' &&
       "$9" == '--expected-purpose' ]] || {
      usage
      exit 2
    }
    release_operation=replace
    expected_installed_manifest_sha256="$6"
    installed_identity="$8"
    expected_release_purpose="${10}"
    ;;
  *)
    usage
    exit 2
    ;;
esac
[[ "$expected_manifest_sha256" =~ ^[0-9a-f]{64}$ ]] || die 'manifest trust anchor must be exactly 64 lowercase hexadecimal characters'
if [[ "$release_operation" == replace ]]; then
  [[ "$expected_installed_manifest_sha256" =~ ^[0-9a-f]{64}$ ]] ||
    die 'installed manifest trust anchor must be exactly 64 lowercase hexadecimal characters'
  [[ "$installed_identity" =~ ^(0|[1-9][0-9]*):(0|[1-9][0-9]*)$ ]] ||
    die 'installed release identity must be canonical DEVICE:INODE decimal values'
  IFS=: read -r installed_device installed_inode <<<"$installed_identity"
  readonly installed_device installed_inode
fi
[[ "$expected_release_purpose" == production || "$expected_release_purpose" == acceptance_test ]] ||
  die 'expected release purpose must be exactly production or acceptance_test'
readonly release_operation expected_installed_manifest_sha256 installed_identity expected_release_purpose
readonly install_parent='/opt/ascendany'
readonly install_target='/opt/ascendany/v2'
readonly lock_path='/opt/ascendany/.install-v2-release.lock'
readonly manifest_relative='release-manifest.json'
readonly max_manifest_size=1048576
readonly max_payload_file_size=1073741824
readonly max_payload_total_size=4294967296
readonly canonical_semver_pattern='^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))([.]((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)))*)?([+][0-9A-Za-z-]+([.][0-9A-Za-z-]+)*)?$'

readonly -a required_paths=(
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
  systemd/ascendany-cloudflared.service
  systemd/ascendany-judge@.service
  systemd/ascendany-lsp@.service
  systemd/ascendany-migrate.service
  systemd/ascendany-pgbouncer.service
  systemd/ascendany-restore-verify@.service
  polkit-1/rules.d/60-ascendany-judge.rules
  polkit-1/rules.d/61-ascendany-lsp.rules
  sysusers.d/ascendany-v2.conf
  tmpfiles.d/ascendany-v2.conf
  scripts/publish-restore-evidence.sh
  scripts/restore-verify-operator.sh
  scripts/install-v2-release.sh
  scripts/acquire-judge-image.sh
  scripts/attest-judge-image.sh
  scripts/judge-image-contract.sh
  scripts/preload-judge-image.sh
  scripts/acquire-pgbouncer-rpm.sh
  scripts/attest-pgbouncer-rpm.sh
  scripts/provision-postgres-pgbouncer.sh
  scripts/postgres-schema-fingerprint.sh
  scripts/validate-cloudflared.sh
  scripts/validate-production.sh
)
readonly -a required_directories=(
  bin
  models
  operators
  contracts
  contracts/openapi
  contracts/pintia
  db
  db/roles
  config
  systemd
  systemd/ascendanyd.service.d
  polkit-1
  polkit-1/rules.d
  sysusers.d
  tmpfiles.d
  scripts
)
readonly -a release_consumer_service_units=(
  ascendanyd.service
  ascendany-model-register.service
  ascendany-model-activate.service
  ascendany-catalog-publish.service
  ascendany-admin-bootstrap.service
  ascendany-backup.service
  ascendany-cloudflared.service
  ascendany-migrate.service
  ascendany-pgbouncer.service
)
readonly -a release_consumer_timer_units=(
  ascendany-backup.timer
)
readonly -a release_consumer_instance_patterns=(
  'ascendany-restore-verify@*.service'
  'ascendany-judge@*.service'
  'ascendany-lsp@*.service'
)
declare -A release_object_identities=()

validate_safe_relative_path() {
  local relative="$1"
  local remaining component

  [[ -n "$relative" && "$relative" != /* && "$relative" != */ &&
     "$relative" != *//* && "$relative" =~ ^[0-9A-Za-z_@./+-]+$ ]] || return 1
  (( ${#relative} <= 4096 )) || return 1
  remaining="$relative"
  while :; do
    component="${remaining%%/*}"
    [[ -n "$component" && "$component" != . && "$component" != .. ]] || return 1
    (( ${#component} <= 255 )) || return 1
    [[ "$remaining" == */* ]] || break
    remaining="${remaining#*/}"
  done
}

validate_root_owned_ancestry() {
  local label="$1"
  local directory="$2"
  local current=/ component metadata owner group mode_text mode is_leaf
  local -a components=()

  [[ "$directory" == /* && ! "$directory" =~ [[:cntrl:]] ]] || die "$label must be one absolute path without control characters"
  IFS=/ read -r -a components <<<"${directory#/}"
  for component in '' "${components[@]}"; do
    [[ -z "$component" ]] || current="${current%/}/$component"
    [[ -d "$current" && ! -L "$current" ]] || die "$label has a missing, non-directory, or symbolic-link ancestor: $current"
    metadata="$(/usr/bin/stat -Lc '%u:%g:%a' -- "$current")" || die "$label metadata cannot be read: $current"
    IFS=: read -r owner group mode_text <<<"$metadata"
    [[ "$owner" == 0 && "$group" == 0 ]] || die "$label ancestry must be owned by root:root: $current"
    mode="$((8#$mode_text))"
    is_leaf=0
    [[ "$current" != "$directory" ]] || is_leaf=1
    if (( (mode & 8#022) != 0 )); then
      if (( is_leaf == 1 )); then
        die "$label leaf must not be group- or other-writable: $current"
      fi
      if (( (mode & 8#1000) == 0 )); then
        die "$label has an unprotected writable ancestor: $current"
      fi
    fi
  done
}

directory_identity() {
  /usr/bin/stat -Lc '%d:%i' -- "$1"
}

assert_source_identity() {
  local phase="$1"
  validate_root_owned_ancestry 'release source' "$source_root"
  [[ -d "$source_root" && ! -L "$source_root" &&
     "$(directory_identity "$source_root")" == "$source_identity" &&
     "$(directory_identity "$source_anchor")" == "$source_identity" ]] ||
    die "release source identity changed before $phase"
}

assert_install_parent_identity() {
  local phase="$1"
  validate_root_owned_ancestry 'installation parent' "$install_parent"
  [[ "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$install_parent")" == '0:0:755' &&
     "$(directory_identity "$install_parent")" == "$install_parent_identity" &&
     "$(directory_identity "$install_parent_anchor")" == "$install_parent_identity" ]] ||
    die "installation parent identity changed before $phase"
}

assert_installed_identity() {
  local phase="$1"

  assert_install_parent_identity "$phase"
  validate_root_owned_ancestry 'installed release' "$install_target"
  [[ -d "$install_target" && ! -L "$install_target" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$install_target")" == '0:0:755' &&
     "$(directory_identity "$install_target")" == "$installed_identity" &&
     "$(directory_identity "$install_parent_anchor/v2")" == "$installed_identity" &&
     "$(directory_identity "$installed_anchor")" == "$installed_identity" ]] ||
    die "installed release identity changed before $phase"
}

index_release_tree_objects() {
  local tree_root="$1"
  local path identity

  while IFS= read -r -d '' path; do
    identity="$(/usr/bin/stat -Lc '%d:%i' -- "$path")" ||
      die "release object identity cannot be read while establishing replacement quiescence: $path"
    release_object_identities["$identity"]=1
  done < <(/usr/bin/find -H "$tree_root" -print0)
}

assert_release_consumers_quiesced() {
  local phase="$1"
  local unit state instance_rows instance load active sub remainder argument
  local process_path process_pid process_fd process_anchor reference identity process_uses_release
  local -a process_paths=() process_references=()

  for unit in "${release_consumer_service_units[@]}"; do
    if ! state="$(/usr/bin/systemctl show --no-pager \
        --property=LoadState --property=ActiveState --property=SubState --property=MainPID \
        -- "$unit")"; then
      die "release consumer systemd state cannot be read before $phase: $unit"
    fi
    [[ "$state" == $'LoadState=loaded\nActiveState=inactive\nSubState=dead\nMainPID=0' ]] ||
      die "release consumer systemd unit is not quiesced before $phase: $unit"
  done
  for unit in "${release_consumer_timer_units[@]}"; do
    if ! state="$(/usr/bin/systemctl show --no-pager \
        --property=LoadState --property=ActiveState --property=SubState \
        -- "$unit")"; then
      die "release consumer systemd state cannot be read before $phase: $unit"
    fi
    [[ "$state" == $'LoadState=loaded\nActiveState=inactive\nSubState=dead' ]] ||
      die "release consumer systemd unit is not quiesced before $phase: $unit"
  done

  if ! instance_rows="$(/usr/bin/systemctl list-units --all --plain --no-legend --no-pager \
      --type=service -- "${release_consumer_instance_patterns[@]}")"; then
    die "release consumer systemd instances cannot be enumerated before $phase"
  fi
  while read -r instance load active sub remainder; do
    [[ -n "$instance" ]] || continue
    if ! state="$(/usr/bin/systemctl show --no-pager \
        --property=LoadState --property=ActiveState --property=SubState --property=MainPID \
        -- "$instance")"; then
      die "release consumer systemd instance state cannot be read before $phase: $instance"
    fi
    [[ "$state" == $'LoadState=loaded\nActiveState=inactive\nSubState=dead\nMainPID=0' ]] ||
      die "release consumer systemd instance is not quiesced before $phase: $instance"
  done <<<"$instance_rows"

  shopt -s nullglob
  process_paths=(/proc/[1-9]*)
  shopt -u nullglob
  for process_path in "${process_paths[@]}"; do
    [[ -d "$process_path" ]] || continue
    process_pid="${process_path##*/}"
    [[ "$process_pid" != "$BASHPID" ]] || continue
    process_fd=''
    if ! { exec {process_fd}<"$process_path"; } 2>/dev/null; then
      continue
    fi
    process_anchor="/proc/$BASHPID/fd/$process_fd"
    if [[ ! -d "$process_anchor" ]]; then
      exec {process_fd}<&-
      continue
    fi
    process_references=("$process_anchor/exe" "$process_anchor/cwd" "$process_anchor/root")
    shopt -s nullglob
    process_references+=("$process_anchor"/fd/* "$process_anchor"/map_files/*)
    shopt -u nullglob
    process_uses_release=0
    for reference in "${process_references[@]}"; do
      [[ -e "$reference" || -L "$reference" ]] || continue
      identity="$(/usr/bin/stat -Lc '%d:%i' -- "$reference" 2>/dev/null)" || continue
      if [[ -n "${release_object_identities[$identity]+present}" ]]; then
        process_uses_release=1
        break
      fi
    done
    if (( process_uses_release == 0 )) && [[ -r "$process_anchor/cmdline" ]]; then
      while IFS= read -r -d '' argument; do
        case "$argument" in
          "$install_target"|"$install_target"/*|*="$install_target"|*="$install_target"/*)
            process_uses_release=1
            break
            ;;
        esac
      done <"$process_anchor/cmdline"
    fi
    exec {process_fd}<&-
    (( process_uses_release == 0 )) ||
      die "release-owned process is not quiesced before $phase: pid=$process_pid"
  done
}

copy_bounded_regular_file() {
  local source="$1"
  local destination="$2"
  local expected_size="$3"

  (set -o noclobber; : >"$destination") 2>/dev/null ||
    die "capture destination already exists: $destination"
  [[ -f "$destination" && ! -L "$destination" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$destination")" == '0:0:600:1' ]] ||
    die "capture destination metadata is invalid: $destination"
  /usr/bin/dd \
    if="$source" \
    of="$destination" \
    bs=1M \
    count="$((expected_size + 1))" \
    iflag=fullblock,nofollow,nonblock,count_bytes \
    oflag=nofollow \
    status=none || die "failed to capture regular file: $source"
  [[ "$(/usr/bin/stat -Lc '%s' -- "$destination")" == "$expected_size" ]] ||
    die "captured file size differs from its manifest-bound size: $source"
}

file_sha256() {
  /usr/bin/sha256sum -- "$1" | /usr/bin/awk '{print $1}'
}

validate_manifest_contract() {
  local manifest_path="$1"
  local label="$2"
  local version_name="$3"
  local manifest_release_purpose
  local -n manifest_version_ref="$version_name"

  /usr/bin/jq -e 'type' "$manifest_path" >/dev/null 2>&1 || die "$label is not valid JSON"
  if ! /usr/bin/jq -jSc . "$manifest_path" | /usr/bin/cmp --silent -- "$manifest_path" -; then
    die "$label bytes are not canonical jq -jSc JSON"
  fi
  manifest_release_purpose="$(/usr/bin/jq -er '.purpose | select(. == "production" or . == "acceptance_test")' "$manifest_path" 2>/dev/null)" ||
    die "$label purpose is invalid"
  if [[ "$manifest_release_purpose" != "$expected_release_purpose" ]]; then
    if [[ "$label" == 'release manifest' ]]; then
      die "release purpose $manifest_release_purpose differs from expected $expected_release_purpose"
    fi
    die "$label purpose $manifest_release_purpose differs from expected $expected_release_purpose"
  fi

  /usr/bin/jq -e \
    --arg expected_purpose "$expected_release_purpose" \
    --argjson max_file_size "$max_payload_file_size" \
    --argjson max_total_size "$max_payload_total_size" '
    type == "object" and
    keys == ["build", "commit", "files", "purpose", "schema", "sourceDateEpoch", "version"] and
    .schema == "ascendany.release.v2" and
    .purpose == $expected_purpose and
    (.version | type == "string" and length >= 1 and length <= 128) and
    (.commit | type == "string" and test("^[0-9a-f]{40}$")) and
    (.sourceDateEpoch | type == "number" and (floor == .) and . >= 0 and . <= 4102444800) and
    (.build | type == "object" and
      keys == ["cgoEnabled", "goExperiment", "goVersion", "goamd64", "goarch", "gofips140", "goos"] and
      .cgoEnabled == false and .goos == "linux" and .goarch == "amd64" and
      .goamd64 == "v1" and .gofips140 == "off" and
      (.goVersion | type == "string" and test("^go[0-9]+[.][0-9]+([.][0-9]+)?([A-Za-z0-9.:_+~-]+)?$")) and
      (.goExperiment | type == "string" and test("^(none|[0-9A-Za-z_,.-]+)$"))) and
    (.files | type == "array" and length >= 1 and length <= 1024) and
    (all(.files[];
      type == "object" and keys == ["mode", "path", "sha256", "size"] and
      (.path | type == "string" and length >= 1 and length <= 4096 and test("^[0-9A-Za-z_@./+-]+$")) and
      (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
      (.size | type == "number" and (floor == .) and . > 0 and . <= $max_file_size) and
      (.mode == "0555" or .mode == "0644" or .mode == "0755"))) and
    ([.files[].path] | length == (unique | length)) and
    ([.files[].size] | add <= $max_total_size)
    ' "$manifest_path" >/dev/null || die "$label violates the strict installation contract"

  manifest_version_ref="$(/usr/bin/jq -r '.version' "$manifest_path")"
  [[ "$manifest_version_ref" =~ $canonical_semver_pattern ]] || die "$label version is not canonical SemVer"
}

load_manifest_entries() {
  local manifest_path="$1"
  local label="$2"
  local paths_name="$3"
  local hashes_name="$4"
  local sizes_name="$5"
  local modes_name="$6"
  local relative expected_sha expected_size expected_mode index
  local -n paths_ref="$paths_name"
  local -n hashes_ref="$hashes_name"
  local -n sizes_ref="$sizes_name"
  local -n modes_ref="$modes_name"

  paths_ref=()
  hashes_ref=()
  sizes_ref=()
  modes_ref=()
  while IFS=$'\t' read -r relative expected_sha expected_size expected_mode; do
    validate_safe_relative_path "$relative" || die "$label contains an unsafe path"
    paths_ref+=("$relative")
    hashes_ref+=("$expected_sha")
    sizes_ref+=("$expected_size")
    modes_ref+=("$expected_mode")
  done < <(/usr/bin/jq -r '.files[] | [.path, .sha256, (.size | tostring), .mode] | @tsv' "$manifest_path")

  (( ${#paths_ref[@]} == ${#required_paths[@]} )) || die "$label file count differs from the release contract"
  for index in "${!required_paths[@]}"; do
    relative="${required_paths[$index]}"
    [[ "${paths_ref[$index]}" == "$relative" ]] || die "$label path order or closed set differs from the release contract"
    expected_mode=0644
    if [[ "$relative" == bin/* || "$relative" == scripts/* ]]; then
      expected_mode=0755
    elif [[ "$relative" == 'operators/ascendany-production-initialize.mjs' ]]; then
      expected_mode=0555
    fi
    [[ "${modes_ref[$index]}" == "$expected_mode" ]] || die "$label mode differs from the release contract: $relative"
  done
}

decimal_string_is_greater() {
  local candidate="$1"
  local installed="$2"

  (( ${#candidate} > ${#installed} )) && return 0
  (( ${#candidate} < ${#installed} )) && return 1
  [[ "$candidate" > "$installed" ]]
}

semver_is_strictly_greater() {
  local candidate="${1%%+*}"
  local installed="${2%%+*}"
  local candidate_core candidate_prerelease installed_core installed_prerelease
  local candidate_identifier installed_identifier index
  local -a candidate_core_parts installed_core_parts candidate_prerelease_parts installed_prerelease_parts

  candidate_core="${candidate%%-*}"
  installed_core="${installed%%-*}"
  candidate_prerelease=''
  installed_prerelease=''
  [[ "$candidate" != *-* ]] || candidate_prerelease="${candidate#*-}"
  [[ "$installed" != *-* ]] || installed_prerelease="${installed#*-}"
  IFS=. read -r -a candidate_core_parts <<<"$candidate_core"
  IFS=. read -r -a installed_core_parts <<<"$installed_core"
  for index in 0 1 2; do
    if [[ "${candidate_core_parts[$index]}" != "${installed_core_parts[$index]}" ]]; then
      if decimal_string_is_greater "${candidate_core_parts[$index]}" "${installed_core_parts[$index]}"; then
        return 0
      fi
      return 1
    fi
  done

  [[ -n "$installed_prerelease" ]] || return 1
  [[ -n "$candidate_prerelease" ]] || return 0
  IFS=. read -r -a candidate_prerelease_parts <<<"$candidate_prerelease"
  IFS=. read -r -a installed_prerelease_parts <<<"$installed_prerelease"
  for (( index = 0; ; index++ )); do
    (( index < ${#candidate_prerelease_parts[@]} )) || return 1
    (( index < ${#installed_prerelease_parts[@]} )) || return 0
    candidate_identifier="${candidate_prerelease_parts[$index]}"
    installed_identifier="${installed_prerelease_parts[$index]}"
    [[ "$candidate_identifier" != "$installed_identifier" ]] || continue
    if [[ "$candidate_identifier" =~ ^[0-9]+$ ]]; then
      [[ "$installed_identifier" =~ ^[0-9]+$ ]] || return 1
      if decimal_string_is_greater "$candidate_identifier" "$installed_identifier"; then
        return 0
      fi
      return 1
    fi
    [[ "$installed_identifier" =~ ^[0-9]+$ ]] && return 0
    [[ "$candidate_identifier" > "$installed_identifier" ]]
    return $?
  done
}

verify_installed_tree() {
  local tree_root="$1"
  local label="$2"
  local mount_root="$3"
  local expected_manifest_size="$4"
  local expected_manifest_sha256="$5"
  local paths_name="$6"
  local hashes_name="$7"
  local sizes_name="$8"
  local modes_name="$9"
  local relative path mount_path index metadata actual_owner actual_group actual_mode actual_size actual_links actual_sha
  local tree_device
  local -a actual_files=() actual_directories=()
  local -n expected_paths_ref="$paths_name"
  local -n expected_hashes_ref="$hashes_name"
  local -n expected_sizes_ref="$sizes_name"
  local -n expected_modes_ref="$modes_name"

  [[ -d "$tree_root" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$tree_root")" == '0:0:755' ]] ||
    die "$label root metadata drifted"
  if /usr/bin/findmnt -rn --mountpoint "$mount_root" >/dev/null 2>&1; then
    die "$label root is a mount point"
  fi
  tree_device="$(/usr/bin/stat -Lc '%d' -- "$tree_root")"
  while IFS= read -r -d '' path; do
    relative="${path#"$tree_root"/}"
    validate_safe_relative_path "$relative" || die "$label contains an unsafe path"
    [[ "$(/usr/bin/stat -Lc '%d' -- "$path")" == "$tree_device" ]] ||
      die "$label crosses a filesystem boundary: $relative"
    mount_path="$mount_root/$relative"
    if /usr/bin/findmnt -rn --mountpoint "$mount_path" >/dev/null 2>&1; then
      die "$label contains a descendant mount: $relative"
    fi
    if [[ -d "$path" && ! -L "$path" ]]; then
      actual_directories+=("$relative")
      [[ "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == '0:0:755' ]] ||
        die "$label directory metadata drifted: $relative"
    elif [[ -f "$path" && ! -L "$path" ]]; then
      actual_files+=("$relative")
    else
      die "$label contains a symbolic link or special filesystem node: $relative"
    fi
  done < <(/usr/bin/find -H "$tree_root" -mindepth 1 -print0)

  mapfile -t actual_directories < <(/usr/bin/printf '%s\n' "${actual_directories[@]}" | /usr/bin/sort)
  mapfile -t expected_sorted_directories < <(/usr/bin/printf '%s\n' "${required_directories[@]}" | /usr/bin/sort)
  if [[ "$(/usr/bin/printf '%s\n' "${actual_directories[@]}")" != "$(/usr/bin/printf '%s\n' "${expected_sorted_directories[@]}")" ]]; then
    /usr/bin/diff -u \
      <(/usr/bin/printf '%s\n' "${expected_sorted_directories[@]}") \
      <(/usr/bin/printf '%s\n' "${actual_directories[@]}") >&2 || true
    die "$label directory set differs from the release contract"
  fi

  mapfile -t actual_files < <(/usr/bin/printf '%s\n' "${actual_files[@]}" | /usr/bin/sort)
  mapfile -t expected_sorted_files < <(/usr/bin/printf '%s\n' "${expected_paths_ref[@]}" "$manifest_relative" | /usr/bin/sort)
  if [[ "$(/usr/bin/printf '%s\n' "${actual_files[@]}")" != "$(/usr/bin/printf '%s\n' "${expected_sorted_files[@]}")" ]]; then
    /usr/bin/diff -u \
      <(/usr/bin/printf '%s\n' "${expected_sorted_files[@]}") \
      <(/usr/bin/printf '%s\n' "${actual_files[@]}") >&2 || true
    die "$label file set differs from the manifest-closed release contract"
  fi

  for index in "${!expected_paths_ref[@]}"; do
    relative="${expected_paths_ref[$index]}"
    path="$tree_root/$relative"
    metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$path")" || die "$label payload metadata cannot be read: $relative"
    IFS=: read -r actual_owner actual_group actual_mode actual_size actual_links <<<"$metadata"
    actual_sha="$(file_sha256 "$path")"
    [[ "$actual_owner" == 0 && "$actual_group" == 0 &&
       "0$actual_mode" == "${expected_modes_ref[$index]}" &&
       "$actual_size" == "${expected_sizes_ref[$index]}" &&
       "$actual_links" == 1 && "$actual_sha" == "${expected_hashes_ref[$index]}" ]] ||
      die "$label payload integrity drifted: $relative"
  done
  path="$tree_root/$manifest_relative"
  [[ "$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$path")" == "0:0:644:${expected_manifest_size}:1" &&
     "$(file_sha256 "$path")" == "$expected_manifest_sha256" ]] ||
    die "$label manifest integrity drifted"
}

[[ "$source_root" == /* && ! "$source_root" =~ [[:cntrl:]] &&
   "$source_root" == "$(/usr/bin/realpath -e -- "$source_root" 2>/dev/null || true)" &&
   -d "$source_root" && ! -L "$source_root" ]] || die 'release source must be one canonical absolute real directory'
validate_root_owned_ancestry 'release source' "$source_root"
[[ "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$source_root")" == '0:0:755' ]] ||
  die 'release source root must be root:root mode 0755'
readonly source_identity="$(directory_identity "$source_root")"
exec {source_fd}<"$source_root"
readonly source_fd
readonly source_anchor="/proc/$BASHPID/fd/$source_fd"
[[ "$(directory_identity "$source_anchor")" == "$source_identity" ]] || die 'release source changed while anchoring its directory descriptor'

script_entry="${BASH_SOURCE[0]}"
[[ "$script_entry" == /* && ! "$script_entry" =~ [[:cntrl:]] &&
   "$script_entry" == "$(/usr/bin/realpath -e -- "$script_entry" 2>/dev/null || true)" &&
   -f "$script_entry" && ! -L "$script_entry" ]] ||
  die 'release installer bootstrap must be one canonical absolute regular file'
[[ "$script_entry" != "$source_root" && "$script_entry" != "$source_root/"* ]] ||
  die 'release installer bootstrap must be external to the untrusted release payload'
script_parent="${script_entry%/*}"
[[ -n "$script_parent" ]] || script_parent=/
validate_root_owned_ancestry 'release installer bootstrap' "$script_parent"
[[ "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$script_entry")" == '0:0:755:1' ]] ||
  die 'release installer bootstrap must be root:root mode 0755 with one link'
readonly bootstrap_identity="$(/usr/bin/stat -Lc '%d:%i' -- "$script_entry")"
exec {bootstrap_fd}<"$script_entry"
readonly bootstrap_fd
readonly bootstrap_anchor="/proc/$BASHPID/fd/$bootstrap_fd"
[[ "$(/usr/bin/stat -Lc '%d:%i:%u:%g:%a:%h' -- "$bootstrap_anchor")" == "$bootstrap_identity:0:0:755:1" ]] ||
  die 'release installer bootstrap identity changed while anchoring its file descriptor'
unset script_parent

validate_root_owned_ancestry '/opt' '/opt'
[[ "$(/usr/bin/stat -Lc '%u:%g:%a' -- /opt)" == '0:0:755' ]] || die '/opt must be root:root mode 0755'
if [[ ! -e "$install_parent" && ! -L "$install_parent" ]]; then
  /usr/bin/mkdir -m 0755 -- "$install_parent" || die 'failed to create /opt/ascendany'
  /usr/bin/sync -- /opt
fi
validate_root_owned_ancestry 'installation parent' "$install_parent"
[[ "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$install_parent")" == '0:0:755' ]] ||
  die 'installation parent must be root:root mode 0755'
readonly install_parent_identity="$(directory_identity "$install_parent")"
exec {install_parent_fd}<"$install_parent"
readonly install_parent_fd
readonly install_parent_anchor="/proc/$BASHPID/fd/$install_parent_fd"
[[ "$(directory_identity "$install_parent_anchor")" == "$install_parent_identity" ]] ||
  die 'installation parent changed while anchoring its directory descriptor'

if [[ ! -e "$lock_path" && ! -L "$lock_path" ]]; then
  (set -o noclobber; : >"$lock_path") 2>/dev/null || true
fi
[[ -f "$lock_path" && ! -L "$lock_path" &&
   "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$lock_path")" == '0:0:600:1' ]] ||
  die 'installation lock must be one root:root mode 0600 regular file'
readonly lock_identity="$(/usr/bin/stat -Lc '%d:%i' -- "$lock_path")"
exec {lock_fd}<>"$lock_path"
readonly lock_fd
/usr/bin/flock --exclusive --nonblock "$lock_fd" || die 'another release installer holds the installation lock'
readonly lock_anchor="/proc/$BASHPID/fd/$lock_fd"
[[ "$(/usr/bin/stat -Lc '%d:%i:%u:%g:%a:%h' -- "$lock_path")" == "$lock_identity:0:0:600:1" &&
   "$(/usr/bin/stat -Lc '%d:%i:%u:%g:%a:%h' -- "$lock_anchor")" == "$lock_identity:0:0:600:1" ]] ||
  die 'installation lock identity changed while acquiring it'

assert_install_parent_identity 'target operation check'
if [[ "$release_operation" == promote ]]; then
  [[ ! -e "$install_parent_anchor/v2" && ! -L "$install_parent_anchor/v2" ]] ||
    die 'canonical release target already exists'
else
  [[ -d "$install_parent_anchor/v2" && ! -L "$install_parent_anchor/v2" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$install_parent_anchor/v2")" == '0:0:755' ]] ||
    die 'installed release target must be one root:root mode 0755 real directory'
  [[ "$(directory_identity "$install_parent_anchor/v2")" == "$installed_identity" ]] ||
    die 'installed release identity differs from the explicit trust input'
  [[ "$installed_device" == "${install_parent_identity%%:*}" ]] ||
    die 'installed release and installation parent are on different filesystems'
  exec {installed_fd}<"$install_parent_anchor/v2"
  readonly installed_fd
  readonly installed_anchor="/proc/$BASHPID/fd/$installed_fd"
  assert_installed_identity 'installed release trust verification'

  readonly installed_manifest="$installed_anchor/$manifest_relative"
  [[ -f "$installed_manifest" && ! -L "$installed_manifest" ]] ||
    die 'installed release manifest is missing or non-regular'
  installed_manifest_metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$installed_manifest")"
  IFS=: read -r installed_manifest_owner installed_manifest_group installed_manifest_mode installed_manifest_size installed_manifest_links <<<"$installed_manifest_metadata"
  [[ "$installed_manifest_owner" == 0 && "$installed_manifest_group" == 0 &&
     "$installed_manifest_mode" == 644 && "$installed_manifest_links" == 1 &&
     "$installed_manifest_size" =~ ^[1-9][0-9]*$ && "$installed_manifest_size" -le "$max_manifest_size" ]] ||
    die 'installed release manifest must be a bounded root:root mode 0644 single-link regular file'
  readonly installed_manifest_size
  readonly installed_manifest_sha256="$(file_sha256 "$installed_manifest")"
  [[ "$installed_manifest_sha256" == "$expected_installed_manifest_sha256" ]] ||
    die 'installed release manifest digest differs from the explicit trust input'

  installed_manifest_version=''
  validate_manifest_contract "$installed_manifest" 'installed release manifest' installed_manifest_version
  readonly installed_manifest_version
  declare -a installed_manifest_paths=() installed_manifest_hashes=() installed_manifest_sizes=() installed_manifest_modes=()
  load_manifest_entries \
    "$installed_manifest" \
    'installed release manifest' \
    installed_manifest_paths \
    installed_manifest_hashes \
    installed_manifest_sizes \
    installed_manifest_modes
  readonly -a installed_manifest_paths installed_manifest_hashes installed_manifest_sizes installed_manifest_modes
  verify_installed_tree \
    "$installed_anchor" \
    'installed release' \
    "$install_target" \
    "$installed_manifest_size" \
    "$installed_manifest_sha256" \
    installed_manifest_paths \
    installed_manifest_hashes \
    installed_manifest_sizes \
    installed_manifest_modes
  assert_installed_identity 'installed release tree verification'
  index_release_tree_objects "$installed_anchor"
  assert_release_consumers_quiesced 'replacement preflight'
fi
declare -a incomplete_private_entries=()
while IFS= read -r -d '' incomplete_private_entry; do
  incomplete_private_entries+=("${incomplete_private_entry##*/}")
done < <(
  /usr/bin/find -H "$install_parent_anchor" -mindepth 1 -maxdepth 1 \
    \( -name '.v2.installing.*' -o -name '.v2.removing.*' \) -print0
)
(( ${#incomplete_private_entries[@]} == 0 )) ||
  die "pre-existing incomplete release private entries require explicit operator resolution: ${incomplete_private_entries[*]}"
unset incomplete_private_entry incomplete_private_entries

stage_root="$(/usr/bin/mktemp -d "$install_parent_anchor/.v2.installing.XXXXXXXXXX")" || die 'failed to create private installation staging directory'
readonly stage_name="${stage_root##*/}"
readonly removal_name=".v2.removing.${stage_name#.v2.installing.}"
release_state=staged
[[ "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$stage_root")" == '0:0:700' ]] || die 'private installation staging metadata is invalid'
[[ "$(/usr/bin/stat -Lc '%d' -- "$stage_root")" == "$(/usr/bin/stat -Lc '%d' -- "$install_parent_anchor")" ]] ||
  die 'installation staging and canonical target parent are on different filesystems'

source_manifest="$source_anchor/$manifest_relative"
[[ -f "$source_manifest" && ! -L "$source_manifest" ]] || die 'release manifest is missing or non-regular'
source_manifest_metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$source_manifest")"
IFS=: read -r manifest_owner manifest_group manifest_mode source_manifest_size manifest_links <<<"$source_manifest_metadata"
[[ "$manifest_owner" == 0 && "$manifest_group" == 0 && "$manifest_mode" == 644 && "$manifest_links" == 1 &&
   "$source_manifest_size" =~ ^[1-9][0-9]*$ && "$source_manifest_size" -le "$max_manifest_size" ]] ||
  die 'release manifest must be a bounded root:root mode 0644 single-link regular file'
captured_manifest="$stage_root/.captured-release-manifest.json"
copy_bounded_regular_file "$source_manifest" "$captured_manifest" "$source_manifest_size"
/usr/bin/chmod 0600 -- "$captured_manifest"
readonly captured_manifest_size="$(/usr/bin/stat -Lc '%s' -- "$captured_manifest")"
readonly captured_manifest_sha256="$(file_sha256 "$captured_manifest")"
[[ "$captured_manifest_sha256" == "$expected_manifest_sha256" ]] ||
  die 'release manifest digest differs from the external trust anchor'

manifest_version=''
validate_manifest_contract "$captured_manifest" 'release manifest' manifest_version
readonly manifest_version
declare -a manifest_paths=() manifest_hashes=() manifest_sizes=() manifest_modes=()
load_manifest_entries \
  "$captured_manifest" \
  'release manifest' \
  manifest_paths \
  manifest_hashes \
  manifest_sizes \
  manifest_modes
readonly -a manifest_paths manifest_hashes manifest_sizes manifest_modes
if [[ "$release_operation" == replace ]]; then
  [[ "$captured_manifest_sha256" != "$installed_manifest_sha256" ]] ||
    die 'replacement release manifest equals the installed release manifest'
  semver_is_strictly_greater "$manifest_version" "$installed_manifest_version" ||
    die "replacement release version $manifest_version does not advance installed version $installed_manifest_version"
fi

declare -a actual_source_files=() actual_source_directories=()
source_device="${source_identity%%:*}"
while IFS= read -r -d '' source_path; do
  relative="${source_path#"$source_anchor"/}"
  validate_safe_relative_path "$relative" || die 'release source contains an unsafe path'
  if [[ -L "$source_path" || ( ! -d "$source_path" && ! -f "$source_path" ) ]]; then
    die "release source contains a symbolic link or special filesystem node: $relative"
  fi
  [[ "$(/usr/bin/stat -Lc '%u:%g' -- "$source_path")" == '0:0' ]] ||
    die "release source entry must be owned by root:root: $relative"
  [[ "$(/usr/bin/stat -Lc '%d' -- "$source_path")" == "$source_device" ]] || die "release source crosses a filesystem boundary: $relative"
  if /usr/bin/findmnt -rn --mountpoint "$source_root/$relative" >/dev/null 2>&1; then
    die "release source contains a descendant mount: $relative"
  fi
  if [[ -d "$source_path" ]]; then
    actual_source_directories+=("$relative")
    [[ "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$source_path")" == '0:0:755' ]] ||
      die "release source directory metadata is invalid: $relative"
  elif [[ -f "$source_path" ]]; then
    actual_source_files+=("$relative")
  fi
done < <(/usr/bin/find -H "$source_anchor" -mindepth 1 -print0)
mapfile -t actual_source_directories < <(/usr/bin/printf '%s\n' "${actual_source_directories[@]}" | /usr/bin/sort)
mapfile -t expected_sorted_directories < <(/usr/bin/printf '%s\n' "${required_directories[@]}" | /usr/bin/sort)
if [[ "$(/usr/bin/printf '%s\n' "${actual_source_directories[@]}")" != "$(/usr/bin/printf '%s\n' "${expected_sorted_directories[@]}")" ]]; then
  /usr/bin/diff -u \
    <(/usr/bin/printf '%s\n' "${expected_sorted_directories[@]}") \
    <(/usr/bin/printf '%s\n' "${actual_source_directories[@]}") >&2 || true
  die 'release source directory set differs from the release contract'
fi
mapfile -t actual_source_files < <(/usr/bin/printf '%s\n' "${actual_source_files[@]}" | /usr/bin/sort)
mapfile -t expected_sorted_files < <(/usr/bin/printf '%s\n' "${required_paths[@]}" "$manifest_relative" | /usr/bin/sort)
if [[ "$(/usr/bin/printf '%s\n' "${actual_source_files[@]}")" != "$(/usr/bin/printf '%s\n' "${expected_sorted_files[@]}")" ]]; then
  /usr/bin/diff -u \
    <(/usr/bin/printf '%s\n' "${expected_sorted_files[@]}") \
    <(/usr/bin/printf '%s\n' "${actual_source_files[@]}") >&2 || true
  die 'release source file set differs from the manifest-closed release contract'
fi

for index in "${!required_paths[@]}"; do
  relative="${required_paths[$index]}"
  source_path="$source_anchor/$relative"
  metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$source_path")" || die "release source payload metadata cannot be read: $relative"
  IFS=: read -r actual_owner actual_group actual_mode actual_size actual_links <<<"$metadata"
  [[ "$actual_owner" == 0 && "$actual_group" == 0 && "0$actual_mode" == "${manifest_modes[$index]}" &&
     "$actual_size" == "${manifest_sizes[$index]}" && "$actual_links" == 1 &&
     "$(file_sha256 "$source_path")" == "${manifest_hashes[$index]}" ]] ||
    die "release source payload integrity differs from its manifest: $relative"
done
assert_source_identity 'payload capture'
[[ "$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$source_manifest")" == "0:0:644:${captured_manifest_size}:1" &&
   "$(file_sha256 "$source_manifest")" == "$captured_manifest_sha256" ]] ||
  die 'release source manifest changed after capture'

for relative in "${required_directories[@]}"; do
  /usr/bin/mkdir -m 0755 -- "$stage_root/$relative" || die "failed to create staged release directory: $relative"
done
for index in "${!required_paths[@]}"; do
  relative="${required_paths[$index]}"
  destination_path="$stage_root/$relative"
  copy_bounded_regular_file "$source_anchor/$relative" "$destination_path" "${manifest_sizes[$index]}"
  /usr/bin/chmod "${manifest_modes[$index]}" -- "$destination_path"
  [[ "$(file_sha256 "$destination_path")" == "${manifest_hashes[$index]}" ]] ||
    die "captured payload digest differs from its manifest: $relative"
done
/usr/bin/mv --no-target-directory -- "$captured_manifest" "$stage_root/$manifest_relative"
/usr/bin/chmod 0644 -- "$stage_root/$manifest_relative"

assert_source_identity 'publication eligibility check'
[[ "$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$source_manifest")" == "0:0:644:${captured_manifest_size}:1" &&
   "$(file_sha256 "$source_manifest")" == "$captured_manifest_sha256" ]] ||
  die 'release source manifest changed before publication'
assert_install_parent_identity 'staged tree verification'
/usr/bin/chmod 0755 -- "$stage_root"
verify_installed_tree \
  "$stage_root" \
  'staged release' \
  "$install_parent/$stage_name" \
  "$captured_manifest_size" \
  "$captured_manifest_sha256" \
  manifest_paths \
  manifest_hashes \
  manifest_sizes \
  manifest_modes
model_index=-1
catalog_index=-1
for index in "${!required_paths[@]}"; do
  if [[ "${required_paths[$index]}" == 'models/recommendation-model.json' ]]; then
    model_index="$index"
  elif [[ "${required_paths[$index]}" == 'models/recommendation-knowledge-catalog.json' ]]; then
    catalog_index="$index"
  fi
done
(( model_index >= 0 )) || die 'recommendation model is absent from the release contract'
(( catalog_index >= 0 )) || die 'knowledge catalog is absent from the release contract'
if ! /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
    "$stage_root/bin/ascendany-model" verify-catalog \
      --catalog "$stage_root/models/recommendation-knowledge-catalog.json" \
      --catalog-sha256 "${manifest_hashes[$catalog_index]}" \
      --model "$stage_root/models/recommendation-model.json" \
      --model-sha256 "${manifest_hashes[$model_index]}" \
      --expected-purpose "$expected_release_purpose"; then
  die 'manifest-bound recommendation model/catalog pair failed semantic verification'
fi
for release_config in "$stage_root/config/ascendanyd.env" "$stage_root/config/catalog-publish.env"; do
  [[ "$(/usr/bin/grep -Fxc "ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=$expected_release_purpose" "$release_config")" == 1 &&
     "$(/usr/bin/grep -Ec '^ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=' "$release_config")" == 1 &&
     "$(/usr/bin/grep -Fxc "ASCENDANY_RECOMMENDATION_MODEL_SHA256=${manifest_hashes[$model_index]}" "$release_config")" == 1 &&
     "$(/usr/bin/grep -Ec '^ASCENDANY_RECOMMENDATION_MODEL_SHA256=' "$release_config")" == 1 &&
     "$(/usr/bin/grep -Fxc "ASCENDANY_KNOWLEDGE_CATALOG_SHA256=${manifest_hashes[$catalog_index]}" "$release_config")" == 1 &&
     "$(/usr/bin/grep -Ec '^ASCENDANY_KNOWLEDGE_CATALOG_SHA256=' "$release_config")" == 1 ]] ||
    die "release model/catalog configuration differs from the manifest: $release_config"
done
unset release_config

while IFS= read -r -d '' staged_file; do
  /usr/bin/sync -- "$staged_file"
done < <(/usr/bin/find "$stage_root" -type f -print0)
while IFS= read -r -d '' staged_directory; do
  /usr/bin/sync -- "$staged_directory"
done < <(/usr/bin/find "$stage_root" -depth -type d -print0)
if [[ "$release_operation" == replace ]]; then
  assert_installed_identity 'atomic replacement'
  verify_installed_tree \
    "$installed_anchor" \
    'installed release before replacement' \
    "$install_target" \
    "$installed_manifest_size" \
    "$installed_manifest_sha256" \
    installed_manifest_paths \
    installed_manifest_hashes \
    installed_manifest_sizes \
    installed_manifest_modes
  assert_release_consumers_quiesced 'atomic replacement'
else
  assert_install_parent_identity 'atomic promotion'
  [[ ! -e "$install_parent_anchor/v2" && ! -L "$install_parent_anchor/v2" ]] ||
    die 'canonical release target appeared before atomic promotion'
fi
readonly staged_identity="$(directory_identity "$stage_root")"
IFS=: read -r staged_device staged_inode <<<"$staged_identity"
release_ops_index=-1
for index in "${!required_paths[@]}"; do
  if [[ "${required_paths[$index]}" == 'bin/ascendany-release-ops' ]]; then
    release_ops_index="$index"
    break
  fi
done
(( release_ops_index >= 0 )) || die 'native atomic release helper is absent from the release contract'
native_helper="$stage_root/bin/ascendany-release-ops"
exec {native_helper_fd}<"$native_helper"
readonly native_helper_fd
readonly native_helper_anchor="/proc/$BASHPID/fd/$native_helper_fd"
[[ "$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$native_helper_anchor")" == "0:0:755:${manifest_sizes[$release_ops_index]}:1" &&
   "$(file_sha256 "$native_helper_anchor")" == "${manifest_hashes[$release_ops_index]}" ]] ||
  die 'native atomic release helper differs from the externally anchored manifest'
set +e
if [[ "$release_operation" == replace ]]; then
  "$native_helper_anchor" replace \
    --parent-fd "$install_parent_fd" \
    --stage-name "$stage_name" \
    --expected-device "$staged_device" \
    --expected-inode "$staged_inode" \
    --expected-installed-device "$installed_device" \
    --expected-installed-inode "$installed_inode"
else
  "$native_helper_anchor" promote \
    --parent-fd "$install_parent_fd" \
    --stage-name "$stage_name" \
    --expected-device "$staged_device" \
    --expected-inode "$staged_inode"
fi
native_helper_status=$?
set -e
published_target_identity=''
if (( native_helper_status == 0 || native_helper_status == 3 )); then
  release_state=committed
fi
if [[ -d "$install_parent_anchor/v2" && ! -L "$install_parent_anchor/v2" &&
      "$(directory_identity "$install_parent_anchor/v2")" == "$staged_identity" ]]; then
  published_target_identity="$staged_identity"
fi
if (( native_helper_status != 0 )); then
  if [[ "$release_operation" == replace ]]; then
    die 'native same-filesystem RENAME_EXCHANGE replacement failed'
  fi
  die 'native same-filesystem RENAME_NOREPLACE promotion failed'
fi
if [[ "$release_operation" == replace ]]; then
  [[ -d "$stage_root" && ! -L "$stage_root" &&
     "$(directory_identity "$stage_root")" == "$installed_identity" &&
     "$(directory_identity "$installed_anchor")" == "$installed_identity" ]] ||
    die 'native atomic helper did not retain the trusted installed tree at the staging name'
else
  [[ ! -e "$stage_root" && ! -L "$stage_root" ]] ||
    die 'native atomic helper returned without moving the staged release'
fi
[[ "$release_state" == committed && "$published_target_identity" == "$staged_identity" ]] ||
  die 'native atomic helper did not expose the verified stage at the canonical target'

assert_install_parent_identity 'post-promotion verification'
[[ -d "$install_parent_anchor/v2" && ! -L "$install_parent_anchor/v2" &&
   "$(directory_identity "$install_parent_anchor/v2")" == "$published_target_identity" &&
   -d "$install_target" && ! -L "$install_target" &&
   "$(directory_identity "$install_target")" == "$published_target_identity" ]] ||
  die 'promoted canonical release identity differs from verified staging'
verify_installed_tree \
  "$install_parent_anchor/v2" \
  'promoted release' \
  "$install_target" \
  "$captured_manifest_size" \
  "$captured_manifest_sha256" \
  manifest_paths \
  manifest_hashes \
  manifest_sizes \
  manifest_modes
if [[ "$release_operation" == replace ]]; then
  [[ -d "$install_parent_anchor/$stage_name" && ! -L "$install_parent_anchor/$stage_name" &&
     "$(directory_identity "$install_parent_anchor/$stage_name")" == "$installed_identity" &&
     "$(directory_identity "$installed_anchor")" == "$installed_identity" ]] ||
    die 'retired installed tree identity differs after atomic replacement'
  verify_installed_tree \
    "$installed_anchor" \
    'retired installed release' \
    "$install_parent/$stage_name" \
    "$installed_manifest_size" \
    "$installed_manifest_sha256" \
    installed_manifest_paths \
    installed_manifest_hashes \
    installed_manifest_sizes \
    installed_manifest_modes
fi
/usr/bin/sync -- "$install_parent_anchor/v2"
/usr/bin/sync -- "$install_parent_anchor"
assert_install_parent_identity 'durable publication verification'
[[ "$(directory_identity "$install_target")" == "$published_target_identity" ]] ||
  die 'canonical release identity changed after durable publication'
verify_installed_tree \
  "$install_target" \
  'durable release' \
  "$install_target" \
  "$captured_manifest_size" \
  "$captured_manifest_sha256" \
  manifest_paths \
  manifest_hashes \
  manifest_sizes \
  manifest_modes
if [[ "$release_operation" == replace ]]; then
  verify_installed_tree \
    "$installed_anchor" \
    'retired installed release before removal' \
    "$install_parent/$stage_name" \
    "$installed_manifest_size" \
    "$installed_manifest_sha256" \
    installed_manifest_paths \
    installed_manifest_hashes \
    installed_manifest_sizes \
    installed_manifest_modes
  index_release_tree_objects "$install_parent_anchor/v2"
  assert_release_consumers_quiesced 'retired release removal'
  "$native_helper_anchor" remove-retired \
    --parent-fd "$install_parent_fd" \
    --stage-name "$stage_name" \
    --remove-name "$removal_name" \
    --expected-device "$installed_device" \
    --expected-inode "$installed_inode" \
    --expected-target-device "$staged_device" \
    --expected-target-inode "$staged_inode" ||
    die 'native identity-bound retired-tree removal failed'
  [[ ! -e "$install_parent_anchor/$stage_name" && ! -L "$install_parent_anchor/$stage_name" ]] ||
    die 'retired installed release tree remains after removal'
  [[ ! -e "$install_parent_anchor/$removal_name" && ! -L "$install_parent_anchor/$removal_name" ]] ||
    die 'retired installed release removal tombstone remains after removal'
  [[ "$(directory_identity "$install_target")" == "$published_target_identity" ]] ||
    die 'canonical release identity changed while removing the retired release'
  verify_installed_tree \
    "$install_target" \
    'replacement release after retired-tree removal' \
    "$install_target" \
    "$captured_manifest_size" \
    "$captured_manifest_sha256" \
    manifest_paths \
    manifest_hashes \
    manifest_sizes \
    manifest_modes
fi
release_state=verified

/usr/bin/printf '%s\n' "$install_target"
