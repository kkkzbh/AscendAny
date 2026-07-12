#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly VALIDATOR="$REPOSITORY_ROOT/deploy/v2/scripts/validate-cloudflared.sh"
readonly CONFIG="$REPOSITORY_ROOT/deploy/v2/config/cloudflared.yaml"
readonly PACKAGE_LOCK="$REPOSITORY_ROOT/deploy/v2/config/fedora-runtime-packages.json"
readonly UNIT="$REPOSITORY_ROOT/deploy/v2/systemd/ascendany-cloudflared.service"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-cloudflared-fixture.XXXXXX")"

cleanup() {
  rm -rf -- "$WORK_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for command in base64 cmp jq sha256sum systemd-analyze; do
  command -v "$command" >/dev/null 2>&1 || fail "fixture dependency is missing: $command"
done
[[ -x "$VALIDATOR" && -f "$CONFIG" && -f "$PACKAGE_LOCK" && -f "$UNIT" ]] ||
  fail 'locally managed cloudflared deployment inputs are incomplete'
[[ "$(head -n 1 "$VALIDATOR")" == '#!/usr/bin/bash -p' ]]
[[ "$(sed -n '2p' "$VALIDATOR")" == 'set +x' ]]

printf '%s\n' 'touch "$BASH_ENV_MARKER"' >"$WORK_ROOT/bash-env"
if env \
    BASH_ENV="$WORK_ROOT/bash-env" \
    BASH_ENV_MARKER="$WORK_ROOT/bash-env-executed" \
    SHELLOPTS=xtrace \
    ASCENDANY_VALIDATION_PHASE=invalid \
    DO_NOT_TRACE_THIS_VALUE=present \
    "$VALIDATOR" >"$WORK_ROOT/clean-env.out" 2>"$WORK_ROOT/clean-env.err"; then
  fail 'invalid clean-environment invocation unexpectedly passed'
fi
[[ ! -e "$WORK_ROOT/bash-env-executed" ]] || fail 'validator evaluated caller BASH_ENV'
if grep -F 'DO_NOT_TRACE_THIS_VALUE' "$WORK_ROOT/clean-env.out" "$WORK_ROOT/clean-env.err" >/dev/null; then
  fail 'validator leaked an inherited value through tracing'
fi
grep -Fx 'FAIL ASCENDANY_VALIDATION_PHASE must be exactly staged, smoke, or production' \
  "$WORK_ROOT/clean-env.err" >/dev/null

(
  # shellcheck source=../../deploy/v2/scripts/validate-cloudflared.sh
  source "$VALIDATOR"
  trap - EXIT

  package_lock_document_is_valid "$PACKAGE_LOCK" ||
    fail 'canonical Fedora runtime package lock was rejected'
  jq '.packages.cloudflared.files[0].sha256 = ("0" * 64)' \
    "$PACKAGE_LOCK" >"$WORK_ROOT/package-lock-digest-drift.json"
  if package_lock_document_is_valid "$WORK_ROOT/package-lock-digest-drift.json"; then
    fail 'cloudflared binary digest drift was accepted'
  fi
  jq '.packages.extra = .packages.cloudflared' \
    "$PACKAGE_LOCK" >"$WORK_ROOT/package-lock-extra.json"
  if package_lock_document_is_valid "$WORK_ROOT/package-lock-extra.json"; then
    fail 'extra Fedora runtime package was accepted'
  fi

  secret="$(printf 's%.0s' {1..32} | base64 -w0)"
  jq -cn \
    --arg account '0123456789abcdef0123456789abcdef' \
    --arg tunnel "$tunnel_id" \
    --arg secret "$secret" \
    '{AccountTag:$account,TunnelSecret:$secret,TunnelID:$tunnel,Endpoint:""}' \
    >"$WORK_ROOT/tunnel-credential.json"
  tunnel_credential_document_is_valid "$WORK_ROOT/tunnel-credential.json" ||
    fail 'canonical tunnel-scoped credential was rejected'
  jq '.TunnelID = "12345678-1234-4123-8123-123456789abc"' \
    "$WORK_ROOT/tunnel-credential.json" >"$WORK_ROOT/tunnel-wrong-id.json"
  if tunnel_credential_document_is_valid "$WORK_ROOT/tunnel-wrong-id.json"; then
    fail 'wrong Tunnel identity was accepted'
  fi
  short_secret="$(printf 's%.0s' {1..31} | base64 -w0)"
  jq --arg secret "$short_secret" '.TunnelSecret = $secret' \
    "$WORK_ROOT/tunnel-credential.json" >"$WORK_ROOT/tunnel-short-secret.json"
  if tunnel_credential_document_is_valid "$WORK_ROOT/tunnel-short-secret.json"; then
    fail 'short Tunnel secret was accepted'
  fi
  jq '.extra = true' "$WORK_ROOT/tunnel-credential.json" >"$WORK_ROOT/tunnel-extra.json"
  if tunnel_credential_document_is_valid "$WORK_ROOT/tunnel-extra.json"; then
    fail 'Tunnel credential with an unknown field was accepted'
  fi
)

cloudflared tunnel --config "$CONFIG" ingress validate >/dev/null
for probe in \
  'https://ascendany.kkkzbh.cn/version|Matched rule #0' \
  'https://ascendany-v2.kkkzbh.cn/version|Matched rule #1' \
  'https://ascendany-trainer.kkkzbh.cn/version|Matched rule #2' \
  'https://ascendany-trainer.kkkzbh.cn/api/v2/internal/recommendation/trainer-agent/claims/test|Matched rule #3' \
  'https://ascendany-trainer.kkkzbh.cn/unowned|Matched rule #4' \
  'https://unowned.example.invalid/path|Matched rule #5'; do
  url="${probe%%|*}"
  expected="${probe#*|}"
  cloudflared tunnel --config "$CONFIG" ingress rule "$url" >"$WORK_ROOT/rule.out"
  grep -F "$expected" "$WORK_ROOT/rule.out" >/dev/null ||
    fail "ingress routing drifted for $url"
done

for directive in \
  'DynamicUser=yes' \
  'User=ascendany-cloudflared' \
  'Group=ascendany-cloudflared' \
  'LoadCredentialEncrypted=tunnel_credentials:/etc/ascendany/credentials/cloudflare_tunnel_credentials.cred' \
  'PrivateUsers=yes' \
  'CapabilityBoundingSet=' \
  'AmbientCapabilities=' \
  'MemoryDenyWriteExecute=yes' \
  'InaccessiblePaths=-/etc/ascendany -/opt/ascendany/Release -/var/lib/ascendany/artifacts' \
  'SystemCallFilter=@system-service'; do
  [[ "$(grep -Fxc -- "$directive" "$UNIT")" == 1 ]] ||
    fail "cloudflared unit directive is missing or duplicated: $directive"
done
grep -F -- '--credentials-file %d/tunnel_credentials e448a34c-9274-4c9d-8c69-e1a7fa369e52' \
  "$UNIT" >/dev/null
if grep -E '(^|[[:space:]])(Environment=|EnvironmentFile=).*([Tt][Oo][Kk][Ee][Nn]|[Ss][Ee][Cc][Rr][Ee][Tt])' \
    "$UNIT" >/dev/null; then
  fail 'cloudflared unit carries a plaintext credential environment path'
fi
if grep -F 'docker.io/cloudflare/cloudflared@' "$VALIDATOR" >/dev/null ||
   grep -F 'cloudflare-api-token' "$VALIDATOR" >/dev/null ||
   grep -F 'token-file' "$VALIDATOR" >/dev/null; then
  fail 'validator retains the retired container or remote-management credential path'
fi

systemd-analyze verify "$UNIT"
bash -n "$VALIDATOR"
git -C "$REPOSITORY_ROOT" diff --check -- \
  deploy/v2/config/cloudflared.yaml \
  deploy/v2/config/fedora-runtime-packages.json \
  deploy/v2/scripts/validate-cloudflared.sh \
  deploy/v2/systemd/ascendany-cloudflared.service \
  tools/tests/validate-cloudflared-fixture.sh

printf 'locally managed cloudflared fixture passed\n'
