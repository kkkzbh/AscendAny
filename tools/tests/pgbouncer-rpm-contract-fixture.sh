#!/usr/bin/env bash

set -Eeuo pipefail

export LC_ALL=C
export TZ=UTC

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly ACQUIRE_SOURCE="$REPOSITORY_ROOT/deploy/v2/scripts/acquire-pgbouncer-rpm.sh"
readonly ATTEST_SOURCE="$REPOSITORY_ROOT/deploy/v2/scripts/attest-pgbouncer-rpm.sh"
readonly LOCK_SOURCE="$REPOSITORY_ROOT/deploy/v2/config/fedora-runtime-packages.json"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-pgbouncer-rpm-fixture.XXXXXX")"
readonly FAKE_BIN="$WORK_ROOT/fake-bin"
readonly STATE="$WORK_ROOT/state"
readonly FIXTURE_RELEASE_ROOT="$WORK_ROOT/release"
readonly FIXTURE_ACQUIRE="$FIXTURE_RELEASE_ROOT/scripts/acquire-pgbouncer-rpm.sh"
readonly FIXTURE_ATTEST="$FIXTURE_RELEASE_ROOT/scripts/attest-pgbouncer-rpm.sh"
readonly FIXTURE_RPM="$WORK_ROOT/fixture-pgbouncer.rpm"
readonly FIXTURE_SHA256="$WORK_ROOT/fixture-pgbouncer.rpm.sha256"

