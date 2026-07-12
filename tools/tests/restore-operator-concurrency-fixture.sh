#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
operator="$repository_root/deploy/v2/scripts/restore-verify-operator.sh"
publisher="$repository_root/deploy/v2/scripts/publish-restore-evidence.sh"
unit="$repository_root/deploy/v2/systemd/ascendany-restore-verify@.service"
tmpfiles="$repository_root/deploy/v2/tmpfiles.d/ascendany-v2.conf"
fixture_root="$(mktemp -d)"

cleanup_fixture() {
  rm -rf -- "$fixture_root"
}
trap cleanup_fixture EXIT

fail() {
  printf 'fixture failure: %s\n' "$1" >&2
  exit 1
}

require_exact_line() {
  local file="$1" expected="$2"
  [[ "$(grep -Fxc -- "$expected" "$file")" == "1" ]] ||
    fail "$file does not contain exactly one expected line: $expected"
}

line_number() {
  local file="$1" needle="$2"
  local -a matches=()
  mapfile -t matches < <(grep -nF -- "$needle" "$file")
  [[ "${#matches[@]}" == "1" ]] ||
    fail "$file does not contain exactly one ordered marker: $needle"
  printf '%s\n' "${matches[0]%%:*}"
}

attempt_lock() {
  local path="$1"
  /usr/bin/bash -c 'exec 9<>"$1"; flock -n 9' _ "$path"
}

publish_validated_temp() {
  local temporary="$1" expected_sha256="$2" destination="$3"
  [[ "$(sha256sum -- "$temporary" | awk '{print $1}')" == "$expected_sha256" ]] || return 42
  mv -f -- "$temporary" "$destination"
}

for required_command in awk cmp flock grep mapfile mktemp mv sha256sum stat; do
  command -v "$required_command" >/dev/null 2>&1 || fail "required command is unavailable: $required_command"
done

# The real unit must keep the durable lock inode outside every instance runtime
# directory. systemd may remove one instance directory without touching another
# instance or the stable lock.
require_exact_line "$unit" 'Environment=ASCENDANY_RESTORE_RUNTIME_ROOT=%t/ascendany-restore-verify-%i'
require_exact_line "$unit" 'RuntimeDirectory=ascendany-restore-verify-%i'
require_exact_line "$unit" 'RuntimeDirectoryMode=0700'
require_exact_line "$unit" 'ReadWritePaths=/var/lib/ascendany-restore /var/lib/ascendany-acceptance /run/ascendany-restore-operator /run/ascendany-restore-verify-%i'
if grep -Eq '^[[:space:]]*ExecStopPost=' "$unit"; then
  fail 'restore unit reintroduced cross-instance ExecStopPost cleanup'
fi
if grep -F 'RuntimeDirectory=ascendany-restore-operator' "$unit" >/dev/null; then
  fail 'stable operator lock directory is still owned by an instance lifecycle'
fi

awk '
  $1 == "d" && $2 == "/run/ascendany-restore-operator" &&
    $3 == "0750" && $4 == "root" && $5 == "ascendany-restore" { directory += 1 }
  $1 == "f" && $2 == "/run/ascendany-restore-operator/operator.lock" &&
    $3 == "0600" && $4 == "ascendany-restore" && $5 == "ascendany-restore" { operator += 1 }
  $1 == "f" && $2 == "/run/ascendany-restore-operator/publication.lock" &&
    $3 == "0600" && $4 == "root" && $5 == "root" { publisher += 1 }
  END { exit !(directory == 1 && operator == 1 && publisher == 1) }
' "$tmpfiles" || fail 'tmpfiles does not create the three stable lock objects with canonical ownership'

require_exact_line "$operator" 'readonly lock_directory="/run/ascendany-restore-operator"'
require_exact_line "$operator" 'readonly lock_file="${lock_directory}/operator.lock"'
require_exact_line "$operator" '  local pending_evidence="$restore_parent/restore-verify.${backup_id}.pending.json"'
require_exact_line "$operator" '  local pending_staging="$restore_parent/.restore-verify.${backup_id}.pending.tmp"'
require_exact_line "$operator" '    ASCENDANY_RESTORE_RUNTIME_ROOT="$ASCENDANY_RESTORE_RUNTIME_ROOT" \'
require_exact_line "$operator" 'exec 9<>"$lock_file"'
require_exact_line "$operator" 'flock -n 9 || fail "another restore verification is active"'

