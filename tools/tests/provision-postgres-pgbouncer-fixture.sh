#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly PROVISIONER="$REPOSITORY_ROOT/deploy/v2/scripts/provision-postgres-pgbouncer.sh"
readonly VALIDATOR="$REPOSITORY_ROOT/deploy/v2/scripts/validate-production.sh"
readonly BUILDER="$REPOSITORY_ROOT/tools/build-v2-release.sh"
readonly INSTALLER="$REPOSITORY_ROOT/deploy/v2/scripts/install-v2-release.sh"
readonly README="$REPOSITORY_ROOT/deploy/v2/README.md"
readonly POOL_CONFIG="$REPOSITORY_ROOT/deploy/v2/config/pgbouncer.ini"
readonly POOL_HBA="$REPOSITORY_ROOT/deploy/v2/config/pgbouncer-hba.conf"
readonly POSTGRES_HBA="$REPOSITORY_ROOT/deploy/v2/config/postgresql-hba.conf"
readonly POSTGRES_IDENT="$REPOSITORY_ROOT/deploy/v2/config/postgresql-ident.conf"
readonly POOL_UNIT="$REPOSITORY_ROOT/deploy/v2/systemd/ascendany-pgbouncer.service"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-clean-provision-fixture.XXXXXX")"
trap 'rm -rf -- "$WORK_ROOT"' EXIT

fail() {
  printf 'FAIL [clean provision fixture]: %s\n' "$1" >&2
  exit 1
}

require_literal() {
  local path="$1" literal="$2"
  grep -F -- "$literal" "$path" >/dev/null ||
    fail "required contract is absent from ${path#$REPOSITORY_ROOT/}: $literal"
}

for command in bash grep rg sed sort systemd-analyze; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is missing: $command"
done

bash -n "$PROVISIONER" "$VALIDATOR" "$BUILDER" "$INSTALLER"
systemd-analyze verify --man=no "$POOL_UNIT"

help_log="$WORK_ROOT/help.log"
if ! BASH_ENV=/dev/fd/3 "$PROVISIONER" --help 3<<<'printf "BASH_ENV_EXECUTED\n" >&2' \
    >"$help_log" 2>&1; then
  fail 'privileged provisioner help boundary failed'
fi
if grep -F 'BASH_ENV_EXECUTED' "$help_log" >/dev/null; then
  fail 'provisioner evaluated BASH_ENV before entering its clean environment'
fi
for help_term in \
  '--postgres-container ascendany-postgres' \
  '--postgres-dba-role postgres' \
  '--confirm-fresh-database ascendany_v2' \
  'The command is deliberately one-way'; do
  require_literal "$help_log" "$help_term"
done

forged_log="$WORK_ROOT/forged.log"
if /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C ASCENDANY_PROVISION_CLEAN_ENV=1 \
    FORGED_PROVISION_INPUT=forged "$PROVISIONER" --help >"$forged_log" 2>&1; then
  fail 'forged clean-environment marker passed'
fi
require_literal "$forged_log" 'provisioning requires the canonical clean environment'

if rg -n 'python|uvicorn|apps[./]api|/api/v1|ascendany-api|"AscendAny"|dbname=AscendAny([[:space:]]|$)|rollback|recovered|TCP ports? 8000|port 8000' \
    "$PROVISIONER" "$POOL_CONFIG" "$POOL_HBA" "$POSTGRES_HBA" "$POSTGRES_IDENT" >/dev/null; then
  fail 'clean provisioning closure contains a retired runtime, database route or reverse path'
fi
if rg -n 'PGPASSWORD|DATABASE_PASSWORD=|set[[:space:]]+-x|--set=[^[:space:]]*password|--env[^[:space:]]*password' \
    "$PROVISIONER" >/dev/null; then
  fail 'provisioner contains a plaintext-secret transport'
fi
if rg -n '/usr/bin/(podman|systemctl)[[:space:]]+(start|stop|restart|rm|rename|update)[[:space:]]+[^\n]*RESERVED_POOL_CONTAINER' \
    "$PROVISIONER" >/dev/null; then
  fail 'provisioner mutates a pre-existing container'
fi

