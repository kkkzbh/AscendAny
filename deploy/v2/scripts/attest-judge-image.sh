#!/usr/bin/bash

set -Eeuo pipefail

script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=judge-image-contract.sh
source "${script_directory}/judge-image-contract.sh"
load_judge_image_contract

die() { printf 'attest Judge images: %s\n' "$1" >&2; exit 1; }
[[ "$#" == 0 ]] || die 'this command accepts no arguments'
for command in cmp find jq podman readlink sha256sum sort stat; do command -v "$command" >/dev/null || die "required command is missing: $command"; done
operator_runtime="/run/ascendany-judge-image-podman"
[[ "${XDG_RUNTIME_DIR-}" == "$operator_runtime" && -d "$operator_runtime" && ! -L "$operator_runtime" &&
   "$operator_runtime" == "$(realpath -e -- "$operator_runtime" 2>/dev/null || true)" ]] ||
  die 'XDG runtime differs from the dedicated image-operator boundary'
operator_runroot="$operator_runtime/containers"

inspect_image() {
  local image="$1" config="$2" label="$3" inspect
  inspect="$(podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" image inspect --format json "$image")" ||
    die "$label image is absent from the current Podman store"
  jq -e --arg digest "${image##*@}" --arg config "${config#sha256:}" --arg os "$JUDGE_IMAGE_OS" --arg arch "$JUDGE_IMAGE_ARCHITECTURE" '
    type == "array" and length == 1 and .[0].Digest == $digest and .[0].Id == $config and
    .[0].Os == $os and .[0].Architecture == $arch
  ' <<<"$inspect" >/dev/null || die "$label image identity, config, or platform differs from the lock"
}
inspect_image "$JUDGE_COMPILER_IMAGE" "$JUDGE_COMPILER_CONFIG_DIGEST" compiler
inspect_image "$JUDGE_RUNTIME_IMAGE" "$JUDGE_RUNTIME_CONFIG_DIGEST" runtime

temporary_directory="$(mktemp -d)"
cleanup() { rm -rf -- "$temporary_directory"; }
trap cleanup EXIT
generate_inventory() {
  local image="$1" output="$2"
  podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" unshare /usr/bin/bash -Eeuo pipefail -c '
    runroot="$1"; image="$2"; output="$3"
    root="$(podman --cgroup-manager=cgroupfs --runroot="$runroot" image mount "$image")"
    cleanup_mount() { podman --cgroup-manager=cgroupfs --runroot="$runroot" image unmount "$image" >/dev/null; }
    trap cleanup_mount EXIT
    while IFS= read -r -d "" path; do
      relative="${path#"$root"/}"
      mode="$(stat -c "%#a" -- "$path")"
      if [[ -L "$path" ]]; then
        type=l; digest="$(printf "%s" "$(readlink -- "$path")" | sha256sum | cut -d " " -f1)"
      elif [[ -d "$path" ]]; then
        type=d; digest=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
      elif [[ -f "$path" ]]; then
        type=f; digest="$(sha256sum -- "$path" | cut -d " " -f1)"
      else
        exit 1
      fi
      printf "%s|%s|%s|%s\n" "$type" "$mode" "$digest" "$relative"
    done < <(find "$root" -mindepth 1 -print0 | LC_ALL=C sort -z)
  ' bash "$operator_runroot" "$image" "$output" >"$output" || die 'image rootfs inventory generation failed'
}
compiler_inventory="$temporary_directory/compiler.inventory"
runtime_inventory="$temporary_directory/runtime.inventory"
generate_inventory "$JUDGE_COMPILER_IMAGE" "$compiler_inventory"
generate_inventory "$JUDGE_RUNTIME_IMAGE" "$runtime_inventory"
[[ "$(sha256sum "$compiler_inventory" | awk '{print $1}')" == "$JUDGE_COMPILER_INVENTORY_SHA256" &&
   "$(wc -l <"$compiler_inventory" | tr -d ' ')" == "$JUDGE_COMPILER_ROOTFS_ENTRY_COUNT" ]] ||
  die 'compiler rootfs bytes, mode, symlink, or entry set differs from the lock'
