#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPOSITORY_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
readonly BACKEND_ROOT="${REPOSITORY_ROOT}/backend"
readonly DATABASE_NAME="ascendany_v2"
readonly EXPECTED_POSTGRES_MAJOR="17"
readonly EXPECTED_SCHEMA_VERSION="5"
readonly MIGRATOR_TEST_PASSWORD="local-rehearsal-password"
readonly RESTORE_TEST_PASSWORD="local-restore-rehearsal-password"

required_environment() {
  local name="$1"
  local value="${!name:-}"
  if [[ -z "${value}" ]]; then
    printf 'required environment variable is empty: %s\n' "${name}" >&2
    exit 2
  fi
  printf '%s' "${value}"
}

optional_port() {
  local name="$1"
  local default_value="$2"
  local value="${!name:-${default_value}}"
  if [[ ! "${value}" =~ ^[0-9]+$ ]] || (( ${#value} > 5 )) ||
    (( 10#${value} < 1 || 10#${value} > 65535 )); then
    printf '%s must be a decimal TCP port between 1 and 65535\n' "${name}" >&2
    exit 2
  fi
  printf '%d' "$((10#${value}))"
}

optional_absolute_file() {
  local name="$1"
  local default_value="$2"
  local value="${!name:-${default_value}}"
  if [[ "${value}" != /* ]]; then
    printf '%s must be an absolute path\n' "${name}" >&2
    exit 2
  fi
  if [[ ! -f "${value}" ]]; then
    printf '%s does not identify a regular file: %s\n' "${name}" "${value}" >&2
    exit 2
  fi
  printf '%s' "${value}"
}

readonly POSTGRES_HOST="$(required_environment ASCENDANY_CI_POSTGRES_HOST)"
readonly MIGRATOR_POSTGRES_HOST="${ASCENDANY_CI_MIGRATOR_POSTGRES_HOST:-${POSTGRES_HOST}}"
readonly POSTGRES_ADMIN_USER="$(required_environment ASCENDANY_CI_POSTGRES_ADMIN_USER)"
readonly POSTGRES_ADMIN_PASSWORD="$(required_environment ASCENDANY_CI_POSTGRES_ADMIN_PASSWORD)"
readonly DATABASE_RESET_CONFIRMATION="$(required_environment ASCENDANY_CI_DATABASE_RESET_CONFIRMATION)"
readonly PGBOUNCER_HOST="$(required_environment ASCENDANY_CI_PGBOUNCER_HOST)"
readonly PGBOUNCER_ADMIN_USER="$(required_environment ASCENDANY_CI_PGBOUNCER_ADMIN_USER)"
readonly PGBOUNCER_ADMIN_PASSWORD="$(required_environment ASCENDANY_CI_PGBOUNCER_ADMIN_PASSWORD)"
readonly PGBOUNCER_USERLIST_PATH="$(required_environment ASCENDANY_CI_PGBOUNCER_USERLIST_PATH)"
readonly RUNTIME_PASSWORD="$(required_environment ASCENDANY_CI_RUNTIME_PASSWORD)"
readonly MIGRATOR_PASSWORD="$(required_environment ASCENDANY_CI_MIGRATOR_PASSWORD)"
readonly BACKUP_PASSWORD="$(required_environment ASCENDANY_CI_BACKUP_PASSWORD)"
readonly DIRECT_PORT="$(optional_port ASCENDANY_CI_POSTGRES_PORT 5432)"
readonly PGBOUNCER_PORT="$(optional_port ASCENDANY_CI_PGBOUNCER_PORT 6432)"

if [[ "${DATABASE_RESET_CONFIRMATION}" != "drop-disposable-ascendany-v2" ]]; then
  printf 'ASCENDANY_CI_DATABASE_RESET_CONFIRMATION does not authorize the disposable database reset\n' >&2
  exit 2
fi

for credential_name in \
  ASCENDANY_CI_POSTGRES_ADMIN_PASSWORD \
  ASCENDANY_CI_PGBOUNCER_ADMIN_PASSWORD \
  ASCENDANY_CI_RUNTIME_PASSWORD \
  ASCENDANY_CI_MIGRATOR_PASSWORD \
  ASCENDANY_CI_BACKUP_PASSWORD; do
  credential_value="${!credential_name}"
  if [[ ! "${credential_value}" =~ ^[A-Za-z0-9._~-]{16,}$ ]]; then
    printf '%s must be an explicit PGPASS-safe ephemeral credential of at least 16 characters\n' "${credential_name}" >&2
    exit 2
  fi
done
unset credential_name credential_value

if [[ "${MIGRATOR_PASSWORD}" != "${MIGRATOR_TEST_PASSWORD}" ]]; then
  printf 'ASCENDANY_CI_MIGRATOR_PASSWORD must equal the isolated migration-test credential\n' >&2
  exit 2
fi

if [[ ! "${PGBOUNCER_ADMIN_USER}" =~ ^[a-z][a-z0-9_]{0,62}$ ||
  "${PGBOUNCER_ADMIN_USER}" == ascendanyd_login ||
  "${PGBOUNCER_ADMIN_USER}" == AscendAny ]]; then
  printf 'ASCENDANY_CI_PGBOUNCER_ADMIN_USER must be one distinct canonical PostgreSQL identifier\n' >&2
  exit 2
fi
if [[ "${PGBOUNCER_USERLIST_PATH}" != /* || ! -f "${PGBOUNCER_USERLIST_PATH}" ||
  -L "${PGBOUNCER_USERLIST_PATH}" ||
  "$(realpath -e -- "${PGBOUNCER_USERLIST_PATH}")" != "${PGBOUNCER_USERLIST_PATH}" ||
  "$(stat -Lc '%u:%a' -- "${PGBOUNCER_USERLIST_PATH}")" != "${EUID}:400" ]]; then
  printf 'ASCENDANY_CI_PGBOUNCER_USERLIST_PATH must be a canonical owned mode-0400 regular file\n' >&2
  exit 2
fi
readonly PGBOUNCER_USERLIST_PARENT="$(dirname -- "${PGBOUNCER_USERLIST_PATH}")"
if [[ ! -d "${PGBOUNCER_USERLIST_PARENT}" || -L "${PGBOUNCER_USERLIST_PARENT}" ||
  "$(realpath -e -- "${PGBOUNCER_USERLIST_PARENT}")" != "${PGBOUNCER_USERLIST_PARENT}" ||
  "$(stat -Lc '%u:%a' -- "${PGBOUNCER_USERLIST_PARENT}")" != "${EUID}:700" ]]; then
  printf 'PgBouncer userlist parent must be a canonical owned mode-0700 directory\n' >&2
  exit 2
fi

for command_name in chmod diff dirname go grep mktemp mv psql realpath rg rm sed sort stat sync tr wc; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    printf 'required command is unavailable: %s\n' "${command_name}" >&2
    exit 2
  fi
done
unset command_name

readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-v2-postgres-integration.XXXXXX")"
readonly MIGRATOR_PASSWORD_FILE="${WORK_ROOT}/migrator-password"
readonly RUNTIME_PGPASS_FILE="${WORK_ROOT}/runtime.pgpass"
readonly MIGRATOR_PGPASS_FILE="${WORK_ROOT}/migrator.pgpass"
readonly MIGRATOR_BINARY="${WORK_ROOT}/ascendany-migrate"
readonly GO_BINARY="$(command -v go)"
# Env-gated integration packages construct their own pgx pools. Encode the
# transaction-pooling execution contract in the URL they all receive.
readonly RUNTIME_DATABASE_URL="postgresql://ascendanyd_login@${PGBOUNCER_HOST}:${PGBOUNCER_PORT}/${DATABASE_NAME}?sslmode=disable&default_query_exec_mode=exec&statement_cache_capacity=0&description_cache_capacity=0"
readonly MIGRATOR_DATABASE_URL="postgresql://ascendany_migrator_login@${MIGRATOR_POSTGRES_HOST}:5432/${DATABASE_NAME}?sslmode=disable"
readonly REAL_SNAPSHOT_PATH="$(optional_absolute_file \
  ASCENDANY_CI_REAL_PINTIA_SNAPSHOT_PATH \
  "${REPOSITORY_ROOT}/contracts/pintia/fixtures/valid/complete.json")"
readonly ANALYTICS_CONFIG_PATH="${REPOSITORY_ROOT}/deploy/v2/config/analytics.json.example"

cleanup() {
  rm -rf -- "${WORK_ROOT}"
}
trap cleanup EXIT

umask 077
printf '%s' "${MIGRATOR_PASSWORD}" >"${MIGRATOR_PASSWORD_FILE}"
printf '%s:%s:%s:%s:%s\n' \
  "${PGBOUNCER_HOST}" "${PGBOUNCER_PORT}" "${DATABASE_NAME}" ascendanyd_login "${RUNTIME_PASSWORD}" \
  >"${RUNTIME_PGPASS_FILE}"
printf '%s:%s:%s:%s:%s\n' \
  "${MIGRATOR_POSTGRES_HOST}" 5432 "${DATABASE_NAME}" ascendany_migrator_login "${MIGRATOR_PASSWORD}" \
  >"${MIGRATOR_PGPASS_FILE}"

admin_psql() {
  local database="$1"
  shift
  PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" psql \
    -X \
    --no-password \
    --set=ON_ERROR_STOP=1 \
    --host="${POSTGRES_HOST}" \
    --port="${DIRECT_PORT}" \
    --username="${POSTGRES_ADMIN_USER}" \
    --dbname="${database}" \
    "$@"
}

apply_role_bootstrap() {
  admin_psql "${DATABASE_NAME}" \
    --file="${REPOSITORY_ROOT}/db/roles/001_v2_roles.sql" >/dev/null
}

verify_role_boundary() {
  admin_psql "${DATABASE_NAME}" \
    --file="${REPOSITORY_ROOT}/db/roles/verify_v2_roles.sql" >/dev/null
}

pgbouncer_admin_psql() {
  PGPASSWORD="${PGBOUNCER_ADMIN_PASSWORD}" psql \
    -X \
    --no-password \
    --set=ON_ERROR_STOP=1 \
    --host="${PGBOUNCER_HOST}" \
    --port="${PGBOUNCER_PORT}" \
    --username="${PGBOUNCER_ADMIN_USER}" \
    --dbname=pgbouncer \
    "$@"
}

publish_pgbouncer_userlist() {
  local temporary_userlist admin_line
  local -a admin_lines=()
  mapfile -t admin_lines < <(
    grep -E "^\"${PGBOUNCER_ADMIN_USER}\" \"SCRAM-SHA-256\\\$" "${PGBOUNCER_USERLIST_PATH}" || true
  )
  if [[ "${#admin_lines[@]}" != 1 ||
    ! "${admin_lines[0]}" =~ ^\"${PGBOUNCER_ADMIN_USER}\"[[:space:]]\"SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}\"$ ]]; then
    printf 'PgBouncer admin SCRAM verifier is absent or noncanonical\n' >&2
    exit 1
  fi
  admin_line="${admin_lines[0]}"
  temporary_userlist="$(mktemp "${PGBOUNCER_USERLIST_PATH}.new.XXXXXX")"
  (
    trap 'rm -f -- "${temporary_userlist}"' EXIT
    printf '%s\n' "${admin_line}" >"${temporary_userlist}"
    admin_psql postgres \
      --tuples-only \
      --no-align \
      >>"${temporary_userlist}" <<'SQL'
SELECT format('"%s" "%s"', rolname, rolpassword)
FROM pg_authid
WHERE rolname IN ('AscendAny', 'ascendanyd_login')
  AND rolpassword LIKE 'SCRAM-SHA-256$%'
ORDER BY CASE rolname
    WHEN 'AscendAny' THEN 0
    WHEN 'ascendanyd_login' THEN 1
END;
SQL
    if [[ "$(wc -l <"${temporary_userlist}" | tr -d ' ')" != 3 ||
      "$(grep -c ' "SCRAM-SHA-256\$' "${temporary_userlist}")" != 3 ]]; then
      printf 'PostgreSQL did not yield the exact ordered PgBouncer SCRAM identities\n' >&2
      exit 1
    fi
    if [[ "$(sed -n '1s/^"\([^"]*\)" .*/\1/p' "${temporary_userlist}")" != "${PGBOUNCER_ADMIN_USER}" ||
      "$(sed -n '2s/^"\([^"]*\)" .*/\1/p' "${temporary_userlist}")" != AscendAny ||
      "$(sed -n '3s/^"\([^"]*\)" .*/\1/p' "${temporary_userlist}")" != ascendanyd_login ]]; then
      printf 'PostgreSQL returned PgBouncer SCRAM identities in a noncanonical order\n' >&2
      exit 1
    fi
    if grep -Fq -- "${PGBOUNCER_ADMIN_PASSWORD}" "${temporary_userlist}" ||
      grep -Fq -- "${RUNTIME_PASSWORD}" "${temporary_userlist}"; then
      printf 'PgBouncer userlist publication exposed plaintext credential material\n' >&2
      exit 1
    fi
    chmod 0400 -- "${temporary_userlist}"
    sync -f -- "${temporary_userlist}"
    mv -T -- "${temporary_userlist}" "${PGBOUNCER_USERLIST_PATH}"
    sync -f -- "${PGBOUNCER_USERLIST_PARENT}"
    trap - EXIT
  )
}

runtime_psql() {
  PGPASSWORD="${RUNTIME_PASSWORD}" psql \
    -X \
    --no-password \
    --set=ON_ERROR_STOP=1 \
    --host="${PGBOUNCER_HOST}" \
    --port="${PGBOUNCER_PORT}" \
    --username=ascendanyd_login \
    --dbname="${DATABASE_NAME}" \
    "$@"
}

backup_psql() {
  PGPASSWORD="${BACKUP_PASSWORD}" psql \
    -X \
    --no-password \
    --set=ON_ERROR_STOP=1 \
    --host="${POSTGRES_HOST}" \
    --port="${DIRECT_PORT}" \
    --username=ascendany_backup_login \
    --dbname="${DATABASE_NAME}" \
    "$@"
}

verify_infrastructure() {
  local server_version
  local postgres_major
  local admin_is_superuser
  local pool_mode

  server_version="$(admin_psql postgres --tuples-only --no-align --command='SHOW server_version_num')"
  postgres_major="$((server_version / 10000))"
  if [[ "${postgres_major}" != "${EXPECTED_POSTGRES_MAJOR}" ]]; then
    printf 'PostgreSQL major version is %s, want %s\n' "${postgres_major}" "${EXPECTED_POSTGRES_MAJOR}" >&2
    exit 1
  fi

  admin_is_superuser="$(admin_psql postgres --tuples-only --no-align --command='SELECT rolsuper FROM pg_roles WHERE rolname = current_user')"
  if [[ "${admin_is_superuser}" != "t" ]]; then
    printf 'PostgreSQL integration administrator must be a disposable cluster superuser\n' >&2
    exit 1
  fi

  pool_mode="$(
    pgbouncer_admin_psql --tuples-only --no-align --field-separator='|' --command='SHOW CONFIG' |
      awk -F '|' '$1 == "pool_mode" { print $2 }'
  )"
  if [[ "${pool_mode}" != "transaction" ]]; then
    printf 'PgBouncer pool_mode is %s, want transaction\n' "${pool_mode:-<missing>}" >&2
    exit 1
  fi
}

reset_exact_database() {
  admin_psql postgres --command="DROP DATABASE IF EXISTS ${DATABASE_NAME} WITH (FORCE)" >/dev/null
  admin_psql postgres --command="CREATE DATABASE ${DATABASE_NAME} TEMPLATE template0 ENCODING 'UTF8'" >/dev/null
  # Model a reused cluster where the managed runtime login already owns a
  # PostgreSQL built-in write capability. The bootstrap must normalize the
  # complete membership graph before it grants the reviewed v2 edges.
  admin_psql "${DATABASE_NAME}" >/dev/null <<'SQL'
DO $preexisting_runtime_login$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendanyd_login') THEN
        CREATE ROLE ascendanyd_login LOGIN;
    END IF;
END
$preexisting_runtime_login$;
DROP ROLE IF EXISTS ascendany_ci_membership_grantor;
CREATE ROLE ascendany_ci_membership_grantor CREATEROLE;
GRANT pg_write_all_data TO ascendany_ci_membership_grantor WITH ADMIN OPTION;
SET ROLE ascendany_ci_membership_grantor;
GRANT pg_write_all_data TO ascendanyd_login WITH ADMIN OPTION;
RESET ROLE;
SQL
  apply_role_bootstrap

  if [[ "$(admin_psql "${DATABASE_NAME}" --tuples-only --no-align --command="SELECT pg_has_role('ascendanyd_login', 'pg_write_all_data', 'MEMBER')")" != "f" ]]; then
    printf 'role bootstrap retained a membership recorded under a different grantor\n' >&2
    exit 1
  fi
  admin_psql "${DATABASE_NAME}" >/dev/null <<'SQL'
REVOKE pg_write_all_data FROM ascendany_ci_membership_grantor CASCADE;
DROP ROLE ascendany_ci_membership_grantor;
SQL

  admin_psql "${DATABASE_NAME}" \
    --set=runtime_password="${RUNTIME_PASSWORD}" \
    --set=migrator_password="${MIGRATOR_PASSWORD}" \
    --set=backup_password="${BACKUP_PASSWORD}" \
    --set=restore_password="${RESTORE_TEST_PASSWORD}" >/dev/null <<'SQL'
ALTER ROLE ascendanyd_login PASSWORD :'runtime_password';
ALTER ROLE ascendany_migrator_login PASSWORD :'migrator_password';
ALTER ROLE ascendany_backup_login PASSWORD :'backup_password';
ALTER ROLE ascendany_restore_login PASSWORD :'restore_password';
SQL

  publish_pgbouncer_userlist
  pgbouncer_admin_psql --command='RELOAD' >/dev/null
  pgbouncer_admin_psql --command="RECONNECT ${DATABASE_NAME}" >/dev/null
}

run_migrations() {
  ASCENDANY_DATABASE_URL="${MIGRATOR_DATABASE_URL}" \
  ASCENDANY_DATABASE_PASSWORD_FILE="${MIGRATOR_PASSWORD_FILE}" \
  ASCENDANY_DATABASE_ROLE=ascendany_owner \
  ASCENDANY_DATABASE_SCHEMA=ascendany \
  ASCENDANY_MIGRATION_HISTORY_TABLE=ascendany.schema_migrations_v2 \
  ASCENDANY_DATABASE_SCHEMA_VERSION="${EXPECTED_SCHEMA_VERSION}" \
  ASCENDANY_MIGRATION_LOCK_TIMEOUT=5s \
  ASCENDANY_DATABASE_CONNECT_TIMEOUT=5s \
    "${MIGRATOR_BINARY}" up >/dev/null
}

assert_schema_roles_and_ports() {
  local migration_state
  local backup_identity
  local runtime_identity
  local denial_log="${WORK_ROOT}/denial.log"
  local restore_denial_log="${WORK_ROOT}/restore-denial.log"
  local restore_maintenance_identity

  migration_state="$(admin_psql "${DATABASE_NAME}" --tuples-only --no-align --command="
SELECT count(*)::text || ':' || max(version)::text
FROM ascendany.schema_migrations_v2")"
  if [[ "${migration_state}" != "${EXPECTED_SCHEMA_VERSION}:${EXPECTED_SCHEMA_VERSION}" ]]; then
    printf 'migration state is %s, want %s:%s\n' \
      "${migration_state}" "${EXPECTED_SCHEMA_VERSION}" "${EXPECTED_SCHEMA_VERSION}" >&2
    exit 1
  fi

  # Table row types do not accept PostgreSQL default type ACLs. Reapply the
  # idempotent closure after migrations so every concrete type and ACL is
  # normalized before verification and service startup.
  apply_role_bootstrap
  verify_role_boundary

  backup_identity="$(backup_psql --tuples-only --no-align --command="SELECT session_user || ':' || current_database()")"
  if [[ "${backup_identity}" != "ascendany_backup_login:${DATABASE_NAME}" ]]; then
    printf 'backup direct identity is %s\n' "${backup_identity}" >&2
    exit 1
  fi

  runtime_identity="$(runtime_psql --tuples-only --no-align --command="SELECT session_user || ':' || current_user || ':' || current_database()")"
  if [[ "${runtime_identity}" != "ascendanyd_login:ascendanyd_login:${DATABASE_NAME}" ]]; then
    printf 'runtime PgBouncer identity is %s\n' "${runtime_identity}" >&2
    exit 1
  fi

  if runtime_psql --set=VERBOSITY=verbose --command='SELECT count(*) FROM pg_authid' >"${denial_log}" 2>&1; then
    printf 'runtime login unexpectedly read the privileged authentication catalog\n' >&2
    exit 1
  fi
  if ! rg --quiet '42501|permission denied for (table )?pg_authid' "${denial_log}"; then
    printf 'runtime authentication-catalog denial did not report an authorization failure\n' >&2
    sed -n '1,80p' "${denial_log}" >&2
    exit 1
  fi

  if runtime_psql --set=VERBOSITY=verbose --command='SET ROLE ascendany_owner' >"${denial_log}" 2>&1; then
    printf 'runtime login unexpectedly assumed ascendany_owner\n' >&2
    exit 1
  fi
  if ! rg --quiet '42501|permission denied to set role' "${denial_log}"; then
    printf 'runtime owner denial did not report an authorization failure\n' >&2
    sed -n '1,80p' "${denial_log}" >&2
    exit 1
  fi

  if runtime_psql --set=VERBOSITY=verbose --command='CREATE TABLE ascendany.ci_runtime_ddl_probe (id bigint)' >"${denial_log}" 2>&1; then
    printf 'runtime login unexpectedly created schema DDL\n' >&2
    exit 1
  fi
  if ! rg --quiet '42501|permission denied for schema' "${denial_log}"; then
    printf 'runtime DDL denial did not report an authorization failure\n' >&2
    sed -n '1,80p' "${denial_log}" >&2
    exit 1
  fi
  if [[ "$(admin_psql "${DATABASE_NAME}" --tuples-only --no-align --command="SELECT to_regclass('ascendany.ci_runtime_ddl_probe') IS NULL")" != "t" ]]; then
    printf 'runtime DDL denial left a relation behind\n' >&2
    exit 1
  fi

  if PGPASSWORD="${RESTORE_TEST_PASSWORD}" psql \
    -X \
    --no-password \
    --set=VERBOSITY=verbose \
    --host="${POSTGRES_HOST}" \
    --port="${DIRECT_PORT}" \
    --username=ascendany_restore_login \
    --dbname="${DATABASE_NAME}" \
    --command='SELECT 1' >"${restore_denial_log}" 2>&1; then
    printf 'restore operator unexpectedly connected to the production database\n' >&2
    exit 1
  fi
  if ! rg --quiet '42501|permission denied for database' "${restore_denial_log}"; then
    printf 'restore operator denial did not report a database authorization failure\n' >&2
    sed -n '1,80p' "${restore_denial_log}" >&2
    exit 1
  fi

  admin_psql postgres --command='REVOKE CONNECT ON DATABASE postgres FROM PUBLIC' >/dev/null
  restore_maintenance_identity="$(
    PGPASSWORD="${RESTORE_TEST_PASSWORD}" psql \
      -X \
      --no-password \
      --tuples-only \
      --no-align \
      --host="${POSTGRES_HOST}" \
      --port="${DIRECT_PORT}" \
      --username=ascendany_restore_login \
      --dbname=postgres \
      --command="SELECT session_user || ':' || current_database()"
  )"
  admin_psql postgres --command='GRANT CONNECT ON DATABASE postgres TO PUBLIC' >/dev/null
  if [[ "${restore_maintenance_identity}" != 'ascendany_restore_login:postgres' ]]; then
    printf 'restore maintenance identity is %s\n' "${restore_maintenance_identity:-<missing>}" >&2
    exit 1
  fi
}

expect_role_verifier_failure() {
  local fixture_name="$1"
  local verifier_log="${WORK_ROOT}/role-verifier-${fixture_name}.log"

  if admin_psql "${DATABASE_NAME}" \
    --file="${REPOSITORY_ROOT}/db/roles/verify_v2_roles.sql" \
    >"${verifier_log}" 2>&1; then
    printf 'role verifier accepted the %s drift fixture\n' "${fixture_name}" >&2
    exit 1
  fi
  if [[ ! -s "${verifier_log}" ]]; then
    printf 'role verifier produced no diagnostic for the %s drift fixture\n' "${fixture_name}" >&2
    exit 1
  fi
}

repair_and_verify_role_boundary() {
  apply_role_bootstrap
  verify_role_boundary
}

expect_maintenance_drop_denial() {
  local session_role="$1"
  local fixture_name="$2"
  local denial_log="${WORK_ROOT}/maintenance-drop-${fixture_name}.log"

  if admin_psql postgres \
    --set=VERBOSITY=verbose \
    --set=session_role="${session_role}" \
    >"${denial_log}" 2>&1 <<'SQL'
SET SESSION AUTHORIZATION :"session_role";
SET ROLE ascendany_owner;
DROP DATABASE ascendany_v2;
SQL
  then
    printf '%s unexpectedly dropped the production database through ascendany_owner\n' "${session_role}" >&2
    exit 1
  fi
  if ! rg --quiet '42501|must be owner of database' "${denial_log}"; then
    printf '%s maintenance DROP denial did not report database ownership failure\n' "${session_role}" >&2
    sed -n '1,80p' "${denial_log}" >&2
    exit 1
  fi
}

expect_database_owner_set_role_denial() {
  local session_role="$1"
  local fixture_name="$2"
  local denial_log="${WORK_ROOT}/database-owner-set-role-${fixture_name}.log"

  if admin_psql postgres \
    --set=VERBOSITY=verbose \
    --set=session_role="${session_role}" \
    >"${denial_log}" 2>&1 <<'SQL'
SET SESSION AUTHORIZATION :"session_role";
SET ROLE ascendany_database_owner;
SQL
  then
    printf '%s unexpectedly assumed the isolated production database owner\n' "${session_role}" >&2
    exit 1
  fi
  if ! rg --quiet '42501|permission denied to set role' "${denial_log}"; then
    printf '%s database-owner SET ROLE denial did not report authorization failure\n' "${session_role}" >&2
    sed -n '1,80p' "${denial_log}" >&2
    exit 1
  fi
}

expect_direct_maintenance_drop_denial() {
  local session_role="$1"
  local fixture_name="$2"
  local denial_log="${WORK_ROOT}/maintenance-direct-drop-${fixture_name}.log"

  if admin_psql postgres \
    --set=VERBOSITY=verbose \
    --set=session_role="${session_role}" \
    >"${denial_log}" 2>&1 <<'SQL'
SET SESSION AUTHORIZATION :"session_role";
DROP DATABASE ascendany_v2;
SQL
  then
    printf '%s unexpectedly dropped the production database directly\n' "${session_role}" >&2
    exit 1
  fi
  if ! rg --quiet '42501|must be owner of database' "${denial_log}"; then
    printf '%s direct maintenance DROP denial did not report database ownership failure\n' "${session_role}" >&2
    sed -n '1,80p' "${denial_log}" >&2
    exit 1
  fi
}

assert_role_drift_repair() {
  printf '  RUN  PostgreSQL role and ACL negative fixtures\n'

  admin_psql "${DATABASE_NAME}" \
    --set=administrator_role="${POSTGRES_ADMIN_USER}" >/dev/null <<'SQL'
ALTER DATABASE ascendany_v2 OWNER TO :"administrator_role";
SQL
  expect_role_verifier_failure database-owner
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='ALTER ROLE ascendany_database_owner INHERIT' >/dev/null
  expect_role_verifier_failure database-owner-inherit
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command="ALTER ROLE ascendany_database_owner SET search_path TO public" >/dev/null
  expect_role_verifier_failure database-owner-rolconfig
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='GRANT ascendany_database_owner TO ascendany_restore_login WITH INHERIT FALSE, SET TRUE' >/dev/null
  expect_role_verifier_failure database-owner-membership
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" --command='ALTER ROLE ascendany_runtime NOINHERIT' >/dev/null
  expect_role_verifier_failure runtime-noinherit
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='GRANT UPDATE ON TABLE ascendany.artifacts TO ascendanyd_login' >/dev/null
  expect_role_verifier_failure login-direct-update
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='GRANT CREATE ON SCHEMA public TO PUBLIC' >/dev/null
  expect_role_verifier_failure public-create
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='GRANT TEMPORARY ON DATABASE ascendany_v2 TO ascendany_runtime' >/dev/null
  expect_role_verifier_failure runtime-temporary
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='GRANT EXECUTE ON FUNCTION ascendany.reject_immutable_mutation() TO ascendany_runtime' >/dev/null
  expect_role_verifier_failure routine-execute
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" >/dev/null <<'SQL'
ALTER DEFAULT PRIVILEGES FOR ROLE ascendany_owner IN SCHEMA ascendany
GRANT INSERT ON TABLES TO ascendany_backup;
SQL
  expect_role_verifier_failure default-acl
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='GRANT CONNECT ON DATABASE ascendany_v2 TO ascendany_restore_login' >/dev/null
  expect_role_verifier_failure restore-connect
  repair_and_verify_role_boundary

  admin_psql postgres \
    --command='REVOKE CONNECT ON DATABASE postgres FROM ascendany_restore_login' >/dev/null
  expect_role_verifier_failure restore-maintenance-connect
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='ALTER ROLE ascendany_restore_login INHERIT' >/dev/null
  expect_role_verifier_failure restore-inherit
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='GRANT ascendany_runtime TO ascendany_restore_login' >/dev/null
  expect_role_verifier_failure restore-extra-membership
  repair_and_verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='CREATE ROLE ascendany_unowned_fixture LOGIN NOSUPERUSER' >/dev/null
  expect_role_verifier_failure unowned-role
  admin_psql "${DATABASE_NAME}" \
    --command='DROP ROLE ascendany_unowned_fixture' >/dev/null
  verify_role_boundary

  admin_psql "${DATABASE_NAME}" \
    --command='ALTER ROLE ascendany_runtime IN DATABASE ascendany_v2 SET statement_timeout TO 1' >/dev/null
  expect_role_verifier_failure per-database-role-setting
  admin_psql "${DATABASE_NAME}" \
    --command='ALTER ROLE ascendany_runtime IN DATABASE ascendany_v2 RESET statement_timeout' >/dev/null
  verify_role_boundary

  expect_database_owner_set_role_denial ascendany_migrator_login migrator
  expect_database_owner_set_role_denial ascendany_restore_login restore
  expect_direct_maintenance_drop_denial ascendany_migrator_login migrator
  expect_direct_maintenance_drop_denial ascendany_restore_login restore
  expect_maintenance_drop_denial ascendany_migrator_login migrator
  expect_maintenance_drop_denial ascendany_restore_login restore
  if [[ "$(admin_psql postgres --tuples-only --no-align --command="SELECT count(*) FROM pg_database WHERE datname = '${DATABASE_NAME}'")" != "1" ]]; then
    printf 'maintenance ownership regression removed the production database\n' >&2
    exit 1
  fi

  printf '  PASS PostgreSQL role and ACL negative fixtures\n'
}

seed_admin_fixture() {
  runtime_psql >/dev/null <<'SQL'
INSERT INTO ascendany.auth_accounts (
    public_id,
    username,
    password_phc,
    display_name,
    role,
    auth_revision,
    created_at,
    updated_at
)
VALUES (
    '11111111-1111-4111-8111-111111111111'::uuid,
    'ci_admin',
    'ci-fixture-password-phc',
    'CI Administrator',
    'admin',
    1,
    clock_timestamp(),
    clock_timestamp()
);

INSERT INTO ascendany.auth_sessions (
    public_id,
    account_id,
    auth_revision,
    created_at,
    expires_at,
    last_seen_at
)
SELECT
    '22222222-2222-4222-8222-222222222222'::uuid,
    account_id,
    auth_revision,
    clock_timestamp(),
    clock_timestamp() + interval '1 hour',
    clock_timestamp()
FROM ascendany.auth_accounts
WHERE public_id = '11111111-1111-4111-8111-111111111111'::uuid;
SQL
}

report_real_snapshot_database_summary() {
  local summary
  summary="$(runtime_psql --tuples-only --no-align --field-separator='|' --command="
SELECT exam.source_exam_id,
       snapshot.domain_hash,
       (SELECT count(*) FROM ascendany.pintia_snapshot_problems WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_snapshot_participants WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_rankings WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_ranking_problem_results WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_snapshot_submissions WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_submission_case_results WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.student_analytics WHERE analytics_generation_id = head.current_generation_id),
       (SELECT count(*) FROM ascendany.problem_analytics WHERE analytics_generation_id = head.current_generation_id),
       head.head_revision,
       generation.status
FROM ascendany.logical_exams AS exam
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = exam.active_snapshot_id
JOIN ascendany.analytics_head AS head
  ON head.singleton
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
WHERE exam.platform = 'pintia'
ORDER BY exam.updated_at DESC, exam.exam_id DESC
LIMIT 1")"
  if [[ -z "${summary}" || "${summary}" == *$'\n'* ]]; then
    printf 'real snapshot database summary did not resolve exactly one imported exam\n' >&2
    exit 1
  fi

  local source_exam_id
  local domain_hash
  local problems
  local participants
  local rankings
  local ranking_results
  local submissions
  local case_results
  local student_analytics
  local problem_analytics
  local analytics_head_revision
  local analytics_status
  IFS='|' read -r \
    source_exam_id \
    domain_hash \
    problems \
    participants \
    rankings \
    ranking_results \
    submissions \
    case_results \
    student_analytics \
    problem_analytics \
    analytics_head_revision \
    analytics_status <<<"${summary}"
  printf '  REAL_SNAPSHOT_DATABASE_SUMMARY source_exam_id=%s domain_hash=%s problems=%s participants=%s rankings=%s ranking_results=%s submissions=%s case_results=%s student_analytics=%s problem_analytics=%s analytics_head_revision=%s analytics_status=%s\n' \
    "${source_exam_id}" \
    "${domain_hash}" \
    "${problems}" \
    "${participants}" \
    "${rankings}" \
    "${ranking_results}" \
    "${submissions}" \
    "${case_results}" \
    "${student_analytics}" \
    "${problem_analytics}" \
    "${analytics_head_revision}" \
    "${analytics_status}"
}

run_go_test() {
  local connection_mode="$1"
  local package_path="$2"
  local test_name="$3"
  local log_path="${WORK_ROOT}/${test_name}.json"

  printf '  RUN  %s %s\n' "${package_path}" "${test_name}"
  case "${connection_mode}" in
    runtime)
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
          ASCENDANY_TEST_DATABASE_URL="${RUNTIME_DATABASE_URL}" \
          ASCENDANY_TEST_DATABASE_PASSWORD="${RUNTIME_PASSWORD}" \
          ASCENDANY_REAL_PINTIA_SNAPSHOT_PATH="${REAL_SNAPSHOT_PATH}" \
          ASCENDANY_REAL_ANALYTICS_CONFIG_PATH="${ANALYTICS_CONFIG_PATH}" \
          PGPASSFILE="${RUNTIME_PGPASS_FILE}" \
          "${GO_BINARY}" test -count=1 -json "${package_path}" -run "^${test_name}$"
      ) >"${log_path}" 2>&1; then
        cat "${log_path}" >&2
        return 1
      fi
      ;;
    migrator)
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
          ASCENDANY_MIGRATE_TEST_DATABASE_URL="${MIGRATOR_DATABASE_URL}" \
          PGPASSFILE="${MIGRATOR_PGPASS_FILE}" \
          "${GO_BINARY}" test -count=1 -json "${package_path}" -run "^${test_name}$"
      ) >"${log_path}" 2>&1; then
        cat "${log_path}" >&2
        return 1
      fi
      ;;
    *)
      printf 'unknown integration connection mode: %s\n' "${connection_mode}" >&2
      return 1
      ;;
  esac

  if [[ ! -s "${log_path}" ]]; then
    printf 'integration test emitted no events: %s %s\n' "${package_path}" "${test_name}" >&2
    return 1
  fi

  if ! rg --fixed-strings --quiet "\"Action\":\"run\",\"Package\":\"github.com/kkkzbh/AscendAny/backend/${package_path#./}\",\"Test\":\"${test_name}\"" "${log_path}"; then
    printf 'integration test was not executed: %s %s\n' "${package_path}" "${test_name}" >&2
    cat "${log_path}" >&2
    return 1
  fi
  if rg --fixed-strings --quiet "\"Action\":\"skip\"" "${log_path}"; then
    printf 'integration test or subtest skipped: %s %s\n' "${package_path}" "${test_name}" >&2
    cat "${log_path}" >&2
    return 1
  fi
  if ! rg --fixed-strings "\"Action\":\"pass\"" "${log_path}" | rg --fixed-strings --quiet "\"Test\":\"${test_name}\""; then
    printf 'integration test has no pass event: %s %s\n' "${package_path}" "${test_name}" >&2
    cat "${log_path}" >&2
    return 1
  fi
  printf '  PASS %s\n' "${test_name}"
}

readonly -a TEST_CASES=(
  'fresh-migrator|./internal/migrate|TestPostgresFreshMigrationAndIdempotentReentry|none'
  'runtime|./internal/achievement|TestPostgresAchievementReadMatchesCurrentDatabaseSnapshot|none'
  'runtime|./internal/administration|TestPostgresAdministrationReadModelsUseOneActiveAdminPrincipal|admin'
  'runtime|./internal/administration|TestPostgresAccountDisableOrdersAfterConcurrentLoginSession|none'
  'runtime|./internal/administration|TestPostgresMutualAdminDisableUsesOneAccountLockOrder|none'
  'runtime|./internal/agentnotes|TestPostgresAgentNoteOwnedLifecycleAndFencing|none'
  'runtime|./internal/analytics|TestPostgresAnalyticsClaimReclaimPublishAndReplacementReuse|none'
  'runtime|./internal/auth|TestPostgresEnrollmentIssueConcurrentClaimAndRevocation|none'
  'runtime|./internal/chatagent|TestPostgresAgentRunIsIdempotentFencedAndAtomicallyPublished|none'
  'runtime|./internal/configuration|TestPostgresConfigurationVersionLifecycle|admin'
  'runtime|./internal/examcatalog|TestPostgresCatalogReadsOneImportedSnapshot|catalog'
  'runtime|./internal/examgeneration|TestPostgresCurrentGenerationUsesActiveSnapshotAndRevalidatesPrincipal|none'
  'runtime|./internal/feedback|TestPostgresAuthenticatedFeedbackIsIdempotentAndRateLimited|none'
  'runtime|./internal/importing|TestPostgresPintiaImportVertical|none'
  'runtime|./internal/importing|TestPostgresRealPintiaSnapshotImport|none'
  'runtime|./internal/migrate|TestPostgresImportLifecycleCannotBeBypassed|none'
  'migrator|./internal/migrate|TestPostgresAchievementRuleVersionsAreAppendOnly|none'
  'runtime|./internal/oj|TestPostgresOJProblemSubmissionAndFencedJudgeLifecycle|none'
  'runtime|./internal/oj|TestPostgresOJConcurrentNewSlugConvergesOnOneHead|none'
  'runtime|./internal/recommendation|TestPostgresRecommendationTrainingLifecycleAndStudentFreshness|none'
  'runtime|./internal/recommendation|TestPostgresRecommendationOperatorBootstrapReviewQueueAndEvents|none'
  'runtime|./internal/recommendation|TestPostgresTrainerAgentTerminalReceiptsFenceAndReplay|none'
  'runtime|./internal/studentanalytics|TestPostgresSelfAnalyticsReadPath|none'
)

audit_test_manifest() {
  local discovered="${WORK_ROOT}/discovered-tests.txt"
  local expected="${WORK_ROOT}/expected-tests.txt"
  local entry
  local ignored_mode
  local package_path
  local test_name
  local ignored_fixture
  local integration_file
  local source_record
  local source_file
  local source_text

  : >"${discovered}"
  while IFS= read -r source_record; do
    source_file="${source_record%%:*}"
    source_text="${source_record#*:}"
    source_text="${source_text#*:}"
    package_path="./${source_file#"${BACKEND_ROOT}/"}"
    package_path="${package_path%/*}"
    test_name="${source_text#func }"
    test_name="${test_name%%(*}"
    printf '%s|%s\n' "${package_path}" "${test_name}" >>"${discovered}"
  done < <(
    while IFS= read -r integration_file; do
      rg --line-number --with-filename '^func Test[^ (]+\(' "${integration_file}"
    done < <(
      rg --files-with-matches \
        'ASCENDANY_(TEST|MIGRATE_TEST)_DATABASE_URL' \
        "${BACKEND_ROOT}" \
        --glob '*_test.go'
    )
  )

  : >"${expected}"
  for entry in "${TEST_CASES[@]}"; do
    IFS='|' read -r ignored_mode package_path test_name ignored_fixture <<<"${entry}"
    printf '%s|%s\n' "${package_path}" "${test_name}" >>"${expected}"
  done

  sort -u -o "${discovered}" "${discovered}"
  sort -u -o "${expected}" "${expected}"
  if ! diff -u "${discovered}" "${expected}"; then
    printf 'PostgreSQL integration manifest does not cover the env-gated test surface\n' >&2
    exit 1
  fi
}

main() {
  local entry
  local mode
  local package_path
  local test_name
  local fixture

  verify_infrastructure
  audit_test_manifest

  printf 'Building the embedded v2 migrator once\n'
  (cd -- "${BACKEND_ROOT}" && go build -o "${MIGRATOR_BINARY}" ./cmd/ascendany-migrate)

  printf 'Verifying the production and integration PgBouncer pool configuration contracts\n'
  (
    cd -- "${BACKEND_ROOT}"
    ASCENDANY_INTEGRATION_RUNTIME_DATABASE_URL_CONTRACT="${RUNTIME_DATABASE_URL}" \
      go test -count=1 ./internal/database \
      -run '^(TestParsePoolConfigIsSafeForPgBouncerTransactionPooling|TestIntegrationRuntimeURLIsSafeForPgBouncerTransactionPooling)$'
  )

  for entry in "${TEST_CASES[@]}"; do
    IFS='|' read -r mode package_path test_name fixture <<<"${entry}"
    printf '\nFresh exact %s for %s\n' "${DATABASE_NAME}" "${test_name}"
    reset_exact_database

    if [[ "${mode}" == "fresh-migrator" ]]; then
      run_go_test migrator "${package_path}" "${test_name}"
      assert_schema_roles_and_ports
      assert_role_drift_repair
      continue
    fi

    run_migrations
    assert_schema_roles_and_ports

    case "${fixture}" in
      none)
        ;;
      admin)
        seed_admin_fixture
        ;;
      catalog)
        run_go_test runtime ./internal/importing TestPostgresRealPintiaSnapshotImport
        report_real_snapshot_database_summary
        seed_admin_fixture
        ;;
      *)
        printf 'unknown integration fixture owner: %s\n' "${fixture}" >&2
        exit 1
        ;;
    esac

    run_go_test "${mode}" "${package_path}" "${test_name}"
    if [[ "${test_name}" == "TestPostgresRealPintiaSnapshotImport" ]]; then
      report_real_snapshot_database_summary
    fi
  done

  printf '\nAll %d env-gated PostgreSQL integration tests executed without skips.\n' "${#TEST_CASES[@]}"
}

main "$@"
