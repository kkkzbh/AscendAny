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
readonly SCRATCH_DATABASE="ascendany_v2_restore_verify"
readonly DATABASE_OWNER="ascendany_database_owner"
readonly SCHEMA_OWNER="ascendany_owner"
readonly MIGRATOR_LOGIN="ascendany_migrator_login"
readonly BACKUP_LOGIN="ascendany_backup_login"
readonly RESTORE_LOGIN="ascendany_restore_login"
readonly RUNTIME_LOGIN="ascendanyd_login"
readonly CATALOG_PUBLISHER_LOGIN="ascendany_catalog_publisher_login"
readonly BACKUP_RUNTIME_PATH="/run/ascendany-backup"

usage() {
  cat <<'EOF'
Usage:
  tools/run-v2-backup-restore-podman-rehearsal.sh \
    --confirm-reset drop-disposable-ascendany-v2-backup-restore \
    --recommendation-model /absolute/path/to/recommendation-model.json \
    --recommendation-model-sha256 64_lowercase_hex \
    --recommendation-catalog /absolute/path/to/recommendation-knowledge-catalog.json \
    --recommendation-catalog-sha256 64_lowercase_hex \
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
RECOMMENDATION_MODEL_PATH=""
RECOMMENDATION_MODEL_SHA256=""
RECOMMENDATION_CATALOG_PATH=""
RECOMMENDATION_CATALOG_SHA256=""
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
    --recommendation-model)
      (($# >= 2)) || fail '--recommendation-model requires a value'
      [[ -z "${RECOMMENDATION_MODEL_PATH}" ]] || fail '--recommendation-model may be specified only once'
      RECOMMENDATION_MODEL_PATH="$2"
      shift 2
      ;;
    --recommendation-model-sha256)
      (($# >= 2)) || fail '--recommendation-model-sha256 requires a value'
      [[ -z "${RECOMMENDATION_MODEL_SHA256}" ]] || fail '--recommendation-model-sha256 may be specified only once'
      RECOMMENDATION_MODEL_SHA256="$2"
      shift 2
      ;;
    --recommendation-catalog)
      (($# >= 2)) || fail '--recommendation-catalog requires a value'
      [[ -z "${RECOMMENDATION_CATALOG_PATH}" ]] || fail '--recommendation-catalog may be specified only once'
      RECOMMENDATION_CATALOG_PATH="$2"
      shift 2
      ;;
    --recommendation-catalog-sha256)
      (($# >= 2)) || fail '--recommendation-catalog-sha256 requires a value'
      [[ -z "${RECOMMENDATION_CATALOG_SHA256}" ]] || fail '--recommendation-catalog-sha256 may be specified only once'
      RECOMMENDATION_CATALOG_SHA256="$2"
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
[[ -n "${RECOMMENDATION_MODEL_PATH}" ]] || fail '--recommendation-model is required'
[[ "${RECOMMENDATION_MODEL_SHA256}" =~ ^[0-9a-f]{64}$ ]] ||
  fail '--recommendation-model-sha256 must be 64 lowercase hexadecimal characters'
[[ -n "${RECOMMENDATION_CATALOG_PATH}" ]] || fail '--recommendation-catalog is required'
[[ "${RECOMMENDATION_CATALOG_SHA256}" =~ ^[0-9a-f]{64}$ ]] ||
  fail '--recommendation-catalog-sha256 must be 64 lowercase hexadecimal characters'
[[ "${SNAPSHOT_PATH}" == /* && ! "${SNAPSHOT_PATH}" =~ [[:cntrl:]] ]] ||
  fail '--snapshot must be one absolute path without control characters'
[[ -f "${SNAPSHOT_PATH}" && ! -L "${SNAPSHOT_PATH}" ]] ||
  fail "the Pintia snapshot must be a regular non-symlink file: ${SNAPSHOT_PATH}"
[[ -r "${SNAPSHOT_PATH}" ]] || fail 'the Pintia snapshot is not readable'
[[ "${RECOMMENDATION_MODEL_PATH}" == /* && ! "${RECOMMENDATION_MODEL_PATH}" =~ [[:cntrl:]] ]] ||
  fail '--recommendation-model must be one absolute path without control characters'
[[ -f "${RECOMMENDATION_MODEL_PATH}" && ! -L "${RECOMMENDATION_MODEL_PATH}" && -r "${RECOMMENDATION_MODEL_PATH}" ]] ||
  fail '--recommendation-model must identify one readable regular non-symlink file'
[[ "${RECOMMENDATION_CATALOG_PATH}" == /* && ! "${RECOMMENDATION_CATALOG_PATH}" =~ [[:cntrl:]] ]] ||
  fail '--recommendation-catalog must be one absolute path without control characters'
[[ -f "${RECOMMENDATION_CATALOG_PATH}" && ! -L "${RECOMMENDATION_CATALOG_PATH}" && -r "${RECOMMENDATION_CATALOG_PATH}" ]] ||
  fail '--recommendation-catalog must identify one readable regular non-symlink file'
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
[[ "$(realpath -e -- "${RECOMMENDATION_MODEL_PATH}")" == "${RECOMMENDATION_MODEL_PATH}" ]] ||
  fail '--recommendation-model must already be canonical and have no symlink ancestry'
[[ "$(realpath -e -- "${RECOMMENDATION_CATALOG_PATH}")" == "${RECOMMENDATION_CATALOG_PATH}" ]] ||
  fail '--recommendation-catalog must already be canonical and have no symlink ancestry'
[[ "$(stat -Lc '%a:%h' -- "${RECOMMENDATION_MODEL_PATH}")" == "644:1" ]] ||
  fail '--recommendation-model must be one mode-0644 regular file with one hard link'
[[ "$(stat -Lc '%a:%h' -- "${RECOMMENDATION_CATALOG_PATH}")" == "644:1" ]] ||
  fail '--recommendation-catalog must be one mode-0644 regular file with one hard link'
[[ "$(sha256sum -- "${RECOMMENDATION_MODEL_PATH}" | awk '{print $1}')" == "${RECOMMENDATION_MODEL_SHA256}" ]] ||
  fail 'recommendation model bytes differ from --recommendation-model-sha256'
[[ "$(sha256sum -- "${RECOMMENDATION_CATALOG_PATH}" | awk '{print $1}')" == "${RECOMMENDATION_CATALOG_SHA256}" ]] ||
  fail 'recommendation catalog bytes differ from --recommendation-catalog-sha256'
[[ "$(jq -jSc . "${RECOMMENDATION_CATALOG_PATH}" | sha256sum | awk '{print $1}')" == "${RECOMMENDATION_CATALOG_SHA256}" ]] ||
  fail 'recommendation catalog must be one canonical JSON object'
[[ "$(jq -er '.manifest.knowledgeCatalogSha256' "${RECOMMENDATION_MODEL_PATH}")" == "${RECOMMENDATION_CATALOG_SHA256}" ]] ||
  fail 'recommendation catalog digest differs from the model manifest trust anchor'
readonly RECOMMENDATION_CATALOG_DOCUMENT="$(jq -jSc . "${RECOMMENDATION_CATALOG_PATH}")"
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
readonly REHEARSAL_DOMAIN_HASH="dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
readonly REHEARSAL_ANALYTICS_MANIFEST="$(jq -jcn --arg domainHash "${REHEARSAL_DOMAIN_HASH}" '{
  protocol: "analytics_input_manifest_v1",
  baseAnalyticsGenerationId: null,
  baseHeadRevision: 0,
  target: {examId: 1, snapshotId: 1, examHeadRevision: 1},
  snapshots: [{examId: 1, snapshotId: 1, domainHash: $domainHash}]
}')"
readonly REHEARSAL_ANALYTICS_MANIFEST_SHA256="$(printf '%s' "${REHEARSAL_ANALYTICS_MANIFEST}" | sha256sum | awk '{print $1}')"

readonly WORK_ROOT_PREFIX="${PRIVATE_RUNTIME_ROOT}/ascendany-v2-backup-restore."
WORK_ROOT=""
ADMIN_PASSWORD_FILE=""
ADMIN_PGPASS_FILE=""
RUNTIME_PASSWORD_FILE=""
RUNTIME_PGPASS_FILE=""
MIGRATOR_PASSWORD_FILE=""
BACKUP_PASSWORD_FILE=""
RESTORE_PASSWORD_FILE=""
CATALOG_PUBLISHER_PASSWORD_FILE=""
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
    "${RUNTIME_PASSWORD_FILE}" "${RUNTIME_PGPASS_FILE}" "${MIGRATOR_PASSWORD_FILE}" "${BACKUP_PASSWORD_FILE}" \
    "${RESTORE_PASSWORD_FILE}" "${CATALOG_PUBLISHER_PASSWORD_FILE}" \
    "${PASSWORD_PEPPER_FILE}" "${BOOTSTRAP_ADMIN_PASSWORD_FILE}"; do
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
readonly SOURCE_CATALOG_RECEIPT_ROOT="${WORK_ROOT}/source-catalog-receipts"
readonly SOURCE_CATALOG_RECEIPT_PATH="${SOURCE_CATALOG_RECEIPT_ROOT}/1.json"
readonly BACKUP_ROOT="${WORK_ROOT}/backups"
readonly RESTORE_PARENT="${WORK_ROOT}/restore"
readonly RESTORE_ARTIFACT_ROOT="${RESTORE_PARENT}/artifacts"
readonly RESTORE_CATALOG_RECEIPT_ROOT="${RESTORE_PARENT}/catalog-receipts"
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
readonly RUNTIME_PGPASS_FILE="${CREDENTIAL_DIR}/runtime.pgpass"
readonly MIGRATOR_PASSWORD_FILE="${CREDENTIAL_DIR}/migrator-password"
readonly BACKUP_PASSWORD_FILE="${CREDENTIAL_DIR}/backup-password"
readonly RESTORE_PASSWORD_FILE="${CREDENTIAL_DIR}/restore-password"
readonly CATALOG_PUBLISHER_PASSWORD_FILE="${CREDENTIAL_DIR}/catalog-publisher-password"
readonly PASSWORD_PEPPER_FILE="${CREDENTIAL_DIR}/password-pepper"
readonly ADMIN_BOOTSTRAP_CREDENTIAL_DIR="${CREDENTIAL_DIR}/admin-bootstrap"
readonly BOOTSTRAP_ADMIN_PASSWORD_FILE="${ADMIN_BOOTSTRAP_CREDENTIAL_DIR}/admin_password"
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
  "${ADMIN_BOOTSTRAP_CREDENTIAL_DIR}" \
  "${RESTORE_PARENT}" "${RUNTIME_PARENT}" "${BACKUP_RUNTIME_ROOT}" \
  "${OPERATOR_RUNTIME_ROOT}"
install -d -m 0750 -- \
  "${SOURCE_ARTIFACT_ROOT}" \
  "${SOURCE_ARTIFACT_ROOT}/sha256" \
  "${SOURCE_ARTIFACT_ROOT}/sha256/${SOURCE_SHA256:0:2}" \
  "${SOURCE_CATALOG_RECEIPT_ROOT}" \
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
readonly CATALOG_PUBLISHER_PASSWORD="$(openssl rand -hex 24)"
readonly PASSWORD_PEPPER="$(openssl rand -hex 32)"
readonly BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -hex 24)"

printf '%s' "${POSTGRES_ADMIN_PASSWORD}" >"${ADMIN_PASSWORD_FILE}"
printf '%s' "${RUNTIME_PASSWORD}" >"${RUNTIME_PASSWORD_FILE}"
printf '%s' "${MIGRATOR_PASSWORD}" >"${MIGRATOR_PASSWORD_FILE}"
printf '%s' "${BACKUP_PASSWORD}" >"${BACKUP_PASSWORD_FILE}"
printf '%s' "${RESTORE_PASSWORD}" >"${RESTORE_PASSWORD_FILE}"
printf '%s' "${CATALOG_PUBLISHER_PASSWORD}" >"${CATALOG_PUBLISHER_PASSWORD_FILE}"
printf '%s' "${PASSWORD_PEPPER}" >"${PASSWORD_PEPPER_FILE}"
printf '%s' "${BOOTSTRAP_ADMIN_PASSWORD}" >"${BOOTSTRAP_ADMIN_PASSWORD_FILE}"
printf '%s:5432:*:postgres:%s\n' "${DIRECT_HOST}" "${POSTGRES_ADMIN_PASSWORD}" >"${ADMIN_PGPASS_FILE}"
printf '%s:5432:%s:%s:%s\n' "${DIRECT_HOST}" "${SOURCE_DATABASE}" "${RUNTIME_LOGIN}" "${RUNTIME_PASSWORD}" >"${RUNTIME_PGPASS_FILE}"
printf '%s:5432:*:%s:%s\n' "${DIRECT_HOST}" "${RESTORE_LOGIN}" "${RESTORE_PASSWORD}" >"${OPERATOR_PGPASS_FILE}"
chmod 0600 -- "${ADMIN_PASSWORD_FILE}" "${ADMIN_PGPASS_FILE}" \
  "${RUNTIME_PASSWORD_FILE}" "${RUNTIME_PGPASS_FILE}" "${MIGRATOR_PASSWORD_FILE}" \
  "${BACKUP_PASSWORD_FILE}" "${RESTORE_PASSWORD_FILE}" "${CATALOG_PUBLISHER_PASSWORD_FILE}" \
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
    (if $message == "backup restore verified" then
      keys == [
        "artifactCount", "backupId", "catalogReceiptCount", "catalogReceiptRoot", "databaseName",
        "level", "manifestSHA256", "modelApplicationBuildTime", "modelApplicationCommit",
        "modelApplicationVersion", "modelArtifactSHA256", "modelFeatureSchemaSHA256",
        "modelHeadRevision", "modelId", "modelKnowledgeCatalogSHA256", "modelManifestSHA256",
        "modelPurpose", "msg", "releaseCommit", "releaseVersion", "time"
      ] and
      (.databaseName | type == "string" and length > 0) and
      (.catalogReceiptRoot | type == "string" and startswith("/"))
    else
      keys == [
        "artifactCount", "backupId", "catalogReceiptCount", "level", "manifestSHA256", "msg", "time"
      ]
    end) and
    (.backupId | type == "string" and test("^backup-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$")) and
    (.manifestSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
    .artifactCount == 1 and .catalogReceiptCount == 1
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
  # Exercise the same restore login and explicit owner role used by the Go
  # restore gate. The closed restore profile validates the scratch database in
  # place, so this assertion never mutates database identity or ACLs.
  owner_operator_psql \
    --dbname="${SCRATCH_DATABASE}" \
    --set=ascendany_verification_profile=restore \
    --file="${ROLE_VERIFIER}" >/dev/null
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
ALTER ROLE ${CATALOG_PUBLISHER_LOGIN} PASSWORD '${CATALOG_PUBLISHER_PASSWORD}';
SQL

if ! /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_DATABASE_URL="postgresql://${MIGRATOR_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
    ASCENDANY_DATABASE_PASSWORD_FILE="${MIGRATOR_PASSWORD_FILE}" \
    ASCENDANY_DATABASE_ROLE="${SCHEMA_OWNER}" \
    ASCENDANY_DATABASE_SCHEMA=ascendany \
    ASCENDANY_MIGRATION_HISTORY_TABLE=ascendany.schema_migrations_v2 \
    ASCENDANY_DATABASE_SCHEMA_VERSION=7 \
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

printf 'Binding the verified inference model through the production model release repository\n'
if ! (
  cd -- "${BACKEND_ROOT}"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    HOME="${HOME}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    LC_ALL=C \
    TZ=UTC \
    GOTOOLCHAIN=local \
    GOENV=off \
    GOWORK=off \
    GOFLAGS= \
    GOPROXY=off \
    PGPASSFILE="${RUNTIME_PGPASS_FILE}" \
    ASCENDANY_TEST_DATABASE_URL="postgresql://${RUNTIME_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
    ASCENDANY_TEST_RECOMMENDATION_MODEL_PATH="${RECOMMENDATION_MODEL_PATH}" \
    ASCENDANY_TEST_RECOMMENDATION_MODEL_SHA256="${RECOMMENDATION_MODEL_SHA256}" \
    go test -count=1 ./internal/modelrelease -run '^TestPostgresBindingPersistsVerifiedArtifact$'
) >"${LOG_DIR}/model-binding.log" 2>&1; then
  print_command_log_on_failure 'recommendation model binding' "${LOG_DIR}/model-binding.log"
  exit 1
fi

readonly SOURCE_MODEL_PROVENANCE="$(admin_psql "${SOURCE_DATABASE}" --tuples-only --no-align <<'SQL'
SELECT jsonb_build_object(
  'releases', (SELECT jsonb_agg(to_jsonb(release) ORDER BY release.recommendation_model_release_id)
               FROM ascendany.recommendation_model_releases AS release),
  'activations', (SELECT jsonb_agg(to_jsonb(event) ORDER BY event.head_revision)
                  FROM ascendany.recommendation_model_activation_events AS event),
  'head', (SELECT to_jsonb(head) FROM ascendany.recommendation_model_head AS head WHERE singleton)
)::text;
SQL
)"
[[ -n "${SOURCE_MODEL_PROVENANCE}" ]] || fail 'source model provenance is empty'

run_admin_bootstrap() {
  local stdout_path="$1"
  local stderr_path="$2"
  /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_DATABASE_URL="postgresql://${RUNTIME_LOGIN}@${DIRECT_HOST}:5432/${SOURCE_DATABASE}?sslmode=disable" \
    ASCENDANY_DATABASE_POOL_MODE=transaction \
    ASCENDANY_DATABASE_PASSWORD_FILE="${RUNTIME_PASSWORD_FILE}" \
    ASCENDANY_DATABASE_SCHEMA_VERSION=7 \
    ASCENDANY_DATABASE_MAX_CONNECTIONS=1 \
    ASCENDANY_DATABASE_MIN_CONNECTIONS=0 \
    ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    ASCENDANY_DATABASE_MAX_CONNECTION_LIFETIME=5m \
    ASCENDANY_DATABASE_MAX_CONNECTION_IDLE_TIME=1m \
    ASCENDANY_DATABASE_HEALTH_TIMEOUT=5s \
    ASCENDANY_PASSWORD_PEPPER_FILE="${PASSWORD_PEPPER_FILE}" \
    CREDENTIALS_DIRECTORY="${ADMIN_BOOTSTRAP_CREDENTIAL_DIR}" \
    "${ADMIN_BOOTSTRAP_BINARY}" create \
      --username admin \
      --display-name admin \
      >"${stdout_path}" 2>"${stderr_path}"
}

if ! run_admin_bootstrap "${ADMIN_BOOTSTRAP_RESULT}" "${ADMIN_BOOTSTRAP_ERROR}"; then
  fail 'administrator bootstrap command failed'
fi
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

# Seed one complete, immutable catalog publication graph. Backup v2 requires
# every database publication to have one canonical durable receipt, so this
# rehearsal exercises the same analytics, model, publication, and receipt
# provenance closure as production.
admin_psql "${SOURCE_DATABASE}" \
  --set=admin_public_id="${BOOTSTRAP_ADMIN_ID}" \
  --set=artifact_sha="${SOURCE_SHA256}" \
  --set=pintia_schema_sha="${PINTIA_SCHEMA_SHA256}" \
  --set=domain_hash="${REHEARSAL_DOMAIN_HASH}" \
  --set=analytics_manifest="${REHEARSAL_ANALYTICS_MANIFEST}" \
  --set=analytics_manifest_sha="${REHEARSAL_ANALYTICS_MANIFEST_SHA256}" \
  --set=catalog_sha="${RECOMMENDATION_CATALOG_SHA256}" \
  --set=catalog_document="${RECOMMENDATION_CATALOG_DOCUMENT}" >/dev/null <<'SQL'
BEGIN;

INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
SELECT '66666666-6666-4666-8666-666666666666'::uuid,
       account.account_id, account.auth_revision,
       clock_timestamp(), clock_timestamp() + interval '1 day', clock_timestamp()
FROM ascendany.auth_accounts AS account
WHERE account.public_id = :'admin_public_id'::uuid;

INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ('44444444-4444-4444-8444-444444444444'::uuid, 'pintia', 'backup-rehearsal');

INSERT INTO ascendany.import_jobs (public_id, artifact_id, job_kind, status, stage)
SELECT '22222222-2222-4222-8222-222222222222'::uuid,
       artifact.artifact_id, 'pintia_snapshot_v2', 'queued', 'received'
FROM ascendany.artifacts AS artifact
WHERE artifact.sha256 = :'artifact_sha';

UPDATE ascendany.import_jobs
SET status = 'running', stage = 'validating', attempt_count = 1,
    lease_owner = 'backup-rehearsal', lease_expires_at = clock_timestamp() + interval '1 hour',
    started_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE public_id = '22222222-2222-4222-8222-222222222222'::uuid;

UPDATE ascendany.import_jobs
SET stage = 'importing', updated_at = clock_timestamp()
WHERE public_id = '22222222-2222-4222-8222-222222222222'::uuid;

INSERT INTO ascendany.exam_snapshots (
    public_id, exam_id, snapshot_sequence, source_artifact_id, import_job_id,
    contract_schema, contract_schema_sha256, domain_hash_protocol, domain_hash,
    exporter_name, exporter_version, exported_at, title, source_url,
    problems_source_count, problems_observed_count, problems_exported_count,
    problems_pagination_exhausted, rankings_source_count, rankings_observed_count,
    rankings_exported_count, rankings_pagination_exhausted, submissions_source_count,
    submissions_observed_count, submissions_exported_count,
    submissions_pagination_exhausted, participants_exported_count
)
SELECT '33333333-3333-4333-8333-333333333333'::uuid,
       exam.exam_id, 1, artifact.artifact_id, job.import_job_id,
       'ascendany.pintia.snapshot.v2', :'pintia_schema_sha',
       'domain_hash_proto_v1', :'domain_hash',
       'ascendany-pintia-exporter', 'backup-rehearsal', clock_timestamp(),
       'Backup rehearsal', 'https://pintia.cn/problem-sets/backup-rehearsal',
       1, 1, 1, true, 0, 0, 0, true, 0, 0, 0, true, 0
FROM ascendany.logical_exams AS exam
JOIN ascendany.import_jobs AS job
  ON job.public_id = '22222222-2222-4222-8222-222222222222'::uuid
JOIN ascendany.artifacts AS artifact ON artifact.artifact_id = job.artifact_id
WHERE exam.public_id = '44444444-4444-4444-8444-444444444444'::uuid;

INSERT INTO ascendany.pintia_snapshot_problems (
    snapshot_id, problem_set_problem_id, problem_id, title, problem_type, max_score
)
SELECT snapshot.snapshot_id, 'problem-set-problem-1', 'problem-1',
       'Backup rehearsal problem', 'PROGRAMMING', 100
FROM ascendany.exam_snapshots AS snapshot
WHERE snapshot.public_id = '33333333-3333-4333-8333-333333333333'::uuid;

UPDATE ascendany.logical_exams AS exam
SET active_snapshot_id = snapshot.snapshot_id, head_revision = 1,
    updated_at = clock_timestamp()
FROM ascendany.exam_snapshots AS snapshot
WHERE exam.public_id = '44444444-4444-4444-8444-444444444444'::uuid
  AND snapshot.exam_id = exam.exam_id;

UPDATE ascendany.import_jobs AS job
SET stage = 'analyzing', lease_owner = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
FROM ascendany.exam_snapshots AS snapshot
WHERE job.public_id = '22222222-2222-4222-8222-222222222222'::uuid
  AND snapshot.import_job_id = job.import_job_id;

INSERT INTO ascendany.analytics_generations (
    status, base_analytics_generation_id, base_head_revision,
    target_exam_id, target_snapshot_id, target_exam_head_revision,
    input_manifest, input_manifest_sha256, algorithm_version, config_sha256
)
SELECT 'queued', NULL, 0, exam.exam_id, snapshot.snapshot_id, 1,
       :'analytics_manifest'::jsonb, :'analytics_manifest_sha',
       'backup_rehearsal_v1', repeat('c', 64)
FROM ascendany.logical_exams AS exam
JOIN ascendany.exam_snapshots AS snapshot ON snapshot.exam_id = exam.exam_id
WHERE exam.public_id = '44444444-4444-4444-8444-444444444444'::uuid
  AND snapshot.public_id = '33333333-3333-4333-8333-333333333333'::uuid;

INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id, exam_id, snapshot_id, domain_hash
)
SELECT generation.analytics_generation_id, snapshot.exam_id,
       snapshot.snapshot_id, snapshot.domain_hash
FROM ascendany.analytics_generations AS generation
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = generation.target_snapshot_id;

UPDATE ascendany.analytics_generations
SET status = 'running', attempt_count = 1, lease_owner = 'backup-rehearsal',
    lease_expires_at = clock_timestamp() + interval '1 hour', started_at = clock_timestamp()
WHERE input_manifest_sha256 = :'analytics_manifest_sha';

UPDATE ascendany.analytics_generations
SET status = 'succeeded', lease_owner = NULL, lease_expires_at = NULL,
    finished_at = clock_timestamp()
WHERE input_manifest_sha256 = :'analytics_manifest_sha';

UPDATE ascendany.analytics_head AS head
SET current_generation_id = generation.analytics_generation_id,
    head_revision = 1, updated_at = clock_timestamp()
FROM ascendany.analytics_generations AS generation
WHERE head.singleton AND head.current_generation_id IS NULL AND head.head_revision = 0
  AND generation.input_manifest_sha256 = :'analytics_manifest_sha';

UPDATE ascendany.import_jobs AS job
SET status = 'succeeded', stage = 'completed', snapshot_id = snapshot.snapshot_id,
    finished_at = clock_timestamp(), updated_at = clock_timestamp()
FROM ascendany.exam_snapshots AS snapshot
WHERE job.public_id = '22222222-2222-4222-8222-222222222222'::uuid
  AND job.status = 'running'
  AND job.stage = 'analyzing'
  AND job.snapshot_id IS NULL
  AND snapshot.import_job_id = job.import_job_id;

SELECT clock_timestamp() AS publication_moment \gset

WITH actor AS MATERIALIZED (
  SELECT account.account_id, account.auth_revision, session.session_id, session.expires_at
  FROM ascendany.auth_accounts AS account
  JOIN ascendany.auth_sessions AS session ON session.account_id = account.account_id
  WHERE account.public_id = :'admin_public_id'::uuid
    AND session.public_id = '66666666-6666-4666-8666-666666666666'::uuid
), model AS MATERIALIZED (
  SELECT release.recommendation_model_release_id, release.model_id,
         release.artifact_sha256, release.knowledge_catalog_sha256,
         head.head_revision, activation.application_version,
         activation.application_commit, activation.application_build_time
  FROM ascendany.recommendation_model_head AS head
  JOIN ascendany.recommendation_model_releases AS release
    ON release.recommendation_model_release_id = head.current_release_id
  JOIN ascendany.recommendation_model_activation_events AS activation
    ON activation.head_revision = head.head_revision
   AND activation.recommendation_model_release_id = head.current_release_id
  WHERE head.singleton
), analytics AS MATERIALIZED (
  SELECT head.current_generation_id, head.head_revision,
         generation.input_manifest_sha256
  FROM ascendany.analytics_head AS head
  JOIN ascendany.analytics_generations AS generation
    ON generation.analytics_generation_id = head.current_generation_id
  WHERE head.singleton
)
INSERT INTO ascendany.knowledge_catalog_publication_authorizations (
    public_id, access_jwt_id, access_token_sha256, request_canonical_json,
    configuration_public_id, expected_configuration_head_revision,
    expected_analytics_generation_id, expected_analytics_head_revision,
    expected_input_manifest_sha256, expected_current_model_head_revision,
    expected_current_model_artifact_sha256, catalog_schema_id, catalog_document,
    catalog_sha256, target_model_release_id, target_model_id,
    target_model_artifact_sha256, target_application_version,
    target_application_commit, target_application_build_time,
    authorized_account_id, authorized_session_id, authorized_auth_revision,
    access_token_expires_at, authorized_at
)
SELECT '77777777-7777-4777-8777-777777777777'::uuid,
       '88888888-8888-4888-8888-888888888888'::uuid, repeat('f', 64),
       jsonb_build_object(
         'schema', 'ascendany.knowledge_catalog.publication-request.v1',
         'authorizationId', '77777777-7777-4777-8777-777777777777',
         'expectedConfigurationHeadRevision', 0,
         'expectedAnalyticsGenerationId', analytics.current_generation_id::text,
         'expectedAnalyticsHeadRevision', analytics.head_revision,
         'expectedInputManifestSha256', analytics.input_manifest_sha256,
         'expectedCurrentModelHeadRevision', model.head_revision,
         'expectedCurrentModelArtifactSha256', model.artifact_sha256,
         'targetCatalogSha256', model.knowledge_catalog_sha256,
         'targetModelArtifactSha256', model.artifact_sha256,
         'targetApplicationVersion', model.application_version,
         'targetApplicationCommit', model.application_commit,
         'targetApplicationBuildTime', model.application_build_time
       )::text,
       '55555555-5555-4555-8555-555555555555'::uuid, 0,
       analytics.current_generation_id, analytics.head_revision,
       analytics.input_manifest_sha256, model.head_revision, model.artifact_sha256,
       'ascendany.knowledge_catalog.recommendation.v1', :'catalog_document'::jsonb,
       model.knowledge_catalog_sha256, model.recommendation_model_release_id,
       model.model_id, model.artifact_sha256, model.application_version,
       model.application_commit, model.application_build_time,
       actor.account_id, actor.session_id, actor.auth_revision, actor.expires_at,
       :'publication_moment'::timestamptz - interval '1 second'
FROM actor CROSS JOIN model CROSS JOIN analytics;

INSERT INTO ascendany.configuration_items (
    public_id, configuration_key, configuration_kind, created_at, updated_at
)
VALUES (
    '55555555-5555-4555-8555-555555555555'::uuid,
    'recommendation.catalog.active', 'knowledge_catalog',
    :'publication_moment'::timestamptz, :'publication_moment'::timestamptz
);

INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, credential_ref,
    created_by_account_id, created_by_role, created_by_session_id, created_at
)
SELECT item.configuration_item_id, item.configuration_kind, 1,
       'ascendany.knowledge_catalog.recommendation.v1',
       :'catalog_document'::jsonb, :'catalog_sha', NULL,
       account.account_id, 'admin', session.session_id, :'publication_moment'::timestamptz
FROM ascendany.configuration_items AS item
JOIN ascendany.auth_accounts AS account ON account.public_id = :'admin_public_id'::uuid
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
 AND session.public_id = '66666666-6666-4666-8666-666666666666'::uuid
WHERE item.configuration_key = 'recommendation.catalog.active';

UPDATE ascendany.configuration_items AS item
SET active_version_id = version.configuration_version_id,
    head_revision = 1, updated_at = :'publication_moment'::timestamptz
FROM ascendany.configuration_versions AS version
WHERE item.configuration_key = 'recommendation.catalog.active'
  AND version.configuration_item_id = item.configuration_item_id;

WITH actor AS MATERIALIZED (
  SELECT account.account_id, session.session_id
  FROM ascendany.auth_accounts AS account
  JOIN ascendany.auth_sessions AS session ON session.account_id = account.account_id
  WHERE account.public_id = :'admin_public_id'::uuid
    AND session.public_id = '66666666-6666-4666-8666-666666666666'::uuid
), model AS MATERIALIZED (
  SELECT release.recommendation_model_release_id, release.model_id,
         release.artifact_sha256, release.knowledge_catalog_sha256,
         head.head_revision, activation.application_version,
         activation.application_commit, activation.application_build_time
  FROM ascendany.recommendation_model_head AS head
  JOIN ascendany.recommendation_model_releases AS release
    ON release.recommendation_model_release_id = head.current_release_id
  JOIN ascendany.recommendation_model_activation_events AS activation
    ON activation.head_revision = head.head_revision
   AND activation.recommendation_model_release_id = head.current_release_id
  WHERE head.singleton
), analytics AS MATERIALIZED (
  SELECT head.current_generation_id, head.head_revision,
         generation.input_manifest_sha256
  FROM ascendany.analytics_head AS head
  JOIN ascendany.analytics_generations AS generation
    ON generation.analytics_generation_id = head.current_generation_id
  WHERE head.singleton
), configuration AS MATERIALIZED (
  SELECT item.configuration_item_id, item.active_version_id
  FROM ascendany.configuration_items AS item
  WHERE item.configuration_key = 'recommendation.catalog.active'
), publication_audit AS (
  INSERT INTO ascendany.audit_events (account_id, session_id, event_type, occurred_at, payload)
  SELECT actor.account_id, actor.session_id,
         'admin.configuration_version_created', :'publication_moment'::timestamptz,
         jsonb_build_object(
           'authorizationId', '77777777-7777-4777-8777-777777777777',
           'configurationId', '55555555-5555-4555-8555-555555555555',
           'key', 'recommendation.catalog.active',
           'kind', 'knowledge_catalog',
           'versionNumber', 1,
           'schemaId', 'ascendany.knowledge_catalog.recommendation.v1',
           'documentSha256', model.knowledge_catalog_sha256,
           'headRevision', 1,
           'credentialRef', NULL,
           'analyticsGenerationId', analytics.current_generation_id::text,
           'analyticsHeadRevision', analytics.head_revision,
           'inputManifestSha256', analytics.input_manifest_sha256,
           'currentModelHeadRevision', model.head_revision,
           'currentModelArtifactSha256', model.artifact_sha256,
           'targetCatalogSha256', model.knowledge_catalog_sha256,
           'targetModelId', model.model_id::text,
           'targetModelArtifactSha256', model.artifact_sha256,
           'targetModelReleaseId', model.recommendation_model_release_id::text,
           'targetApplicationVersion', model.application_version,
           'targetApplicationCommit', model.application_commit,
           'targetApplicationBuildTime', model.application_build_time,
           'expectedConfigurationHeadRevision', 0,
           'configurationMutated', true
         )
  FROM actor CROSS JOIN model CROSS JOIN analytics
  RETURNING audit_event_id, occurred_at
)
INSERT INTO ascendany.knowledge_catalog_publications (
    publication_authorization_id, configuration_item_id, configuration_version_id,
    expected_configuration_head_revision, configuration_head_revision,
    configuration_mutated, catalog_sha256, target_model_release_id,
    target_model_id, target_model_artifact_sha256,
    target_application_version, target_application_commit, target_application_build_time,
    analytics_generation_id, analytics_head_revision, input_manifest_sha256,
    current_model_head_revision, current_model_artifact_sha256,
    published_by_account_id, published_by_session_id, published_at, audit_event_id
)
SELECT '77777777-7777-4777-8777-777777777777'::uuid,
       configuration.configuration_item_id, configuration.active_version_id,
       0, 1, true, model.knowledge_catalog_sha256,
       model.recommendation_model_release_id, model.model_id, model.artifact_sha256,
       model.application_version, model.application_commit, model.application_build_time,
       analytics.current_generation_id, analytics.head_revision, analytics.input_manifest_sha256,
       model.head_revision, model.artifact_sha256,
       actor.account_id, actor.session_id, :'publication_moment'::timestamptz,
       publication_audit.audit_event_id
FROM actor CROSS JOIN model CROSS JOIN analytics
CROSS JOIN configuration CROSS JOIN publication_audit;

UPDATE ascendany.knowledge_catalog_publication_authorizations AS capability
SET consumed_publication_id = publication.knowledge_catalog_publication_id,
    consumed_at = publication.published_at
FROM ascendany.knowledge_catalog_publications AS publication
WHERE capability.public_id = '77777777-7777-4777-8777-777777777777'::uuid
  AND publication.publication_authorization_id = capability.public_id;

COMMIT;
SQL

readonly SOURCE_CATALOG_RECEIPT_JSON="$(admin_psql "${SOURCE_DATABASE}" --tuples-only --no-align <<'SQL'
SELECT jsonb_build_object(
  'schema', 'ascendany.knowledge_catalog.publication-receipt.v1',
  'authorizationId', publication.publication_authorization_id::text,
  'knowledgeCatalogPublicationId', publication.knowledge_catalog_publication_id::text,
  'targetModelReleaseId', publication.target_model_release_id::text,
  'catalogSha256', publication.catalog_sha256,
  'modelArtifactSha256', publication.target_model_artifact_sha256,
  'modelId', publication.target_model_id::text,
  'targetApplicationVersion', publication.target_application_version,
  'targetApplicationCommit', publication.target_application_commit,
  'targetApplicationBuildTime', publication.target_application_build_time,
  'configurationKey', item.configuration_key,
  'configurationId', item.public_id::text,
  'expectedConfigurationHeadRevision', publication.expected_configuration_head_revision,
  'configurationHeadRevision', publication.configuration_head_revision,
  'configurationVersionId', publication.configuration_version_id::text,
  'configurationVersionNumber', version.version_number,
  'analyticsGenerationId', publication.analytics_generation_id::text,
  'analyticsHeadRevision', publication.analytics_head_revision,
  'inputManifestSha256', publication.input_manifest_sha256,
  'currentModelHeadRevision', publication.current_model_head_revision,
  'currentModelArtifactSha256', publication.current_model_artifact_sha256,
  'publishedByAccountId', account.public_id::text,
  'publishedBySessionId', session.public_id::text,
  'publishedAt', to_char(publication.published_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS') ||
    CASE
      WHEN rtrim(to_char(publication.published_at AT TIME ZONE 'UTC', 'US'), '0') = '' THEN ''
      ELSE '.' || rtrim(to_char(publication.published_at AT TIME ZONE 'UTC', 'US'), '0')
    END || 'Z',
  'auditEventId', publication.audit_event_id::text,
  'configurationMutated', publication.configuration_mutated
)::text
FROM ascendany.knowledge_catalog_publications AS publication
JOIN ascendany.knowledge_catalog_publication_authorizations AS capability
  ON capability.public_id = publication.publication_authorization_id
 AND capability.consumed_publication_id = publication.knowledge_catalog_publication_id
 AND capability.consumed_at = publication.published_at
JOIN ascendany.configuration_items AS item
  ON item.configuration_item_id = publication.configuration_item_id
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = publication.configuration_version_id
JOIN ascendany.auth_accounts AS account
  ON account.account_id = publication.published_by_account_id
JOIN ascendany.auth_sessions AS session
  ON session.session_id = publication.published_by_session_id;
SQL
)"
printf '%s' "${SOURCE_CATALOG_RECEIPT_JSON}" | jq -jSc . >"${SOURCE_CATALOG_RECEIPT_PATH}"
chmod 0640 -- "${SOURCE_CATALOG_RECEIPT_PATH}"
[[ "$(find "${SOURCE_CATALOG_RECEIPT_ROOT}" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C sort)" == '1.json|f' ]] ||
  fail 'source catalog receipt entry set differs'
[[ "$(stat -Lc '%a:%h' -- "${SOURCE_CATALOG_RECEIPT_PATH}")" == '640:1' ]] ||
  fail 'source catalog receipt mode/link count differs'
jq -e '
  type == "object" and
  (keys == [
    "analyticsGenerationId",
    "analyticsHeadRevision",
    "auditEventId",
    "authorizationId",
    "catalogSha256",
    "configurationHeadRevision",
    "configurationId",
    "configurationKey",
    "configurationMutated",
    "configurationVersionId",
    "configurationVersionNumber",
    "currentModelArtifactSha256",
    "currentModelHeadRevision",
    "expectedConfigurationHeadRevision",
    "inputManifestSha256",
    "knowledgeCatalogPublicationId",
    "modelArtifactSha256",
    "modelId",
    "publishedAt",
    "publishedByAccountId",
    "publishedBySessionId",
    "schema",
    "targetApplicationBuildTime",
    "targetApplicationCommit",
    "targetApplicationVersion",
    "targetModelReleaseId"
  ]) and
  .schema == "ascendany.knowledge_catalog.publication-receipt.v1" and
  .authorizationId == "77777777-7777-4777-8777-777777777777" and
  .knowledgeCatalogPublicationId == "1"
' "${SOURCE_CATALOG_RECEIPT_PATH}" >/dev/null ||
  fail 'source catalog receipt identity differs from the seeded publication'

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
    ASCENDANY_CATALOG_RECEIPT_ROOT="${SOURCE_CATALOG_RECEIPT_ROOT}" \
    ASCENDANY_BACKUP_ROOT="${BACKUP_ROOT}" \
    ASCENDANY_BACKUP_RUNTIME_ROOT="${BACKUP_RUNTIME_PATH}" \
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_and_catalog_receipt_tar_zstd \
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
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_and_catalog_receipt_tar_zstd \
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
  'artifacts.tar.zst catalog-receipts.tar.zst database.dump manifest.json manifest.sha256 ' ]] || fail 'backup bundle entry set differs'
while IFS= read -r bundle_file; do
  [[ "$(stat -Lc '%a' -- "${bundle_file}")" == "640" ]] || fail 'backup bundle file mode is not 0640'
done < <(find "${BACKUP_ROOT}/${BACKUP_ID}" -mindepth 1 -maxdepth 1 -type f -print | sort)
jq -e '
  .schema == "ascendany.backup.bundle.v2" and
  (.catalogPublicationReceipts | type == "object" and
    .count == 1 and (.entries | length == 1) and
    .entries[0].publicationId == "1" and .entries[0].path == "1.json" and
    .entries[0].mode == 416 and
    .file.filename == "catalog-receipts.tar.zst" and .file.format == "tar+zstd") and
  .database.knowledgeCatalogPublicationIds == ["1"] and
  (.database.knowledgeCatalogPublications | length == 1) and
  .database.knowledgeCatalogPublications[0].knowledgeCatalogPublicationId == "1" and
  .database.knowledgeCatalogPublications[0].authorizationId ==
    "77777777-7777-4777-8777-777777777777"
' "${BACKUP_ROOT}/${BACKUP_ID}/manifest.json" >/dev/null ||
  fail 'backup manifest catalog publication receipt closure differs'

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
    ASCENDANY_BACKUP_FORMAT=pg_custom_plus_artifact_and_catalog_receipt_tar_zstd \
    ASCENDANY_BACKUP_MANIFEST_HASH=sha256 \
    ASCENDANY_RESTORE_DATABASE_URL="postgresql://${RESTORE_LOGIN}@${DIRECT_HOST}:5432/${SCRATCH_DATABASE}?sslmode=disable" \
    ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE="${RESTORE_PASSWORD_FILE}" \
    ASCENDANY_RESTORE_ARTIFACT_ROOT="${RESTORE_ARTIFACT_ROOT}" \
    ASCENDANY_RESTORE_CATALOG_RECEIPT_ROOT="${RESTORE_CATALOG_RECEIPT_ROOT}" \
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
[[ "$(jq -er '.catalogReceiptRoot' "${RESTORE_LOG}")" == "${RESTORE_CATALOG_RECEIPT_ROOT}" ]] ||
  fail 'restore verifier reported an unexpected catalog receipt root'

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
       AND audit.occurred_at = account.created_at)::text || '|' ||
    (SELECT count(*) FROM ascendany.recommendation_model_releases)::text || '|' ||
    (SELECT count(*) FROM ascendany.recommendation_model_activation_events)::text || '|' ||
    (SELECT count(*) FROM ascendany.recommendation_model_head WHERE singleton)::text || '|' ||
    (SELECT count(*)
     FROM ascendany.knowledge_catalog_publication_authorizations AS capability
     JOIN ascendany.knowledge_catalog_publications AS publication
       ON publication.publication_authorization_id = capability.public_id
      AND capability.consumed_publication_id = publication.knowledge_catalog_publication_id
      AND capability.consumed_at = publication.published_at)::text || '|' ||
    (SELECT count(*) FROM ascendany.knowledge_catalog_publications)::text;
SQL
)"
[[ "${RESTORED_DATABASE_SUMMARY}" == "7|1|1|1|1|1|1|1|1|1" ]] ||
  fail 'restored migration, artifact, administrator, model, authorization, or publication state differs from the source manifest'
readonly RESTORED_MODEL_PROVENANCE="$(admin_psql "${SCRATCH_DATABASE}" --tuples-only --no-align <<'SQL'
SELECT jsonb_build_object(
  'releases', (SELECT jsonb_agg(to_jsonb(release) ORDER BY release.recommendation_model_release_id)
               FROM ascendany.recommendation_model_releases AS release),
  'activations', (SELECT jsonb_agg(to_jsonb(event) ORDER BY event.head_revision)
                  FROM ascendany.recommendation_model_activation_events AS event),
  'head', (SELECT to_jsonb(head) FROM ascendany.recommendation_model_head AS head WHERE singleton)
)::text;
SQL
)"
[[ "${RESTORED_MODEL_PROVENANCE}" == "${SOURCE_MODEL_PROVENANCE}" ]] ||
  fail 'restored immutable model provenance differs from the source database'
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
[[ -d "${RESTORE_CATALOG_RECEIPT_ROOT}" && ! -L "${RESTORE_CATALOG_RECEIPT_ROOT}" &&
   "$(stat -Lc '%a' -- "${RESTORE_CATALOG_RECEIPT_ROOT}")" == "750" ]] ||
  fail 'restored catalog receipt root mode is not 0750'
[[ "$(find "${RESTORE_CATALOG_RECEIPT_ROOT}" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C sort)" == '1.json|f' ]] ||
  fail 'restored catalog receipt entry set differs'
[[ "$(stat -Lc '%a:%h' -- "${RESTORE_CATALOG_RECEIPT_ROOT}/1.json")" == "640:1" ]] ||
  fail 'restored catalog receipt mode/link count differs'
cmp --silent -- "${SOURCE_CATALOG_RECEIPT_PATH}" "${RESTORE_CATALOG_RECEIPT_ROOT}/1.json" ||
  fail 'restored catalog receipt bytes differ from the source publication receipt'
[[ -z "$(find "${RESTORE_PARENT}" -mindepth 1 -maxdepth 1 \
  \( -name '.restore-*' -o -name '.restore-pgpass-*' \) -print)" ]] ||
  fail 'restore verifier left staging or private pgpass paths behind'

# Exercise the production operator's ownership path: the restore login must
# SET ROLE to the schema owner to disable and drop its scratch database.
owner_operator_psql --command="ALTER DATABASE ${SCRATCH_DATABASE} WITH ALLOW_CONNECTIONS false" >/dev/null
owner_operator_psql --command="DROP DATABASE ${SCRATCH_DATABASE} WITH (FORCE)" >/dev/null
rm -rf --one-file-system -- \
  "${RESTORE_ARTIFACT_ROOT}" \
  "${RESTORE_CATALOG_RECEIPT_ROOT}" \
  "${RESTORE_PARENT}/.restore-${BACKUP_ID}" \
  "${RESTORE_PARENT}/.restore-catalog-receipts-${BACKUP_ID}"
rm -f -- "${OPERATOR_PGPASS_FILE}" "${RUNTIME_PASSWORD_FILE}" "${RUNTIME_PGPASS_FILE}" \
  "${MIGRATOR_PASSWORD_FILE}" "${BACKUP_PASSWORD_FILE}" "${RESTORE_PASSWORD_FILE}" \
  "${CATALOG_PUBLISHER_PASSWORD_FILE}" "${PASSWORD_PEPPER_FILE}" "${BOOTSTRAP_ADMIN_PASSWORD_FILE}"
rmdir -- "${ADMIN_BOOTSTRAP_CREDENTIAL_DIR}"
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

printf 'BACKUP_RESTORE_REHEARSAL_RESULT postgres_major=17 backup_commands=3 artifact_count=1 catalog_publication_count=1 catalog_receipt_count=1 bundle_files=5 source_bytes=%s migrations=7 model_releases=1 model_activations=1 model_head_exact=true model_provenance_exact=true admin_bootstrap_exact=true second_admin_bootstrap_rejected=true restored_admin_bootstrap_exact=true scratch_owner=%s scratch_acl_exact=true restored_full_role_verifier=true xtrace_disabled_before_secrets=true repeated_role_bootstrap_verified=true scratch_cleanup=%s restore_credentials_removed=%s preexisting_containers=%s preexisting_pods=%s\n' \
  "${SOURCE_SIZE}" \
  "${SCHEMA_OWNER}" \
  "$([[ "${SCRATCH_CLEANED}" == "1" ]] && printf true || printf false)" \
  "$([[ "${CREDENTIALS_CLEANED}" == "1" ]] && printf true || printf false)" \
  "${PREEXISTING_CONTAINER_COUNT}" \
  "${PREEXISTING_POD_COUNT}"
