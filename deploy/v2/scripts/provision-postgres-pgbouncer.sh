#!/usr/bin/bash -p
set -Eeuo pipefail

readonly SELF="$(/usr/bin/readlink -e -- "${BASH_SOURCE[0]}")"
if [[ -z "$SELF" ]]; then
  /usr/bin/printf '%s\n' 'FAIL [identity]: provisioner path is not canonical' >&2
  exit 1
fi
if [[ "${ASCENDANY_PROVISION_CLEAN_ENV-}" != 1 ]]; then
  exec /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_PROVISION_CLEAN_ENV=1 \
    /usr/bin/bash -p "$SELF" "$@"
fi

environment_is_clean=1
while IFS= read -r -d '' entry; do
  environment_name="${entry%%=*}"
  case "$environment_name" in
    ASCENDANY_PROVISION_CLEAN_ENV|LC_ALL|PATH|PWD|SHLVL|_)
      ;;
    *)
      environment_is_clean=0
      ;;
  esac
done < <(/usr/bin/env -0)
if [[ "${PATH-}" != /usr/bin:/bin || "${LC_ALL-}" != C ||
      "$environment_is_clean" != 1 ]]; then
  /usr/bin/printf '%s\n' 'FAIL [environment]: provisioning requires the canonical clean environment' >&2
  exit 1
fi
builtin unset ASCENDANY_PROVISION_CLEAN_ENV BASH_ENV ENV CDPATH GLOBIGNORE \
  POSIXLY_CORRECT TMPDIR environment_is_clean environment_name entry
builtin export -n SHELLOPTS BASHOPTS
builtin export PATH=/usr/bin:/bin LC_ALL=C
umask 077

readonly RELEASE_ROOT="$(builtin cd -- "$(/usr/bin/dirname -- "$SELF")/.." && builtin pwd -P)"
readonly ROLE_BOOTSTRAP="$RELEASE_ROOT/db/roles/001_v2_roles.sql"
readonly POOL_CONFIG_SOURCE="$RELEASE_ROOT/config/pgbouncer.ini"
readonly POOL_HBA_SOURCE="$RELEASE_ROOT/config/pgbouncer-hba.conf"
readonly POSTGRES_HBA_SOURCE="$RELEASE_ROOT/config/postgresql-hba.conf"
readonly POSTGRES_IDENT_SOURCE="$RELEASE_ROOT/config/postgresql-ident.conf"
readonly PACKAGE_LOCK="$RELEASE_ROOT/config/fedora-runtime-packages.json"
readonly POOL_UNIT_SOURCE="$RELEASE_ROOT/systemd/ascendany-pgbouncer.service"

readonly POSTGRES_CONTAINER=ascendany-postgres
readonly POSTGRES_DBA_ROLE=postgres
readonly V2_DATABASE=ascendany_v2
readonly POSTGRES_NETWORK=podman
readonly POSTGRES_GATEWAY=10.88.0.1
readonly POSTGRES_ADDRESS=10.88.0.2
readonly POSTGRES_SUBNET=10.88.0.0/16
readonly POSTGRES_HBA_PATH=/var/lib/postgresql/data/pg_hba.conf
readonly POSTGRES_IDENT_PATH=/var/lib/postgresql/data/pg_ident.conf

readonly PACKAGE_POOL_UNIT=pgbouncer.service
readonly TARGET_POOL_UNIT=ascendany-pgbouncer.service
readonly TARGET_POOL_UNIT_PATH=/etc/systemd/system/ascendany-pgbouncer.service
readonly RESERVED_POOL_CONTAINER=ascendany-pgbouncer

readonly INPUT_ROOT=/run/ascendany-v2-provision
readonly CREDENTIAL_ROOT=/etc/ascendany/credentials
readonly CATALOG_PUBLISHER_CREDENTIAL_ROOT=/etc/ascendany-catalog-publisher/credentials
readonly POOL_PARENT=/opt/ascendany/infra
readonly POOL_CONFIG_ROOT="$POOL_PARENT/pgbouncer"
readonly RECEIPT_ROOT=/var/lib/ascendany-v2-provision
readonly RECEIPT_PATH="$RECEIPT_ROOT/receipt"
readonly LOCK_PATH=/run/lock/ascendany-v2-provision.lock

readonly RUNTIME_PASSWORD_FILE="$INPUT_ROOT/runtime_db_password"
readonly MIGRATOR_PASSWORD_FILE="$INPUT_ROOT/migrator_db_password"
readonly BACKUP_PASSWORD_FILE="$INPUT_ROOT/backup_db_password"
readonly RESTORE_PASSWORD_FILE="$INPUT_ROOT/restore_db_password"
readonly CATALOG_PUBLISHER_PASSWORD_FILE="$INPUT_ROOT/catalog_publisher_db_password"

stage_root=''
pool_stage=''
receipt_stage=''
pool_started=0

