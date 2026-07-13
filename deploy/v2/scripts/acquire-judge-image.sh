#!/usr/bin/bash

set -Eeuo pipefail

script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=judge-image-contract.sh
source "${script_directory}/judge-image-contract.sh"
load_judge_image_contract

die() { printf 'acquire Judge images: %s\n' "$1" >&2; exit 1; }
usage() {
  printf 'usage: %s --compiler-output /absolute/compiler.oci.tar --compiler-sha256-output /absolute/compiler.sha256 --runtime-output /absolute/runtime.oci.tar --runtime-sha256-output /absolute/runtime.sha256\n' "$0" >&2
}

[[ "$#" == 8 && "$1" == '--compiler-output' && "$3" == '--compiler-sha256-output' &&
   "$5" == '--runtime-output' && "$7" == '--runtime-sha256-output' ]] || { usage; exit 2; }
compiler_output="$2"; compiler_sha256_output="$4"; runtime_output="$6"; runtime_sha256_output="$8"
declare -a outputs=("$compiler_output" "$compiler_sha256_output" "$runtime_output" "$runtime_sha256_output")
[[ "${outputs[0]}" != "${outputs[1]}" && "${outputs[0]}" != "${outputs[2]}" && "${outputs[0]}" != "${outputs[3]}" &&
   "${outputs[1]}" != "${outputs[2]}" && "${outputs[1]}" != "${outputs[3]}" && "${outputs[2]}" != "${outputs[3]}" ]] ||
  die 'all output paths must differ'
