#!/usr/bin/bash -p
if [[ "${BASH:-}" != "/usr/bin/bash" ||
      "$-" != *p* ||
      "$-" == *[cis]* ||
      -n "${BASH_EXECUTION_STRING:-}" ||
      "${#BASH_SOURCE[@]}" -ne 1 ||
      "${BASH_SOURCE[0]}" != "$0" ]]; then
  /usr/bin/printf '%s\n' 'release builder must run directly under /usr/bin/bash -p' >&2
  /usr/bin/kill -KILL "${BASHPID}"
fi
declare -a environment_removals=()
while IFS= read -r -d '' environment_entry; do
  environment_name="${environment_entry%%=*}"
  case "$environment_name" in
    BASH_FUNC_*%%|GO*) environment_removals+=( -u "$environment_name" ) ;;
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

usage() {
  printf 'usage: %s --version SEMVER --commit 40_HEX --source-date-epoch SECONDS --go-path /canonical/go --go-version GOVERSION --goos linux --goarch amd64 --goamd64 v1 --release-purpose production|acceptance_test --recommendation-model /protected/absolute/canonical.json --recommendation-model-sha256 64_HEX --knowledge-catalog /protected/absolute/canonical.json --knowledge-catalog-sha256 64_HEX --output /absolute/path\n' "$0" >&2
}

