#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly PROVISIONER="$REPOSITORY_ROOT/deploy/v2/scripts/provision-postgres-pgbouncer.sh"
readonly PRODUCTION_VALIDATOR="$REPOSITORY_ROOT/deploy/v2/scripts/validate-production.sh"
readonly PROVISION_SOURCE="$REPOSITORY_ROOT/deploy/v2/scripts/provision-postgres-pgbouncer.sh"
readonly POOL_CONFIG_SOURCE="$REPOSITORY_ROOT/deploy/v2/config/pgbouncer.ini"
readonly POOL_HBA_SOURCE="$REPOSITORY_ROOT/deploy/v2/config/pgbouncer-hba.conf"
readonly POSTGRES_HBA_BOOTSTRAP_SOURCE="$REPOSITORY_ROOT/deploy/v2/config/postgresql-hba-bootstrap.conf"
readonly POSTGRES_HBA_SOURCE="$REPOSITORY_ROOT/deploy/v2/config/postgresql-hba.conf"
readonly POSTGRES_IDENT_BOOTSTRAP_SOURCE="$REPOSITORY_ROOT/deploy/v2/config/postgresql-ident-bootstrap.conf"
readonly POSTGRES_IDENT_SOURCE="$REPOSITORY_ROOT/deploy/v2/config/postgresql-ident.conf"
readonly POOL_UNIT_SOURCE="$REPOSITORY_ROOT/deploy/v2/systemd/ascendany-pgbouncer.service"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-postgres-pool-contract.XXXXXX")"
readonly POSTGRES_CONTAINER="ascendany-postgres-pool-contract-$$"
readonly LEGACY_PASSWORD='fixture-legacy-password-aaaaaaaa'
readonly RUNTIME_PASSWORD='fixture-runtime-password-bbbbbbb'

pool_pid=''
restart_fixture_container="ascendany-pgbouncer-restart-fixture-$$"
restart_fixture_rollback="${restart_fixture_container}-rollback"
last_access_reload_previous=''
idle_client_pid=''
active_client_pid=''

cleanup() {
  if [[ -n "$idle_client_pid" ]]; then
    kill "$idle_client_pid" >/dev/null 2>&1 || true
    wait "$idle_client_pid" 2>/dev/null || true
  fi
  if [[ -n "$active_client_pid" ]]; then
    kill "$active_client_pid" >/dev/null 2>&1 || true
    wait "$active_client_pid" 2>/dev/null || true
  fi
  if [[ -n "$pool_pid" ]]; then
    kill "$pool_pid" >/dev/null 2>&1 || true
    wait "$pool_pid" 2>/dev/null || true
  fi
  podman rm -f "$POSTGRES_CONTAINER" >/dev/null 2>&1 || true
  podman rm -f "$restart_fixture_container" "$restart_fixture_rollback" >/dev/null 2>&1 || true
  rm -rf -- "$WORK_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL [provision fixture]: %s\n' "$1" >&2
  exit 1
}

for command in awk bash cmp grep jq mkfifo pgbouncer podman psql sed sha256sum ss stdbuf systemd-analyze systemd-run; do
  command -v "$command" >/dev/null 2>&1 || fail "required executable is missing: $command"
done

bash -n "$PROVISION_SOURCE"
systemd-analyze verify --man=no "$POOL_UNIT_SOURCE"
[[ "$(pgbouncer --version 2>&1 | head -n 1)" == 'PgBouncer 1.25.2' ]] ||
  fail 'fixture requires the locked PgBouncer 1.25.2 runtime'

bash_env_log="$WORK_ROOT/bash-env.log"
if ! BASH_ENV=/dev/fd/3 "$PROVISION_SOURCE" --help 3<<<'printf "BASH_ENV_EXECUTED\n" >&2' \
    >"$bash_env_log" 2>&1; then
  fail 'privileged provisioner help boundary failed'
fi
if grep -F 'BASH_ENV_EXECUTED' "$bash_env_log" >/dev/null; then
  fail 'provisioner evaluated BASH_ENV before its clean-environment boundary'
fi

if grep -En -- 'PGPASSWORD|DATABASES_PASSWORD|set[[:space:]]+-x|--set=[^[:space:]]*password|--env[^[:space:]]*password' \
  "$PROVISION_SOURCE" >/dev/null; then
  fail 'provisioner contains a forbidden plaintext-secret transport'
fi
if grep -F -- 'rollback_legacy_split' "$PROVISION_SOURCE" >/dev/null; then
  fail 'role split has a reverse path back to the bootstrap application superuser'
fi
for required in \
  'quiesce_legacy_pool' \
  'provision_unit_runtime_matches' \
  'enter_provision_unit_boundary' \
  '0::/system.slice/$PROVISION_UNIT' \
  'LEGACY_POOL_CONMON_SCOPE=ascendany-legacy-pgbouncer-conmon.scope' \
  'POSTGRES_NETWORK=podman' \
  'POSTGRES_ADDRESS=10.88.0.2' \
  'POSTGRES_SUBNET=10.88.0.0/16' \
  'start_legacy_pool_container' \
  'systemd-run --scope' \
  '--verbose' \
  '--property=KillMode=control-group' \
  '--property=RuntimeMaxSec=30min' \
  '--property=MemorySwapMax=0' \
  'stop_legacy_api_for_pool_switch' \
  'start_legacy_api_and_probe' \
  'require_legacy_api_unit_contract' \
  "--format '{{.MountLabel}}'" \
  "--format '{{.ProcessLabel}}'" \
  "WHERE usesysid = 10" \
  "catalog_role_state_for_recovery" \
  "restore_safe_postgres_access \"\$role_state\"" \
  'install_postgres_access bootstrap "$POSTGRES_HBA_BOOTSTRAP_SOURCE" "$POSTGRES_IDENT_BOOTSTRAP_SOURCE"' \
  'install_postgres_access final "$POSTGRES_HBA_SOURCE" "$POSTGRES_IDENT_SOURCE"' \
  'install_postgres_access original "$hba_backup" "$ident_backup"' \
  'require_pool_unit_contract' \
  '/usr/bin/sync -f "$CREDENTIAL_ROOT" || recovery_failed=1' \
  'legacy_pool_selinux_context_matches' \
  'label_legacy_pool_entry_for_publish "$path" "$temporary"' \
  'durably_verify_legacy_pool_tree "$POOL_CONFIG_ROOT" legacy' \
  "COMMENT ON ROLE \"AscendAny\" IS 'ascendany.legacy.runtime.v2'" \
  'LoadCredentialEncrypted=pgbouncer_userlist:/etc/ascendany/credentials/pgbouncer_userlist.cred' \
  'KillSignal=SIGINT' \
  'DynamicUser=yes'; do
  grep -F -- "$required" "$PROVISION_SOURCE" "$POOL_UNIT_SOURCE" >/dev/null ||
    fail "required cutover contract is absent: $required"
done

if grep -E '/usr/bin/flock|exec 9>' "$PROVISION_SOURCE" >/dev/null; then
  fail 'provisioner retains an inheritable process-local lock path'
fi

require_function_term() {
  local function_name="$1" term="$2" body
  body="$(sed -n "/^${function_name}[(][)] [{]$/,/^}$/p" "$PROVISION_SOURCE")"
  [[ -n "$body" && "$body" == *"$term"* ]] ||
    fail "required lifecycle term is absent from ${function_name}: ${term}"
}

forbid_function_term() {
  local function_name="$1" term="$2" body
  body="$(sed -n "/^${function_name}[(][)] [{]$/,/^}$/p" "$PROVISION_SOURCE")"
  [[ -n "$body" ]] || fail "lifecycle function is absent: ${function_name}"
  [[ "$body" != *"$term"* ]] ||
    fail "forbidden lifecycle term is present in ${function_name}: ${term}"
}

require_function_term verify_legacy_pool_reconnect_before_split stop_legacy_api_for_pool_switch
require_function_term verify_legacy_pool_reconnect_before_split start_legacy_api_and_probe
require_function_term verify_legacy_pool_reconnect_before_split start_legacy_pool_container
require_function_term quiesce_legacy_pool stop_legacy_api_for_pool_switch
require_function_term restart_legacy_pool_after_split start_legacy_api_and_probe
require_function_term restart_legacy_pool_after_split start_legacy_pool_container
require_function_term switch_to_native_pool stop_legacy_api_for_pool_switch
require_function_term switch_to_native_pool start_legacy_api_and_probe
require_function_term recover_precommit stop_legacy_api_for_pool_switch
require_function_term recover_precommit start_legacy_api_and_probe
require_function_term start_old_pool_after_recovery start_legacy_pool_container
require_function_term fence_failed_legacy_recovery stop_legacy_api_for_pool_switch
require_function_term enter_provision_unit_boundary --verbose
forbid_function_term enter_provision_unit_boundary --pipe