usage() {
  /usr/bin/cat >&2 <<'USAGE'
Usage:
  sudo /opt/ascendany/v2/scripts/provision-postgres-pgbouncer.sh \
    --postgres-container ascendany-postgres \
    --postgres-dba-role postgres \
    --confirm-fresh-database ascendany_v2

Required protected plaintext inputs (root:root 0600, one distinct 32..128 byte
ASCII value per file, no newline):
  /run/ascendany-v2-provision/runtime_db_password
  /run/ascendany-v2-provision/migrator_db_password
  /run/ascendany-v2-provision/backup_db_password
  /run/ascendany-v2-provision/restore_db_password
  /run/ascendany-v2-provision/catalog_publisher_db_password

Preconditions:
  - package pgbouncer.service is masked and inactive;
  - the release-owned ascendany-pgbouncer.service is installed, disabled and inactive;
  - TCP ports 6432 and 18000 have no listeners;
  - the reserved ascendany-pgbouncer container name and pool configuration are absent;
  - ascendany-postgres is a running PostgreSQL 17 container with the exact
    Podman network and an explicit local postgres DBA channel;
  - the cluster contains only postgres/template databases and the postgres
    bootstrap role. ascendany_v2 and every v2 managed role must be absent.

The command is deliberately one-way. Any partial database or filesystem state
causes the next invocation to fail and must be reviewed and removed through the
explicit DBA/operator boundary before a new attempt.
USAGE
}

fail() {
  /usr/bin/printf 'FAIL [%s]: %s\n' "$1" "$2" >&2
  exit 1
}

pass() {
  /usr/bin/printf 'PASS [%s]\n' "$1"
}

require_exact_args() {
  if [[ "$#" == 1 && "$1" == --help ]]; then
    usage
    exit 0
  fi
  [[ "$#" == 6 ]] || { usage; fail arguments 'six exact argument tokens are required'; }
  [[ "$1" == --postgres-container && "$2" == "$POSTGRES_CONTAINER" ]] ||
    fail arguments 'PostgreSQL container identity differs from the closed contract'
  [[ "$3" == --postgres-dba-role && "$4" == "$POSTGRES_DBA_ROLE" ]] ||
    fail arguments 'PostgreSQL DBA role differs from the explicit administration contract'
  [[ "$5" == --confirm-fresh-database && "$6" == "$V2_DATABASE" ]] ||
    fail arguments 'fresh database confirmation is invalid'
}

require_regular_file() {
  local path="$1" metadata="$2"
  [[ -f "$path" && ! -L "$path" && "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null || true)" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path" 2>/dev/null || true)" == "$metadata" ]] ||
    fail release "file identity differs: $path"
}

require_masked_inactive_unit() {
  local unit="$1"
  [[ "$(/usr/bin/systemctl is-enabled "$unit" 2>/dev/null || true)" == masked ]] ||
    fail systemd "$unit must be masked"
  [[ "$(/usr/bin/systemctl is-active "$unit" 2>/dev/null || true)" == inactive ]] ||
    fail systemd "$unit must be inactive"
  [[ "$(/usr/bin/systemctl show -P MainPID "$unit" 2>/dev/null || true)" == 0 ]] ||
    fail systemd "$unit retains a process"
}

require_unused_port() {
  local port="$1"
  [[ -z "$(/usr/bin/ss -H -ltn "sport = :$port" 2>/dev/null || true)" ]] ||
    fail listener "TCP port $port must be unused before provisioning"
}

require_password_file() {
  local path="$1" value size
  [[ -f "$path" && ! -L "$path" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path" 2>/dev/null || true)" == 0:0:600:1 ]] ||
    fail password_input "password input identity differs: $path"
  value="$(<"$path")"
  size="$(/usr/bin/stat -Lc '%s' -- "$path")"
  [[ "$size" -ge 32 && "$size" -le 128 && "${#value}" == "$size" &&
     "$value" =~ ^[A-Za-z0-9._~+/@%=-]+$ ]] ||
    fail password_input "password input has a noncanonical value: $path"
}

postgres_psql() {
  /usr/bin/podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    /usr/bin/env -i \
      HOME=/var/lib/postgresql \
      PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
      LC_ALL=C \
      /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
        --username="$POSTGRES_DBA_ROLE" "$@"
}

