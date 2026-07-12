#!/usr/bin/bash

set -euo pipefail

script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=judge-image-contract.sh
source "${script_directory}/judge-image-contract.sh"
load_judge_image_contract

die() {
  printf 'acquire Judge image: %s\n' "$1" >&2
  exit 1
}

usage() {
  printf 'usage: %s --output /absolute/image.oci.tar --sha256-output /absolute/image.oci.tar.sha256\n' "$0" >&2
}

[[ "$#" == 4 && "$1" == '--output' && "$3" == '--sha256-output' ]] || {
  usage
  exit 2
}
output="$2"
sha256_output="$4"
for path in "$output" "$sha256_output"; do
  [[ "$path" == /* && "$path" == "$(realpath -m -- "$path")" && "$path" != *:* && "$path" != *$'\n'* ]] ||
    die 'output paths must be canonical absolute paths without transport delimiters'
  [[ -d "$(dirname -- "$path")" && ! -L "$(dirname -- "$path")" ]] || die 'output parent must be an existing real directory'
  [[ ! -e "$path" && ! -L "$path" ]] || die "refusing to replace existing output: $path"
done
[[ "$output" != "$sha256_output" ]] || die 'archive and digest outputs must differ'
for command in curl jq sha256sum skopeo stat; do
  command -v "$command" >/dev/null || die "required command is missing: $command"
done

work_directory="$(mktemp -d)"
temporary_archive="${output}.partial.$$"
temporary_sha256="${sha256_output}.partial.$$"
cleanup() {
  rm -rf -- "$work_directory"
  rm -f -- "$temporary_archive" "$temporary_sha256"
}
trap cleanup EXIT

dockerfile_url="https://raw.githubusercontent.com/docker-library/gcc/${JUDGE_DOCKERFILE_REVISION}/${JUDGE_DOCKERFILE_PATH}"
curl --fail --location --silent --show-error --proto '=https' --tlsv1.2 \
  --output "$work_directory/Dockerfile" "$dockerfile_url"
[[ "$(sha256sum "$work_directory/Dockerfile" | awk '{print $1}')" == "$JUDGE_DOCKERFILE_SHA256" ]] ||
  die 'upstream Dockerfile differs from the reviewed revision identity'

skopeo inspect --raw "docker://${JUDGE_IMAGE_INDEX}" >"$work_directory/index.json"
[[ "sha256:$(sha256sum "$work_directory/index.json" | awk '{print $1}')" == "${JUDGE_IMAGE_INDEX##*@}" ]] ||
  die 'registry index bytes differ from the locked digest'
jq -e \
  --arg media_type "$JUDGE_IMAGE_INDEX_MEDIA_TYPE" \
  --arg os "$JUDGE_IMAGE_OS" \
  --arg architecture "$JUDGE_IMAGE_ARCHITECTURE" \
  --arg leaf_digest "${JUDGE_IMAGE_LEAF##*@}" \
  --argjson manifest_size "$JUDGE_IMAGE_MANIFEST_SIZE" \
  --arg revision "$JUDGE_DOCKERFILE_REVISION" \
  --arg source "${JUDGE_DOCKERFILE_REPOSITORY}#${JUDGE_DOCKERFILE_REVISION}:15" \
  --arg version "$JUDGE_IMAGE_TOOLCHAIN_VERSION" '
    .mediaType == $media_type and
    ([.manifests[] | select(.platform.os == $os and .platform.architecture == $architecture)] | length == 1) and
    ([.manifests[] | select(.platform.os == $os and .platform.architecture == $architecture)][0] |
      .digest == $leaf_digest and .size == $manifest_size and
      .annotations["org.opencontainers.image.revision"] == $revision and
      .annotations["org.opencontainers.image.source"] == $source and
      .annotations["org.opencontainers.image.version"] == $version)
  ' "$work_directory/index.json" >/dev/null || die 'registry index does not map the locked platform to the locked leaf'

skopeo inspect --raw "docker://${JUDGE_IMAGE_LEAF}" >"$work_directory/manifest.json"
[[ "sha256:$(sha256sum "$work_directory/manifest.json" | awk '{print $1}')" == "${JUDGE_IMAGE_LEAF##*@}" ]] ||
  die 'registry leaf manifest bytes differ from the locked digest'
jq -e \
  --arg media_type "$JUDGE_IMAGE_MANIFEST_MEDIA_TYPE" \
  --arg config_digest "$JUDGE_IMAGE_CONFIG_DIGEST" \
  --argjson config_size "$JUDGE_IMAGE_CONFIG_SIZE" '
    .schemaVersion == 2 and .mediaType == $media_type and
    .config.digest == $config_digest and .config.size == $config_size
  ' "$work_directory/manifest.json" >/dev/null || die 'leaf manifest config identity differs from the lock'

archive_reference='docker.io/library/gcc:ascendany-gcc-15.2.0-linux-amd64'
skopeo copy --preserve-digests --override-os "$JUDGE_IMAGE_OS" --override-arch "$JUDGE_IMAGE_ARCHITECTURE" \
  "docker://${JUDGE_IMAGE_LEAF}" "oci-archive:${temporary_archive}:${archive_reference}"
chmod 0644 -- "$temporary_archive"
archive_sha256="$(sha256sum "$temporary_archive" | awk '{print $1}')"
[[ "$archive_sha256" =~ ^[0-9a-f]{64}$ ]] || die 'archive digest generation failed'
printf '%s\n' "$archive_sha256" >"$temporary_sha256"
chmod 0644 -- "$temporary_sha256"
mv --no-target-directory -- "$temporary_archive" "$output"
mv --no-target-directory -- "$temporary_sha256" "$sha256_output"
trap - EXIT
rm -rf -- "$work_directory"
printf 'acquired %s as %s (sha256:%s)\n' "$JUDGE_IMAGE_LEAF" "$output" "$archive_sha256"