require_exact_line "$publisher" 'readonly operator_lock="${lock_directory}/operator.lock"'
require_exact_line "$publisher" 'readonly publication_lock="${lock_directory}/publication.lock"'
require_exact_line "$publisher" 'readonly pending_evidence="${restore_parent}/restore-verify.${backup_id}.pending.json"'
require_exact_line "$publisher" 'exec 8<>"$operator_lock"'
require_exact_line "$publisher" 'flock -n 8 || fail "another restore verification is active"'
require_exact_line "$publisher" 'exec 9<>"$publication_lock"'
require_exact_line "$publisher" 'flock -n 9 || fail "another restore evidence publication is active"'
require_exact_line "$publisher" 'quarantine="$(mktemp -d "$evidence_directory/.restore-quarantine.XXXXXX")"'
require_exact_line "$publisher" 'temporary="$(mktemp "$evidence_directory/.restore-verify.XXXXXX")"'
require_exact_line "$publisher" 'mv --no-copy --no-clobber --no-target-directory -- "$pending_evidence" "$captured" ||'
require_exact_line "$publisher" 'cp --no-dereference --reflink=never -- "$captured" "$temporary"'
require_exact_line "$publisher" 'chown root:root "$temporary"'
require_exact_line "$publisher" 'chmod 0600 "$temporary"'
require_exact_line "$publisher" "  ' \"\$temporary\" >/dev/null || fail \"captured restore evidence does not bind the installed release and backup\""
require_exact_line "$publisher" 'evidence_time="$(jq -er '\''.time'\'' "$temporary")"'
require_exact_line "$publisher" 'mv -f -- "$temporary" "$evidence_path"'
[[ "$(grep -Fc -- '"$pending_evidence"' "$publisher")" == "1" ]] ||
  fail 'publisher reads the mutable pending pathname outside the single atomic capture'

operator_lock_line="$(line_number "$publisher" 'flock -n 8 || fail "another restore verification is active"')"
capture_line="$(line_number "$publisher" 'mv --no-copy --no-clobber --no-target-directory -- "$pending_evidence" "$captured" ||')"
copy_line="$(line_number "$publisher" 'cp --no-dereference --reflink=never -- "$captured" "$temporary"')"
validation_line="$(line_number "$publisher" "  ' \"\$temporary\" >/dev/null || fail \"captured restore evidence does not bind the installed release and backup\"")"
time_line="$(line_number "$publisher" 'evidence_time="$(jq -er '\''.time'\'' "$temporary")"')"
publish_line="$(line_number "$publisher" 'mv -f -- "$temporary" "$evidence_path"')"
((operator_lock_line < capture_line && capture_line < copy_line && copy_line < validation_line &&
  validation_line < time_line && time_line < publish_line)) ||
  fail 'publisher capture, validation, and publication order drifted'
printf 'PASS fixture restore-static-lock-and-capture-contract\n'

# Three instances exercise the stable-inode design. Instance A keeps the lock;
# instance B disappears; instance C gets a fresh private runtime directory and
# still cannot acquire a different lock inode.
stable_directory="$fixture_root/stable-operator"
stable_lock="$stable_directory/operator.lock"
runtime_a="$fixture_root/runtime-a"
runtime_b="$fixture_root/runtime-b"
runtime_c="$fixture_root/runtime-c"
mkdir -m 0750 -- "$stable_directory"
mkdir -m 0700 -- "$runtime_a" "$runtime_b"
: >"$stable_lock"
chmod 0600 "$stable_lock"
exec {stable_holder_fd}<>"$stable_lock"
flock -n "$stable_holder_fd" || fail 'instance A could not acquire the stable lock'
stable_inode="$(stat -Lc '%d:%i' "$stable_lock")"
rm -rf -- "$runtime_b"
mkdir -m 0700 -- "$runtime_c"
[[ -d "$runtime_a" && ! -e "$runtime_b" && -d "$runtime_c" ]] ||
  fail 'per-instance runtime lifecycle simulation is invalid'
[[ "$(stat -Lc '%d:%i' "$stable_lock")" == "$stable_inode" ]] ||
  fail 'instance B lifecycle replaced the stable lock inode'
if attempt_lock "$stable_lock"; then
  fail 'instance C acquired the stable lock while instance A still held it'
fi
printf 'PASS fixture three-instance-stable-lock\n'

