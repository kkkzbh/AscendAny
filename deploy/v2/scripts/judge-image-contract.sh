#!/usr/bin/bash

set -euo pipefail

judge_image_contract_die() {
  printf 'judge image contract: %s\n' "$1" >&2
  return 1
}

load_judge_image_contract() {
  local script_directory lock_path
  local -a values=()
  script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
  lock_path="${script_directory}/../config/judge-image-lock.json"
  [[ -f "$lock_path" && ! -L "$lock_path" ]] ||
    judge_image_contract_die 'release-bound lock is missing or non-regular'
  command -v jq >/dev/null || judge_image_contract_die 'jq is required'

  jq -e '
    type == "object" and
    keys == ["dockerfile", "image", "platform", "schema", "toolchain"] and
    .schema == "ascendany.judge-image-lock.v1" and
    (.dockerfile | type == "object" and
      keys == ["path", "repository", "revision", "sha256"] and
      .path == "15/Dockerfile" and
      .repository == "https://github.com/docker-library/gcc.git" and
      (.revision | test("^[0-9a-f]{40}$")) and
      (.sha256 | test("^[0-9a-f]{64}$"))) and
    (.image | type == "object" and
      keys == ["configDigest", "configSize", "index", "indexMediaType", "leaf", "manifestMediaType", "manifestSize"] and
      (.configDigest | test("^sha256:[0-9a-f]{64}$")) and
      (.configSize | type == "number" and floor == . and . > 0 and . <= 1048576) and
      (.index | test("^docker[.]io/library/gcc@sha256:[0-9a-f]{64}$")) and
      .indexMediaType == "application/vnd.oci.image.index.v1+json" and
      (.leaf | test("^docker[.]io/library/gcc@sha256:[0-9a-f]{64}$")) and
      .manifestMediaType == "application/vnd.oci.image.manifest.v1+json" and
      (.manifestSize | type == "number" and floor == . and . > 0 and . <= 1048576)) and
    .platform == {"architecture":"amd64","os":"linux"} and
    .toolchain == {"compiler":"/usr/local/bin/g++","version":"15.2.0"}
  ' "$lock_path" >/dev/null || judge_image_contract_die 'release-bound lock violates its closed schema'

  mapfile -t values < <(jq -r '
    .dockerfile.repository,
    .dockerfile.revision,
    .dockerfile.path,
    .dockerfile.sha256,
    .image.index,
    .image.indexMediaType,
    .image.leaf,
    .image.manifestMediaType,
    (.image.manifestSize | tostring),
    .image.configDigest,
    (.image.configSize | tostring),
    .platform.os,
    .platform.architecture,
    .toolchain.compiler,
    .toolchain.version
  ' "$lock_path")
  (( ${#values[@]} == 15 )) || judge_image_contract_die 'release-bound lock field extraction failed'

  JUDGE_DOCKERFILE_REPOSITORY="${values[0]}"
  JUDGE_DOCKERFILE_REVISION="${values[1]}"
  JUDGE_DOCKERFILE_PATH="${values[2]}"
  JUDGE_DOCKERFILE_SHA256="${values[3]}"
  JUDGE_IMAGE_INDEX="${values[4]}"
  JUDGE_IMAGE_INDEX_MEDIA_TYPE="${values[5]}"
  JUDGE_IMAGE_LEAF="${values[6]}"
  JUDGE_IMAGE_MANIFEST_MEDIA_TYPE="${values[7]}"
  JUDGE_IMAGE_MANIFEST_SIZE="${values[8]}"
  JUDGE_IMAGE_CONFIG_DIGEST="${values[9]}"
  JUDGE_IMAGE_CONFIG_SIZE="${values[10]}"
  JUDGE_IMAGE_OS="${values[11]}"
  JUDGE_IMAGE_ARCHITECTURE="${values[12]}"
  JUDGE_IMAGE_COMPILER="${values[13]}"
  JUDGE_IMAGE_TOOLCHAIN_VERSION="${values[14]}"
  JUDGE_IMAGE_LOCK_PATH="$lock_path"
  readonly JUDGE_DOCKERFILE_REPOSITORY JUDGE_DOCKERFILE_REVISION JUDGE_DOCKERFILE_PATH JUDGE_DOCKERFILE_SHA256
  readonly JUDGE_IMAGE_INDEX JUDGE_IMAGE_INDEX_MEDIA_TYPE JUDGE_IMAGE_LEAF JUDGE_IMAGE_MANIFEST_MEDIA_TYPE
  readonly JUDGE_IMAGE_MANIFEST_SIZE JUDGE_IMAGE_CONFIG_DIGEST JUDGE_IMAGE_CONFIG_SIZE
  readonly JUDGE_IMAGE_OS JUDGE_IMAGE_ARCHITECTURE JUDGE_IMAGE_COMPILER JUDGE_IMAGE_TOOLCHAIN_VERSION JUDGE_IMAGE_LOCK_PATH
}
