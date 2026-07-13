#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly INSTALLER_SOURCE="$REPOSITORY_ROOT/deploy/v2/scripts/install-v2-release.sh"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-install-v2-release-fixture.XXXXXX")"
readonly RELEASE_OPS_BINARY="$WORK_ROOT/ascendany-release-ops"
readonly MODEL_BINARY="$WORK_ROOT/ascendany-model"
readonly SYSTEMCTL_BINARY="$WORK_ROOT/systemctl"

readonly -a PAYLOAD_PATHS=(
  bin/ascendanyd
  bin/ascendany-admin-bootstrap
  bin/ascendany-backup
  bin/ascendany-judge
  bin/ascendany-lsp
  bin/ascendany-migrate
  bin/ascendany-model
  bin/ascendany-release-ops
  models/recommendation-model.json
  README.md
  OJ_JUDGE_CONTRACT.md
  LSP_CONTROL_CONTRACT.md
  contracts/openapi/ascendany-v2.yaml
  contracts/pintia/ascendany.pintia.snapshot.v2.schema.json
  db/roles/README.md
  db/roles/001_v2_roles.sql
  db/roles/verify_v2_roles.sql
  config/analytics.json
  config/ascendanyd.env
  config/ascendanyd-read-only-smoke.env
  config/backup.env
  config/cloudflared.yaml
  config/fedora-runtime-packages.json
  config/judge.env
  config/judge-image-lock.json
  config/migrate.env
  config/pgbouncer-hba.conf
  config/pgbouncer.ini
  config/postgresql-hba.conf
  config/postgresql-ident.conf
  config/restore.env
  systemd/ascendanyd.service
  systemd/ascendany-model-activate.service
  systemd/ascendanyd.service.d/40-read-only-smoke.conf
  systemd/ascendany-admin-bootstrap.service
  systemd/ascendany-backup.service
  systemd/ascendany-backup.timer
  systemd/ascendany-cloudflared.service
  systemd/ascendany-judge@.service
  systemd/ascendany-lsp@.service
  systemd/ascendany-migrate.service
  systemd/ascendany-pgbouncer.service
  systemd/ascendany-restore-verify@.service
  polkit-1/rules.d/60-ascendany-judge.rules
  polkit-1/rules.d/61-ascendany-lsp.rules
  sysusers.d/ascendany-v2.conf
  tmpfiles.d/ascendany-v2.conf
  scripts/publish-restore-evidence.sh
  scripts/restore-verify-operator.sh
  scripts/install-v2-release.sh
  scripts/acquire-judge-image.sh
  scripts/attest-judge-image.sh
  scripts/judge-image-contract.sh
  scripts/preload-judge-image.sh
  scripts/acquire-pgbouncer-rpm.sh
  scripts/attest-pgbouncer-rpm.sh
  scripts/provision-postgres-pgbouncer.sh
  scripts/validate-cloudflared.sh
  scripts/validate-production.sh
)

cleanup() {
  if [[ "${ASCENDANY_FIXTURE_KEEP_WORK_ROOT:-0}" == 1 ]]; then
    printf 'fixture work root retained: %s\n' "$WORK_ROOT" >&2
    return
  fi
  rm -rf -- "$WORK_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

expect_failure() {
  local log_path="$1"
  shift
  if "$@" >"$log_path" 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

require_log() {
  local log_path="$1"
  local expected="$2"
  grep -F -- "$expected" "$log_path" >/dev/null || {
    printf '%s\n' "expected log fragment: $expected" >&2
    cat "$log_path" >&2
    fail "failure log did not identify the rejected boundary"
  }
}

for command in bwrap go jq sha256sum stat; do
  command -v "$command" >/dev/null 2>&1 || fail "required fixture command is missing: $command"
done
[[ -x "$INSTALLER_SOURCE" ]] || fail 'release installer is not executable'

(
  cd "$REPOSITORY_ROOT/backend"
  env -i \
    PATH=/usr/bin:/bin \
    HOME="${HOME:-/tmp}" \
    GOTOOLCHAIN=local \
    GOENV=off \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOAMD64=v1 \
    /usr/bin/go build -buildvcs=false -trimpath -o "$RELEASE_OPS_BINARY" ./cmd/ascendany-release-ops
)
chmod 0755 "$RELEASE_OPS_BINARY"
cat >"$MODEL_BINARY" <<'MODEL_VERIFIER'
#!/usr/bin/bash -p
set -Eeuo pipefail
[[ "$#" == 7 && "$1" == verify && "$2" == --model && "$4" == --sha256 && "$6" == --expected-purpose ]]
[[ "$(/usr/bin/sha256sum -- "$3" | /usr/bin/awk '{print $1}')" == "$5" ]]
[[ "$(/usr/bin/jq -er '.manifest.purpose' "$3")" == "$7" ]]
MODEL_VERIFIER
chmod 0755 "$MODEL_BINARY"
cat >"$SYSTEMCTL_BINARY" <<'SYSTEMCTL_STUB'
#!/usr/bin/bash -p
set -Eeuo pipefail
case "$1" in
  show)
    unit="${!#}"
    include_main_pid=0
    for argument in "$@"; do
      [[ "$argument" != '--property=MainPID' ]] || include_main_pid=1
    done
    if [[ -f /fixture/active-systemd-unit && "$(</fixture/active-systemd-unit)" == "$unit" ]]; then
      printf '%s\n' 'LoadState=loaded' 'ActiveState=active' 'SubState=running'
      (( include_main_pid == 0 )) || printf '%s\n' 'MainPID=4242'
    else
      printf '%s\n' 'LoadState=loaded' 'ActiveState=inactive' 'SubState=dead'
      (( include_main_pid == 0 )) || printf '%s\n' 'MainPID=0'
    fi
    ;;
  list-units)
    if [[ -f /fixture/active-systemd-unit && "$(</fixture/active-systemd-unit)" == *@*.service ]]; then
      printf '%s loaded active running fixture\n' "$(</fixture/active-systemd-unit)"
    fi
    ;;
  *)
    exit 64
    ;;
esac
SYSTEMCTL_STUB
chmod 0755 "$SYSTEMCTL_BINARY"