cleanup() {
  rm -rf -- "$WORK_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

require_log() {
  local path="$1"
  local text="$2"
  grep -F -- "$text" "$path" >/dev/null || {
    printf 'Expected log fragment: %s\n' "$text" >&2
    sed -n '1,200p' "$path" >&2
    fail 'fixture log assertion failed'
  }
}

for command in bash cmp grep jq realpath sha256sum stat; do
  command -v "$command" >/dev/null || fail "required fixture command is missing: $command"
done
[[ -x "$ACQUIRE_SOURCE" ]] || fail 'RPM acquisition source is not executable'
[[ -x "$ATTEST_SOURCE" ]] || fail 'RPM attestation source is not executable'
for source in "$ACQUIRE_SOURCE" "$ATTEST_SOURCE"; do
  [[ "$(head -n 1 "$source")" == '#!/usr/bin/bash -p' ]] ||
    fail "production script lacks the privileged-mode Bash boundary: $source"
  grep -F 'PATH=/usr/bin:/bin' "$source" >/dev/null ||
    fail "production script lacks its exact PATH boundary: $source"
  grep -F 'LC_ALL=C' "$source" >/dev/null ||
    fail "production script lacks its exact locale boundary: $source"
  grep -F 'TZ=UTC' "$source" >/dev/null ||
    fail "production script lacks its exact timezone boundary: $source"
  grep -F '/usr/bin/env -i' "$source" >/dev/null ||
    fail "production script lacks clean-environment reexecution: $source"
done
for production_command in \
  /usr/bin/awk /usr/bin/env /usr/bin/jq /usr/bin/printf /usr/bin/readlink \
  /usr/bin/realpath /usr/bin/rm /usr/bin/sha256sum /usr/bin/stat; do
  grep -F "$production_command" "$ACQUIRE_SOURCE" "$ATTEST_SOURCE" >/dev/null ||
    fail "production scripts lack an absolute command binding: $production_command"
done
for production_command in /usr/bin/curl /usr/bin/ln; do
  grep -F "$production_command" "$ACQUIRE_SOURCE" >/dev/null ||
    fail "acquisition lacks an absolute command binding: $production_command"
done
for production_command in /usr/bin/gpg /usr/bin/rpm /usr/bin/rpmkeys /usr/bin/wc; do
  grep -F "$production_command" "$ATTEST_SOURCE" >/dev/null ||
    fail "attestation lacks an absolute command binding: $production_command"
done
if grep -En -- '(^|[;&|()][[:space:]]*)(awk|cat|chmod|curl|dirname|env|gpg|install|jq|ln|mktemp|printf|readlink|realpath|rm|rpm|rpmkeys|sha256sum|stat|wc)([[:space:]]|$)' \
    "$ACQUIRE_SOURCE" "$ATTEST_SOURCE" >/dev/null; then
  fail 'production scripts contain a PATH-resolved external command'
fi
jq -e '
  .schema == "ascendany.fedora-runtime-packages.v1" and
  .architecture == "x86_64" and .fedoraRelease == 44 and
  (.packages | keys == ["cloudflared", "pgbouncer"]) and
  .packages.pgbouncer.nevra == "pgbouncer-1.25.2-1.fc44.x86_64" and
  .packages.pgbouncer.rpmSHA256 == "ad409c6bef77aba14288cd2464128eb5a151d75d7c28aa0b66451febb0d978c2" and
  .packages.pgbouncer.signingFingerprint == "36f612dcf27f7d1a48a835e4dbfcf71c6d9f90a6"
' "$LOCK_SOURCE" >/dev/null || fail 'release-owned PgBouncer package lock identity drifted'
if grep -En -- 'dnf|rpm[[:space:]]+(-i|--install)|rpm-ostree|curl|wget' "$ATTEST_SOURCE" >/dev/null; then
  fail 'offline attestation contains a network or package-install command'
fi
if grep -En -- 'dnf|rpm[[:space:]]+(-i|--install)|rpm-ostree' "$ACQUIRE_SOURCE" >/dev/null; then
  fail 'RPM acquisition contains a host package-install command'
fi

production_bash_env_marker="$WORK_ROOT/production-bash-env-executed"
printf '/usr/bin/touch %q\n' "$production_bash_env_marker" >"$WORK_ROOT/production-bash-env-attack"
chmod 0600 "$WORK_ROOT/production-bash-env-attack"
for source in "$ACQUIRE_SOURCE" "$ATTEST_SOURCE"; do
  if BASH_ENV="$WORK_ROOT/production-bash-env-attack" \
      "$source" --invalid >"$WORK_ROOT/${source##*/}.invalid.log" 2>&1; then
    fail "production script accepted invalid arguments during its BASH_ENV probe: $source"
  fi
  [[ ! -e "$production_bash_env_marker" ]] ||
    fail "production script evaluated caller BASH_ENV before reexecution: $source"
done

install -d -m 0700 -- "$FAKE_BIN" "$STATE" \
  "$FIXTURE_RELEASE_ROOT/scripts" "$FIXTURE_RELEASE_ROOT/config"
printf '%s\n' 'fixture-pgbouncer-rpm' >"$FIXTURE_RPM"
printf '%s\n' 'ad409c6bef77aba14288cd2464128eb5a151d75d7c28aa0b66451febb0d978c2' >"$FIXTURE_SHA256"

cat >"$FAKE_BIN/sha256sum" <<'SH'
#!/usr/bin/bash
set -euo pipefail
fixture_state="$(builtin cd -- "$(/usr/bin/dirname -- "${BASH_SOURCE[0]}")/../state" && builtin pwd -P)"
file="${!#}"
if [[ -f "$file" ]] && /usr/bin/grep -Fqx 'fixture-pgbouncer-rpm' "$file"; then
  printf 'ad409c6bef77aba14288cd2464128eb5a151d75d7c28aa0b66451febb0d978c2  %s\n' "$file"
  exit 0
fi
if [[ -f "$file" ]] && /usr/bin/grep -Fq 'fixture-package-manifest' "$file"; then
  if [[ -e "$fixture_state/bad-manifest" ||
        ( -e "$fixture_state/bad-installed-manifest" &&
          "$file" == */installed-package-files.dump ) ]]; then
    printf '0000000000000000000000000000000000000000000000000000000000000000  %s\n' "$file"
  else
    printf 'a3e2b707fc84df91e8a53ea0b1fdca6fcd40d579af547c97e881a1253c65209b  %s\n' "$file"
  fi
  exit 0
fi
exec /usr/bin/sha256sum "$@"
SH

cat >"$FAKE_BIN/stat" <<'SH'
#!/usr/bin/bash
set -euo pipefail
file="${!#}"
if [[ -f "$file" ]] && /usr/bin/grep -Fqx 'fixture-pgbouncer-rpm' "$file"; then
  printf '294992\n'
  exit 0
fi
exec /usr/bin/stat "$@"
SH

cat >"$FAKE_BIN/curl" <<'SH'
#!/usr/bin/bash
set -euo pipefail
fixture_state="$(builtin cd -- "$(/usr/bin/dirname -- "${BASH_SOURCE[0]}")/../state" && builtin pwd -P)"
if [[ "${PATH-}" != /usr/bin:/bin || "${LC_ALL-}" != C || "${TZ-}" != UTC ||
      -n "${HTTP_PROXY+x}" || -n "${HTTPS_PROXY+x}" || -n "${ALL_PROXY+x}" ||
      -n "${NO_PROXY+x}" || -n "${BASH_ENV+x}" || -n "${ENV+x}" ]]; then
  /usr/bin/touch "$fixture_state/hostile-environment-reached-curl"
  exit 97
fi
output=''
url=''
while (($# > 0)); do
  case "$1" in
    --output)
      output="$2"
      shift 2
      ;;
    --fail|--location|--silent|--show-error|--tlsv1.2)
      shift
      ;;
    --proto|--proto-redir)
      [[ "$2" == '=https' ]]
      shift 2
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done
[[ ! -e "$fixture_state/forbid-network" ]] || exit 93
[[ "$url" == 'https://dl.fedoraproject.org/pub/fedora/linux/updates/44/Everything/x86_64/Packages/p/pgbouncer-1.25.2-1.fc44.x86_64.rpm' ]]
[[ -n "$output" ]]
printf '%s\n' 'fixture-pgbouncer-rpm' >"$output"
printf '%s\n' "$url" >>"$fixture_state/curl.log"
SH