for required in \
  'readonly POSTGRES_DBA_ROLE=postgres' \
  'readonly RESERVED_POOL_CONTAINER=ascendany-pgbouncer' \
  'require_masked_inactive_unit "$PACKAGE_POOL_UNIT"' \
  'require_unused_port 6432' \
  'require_unused_port 18000' \
  'PostgreSQL DBA, durability, role or database entry state is not fresh' \
  'CREATE DATABASE ascendany_v2 OWNER ascendany_database_owner TEMPLATE template0;' \
  'COMMENT ON ROLE postgres IS '\''ascendany.postgres.dba.v2'\'';' \
  'WHERE rolname = ANY(ARRAY['\''ascendanyd_login'\'', '\''ascendany_catalog_publisher_login'\''])' \
  'install_postgres_access_files' \
  'verify_pool_once' \
  'schema=ascendany.postgres-pgbouncer.provision.v2' \
  'consume_password_inputs' \
  'pass committed'; do
  require_literal "$PROVISIONER" "$required"
done

mapfile -t pool_databases < <(sed -n '/^\[databases\]$/,/^$/p' "$POOL_CONFIG" |
  sed '1d;/^$/d')
[[ "${#pool_databases[@]}" == 1 &&
   "${pool_databases[0]}" == 'ascendany_v2 = host=127.0.0.1 port=5432 dbname=ascendany_v2' ]] ||
  fail 'PgBouncer database map is not the exact v2-only route'

mapfile -t pool_hba_rules < <(sed '/^[[:space:]]*#/d;/^[[:space:]]*$/d' "$POOL_HBA")
[[ "${#pool_hba_rules[@]}" == 2 &&
   "${pool_hba_rules[0]}" == 'host ascendany_v2 ascendanyd_login,ascendany_catalog_publisher_login 127.0.0.1/32 scram-sha-256' &&
   "${pool_hba_rules[1]}" == 'host all all 0.0.0.0/0 reject' ]] ||
  fail 'PgBouncer client HBA is not the exact v2 runtime/catch-all closure'

mapfile -t postgres_hba_rules < <(sed '/^[[:space:]]*#/d;/^[[:space:]]*$/d' "$POSTGRES_HBA")
expected_postgres_hba=(
  'local replication all reject'
  'local all postgres peer map=ascendany_postgres_dba'
  'local all all reject'
  'host replication all 0.0.0.0/0 reject'
  'host replication all ::/0 reject'
  'host ascendany_v2 ascendanyd_login,ascendany_migrator_login,ascendany_backup_login,ascendany_catalog_publisher_login 10.88.0.1/32 scram-sha-256'
  'host postgres,ascendany_v2_restore_verify ascendany_restore_login 10.88.0.1/32 scram-sha-256'
  'host all all 10.88.0.1/32 reject'
  'host all all 0.0.0.0/0 reject'
  'host all all ::/0 reject'
)
[[ "$(printf '%s\n' "${postgres_hba_rules[@]}")" == "$(printf '%s\n' "${expected_postgres_hba[@]}")" ]] ||
  fail 'PostgreSQL HBA differs from the exact DBA/v2 capability closure'

mapfile -t postgres_ident_rules < <(sed '/^[[:space:]]*#/d;/^[[:space:]]*$/d' "$POSTGRES_IDENT")
ident_shape=''
if [[ "${#postgres_ident_rules[@]}" == 1 ]]; then
  read -r map_name os_user db_role extra <<<"${postgres_ident_rules[0]}"
  ident_shape="${map_name}|${os_user}|${db_role}|${extra-}"
fi
[[ "${#postgres_ident_rules[@]}" == 1 &&
   "$ident_shape" == 'ascendany_postgres_dba|postgres|postgres|' ]] ||
  fail 'PostgreSQL ident map differs from the explicit container-local DBA channel'

for obsolete in \
  "$REPOSITORY_ROOT/deploy/v2/config/postgresql-hba-bootstrap.conf" \
  "$REPOSITORY_ROOT/deploy/v2/config/postgresql-ident-bootstrap.conf"; do
  [[ ! -e "$obsolete" && ! -L "$obsolete" ]] ||
    fail "obsolete transition configuration remains: ${obsolete#$REPOSITORY_ROOT/}"
done

for inventory in "$BUILDER" "$INSTALLER" "$VALIDATOR"; do
  if grep -E 'postgresql-(hba|ident)-bootstrap[.]conf' "$inventory" >/dev/null; then
    fail "release inventory retains an obsolete transition file: ${inventory#$REPOSITORY_ROOT/}"
  fi
done
require_literal "$BUILDER" 'release payload path contract must contain exactly 68 entries'
require_literal "$BUILDER" 'staged release payload differs from the exact 68-path contract'
require_literal "$README" 'manifest-closed payload contains 68 files'
require_literal "$VALIDATOR" 'schema=ascendany.postgres-pgbouncer.provision.v2'
require_literal "$VALIDATOR" '--username=postgres'

printf 'clean PostgreSQL/PgBouncer provision fixture: PASS\n'
