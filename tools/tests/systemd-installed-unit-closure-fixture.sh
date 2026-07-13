#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly DEPLOY_UNIT_SOURCE="$REPOSITORY_ROOT/deploy/v2/systemd"
readonly INSTALLER_FIXTURE="$REPOSITORY_ROOT/tools/tests/install-v2-release-fixture.sh"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-systemd-unit-closure.XXXXXX")"
readonly INSTALLED_RELEASE="$WORK_ROOT/installed-release"
readonly UNIT_SOURCE="$INSTALLED_RELEASE/systemd"
readonly FAKE_ROOT="$WORK_ROOT/root"
readonly ANALYZE_LOG="$WORK_ROOT/systemd-analyze.log"
readonly -a UNIT_NAMES=(
  ascendany-admin-bootstrap.service
  ascendany-backup.service
  ascendany-backup.timer
  ascendany-cloudflared.service
  ascendany-judge@.service
  ascendany-lsp@.service
  ascendany-migrate.service
  ascendany-model-activate.service
  ascendany-pgbouncer.service
  ascendany-restore-verify@.service
  ascendanyd.service
)
readonly -a RELEASE_EXECUTABLES=(
  /opt/ascendany/v2/bin/ascendany-admin-bootstrap
  /opt/ascendany/v2/bin/ascendany-backup
  /opt/ascendany/v2/bin/ascendany-judge
  /opt/ascendany/v2/bin/ascendany-lsp
  /opt/ascendany/v2/bin/ascendany-migrate
  /opt/ascendany/v2/bin/ascendany-model
  /opt/ascendany/v2/bin/ascendanyd
  /opt/ascendany/v2/scripts/publish-restore-evidence.sh
  /opt/ascendany/v2/scripts/restore-verify-operator.sh
)
readonly -a NATIVE_RUNTIME_EXECUTABLES=(
  /usr/bin/cloudflared
  /usr/bin/pgbouncer
)