cat >"$FAKE_BIN/gpg" <<'SH'
#!/usr/bin/bash
set -euo pipefail
printf '%s\n' \
  'pub:-:4096:1:DBFCF71C6D9F90A6:1736879931:::-:::escESC::::::23::0:' \
  'fpr:::::::::36F612DCF27F7D1A48A835E4DBFCF71C6D9F90A6:' \
  'uid:-::::1736879931::562CE2BA065730B086982FA62864934F74BCBEAA::Fedora (44) <fedora-44-primary@fedoraproject.org>::::::::::0:'
SH

cat >"$FAKE_BIN/rpmkeys" <<'SH'
#!/usr/bin/bash
set -euo pipefail
fixture_state="$(builtin cd -- "$(/usr/bin/dirname -- "${BASH_SOURCE[0]}")/../state" && builtin pwd -P)"
action=''
for argument in "$@"; do
  case "$argument" in
    --import|--checksig) action="$argument" ;;
  esac
done
case "$action" in
  --import)
    exit 0
    ;;
  --checksig)
    rpm_path="${!#}"
    if [[ -e "$fixture_state/bad-signature" ]]; then
      printf '%s:\n' "$rpm_path"
      printf '%s\n' '    Header OpenPGP V4 RSA/SHA256 signature, key fingerprint: 0000000000000000000000000000000000000000: NOT OK'
      exit 1
    fi
    printf '%s:\n' "$rpm_path"
    printf '%s\n' \
      '    Header OpenPGP V4 RSA/SHA256 signature, key fingerprint: 36f612dcf27f7d1a48a835e4dbfcf71c6d9f90a6: OK' \
      '    Header SHA256 digest: OK'
    if [[ -e "$fixture_state/bad-payload" ]]; then
      printf '%s\n' '    Payload SHA256 digest: NOT OK'
    else
      printf '%s\n' '    Payload SHA256 digest: OK'
    fi
    ;;
  *)
    exit 64
    ;;
esac
SH

cat >"$FAKE_BIN/rpm" <<'SH'
#!/usr/bin/bash
set -euo pipefail
fixture_state="$(builtin cd -- "$(/usr/bin/dirname -- "${BASH_SOURCE[0]}")/../state" && builtin pwd -P)"
if [[ "${PATH-}" != /usr/bin:/bin || "${LC_ALL-}" != C || "${TZ-}" != UTC ||
      -n "${RPM_CONFIGDIR+x}" || -n "${RPM_MACROS+x}" || -n "${HOME+x}" ||
      -n "${GNUPGHOME+x}" || -n "${BASH_ENV+x}" || -n "${ENV+x}" ]]; then
  /usr/bin/touch "$fixture_state/hostile-environment-reached-rpm"
  exit 97
