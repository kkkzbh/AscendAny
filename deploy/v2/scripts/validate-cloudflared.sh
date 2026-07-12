#!/usr/bin/bash -p
set +x
set -euo pipefail

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  environment_is_clean=1
  while IFS= read -r -d '' entry; do
    name="${entry%%=*}"
    case "$name" in
      PATH|LC_ALL|PWD|SHLVL|_|ASCENDANY_CLOUDFLARED_GATE_CLEAN_ENV|ASCENDANY_VALIDATION_PHASE)
        ;;
      *) environment_is_clean=0 ;;
    esac
  done < <(/usr/bin/env -0)
  if [[ "${ASCENDANY_CLOUDFLARED_GATE_CLEAN_ENV-}" != "1" ||
        "${PATH-}" != "/usr/bin:/bin" || "${LC_ALL-}" != "C" ||
        "$environment_is_clean" != "1" ]]; then
    phase_input="${ASCENDANY_VALIDATION_PHASE-}"
    exec /usr/bin/env -i \
      PATH=/usr/bin:/bin \
      LC_ALL=C \
      ASCENDANY_CLOUDFLARED_GATE_CLEAN_ENV=1 \
      "ASCENDANY_VALIDATION_PHASE=$phase_input" \
      /usr/bin/bash -p "$0" "$@"
  fi
fi

export LC_ALL=C
umask 077

readonly release_root=/opt/ascendany/v2
readonly release_config="$release_root/config/cloudflared.yaml"
readonly release_package_lock="$release_root/config/fedora-runtime-packages.json"
readonly release_unit="$release_root/systemd/ascendany-cloudflared.service"
readonly installed_unit=/etc/systemd/system/ascendany-cloudflared.service
readonly unit_name=ascendany-cloudflared.service
readonly fedora_global_service_dropin=/usr/lib/systemd/system/service.d/10-timeout-abort.conf
readonly package_name=cloudflared
readonly package_nevra=cloudflared-2026.7.1-1.x86_64
readonly package_binary=/usr/bin/cloudflared
readonly package_binary_sha256=79a0ade7fc854f62c1aaef48424d9d979e8c2fcd039189d24db82b84cd146be1
readonly package_binary_size=39251522
readonly package_signing_fingerprint=cc94b39c77ae7342a68b89628a682d308d4e5e73
readonly tunnel_id=e448a34c-9274-4c9d-8c69-e1a7fa369e52
readonly public_hostname=ascendany.kkkzbh.cn
readonly shadow_hostname=ascendany-v2.kkkzbh.cn
readonly trainer_hostname=ascendany-trainer.kkkzbh.cn
readonly metrics_origin=http://127.0.0.1:20090
readonly local_origin=http://127.0.0.1:18000
readonly encrypted_credential=/etc/ascendany/credentials/cloudflare_tunnel_credentials.cred
readonly runtime_credential=/run/credentials/ascendany-cloudflared.service/tunnel_credentials
readonly expected_exec_start='/usr/bin/cloudflared tunnel --config /opt/ascendany/v2/config/cloudflared.yaml --metrics 127.0.0.1:20090 --loglevel info --transport-loglevel warn run --credentials-file /run/credentials/ascendany-cloudflared.service/tunnel_credentials e448a34c-9274-4c9d-8c69-e1a7fa369e52'

failures=0
temporary_workspace=''

pass() {
  printf 'PASS %s\n' "$*"
}

fail() {
  printf 'FAIL %s\n' "$*" >&2
  failures=$((failures + 1))
}

cleanup() {
  if [[ -n "$temporary_workspace" && -d "$temporary_workspace" ]]; then
    /usr/bin/rm -rf -- "$temporary_workspace"
  fi
}
trap cleanup EXIT

