#!/usr/bin/bash -p
set +x
readonly PATH=/usr/bin:/bin
export PATH
set -Eeuo pipefail

[[ "$-" != *x* ]] || {
  /usr/bin/printf '%s\n' 'xtrace must be disabled before the rehearsal initializes secrets' >&2
  exit 2
}

export LC_ALL=C
umask 077

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPOSITORY_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
readonly BACKEND_ROOT="${REPOSITORY_ROOT}/backend"
readonly ROLE_BOOTSTRAP="${REPOSITORY_ROOT}/db/roles/001_v2_roles.sql"
readonly ROLE_VERIFIER="${REPOSITORY_ROOT}/db/roles/verify_v2_roles.sql"
readonly PINTIA_SCHEMA="${REPOSITORY_ROOT}/contracts/pintia/ascendany.pintia.snapshot.v2.schema.json"
readonly DEFAULT_SNAPSHOT_PATH="${REPOSITORY_ROOT}/contracts/pintia/fixtures/valid/complete.json"
readonly RESET_CONFIRMATION="drop-disposable-ascendany-v2-backup-restore"
readonly DEFAULT_POSTGRES_IMAGE="docker.io/library/postgres@sha256:030da09481c3876b71a7e49738a932e1c18c398201a1e4ccfdbff1e5a541215b"
readonly LABEL_KEY="io.ascendany.v2-backup-restore-rehearsal"
readonly SOURCE_DATABASE="ascendany_v2"
readonly SOURCE_HOLD_DATABASE="ascendany_v2_rehearsal_source"
readonly SCRATCH_DATABASE="ascendany_v2_restore_verify"
readonly DATABASE_OWNER="ascendany_database_owner"
readonly SCHEMA_OWNER="ascendany_owner"
readonly MIGRATOR_LOGIN="ascendany_migrator_login"
readonly BACKUP_LOGIN="ascendany_backup_login"
readonly RESTORE_LOGIN="ascendany_restore_login"
readonly RUNTIME_LOGIN="ascendanyd_login"
readonly BACKUP_RUNTIME_PATH="/run/ascendany-backup"

usage() {
  cat <<'EOF'
Usage:
  tools/run-v2-backup-restore-podman-rehearsal.sh \
    --confirm-reset drop-disposable-ascendany-v2-backup-restore \
    [--snapshot /absolute/path/to/ascendany-pintia-snapshot-v2.json]

The default input is the committed sanitized Pintia v2 fixture. --snapshot can
select another absolute, canonical, protected regular file. The rehearsal
validates it with the production Go Pintia validator before treating it as one
immutable artifact. It builds and runs the real Go migrate and backup binaries,
executes create, verify, and restore-verify against a rootless disposable
PostgreSQL 17 pod, checks scratch database ownership and ACLs, then removes the
scratch database, restored artifacts, credentials, pod, and temporary work tree.
The private work tree is created only inside a canonical, mode-0700, user-owned
XDG_RUNTIME_DIR backed by tmpfs.

The export is read only. Its content and digest are never printed.

Optional image override:
  ASCENDANY_BACKUP_REHEARSAL_POSTGRES_IMAGE
EOF
}

fail() {
  printf '%s\n' "$1" >&2
  exit 2
}