fi
query_kind=''
operation=''
for argument in "$@"; do
  case "$argument" in
    -qp) query_kind=artifact ;;
    -q) query_kind=installed ;;
    --qf) operation=metadata ;;
    --dump) operation=dump ;;
    --verify) operation=verify ;;
  esac
done
if [[ "$operation" == verify ]]; then
  if [[ -e "$fixture_state/bad-installed-verify" ]]; then
    printf '%s\n' 'S.5....T.  /usr/bin/pgbouncer'
    exit 1
  fi
  exit 0
fi
if [[ "$query_kind" == installed && -e "$fixture_state/installed-absent" ]]; then
  exit 1
fi
if [[ "$operation" == dump ]]; then
  for ((index = 1; index <= 29; index++)); do
    printf '/fixture-package-manifest/%02d 0 1778284800 %064d 040755 root root 0 0 0 X\n' "$index" 0
  done
  printf '%s\n' '/usr/bin/pgbouncer 467960 1778284800 42c722ab7352ccbb1eaba8dcc6d7fb9d28df11fbe1a73aa8b177c88dcd0bb318 0100755 root root 0 0 0 X'
  if [[ -e "$fixture_state/bad-manifest" ]]; then
    printf '%s\n' '/forged 1 1 00 0100644 root root 0 0 0 X'
  fi
  exit 0
fi
[[ "$operation" == metadata ]]
if [[ "$query_kind" == installed ]]; then
  if [[ -e "$fixture_state/bad-installed-nevra" ]]; then
    printf '%s\n' 'pgbouncer-1.25.1-1.fc44.x86_64'
  else
    printf '%s\n' 'pgbouncer-1.25.2-1.fc44.x86_64'
  fi
  exit 0
fi
version=1.25.2
[[ ! -e "$fixture_state/old-version" ]] || version=1.25.1
printf '%s\n' \
  pgbouncer \
  0 \
  "$version" \
  1.fc44 \
  x86_64 \
  pgbouncer-1.25.2-1.fc44.src.rpm \
  1778342976 \
  buildhw-x86-12.rdu3.fedoraproject.org \
  'Fedora Project' \
  'ISC and BSD-2-Clause' \
  093131979d3d1858e30103e1490dbb7af40d51c3541f27f1dfb753ff7fb63eca \
  8 \
  cpio \
  zstd \
  19
if [[ "$query_kind" == artifact ]]; then
  printf '%s\n' \
    'RSA/SHA256, Sat May  9 16:16:26 2026, Key ID dbfcf71c6d9f90a6' \
    10626db037a57e4d205a1b915f2b57176b474c83dc37c616da92e5c2eee89c58
fi
SH

cat >"$FAKE_BIN/pgbouncer" <<'SH'
#!/usr/bin/bash
set -euo pipefail
fixture_state="$(builtin cd -- "$(/usr/bin/dirname -- "${BASH_SOURCE[0]}")/../state" && builtin pwd -P)"
if [[ -e "$fixture_state/bad-installed-runtime" ]]; then
  printf '%s\n' 'PgBouncer 1.25.1'
else
  printf '%s\n' 'PgBouncer 1.25.2'
fi
SH

