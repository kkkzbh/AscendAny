#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPOSITORY_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
readonly INTEGRATION_RUNNER="${SCRIPT_DIR}/run-v2-postgres-integration.sh"
readonly RESET_CONFIRMATION="drop-disposable-ascendany-v2"
readonly DEFAULT_DIRECT_PORT="55432"
readonly DEFAULT_PGBOUNCER_PORT="56432"
readonly DEFAULT_SNAPSHOT_PATH="${REPOSITORY_ROOT}/contracts/pintia/fixtures/valid/complete.json"
readonly DEFAULT_POSTGRES_IMAGE="docker.io/library/postgres@sha256:030da09481c3876b71a7e49738a932e1c18c398201a1e4ccfdbff1e5a541215b"
readonly FEDORA_PACKAGE_LOCK="${REPOSITORY_ROOT}/deploy/v2/config/fedora-runtime-packages.json"
readonly RELEASE_PGBOUNCER_CONFIG="${REPOSITORY_ROOT}/deploy/v2/config/pgbouncer.ini"
readonly RELEASE_PGBOUNCER_HBA="${REPOSITORY_ROOT}/deploy/v2/config/pgbouncer-hba.conf"
readonly PGBOUNCER_BINARY="/usr/bin/pgbouncer"
readonly EXPECTED_MANIFEST_TESTS="23"
readonly LABEL_KEY="io.ascendany.v2-postgres-rehearsal"

usage() {
  cat <<'EOF'
Usage:
  tools/run-v2-postgres-podman-rehearsal.sh \
    --confirm-reset drop-disposable-ascendany-v2 \
    [--snapshot /absolute/path/to/ascendany-pintia-snapshot.json] \
    [--direct-port 55432] \
    [--pgbouncer-port 56432]

The default snapshot is the sanitized complete fixture. The wrapper creates one
randomly named rootless Podman pod containing PostgreSQL 17, starts the exact
release-locked Fedora 44 PgBouncer 1.25.2 binary as a native child process,
binds both services to loopback only, executes all env-gated integration tests,
and removes only its labeled pod and temporary credential directory on exit.
The private work root is created only inside a canonical, mode-0700, user-owned
XDG_RUNTIME_DIR backed by tmpfs.

Optional image overrides:
  ASCENDANY_REHEARSAL_POSTGRES_IMAGE
EOF
}

fail() {
  printf '%s\n' "$1" >&2
  exit 2
}