cmp --silent "$compiler_inventory" "$JUDGE_COMPILER_INVENTORY_PATH" || die 'compiler rootfs differs from the release-bound inventory'
[[ "$(sha256sum "$runtime_inventory" | awk '{print $1}')" == "$JUDGE_RUNTIME_INVENTORY_SHA256" &&
   "$(wc -l <"$runtime_inventory" | tr -d ' ')" == "$JUDGE_RUNTIME_ROOTFS_ENTRY_COUNT" ]] ||
  die 'runtime rootfs is not exactly empty'

probe_directory="$temporary_directory/probe"
mkdir -m 0700 -- "$probe_directory"
printf '%s\n' 'int main() { return 0; }' >"$probe_directory/probe.cpp"
podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" run --userns=host --rm --pull=never --network=none --http-proxy=false --hosts-file=none \
  --read-only --cap-drop=all --security-opt=no-new-privileges --hooks-dir=/var/empty --pids-limit=64 --memory=512m --cpus=1 \
  --volume="$probe_directory:/workspace:rw,Z" --workdir=/workspace "$JUDGE_COMPILER_IMAGE" \
  "$JUDGE_COMPILER" -std=c++20 -O2 -pipe -static -o /workspace/probe /workspace/probe.cpp || die 'static C++20 probe compilation failed'
version="$(podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" run --userns=host --rm --pull=never --network=none --http-proxy=false --hosts-file=none \
  --read-only --cap-drop=all --security-opt=no-new-privileges --hooks-dir=/var/empty "$JUDGE_COMPILER_IMAGE" \
  "$JUDGE_COMPILER" -dumpfullversion -dumpversion)" || die 'compiler version probe failed'
[[ "$version" == "$JUDGE_COMPILER_VERSION" ]] || die 'compiler version differs from the lock'
elf_program_headers="$(podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" run --userns=host --rm --pull=never --network=none --http-proxy=false --hosts-file=none \
  --read-only --cap-drop=all --security-opt=no-new-privileges --hooks-dir=/var/empty --volume="$probe_directory:/workspace:ro,Z" \
  "$JUDGE_COMPILER_IMAGE" /usr/bin/readelf -l /workspace/probe)" || die 'static probe ELF inspection failed'
[[ "$elf_program_headers" != *INTERP* ]] || die 'compiler emitted an executable that requires a program interpreter'
elf_dynamic_section="$(podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" run --userns=host --rm --pull=never --network=none --http-proxy=false --hosts-file=none \
  --read-only --cap-drop=all --security-opt=no-new-privileges --hooks-dir=/var/empty --volume="$probe_directory:/workspace:ro,Z" \
  "$JUDGE_COMPILER_IMAGE" /usr/bin/readelf -d /workspace/probe)" || die 'static probe dynamic-section inspection failed'
[[ "$elf_dynamic_section" != *'(NEEDED)'* ]] || die 'compiler emitted an executable with a shared-library dependency'
podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" run --userns=host --rm --pull=never --network=none --http-proxy=false --hosts-file=none \
  --read-only --cap-drop=all --security-opt=no-new-privileges --hooks-dir=/var/empty --pids-limit=16 --memory=64m --cpus=1 \
  --volume="$probe_directory:/workspace:ro,Z" --entrypoint=/workspace/probe "$JUDGE_RUNTIME_IMAGE" || die 'static probe cannot execute in the empty runtime image'
probe_sha256="$(sha256sum "$probe_directory/probe" | awk '{print $1}')"
jq -cn --arg compilerImage "$JUDGE_COMPILER_IMAGE" --arg compilerConfigDigest "$JUDGE_COMPILER_CONFIG_DIGEST" \
  --arg runtimeImage "$JUDGE_RUNTIME_IMAGE" --arg runtimeConfigDigest "$JUDGE_RUNTIME_CONFIG_DIGEST" \
  --arg compiler "$JUDGE_COMPILER" --arg version "$version" --arg probeSHA256 "$probe_sha256" '
  {compiler:{configDigest:$compilerConfigDigest,image:$compilerImage,path:$compiler,version:$version},
   runtime:{configDigest:$runtimeConfigDigest,image:$runtimeImage,rootfsEntryCount:0},
   schema:"ascendany.judge-image-attestation.v2",staticProbeSHA256:$probeSHA256}
'
