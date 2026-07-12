#!/usr/bin/bash -p
set +x
set -Eeuo pipefail

readonly SELF="$(/usr/bin/readlink -e -- "${BASH_SOURCE[0]}")"
if [[ -z "$SELF" ]]; then
  /usr/bin/printf '%s\n' 'attest PgBouncer RPM: script path is not canonical' >&2
  exit 1
fi
if [[ "${ASCENDANY_PGBOUNCER_ATTEST_CLEAN_ENV-}" != 1 ]]; then
  exec /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    TZ=UTC \
    ASCENDANY_PGBOUNCER_ATTEST_CLEAN_ENV=1 \
    /usr/bin/bash -p "$SELF" "$@"
fi

environment_is_clean=1
while IFS= read -r -d '' entry; do
  environment_name="${entry%%=*}"
  case "$environment_name" in
    ASCENDANY_PGBOUNCER_ATTEST_CLEAN_ENV|LC_ALL|PATH|PWD|SHLVL|TZ|_)
      ;;
    *)
      environment_is_clean=0
      ;;
  esac
done < <(/usr/bin/env -0)
if [[ "${PATH-}" != /usr/bin:/bin || "${LC_ALL-}" != C || "${TZ-}" != UTC ||
      "$environment_is_clean" != 1 ]]; then
  /usr/bin/printf '%s\n' 'attest PgBouncer RPM: clean-environment boundary was forged' >&2
  exit 1
fi
builtin unset ASCENDANY_PGBOUNCER_ATTEST_CLEAN_ENV BASH_ENV ENV CDPATH GLOBIGNORE \
  POSIXLY_CORRECT TMPDIR environment_is_clean environment_name entry
builtin export -n SHELLOPTS BASHOPTS
builtin export PATH=/usr/bin:/bin LC_ALL=C TZ=UTC
umask 077

script_directory="$(builtin cd -- "$(/usr/bin/dirname -- "$SELF")" && builtin pwd -P)"
readonly package_lock_path="${script_directory}/../config/fedora-runtime-packages.json"

die() {
  /usr/bin/printf 'attest PgBouncer RPM: %s\n' "$1" >&2
  exit 1
}