[[ "$(grep -Fc '/usr/bin/podman start' "$PROVISION_SOURCE")" == 1 ]] ||
  fail 'legacy PgBouncer start bypasses the dedicated conmon scope capability'

if grep -E '(^|[[:space:]])(password|user)[[:space:]]*=' "$POOL_CONFIG_SOURCE" >/dev/null; then
  fail 'PgBouncer release config contains an inline backend identity or password'
fi
[[ "$(grep -vcE '^[[:space:]]*(#|$)' "$POOL_HBA_SOURCE")" == 3 ]] ||
  fail 'PgBouncer client HBA does not have exactly three rules'
[[ "$(sed -n '1p' "$POOL_HBA_SOURCE")" == 'host "AscendAny" "AscendAny" 127.0.0.1/32 scram-sha-256' ]] ||
  fail 'legacy PgBouncer route differs'
[[ "$(sed -n '2p' "$POOL_HBA_SOURCE")" == 'host ascendany_v2 ascendanyd_login 127.0.0.1/32 scram-sha-256' ]] ||
  fail 'v2 PgBouncer route differs'
[[ "$(sed -n '3p' "$POOL_HBA_SOURCE")" == 'host all all 0.0.0.0/0 reject' ]] ||
  fail 'PgBouncer catch-all reject is absent or out of order'

forged_environment_log="$WORK_ROOT/forged-environment.log"
if /usr/bin/env -i PATH=/usr/bin:/bin LC_ALL=C ASCENDANY_PROVISION_CLEAN_ENV=1 \
  FORGED_PROVISION_INPUT=forged "$PROVISION_SOURCE" --help >"$forged_environment_log" 2>&1; then
  fail 'forged clean-environment marker passed'
fi
grep -F 'provisioning requires the canonical clean environment' "$forged_environment_log" >/dev/null ||
  fail 'forged environment was not rejected at the process boundary'
if grep -F 'FORGED_PROVISION_INPUT' "$forged_environment_log" >/dev/null; then
  fail 'forged environment name leaked into provisioner output'
fi