check_root_owned_ancestry() {
  local path="$1" current metadata owner mode
  [[ "$path" == /* && "$path" == "$(/usr/bin/realpath -m -- "$path")" &&
     -e "$path" && "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null || true)" ]] ||
    return 1
  current="$(/usr/bin/dirname -- "$path")"
  while :; do
    [[ -d "$current" && ! -L "$current" ]] || return 1
    metadata="$(/usr/bin/stat -Lc '%u:%a' -- "$current" 2>/dev/null)" || return 1
    IFS=: read -r owner mode <<<"$metadata"
    [[ "$owner" == 0 ]] || return 1
    (( (8#$mode & 8#022) == 0 )) || return 1
    [[ "$current" == / ]] && break
    current="$(/usr/bin/dirname -- "$current")"
  done
}

unit_property() {
  /usr/bin/systemctl show "$1" --property="$2" --value 2>/dev/null
}

normalize_word_set() {
  /usr/bin/tr '[:space:]' '\n' | /usr/bin/sed '/^$/d' | LC_ALL=C /usr/bin/sort
}

package_lock_document_is_valid() {
  local document="$1"
  /usr/bin/jq -e \
    --arg nevra "$package_nevra" \
    --arg binary "$package_binary" \
    --arg binary_sha "$package_binary_sha256" \
    --arg fingerprint "$package_signing_fingerprint" \
    --argjson binary_size "$package_binary_size" '
      type == "object" and
      (keys == ["architecture", "fedoraRelease", "packages", "schema"]) and
      .schema == "ascendany.fedora-runtime-packages.v1" and
      .fedoraRelease == 44 and .architecture == "x86_64" and
      (.packages | type == "object" and keys == ["cloudflared", "pgbouncer"]) and
      (.packages.cloudflared | type == "object" and
        keys == ["files", "nevra", "rpmSHA256", "signingFingerprint"]) and
      .packages.cloudflared.nevra == $nevra and
      .packages.cloudflared.rpmSHA256 == "b9143a52ee388e330fb7300fa740de0c488415e777fb219af7ec9a070982f790" and
      .packages.cloudflared.signingFingerprint == $fingerprint and
      .packages.cloudflared.files == [{
        group:"root", mode:"0755", owner:"root", path:$binary,
        sha256:$binary_sha, size:$binary_size
      }]
    ' "$document" >/dev/null
}

tunnel_credential_document_is_valid() {
  local document="$1" secret_length
  [[ -s "$document" && ! -L "$document" ]] || return 1
  /usr/bin/jq -e --arg tunnel "$tunnel_id" '
    type == "object" and
    (keys == ["AccountTag", "Endpoint", "TunnelID", "TunnelSecret"]) and
    .TunnelID == $tunnel and .Endpoint == "" and
    (.AccountTag | type == "string" and test("^[0-9a-f]{32}$")) and
    (.TunnelSecret | type == "string" and test("^[A-Za-z0-9+/]+={0,2}$"))
  ' "$document" >/dev/null 2>&1 || return 1
  secret_length="$(/usr/bin/jq -er '.TunnelSecret' "$document" |
    /usr/bin/base64 --decode 2>/dev/null | /usr/bin/wc -c)" || return 1
  [[ "$secret_length" == 32 ]]
}

validate_release_inputs() {
  local path metadata
  for path in "$release_config" "$release_package_lock" "$release_unit"; do
    metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path" 2>/dev/null || true)"
    if [[ ! -f "$path" || -L "$path" || "$metadata" != 0:0:644:1 ]] ||
       ! check_root_owned_ancestry "$path"; then
      fail "release-owned cloudflared input is missing or mutable: $path"
    fi
  done
  metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$installed_unit" 2>/dev/null || true)"
  if [[ ! -f "$installed_unit" || -L "$installed_unit" || "$metadata" != 0:0:644:1 ]] ||
     ! check_root_owned_ancestry "$installed_unit" ||
     ! /usr/bin/cmp --silent -- "$release_unit" "$installed_unit"; then
    fail "installed cloudflared unit differs from the reviewed release"
  else
    pass "installed cloudflared unit matches the reviewed release"
  fi

  if ! package_lock_document_is_valid "$release_package_lock"; then
    fail "Fedora runtime package lock does not contain the exact cloudflared artifact"
  else
    pass "Fedora runtime package lock fixes the cloudflared RPM and binary"
  fi

  if ! "$package_binary" tunnel --config "$release_config" ingress validate >/dev/null 2>&1; then
    fail "release-owned locally managed Tunnel ingress is invalid"
  else
    pass "release-owned locally managed Tunnel ingress is valid"
  fi
}

validate_package() {
  local metadata installed_nevra binary_sha version verify_output verify_status=0
  local package_header package_manifest package_manifest_sha package_manifest_count signing_key
  installed_nevra="$(/usr/bin/rpm -q --qf '%{NEVRA}' "$package_name" 2>/dev/null || true)"
  metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h:%s' -- "$package_binary" 2>/dev/null || true)"
  binary_sha="$(/usr/bin/sha256sum -- "$package_binary" 2>/dev/null | /usr/bin/awk '{print $1}' || true)"
  version="$($package_binary --version 2>/dev/null || true)"
  verify_output="$(/usr/bin/rpm --verify "$package_name" 2>&1)" || verify_status=$?
  package_header="$(/usr/bin/rpm -q --qf $'%{SOURCERPM}\n%{BUILDTIME}\n%{BUILDHOST}\n%{PACKAGER}\n%{LICENSE}\n%{PAYLOADSHA256}\n%{PAYLOADSHA256ALGO}\n%{PAYLOADFORMAT}\n%{PAYLOADCOMPRESSOR}\n%{PAYLOADFLAGS}\n%{RSAHEADER:pgpsig}\n%{SHA256HEADER}' "$package_name" 2>/dev/null || true)"
  package_manifest="$(/usr/bin/rpm -q --dump "$package_name" 2>/dev/null || true)"
  package_manifest_sha="$(/usr/bin/printf '%s\n' "$package_manifest" | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')"
  package_manifest_count="$(/usr/bin/printf '%s\n' "$package_manifest" | /usr/bin/wc -l)"
  signing_key="$(/usr/bin/rpm -q --qf '%{NAME}-%{VERSION}-%{RELEASE}' \
    gpg-pubkey-cc94b39c77ae7342a68b89628a682d308d4e5e73-68fa3b78 2>/dev/null || true)"
  if [[ "$installed_nevra" != "$package_nevra" ||
        "$metadata" != "0:0:755:1:$package_binary_size" ||
        "$binary_sha" != "$package_binary_sha256" ||
        "$version" != 'cloudflared version 2026.7.1 (built 2026-07-09-13:00 UTC)' ||
        "$verify_status" != 0 || -n "$verify_output" ||
        "$signing_key" != 'gpg-pubkey-cc94b39c77ae7342a68b89628a682d308d4e5e73-68fa3b78' ||
        "$package_manifest_sha" != 'a4bd7c1a93058c19e9e47134ad5192bd57cebe3d6e197998ec478152b68bb04d' ||
        "$package_manifest_count" != 2 ||
        "$package_header" != $'cloudflared-2026.7.1-1.src.rpm\n1783602065\nrunner-0-zkdodqd-project-417-concurrent-0-fjb2kqg4\nCloudflare <support@cloudflare.com>\nApache License Version 2.0\n1c6b7707938e2d5544476d8ed69281833ac7ef7d7da3c8fc6c8f4c473df20451\n8\ncpio\ngzip\n9\nRSA/SHA512, Thu Jul  9 21:22:12 2026, Key ID 8a682d308d4e5e73\n815cbdef99b08d7c89207416152aad8786c331d06c64b7d54e985dbd455231bd' ]] ||
     ! check_root_owned_ancestry "$package_binary"; then
    fail "installed cloudflared package, signer, manifest, or binary differs from the release lock"
  else
    pass "installed cloudflared signed package manifest and binary match the release lock"
  fi
}

validate_credential_source() {
  local metadata
  metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$encrypted_credential" 2>/dev/null || true)"
  if [[ ! -s "$encrypted_credential" || -L "$encrypted_credential" ||
        "$metadata" != 0:0:400:1 ]] || ! check_root_owned_ancestry "$encrypted_credential"; then
    fail "cloudflared encrypted Tunnel credential source is not protected"
    return
  fi
  if ! tunnel_credential_document_is_valid "$runtime_credential"; then
    fail "decrypted cloudflared Tunnel credential has an invalid identity document"
    return
  fi
  pass "cloudflared Tunnel credential is encrypted at rest and bound to the local Tunnel"
}

validate_unit() {
  local active enabled fragment dropins reload dynamic user group main_pid exec_start
  local actual_address_families expected_address_families
  active="$(/usr/bin/systemctl is-active "$unit_name" 2>/dev/null || true)"
  enabled="$(/usr/bin/systemctl is-enabled "$unit_name" 2>/dev/null || true)"
  fragment="$(unit_property "$unit_name" FragmentPath || true)"
  dropins="$(unit_property "$unit_name" DropInPaths || true)"
  reload="$(unit_property "$unit_name" NeedDaemonReload || true)"
  dynamic="$(unit_property "$unit_name" DynamicUser || true)"
  user="$(unit_property "$unit_name" User || true)"
  group="$(unit_property "$unit_name" Group || true)"
  main_pid="$(unit_property "$unit_name" MainPID || true)"
  exec_start="$(unit_property "$unit_name" ExecStart || true)"
  if [[ "$active" != active || "$enabled" != enabled ||
        "$fragment" != "$installed_unit" ||
        "$dropins" != "$fedora_global_service_dropin" || "$reload" != no ||
        "$dynamic" != yes || "$user" != ascendany-cloudflared ||
        "$group" != ascendany-cloudflared || ! "$main_pid" =~ ^[1-9][0-9]*$ ]]; then
    fail "cloudflared systemd unit state or dynamic capability identity differs from the contract"
    return
  fi
  if [[ ! -f "$fedora_global_service_dropin" || -L "$fedora_global_service_dropin" ||
        "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$fedora_global_service_dropin" 2>/dev/null || true)" != 0:0:644:1 ]] ||
     ! check_root_owned_ancestry "$fedora_global_service_dropin" ||
     [[ "$(/usr/bin/sed -E -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$/d' -e 's/^[[:space:]]+//' -e 's/[[:space:]]+$//' "$fedora_global_service_dropin" 2>/dev/null)" != $'[Service]\nTimeoutStopFailureMode=abort' ]]; then
    fail "Fedora global service drop-in differs from the reviewed timeout-abort contract"
  fi
  if [[ "$exec_start" != *"argv[]=$expected_exec_start"* ]]; then
    fail "cloudflared effective ExecStart differs from the reviewed command"
  fi
  for property_value in \
    'NoNewPrivileges yes' \
    'PrivateTmp yes' \
    'PrivateDevices yes' \
    'PrivateUsers yes' \
    'ProtectSystem strict' \
    'ProtectHome yes' \
    'ProtectControlGroups yes' \
    'ProtectKernelTunables yes' \
    'ProtectKernelModules yes' \
    'ProtectKernelLogs yes' \
    'ProtectClock yes' \
    'ProtectHostname yes' \
    'ProtectProc invisible' \
    'ProcSubset pid' \
    'RestrictNamespaces yes' \
    'RestrictRealtime yes' \
    'RestrictSUIDSGID yes' \
    'LockPersonality yes' \
    'MemoryDenyWriteExecute yes' \
    'RemoveIPC yes' \
    'KeyringMode private' \
    'DevicePolicy closed'; do
    property="${property_value%% *}"
    expected="${property_value#* }"
    if [[ "$(unit_property "$unit_name" "$property" || true)" != "$expected" ]]; then
      fail "$unit_name effective $property differs from $expected"
    fi
  done
  actual_address_families="$(unit_property "$unit_name" RestrictAddressFamilies | normalize_word_set)"
  expected_address_families="$(printf '%s\n' AF_INET AF_INET6 AF_UNIX | LC_ALL=C /usr/bin/sort)"
  if [[ -n "$(unit_property "$unit_name" CapabilityBoundingSet || true)" ||
        -n "$(unit_property "$unit_name" AmbientCapabilities || true)" ||
        "$actual_address_families" != "$expected_address_families" ]]; then
    fail "cloudflared effective capability or address-family set is not closed"
  fi
  pass "cloudflared systemd unit is active under the hardened dynamic identity"
}

validate_process() {
  local pid exe status uid_values gid_values no_new_privs seccomp field value
  local uid_real uid_effective uid_saved uid_fs gid_real gid_effective gid_saved gid_fs
  local -a argv expected_argv
  pid="$(unit_property "$unit_name" MainPID || true)"
  [[ "$pid" =~ ^[1-9][0-9]*$ && -d "/proc/$pid" ]] || {
    fail "cloudflared process is unavailable"
    return
  }
  exe="$(/usr/bin/readlink -e -- "/proc/$pid/exe" 2>/dev/null || true)"
  mapfile -d '' -t argv <"/proc/$pid/cmdline"
  expected_argv=(
    /usr/bin/cloudflared tunnel
    --config /opt/ascendany/v2/config/cloudflared.yaml
    --metrics 127.0.0.1:20090
    --loglevel info
    --transport-loglevel warn
    run --credentials-file "$runtime_credential" "$tunnel_id"
  )
  if [[ "$exe" != "$package_binary" || "$(printf '%s\n' "${argv[@]}")" != "$(printf '%s\n' "${expected_argv[@]}")" ]]; then
    fail "cloudflared runtime executable or argv differs from the reviewed unit"
  fi
  status="/proc/$pid/status"
  uid_values="$(/usr/bin/awk '/^Uid:/ {$1=""; sub(/^[[:space:]]+/, ""); print; exit}' "$status")"
  gid_values="$(/usr/bin/awk '/^Gid:/ {$1=""; sub(/^[[:space:]]+/, ""); print; exit}' "$status")"
  read -r uid_real uid_effective uid_saved uid_fs <<<"$uid_values"
  read -r gid_real gid_effective gid_saved gid_fs <<<"$gid_values"
  no_new_privs="$(/usr/bin/awk '/^NoNewPrivs:/ {print $2; exit}' "$status")"
  seccomp="$(/usr/bin/awk '/^Seccomp:/ {print $2; exit}' "$status")"
  if [[ ! "$uid_real" =~ ^[1-9][0-9]*$ ||
        "$uid_real:$uid_effective:$uid_saved:$uid_fs" != "$uid_real:$uid_real:$uid_real:$uid_real" ||
        ! "$gid_real" =~ ^[1-9][0-9]*$ ||
        "$gid_real:$gid_effective:$gid_saved:$gid_fs" != "$gid_real:$gid_real:$gid_real:$gid_real" ||
        "$no_new_privs" != 1 || "$seccomp" != 2 ]]; then
    fail "cloudflared runtime UID/GID, no-new-privileges, or seccomp state is invalid"
  fi
  for field in CapInh CapPrm CapEff CapBnd CapAmb; do
    value="$(/usr/bin/awk -v field="$field:" '$1 == field {print $2; exit}' "$status")"
    [[ "$value" == 0000000000000000 ]] || fail "cloudflared runtime $field is not empty"
  done
  if /usr/bin/tr '\0' '\n' <"/proc/$pid/environ" |
      /usr/bin/grep -E '(^|_)(HTTP|HTTPS|ALL|NO)_PROXY=|TOKEN=|SECRET=|CREDENTIAL=' >/dev/null; then
    fail "cloudflared runtime inherited proxy or plaintext credential environment"
  fi
  pass "cloudflared runtime process has the exact executable and empty capability sets"
}

probe_http_code() {
  local url="$1" expected="$2" description="$3" code scheme
  scheme="${url%%:*}"
  if ! code="$(/usr/bin/curl --disable --silent --show-error --connect-timeout 5 \
      --max-time 15 --max-filesize 65536 --noproxy '*' --proto "=$scheme" \
      --output /dev/null --write-out '%{http_code}' "$url")"; then
    fail "$description request failed"
  elif [[ "$code" != "$expected" ]]; then
    fail "$description returned HTTP $code; expected $expected"
  else
    pass "$description returned HTTP $expected"
  fi
}

probe_pre_cutover_public_route() {
  local nonce="$1"
  local loopback_legacy="$temporary_workspace/loopback-legacy-meta"
  local public_legacy="$temporary_workspace/public-legacy-meta"
  local legacy_path="/api/v1/meta/latest_exam_imported_at?ascendany_acceptance=$nonce"

  if ! /usr/bin/curl --disable --fail --silent --show-error --connect-timeout 5 \
      --max-time 15 --max-filesize 65536 --noproxy '*' --proto '=http' \
      --output "$loopback_legacy" "http://127.0.0.1:8000$legacy_path" ||
     ! /usr/bin/jq -e '
       type == "object" and keys == ["latestExamImportedAt"] and
       (.latestExamImportedAt | type == "string" and
         test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z$"))
     ' "$loopback_legacy" >/dev/null; then
    fail "legacy loopback ownership probe is unavailable"
    return
  fi
  if ! /usr/bin/curl --disable --fail --silent --show-error --connect-timeout 5 \
      --max-time 15 --max-filesize 65536 --noproxy '*' --proto '=https' \
      --header 'Cache-Control: no-cache' --output "$public_legacy" \
      "https://$public_hostname$legacy_path" ||
     ! /usr/bin/cmp --silent -- "$loopback_legacy" "$public_legacy"; then
    fail "public hostname left the exact legacy origin before the production cutover"
  else
    pass "public hostname remains bound to the exact legacy origin"
  fi
}

probe_live_routes() {
  local phase="$1" nonce local_version shadow_version trainer_version public_version
  if ! IFS= read -r nonce </proc/sys/kernel/random/uuid ||
     [[ ! "$nonce" =~ ^[0-9a-f-]{36}$ ]]; then
    fail "cloudflared route probe nonce cannot be generated"
    return
  fi
  probe_http_code "$metrics_origin/ready" 200 "cloudflared connector readiness"
  probe_http_code "https://$trainer_hostname/__ascendany_unowned_$nonce" 404 \
    "trainer hostname closed route"
  if [[ "$phase" != production ]]; then
    probe_pre_cutover_public_route "$nonce"
  fi
  [[ "$phase" == staged ]] && return

  local_version="$temporary_workspace/local-version"
  shadow_version="$temporary_workspace/shadow-version"
  trainer_version="$temporary_workspace/trainer-version"
  public_version="$temporary_workspace/public-version"
  if ! /usr/bin/curl --disable --fail --silent --show-error --max-time 10 \
      --max-filesize 65536 --noproxy '*' --proto '=http' --output "$local_version" \
      "$local_origin/version?ascendany_acceptance=$nonce"; then
    fail "loopback v2 version probe failed"
    return
  fi
  if ! /usr/bin/curl --disable --fail --silent --show-error --connect-timeout 5 \
      --max-time 15 --max-filesize 65536 --noproxy '*' --proto '=https' \
      --header 'Cache-Control: no-cache' --output "$shadow_version" \
      "https://$shadow_hostname/version?ascendany_acceptance=$nonce" ||
     ! /usr/bin/cmp --silent -- "$local_version" "$shadow_version"; then
    fail "shadow hostname does not reach the exact loopback v2 version"
  else
    pass "shadow hostname reaches the exact loopback v2 version"
  fi
  if ! /usr/bin/curl --disable --fail --silent --show-error --connect-timeout 5 \
      --max-time 15 --max-filesize 65536 --noproxy '*' --proto '=https' \
      --header 'Cache-Control: no-cache' --output "$trainer_version" \
      "https://$trainer_hostname/version?ascendany_acceptance=$nonce" ||
     ! /usr/bin/cmp --silent -- "$local_version" "$trainer_version"; then
    fail "trainer hostname version route does not reach loopback v2"
  else
    pass "trainer hostname version route reaches loopback v2"
  fi
  if [[ "$phase" == production ]]; then
    if ! /usr/bin/curl --disable --fail --silent --show-error --connect-timeout 5 \
        --max-time 15 --max-filesize 65536 --noproxy '*' --proto '=https' \
        --header 'Cache-Control: no-cache' --output "$public_version" \
        "https://$public_hostname/version?ascendany_acceptance=$nonce" ||
       ! /usr/bin/cmp --silent -- "$local_version" "$public_version"; then
      fail "public hostname does not reach the exact loopback v2 version"
    else
      pass "public hostname reaches the exact loopback v2 version"
    fi
  fi
}

validate_retired_connector() {
  local phase="$1"
  [[ "$phase" != production ]] && return
  if /usr/bin/podman container exists ascendany-cloudflared >/dev/null 2>&1 ||
     [[ -e /opt/ascendany/infra/cloudflared || -L /opt/ascendany/infra/cloudflared ]]; then
    fail "production retains the retired remotely managed cloudflared connector"
  else
    pass "production removed the retired remotely managed connector"
  fi
}

main() {
  local phase command
  if (( $# != 0 )); then
    fail "validate-cloudflared.sh accepts no positional arguments"
  fi
  if [[ "$(/usr/bin/id -u)" != 0 ]]; then
    fail "validate-cloudflared.sh must run as root"
  fi
  phase="${ASCENDANY_VALIDATION_PHASE-}"
  [[ "$phase" == staged || "$phase" == smoke || "$phase" == production ]] ||
    fail "ASCENDANY_VALIDATION_PHASE must be exactly staged, smoke, or production"
  for command in awk base64 cmp curl dirname grep id jq mapfile podman readlink realpath \
      rm rpm sed sha256sum sort stat systemctl tail tr wc; do
    command -v "$command" >/dev/null 2>&1 || fail "required command is missing: $command"
  done
  if (( failures > 0 )); then
    printf 'cloudflared validation failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi

  temporary_workspace="$(/usr/bin/mktemp -d /run/ascendany-cloudflared-validation.XXXXXX)"
  /usr/bin/chmod 0700 "$temporary_workspace"
  validate_release_inputs
  validate_package
  validate_unit
  validate_credential_source
  validate_process
  probe_live_routes "$phase"
  validate_retired_connector "$phase"

  if (( failures > 0 )); then
    printf 'cloudflared validation failed with %d finding(s).\n' "$failures" >&2
    return 1
  fi
  printf 'cloudflared %s validation passed.\n' "$phase"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
