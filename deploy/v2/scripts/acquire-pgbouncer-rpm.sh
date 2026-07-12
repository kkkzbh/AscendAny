#!/usr/bin/bash -p
set +x
set -Eeuo pipefail

readonly SELF="$(/usr/bin/readlink -e -- "${BASH_SOURCE[0]}")"
if [[ -z "$SELF" ]]; then
  /usr/bin/printf '%s\n' 'acquire PgBouncer RPM: script path is not canonical' >&2
  exit 1
fi
if [[ "${ASCENDANY_PGBOUNCER_ACQUIRE_CLEAN_ENV-}" != 1 ]]; then
  exec /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    TZ=UTC \
    ASCENDANY_PGBOUNCER_ACQUIRE_CLEAN_ENV=1 \
    /usr/bin/bash -p "$SELF" "$@"
fi

environment_is_clean=1
while IFS= read -r -d '' entry; do
  environment_name="${entry%%=*}"
  case "$environment_name" in
    ASCENDANY_PGBOUNCER_ACQUIRE_CLEAN_ENV|LC_ALL|PATH|PWD|SHLVL|TZ|_)
      ;;
    *)
      environment_is_clean=0
      ;;
  esac
done < <(/usr/bin/env -0)
if [[ "${PATH-}" != /usr/bin:/bin || "${LC_ALL-}" != C || "${TZ-}" != UTC ||
      "$environment_is_clean" != 1 ]]; then
  /usr/bin/printf '%s\n' 'acquire PgBouncer RPM: clean-environment boundary was forged' >&2
  exit 1
fi
builtin unset ASCENDANY_PGBOUNCER_ACQUIRE_CLEAN_ENV BASH_ENV ENV CDPATH GLOBIGNORE \
  POSIXLY_CORRECT TMPDIR environment_is_clean environment_name entry
builtin export -n SHELLOPTS BASHOPTS
builtin export PATH=/usr/bin:/bin LC_ALL=C TZ=UTC
umask 077

script_directory="$(builtin cd -- "$(/usr/bin/dirname -- "$SELF")" && builtin pwd -P)"
readonly package_lock_path="${script_directory}/../config/fedora-runtime-packages.json"
readonly download_url='https://dl.fedoraproject.org/pub/fedora/linux/updates/44/Everything/x86_64/Packages/p/pgbouncer-1.25.2-1.fc44.x86_64.rpm'
readonly rpm_size=294992

die() {
  /usr/bin/printf 'acquire PgBouncer RPM: %s\n' "$1" >&2
  exit 1
}

usage() {
  /usr/bin/printf 'usage: %s --output /absolute/pgbouncer.rpm --sha256-output /absolute/pgbouncer.rpm.sha256\n' "$0" >&2
}

