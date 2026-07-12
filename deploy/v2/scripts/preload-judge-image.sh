#!/usr/bin/bash -p

set +x
set -Eeuo pipefail

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  preloader_environment_is_clean=1
  while IFS= read -r -d '' entry; do
    name="${entry%%=*}"
    case "$name" in
      PATH|LC_ALL|PWD|SHLVL|_|ASCENDANY_JUDGE_PRELOADER_CLEAN_ENV)
        ;;
      *)
        preloader_environment_is_clean=0
        ;;
    esac
  done < <(/usr/bin/env -0)
  if [[ "${ASCENDANY_JUDGE_PRELOADER_CLEAN_ENV-}" != "1" ||
        "${PATH-}" != "/usr/bin:/bin" || "${LC_ALL-}" != "C" ||
        "$preloader_environment_is_clean" != "1" ]]; then
    exec /usr/bin/env -i \
      PATH=/usr/bin:/bin \
      LC_ALL=C \
      ASCENDANY_JUDGE_PRELOADER_CLEAN_ENV=1 \
      /usr/bin/bash -p "$0" "$@"
  fi
fi

umask 077

script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=judge-image-contract.sh
source "${script_directory}/judge-image-contract.sh"
load_judge_image_contract

die() {
  printf 'preload Judge image: %s\n' "$1" >&2
  exit 1
}

usage() {
  printf 'usage: %s --archive /absolute/image.oci.tar --archive-sha256 /absolute/image.oci.tar.sha256 --target-user ascendany-judge\n' "$0" >&2
}

[[ "$EUID" == 0 ]] || die 'preload must run as root'
[[ "$#" == 6 && "$1" == '--archive' && "$3" == '--archive-sha256' && "$5" == '--target-user' ]] || {
  usage
  exit 2
}
archive="$2"
archive_sha256_file="$4"
target_user="$6"
[[ "$archive" == /* && "$archive" == "$(realpath -e -- "$archive")" && -f "$archive" && ! -L "$archive" ]] ||
  die 'archive must be a canonical absolute regular file'
[[ "$archive_sha256_file" == /* && "$archive_sha256_file" == "$(realpath -e -- "$archive_sha256_file")" &&
   -f "$archive_sha256_file" && ! -L "$archive_sha256_file" ]] ||
  die 'archive digest must be a canonical absolute regular file'
[[ "$target_user" == "ascendany-judge" ]] || die 'target user must be exactly ascendany-judge'
target_home="$(getent passwd "$target_user" | cut -d: -f6)"
[[ "$target_home" == "/var/lib/ascendany-judge" && -d "$target_home" && ! -L "$target_home" ]] ||
  die 'target user home differs from the dedicated Judge state root'
target_uid="$(id -u "$target_user")"
target_gid="$(id -g "$target_user")"
target_runtime="/run/ascendany-judge-image-podman"
[[ -d "$target_runtime" && ! -L "$target_runtime" &&
   "$target_runtime" == "$(realpath -e -- "$target_runtime" 2>/dev/null || true)" &&
   "$(stat -Lc '%u:%g:%a' "$target_runtime" 2>/dev/null || true)" == "$target_uid:$target_gid:700" ]] ||
  die 'target user XDG runtime directory differs from the dedicated 0700 boundary'

run_as_target() {
  (
    cd "$target_home" || exit 1
    exec /usr/bin/runuser -u "$target_user" -- /usr/bin/env -i \
      PATH=/usr/bin:/bin \
      LANG=C.UTF-8 \
      HOME="$target_home" \
      XDG_RUNTIME_DIR="$target_runtime" \
      XDG_DATA_HOME="$target_home/.local/share" \
      XDG_CONFIG_HOME="$target_home/.config" \
      XDG_CACHE_HOME="$target_home/.cache" \
      "$@"
  )
}

mapfile -t digest_lines <"$archive_sha256_file"
(( ${#digest_lines[@]} == 1 )) || die 'archive digest file must contain exactly one line'
expected_sha256="${digest_lines[0]}"
[[ "$expected_sha256" =~ ^[0-9a-f]{64}$ ]] || die 'archive digest is noncanonical'
[[ "$(sha256sum "$archive" | awk '{print $1}')" == "$expected_sha256" ]] || die 'archive bytes differ from the offline trust anchor'

run_as_target /usr/bin/podman --cgroup-manager=cgroupfs --runroot="$target_runtime/containers" load <"$archive" >/dev/null ||
  die 'rootless Podman could not load the verified OCI archive'
run_as_target "${script_directory}/attest-judge-image.sh"
