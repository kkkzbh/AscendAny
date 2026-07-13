#!/usr/bin/bash -p
set +x
set -Eeuo pipefail

umask 077
export PATH=/usr/bin:/bin
readonly PATH
export LC_ALL=C

readonly restore_user="ascendany-restore"
readonly restore_login="ascendany_restore_login"
readonly owner_role="ascendany_owner"
readonly scratch_database="ascendany_v2_restore_verify"
readonly maintenance_database="postgres"
readonly restore_parent="/var/lib/ascendany-restore"
readonly artifact_root="${restore_parent}/artifacts"
readonly catalog_receipt_root="${restore_parent}/catalog-receipts"
readonly lock_directory="/run/ascendany-restore-operator"
readonly lock_file="${lock_directory}/operator.lock"
readonly backup_root="/var/backups/ascendany"
readonly backup_binary="/opt/ascendany/v2/bin/ascendany-backup"
readonly release_manifest="/opt/ascendany/v2/release-manifest.json"
readonly recommendation_model="/opt/ascendany/v2/models/recommendation-model.json"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

validate_backup_id() {
  [[ "$1" =~ ^backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$ ]] ||
    fail "restore backup ID is noncanonical"
}

validate_context() {
  [[ "${BASH_SOURCE[0]}" == "$0" ]] || fail "restore operator must execute directly"
  [[ "$#" == "2" ]] || fail "usage: restore-verify-operator.sh run BACKUP_ID"
  [[ "$1" == "run" ]] || fail "restore operator command is invalid"
  validate_backup_id "$2"
  [[ "$(id -un)" == "$restore_user" ]] || fail "restore operator must run as ${restore_user}"
  [[ "${ASCENDANY_BACKUP_ROOT-}" == "$backup_root" ]] || fail "restore backup root differs from the unit contract"
  [[ "${ASCENDANY_BACKUP_FORMAT-}" == "pg_custom_plus_artifact_and_catalog_receipt_tar_zstd" ]] || fail "restore backup format differs from the unit contract"
  [[ "${ASCENDANY_BACKUP_MANIFEST_HASH-}" == "sha256" ]] || fail "restore manifest hash differs from the unit contract"
  [[ "${ASCENDANY_RESTORE_DATABASE_URL-}" == "postgresql://${restore_login}@127.0.0.1:5432/${scratch_database}" ]] ||
    fail "restore database URL differs from the unit contract"
  [[ "${ASCENDANY_RESTORE_ARTIFACT_ROOT-}" == "$artifact_root" ]] || fail "restore artifact root differs from the unit contract"
  [[ "${ASCENDANY_RESTORE_CATALOG_RECEIPT_ROOT-}" == "$catalog_receipt_root" ]] ||
    fail "restore catalog receipt root differs from the unit contract"
  [[ "${ASCENDANY_RESTORE_RUNTIME_ROOT-}" == "/run/ascendany-restore-verify-$2" ]] ||
    fail "restore runtime root differs from the per-instance unit contract"
  [[ "${ASCENDANY_DATABASE_CONNECT_TIMEOUT-}" == "5s" ]] || fail "restore connect timeout differs from the unit contract"
  [[ "${ASCENDANY_BACKUP_COMMAND_TIMEOUT-}" == "2h" ]] || fail "restore command timeout differs from the unit contract"
  [[ "${ASCENDANY_PG_DUMP_PATH-}" == "/usr/bin/pg_dump" &&
     "${ASCENDANY_PG_RESTORE_PATH-}" == "/usr/bin/pg_restore" &&
     "${ASCENDANY_ZSTD_PATH-}" == "/usr/bin/zstd" ]] || fail "restore tool paths differ from the unit contract"
  [[ "${ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE-}" == /* &&
     -f "${ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE}" &&
     ! -L "${ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE}" ]] || fail "restore database credential is unavailable"
  [[ -x "$backup_binary" && ! -L "$backup_binary" ]] || fail "restore binary is unavailable"
  [[ -f "$release_manifest" && ! -L "$release_manifest" ]] || fail "installed release manifest is unavailable"
  [[ -f "$recommendation_model" && ! -L "$recommendation_model" ]] || fail "installed recommendation model is unavailable"
  [[ -d "$restore_parent" && ! -L "$restore_parent" ]] || fail "restore state directory is unavailable"
  [[ -d "$ASCENDANY_RESTORE_RUNTIME_ROOT" && ! -L "$ASCENDANY_RESTORE_RUNTIME_ROOT" &&
     "$(stat -Lc '%U:%G:%a' "$ASCENDANY_RESTORE_RUNTIME_ROOT")" == "${restore_user}:${restore_user}:700" ]] ||
    fail "restore runtime directory violates the per-instance owner/mode contract"
  [[ -d "$lock_directory" && ! -L "$lock_directory" &&
     "$(stat -Lc '%U:%G:%a' "$lock_directory")" == "root:${restore_user}:750" &&
     -f "$lock_file" && ! -L "$lock_file" &&
     "$(stat -Lc '%U:%G:%a:%h' "$lock_file")" == "${restore_user}:${restore_user}:600:1" ]] ||
    fail "restore operator lock violates the stable inode contract"
  [[ -d "$backup_root/$2" && ! -L "$backup_root/$2" ]] || fail "requested backup bundle is unavailable"
  for command in awk chmod cp date flock id jq mv psql rm sha256sum stat sync wc; do
    command -v "$command" >/dev/null 2>&1 || fail "required restore command is unavailable: $command"
  done
}

write_pgpass() {
  local target="$1"
  /usr/bin/awk -v user="$restore_login" '
    NR == 1 {
      password = $0
      if (length(password) < 16 || password ~ /^[[:space:]]/ || password ~ /[[:space:]]$/) exit 2
      gsub(/\\/, "\\\\", password)
      gsub(/:/, "\\:", password)
      printf "127.0.0.1:5432:*:%s:%s\n", user, password
      next
    }
    { exit 2 }
    END { if (NR != 1 || length(password) == 0) exit 2 }
  ' "$ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE" >"$target" || fail "restore database credential is not one canonical line"
  chmod 0600 "$target"
}

run_psql() {
  local pgpass="$1"
  shift
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    PGHOST=127.0.0.1 \
    PGPORT=5432 \
    PGDATABASE="$maintenance_database" \
    PGUSER="$restore_login" \
    PGCONNECT_TIMEOUT=5 \
    PGPASSFILE="$pgpass" \
    /usr/bin/psql -X -v ON_ERROR_STOP=1 "$@"
}

run_owner_psql() {
  local pgpass="$1"
  shift
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    PGHOST=127.0.0.1 \
    PGPORT=5432 \
    PGDATABASE="$maintenance_database" \
    PGUSER="$restore_login" \
    PGCONNECT_TIMEOUT=5 \
    PGPASSFILE="$pgpass" \
    PGOPTIONS="-c role=${owner_role}" \
    /usr/bin/psql -X -v ON_ERROR_STOP=1 "$@"
}

drop_owned_scratch_database() {
  local pgpass="$1" owner
  if ! owner="$(run_psql "$pgpass" -A -t -c \
    "SELECT owner.rolname FROM pg_database AS database JOIN pg_roles AS owner ON owner.oid = database.datdba WHERE database.datname = '${scratch_database}'")"; then
    printf '%s\n' "failed to inspect the scratch database owner" >&2
    return 1
  fi
  if [[ -z "$owner" ]]; then
    return
  fi
  if [[ "$owner" != "$owner_role" ]]; then
    printf '%s\n' "scratch database exists with an unexpected owner" >&2
    return 1
  fi
  run_owner_psql "$pgpass" -c "ALTER DATABASE ${scratch_database} WITH ALLOW_CONNECTIONS false" >/dev/null || return 1
  run_owner_psql "$pgpass" -c "DROP DATABASE ${scratch_database} WITH (FORCE)" >/dev/null || return 1
}

remove_owned_scratch_paths() {
  local backup_id="$1"
  if [[ "$artifact_root" != "$restore_parent/artifacts" ||
        "$catalog_receipt_root" != "$restore_parent/catalog-receipts" ||
        "$restore_parent" == "/" ]]; then
    printf '%s\n' "restore cleanup path contract is invalid" >&2
    return 1
  fi
  rm -rf --one-file-system -- \
    "$artifact_root" \
    "$catalog_receipt_root" \
    "$restore_parent/.restore-$backup_id" \
    "$restore_parent/.restore-catalog-receipts-$backup_id"
  sync -f "$restore_parent"
}

validate_result() {
  local backup_id="$1" result_file="$2" manifest_sha result_time
  [[ "$(wc -l <"$result_file")" == "1" ]] || fail "restore verifier must emit exactly one JSON log line"
  manifest_sha="$(sha256sum -- "$backup_root/$backup_id/manifest.json" | awk '{print $1}')"
  jq -e --arg backupId "$backup_id" --arg manifestSHA256 "$manifest_sha" \
    --arg catalogReceiptRoot "$catalog_receipt_root" \
    --slurpfile manifest "$backup_root/$backup_id/manifest.json" \
    --slurpfile release "$release_manifest" \
    --slurpfile model "$recommendation_model" '
    type == "object" and
    (keys == ["artifactCount", "backupId", "catalogReceiptCount", "catalogReceiptRoot", "databaseName", "level", "manifestSHA256", "modelApplicationBuildTime", "modelApplicationCommit", "modelApplicationVersion", "modelArtifactSHA256", "modelFeatureSchemaSHA256", "modelHeadRevision", "modelId", "modelKnowledgeCatalogSHA256", "modelManifestSHA256", "modelPurpose", "msg", "releaseCommit", "releaseVersion", "time"]) and
    .level == "INFO" and .msg == "backup restore verified" and
    .backupId == $backupId and .manifestSHA256 == $manifestSHA256 and
    .databaseName == "ascendany_v2_restore_verify" and
    (.artifactCount | type == "number" and floor == . and . >= 0) and
    (.catalogReceiptCount | type == "number" and floor == . and . > 0) and
    .catalogReceiptRoot == $catalogReceiptRoot and
    ($manifest | length == 1) and
    $manifest[0].schema == "ascendany.backup.bundle.v2" and
    .artifactCount == $manifest[0].artifacts.count and
    .catalogReceiptCount == $manifest[0].catalogPublicationReceipts.count and
    .catalogReceiptCount == ($manifest[0].catalogPublicationReceipts.entries | length) and
    .catalogReceiptCount == ($manifest[0].database.knowledgeCatalogPublicationIds | length) and
    .catalogReceiptCount == ($manifest[0].database.knowledgeCatalogPublications | length) and
    (.modelId | type == "string" and test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
    (.modelArtifactSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.modelHeadRevision | type == "number" and floor == . and . > 0) and
    (.modelApplicationVersion | type == "string" and length > 0 and length <= 128) and
    (.modelApplicationCommit | type == "string" and length > 0 and length <= 128) and
    (.modelApplicationBuildTime | type == "string" and length > 0 and length <= 128) and
    (.modelFeatureSchemaSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.modelKnowledgeCatalogSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.modelManifestSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.modelPurpose == "production") and
    .modelId == $manifest[0].database.recommendationModel.modelId and
    .modelArtifactSHA256 == $manifest[0].database.recommendationModel.artifactSha256 and
    .modelHeadRevision == $manifest[0].database.recommendationModel.headRevision and
    .modelApplicationVersion == $manifest[0].database.recommendationModel.applicationVersion and
    .modelApplicationCommit == $manifest[0].database.recommendationModel.applicationCommit and
    .modelApplicationBuildTime == $manifest[0].database.recommendationModel.applicationBuildTime and
    .modelFeatureSchemaSHA256 == $manifest[0].database.recommendationModel.featureSchemaSha256 and
    .modelKnowledgeCatalogSHA256 == $manifest[0].database.recommendationModel.knowledgeCatalogSha256 and
    .modelManifestSHA256 == $manifest[0].database.recommendationModel.manifestSha256 and
    .modelPurpose == $manifest[0].database.recommendationModel.modelPurpose and
    (.releaseCommit | type == "string" and test("^[0-9a-f]{40}$")) and
    (.releaseVersion | type == "string" and length > 0 and length <= 128) and
    ($release | length == 1) and
    ($release[0] | type == "object" and .schema == "ascendany.release.v2" and .purpose == "production") and
    ($model | length == 1) and $model[0].manifest.purpose == "production" and
    ([ $release[0].files[] | select(.path == "models/recommendation-model.json") ] | length == 1) and
    .releaseCommit == $release[0].commit and
    .releaseVersion == $release[0].version and
    .modelArtifactSHA256 == ([ $release[0].files[] | select(.path == "models/recommendation-model.json") ][0].sha256) and
    .modelPurpose == $release[0].purpose and .modelPurpose == $model[0].manifest.purpose and
    .modelApplicationCommit == $release[0].commit and
    .modelApplicationVersion == $release[0].version and
    .modelApplicationBuildTime == ($release[0].sourceDateEpoch | todateiso8601) and
    (.time | type == "string" and test("^[0-9]{4}-(0[1-9]|1[0-2])-([0-2][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\\.[0-9]{1,9})?Z$"))
  ' "$result_file" >/dev/null || fail "restore verifier result violates the canonical evidence contract"
  result_time="$(jq -er '.time' "$result_file")"
  date -u --date="$result_time" +%s >/dev/null || fail "restore verifier result has an invalid timestamp"
}

run_restore() {
  local backup_id="$1"
  local runtime_root="$ASCENDANY_RESTORE_RUNTIME_ROOT"
  local pgpass="$runtime_root/operator.pgpass"
  local result_file="$runtime_root/restore-verify.log"
  local go_pgpass="$runtime_root/restore.pgpass"
  local pending_evidence="$restore_parent/restore-verify.${backup_id}.pending.json"
  local pending_staging="$restore_parent/.restore-verify.${backup_id}.pending.tmp"
  local complete=0

  emergency_cleanup() {
    local status=$?
    trap - EXIT
    set +e
    if [[ "$complete" != "1" ]]; then
      drop_owned_scratch_database "$pgpass" >/dev/null 2>&1
      remove_owned_scratch_paths "$backup_id" >/dev/null 2>&1
      rm -f -- "$pending_evidence" "$pending_staging"
    fi
    rm -f -- "$pgpass" "$go_pgpass" "$result_file"
    exit "$status"
  }
  trap emergency_cleanup EXIT

  rm -f -- "$pending_evidence" "$pending_staging" "$pgpass" "$go_pgpass" "$result_file"
  write_pgpass "$pgpass"
  drop_owned_scratch_database "$pgpass"
  remove_owned_scratch_paths "$backup_id"
  run_psql "$pgpass" -c \
    "CREATE DATABASE ${scratch_database} WITH OWNER ${owner_role} TEMPLATE template0 ENCODING 'UTF8' ALLOW_CONNECTIONS false" >/dev/null
  run_owner_psql "$pgpass" -c \
    "REVOKE ALL PRIVILEGES ON DATABASE ${scratch_database} FROM PUBLIC" >/dev/null
  run_owner_psql "$pgpass" -c \
    "GRANT CONNECT ON DATABASE ${scratch_database} TO ${restore_login}" >/dev/null
  run_owner_psql "$pgpass" -c \
    "ALTER DATABASE ${scratch_database} WITH ALLOW_CONNECTIONS true" >/dev/null

  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_BACKUP_ROOT="$ASCENDANY_BACKUP_ROOT" \
    ASCENDANY_BACKUP_FORMAT="$ASCENDANY_BACKUP_FORMAT" \
    ASCENDANY_BACKUP_MANIFEST_HASH="$ASCENDANY_BACKUP_MANIFEST_HASH" \
    ASCENDANY_RESTORE_DATABASE_URL="$ASCENDANY_RESTORE_DATABASE_URL" \
    ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE="$ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE" \
    ASCENDANY_RESTORE_ARTIFACT_ROOT="$ASCENDANY_RESTORE_ARTIFACT_ROOT" \
    ASCENDANY_RESTORE_CATALOG_RECEIPT_ROOT="$ASCENDANY_RESTORE_CATALOG_RECEIPT_ROOT" \
    ASCENDANY_RESTORE_RUNTIME_ROOT="$ASCENDANY_RESTORE_RUNTIME_ROOT" \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT="$ASCENDANY_DATABASE_CONNECT_TIMEOUT" \
    ASCENDANY_BACKUP_COMMAND_TIMEOUT="$ASCENDANY_BACKUP_COMMAND_TIMEOUT" \
    ASCENDANY_PG_DUMP_PATH="$ASCENDANY_PG_DUMP_PATH" \
    ASCENDANY_PG_RESTORE_PATH="$ASCENDANY_PG_RESTORE_PATH" \
    ASCENDANY_ZSTD_PATH="$ASCENDANY_ZSTD_PATH" \
    "$backup_binary" restore-verify "$backup_id" > /dev/null 2>"$result_file"

  validate_result "$backup_id" "$result_file"
  cp --no-dereference --reflink=never -- "$result_file" "$pending_staging"
  chmod 0600 "$pending_staging"
  sync -f "$pending_staging"
  mv --no-copy --no-clobber --no-target-directory -- "$pending_staging" "$pending_evidence"
  sync -f "$pending_evidence"
  sync -f "$restore_parent"
  drop_owned_scratch_database "$pgpass"
  remove_owned_scratch_paths "$backup_id"
  rm -f -- "$pgpass" "$go_pgpass" "$result_file"
  complete=1
  trap - EXIT
}

validate_context "$@"
exec 9<>"$lock_file"
flock -n 9 || fail "another restore verification is active"
run_restore "$2"
