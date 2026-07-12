#!/usr/bin/bash -p
set -Eeuo pipefail

export LC_ALL=C
export PATH=/usr/bin:/bin
readonly repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly identity_script="$repository_root/deploy/v2/scripts/trainer-runtime-tree-identity.sh"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

if [[ "${1:-}" != --inside-user-namespace && "$EUID" != 0 ]]; then
  exec unshare --user --map-root-user --mount --fork -- "$0" --inside-user-namespace
fi
[[ "$EUID" == 0 ]] || fail "tree identity fixture requires a mapped root identity"
mount --make-rprivate /

fixture_root="$(mktemp -d /tmp/ascendany-tree-identity-fixture.XXXXXXXX)"
readonly fixture_root
cleanup() {
  if mountpoint -q "$fixture_root/python/runtime-mount"; then
    umount -- "$fixture_root/python/runtime-mount"
  fi
  rm -rf --one-file-system -- "$fixture_root"
}
trap cleanup EXIT

install -d -m 0755 "$fixture_root/python/bin" "$fixture_root/python/lib"
printf '#!/usr/bin/bash\nexit 0\n' >"$fixture_root/python/bin/python3.14"
printf 'portable-runtime-fixture\n' >"$fixture_root/python/lib/runtime.txt"
chmod 0755 "$fixture_root/python/bin/python3.14"
chmod 0644 "$fixture_root/python/lib/runtime.txt"
ln -s -- ../lib/runtime.txt "$fixture_root/python/bin/runtime-link"

identity="$($identity_script "$fixture_root/python")" || fail "safe portable tree was rejected"
jq -e '
  .algorithm == "ascendany.portable-python-tree.v1" and
  .directories == 3 and .files == 2 and .symlinks == 1 and
  (.sha256 | test("^[0-9a-f]{64}$"))
' <<<"$identity" >/dev/null || fail "safe portable tree identity is invalid"

install -d -m 0755 "$fixture_root/python/runtime-mount"
mount -t tmpfs -o nodev,nosuid,noexec,size=1m tmpfs "$fixture_root/python/runtime-mount"
printf 'mounted escape\n' >"$fixture_root/python/runtime-mount/escape.txt"
chmod 0644 "$fixture_root/python/runtime-mount/escape.txt"
if "$identity_script" "$fixture_root/python" >"$fixture_root/unexpected.json" 2>"$fixture_root/rejection.log"; then
  fail "portable tree with a descendant mount was accepted"
fi
grep -F 'portable Python tree contains a descendant mount: runtime-mount' \
  "$fixture_root/rejection.log" >/dev/null || fail "descendant-mount rejection was not explicit"

printf 'trainer runtime tree identity fixture passed\n'