load_package_contract() {
  local -a values=()

  [[ -f "$package_lock_path" && ! -L "$package_lock_path" ]] ||
    die 'release-bound Fedora runtime package lock is missing or non-regular'
  /usr/bin/jq -e '
    def runtime_file:
      type == "object" and
      keys == ["group", "mode", "owner", "path", "sha256", "size"] and
      (.path | type == "string" and test("^/usr/bin/[a-z0-9-]+$")) and
      (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
      (.size | type == "number" and floor == . and . > 0 and . <= 134217728) and
      .mode == "0755" and .owner == "root" and .group == "root";
    def runtime_package:
      type == "object" and
      keys == ["files", "nevra", "rpmSHA256", "signingFingerprint"] and
      (.nevra | type == "string" and test("^[A-Za-z0-9+_.-]+-[A-Za-z0-9+_.~-]+-[A-Za-z0-9+_.~-]+[.]x86_64$")) and
      (.rpmSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
      (.signingFingerprint | type == "string" and test("^[0-9a-f]{40}$")) and
      (.files | type == "array" and length > 0 and length <= 16 and all(.[]; runtime_file));
    type == "object" and
    keys == ["architecture", "fedoraRelease", "packages", "schema"] and
    .schema == "ascendany.fedora-runtime-packages.v1" and
    .architecture == "x86_64" and .fedoraRelease == 44 and
    (.packages | type == "object" and keys == ["cloudflared", "pgbouncer"]) and
    (.packages.cloudflared | runtime_package) and
    (.packages.pgbouncer | runtime_package) and
    .packages.pgbouncer.nevra == "pgbouncer-1.25.2-1.fc44.x86_64" and
    .packages.pgbouncer.signingFingerprint == "36f612dcf27f7d1a48a835e4dbfcf71c6d9f90a6" and
    (.packages.pgbouncer.files | length == 1) and
    .packages.pgbouncer.files[0].path == "/usr/bin/pgbouncer"
  ' "$package_lock_path" >/dev/null ||
    die 'release-bound Fedora runtime package lock violates its closed schema'
  mapfile -t values < <(/usr/bin/jq -r '.packages.pgbouncer.nevra, .packages.pgbouncer.rpmSHA256' "$package_lock_path")
  (( ${#values[@]} == 2 )) || die 'PgBouncer package lock field extraction failed'
  PGBOUNCER_RPM_NEVRA="${values[0]}"
  PGBOUNCER_RPM_SHA256="${values[1]}"
  readonly PGBOUNCER_RPM_NEVRA PGBOUNCER_RPM_SHA256
}

for command_path in \
  /usr/bin/awk /usr/bin/chmod /usr/bin/curl /usr/bin/dirname /usr/bin/env \
  /usr/bin/jq /usr/bin/ln /usr/bin/mktemp /usr/bin/printf /usr/bin/readlink \
  /usr/bin/realpath /usr/bin/rm /usr/bin/sha256sum /usr/bin/stat; do
  [[ -x "$command_path" ]] || die "required command is missing: $command_path"
done
unset command_path
load_package_contract

[[ "$#" == 4 && "$1" == '--output' && "$3" == '--sha256-output' ]] || {
  usage
  exit 2
}
output="$2"
sha256_output="$4"
for path in "$output" "$sha256_output"; do
  [[ "$path" == /* && "$path" == "$(/usr/bin/realpath -m -- "$path")" && "$path" != *:* && "$path" != *$'\n'* ]] ||
    die 'output paths must be canonical absolute paths without transport delimiters'
  parent="$(/usr/bin/dirname -- "$path")"
  [[ -d "$parent" && ! -L "$parent" ]] || die 'output parent must be an existing real directory'
  [[ ! -e "$path" && ! -L "$path" ]] || die "refusing to replace existing output: $path"
done
[[ "$output" != "$sha256_output" ]] || die 'RPM and digest outputs must differ'

temporary_rpm="$(/usr/bin/mktemp --tmpdir="$(/usr/bin/dirname -- "$output")" '.pgbouncer-rpm.partial.XXXXXXXX')"
temporary_sha256="$(/usr/bin/mktemp --tmpdir="$(/usr/bin/dirname -- "$sha256_output")" '.pgbouncer-rpm-sha256.partial.XXXXXXXX')"
rpm_published=0
sha256_published=0
cleanup() {
  /usr/bin/rm -f -- "$temporary_rpm" "$temporary_sha256"
  (( sha256_published == 0 )) || /usr/bin/rm -f -- "$sha256_output"
  (( rpm_published == 0 )) || /usr/bin/rm -f -- "$output"
}
trap cleanup EXIT

/usr/bin/curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --output "$temporary_rpm" "$download_url"
[[ "$(/usr/bin/stat -c '%s' -- "$temporary_rpm")" == "$rpm_size" ]] ||
  die 'downloaded RPM size differs from the reviewed artifact'
actual_sha256="$(/usr/bin/sha256sum -- "$temporary_rpm" | /usr/bin/awk '{print $1}')"
[[ "$actual_sha256" == "$PGBOUNCER_RPM_SHA256" ]] ||
  die 'downloaded RPM bytes differ from the release lock'
/usr/bin/printf '%s\n' "$actual_sha256" >"$temporary_sha256"
/usr/bin/chmod 0644 -- "$temporary_rpm" "$temporary_sha256"

"${script_directory}/attest-pgbouncer-rpm.sh" \
  --rpm "$temporary_rpm" --sha256-file "$temporary_sha256" >/dev/null

/usr/bin/ln -- "$temporary_rpm" "$output" || die 'RPM output appeared during acquisition'
rpm_published=1
/usr/bin/ln -- "$temporary_sha256" "$sha256_output" || die 'RPM digest output appeared during acquisition'
sha256_published=1
/usr/bin/rm -f -- "$temporary_rpm" "$temporary_sha256"
rpm_published=0
sha256_published=0
trap - EXIT
/usr/bin/printf 'acquired %s as %s (sha256:%s)\n' "$PGBOUNCER_RPM_NEVRA" "$output" "$actual_sha256"