# Negative control: the former shared-RuntimeDirectory layout permits unlinking
# a locked inode and acquiring its replacement. The fixture must demonstrate the
# bypass so a regression to that layout cannot produce a false-green test.
legacy_runtime="$fixture_root/legacy-shared-runtime"
legacy_lock="$legacy_runtime/operator.lock"
mkdir -m 0700 -- "$legacy_runtime"
: >"$legacy_lock"
exec {legacy_holder_fd}<>"$legacy_lock"
flock -n "$legacy_holder_fd" || fail 'legacy negative-control holder could not acquire its lock'
legacy_inode="$(stat -Lc '%d:%i' "$legacy_lock")"
rm -rf -- "$legacy_runtime"
mkdir -m 0700 -- "$legacy_runtime"
: >"$legacy_lock"
replacement_inode="$(stat -Lc '%d:%i' "$legacy_lock")"
[[ "$replacement_inode" != "$legacy_inode" ]] || fail 'legacy negative control did not replace the lock inode'
[[ "$(stat -Lc '%d:%i' "/proc/$$/fd/$legacy_holder_fd")" == "$legacy_inode" ]] ||
  fail 'legacy negative-control holder no longer owns the unlinked inode'
attempt_lock "$legacy_lock" || fail 'legacy replacement inode did not reproduce the concurrent-lock bypass'
printf 'PASS fixture shared-runtime-lock-replacement-negative-control\n'

# Capture first, then replace the source pathname. Validation and publication
# consume the captured publisher-owned temporary bytes, so the replacement does
# not alter the promoted evidence.
restore_state="$fixture_root/restore-state"
acceptance_state="$fixture_root/acceptance-state"
mkdir -m 0700 -- "$restore_state" "$acceptance_state"
good="$fixture_root/good.json"
bad="$fixture_root/bad.json"
printf '%s\n' '{"backupId":"backup-20260711T000000Z-0123456789abcdef","status":"verified"}' >"$good"
printf '%s\n' '{"backupId":"backup-20260711T000000Z-0123456789abcdef","status":"forged"}' >"$bad"
good_sha256="$(sha256sum -- "$good" | awk '{print $1}')"

pending="$restore_state/restore-verify.backup-20260711T000000Z-0123456789abcdef.pending.json"
quarantine="$(mktemp -d "$acceptance_state/.restore-quarantine.XXXXXX")"
captured="$quarantine/pending.json"
temporary="$(mktemp "$acceptance_state/.restore-verify.XXXXXX")"
evidence="$acceptance_state/restore-verify.json"
cp -- "$good" "$pending"
mv --no-copy --no-clobber --no-target-directory -- "$pending" "$captured"
cp -- "$bad" "$pending"
cp --no-dereference --reflink=never -- "$captured" "$temporary"
publish_validated_temp "$temporary" "$good_sha256" "$evidence" ||
  fail 'publisher-owned temporary rejected the captured valid bytes'
cmp --silent -- "$good" "$evidence" || fail 'published evidence changed after source-path replacement'
cmp --silent -- "$bad" "$pending" || fail 'source replacement race was not exercised'
printf 'PASS fixture capture-before-source-replacement\n'

# A replacement that wins before capture is captured, fails validation, and
# cannot publish evidence.
pending_before="$restore_state/restore-verify.backup-20260711T000001Z-fedcba9876543210.pending.json"
quarantine_before="$(mktemp -d "$acceptance_state/.restore-quarantine.XXXXXX")"
captured_before="$quarantine_before/pending.json"
temporary_before="$(mktemp "$acceptance_state/.restore-verify.XXXXXX")"
evidence_before="$acceptance_state/restore-verify-before.json"
cp -- "$good" "$pending_before"
cp -- "$bad" "$pending_before"
mv --no-copy --no-clobber --no-target-directory -- "$pending_before" "$captured_before"
cp --no-dereference --reflink=never -- "$captured_before" "$temporary_before"
if publish_validated_temp "$temporary_before" "$good_sha256" "$evidence_before"; then
  fail 'pre-capture source replacement published unvalidated bytes'
else
  status=$?
  [[ "$status" == "42" ]] || fail 'pre-capture source replacement failed for an unexpected reason'
fi
[[ ! -e "$evidence_before" ]] || fail 'failed capture validation left published evidence'
printf 'PASS fixture pre-capture-source-replacement-fails-closed\n'
