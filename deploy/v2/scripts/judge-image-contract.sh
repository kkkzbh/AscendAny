#!/usr/bin/bash

set -euo pipefail

judge_image_contract_die() {
  printf 'judge image contract: %s\n' "$1" >&2
  return 1
}

load_judge_image_contract() {
  local script_directory release_root lock_path containerfile_path inventory_path
  local -a values=()
  script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
  release_root="$(cd -- "${script_directory}/.." && pwd -P)"
  lock_path="${release_root}/config/judge-image-lock.json"
  [[ -f "$lock_path" && ! -L "$lock_path" ]] ||
    judge_image_contract_die 'release-bound lock is missing or non-regular'
  command -v jq >/dev/null || judge_image_contract_die 'jq is required'

  jq -e '
    type == "object" and keys == ["build", "compiler", "runtime", "schema", "source"] and
    .schema == "ascendany.judge-image-lock.v2" and
    (.build | type == "object" and
      keys == ["buildahVersion", "containerfilePath", "containerfileSHA256", "format", "podmanVersion", "sourceDateEpoch"] and
      .containerfilePath == "config/judge-images.Containerfile" and
      (.containerfileSHA256 | test("^[0-9a-f]{64}$")) and .format == "oci" and
      (.buildahVersion | test("^[0-9]+[.][0-9]+[.][0-9]+$")) and
      (.podmanVersion | test("^[0-9]+[.][0-9]+[.][0-9]+$")) and .sourceDateEpoch == 0) and
    (.source | type == "object" and
      keys == ["architecture", "configDigest", "configSize", "index", "indexMediaType", "indexSize", "leaf", "manifestMediaType", "manifestSize", "os", "release", "rootfsLayerDigest", "rootfsLayerMediaType", "rootfsLayerSize"] and
      .architecture == "amd64" and .os == "linux" and .release == "3.23.5" and
      (.index | test("^docker[.]io/library/alpine@sha256:[0-9a-f]{64}$")) and
      (.leaf | test("^docker[.]io/library/alpine@sha256:[0-9a-f]{64}$")) and .index != .leaf and
      .indexMediaType == "application/vnd.oci.image.index.v1+json" and
      .manifestMediaType == "application/vnd.oci.image.manifest.v1+json" and
      .rootfsLayerMediaType == "application/vnd.oci.image.layer.v1.tar+gzip" and
      ([.configDigest, .rootfsLayerDigest][] | test("^sha256:[0-9a-f]{64}$")) and
      ([.configSize, .indexSize, .manifestSize, .rootfsLayerSize][] | type == "number" and floor == . and . > 0)) and
    (.compiler | type == "object" and
      keys == ["architecture", "configDigest", "configSize", "identity", "manifestMediaType", "manifestSize", "os", "packages", "rootfs", "toolchain"] and
      .architecture == "amd64" and .os == "linux" and
      (.identity | test("^localhost/ascendany-judge-compiler@sha256:[0-9a-f]{64}$")) and
      (.configDigest | test("^sha256:[0-9a-f]{64}$")) and .configSize > 0 and .manifestSize > 0 and
      .manifestMediaType == "application/vnd.oci.image.manifest.v1+json" and
      (.packages | type == "array" and length > 0 and . == (sort | unique) and
        all(.[]; type == "string" and test("^[a-z0-9+_-]+-[0-9][a-zA-Z0-9._-]*-r[0-9]+$") and
          (test("python|trainer|cuda|nvidia|rocm|opencl|accelerator"; "i") | not))) and
      .toolchain == {"compiler":"/usr/bin/g++","package":"g++","packageVersion":"15.2.0-r2","version":"15.2.0"} and
      (.rootfs | type == "object" and
        keys == ["entryCount", "inventoryPath", "inventorySHA256", "layerDigest", "layerMediaType", "layerSize"] and
        .entryCount > 0 and .inventoryPath == "config/judge-compiler-rootfs.inventory" and
        (.inventorySHA256 | test("^[0-9a-f]{64}$")) and
        (.layerDigest | test("^sha256:[0-9a-f]{64}$")) and
        .layerMediaType == "application/vnd.oci.image.layer.v1.tar" and .layerSize > 0)) and
    (.runtime | type == "object" and
      keys == ["architecture", "configDigest", "configSize", "identity", "manifestMediaType", "manifestSize", "os", "rootfs"] and
      .architecture == "amd64" and .os == "linux" and
      (.identity | test("^localhost/ascendany-judge-runtime@sha256:[0-9a-f]{64}$")) and
      (.configDigest | test("^sha256:[0-9a-f]{64}$")) and .configSize > 0 and .manifestSize > 0 and
      .manifestMediaType == "application/vnd.oci.image.manifest.v1+json" and
      (.rootfs | type == "object" and
        keys == ["entryCount", "inventorySHA256", "layerDigest", "layerMediaType", "layerSize"] and
        .entryCount == 0 and .inventorySHA256 == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" and
        (.layerDigest | test("^sha256:[0-9a-f]{64}$")) and
        .layerMediaType == "application/vnd.oci.image.layer.v1.tar" and .layerSize > 0)) and
    .compiler.identity != .runtime.identity
  ' "$lock_path" >/dev/null || judge_image_contract_die 'release-bound lock violates its closed v2 schema'

  mapfile -t values < <(jq -r '
    .build.containerfilePath, .build.containerfileSHA256, .build.podmanVersion, .build.buildahVersion,
    .source.index, .source.indexMediaType, (.source.indexSize|tostring), .source.leaf,
    .source.manifestMediaType, (.source.manifestSize|tostring), .source.configDigest,
    (.source.configSize|tostring), .source.rootfsLayerDigest, .source.rootfsLayerMediaType,
    (.source.rootfsLayerSize|tostring), .source.os, .source.architecture,
    .compiler.identity, .compiler.configDigest, (.compiler.configSize|tostring),
    .compiler.manifestMediaType, (.compiler.manifestSize|tostring), .compiler.toolchain.compiler,
    .compiler.toolchain.version, .compiler.rootfs.inventoryPath, .compiler.rootfs.inventorySHA256,
    (.compiler.rootfs.entryCount|tostring), .compiler.rootfs.layerDigest,
    .runtime.identity, .runtime.configDigest, (.runtime.configSize|tostring),
    .runtime.manifestMediaType, (.runtime.manifestSize|tostring), .runtime.rootfs.inventorySHA256,
    (.runtime.rootfs.entryCount|tostring), .runtime.rootfs.layerDigest
  ' "$lock_path")
  (( ${#values[@]} == 36 )) || judge_image_contract_die 'release-bound lock field extraction failed'

  JUDGE_CONTAINERFILE_RELATIVE="${values[0]}"; JUDGE_CONTAINERFILE_SHA256="${values[1]}"
  JUDGE_PODMAN_VERSION="${values[2]}"; JUDGE_BUILDAH_VERSION="${values[3]}"
  JUDGE_SOURCE_INDEX="${values[4]}"; JUDGE_SOURCE_INDEX_MEDIA_TYPE="${values[5]}"; JUDGE_SOURCE_INDEX_SIZE="${values[6]}"
  JUDGE_SOURCE_LEAF="${values[7]}"; JUDGE_SOURCE_MANIFEST_MEDIA_TYPE="${values[8]}"; JUDGE_SOURCE_MANIFEST_SIZE="${values[9]}"
  JUDGE_SOURCE_CONFIG_DIGEST="${values[10]}"; JUDGE_SOURCE_CONFIG_SIZE="${values[11]}"
  JUDGE_SOURCE_LAYER_DIGEST="${values[12]}"; JUDGE_SOURCE_LAYER_MEDIA_TYPE="${values[13]}"; JUDGE_SOURCE_LAYER_SIZE="${values[14]}"
  JUDGE_IMAGE_OS="${values[15]}"; JUDGE_IMAGE_ARCHITECTURE="${values[16]}"
  JUDGE_COMPILER_IMAGE="${values[17]}"; JUDGE_COMPILER_CONFIG_DIGEST="${values[18]}"; JUDGE_COMPILER_CONFIG_SIZE="${values[19]}"
  JUDGE_COMPILER_MANIFEST_MEDIA_TYPE="${values[20]}"; JUDGE_COMPILER_MANIFEST_SIZE="${values[21]}"
  JUDGE_COMPILER="${values[22]}"; JUDGE_COMPILER_VERSION="${values[23]}"
  JUDGE_COMPILER_INVENTORY_RELATIVE="${values[24]}"; JUDGE_COMPILER_INVENTORY_SHA256="${values[25]}"
  JUDGE_COMPILER_ROOTFS_ENTRY_COUNT="${values[26]}"; JUDGE_COMPILER_LAYER_DIGEST="${values[27]}"
  JUDGE_RUNTIME_IMAGE="${values[28]}"; JUDGE_RUNTIME_CONFIG_DIGEST="${values[29]}"; JUDGE_RUNTIME_CONFIG_SIZE="${values[30]}"
  JUDGE_RUNTIME_MANIFEST_MEDIA_TYPE="${values[31]}"; JUDGE_RUNTIME_MANIFEST_SIZE="${values[32]}"
  JUDGE_RUNTIME_INVENTORY_SHA256="${values[33]}"; JUDGE_RUNTIME_ROOTFS_ENTRY_COUNT="${values[34]}"; JUDGE_RUNTIME_LAYER_DIGEST="${values[35]}"
  JUDGE_IMAGE_LOCK_PATH="$lock_path"; JUDGE_RELEASE_ROOT="$release_root"

  containerfile_path="${release_root}/${JUDGE_CONTAINERFILE_RELATIVE}"
  inventory_path="${release_root}/${JUDGE_COMPILER_INVENTORY_RELATIVE}"
  [[ -f "$containerfile_path" && ! -L "$containerfile_path" &&
     "$(sha256sum "$containerfile_path" | awk '{print $1}')" == "$JUDGE_CONTAINERFILE_SHA256" ]] ||
    judge_image_contract_die 'release-bound Containerfile bytes differ from the lock'
  [[ -f "$inventory_path" && ! -L "$inventory_path" &&
     "$(sha256sum "$inventory_path" | awk '{print $1}')" == "$JUDGE_COMPILER_INVENTORY_SHA256" &&
     "$(wc -l <"$inventory_path" | tr -d ' ')" == "$JUDGE_COMPILER_ROOTFS_ENTRY_COUNT" ]] ||
    judge_image_contract_die 'compiler rootfs inventory differs from the lock'
  LC_ALL=C cut -d'|' -f4 "$inventory_path" | sort -c -u 2>/dev/null ||
    judge_image_contract_die 'compiler rootfs inventory is not strictly path-sorted'
  awk -F'|' '
    NF != 4 { exit 1 }
    $1 !~ /^[dfl]$/ || $2 !~ /^[0-7][0-7][0-7][0-7][0-7]?$/ { exit 1 }
    length($3) != 64 || $3 !~ /^[0-9a-f]+$/ { exit 1 }
    $4 == "" || $4 ~ /^\// || $4 ~ /(^|\/)\.{1,2}(\/|$)/ || $4 ~ /\/\// { exit 1 }
  ' "$inventory_path" || judge_image_contract_die 'compiler rootfs inventory has an invalid entry'
  ! grep -Eiq 'python|trainer|cuda|nvidia|rocm|opencl|accelerator' "$inventory_path" ||
    judge_image_contract_die 'compiler rootfs inventory contains a prohibited production path'

  JUDGE_CONTAINERFILE_PATH="$containerfile_path"; JUDGE_COMPILER_INVENTORY_PATH="$inventory_path"
  readonly JUDGE_CONTAINERFILE_RELATIVE JUDGE_CONTAINERFILE_PATH JUDGE_CONTAINERFILE_SHA256 JUDGE_PODMAN_VERSION JUDGE_BUILDAH_VERSION
  readonly JUDGE_SOURCE_INDEX JUDGE_SOURCE_INDEX_MEDIA_TYPE JUDGE_SOURCE_INDEX_SIZE JUDGE_SOURCE_LEAF JUDGE_SOURCE_MANIFEST_MEDIA_TYPE JUDGE_SOURCE_MANIFEST_SIZE
  readonly JUDGE_SOURCE_CONFIG_DIGEST JUDGE_SOURCE_CONFIG_SIZE JUDGE_SOURCE_LAYER_DIGEST JUDGE_SOURCE_LAYER_MEDIA_TYPE JUDGE_SOURCE_LAYER_SIZE
  readonly JUDGE_IMAGE_OS JUDGE_IMAGE_ARCHITECTURE JUDGE_COMPILER_IMAGE JUDGE_COMPILER_CONFIG_DIGEST JUDGE_COMPILER_CONFIG_SIZE
  readonly JUDGE_COMPILER_MANIFEST_MEDIA_TYPE JUDGE_COMPILER_MANIFEST_SIZE JUDGE_COMPILER JUDGE_COMPILER_VERSION
  readonly JUDGE_COMPILER_INVENTORY_RELATIVE JUDGE_COMPILER_INVENTORY_PATH JUDGE_COMPILER_INVENTORY_SHA256 JUDGE_COMPILER_ROOTFS_ENTRY_COUNT JUDGE_COMPILER_LAYER_DIGEST
  readonly JUDGE_RUNTIME_IMAGE JUDGE_RUNTIME_CONFIG_DIGEST JUDGE_RUNTIME_CONFIG_SIZE JUDGE_RUNTIME_MANIFEST_MEDIA_TYPE JUDGE_RUNTIME_MANIFEST_SIZE
  readonly JUDGE_RUNTIME_INVENTORY_SHA256 JUDGE_RUNTIME_ROOTFS_ENTRY_COUNT JUDGE_RUNTIME_LAYER_DIGEST JUDGE_IMAGE_LOCK_PATH JUDGE_RELEASE_ROOT
}
