#!/usr/bin/bash -p
set +x
set -Eeuo pipefail

umask 077
export PATH=/usr/bin:/bin
readonly PATH
export LC_ALL=C

readonly restore_user="ascendany-restore"
readonly restore_parent="/var/lib/ascendany-restore"
readonly evidence_directory="/var/lib/ascendany-acceptance"
readonly evidence_path="${evidence_directory}/restore-verify.json"
readonly release_manifest="/opt/ascendany/v2/release-manifest.json"
readonly backup_root="/var/backups/ascendany"
readonly lock_directory="/run/ascendany-restore-operator"
readonly operator_lock="${lock_directory}/operator.lock"
readonly publication_lock="${lock_directory}/publication.lock"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

[[ "$EUID" == "0" ]] || fail "restore evidence publisher requires root"
[[ "$#" == "1" && "$1" =~ ^backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$ ]] || fail "restore evidence backup ID is noncanonical"
readonly backup_id="$1"
readonly pending_evidence="${restore_parent}/restore-verify.${backup_id}.pending.json"
restore_uid="$(id -u "$restore_user")" || fail "restore service identity is unavailable"
[[ -d "$evidence_directory" && ! -L "$evidence_directory" &&
   "$(stat -Lc '%u:%g:%a' "$evidence_directory")" == "0:0:700" ]] || fail "restore evidence directory has unsafe metadata"
[[ -f "$release_manifest" && ! -L "$release_manifest" ]] || fail "release manifest is unavailable"
[[ -f "$backup_root/$backup_id/manifest.json" && ! -L "$backup_root/$backup_id/manifest.json" ]] || fail "restored backup manifest is unavailable"
[[ -d "$lock_directory" && ! -L "$lock_directory" &&
   "$(stat -Lc '%U:%G:%a' "$lock_directory")" == "root:${restore_user}:750" &&
   -f "$operator_lock" && ! -L "$operator_lock" &&
   "$(stat -Lc '%U:%G:%a:%h' "$operator_lock")" == "${restore_user}:${restore_user}:600:1" &&
   -f "$publication_lock" && ! -L "$publication_lock" &&
   "$(stat -Lc '%U:%G:%a:%h' "$publication_lock")" == "root:root:600:1" ]] ||
  fail "restore publication locks violate the stable inode contract"
[[ "$(stat -Lc '%d' "$restore_parent")" == "$(stat -Lc '%d' "$evidence_directory")" ]] ||
  fail "restore pending and acceptance directories must share one filesystem"

exec 8<>"$operator_lock"
flock -n 8 || fail "another restore verification is active"
exec 9<>"$publication_lock"
flock -n 9 || fail "another restore evidence publication is active"

quarantine="$(mktemp -d "$evidence_directory/.restore-quarantine.XXXXXX")"
captured="$quarantine/pending.json"
temporary="$(mktemp "$evidence_directory/.restore-verify.XXXXXX")"
cleanup() {
  rm -f -- "$temporary" "$captured"
  rmdir -- "$quarantine" 2>/dev/null || true
}
trap cleanup EXIT

mv --no-copy --no-clobber --no-target-directory -- "$pending_evidence" "$captured" ||
  fail "pending restore evidence could not be captured atomically"
[[ -f "$captured" && ! -L "$captured" &&
   "$(stat -Lc '%u:%a:%h' "$captured")" == "${restore_uid}:600:1" ]] ||
  fail "captured restore evidence has unsafe metadata"
chown root:root "$captured"
chmod 0400 "$captured"
cp --no-dereference --reflink=never -- "$captured" "$temporary"
chown root:root "$temporary"
chmod 0600 "$temporary"
sync -f "$temporary"
[[ -f "$temporary" && ! -L "$temporary" &&
   "$(stat -Lc '%u:%g:%a:%h' "$temporary")" == "0:0:600:1" ]] ||
  fail "root-captured restore evidence has unsafe metadata"

release_commit="$(jq -er '.commit | select(type == "string" and test("^[0-9a-f]{40}$"))' "$release_manifest")" || fail "release commit is invalid"
release_version="$(jq -er '.version | select(type == "string" and length > 0 and length <= 128)' "$release_manifest")" || fail "release version is invalid"
manifest_sha="$(sha256sum -- "$backup_root/$backup_id/manifest.json" | awk '{print $1}')"
artifact_count="$(jq -er '.artifacts.count | select(type == "number" and floor == . and . >= 0)' "$backup_root/$backup_id/manifest.json")" ||
  fail "restored backup artifact count is invalid"
jq -e \
  --arg backupId "$backup_id" \
  --arg manifestSHA256 "$manifest_sha" \
  --arg releaseCommit "$release_commit" \
  --arg releaseVersion "$release_version" \
  --argjson artifactCount "$artifact_count" '
    type == "object" and
    (keys == ["artifactCount", "backupId", "databaseName", "level", "manifestSHA256", "msg", "releaseCommit", "releaseVersion", "time"]) and
    .level == "INFO" and .msg == "backup restore verified" and
    .backupId == $backupId and .manifestSHA256 == $manifestSHA256 and
    .databaseName == "ascendany_v2_restore_verify" and
    .releaseCommit == $releaseCommit and .releaseVersion == $releaseVersion and
    .artifactCount == $artifactCount and
    (.time | type == "string" and test("^[0-9]{4}-(0[1-9]|1[0-2])-([0-2][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\\.[0-9]{1,9})?Z$"))
  ' "$temporary" >/dev/null || fail "captured restore evidence does not bind the installed release and backup"
evidence_time="$(jq -er '.time' "$temporary")"
evidence_epoch="$(date -u --date="$evidence_time" +%s 2>/dev/null || true)"
now_epoch="$(date -u +%s)"
[[ -n "$evidence_epoch" && "$evidence_epoch" -le "$now_epoch" &&
   $((now_epoch - evidence_epoch)) -le 3600 ]] ||
  fail "captured restore evidence timestamp is invalid, future-dated, or stale"

if [[ -e "$evidence_path" || -L "$evidence_path" ]]; then
  [[ -f "$evidence_path" && ! -L "$evidence_path" &&
     "$(stat -Lc '%u:%g:%a:%h' "$evidence_path")" == "0:0:600:1" ]] ||
    fail "existing restore evidence has unsafe metadata"
  existing_backup_id="$(jq -er '.backupId | select(type == "string" and test("^backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$"))' "$evidence_path")" ||
    fail "existing restore evidence has an invalid backup ID"
  existing_time="$(jq -er '.time | select(type == "string")' "$evidence_path")" ||
    fail "existing restore evidence has an invalid timestamp"
  existing_epoch="$(date -u --date="$existing_time" +%s 2>/dev/null || true)"
  [[ -n "$existing_epoch" ]] || fail "existing restore evidence timestamp cannot be parsed"
  if [[ "$existing_backup_id" > "$backup_id" ]] ||
     [[ "$existing_backup_id" == "$backup_id" && "$existing_epoch" -ge "$evidence_epoch" ]]; then
    exit 0
  fi
fi

mv -f -- "$temporary" "$evidence_path"
temporary=""
sync -f "$evidence_directory"
rm -f -- "$captured"
rmdir -- "$quarantine"
trap - EXIT