chmod 0755 "$FAKE_BIN"/*
cp "$LOCK_SOURCE" "$FIXTURE_RELEASE_ROOT/config/fedora-runtime-packages.json"
sed \
  -e "s#/usr/bin/curl#$FAKE_BIN/curl#g" \
  -e "s#/usr/bin/sha256sum#$FAKE_BIN/sha256sum#g" \
  -e "s#/usr/bin/stat#$FAKE_BIN/stat#g" \
  "$ACQUIRE_SOURCE" >"$FIXTURE_ACQUIRE"
sed \
  -e "s#/usr/bin/gpg#$FAKE_BIN/gpg#g" \
  -e "s#/usr/bin/rpmkeys#$FAKE_BIN/rpmkeys#g" \
  -e "s#/usr/bin/rpm#$FAKE_BIN/rpm#g" \
  -e "s#/usr/bin/sha256sum#$FAKE_BIN/sha256sum#g" \
  -e "s#/usr/bin/stat#$FAKE_BIN/stat#g" \
  -e '/^  runtime_version=/s#"$PGBOUNCER_BINARY_PATH"#"__FIXTURE_PGBOUNCER__"#' \
  -e "s#__FIXTURE_PGBOUNCER__#$FAKE_BIN/pgbouncer#g" \
  "$ATTEST_SOURCE" >"$FIXTURE_ATTEST"
chmod 0755 "$FIXTURE_ACQUIRE" "$FIXTURE_ATTEST"

grep -F "$FAKE_BIN/curl" "$FIXTURE_ACQUIRE" >/dev/null ||
  fail 'test-only acquisition copy is not rooted at the curl mock'
for mock_path in "$FAKE_BIN/gpg" "$FAKE_BIN/rpm" "$FAKE_BIN/rpmkeys" \
  "$FAKE_BIN/pgbouncer" "$FAKE_BIN/sha256sum" "$FAKE_BIN/stat"; do
  grep -F "$mock_path" "$FIXTURE_ATTEST" >/dev/null ||
    fail "test-only attestation copy is not rooted at its mock: $mock_path"
done

bash_env_marker="$WORK_ROOT/bash-env-executed"
hostile_rpm_environment_marker="$STATE/hostile-environment-reached-rpm"
hostile_curl_environment_marker="$STATE/hostile-environment-reached-curl"
printf '/usr/bin/touch %q\n' "$bash_env_marker" >"$WORK_ROOT/bash-env-attack"
chmod 0600 "$WORK_ROOT/bash-env-attack"
/usr/bin/env -i \
  PATH="$FAKE_BIN" \
  LC_ALL=C.UTF-8 \
  TZ=Pacific/Honolulu \
  BASH_ENV="$WORK_ROOT/bash-env-attack" \
  ENV="$WORK_ROOT/bash-env-attack" \
  HOME="$WORK_ROOT/attacker-home" \
  GNUPGHOME="$WORK_ROOT/attacker-gnupg" \
  RPM_CONFIGDIR="$WORK_ROOT/attacker-rpm-config" \
  RPM_MACROS="$WORK_ROOT/attacker-rpm-macros" \
  HTTP_PROXY=http://attacker.invalid \
  "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" \
  >"$WORK_ROOT/hostile-attestation.log"
[[ ! -e "$bash_env_marker" && ! -e "$hostile_rpm_environment_marker" &&
   ! -e "$hostile_curl_environment_marker" ]] ||
  fail 'attestation evaluated BASH_ENV or passed hostile RPM environment to a command'

hostile_acquired_rpm="$WORK_ROOT/hostile-acquired-pgbouncer.rpm"
hostile_acquired_sha256="$WORK_ROOT/hostile-acquired-pgbouncer.rpm.sha256"
/usr/bin/env -i \
  PATH="$FAKE_BIN" \
  LC_ALL=C.UTF-8 \
  TZ=Pacific/Honolulu \
  BASH_ENV="$WORK_ROOT/bash-env-attack" \
  ENV="$WORK_ROOT/bash-env-attack" \
  HOME="$WORK_ROOT/attacker-home" \
  GNUPGHOME="$WORK_ROOT/attacker-gnupg" \
  RPM_CONFIGDIR="$WORK_ROOT/attacker-rpm-config" \
  RPM_MACROS="$WORK_ROOT/attacker-rpm-macros" \
  HTTPS_PROXY=http://attacker.invalid \
  "$FIXTURE_ACQUIRE" --output "$hostile_acquired_rpm" \
    --sha256-output "$hostile_acquired_sha256" >"$WORK_ROOT/hostile-acquisition.log"
[[ ! -e "$bash_env_marker" && ! -e "$hostile_rpm_environment_marker" &&
   ! -e "$hostile_curl_environment_marker" ]] ||
  fail 'acquisition evaluated BASH_ENV or passed hostile RPM environment to a command'
cmp -s "$FIXTURE_RPM" "$hostile_acquired_rpm" ||
  fail 'clean-environment acquisition did not publish the mocked RPM'
rm -f "$hostile_acquired_rpm" "$hostile_acquired_sha256" "$STATE/curl.log"

if /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C TZ=UTC \
    ASCENDANY_PGBOUNCER_ATTEST_CLEAN_ENV=1 \
    RPM_CONFIGDIR="$WORK_ROOT/forged-rpm-config" \
    "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" \
    >"$WORK_ROOT/forged-attestation-environment.log" 2>&1; then
  fail 'attestation accepted a forged clean-environment marker with RPM state'
fi
require_log "$WORK_ROOT/forged-attestation-environment.log" \
  'clean-environment boundary was forged'
if /usr/bin/env -i \
    PATH=/usr/bin:/bin LC_ALL=C TZ=UTC \
    ASCENDANY_PGBOUNCER_ACQUIRE_CLEAN_ENV=1 \
    RPM_CONFIGDIR="$WORK_ROOT/forged-rpm-config" \
    "$FIXTURE_ACQUIRE" --output "$hostile_acquired_rpm" \
      --sha256-output "$hostile_acquired_sha256" \
    >"$WORK_ROOT/forged-acquisition-environment.log" 2>&1; then
  fail 'acquisition accepted a forged clean-environment marker with RPM state'
fi
require_log "$WORK_ROOT/forged-acquisition-environment.log" \
  'clean-environment boundary was forged'
[[ ! -e "$hostile_acquired_rpm" && ! -e "$hostile_acquired_sha256" ]] ||
  fail 'forged acquisition environment mutated an output path'

schema_case_root="$WORK_ROOT/schema-case"
install -d -m 0700 -- "$schema_case_root/scripts" "$schema_case_root/config"
install -m 0755 -- "$FIXTURE_ATTEST" "$schema_case_root/scripts/attest-pgbouncer-rpm.sh"
jq '.packages.cloudflared.unexpected = true' "$LOCK_SOURCE" >"$schema_case_root/config/fedora-runtime-packages.json"
if "$schema_case_root/scripts/attest-pgbouncer-rpm.sh" \
  --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" >"$WORK_ROOT/cloudflared-schema.log" 2>&1; then
  fail 'attestation accepted an unknown field in the shared Cloudflared lock entry'
fi
require_log "$WORK_ROOT/cloudflared-schema.log" 'release-bound Fedora runtime package lock violates its closed schema'

touch "$STATE/forbid-network"
attestation_log="$WORK_ROOT/attestation.log"
"$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" >"$attestation_log"
jq -e '
  .schema == "ascendany.pgbouncer-rpm-attestation.v1" and
  .nevra == "pgbouncer-1.25.2-1.fc44.x86_64" and
  .installedVerified == false and
  .signingKeyFingerprint == "36F612DCF27F7D1A48A835E4DBFCF71C6D9F90A6"
' "$attestation_log" >/dev/null || fail 'offline attestation output differs from the contract'
[[ ! -e "$STATE/curl.log" ]] || fail 'offline attestation attempted network access'

installed_attestation_log="$WORK_ROOT/installed-attestation.log"
"$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" --verify-installed \
  >"$installed_attestation_log"
jq -e '
  .schema == "ascendany.pgbouncer-rpm-attestation.v1" and
  .nevra == "pgbouncer-1.25.2-1.fc44.x86_64" and
  .installedVerified == true
' "$installed_attestation_log" >/dev/null ||
  fail 'installed-runtime attestation output differs from the contract'

touch "$STATE/bad-installed-nevra"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" --verify-installed \
    >"$WORK_ROOT/bad-installed-nevra.log" 2>&1; then
  fail 'installed-runtime attestation accepted a different installed NEVRA'
fi
require_log "$WORK_ROOT/bad-installed-nevra.log" \
  'installed PgBouncer NEVRA differs from the release lock'
rm -f "$STATE/bad-installed-nevra"

touch "$STATE/bad-installed-manifest"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" --verify-installed \
    >"$WORK_ROOT/bad-installed-manifest.log" 2>&1; then
  fail 'installed-runtime attestation accepted a different installed file manifest'
fi
require_log "$WORK_ROOT/bad-installed-manifest.log" \
  'installed PgBouncer package file manifest differs from the reviewed artifact'
rm -f "$STATE/bad-installed-manifest"

touch "$STATE/bad-installed-verify"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" --verify-installed \
    >"$WORK_ROOT/bad-installed-verify.log" 2>&1; then
  fail 'installed-runtime attestation accepted an rpm --verify file difference'
fi
require_log "$WORK_ROOT/bad-installed-verify.log" \
  'installed PgBouncer files differ from the signed package manifest'
rm -f "$STATE/bad-installed-verify"

touch "$STATE/bad-installed-runtime"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" --verify-installed \
    >"$WORK_ROOT/bad-installed-runtime.log" 2>&1; then
  fail 'installed-runtime attestation accepted a different runtime binary version'
fi
require_log "$WORK_ROOT/bad-installed-runtime.log" \
  'installed PgBouncer binary version differs from the release lock'
rm -f "$STATE/bad-installed-runtime"

touch "$STATE/old-version"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" >"$WORK_ROOT/old-version.log" 2>&1; then
  fail 'attestation accepted PgBouncer 1.25.1'
fi
require_log "$WORK_ROOT/old-version.log" 'RPM header metadata differs from the reviewed PgBouncer 1.25.2 artifact'
rm -f "$STATE/old-version"

touch "$STATE/bad-signature"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" >"$WORK_ROOT/bad-signature.log" 2>&1; then
  fail 'attestation accepted the wrong signing-key identity'
fi
require_log "$WORK_ROOT/bad-signature.log" 'RPM signature, header digest, or payload digest verification failed'
rm -f "$STATE/bad-signature"

touch "$STATE/bad-payload"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" >"$WORK_ROOT/bad-payload.log" 2>&1; then
  fail 'attestation accepted a failed payload digest'
fi
require_log "$WORK_ROOT/bad-payload.log" 'RPM verification result does not exactly attest the locked signature, header, and payload'
rm -f "$STATE/bad-payload"

touch "$STATE/bad-manifest"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$FIXTURE_SHA256" >"$WORK_ROOT/bad-manifest.log" 2>&1; then
  fail 'attestation accepted a different package file manifest'
fi
require_log "$WORK_ROOT/bad-manifest.log" 'RPM file manifest differs from the reviewed artifact'
rm -f "$STATE/bad-manifest"

printf '%s\n' '0000000000000000000000000000000000000000000000000000000000000000' >"$WORK_ROOT/wrong.sha256"
if "$FIXTURE_ATTEST" --rpm "$FIXTURE_RPM" --sha256-file "$WORK_ROOT/wrong.sha256" >"$WORK_ROOT/wrong-digest.log" 2>&1; then
  fail 'attestation accepted a different transfer trust anchor'
fi
require_log "$WORK_ROOT/wrong-digest.log" 'RPM digest file differs from the release lock'

rm -f "$STATE/forbid-network"
acquired_rpm="$WORK_ROOT/acquired-pgbouncer.rpm"
acquired_sha256="$WORK_ROOT/acquired-pgbouncer.rpm.sha256"
"$FIXTURE_ACQUIRE" --output "$acquired_rpm" --sha256-output "$acquired_sha256" >"$WORK_ROOT/acquire.log"
cmp -s "$FIXTURE_RPM" "$acquired_rpm" || fail 'acquisition did not publish the verified RPM bytes'
cmp -s "$FIXTURE_SHA256" "$acquired_sha256" || fail 'acquisition did not publish the canonical RPM digest'
[[ "$(wc -l <"$STATE/curl.log")" == 1 ]] || fail 'acquisition did not use exactly one download request'
require_log "$STATE/curl.log" 'https://dl.fedoraproject.org/pub/fedora/linux/updates/44/Everything/x86_64/Packages/p/pgbouncer-1.25.2-1.fc44.x86_64.rpm'

touch "$STATE/forbid-network"
"$FIXTURE_ATTEST" --rpm "$acquired_rpm" --sha256-file "$acquired_sha256" >/dev/null
[[ "$(wc -l <"$STATE/curl.log")" == 1 ]] || fail 'offline transferred-RPM attestation attempted network access'
if "$FIXTURE_ACQUIRE" --output "$acquired_rpm" --sha256-output "$acquired_sha256" >"$WORK_ROOT/reacquire.log" 2>&1; then
  fail 'acquisition replaced an existing operator artifact'
fi
require_log "$WORK_ROOT/reacquire.log" 'refusing to replace existing output'
[[ "$(wc -l <"$STATE/curl.log")" == 1 ]] || fail 'existing-output rejection occurred after a download'

printf '%s\n' 'PgBouncer RPM contract fixture passed'