CONFIRMATION=""
SNAPSHOT_PATH=""
while (($# > 0)); do
  case "$1" in
    --confirm-reset)
      (($# >= 2)) || fail '--confirm-reset requires a value'
      CONFIRMATION="$2"
      shift 2
      ;;
    --snapshot)
      (($# >= 2)) || fail '--snapshot requires a value'
      [[ -z "${SNAPSHOT_PATH}" ]] || fail '--snapshot may be specified only once'
      SNAPSHOT_PATH="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

[[ "${CONFIRMATION}" == "${RESET_CONFIRMATION}" ]] ||
  fail "--confirm-reset must equal ${RESET_CONFIRMATION}"
((EUID != 0)) || fail 'the backup/restore rehearsal must run as a rootless user'
[[ -n "${SNAPSHOT_PATH}" ]] || SNAPSHOT_PATH="${DEFAULT_SNAPSHOT_PATH}"
[[ "${SNAPSHOT_PATH}" == /* && ! "${SNAPSHOT_PATH}" =~ [[:cntrl:]] ]] ||
  fail '--snapshot must be one absolute path without control characters'
[[ -f "${SNAPSHOT_PATH}" && ! -L "${SNAPSHOT_PATH}" ]] ||
  fail "the Pintia snapshot must be a regular non-symlink file: ${SNAPSHOT_PATH}"
[[ -r "${SNAPSHOT_PATH}" ]] || fail 'the Pintia snapshot is not readable'
[[ -f "${ROLE_BOOTSTRAP}" && -f "${ROLE_VERIFIER}" && -f "${PINTIA_SCHEMA}" ]] ||
  fail 'database role bootstrap, verifier, or Pintia schema is unavailable'

for command_name in awk bwrap cmp find go id install jq mktemp nproc openssl pg_dump \
  pg_isready pg_restore podman psql realpath rmdir sha256sum sort ss stat tr wc zstd; do
  command -v "${command_name}" >/dev/null 2>&1 ||
    fail "required command is unavailable: ${command_name}"
done
unset command_name

if [[ -z "${XDG_RUNTIME_DIR:-}" || "${XDG_RUNTIME_DIR}" != /* ||
  ! -d "${XDG_RUNTIME_DIR}" || -L "${XDG_RUNTIME_DIR}" ]]; then
  fail 'XDG_RUNTIME_DIR must identify an absolute canonical directory'
fi
readonly PRIVATE_RUNTIME_ROOT="$(realpath -e -- "${XDG_RUNTIME_DIR}")"
[[ "${PRIVATE_RUNTIME_ROOT}" == "${XDG_RUNTIME_DIR}" ]] ||
  fail 'XDG_RUNTIME_DIR must identify an absolute canonical directory'
[[ "$(stat -Lc '%u:%a' -- "${PRIVATE_RUNTIME_ROOT}")" == "${EUID}:700" ]] ||
  fail 'XDG_RUNTIME_DIR must be owned by the rehearsal user with mode 0700'
[[ "$(stat -f -c '%T' -- "${PRIVATE_RUNTIME_ROOT}")" == 'tmpfs' ]] ||
  fail 'XDG_RUNTIME_DIR must use tmpfs'

[[ "$(realpath -e -- "${SNAPSHOT_PATH}")" == "${SNAPSHOT_PATH}" ]] ||
  fail '--snapshot must already be canonical and have no symlink ancestry'
readonly SNAPSHOT_OWNER="$(stat -Lc '%u' -- "${SNAPSHOT_PATH}")"
readonly SNAPSHOT_MODE_TEXT="$(stat -Lc '%a' -- "${SNAPSHOT_PATH}")"
readonly SNAPSHOT_MODE="$((8#${SNAPSHOT_MODE_TEXT}))"
[[ "${SNAPSHOT_OWNER}" == "0" || "${SNAPSHOT_OWNER}" == "${EUID}" ]] ||
  fail 'the Pintia snapshot must be owned by root or the rehearsal user'
(( (SNAPSHOT_MODE & 8#022) == 0 )) ||
  fail 'the Pintia snapshot must not be group- or other-writable'

[[ "$(podman info --format '{{.Host.Security.Rootless}}')" == "true" ]] ||
  fail 'Podman is not operating in rootless mode'

readonly POSTGRES_IMAGE="${ASCENDANY_BACKUP_REHEARSAL_POSTGRES_IMAGE:-${DEFAULT_POSTGRES_IMAGE}}"
podman image exists "${POSTGRES_IMAGE}" ||
  fail "PostgreSQL rehearsal image is unavailable; pull it explicitly: ${POSTGRES_IMAGE}"

readonly SOURCE_SIZE="$(stat -Lc '%s' -- "${SNAPSHOT_PATH}")"
[[ "${SOURCE_SIZE}" =~ ^[0-9]+$ ]] && ((SOURCE_SIZE > 0 && SOURCE_SIZE <= 64 * 1024 * 1024)) ||
  fail 'the Pintia snapshot must be between 1 byte and 64 MiB'
readonly SOURCE_SHA256="$(sha256sum -- "${SNAPSHOT_PATH}" | awk '{print $1}')"
[[ "${SOURCE_SHA256}" =~ ^[0-9a-f]{64}$ ]] || fail 'could not hash the Pintia snapshot'
readonly PINTIA_SCHEMA_SHA256="$(sha256sum -- "${PINTIA_SCHEMA}" | awk '{print $1}')"
[[ "${PINTIA_SCHEMA_SHA256}" =~ ^[0-9a-f]{64}$ ]] || fail 'could not hash the Pintia schema'
jq -e --arg digest "${PINTIA_SCHEMA_SHA256}" '
  type == "object" and
  .schema == "ascendany.pintia.snapshot.v2" and
  .schemaSha256 == $digest
' "${SNAPSHOT_PATH}" >/dev/null ||
  fail 'the Pintia snapshot identity does not match the authoritative v2 schema'
readonly SOURCE_STORAGE_KEY="sha256/${SOURCE_SHA256:0:2}/${SOURCE_SHA256}"

readonly WORK_ROOT_PREFIX="${PRIVATE_RUNTIME_ROOT}/ascendany-v2-backup-restore."
WORK_ROOT=""
ADMIN_PASSWORD_FILE=""
ADMIN_PGPASS_FILE=""
RUNTIME_PASSWORD_FILE=""
MIGRATOR_PASSWORD_FILE=""
BACKUP_PASSWORD_FILE=""
RESTORE_PASSWORD_FILE=""
PASSWORD_PEPPER_FILE=""
BOOTSTRAP_ADMIN_PASSWORD_FILE=""
OPERATOR_PGPASS_FILE=""
BEFORE_CONTAINERS=""
BEFORE_PODS=""
AFTER_CONTAINERS=""
AFTER_PODS=""
TOKEN=""
POD_NAME=""
INFRA_CONTAINER_NAME=""
POSTGRES_CONTAINER_NAME=""
DIRECT_HOST=""
LABEL_VALUE=""
POD_CREATE_ATTEMPTED=0
CREDENTIALS_CLEANED=0
SCRATCH_CLEANED=0
BASELINE_CAPTURED=0

cleanup() {
  local original_status=$?
  local cleanup_status=0
  local identities_unchanged=true
  local labeled_containers=""
  local labeled_pods=""
  local labeled_container_count=0
  local labeled_pod_count=0
  local pod_exists_status=1
  local pod_label=""
  trap - EXIT INT TERM
  set +e

  local credential_path
  for credential_path in \
    "${OPERATOR_PGPASS_FILE}" "${ADMIN_PASSWORD_FILE}" "${ADMIN_PGPASS_FILE}" \
    "${RUNTIME_PASSWORD_FILE}" "${MIGRATOR_PASSWORD_FILE}" "${BACKUP_PASSWORD_FILE}" \
    "${RESTORE_PASSWORD_FILE}" "${PASSWORD_PEPPER_FILE}" "${BOOTSTRAP_ADMIN_PASSWORD_FILE}"; do
    if [[ -n "${credential_path}" ]]; then
      rm -f -- "${credential_path}"
    fi
  done

  if ((POD_CREATE_ATTEMPTED == 1)) && [[ -n "${POD_NAME}" ]]; then
    podman pod exists "${POD_NAME}"
    pod_exists_status=$?
    if ((pod_exists_status == 0)); then
      if ! pod_label="$(podman pod inspect \
        --format "{{ index .Labels \"${LABEL_KEY}\" }}" "${POD_NAME}")"; then
        printf 'failed to inspect backup/restore pod ownership: %s\n' "${POD_NAME}" >&2
        cleanup_status=1
      elif [[ "${pod_label}" != "${LABEL_VALUE}" ]]; then
        printf 'refusing to remove a backup/restore pod without the exact ownership label: %s\n' \
          "${POD_NAME}" >&2
        cleanup_status=1
      elif ! podman pod rm --force "${POD_NAME}" >/dev/null; then
        printf 'failed to remove backup/restore rehearsal pod: %s\n' "${POD_NAME}" >&2
        cleanup_status=1
      fi
    elif ((pod_exists_status != 1)); then
      printf 'failed to determine whether the backup/restore pod exists: %s\n' "${POD_NAME}" >&2
      cleanup_status=1
    fi
  fi

  if [[ -n "${LABEL_VALUE}" ]]; then
    if ! labeled_containers="$(podman ps --all \
      --filter "label=${LABEL_KEY}=${LABEL_VALUE}" --format '{{.Names}}' | sort)"; then
      printf 'failed to query labeled backup/restore containers during cleanup\n' >&2
      labeled_container_count=unknown
      cleanup_status=1
    fi
    if ! labeled_pods="$(podman pod ps \
      --filter "label=${LABEL_KEY}=${LABEL_VALUE}" --format '{{.Name}}' | sort)"; then
      printf 'failed to query labeled backup/restore pods during cleanup\n' >&2
      labeled_pod_count=unknown
      cleanup_status=1
    fi
    if [[ -n "${labeled_containers}" || -n "${labeled_pods}" ]]; then
      if [[ -n "${labeled_containers}" && "${labeled_container_count}" == 0 ]]; then
        labeled_container_count="$(wc -l <<<"${labeled_containers}" | tr -d ' ')"
      fi
      if [[ -n "${labeled_pods}" && "${labeled_pod_count}" == 0 ]]; then
        labeled_pod_count="$(wc -l <<<"${labeled_pods}" | tr -d ' ')"
      fi
      printf 'labeled backup/restore resources remain after cleanup\n' >&2
      cleanup_status=1
    fi
  fi

  if ((BASELINE_CAPTURED == 1)); then
    if ! podman ps --all --format '{{.ID}}' | sort >"${AFTER_CONTAINERS}" ||
      ! podman pod ps --format '{{.ID}}' | sort >"${AFTER_PODS}"; then
      identities_unchanged=unknown
      printf 'failed to recapture Podman resource identities during cleanup\n' >&2
      cleanup_status=1
    elif ! cmp --silent "${BEFORE_CONTAINERS}" "${AFTER_CONTAINERS}" ||
      ! cmp --silent "${BEFORE_PODS}" "${AFTER_PODS}"; then
      identities_unchanged=false
      printf 'pre-existing Podman resource identities changed during rehearsal\n' >&2
      cleanup_status=1
    fi
  fi

  if [[ -n "${WORK_ROOT}" && -e "${WORK_ROOT}" ]]; then
    if [[ ! -d "${WORK_ROOT}" || -L "${WORK_ROOT}" ||
      "${WORK_ROOT}" != "${WORK_ROOT_PREFIX}"* ||
      "$(realpath -e -- "${WORK_ROOT}" 2>/dev/null)" != "${WORK_ROOT}" ||
      "$(stat -Lc '%u' -- "${WORK_ROOT}" 2>/dev/null)" != "${EUID}" ]]; then
      printf 'refusing to remove an unowned backup/restore work root\n' >&2
      cleanup_status=1
    elif ! rm -rf --one-file-system -- "${WORK_ROOT}"; then
      printf 'failed to remove the backup/restore work root\n' >&2
      cleanup_status=1
    fi
  fi
  printf 'BACKUP_RESTORE_REHEARSAL_CLEANUP labeled_containers=%s labeled_pods=%s preexisting_identities_unchanged=%s temporary_tree_removed=%s\n' \
    "${labeled_container_count}" \
    "${labeled_pod_count}" \
    "${identities_unchanged}" \
    "$([[ -z "${WORK_ROOT}" || ! -e "${WORK_ROOT}" ]] && printf true || printf false)"

  if ((original_status != 0)); then
    exit "${original_status}"
  fi
  exit "${cleanup_status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

WORK_ROOT="$(mktemp -d "${WORK_ROOT_PREFIX}XXXXXX")"
readonly WORK_ROOT
readonly BIN_DIR="${WORK_ROOT}/bin"
readonly LOG_DIR="${WORK_ROOT}/logs"
readonly CREDENTIAL_DIR="${WORK_ROOT}/credentials"
readonly SOURCE_ARTIFACT_ROOT="${WORK_ROOT}/source-artifacts"
readonly BACKUP_ROOT="${WORK_ROOT}/backups"
readonly RESTORE_PARENT="${WORK_ROOT}/restore"
readonly RESTORE_ARTIFACT_ROOT="${RESTORE_PARENT}/artifacts"
readonly RUNTIME_PARENT="${WORK_ROOT}/runtime"
readonly BACKUP_RUNTIME_ROOT="${RUNTIME_PARENT}/ascendany-backup"
readonly OPERATOR_RUNTIME_ROOT="${RUNTIME_PARENT}/restore-operator"
readonly MIGRATOR_BINARY="${BIN_DIR}/ascendany-migrate"
readonly BACKUP_BINARY="${BIN_DIR}/ascendany-backup"
readonly ADMIN_BOOTSTRAP_BINARY="${BIN_DIR}/ascendany-admin-bootstrap"
readonly PINTIA_VALIDATOR_BINARY="${BIN_DIR}/ascendany-pintia-validate"
readonly ADMIN_PASSWORD_FILE="${CREDENTIAL_DIR}/postgres-password"
readonly ADMIN_PGPASS_FILE="${CREDENTIAL_DIR}/postgres.pgpass"
readonly RUNTIME_PASSWORD_FILE="${CREDENTIAL_DIR}/runtime-password"
readonly MIGRATOR_PASSWORD_FILE="${CREDENTIAL_DIR}/migrator-password"
readonly BACKUP_PASSWORD_FILE="${CREDENTIAL_DIR}/backup-password"
readonly RESTORE_PASSWORD_FILE="${CREDENTIAL_DIR}/restore-password"
readonly PASSWORD_PEPPER_FILE="${CREDENTIAL_DIR}/password-pepper"
readonly BOOTSTRAP_ADMIN_PASSWORD_FILE="${CREDENTIAL_DIR}/bootstrap-admin-password"
readonly OPERATOR_PGPASS_FILE="${OPERATOR_RUNTIME_ROOT}/operator.pgpass"
readonly BEFORE_CONTAINERS="${WORK_ROOT}/containers.before"
readonly BEFORE_PODS="${WORK_ROOT}/pods.before"
readonly AFTER_CONTAINERS="${WORK_ROOT}/containers.after"
readonly AFTER_PODS="${WORK_ROOT}/pods.after"
readonly CREATE_LOG="${LOG_DIR}/create.json"
readonly VERIFY_LOG="${LOG_DIR}/verify.json"
readonly RESTORE_LOG="${LOG_DIR}/restore.json"
readonly ADMIN_BOOTSTRAP_RESULT="${LOG_DIR}/admin-bootstrap.json"
readonly ADMIN_BOOTSTRAP_ERROR="${LOG_DIR}/admin-bootstrap.error"
readonly ADMIN_BOOTSTRAP_SECOND_RESULT="${LOG_DIR}/admin-bootstrap-second.json"
readonly ADMIN_BOOTSTRAP_SECOND_ERROR="${LOG_DIR}/admin-bootstrap-second.error"

mkdir -m 0700 -- "${BIN_DIR}" "${LOG_DIR}" "${CREDENTIAL_DIR}" \
  "${RESTORE_PARENT}" "${RUNTIME_PARENT}" "${BACKUP_RUNTIME_ROOT}" \
  "${OPERATOR_RUNTIME_ROOT}"
install -d -m 0750 -- \
  "${SOURCE_ARTIFACT_ROOT}" \
  "${SOURCE_ARTIFACT_ROOT}/sha256" \
  "${SOURCE_ARTIFACT_ROOT}/sha256/${SOURCE_SHA256:0:2}" \
  "${BACKUP_ROOT}"

podman ps --all --format '{{.ID}}' | sort >"${BEFORE_CONTAINERS}"
podman pod ps --format '{{.ID}}' | sort >"${BEFORE_PODS}"
BASELINE_CAPTURED=1
readonly PREEXISTING_CONTAINER_COUNT="$(wc -l <"${BEFORE_CONTAINERS}" | tr -d ' ')"
readonly PREEXISTING_POD_COUNT="$(wc -l <"${BEFORE_PODS}" | tr -d ' ')"

endpoint_listener() {
  local host="$1"
  ss -H -ltn "src ${host} and sport = :5432"
}

for _attempt in {1..32}; do
  TOKEN="$(openssl rand -hex 6)"
  POD_NAME="ascendany-v2-backup-${TOKEN}"
  INFRA_CONTAINER_NAME="${POD_NAME}-infra"
  POSTGRES_CONTAINER_NAME="${POD_NAME}-postgres"
  DIRECT_HOST="127.127.$((16#${TOKEN:0:2} % 254 + 1)).$((16#${TOKEN:2:2} % 254 + 1))"
  if ! podman pod exists "${POD_NAME}" &&
    ! podman container exists "${INFRA_CONTAINER_NAME}" &&
    ! podman container exists "${POSTGRES_CONTAINER_NAME}" &&
    [[ -z "$(endpoint_listener "${DIRECT_HOST}")" ]]; then
    break
  fi
  TOKEN=""
done
[[ -n "${TOKEN}" ]] || fail 'could not allocate collision-free rootless Podman resources'
readonly TOKEN POD_NAME INFRA_CONTAINER_NAME POSTGRES_CONTAINER_NAME DIRECT_HOST
readonly LABEL_VALUE="${TOKEN}"

readonly POSTGRES_ADMIN_PASSWORD="$(openssl rand -hex 24)"
readonly RUNTIME_PASSWORD="$(openssl rand -hex 24)"
readonly MIGRATOR_PASSWORD="$(openssl rand -hex 24)"
readonly BACKUP_PASSWORD="$(openssl rand -hex 24)"
readonly RESTORE_PASSWORD="$(openssl rand -hex 24)"
readonly PASSWORD_PEPPER="$(openssl rand -hex 32)"
readonly BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -hex 24)"

printf '%s' "${POSTGRES_ADMIN_PASSWORD}" >"${ADMIN_PASSWORD_FILE}"
printf '%s' "${RUNTIME_PASSWORD}" >"${RUNTIME_PASSWORD_FILE}"
printf '%s' "${MIGRATOR_PASSWORD}" >"${MIGRATOR_PASSWORD_FILE}"
printf '%s' "${BACKUP_PASSWORD}" >"${BACKUP_PASSWORD_FILE}"
printf '%s' "${RESTORE_PASSWORD}" >"${RESTORE_PASSWORD_FILE}"
printf '%s' "${PASSWORD_PEPPER}" >"${PASSWORD_PEPPER_FILE}"
printf '%s' "${BOOTSTRAP_ADMIN_PASSWORD}" >"${BOOTSTRAP_ADMIN_PASSWORD_FILE}"
printf '%s:5432:*:postgres:%s\n' "${DIRECT_HOST}" "${POSTGRES_ADMIN_PASSWORD}" >"${ADMIN_PGPASS_FILE}"
printf '%s:5432:*:%s:%s\n' "${DIRECT_HOST}" "${RESTORE_LOGIN}" "${RESTORE_PASSWORD}" >"${OPERATOR_PGPASS_FILE}"
chmod 0600 -- "${ADMIN_PASSWORD_FILE}" "${ADMIN_PGPASS_FILE}" \
  "${RUNTIME_PASSWORD_FILE}" "${MIGRATOR_PASSWORD_FILE}" \
  "${BACKUP_PASSWORD_FILE}" "${RESTORE_PASSWORD_FILE}" \
  "${PASSWORD_PEPPER_FILE}" "${BOOTSTRAP_ADMIN_PASSWORD_FILE}" \
  "${OPERATOR_PGPASS_FILE}"
[[ "$(stat -Lc '%a:%u' -- "${OPERATOR_RUNTIME_ROOT}")" == "700:$(id -u)" ]] ||
  fail 'restore operator runtime root violates the private owner/mode contract'
[[ "$(stat -Lc '%a:%u' -- "${OPERATOR_PGPASS_FILE}")" == "600:$(id -u)" ]] ||
  fail 'restore operator pgpass violates the private owner/mode contract'
[[ -z "$(find "${RESTORE_PARENT}" -mindepth 1 -maxdepth 1 -iname '*pgpass*' -print)" ]] ||
  fail 'durable restore parent contains a pgpass path'

admin_psql() {
  local database="$1"
  shift
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    PGHOST="${DIRECT_HOST}" \
    PGPORT=5432 \
    PGDATABASE="${database}" \
    PGUSER=postgres \
    PGCONNECT_TIMEOUT=5 \
    PGPASSFILE="${ADMIN_PGPASS_FILE}" \
    /usr/bin/psql -X --no-password --set=ON_ERROR_STOP=1 "$@"
}

operator_psql() {
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    PGHOST="${DIRECT_HOST}" \
    PGPORT=5432 \
    PGDATABASE=postgres \
    PGUSER="${RESTORE_LOGIN}" \
    PGCONNECT_TIMEOUT=5 \
    PGPASSFILE="${OPERATOR_PGPASS_FILE}" \
    /usr/bin/psql -X --no-password --set=ON_ERROR_STOP=1 "$@"
}

owner_operator_psql() {
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    PGHOST="${DIRECT_HOST}" \
    PGPORT=5432 \
    PGDATABASE=postgres \
    PGUSER="${RESTORE_LOGIN}" \
    PGCONNECT_TIMEOUT=5 \
    PGPASSFILE="${OPERATOR_PGPASS_FILE}" \
    PGOPTIONS="-c role=${SCHEMA_OWNER}" \
    /usr/bin/psql -X --no-password --set=ON_ERROR_STOP=1 "$@"
}

run_with_private_runtime_root() {
  local host_runtime_root="$1"
  local visible_runtime_root="$2"
  shift 2
  [[ -d "${host_runtime_root}" && ! -L "${host_runtime_root}" &&
     "$(stat -Lc '%a:%u' -- "${host_runtime_root}")" == "700:$(id -u)" ]] ||
    fail 'host runtime root violates the private owner/mode contract'
  [[ "${visible_runtime_root}" == "${BACKUP_RUNTIME_PATH}" ||
     "${visible_runtime_root}" =~ ^/run/ascendany-restore-verify-backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$ ]] ||
    fail 'visible runtime root violates the production path contract'
  /usr/bin/bwrap \
    --die-with-parent \
    --ro-bind / / \
    --dev /dev \
    --tmpfs /run \
    --bind "${WORK_ROOT}" "${WORK_ROOT}" \
    --dir "${visible_runtime_root}" \
    --bind "${host_runtime_root}" "${visible_runtime_root}" \
    -- "$@"
}

print_command_log_on_failure() {
  local label="$1"
  local log_path="$2"
  printf '%s failed; structured command log follows\n' "${label}" >&2
  jq -c '
      {
        time,
        level,
        msg,
        error: ((.error // "")
          | gsub("backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}"; "<redacted-backup-id>")
          | gsub("[0-9a-f]{48,64}"; "<redacted-secret-or-digest>"))
      }
    ' "${log_path}" >&2 2>/dev/null ||
    printf 'structured command log could not be decoded\n' >&2
}

assert_single_success_log() {
  local log_path="$1"
  local expected_message="$2"
  [[ "$(wc -l <"${log_path}" | tr -d ' ')" == "1" ]] ||
    fail "${expected_message} must emit exactly one JSON log line"
  jq -e --arg message "${expected_message}" '
    type == "object" and
    .level == "INFO" and
    .msg == $message and
    (.backupId | type == "string" and test("^backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$")) and
    (.manifestSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
    .artifactCount == 1
  ' "${log_path}" >/dev/null || fail "${expected_message} result violates the JSON evidence contract"
}

assert_scratch_contract() {
  local result
  result="$(admin_psql postgres --tuples-only --no-align --command="
WITH database_boundary AS (
    SELECT database.datdba, database.datacl, database.datallowconn, owner.rolname AS owner_name
    FROM pg_database AS database
    JOIN pg_roles AS owner ON owner.oid = database.datdba
    WHERE database.datname = '${SCRATCH_DATABASE}'
), actual_acl AS (
    SELECT CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
           acl.privilege_type,
           acl.is_grantable
    FROM database_boundary AS database
    CROSS JOIN LATERAL aclexplode(COALESCE(database.datacl, acldefault('d', database.datdba))) AS acl
    LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
), expected_acl(grantee_name, privilege_type, is_grantable) AS (
    VALUES
        ('${SCHEMA_OWNER}', 'CONNECT', false),
        ('${SCHEMA_OWNER}', 'CREATE', false),
        ('${SCHEMA_OWNER}', 'TEMPORARY', false),
        ('${RESTORE_LOGIN}', 'CONNECT', false)
), difference AS (
    (SELECT * FROM actual_acl EXCEPT ALL SELECT * FROM expected_acl)
    UNION ALL
    (SELECT * FROM expected_acl EXCEPT ALL SELECT * FROM actual_acl)
)
SELECT owner_name || '|' || datallowconn::text || '|' ||
       (NOT EXISTS (SELECT 1 FROM difference))::text
FROM database_boundary")"
  [[ "${result}" == "${SCHEMA_OWNER}|true|true" ]] ||
    fail 'scratch database owner or ACL differs from the exact restore contract'
}

assert_restored_full_role_contract() {
  # verify_v2_roles.sql deliberately owns one production database name and
  # database-level boundary. Temporarily swap the two disposable database
  # names and normalize only the restored database's database-level ACL so the
  # authoritative verifier can inspect every restored schema object and ACL.
  admin_psql postgres \
    --set=source_database="${SOURCE_DATABASE}" \
    --set=source_hold_database="${SOURCE_HOLD_DATABASE}" \
    --set=scratch_database="${SCRATCH_DATABASE}" \
    --set=database_owner="${DATABASE_OWNER}" \
    --set=runtime_login="${RUNTIME_LOGIN}" \
    --set=migrator_login="${MIGRATOR_LOGIN}" \
    --set=backup_login="${BACKUP_LOGIN}" >/dev/null <<'SQL'
SELECT format('ALTER DATABASE %I RENAME TO %I', :'source_database', :'source_hold_database')
\gexec
SELECT format('ALTER DATABASE %I RENAME TO %I', :'scratch_database', :'source_database')
\gexec
SELECT format('ALTER DATABASE %I OWNER TO %I', :'source_database', :'database_owner')
\gexec

SELECT format(
    'REVOKE ALL PRIVILEGES ON DATABASE %I FROM %s',
    :'source_database',
    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE format('%I', grantee.rolname) END
)
FROM pg_database AS database
CROSS JOIN LATERAL aclexplode(database.datacl) AS acl
LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
WHERE database.datname = :'source_database'
\gexec
SELECT format('REVOKE ALL PRIVILEGES ON DATABASE %I FROM PUBLIC', :'source_database')
\gexec
SELECT format('GRANT ALL PRIVILEGES ON DATABASE %I TO %I', :'source_database', :'database_owner')
\gexec
SELECT format(
    'GRANT CONNECT ON DATABASE %I TO %I, %I, %I',
    :'source_database', :'runtime_login', :'migrator_login', :'backup_login'
)
\gexec
SQL

  admin_psql "${SOURCE_DATABASE}" --file="${ROLE_VERIFIER}" >/dev/null

  # Restore the original names and the exact scratch database boundary. This
  # touches no restored schema object, object ACL, or default ACL.
  admin_psql postgres \
    --set=source_database="${SOURCE_DATABASE}" \
    --set=source_hold_database="${SOURCE_HOLD_DATABASE}" \
    --set=scratch_database="${SCRATCH_DATABASE}" \
    --set=schema_owner="${SCHEMA_OWNER}" \
    --set=restore_login="${RESTORE_LOGIN}" >/dev/null <<'SQL'
SELECT format('ALTER DATABASE %I RENAME TO %I', :'source_database', :'scratch_database')
\gexec
SELECT format('ALTER DATABASE %I RENAME TO %I', :'source_hold_database', :'source_database')
\gexec
SELECT format('ALTER DATABASE %I OWNER TO %I', :'scratch_database', :'schema_owner')
\gexec

SELECT format(
    'REVOKE ALL PRIVILEGES ON DATABASE %I FROM %s',
    :'scratch_database',
    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE format('%I', grantee.rolname) END
)
FROM pg_database AS database
CROSS JOIN LATERAL aclexplode(database.datacl) AS acl
LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
WHERE database.datname = :'scratch_database'
\gexec
SELECT format('REVOKE ALL PRIVILEGES ON DATABASE %I FROM PUBLIC', :'scratch_database')
\gexec
SELECT format('GRANT ALL PRIVILEGES ON DATABASE %I TO %I', :'scratch_database', :'schema_owner')
\gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'scratch_database', :'restore_login')
\gexec
SELECT format('ALTER DATABASE %I WITH ALLOW_CONNECTIONS true', :'scratch_database')
\gexec
SQL

  assert_scratch_contract
}

printf 'Building real Go validation, migration, admin-bootstrap, and backup binaries under guarded rehearsal control\n'
(
  cd -- "${BACKEND_ROOT}"
  go build -trimpath -p "$(nproc)" -o "${PINTIA_VALIDATOR_BINARY}" ./cmd/ascendany-pintia-validate
  go build -trimpath -p "$(nproc)" -o "${MIGRATOR_BINARY}" ./cmd/ascendany-migrate
  go build -trimpath -p "$(nproc)" -o "${ADMIN_BOOTSTRAP_BINARY}" ./cmd/ascendany-admin-bootstrap
  go build -trimpath -p "$(nproc)" \
    -ldflags "-X github.com/kkkzbh/AscendAny/backend/internal/version.Version=v2-backup-rehearsal -X github.com/kkkzbh/AscendAny/backend/internal/version.Commit=0000000000000000000000000000000000000000 -X github.com/kkkzbh/AscendAny/backend/internal/version.BuildTime=1970-01-01T00:00:00Z" \
    -o "${BACKUP_BINARY}" ./cmd/ascendany-backup
)

/usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C \
  "${PINTIA_VALIDATOR_BINARY}" "${SNAPSHOT_PATH}" >/dev/null 2>"${LOG_DIR}/pintia-validate.json" || {
  print_command_log_on_failure 'Pintia snapshot validation' "${LOG_DIR}/pintia-validate.json"
  exit 1
}

printf 'Creating isolated rootless PostgreSQL 17 pod on %s:5432\n' "${DIRECT_HOST}"
POD_CREATE_ATTEMPTED=1
if podman pod create \
    --name "${POD_NAME}" \
    --infra-name "${INFRA_CONTAINER_NAME}" \
    --label "${LABEL_KEY}=${LABEL_VALUE}" \
    --publish "${DIRECT_HOST}:5432:5432" \
    >/dev/null; then
  :
else
  fail 'could not create the isolated backup/restore rehearsal pod'
fi

podman run --detach \
  --pod "${POD_NAME}" \
  --name "${POSTGRES_CONTAINER_NAME}" \
  --label "${LABEL_KEY}=${LABEL_VALUE}" \
  --env POSTGRES_USER=postgres \
  --env POSTGRES_DB=postgres \
  --env POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password \
  --volume "${ADMIN_PASSWORD_FILE}:/run/secrets/postgres-password:ro,Z" \
  "${POSTGRES_IMAGE}" \
  -c password_encryption=scram-sha-256 \
  >/dev/null

postgres_ready=0
for _attempt in {1..120}; do
  if pg_isready --host="${DIRECT_HOST}" --port=5432 --username=postgres --dbname=postgres \
      >/dev/null 2>&1; then
    postgres_ready=1
    break
  fi
  if [[ "$(podman inspect --format '{{.State.Running}}' "${POSTGRES_CONTAINER_NAME}")" != "true" ]]; then
    podman logs "${POSTGRES_CONTAINER_NAME}" >&2
    fail 'the PostgreSQL 17 container stopped before becoming ready'
  fi
  sleep 0.5
done
((postgres_ready == 1)) || fail 'PostgreSQL 17 did not become ready'
readonly POSTGRES_SERVER_VERSION_NUM="$(admin_psql postgres --tuples-only --no-align \
  --command='SHOW server_version_num')"
[[ "${POSTGRES_SERVER_VERSION_NUM}" =~ ^17[0-9]{4}$ ]] ||
  fail 'the disposable database server is not PostgreSQL 17'

admin_psql postgres --command="CREATE DATABASE ${SOURCE_DATABASE} WITH TEMPLATE template0 ENCODING 'UTF8'" >/dev/null
admin_psql "${SOURCE_DATABASE}" --file="${ROLE_BOOTSTRAP}" >/dev/null

admin_psql "${SOURCE_DATABASE}" >/dev/null <<SQL
ALTER ROLE ${RUNTIME_LOGIN} PASSWORD '${RUNTIME_PASSWORD}';
ALTER ROLE ${MIGRATOR_LOGIN} PASSWORD '${MIGRATOR_PASSWORD}';
ALTER ROLE ${BACKUP_LOGIN} PASSWORD '${BACKUP_PASSWORD}';
ALTER ROLE ${RESTORE_LOGIN} PASSWORD '${RESTORE_PASSWORD}';
SQL

if ! /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_DATABASE_URL="postgresql://${MIGRATOR_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
    ASCENDANY_DATABASE_PASSWORD_FILE="${MIGRATOR_PASSWORD_FILE}" \
    ASCENDANY_DATABASE_ROLE="${SCHEMA_OWNER}" \
    ASCENDANY_DATABASE_SCHEMA=ascendany \
    ASCENDANY_MIGRATION_HISTORY_TABLE=ascendany.schema_migrations_v2 \
    ASCENDANY_DATABASE_SCHEMA_VERSION=5 \
    ASCENDANY_MIGRATION_LOCK_TIMEOUT=30s \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    "${MIGRATOR_BINARY}" up >/dev/null 2>"${LOG_DIR}/migrate.json"; then
  print_command_log_on_failure 'migration' "${LOG_DIR}/migrate.json"
  exit 1
fi

# Reapply the bootstrap after migrations so object ownership, default ACLs, and
# the isolated database-owner boundary are closed over the complete schema.
admin_psql "${SOURCE_DATABASE}" --file="${ROLE_BOOTSTRAP}" >/dev/null
admin_psql "${SOURCE_DATABASE}" --file="${ROLE_VERIFIER}" >/dev/null

run_admin_bootstrap() {
  local stdout_path="$1"
  local stderr_path="$2"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_DATABASE_URL="postgresql://${RUNTIME_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
    ASCENDANY_DATABASE_POOL_MODE=transaction \
    ASCENDANY_DATABASE_PASSWORD_FILE="${RUNTIME_PASSWORD_FILE}" \
    ASCENDANY_DATABASE_SCHEMA_VERSION=5 \
    ASCENDANY_DATABASE_MAX_CONNECTIONS=1 \
    ASCENDANY_DATABASE_MIN_CONNECTIONS=0 \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    ASCENDANY_DATABASE_MAX_CONNECTION_LIFETIME=5m \
    ASCENDANY_DATABASE_MAX_CONNECTION_IDLE_TIME=1m \
    ASCENDANY_DATABASE_HEALTH_TIMEOUT=5s \
    ASCENDANY_PASSWORD_PEPPER_FILE="${PASSWORD_PEPPER_FILE}" \
    "${ADMIN_BOOTSTRAP_BINARY}" create \
      --username admin \
      --display-name admin \
      --password-file "${BOOTSTRAP_ADMIN_PASSWORD_FILE}" \
      >"${stdout_path}" 2>"${stderr_path}"
}

run_admin_bootstrap "${ADMIN_BOOTSTRAP_RESULT}" "${ADMIN_BOOTSTRAP_ERROR}" ||
  fail 'the real administrator bootstrap failed against the fresh migrated database'
[[ ! -s "${ADMIN_BOOTSTRAP_ERROR}" ]] || fail 'administrator bootstrap emitted unexpected stderr'
[[ "$(wc -l <"${ADMIN_BOOTSTRAP_RESULT}" | tr -d ' ')" == "1" ]] ||
  fail 'administrator bootstrap must emit exactly one JSON line'
jq -e '
  type == "object" and
  (keys == ["authRevision", "displayName", "id", "role", "studentNumber", "username"]) and
  (.id | type == "string" and test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
  .username == "admin" and
  .displayName == "admin" and
  .studentNumber == null and
  .role == "admin" and
  .authRevision == 1
' "${ADMIN_BOOTSTRAP_RESULT}" >/dev/null || fail 'administrator bootstrap output violates the exact account contract'
readonly BOOTSTRAP_ADMIN_ID="$(jq -er '.id' "${ADMIN_BOOTSTRAP_RESULT}")"
readonly ADMIN_DATABASE_SUMMARY="$(admin_psql "${SOURCE_DATABASE}" \
  --set=admin_public_id="${BOOTSTRAP_ADMIN_ID}" --tuples-only --no-align <<'SQL'
SELECT
  count(*) FILTER (WHERE account.role = 'admin')::text || '|' ||
  count(*) FILTER (WHERE account.role = 'admin' AND account.disabled_at IS NULL)::text || '|' ||
  count(*) FILTER (
    WHERE account.role = 'admin'
      AND account.public_id = :'admin_public_id'::uuid
      AND account.username = 'admin'
      AND account.display_name = 'admin'
      AND account.actor_id IS NULL
      AND account.student_number IS NULL
      AND account.auth_revision = 1
      AND account.disabled_at IS NULL
      AND account.password_phc LIKE '$argon2id$v=19$m=19456,t=2,p=1$%'
      AND account.created_at = account.updated_at
  )::text || '|' ||
  (SELECT count(*) FROM ascendany.audit_events WHERE event_type = 'auth.admin_bootstrap')::text || '|' ||
  (SELECT count(*)
   FROM ascendany.audit_events AS audit
   JOIN ascendany.auth_accounts AS bootstrap_account
     ON bootstrap_account.account_id = audit.account_id
   WHERE audit.event_type = 'auth.admin_bootstrap'
     AND audit.session_id IS NULL
     AND audit.payload = '{}'::jsonb
     AND audit.occurred_at = bootstrap_account.created_at
     AND bootstrap_account.public_id = :'admin_public_id'::uuid
     AND bootstrap_account.role = 'admin'
     AND bootstrap_account.username = 'admin')::text
FROM ascendany.auth_accounts AS account;
SQL
)"
[[ "${ADMIN_DATABASE_SUMMARY}" == "1|1|1|1|1" ]] ||
  fail 'fresh administrator row or auth.admin_bootstrap audit is noncanonical'

if run_admin_bootstrap "${ADMIN_BOOTSTRAP_SECOND_RESULT}" "${ADMIN_BOOTSTRAP_SECOND_ERROR}"; then
  fail 'a second administrator bootstrap invocation unexpectedly succeeded'
fi
[[ ! -s "${ADMIN_BOOTSTRAP_SECOND_RESULT}" ]] ||
  fail 'rejected second administrator bootstrap emitted an account'
[[ "$(<"${ADMIN_BOOTSTRAP_SECOND_ERROR}")" == \
  'administrator bootstrap failed: auth_admin_already_exists' ]] ||
  fail 'second administrator bootstrap did not fail with auth_admin_already_exists'
[[ "$(admin_psql "${SOURCE_DATABASE}" --tuples-only --no-align --command="
SELECT (SELECT count(*) FROM ascendany.auth_accounts WHERE role = 'admin')::text || '|' ||
       (SELECT count(*) FROM ascendany.audit_events WHERE event_type = 'auth.admin_bootstrap')::text")" == "1|1" ]] ||
  fail 'second administrator bootstrap changed database state'

install -m 0640 -- "${SNAPSHOT_PATH}" "${SOURCE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}"
[[ "$(stat -Lc '%s' -- "${SOURCE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}")" == "${SOURCE_SIZE}" ]] ||
  fail 'the source artifact copy size changed'
[[ "$(sha256sum -- "${SOURCE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}" | awk '{print $1}')" == "${SOURCE_SHA256}" ]] ||
  fail 'the source artifact copy digest changed'

admin_psql "${SOURCE_DATABASE}" \
  --set=artifact_sha="${SOURCE_SHA256}" \
  --set=artifact_size="${SOURCE_SIZE}" \
  --set=artifact_key="${SOURCE_STORAGE_KEY}" >/dev/null <<'SQL'
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES (
    :'artifact_sha',
    :'artifact_size'::bigint,
    'application/vnd.ascendany.pintia.snapshot.v2+json',
    :'artifact_key'
);
SQL

if ! run_with_private_runtime_root \
    "${BACKUP_RUNTIME_ROOT}" \
    "${BACKUP_RUNTIME_PATH}" \
    /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    TZ=UTC \
    ASCENDANY_DATABASE_URL="postgresql://${BACKUP_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
    ASCENDANY_DATABASE_PASSWORD_FILE="${BACKUP_PASSWORD_FILE}" \
    ASCENDANY_ARTIFACT_ROOT="${SOURCE_ARTIFACT_ROOT}" \
    ASCENDANY_BACKUP_ROOT="${BACKUP_ROOT}" \
    ASCENDANY_BACKUP_RUNTIME_ROOT="${BACKUP_RUNTIME_PATH}" \
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_tar_zstd \
    ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
    ASCENDANY_BACKUP_RETAIN_DAILY=1 \
    ASCENDANY_BACKUP_RETAIN_WEEKLY=0 \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    ASCENDANY_BACKUP_COMMAND_TIMEOUT=30m \
    ASCENDANY_PG_DUMP_PATH=/usr/bin/pg_dump \
    ASCENDANY_PG_RESTORE_PATH=/usr/bin/pg_restore \
    ASCENDANY_ZSTD_PATH=/usr/bin/zstd \
    "${BACKUP_BINARY}" create >/dev/null 2>"${CREATE_LOG}"; then
  print_command_log_on_failure 'backup create' "${CREATE_LOG}"
  exit 1
fi
assert_single_success_log "${CREATE_LOG}" 'backup published'
[[ -z "$(find "${BACKUP_RUNTIME_ROOT}" -mindepth 1 -maxdepth 1 -print)" ]] ||
  fail 'backup runtime root retained a private pgpass file'
rmdir -- "${BACKUP_RUNTIME_ROOT}"
readonly BACKUP_ID="$(jq -er '.backupId' "${CREATE_LOG}")"
readonly MANIFEST_SHA256="$(jq -er '.manifestSHA256' "${CREATE_LOG}")"
readonly RESTORE_RUNTIME_PATH="/run/ascendany-restore-verify-${BACKUP_ID}"
readonly RESTORE_RUNTIME_ROOT="${RUNTIME_PARENT}/ascendany-restore-verify-${BACKUP_ID}"
mkdir -m 0700 -- "${RESTORE_RUNTIME_ROOT}"

if ! /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_BACKUP_ROOT="${BACKUP_ROOT}" \
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_tar_zstd \
    ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
    ASCENDANY_BACKUP_COMMAND_TIMEOUT=30m \
    ASCENDANY_PG_DUMP_PATH=/usr/bin/pg_dump \
    ASCENDANY_PG_RESTORE_PATH=/usr/bin/pg_restore \
    ASCENDANY_ZSTD_PATH=/usr/bin/zstd \
    "${BACKUP_BINARY}" verify "${BACKUP_ID}" >/dev/null 2>"${VERIFY_LOG}"; then
  print_command_log_on_failure 'backup verify' "${VERIFY_LOG}"
  exit 1
fi
assert_single_success_log "${VERIFY_LOG}" 'backup verified'
[[ "$(jq -er '.backupId' "${VERIFY_LOG}")" == "${BACKUP_ID}" ]] || fail 'verify backup ID changed'
[[ "$(jq -er '.manifestSHA256' "${VERIFY_LOG}")" == "${MANIFEST_SHA256}" ]] ||
  fail 'verify manifest digest changed'

[[ "$(stat -Lc '%a' -- "${BACKUP_ROOT}")" == "750" ]] || fail 'backup root mode is not 0750'
[[ "$(stat -Lc '%a' -- "${BACKUP_ROOT}/${BACKUP_ID}")" == "750" ]] || fail 'bundle mode is not 0750'
[[ "$(find "${BACKUP_ROOT}/${BACKUP_ID}" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort | tr '\n' ' ')" == \
  'artifacts.tar.zst database.dump manifest.json manifest.sha256 ' ]] || fail 'backup bundle entry set differs'
while IFS= read -r bundle_file; do
  [[ "$(stat -Lc '%a' -- "${bundle_file}")" == "640" ]] || fail 'backup bundle file mode is not 0640'
done < <(find "${BACKUP_ROOT}/${BACKUP_ID}" -mindepth 1 -maxdepth 1 -type f -print | sort)

operator_psql --command="CREATE DATABASE ${SCRATCH_DATABASE} WITH OWNER ${SCHEMA_OWNER} TEMPLATE template0 ENCODING 'UTF8' ALLOW_CONNECTIONS false" >/dev/null
owner_operator_psql --command="REVOKE ALL PRIVILEGES ON DATABASE ${SCRATCH_DATABASE} FROM PUBLIC" >/dev/null
owner_operator_psql --command="GRANT CONNECT ON DATABASE ${SCRATCH_DATABASE} TO ${RESTORE_LOGIN}" >/dev/null
owner_operator_psql --command="ALTER DATABASE ${SCRATCH_DATABASE} WITH ALLOW_CONNECTIONS true" >/dev/null
assert_scratch_contract

if ! run_with_private_runtime_root \
    "${RESTORE_RUNTIME_ROOT}" \
    "${RESTORE_RUNTIME_PATH}" \
    /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_BACKUP_ROOT="${BACKUP_ROOT}" \
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_tar_zstd \
    ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
    ASCENDANY_RESTORE_DATABASE_URL="postgresql://${RESTORE_LOGIN}@${DIRECT_HOST}:5432/${SCRATCH_DATABASE}?sslmode=disable" \
    ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE="${RESTORE_PASSWORD_FILE}" \
    ASCENDANY_RESTORE_ARTIFACT_ROOT="${RESTORE_ARTIFACT_ROOT}" \
    ASCENDANY_RESTORE_RUNTIME_ROOT="${RESTORE_RUNTIME_PATH}" \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    ASCENDANY_BACKUP_COMMAND_TIMEOUT=30m \
    ASCENDANY_PG_DUMP_PATH=/usr/bin/pg_dump \
    ASCENDANY_PG_RESTORE_PATH=/usr/bin/pg_restore \
    ASCENDANY_ZSTD_PATH=/usr/bin/zstd \
    "${BACKUP_BINARY}" restore-verify "${BACKUP_ID}" >/dev/null 2>"${RESTORE_LOG}"; then
  print_command_log_on_failure 'backup restore-verify' "${RESTORE_LOG}"
  exit 1
fi
assert_single_success_log "${RESTORE_LOG}" 'backup restore verified'
[[ -z "$(find "${RESTORE_RUNTIME_ROOT}" -mindepth 1 -maxdepth 1 -print)" ]] ||
  fail 'restore runtime root retained a private pgpass file'
[[ "$(jq -er '.databaseName' "${RESTORE_LOG}")" == "${SCRATCH_DATABASE}" ]] ||
  fail 'restore verifier reported an unexpected database'
[[ "$(jq -er '.backupId' "${RESTORE_LOG}")" == "${BACKUP_ID}" ]] || fail 'restore backup ID changed'
[[ "$(jq -er '.manifestSHA256' "${RESTORE_LOG}")" == "${MANIFEST_SHA256}" ]] ||
  fail 'restore manifest digest changed'

assert_scratch_contract
assert_restored_full_role_contract

readonly RESTORED_DATABASE_SUMMARY="$(admin_psql "${SCRATCH_DATABASE}" \
  --set=admin_public_id="${BOOTSTRAP_ADMIN_ID}" \
  --set=artifact_sha="${SOURCE_SHA256}" \
  --set=artifact_size="${SOURCE_SIZE}" \
  --set=artifact_key="${SOURCE_STORAGE_KEY}" \
  --tuples-only --no-align <<'SQL'
SELECT
    (SELECT count(*) FROM ascendany.schema_migrations_v2)::text || '|' ||
    (SELECT count(*) FROM ascendany.artifacts)::text || '|' ||
    (SELECT count(*)
     FROM ascendany.artifacts
     WHERE sha256 = :'artifact_sha'
       AND size_bytes = :'artifact_size'::bigint
       AND storage_key = :'artifact_key')::text || '|' ||
    (SELECT count(*)
     FROM ascendany.auth_accounts
     WHERE public_id = :'admin_public_id'::uuid
       AND username = 'admin'
       AND display_name = 'admin'
       AND role = 'admin'
       AND actor_id IS NULL
       AND student_number IS NULL
       AND auth_revision = 1
       AND disabled_at IS NULL
       AND password_phc LIKE '$argon2id$v=19$m=19456,t=2,p=1$%'
       AND created_at = updated_at)::text || '|' ||
    (SELECT count(*)
     FROM ascendany.audit_events AS audit
     JOIN ascendany.auth_accounts AS account ON account.account_id = audit.account_id
     WHERE account.public_id = :'admin_public_id'::uuid
       AND audit.event_type = 'auth.admin_bootstrap'
       AND audit.session_id IS NULL
       AND audit.payload = '{}'::jsonb
       AND audit.occurred_at = account.created_at)::text;
SQL
)"
[[ "${RESTORED_DATABASE_SUMMARY}" == "5|1|1|1|1" ]] ||
  fail 'restored migration, artifact, or administrator bootstrap state differs from the source manifest'
[[ -f "${RESTORE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}" &&
   ! -L "${RESTORE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}" ]] || fail 'restored artifact is unavailable'
[[ "$(stat -Lc '%a' -- "${RESTORE_ARTIFACT_ROOT}")" == "750" ]] || fail 'restored artifact root mode is not 0750'
[[ "$(stat -Lc '%a' -- "${RESTORE_ARTIFACT_ROOT}/sha256")" == "750" ]] || fail 'restored sha256 directory mode is not 0750'
[[ "$(stat -Lc '%a' -- "${RESTORE_ARTIFACT_ROOT}/sha256/${SOURCE_SHA256:0:2}")" == "750" ]] ||
  fail 'restored artifact prefix mode is not 0750'
[[ "$(stat -Lc '%a' -- "${RESTORE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}")" == "640" ]] ||
  fail 'restored artifact mode is not 0640'
[[ "$(stat -Lc '%s' -- "${RESTORE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}")" == "${SOURCE_SIZE}" ]] ||
  fail 'restored artifact size changed'
[[ "$(sha256sum -- "${RESTORE_ARTIFACT_ROOT}/${SOURCE_STORAGE_KEY}" | awk '{print $1}')" == "${SOURCE_SHA256}" ]] ||
  fail 'restored artifact digest changed'
[[ -z "$(find "${RESTORE_PARENT}" -mindepth 1 -maxdepth 1 \
  \( -name '.restore-*' -o -name '.restore-pgpass-*' \) -print)" ]] ||
  fail 'restore verifier left staging or private pgpass paths behind'

# Exercise the production operator's ownership path: the restore login must
# SET ROLE to the schema owner to disable and drop its scratch database.
owner_operator_psql --command="ALTER DATABASE ${SCRATCH_DATABASE} WITH ALLOW_CONNECTIONS false" >/dev/null
owner_operator_psql --command="DROP DATABASE ${SCRATCH_DATABASE} WITH (FORCE)" >/dev/null
rm -rf --one-file-system -- "${RESTORE_ARTIFACT_ROOT}" "${RESTORE_PARENT}/.restore-${BACKUP_ID}"
rm -f -- "${OPERATOR_PGPASS_FILE}" "${RUNTIME_PASSWORD_FILE}" \
  "${MIGRATOR_PASSWORD_FILE}" "${BACKUP_PASSWORD_FILE}" "${RESTORE_PASSWORD_FILE}" \
  "${PASSWORD_PEPPER_FILE}" "${BOOTSTRAP_ADMIN_PASSWORD_FILE}"
rmdir -- "${RESTORE_RUNTIME_ROOT}" "${OPERATOR_RUNTIME_ROOT}" "${RUNTIME_PARENT}"

[[ "$(admin_psql postgres --tuples-only --no-align --command="SELECT count(*) FROM pg_database WHERE datname = '${SCRATCH_DATABASE}'")" == "0" ]] ||
  fail 'scratch database remains after operator cleanup'
[[ -z "$(find "${RESTORE_PARENT}" -mindepth 1 -maxdepth 1 -print)" ]] ||
  fail 'restore operator paths remain after cleanup'
[[ -z "$(find "${CREDENTIAL_DIR}" -mindepth 1 -maxdepth 1 \
  ! -name 'postgres-password' ! -name 'postgres.pgpass' -print)" ]] ||
  fail 'non-admin rehearsal credentials remain after operator cleanup'
[[ ! -e "${RUNTIME_PARENT}" ]] || fail 'private backup runtime roots remain after cleanup'
SCRATCH_CLEANED=1
CREDENTIALS_CLEANED=1

# One final application proves repeated bootstrap closure still has authority
# after the source database owner has been isolated and after restore cleanup.
admin_psql "${SOURCE_DATABASE}" --file="${ROLE_BOOTSTRAP}" >/dev/null
admin_psql "${SOURCE_DATABASE}" --file="${ROLE_VERIFIER}" >/dev/null
readonly SOURCE_OWNER="$(admin_psql postgres --tuples-only --no-align --command="
SELECT owner.rolname
FROM pg_database AS database
JOIN pg_roles AS owner ON owner.oid = database.datdba
WHERE database.datname = '${SOURCE_DATABASE}'")"
[[ "${SOURCE_OWNER}" == "${DATABASE_OWNER}" ]] || fail 'source database owner isolation changed'

printf 'BACKUP_RESTORE_REHEARSAL_RESULT postgres_major=17 backup_commands=3 artifact_count=1 source_bytes=%s migrations=5 admin_bootstrap_exact=true second_admin_bootstrap_rejected=true restored_admin_bootstrap_exact=true scratch_owner=%s scratch_acl_exact=true restored_full_role_verifier=true xtrace_disabled_before_secrets=true repeated_role_bootstrap_verified=true scratch_cleanup=%s restore_credentials_removed=%s preexisting_containers=%s preexisting_pods=%s\n' \
  "${SOURCE_SIZE}" \
  "${SCHEMA_OWNER}" \
  "$([[ "${SCRATCH_CLEANED}" == "1" ]] && printf true || printf false)" \
  "$([[ "${CREDENTIALS_CLEANED}" == "1" ]] && printf true || printf false)" \
  "${PREEXISTING_CONTAINER_COUNT}" \
  "${PREEXISTING_POD_COUNT}"