for path in "${outputs[@]}"; do
  [[ "$path" == /* && "$path" == "$(realpath -m -- "$path")" && "$path" != *:* && "$path" != *$'\n'* ]] ||
    die 'output paths must be canonical absolute paths without transport delimiters'
  [[ -d "$(dirname -- "$path")" && ! -L "$(dirname -- "$path")" ]] || die 'output parent must be an existing real directory'
  [[ ! -e "$path" && ! -L "$path" ]] || die "refusing to replace existing output: $path"
done
for command in buildah jq podman sha256sum skopeo stat; do command -v "$command" >/dev/null || die "required command is missing: $command"; done
[[ "$(podman version --format '{{.Client.Version}}')" == "$JUDGE_PODMAN_VERSION" ]] || die 'Podman version differs from the reproducible-build lock'
[[ "$(buildah version | awk '$1 == "Version:" {print $2; exit}')" == "$JUDGE_BUILDAH_VERSION" ]] || die 'Buildah version differs from the reproducible-build lock'

work_directory="$(mktemp -d)"
compiler_archive="${compiler_output}.partial.$$"; compiler_digest_file="${compiler_sha256_output}.partial.$$"
runtime_archive="${runtime_output}.partial.$$"; runtime_digest_file="${runtime_sha256_output}.partial.$$"
cleanup() { rm -rf -- "$work_directory"; rm -f -- "$compiler_archive" "$compiler_digest_file" "$runtime_archive" "$runtime_digest_file"; }
trap cleanup EXIT

skopeo inspect --raw "docker://${JUDGE_SOURCE_INDEX}" >"$work_directory/index.json"
[[ "$(stat -Lc '%s' "$work_directory/index.json")" == "$JUDGE_SOURCE_INDEX_SIZE" &&
   "sha256:$(sha256sum "$work_directory/index.json" | awk '{print $1}')" == "${JUDGE_SOURCE_INDEX##*@}" ]] ||
  die 'Alpine registry index bytes differ from the lock'
jq -e --arg media "$JUDGE_SOURCE_INDEX_MEDIA_TYPE" --arg os "$JUDGE_IMAGE_OS" --arg arch "$JUDGE_IMAGE_ARCHITECTURE" \
  --arg leaf "${JUDGE_SOURCE_LEAF##*@}" --argjson size "$JUDGE_SOURCE_MANIFEST_SIZE" '
    .mediaType == $media and
    ([.manifests[] | select(.platform.os == $os and .platform.architecture == $arch)] | length == 1) and
    ([.manifests[] | select(.platform.os == $os and .platform.architecture == $arch)][0] | .digest == $leaf and .size == $size)
  ' "$work_directory/index.json" >/dev/null || die 'Alpine index platform mapping differs from the lock'
skopeo inspect --raw "docker://${JUDGE_SOURCE_LEAF}" >"$work_directory/manifest.json"
[[ "$(stat -Lc '%s' "$work_directory/manifest.json")" == "$JUDGE_SOURCE_MANIFEST_SIZE" &&
   "sha256:$(sha256sum "$work_directory/manifest.json" | awk '{print $1}')" == "${JUDGE_SOURCE_LEAF##*@}" ]] ||
  die 'Alpine leaf manifest bytes differ from the lock'
jq -e --arg media "$JUDGE_SOURCE_MANIFEST_MEDIA_TYPE" --arg config "$JUDGE_SOURCE_CONFIG_DIGEST" \
  --argjson config_size "$JUDGE_SOURCE_CONFIG_SIZE" --arg layer "$JUDGE_SOURCE_LAYER_DIGEST" \
  --arg layer_media "$JUDGE_SOURCE_LAYER_MEDIA_TYPE" --argjson layer_size "$JUDGE_SOURCE_LAYER_SIZE" '
    .schemaVersion == 2 and .mediaType == $media and
    .config == {"digest":$config,"mediaType":"application/vnd.oci.image.config.v1+json","size":$config_size} and
    .layers == [{"digest":$layer,"mediaType":$layer_media,"size":$layer_size}]
  ' "$work_directory/manifest.json" >/dev/null || die 'Alpine leaf closure differs from the lock'

podman pull --quiet "$JUDGE_SOURCE_LEAF" >/dev/null || die 'locked Alpine leaf could not be acquired'
build_image() {
  local target="$1" tag="$2" expected="$3"
  podman build --jobs 1 --network host --pull=never --no-cache --layers=false --squash-all --timestamp 0 --unsetenv PATH \
    --format oci --target "$target" --tag "$tag" --file "$JUDGE_CONTAINERFILE_PATH" "$JUDGE_RELEASE_ROOT" >/dev/null ||
    die "$target image build failed"
  local inspect
  inspect="$(podman image inspect --format json "$tag")" || die "$target image inspection failed"
  jq -e --arg digest "${expected##*@}" --arg os "$JUDGE_IMAGE_OS" --arg arch "$JUDGE_IMAGE_ARCHITECTURE" '
    type == "array" and length == 1 and .[0].Digest == $digest and .[0].Os == $os and .[0].Architecture == $arch
  ' <<<"$inspect" >/dev/null || die "$target image identity differs from the lock"
}
build_image compiler localhost/ascendany-judge-compiler:contract-v2 "$JUDGE_COMPILER_IMAGE"
build_image runtime localhost/ascendany-judge-runtime:contract-v2 "$JUDGE_RUNTIME_IMAGE"

skopeo copy --preserve-digests "containers-storage:${JUDGE_COMPILER_IMAGE}" \
  "oci-archive:${compiler_archive}:localhost/ascendany-judge-compiler:contract-v2" >/dev/null
skopeo copy --preserve-digests "containers-storage:${JUDGE_RUNTIME_IMAGE}" \
  "oci-archive:${runtime_archive}:localhost/ascendany-judge-runtime:contract-v2" >/dev/null
for pair in "$compiler_archive:$compiler_digest_file" "$runtime_archive:$runtime_digest_file"; do
  archive="${pair%%:*}"; digest_file="${pair##*:}"
  chmod 0644 -- "$archive"
  sha256sum "$archive" | awk '{print $1}' >"$digest_file"
  chmod 0644 -- "$digest_file"
done
mv --no-target-directory -- "$compiler_archive" "$compiler_output"
mv --no-target-directory -- "$compiler_digest_file" "$compiler_sha256_output"
mv --no-target-directory -- "$runtime_archive" "$runtime_output"
mv --no-target-directory -- "$runtime_digest_file" "$runtime_sha256_output"
trap - EXIT
rm -rf -- "$work_directory"
printf 'acquired compiler %s and runtime %s\n' "$JUDGE_COMPILER_IMAGE" "$JUDGE_RUNTIME_IMAGE"