extract_split_sql() {
  awk '
    /^split_legacy_role\(\) \{/ { function_seen=1 }
    function_seen && /^BEGIN;$/ { sql_seen=1 }
    sql_seen && /^SQL$/ { exit }
    sql_seen { print }
  ' "$PROVISION_SOURCE"
}

awk '
  /^postgres_durability_settings_are_enabled[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >"$WORK_ROOT/postgres-durability-settings.sh"
# shellcheck source=/dev/null
source "$WORK_ROOT/postgres-durability-settings.sh"
postgres_durability_settings_are_enabled 'on|on|on' ||
  fail 'the all-on PostgreSQL durability tuple was rejected'
for unsafe_durability in 'off|on|on' 'on|off|on' 'on|on|off'; do
  if postgres_durability_settings_are_enabled "$unsafe_durability"; then
    fail "unsafe PostgreSQL durability tuple was accepted: $unsafe_durability"
  fi
done

for required_receipt_term in \
  "current_setting('fsync') = 'on'" \
  "current_setting('synchronous_commit') = 'on'" \
  "current_setting('full_page_writes') = 'on'" \
  ":'hba_mtime'::timestamptz <= pg_conf_load_time()" \
  ":'ident_mtime'::timestamptz <= pg_conf_load_time()"; do
  grep -F -- "$required_receipt_term" "$PRODUCTION_VALIDATOR" >/dev/null ||
    fail "production PostgreSQL receipt term is absent: $required_receipt_term"
done

wait_for_postgres() {
  local attempt
  for attempt in {1..180}; do
    if podman exec --user postgres "$POSTGRES_CONTAINER" \
      psql -X --no-psqlrc -U AscendAny -d AscendAny --tuples-only --no-align \
        --command='SELECT 1' 2>/dev/null | grep -qx 1; then
      return
    fi
    sleep 0.2
  done
  fail 'PostgreSQL 17 fixture did not become ready'
}

wait_for_postgres_role() {
  local role="$1" attempt
  for attempt in {1..180}; do
    if podman exec --user postgres "$POSTGRES_CONTAINER" \
      psql -X --no-psqlrc -U "$role" -d AscendAny --tuples-only --no-align \
        --command='SELECT current_user' 2>/dev/null | grep -qx "$role"; then
      return
    fi
    sleep 0.2
  done
  fail "PostgreSQL fixture did not recover the $role maintenance route after restart"
}

publish_one_access_member_then_restart() {
  local source="$1" target="$2" role="$3" temporary
  temporary="${target}.fixture-crash"
  podman cp "$source" "$POSTGRES_CONTAINER:$temporary"
  podman exec --user root "$POSTGRES_CONTAINER" sh -ceu '
    target="$1"
    temporary="$2"
    chown postgres:postgres "$temporary"
    chmod 0600 "$temporary"
    sync -f "$temporary"
    mv -f "$temporary" "$target"
    sync -f "$(dirname "$target")"
  ' sh "$target" "$temporary"
  podman restart "$POSTGRES_CONTAINER" >/dev/null
  wait_for_postgres_role "$role"
}

postgres_access_load_receipt_fixture() {
  local previous_load_time="$1" reload_role="$2" result
  local -a access_mtimes=()
  mapfile -t access_mtimes < <(
    podman exec --user postgres "$POSTGRES_CONTAINER" \
      /usr/bin/stat -Lc '%y' -- \
        /var/lib/postgresql/data/pg_hba.conf /var/lib/postgresql/data/pg_ident.conf
  )
  [[ "${#access_mtimes[@]}" == 2 ]] || return 1
  result="$(podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    psql -X --no-psqlrc -U "$reload_role" -d AscendAny --tuples-only --no-align \
      --set=previous_load_time="$previous_load_time" \
      --set=hba_mtime="${access_mtimes[0]}" \
      --set=ident_mtime="${access_mtimes[1]}" <<'SQL'
SELECT pg_conf_load_time() > :'previous_load_time'::timestamptz
   AND :'hba_mtime'::timestamptz <= pg_conf_load_time()
   AND :'ident_mtime'::timestamptz <= pg_conf_load_time();
SQL
)" || return 1
  [[ "$result" == t ]]
}

install_postgres_access_fixture() {
  local hba_source="$1" ident_source="$2" reload_role="$3"
  local attempt
  last_access_reload_previous="$(podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    psql -X --no-psqlrc -U "$reload_role" -d AscendAny --tuples-only --no-align \
      --command='SELECT pg_conf_load_time()')"
  [[ -n "$last_access_reload_previous" ]] ||
    fail "fixture could not capture the PostgreSQL load time for $reload_role"
  podman cp "$hba_source" "$POSTGRES_CONTAINER":/var/lib/postgresql/data/pg_hba.conf
  podman cp "$ident_source" "$POSTGRES_CONTAINER":/var/lib/postgresql/data/pg_ident.conf
  podman exec --user root "$POSTGRES_CONTAINER" sh -ceu '
    chown postgres:postgres /var/lib/postgresql/data/pg_hba.conf /var/lib/postgresql/data/pg_ident.conf
    chmod 0600 /var/lib/postgresql/data/pg_hba.conf /var/lib/postgresql/data/pg_ident.conf
  '
  podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    psql -X --no-psqlrc -U "$reload_role" -d AscendAny --tuples-only --no-align \
      --command='SELECT pg_reload_conf()' 2>/dev/null | grep -qx t ||
    fail "PostgreSQL rejected the fixture access contract for $reload_role"
  for attempt in {1..100}; do
    if postgres_access_load_receipt_fixture "$last_access_reload_previous" "$reload_role"; then
      return
    fi
    sleep 0.1
  done
  fail "PostgreSQL did not issue a loaded access-file receipt for $reload_role"
}

normalized_hba_fixture() {
  local role="$1"
  podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    psql -X --no-psqlrc -U "$role" -d AscendAny --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT type,
       array_to_string(database, ','),
       array_to_string(user_name, ','),
       coalesce(address, ''),
       coalesce(netmask, ''),
       auth_method,
       coalesce(array_to_string(options, ','), ''),
       coalesce(error, '')
FROM pg_hba_file_rules
ORDER BY line_number;
SQL
}

normalized_ident_fixture() {
  local role="$1"
  podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    psql -X --no-psqlrc -U "$role" -d AscendAny --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT map_name, sys_name, pg_username, coalesce(error, '')
FROM pg_ident_file_mappings
ORDER BY line_number;
SQL
}

readonly EXPECTED_BOOTSTRAP_IDENT=$'ascendany_role_split|postgres|AscendAny|\nascendany_role_split|postgres|ascendany_cluster_admin|\nascendany_cluster_admin|postgres|ascendany_cluster_admin|'
readonly EXPECTED_FINAL_IDENT='ascendany_cluster_admin|postgres|ascendany_cluster_admin|'
readonly EXPECTED_BOOTSTRAP_HBA=$'local|replication|all|||reject||\nlocal|all|AscendAny|||peer|map=ascendany_role_split|\nlocal|all|ascendany_cluster_admin|||peer|map=ascendany_role_split|\nlocal|all|all|||reject||\nhost|replication|all|0.0.0.0|0.0.0.0|reject||\nhost|replication|all|::|::|reject||\nhost|AscendAny|AscendAny|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|ascendany_v2|ascendanyd_login,ascendany_migrator_login,ascendany_backup_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|postgres,ascendany_v2_restore_verify|ascendany_restore_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|all|all|10.88.0.1|255.255.255.255|reject||\nhost|all|all|0.0.0.0|0.0.0.0|reject||\nhost|all|all|::|::|reject||'
readonly EXPECTED_FINAL_HBA=$'local|replication|all|||reject||\nlocal|all|ascendany_cluster_admin|||peer|map=ascendany_cluster_admin|\nlocal|all|all|||reject||\nhost|replication|all|0.0.0.0|0.0.0.0|reject||\nhost|replication|all|::|::|reject||\nhost|AscendAny|AscendAny|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|ascendany_v2|ascendanyd_login,ascendany_migrator_login,ascendany_backup_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|postgres,ascendany_v2_restore_verify|ascendany_restore_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|all|all|10.88.0.1|255.255.255.255|reject||\nhost|all|all|0.0.0.0|0.0.0.0|reject||\nhost|all|all|::|::|reject||'

podman run -d --name "$POSTGRES_CONTAINER" \
  --network podman \
  --env POSTGRES_USER=AscendAny \
  --env POSTGRES_PASSWORD="$LEGACY_PASSWORD" \
  --env POSTGRES_DB=AscendAny \
  --publish 127.0.0.1::5432 \
  postgres:17 >/dev/null
wait_for_postgres

awk '
  /^require_protected_ancestry[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >"$WORK_ROOT/pool-unit-file-contract.sh"
awk '
  /^require_pool_unit_files[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >>"$WORK_ROOT/pool-unit-file-contract.sh"
podman cp "$WORK_ROOT/pool-unit-file-contract.sh" \
  "$POSTGRES_CONTAINER":/root/pool-unit-file-contract.sh
podman cp "$POOL_UNIT_SOURCE" "$POSTGRES_CONTAINER":/root/ascendany-pgbouncer.release.service
podman cp /usr/lib/systemd/system/service.d/10-timeout-abort.conf \
  "$POSTGRES_CONTAINER":/root/10-timeout-abort.release.conf
podman exec -i --user root "$POSTGRES_CONTAINER" /usr/bin/bash -s <<'UNIT_FIXTURE'
set -Eeuo pipefail
fail() {
  printf 'pool unit fixture: %s\n' "$2" >&2
  exit 1
}
POOL_UNIT_SOURCE=/root/ascendany-pgbouncer.release.service
POOL_UNIT_INSTALLED=/root/pool-unit-contract/etc/systemd/system/ascendany-pgbouncer.service
POOL_GLOBAL_DROPIN=/root/pool-unit-contract/usr/lib/systemd/system/service.d/10-timeout-abort.conf
install -d -o root -g root -m 0755 \
  "$(dirname "$POOL_UNIT_INSTALLED")" "$(dirname "$POOL_GLOBAL_DROPIN")"
install -o root -g root -m 0644 "$POOL_UNIT_SOURCE" "$POOL_UNIT_INSTALLED"
install -o root -g root -m 0644 /root/10-timeout-abort.release.conf "$POOL_GLOBAL_DROPIN"
# shellcheck source=/dev/null
source /root/pool-unit-file-contract.sh
require_pool_unit_files

printf '%s\n' '# tampered' >>"$POOL_UNIT_INSTALLED"
if (require_pool_unit_files) >/dev/null 2>&1; then
  printf 'tampered installed unit passed its release byte contract\n' >&2
  exit 1
fi
install -o root -g root -m 0644 "$POOL_UNIT_SOURCE" "$POOL_UNIT_INSTALLED"

printf '%s\n' 'TimeoutStopFailureMode=terminate' >"$POOL_GLOBAL_DROPIN"
if (require_pool_unit_files) >/dev/null 2>&1; then
  printf 'tampered global service drop-in passed its exact byte contract\n' >&2
  exit 1
fi
install -o root -g root -m 0644 /root/10-timeout-abort.release.conf "$POOL_GLOBAL_DROPIN"

chmod 0666 "$POOL_UNIT_INSTALLED"
if (require_pool_unit_files) >/dev/null 2>&1; then
  printf 'writable installed unit passed its protected metadata contract\n' >&2
  exit 1
fi
UNIT_FIXTURE

podman cp "$POSTGRES_CONTAINER":/var/lib/postgresql/data/pg_hba.conf "$WORK_ROOT/postgresql-hba-original.conf"
podman cp "$POSTGRES_CONTAINER":/var/lib/postgresql/data/pg_ident.conf "$WORK_ROOT/postgresql-ident-original.conf"

durability_settings="$(podman exec -i --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U AscendAny -d AscendAny --tuples-only --no-align --field-separator='|' \
    --command="SELECT current_setting('fsync'), current_setting('synchronous_commit'), current_setting('full_page_writes')")"
postgres_durability_settings_are_enabled "$durability_settings" ||
  fail "fixture PostgreSQL durability settings differ: $durability_settings"

gateway="$(podman inspect "$POSTGRES_CONTAINER" |
  jq -r '.[0].NetworkSettings.Networks | to_entries | map(.value.Gateway) | unique | if length == 1 then .[0] else "" end')"
[[ "$gateway" == 10.88.0.1 ]] || fail 'fixture Podman gateway differs from the production HBA source identity'
podman network inspect podman | jq -e '
  type == "array" and length == 1 and
  .[0].name == "podman" and
  .[0].driver == "bridge" and
  .[0].network_interface == "podman0" and
  .[0].internal == false and
  .[0].ipv6_enabled == false and
  .[0].subnets == [{"subnet":"10.88.0.0/16","gateway":"10.88.0.1"}]
' >/dev/null || fail 'fixture Podman bridge differs from the production native service boundary'
container_ip="$(podman inspect "$POSTGRES_CONTAINER" |
  jq -r '.[0].NetworkSettings.Networks | to_entries | map(.value.IPAddress) | unique | if length == 1 then .[0] else "" end')"
[[ "$container_ip" =~ ^10\.88\.[0-9]+\.[0-9]+$ ]] || fail 'fixture PostgreSQL container IP is invalid'
postgres_port="$(podman port "$POSTGRES_CONTAINER" 5432/tcp | awk -F: 'NR == 1 { print $NF }')"
[[ "$postgres_port" =~ ^[0-9]+$ ]] || fail 'fixture PostgreSQL host port is invalid'

podman exec -i --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U AscendAny -d AscendAny --set=ON_ERROR_STOP=1 >/dev/null <<'SQL'
CREATE SCHEMA ascendany AUTHORIZATION "AscendAny";
CREATE SCHEMA pgbouncer AUTHORIZATION "AscendAny";
CREATE FUNCTION pgbouncer.user_lookup(i_username text, OUT uname text, OUT phash text)
RETURNS record
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  SELECT rolname, rolpassword
  FROM pg_authid
  WHERE rolname = i_username AND rolcanlogin;
$$;
CREATE TABLE ascendany.import_tasks (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  created_at timestamptz NOT NULL
);
CREATE INDEX import_tasks_created_at_idx ON ascendany.import_tasks(created_at);
CREATE TABLE ascendany.import_task_events (
  run_id text NOT NULL,
  event_id bigint NOT NULL,
  PRIMARY KEY (run_id, event_id)
);
CREATE INDEX import_task_events_run_id_event_id_idx
  ON ascendany.import_task_events(run_id, event_id);
CREATE TABLE ascendany.payloads (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  value text
);
CREATE TYPE ascendany.job_state AS ENUM ('queued', 'done');
CREATE DOMAIN ascendany.nonempty_text AS text CHECK (value <> '');
CREATE TYPE ascendany.score_range AS RANGE (subtype = integer);
SQL
podman exec --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U AscendAny -d AscendAny --tuples-only --no-align \
    --command="SELECT rolpassword FROM pg_authid WHERE rolname = 'AscendAny'" |
  sha256sum | awk '{print $1}' >"$WORK_ROOT/legacy-verifier-before.sha256"

# Entering bootstrap publishes ident first. A reboot after that first durable
# rename leaves the original HBA independent of the new map and preserves the
# bootstrap maintenance identity.
publish_one_access_member_then_restart \
  "$POSTGRES_IDENT_BOOTSTRAP_SOURCE" /var/lib/postgresql/data/pg_ident.conf AscendAny
install_postgres_access_fixture "$POSTGRES_HBA_BOOTSTRAP_SOURCE" "$POSTGRES_IDENT_BOOTSTRAP_SOURCE" AscendAny

# Rolling bootstrap back to the original pair publishes HBA first. A reboot at
# the one-file boundary cannot leave an HBA reference to a removed ident map.
publish_one_access_member_then_restart \
  "$WORK_ROOT/postgresql-hba-original.conf" /var/lib/postgresql/data/pg_hba.conf AscendAny
publish_one_access_member_then_restart \
  "$WORK_ROOT/postgresql-ident-original.conf" /var/lib/postgresql/data/pg_ident.conf AscendAny
install_postgres_access_fixture "$POSTGRES_HBA_BOOTSTRAP_SOURCE" "$POSTGRES_IDENT_BOOTSTRAP_SOURCE" AscendAny
postgres_access_load_receipt_fixture "$last_access_reload_previous" AscendAny ||
  fail 'freshly loaded bootstrap access files lack a positive receipt'
podman exec --user root "$POSTGRES_CONTAINER" sh -ceu '
  target=/var/lib/postgresql/data/pg_ident.conf
  temporary="${target}.after-reload"
  sleep 1
  cp -- "$target" "$temporary"
  chown postgres:postgres "$temporary"
  chmod 0600 "$temporary"
  sync -f "$temporary"
  mv -f -- "$temporary" "$target"
  sync -f "$(dirname "$target")"
'
if postgres_access_load_receipt_fixture "$last_access_reload_previous" AscendAny; then
  fail 'an access file replaced after reload retained a false loaded-generation receipt'
fi
actual_bootstrap_hba="$(normalized_hba_fixture AscendAny)"
if [[ "$actual_bootstrap_hba" != "$EXPECTED_BOOTSTRAP_HBA" ]]; then
  printf 'Actual bootstrap HBA:\n%s\n' "$actual_bootstrap_hba" >&2
  fail 'bootstrap PostgreSQL HBA catalog shape differs'
fi
[[ "$(normalized_ident_fixture AscendAny)" == "$EXPECTED_BOOTSTRAP_IDENT" ]] ||
  fail 'bootstrap PostgreSQL ident catalog shape differs'

extract_split_sql >"$WORK_ROOT/split-legacy-role.sql"
podman cp "$WORK_ROOT/split-legacy-role.sql" "$POSTGRES_CONTAINER":/tmp/split-legacy-role.sql
podman exec --user root "$POSTGRES_CONTAINER" \
  chown postgres:postgres /tmp/split-legacy-role.sql
podman exec -i --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U AscendAny -d AscendAny --set=ON_ERROR_STOP=1 \
    --set=splitter_role=ascendany_split_fixture \
    --file=/tmp/split-legacy-role.sql >/dev/null

# Leaving bootstrap publishes HBA first. The bootstrap ident still contains the
# final peer map, so a reboot at the one-file boundary preserves cluster-admin
# maintenance access.
publish_one_access_member_then_restart \
  "$POSTGRES_HBA_SOURCE" /var/lib/postgresql/data/pg_hba.conf ascendany_cluster_admin
publish_one_access_member_then_restart \
  "$POSTGRES_IDENT_SOURCE" /var/lib/postgresql/data/pg_ident.conf ascendany_cluster_admin
install_postgres_access_fixture "$POSTGRES_HBA_SOURCE" "$POSTGRES_IDENT_SOURCE" ascendany_cluster_admin
actual_final_hba="$(normalized_hba_fixture ascendany_cluster_admin)"
if [[ "$actual_final_hba" != "$EXPECTED_FINAL_HBA" ]]; then
  printf 'Actual final HBA:\n%s\n' "$actual_final_hba" >&2
  fail 'final PostgreSQL HBA catalog shape differs'
fi
[[ "$(normalized_ident_fixture ascendany_cluster_admin)" == "$EXPECTED_FINAL_IDENT" ]] ||
  fail 'final PostgreSQL ident catalog shape differs'

role_contract="$(podman exec -i --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U ascendany_cluster_admin -d AscendAny --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT admin.oid,
       admin.rolsuper,
       admin.rolinherit,
       admin.rolcreatedb,
       admin.rolcreaterole,
       admin.rolreplication,
       admin.rolbypassrls,
       (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = admin.oid) IS NULL,
       admin.rolpassword IS NULL,
       legacy.oid <> 10,
       legacy.rolsuper,
       legacy.rolinherit,
       (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = legacy.oid) IS NULL,
       legacy.rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$',
       pg_get_userbyid(task.relowner),
       pg_get_userbyid(events.relowner),
       pg_get_userbyid(payload.relowner)
FROM pg_authid AS admin
CROSS JOIN pg_authid AS legacy
JOIN pg_class AS task ON task.oid = 'ascendany.import_tasks'::regclass
JOIN pg_class AS events ON events.oid = 'ascendany.import_task_events'::regclass
JOIN pg_class AS payload ON payload.oid = 'ascendany.payloads'::regclass
WHERE admin.rolname = 'ascendany_cluster_admin'
  AND legacy.rolname = 'AscendAny';
SQL
)"
[[ "$role_contract" == '10|t|f|f|f|f|f|t|t|t|f|t|t|t|AscendAny|AscendAny|ascendany_cluster_admin' ]] ||
  fail 'bootstrap OID split or owner-only DDL transfer differs'
