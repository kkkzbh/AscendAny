#!/usr/bin/bash -p

set +x
set -Eeuo pipefail

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  preloader_environment_is_clean=1
  while IFS= read -r -d '' entry; do
    name="${entry%%=*}"
    case "$name" in PATH|LC_ALL|PWD|SHLVL|_|ASCENDANY_JUDGE_PRELOADER_CLEAN_ENV) ;; *) preloader_environment_is_clean=0 ;; esac
  done < <(/usr/bin/env -0)
  if [[ "${ASCENDANY_JUDGE_PRELOADER_CLEAN_ENV-}" != "1" || "${PATH-}" != "/usr/bin:/bin" ||
        "${LC_ALL-}" != "C" || "$preloader_environment_is_clean" != "1" ]]; then
    exec /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C ASCENDANY_JUDGE_PRELOADER_CLEAN_ENV=1 /usr/bin/bash -p "$0" "$@"
  fi
fi

umask 077
script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=judge-image-contract.sh
source "${script_directory}/judge-image-contract.sh"
load_judge_image_contract
die() { printf 'preload Judge images: %s\n' "$1" >&2; exit 1; }
usage() {
  printf 'usage: %s --compiler-archive /absolute/compiler.oci.tar --compiler-archive-sha256 /absolute/compiler.sha256 --runtime-archive /absolute/runtime.oci.tar --runtime-archive-sha256 /absolute/runtime.sha256 --target-user ascendany-judge\n' "$0" >&2
}

[[ "$EUID" == 0 ]] || die 'preload must run as root'
[[ "$#" == 10 && "$1" == '--compiler-archive' && "$3" == '--compiler-archive-sha256' &&
   "$5" == '--runtime-archive' && "$7" == '--runtime-archive-sha256' && "$9" == '--target-user' ]] || { usage; exit 2; }
compiler_archive="$2"; compiler_sha256_file="$4"; runtime_archive="$6"; runtime_sha256_file="$8"; target_user="${10}"
for path in "$compiler_archive" "$compiler_sha256_file" "$runtime_archive" "$runtime_sha256_file"; do
  [[ "$path" == /* && "$path" == "$(realpath -e -- "$path")" && -f "$path" && ! -L "$path" ]] ||
    die 'archive inputs must be canonical absolute regular files'
done
[[ "$target_user" == "ascendany-judge" ]] || die 'target user must be exactly ascendany-judge'
target_home="$(getent passwd "$target_user" | cut -d: -f6)"
[[ "$target_home" == "/var/lib/ascendany-judge" && -d "$target_home" && ! -L "$target_home" ]] ||
  die 'target user home differs from the dedicated Judge state root'
target_uid="$(id -u "$target_user")"; target_gid="$(id -g "$target_user")"
target_runtime="/run/ascendany-judge-image-podman"
[[ -d "$target_runtime" && ! -L "$target_runtime" &&
   "$target_runtime" == "$(realpath -e -- "$target_runtime" 2>/dev/null || true)" &&
   "$(stat -Lc '%u:%g:%a' "$target_runtime" 2>/dev/null || true)" == "$target_uid:$target_gid:700" ]] ||
  die 'target user XDG runtime directory differs from the dedicated 0700 boundary'

run_as_target() {
  (
    cd "$target_home" || exit 1
    exec /usr/bin/runuser -u "$target_user" -- /usr/bin/env -i \
      PATH=/usr/bin:/bin LANG=C.UTF-8 HOME="$target_home" XDG_RUNTIME_DIR="$target_runtime" \
      XDG_DATA_HOME="$target_home/.local/share" XDG_CONFIG_HOME="$target_home/.config" XDG_CACHE_HOME="$target_home/.cache" "$@"
  )
}
verify_archive() {
  local archive="$1" digest_file="$2" label="$3" expected
  mapfile -t digest_lines <"$digest_file"
  (( ${#digest_lines[@]} == 1 )) || die "$label archive digest file must contain exactly one line"
  expected="${digest_lines[0]}"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || die "$label archive digest is noncanonical"
  [[ "$(sha256sum "$archive" | awk '{print $1}')" == "$expected" ]] || die "$label archive bytes differ from the offline trust anchor"
}
verify_archive "$compiler_archive" "$compiler_sha256_file" compiler
verify_archive "$runtime_archive" "$runtime_sha256_file" runtime
run_as_target /usr/bin/podman --cgroup-manager=cgroupfs --runroot="$target_runtime/containers" load <"$compiler_archive" >/dev/null ||
  die 'rootless Podman could not load the compiler OCI archive'
run_as_target /usr/bin/podman --cgroup-manager=cgroupfs --runroot="$target_runtime/containers" load <"$runtime_archive" >/dev/null ||
  die 'rootless Podman could not load the runtime OCI archive'
run_as_target "${script_directory}/attest-judge-image.sh"