cleanup() {
  rm -rf -- "$WORK_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for command in jq sha256sum systemd-analyze; do
  command -v "$command" >/dev/null 2>&1 || fail "required fixture command is missing: $command"
done
[[ -x "$INSTALLER_FIXTURE" ]] || fail 'release installer fixture is missing'
[[ -d "$DEPLOY_UNIT_SOURCE" ]] || fail 'deployment systemd source is missing'
"$INSTALLER_FIXTURE" --materialize-installed-tree "$INSTALLED_RELEASE" >/dev/null
[[ -d "$UNIT_SOURCE" && -f "$INSTALLED_RELEASE/release-manifest.json" ]] ||
  fail 'materialized installed release is incomplete'
diff -r -- "$DEPLOY_UNIT_SOURCE" "$UNIT_SOURCE" >/dev/null ||
  fail 'installed manifest-bound systemd tree differs from deployment sources'
jq -e '
  .schema == "ascendany.release.v2" and
  .purpose == "production" and
  (.files | length == 59) and
  any(.files[]; .path == "bin/ascendany-release-ops" and .mode == "0755")
' "$INSTALLED_RELEASE/release-manifest.json" >/dev/null ||
  fail 'installed release manifest does not carry the exact native-helper release contract'
mapfile -t actual_unit_names < <(
  find "$UNIT_SOURCE" -mindepth 1 -maxdepth 1 -type f \
    \( -name '*.service' -o -name '*.timer' \) -printf '%f\n' |
    LC_ALL=C sort
)
mapfile -t expected_unit_names < <(printf '%s\n' "${UNIT_NAMES[@]}" | LC_ALL=C sort)
if [[ "$(printf '%s\n' "${actual_unit_names[@]}")" != "$(printf '%s\n' "${expected_unit_names[@]}")" ]]; then
  diff -u \
    <(printf '%s\n' "${expected_unit_names[@]}") \
    <(printf '%s\n' "${actual_unit_names[@]}") >&2 || true
  fail 'deployment unit source set drifted without fixture ownership'
fi
mapfile -t actual_dropins < <(
  find "$UNIT_SOURCE" -mindepth 2 -type f -printf '%P\n' | LC_ALL=C sort
)
[[ "$(printf '%s\n' "${actual_dropins[@]}")" == 'ascendanyd.service.d/40-read-only-smoke.conf' ]] ||
  fail 'deployment drop-in source set drifted without fixture ownership'

install -d -m 0755 \
  "$FAKE_ROOT/etc/systemd/system/ascendanyd.service.d" \
  "$FAKE_ROOT/usr/lib/systemd/system/service.d" \
  "$FAKE_ROOT/etc/ascendany/v2" \
  "$FAKE_ROOT/etc/ascendany/credentials" \
  "$FAKE_ROOT/opt/ascendany/infra/pgbouncer" \
  "$FAKE_ROOT/opt/ascendany/v2/config" \
  "$FAKE_ROOT/run/ascendany" \
  "$FAKE_ROOT/run/ascendany-lsp-control" \
  "$FAKE_ROOT/run/ascendany-restore-operator" \
  "$FAKE_ROOT/run/ascendany-admin-bootstrap-input" \
  "$FAKE_ROOT/run/postgresql" \
  "$FAKE_ROOT/var/empty" \
  "$FAKE_ROOT/var/lib/ascendany/artifacts" \
  "$FAKE_ROOT/var/lib/ascendany-judge" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/dev" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/etc" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/home" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/opt/ascendany/v2/bin" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/proc" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/run/ascendany-lsp-control" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/sys" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/tmp" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/usr" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/var" \
  "$FAKE_ROOT/var/lib/ascendany-migrate" \
  "$FAKE_ROOT/var/lib/ascendany-restore" \
  "$FAKE_ROOT/var/backups/ascendany" \
  "$FAKE_ROOT/tmp/ascendany-lsp-sessions"

for unit in "${UNIT_NAMES[@]}"; do
  [[ -f "$UNIT_SOURCE/$unit" && ! -L "$UNIT_SOURCE/$unit" ]] || fail "unit source is missing or symbolic: $unit"
  install -m 0644 -- "$UNIT_SOURCE/$unit" "$FAKE_ROOT/etc/systemd/system/$unit"
done
install -m 0644 -- \
  "$UNIT_SOURCE/ascendanyd.service.d/40-read-only-smoke.conf" \
  "$FAKE_ROOT/etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf"
printf '%s\n' \
  '# Fedora package global service timeout contract.' \
  '[Service]' \
  'TimeoutStopFailureMode=abort' \
  >"$FAKE_ROOT/usr/lib/systemd/system/service.d/10-timeout-abort.conf"
chmod 0644 "$FAKE_ROOT/usr/lib/systemd/system/service.d/10-timeout-abort.conf"

cat >"$FAKE_ROOT/etc/passwd" <<'PASSWD'
root:x:0:0:root:/root:/usr/sbin/nologin
ascendany:x:1001:1001:AscendAny:/var/lib/ascendany:/usr/sbin/nologin
ascendany-backup:x:1002:1002:AscendAny backup:/var/backups/ascendany:/usr/sbin/nologin
ascendany-judge:x:1003:1003:AscendAny judge:/var/lib/ascendany-judge:/usr/sbin/nologin
ascendany-lsp:x:1004:1004:AscendAny LSP:/var/empty:/usr/sbin/nologin
ascendany-migrator:x:1005:1005:AscendAny migrator:/var/lib/ascendany-migrate:/usr/sbin/nologin
ascendany-restore:x:1006:1006:AscendAny restore:/var/lib/ascendany-restore:/usr/sbin/nologin
PASSWD
cat >"$FAKE_ROOT/etc/group" <<'GROUP'
root:x:0:
ascendany:x:1001:ascendany-backup
ascendany-backup-readers:x:1002:ascendany-backup,ascendany-restore
ascendany-runtime:x:1003:ascendany,ascendany-judge
ascendany-lsp-control:x:1009:ascendany,ascendany-lsp
ascendany-judge:x:1004:
ascendany-lsp:x:1005:
ascendany-migrator:x:1006:
ascendany-restore:x:1007:
GROUP
chmod 0644 "$FAKE_ROOT/etc/passwd" "$FAKE_ROOT/etc/group"

install -m 0644 -- \
  "$INSTALLED_RELEASE/config/cloudflared.yaml" \
  "$FAKE_ROOT/opt/ascendany/v2/config/cloudflared.yaml"
install -m 0644 -- \
  "$INSTALLED_RELEASE/config/pgbouncer.ini" \
  "$FAKE_ROOT/opt/ascendany/infra/pgbouncer/pgbouncer.ini"
install -m 0644 -- \
  "$INSTALLED_RELEASE/config/pgbouncer-hba.conf" \
  "$FAKE_ROOT/opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf"
printf '%s\n' 'fixture encrypted cloudflared credential' \
  >"$FAKE_ROOT/etc/ascendany/credentials/cloudflare_tunnel_credentials.cred"
printf '%s\n' 'fixture encrypted PgBouncer userlist credential' \
  >"$FAKE_ROOT/etc/ascendany/credentials/pgbouncer_userlist.cred"
chmod 0400 \
  "$FAKE_ROOT/etc/ascendany/credentials/cloudflare_tunnel_credentials.cred" \
  "$FAKE_ROOT/etc/ascendany/credentials/pgbouncer_userlist.cred"

for unit_contract in \
  'ascendany-cloudflared.service|cloudflared|AssertFileIsExecutable=/usr/bin/cloudflared|AssertPathExists=/opt/ascendany/v2/config/cloudflared.yaml|LoadCredentialEncrypted=tunnel_credentials:/etc/ascendany/credentials/cloudflare_tunnel_credentials.cred' \
  'ascendany-pgbouncer.service|pgbouncer|AssertFileIsExecutable=/usr/bin/pgbouncer|AssertPathExists=/opt/ascendany/infra/pgbouncer/pgbouncer.ini|LoadCredentialEncrypted=pgbouncer_userlist:/etc/ascendany/credentials/pgbouncer_userlist.cred'; do
  IFS='|' read -r unit package executable_assert config_assert credential_directive <<<"$unit_contract"
  [[ "$(grep -Fxc -- "$executable_assert" "$UNIT_SOURCE/$unit")" == "1" ]] ||
    fail "$unit does not own its exact native host executable prerequisite"
  [[ "$(grep -Fxc -- "$config_assert" "$UNIT_SOURCE/$unit")" == "1" ]] ||
    fail "$unit does not own its exact immutable configuration prerequisite"
  [[ "$(grep -Fxc -- "$credential_directive" "$UNIT_SOURCE/$unit")" == "1" ]] ||
    fail "$unit does not own its exact encrypted credential prerequisite"
  [[ "$(grep -Ec '^TimeoutStopFailureMode=' "$UNIT_SOURCE/$unit")" == "0" ]] ||
    fail "$unit overrides the Fedora global timeout-abort contract"
  executable="${executable_assert#AssertFileIsExecutable=}"
  jq -e --arg package "$package" --arg executable "$executable" '
    .schema == "ascendany.fedora-runtime-packages.v1" and
    (.packages[$package].files | length == 1) and
    .packages[$package].files[0].path == $executable and
    .packages[$package].files[0].owner == "root" and
    .packages[$package].files[0].group == "root" and
    .packages[$package].files[0].mode == "0755" and
    (.packages[$package].files[0].sha256 | test("^[0-9a-f]{64}$")) and
    (.packages[$package].files[0].size | type == "number" and . > 0)
  ' "$REPOSITORY_ROOT/deploy/v2/config/fedora-runtime-packages.json" >/dev/null ||
    fail "$unit native host executable is absent from the Fedora runtime lock"
done
[[ "$(grep -Fxc -- 'AssertPathExists=/opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf' \
  "$UNIT_SOURCE/ascendany-pgbouncer.service")" == "1" ]] ||
  fail 'ascendany-pgbouncer.service does not assert its exact HBA prerequisite'

for direct_postgres_unit in \
  ascendany-pgbouncer.service \
  ascendany-migrate.service \
  ascendany-model-activate.service \
  ascendany-backup.service \
  'ascendany-restore-verify@.service'; do
  [[ "$(grep -Fxc -- 'IPAddressAllow=localhost' "$UNIT_SOURCE/$direct_postgres_unit")" == "1" &&
     "$(grep -Fxc -- 'IPAddressAllow=10.88.0.2/32' "$UNIT_SOURCE/$direct_postgres_unit")" == "1" ]] ||
    fail "$direct_postgres_unit does not admit the exact loopback and Podman-NAT database path"
done

ln -s usr/bin "$FAKE_ROOT/var/lib/ascendany-lsp-root/bin"
ln -s usr/lib "$FAKE_ROOT/var/lib/ascendany-lsp-root/lib"
ln -s usr/lib64 "$FAKE_ROOT/var/lib/ascendany-lsp-root/lib64"
: >"$FAKE_ROOT/run/ascendany-lsp-control/control.sock"
: >"$FAKE_ROOT/var/lib/ascendany-lsp-root/opt/ascendany/v2/bin/ascendany-lsp"
: >"$FAKE_ROOT/var/lib/ascendany-lsp-root/run/ascendany-lsp-control/control.sock"
chmod 000 \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/opt/ascendany/v2/bin/ascendany-lsp" \
  "$FAKE_ROOT/var/lib/ascendany-lsp-root/run/ascendany-lsp-control/control.sock"
chmod 1777 "$FAKE_ROOT/var/lib/ascendany-lsp-root/tmp"

for environment_file in \
  ascendanyd.env ascendanyd-read-only-smoke.env backup.env judge.env migrate.env restore.env; do
  : >"$FAKE_ROOT/etc/ascendany/v2/$environment_file"
  chmod 0644 "$FAKE_ROOT/etc/ascendany/v2/$environment_file"
done

mapfile -t referenced_executables < <(
  sed -n -E \
    's/^(ExecStart|ExecStartPre|ExecStartPost|ExecStopPost|ExecReload|AssertFileIsExecutable)=[+!:@|-]*([^ ;]+).*/\2/p' \
    "$UNIT_SOURCE"/*.service |
    LC_ALL=C sort -u
)
(( ${#referenced_executables[@]} > 0 )) || fail 'no unit executable references were discovered'
for executable in "${referenced_executables[@]}"; do
  [[ "$executable" == /* && ! "$executable" =~ [%$\{\}] ]] || fail "unit executable path is not one fixed absolute path: $executable"
  if [[ "$executable" == /opt/ascendany/v2/* ]]; then
    relative="${executable#/opt/ascendany/v2/}"
    manifest_record="$(jq -er --arg path "$relative" '
      [.files[] | select(.path == $path)] |
      if length == 1 then .[0] | [.sha256, (.size | tostring), .mode] | @tsv else error("missing or duplicate path") end
    ' "$INSTALLED_RELEASE/release-manifest.json")" ||
      fail "unit executable is absent or duplicated in the installed release manifest: $executable"
    IFS=$'\t' read -r expected_hash expected_size expected_mode <<<"$manifest_record"
    installed_path="$INSTALLED_RELEASE/$relative"
    [[ -f "$installed_path" && ! -L "$installed_path" && -x "$installed_path" ]] ||
      fail "manifest-declared unit executable is missing from the installed release: $executable"
    actual_hash="$(sha256sum "$installed_path" | awk '{print $1}')"
    actual_size="$(stat -Lc '%s' "$installed_path")"
    actual_mode="0$(stat -Lc '%a' "$installed_path")"
    [[ "$actual_hash:$actual_size:$actual_mode" == "$expected_hash:$expected_size:$expected_mode" ]] ||
      fail "installed unit executable differs from its manifest record: $executable"
    install -D -m 0755 -- "$installed_path" "$FAKE_ROOT$executable"
  else
    install -D -m 0755 /dev/null "$FAKE_ROOT$executable"
  fi
done

mapfile -t actual_release_executables < <(
  printf '%s\n' "${referenced_executables[@]}" |
    sed -n '\|^/opt/ascendany/v2/|p' |
    LC_ALL=C sort
)
mapfile -t expected_release_executables < <(printf '%s\n' "${RELEASE_EXECUTABLES[@]}" | LC_ALL=C sort)
if [[ "$(printf '%s\n' "${actual_release_executables[@]}")" != "$(printf '%s\n' "${expected_release_executables[@]}")" ]]; then
  diff -u \
    <(printf '%s\n' "${expected_release_executables[@]}") \
    <(printf '%s\n' "${actual_release_executables[@]}") >&2 || true
  fail 'systemd release executable closure drifted'
fi

mapfile -t actual_native_runtime_executables < <(
  printf '%s\n' "${referenced_executables[@]}" |
    awk '$0 == "/usr/bin/cloudflared" || $0 == "/usr/bin/pgbouncer"' |
    LC_ALL=C sort
)
mapfile -t expected_native_runtime_executables < <(
  printf '%s\n' "${NATIVE_RUNTIME_EXECUTABLES[@]}" | LC_ALL=C sort
)
if [[ "$(printf '%s\n' "${actual_native_runtime_executables[@]}")" != \
      "$(printf '%s\n' "${expected_native_runtime_executables[@]}")" ]]; then
  diff -u \
    <(printf '%s\n' "${expected_native_runtime_executables[@]}") \
    <(printf '%s\n' "${actual_native_runtime_executables[@]}") >&2 || true
  fail 'native systemd host executable closure drifted'
fi

verify_units() {
  systemd-analyze \
    --root="$FAKE_ROOT" \
    --man=no \
    --generators=no \
    --recursive-errors=no \
    verify "${UNIT_NAMES[@]}"
}

if ! verify_units >"$ANALYZE_LOG" 2>&1; then
  cat "$ANALYZE_LOG" >&2
  fail 'systemd-analyze rejected the installed unit and executable closure'
fi
[[ ! -s "$ANALYZE_LOG" ]] || {
  cat "$ANALYZE_LOG" >&2
  fail 'systemd-analyze emitted diagnostics for the installed unit closure'
}

rm -- "$FAKE_ROOT/opt/ascendany/v2/bin/ascendanyd"
if verify_units >"$ANALYZE_LOG" 2>&1; then
  fail 'systemd-analyze accepted a missing installed release executable'
fi
grep -F 'Command /opt/ascendany/v2/bin/ascendanyd is not executable' "$ANALYZE_LOG" >/dev/null || {
  cat "$ANALYZE_LOG" >&2
  fail 'missing-executable diagnostic did not bind the expected path'
}
install -D -m 0755 /dev/null "$FAKE_ROOT/opt/ascendany/v2/bin/ascendanyd"

printf '%s\n' 'DefinitelyInvalidAscendAnyDirective=yes' \
  >>"$FAKE_ROOT/etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf"
if verify_units >"$ANALYZE_LOG" 2>&1; then
  fail 'systemd-analyze accepted an invalid installed drop-in directive'
fi
grep -F 'Unknown key' "$ANALYZE_LOG" >/dev/null || {
  cat "$ANALYZE_LOG" >&2
  fail 'invalid drop-in diagnostic was not reported'
}

printf '%s\n' 'installed systemd unit and executable closure fixture passed'