usage() {
  /usr/bin/printf 'usage: %s --rpm /absolute/pgbouncer.rpm --sha256-file /absolute/pgbouncer.rpm.sha256 [--verify-installed]\n' "$0" >&2
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

  mapfile -t values < <(/usr/bin/jq -r '
    .packages.pgbouncer.nevra,
    .packages.pgbouncer.rpmSHA256,
    .packages.pgbouncer.signingFingerprint,
    .packages.pgbouncer.files[0].path,
    .packages.pgbouncer.files[0].sha256,
    (.packages.pgbouncer.files[0].size | tostring),
    .packages.pgbouncer.files[0].mode,
    .packages.pgbouncer.files[0].owner,
    .packages.pgbouncer.files[0].group
  ' "$package_lock_path")
  (( ${#values[@]} == 9 )) || die 'PgBouncer package lock field extraction failed'

  PGBOUNCER_RPM_NEVRA="${values[0]}"
  PGBOUNCER_RPM_SHA256="${values[1]}"
  PGBOUNCER_RPM_SIGNING_FINGERPRINT="${values[2]}"
  PGBOUNCER_BINARY_PATH="${values[3]}"
  PGBOUNCER_BINARY_SHA256="${values[4]}"
  PGBOUNCER_BINARY_SIZE="${values[5]}"
  PGBOUNCER_BINARY_MODE="${values[6]}"
  PGBOUNCER_BINARY_OWNER="${values[7]}"
  PGBOUNCER_BINARY_GROUP="${values[8]}"
  readonly PGBOUNCER_RPM_NEVRA PGBOUNCER_RPM_SHA256 PGBOUNCER_RPM_SIGNING_FINGERPRINT
  readonly PGBOUNCER_BINARY_PATH PGBOUNCER_BINARY_SHA256 PGBOUNCER_BINARY_SIZE
  readonly PGBOUNCER_BINARY_MODE PGBOUNCER_BINARY_OWNER PGBOUNCER_BINARY_GROUP
}

write_fedora_44_signing_key() {
  local destination="$1"
  # Exact bytes from Fedora's f44 fedora-repos dist-git branch:
  # https://src.fedoraproject.org/rpms/fedora-repos/raw/f44/f/RPM-GPG-KEY-fedora-44-primary
  /usr/bin/cat >"$destination" <<'KEY'
-----BEGIN PGP PUBLIC KEY BLOCK-----

mQINBGeGrzsBEAC4UV5Ij9oz6h6abEKIRoiezttFfnLhwOAfE9tWtfIFMRmhY91u
L88PKf12n2xHBd3oc5ahBzGeTBhaMV+VJAppoQMSOIMI5q966D9GQ0LkJT+E5bwn
xGRJKp7qccevh2KFOUt2vHtFskhDOuAIupoKfo5FgI9PkvAVBsrUpO/22yjNv0V/
aeDXxZhRX8m/8FKJ77VcZtBRPcp7M41bCmW9gV9IDpD81hAdTjYoQr1Y3KU0FTm5
W4l1mf9mZcKMskOk08TyzQeC2YRB20EYRK439XCGJ4P7BFiOl96EbPpky2pHe2FV
AvX474o3QEecTK3KxZrsRjmXOqpjRPy5YyMfKEYBM9j3zBDvpDFk79Mfuw5n2Nr5
U4Wn/rqfhKLUKkfpfCow97nzq8NqynwS09yVobIfjHCKRtjwun6ife+s7R4L2nAu
rTWPAHqzIjjW5nnjaFtoSulIadVKx+KibKajA6gRAc6K7xMyMTHfqZeTAIcawvX6
h2d/nd8xCfogM5FTI5obNSUVNaMv5vQg6vcV1fb6oRgodF0Bi+1dssq5EMQpHFJM
nIQ5NVwuzSjCLt3X2mWUp0mfIt2K9oBpBct12uXho7Nm1bSC5UFNYsvw+rj6vTqZ
ilK9pyfcYmELv7a/NPkyuACsBFGoc66nBfrEvk57kW9FaJK9mjSqGftykQARAQAB
tDFGZWRvcmEgKDQ0KSA8ZmVkb3JhLTQ0LXByaW1hcnlAZmVkb3JhcHJvamVjdC5v
cmc+iQJSBBMBCAA8FiEENvYS3PJ/fRpIqDXk2/z3HG2fkKYFAmeGrzsCGw8FCwkI
BwIDIgIBBhUKCQgLAgQWAgMBAh4HAheAAAoJENv89xxtn5CmIU0P/iaFVxJjVi4P
yu8A04PbdGy2vuBBCceIjYn5HaMDwJMRjdJT6uMS494pSKNEl/JJ8K5rRdigfUV1
2Z22X3kI5aNb4k2wpaPg5Xq0JQS9FvG4Pjm//kNy5WplmEA8HVg4MVkvySWiXay4
+tkCelhE8aQDstYEm3uh+lZ6udgoInfprwFMn6H+8RXkakTW1z5NkuAA8PpMDA9o
SOFc4Hk6bhE6exEp4VNwBEkxwh4z9CGjarlXL4QEyM1UK60vtbXIHVjITjFfKVQP
j6ifdn5X69oSuK+1mUFXEV+l9pc1mVjTVTwOrG3EMBsoekFyICp1pPtfMo1dxBed
R8BFHqQFsFdmIG+59ycFznFOXzDRfaVn6OTEAk7T8nDqnpe/T4GlybLYic6KMKcM
nbMLaJZjHZ97qJb5Scpsd1TWB5TDERi4VPB7NAVC/EwxMPC3IJUbRej/s05gNjg0
+2yyuV/U/DDnNGWnLTJDFLUaE8HhQBMvNSfmdMA47mo5CCuYmpzX/3M9vlVsv8/R
xJBxFLIj9VFCPFNgXPeu9gyyytXeWgsIpDzMNJil9tgbBuQ1dX5GFMkWtK/kPexM
KfiSU0JgJFfFSm0OKI/KXcRlbA1zP3IF+2YwbL+P5ePinHsDiAPLCQt/dWgw2tfB
ZZLj9c3Ukew6Qobuy3V1knl564qQ6wjf
=1m7R
-----END PGP PUBLIC KEY BLOCK-----
KEY
}

load_package_contract

readonly rpm_size=294992
readonly package_name=pgbouncer
readonly package_epoch=0
readonly package_version=1.25.2
readonly package_release=1.fc44
readonly package_architecture=x86_64
readonly source_rpm=pgbouncer-1.25.2-1.fc44.src.rpm
readonly build_time=1778342976
readonly build_host=buildhw-x86-12.rdu3.fedoraproject.org
readonly packager='Fedora Project'
readonly package_license='ISC and BSD-2-Clause'
readonly payload_sha256=093131979d3d1858e30103e1490dbb7af40d51c3541f27f1dfb753ff7fb63eca
readonly payload_sha256_algorithm=8
readonly payload_format=cpio
readonly payload_compressor=zstd
readonly payload_flags=19
readonly signature_header='RSA/SHA256, Sat May  9 16:16:26 2026, Key ID dbfcf71c6d9f90a6'
readonly header_sha256=10626db037a57e4d205a1b915f2b57176b474c83dc37c616da92e5c2eee89c58
readonly signing_key_sha256=93642aec521a1e5e96dd715f7ae0ec0850ebc9de09a94ce03cae5263f26cc18a
readonly signing_key_created=1736879931
readonly signing_key_uid='Fedora (44) <fedora-44-primary@fedoraproject.org>'
readonly file_manifest_sha256=a3e2b707fc84df91e8a53ea0b1fdca6fcd40d579af547c97e881a1253c65209b
readonly file_manifest_count=30

verify_installed=0
if [[ "$#" == 5 && "$1" == '--rpm' && "$3" == '--sha256-file' && "$5" == '--verify-installed' ]]; then
  verify_installed=1
elif [[ "$#" != 4 || "$1" != '--rpm' || "$3" != '--sha256-file' ]]; then
  usage
  exit 2
fi
rpm_path="$2"
sha256_path="$4"

for command_path in \
  /usr/bin/awk /usr/bin/cat /usr/bin/env /usr/bin/gpg /usr/bin/install \
  /usr/bin/jq /usr/bin/mktemp /usr/bin/printf /usr/bin/readlink /usr/bin/realpath \
  /usr/bin/rm /usr/bin/rpm /usr/bin/rpmkeys /usr/bin/sha256sum /usr/bin/stat \
  /usr/bin/wc; do
  [[ -x "$command_path" ]] || die "required command is missing: $command_path"
done
unset command_path
for path in "$rpm_path" "$sha256_path"; do
  [[ "$path" == /* && "$path" == "$(/usr/bin/realpath -e -- "$path")" && -f "$path" && ! -L "$path" &&
     "$path" != *:* && "$path" != *$'\n'* ]] ||
    die 'artifact paths must name canonical absolute non-symlink regular files without transport delimiters'
done
[[ "$rpm_path" != "$sha256_path" ]] || die 'RPM and digest paths must differ'

mapfile -t digest_lines <"$sha256_path"
(( ${#digest_lines[@]} == 1 )) || die 'RPM digest file must contain exactly one line'
expected_sha256="${digest_lines[0]}"
[[ "$expected_sha256" =~ ^[0-9a-f]{64}$ ]] || die 'RPM digest file is noncanonical'
[[ "$expected_sha256" == "$PGBOUNCER_RPM_SHA256" ]] || die 'RPM digest file differs from the release lock'
[[ "$(/usr/bin/sha256sum -- "$rpm_path" | /usr/bin/awk '{print $1}')" == "$PGBOUNCER_RPM_SHA256" ]] ||
  die 'RPM bytes differ from the release lock'
[[ "$(/usr/bin/stat -c '%s' -- "$rpm_path")" == "$rpm_size" ]] || die 'RPM size differs from the reviewed artifact'

work_directory="$(/usr/bin/mktemp -d)"
cleanup() {
  /usr/bin/rm -rf -- "$work_directory"
}
trap cleanup EXIT
/usr/bin/install -d -m 0700 -- "$work_directory/gnupg" "$work_directory/rpmdb"
key_path="$work_directory/RPM-GPG-KEY-fedora-44-primary"
write_fedora_44_signing_key "$key_path"
[[ "$(/usr/bin/sha256sum -- "$key_path" | /usr/bin/awk '{print $1}')" == "$signing_key_sha256" ]] ||
  die 'embedded Fedora 44 signing key bytes differ from the reviewed identity'

key_metadata="$(GNUPGHOME="$work_directory/gnupg" /usr/bin/gpg --batch --no-options --with-colons \
  --show-keys "$key_path" 2>/dev/null)" || die 'embedded Fedora 44 signing key is not a valid OpenPGP key'
[[ "$(/usr/bin/awk -F: '$1 == "pub" { count++ } END { print count + 0 }' <<<"$key_metadata")" == 1 ]] ||
  die 'embedded Fedora 44 signing key does not contain exactly one primary key'
[[ "$(/usr/bin/awk -F: '$1 == "fpr" { count++ } END { print count + 0 }' <<<"$key_metadata")" == 1 ]] ||
  die 'embedded Fedora 44 signing key does not contain exactly one fingerprint'
signing_key_id="${PGBOUNCER_RPM_SIGNING_FINGERPRINT: -16}"
key_primary="$(/usr/bin/awk -F: '$1 == "pub" { print $3 "|" $4 "|" tolower($5) "|" $6; exit }' <<<"$key_metadata")"
[[ "$key_primary" == "4096|1|${signing_key_id}|${signing_key_created}" ]] ||
  die 'embedded Fedora 44 signing key primary identity differs from the package lock'
[[ "$(/usr/bin/awk -F: '$1 == "fpr" { print tolower($10); exit }' <<<"$key_metadata")" == "$PGBOUNCER_RPM_SIGNING_FINGERPRINT" ]] ||
  die 'embedded Fedora 44 signing key fingerprint differs from the package lock'
[[ "$(/usr/bin/awk -F: '$1 == "uid" { print $10; exit }' <<<"$key_metadata")" == "$signing_key_uid" ]] ||
  die 'embedded Fedora 44 signing key UID differs from the reviewed identity'

/usr/bin/rpmkeys --dbpath "$work_directory/rpmdb" --import "$key_path" >/dev/null 2>&1 ||
  die 'embedded Fedora 44 signing key could not be imported into the isolated RPM database'
signature_output="$(/usr/bin/rpmkeys --dbpath "$work_directory/rpmdb" --checksig --verbose "$rpm_path" 2>&1)" ||
  die 'RPM signature, header digest, or payload digest verification failed'
mapfile -t signature_lines <<<"$signature_output"
expected_signature_line="    Header OpenPGP V4 RSA/SHA256 signature, key fingerprint: ${PGBOUNCER_RPM_SIGNING_FINGERPRINT}: OK"
(( ${#signature_lines[@]} == 4 )) &&
  [[ "${signature_lines[0]}" == "${rpm_path}:" ]] &&
  [[ "${signature_lines[1]}" == "$expected_signature_line" ]] &&
  [[ "${signature_lines[2]}" == '    Header SHA256 digest: OK' ]] &&
  [[ "${signature_lines[3]}" == '    Payload SHA256 digest: OK' ]] ||
  die 'RPM verification result does not exactly attest the locked signature, header, and payload'

metadata="$(/usr/bin/rpm -qp --qf $'%{NAME}\n%{EPOCHNUM}\n%{VERSION}\n%{RELEASE}\n%{ARCH}\n%{SOURCERPM}\n%{BUILDTIME}\n%{BUILDHOST}\n%{PACKAGER}\n%{LICENSE}\n%{PAYLOADSHA256}\n%{PAYLOADSHA256ALGO}\n%{PAYLOADFORMAT}\n%{PAYLOADCOMPRESSOR}\n%{PAYLOADFLAGS}\n%{RSAHEADER:pgpsig}\n%{SHA256HEADER}\n' -- "$rpm_path")" ||
  die 'RPM header metadata could not be queried'
mapfile -t metadata_values <<<"$metadata"
(( ${#metadata_values[@]} == 17 )) &&
  [[ "${metadata_values[0]}" == "$package_name" ]] &&
  [[ "${metadata_values[1]}" == "$package_epoch" ]] &&
  [[ "${metadata_values[2]}" == "$package_version" ]] &&
  [[ "${metadata_values[3]}" == "$package_release" ]] &&
  [[ "${metadata_values[4]}" == "$package_architecture" ]] &&
  [[ "${metadata_values[5]}" == "$source_rpm" ]] &&
  [[ "${metadata_values[6]}" == "$build_time" ]] &&
  [[ "${metadata_values[7]}" == "$build_host" ]] &&
  [[ "${metadata_values[8]}" == "$packager" ]] &&
  [[ "${metadata_values[9]}" == "$package_license" ]] &&
  [[ "${metadata_values[10]}" == "$payload_sha256" ]] &&
  [[ "${metadata_values[11]}" == "$payload_sha256_algorithm" ]] &&
  [[ "${metadata_values[12]}" == "$payload_format" ]] &&
  [[ "${metadata_values[13]}" == "$payload_compressor" ]] &&
  [[ "${metadata_values[14]}" == "$payload_flags" ]] &&
  [[ "${metadata_values[15]}" == "$signature_header" ]] &&
  [[ "${metadata_values[16]}" == "$header_sha256" ]] ||
  die 'RPM header metadata differs from the reviewed PgBouncer 1.25.2 artifact'
[[ "${metadata_values[0]}-${metadata_values[2]}-${metadata_values[3]}.${metadata_values[4]}" == "$PGBOUNCER_RPM_NEVRA" ]] ||
  die 'RPM NEVRA differs from the release lock'

queried_manifest="$work_directory/package-files.dump"
/usr/bin/rpm -qp --dump -- "$rpm_path" >"$queried_manifest" || die 'RPM file manifest could not be queried'
[[ "$(/usr/bin/sha256sum -- "$queried_manifest" | /usr/bin/awk '{print $1}')" == "$file_manifest_sha256" ]] ||
  die 'RPM file manifest differs from the reviewed artifact'
[[ "$(/usr/bin/wc -l <"$queried_manifest")" == "$file_manifest_count" ]] ||
  die 'RPM file manifest entry count differs from the reviewed artifact'
binary_manifest_line="$(/usr/bin/awk -v path="$PGBOUNCER_BINARY_PATH" '$1 == path { print; count++ } END { if (count != 1) exit 1 }' "$queried_manifest")" ||
  die 'RPM file manifest does not contain exactly one locked PgBouncer binary'
read -r binary_path binary_size _ binary_sha256 binary_mode binary_owner binary_group binary_config binary_doc binary_rdev binary_link <<<"$binary_manifest_line"
[[ "$binary_path" == "$PGBOUNCER_BINARY_PATH" && "$binary_size" == "$PGBOUNCER_BINARY_SIZE" &&
   "$binary_sha256" == "$PGBOUNCER_BINARY_SHA256" && "$binary_mode" == "010${PGBOUNCER_BINARY_MODE}" &&
   "$binary_owner" == "$PGBOUNCER_BINARY_OWNER" && "$binary_group" == "$PGBOUNCER_BINARY_GROUP" &&
   "$binary_config" == 0 && "$binary_doc" == 0 && "$binary_rdev" == 0 && "$binary_link" == X ]] ||
  die 'RPM PgBouncer binary manifest entry differs from the shared package lock'

if (( verify_installed == 1 )); then
  installed_nevra="$(/usr/bin/rpm -q --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}\n' -- "$package_name")" ||
    die 'locked PgBouncer package is not installed'
  [[ "$installed_nevra" == "$PGBOUNCER_RPM_NEVRA" ]] ||
    die 'installed PgBouncer NEVRA differs from the release lock'
  installed_manifest="$work_directory/installed-package-files.dump"
  /usr/bin/rpm -q --dump -- "$package_name" >"$installed_manifest" ||
    die 'installed PgBouncer package file manifest could not be queried'
  [[ "$(/usr/bin/sha256sum -- "$installed_manifest" | /usr/bin/awk '{print $1}')" == "$file_manifest_sha256" ]] ||
    die 'installed PgBouncer package file manifest differs from the reviewed artifact'
  if ! /usr/bin/rpm --verify -- "$package_name" >"$work_directory/installed-verify.log" 2>&1; then
    die 'installed PgBouncer files differ from the signed package manifest'
  fi
  [[ ! -s "$work_directory/installed-verify.log" ]] ||
    die 'installed PgBouncer verification produced unexpected file differences'
  runtime_version="$("$PGBOUNCER_BINARY_PATH" --version 2>&1)" ||
    die 'installed PgBouncer binary cannot report its version'
  [[ "${runtime_version%%$'\n'*}" == "PgBouncer ${package_version}" ]] ||
    die 'installed PgBouncer binary version differs from the release lock'
fi

/usr/bin/jq -cn \
  --arg architecture "$package_architecture" \
  --arg file_manifest_sha256 "$file_manifest_sha256" \
  --arg key_fingerprint "${PGBOUNCER_RPM_SIGNING_FINGERPRINT^^}" \
  --arg nevra "$PGBOUNCER_RPM_NEVRA" \
  --arg payload_sha256 "$payload_sha256" \
  --arg rpm_sha256 "$PGBOUNCER_RPM_SHA256" \
  --argjson installed_verified "$verify_installed" '
    {
      architecture: $architecture,
      fileManifestSha256: $file_manifest_sha256,
      installedVerified: ($installed_verified == 1),
      nevra: $nevra,
      payloadSha256: $payload_sha256,
      rpmSha256: $rpm_sha256,
      schema: "ascendany.pgbouncer-rpm-attestation.v1",
      signingKeyFingerprint: $key_fingerprint
    }
  '