validate_port() {
  local option_name="$1"
  local value="$2"
  if [[ ! "${value}" =~ ^[0-9]+$ ]] || (( ${#value} > 5 )) ||
    (( 10#${value} < 1024 || 10#${value} > 65535 )); then
    fail "${option_name} must be an unprivileged decimal TCP port between 1024 and 65535"
  fi
  printf '%d' "$((10#${value}))"
}

DIRECT_PORT="${DEFAULT_DIRECT_PORT}"
PGBOUNCER_PORT="${DEFAULT_PGBOUNCER_PORT}"
SNAPSHOT_PATH="${DEFAULT_SNAPSHOT_PATH}"
CONFIRMATION=""

while (($# > 0)); do
  case "$1" in
    --confirm-reset)
      (($# >= 2)) || fail '--confirm-reset requires a value'
      CONFIRMATION="$2"
      shift 2
      ;;
    --snapshot)
      (($# >= 2)) || fail '--snapshot requires an absolute file path'
      SNAPSHOT_PATH="$2"
      shift 2
      ;;
    --direct-port)
      (($# >= 2)) || fail '--direct-port requires a value'
      DIRECT_PORT="$2"
      shift 2
      ;;
    --pgbouncer-port)
      (($# >= 2)) || fail '--pgbouncer-port requires a value'
      PGBOUNCER_PORT="$2"
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

if [[ "${CONFIRMATION}" != "${RESET_CONFIRMATION}" ]]; then
  fail "--confirm-reset must equal ${RESET_CONFIRMATION}"
fi
if ((EUID == 0)); then
  fail 'the PostgreSQL rehearsal must run as a rootless user'
fi
if [[ "${SNAPSHOT_PATH}" != /* || ! -f "${SNAPSHOT_PATH}" ]]; then
  fail "--snapshot must identify an absolute regular file: ${SNAPSHOT_PATH}"
fi

DIRECT_PORT="$(validate_port --direct-port "${DIRECT_PORT}")"
PGBOUNCER_PORT="$(validate_port --pgbouncer-port "${PGBOUNCER_PORT}")"
if [[ "${DIRECT_PORT}" == "${PGBOUNCER_PORT}" ]]; then
  fail '--direct-port and --pgbouncer-port must differ'
fi

for command_name in awk cat chmod cmp go grep install jq mktemp openssl pg_isready podman psql \
  realpath rg rm sed sha256sum sort ss stat tee tr uname wc; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    fail "required command is unavailable: ${command_name}"
  fi
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

if [[ "$(podman info --format '{{.Host.Security.Rootless}}')" != "true" ]]; then
  fail 'Podman is not operating in rootless mode'
fi

port_listener() {
  local port="$1"
  ss -H -ltn "sport = :${port}"
}

endpoint_listener() {
  local host="$1"
  local port="$2"
  ss -H -ltn "src ${host} and sport = :${port}"
}

if [[ -n "$(port_listener "${DIRECT_PORT}")" ]]; then
  fail "loopback direct port is already in use: ${DIRECT_PORT}"
fi
if [[ -n "$(port_listener "${PGBOUNCER_PORT}")" ]]; then
  fail "loopback PgBouncer port is already in use: ${PGBOUNCER_PORT}"
fi

readonly POSTGRES_IMAGE="${ASCENDANY_REHEARSAL_POSTGRES_IMAGE:-${DEFAULT_POSTGRES_IMAGE}}"
if ! podman image exists "${POSTGRES_IMAGE}"; then
  fail "PostgreSQL rehearsal image is unavailable; pull it explicitly: ${POSTGRES_IMAGE}"
fi

umask 077
readonly WORK_ROOT_PREFIX="${PRIVATE_RUNTIME_ROOT}/ascendany-v2-postgres-podman."
WORK_ROOT=""
RUN_LOG=""
BEFORE_CONTAINERS=""
BEFORE_PODS=""
AFTER_CONTAINERS=""
AFTER_PODS=""
POSTGRES_PASSWORD_FILE=""
PGBOUNCER_CONFIG_DIR=""
PGBOUNCER_CONFIG_FILE=""
PGBOUNCER_HBA_FILE=""
PGBOUNCER_USERLIST_FILE=""
TOKEN=""
POD_NAME=""
INFRA_CONTAINER_NAME=""
POSTGRES_CONTAINER_NAME=""
PGBOUNCER_PID=""
MIGRATOR_LOOPBACK_HOST=""
LABEL_VALUE=""
POD_CREATE_ATTEMPTED=0
BASELINE_CAPTURED=0

cleanup() {
  local original_status=$?
  local cleanup_status=0
  local labeled_containers=""
  local labeled_pods=""
  local labeled_container_count=0
  local labeled_pod_count=0
  local pod_exists_status=1
  local pod_label=""
  local identities_unchanged=true
  trap - EXIT INT TERM
  set +e

  if [[ -n "${PGBOUNCER_PID}" ]] && kill -0 "${PGBOUNCER_PID}" 2>/dev/null; then
    kill -TERM "${PGBOUNCER_PID}"
    wait "${PGBOUNCER_PID}" 2>/dev/null || cleanup_status=1
  fi

  if ((POD_CREATE_ATTEMPTED == 1)) && [[ -n "${POD_NAME}" ]]; then
    podman pod exists "${POD_NAME}"
    pod_exists_status=$?
    if ((pod_exists_status == 0)); then
      if ! pod_label="$(podman pod inspect \
        --format "{{ index .Labels \"${LABEL_KEY}\" }}" "${POD_NAME}")"; then
        printf 'failed to inspect rehearsal pod ownership: %s\n' "${POD_NAME}" >&2
        cleanup_status=1
      elif [[ "${pod_label}" != "${LABEL_VALUE}" ]]; then
        printf 'refusing to remove a rehearsal pod without the exact ownership label: %s\n' \
          "${POD_NAME}" >&2
        cleanup_status=1
      elif ! podman pod rm --force "${POD_NAME}" >/dev/null; then
        printf 'failed to remove rehearsal pod: %s\n' "${POD_NAME}" >&2
        cleanup_status=1
      fi
    elif ((pod_exists_status != 1)); then
      printf 'failed to determine whether the rehearsal pod exists: %s\n' "${POD_NAME}" >&2
      cleanup_status=1
    fi
  fi

  if [[ -n "${LABEL_VALUE}" ]]; then
    if ! labeled_containers="$(podman ps --all \
      --filter "label=${LABEL_KEY}=${LABEL_VALUE}" \
      --format '{{.Names}}' | sort)"; then
      printf 'failed to query labeled rehearsal containers during cleanup\n' >&2
      labeled_container_count=unknown
      cleanup_status=1
    fi
    if ! labeled_pods="$(podman pod ps \
      --filter "label=${LABEL_KEY}=${LABEL_VALUE}" \
      --format '{{.Name}}' | sort)"; then
      printf 'failed to query labeled rehearsal pods during cleanup\n' >&2
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
      printf 'labeled rehearsal resources remain after cleanup: containers=%s pods=%s\n' \
        "${labeled_containers:-<none>}" "${labeled_pods:-<none>}" >&2
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
      printf 'refusing to remove an unowned rehearsal work root\n' >&2
      cleanup_status=1
    elif ! rm -rf --one-file-system -- "${WORK_ROOT}"; then
      printf 'failed to remove the rehearsal work root\n' >&2
      cleanup_status=1
    fi
  fi
  printf 'REHEARSAL_CLEANUP token=%s labeled_containers=%s labeled_pods=%s preexisting_identities_unchanged=%s temporary_credentials_removed=%s\n' \
    "${TOKEN:-unallocated}" \
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
readonly RUN_LOG="${WORK_ROOT}/integration.log"
readonly BEFORE_CONTAINERS="${WORK_ROOT}/containers.before"
readonly BEFORE_PODS="${WORK_ROOT}/pods.before"
readonly AFTER_CONTAINERS="${WORK_ROOT}/containers.after"
readonly AFTER_PODS="${WORK_ROOT}/pods.after"
readonly POSTGRES_PASSWORD_FILE="${WORK_ROOT}/postgres-password"
readonly PGBOUNCER_CONFIG_DIR="${WORK_ROOT}/pgbouncer"
readonly PGBOUNCER_CONFIG_FILE="${PGBOUNCER_CONFIG_DIR}/pgbouncer.ini"
readonly PGBOUNCER_HBA_FILE="${PGBOUNCER_CONFIG_DIR}/pgbouncer-hba.conf"
readonly PGBOUNCER_USERLIST_FILE="${PGBOUNCER_CONFIG_DIR}/userlist.txt"

podman ps --all --format '{{.ID}}' | sort >"${BEFORE_CONTAINERS}"
podman pod ps --format '{{.ID}}' | sort >"${BEFORE_PODS}"
BASELINE_CAPTURED=1
readonly PREEXISTING_CONTAINER_COUNT="$(wc -l <"${BEFORE_CONTAINERS}" | tr -d ' ')"
readonly PREEXISTING_POD_COUNT="$(wc -l <"${BEFORE_PODS}" | tr -d ' ')"

[[ -x "${PGBOUNCER_BINARY}" && ! -L "${PGBOUNCER_BINARY}" &&
  "$(realpath -e -- "${PGBOUNCER_BINARY}")" == "${PGBOUNCER_BINARY}" ]] ||
  fail 'the release-locked native PgBouncer binary is unavailable'
[[ -f "${FEDORA_PACKAGE_LOCK}" && ! -L "${FEDORA_PACKAGE_LOCK}" ]] ||
  fail 'the release-owned Fedora package lock is unavailable'
EXPECTED_PGBOUNCER_NEVRA="$(jq -er '
  select(
    .schema == "ascendany.fedora-runtime-packages.v1" and
    .fedoraRelease == 44 and .architecture == "x86_64" and
    (.packages | keys == ["cloudflared", "pgbouncer"]) and
    (.packages.pgbouncer | keys == ["files", "nevra", "rpmSHA256", "signingFingerprint"]) and
    (.packages.pgbouncer.files | length == 1) and
    (.packages.pgbouncer.files[0] | keys == ["group", "mode", "owner", "path", "sha256", "size"]) and
    .packages.pgbouncer.files[0].path == "/usr/bin/pgbouncer"
  ) | .packages.pgbouncer.nevra
' "${FEDORA_PACKAGE_LOCK}")" || fail 'the Fedora runtime package lock violates its closed schema'
readonly EXPECTED_PGBOUNCER_NEVRA
EXPECTED_PGBOUNCER_SHA256="$(jq -er '.packages.pgbouncer.files[0].sha256' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary digest is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_SHA256
EXPECTED_PGBOUNCER_SIZE="$(jq -er '.packages.pgbouncer.files[0].size' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary size is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_SIZE
EXPECTED_PGBOUNCER_MODE="$(jq -er '.packages.pgbouncer.files[0].mode' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary mode is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_MODE
EXPECTED_PGBOUNCER_OWNER="$(jq -er '.packages.pgbouncer.files[0].owner' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary owner is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_OWNER
EXPECTED_PGBOUNCER_GROUP="$(jq -er '.packages.pgbouncer.files[0].group' "${FEDORA_PACKAGE_LOCK}")" ||
  fail 'the PgBouncer binary group is absent from the Fedora package lock'
readonly EXPECTED_PGBOUNCER_GROUP
[[ -x /usr/bin/rpm && "$(/usr/bin/rpm -q --qf '%{NEVRA}' pgbouncer)" == "${EXPECTED_PGBOUNCER_NEVRA}" ]] ||
  fail 'installed PgBouncer NEVRA differs from the Fedora 44 release lock'
[[ "$(sha256sum -- "${PGBOUNCER_BINARY}" | awk '{print $1}')" == "${EXPECTED_PGBOUNCER_SHA256}" &&
  "$(stat -Lc '%s' -- "${PGBOUNCER_BINARY}")" == "${EXPECTED_PGBOUNCER_SIZE}" &&
  "$(stat -Lc '%04a' -- "${PGBOUNCER_BINARY}")" == "${EXPECTED_PGBOUNCER_MODE}" &&
  "$(stat -Lc '%U:%G' -- "${PGBOUNCER_BINARY}")" == "${EXPECTED_PGBOUNCER_OWNER}:${EXPECTED_PGBOUNCER_GROUP}" &&
  "$("${PGBOUNCER_BINARY}" --version | sed -n '1p')" == 'PgBouncer 1.25.2' ]] ||
  fail 'installed PgBouncer binary identity differs from the Fedora 44 release lock'
grep -Fqx 'ID=fedora' /etc/os-release && grep -Fqx 'VERSION_ID=44' /etc/os-release &&
  [[ "$(uname -m)" == x86_64 ]] ||
  fail 'native PgBouncer rehearsal requires Fedora 44 x86_64'

for _attempt in {1..10}; do
  TOKEN="$(openssl rand -hex 6)"
  POD_NAME="ascendany-v2-pg-${TOKEN}"
  INFRA_CONTAINER_NAME="${POD_NAME}-infra"
  POSTGRES_CONTAINER_NAME="${POD_NAME}-postgres"
  MIGRATOR_LOOPBACK_HOST="127.127.$((16#${TOKEN:0:2} % 254 + 1)).$((16#${TOKEN:2:2} % 254 + 1))"
  if ! podman pod exists "${POD_NAME}" &&
    ! podman container exists "${INFRA_CONTAINER_NAME}" &&
    ! podman container exists "${POSTGRES_CONTAINER_NAME}" &&
    [[ -z "$(endpoint_listener "${MIGRATOR_LOOPBACK_HOST}" 5432)" ]]; then
    break
  fi
  TOKEN=""
done
if [[ -z "${TOKEN}" ]]; then
  fail 'could not allocate collision-free random Podman resource names'
fi
readonly TOKEN POD_NAME INFRA_CONTAINER_NAME POSTGRES_CONTAINER_NAME MIGRATOR_LOOPBACK_HOST
readonly LABEL_VALUE="${TOKEN}"

readonly POSTGRES_ADMIN_PASSWORD="$(openssl rand -hex 24)"
readonly PGBOUNCER_ADMIN_USER="pgbouncer_rehearsal"
readonly PGBOUNCER_ADMIN_PASSWORD="$(openssl rand -hex 24)"
readonly LEGACY_PASSWORD="$(openssl rand -hex 24)"
readonly RUNTIME_PASSWORD="$(openssl rand -hex 24)"
readonly BACKUP_PASSWORD="$(openssl rand -hex 24)"
# Two migration integration tests construct this isolated credential internally.
# It exists only for this disposable pod and is destroyed by the EXIT trap.
readonly MIGRATOR_PASSWORD="local-rehearsal-password"

printf '%s' "${POSTGRES_ADMIN_PASSWORD}" >"${POSTGRES_PASSWORD_FILE}"
chmod 600 "${POSTGRES_PASSWORD_FILE}"
mkdir -m 0700 -- "${PGBOUNCER_CONFIG_DIR}"
install -m 0400 -- "${RELEASE_PGBOUNCER_CONFIG}" "${PGBOUNCER_CONFIG_FILE}.release"
install -m 0400 -- "${RELEASE_PGBOUNCER_HBA}" "${PGBOUNCER_HBA_FILE}.release"
[[ "$(grep -Ec '^auth_type = hba$' "${PGBOUNCER_CONFIG_FILE}.release")" == 1 &&
  "$(grep -Ec '^pool_mode = transaction$' "${PGBOUNCER_CONFIG_FILE}.release")" == 1 &&
  "$(grep -Ec '^listen_addr = 127\.0\.0\.1$' "${PGBOUNCER_CONFIG_FILE}.release")" == 1 &&
  "$(grep -Ec '^listen_port = 6432$' "${PGBOUNCER_CONFIG_FILE}.release")" == 1 &&
  "$(grep -Ec '^auth_file = ' "${PGBOUNCER_CONFIG_FILE}.release")" == 1 &&
  "$(grep -Ec '^auth_hba_file = ' "${PGBOUNCER_CONFIG_FILE}.release")" == 1 ]] ||
  fail 'release-owned PgBouncer configuration violates the rehearsal transform contract'
sed \
  -e "s|host=127.0.0.1 port=5432|host=127.0.0.1 port=${DIRECT_PORT}|g" \
  -e "s|^listen_port = 6432$|listen_port = ${PGBOUNCER_PORT}|" \
  -e "s|^auth_file = .*$|auth_file = ${PGBOUNCER_USERLIST_FILE}|" \
  -e "s|^auth_hba_file = .*$|auth_hba_file = ${PGBOUNCER_HBA_FILE}|" \
  "${PGBOUNCER_CONFIG_FILE}.release" >"${PGBOUNCER_CONFIG_FILE}"
printf 'admin_users = %s\nstats_users = %s\n' \
  "${PGBOUNCER_ADMIN_USER}" "${PGBOUNCER_ADMIN_USER}" >>"${PGBOUNCER_CONFIG_FILE}"
{
  printf 'host pgbouncer %s 127.0.0.1/32 scram-sha-256\n' "${PGBOUNCER_ADMIN_USER}"
  cat "${PGBOUNCER_HBA_FILE}.release"
} >"${PGBOUNCER_HBA_FILE}"
rm -f -- "${PGBOUNCER_CONFIG_FILE}.release" "${PGBOUNCER_HBA_FILE}.release"
chmod 0400 -- "${PGBOUNCER_CONFIG_FILE}" "${PGBOUNCER_HBA_FILE}"

printf 'Creating rootless rehearsal pod %s on 127.0.0.1:%s and 127.0.0.1:%s\n' \
  "${POD_NAME}" "${DIRECT_PORT}" "${PGBOUNCER_PORT}"
POD_CREATE_ATTEMPTED=1
if podman pod create \
    --name "${POD_NAME}" \
    --infra-name "${INFRA_CONTAINER_NAME}" \
    --label "${LABEL_KEY}=${LABEL_VALUE}" \
    --publish "127.0.0.1:${DIRECT_PORT}:5432" \
    --publish "${MIGRATOR_LOOPBACK_HOST}:5432:5432" \
    >/dev/null; then
  :
else
  fail "could not create the isolated rehearsal pod: ${POD_NAME}"
fi

podman run --detach \
  --pod "${POD_NAME}" \
  --name "${POSTGRES_CONTAINER_NAME}" \
  --label "${LABEL_KEY}=${LABEL_VALUE}" \
  --env POSTGRES_USER=postgres \
  --env POSTGRES_DB=postgres \
  --env POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password \
  --volume "${POSTGRES_PASSWORD_FILE}:/run/secrets/postgres-password:ro,Z" \
  "${POSTGRES_IMAGE}" \
  -c password_encryption=scram-sha-256 \
  >/dev/null

wait_for_postgres() {
  local attempt
  for attempt in {1..120}; do
    if pg_isready \
      --host=127.0.0.1 \
      --port="${DIRECT_PORT}" \
      --username=postgres \
      --dbname=postgres >/dev/null 2>&1; then
      return 0
    fi
    if [[ "$(podman inspect --format '{{.State.Running}}' "${POSTGRES_CONTAINER_NAME}")" != "true" ]]; then
      podman logs "${POSTGRES_CONTAINER_NAME}" >&2
      return 1
    fi
    sleep 0.5
  done
  podman logs "${POSTGRES_CONTAINER_NAME}" >&2
  printf 'PostgreSQL did not become ready on loopback port %s\n' "${DIRECT_PORT}" >&2
  return 1
}
wait_for_postgres

PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" psql \
  -X --no-password --set=ON_ERROR_STOP=1 \
  --host=127.0.0.1 --port="${DIRECT_PORT}" --username=postgres --dbname=postgres \
  --set=pgbouncer_admin_password="${PGBOUNCER_ADMIN_PASSWORD}" \
  --set=legacy_password="${LEGACY_PASSWORD}" \
  --set=runtime_password="${RUNTIME_PASSWORD}" >/dev/null <<'SQL'
CREATE ROLE pgbouncer_rehearsal LOGIN PASSWORD :'pgbouncer_admin_password';
CREATE ROLE "AscendAny" LOGIN PASSWORD :'legacy_password';
CREATE ROLE ascendanyd_login LOGIN PASSWORD :'runtime_password';
CREATE DATABASE "AscendAny" WITH TEMPLATE template0 OWNER "AscendAny";
REVOKE CONNECT ON DATABASE "AscendAny" FROM PUBLIC;
GRANT CONNECT ON DATABASE "AscendAny" TO "AscendAny";
SQL

PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" psql \
  -X --no-password --set=ON_ERROR_STOP=1 --tuples-only --no-align \
  --host=127.0.0.1 --port="${DIRECT_PORT}" --username=postgres --dbname=postgres \
  >"${PGBOUNCER_USERLIST_FILE}" <<'SQL'
SELECT format('"%s" "%s"', rolname, rolpassword)
FROM pg_authid
WHERE rolname IN ('pgbouncer_rehearsal', 'AscendAny', 'ascendanyd_login')
  AND rolpassword LIKE 'SCRAM-SHA-256$%'
ORDER BY rolname COLLATE "C";
SQL
[[ "$(wc -l <"${PGBOUNCER_USERLIST_FILE}" | tr -d ' ')" == 3 &&
  "$(grep -c ' "SCRAM-SHA-256\$' "${PGBOUNCER_USERLIST_FILE}")" == 3 ]] ||
  fail 'native PgBouncer rehearsal did not capture exactly three SCRAM verifiers'
if grep -Fq -- "${PGBOUNCER_ADMIN_PASSWORD}" "${PGBOUNCER_USERLIST_FILE}" ||
  grep -Fq -- "${LEGACY_PASSWORD}" "${PGBOUNCER_USERLIST_FILE}" ||
  grep -Fq -- "${RUNTIME_PASSWORD}" "${PGBOUNCER_USERLIST_FILE}"; then
  fail 'native PgBouncer userlist contains plaintext credential material'
fi
chmod 0400 -- "${PGBOUNCER_USERLIST_FILE}"

readonly PGBOUNCER_STDOUT="${WORK_ROOT}/pgbouncer.out"
readonly PGBOUNCER_STDERR="${WORK_ROOT}/pgbouncer.err"
"${PGBOUNCER_BINARY}" -q "${PGBOUNCER_CONFIG_FILE}" \
  >"${PGBOUNCER_STDOUT}" 2>"${PGBOUNCER_STDERR}" &
PGBOUNCER_PID=$!

wait_for_pgbouncer() {
  local attempt
  for attempt in {1..120}; do
    if PGPASSWORD="${PGBOUNCER_ADMIN_PASSWORD}" psql \
      -X \
      --no-password \
      --set=ON_ERROR_STOP=1 \
      --host=127.0.0.1 \
      --port="${PGBOUNCER_PORT}" \
      --username="${PGBOUNCER_ADMIN_USER}" \
      --dbname=pgbouncer \
      --command='SHOW VERSION' >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "${PGBOUNCER_PID}" 2>/dev/null; then
      sed -n '1,160p' "${PGBOUNCER_STDOUT}" "${PGBOUNCER_STDERR}" >&2
      return 1
    fi
    sleep 0.5
  done
  sed -n '1,160p' "${PGBOUNCER_STDOUT}" "${PGBOUNCER_STDERR}" >&2
  printf 'PgBouncer did not become ready on loopback port %s\n' "${PGBOUNCER_PORT}" >&2
  return 1
}
wait_for_pgbouncer

readonly PGBOUNCER_RUNTIME_CONFIG="$(PGPASSWORD="${PGBOUNCER_ADMIN_PASSWORD}" psql \
  -X \
  --no-password \
  --tuples-only \
  --no-align \
  --field-separator='|' \
  --host=127.0.0.1 \
  --port="${PGBOUNCER_PORT}" \
  --username="${PGBOUNCER_ADMIN_USER}" \
  --dbname=pgbouncer \
  --command='SHOW CONFIG')"
readonly ACTUAL_POOL_MODE="$(awk -F '|' '$1 == "pool_mode" { print $2 }' <<<"${PGBOUNCER_RUNTIME_CONFIG}")"
readonly ACTUAL_AUTH_TYPE="$(awk -F '|' '$1 == "auth_type" { print $2 }' <<<"${PGBOUNCER_RUNTIME_CONFIG}")"
if [[ "${ACTUAL_POOL_MODE}" != "transaction" || "${ACTUAL_AUTH_TYPE}" != "hba" ]]; then
  printf 'PgBouncer contract mismatch: pool_mode=%s auth_type=%s\n' \
    "${ACTUAL_POOL_MODE:-<missing>}" "${ACTUAL_AUTH_TYPE:-<missing>}" >&2
  exit 1
fi

# The PgBouncer console identity is owned entirely by its protected auth_file.
# Retire the bootstrap-only PostgreSQL role before the exact production role
# verifier runs, then prove the virtual PgBouncer admin database remains usable.
PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" psql \
  -X --no-password --set=ON_ERROR_STOP=1 \
  --host=127.0.0.1 --port="${DIRECT_PORT}" --username=postgres --dbname=postgres \
  --command='DROP ROLE pgbouncer_rehearsal' >/dev/null
[[ "$(PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" psql \
  -X --no-password --tuples-only --no-align \
  --host=127.0.0.1 --port="${DIRECT_PORT}" --username=postgres --dbname=postgres \
  --command="SELECT count(*) FROM pg_roles WHERE rolname = 'pgbouncer_rehearsal'")" == 0 ]] ||
  fail 'PgBouncer rehearsal bootstrap role survived its database retirement boundary'
PGPASSWORD="${PGBOUNCER_ADMIN_PASSWORD}" psql \
  -X --no-password --set=ON_ERROR_STOP=1 \
  --host=127.0.0.1 --port="${PGBOUNCER_PORT}" \
  --username="${PGBOUNCER_ADMIN_USER}" --dbname=pgbouncer \
  --command='SHOW VERSION' >/dev/null ||
  fail 'PgBouncer auth_file console identity failed after database role retirement'

readonly POSTGRES_IMAGE_ID="$(podman image inspect "${POSTGRES_IMAGE}" --format '{{.Id}}')"
printf 'Infrastructure ready: rootless=true PostgreSQL_image=%s PgBouncer_nevra=%s pool_mode=transaction auth_type=hba userlist=SCRAM-only\n' \
  "${POSTGRES_IMAGE_ID}" "${EXPECTED_PGBOUNCER_NEVRA}"
printf 'Migrator contract endpoint: %s:5432 (isolated loopback alias)\n' "${MIGRATOR_LOOPBACK_HOST}"
printf 'Snapshot under rehearsal: %s\n' "${SNAPSHOT_PATH}"

probe_hba_rejection() {
  local user="$1"
  local database="$2"
  local password="$3"
  local output
  local expected
  expected="psql: error: connection to server at \"127.0.0.1\", port ${PGBOUNCER_PORT} failed: FATAL:  login rejected"
  if output="$(PGPASSWORD="${password}" psql \
      -X --no-password --host=127.0.0.1 --port="${PGBOUNCER_PORT}" \
      --username="${user}" --dbname="${database}" --command='SELECT 1' 2>&1)"; then
    fail "PgBouncer HBA accepted forbidden ${user} access to ${database}"
  fi
  if [[ "${output}" != "${expected}" ]]; then
    printf 'PgBouncer HBA rejection output for %s on %s: %s\n' \
      "${user}" "${database}" "${output}" >&2
    fail "PgBouncer HBA returned a noncanonical rejection for ${user} on ${database}"
  fi
}
probe_hba_rejection ascendanyd_login AscendAny "${RUNTIME_PASSWORD}"
probe_hba_rejection AscendAny ascendany_v2 "${LEGACY_PASSWORD}"

if ! env \
  ASCENDANY_CI_POSTGRES_HOST=127.0.0.1 \
  ASCENDANY_CI_POSTGRES_PORT="${DIRECT_PORT}" \
  ASCENDANY_CI_MIGRATOR_POSTGRES_HOST="${MIGRATOR_LOOPBACK_HOST}" \
  ASCENDANY_CI_POSTGRES_ADMIN_USER=postgres \
  ASCENDANY_CI_POSTGRES_ADMIN_PASSWORD="${POSTGRES_ADMIN_PASSWORD}" \
  ASCENDANY_CI_DATABASE_RESET_CONFIRMATION="${RESET_CONFIRMATION}" \
  ASCENDANY_CI_PGBOUNCER_HOST=127.0.0.1 \
  ASCENDANY_CI_PGBOUNCER_PORT="${PGBOUNCER_PORT}" \
  ASCENDANY_CI_PGBOUNCER_ADMIN_USER="${PGBOUNCER_ADMIN_USER}" \
  ASCENDANY_CI_PGBOUNCER_ADMIN_PASSWORD="${PGBOUNCER_ADMIN_PASSWORD}" \
  ASCENDANY_CI_PGBOUNCER_USERLIST_PATH="${PGBOUNCER_USERLIST_FILE}" \
  ASCENDANY_CI_RUNTIME_PASSWORD="${RUNTIME_PASSWORD}" \
  ASCENDANY_CI_MIGRATOR_PASSWORD="${MIGRATOR_PASSWORD}" \
  ASCENDANY_CI_BACKUP_PASSWORD="${BACKUP_PASSWORD}" \
  ASCENDANY_CI_REAL_PINTIA_SNAPSHOT_PATH="${SNAPSHOT_PATH}" \
  "${INTEGRATION_RUNNER}" 2>&1 | tee "${RUN_LOG}"; then
  printf 'PostgreSQL integration rehearsal failed; container diagnostics follow\n' >&2
  podman logs "${POSTGRES_CONTAINER_NAME}" >&2
  sed -n '1,160p' "${PGBOUNCER_STDOUT}" "${PGBOUNCER_STDERR}" >&2
  exit 1
fi

readonly LEGACY_POOL_IDENTITY="$(PGPASSWORD="${LEGACY_PASSWORD}" psql \
  -X --no-password --tuples-only --no-align \
  --host=127.0.0.1 --port="${PGBOUNCER_PORT}" \
  --username=AscendAny --dbname=AscendAny --command='SELECT current_user')"
[[ "${LEGACY_POOL_IDENTITY}" == AscendAny ]] ||
  fail 'native PgBouncer rehearsal did not preserve the allowed legacy pool route'
[[ "$(stat -Lc '%u:%a' -- "${PGBOUNCER_USERLIST_FILE}")" == "${EUID}:400" &&
  "$(wc -l <"${PGBOUNCER_USERLIST_FILE}" | tr -d ' ')" == 3 &&
  "$(grep -c ' "SCRAM-SHA-256\$' "${PGBOUNCER_USERLIST_FILE}")" == 3 ]] ||
  fail 'integration runner did not atomically publish the exact SCRAM userlist'

if ! rg --fixed-strings --quiet \
  "All ${EXPECTED_MANIFEST_TESTS} env-gated PostgreSQL integration tests executed without skips." \
  "${RUN_LOG}"; then
  printf 'integration runner did not report the complete %s-test manifest\n' \
    "${EXPECTED_MANIFEST_TESTS}" >&2
  exit 1
fi
readonly FINAL_SNAPSHOT_SUMMARY="$(rg '^  REAL_SNAPSHOT_DATABASE_SUMMARY ' "${RUN_LOG}" | tail -n 1)"
if [[ -z "${FINAL_SNAPSHOT_SUMMARY}" ]]; then
  printf 'integration runner did not emit a real snapshot database summary\n' >&2
  exit 1
fi

printf 'REHEARSAL_RESULT manifest_tests=%s passed=%s skipped=0 preexisting_containers=%s preexisting_pods=%s\n' \
  "${EXPECTED_MANIFEST_TESTS}" \
  "${EXPECTED_MANIFEST_TESTS}" \
  "${PREEXISTING_CONTAINER_COUNT}" \
  "${PREEXISTING_POD_COUNT}"
printf '%s\n' "${FINAL_SNAPSHOT_SUMMARY}"