[[ "$(podman exec --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U ascendany_cluster_admin -d AscendAny --tuples-only --no-align \
    --command="SELECT to_regnamespace('pgbouncer') IS NULL")" == t ]] ||
  fail 'obsolete PgBouncer SECURITY DEFINER schema survived the role split'
podman exec --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U ascendany_cluster_admin -d AscendAny --tuples-only --no-align \
    --command="SELECT rolpassword FROM pg_authid WHERE rolname = 'AscendAny'" |
  sha256sum | awk '{print $1}' >"$WORK_ROOT/legacy-verifier-after.sha256"
cmp -s "$WORK_ROOT/legacy-verifier-before.sha256" "$WORK_ROOT/legacy-verifier-after.sha256" ||
  fail 'legacy SCRAM verifier bytes changed during the role split'

if podman exec --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U AscendAny -d AscendAny --command='SELECT 1' >/dev/null 2>&1; then
  fail 'legacy role crossed the final local peer maintenance boundary'
fi

# Rootless Podman forwards a host-published connection with the container IP as
# its observed source. Production rootful Podman uses the locked gateway
# 10.88.0.1. The exact production bytes were already validated above; this
# derived file exercises the same database/user matrix on the fixture topology.
container_ip="$(podman inspect "$POSTGRES_CONTAINER" |
  jq -r '.[0].NetworkSettings.Networks | to_entries | map(.value.IPAddress) | unique | if length == 1 then .[0] else "" end')"
[[ "$container_ip" =~ ^10\.88\.[0-9]+\.[0-9]+$ ]] ||
  fail 'fixture PostgreSQL container IP is invalid after access-generation restart tests'
sed "s/10\.88\.0\.1/$container_ip/g" "$POSTGRES_HBA_SOURCE" >"$WORK_ROOT/postgresql-hba-routing.conf"
install_postgres_access_fixture "$WORK_ROOT/postgresql-hba-routing.conf" "$POSTGRES_IDENT_SOURCE" ascendany_cluster_admin

password_psql() {
  local password="$1" port="$2" role="$3" database="$4" sql="$5"
  { printf '%s\n' "$password"; } |
    psql -X --no-psqlrc --password --host=127.0.0.1 --port="$port" \
      --username="$role" --dbname="$database" --tuples-only --no-align \
      --set=ON_ERROR_STOP=1 --command="$sql"
}

legacy_identity="$(password_psql "$LEGACY_PASSWORD" "$postgres_port" AscendAny AscendAny 'SELECT current_user' \
  2>"$WORK_ROOT/legacy-direct.log" || true)"
if [[ "$legacy_identity" != AscendAny ]]; then
  sed -n '1,40p' "$WORK_ROOT/legacy-direct.log" >&2
  fail 'legacy SCRAM verifier was not preserved across the role split'
fi
password_psql "$LEGACY_PASSWORD" "$postgres_port" AscendAny AscendAny \
  'CREATE INDEX IF NOT EXISTS import_tasks_created_at_idx ON ascendany.import_tasks(created_at)' \
  >/dev/null 2>&1 || fail 'legacy owner-only CREATE INDEX IF NOT EXISTS path failed'

podman exec -i --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U ascendany_cluster_admin -d postgres --set=ON_ERROR_STOP=1 >/dev/null <<SQL
CREATE ROLE ascendanyd_login LOGIN NOSUPERUSER PASSWORD '$RUNTIME_PASSWORD';
CREATE DATABASE ascendany_v2 OWNER ascendanyd_login;
SQL

