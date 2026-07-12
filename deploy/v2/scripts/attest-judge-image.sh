#!/usr/bin/bash

set -euo pipefail

script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=judge-image-contract.sh
source "${script_directory}/judge-image-contract.sh"
load_judge_image_contract

die() {
  printf 'attest Judge image: %s\n' "$1" >&2
  exit 1
}

[[ "$#" == 0 ]] || die 'this command accepts no arguments'
for command in jq podman; do
  command -v "$command" >/dev/null || die "required command is missing: $command"
done
operator_runtime="/run/ascendany-judge-image-podman"
[[ "${XDG_RUNTIME_DIR-}" == "$operator_runtime" && -d "$operator_runtime" && ! -L "$operator_runtime" &&
   "$operator_runtime" == "$(realpath -e -- "$operator_runtime" 2>/dev/null || true)" ]] ||
  die 'XDG runtime differs from the dedicated image-operator boundary'
operator_runroot="$operator_runtime/containers"

inspect="$(podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" image inspect --format json "$JUDGE_IMAGE_LEAF")" ||
  die 'locked image is absent from the current Podman store'
jq -e \
  --arg leaf_digest "${JUDGE_IMAGE_LEAF##*@}" \
  --arg config_digest "$JUDGE_IMAGE_CONFIG_DIGEST" \
  --arg os "$JUDGE_IMAGE_OS" \
  --arg architecture "$JUDGE_IMAGE_ARCHITECTURE" '
    type == "array" and length == 1 and
    .[0].Digest == $leaf_digest and
    .[0].Id == ($config_digest | sub("^sha256:"; "")) and
    .[0].Os == $os and .[0].Architecture == $architecture
  ' <<<"$inspect" >/dev/null || die 'Podman image identity, config, or platform differs from the lock'

version="$(podman --cgroup-manager=cgroupfs --runroot="$operator_runroot" run --userns=host --rm --pull=never --network=none --http-proxy=false --hosts-file=none \
  --read-only --cap-drop=all --security-opt=no-new-privileges --hooks-dir=/var/empty \
  "$JUDGE_IMAGE_LEAF" "$JUDGE_IMAGE_COMPILER" -dumpfullversion -dumpversion)" ||
  die 'locked compiler path cannot execute in the image'
[[ "$version" == "$JUDGE_IMAGE_TOOLCHAIN_VERSION" ]] || die 'compiler version differs from the lock'
printf '{"architecture":"%s","compiler":"%s","configDigest":"%s","image":"%s","os":"%s","schema":"ascendany.judge-image-attestation.v1","version":"%s"}\n' \
  "$JUDGE_IMAGE_ARCHITECTURE" "$JUDGE_IMAGE_COMPILER" "$JUDGE_IMAGE_CONFIG_DIGEST" \
  "$JUDGE_IMAGE_LEAF" "$JUDGE_IMAGE_OS" "$version"
