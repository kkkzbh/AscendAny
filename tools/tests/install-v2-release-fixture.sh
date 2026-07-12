#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly INSTALLER_SOURCE="$REPOSITORY_ROOT/deploy/v2/scripts/install-v2-release.sh"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-install-v2-release-fixture.XXXXXX")"
readonly RELEASE_OPS_BINARY="$WORK_ROOT/ascendany-release-ops"

readonly -a PAYLOAD_PATHS=(
  bin/ascendanyd
  bin/ascendany-admin-bootstrap
  bin/ascendany-backup
  bin/ascendany-judge
  bin/ascendany-lsp
  bin/ascendany-migrate
  bin/ascendany-release-ops
  bin/ascendany-trainer-agent
  trainers/recommendation/ascendany_recommendation_trainer/__init__.py
  trainers/recommendation/ascendany_recommendation_trainer/__main__.py
  trainers/recommendation/ascendany_recommendation_trainer/attestation.py
  trainers/recommendation/ascendany_recommendation_trainer/cli.py
  trainers/recommendation/ascendany_recommendation_trainer/contract.py
  trainers/recommendation/ascendany_recommendation_trainer/model.py
  trainers/recommendation/ascendany_recommendation_trainer/train.py
  trainers/recommendation/runtime-closure-cu130.json
  trainers/recommendation/runtime-python-cu130.json
  trainers/recommendation/runtime-requirements-cu130.lock
  trainers/recommendation/runtime-wheels-cu130.json
  README.md
  OJ_JUDGE_CONTRACT.md
  LSP_CONTROL_CONTRACT.md
  TRAINER_AGENT_CONTRACT.md
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
  config/postgresql-hba-bootstrap.conf
  config/postgresql-hba.conf
  config/postgresql-ident-bootstrap.conf
  config/postgresql-ident.conf
  config/restore.env
  config/trainer-agent.env
  systemd/ascendanyd.service
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
  systemd/ascendany-trainer-agent.service
  polkit-1/rules.d/60-ascendany-judge.rules
  polkit-1/rules.d/61-ascendany-lsp.rules
  sysusers.d/ascendany-v2.conf
  tmpfiles.d/ascendany-v2.conf
  scripts/publish-restore-evidence.sh
  scripts/restore-verify-operator.sh
  scripts/install-trainer-runtime.sh
  scripts/install-v2-release.sh
  scripts/acquire-judge-image.sh
  scripts/attest-judge-image.sh
  scripts/judge-image-contract.sh
  scripts/preload-judge-image.sh
  scripts/acquire-pgbouncer-rpm.sh
  scripts/attest-pgbouncer-rpm.sh
  scripts/provision-postgres-pgbouncer.sh
  scripts/trainer-host-capability-identity.sh
  scripts/trainer-runtime-tree-identity.sh
  scripts/validate-cloudflared.sh
  scripts/validate-production.sh
  scripts/validate-trainer-host.sh
)

cleanup() {
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
    elif [[ "$relative" == systemd/* ]]; then
      install -m 0644 -- "$REPOSITORY_ROOT/deploy/v2/$relative" "$path"
    elif [[ "$large_payload" == 1 && "$relative" == 'bin/ascendanyd' ]]; then
      dd if=/dev/zero of="$path" bs=1M count=64 status=none
      chmod "$mode" -- "$path"
    else
      printf 'fixture payload: %s\n' "$relative" >"$path"
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
    --argjson files "$files" '
      {
        schema: "ascendany.release.v2",
        version: "1.2.3",
        commit: "0123456789abcdef0123456789abcdef01234567",
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
  create_release "$case_root/source" "${2:-0}"
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

run_installer() {
  local case_root="$1"
  local manifest_sha256
  shift
  manifest_sha256="$(<"$case_root/expected-manifest.sha256")"
  bwrap "${BWRAP_RUNTIME[@]}" \
    --bind "$case_root" /fixture \
    --bind "$case_root/opt" /opt \
    --dir /trusted \
    --ro-bind "$INSTALLER_SOURCE" /trusted/install-v2-release.sh \
    --chdir / \
    "$@" \
    /trusted/install-v2-release.sh \
      --source /fixture/source \
      --manifest-sha256 "$manifest_sha256"
}

run_shell() {
  local case_root="$1"
  local command="$2"
  shift 2
  bwrap "${BWRAP_RUNTIME[@]}" \
    --bind "$case_root" /fixture \
    --bind "$case_root/opt" /opt \
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

expect_failure "$happy_case/duplicate.log" run_installer "$happy_case" /usr/bin/env
require_log "$happy_case/duplicate.log" 'canonical release target already exists'
diff -r -- "$happy_case/source" "$happy_case/opt/ascendany/v2" >/dev/null || fail 'duplicate install changed the existing canonical release'

bytes_case="$(new_case bytes-drift)"
printf 'tampered bytes\n' >"$bytes_case/source/README.md"
expect_failure "$bytes_case/install.log" run_installer "$bytes_case" /usr/bin/env
require_log "$bytes_case/install.log" 'release source payload integrity differs from its manifest: README.md'
require_log "$bytes_case/install.log" 'release installation state: pre-commit-staging-retained'
[[ ! -e "$bytes_case/opt/ascendany/v2" ]] || fail 'byte-drift case published a target'
expect_failure "$bytes_case/retry.log" run_installer "$bytes_case" /usr/bin/env
require_log "$bytes_case/retry.log" 'pre-existing incomplete release staging requires explicit operator resolution:'

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

if find "$happy_case/opt" "$concurrent_case/opt" -path '*/.v2.installing.*' -print -quit | grep -q .; then
  fail 'successful installation left a private staging directory'
fi

printf '%s\n' 'release installer hostile fixture passed'