podman exec -i --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U ascendany_cluster_admin -d postgres --tuples-only --no-align >"$WORK_ROOT/userlist" <<'SQL'
SELECT format('"%s" "%s"', rolname, rolpassword)
FROM pg_authid
WHERE rolname IN ('AscendAny', 'ascendanyd_login')
  AND rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'
ORDER BY CASE rolname WHEN 'AscendAny' THEN 0 ELSE 1 END;
SQL
[[ "$(wc -l <"$WORK_ROOT/userlist")" == 2 ]] || fail 'PgBouncer SCRAM userlist does not contain exactly two roles'
chmod 0600 "$WORK_ROOT/userlist"
cp "$POOL_HBA_SOURCE" "$WORK_ROOT/pgbouncer-hba.conf"
chmod 0600 "$WORK_ROOT/pgbouncer-hba.conf"

pool_port=$((20000 + $$ % 20000))
while ss -H -ltn | awk -v expected="127.0.0.1:$pool_port" \
  '$4 == expected { found=1 } END { exit(found ? 0 : 1) }'; do
  pool_port=$((pool_port + 1))
done
sed \
  -e "s/port=5432/port=$postgres_port/g" \
  -e "s/listen_port = 6432/listen_port = $pool_port/" \
  -e "s#auth_file = .*#auth_file = $WORK_ROOT/userlist#" \
  -e "s#auth_hba_file = .*#auth_hba_file = $WORK_ROOT/pgbouncer-hba.conf#" \
  "$POOL_CONFIG_SOURCE" >"$WORK_ROOT/pgbouncer.ini"
chmod 0600 "$WORK_ROOT/pgbouncer.ini"
pgbouncer -q "$WORK_ROOT/pgbouncer.ini" >"$WORK_ROOT/pgbouncer.log" 2>&1 &
pool_pid="$!"
for attempt in {1..100}; do
  if ss -H -ltn | awk -v expected="127.0.0.1:$pool_port" \
    '$4 == expected { found=1 } END { exit(found ? 0 : 1) }'; then
    break
  fi
  kill -0 "$pool_pid" 2>/dev/null || {
    sed -n '1,120p' "$WORK_ROOT/pgbouncer.log" >&2
    fail 'native PgBouncer exited before binding its fixture port'
  }
  sleep 0.1
done

[[ "$(password_psql "$LEGACY_PASSWORD" "$pool_port" AscendAny AscendAny 'SELECT current_user' 2>/dev/null)" == AscendAny ]] ||
  fail 'legacy route through native PgBouncer failed'
[[ "$(password_psql "$RUNTIME_PASSWORD" "$pool_port" ascendanyd_login ascendany_v2 'SELECT current_user' 2>/dev/null)" == ascendanyd_login ]] ||
  fail 'v2 route through native PgBouncer failed'
if password_psql "$RUNTIME_PASSWORD" "$pool_port" ascendanyd_login AscendAny 'SELECT current_user' >/dev/null 2>&1; then
  fail 'v2 runtime crossed the PgBouncer legacy database boundary'
fi
if password_psql "$LEGACY_PASSWORD" "$pool_port" AscendAny ascendany_v2 'SELECT current_user' >/dev/null 2>&1; then
  fail 'legacy runtime crossed the PgBouncer v2 database boundary'
fi
if password_psql "$RUNTIME_PASSWORD" "$postgres_port" ascendanyd_login AscendAny 'SELECT current_user' >/dev/null 2>&1; then
  fail 'v2 runtime crossed the direct PostgreSQL legacy database boundary'
fi
if password_psql "$LEGACY_PASSWORD" "$postgres_port" AscendAny ascendany_v2 'SELECT current_user' >/dev/null 2>&1; then
  fail 'legacy runtime crossed the direct PostgreSQL v2 database boundary'
fi

password_psql "$RUNTIME_PASSWORD" "$pool_port" ascendanyd_login ascendany_v2 \
  'CREATE TABLE pgbouncer_shutdown_fixture (marker text PRIMARY KEY)' \
  >/dev/null 2>&1 || fail 'shutdown fixture table creation through PgBouncer failed'

printf '127.0.0.1:%s:ascendany_v2:ascendanyd_login:%s\n' "$pool_port" "$RUNTIME_PASSWORD" \
  >"$WORK_ROOT/shutdown.pgpass"
chmod 0600 "$WORK_ROOT/shutdown.pgpass"
mkfifo -m 0600 "$WORK_ROOT/idle-client-input"
exec {idle_client_input_fd}<>"$WORK_ROOT/idle-client-input"
# The writer stays open through the pool shutdown, so the idle client cannot
# disconnect voluntarily and a WAIT_FOR_CLIENTS shutdown cannot satisfy the bound.
PGPASSFILE="$WORK_ROOT/shutdown.pgpass" \
  stdbuf -oL -eL psql -X --no-psqlrc --host=127.0.0.1 --port="$pool_port" \
    --username=ascendanyd_login --dbname=ascendany_v2 --tuples-only --no-align \
    --set=ON_ERROR_STOP=1 <"$WORK_ROOT/idle-client-input" \
    >"$WORK_ROOT/idle-client.log" 2>&1 &
idle_client_pid="$!"
printf 'SELECT 1;\n' >&"$idle_client_input_fd"
idle_ready=0
for attempt in {1..100}; do
  if grep -qx 1 "$WORK_ROOT/idle-client.log"; then
    idle_ready=1
    break
  fi
  kill -0 "$idle_client_pid" 2>/dev/null || {
    sed -n '1,80p' "$WORK_ROOT/idle-client.log" >&2
    fail 'persistent idle psql client exited before shutdown'
  }
  sleep 0.1
done
[[ "$idle_ready" == 1 ]] || fail 'persistent idle psql client did not become ready'

PGPASSFILE="$WORK_ROOT/shutdown.pgpass" \
  psql -X --no-psqlrc --host=127.0.0.1 --port="$pool_port" \
    --username=ascendanyd_login --dbname=ascendany_v2 --set=ON_ERROR_STOP=1 \
    --command=$'BEGIN;\nINSERT INTO pgbouncer_shutdown_fixture(marker) VALUES (\'committed-after-sigint\');\nSELECT pg_sleep(5);\nCOMMIT;' \
    >"$WORK_ROOT/active-client.log" 2>&1 &
active_client_pid="$!"
active_ready=0
for attempt in {1..100}; do
  active_count="$(podman exec --user postgres "$POSTGRES_CONTAINER" \
    psql -X --no-psqlrc -U ascendany_cluster_admin -d ascendany_v2 --tuples-only --no-align \
      --command="SELECT count(*) FROM pg_stat_activity WHERE usename = 'ascendanyd_login' AND datname = 'ascendany_v2' AND state = 'active' AND query LIKE '%pg_sleep(5)%'" \
      2>/dev/null || true)"
  if [[ "$active_count" == 1 ]]; then
    active_ready=1
    break
  fi
  kill -0 "$active_client_pid" 2>/dev/null || {
    sed -n '1,80p' "$WORK_ROOT/active-client.log" >&2
    fail 'active transaction exited before PgBouncer shutdown'
  }
  sleep 0.1
done
[[ "$active_ready" == 1 ]] || fail 'active transaction did not reach PostgreSQL before shutdown'
kill -0 "$idle_client_pid" 2>/dev/null || fail 'idle psql client was not persistent at shutdown'

kill -INT "$pool_pid"
shutdown_complete=0
for attempt in {1..150}; do
  if ! kill -0 "$pool_pid" 2>/dev/null; then
    shutdown_complete=1
    break
  fi
  sleep 0.1
done
[[ "$shutdown_complete" == 1 ]] ||
  fail 'SIGINT shutdown waited on the persistent idle client or exceeded its bound'
pool_status=0
wait "$pool_pid" || pool_status=$?
pool_pid=''
[[ "$pool_status" == 0 ]] || fail "SIGINT shutdown exited with status $pool_status"

active_status=0
wait "$active_client_pid" || active_status=$?
active_client_pid=''
if [[ "$active_status" != 0 ]]; then
  sed -n '1,80p' "$WORK_ROOT/active-client.log" >&2
  fail "active transaction client exited with status $active_status"
fi
committed_marker="$(password_psql "$RUNTIME_PASSWORD" "$postgres_port" ascendanyd_login ascendany_v2 \
  "SELECT marker FROM pgbouncer_shutdown_fixture WHERE marker = 'committed-after-sigint'" 2>/dev/null || true)"
[[ "$committed_marker" == committed-after-sigint ]] ||
  fail 'SIGINT shutdown did not preserve the active transaction commit'