declare -a BWRAP_RUNTIME=(
  --unshare-user
  --uid 0
  --gid 0
  --unshare-pid
  --unshare-ipc
  --unshare-uts
  --die-with-parent
  --new-session
  --proc /proc
  --dev-bind /dev /dev
  --ro-bind /usr /usr
  --symlink usr/bin /bin
  --symlink usr/lib /lib
  --tmpfs /tmp
  --tmpfs /run
)
[[ -d /usr/lib64 ]] || fail 'fixture requires the production merged-/usr lib64 layout'
BWRAP_RUNTIME+=(--symlink usr/lib64 /lib64)
readonly -a BWRAP_RUNTIME

create_release() {
  local source_root="$1"
  local large_payload="${2:-0}"
  local release_purpose="${3:-production}"
  local release_version="${4:-1.2.3}"
  local relative parent mode path sha size files='[]'

  install -d -m 0755 -- "$source_root"
  for relative in "${PAYLOAD_PATHS[@]}"; do
    parent="${relative%/*}"
    [[ "$parent" != "$relative" ]] || parent=.
    install -d -m 0755 -- "$source_root/$parent"
    mode=0644
    if [[ "$relative" == bin/* || "$relative" == scripts/* ]]; then
      mode=0755
    fi
    path="$source_root/$relative"
    if [[ "$relative" == 'scripts/install-v2-release.sh' ]]; then
      install -m 0755 -- "$INSTALLER_SOURCE" "$path"
    elif [[ "$relative" == 'bin/ascendany-release-ops' ]]; then
      install -m 0755 -- "$RELEASE_OPS_BINARY" "$path"
    elif [[ "$relative" == 'bin/ascendany-model' ]]; then
      install -m 0755 -- "$MODEL_BINARY" "$path"
    elif [[ "$relative" == 'models/recommendation-model.json' ]]; then
      jq -jcnS --arg purpose "$release_purpose" '{manifest: {purpose: $purpose}}' >"$path"
      chmod "$mode" -- "$path"
    elif [[ "$relative" == 'config/ascendanyd.env' ]]; then
      printf 'ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=%s\n' "$release_purpose" >"$path"
      chmod "$mode" -- "$path"
    elif [[ "$relative" == systemd/* ]]; then
      install -m 0644 -- "$REPOSITORY_ROOT/deploy/v2/$relative" "$path"
    elif [[ "$large_payload" == 1 && "$relative" == 'bin/ascendanyd' ]]; then
      dd if=/dev/zero of="$path" bs=1M count=64 status=none
      chmod "$mode" -- "$path"
    else
      printf 'fixture payload: %s release=%s\n' "$relative" "$release_version" >"$path"
      chmod "$mode" -- "$path"
    fi
  done
  for relative in "${PAYLOAD_PATHS[@]}"; do
    path="$source_root/$relative"
    sha="$(sha256sum -- "$path" | awk '{print $1}')"
    size="$(stat -Lc '%s' -- "$path")"
    mode="0$(stat -Lc '%a' -- "$path")"
    files="$(
      jq -c \
        --arg path "$relative" \
        --arg sha "$sha" \
        --argjson size "$size" \
        --arg mode "$mode" \
        '. + [{path: $path, sha256: $sha, size: $size, mode: $mode}]' <<<"$files"
    )"
  done
  jq -jcnS \
    --argjson files "$files" \
    --arg purpose "$release_purpose" \
    --arg version "$release_version" '
      {
        schema: "ascendany.release.v2",
        version: $version,
        commit: "0123456789abcdef0123456789abcdef01234567",
        purpose: $purpose,
        sourceDateEpoch: 1700000000,
        build: {
          goVersion: "go1.26.0",
          goos: "linux",
          goarch: "amd64",
          goamd64: "v1",
          goExperiment: "none",
          gofips140: "off",
          cgoEnabled: false
        },
        files: $files
      }
    ' >"$source_root/release-manifest.json"
  chmod 0644 -- "$source_root/release-manifest.json"
  find "$source_root" -type d -exec chmod 0755 {} +
}

new_case() {
  local name="$1"
  local case_root="$WORK_ROOT/$name"
  install -d -m 0700 -- "$case_root"
  install -d -m 0755 -- "$case_root/opt"
  create_release "$case_root/source" "${2:-0}" "${3:-production}" "${4:-1.2.3}"
  sha256sum "$case_root/source/release-manifest.json" | awk '{print $1}' \
    >"$case_root/expected-manifest.sha256"
  chmod 0600 "$case_root/expected-manifest.sha256"
  printf '%s\n' "$case_root"
}

refresh_manifest_anchor() {
  local case_root="$1"
  sha256sum "$case_root/source/release-manifest.json" | awk '{print $1}' \
    >"$case_root/expected-manifest.sha256"
  chmod 0600 "$case_root/expected-manifest.sha256"
}

prepare_replacement_source() {
  local case_root="$1"
  local version="$2"
  local large_payload="${3:-0}"
  local purpose="${4:-production}"

  rm -rf -- "$case_root/source"
  create_release "$case_root/source" "$large_payload" "$purpose" "$version"
  refresh_manifest_anchor "$case_root"
}

run_installer() {
  local case_root="$1"
  local manifest_sha256
  local -a purpose_arguments=() replacement_arguments=()
  shift
  manifest_sha256="$(<"$case_root/expected-manifest.sha256")"
  if [[ -n "${FIXTURE_EXPECTED_PURPOSE:-}" ]]; then
    purpose_arguments=(--expected-purpose "$FIXTURE_EXPECTED_PURPOSE")
  fi
  if [[ -n "${FIXTURE_INSTALLED_MANIFEST_SHA256:-}" || -n "${FIXTURE_INSTALLED_IDENTITY:-}" ]]; then
    [[ -n "${FIXTURE_INSTALLED_MANIFEST_SHA256:-}" && -n "${FIXTURE_INSTALLED_IDENTITY:-}" ]] ||
      fail 'fixture replacement requires both installed manifest SHA-256 and identity'
    replacement_arguments=(
      --replace-installed-manifest-sha256 "$FIXTURE_INSTALLED_MANIFEST_SHA256"
      --replace-installed-identity "$FIXTURE_INSTALLED_IDENTITY"
    )
  fi
  bwrap "${BWRAP_RUNTIME[@]}" \
    --bind "$case_root" /fixture \
    --bind "$case_root/opt" /opt \
    --ro-bind "$SYSTEMCTL_BINARY" /usr/bin/systemctl \
    --dir /trusted \
    --ro-bind "$INSTALLER_SOURCE" /trusted/install-v2-release.sh \
    --chdir / \
    "$@" \
    /trusted/install-v2-release.sh \
      --source /fixture/source \
      --manifest-sha256 "$manifest_sha256" \
      "${replacement_arguments[@]}" \
      "${purpose_arguments[@]}"
}

run_shell() {
  local case_root="$1"
  local command="$2"
  shift 2
  bwrap "${BWRAP_RUNTIME[@]}" \
    --bind "$case_root" /fixture \
    --bind "$case_root/opt" /opt \
    --ro-bind "$SYSTEMCTL_BINARY" /usr/bin/systemctl \
    --dir /trusted \
    --ro-bind "$INSTALLER_SOURCE" /trusted/install-v2-release.sh \
    --chdir / \
    "$@" \
    /usr/bin/bash -p -c "$command"
}

if [[ "${1:-}" == '--materialize-installed-tree' ]]; then
  [[ "$#" == 2 && "$2" == /* && ! -e "$2" && ! -L "$2" ]] ||
    fail 'usage: install-v2-release-fixture.sh --materialize-installed-tree /absent/absolute/path'
  materialize_case="$(new_case materialize)"
  run_installer "$materialize_case" /usr/bin/env >/dev/null
  cp -a -- "$materialize_case/opt/ascendany/v2" "$2"
  printf '%s\n' "$2"
  exit 0
fi
[[ "$#" == 0 ]] || fail 'fixture accepts only --materialize-installed-tree /absent/absolute/path'

happy_case="$(new_case happy)"
printf 'touch /fixture/bash-env-executed\n' >"$happy_case/bash-env-attack"
run_installer "$happy_case" \
  /usr/bin/env \
    ASCENDANY_RELEASE_INSTALLER_CLEAN_ENV=1 \
    ASCENDANY_ATTACK_ENVIRONMENT=must_be_removed \
    BASH_ENV=/fixture/bash-env-attack \
    SHELLOPTS=xtrace \
    PATH=/fixture/attacker-bin \
  >"$happy_case/install.out" 2>"$happy_case/install.err"
[[ "$(<"$happy_case/install.out")" == '/opt/ascendany/v2' ]] || fail 'installer did not report the canonical target'
[[ ! -s "$happy_case/install.err" ]] || fail 'successful installer emitted diagnostics'
[[ ! -e "$happy_case/bash-env-executed" ]] || fail 'installer executed inherited BASH_ENV content'
diff -r -- "$happy_case/source" "$happy_case/opt/ascendany/v2" >/dev/null || fail 'promoted tree bytes differ from the source release'
run_shell "$happy_case" '
  set -Eeuo pipefail
  [[ "$(stat -Lc "%u:%g:%a" /opt/ascendany/v2)" == 0:0:755 ]]
  [[ "$(stat -Lc "%u:%g:%a:%h" /opt/ascendany/v2/release-manifest.json)" == 0:0:644:1 ]]
  if find /opt/ascendany/v2 -type d ! -perm 0755 -print -quit | grep -q .; then exit 1; fi
  if find /opt/ascendany/v2 -type f \( ! -uid 0 -o ! -gid 0 -o -links +1 \) -print -quit | grep -q .; then exit 1; fi
' >/dev/null

acceptance_default_case="$(new_case acceptance-default-rejected 0 acceptance_test)"
expect_failure "$acceptance_default_case/install.log" run_installer "$acceptance_default_case" /usr/bin/env
require_log "$acceptance_default_case/install.log" 'release purpose acceptance_test differs from expected production'
[[ ! -e "$acceptance_default_case/opt/ascendany/v2" ]] || fail 'default production installer published an acceptance release'

acceptance_explicit_case="$(new_case acceptance-explicit 0 acceptance_test)"
FIXTURE_EXPECTED_PURPOSE=acceptance_test \
  run_installer "$acceptance_explicit_case" /usr/bin/env >/dev/null
[[ "$(jq -r '.purpose' "$acceptance_explicit_case/opt/ascendany/v2/release-manifest.json")" == acceptance_test ]] ||
  fail 'explicit acceptance capability did not publish the acceptance release'

expect_failure "$happy_case/duplicate.log" run_installer "$happy_case" /usr/bin/env
require_log "$happy_case/duplicate.log" 'canonical release target already exists'
diff -r -- "$happy_case/source" "$happy_case/opt/ascendany/v2" >/dev/null || fail 'duplicate install changed the existing canonical release'

forward_case="$(new_case forward-replacement 0 production 1.2.3)"
run_installer "$forward_case" /usr/bin/env >/dev/null
forward_installed_manifest_sha256="$(<"$forward_case/expected-manifest.sha256")"
forward_installed_identity="$(stat -Lc '%d:%i' -- "$forward_case/opt/ascendany/v2")"
forward_installed_inode="${forward_installed_identity#*:}"
prepare_replacement_source "$forward_case" 1.2.4
FIXTURE_INSTALLED_MANIFEST_SHA256="$forward_installed_manifest_sha256" \
FIXTURE_INSTALLED_IDENTITY="$forward_installed_identity" \
FIXTURE_EXPECTED_PURPOSE=production \
  run_installer "$forward_case" /usr/bin/env >"$forward_case/replace.out" 2>"$forward_case/replace.err"
[[ "$(<"$forward_case/replace.out")" == '/opt/ascendany/v2' ]] || fail 'forward replacement did not report the canonical target'
[[ ! -s "$forward_case/replace.err" ]] || fail 'successful forward replacement emitted diagnostics'
[[ "$(jq -r '.version' "$forward_case/opt/ascendany/v2/release-manifest.json")" == 1.2.4 ]] ||
  fail 'forward replacement did not publish the advancing release'
diff -r -- "$forward_case/source" "$forward_case/opt/ascendany/v2" >/dev/null ||
  fail 'forward replacement target differs from the new source release'
if find "$forward_case/opt/ascendany" -mindepth 1 -maxdepth 1 \
    \( -name '.v2.installing.*' -o -name '.v2.removing.*' \) -print -quit | grep -q .; then
  fail 'successful forward replacement retained a private cleanup entry'
fi
if find "$forward_case/opt/ascendany" -mindepth 1 -maxdepth 1 -inum "$forward_installed_inode" -print -quit | grep -q .; then
  fail 'successful forward replacement retained the installed release identity'
fi

active_unit_case="$(new_case active-systemd-consumer 0 production 1.2.3)"
run_installer "$active_unit_case" /usr/bin/env >/dev/null
active_unit_manifest_sha256="$(<"$active_unit_case/expected-manifest.sha256")"
active_unit_identity="$(stat -Lc '%d:%i' -- "$active_unit_case/opt/ascendany/v2")"
prepare_replacement_source "$active_unit_case" 1.2.4
printf '%s\n' 'ascendanyd.service' >"$active_unit_case/active-systemd-unit"
FIXTURE_INSTALLED_MANIFEST_SHA256="$active_unit_manifest_sha256" \
FIXTURE_INSTALLED_IDENTITY="$active_unit_identity" \
  expect_failure "$active_unit_case/replace.log" run_installer "$active_unit_case" /usr/bin/env
require_log "$active_unit_case/replace.log" \
  'release consumer systemd unit is not quiesced before replacement preflight: ascendanyd.service'
[[ "$(stat -Lc '%d:%i' -- "$active_unit_case/opt/ascendany/v2")" == "$active_unit_identity" ]] ||
  fail 'active systemd consumer rejection changed the installed release'
if find "$active_unit_case/opt/ascendany" -mindepth 1 -maxdepth 1 -name '.v2.*' -print -quit | grep -q .; then
  fail 'active systemd consumer rejection created a private release entry'
fi
printf '%s\n' 'ascendany-judge@fixture.service' >"$active_unit_case/active-systemd-unit"
FIXTURE_INSTALLED_MANIFEST_SHA256="$active_unit_manifest_sha256" \
FIXTURE_INSTALLED_IDENTITY="$active_unit_identity" \
  expect_failure "$active_unit_case/instance.log" run_installer "$active_unit_case" /usr/bin/env
require_log "$active_unit_case/instance.log" \
  'release consumer systemd instance is not quiesced before replacement preflight: ascendany-judge@fixture.service'
if find "$active_unit_case/opt/ascendany" -mindepth 1 -maxdepth 1 -name '.v2.*' -print -quit | grep -q .; then
  fail 'active systemd instance rejection created a private release entry'
fi

live_process_case="$(new_case live-release-process 0 production 1.2.3)"
run_installer "$live_process_case" /usr/bin/env >/dev/null
cp -- "$live_process_case/expected-manifest.sha256" "$live_process_case/installed-manifest.sha256"
stat -Lc '%d:%i' -- "$live_process_case/opt/ascendany/v2" >"$live_process_case/installed-identity"
live_process_old_inode="$(<"$live_process_case/installed-identity")"
live_process_old_inode="${live_process_old_inode#*:}"
prepare_replacement_source "$live_process_case" 1.2.4
run_shell "$live_process_case" '
  set -Eeuo pipefail
  exec {consumer_fd}</opt/ascendany/v2/README.md
  /usr/bin/sleep 30 &
  consumer_pid=$!
  set +e
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    --replace-installed-manifest-sha256 "$(</fixture/installed-manifest.sha256)" \
    --replace-installed-identity "$(</fixture/installed-identity)" \
    >/fixture/live-replace.out 2>/fixture/live-replace.err
  live_status=$?
  set -e
  [[ "$live_status" != 0 ]]
  grep -F "release-owned process is not quiesced before replacement preflight" \
    /fixture/live-replace.err >/dev/null
  shopt -s nullglob
  private_entries=(/opt/ascendany/.v2.installing.* /opt/ascendany/.v2.removing.*)
  shopt -u nullglob
  (( ${#private_entries[@]} == 0 ))
  exec {consumer_fd}<&-
  /usr/bin/kill "$consumer_pid"
  wait "$consumer_pid" 2>/dev/null || true
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    --replace-installed-manifest-sha256 "$(</fixture/installed-manifest.sha256)" \
    --replace-installed-identity "$(</fixture/installed-identity)" \
    >/fixture/quiesced-replace.out 2>/fixture/quiesced-replace.err
' >/dev/null
require_log "$live_process_case/live-replace.err" \
  'release-owned process is not quiesced before replacement preflight'
[[ "$(<"$live_process_case/quiesced-replace.out")" == '/opt/ascendany/v2' ]] ||
  fail 'quiesced replacement did not report the canonical target'
[[ ! -s "$live_process_case/quiesced-replace.err" ]] ||
  fail 'quiesced replacement emitted diagnostics'
[[ "$(jq -r '.version' "$live_process_case/opt/ascendany/v2/release-manifest.json")" == 1.2.4 ]] ||
  fail 'replacement did not close successfully after the live release consumer exited'
if find "$live_process_case/opt/ascendany" -mindepth 1 -maxdepth 1 \
    \( -name '.v2.installing.*' -o -name '.v2.removing.*' -o -inum "$live_process_old_inode" \) \
    -print -quit | grep -q .; then
  fail 'quiesced replacement retained the old release or a private release entry'
fi

prerelease_forward_case="$(new_case prerelease-forward 0 production 1.2.4-rc.2)"
run_installer "$prerelease_forward_case" /usr/bin/env >/dev/null
prerelease_installed_manifest_sha256="$(<"$prerelease_forward_case/expected-manifest.sha256")"
prerelease_installed_identity="$(stat -Lc '%d:%i' -- "$prerelease_forward_case/opt/ascendany/v2")"
prepare_replacement_source "$prerelease_forward_case" 1.2.4
FIXTURE_INSTALLED_MANIFEST_SHA256="$prerelease_installed_manifest_sha256" \
FIXTURE_INSTALLED_IDENTITY="$prerelease_installed_identity" \
  run_installer "$prerelease_forward_case" /usr/bin/env >/dev/null
[[ "$(jq -r '.version' "$prerelease_forward_case/opt/ascendany/v2/release-manifest.json")" == 1.2.4 ]] ||
  fail 'SemVer prerelease-to-release advancement was rejected'

installed_anchor_case="$(new_case installed-manifest-anchor 0 production 1.2.3)"
run_installer "$installed_anchor_case" /usr/bin/env >/dev/null
installed_anchor_identity="$(stat -Lc '%d:%i' -- "$installed_anchor_case/opt/ascendany/v2")"
installed_anchor_original_sha256="$(<"$installed_anchor_case/expected-manifest.sha256")"
prepare_replacement_source "$installed_anchor_case" 1.2.4
FIXTURE_INSTALLED_MANIFEST_SHA256="$(printf '%064d' 0)" \
FIXTURE_INSTALLED_IDENTITY="$installed_anchor_identity" \
  expect_failure "$installed_anchor_case/replace.log" run_installer "$installed_anchor_case" /usr/bin/env
require_log "$installed_anchor_case/replace.log" 'installed release manifest digest differs from the explicit trust input'
[[ "$(sha256sum "$installed_anchor_case/opt/ascendany/v2/release-manifest.json" | awk '{print $1}')" == "$installed_anchor_original_sha256" ]] ||
  fail 'wrong installed manifest trust input changed the canonical release'
if find "$installed_anchor_case/opt/ascendany" -mindepth 1 -maxdepth 1 -name '.v2.installing.*' -print -quit | grep -q .; then
  fail 'wrong installed manifest trust input created a staging tree'
fi

installed_identity_case="$(new_case installed-identity 0 production 1.2.3)"
run_installer "$installed_identity_case" /usr/bin/env >/dev/null
installed_identity_manifest_sha256="$(<"$installed_identity_case/expected-manifest.sha256")"
installed_identity_original="$(stat -Lc '%d:%i' -- "$installed_identity_case/opt/ascendany/v2")"
prepare_replacement_source "$installed_identity_case" 1.2.4
FIXTURE_INSTALLED_MANIFEST_SHA256="$installed_identity_manifest_sha256" \
FIXTURE_INSTALLED_IDENTITY='0:1' \
  expect_failure "$installed_identity_case/replace.log" run_installer "$installed_identity_case" /usr/bin/env
require_log "$installed_identity_case/replace.log" 'installed release identity differs from the explicit trust input'
[[ "$(stat -Lc '%d:%i' -- "$installed_identity_case/opt/ascendany/v2")" == "$installed_identity_original" ]] ||
  fail 'wrong installed identity changed the canonical release'

installed_drift_case="$(new_case installed-tree-drift 0 production 1.2.3)"
run_installer "$installed_drift_case" /usr/bin/env >/dev/null
installed_drift_manifest_sha256="$(<"$installed_drift_case/expected-manifest.sha256")"
installed_drift_identity="$(stat -Lc '%d:%i' -- "$installed_drift_case/opt/ascendany/v2")"
printf 'installed tree drift\n' >"$installed_drift_case/opt/ascendany/v2/README.md"
prepare_replacement_source "$installed_drift_case" 1.2.4
FIXTURE_INSTALLED_MANIFEST_SHA256="$installed_drift_manifest_sha256" \
FIXTURE_INSTALLED_IDENTITY="$installed_drift_identity" \
  expect_failure "$installed_drift_case/replace.log" run_installer "$installed_drift_case" /usr/bin/env
require_log "$installed_drift_case/replace.log" 'installed release payload integrity drifted: README.md'
[[ "$(<"$installed_drift_case/opt/ascendany/v2/README.md")" == 'installed tree drift' ]] ||
  fail 'installed-tree drift rejection changed the installed release'

nonforward_case="$(new_case nonforward-version 0 production 1.2.3)"
run_installer "$nonforward_case" /usr/bin/env >/dev/null
nonforward_manifest_sha256="$(<"$nonforward_case/expected-manifest.sha256")"
nonforward_identity="$(stat -Lc '%d:%i' -- "$nonforward_case/opt/ascendany/v2")"
prepare_replacement_source "$nonforward_case" 1.2.3+rebuild.1
FIXTURE_INSTALLED_MANIFEST_SHA256="$nonforward_manifest_sha256" \
FIXTURE_INSTALLED_IDENTITY="$nonforward_identity" \
  expect_failure "$nonforward_case/replace.log" run_installer "$nonforward_case" /usr/bin/env
require_log "$nonforward_case/replace.log" 'replacement release version 1.2.3+rebuild.1 does not advance installed version 1.2.3'
[[ "$(jq -r '.version' "$nonforward_case/opt/ascendany/v2/release-manifest.json")" == 1.2.3 ]] ||
  fail 'non-forward version rejection changed the installed release'

bytes_case="$(new_case bytes-drift)"
printf 'tampered bytes\n' >"$bytes_case/source/README.md"
expect_failure "$bytes_case/install.log" run_installer "$bytes_case" /usr/bin/env
require_log "$bytes_case/install.log" 'release source payload integrity differs from its manifest: README.md'
require_log "$bytes_case/install.log" 'release installation state: pre-commit-staging-retained'
[[ ! -e "$bytes_case/opt/ascendany/v2" ]] || fail 'byte-drift case published a target'
expect_failure "$bytes_case/retry.log" run_installer "$bytes_case" /usr/bin/env
require_log "$bytes_case/retry.log" 'pre-existing incomplete release private entries require explicit operator resolution:'

removal_tombstone_case="$(new_case removal-tombstone)"
install -d -m 0755 -- "$removal_tombstone_case/opt/ascendany/.v2.removing.A1b2C3d4E5"
expect_failure "$removal_tombstone_case/install.log" run_installer "$removal_tombstone_case" /usr/bin/env
require_log "$removal_tombstone_case/install.log" 'pre-existing incomplete release private entries require explicit operator resolution: .v2.removing.A1b2C3d4E5'
[[ ! -e "$removal_tombstone_case/opt/ascendany/v2" ]] || fail 'pre-existing removal tombstone published a target'

mode_case="$(new_case mode-drift)"
chmod 0664 "$mode_case/source/config/analytics.json"
expect_failure "$mode_case/install.log" run_installer "$mode_case" /usr/bin/env
require_log "$mode_case/install.log" 'release source payload integrity differs from its manifest: config/analytics.json'
[[ ! -e "$mode_case/opt/ascendany/v2" ]] || fail 'mode-drift case published a target'

anchor_case="$(new_case external-trust-anchor)"
printf '%064d\n' 0 >"$anchor_case/expected-manifest.sha256"
expect_failure "$anchor_case/install.log" run_installer "$anchor_case" /usr/bin/env
require_log "$anchor_case/install.log" 'release manifest digest differs from the external trust anchor'
[[ ! -e "$anchor_case/opt/ascendany/v2" ]] || fail 'wrong manifest trust anchor published a target'

manifest_case="$(new_case manifest-drift)"
printf '\n' >>"$manifest_case/source/release-manifest.json"
expect_failure "$manifest_case/install.log" run_installer "$manifest_case" /usr/bin/env
require_log "$manifest_case/install.log" 'release manifest digest differs from the external trust anchor'
[[ ! -e "$manifest_case/opt/ascendany/v2" ]] || fail 'manifest-drift case published a target'

self_case="$(new_case payload-installer-rejected)"
expect_failure "$self_case/install.log" run_shell "$self_case" \
  '/fixture/source/scripts/install-v2-release.sh --source /fixture/source --manifest-sha256 "$(</fixture/expected-manifest.sha256)"'
require_log "$self_case/install.log" 'release installer bootstrap must be external to the untrusted release payload'
[[ ! -e "$self_case/opt/ascendany/v2" ]] || fail 'payload-installer case published a target'

self_bytes_case="$(new_case payload-installer-bytes)"
printf '\n# unmanifested installer bytes\n' >>"$self_bytes_case/source/scripts/install-v2-release.sh"
expect_failure "$self_bytes_case/install.log" run_installer "$self_bytes_case" /usr/bin/env
require_log "$self_bytes_case/install.log" 'release source payload integrity differs from its manifest: scripts/install-v2-release.sh'
[[ ! -e "$self_bytes_case/opt/ascendany/v2" ]] || fail 'installer-byte-drift case published a target'

traversal_case="$(new_case traversal)"
jq -jSc '.files[0].path = "../escape"' \
  "$traversal_case/source/release-manifest.json" >"$traversal_case/manifest.new"
mv -- "$traversal_case/manifest.new" "$traversal_case/source/release-manifest.json"
chmod 0644 "$traversal_case/source/release-manifest.json"
refresh_manifest_anchor "$traversal_case"
expect_failure "$traversal_case/install.log" run_installer "$traversal_case" /usr/bin/env
require_log "$traversal_case/install.log" 'release manifest contains an unsafe path'
[[ ! -e "$traversal_case/escape" && ! -e "$traversal_case/opt/ascendany/v2" ]] || fail 'path traversal escaped or published'

symlink_case="$(new_case symlink)"
rm -- "$symlink_case/source/README.md"
ln -s /usr/bin/true "$symlink_case/source/README.md"
expect_failure "$symlink_case/install.log" run_installer "$symlink_case" /usr/bin/env
require_log "$symlink_case/install.log" 'release source contains a symbolic link or special filesystem node: README.md'
[[ ! -e "$symlink_case/opt/ascendany/v2" ]] || fail 'symbolic-link case published a target'

fifo_case="$(new_case nonregular)"
rm -- "$fifo_case/source/README.md"
mkfifo -m 0644 "$fifo_case/source/README.md"
expect_failure "$fifo_case/install.log" run_installer "$fifo_case" /usr/bin/env
require_log "$fifo_case/install.log" 'release source contains a symbolic link or special filesystem node: README.md'
[[ ! -e "$fifo_case/opt/ascendany/v2" ]] || fail 'special-node case published a target'

extra_case="$(new_case extra-file)"
printf 'unmanifested\n' >"$extra_case/source/extra"
chmod 0644 "$extra_case/source/extra"
expect_failure "$extra_case/install.log" run_installer "$extra_case" /usr/bin/env
require_log "$extra_case/install.log" 'release source file set differs from the manifest-closed release contract'
[[ ! -e "$extra_case/opt/ascendany/v2" ]] || fail 'extra-file case published a target'

hardlink_case="$(new_case hardlink)"
ln "$hardlink_case/source/README.md" "$hardlink_case/outside-hardlink"
expect_failure "$hardlink_case/install.log" run_installer "$hardlink_case" /usr/bin/env
require_log "$hardlink_case/install.log" 'release source payload integrity differs from its manifest: README.md'
[[ ! -e "$hardlink_case/opt/ascendany/v2" ]] || fail 'hard-link case published a target'

owner_case="$(new_case owner)"
expect_failure "$owner_case/install.log" \
  run_installer "$owner_case" \
    --ro-bind /usr/bin/true /fixture/source/README.md \
    /usr/bin/env
require_log "$owner_case/install.log" 'release source entry must be owned by root:root: README.md'
[[ ! -e "$owner_case/opt/ascendany/v2" ]] || fail 'foreign-owner case published a target'

mount_case="$(new_case descendant-mount)"
cp -a -- "$mount_case/source/config" "$mount_case/config-mount"
expect_failure "$mount_case/install.log" \
  run_installer "$mount_case" \
    --bind "$mount_case/config-mount" /fixture/source/config \
    /usr/bin/env
require_log "$mount_case/install.log" 'release source contains a descendant mount: config'
[[ ! -e "$mount_case/opt/ascendany/v2" ]] || fail 'descendant-mount case published a target'

file_mount_case="$(new_case descendant-file-mount)"
cp -a -- "$file_mount_case/source/README.md" "$file_mount_case/readme-mount"
expect_failure "$file_mount_case/install.log" \
  run_installer "$file_mount_case" \
    --bind "$file_mount_case/readme-mount" /fixture/source/README.md \
    /usr/bin/env
require_log "$file_mount_case/install.log" 'release source contains a descendant mount: README.md'
[[ ! -e "$file_mount_case/opt/ascendany/v2" ]] || fail 'descendant file-mount case published a target'

ancestry_case="$(new_case ancestry)"
chmod 0777 "$ancestry_case"
expect_failure "$ancestry_case/install.log" run_installer "$ancestry_case" /usr/bin/env
require_log "$ancestry_case/install.log" 'release source has an unprotected writable ancestor: /fixture'
[[ ! -e "$ancestry_case/opt/ascendany/v2" ]] || fail 'unsafe-ancestry case published a target'
chmod 0700 "$ancestry_case"

bootstrap_ancestry_case="$(new_case bootstrap-ancestry)"
install -d -m 0777 "$bootstrap_ancestry_case/untrusted"
install -m 0755 "$INSTALLER_SOURCE" "$bootstrap_ancestry_case/untrusted/install-v2-release.sh"
expect_failure "$bootstrap_ancestry_case/install.log" run_shell "$bootstrap_ancestry_case" \
  '/fixture/untrusted/install-v2-release.sh --source /fixture/source --manifest-sha256 "$(</fixture/expected-manifest.sha256)"'
require_log "$bootstrap_ancestry_case/install.log" 'release installer bootstrap leaf must not be group- or other-writable: /fixture/untrusted'
[[ ! -e "$bootstrap_ancestry_case/opt/ascendany/v2" ]] || fail 'unsafe bootstrap ancestry published a target'

target_symlink_case="$(new_case target-symlink)"
install -d -m 0755 "$target_symlink_case/opt/ascendany"
ln -s /tmp/escape "$target_symlink_case/opt/ascendany/v2"
expect_failure "$target_symlink_case/install.log" run_installer "$target_symlink_case" /usr/bin/env
require_log "$target_symlink_case/install.log" 'canonical release target already exists'
[[ ! -e "$target_symlink_case/escape" ]] || fail 'target symbolic link was followed'

lock_symlink_case="$(new_case lock-symlink)"
install -d -m 0755 "$lock_symlink_case/opt/ascendany"
ln -s /tmp/escape "$lock_symlink_case/opt/ascendany/.install-v2-release.lock"
expect_failure "$lock_symlink_case/install.log" run_installer "$lock_symlink_case" /usr/bin/env
require_log "$lock_symlink_case/install.log" 'installation lock must be one root:root mode 0600 regular file'
[[ ! -e "$lock_symlink_case/escape" && ! -e "$lock_symlink_case/opt/ascendany/v2" ]] || fail 'lock symbolic link was followed or a target was published'

concurrent_case="$(new_case concurrent 1)"
run_shell "$concurrent_case" '
  set -Eeuo pipefail
  manifest_sha256="$(</fixture/expected-manifest.sha256)"
  /trusted/install-v2-release.sh --source /fixture/source --manifest-sha256 "$manifest_sha256" \
    >/fixture/first-installer.out 2>/fixture/first-installer.err &
  first_installer=$!
  deadline=$((SECONDS + 10))
  while (( SECONDS < deadline )); do
    candidate=(/opt/ascendany/.v2.installing.*)
    [[ -d "${candidate[0]}" ]] && break
  done
  [[ -d "${candidate[0]}" ]]
  set +e
  /trusted/install-v2-release.sh --source /fixture/source --manifest-sha256 "$manifest_sha256" \
    >/fixture/second-installer.out 2>/fixture/second-installer.err
  second_status=$?
  set -e
  [[ "$second_status" != 0 ]]
  grep -F "another release installer holds the installation lock" \
    /fixture/second-installer.err >/dev/null
  wait "$first_installer"
  [[ "$(cat /fixture/first-installer.out)" == /opt/ascendany/v2 ]]
  [[ ! -s /fixture/first-installer.err ]]
  [[ -f /opt/ascendany/v2/release-manifest.json ]]
' >/dev/null
[[ ! -s "$concurrent_case/second-installer.out" ]] || fail 'contending installer reported a successful target'
require_log "$concurrent_case/second-installer.err" 'another release installer holds the installation lock'

target_race_case="$(new_case target-race 1)"
run_shell "$target_race_case" '
  set -Eeuo pipefail
  (
    deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
      candidate=(/opt/ascendany/.v2.installing.*)
      if [[ -d "${candidate[0]}" ]]; then
        mkdir /opt/ascendany/v2
        printf "racing target\n" >/opt/ascendany/v2/marker
        exit 0
      fi
    done
    exit 91
  ) &
  attacker=$!
  set +e
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    >/fixture/raced-installer.out 2>/fixture/raced-installer.err
  installer_status=$?
  set -e
  wait "$attacker"
  [[ "$installer_status" != 0 ]]
  [[ "$(cat /opt/ascendany/v2/marker)" == "racing target" ]]
  [[ "$(find /opt/ascendany/v2 -mindepth 1 -maxdepth 1 -printf "%f\n")" == marker ]]
' >/dev/null
require_log "$target_race_case/raced-installer.err" 'canonical release target appeared before atomic promotion'
[[ "$(<"$target_race_case/opt/ascendany/v2/marker")" == 'racing target' ]] || fail 'target race replaced the attacker-created target'
[[ ! -e "$target_race_case/opt/ascendany/v2/release-manifest.json" ]] || fail 'target race nested release content into the existing target'

replacement_identity_race_case="$(new_case replacement-identity-race 0 production 1.2.3)"
run_installer "$replacement_identity_race_case" /usr/bin/env >/dev/null
cp -- "$replacement_identity_race_case/expected-manifest.sha256" \
  "$replacement_identity_race_case/installed-manifest.sha256"
stat -Lc '%d:%i' -- "$replacement_identity_race_case/opt/ascendany/v2" \
  >"$replacement_identity_race_case/installed-identity"
prepare_replacement_source "$replacement_identity_race_case" 1.2.4 1
run_shell "$replacement_identity_race_case" '
  set -Eeuo pipefail
  (
    deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
      candidate=(/opt/ascendany/.v2.installing.*)
      if [[ -d "${candidate[0]}" ]]; then
        mv /opt/ascendany/v2 /opt/ascendany/displaced-installed
        mkdir -m 0755 /opt/ascendany/v2
        printf "racing replacement target\n" >/opt/ascendany/v2/marker
        exit 0
      fi
    done
    exit 91
  ) &
  attacker=$!
  set +e
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    --replace-installed-manifest-sha256 "$(</fixture/installed-manifest.sha256)" \
    --replace-installed-identity "$(</fixture/installed-identity)" \
    >/fixture/raced-installer.out 2>/fixture/raced-installer.err
  installer_status=$?
  set -e
  wait "$attacker"
  [[ "$installer_status" != 0 ]]
  [[ "$(cat /opt/ascendany/v2/marker)" == "racing replacement target" ]]
  [[ "$(jq -r .version /opt/ascendany/displaced-installed/release-manifest.json)" == 1.2.3 ]]
  candidate=(/opt/ascendany/.v2.installing.*)
  [[ -d "${candidate[0]}" ]]
' >/dev/null
require_log "$replacement_identity_race_case/raced-installer.err" 'installed release identity changed before atomic replacement'
[[ "$(<"$replacement_identity_race_case/opt/ascendany/v2/marker")" == 'racing replacement target' ]] ||
  fail 'replacement identity race changed the racing canonical target'
[[ "$(jq -r '.version' "$replacement_identity_race_case/opt/ascendany/displaced-installed/release-manifest.json")" == 1.2.3 ]] ||
  fail 'replacement identity race changed the displaced trusted release'

post_exchange_race_case="$(new_case post-exchange-retired-race 1 production 1.2.3)"
run_installer "$post_exchange_race_case" /usr/bin/env >/dev/null
cp -- "$post_exchange_race_case/expected-manifest.sha256" \
  "$post_exchange_race_case/installed-manifest.sha256"
stat -Lc '%d:%i' -- "$post_exchange_race_case/opt/ascendany/v2" \
  >"$post_exchange_race_case/installed-identity"
prepare_replacement_source "$post_exchange_race_case" 1.2.4
run_shell "$post_exchange_race_case" '
  set -Eeuo pipefail
  (
    deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
      if [[ -f /opt/ascendany/v2/release-manifest.json ]] &&
         [[ "$(jq -r .version /opt/ascendany/v2/release-manifest.json 2>/dev/null || true)" == 1.2.4 ]]; then
        candidate=(/opt/ascendany/.v2.installing.*)
        if [[ -d "${candidate[0]}" ]]; then
          /usr/bin/sleep 0.1
          [[ -d "${candidate[0]}" ]] || continue
          mv -- "${candidate[0]}" /opt/ascendany/displaced-retired
          mkdir -m 0755 -- "${candidate[0]}"
          printf "racing retired stage\n" >"${candidate[0]}/marker"
          exit 0
        fi
      fi
    done
    exit 91
  ) &
  attacker=$!
  set +e
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    --replace-installed-manifest-sha256 "$(</fixture/installed-manifest.sha256)" \
    --replace-installed-identity "$(</fixture/installed-identity)" \
    >/fixture/raced-installer.out 2>/fixture/raced-installer.err
  installer_status=$?
  set -e
  wait "$attacker"
  [[ "$installer_status" != 0 ]]
  [[ "$(jq -r .version /opt/ascendany/v2/release-manifest.json)" == 1.2.4 ]]
  [[ "$(jq -r .version /opt/ascendany/displaced-retired/release-manifest.json)" == 1.2.3 ]]
  find /opt/ascendany -mindepth 2 -maxdepth 2 \
    \( -path "/opt/ascendany/.v2.installing.*/marker" -o -path "/opt/ascendany/.v2.removing.*/marker" \) \
    -exec grep -Fqx "racing retired stage" {} \; -print -quit | grep -q .
' >/dev/null
require_log "$post_exchange_race_case/raced-installer.err" 'release installation state: committed-unverified target=/opt/ascendany/v2'
[[ "$(jq -r '.version' "$post_exchange_race_case/opt/ascendany/v2/release-manifest.json")" == 1.2.4 ]] ||
  fail 'post-exchange race reversed the replacement release'
[[ "$(jq -r '.version' "$post_exchange_race_case/opt/ascendany/displaced-retired/release-manifest.json")" == 1.2.3 ]] ||
  fail 'post-exchange race deleted the displaced trusted release'

parent_race_case="$(new_case parent-race 1)"
run_shell "$parent_race_case" '
  set -Eeuo pipefail
  (
    deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
      candidate=(/opt/ascendany/.v2.installing.*)
      if [[ -d "${candidate[0]}" ]]; then
        mv /opt/ascendany /opt/ascendany.displaced
        mkdir -m 0755 /opt/ascendany
        exit 0
      fi
    done
    exit 91
  ) &
  attacker=$!
  set +e
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    >/fixture/raced-installer.out 2>/fixture/raced-installer.err
  installer_status=$?
  set -e
  wait "$attacker"
  [[ "$installer_status" != 0 ]]
  [[ ! -e /opt/ascendany/v2 ]]
' >/dev/null
require_log "$parent_race_case/raced-installer.err" 'installation parent identity changed before'
[[ ! -e "$parent_race_case/opt/ascendany/v2" ]] || fail 'parent rename race published into the replacement parent'
[[ ! -e "$parent_race_case/opt/ascendany.displaced/v2" ]] || fail 'parent rename race left a release under the displaced parent'

source_race_case="$(new_case source-race 1)"
run_shell "$source_race_case" '
  set -Eeuo pipefail
  (
    deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
      candidate=(/opt/ascendany/.v2.installing.*)
      if [[ -d "${candidate[0]}" ]]; then
        mv /fixture/source /fixture/source.displaced
        mkdir -m 0755 /fixture/source
        exit 0
      fi
    done
    exit 91
  ) &
  attacker=$!
  set +e
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    >/fixture/raced-installer.out 2>/fixture/raced-installer.err
  installer_status=$?
  set -e
  wait "$attacker"
  [[ "$installer_status" != 0 ]]
  [[ ! -e /opt/ascendany/v2 ]]
' >/dev/null
require_log "$source_race_case/raced-installer.err" 'release source identity changed before'
[[ ! -e "$source_race_case/opt/ascendany/v2" ]] || fail 'source rename race published a target'

post_commit_case="$(new_case post-commit-verification 1)"
run_shell "$post_commit_case" '
  set -Eeuo pipefail
  (
    deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
      if [[ -d /opt/ascendany/v2 ]]; then
        printf "post-commit drift\n" >/opt/ascendany/v2/README.md
        exit 0
      fi
    done
    exit 91
  ) &
  attacker=$!
  set +e
  /trusted/install-v2-release.sh \
    --source /fixture/source \
    --manifest-sha256 "$(</fixture/expected-manifest.sha256)" \
    >/fixture/committed-installer.out 2>/fixture/committed-installer.err
  installer_status=$?
  set -e
  wait "$attacker"
  [[ "$installer_status" != 0 ]]
  [[ -d /opt/ascendany/v2 ]]
  [[ "$(cat /opt/ascendany/v2/README.md)" == "post-commit drift" ]]
' >/dev/null
require_log "$post_commit_case/committed-installer.err" 'release installation state: committed-unverified target=/opt/ascendany/v2'
require_log "$post_commit_case/committed-installer.err" 'promoted release payload integrity drifted: README.md'

if find "$happy_case/opt" "$concurrent_case/opt" "$forward_case/opt" \
    \( -path '*/.v2.installing.*' -o -path '*/.v2.removing.*' \) -print -quit | grep -q .; then
  fail 'successful installation left a private release entry'
fi

printf '%s\n' 'release installer hostile fixture passed'