cleanup() {
  local status=$?
  trap - EXIT
  if (( pool_started == 1 )); then
    /usr/bin/systemctl stop "$TARGET_POOL_UNIT" >/dev/null 2>&1 || true
  fi
  if [[ -n "$stage_root" && -d "$stage_root" && ! -L "$stage_root" ]]; then
    /usr/bin/rm -f -- "$stage_root"/*
    /usr/bin/rmdir -- "$stage_root" 2>/dev/null || true
  fi
  if [[ -n "$pool_stage" && -d "$pool_stage" && ! -L "$pool_stage" ]]; then
    /usr/bin/rm -f -- "$pool_stage"/*
    /usr/bin/rmdir -- "$pool_stage" 2>/dev/null || true
  fi
  if [[ -n "$receipt_stage" && -d "$receipt_stage" && ! -L "$receipt_stage" ]]; then
    /usr/bin/rm -f -- "$receipt_stage"/*
    /usr/bin/rmdir -- "$receipt_stage" 2>/dev/null || true
  fi
  exit "$status"
}

verify_release_inputs() {
  require_regular_file "$ROLE_BOOTSTRAP" 0:0:644:1
  require_regular_file "$POOL_CONFIG_SOURCE" 0:0:644:1
  require_regular_file "$POOL_HBA_SOURCE" 0:0:644:1
  require_regular_file "$POSTGRES_HBA_SOURCE" 0:0:644:1
  require_regular_file "$POSTGRES_IDENT_SOURCE" 0:0:644:1
  require_regular_file "$PACKAGE_LOCK" 0:0:644:1
  require_regular_file "$POOL_UNIT_SOURCE" 0:0:644:1
  require_regular_file "$TARGET_POOL_UNIT_PATH" 0:0:644:1
  /usr/bin/cmp -s -- "$POOL_UNIT_SOURCE" "$TARGET_POOL_UNIT_PATH" ||
    fail release 'installed PgBouncer unit differs from the release bytes'

  /usr/bin/jq -e '
    type == "object" and
    .schema == "ascendany.fedora-runtime-packages.v1" and
    .fedoraRelease == 44 and .architecture == "x86_64" and
    (.packages.pgbouncer | keys == ["files", "nevra", "rpmSHA256", "signingFingerprint"]) and
    (.packages.pgbouncer.files | length == 1) and
    .packages.pgbouncer.files[0].path == "/usr/bin/pgbouncer" and
    .packages.pgbouncer.files[0].mode == "0755" and
    .packages.pgbouncer.files[0].owner == "root" and
    .packages.pgbouncer.files[0].group == "root"
  ' "$PACKAGE_LOCK" >/dev/null || fail package 'PgBouncer package lock violates its closed schema'

  local expected_nevra expected_sha expected_size expected_mode package_verify_output package_verify_status=0
  expected_nevra="$(/usr/bin/jq -er '.packages.pgbouncer.nevra' "$PACKAGE_LOCK")"
  expected_sha="$(/usr/bin/jq -er '.packages.pgbouncer.files[0].sha256' "$PACKAGE_LOCK")"
  expected_size="$(/usr/bin/jq -er '.packages.pgbouncer.files[0].size' "$PACKAGE_LOCK")"
  expected_mode="$(/usr/bin/jq -er '.packages.pgbouncer.files[0].mode' "$PACKAGE_LOCK")"
  [[ "$(/usr/bin/rpm -q --qf '%{NEVRA}' pgbouncer 2>/dev/null || true)" == "$expected_nevra" ]] ||
    fail package 'installed PgBouncer NEVRA differs from the release lock'
  [[ -x /usr/bin/pgbouncer && ! -L /usr/bin/pgbouncer &&
     "$(/usr/bin/stat -Lc '%u:%g:0%a:%s:%h' /usr/bin/pgbouncer)" == "0:0:$expected_mode:$expected_size:1" &&
     "$(/usr/bin/sha256sum /usr/bin/pgbouncer | /usr/bin/awk '{print $1}')" == "$expected_sha" ]] ||
    fail package 'installed PgBouncer binary differs from the release lock'
  package_verify_output="$(/usr/bin/rpm --verify pgbouncer 2>&1)" || package_verify_status=$?
  [[ "$package_verify_status" == 0 && -z "$package_verify_output" ]] ||
    fail package 'installed PgBouncer package verification failed'
  [[ "$(/usr/bin/pgbouncer --version 2>&1 | /usr/bin/head -n 1)" == 'PgBouncer 1.25.2' ]] ||
    fail package 'installed PgBouncer version differs from the release lock'
}

verify_service_boundaries() {
  require_masked_inactive_unit "$PACKAGE_POOL_UNIT"
  [[ "$(/usr/bin/systemctl is-enabled "$TARGET_POOL_UNIT" 2>/dev/null || true)" == disabled ]] ||
    fail systemd 'release-owned PgBouncer unit must be disabled before provisioning'
  [[ "$(/usr/bin/systemctl is-active "$TARGET_POOL_UNIT" 2>/dev/null || true)" == inactive ]] ||
    fail systemd 'release-owned PgBouncer unit must be inactive before provisioning'
  [[ "$(/usr/bin/systemctl show -P MainPID "$TARGET_POOL_UNIT" 2>/dev/null || true)" == 0 ]] ||
    fail systemd 'release-owned PgBouncer unit retains a process'
  [[ "$(/usr/bin/systemctl show -P NeedDaemonReload "$TARGET_POOL_UNIT" 2>/dev/null || true)" == no ]] ||
    fail systemd 'systemd has a pending daemon reload'
  [[ "$(/usr/bin/systemctl show -P FragmentPath "$TARGET_POOL_UNIT" 2>/dev/null || true)" == "$TARGET_POOL_UNIT_PATH" ]] ||
    fail systemd 'release-owned PgBouncer unit path differs'

  if /usr/bin/podman container exists "$RESERVED_POOL_CONTAINER" >/dev/null 2>&1; then
    fail container 'reserved PgBouncer container name already exists'
  fi
  require_unused_port 6432
  require_unused_port 18000
}

verify_output_boundaries() {
  [[ -d "$CREDENTIAL_ROOT" && ! -L "$CREDENTIAL_ROOT" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$CREDENTIAL_ROOT")" == 0:0:700 ]] ||
    fail credential_output 'credential root identity differs'
  local output
  for output in runtime_db_password.cred migrator_db_password.cred backup_db_password.cred \
    restore_db_password.cred pgbouncer_userlist.cred; do
    [[ ! -e "$CREDENTIAL_ROOT/$output" && ! -L "$CREDENTIAL_ROOT/$output" ]] ||
      fail credential_output "credential output already exists: $output"
  done
  [[ -d "$CATALOG_PUBLISHER_CREDENTIAL_ROOT" && ! -L "$CATALOG_PUBLISHER_CREDENTIAL_ROOT" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$CATALOG_PUBLISHER_CREDENTIAL_ROOT")" == 0:0:700 ]] ||
    fail credential_output 'catalog publisher credential root identity differs'
  [[ ! -e "$CATALOG_PUBLISHER_CREDENTIAL_ROOT/catalog_publisher_db_password.cred" &&
     ! -L "$CATALOG_PUBLISHER_CREDENTIAL_ROOT/catalog_publisher_db_password.cred" ]] ||
    fail credential_output 'catalog publisher database credential output already exists'

  [[ -d /opt/ascendany && ! -L /opt/ascendany &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' /opt/ascendany)" == 0:0:755 ]] ||
    fail pool_output '/opt/ascendany identity differs'
  [[ ! -e "$POOL_CONFIG_ROOT" && ! -L "$POOL_CONFIG_ROOT" ]] ||
    fail pool_output 'PgBouncer configuration output already exists'
  [[ ! -e "$RECEIPT_ROOT" && ! -L "$RECEIPT_ROOT" ]] ||
    fail receipt 'provisioning receipt already exists'
}

verify_password_inputs() {
  [[ -d "$INPUT_ROOT" && ! -L "$INPUT_ROOT" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' "$INPUT_ROOT")" == 0:0:700 ]] ||
    fail password_input 'password input root identity differs'
  local entries
  entries="$(/usr/bin/find "$INPUT_ROOT" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C /usr/bin/sort)"
  [[ "$entries" == $'backup_db_password|f\ncatalog_publisher_db_password|f\nmigrator_db_password|f\nrestore_db_password|f\nruntime_db_password|f' ]] ||
    fail password_input 'password input root has an unexpected entry set'
  require_password_file "$RUNTIME_PASSWORD_FILE"
  require_password_file "$MIGRATOR_PASSWORD_FILE"
  require_password_file "$BACKUP_PASSWORD_FILE"
  require_password_file "$RESTORE_PASSWORD_FILE"
  require_password_file "$CATALOG_PUBLISHER_PASSWORD_FILE"
  local -a password_files=(
    "$RUNTIME_PASSWORD_FILE" "$MIGRATOR_PASSWORD_FILE"
    "$BACKUP_PASSWORD_FILE" "$RESTORE_PASSWORD_FILE"
    "$CATALOG_PUBLISHER_PASSWORD_FILE"
  )
  local left right
  for ((left = 0; left < ${#password_files[@]}; left++)); do
    for ((right = left + 1; right < ${#password_files[@]}; right++)); do
      /usr/bin/cmp -s "${password_files[$left]}" "${password_files[$right]}" &&
        fail password_input 'database password inputs must be pairwise distinct'
    done
  done
}

verify_postgres_container() {
  /usr/bin/podman container exists "$POSTGRES_CONTAINER" >/dev/null 2>&1 ||
    fail postgres 'PostgreSQL container is missing'
  [[ "$(/usr/bin/podman inspect --format '{{.State.Running}}' "$POSTGRES_CONTAINER" 2>/dev/null || true)" == true ]] ||
    fail postgres 'PostgreSQL container is inactive'

  /usr/bin/podman inspect "$POSTGRES_CONTAINER" |
    /usr/bin/jq -e --arg network "$POSTGRES_NETWORK" --arg gateway "$POSTGRES_GATEWAY" --arg address "$POSTGRES_ADDRESS" '
      type == "array" and length == 1 and
      (.[0].NetworkSettings.Networks | keys) == [$network] and
      .[0].NetworkSettings.Networks[$network].Gateway == $gateway and
      .[0].NetworkSettings.Networks[$network].IPAddress == $address and
      .[0].NetworkSettings.Networks[$network].IPPrefixLen == 16
    ' >/dev/null || fail postgres 'PostgreSQL container network attachment differs'
  /usr/bin/podman network inspect "$POSTGRES_NETWORK" |
    /usr/bin/jq -e --arg network "$POSTGRES_NETWORK" --arg gateway "$POSTGRES_GATEWAY" --arg subnet "$POSTGRES_SUBNET" '
      type == "array" and length == 1 and
      .[0].name == $network and .[0].driver == "bridge" and
      .[0].network_interface == "podman0" and .[0].internal == false and
      .[0].ipv6_enabled == false and
      .[0].subnets == [{"subnet": $subnet, "gateway": $gateway}]
    ' >/dev/null || fail postgres 'PostgreSQL Podman network differs'

  local listener
  listener="$(/usr/bin/ss -H -ltn 'sport = :5432' 2>/dev/null || true)"
  [[ -n "$listener" && "$listener" != *$'\n'* && "$listener" == *'127.0.0.1:5432'* ]] ||
    fail postgres 'PostgreSQL must expose exactly one loopback TCP listener on port 5432'

  /usr/bin/podman exec --user root "$POSTGRES_CONTAINER" /bin/sh -ceu '
    for path in "$1" "$2"; do
      test -f "$path" && test ! -L "$path"
    done
  ' sh "$POSTGRES_HBA_PATH" "$POSTGRES_IDENT_PATH" ||
    fail postgres 'PostgreSQL access-file paths are not regular files'

  local cluster_state
  cluster_state="$(postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT current_user,
       current_setting('server_version_num')::int / 10000,
       current_setting('password_encryption'),
       current_setting('fsync'),
       current_setting('synchronous_commit'),
       current_setting('full_page_writes'),
       current_setting('hba_file'),
       current_setting('ident_file'),
       (SELECT string_agg(rolname, ',' ORDER BY rolname) FROM pg_roles WHERE rolname !~ '^pg_'),
       (SELECT string_agg(datname, ',' ORDER BY datname) FROM pg_database),
       (SELECT count(*) FROM pg_db_role_setting),
       (SELECT count(*) FROM pg_replication_slots),
       (SELECT rolcanlogin::text || ',' || rolsuper::text || ',' || rolinherit::text || ',' ||
               rolcreatedb::text || ',' || rolcreaterole::text || ',' || rolreplication::text || ',' ||
               rolbypassrls::text
        FROM pg_roles WHERE rolname = 'postgres');
SQL
)"
  [[ "$cluster_state" == "postgres|17|scram-sha-256|on|on|on|$POSTGRES_HBA_PATH|$POSTGRES_IDENT_PATH|postgres|postgres,template0,template1|0|0|true,true,true,true,true,true,true" ]] ||
    fail postgres 'PostgreSQL DBA, durability, role or database entry state is not fresh'
}

encrypt_credential() {
  local credential_name="$1" input="$2" output_name="$3"
  local output="$stage_root/$output_name"
  /usr/bin/systemd-creds encrypt --name="$credential_name" "$input" "$output" >/dev/null 2>&1 ||
    fail credential_encryption "failed to encrypt $credential_name"
  /usr/bin/chown 0:0 "$output"
  /usr/bin/chmod 0400 "$output"
  [[ "$(/usr/bin/stat -Lc '%u:%g:%a:%h' "$output")" == 0:0:400:1 ]] ||
    fail credential_encryption "encrypted credential metadata differs: $output_name"
  /usr/bin/systemd-creds decrypt --name="$credential_name" "$output" - 2>/dev/null |
    /usr/bin/cmp -s - "$input" || fail credential_encryption "encrypted credential verification failed: $credential_name"
}

set_role_password() {
  local role="$1" password_file="$2"
  if ! {
    /usr/bin/cat -- "$password_file"
    /usr/bin/printf '\n'
    /usr/bin/cat -- "$password_file"
    /usr/bin/printf '\n'
  } | postgres_psql --dbname=postgres --command="\\password $role" >/dev/null 2>&1; then
    fail database_password "failed to provision password for $role"
  fi
}

create_v2_database_and_roles() {
  postgres_psql --dbname=postgres >/dev/null <<'SQL'
BEGIN;
ALTER ROLE postgres WITH LOGIN SUPERUSER NOINHERIT NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1 PASSWORD NULL;
ALTER ROLE postgres RESET ALL;
COMMENT ON ROLE postgres IS 'ascendany.postgres.dba.v2';
CREATE ROLE ascendany_database_owner NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
COMMIT;
CREATE DATABASE ascendany_v2 OWNER ascendany_database_owner TEMPLATE template0;
COMMENT ON DATABASE ascendany_v2 IS 'ascendany.v2.fresh';
SQL
  postgres_psql --dbname="$V2_DATABASE" <"$ROLE_BOOTSTRAP" >/dev/null
  set_role_password ascendanyd_login "$RUNTIME_PASSWORD_FILE"
  set_role_password ascendany_migrator_login "$MIGRATOR_PASSWORD_FILE"
  set_role_password ascendany_backup_login "$BACKUP_PASSWORD_FILE"
  set_role_password ascendany_restore_login "$RESTORE_PASSWORD_FILE"
  set_role_password ascendany_catalog_publisher_login "$CATALOG_PUBLISHER_PASSWORD_FILE"

  local created_state
  created_state="$(postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'ascendany_v2'),
       (SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname = 'ascendany_v2'),
       (SELECT string_agg(rolname, ',' ORDER BY rolname) FROM pg_roles WHERE rolname !~ '^pg_'),
       (SELECT count(*) = 5 AND count(DISTINCT rolpassword) = 5
        FROM pg_authid
        WHERE rolname = ANY(ARRAY[
          'ascendanyd_login', 'ascendany_migrator_login',
          'ascendany_backup_login', 'ascendany_restore_login',
          'ascendany_catalog_publisher_login'
        ])
          AND rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'),
       (SELECT rolpassword IS NULL FROM pg_authid WHERE rolname = 'postgres'),
       (SELECT shobj_description(oid, 'pg_authid') FROM pg_roles WHERE rolname = 'postgres');
SQL
)"
  [[ "$created_state" == 'ascendany_database_owner|ascendany.v2.fresh|ascendany_backup,ascendany_backup_login,ascendany_catalog_publisher,ascendany_catalog_publisher_login,ascendany_database_owner,ascendany_migrator,ascendany_migrator_login,ascendany_owner,ascendany_restore_login,ascendany_runtime,ascendanyd_login,postgres|t|t|ascendany.postgres.dba.v2' ]] ||
    fail v2_database 'created v2 database, role set or SCRAM credentials differ'
}

generate_pool_credential() {
  local plaintext="$stage_root/pgbouncer-userlist"
  {
    /usr/bin/printf '%s' '"ascendany_catalog_publisher_login" "'
    /usr/bin/cat -- "$CATALOG_PUBLISHER_PASSWORD_FILE"
    /usr/bin/printf '%s' $'"\n"ascendanyd_login" "'
    /usr/bin/cat -- "$RUNTIME_PASSWORD_FILE"
    /usr/bin/printf '%s\n' '"'
  } >"$plaintext"
  [[ "$(/usr/bin/wc -l <"$plaintext")" == 2 ]] ||
    fail pgbouncer_auth 'PgBouncer userlist generation did not produce exactly two plaintext password identities'
  /usr/bin/chown 0:0 "$plaintext"
  /usr/bin/chmod 0600 "$plaintext"
  encrypt_credential pgbouncer_userlist "$plaintext" pgbouncer_userlist.cred
  /usr/bin/rm -f -- "$plaintext"
}

publish_credentials() {
  local name temporary
  for name in runtime_db_password.cred migrator_db_password.cred backup_db_password.cred \
    restore_db_password.cred pgbouncer_userlist.cred; do
    temporary="$CREDENTIAL_ROOT/.${name}.$$"
    [[ ! -e "$temporary" && ! -L "$temporary" ]] ||
      fail credential_output "credential temporary output exists: $temporary"
    /usr/bin/install -o root -g root -m 0400 "$stage_root/$name" "$temporary"
    /usr/bin/sync -f "$temporary"
    /usr/bin/mv -T "$temporary" "$CREDENTIAL_ROOT/$name"
    /usr/bin/sync -f "$CREDENTIAL_ROOT"
  done
  temporary="$CATALOG_PUBLISHER_CREDENTIAL_ROOT/.catalog_publisher_db_password.cred.$$"
  [[ ! -e "$temporary" && ! -L "$temporary" ]] ||
    fail credential_output "credential temporary output exists: $temporary"
  /usr/bin/install -o root -g root -m 0400 \
    "$stage_root/catalog_publisher_db_password.cred" "$temporary"
  /usr/bin/sync -f "$temporary"
  /usr/bin/mv -T "$temporary" \
    "$CATALOG_PUBLISHER_CREDENTIAL_ROOT/catalog_publisher_db_password.cred"
  /usr/bin/sync -f "$CATALOG_PUBLISHER_CREDENTIAL_ROOT"
}

publish_pool_config() {
  /usr/bin/install -d -o root -g root -m 0755 "$POOL_PARENT"
  pool_stage="$POOL_PARENT/.pgbouncer.stage.$$"
  [[ ! -e "$pool_stage" && ! -L "$pool_stage" ]] ||
    fail pool_output 'PgBouncer staging path already exists'
  /usr/bin/install -d -o root -g root -m 0755 "$pool_stage"
  /usr/bin/install -o root -g root -m 0644 "$POOL_CONFIG_SOURCE" "$pool_stage/pgbouncer.ini"
  /usr/bin/install -o root -g root -m 0644 "$POOL_HBA_SOURCE" "$pool_stage/pgbouncer-hba.conf"
  /usr/bin/sync -f "$pool_stage/pgbouncer.ini"
  /usr/bin/sync -f "$pool_stage/pgbouncer-hba.conf"
  /usr/bin/sync -f "$pool_stage"
  /usr/bin/mv -T "$pool_stage" "$POOL_CONFIG_ROOT"
  pool_stage=''
  /usr/bin/sync -f "$POOL_PARENT"
}

install_postgres_access_files() {
  local hba_temporary="${POSTGRES_HBA_PATH}.ascendany-v2.$$"
  local ident_temporary="${POSTGRES_IDENT_PATH}.ascendany-v2.$$"
  /usr/bin/podman exec -i --user root "$POSTGRES_CONTAINER" /bin/sh -ceu '
    target="$1"
    temporary="$2"
    test -f "$target" && test ! -L "$target"
    test ! -e "$temporary" && test ! -L "$temporary"
    umask 077
    cat >"$temporary"
    chown --reference="$target" "$temporary"
    chmod 0600 "$temporary"
    sync -f "$temporary"
  ' sh "$POSTGRES_IDENT_PATH" "$ident_temporary" <"$POSTGRES_IDENT_SOURCE"
  /usr/bin/podman exec -i --user root "$POSTGRES_CONTAINER" /bin/sh -ceu '
    target="$1"
    temporary="$2"
    test -f "$target" && test ! -L "$target"
    test ! -e "$temporary" && test ! -L "$temporary"
    umask 077
    cat >"$temporary"
    chown --reference="$target" "$temporary"
    chmod 0600 "$temporary"
    sync -f "$temporary"
  ' sh "$POSTGRES_HBA_PATH" "$hba_temporary" <"$POSTGRES_HBA_SOURCE"
  /usr/bin/podman exec --user root "$POSTGRES_CONTAINER" /bin/sh -ceu '
    mv -fT "$3" "$1"
    mv -fT "$4" "$2"
    sync -f "$1"
    sync -f "$2"
    sync -f "$(dirname "$1")"
  ' sh "$POSTGRES_IDENT_PATH" "$POSTGRES_HBA_PATH" "$ident_temporary" "$hba_temporary"
  [[ "$(postgres_psql --dbname=postgres --tuples-only --no-align --command='SELECT pg_reload_conf()')" == t ]] ||
    fail postgres_access 'PostgreSQL rejected the release access files'

  local hba_sha ident_sha access_state
  hba_sha="$(/usr/bin/sha256sum "$POSTGRES_HBA_SOURCE" | /usr/bin/awk '{print $1}')"
  ident_sha="$(/usr/bin/sha256sum "$POSTGRES_IDENT_SOURCE" | /usr/bin/awk '{print $1}')"
  [[ "$(/usr/bin/podman exec "$POSTGRES_CONTAINER" /usr/bin/sha256sum "$POSTGRES_HBA_PATH" | /usr/bin/awk '{print $1}')" == "$hba_sha" &&
     "$(/usr/bin/podman exec "$POSTGRES_CONTAINER" /usr/bin/sha256sum "$POSTGRES_IDENT_PATH" | /usr/bin/awk '{print $1}')" == "$ident_sha" ]] ||
    fail postgres_access 'live PostgreSQL access-file bytes differ from the release'
  access_state="$(postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT current_user,
       NOT EXISTS (SELECT 1 FROM pg_hba_file_rules WHERE error IS NOT NULL),
       NOT EXISTS (SELECT 1 FROM pg_ident_file_mappings WHERE error IS NOT NULL),
       (SELECT count(*) FROM pg_hba_file_rules),
       (SELECT count(*) FROM pg_ident_file_mappings),
       (SELECT map_name || ',' || sys_name || ',' || pg_username FROM pg_ident_file_mappings);
SQL
)"
  [[ "$access_state" == 'postgres|t|t|10|1|ascendany_postgres_dba,postgres,postgres' ]] ||
    fail postgres_access 'loaded PostgreSQL HBA/ident catalog differs from the closed contract'
}

verify_pool_identity() {
  local login="$1" password_file="$2"
  local pgpass="$stage_root/${login}.pgpass" identity='' backend_pid='' attempt
  /usr/bin/printf '127.0.0.1:6432:ascendany_v2:%s:' "$login" >"$pgpass"
  /usr/bin/cat "$password_file" >>"$pgpass"
  /usr/bin/printf '\n' >>"$pgpass"
  /usr/bin/chmod 0600 "$pgpass"
  for attempt in {1..200}; do
    if identity="$(PGPASSFILE="$pgpass" /usr/bin/psql -X --no-psqlrc --no-password \
      --host=127.0.0.1 --port=6432 --dbname=ascendany_v2 --username="$login" \
      --tuples-only --no-align --set=ON_ERROR_STOP=1 \
      --command="SELECT current_user || '|' || pg_backend_pid()" 2>/dev/null)"; then
      break
    fi
    /usr/bin/sleep 0.1
  done
  [[ "$identity" =~ ^${login}\|([1-9][0-9]*)$ ]] ||
    fail pgbouncer "$login authentication through native PgBouncer failed"
  backend_pid="${BASH_REMATCH[1]}"
  /usr/bin/rm -f -- "$pgpass"
  /usr/bin/printf '%s' "$backend_pid"
}

terminate_pool_backends() {
  local runtime_pid="$1" catalog_pid="$2" terminated
  [[ "$runtime_pid" =~ ^[1-9][0-9]*$ && "$catalog_pid" =~ ^[1-9][0-9]*$ &&
     "$runtime_pid" != "$catalog_pid" ]] ||
    fail pgbouncer 'native PgBouncer returned invalid or shared app backend identities'
  terminated="$(postgres_psql --dbname=postgres --tuples-only --no-align <<SQL
SELECT count(*)
FROM (
  SELECT pg_terminate_backend(pid, 5000) AS terminated
  FROM (VALUES ($runtime_pid), ($catalog_pid)) AS expected(pid)
) AS pool_backends
WHERE terminated;
SQL
)"
  [[ "$terminated" == 2 ]] ||
    fail pgbouncer 'native PgBouncer did not retain exactly two app backend connections for reconnect verification'
  [[ "$(postgres_psql --dbname=postgres --tuples-only --no-align \
      --command="SELECT count(*) FROM pg_stat_activity WHERE pid IN ($runtime_pid, $catalog_pid)")" == 0 ]] ||
    fail pgbouncer 'terminated PgBouncer app backends remain visible in PostgreSQL'
}

verify_pool_reconnect() {
  local runtime_pid_before catalog_pid_before runtime_pid_after catalog_pid_after
  /usr/bin/systemctl start "$TARGET_POOL_UNIT" || fail pgbouncer 'native PgBouncer failed to start'
  pool_started=1
  runtime_pid_before="$(verify_pool_identity ascendanyd_login "$RUNTIME_PASSWORD_FILE")"
  catalog_pid_before="$(verify_pool_identity ascendany_catalog_publisher_login "$CATALOG_PUBLISHER_PASSWORD_FILE")"
  terminate_pool_backends "$runtime_pid_before" "$catalog_pid_before"
  runtime_pid_after="$(verify_pool_identity ascendanyd_login "$RUNTIME_PASSWORD_FILE")"
  catalog_pid_after="$(verify_pool_identity ascendany_catalog_publisher_login "$CATALOG_PUBLISHER_PASSWORD_FILE")"
  [[ "$runtime_pid_after" != "$runtime_pid_before" &&
     "$catalog_pid_after" != "$catalog_pid_before" ]] ||
    fail pgbouncer 'native PgBouncer did not establish new app backends after forced termination'
  /usr/bin/systemctl stop "$TARGET_POOL_UNIT" || fail pgbouncer 'native PgBouncer failed to return to inactive state'
  pool_started=0
  [[ "$(/usr/bin/systemctl is-enabled "$TARGET_POOL_UNIT" 2>/dev/null || true)" == disabled &&
     "$(/usr/bin/systemctl is-active "$TARGET_POOL_UNIT" 2>/dev/null || true)" == inactive ]] ||
    fail pgbouncer 'native PgBouncer did not remain disabled and inactive after verification'
  require_unused_port 6432
}

write_receipt() {
  local system_identifier role_sha hba_sha ident_sha pool_sha pool_hba_sha temporary
  system_identifier="$(postgres_psql --dbname=postgres --tuples-only --no-align --command='SELECT system_identifier FROM pg_control_system()')"
  [[ "$system_identifier" =~ ^[0-9]{10,20}$ ]] || fail receipt 'PostgreSQL system identifier is invalid'
  role_sha="$(/usr/bin/sha256sum "$ROLE_BOOTSTRAP" | /usr/bin/awk '{print $1}')"
  hba_sha="$(/usr/bin/sha256sum "$POSTGRES_HBA_SOURCE" | /usr/bin/awk '{print $1}')"
  ident_sha="$(/usr/bin/sha256sum "$POSTGRES_IDENT_SOURCE" | /usr/bin/awk '{print $1}')"
  pool_sha="$(/usr/bin/sha256sum "$POOL_CONFIG_SOURCE" | /usr/bin/awk '{print $1}')"
  pool_hba_sha="$(/usr/bin/sha256sum "$POOL_HBA_SOURCE" | /usr/bin/awk '{print $1}')"
  receipt_stage="/var/lib/.ascendany-v2-provision.$$"
  [[ ! -e "$receipt_stage" && ! -L "$receipt_stage" ]] || fail receipt 'receipt staging path already exists'
  /usr/bin/install -d -o root -g root -m 0700 "$receipt_stage"
  temporary="$receipt_stage/.receipt"
  /usr/bin/printf '%s\n' \
    'schema=ascendany.postgres-pgbouncer.provision.v2' \
    "database=$V2_DATABASE" \
    "postgresSystemIdentifier=$system_identifier" \
    "roleBootstrapSHA256=$role_sha" \
    "postgresHBASHA256=$hba_sha" \
    "postgresIdentSHA256=$ident_sha" \
    "pgbouncerConfigSHA256=$pool_sha" \
    "pgbouncerHBASHA256=$pool_hba_sha" >"$temporary"
  /usr/bin/chown 0:0 "$temporary"
  /usr/bin/chmod 0400 "$temporary"
  /usr/bin/sync -f "$temporary"
  /usr/bin/mv "$temporary" "$receipt_stage/receipt"
  /usr/bin/sync -f "$receipt_stage"
  /usr/bin/mv -T "$receipt_stage" "$RECEIPT_ROOT"
  receipt_stage=''
  /usr/bin/sync -f /var/lib
}

consume_password_inputs() {
  /usr/bin/rm -f -- "$RUNTIME_PASSWORD_FILE" "$MIGRATOR_PASSWORD_FILE" \
    "$BACKUP_PASSWORD_FILE" "$RESTORE_PASSWORD_FILE" "$CATALOG_PUBLISHER_PASSWORD_FILE"
  /usr/bin/rmdir -- "$INPUT_ROOT"
  /usr/bin/sync -f /run
}

main() {
  require_exact_args "$@"
  ((EUID == 0)) || fail identity 'provisioning must run as root'
  local command
  for command in awk bash cat chmod chown cmp dirname env find flock head install jq \
    mktemp mv podman psql readlink realpath rm rmdir rpm sha256sum sleep sort ss \
    stat sync systemctl systemd-creds wc; do
    command -v "$command" >/dev/null 2>&1 ||
      fail dependency "required executable is missing: $command"
  done

  exec 9>"$LOCK_PATH"
  /usr/bin/flock --exclusive --nonblock 9 || fail concurrency 'another provisioning process owns the lock'
  trap cleanup EXIT

  verify_release_inputs
  verify_service_boundaries
  verify_output_boundaries
  verify_password_inputs
  verify_postgres_container
  pass preflight

  stage_root="$(/usr/bin/mktemp -d /run/ascendany-v2-provision-stage.XXXXXX)"
  /usr/bin/chown 0:0 "$stage_root"
  /usr/bin/chmod 0700 "$stage_root"
  encrypt_credential db_password "$RUNTIME_PASSWORD_FILE" runtime_db_password.cred
  encrypt_credential migrator_db_password "$MIGRATOR_PASSWORD_FILE" migrator_db_password.cred
  encrypt_credential backup_db_password "$BACKUP_PASSWORD_FILE" backup_db_password.cred
  encrypt_credential restore_db_password "$RESTORE_PASSWORD_FILE" restore_db_password.cred
  encrypt_credential catalog_publisher_db_password "$CATALOG_PUBLISHER_PASSWORD_FILE" catalog_publisher_db_password.cred
  pass credential_stage

  create_v2_database_and_roles
  generate_pool_credential
  pass database

  publish_credentials
  publish_pool_config
  install_postgres_access_files
  pass access_contract

  verify_pool_reconnect
  pass pgbouncer

  write_receipt
  consume_password_inputs
  trap - EXIT
  /usr/bin/rm -f -- "$stage_root"/*
  /usr/bin/rmdir -- "$stage_root"
  stage_root=''
  pass committed
}

main "$@"