kill "$idle_client_pid" >/dev/null 2>&1 || true
wait "$idle_client_pid" 2>/dev/null || true
idle_client_pid=''

podman exec -i --user postgres "$POSTGRES_CONTAINER" \
  psql -X --no-psqlrc -U ascendany_cluster_admin -d postgres --set=ON_ERROR_STOP=1 >/dev/null <<'SQL'
DROP DATABASE ascendany_v2;
DROP ROLE ascendanyd_login;
SQL
awk '
  /^rollback_v2[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >"$WORK_ROOT/rollback-v2-function.sh"
postgres_psql() {
  podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    psql -X --no-psqlrc -U ascendany_cluster_admin --set=ON_ERROR_STOP=1 "$@"
}
run_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
marker_role="ascendany_v2_marker_${run_id}"
recovery_failed=0
postgres_psql --dbname=postgres --set=marker_role="$marker_role" --set=run_id="$run_id" >/dev/null <<'SQL'
SELECT format('CREATE ROLE %I NOLOGIN', :'marker_role')
\gexec
SELECT format('COMMENT ON ROLE %I IS %L', :'marker_role', 'ascendany.v2.provision:' || :'run_id')
\gexec
SELECT format('CREATE DATABASE ascendany_v2 OWNER %I TEMPLATE template0', :'marker_role')
\gexec
SQL
# shellcheck source=/dev/null
source "$WORK_ROOT/rollback-v2-function.sh"
rollback_v2
[[ "$recovery_failed" == 0 ]] ||
  fail 'recovery rejected the run-owned database state between CREATE DATABASE and COMMENT'
[[ "$(postgres_psql --dbname=postgres --tuples-only --no-align \
  --command="SELECT count(*) FROM pg_database WHERE datname = 'ascendany_v2'")" == 0 ]] ||
  fail 'recovery retained the uncommented run-owned database transition'
[[ "$(postgres_psql --dbname=postgres --tuples-only --no-align \
  --command="SELECT count(*) FROM pg_roles WHERE rolname = '$marker_role'")" == 0 ]] ||
  fail 'recovery retained the run marker after the uncommented database transition'

awk '
  /^container_exists[(][)] [{]$/ { emit = 1 }
  /^postgres_psql_as[(][)] [{]$/ { exit }
  emit { print }
' "$PROVISIONER" >"$WORK_ROOT/container-restart-functions.sh"
# shellcheck source=/dev/null
source "$WORK_ROOT/container-restart-functions.sh"
podman run -d --name "$restart_fixture_container" --restart=always \
  --entrypoint /bin/sh postgres:17 -c 'trap "exit 0" INT TERM; while :; do sleep 1; done' >/dev/null
[[ "$(container_restart_policy "$restart_fixture_container")" == always ]] ||
  fail 'restart fixture did not start with the legacy always policy'
set_container_restart_policy "$restart_fixture_container" no ||
  fail 'restart ownership fence rejected the no policy'
stop_container_if_running "$restart_fixture_container" ||
  fail 'restart ownership fence did not stop the old container'
podman rename "$restart_fixture_container" "$restart_fixture_rollback"
podman start "$restart_fixture_rollback" >/dev/null
stop_container_if_running "$restart_fixture_rollback" ||
  fail 'recovery did not quiesce a reboot-started rollback container'
podman rename "$restart_fixture_rollback" "$restart_fixture_container"
set_container_restart_policy "$restart_fixture_container" always ||
  fail 'legacy recovery did not restore the always restart policy'
podman start "$restart_fixture_container" >/dev/null
container_running "$restart_fixture_container" ||
  fail 'legacy recovery did not restore the running container state'
podman rm -f "$restart_fixture_container" >/dev/null

awk '
  /^legacy_pool_semantics_match[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >"$WORK_ROOT/legacy-pool-ownership-functions.sh"
awk '
  /^legacy_pool_selinux_context_matches[(][)] [{]$/ { emit = 1 }
  /^switch_to_native_pool[(][)] [{]$/ { exit }
  emit { print }
' "$PROVISIONER" >>"$WORK_ROOT/legacy-pool-ownership-functions.sh"
podman cp "$WORK_ROOT/legacy-pool-ownership-functions.sh" \
  "$POSTGRES_CONTAINER":/tmp/legacy-pool-ownership-functions.sh
podman cp "$POOL_CONFIG_SOURCE" "$POSTGRES_CONTAINER":/tmp/native-pgbouncer.ini
podman cp "$POOL_HBA_SOURCE" "$POSTGRES_CONTAINER":/tmp/native-pgbouncer-hba.conf
podman exec -i --user root "$POSTGRES_CONTAINER" /usr/bin/bash -s <<'FIXTURE'
set -Eeuo pipefail
fail() {
  printf 'legacy pool ownership fixture: %s\n' "$2" >&2
  exit 1
}
STATE_ROOT=/tmp/legacy-pool-ownership
POOL_CONFIG_ROOT="$STATE_ROOT/parent/pgbouncer"
legacy_pool_manifest="$STATE_ROOT/state/legacy-pgbouncer-fixture.manifest"
legacy_pool_ini_backup="$STATE_ROOT/state/legacy-pgbouncer-fixture.pgbouncer.ini"
legacy_pool_userlist_backup="$STATE_ROOT/state/legacy-pgbouncer-fixture.userlist.txt"
POOL_CONFIG_SOURCE=/tmp/native-pgbouncer.ini
POOL_HBA_SOURCE=/tmp/native-pgbouncer-hba.conf
run_id=0123456789abcdef0123456789abcdef
install -d -o root -g root -m 0700 "$STATE_ROOT/parent" "$POOL_CONFIG_ROOT" "$STATE_ROOT/state"
head -c 494 /dev/zero | tr '\0' x >"$POOL_CONFIG_ROOT/pgbouncer.ini"
head -c 50 /dev/zero | tr '\0' y >"$POOL_CONFIG_ROOT/userlist.txt"
chown 70:70 "$POOL_CONFIG_ROOT" "$POOL_CONFIG_ROOT/pgbouncer.ini" "$POOL_CONFIG_ROOT/userlist.txt"
chmod 0700 "$POOL_CONFIG_ROOT"
chmod 0644 "$POOL_CONFIG_ROOT/pgbouncer.ini"
chmod 0600 "$POOL_CONFIG_ROOT/userlist.txt"
legacy_pool_mount_label="$(stat -Lc '%C' "$POOL_CONFIG_ROOT")"
# shellcheck source=/dev/null
source /tmp/legacy-pool-ownership-functions.sh

actual_mount_label="$legacy_pool_mount_label"
legacy_pool_mount_label="${actual_mount_label/container_file_t/usr_t}"
if legacy_pool_structure_matches "$POOL_CONFIG_ROOT" legacy; then
  printf 'legacy pool tree accepted a context outside the container mount label\n' >&2
  exit 1
fi
legacy_pool_mount_label="$actual_mount_label"

label_fixture="$STATE_ROOT/label-fixture"
install -d -o root -g root -m 0700 "$label_fixture"
touch "$label_fixture/entry"
label_legacy_pool_entry_for_publish "$label_fixture" "$label_fixture/entry"
[[ "$(stat -Lc '%C' "$label_fixture")" == "$(stat -Lc '%C' "$label_fixture/entry")" ]]
rm -rf "$label_fixture"

saved_pool_config_root="$POOL_CONFIG_ROOT"
saved_legacy_pool_manifest="$legacy_pool_manifest"
saved_legacy_pool_ini_backup="$legacy_pool_ini_backup"
saved_legacy_pool_userlist_backup="$legacy_pool_userlist_backup"
semantic_pool="$STATE_ROOT/parent/semantic-pgbouncer"
capture_state="$STATE_ROOT/capture-state"
POOL_CONFIG_ROOT="$semantic_pool"
legacy_pool_manifest="$capture_state/legacy-pgbouncer-${run_id}.manifest"
legacy_pool_ini_backup="$capture_state/legacy-pgbouncer-${run_id}.pgbouncer.ini"
legacy_pool_userlist_backup="$capture_state/legacy-pgbouncer-${run_id}.userlist.txt"
LEGACY_ROLE=AscendAny
phase=prepared
password="$(printf 'a%.0s' {1..40})"
install -d -o 70 -g 70 -m 0700 "$semantic_pool"
cat >"$semantic_pool/pgbouncer.ini" <<SEMANTIC_CONFIG
[databases]
AscendAny = host = 127.0.0.1 port=5432 user=AscendAny password=$password dbname=AscendAny
[pgbouncer]
listen_addr = 127.0.0.1
listen_port = 6432
auth_file = /etc/pgbouncer/userlist.txt
auth_type = md5
pool_mode = transaction
max_client_conn = 100
default_pool_size = 20
ignore_startup_parameters = extra_float_digits
admin_users = AscendAny
SEMANTIC_CONFIG
remaining=$((494 - $(stat -Lc '%s' "$semantic_pool/pgbouncer.ini")))
((remaining >= 2))
printf '#' >>"$semantic_pool/pgbouncer.ini"
head -c "$((remaining - 2))" /dev/zero | tr '\0' x >>"$semantic_pool/pgbouncer.ini"
printf '\n' >>"$semantic_pool/pgbouncer.ini"
verifier="$(printf '%s' "${password}${LEGACY_ROLE}" | md5sum)"
printf '"AscendAny" "md5%s"\n' "${verifier%% *}" >"$semantic_pool/userlist.txt"
chown 70:70 "$semantic_pool/pgbouncer.ini" "$semantic_pool/userlist.txt"
chmod 0644 "$semantic_pool/pgbouncer.ini"
chmod 0600 "$semantic_pool/userlist.txt"
[[ "$(stat -Lc '%s' "$semantic_pool/pgbouncer.ini")" == 494 &&
   "$(stat -Lc '%s' "$semantic_pool/userlist.txt")" == 50 ]]

for capture_point in 0 1 2 3 4 5; do
  rm -rf -- "$capture_state"
  install -d -o root -g root -m 0700 "$capture_state"
  ((capture_point < 1)) || install -o root -g root -m 0600 \
    "$semantic_pool/pgbouncer.ini" "$legacy_pool_ini_backup.tmp"
  ((capture_point < 2)) || mv "$legacy_pool_ini_backup.tmp" "$legacy_pool_ini_backup"
  ((capture_point < 3)) || install -o root -g root -m 0600 \
    "$semantic_pool/userlist.txt" "$legacy_pool_userlist_backup.tmp"
  ((capture_point < 4)) || mv "$legacy_pool_userlist_backup.tmp" "$legacy_pool_userlist_backup"
  if ((capture_point >= 5)); then
    printf '%s\n' \
      'schema=ascendany.legacy-pgbouncer-manifest.v1' \
      "pgbouncer.ini|$(sha256sum "$semantic_pool/pgbouncer.ini" | awk '{print $1}')" \
      "userlist.txt|$(sha256sum "$semantic_pool/userlist.txt" | awk '{print $1}')" \
      >"$legacy_pool_manifest.tmp"
    chmod 0600 "$legacy_pool_manifest.tmp"
  fi
  durably_verify_recovered_legacy_pool_tree || {
    printf 'prepared recovery rejected capture interruption point %s\n' "$capture_point" >&2
    exit 1
  }
done

mv "$legacy_pool_manifest.tmp" "$legacy_pool_manifest"
durably_verify_recovered_legacy_pool_tree
printf '%s\n' 'schema=corrupt' >"$legacy_pool_manifest"
chmod 0600 "$legacy_pool_manifest"
if durably_verify_recovered_legacy_pool_tree; then
  printf 'completed corrupt legacy manifest fell back to uncaptured semantics\n' >&2
  exit 1
fi
rm -rf -- "$semantic_pool" "$capture_state"
POOL_CONFIG_ROOT="$saved_pool_config_root"
legacy_pool_manifest="$saved_legacy_pool_manifest"
legacy_pool_ini_backup="$saved_legacy_pool_ini_backup"
legacy_pool_userlist_backup="$saved_legacy_pool_userlist_backup"

capture_legacy_pool_manifest
normalize_legacy_pool_for_rollback
chmod 0755 "$STATE_ROOT/parent"
if setpriv --reuid=70 --regid=70 --clear-groups test -r "$POOL_CONFIG_ROOT/userlist.txt"; then
  printf 'normalized rollback userlist remained readable by host uid 70\n' >&2
  exit 1
fi

chown 70:70 "$POOL_CONFIG_ROOT/pgbouncer.ini"
restore_legacy_pool_bytes "$POOL_CONFIG_ROOT" legacy
[[ "$(stat -Lc '%u:%g:%a' "$POOL_CONFIG_ROOT")" == 70:70:700 &&
   "$(stat -Lc '%u:%g:%a' "$POOL_CONFIG_ROOT/pgbouncer.ini")" == 70:70:644 &&
   "$(stat -Lc '%u:%g:%a' "$POOL_CONFIG_ROOT/userlist.txt")" == 70:70:600 ]]
legacy_pool_selinux_context_matches "$POOL_CONFIG_ROOT"
setpriv --reuid=70 --regid=70 --clear-groups test -r "$POOL_CONFIG_ROOT/userlist.txt"

normalize_legacy_pool_for_rollback
head -c 7 "$legacy_pool_userlist_backup" >"$POOL_CONFIG_ROOT/userlist.txt"
chown 0:0 "$POOL_CONFIG_ROOT/userlist.txt"
chmod 0600 "$POOL_CONFIG_ROOT/userlist.txt"
restore_legacy_pool_bytes "$POOL_CONFIG_ROOT" legacy
legacy_pool_tree_matches "$POOL_CONFIG_ROOT" legacy

touch "$POOL_CONFIG_ROOT/foreign"
if restore_legacy_pool_bytes "$POOL_CONFIG_ROOT" legacy; then
  printf 'foreign rollback entry was accepted for protected-byte restoration\n' >&2
  exit 1
fi
rm -f "$POOL_CONFIG_ROOT/foreign"
for removed_count in 0 1 2; do
  legacy_path="$STATE_ROOT/parent/legacy-delete-$removed_count"
  install -d -o root -g root -m 0700 "$legacy_path"
  head -c 494 /dev/zero | tr '\0' x >"$legacy_path/pgbouncer.ini"
  head -c 50 /dev/zero | tr '\0' y >"$legacy_path/userlist.txt"
  chmod 0644 "$legacy_path/pgbouncer.ini"
  chmod 0600 "$legacy_path/userlist.txt"
  deletion="${legacy_path}.deleting-${run_id}"
  mv "$legacy_path" "$deletion"
  ((removed_count < 1)) || rm -f "$deletion/pgbouncer.ini"
  ((removed_count < 2)) || rm -f "$deletion/userlist.txt"
  remove_legacy_pool_tree "$legacy_path"
  [[ ! -e "$legacy_path" && ! -e "$deletion" ]]
done

for removed_count in 0 1 2; do
  native_path="$STATE_ROOT/parent/native-delete-$removed_count"
  install -d -o root -g root -m 0755 "$native_path"
  install -o root -g root -m 0644 "$POOL_CONFIG_SOURCE" "$native_path/pgbouncer.ini"
  install -o root -g root -m 0644 "$POOL_HBA_SOURCE" "$native_path/pgbouncer-hba.conf"
  deletion="${native_path}.deleting-${run_id}"
  mv "$native_path" "$deletion"
  ((removed_count < 1)) || rm -f "$deletion/pgbouncer.ini"
  ((removed_count < 2)) || rm -f "$deletion/pgbouncer-hba.conf"
  remove_native_pool_tree "$native_path"
  [[ ! -e "$native_path" && ! -e "$deletion" ]]
done

for staged_count in 0 1 2; do
  stage_path="$STATE_ROOT/parent/native-stage-$staged_count"
  install -d -o root -g root -m 0755 "$stage_path"
  ((staged_count < 1)) || install -o root -g root -m 0644 \
    "$POOL_CONFIG_SOURCE" "$stage_path/pgbouncer.ini"
  ((staged_count < 2)) || install -o root -g root -m 0644 \
    "$POOL_HBA_SOURCE" "$stage_path/pgbouncer-hba.conf"
  remove_native_pool_stage_tree "$stage_path"
  [[ ! -e "$stage_path" && ! -e "${stage_path}.deleting-${run_id}" ]]
done
partial_stage="$STATE_ROOT/parent/native-stage-partial-copy"
install -d -o root -g root -m 0755 "$partial_stage"
head -c 17 "$POOL_CONFIG_SOURCE" >"$partial_stage/.pgbouncer.ini.tmp"
chmod 0600 "$partial_stage/.pgbouncer.ini.tmp"
remove_native_pool_stage_tree "$partial_stage"
[[ ! -e "$partial_stage" && ! -e "${partial_stage}.deleting-${run_id}" ]]
partial_stage_directory="$STATE_ROOT/parent/native-stage-partial-directory"
install -d -o root -g root -m 0700 "$partial_stage_directory"
remove_native_pool_stage_tree "$partial_stage_directory"
[[ ! -e "$partial_stage_directory" &&
   ! -e "${partial_stage_directory}.deleting-${run_id}" ]]
FIXTURE

awk '
  /^journal_file_matches_run[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >"$WORK_ROOT/consume-committed-state-root.sh"
awk '
  /^consume_recovered_state_root[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >>"$WORK_ROOT/consume-committed-state-root.sh"
awk '
  /^consume_committed_state_root[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >>"$WORK_ROOT/consume-committed-state-root.sh"
# shellcheck source=/dev/null
source "$WORK_ROOT/consume-committed-state-root.sh"
awk '
  /^tombstone_recovered_state_root[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" |
  sed -e 's#/usr/bin/sync#fixture_sync#g' -e 's#/usr/bin/mv#fixture_mv#g' \
    >"$WORK_ROOT/tombstone-recovered-state-root.sh"
# shellcheck source=/dev/null
source "$WORK_ROOT/tombstone-recovered-state-root.sh"
fixture_sync_calls=0
fixture_sync_fail_at=0
fixture_mv_fails=0
fixture_sync() {
  fixture_sync_calls=$((fixture_sync_calls + 1))
  if [[ "$fixture_sync_calls" == "$fixture_sync_fail_at" ]]; then
    return 1
  fi
  command sync "$@"
}
fixture_mv() {
  ((fixture_mv_fails == 0)) || return 1
  command mv "$@"
}
terminal_parent="$WORK_ROOT/recovery-terminal-parent"
STATE_ROOT="$terminal_parent/state"
for terminal_failpoint in state_sync rename parent_sync; do
  rm -rf -- "$STATE_ROOT" "${STATE_ROOT}.recovered-fixture"
  install -d -m 0700 "$STATE_ROOT"
  printf '%s\n' durable-journal >"$STATE_ROOT/journal"
  fixture_sync_calls=0
  fixture_sync_fail_at=0
  fixture_mv_fails=0
  case "$terminal_failpoint" in
    state_sync) fixture_sync_fail_at=1 ;;
    rename) fixture_mv_fails=1 ;;
    parent_sync) fixture_sync_fail_at=2 ;;
  esac
  if tombstone_recovered_state_root "${STATE_ROOT}.recovered-fixture"; then
    fail "recovery terminal failpoint succeeded unexpectedly: $terminal_failpoint"
  fi
  case "$terminal_failpoint" in
    state_sync|rename)
      [[ -f "$STATE_ROOT/journal" && ! -e "${STATE_ROOT}.recovered-fixture" ]] ||
        fail "active recovery journal was consumed at failpoint: $terminal_failpoint"
      ;;
    parent_sync)
      [[ ! -e "$STATE_ROOT" && -f "${STATE_ROOT}.recovered-fixture/journal" ]] ||
        fail 'recovered journal tombstone was consumed before its parent fsync'
      ;;
  esac
done
rm -rf -- "$STATE_ROOT" "${STATE_ROOT}.recovered-fixture"

finalize_parent="$WORK_ROOT/finalize-parent"
finalize_run_id=0123456789abcdef0123456789abcdef
finalize_state="$finalize_parent/state"
finalize_manifest="$finalize_state/credentials-${finalize_run_id}.manifest"
install -d -m 0700 "$finalize_parent" "$finalize_state"
printf '%s\n' \
  'schema=ascendany.postgres-pgbouncer.provision.v1' \
  "run_id=$finalize_run_id" \
  'phase=committed' \
  "marker_role=ascendany_v2_marker_${finalize_run_id}" >"$finalize_state/journal"
printf '%s\n' \
  'runtime_db_password.cred|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  'migrator_db_password.cred|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' \
  'backup_db_password.cred|cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' \
  'restore_db_password.cred|dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd' \
  'pgbouncer_userlist.cred|eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee' \
  >"$finalize_manifest"
chmod 0600 "$finalize_state/journal" "$finalize_manifest"
consume_committed_state_root "$finalize_state" "$finalize_run_id" "$finalize_manifest"
[[ ! -e "$finalize_state" && ! -e "${finalize_state}.committed-${finalize_run_id}" ]] ||
  fail 'committed state finalization left its state root or tombstone behind'

for removed_count in 0 1 2; do
  replay_run_id="$(printf '%032x' "$((removed_count + 16))")"
  replay_state="$finalize_parent/replay-$removed_count"
  replay_manifest="$replay_state/credentials-${replay_run_id}.manifest"
  replay_tombstone="${replay_state}.committed-${replay_run_id}"
  install -d -m 0700 "$replay_tombstone"
  printf '%s\n' \
    'schema=ascendany.postgres-pgbouncer.provision.v1' \
    "run_id=$replay_run_id" \
    'phase=committed' \
    "marker_role=ascendany_v2_marker_${replay_run_id}" >"$replay_tombstone/journal"
  printf '%s\n' \
    'runtime_db_password.cred|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
    'migrator_db_password.cred|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' \
    'backup_db_password.cred|cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' \
    'restore_db_password.cred|dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd' \
    'pgbouncer_userlist.cred|eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee' \
    >"$replay_tombstone/credentials-${replay_run_id}.manifest"
  chmod 0600 "$replay_tombstone/journal" "$replay_tombstone/credentials-${replay_run_id}.manifest"
  ((removed_count < 1)) || rm -f "$replay_tombstone/credentials-${replay_run_id}.manifest"
  ((removed_count < 2)) || rm -f "$replay_tombstone/journal"
  consume_committed_state_root "$replay_state" "$replay_run_id" "$replay_manifest"
  [[ ! -e "$replay_tombstone" ]] || fail 'committed tombstone replay left terminal state behind'
done

STATE_ROOT="$finalize_parent/recovered-state"
for removed_count in 0 1 2; do
  recovered_run_id="$(printf '%032x' "$((removed_count + 32))")"
  recovered_tombstone="${STATE_ROOT}.recovered-${recovered_run_id}"
  install -d -m 0700 "$recovered_tombstone"
  printf '%s\n' \
    'schema=ascendany.postgres-pgbouncer.provision.v1' \
    "run_id=$recovered_run_id" \
    'phase=native_active' \
    "marker_role=ascendany_v2_marker_${recovered_run_id}" >"$recovered_tombstone/journal"
  printf '%s\n' fixture >"$recovered_tombstone/legacy-pgbouncer-${recovered_run_id}.manifest"
  chmod 0600 "$recovered_tombstone/journal" \
    "$recovered_tombstone/legacy-pgbouncer-${recovered_run_id}.manifest"
  ((removed_count < 1)) || rm -f "$recovered_tombstone/legacy-pgbouncer-${recovered_run_id}.manifest"
  ((removed_count < 2)) || rm -f "$recovered_tombstone/journal"
  consume_recovered_state_root "$recovered_run_id"
  [[ ! -e "$recovered_tombstone" ]] || fail 'recovered tombstone replay left terminal state behind'
done

awk '
  /^consume_initializing_state_roots[(][)] [{]$/ { emit = 1 }
  emit { print }
  emit && /^}$/ { exit }
' "$PROVISIONER" >"$WORK_ROOT/consume-initializing-state-roots.sh"
# shellcheck source=/dev/null
source "$WORK_ROOT/consume-initializing-state-roots.sh"
STATE_ROOT="$finalize_parent/initial-state"
for initial_case in empty partial; do
  if [[ "$initial_case" == empty ]]; then
    initial_run_id="$(printf '%032x' 48)"
  else
    initial_run_id="$(printf '%032x' 49)"
  fi
  initial_tombstone="${STATE_ROOT}.initializing-${initial_run_id}"
  install -d -m 0700 "$initial_tombstone"
  if [[ "$initial_case" == partial ]]; then
    printf '%s' 'partial-first-journal' >"$initial_tombstone/journal"
    chmod 0600 "$initial_tombstone/journal"
  fi
  consume_initializing_state_roots
  [[ ! -e "$initial_tombstone" ]] || fail 'initializing tombstone replay left terminal state behind'
done

unexpected_run_id=fedcba9876543210fedcba9876543210
unexpected_state="$finalize_parent/unexpected"
unexpected_manifest="$unexpected_state/credentials-${unexpected_run_id}.manifest"
install -d -m 0700 "$unexpected_state"
printf '%s\n' 'schema=fixture' >"$unexpected_state/journal"
printf '%s\n' 'runtime_db_password.cred|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' \
  >"$unexpected_manifest"
printf '%s\n' foreign >"$unexpected_state/foreign"
chmod 0600 "$unexpected_state/journal" "$unexpected_manifest" "$unexpected_state/foreign"
if (consume_committed_state_root "$unexpected_state" "$unexpected_run_id" "$unexpected_manifest") \
    >/dev/null 2>&1; then
  fail 'committed state finalization consumed an unexpected entry'
fi
[[ -d "$unexpected_state" && -f "$unexpected_state/foreign" ]] ||
  fail 'rejected committed state was modified before direct failure'
if grep -E '(admin|legacy|auth)[.]rolconfig' "$PROVISIONER" "$PRODUCTION_VALIDATOR" >/dev/null; then
  fail 'a production catalog query reads rolconfig from pg_authid instead of pg_roles'
fi

printf 'provision PostgreSQL/PgBouncer fixture: PASS\n'