validate_protected_directory_ancestry() {
  local label="$1"
  local directory="$2"
  local current=/
  local component owner mode_text mode
  local -a components=()

  if [[ "$directory" != /* || "$directory" =~ [[:cntrl:]] ]]; then
    printf '%s must use one absolute path without control characters\n' "$label" >&2
    exit 2
  fi
  IFS=/ read -r -a components <<<"${directory#/}"
  for component in '' "${components[@]}"; do
    if [[ -n "$component" ]]; then
      current="${current%/}/$component"
    fi
    if [[ ! -d "$current" || -L "$current" ]]; then
      printf '%s has a missing, non-directory, or symlink ancestor: %s\n' "$label" "$current" >&2
      exit 2
    fi
    owner="$(/usr/bin/stat -c '%u' -- "$current")"
    mode_text="$(/usr/bin/stat -c '%a' -- "$current")"
    mode="$((8#$mode_text))"
    if [[ "$owner" != 0 && "$owner" != "$EUID" ]]; then
      printf '%s has an ancestor outside the root/release-user ownership boundary: %s\n' "$label" "$current" >&2
      exit 2
    fi
    if (( (mode & 8#022) != 0 && (owner != 0 || (mode & 8#1000) == 0) )); then
      printf '%s has an unprotected writable ancestor: %s\n' "$label" "$current" >&2
      exit 2
    fi
  done
}

validate_protected_file_ancestry() {
  local label="$1"
  local file="$2"
  validate_protected_directory_ancestry "$label" "$(/usr/bin/dirname -- "$file")"
}

version=""
requested_commit=""
source_date_epoch=""
go_binary=""
go_version=""
goos=""
goarch=""
goamd64=""
release_purpose=""
recommendation_model=""
recommendation_model_sha256=""
knowledge_catalog=""
knowledge_catalog_sha256=""
output=""
release_home="${HOME:-}"

script_source="${BASH_SOURCE[0]}"
if [[ "$script_source" != /* ]]; then
  script_source="$PWD/$script_source"
fi
builder_path="$script_source"
script_root="$(cd -- "${script_source%/*}" && pwd -P)"
repository_root="$(cd -- "$script_root/.." && pwd -P)"
readonly esbuild_version='0.25.12'
readonly esbuild_binary="$repository_root/node_modules/.pnpm/esbuild@${esbuild_version}/node_modules/esbuild/bin/esbuild"
unset script_source

validate_go_binary() {
  local mode owner

  if [[ ! "$go_binary" =~ ^/[0-9A-Za-z_./:+-]+$ ||
        ! -f "$go_binary" || -L "$go_binary" || ! -x "$go_binary" ]]; then
    printf 'Go tool must name an explicit canonical executable regular file\n' >&2
    exit 2
  fi
  if [[ "$go_binary" != "$(/usr/bin/realpath -e -- "$go_binary")" ]]; then
    printf 'Go tool path must not contain symlink ancestry\n' >&2
    exit 2
  fi
  owner="$(/usr/bin/stat -Lc '%u' -- "$go_binary")"
  if [[ "$owner" != 0 && "$owner" != "$EUID" ]]; then
    printf 'Go tool must be owned by root or the release user\n' >&2
    exit 2
  fi
  mode="$((8#$(/usr/bin/stat -Lc '%a' -- "$go_binary")))"
  if (( (mode & 8#022) != 0 )); then
    printf 'Go tool must not be group- or other-writable\n' >&2
    exit 2
  fi
  validate_protected_file_ancestry 'Go tool path' "$go_binary"
}

validate_esbuild_binary() {
  local owner mode actual_version

  if [[ ! -f "$esbuild_binary" || -L "$esbuild_binary" || ! -x "$esbuild_binary" ||
        "$esbuild_binary" != "$(/usr/bin/realpath -e -- "$esbuild_binary")" ]]; then
    printf 'workspace esbuild must be the explicit canonical pinned executable regular file\n' >&2
    exit 2
  fi
  owner="$(/usr/bin/stat -Lc '%u' -- "$esbuild_binary")"
  mode="$((8#$(/usr/bin/stat -Lc '%a' -- "$esbuild_binary")))"
  if [[ "$owner" != 0 && "$owner" != "$EUID" ]] || (( (mode & 8#022) != 0 )); then
    printf 'workspace esbuild must be root/release-user owned and immutable to group/other\n' >&2
    exit 2
  fi
  validate_protected_file_ancestry 'workspace esbuild path' "$esbuild_binary"
  actual_version="$(
    /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C "$esbuild_binary" --version
  )" || {
    printf 'workspace esbuild version cannot be read\n' >&2
    exit 2
  }
  if [[ "$actual_version" != "$esbuild_version" ]]; then
    printf 'workspace esbuild is %s; the release requires %s\n' "$actual_version" "$esbuild_version" >&2
    exit 2
  fi
}

validate_recommendation_model_source() {
  local metadata owner mode_text mode size links actual_sha

  if [[ ! "$recommendation_model" =~ ^/[0-9A-Za-z_./:+-]+$ ||
        ! -f "$recommendation_model" || -L "$recommendation_model" ]]; then
    printf 'recommendation model must name an explicit absolute regular file\n' >&2
    exit 2
  fi
  if [[ "$recommendation_model" != "$(/usr/bin/realpath -e -- "$recommendation_model")" ]]; then
    printf 'recommendation model path must be canonical and have no symlink ancestry\n' >&2
    exit 2
  fi
  metadata="$(/usr/bin/stat -Lc '%u:%a:%s:%h' -- "$recommendation_model")"
  IFS=: read -r owner mode_text size links <<<"$metadata"
  mode="$((8#$mode_text))"
  if [[ "$owner" != 0 && "$owner" != "$EUID" ]] || (( (mode & 8#022) != 0 )); then
    printf 'recommendation model must be root/release-user owned and immutable to group/other\n' >&2
    exit 2
  fi
  if [[ "$links" != 1 || ! "$size" =~ ^[1-9][0-9]*$ || "$size" -gt 16777216 ]]; then
    printf 'recommendation model must be one single-link file between 1 and 16777216 bytes\n' >&2
    exit 2
  fi
  validate_protected_file_ancestry 'recommendation model path' "$recommendation_model"
  actual_sha="$(/usr/bin/sha256sum -- "$recommendation_model" | /usr/bin/awk '{print $1}')"
  if [[ "$actual_sha" != "$recommendation_model_sha256" ]]; then
    printf 'recommendation model digest differs from --recommendation-model-sha256\n' >&2
    exit 2
  fi
}

validate_knowledge_catalog_source() {
  local metadata owner mode_text mode size links actual_sha

  if [[ ! "$knowledge_catalog" =~ ^/[0-9A-Za-z_./:+-]+$ ||
        ! -f "$knowledge_catalog" || -L "$knowledge_catalog" ]]; then
    printf 'knowledge catalog must name an explicit absolute regular file\n' >&2
    exit 2
  fi
  if [[ "$knowledge_catalog" != "$(/usr/bin/realpath -e -- "$knowledge_catalog")" ]]; then
    printf 'knowledge catalog path must be canonical and have no symlink ancestry\n' >&2
    exit 2
  fi
  metadata="$(/usr/bin/stat -Lc '%u:%a:%s:%h' -- "$knowledge_catalog")"
  IFS=: read -r owner mode_text size links <<<"$metadata"
  mode="$((8#$mode_text))"
  if [[ "$owner" != 0 && "$owner" != "$EUID" ]] || (( (mode & 8#022) != 0 )); then
    printf 'knowledge catalog must be root/release-user owned and immutable to group/other\n' >&2
    exit 2
  fi
  if [[ "$links" != 1 || ! "$size" =~ ^[1-9][0-9]*$ || "$size" -gt 16777216 ]]; then
    printf 'knowledge catalog must be one single-link file between 1 and 16777216 bytes\n' >&2
    exit 2
  fi
  validate_protected_file_ancestry 'knowledge catalog path' "$knowledge_catalog"
  actual_sha="$(/usr/bin/sha256sum -- "$knowledge_catalog" | /usr/bin/awk '{print $1}')"
  if [[ "$actual_sha" != "$knowledge_catalog_sha256" ]]; then
    printf 'knowledge catalog digest differs from --knowledge-catalog-sha256\n' >&2
    exit 2
  fi
}

run_repository_git() {
  env -i \
    PATH="$PATH" \
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
      -C "$repository_root" \
      "$@"
}

run_provenance_git() {
  env -i \
    PATH="$PATH" \
    LC_ALL=C \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_NO_REPLACE_OBJECTS=1 \
    GIT_NO_LAZY_FETCH=1 \
    GIT_TERMINAL_PROMPT=0 \
    GIT_DIR="$provenance_git_dir" \
    GIT_INDEX_FILE="$provenance_index" \
    /usr/bin/git \
      -c core.attributesFile=/dev/null \
      -c core.hooksPath=/dev/null \
      "$@"
}

verify_running_builder() {
  local fixed_path='tools/build-v2-release.sh'
  local expected_path="$repository_root/$fixed_path"
  local reviewed_path="$source_root/$fixed_path"
  local owner live_mode reviewed_mode

  if [[ "$builder_path" != "$expected_path" ||
        ! -f "$builder_path" || -L "$builder_path" ||
        "$builder_path" != "$(/usr/bin/realpath -e -- "$builder_path")" ]]; then
    printf 'release builder must be the canonical repository tools/build-v2-release.sh file\n' >&2
    exit 1
  fi
  owner="$(/usr/bin/stat -Lc '%u' -- "$builder_path")"
  live_mode="$(/usr/bin/stat -Lc '%a' -- "$builder_path")"
  if [[ "$owner" != 0 && "$owner" != "$EUID" ]] || [[ "$live_mode" != 755 ]]; then
    printf 'release builder must be root/release-user owned mode 0755\n' >&2
    exit 1
  fi
  validate_protected_file_ancestry 'release builder' "$builder_path"

  if [[ ! -f "$reviewed_path" || -L "$reviewed_path" ]]; then
    printf 'reviewed commit release builder must be exactly one mode 100755 blob at the fixed path\n' >&2
    exit 1
  fi
  reviewed_mode="$(/usr/bin/stat -Lc '%a' -- "$reviewed_path")"
  if [[ "$reviewed_mode" != 755 ]]; then
    printf 'reviewed commit release builder must be exactly one mode 100755 blob at the fixed path\n' >&2
    exit 1
  fi
  if ! /usr/bin/cmp -s -- "$builder_path" "$reviewed_path"; then
    printf 'running release builder bytes differ from the reviewed commit\n' >&2
    exit 1
  fi
}

validate_reviewed_path() {
  local relative="$1"
  local remaining component

  if [[ -z "$relative" || "$relative" == /* || "$relative" == */ ||
        "$relative" == *//* ]]; then
    return 1
  fi
  remaining="$relative"
  while :; do
    component="${remaining%%/*}"
    if [[ -z "$component" || "$component" == . || "$component" == .. ||
          "${component,,}" == .git ]]; then
      return 1
    fi
    if [[ "$remaining" != */* ]]; then
      break
    fi
    remaining="${remaining#*/}"
  done
}

materialize_reviewed_commit() {
  local destination="$1"
  local listing="$2"
  local index_information="$3"
  local record metadata mode type object_id relative parent file_mode extra
  local materialized_object_id reconstructed_tree
  local entry_count=0

  if find "$destination" -mindepth 1 -print -quit | grep -q .; then
    printf 'detached release source destination is not empty\n' >&2
    exit 1
  fi
  run_repository_git ls-tree -rz --full-tree "$root_tree" >"$listing" || {
    printf 'reviewed commit tree could not be enumerated\n' >&2
    exit 1
  }
  : >"$index_information"
  chmod 0600 "$index_information"

  while IFS= read -r -d '' record; do
    if [[ "$record" != *$'\t'* ]]; then
      printf 'reviewed commit contains an invalid tree record\n' >&2
      exit 1
    fi
    metadata="${record%%$'\t'*}"
    relative="${record#*$'\t'}"
    IFS=' ' read -r mode type object_id extra <<<"$metadata"
    if [[ -n "${extra:-}" || "$type" != blob ||
          ! "$object_id" =~ ^[0-9a-f]{40}$ ||
          ( "$mode" != 100644 && "$mode" != 100755 ) ]]; then
      printf 'reviewed commit contains a non-regular or invalid tree entry: %s\n' "$relative" >&2
      exit 1
    fi
    if ! validate_reviewed_path "$relative"; then
      printf 'reviewed commit contains an unsafe path\n' >&2
      exit 1
    fi
    parent="${relative%/*}"
    [[ "$parent" != "$relative" ]] || parent=.
    install -d -m 0700 -- "$destination/$parent"
    if [[ -e "$destination/$relative" || -L "$destination/$relative" ]]; then
      printf 'reviewed commit path collides during materialization: %s\n' "$relative" >&2
      exit 1
    fi
    if ! run_repository_git cat-file blob "$object_id" >"$destination/$relative"; then
      printf 'reviewed commit blob failed integrity verification during materialization: %s\n' "$relative" >&2
      exit 1
    fi
    materialized_object_id="$(
      run_provenance_git hash-object -w --no-filters -- "$destination/$relative"
    )" || {
      printf 'materialized reviewed commit blob could not be hashed: %s\n' "$relative" >&2
      exit 1
    }
    if [[ "$materialized_object_id" != "$object_id" ]]; then
      printf 'reviewed commit blob failed integrity verification after materialization: %s\n' "$relative" >&2
      exit 1
    fi
    file_mode=0644
    [[ "$mode" != 100755 ]] || file_mode=0755
    chmod "$file_mode" -- "$destination/$relative"
    printf '%s %s\t%s\0' "$mode" "$object_id" "$relative" >>"$index_information"
    (( entry_count += 1 ))
  done <"$listing"

  if (( entry_count == 0 )); then
    printf 'reviewed commit tree is empty\n' >&2
    exit 1
  fi
  run_provenance_git read-tree --empty
  if ! run_provenance_git update-index -z --index-info <"$index_information"; then
    printf 'reviewed commit tree could not be reconstructed from verified blobs\n' >&2
    exit 1
  fi
  reconstructed_tree="$(run_provenance_git write-tree)" || {
    printf 'reviewed commit root tree could not be reconstructed\n' >&2
    exit 1
  }
  if [[ ! "$reconstructed_tree" =~ ^[0-9a-f]{40}$ ||
        "$reconstructed_tree" != "$root_tree" ]]; then
    printf 'reconstructed reviewed commit root tree differs from the verified commit payload\n' >&2
    exit 1
  fi
}

while (( $# > 0 )); do
  case "$1" in
    --version)
      (( $# >= 2 )) || { usage; exit 2; }
      version="$2"
      shift 2
      ;;
    --commit)
      (( $# >= 2 )) || { usage; exit 2; }
      requested_commit="$2"
      shift 2
      ;;
    --source-date-epoch)
      (( $# >= 2 )) || { usage; exit 2; }
      source_date_epoch="$2"
      shift 2
      ;;
    --go-path)
      (( $# >= 2 )) || { usage; exit 2; }
      go_binary="$2"
      shift 2
      ;;
    --go-version)
      (( $# >= 2 )) || { usage; exit 2; }
      go_version="$2"
      shift 2
      ;;
    --goos)
      (( $# >= 2 )) || { usage; exit 2; }
      goos="$2"
      shift 2
      ;;
    --goarch)
      (( $# >= 2 )) || { usage; exit 2; }
      goarch="$2"
      shift 2
      ;;
    --goamd64)
      (( $# >= 2 )) || { usage; exit 2; }
      goamd64="$2"
      shift 2
      ;;
    --release-purpose)
      (( $# >= 2 )) || { usage; exit 2; }
      release_purpose="$2"
      shift 2
      ;;
    --recommendation-model)
      (( $# >= 2 )) || { usage; exit 2; }
      recommendation_model="$2"
      shift 2
      ;;
    --recommendation-model-sha256)
      (( $# >= 2 )) || { usage; exit 2; }
      recommendation_model_sha256="$2"
      shift 2
      ;;
    --knowledge-catalog)
      (( $# >= 2 )) || { usage; exit 2; }
      knowledge_catalog="$2"
      shift 2
      ;;
    --knowledge-catalog-sha256)
      (( $# >= 2 )) || { usage; exit 2; }
      knowledge_catalog_sha256="$2"
      shift 2
      ;;
    --output)
      (( $# >= 2 )) || { usage; exit 2; }
      output="$2"
      shift 2
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

PATH=/usr/bin:/bin
export PATH
hash -r

for command in awk chmod cmp date diff env find git grep install jq mktemp mv realpath rm sed sha256sum sort stat sync; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'required command is missing: %s\n' "$command" >&2
    exit 1
  fi
done
unset command

validate_go_binary
validate_esbuild_binary
if [[ "$release_home" != /* || ! -d "$release_home" || -L "$release_home" ||
      "$release_home" != "$(/usr/bin/realpath -e -- "$release_home")" ||
      "$(/usr/bin/stat -Lc '%u' -- "$release_home")" != "$EUID" ]]; then
  printf 'HOME must be one canonical real directory owned by the release user\n' >&2
  exit 2
fi
validate_protected_directory_ancestry 'HOME' "$release_home"

readonly canonical_semver_pattern='^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))([.]((0|[1-9][0-9]*)|([0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)))*)?([+][0-9A-Za-z-]+([.][0-9A-Za-z-]+)*)?$'
if (( ${#version} > 128 )); then
  printf 'release version must be at most 128 ASCII bytes\n' >&2
  exit 2
fi
if [[ ! "$version" =~ $canonical_semver_pattern ]]; then
  printf 'release version must be a canonical semantic version\n' >&2
  exit 2
fi
if [[ ! "$requested_commit" =~ ^[0-9a-f]{40}$ ]]; then
  printf 'release commit must be an explicit canonical 40-character Git object ID\n' >&2
  exit 2
fi
if [[ ! "$source_date_epoch" =~ ^(0|[1-9][0-9]*)$ ]]; then
  printf 'source date epoch must be a canonical non-negative integer\n' >&2
  exit 2
fi
if [[ ! "$go_version" =~ ^go[0-9]+[.][0-9]+([.][0-9]+)?([A-Za-z0-9.:_+~-]+)?$ ]]; then
  printf 'Go version must be the exact canonical output of GOTOOLCHAIN=local go env GOVERSION\n' >&2
  exit 2
fi
if [[ "$goos" != "linux" || "$goarch" != "amd64" || "$goamd64" != "v1" ]]; then
  printf 'the v2 production release target must be explicit linux/amd64 at GOAMD64=v1\n' >&2
  exit 2
fi
if [[ "$release_purpose" != "production" && "$release_purpose" != "acceptance_test" ]]; then
  printf 'release purpose must be exactly production or acceptance_test\n' >&2
  exit 2
fi
if [[ ! "$recommendation_model_sha256" =~ ^[0-9a-f]{64}$ ]]; then
  printf 'recommendation model SHA-256 must be exactly 64 lowercase hexadecimal characters\n' >&2
  exit 2
fi
validate_recommendation_model_source
if [[ ! "$knowledge_catalog_sha256" =~ ^[0-9a-f]{64}$ ]]; then
  printf 'knowledge catalog SHA-256 must be exactly 64 lowercase hexadecimal characters\n' >&2
  exit 2
fi
validate_knowledge_catalog_source
if [[ "$output" != /* || "$output" != "$(realpath -m -- "$output")" ]]; then
  printf 'output must be a clean absolute path\n' >&2
  exit 2
fi
if [[ -e "$output" || -L "$output" ]]; then
  printf 'output must not already exist: %s\n' "$output" >&2
  exit 2
fi

output_parent="$(dirname -- "$output")"
if [[ ! -d "$output_parent" || -L "$output_parent" ||
      "$output_parent" != "$(realpath -e -- "$output_parent")" ]]; then
  printf 'output parent must be an existing canonical real directory: %s\n' "$output_parent" >&2
  exit 2
fi

output_parent_owner="$(stat -Lc '%u' "$output_parent")"
output_parent_mode="$(stat -Lc '%a' "$output_parent")"
if [[ "$output_parent_owner" != "$EUID" ]]; then
  printf 'output parent must be owned by the effective build user: %s\n' "$output_parent" >&2
  exit 2
fi
if (( (8#$output_parent_mode & 8#022) != 0 )); then
  printf 'output parent must not be group- or other-writable: %s\n' "$output_parent" >&2
  exit 2
fi
validate_protected_directory_ancestry 'output parent' "$output_parent"
readonly output_parent_identity="$(/usr/bin/stat -Lc '%d:%i' -- "$output_parent")"
readonly output_basename="${output##*/}"

exec {output_parent_fd}<"$output_parent"
readonly output_parent_fd
readonly output_parent_anchor="/proc/self/fd/$output_parent_fd"
if [[ "$(/usr/bin/stat -Lc '%d:%i' -- "$output_parent_anchor")" != "$output_parent_identity" ]]; then
  printf 'output parent identity changed while establishing its anchored descriptor\n' >&2
  exit 1
fi

assert_output_parent_identity() {
  local phase="$1"
  local actual_identity

  validate_protected_directory_ancestry 'output parent' "$output_parent"
  actual_identity="$(/usr/bin/stat -Lc '%d:%i' -- "$output_parent")" || {
    printf 'output parent disappeared before %s\n' "$phase" >&2
    exit 1
  }
  if [[ "$actual_identity" != "$output_parent_identity" ]]; then
    printf 'output parent identity changed before %s\n' "$phase" >&2
    exit 1
  fi
}

umask 077
assert_output_parent_identity 'workspace creation'
workspace="$(mktemp -d "$output_parent_anchor/.ascendany-v2-build.XXXXXXXX")"
chmod 0700 "$workspace"
source_root="$workspace/source"
staging="$workspace/release"
provenance_git_dir="$workspace/provenance.git"
provenance_index="$workspace/provenance.index"
publication_moved=0
publication_verified=0
published_output_identity=""

cleanup() {
  local cleanup_anchored_output="$output_parent_anchor/$output_basename"
  local actual_identity=""

  if (( publication_moved == 1 && publication_verified == 0 )) &&
     [[ -n "$published_output_identity" && -d "$cleanup_anchored_output" && ! -L "$cleanup_anchored_output" ]]; then
    actual_identity="$(/usr/bin/stat -Lc '%d:%i' -- "$cleanup_anchored_output" 2>/dev/null || true)"
    if [[ "$actual_identity" == "$published_output_identity" ]]; then
      /usr/bin/rm -rf -- "$cleanup_anchored_output"
      /usr/bin/sync -- "$output_parent_anchor" 2>/dev/null || true
    fi
  fi
  /usr/bin/rm -rf -- "$workspace"
  /usr/bin/sync -- "$output_parent_anchor" 2>/dev/null || true
}
trap cleanup EXIT

install -d -m 0700 "$source_root" "$staging"
readonly recommendation_model_source_identity="$(/usr/bin/stat -Lc '%d:%i:%u:%a:%s:%h' -- "$recommendation_model")"
captured_recommendation_model="$workspace/recommendation-model.json"
/usr/bin/install -m 0600 -- "$recommendation_model" "$captured_recommendation_model"
if [[ "$(/usr/bin/stat -Lc '%d:%i:%u:%a:%s:%h' -- "$recommendation_model")" != "$recommendation_model_source_identity" ||
      "$(/usr/bin/sha256sum -- "$recommendation_model" | /usr/bin/awk '{print $1}')" != "$recommendation_model_sha256" ||
      "$(/usr/bin/sha256sum -- "$captured_recommendation_model" | /usr/bin/awk '{print $1}')" != "$recommendation_model_sha256" ]]; then
  printf 'recommendation model changed while it was captured\n' >&2
  exit 1
fi
readonly knowledge_catalog_source_identity="$(/usr/bin/stat -Lc '%d:%i:%u:%a:%s:%h' -- "$knowledge_catalog")"
captured_knowledge_catalog="$workspace/recommendation-knowledge-catalog.json"
/usr/bin/install -m 0600 -- "$knowledge_catalog" "$captured_knowledge_catalog"
if [[ "$(/usr/bin/stat -Lc '%d:%i:%u:%a:%s:%h' -- "$knowledge_catalog")" != "$knowledge_catalog_source_identity" ||
      "$(/usr/bin/sha256sum -- "$knowledge_catalog" | /usr/bin/awk '{print $1}')" != "$knowledge_catalog_sha256" ||
      "$(/usr/bin/sha256sum -- "$captured_knowledge_catalog" | /usr/bin/awk '{print $1}')" != "$knowledge_catalog_sha256" ]]; then
  printf 'knowledge catalog changed while it was captured\n' >&2
  exit 1
fi
env -i \
  PATH="$PATH" \
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
    init --bare --object-format=sha1 --quiet "$provenance_git_dir"
if [[ "$(run_provenance_git rev-parse --show-object-format)" != sha1 ]]; then
  printf 'isolated provenance repository did not initialize with SHA-1 object format\n' >&2
  exit 1
fi

if [[ "$(run_repository_git rev-parse --show-toplevel 2>/dev/null)" != "$repository_root" ]]; then
  printf 'release builder is not rooted at the repository top level\n' >&2
  exit 1
fi
if [[ "$(run_repository_git rev-parse --show-object-format 2>/dev/null)" != sha1 ]]; then
  printf 'release repository must use the SHA-1 Git object format\n' >&2
  exit 1
fi

commit_payload="$workspace/commit.payload"
if ! run_repository_git cat-file commit "$requested_commit" >"$commit_payload"; then
  printf 'release commit payload could not be captured from the repository object store\n' >&2
  exit 1
fi
commit="$(run_provenance_git hash-object -t commit -w --stdin <"$commit_payload")" || {
  printf 'release commit payload could not be hashed in the isolated object store\n' >&2
  exit 1
}
if [[ ! "$commit" =~ ^[0-9a-f]{40}$ || "$commit" != "$requested_commit" ]]; then
  printf 'release commit payload failed isolated SHA-1 identity verification\n' >&2
  exit 1
fi
IFS= read -r commit_tree_header <"$commit_payload" || {
  printf 'verified release commit payload has no root tree header\n' >&2
  exit 1
}
if [[ ! "$commit_tree_header" =~ ^tree\ ([0-9a-f]{40})$ ]]; then
  printf 'verified release commit payload has an invalid root tree header\n' >&2
  exit 1
fi
root_tree="${BASH_REMATCH[1]}"
readonly commit root_tree

materialize_reviewed_commit \
  "$source_root" \
  "$workspace/reviewed-tree" \
  "$workspace/provenance-index-information"
if [[ "$(stat -Lc '%u:%a' "$source_root")" != "$EUID:700" ]]; then
  printf 'detached release source is not private to the effective build user\n' >&2
  exit 1
fi
if find "$source_root" -type l -print -quit | grep -q .; then
  printf 'detached release source contains a symbolic link\n' >&2
  exit 1
fi
verify_running_builder
build_time="$(date -u -d "@$source_date_epoch" +%FT%TZ)"
actual_go_version="$(
  /usr/bin/env -i PATH=/usr/bin:/bin HOME="$release_home" GOTOOLCHAIN=local GOENV=off \
    "$go_binary" env GOVERSION
)" || {
  printf 'the local Go toolchain version cannot be read without toolchain download\n' >&2
  exit 1
}
if [[ "$actual_go_version" != "$go_version" ]]; then
  printf 'local Go toolchain is %s; the reviewed release requires %s\n' "$actual_go_version" "$go_version" >&2
  exit 1
fi
actual_go_experiment="$(
  /usr/bin/env -i PATH=/usr/bin:/bin HOME="$release_home" GOTOOLCHAIN=local GOENV=off \
    GOEXPERIMENT='' "$go_binary" env GOEXPERIMENT
)" || {
  printf 'the local Go experiment set cannot be read without toolchain download\n' >&2
  exit 1
}
if [[ -z "$actual_go_experiment" ]]; then
  actual_go_experiment=none
elif [[ ! "$actual_go_experiment" =~ ^[0-9A-Za-z_,.-]+$ ]]; then
  printf 'the local Go experiment set is noncanonical\n' >&2
  exit 1
fi

readonly -a binaries=(
  ascendanyd
  ascendany-admin-bootstrap
  ascendany-backup
  ascendany-catalog-publish
  ascendany-judge
  ascendany-lsp
  ascendany-migrate
  ascendany-model
  ascendany-release-ops
)

declare -a payload_paths=()
for binary in "${binaries[@]}"; do
  payload_paths+=("bin/$binary")
done
payload_paths+=(
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
readonly -a payload_paths
if [[ "${#payload_paths[@]}" != "68" ]]; then
  printf 'release payload path contract must contain exactly 68 entries\n' >&2
  exit 1
fi

readonly -a payload_directories=(
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

readonly -a copy_sources=(
  deploy/v2/README.md
  deploy/v2/OJ_JUDGE_CONTRACT.md
  deploy/v2/LSP_CONTROL_CONTRACT.md
  contracts/openapi/ascendany-v2.yaml
  contracts/pintia/ascendany.pintia.snapshot.v2.schema.json
  db/roles/README.md
  db/roles/001_v2_roles.sql
  db/roles/verify_v2_roles.sql
  deploy/v2/config/analytics.json.example
  deploy/v2/config/ascendanyd.env.example
  deploy/v2/config/ascendanyd-read-only-smoke.env.example
  deploy/v2/config/backup.env.example
  deploy/v2/config/catalog-publish.env.example
  deploy/v2/config/cloudflared.yaml
  deploy/v2/config/fedora-runtime-packages.json
  deploy/v2/config/judge.env.example
  deploy/v2/config/judge-compiler-rootfs.inventory
  deploy/v2/config/judge-image-lock.json
  deploy/v2/config/judge-images.Containerfile
  deploy/v2/config/migrate.env.example
  deploy/v2/config/pgbouncer-hba.conf
  deploy/v2/config/pgbouncer.ini
  deploy/v2/config/postgresql-hba.conf
  deploy/v2/config/postgresql-ident.conf
  deploy/v2/config/restore.env.example
  deploy/v2/systemd/ascendanyd.service
  deploy/v2/systemd/ascendany-model-register.service
  deploy/v2/systemd/ascendany-model-activate.service
  deploy/v2/systemd/ascendany-catalog-publish.service
  deploy/v2/systemd/ascendanyd.service.d/40-read-only-smoke.conf
  deploy/v2/systemd/ascendany-admin-bootstrap.service
  deploy/v2/systemd/ascendany-backup.service
  deploy/v2/systemd/ascendany-backup.timer
  deploy/v2/systemd/ascendany-cloudflared.service
  deploy/v2/systemd/ascendany-judge@.service
  deploy/v2/systemd/ascendany-lsp@.service
  deploy/v2/systemd/ascendany-migrate.service
  deploy/v2/systemd/ascendany-pgbouncer.service
  deploy/v2/systemd/ascendany-restore-verify@.service
  deploy/v2/polkit-1/rules.d/60-ascendany-judge.rules
  deploy/v2/polkit-1/rules.d/61-ascendany-lsp.rules
  deploy/v2/sysusers.d/ascendany-v2.conf
  deploy/v2/tmpfiles.d/ascendany-v2.conf
  deploy/v2/scripts/publish-restore-evidence.sh
  deploy/v2/scripts/restore-verify-operator.sh
  deploy/v2/scripts/install-v2-release.sh
  deploy/v2/scripts/acquire-judge-image.sh
  deploy/v2/scripts/attest-judge-image.sh
  deploy/v2/scripts/judge-image-contract.sh
  deploy/v2/scripts/preload-judge-image.sh
  deploy/v2/scripts/acquire-pgbouncer-rpm.sh
  deploy/v2/scripts/attest-pgbouncer-rpm.sh
  deploy/v2/scripts/provision-postgres-pgbouncer.sh
  deploy/v2/scripts/postgres-schema-fingerprint.sh
  deploy/v2/scripts/validate-cloudflared.sh
  deploy/v2/scripts/validate-production.sh
)
readonly -a copy_targets=(
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
if [[ "${#copy_sources[@]}" != "${#copy_targets[@]}" ]]; then
  printf 'release copy source and target contracts differ in length\n' >&2
  exit 1
fi

install -d -m 0755 \
  "$staging/bin" \
  "$staging/models" \
  "$staging/operators" \
  "$staging/contracts/openapi" \
  "$staging/contracts/pintia" \
  "$staging/db/roles" \
  "$staging/config" \
  "$staging/systemd" \
  "$staging/systemd/ascendanyd.service.d" \
  "$staging/polkit-1/rules.d" \
  "$staging/sysusers.d" \
  "$staging/tmpfiles.d" \
  "$staging/scripts"

readonly version_package='github.com/kkkzbh/AscendAny/backend/internal/version'
readonly linker_flags="-s -w -buildid= -X ${version_package}.Version=${version} -X ${version_package}.Commit=${commit} -X ${version_package}.BuildTime=${build_time}"

(
  cd "$source_root/backend"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    HOME="$release_home" \
    GOTOOLCHAIN=local \
    GOENV=off \
    GOWORK=off \
    GOFLAGS='' \
    GOEXPERIMENT='' \
    GOPROXY=off \
    "$go_binary" mod verify
  for binary in "${binaries[@]}"; do
    /usr/bin/env -i \
      PATH=/usr/bin:/bin \
      HOME="$release_home" \
      GOTOOLCHAIN=local \
      GOENV=off \
      GOWORK=off \
      GOFLAGS='' \
      GOEXPERIMENT='' \
      GOPROXY=off \
      CGO_ENABLED=0 \
      GOOS="$goos" \
      GOARCH="$goarch" \
      GOAMD64="$goamd64" \
      GOFIPS140=off \
      "$go_binary" build \
      -mod=readonly \
      -buildvcs=false \
      -trimpath \
      -ldflags "$linker_flags" \
      -o "$staging/bin/$binary" \
      "./cmd/$binary"
    assert_output_parent_identity "publishing $binary build output"
    chmod 0755 "$staging/bin/$binary"
  done
)

if ! /usr/bin/jq -e --arg version "$esbuild_version" \
  '.devDependencies.esbuild == $version' \
  "$source_root/packages/sdk/package.json" >/dev/null; then
  printf 'reviewed SDK workspace must pin esbuild %s exactly\n' "$esbuild_version" >&2
  exit 1
fi
(
  cd "$source_root"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    HOME="$release_home" \
    LC_ALL=C \
    "$esbuild_binary" \
      tools/v2-production-initialization-client.ts \
      --bundle \
      --platform=node \
      --format=esm \
      --target=node22.22 \
      --packages=bundle \
      --tree-shaking=true \
      --charset=utf8 \
      --legal-comments=none \
      --log-level=error \
      --outfile="$staging/operators/ascendany-production-initialize.mjs"
)
assert_output_parent_identity 'publishing production initialization bundle'
/usr/bin/chmod 0555 -- "$staging/operators/ascendany-production-initialize.mjs"

/usr/bin/install -m 0644 -- \
  "$captured_recommendation_model" \
  "$staging/models/recommendation-model.json"
/usr/bin/install -m 0644 -- \
  "$captured_knowledge_catalog" \
  "$staging/models/recommendation-knowledge-catalog.json"
/usr/bin/env -i \
  PATH=/usr/bin:/bin \
  LC_ALL=C \
  "$staging/bin/ascendany-model" verify \
    --model "$staging/models/recommendation-model.json" \
    --sha256 "$recommendation_model_sha256" \
    --expected-purpose "$release_purpose"
/usr/bin/env -i \
  PATH=/usr/bin:/bin \
  LC_ALL=C \
  "$staging/bin/ascendany-model" verify-catalog \
    --catalog "$staging/models/recommendation-knowledge-catalog.json" \
    --catalog-sha256 "$knowledge_catalog_sha256" \
    --model "$staging/models/recommendation-model.json" \
    --model-sha256 "$recommendation_model_sha256" \
    --expected-purpose "$release_purpose"

for index in "${!copy_sources[@]}"; do
  mode=0644
  if [[ "${copy_targets[$index]}" == scripts/* ]]; then
    mode=0755
  fi
  install -m "$mode" \
    "$source_root/${copy_sources[$index]}" \
    "$staging/${copy_targets[$index]}"
done
unset index mode
for release_config in "$staging/config/ascendanyd.env" "$staging/config/catalog-publish.env"; do
  if [[ "$(/usr/bin/grep -Fxc 'ASCENDANY_RECOMMENDATION_MODEL_SHA256=__ASCENDANY_RECOMMENDATION_MODEL_SHA256__' "$release_config")" != 1 ||
        "$(/usr/bin/grep -Fxc 'ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=__ASCENDANY_RECOMMENDATION_MODEL_PURPOSE__' "$release_config")" != 1 ||
        "$(/usr/bin/grep -Fxc 'ASCENDANY_KNOWLEDGE_CATALOG_SHA256=__ASCENDANY_KNOWLEDGE_CATALOG_SHA256__' "$release_config")" != 1 ]]; then
    printf 'release configuration must contain one model/catalog marker of each kind: %s\n' "$release_config" >&2
    exit 1
  fi
  /usr/bin/sed -i \
    -e "s/^ASCENDANY_RECOMMENDATION_MODEL_SHA256=__ASCENDANY_RECOMMENDATION_MODEL_SHA256__$/ASCENDANY_RECOMMENDATION_MODEL_SHA256=$recommendation_model_sha256/" \
    -e "s/^ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=__ASCENDANY_RECOMMENDATION_MODEL_PURPOSE__$/ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=$release_purpose/" \
    -e "s/^ASCENDANY_KNOWLEDGE_CATALOG_SHA256=__ASCENDANY_KNOWLEDGE_CATALOG_SHA256__$/ASCENDANY_KNOWLEDGE_CATALOG_SHA256=$knowledge_catalog_sha256/" \
    "$release_config"
done
unset release_config

expected_payload_paths="$workspace/expected-payload-paths"
actual_payload_paths="$workspace/actual-payload-paths"
printf '%s\n' "${payload_paths[@]}" | sort >"$expected_payload_paths"
find "$staging" -mindepth 1 ! -type d -printf '%P\n' | sort >"$actual_payload_paths"
if ! diff -u "$expected_payload_paths" "$actual_payload_paths"; then
  printf 'staged release payload differs from the exact 68-path contract\n' >&2
  exit 1
fi
for relative in "${payload_paths[@]}"; do
  if [[ ! -f "$staging/$relative" || -L "$staging/$relative" ]]; then
    printf 'release payload path is not one regular file: %s\n' "$relative" >&2
    exit 1
  fi
done

expected_directories="$workspace/expected-directories"
actual_directories="$workspace/actual-directories"
printf '%s\n' "${payload_directories[@]}" | sort >"$expected_directories"
find "$staging" -mindepth 1 -type d -printf '%P\n' | sort >"$actual_directories"
if ! diff -u "$expected_directories" "$actual_directories"; then
  printf 'staged release directory set differs from the closed payload contract\n' >&2
  exit 1
fi

files='[]'
declare -a payload_sha256_values=()
declare -a payload_size_values=()
declare -a payload_mode_values=()
for index in "${!payload_paths[@]}"; do
  relative="${payload_paths[$index]}"
  path="$staging/$relative"
  sha256="$(sha256sum -- "$path" | awk '{print $1}')"
  size="$(stat -Lc '%s' "$path")"
  mode="0$(stat -Lc '%a' "$path")"
  payload_sha256_values[$index]="$sha256"
  payload_size_values[$index]="$size"
  payload_mode_values[$index]="$mode"
  files="$(
    jq -c \
      --arg path "$relative" \
      --arg sha256 "$sha256" \
      --argjson size "$size" \
      --arg mode "$mode" \
      '. + [{path: $path, sha256: $sha256, size: $size, mode: $mode}]' \
      <<<"$files"
  )"
done
readonly -a payload_sha256_values payload_size_values payload_mode_values
unset index relative path sha256 size mode

manifest_json="$(jq -cnS \
  --arg schema 'ascendany.release.v2' \
  --arg version "$version" \
  --arg commit "$commit" \
  --arg purpose "$release_purpose" \
  --arg goVersion "$go_version" \
  --arg goos "$goos" \
  --arg goarch "$goarch" \
  --arg goamd64 "$goamd64" \
  --arg goExperiment "$actual_go_experiment" \
  --argjson sourceDateEpoch "$source_date_epoch" \
  --argjson files "$files" \
  '{
    schema: $schema,
    version: $version,
    commit: $commit,
    purpose: $purpose,
    sourceDateEpoch: $sourceDateEpoch,
    build: {
      goVersion: $goVersion,
      goos: $goos,
      goarch: $goarch,
      goamd64: $goamd64,
      goExperiment: $goExperiment,
      gofips140: "off",
      cgoEnabled: false
    },
    files: $files
  }')"
printf '%s' "$manifest_json" >"$staging/release-manifest.json"
chmod 0644 "$staging/release-manifest.json"
readonly manifest_sha256="$(sha256sum -- "$staging/release-manifest.json" | awk '{print $1}')"
readonly manifest_size="$(stat -Lc '%s' -- "$staging/release-manifest.json")"
readonly manifest_mode="0$(stat -Lc '%a' -- "$staging/release-manifest.json")"

expected_release_paths="$workspace/expected-release-paths"
printf '%s\n' "${payload_paths[@]}" release-manifest.json | sort >"$expected_release_paths"

verify_release_tree() {
  local tree_root="$1"
  local label="$2"
  local actual_release_paths="$workspace/${label}-release-paths"
  local actual_directories="$workspace/${label}-directories"
  local index relative path actual_hash actual_size actual_mode actual_owner actual_links

  find "$tree_root" -mindepth 1 ! -type d -printf '%P\n' | sort >"$actual_release_paths"
  if ! diff -u "$expected_release_paths" "$actual_release_paths"; then
    printf '%s release tree differs from the closed path contract\n' "$label" >&2
    return 1
  fi
  find "$tree_root" -mindepth 1 -type d -printf '%P\n' | sort >"$actual_directories"
  if ! diff -u "$expected_directories" "$actual_directories"; then
    printf '%s release directory set differs from the closed directory contract\n' "$label" >&2
    return 1
  fi
  if [[ "$(stat -Lc '%u:%a' -- "$tree_root")" != "$EUID:755" ]]; then
    printf '%s release root ownership or mode drifted\n' "$label" >&2
    return 1
  fi
  while IFS= read -r -d '' path; do
    if [[ "$(stat -Lc '%u:%a' -- "$path")" != "$EUID:755" ]]; then
      printf '%s release directory ownership or mode drifted\n' "$label" >&2
      return 1
    fi
  done < <(find "$tree_root" -mindepth 1 -type d -print0)

  for index in "${!payload_paths[@]}"; do
    relative="${payload_paths[$index]}"
    path="$tree_root/$relative"
    if [[ ! -f "$path" || -L "$path" ]]; then
      printf '%s release payload path is not one regular file: %s\n' "$label" "$relative" >&2
      return 1
    fi
    IFS=: read -r actual_owner actual_mode actual_size actual_links < <(
      stat -Lc '%u:%a:%s:%h' -- "$path"
    )
    actual_hash="$(sha256sum -- "$path" | awk '{print $1}')"
    if [[ "$actual_owner" != "$EUID" || "0$actual_mode" != "${payload_mode_values[$index]}" ||
          "$actual_size" != "${payload_size_values[$index]}" ||
          "$actual_hash" != "${payload_sha256_values[$index]}" || "$actual_links" != 1 ]]; then
      printf '%s release payload integrity drifted: %s\n' "$label" "$relative" >&2
      return 1
    fi
  done

  path="$tree_root/release-manifest.json"
  if [[ ! -f "$path" || -L "$path" ]]; then
    printf '%s release manifest is not one regular file\n' "$label" >&2
    return 1
  fi
  IFS=: read -r actual_owner actual_mode actual_size actual_links < <(
    stat -Lc '%u:%a:%s:%h' -- "$path"
  )
  actual_hash="$(sha256sum -- "$path" | awk '{print $1}')"
  if [[ "$actual_owner" != "$EUID" || "0$actual_mode" != "$manifest_mode" ||
        "$actual_size" != "$manifest_size" || "$actual_hash" != "$manifest_sha256" ||
        "$actual_links" != 1 ]]; then
    printf '%s release manifest integrity drifted\n' "$label" >&2
    return 1
  fi
}

assert_output_parent_identity 'release publication'
chmod 0755 "$staging"
verify_release_tree "$staging" staged
for relative in "${payload_paths[@]}" release-manifest.json; do
  /usr/bin/sync -- "$staging/$relative"
done
while IFS= read -r -d '' path; do
  /usr/bin/sync -- "$path"
done < <(find "$staging" -depth -type d -print0)

readonly staged_release_identity="$(/usr/bin/stat -Lc '%d:%i' -- "$staging")"
readonly anchored_output="$output_parent_anchor/$output_basename"
if ! mv --no-target-directory --no-clobber -- "$staging" "$anchored_output"; then
  printf 'release publication failed without replacing the target: %s\n' "$output" >&2
  exit 1
fi
if [[ -e "$staging" || -L "$staging" ]]; then
  printf 'release target appeared during publication; no payload was published: %s\n' "$output" >&2
  exit 1
fi
publication_moved=1
published_output_identity="$staged_release_identity"
if [[ ! -d "$anchored_output" || -L "$anchored_output" ||
      "$(/usr/bin/stat -Lc '%d:%i' -- "$anchored_output")" != "$published_output_identity" ]]; then
  printf 'published release is not one real directory: %s\n' "$output" >&2
  exit 1
fi
assert_output_parent_identity 'published release verification'
/usr/bin/sync -- "$workspace"
/usr/bin/sync -- "$output_parent_anchor"
assert_output_parent_identity 'durable published release verification'
verify_release_tree "$anchored_output" published
assert_output_parent_identity 'completed published release verification'
publication_verified=1

printf '%s\n' "$output"
