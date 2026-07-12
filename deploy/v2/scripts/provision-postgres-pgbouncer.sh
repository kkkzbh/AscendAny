#!/usr/bin/bash -p
set -Eeuo pipefail

readonly SELF="$(/usr/bin/readlink -e -- "${BASH_SOURCE[0]}")"
if [[ "${ASCENDANY_PROVISION_CLEAN_ENV-}" != 1 ]]; then
  exec /usr/bin/env -i \
    PATH=/usr/bin:/bin \
    LC_ALL=C \
    ASCENDANY_PROVISION_CLEAN_ENV=1 \
    /usr/bin/bash -p "$SELF" "$@"
fi

while IFS='=' read -r environment_name _; do
  case "$environment_name" in
    ASCENDANY_PROVISION_CLEAN_ENV|LC_ALL|PATH|PWD|SHLVL|_)
      ;;
    *)
      printf 'FAIL [environment]: provisioning requires the canonical clean environment\n' >&2
      exit 1
      ;;
  esac
done < <(/usr/bin/env)
unset ASCENDANY_PROVISION_CLEAN_ENV

export PATH=/usr/bin:/bin
export LC_ALL=C
umask 077

readonly RELEASE_ROOT="$(cd -- "$(dirname -- "$SELF")/.." && pwd -P)"
readonly ROLE_BOOTSTRAP="$RELEASE_ROOT/db/roles/001_v2_roles.sql"
readonly POOL_CONFIG_SOURCE="$RELEASE_ROOT/config/pgbouncer.ini"
readonly POOL_HBA_SOURCE="$RELEASE_ROOT/config/pgbouncer-hba.conf"
readonly POSTGRES_HBA_BOOTSTRAP_SOURCE="$RELEASE_ROOT/config/postgresql-hba-bootstrap.conf"
readonly POSTGRES_HBA_SOURCE="$RELEASE_ROOT/config/postgresql-hba.conf"
readonly POSTGRES_IDENT_BOOTSTRAP_SOURCE="$RELEASE_ROOT/config/postgresql-ident-bootstrap.conf"
readonly POSTGRES_IDENT_SOURCE="$RELEASE_ROOT/config/postgresql-ident.conf"
readonly PACKAGE_LOCK="$RELEASE_ROOT/config/fedora-runtime-packages.json"
readonly POOL_UNIT_SOURCE="$RELEASE_ROOT/systemd/ascendany-pgbouncer.service"

readonly POSTGRES_CONTAINER=ascendany-postgres
readonly OLD_POOL_CONTAINER=ascendany-pgbouncer
readonly LEGACY_API_UNIT=ascendany-api.service
readonly POOL_UNIT=ascendany-pgbouncer.service
readonly PACKAGE_POOL_UNIT=pgbouncer.service
readonly POOL_UNIT_INSTALLED=/etc/systemd/system/ascendany-pgbouncer.service
readonly POOL_GLOBAL_DROPIN=/usr/lib/systemd/system/service.d/10-timeout-abort.conf
readonly LEGACY_API_UNIT_PATH=/etc/systemd/system/ascendany-api.service
readonly LEGACY_API_UNIT_SHA256=0bd410e55b60bca5d3e91f8f2f733b02fe0d46fb1ca7ecb8320f1ecb7a2b6ca9
readonly LEGACY_API_ENV_PATH=/etc/ascendany/api.env
readonly LEGACY_API_GLOBAL_DROPIN_SHA256=ae6b234f92bc22f1201a7572b59b454c9809f33c80d13f361b9674e1801acc37
readonly LEGACY_DATABASE=AscendAny
readonly LEGACY_ROLE=AscendAny
readonly CLUSTER_ADMIN_ROLE=ascendany_cluster_admin
readonly V2_DATABASE=ascendany_v2
readonly POSTGRES_NETWORK=podman
readonly POSTGRES_GATEWAY=10.88.0.1
readonly POSTGRES_ADDRESS=10.88.0.2
readonly POSTGRES_SUBNET=10.88.0.0/16
readonly POSTGRES_HBA_PATH=/var/lib/postgresql/data/pg_hba.conf
readonly POSTGRES_IDENT_PATH=/var/lib/postgresql/data/pg_ident.conf
readonly OLD_POOL_IMAGE_ID=eb9e09528efac231afef1e41dfb0346e239b242c7728881dddce7e6984a97370

readonly INPUT_ROOT=/run/ascendany-v2-provision
readonly CREDENTIAL_ROOT=/etc/ascendany/credentials
readonly POOL_PARENT=/opt/ascendany/infra
readonly POOL_CONFIG_ROOT="$POOL_PARENT/pgbouncer"
readonly STATE_ROOT=/var/lib/ascendany-pgbouncer-provision
readonly JOURNAL_PATH="$STATE_ROOT/journal"
readonly PROVISION_UNIT=ascendany-postgres-pgbouncer-provision.service
readonly LEGACY_POOL_CONMON_SCOPE=ascendany-legacy-pgbouncer-conmon.scope

readonly RUNTIME_PASSWORD_FILE="$INPUT_ROOT/runtime_db_password"
readonly MIGRATOR_PASSWORD_FILE="$INPUT_ROOT/migrator_db_password"
readonly BACKUP_PASSWORD_FILE="$INPUT_ROOT/backup_db_password"
readonly RESTORE_PASSWORD_FILE="$INPUT_ROOT/restore_db_password"

run_id=''
phase=''
marker_role=''
splitter_role=''
old_pool_rollback=''
config_stage=''
config_rollback=''
credential_stage=''
credential_manifest=''
legacy_pool_manifest=''
legacy_pool_ini_backup=''
legacy_pool_userlist_backup=''
hba_backup=''
hba_backup_sha=''
ident_backup=''
ident_backup_sha=''
maintenance_role="$LEGACY_ROLE"
postgres_image_id=''
recovery_failed=0
entry_role_state=''
legacy_pool_mount_label=''
legacy_pool_password=''
legacy_api_previous_pid=0

usage() {
  cat >&2 <<'USAGE'
Usage:
  sudo /opt/ascendany/v2/scripts/provision-postgres-pgbouncer.sh \
    --confirm-fresh-database ascendany_v2 \
    --confirm-legacy-connect-closure AscendAny \
    --confirm-pgbouncer-replacement ascendany-pgbouncer

Required protected plaintext inputs (root:root 0600, no trailing newline):
  /run/ascendany-v2-provision/runtime_db_password
  /run/ascendany-v2-provision/migrator_db_password
  /run/ascendany-v2-provision/backup_db_password
  /run/ascendany-v2-provision/restore_db_password

Host prerequisites:
  - Fedora 44 pgbouncer-1.25.2-1.fc44.x86_64 is installed and attested
    against /opt/ascendany/v2/config/fedora-runtime-packages.json.
  - package pgbouncer.service is masked and inactive.
  - release-owned ascendany-pgbouncer.service is installed, disabled, and
    inactive; systemd has no pending daemon reload.
  - the existing PostgreSQL container and old PgBouncer container are running.

The command atomically splits the PostgreSQL bootstrap identity from the old
application login, closes PostgreSQL and PgBouncer database/user routes, creates
fresh ascendany_v2, publishes encrypted systemd credentials, and replaces the
old container with the hardened native DynamicUser service. A root-only durable
journal fences every pre-commit mutation and drives exact recovery after a
failure or reboot. Plaintext inputs are consumed only after commit.
USAGE
}

fail() {
  printf 'FAIL [%s]: %s\n' "${1:-unknown}" "${2:-provisioning failed}" >&2
  exit 1
}

pass() {
  printf 'PASS [%s]\n' "$1"
}

require_exact_args() {
  [[ "$#" == 6 ]] || { usage; fail arguments 'six exact argument tokens are required'; }
  [[ "$1" == --confirm-fresh-database && "$2" == "$V2_DATABASE" ]] ||
    fail arguments 'fresh database confirmation is invalid'
  [[ "$3" == --confirm-legacy-connect-closure && "$4" == "$LEGACY_DATABASE" ]] ||
    fail arguments 'legacy CONNECT closure confirmation is invalid'
  [[ "$5" == --confirm-pgbouncer-replacement && "$6" == "$OLD_POOL_CONTAINER" ]] ||
    fail arguments 'PgBouncer replacement confirmation is invalid'
}

container_exists() {
  /usr/bin/podman container exists "$1" >/dev/null 2>&1
}

container_running() {
  [[ "$(/usr/bin/podman inspect --format '{{.State.Running}}' "$1" 2>/dev/null || true)" == true ]]
}

container_restart_policy() {
  /usr/bin/podman inspect --format '{{.HostConfig.RestartPolicy.Name}}' "$1" 2>/dev/null
}

set_container_restart_policy() {
  local container="$1" policy="$2"
  [[ "$policy" == no || "$policy" == always ]] || return 1
  /usr/bin/podman update --restart="$policy" "$container" >/dev/null || return 1
  [[ "$(container_restart_policy "$container")" == "$policy" ]]
}

stop_container_if_running() {
  local container="$1"
  container_exists "$container" || return 0
  if container_running "$container"; then
    /usr/bin/podman stop --time 10 "$container" >/dev/null || return 1
  fi
  ! container_running "$container"
}

provision_unit_runtime_matches() {
  local pid cgroup property actual index
  local -a argv=() expected=(/usr/bin/bash -p "$SELF")
  expected+=("$@")
  pid="$(/usr/bin/systemctl show -P MainPID "$PROVISION_UNIT" 2>/dev/null || true)"
  [[ "$pid" == "$BASHPID" ]] || return 1
  cgroup="$(<"/proc/$BASHPID/cgroup")"
  [[ "$cgroup" == "0::/system.slice/$PROVISION_UNIT" ]] || return 1
  for property in \
    'LoadState|loaded' \
    'Transient|yes' \
    "FragmentPath|/run/systemd/transient/$PROVISION_UNIT" \
    'NeedDaemonReload|no' \
    'Type|exec' \
    'KillMode|control-group' \
    'SendSIGKILL|yes' \
    'TimeoutStopUSec|30s' \
    'RuntimeMaxUSec|30min' \
    'OOMPolicy|stop' \
    'MemoryMax|2147483648' \
    'MemorySwapMax|0' \
    'TasksMax|512' \
    'UMask|0077'; do
    actual="$(/usr/bin/systemctl show -P "${property%%|*}" "$PROVISION_UNIT" 2>/dev/null || true)"
    [[ "$actual" == "${property#*|}" ]] || return 1
  done
  [[ "$(/usr/bin/readlink -e -- "/proc/$BASHPID/cwd" 2>/dev/null || true)" == / ]] || return 1
  mapfile -d '' -t argv <"/proc/$BASHPID/cmdline" || return 1
  [[ "${#argv[@]}" == "${#expected[@]}" ]] || return 1
  for index in "${!expected[@]}"; do
    [[ "${argv[$index]}" == "${expected[$index]}" ]] || return 1
  done
}

enter_provision_unit_boundary() {
  local status
  provision_unit_runtime_matches "$@" && return 0
  if /usr/bin/systemd-run \
      --unit="$PROVISION_UNIT" \
      --wait \
      --collect \
      --verbose \
      --property=Type=exec \
      --property=KillMode=control-group \
      --property=SendSIGKILL=yes \
      --property=TimeoutStopSec=30s \
      --property=RuntimeMaxSec=30min \
      --property=OOMPolicy=stop \
      --property=MemoryMax=2G \
      --property=MemorySwapMax=0 \
      --property=TasksMax=512 \
      --property=UMask=0077 \
      --working-directory=/ \
      /usr/bin/env -i \
        PATH=/usr/bin:/bin \
        LC_ALL=C \
        ASCENDANY_PROVISION_CLEAN_ENV=1 \
        /usr/bin/bash -p "$SELF" "$@"; then
    exit 0
  else
    status=$?
  fi
  exit "$status"
}

wait_for_legacy_pool_conmon_scope_inactive() {
  local attempt state
  for attempt in {1..100}; do
    state="$(/usr/bin/systemctl is-active "$LEGACY_POOL_CONMON_SCOPE" 2>/dev/null || true)"
    case "$state" in
      active|activating|deactivating) /usr/bin/sleep 0.05 ;;
      *) return 0 ;;
    esac
  done
  return 1
}

start_legacy_pool_container() {
  local container="$1" conmon_pid conmon_cgroup
  container_exists "$container" || return 1
  ! container_running "$container" || return 1
  wait_for_legacy_pool_conmon_scope_inactive || return 1
  /usr/bin/systemd-run --scope --quiet --unit="$LEGACY_POOL_CONMON_SCOPE" \
    /usr/bin/podman start "$container" >/dev/null || return 1
  container_running "$container" || return 1
  conmon_pid="$(/usr/bin/podman inspect --format '{{.State.ConmonPid}}' "$container" 2>/dev/null || true)"
  [[ "$conmon_pid" =~ ^[1-9][0-9]*$ ]] || return 1
  conmon_cgroup="$(<"/proc/$conmon_pid/cgroup")"
  [[ "$conmon_cgroup" == "0::/system.slice/$LEGACY_POOL_CONMON_SCOPE" &&
     "$(/usr/bin/systemctl is-active "$LEGACY_POOL_CONMON_SCOPE" 2>/dev/null || true)" == active &&
     "$(/usr/bin/systemctl show -P ControlGroup "$LEGACY_POOL_CONMON_SCOPE" 2>/dev/null || true)" == "/system.slice/$LEGACY_POOL_CONMON_SCOPE" ]]
}

capture_legacy_pool_mount_label() {
  local container='' old_exists=0 rollback_exists=0 mount_label process_label mount_level process_level mount
  container_exists "$OLD_POOL_CONTAINER" && old_exists=1
  container_exists "$old_pool_rollback" && rollback_exists=1
  ((old_exists + rollback_exists == 1)) || return 1
  if ((old_exists == 1)); then
    container="$OLD_POOL_CONTAINER"
  else
    container="$old_pool_rollback"
  fi
  [[ "$(/usr/bin/podman inspect --format '{{.Image}}' "$container" 2>/dev/null || true)" == "$OLD_POOL_IMAGE_ID" &&
     "$(/usr/bin/podman inspect --format '{{.HostConfig.NetworkMode}}' "$container" 2>/dev/null || true)" == host &&
     "$(/usr/bin/podman inspect --format '{{.Config.User}}' "$container" 2>/dev/null || true)" == postgres ]] ||
    return 1
  mount="$(/usr/bin/podman inspect --format '{{range .Mounts}}{{printf "%s|%s|%t\n" .Source .Destination .RW}}{{end}}' "$container" 2>/dev/null || true)"
  [[ "$mount" == "$POOL_CONFIG_ROOT|/etc/pgbouncer|true" ]] || return 1
  mount_label="$(/usr/bin/podman inspect --format '{{.MountLabel}}' "$container" 2>/dev/null || true)"
  process_label="$(/usr/bin/podman inspect --format '{{.ProcessLabel}}' "$container" 2>/dev/null || true)"
  [[ "$mount_label" == *:object_r:container_file_t:* &&
     "$process_label" == *:system_r:container_t:* ]] || return 1
  mount_level="${mount_label#*:}"; mount_level="${mount_level#*:}"; mount_level="${mount_level#*:}"
  process_level="${process_label#*:}"; process_level="${process_level#*:}"; process_level="${process_level#*:}"
  [[ -n "$mount_level" && "$mount_level" == "$process_level" ]] || return 1
  legacy_pool_mount_label="$mount_label"
}

postgres_psql_as() {
  local role="$1"
  shift
  /usr/bin/podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    /usr/bin/env -i \
      HOME=/var/lib/postgresql \
      PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
      LC_ALL=C \
      /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
        --username="$role" "$@"
}

postgres_psql() {
  postgres_psql_as "$maintenance_role" "$@"
}

derive_run_paths() {
  [[ "$run_id" =~ ^[0-9a-f]{32}$ ]] || fail journal 'journal run id is invalid'
  marker_role="ascendany_v2_marker_${run_id}"
  splitter_role="ascendany_split_${run_id}"
  old_pool_rollback="ascendany-pgbouncer-rollback-${run_id:0:12}"
  config_stage="$POOL_PARENT/.pgbouncer.stage.$run_id"
  config_rollback="$POOL_PARENT/.pgbouncer.rollback.$run_id"
  credential_stage="$STATE_ROOT/credentials-$run_id"
  credential_manifest="$STATE_ROOT/credentials-$run_id.manifest"
  legacy_pool_manifest="$STATE_ROOT/legacy-pgbouncer-$run_id.manifest"
  legacy_pool_ini_backup="$STATE_ROOT/legacy-pgbouncer-$run_id.pgbouncer.ini"
  legacy_pool_userlist_backup="$STATE_ROOT/legacy-pgbouncer-$run_id.userlist.txt"
  hba_backup="$STATE_ROOT/postgresql-hba-$run_id.original"
  hba_backup_sha="$STATE_ROOT/postgresql-hba-$run_id.original.sha256"
  ident_backup="$STATE_ROOT/postgresql-ident-$run_id.original"
  ident_backup_sha="$STATE_ROOT/postgresql-ident-$run_id.original.sha256"
}

write_journal() {
  local next_phase="$1"
  local temporary="$STATE_ROOT/.journal.$$"
  case "$next_phase" in
    prepared|bootstrap_access|legacy_quiesced|legacy_split|v2_database|credentials_published|old_pool_stopped|config_published|native_active|committed)
      ;;
    *) fail journal "unknown journal phase: $next_phase" ;;
  esac
  printf '%s\n' \
    'schema=ascendany.postgres-pgbouncer.provision.v1' \
    "run_id=$run_id" \
    "phase=$next_phase" \
    "marker_role=$marker_role" >"$temporary"
  /usr/bin/chown 0:0 -- "$temporary"
  /usr/bin/chmod 0600 -- "$temporary"
  /usr/bin/sync -f "$temporary"
  /usr/bin/mv -f -- "$temporary" "$JOURNAL_PATH"
  /usr/bin/sync -f "$STATE_ROOT"
  phase="$next_phase"
}

load_journal() {
  local -a lines=()
  [[ -f "$JOURNAL_PATH" && ! -L "$JOURNAL_PATH" &&
     "$(stat -Lc '%u:%g:%a:%h' -- "$JOURNAL_PATH")" == 0:0:600:1 ]] ||
    fail journal 'durable journal identity or mode is invalid'
  mapfile -t lines <"$JOURNAL_PATH"
  [[ "${#lines[@]}" == 4 &&
     "${lines[0]}" == 'schema=ascendany.postgres-pgbouncer.provision.v1' &&
     "${lines[1]}" == run_id=* && "${lines[2]}" == phase=* &&
     "${lines[3]}" == marker_role=* ]] || fail journal 'durable journal shape is invalid'
  run_id="${lines[1]#run_id=}"
  phase="${lines[2]#phase=}"
  derive_run_paths
  [[ "${lines[3]}" == "marker_role=$marker_role" ]] || fail journal 'journal marker role does not match its run id'
  case "$phase" in
    prepared|bootstrap_access|legacy_quiesced|legacy_split|v2_database|credentials_published|old_pool_stopped|config_published|native_active|committed)
      ;;
    *) fail journal 'journal phase is unknown' ;;
  esac
}

initialize_state_root() {
  local initializing_state="${STATE_ROOT}.initializing-${run_id}" state_parent
  state_parent="$(/usr/bin/dirname "$STATE_ROOT")"
  [[ ! -e "$STATE_ROOT" && ! -L "$STATE_ROOT" &&
     ! -e "$initializing_state" && ! -L "$initializing_state" ]] ||
    fail journal 'initial provisioning state path already exists'
  /usr/bin/install -d -o root -g root -m 0700 "$initializing_state"
  /usr/bin/printf '%s\n' \
    'schema=ascendany.postgres-pgbouncer.provision.v1' \
    "run_id=$run_id" \
    'phase=prepared' \
    "marker_role=$marker_role" >"$initializing_state/journal"
  /usr/bin/chown 0:0 "$initializing_state/journal"
  /usr/bin/chmod 0600 "$initializing_state/journal"
  /usr/bin/sync -d "$initializing_state/journal"
  /usr/bin/sync -f "$initializing_state"
  /usr/bin/mv "$initializing_state" "$STATE_ROOT"
  /usr/bin/sync -f "$state_parent"
  phase=prepared
}

consume_initializing_state_roots() {
  local state_parent state_name path entries metadata owner_gid
  state_parent="$(/usr/bin/dirname "$STATE_ROOT")"
  state_name="$(/usr/bin/basename "$STATE_ROOT")"
  owner_gid="$(/usr/bin/id -g)"
  while IFS= read -r path; do
    [[ "$path" =~ ^${STATE_ROOT}[.]initializing-[0-9a-f]{32}$ &&
       -d "$path" && ! -L "$path" &&
       "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null)" &&
       "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == "$EUID:$owner_gid:700" ]] ||
      fail journal 'initial provisioning tombstone identity is invalid'
    entries="$(/usr/bin/find "$path" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C /usr/bin/sort)"
    [[ -z "$entries" || "$entries" == 'journal|f' ]] ||
      fail journal 'initial provisioning tombstone contains an unexpected entry'
    if [[ -e "$path/journal" || -L "$path/journal" ]]; then
      metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path/journal" 2>/dev/null || true)"
      [[ -f "$path/journal" && ! -L "$path/journal" && "$metadata" == "$EUID:$owner_gid:600:1" ]] ||
        fail journal 'partial initial journal metadata is invalid'
      /usr/bin/rm -f "$path/journal"
      /usr/bin/sync -f "$path"
    fi
    /usr/bin/rmdir "$path"
    /usr/bin/sync -f "$state_parent"
  done < <(/usr/bin/find "$state_parent" -mindepth 1 -maxdepth 1 \
    -name "${state_name}.initializing-*" -print | LC_ALL=C /usr/bin/sort)
}

require_release_file() {
  local path="$1"
  [[ -f "$path" && ! -L "$path" ]] || fail release "required release file is invalid: $path"
}

collect_service_directives() {
  local rendered="$1" directive="$2" output_name="$3" line value section=''
  local -n output="$output_name"
  output=()
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    if [[ "$line" =~ ^\[([A-Za-z]+)\]$ ]]; then
      section="${BASH_REMATCH[1]}"
      continue
    fi
    [[ "$section" == Service ]] || continue
    [[ "$line" =~ ^${directive}[[:space:]]*=(.*)$ ]] || continue
    value="${BASH_REMATCH[1]}"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    if [[ -z "$value" ]]; then
      output=()
    else
      output+=("$value")
    fi
  done <<<"$rendered"
}

require_service_directive_sequence() {
  local rendered="$1" directive="$2" expected_text="$3" index
  local -a actual=() expected=()
  collect_service_directives "$rendered" "$directive" actual
  [[ -z "$expected_text" ]] || mapfile -t expected <<<"$expected_text"
  [[ "${#actual[@]}" == "${#expected[@]}" ]] ||
    fail systemd "effective $directive count differs from the reviewed PgBouncer unit"
  for index in "${!expected[@]}"; do
    [[ "${actual[$index]}" == "${expected[$index]}" ]] ||
      fail systemd "effective $directive sequence differs from the reviewed PgBouncer unit"
  done
}

require_pool_unit_property() {
  local property="$1" expected="$2" actual
  actual="$(/usr/bin/systemctl show -P "$property" "$POOL_UNIT" 2>/dev/null)" ||
    fail systemd "cannot read effective $property for the PgBouncer unit"
  [[ "$actual" == "$expected" ]] ||
    fail systemd "effective $property differs from the reviewed PgBouncer unit"
}

require_pool_unit_files() {
  local installed_metadata dropin_metadata
  installed_metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$POOL_UNIT_INSTALLED" 2>/dev/null || true)"
  [[ -f "$POOL_UNIT_INSTALLED" && ! -L "$POOL_UNIT_INSTALLED" &&
     "$POOL_UNIT_INSTALLED" == "$(/usr/bin/realpath -e -- "$POOL_UNIT_INSTALLED" 2>/dev/null || true)" &&
     "$installed_metadata" == 0:0:644:1 ]] ||
    fail systemd 'installed PgBouncer unit metadata is invalid'
  require_protected_ancestry "$POOL_UNIT_INSTALLED"
  /usr/bin/cmp -s -- "$POOL_UNIT_INSTALLED" "$POOL_UNIT_SOURCE" ||
    fail systemd 'installed PgBouncer unit bytes differ from the release'

  dropin_metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$POOL_GLOBAL_DROPIN" 2>/dev/null || true)"
  [[ -f "$POOL_GLOBAL_DROPIN" && ! -L "$POOL_GLOBAL_DROPIN" &&
     "$POOL_GLOBAL_DROPIN" == "$(/usr/bin/realpath -e -- "$POOL_GLOBAL_DROPIN" 2>/dev/null || true)" &&
     "$dropin_metadata" == 0:0:644:596:1 &&
     "$(/usr/bin/sha256sum -- "$POOL_GLOBAL_DROPIN" | /usr/bin/awk '{print $1}')" == \
       ae6b234f92bc22f1201a7572b59b454c9809f33c80d13f361b9674e1801acc37 ]] ||
    fail systemd 'Fedora global service drop-in differs from the reviewed contract'
  require_protected_ancestry "$POOL_GLOBAL_DROPIN"
}

require_pool_unit_contract() {
  local rendered actual
  require_pool_unit_files
  require_pool_unit_property LoadState loaded
  require_pool_unit_property FragmentPath "$POOL_UNIT_INSTALLED"
  require_pool_unit_property DropInPaths "$POOL_GLOBAL_DROPIN"
  require_pool_unit_property NeedDaemonReload no
  require_pool_unit_property Type notify-reload
  require_pool_unit_property DynamicUser yes
  require_pool_unit_property User ascendany-pgbouncer
  require_pool_unit_property Group ascendany-pgbouncer
  require_pool_unit_property KillSignal 2
  require_pool_unit_property NoNewPrivileges yes
  require_pool_unit_property ProtectSystem strict
  require_pool_unit_property ProtectHome yes
  require_pool_unit_property PrivateTmp yes
  require_pool_unit_property PrivateDevices yes
  require_pool_unit_property RestrictNamespaces yes
  require_pool_unit_property MemoryDenyWriteExecute yes
  require_pool_unit_property CapabilityBoundingSet ''
  require_pool_unit_property AmbientCapabilities ''
  actual="$(/usr/bin/systemctl show -P RestrictAddressFamilies "$POOL_UNIT" 2>/dev/null)" ||
    fail systemd 'cannot read effective RestrictAddressFamilies for the PgBouncer unit'
  [[ "$(/usr/bin/tr ' ' '\n' <<<"$actual" | /usr/bin/sed '/^$/d' | LC_ALL=C /usr/bin/sort)" == $'AF_INET\nAF_UNIX' ]] ||
    fail systemd 'effective RestrictAddressFamilies differs from the reviewed PgBouncer unit'

  rendered="$(/usr/bin/systemctl cat "$POOL_UNIT" 2>/dev/null)" ||
    fail systemd 'cannot render the effective PgBouncer unit'
  [[ -n "$rendered" ]] || fail systemd 'effective PgBouncer unit is empty'
  require_service_directive_sequence "$rendered" ExecStart \
    '/usr/bin/pgbouncer -q /opt/ascendany/infra/pgbouncer/pgbouncer.ini'
  require_service_directive_sequence "$rendered" ExecStartPre \
    $'/usr/bin/test -x /usr/bin/pgbouncer\n/usr/bin/test -r /opt/ascendany/infra/pgbouncer/pgbouncer.ini\n/usr/bin/test -r /opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf\n/usr/bin/test -s %d/pgbouncer_userlist'
  require_service_directive_sequence "$rendered" LoadCredential ''
  require_service_directive_sequence "$rendered" LoadCredentialEncrypted \
    'pgbouncer_userlist:/etc/ascendany/credentials/pgbouncer_userlist.cred'
}

require_protected_ancestry() {
  local path="$1"
  local current mode uid
  current="$(dirname -- "$path")"
  while :; do
    [[ ! -L "$current" && -d "$current" ]] || fail filesystem 'protected ancestry contains a symbolic link'
    uid="$(stat -Lc '%u' -- "$current")"
    mode="$(stat -Lc '%a' -- "$current")"
    [[ "$uid" == 0 ]] || fail filesystem 'protected ancestry is not root-owned'
    (( (8#$mode & 0022) == 0 )) || fail filesystem 'protected ancestry is group/other writable'
    [[ "$current" != / ]] || break
    current="$(dirname -- "$current")"
  done
}

require_password_file() {
  local path="$1"
  local canonical metadata size newline_count
  canonical="$(readlink -e -- "$path" 2>/dev/null || true)"
  [[ "$canonical" == "$path" && -f "$path" && ! -L "$path" ]] ||
    fail credential 'password input is not one canonical regular file'
  metadata="$(stat -Lc '%u:%g:%a:%h' -- "$path")"
  [[ "$metadata" == 0:0:600:1 ]] ||
    fail credential 'password input must be root:root mode 0600 with one link'
  require_protected_ancestry "$path"
  size="$(stat -Lc '%s' -- "$path")"
  ((size >= 16 && size <= 128)) || fail credential 'password input length is outside 16..128 bytes'
  newline_count="$(/usr/bin/tr -cd '\n' <"$path" | /usr/bin/wc -c)"
  [[ "$newline_count" == 0 ]] || fail credential 'password input must not contain a newline'
  LC_ALL=C /usr/bin/grep -aEq '^[A-Za-z0-9._~+/-]+={0,2}$' "$path" ||
    fail credential 'password input contains a noncanonical character'
}

require_distinct_passwords() {
  local path digest
  local -A seen=()
  for path in "$@"; do
    digest="$(/usr/bin/sha256sum -- "$path")"
    digest="${digest%% *}"
    [[ -z "${seen[$digest]+present}" ]] || fail credential 'database passwords must be pairwise distinct'
    seen[$digest]=1
  done
}

legacy_pool_semantics_match() {
  local path="${1:-$POOL_CONFIG_ROOT}" password verifier_hash expected_userlist
  local -a config_lines=() userlist_lines=()
  mapfile -t config_lines < <(/usr/bin/sed -E '/^[[:space:]]*(#|$)/d' "$path/pgbouncer.ini")
  [[ "${#config_lines[@]}" == 12 &&
     "${config_lines[0]}" == '[databases]' &&
     "${config_lines[1]}" =~ ^AscendAny[[:space:]]=[[:space:]]host[[:space:]]=[[:space:]]127[.]0[.]0[.]1[[:space:]]port=5432[[:space:]]user=AscendAny[[:space:]]password=([A-Za-z0-9._~+/-]{40})[[:space:]]dbname=AscendAny$ &&
     "${config_lines[2]}" == '[pgbouncer]' &&
     "${config_lines[3]}" == 'listen_addr = 127.0.0.1' &&
     "${config_lines[4]}" == 'listen_port = 6432' &&
     "${config_lines[5]}" == 'auth_file = /etc/pgbouncer/userlist.txt' &&
     "${config_lines[6]}" == 'auth_type = md5' &&
     "${config_lines[7]}" == 'pool_mode = transaction' &&
     "${config_lines[8]}" == 'max_client_conn = 100' &&
     "${config_lines[9]}" == 'default_pool_size = 20' &&
     "${config_lines[10]}" == 'ignore_startup_parameters = extra_float_digits' &&
     "${config_lines[11]}" == 'admin_users = AscendAny' ]] ||
    return 1
  password="${BASH_REMATCH[1]}"
  mapfile -t userlist_lines <"$path/userlist.txt"
  [[ "${#userlist_lines[@]}" == 1 &&
     "${userlist_lines[0]}" =~ ^\"AscendAny\"[[:space:]]\"md5[0-9a-f]{32}\"$ ]] ||
    return 1
  verifier_hash="$(/usr/bin/printf '%s' "${password}${LEGACY_ROLE}" | /usr/bin/md5sum)"
  verifier_hash="${verifier_hash%% *}"
  expected_userlist="\"AscendAny\" \"md5${verifier_hash}\""
  [[ "${userlist_lines[0]}" == "$expected_userlist" ]] ||
    return 1
  legacy_pool_password="$password"
  unset password verifier_hash expected_userlist
}

require_legacy_pool_semantics() {
  legacy_pool_semantics_match "${1:-$POOL_CONFIG_ROOT}" ||
    fail old_pool 'legacy PgBouncer configuration or verifier semantics differ from the reviewed one-route contract'
}

require_legacy_api_unit_property() {
  local property="$1" expected="$2" actual
  actual="$(/usr/bin/systemctl show -P "$property" "$LEGACY_API_UNIT" 2>/dev/null)" ||
    fail legacy_api "cannot read effective $property for the old API unit"
  [[ "$actual" == "$expected" ]] ||
    fail legacy_api "effective $property differs from the reviewed old API unit"
}

require_legacy_api_environment() {
  local metadata key value
  local -a lines=() keys=()
  local -A values=()
  metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$LEGACY_API_ENV_PATH" 2>/dev/null || true)"
  [[ -f "$LEGACY_API_ENV_PATH" && ! -L "$LEGACY_API_ENV_PATH" &&
     "$LEGACY_API_ENV_PATH" == "$(/usr/bin/realpath -e -- "$LEGACY_API_ENV_PATH" 2>/dev/null || true)" &&
     "$metadata" == 0:0:600:1 ]] ||
    fail legacy_api 'old API environment file metadata is invalid'
  require_protected_ancestry "$LEGACY_API_ENV_PATH"
  mapfile -t lines <"$LEGACY_API_ENV_PATH"
  ((${#lines[@]} == 8)) || fail legacy_api 'old API environment shape differs from the reviewed contract'
  for line in "${lines[@]}"; do
    [[ "$line" == *=* ]] || fail legacy_api 'old API environment contains a malformed entry'
    key="${line%%=*}"
    value="${line#*=}"
    [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || fail legacy_api 'old API environment contains an invalid key'
    [[ -z "${values[$key]+present}" ]] || fail legacy_api 'old API environment contains a duplicate key'
    keys+=("$key")
    values["$key"]="$value"
  done
  [[ "$(/usr/bin/printf '%s\n' "${keys[@]}" | LC_ALL=C /usr/bin/sort)" == \
    $'ASCENDANY_API_CONFIG\nASCENDANY_DB_HOST\nASCENDANY_DB_NAME\nASCENDANY_DB_PASSWORD\nASCENDANY_DB_PORT\nASCENDANY_DB_USER\nASCENDANY_PREPROCESS_CONFIG\nPRACTICE_DATA_ROOT' ]] ||
    fail legacy_api 'old API environment key set differs from the reviewed contract'
  [[ "${values[ASCENDANY_API_CONFIG]}" == /opt/ascendany/Release/apps/api/config/default.yaml &&
     "${values[ASCENDANY_PREPROCESS_CONFIG]}" == /opt/ascendany/Release/preprocess/config/default.yaml &&
     "${values[PRACTICE_DATA_ROOT]}" == /opt/ascendany/data/practice &&
     "${values[ASCENDANY_DB_HOST]}" == 127.0.0.1 &&
     "${values[ASCENDANY_DB_PORT]}" == 6432 &&
     "${values[ASCENDANY_DB_NAME]}" == "$LEGACY_DATABASE" &&
     "${values[ASCENDANY_DB_USER]}" == "$LEGACY_ROLE" &&
     -n "$legacy_pool_password" &&
     "${values[ASCENDANY_DB_PASSWORD]}" == "$legacy_pool_password" ]] ||
    fail legacy_api 'old API environment route differs from the protected legacy pool route'
}

require_legacy_api_unit_contract() {
  local unit_metadata dropin_metadata
  unit_metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$LEGACY_API_UNIT_PATH" 2>/dev/null || true)"
  [[ -f "$LEGACY_API_UNIT_PATH" && ! -L "$LEGACY_API_UNIT_PATH" &&
     "$LEGACY_API_UNIT_PATH" == "$(/usr/bin/realpath -e -- "$LEGACY_API_UNIT_PATH" 2>/dev/null || true)" &&
     "$unit_metadata" == 0:0:600:1 &&
     "$(/usr/bin/sha256sum "$LEGACY_API_UNIT_PATH" | /usr/bin/awk '{print $1}')" == "$LEGACY_API_UNIT_SHA256" ]] ||
    fail legacy_api 'old API unit bytes or metadata differ from the reviewed contract'
  require_protected_ancestry "$LEGACY_API_UNIT_PATH"
  dropin_metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$POOL_GLOBAL_DROPIN" 2>/dev/null || true)"
  [[ -f "$POOL_GLOBAL_DROPIN" && ! -L "$POOL_GLOBAL_DROPIN" &&
     "$dropin_metadata" == 0:0:644:596:1 &&
     "$(/usr/bin/sha256sum "$POOL_GLOBAL_DROPIN" | /usr/bin/awk '{print $1}')" == "$LEGACY_API_GLOBAL_DROPIN_SHA256" ]] ||
    fail legacy_api 'old API global systemd drop-in differs from the reviewed contract'
  require_protected_ancestry "$POOL_GLOBAL_DROPIN"
  require_legacy_api_unit_property LoadState loaded
  require_legacy_api_unit_property FragmentPath "$LEGACY_API_UNIT_PATH"
  require_legacy_api_unit_property DropInPaths "$POOL_GLOBAL_DROPIN"
  require_legacy_api_unit_property NeedDaemonReload no
  require_legacy_api_unit_property Type simple
  require_legacy_api_unit_property User root
  require_legacy_api_unit_property Group ''
  require_legacy_api_unit_property WorkingDirectory /opt/ascendany/Release
  require_legacy_api_unit_property EnvironmentFiles "$LEGACY_API_ENV_PATH (ignore_errors=no)"
  require_legacy_api_unit_property Restart on-failure
  require_legacy_api_unit_property RestartUSec 3s
  require_legacy_api_unit_property UnitFileState enabled
  require_legacy_api_environment
}

legacy_api_runtime_matches() {
  local previous_pid="$1" pid executable working_directory listeners
  local -a argv=()
  [[ "$(/usr/bin/systemctl is-active "$LEGACY_API_UNIT" 2>/dev/null || true)" == active &&
     "$(/usr/bin/systemctl show -P SubState "$LEGACY_API_UNIT" 2>/dev/null || true)" == running ]] ||
    return 1
  pid="$(/usr/bin/systemctl show -P MainPID "$LEGACY_API_UNIT" 2>/dev/null || true)"
  [[ "$pid" =~ ^[1-9][0-9]*$ && ( "$previous_pid" == 0 || "$pid" != "$previous_pid" ) ]] || return 1
  executable="$(/usr/bin/readlink -e -- "/proc/$pid/exe" 2>/dev/null || true)"
  working_directory="$(/usr/bin/readlink -e -- "/proc/$pid/cwd" 2>/dev/null || true)"
  [[ "$executable" == /usr/bin/python3.14 && "$working_directory" == /opt/ascendany/Release ]] || return 1
  mapfile -d '' -t argv <"/proc/$pid/cmdline" || return 1
  [[ "${#argv[@]}" == 8 &&
     "${argv[0]}" == /opt/ascendany/.venv/bin/python &&
     "${argv[1]}" == -m && "${argv[2]}" == uvicorn &&
     "${argv[3]}" == apps.api.main:app &&
     "${argv[4]}" == --host && "${argv[5]}" == 127.0.0.1 &&
     "${argv[6]}" == --port && "${argv[7]}" == 8000 ]] || return 1
  listeners="$(/usr/bin/ss -H -ltnp 'sport = :8000' 2>/dev/null || true)"
  [[ -n "$listeners" && "$listeners" != *$'\n'* ]] || return 1
  /usr/bin/awk -v pid="$pid" '
    $1 == "LISTEN" && $4 == "127.0.0.1:8000" && index($0, "pid=" pid ",") { found = 1 }
    END { exit(found ? 0 : 1) }
  ' <<<"$listeners"
}

stop_legacy_api_for_pool_switch() {
  local pid
  pid="$(/usr/bin/systemctl show -P MainPID "$LEGACY_API_UNIT" 2>/dev/null || true)"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  legacy_api_previous_pid="$pid"
  /usr/bin/systemctl stop "$LEGACY_API_UNIT" || return 1
  [[ "$(/usr/bin/systemctl is-active "$LEGACY_API_UNIT" 2>/dev/null || true)" == inactive &&
     "$(/usr/bin/systemctl show -P MainPID "$LEGACY_API_UNIT" 2>/dev/null || true)" == 0 &&
     -z "$(/usr/bin/ss -H -ltn 'sport = :8000' 2>/dev/null || true)" ]]
}

start_legacy_api_and_probe() {
  local attempt
  /usr/bin/systemctl start "$LEGACY_API_UNIT" || return 1
  for attempt in {1..100}; do
    if legacy_api_runtime_matches "$legacy_api_previous_pid"; then
      probe_legacy_database
      return
    fi
    /usr/bin/sleep 0.1
  done
  return 1
}

encrypt_credential() {
  local credential_name="$1"
  local input="$2"
  local output_name="$3"
  local output="$credential_stage/$output_name"
  /usr/bin/systemd-creds encrypt --name="$credential_name" "$input" "$output" >/dev/null 2>&1 ||
    fail credential_encryption "failed to encrypt $credential_name"
  /usr/bin/chown 0:0 -- "$output"
  /usr/bin/chmod 0400 -- "$output"
  /usr/bin/systemd-creds decrypt --name="$credential_name" "$output" - 2>/dev/null |
    /usr/bin/cmp -s - "$input" || fail credential_encryption "encrypted credential verification failed for $credential_name"
}

build_credential_manifest() {
  local output name digest temporary="$credential_manifest.tmp"
  : >"$temporary"
  for name in \
    runtime_db_password.cred migrator_db_password.cred backup_db_password.cred \
    restore_db_password.cred pgbouncer_userlist.cred; do
    output="$credential_stage/$name"
    [[ -f "$output" && ! -L "$output" && "$(stat -Lc '%u:%g:%a:%h' "$output")" == 0:0:400:1 ]] ||
      fail credential_encryption "staged credential is invalid: $name"
    digest="$(/usr/bin/sha256sum "$output")"
    printf '%s|%s\n' "$name" "${digest%% *}" >>"$temporary"
  done
  /usr/bin/chown 0:0 "$temporary"
  /usr/bin/chmod 0600 "$temporary"
  /usr/bin/sync -f "$temporary"
  /usr/bin/mv "$temporary" "$credential_manifest"
  /usr/bin/sync -f "$STATE_ROOT"
}

set_role_password() {
  local role="$1"
  local password_file="$2"
  if ! {
    /usr/bin/cat -- "$password_file"
    printf '\n'
    /usr/bin/cat -- "$password_file"
    printf '\n'
  } | postgres_psql --dbname=postgres --command="\\password $role" >/dev/null 2>&1; then
    fail database_password "failed to set the password for $role"
  fi
}

postgres_file_hash() {
  local path="$1"
  /usr/bin/podman exec -i --user postgres "$POSTGRES_CONTAINER" \
    /usr/bin/sha256sum "$path" | /usr/bin/awk '{print $1}'
}

stage_postgres_access_file() {
  local source="$1" temporary="$2"
  /usr/bin/podman exec -i --user root "$POSTGRES_CONTAINER" \
    /bin/sh -ceu '
      temporary="$1"
      umask 077
      cat >"$temporary"
      chown postgres:postgres "$temporary"
      chmod 0600 "$temporary"
      sync -f "$temporary"
    ' sh "$temporary" <"$source"
}

install_postgres_access() {
  local target_contract="$1" hba_source="$2" ident_source="$3"
  local hba_temporary="${POSTGRES_HBA_PATH}.ascendany-${run_id}"
  local ident_temporary="${POSTGRES_IDENT_PATH}.ascendany-${run_id}"
  local first_target first_temporary second_target second_temporary
  local previous_load_time attempt
  case "$target_contract" in
    bootstrap)
      first_target="$POSTGRES_IDENT_PATH"
      first_temporary="$ident_temporary"
      second_target="$POSTGRES_HBA_PATH"
      second_temporary="$hba_temporary"
      ;;
    final|original)
      first_target="$POSTGRES_HBA_PATH"
      first_temporary="$hba_temporary"
      second_target="$POSTGRES_IDENT_PATH"
      second_temporary="$ident_temporary"
      ;;
    *)
      return 1
      ;;
  esac
  previous_load_time="$(postgres_psql --dbname=postgres --tuples-only --no-align \
    --command='SELECT pg_conf_load_time()')" || return 1
  [[ -n "$previous_load_time" ]] || return 1
  stage_postgres_access_file "$hba_source" "$hba_temporary" || return 1
  stage_postgres_access_file "$ident_source" "$ident_temporary" || return 1
  /usr/bin/podman exec -i --user root "$POSTGRES_CONTAINER" \
    /bin/sh -ceu '
      hba_target="$1"
      ident_target="$2"
      first_target="$3"
      first_temporary="$4"
      second_target="$5"
      second_temporary="$6"
      test -f "$hba_target" && test ! -L "$hba_target"
      test -f "$ident_target" && test ! -L "$ident_target"
      /usr/bin/mv -f "$first_temporary" "$first_target"
      /usr/bin/sync -f "$(/usr/bin/dirname "$first_target")"
      /usr/bin/mv -f "$second_temporary" "$second_target"
      /usr/bin/sync -f "$(/usr/bin/dirname "$second_target")"
    ' sh "$POSTGRES_HBA_PATH" "$POSTGRES_IDENT_PATH" \
      "$first_target" "$first_temporary" "$second_target" "$second_temporary" || return 1
  [[ "$(postgres_psql --dbname=postgres --tuples-only --no-align --command='SELECT pg_reload_conf()')" == t ]] || return 1
  for attempt in {1..100}; do
    if postgres_access_load_receipt "$previous_load_time"; then
      return
    fi
    /usr/bin/sleep 0.1
  done
  return 1
}

postgres_access_load_receipt() {
  local previous_load_time="$1" result
  local -a access_mtimes=()
  mapfile -t access_mtimes < <(
    /usr/bin/podman exec -i --user postgres "$POSTGRES_CONTAINER" \
      /usr/bin/stat -Lc '%y' -- "$POSTGRES_HBA_PATH" "$POSTGRES_IDENT_PATH"
  )
  [[ "${#access_mtimes[@]}" == 2 ]] || return 1
  result="$(postgres_psql --dbname=postgres --tuples-only --no-align \
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

postgres_durability_settings_are_enabled() {
  [[ "$1" == 'on|on|on' ]]
}

normalized_hba_rules() {
  postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' <<'SQL'
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

normalized_ident_rules() {
  postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT map_name, sys_name, pg_username, coalesce(error, '')
FROM pg_ident_file_mappings
ORDER BY line_number;
SQL
}

require_original_access_rules() {
  local expected_hba expected_ident
  expected_hba=$'local|all|all|||trust||\nhost|all|all|127.0.0.1|255.255.255.255|trust||\nhost|all|all|::1|ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff|trust||\nlocal|replication|all|||trust||\nhost|replication|all|127.0.0.1|255.255.255.255|trust||\nhost|replication|all|::1|ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff|trust||\nhost|all|all|all||scram-sha-256||'
  expected_ident=''
  [[ "$(normalized_hba_rules)" == "$expected_hba" ]] ||
    fail postgres_access 'entry HBA rules differ from the reviewed production state'
  [[ "$(normalized_ident_rules)" == "$expected_ident" ]] ||
    fail postgres_access 'entry ident mappings differ from the reviewed production state'
}

require_bootstrap_hba_rules() {
  local expected
  local expected_ident
  expected=$'local|replication|all|||reject||\nlocal|all|AscendAny|||peer|map=ascendany_role_split|\nlocal|all|ascendany_cluster_admin|||peer|map=ascendany_role_split|\nlocal|all|all|||reject||\nhost|replication|all|0.0.0.0|0.0.0.0|reject||\nhost|replication|all|::|::|reject||\nhost|AscendAny|AscendAny|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|ascendany_v2|ascendanyd_login,ascendany_migrator_login,ascendany_backup_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|postgres,ascendany_v2_restore_verify|ascendany_restore_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|all|all|10.88.0.1|255.255.255.255|reject||\nhost|all|all|0.0.0.0|0.0.0.0|reject||\nhost|all|all|::|::|reject||'
  expected_ident=$'ascendany_role_split|postgres|AscendAny|\nascendany_role_split|postgres|ascendany_cluster_admin|\nascendany_cluster_admin|postgres|ascendany_cluster_admin|'
  [[ "$(normalized_hba_rules)" == "$expected" ]] || fail postgres_access 'bootstrap HBA rules differ from the closed contract'
  [[ "$(normalized_ident_rules)" == "$expected_ident" ]] || fail postgres_access 'bootstrap ident mappings differ from the closed contract'
}

require_final_hba_rules() {
  local expected
  local expected_ident
  expected=$'local|replication|all|||reject||\nlocal|all|ascendany_cluster_admin|||peer|map=ascendany_cluster_admin|\nlocal|all|all|||reject||\nhost|replication|all|0.0.0.0|0.0.0.0|reject||\nhost|replication|all|::|::|reject||\nhost|AscendAny|AscendAny|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|ascendany_v2|ascendanyd_login,ascendany_migrator_login,ascendany_backup_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|postgres,ascendany_v2_restore_verify|ascendany_restore_login|10.88.0.1|255.255.255.255|scram-sha-256||\nhost|all|all|10.88.0.1|255.255.255.255|reject||\nhost|all|all|0.0.0.0|0.0.0.0|reject||\nhost|all|all|::|::|reject||'
  expected_ident='ascendany_cluster_admin|postgres|ascendany_cluster_admin|'
  [[ "$(normalized_hba_rules)" == "$expected" ]] || fail postgres_access 'production HBA rules differ from the closed contract'
  [[ "$(normalized_ident_rules)" == "$expected_ident" ]] || fail postgres_access 'production ident mappings differ from the closed contract'
}

backup_original_access() {
  local source backup digest backup_temporary digest_temporary
  for source in "$POSTGRES_HBA_PATH" "$POSTGRES_IDENT_PATH"; do
    if [[ "$source" == "$POSTGRES_HBA_PATH" ]]; then
      backup="$hba_backup"
      digest="$hba_backup_sha"
    else
      backup="$ident_backup"
      digest="$ident_backup_sha"
    fi
    backup_temporary="$backup.tmp"
    digest_temporary="$digest.tmp"
    [[ ! -e "$backup" && ! -L "$backup" && ! -e "$digest" && ! -L "$digest" &&
       ! -e "$backup_temporary" && ! -L "$backup_temporary" &&
       ! -e "$digest_temporary" && ! -L "$digest_temporary" ]] ||
      fail journal 'PostgreSQL access backup output already exists'
    /usr/bin/podman exec -i --user postgres "$POSTGRES_CONTAINER" /usr/bin/cat "$source" >"$backup_temporary"
    /usr/bin/chown 0:0 "$backup_temporary"
    /usr/bin/chmod 0600 "$backup_temporary"
    /usr/bin/sync -d "$backup_temporary"
    /usr/bin/mv "$backup_temporary" "$backup"
    /usr/bin/sync -f "$STATE_ROOT"
    /usr/bin/sha256sum "$backup" | /usr/bin/awk '{print $1}' >"$digest_temporary"
    /usr/bin/chown 0:0 "$digest_temporary"
    /usr/bin/chmod 0600 "$digest_temporary"
    /usr/bin/sync -d "$digest_temporary"
    /usr/bin/mv "$digest_temporary" "$digest"
    /usr/bin/sync -f "$STATE_ROOT"
  done
  /usr/bin/sync -f "$STATE_ROOT"
}

probe_legacy_database() {
  local output code
  output="$(/usr/bin/mktemp /run/ascendany-legacy-db-probe.XXXXXX)"
  /usr/bin/chmod 0600 "$output"
  code="$(/usr/bin/curl --disable --noproxy '*' --silent --show-error --max-time 15 \
    --output "$output" --write-out '%{http_code}' \
    http://127.0.0.1:8000/api/v1/meta/latest_exam_imported_at 2>/dev/null || true)"
  if [[ "$code" != 200 ]] || ! /usr/bin/jq -e '
      type == "object" and keys == ["latestExamImportedAt"] and
      (.latestExamImportedAt == null or (.latestExamImportedAt | type == "string"))
    ' "$output" >/dev/null 2>&1; then
    /usr/bin/rm -f "$output"
    return 1
  fi
  /usr/bin/rm -f "$output"
}

wait_for_loopback_port() {
  local port="$1" attempt
  for attempt in {1..50}; do
    if /usr/bin/ss -H -ltn | /usr/bin/awk -v expected="127.0.0.1:${port}" \
      '$4 == expected { found=1 } END { exit(found ? 0 : 1) }'; then
      return 0
    fi
    /usr/bin/sleep 0.1
  done
  return 1
}

client_psql() {
  local password_file="$1" port="$2" database="$3" sql="$4"
  {
    /usr/bin/cat "$password_file"
    printf '\n'
  } | /usr/bin/podman run --rm -i \
    --pull=never --network=host --read-only --cap-drop=all \
    --security-opt=no-new-privileges --http-proxy=false \
    --env=PGCONNECT_TIMEOUT=5 \
    --entrypoint /usr/bin/psql "$postgres_image_id" \
    -X --no-psqlrc --password --set=ON_ERROR_STOP=1 \
    --host=127.0.0.1 --port="$port" --username=ascendanyd_login \
    --dbname="$database" --tuples-only --no-align --command="$sql"
}

quiesce_legacy_pool() {
  stop_legacy_api_for_pool_switch ||
    fail legacy_quiesce 'old API could not be quiesced before the role split'
  /usr/bin/podman stop --time 10 "$OLD_POOL_CONTAINER" >/dev/null
  postgres_psql_as "$LEGACY_ROLE" --dbname=postgres >/dev/null <<'SQL'
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE usesysid = 10
  AND backend_type = 'client backend'
  AND pid <> pg_backend_pid();
SQL
  [[ "$(postgres_psql_as "$LEGACY_ROLE" --dbname=postgres --tuples-only --no-align --command="SELECT count(*) FROM pg_stat_activity WHERE usesysid = 10 AND backend_type = 'client backend' AND pid <> pg_backend_pid()")" == 0 ]] ||
    fail legacy_quiesce 'bootstrap OID still owns a concurrent client backend'
  write_journal legacy_quiesced
}

verify_legacy_pool_reconnect_before_split() {
  stop_legacy_api_for_pool_switch ||
    fail legacy_reconnect 'old API could not be quiesced for the fresh authentication probe'
  stop_container_if_running "$OLD_POOL_CONTAINER" ||
    fail legacy_reconnect 'old PgBouncer could not be stopped for the fresh authentication probe'
  postgres_psql_as "$LEGACY_ROLE" --dbname=postgres >/dev/null <<'SQL'
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE usesysid = 10
  AND backend_type = 'client backend'
  AND pid <> pg_backend_pid();
SQL
  start_legacy_pool_container "$OLD_POOL_CONTAINER" ||
    fail legacy_reconnect 'old PgBouncer conmon scope could not be established for the fresh authentication probe'
  wait_for_loopback_port 6432 || fail legacy_reconnect 'old PgBouncer did not rebind for the fresh authentication probe'
  container_running "$OLD_POOL_CONTAINER" || fail legacy_reconnect 'old PgBouncer exited during the fresh authentication probe'
  legacy_pool_tree_matches "$POOL_CONFIG_ROOT" legacy ||
    fail legacy_reconnect 'old PgBouncer rewrote a protected input outside the captured generation'
  start_legacy_api_and_probe ||
    fail legacy_reconnect 'old API failed a fresh backend authentication before the role split'
  [[ "$(postgres_psql_as "$LEGACY_ROLE" --dbname=postgres --tuples-only --no-align --command="SELECT count(*) FROM pg_stat_activity WHERE usesysid = 10 AND datname = 'AscendAny' AND backend_type = 'client backend' AND pid <> pg_backend_pid()")" -ge 1 ]] ||
    fail legacy_reconnect 'old API fresh authentication did not create a legacy database backend'
}

split_legacy_role() {
  postgres_psql_as "$LEGACY_ROLE" --dbname="$LEGACY_DATABASE" \
    --set=splitter_role="$splitter_role" >/dev/null <<'SQL'
BEGIN;
CREATE ROLE :"splitter_role" NOLOGIN SUPERUSER NOINHERIT NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
SET SESSION AUTHORIZATION :"splitter_role";
DROP FUNCTION pgbouncer.user_lookup(text);
DROP SCHEMA pgbouncer;
ALTER ROLE "AscendAny" RENAME TO ascendany_cluster_admin;
ALTER ROLE ascendany_cluster_admin WITH LOGIN SUPERUSER NOINHERIT NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_cluster_admin RESET ALL;
CREATE ROLE "AscendAny" LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
DO $copy_legacy_scram$
DECLARE
  legacy_scram text;
BEGIN
  SELECT rolpassword INTO STRICT legacy_scram
  FROM pg_authid
  WHERE rolname = 'ascendany_cluster_admin'
    AND rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$';
  EXECUTE format('ALTER ROLE "AscendAny" PASSWORD %L', legacy_scram);
END
$copy_legacy_scram$;
ALTER ROLE ascendany_cluster_admin LOGIN PASSWORD NULL;
COMMENT ON ROLE ascendany_cluster_admin IS 'ascendany.cluster.bootstrap.v2';
COMMENT ON ROLE "AscendAny" IS 'ascendany.legacy.runtime.v2';
ALTER ROLE "AscendAny" RESET ALL;
REVOKE ALL PRIVILEGES ON DATABASE "AscendAny" FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE "AscendAny" TO "AscendAny";
GRANT USAGE, CREATE ON SCHEMA ascendany TO "AscendAny";
GRANT SELECT, INSERT, UPDATE, DELETE, REFERENCES, TRIGGER ON ALL TABLES IN SCHEMA ascendany TO "AscendAny";
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA ascendany TO "AscendAny";
SELECT format('GRANT USAGE ON TYPE %I.%I TO "AscendAny"', namespace.nspname, type.typname)
FROM pg_type AS type
JOIN pg_namespace AS namespace ON namespace.oid = type.typnamespace
WHERE namespace.nspname = 'ascendany'
  AND type.typrelid = 0
  AND type.typelem = 0
  AND type.typtype IN ('b', 'c', 'd', 'e', 'r')
ORDER BY namespace.nspname, type.typname
\gexec
ALTER TABLE ascendany.import_tasks OWNER TO "AscendAny";
ALTER TABLE ascendany.import_task_events OWNER TO "AscendAny";
SET SESSION AUTHORIZATION ascendany_cluster_admin;
DROP ROLE :"splitter_role";
COMMIT;
SQL
  maintenance_role="$CLUSTER_ADMIN_ROLE"
  postgres_psql --dbname=postgres >/dev/null <<'SQL'
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE usename = 'ascendany_cluster_admin'
  AND pid <> pg_backend_pid();
SQL
}

verify_legacy_split() {
  local result
  result="$(postgres_psql --dbname="$LEGACY_DATABASE" --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT admin.rolcanlogin,
       admin.rolsuper,
       admin.rolinherit,
       admin.rolcreatedb,
       admin.rolcreaterole,
       admin.rolreplication,
       admin.rolbypassrls,
       admin.rolpassword IS NULL,
       shobj_description(admin.oid, 'pg_authid') = 'ascendany.cluster.bootstrap.v2',
       legacy.rolcanlogin,
       legacy.rolsuper,
       legacy.rolcreatedb,
       legacy.rolcreaterole,
       legacy.rolreplication,
       legacy.rolbypassrls,
       legacy.rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$',
       shobj_description(legacy.oid, 'pg_authid') = 'ascendany.legacy.runtime.v2',
       admin.oid = 10,
       legacy.oid <> 10,
       pg_get_userbyid(database.datdba) = 'ascendany_cluster_admin',
       pg_get_userbyid(task.relowner) = 'AscendAny',
       pg_get_userbyid(events.relowner) = 'AscendAny',
       NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'pgbouncer'),
       NOT EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace = 'ascendany'::regnamespace),
       NOT EXISTS (SELECT 1 FROM pg_db_role_setting)
FROM pg_authid AS admin
CROSS JOIN pg_authid AS legacy
JOIN pg_database AS database ON database.datname = 'AscendAny'
JOIN pg_class AS task ON task.oid = 'ascendany.import_tasks'::regclass
JOIN pg_class AS events ON events.oid = 'ascendany.import_task_events'::regclass
WHERE admin.rolname = 'ascendany_cluster_admin'
  AND legacy.rolname = 'AscendAny';
SQL
)"
  [[ "$result" == 't|t|f|f|f|f|f|t|t|t|f|f|f|f|f|t|t|t|t|t|t|t|t|t|t' ]] ||
    fail legacy_split 'legacy/bootstrap role split does not match the exact capability contract'
}

restart_legacy_pool_after_split() {
  start_legacy_pool_container "$OLD_POOL_CONTAINER" ||
    fail legacy_reconnect 'old PgBouncer conmon scope could not be established after the role split'
  wait_for_loopback_port 6432 || fail legacy_reconnect 'old PgBouncer did not rebind loopback port 6432'
  container_running "$OLD_POOL_CONTAINER" || fail legacy_reconnect 'old PgBouncer exited after the role split'
  legacy_pool_tree_matches "$POOL_CONFIG_ROOT" legacy ||
    fail legacy_reconnect 'old PgBouncer changed its protected input generation after the role split'
  start_legacy_api_and_probe ||
    fail legacy_reconnect 'old API failed its DB-backed reconnect probe after the role split'
}

create_v2_database_and_roles() {
  local provision_comment="ascendany.v2.provision:$run_id"
  postgres_psql --dbname=postgres --set=marker_role="$marker_role" --set=provision_comment="$provision_comment" >/dev/null <<'SQL'
BEGIN;
CREATE ROLE :"marker_role" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
SELECT format('COMMENT ON ROLE %I IS %L', :'marker_role', :'provision_comment')
\gexec
COMMIT;
SQL
  postgres_psql --dbname=postgres --set=marker_role="$marker_role" >/dev/null <<'SQL'
SELECT format('CREATE DATABASE ascendany_v2 OWNER %I TEMPLATE template0', :'marker_role')
\gexec
SQL
  postgres_psql --dbname=postgres --set=provision_comment="$provision_comment" >/dev/null <<'SQL'
SELECT format('COMMENT ON DATABASE ascendany_v2 IS %L', :'provision_comment')
\gexec
SQL
  postgres_psql --dbname=postgres --set=provision_comment="$provision_comment" >/dev/null <<'SQL'
BEGIN;
CREATE ROLE ascendany_database_owner NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendany_owner NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendany_runtime NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendany_migrator NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendany_backup NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendanyd_login LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendany_migrator_login LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendany_backup_login LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE ascendany_restore_login LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
SELECT format('COMMENT ON ROLE %I IS %L', rolname, :'provision_comment')
FROM pg_roles
WHERE rolname = ANY(ARRAY[
  'ascendany_database_owner', 'ascendany_owner', 'ascendany_runtime',
  'ascendany_migrator', 'ascendany_backup', 'ascendanyd_login',
  'ascendany_migrator_login', 'ascendany_backup_login', 'ascendany_restore_login'
])
ORDER BY rolname
\gexec
COMMIT;
SQL
  postgres_psql --dbname="$V2_DATABASE" <"$ROLE_BOOTSTRAP" >/dev/null
  set_role_password ascendanyd_login "$RUNTIME_PASSWORD_FILE"
  set_role_password ascendany_migrator_login "$MIGRATOR_PASSWORD_FILE"
  set_role_password ascendany_backup_login "$BACKUP_PASSWORD_FILE"
  set_role_password ascendany_restore_login "$RESTORE_PASSWORD_FILE"
}

verify_v2_ownership() {
  local result provision_comment="ascendany.v2.provision:$run_id"
  result="$(postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' \
    --set=marker_role="$marker_role" --set=provision_comment="$provision_comment" <<'SQL'
SELECT pg_get_userbyid(database.datdba),
       shobj_description(database.oid, 'pg_database') = :'provision_comment',
       (SELECT count(*) FROM pg_roles WHERE rolname = ANY(ARRAY[
         'ascendany_database_owner', 'ascendany_owner', 'ascendany_runtime',
         'ascendany_migrator', 'ascendany_backup', 'ascendanyd_login',
         'ascendany_migrator_login', 'ascendany_backup_login', 'ascendany_restore_login'
       ]) AND shobj_description(oid, 'pg_authid') = :'provision_comment'),
       (SELECT count(*) FROM pg_authid WHERE rolname = ANY(ARRAY[
         'ascendanyd_login', 'ascendany_migrator_login',
         'ascendany_backup_login', 'ascendany_restore_login'
       ]) AND rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'),
       EXISTS (
         SELECT 1 FROM pg_roles
         WHERE rolname = :'marker_role'
           AND shobj_description(oid, 'pg_authid') = :'provision_comment'
       )
FROM pg_database AS database
WHERE database.datname = 'ascendany_v2';
SQL
)"
  [[ "$result" == 'ascendany_database_owner|t|9|4|t' ]] ||
    fail v2_ownership 'v2 database, role comments, or SCRAM credentials are not owned by this run'
}

generate_pool_userlist_credential() {
  local plaintext="$STATE_ROOT/pgbouncer-userlist-$run_id"
  postgres_psql --dbname=postgres --tuples-only --no-align >"$plaintext" <<'SQL'
SELECT format('"%s" "%s"', rolname, rolpassword)
FROM pg_authid
WHERE rolname IN ('AscendAny', 'ascendanyd_login')
  AND rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'
ORDER BY CASE rolname WHEN 'AscendAny' THEN 0 ELSE 1 END;
SQL
  [[ "$(/usr/bin/wc -l <"$plaintext")" == 2 ]] || fail pgbouncer_auth 'ordered two-role SCRAM userlist generation failed'
  /usr/bin/chown 0:0 "$plaintext"
  /usr/bin/chmod 0600 "$plaintext"
  encrypt_credential pgbouncer_userlist "$plaintext" pgbouncer_userlist.cred
  /usr/bin/rm -f "$plaintext"
}

publish_credentials() {
  local name expected actual temporary destination
  while IFS='|' read -r name expected; do
    [[ "$name" =~ ^(runtime_db_password|migrator_db_password|backup_db_password|restore_db_password|pgbouncer_userlist)\.cred$ &&
       "$expected" =~ ^[0-9a-f]{64}$ ]] || fail credential_manifest 'credential manifest is invalid'
    destination="$CREDENTIAL_ROOT/$name"
    temporary="$CREDENTIAL_ROOT/.${name}.${run_id}"
    [[ ! -e "$destination" && ! -L "$destination" ]] ||
      fail credential_publish "credential output already exists: $name"
    [[ ! -e "$temporary" && ! -L "$temporary" ]] ||
      fail credential_publish "credential staging output already exists: $name"
    /usr/bin/install -o root -g root -m 0400 "$credential_stage/$name" "$temporary"
    /usr/bin/sync -f "$temporary"
    actual="$(/usr/bin/sha256sum "$temporary")"
    [[ "${actual%% *}" == "$expected" ]] || fail credential_publish "staged credential hash differs: $name"
    /usr/bin/ln "$temporary" "$destination"
    /usr/bin/sync -f "$CREDENTIAL_ROOT"
    /usr/bin/rm -f "$temporary"
    /usr/bin/sync -f "$CREDENTIAL_ROOT"
    /usr/bin/rm -f "$credential_stage/$name"
    /usr/bin/sync -f "$credential_stage"
    [[ "$(stat -Lc '%u:%g:%a:%h' "$destination")" == 0:0:400:1 ]] ||
      fail credential_publish "published credential metadata differs: $name"
  done <"$credential_manifest"
  /usr/bin/rmdir "$credential_stage"
}

legacy_pool_selinux_context_matches() {
  local path="$1" entry
  [[ -n "$legacy_pool_mount_label" &&
     "$(/usr/bin/stat -Lc '%C' -- "$path" 2>/dev/null || true)" == "$legacy_pool_mount_label" ]] ||
    return 1
  for entry in pgbouncer.ini userlist.txt; do
    [[ "$(/usr/bin/stat -Lc '%C' -- "$path/$entry" 2>/dev/null || true)" == "$legacy_pool_mount_label" ]] ||
      return 1
  done
}

label_legacy_pool_entry_for_publish() {
  local path="$1" entry="$2"
  [[ "$(/usr/bin/stat -Lc '%C' -- "$path" 2>/dev/null || true)" == "$legacy_pool_mount_label" ]] ||
    return 1
  /usr/bin/chcon --reference="$path" "$entry"
  [[ "$(/usr/bin/stat -Lc '%C' -- "$entry" 2>/dev/null || true)" == "$legacy_pool_mount_label" ]]
}

legacy_pool_structure_matches() {
  local path="$1" ownership="$2" directory_identity entry_identity entries
  [[ -d "$path" && ! -L "$path" &&
     "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null)" ]] || return 1
  entries="$(/usr/bin/find "$path" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C /usr/bin/sort)"
  [[ "$entries" == $'pgbouncer.ini|f\nuserlist.txt|f' ]] || return 1
  legacy_pool_selinux_context_matches "$path" || return 1
  directory_identity="$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")"
  case "$ownership" in
    legacy) [[ "$directory_identity" == 70:70:700 ]] || return 1 ;;
    normalized) [[ "$directory_identity" == 0:0:700 ]] || return 1 ;;
    recoverable) [[ "$directory_identity" == 70:70:700 || "$directory_identity" == 0:0:700 ]] || return 1 ;;
    *) return 1 ;;
  esac
  entry_identity="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$path/pgbouncer.ini")"
  case "$ownership" in
    legacy) [[ "$entry_identity" == 70:70:644:494:1 ]] || return 1 ;;
    normalized) [[ "$entry_identity" == 0:0:644:494:1 ]] || return 1 ;;
    recoverable) [[ "$entry_identity" == 70:70:644:494:1 || "$entry_identity" == 0:0:644:494:1 ]] || return 1 ;;
  esac
  entry_identity="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$path/userlist.txt")"
  case "$ownership" in
    legacy) [[ "$entry_identity" == 70:70:600:50:1 ]] || return 1 ;;
    normalized) [[ "$entry_identity" == 0:0:600:50:1 ]] || return 1 ;;
    recoverable) [[ "$entry_identity" == 70:70:600:50:1 || "$entry_identity" == 0:0:600:50:1 ]] || return 1 ;;
  esac
}

legacy_pool_backups_match() {
  local actual_ini actual_userlist
  local -a lines=()
  [[ -f "$legacy_pool_manifest" && ! -L "$legacy_pool_manifest" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$legacy_pool_manifest")" == 0:0:600:1 ]] || return 1
  mapfile -t lines <"$legacy_pool_manifest"
  [[ "${#lines[@]}" == 3 &&
     "${lines[0]}" == 'schema=ascendany.legacy-pgbouncer-manifest.v1' &&
     "${lines[1]}" =~ ^pgbouncer[.]ini\|[0-9a-f]{64}$ &&
     "${lines[2]}" =~ ^userlist[.]txt\|[0-9a-f]{64}$ ]] || return 1
  [[ -f "$legacy_pool_ini_backup" && ! -L "$legacy_pool_ini_backup" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$legacy_pool_ini_backup")" == 0:0:600:494:1 &&
     -f "$legacy_pool_userlist_backup" && ! -L "$legacy_pool_userlist_backup" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$legacy_pool_userlist_backup")" == 0:0:600:50:1 ]] || return 1
  actual_ini="$(/usr/bin/sha256sum "$legacy_pool_ini_backup")"; actual_ini="${actual_ini%% *}"
  actual_userlist="$(/usr/bin/sha256sum "$legacy_pool_userlist_backup")"; actual_userlist="${actual_userlist%% *}"
  [[ "${lines[1]}" == "pgbouncer.ini|$actual_ini" &&
     "${lines[2]}" == "userlist.txt|$actual_userlist" ]]
}

legacy_pool_tree_matches() {
  local path="$1" ownership="$2" actual_ini actual_userlist
  local -a lines=()
  legacy_pool_backups_match || return 1
  mapfile -t lines <"$legacy_pool_manifest"
  legacy_pool_structure_matches "$path" "$ownership" || return 1
  actual_ini="$(/usr/bin/sha256sum "$path/pgbouncer.ini")"; actual_ini="${actual_ini%% *}"
  actual_userlist="$(/usr/bin/sha256sum "$path/userlist.txt")"; actual_userlist="${actual_userlist%% *}"
  [[ "${lines[1]}" == "pgbouncer.ini|$actual_ini" &&
     "${lines[2]}" == "userlist.txt|$actual_userlist" ]]
}

durably_verify_legacy_pool_tree() {
  local path="$1" ownership="$2"
  legacy_pool_tree_matches "$path" "$ownership" || return 1
  /usr/bin/sync -f "$path/pgbouncer.ini" || return 1
  /usr/bin/sync -f "$path/userlist.txt" || return 1
  /usr/bin/sync -f "$path" || return 1
  legacy_pool_tree_matches "$path" "$ownership"
}

durably_verify_uncaptured_legacy_pool_tree() {
  legacy_pool_structure_matches "$POOL_CONFIG_ROOT" legacy || return 1
  legacy_pool_semantics_match || return 1
  /usr/bin/sync -f "$POOL_CONFIG_ROOT/pgbouncer.ini" || return 1
  /usr/bin/sync -f "$POOL_CONFIG_ROOT/userlist.txt" || return 1
  /usr/bin/sync -f "$POOL_CONFIG_ROOT" || return 1
  legacy_pool_structure_matches "$POOL_CONFIG_ROOT" legacy || return 1
  legacy_pool_semantics_match
}

durably_verify_recovered_legacy_pool_tree() {
  if [[ -e "$legacy_pool_manifest" || -L "$legacy_pool_manifest" ]]; then
    [[ -f "$legacy_pool_manifest" && ! -L "$legacy_pool_manifest" ]] || return 1
    durably_verify_legacy_pool_tree "$POOL_CONFIG_ROOT" legacy
  elif [[ "$phase" == prepared ]]; then
    durably_verify_uncaptured_legacy_pool_tree
  else
    return 1
  fi
}

legacy_pool_repairable_structure_matches() {
  local path="$1" entry name metadata expected_mode expected_size backup actual_size
  [[ -d "$path" && ! -L "$path" &&
     "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null)" &&
     ( "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == 0:0:700 ||
       "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == 70:70:700 ) ]] || return 1
  [[ "$(/usr/bin/stat -Lc '%C' -- "$path" 2>/dev/null || true)" == "$legacy_pool_mount_label" ]] ||
    return 1
  [[ -f "$path/pgbouncer.ini" && ! -L "$path/pgbouncer.ini" &&
     -f "$path/userlist.txt" && ! -L "$path/userlist.txt" ]] || return 1
  while IFS= read -r entry; do
    name="$(/usr/bin/basename "$entry")"
    case "$name" in
      pgbouncer.ini|".pgbouncer.ini.restore-${run_id}")
        expected_mode=644; expected_size=494; backup="$legacy_pool_ini_backup" ;;
      userlist.txt|".userlist.txt.restore-${run_id}")
        expected_mode=600; expected_size=50; backup="$legacy_pool_userlist_backup" ;;
      *) return 1 ;;
    esac
    metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$entry" 2>/dev/null || true)"
    [[ -f "$entry" && ! -L "$entry" ]] || return 1
    actual_size="$(/usr/bin/stat -Lc '%s' -- "$entry")"
    ((actual_size <= expected_size)) || return 1
    if [[ "$name" == .* ]]; then
      [[ "$metadata" == 0:0:600:*:1 ||
         "$metadata" == 70:70:600:*:1 ||
         "$metadata" == 0:0:${expected_mode}:*:1 ||
         "$metadata" == 70:70:${expected_mode}:*:1 ]] || return 1
      /usr/bin/cmp -s -n "$actual_size" "$entry" "$backup" || return 1
    else
      [[ "$metadata" == 0:0:${expected_mode}:*:1 ||
         "$metadata" == 70:70:${expected_mode}:*:1 ]] || return 1
      if ((actual_size < expected_size)); then
        /usr/bin/cmp -s -n "$actual_size" "$entry" "$backup" || return 1
      fi
    fi
  done < <(/usr/bin/find "$path" -mindepth 1 -maxdepth 1 -print | LC_ALL=C /usr/bin/sort)
}

restore_legacy_pool_bytes() {
  local path="$1" ownership="$2" owner temporary entry
  legacy_pool_backups_match || return 1
  legacy_pool_repairable_structure_matches "$path" || return 1
  case "$ownership" in
    legacy) owner=70 ;;
    normalized) owner=0 ;;
    *) return 1 ;;
  esac
  for entry in pgbouncer.ini userlist.txt; do
    temporary="$path/.${entry}.restore-${run_id}"
    if [[ -e "$temporary" || -L "$temporary" ]]; then
      [[ -f "$temporary" && ! -L "$temporary" ]] || return 1
      /usr/bin/rm -f "$temporary" || return 1
      /usr/bin/sync -f "$path" || return 1
    fi
    if [[ "$entry" == pgbouncer.ini ]]; then
      /usr/bin/install -o "$owner" -g "$owner" -m 0644 "$legacy_pool_ini_backup" "$temporary" || return 1
    else
      /usr/bin/install -o "$owner" -g "$owner" -m 0600 "$legacy_pool_userlist_backup" "$temporary" || return 1
    fi
    label_legacy_pool_entry_for_publish "$path" "$temporary" || return 1
    /usr/bin/sync -d "$temporary" || return 1
    /usr/bin/mv -f "$temporary" "$path/$entry" || return 1
    /usr/bin/sync -f "$path" || return 1
  done
  /usr/bin/chown "$owner:$owner" "$path" || return 1
  /usr/bin/chmod 0700 "$path" || return 1
  /usr/bin/sync -f "$path" || return 1
  legacy_pool_tree_matches "$path" "$ownership"
}

capture_legacy_pool_manifest() {
  local temporary="$legacy_pool_manifest.tmp" ini_sha userlist_sha backup
  legacy_pool_structure_matches "$POOL_CONFIG_ROOT" legacy ||
    fail old_pool 'old PgBouncer tree changed after preflight'
  [[ ! -e "$legacy_pool_manifest" && ! -L "$legacy_pool_manifest" &&
     ! -e "$temporary" && ! -L "$temporary" ]] ||
    fail old_pool 'legacy PgBouncer manifest output already exists'
  for backup in "$legacy_pool_ini_backup" "$legacy_pool_userlist_backup"; do
    [[ ! -e "$backup" && ! -L "$backup" && ! -e "$backup.tmp" && ! -L "$backup.tmp" ]] ||
      fail old_pool 'legacy PgBouncer backup output already exists'
  done
  /usr/bin/install -o root -g root -m 0600 "$POOL_CONFIG_ROOT/pgbouncer.ini" "$legacy_pool_ini_backup.tmp"
  /usr/bin/sync -d "$legacy_pool_ini_backup.tmp"
  /usr/bin/mv "$legacy_pool_ini_backup.tmp" "$legacy_pool_ini_backup"
  /usr/bin/sync -f "$STATE_ROOT"
  /usr/bin/install -o root -g root -m 0600 "$POOL_CONFIG_ROOT/userlist.txt" "$legacy_pool_userlist_backup.tmp"
  /usr/bin/sync -d "$legacy_pool_userlist_backup.tmp"
  /usr/bin/mv "$legacy_pool_userlist_backup.tmp" "$legacy_pool_userlist_backup"
  /usr/bin/sync -f "$STATE_ROOT"
  ini_sha="$(/usr/bin/sha256sum "$legacy_pool_ini_backup")"; ini_sha="${ini_sha%% *}"
  userlist_sha="$(/usr/bin/sha256sum "$legacy_pool_userlist_backup")"; userlist_sha="${userlist_sha%% *}"
  [[ "$ini_sha" == "$(/usr/bin/sha256sum "$POOL_CONFIG_ROOT/pgbouncer.ini" | /usr/bin/awk '{print $1}')" &&
     "$userlist_sha" == "$(/usr/bin/sha256sum "$POOL_CONFIG_ROOT/userlist.txt" | /usr/bin/awk '{print $1}')" ]] ||
    fail old_pool 'legacy PgBouncer bytes changed while protected backups were captured'
  /usr/bin/printf '%s\n' \
    'schema=ascendany.legacy-pgbouncer-manifest.v1' \
    "pgbouncer.ini|$ini_sha" \
    "userlist.txt|$userlist_sha" >"$temporary"
  /usr/bin/chown 0:0 "$temporary"
  /usr/bin/chmod 0600 "$temporary"
  /usr/bin/sync -f "$temporary"
  /usr/bin/mv "$temporary" "$legacy_pool_manifest"
  /usr/bin/sync -f "$STATE_ROOT"
  legacy_pool_tree_matches "$POOL_CONFIG_ROOT" legacy ||
    fail old_pool 'old PgBouncer tree changed while its manifest was captured'
}

normalize_legacy_pool_for_rollback() {
  legacy_pool_tree_matches "$POOL_CONFIG_ROOT" legacy ||
    fail old_pool 'old PgBouncer tree differs before rollback normalization'
  /usr/bin/chown 0:0 "$POOL_CONFIG_ROOT/pgbouncer.ini" "$POOL_CONFIG_ROOT/userlist.txt" "$POOL_CONFIG_ROOT"
  /usr/bin/chmod 0644 "$POOL_CONFIG_ROOT/pgbouncer.ini"
  /usr/bin/chmod 0600 "$POOL_CONFIG_ROOT/userlist.txt"
  /usr/bin/chmod 0700 "$POOL_CONFIG_ROOT"
  /usr/bin/sync -f "$POOL_CONFIG_ROOT/pgbouncer.ini"
  /usr/bin/sync -f "$POOL_CONFIG_ROOT/userlist.txt"
  /usr/bin/sync -f "$POOL_CONFIG_ROOT"
  legacy_pool_tree_matches "$POOL_CONFIG_ROOT" normalized ||
    fail old_pool 'old PgBouncer rollback tree was not normalized to root ownership'
}

native_pool_tree_matches() {
  local path="$1" entries
  [[ -d "$path" && ! -L "$path" &&
     "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null)" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == 0:0:755 ]] || return 1
  entries="$(/usr/bin/find "$path" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C /usr/bin/sort)"
  [[ "$entries" == $'pgbouncer-hba.conf|f\npgbouncer.ini|f' &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path/pgbouncer.ini")" == 0:0:644:1 &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path/pgbouncer-hba.conf")" == 0:0:644:1 ]] || return 1
  /usr/bin/cmp -s "$path/pgbouncer.ini" "$POOL_CONFIG_SOURCE" &&
    /usr/bin/cmp -s "$path/pgbouncer-hba.conf" "$POOL_HBA_SOURCE"
}

native_pool_deletion_tree_matches() {
  local path="$1" entries entry
  [[ -d "$path" && ! -L "$path" &&
     "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null)" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == 0:0:755 ]] || return 1
  entries="$(/usr/bin/find "$path" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C /usr/bin/sort)"
  case "$entries" in
    ''|'pgbouncer-hba.conf|f'|'pgbouncer.ini|f'|$'pgbouncer-hba.conf|f\npgbouncer.ini|f') ;;
    *) return 1 ;;
  esac
  for entry in pgbouncer.ini pgbouncer-hba.conf; do
    [[ -e "$path/$entry" || -L "$path/$entry" ]] || continue
    [[ -f "$path/$entry" && ! -L "$path/$entry" &&
       "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path/$entry")" == 0:0:644:1 ]] || return 1
  done
  [[ ! -e "$path/pgbouncer.ini" ]] || /usr/bin/cmp -s "$path/pgbouncer.ini" "$POOL_CONFIG_SOURCE" || return 1
  [[ ! -e "$path/pgbouncer-hba.conf" ]] || /usr/bin/cmp -s "$path/pgbouncer-hba.conf" "$POOL_HBA_SOURCE" || return 1
}

native_pool_staging_tree_matches() {
  local path="$1" entries entry source expected_size actual_size
  [[ -d "$path" && ! -L "$path" &&
     "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null)" &&
     ( "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == 0:0:700 ||
       "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == 0:0:755 ) ]] || return 1
  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    case "$(/usr/bin/basename "$entry")" in
      pgbouncer.ini) source="$POOL_CONFIG_SOURCE" ;;
      pgbouncer-hba.conf) source="$POOL_HBA_SOURCE" ;;
      .pgbouncer.ini.tmp) source="$POOL_CONFIG_SOURCE" ;;
      .pgbouncer-hba.conf.tmp) source="$POOL_HBA_SOURCE" ;;
      *) return 1 ;;
    esac
    [[ -f "$entry" && ! -L "$entry" ]] || return 1
    expected_size="$(/usr/bin/stat -Lc '%s' -- "$source")"
    actual_size="$(/usr/bin/stat -Lc '%s' -- "$entry")"
    if [[ "$(/usr/bin/basename "$entry")" == .* ]]; then
      [[ "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$entry")" == 0:0:600:1 ||
         "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$entry")" == 0:0:644:1 ]] || return 1
      ((actual_size <= expected_size)) || return 1
      /usr/bin/cmp -s -n "$actual_size" "$entry" "$source" || return 1
    else
      [[ "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$entry")" == 0:0:644:1 ]] || return 1
      /usr/bin/cmp -s "$entry" "$source" || return 1
    fi
  done < <(/usr/bin/find "$path" -mindepth 1 -maxdepth 1 -print | LC_ALL=C /usr/bin/sort)
  [[ ! -e "$path/pgbouncer.ini" || ! -e "$path/.pgbouncer.ini.tmp" ]] || return 1
  [[ ! -e "$path/pgbouncer-hba.conf" || ! -e "$path/.pgbouncer-hba.conf.tmp" ]] || return 1
}

legacy_pool_deletion_tree_matches() {
  local path="$1" entries entry expected_mode expected_size actual_sha manifest_line
  local -a lines=()
  [[ -f "$legacy_pool_manifest" && ! -L "$legacy_pool_manifest" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$legacy_pool_manifest")" == 0:0:600:1 ]] || return 1
  mapfile -t lines <"$legacy_pool_manifest"
  [[ "${#lines[@]}" == 3 &&
     "${lines[0]}" == 'schema=ascendany.legacy-pgbouncer-manifest.v1' ]] || return 1
  [[ -d "$path" && ! -L "$path" &&
     "$path" == "$(/usr/bin/realpath -e -- "$path" 2>/dev/null)" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$path")" == 0:0:700 ]] || return 1
  entries="$(/usr/bin/find "$path" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C /usr/bin/sort)"
  case "$entries" in
    ''|'pgbouncer.ini|f'|'userlist.txt|f'|$'pgbouncer.ini|f\nuserlist.txt|f') ;;
    *) return 1 ;;
  esac
  for entry in pgbouncer.ini userlist.txt; do
    [[ -e "$path/$entry" || -L "$path/$entry" ]] || continue
    if [[ "$entry" == pgbouncer.ini ]]; then
      expected_mode=644; expected_size=494; manifest_line="${lines[1]}"
    else
      expected_mode=600; expected_size=50; manifest_line="${lines[2]}"
    fi
    [[ -f "$path/$entry" && ! -L "$path/$entry" &&
       "$(/usr/bin/stat -Lc '%u:%g:%a:%s:%h' -- "$path/$entry")" == "0:0:$expected_mode:$expected_size:1" ]] || return 1
    actual_sha="$(/usr/bin/sha256sum "$path/$entry")"; actual_sha="${actual_sha%% *}"
    [[ "$manifest_line" == "$entry|$actual_sha" ]] || return 1
  done
}

remove_native_pool_tree_generation() {
  local path="$1" source_contract="$2" deletion parent
  deletion="${path}.deleting-${run_id}"
  parent="$(/usr/bin/dirname "$path")"
  [[ ! ( ( -e "$path" || -L "$path" ) && ( -e "$deletion" || -L "$deletion" ) ) ]] || return 1
  if [[ -e "$path" || -L "$path" ]]; then
    case "$source_contract" in
      exact) native_pool_tree_matches "$path" || return 1 ;;
      staging) native_pool_staging_tree_matches "$path" || return 1 ;;
      *) return 1 ;;
    esac
    [[ ! -e "$deletion" && ! -L "$deletion" ]] || return 1
    /usr/bin/mv "$path" "$deletion" || return 1
    /usr/bin/sync -f "$parent" || return 1
  fi
  [[ -e "$deletion" || -L "$deletion" ]] || return 0
  case "$source_contract" in
    exact) native_pool_deletion_tree_matches "$deletion" || return 1 ;;
    staging) native_pool_staging_tree_matches "$deletion" || return 1 ;;
    *) return 1 ;;
  esac
  for entry in pgbouncer.ini pgbouncer-hba.conf .pgbouncer.ini.tmp .pgbouncer-hba.conf.tmp; do
    [[ ! -e "$deletion/$entry" && ! -L "$deletion/$entry" ]] || {
      /usr/bin/rm -f "$deletion/$entry" || return 1
      /usr/bin/sync -f "$deletion" || return 1
    }
  done
  /usr/bin/rmdir "$deletion" || return 1
  /usr/bin/sync -f "$parent"
}

remove_native_pool_tree() {
  remove_native_pool_tree_generation "$1" exact
}

remove_native_pool_stage_tree() {
  remove_native_pool_tree_generation "$1" staging
}

remove_legacy_pool_tree() {
  local path deletion parent entry
  path="$1"
  deletion="${path}.deleting-${run_id}"
  parent="$(/usr/bin/dirname "$path")"
  [[ ! ( ( -e "$path" || -L "$path" ) && ( -e "$deletion" || -L "$deletion" ) ) ]] || return 1
  if [[ -e "$path" || -L "$path" ]]; then
    legacy_pool_tree_matches "$path" normalized || return 1
    [[ ! -e "$deletion" && ! -L "$deletion" ]] || return 1
    /usr/bin/mv "$path" "$deletion" || return 1
    /usr/bin/sync -f "$parent" || return 1
  fi
  [[ -e "$deletion" || -L "$deletion" ]] || return 0
  legacy_pool_deletion_tree_matches "$deletion" || return 1
  for entry in pgbouncer.ini userlist.txt; do
    [[ ! -e "$deletion/$entry" && ! -L "$deletion/$entry" ]] || {
      /usr/bin/rm -f "$deletion/$entry" || return 1
      /usr/bin/sync -f "$deletion" || return 1
    }
  done
  /usr/bin/rmdir "$deletion" || return 1
  /usr/bin/sync -f "$parent"
}

switch_to_native_pool() {
  stop_legacy_api_for_pool_switch ||
    fail old_pool 'old API could not be quiesced before the native PgBouncer cutover'
  set_container_restart_policy "$OLD_POOL_CONTAINER" no ||
    fail old_pool 'old PgBouncer restart ownership could not be fenced'
  stop_container_if_running "$OLD_POOL_CONTAINER" ||
    fail old_pool 'old PgBouncer container could not be stopped'
  wait_for_legacy_pool_conmon_scope_inactive ||
    fail old_pool 'old PgBouncer conmon scope did not quiesce before native ownership transfer'
  /usr/bin/podman rename "$OLD_POOL_CONTAINER" "$old_pool_rollback" >/dev/null
  [[ "$(container_restart_policy "$old_pool_rollback")" == no ]] ||
    fail old_pool 'renamed PgBouncer rollback container retained restart ownership'
  write_journal old_pool_stopped

  normalize_legacy_pool_for_rollback
  /usr/bin/mv "$POOL_CONFIG_ROOT" "$config_rollback"
  /usr/bin/mv "$config_stage" "$POOL_CONFIG_ROOT"
  /usr/bin/chown 0:0 "$POOL_PARENT" "$POOL_CONFIG_ROOT"
  /usr/bin/chmod 0755 "$POOL_PARENT" "$POOL_CONFIG_ROOT"
  /usr/bin/sync -f "$POOL_PARENT"
  write_journal config_published

  /usr/bin/systemctl start "$POOL_UNIT"
  [[ "$(/usr/bin/systemctl is-active "$POOL_UNIT")" == active ]] || fail native_pool 'native PgBouncer unit did not become active'
  wait_for_loopback_port 6432 || fail native_pool 'native PgBouncer did not bind loopback port 6432'

  local main_pid executable argv runtime_identity='' attempt
  main_pid="$(/usr/bin/systemctl show -P MainPID "$POOL_UNIT")"
  [[ "$main_pid" =~ ^[1-9][0-9]*$ ]] || fail native_pool 'native PgBouncer has no main PID'
  executable="$(readlink -e "/proc/$main_pid/exe" 2>/dev/null || true)"
  [[ "$executable" == /usr/bin/pgbouncer ]] || fail native_pool 'native PgBouncer executable differs from the attested RPM binary'
  argv="$(/usr/bin/tr '\0' '\n' <"/proc/$main_pid/cmdline")"
  [[ "$argv" == $'/usr/bin/pgbouncer\n-q\n/opt/ascendany/infra/pgbouncer/pgbouncer.ini' ]] ||
    fail native_pool 'native PgBouncer argv differs from the reviewed unit'

  for attempt in {1..30}; do
    if runtime_identity="$(client_psql "$RUNTIME_PASSWORD_FILE" 6432 "$V2_DATABASE" 'SELECT current_user' 2>/dev/null)"; then
      break
    fi
    /usr/bin/sleep 0.2
  done
  [[ "$runtime_identity" == ascendanyd_login ]] || fail native_pool 'runtime cannot authenticate to v2 through native PgBouncer'
  if client_psql "$RUNTIME_PASSWORD_FILE" 6432 "$LEGACY_DATABASE" 'SELECT current_user' >/dev/null 2>&1; then
    fail native_pool 'runtime crossed the PgBouncer legacy database boundary'
  fi
  if client_psql "$RUNTIME_PASSWORD_FILE" 5432 "$LEGACY_DATABASE" 'SELECT current_user' >/dev/null 2>&1; then
    fail postgres_hba 'runtime crossed the direct PostgreSQL legacy database boundary'
  fi
  start_legacy_api_and_probe || fail legacy_reconnect 'old API failed its DB-backed probe through native PgBouncer'
  [[ "$(postgres_psql --dbname=postgres --tuples-only --no-align <<'SQL'
SELECT count(*) FILTER (
         WHERE usesysid = 10
           AND backend_type = 'client backend'
           AND pid <> pg_backend_pid()
       ) = 0,
       count(*) FILTER (
         WHERE usename = 'AscendAny'
           AND datname = 'AscendAny'
           AND usesysid <> 10
           AND backend_type = 'client backend'
       ) >= 1
FROM pg_stat_activity;
SQL
)" == 't|t' ]] || fail legacy_reconnect 'legacy API did not reconnect under the non-superuser OID boundary'
  write_journal native_active
}

detect_maintenance_role() {
  if postgres_psql_as "$CLUSTER_ADMIN_ROLE" --dbname=postgres --tuples-only --no-align --command='SELECT current_user' 2>/dev/null |
    /usr/bin/grep -qx "$CLUSTER_ADMIN_ROLE"; then
    maintenance_role="$CLUSTER_ADMIN_ROLE"
  else
    maintenance_role="$LEGACY_ROLE"
  fi
}

remove_published_credentials() {
  local name expected actual destination temporary metadata source source_size temporary_size
  [[ -f "$credential_manifest" && ! -L "$credential_manifest" ]] || return 0
  while IFS='|' read -r name expected; do
    [[ "$name" =~ ^(runtime_db_password|migrator_db_password|backup_db_password|restore_db_password|pgbouncer_userlist)\.cred$ &&
       "$expected" =~ ^[0-9a-f]{64}$ ]] || { recovery_failed=1; continue; }
    destination="$CREDENTIAL_ROOT/$name"
    temporary="$CREDENTIAL_ROOT/.${name}.${run_id}"
    if [[ -e "$destination" || -L "$destination" ]]; then
      [[ -f "$destination" && ! -L "$destination" ]] || { recovery_failed=1; continue; }
      actual="$(/usr/bin/sha256sum "$destination")"
      if [[ "${actual%% *}" == "$expected" ]]; then
        /usr/bin/rm -f "$destination" || recovery_failed=1
      else
        recovery_failed=1
      fi
    fi
    if [[ -e "$temporary" || -L "$temporary" ]]; then
      metadata="$(/usr/bin/stat -Lc '%u:%g:%a' "$temporary" 2>/dev/null || true)"
      source="$credential_stage/$name"
      source_size="$(/usr/bin/stat -Lc '%s' "$source" 2>/dev/null || true)"
      temporary_size="$(/usr/bin/stat -Lc '%s' "$temporary" 2>/dev/null || true)"
      actual="$(/usr/bin/sha256sum "$source" 2>/dev/null || true)"
      if [[ -f "$temporary" && ! -L "$temporary" && "$metadata" == 0:0:400 &&
            "$(/usr/bin/sha256sum "$temporary" | /usr/bin/awk '{print $1}')" == "$expected" ]]; then
        /usr/bin/rm -f "$temporary" || recovery_failed=1
      elif [[ -f "$temporary" && ! -L "$temporary" && "$metadata" == 0:0:600 &&
              -f "$source" && ! -L "$source" && "${actual%% *}" == "$expected" &&
              "$source_size" =~ ^[0-9]+$ && "$temporary_size" =~ ^[0-9]+$ &&
              "$temporary_size" -le "$source_size" ]] &&
           /usr/bin/cmp -s -n "$temporary_size" "$temporary" "$source"; then
        /usr/bin/rm -f "$temporary" || recovery_failed=1
      else
        recovery_failed=1
      fi
    fi
  done <"$credential_manifest"
  /usr/bin/sync -f "$CREDENTIAL_ROOT" || recovery_failed=1
}

rollback_v2() {
  local provision_comment="ascendany.v2.provision:$run_id" database_state role_state marker_state
  if ! marker_state="$(postgres_psql --dbname=postgres --tuples-only --no-align --set=marker_role="$marker_role" --set=provision_comment="$provision_comment" <<'SQL' 2>/dev/null
SELECT CASE
  WHEN count(*) = 0 THEN 'absent'
  WHEN count(*) = 1 AND bool_and(shobj_description(oid, 'pg_authid') = :'provision_comment') THEN 'owned'
  ELSE 'foreign'
END
FROM pg_roles
WHERE rolname = :'marker_role';
SQL
)"; then
    recovery_failed=1
    return
  fi
  [[ "$marker_state" == absent || "$marker_state" == owned ]] || { recovery_failed=1; return; }
  if ! database_state="$(postgres_psql --dbname=postgres --tuples-only --no-align \
    --set=provision_comment="$provision_comment" --set=marker_role="$marker_role" <<'SQL' 2>/dev/null
SELECT CASE
  WHEN count(*) = 0 THEN 'absent'
  WHEN count(*) = 1 AND bool_and(pg_get_userbyid(datdba) = :'marker_role')
       AND bool_and(shobj_description(oid, 'pg_database') = :'provision_comment') THEN 'marker_owned'
  WHEN count(*) = 1 AND bool_and(pg_get_userbyid(datdba) = :'marker_role')
       AND bool_and(shobj_description(oid, 'pg_database') IS NULL) THEN 'marker_uncommented'
  WHEN count(*) = 1 AND bool_and(pg_get_userbyid(datdba) = 'ascendany_database_owner')
       AND bool_and(shobj_description(oid, 'pg_database') = :'provision_comment') THEN 'database_owner_owned'
  ELSE 'foreign'
END
FROM pg_database
WHERE datname = 'ascendany_v2';
SQL
)"; then
    recovery_failed=1
    return
  fi
  case "$database_state" in
      absent) ;;
      marker_owned|database_owner_owned)
        postgres_psql --dbname=postgres >/dev/null 2>&1 <<'SQL' || recovery_failed=1
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = 'ascendany_v2' AND pid <> pg_backend_pid();
DROP DATABASE ascendany_v2;
SQL
        ;;
      marker_uncommented)
        if [[ "$marker_state" == owned ]]; then
          postgres_psql --dbname=postgres >/dev/null 2>&1 <<'SQL' || recovery_failed=1
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = 'ascendany_v2' AND pid <> pg_backend_pid();
DROP DATABASE ascendany_v2;
SQL
        else
          recovery_failed=1
        fi
        ;;
      *) recovery_failed=1 ;;
  esac

  role_state="$(postgres_psql --dbname=postgres --tuples-only --no-align --set=provision_comment="$provision_comment" <<'SQL' 2>/dev/null || true
SELECT count(*)
FROM pg_roles
WHERE rolname = ANY(ARRAY[
  'ascendany_database_owner', 'ascendany_owner', 'ascendany_runtime',
  'ascendany_migrator', 'ascendany_backup', 'ascendanyd_login',
  'ascendany_migrator_login', 'ascendany_backup_login', 'ascendany_restore_login'
])
  AND shobj_description(oid, 'pg_authid') IS DISTINCT FROM :'provision_comment';
SQL
)"
  if [[ "$role_state" == 0 ]]; then
    postgres_psql --dbname=postgres >/dev/null 2>&1 <<'SQL' || recovery_failed=1
SELECT format('DROP OWNED BY %I', rolname)
FROM pg_roles
WHERE rolname = ANY(ARRAY[
  'ascendanyd_login', 'ascendany_migrator_login', 'ascendany_backup_login',
  'ascendany_restore_login', 'ascendany_runtime', 'ascendany_backup',
  'ascendany_migrator', 'ascendany_owner', 'ascendany_database_owner'
])
ORDER BY array_position(ARRAY[
  'ascendanyd_login', 'ascendany_migrator_login', 'ascendany_backup_login',
  'ascendany_restore_login', 'ascendany_runtime', 'ascendany_backup',
  'ascendany_migrator', 'ascendany_owner', 'ascendany_database_owner'
], rolname)
\gexec
SELECT format('DROP ROLE %I', rolname)
FROM pg_roles
WHERE rolname = ANY(ARRAY[
  'ascendanyd_login', 'ascendany_migrator_login', 'ascendany_backup_login',
  'ascendany_restore_login', 'ascendany_runtime', 'ascendany_backup',
  'ascendany_migrator', 'ascendany_owner', 'ascendany_database_owner'
])
ORDER BY array_position(ARRAY[
  'ascendanyd_login', 'ascendany_migrator_login', 'ascendany_backup_login',
  'ascendany_restore_login', 'ascendany_runtime', 'ascendany_backup',
  'ascendany_migrator', 'ascendany_owner', 'ascendany_database_owner'
], rolname)
\gexec
SQL
  else
    recovery_failed=1
  fi

  if [[ "$marker_state" == owned ]]; then
    postgres_psql --dbname=postgres --set=marker_role="$marker_role" >/dev/null 2>&1 <<'SQL' || recovery_failed=1
SELECT format('DROP ROLE %I', :'marker_role')
\gexec
SQL
  elif [[ "$marker_state" != absent ]]; then
    recovery_failed=1
  fi
}

catalog_role_state_for_recovery() {
  local result
  detect_maintenance_role
  if [[ "$maintenance_role" == "$CLUSTER_ADMIN_ROLE" ]]; then
    result="$(postgres_psql --dbname="$LEGACY_DATABASE" --tuples-only --no-align <<'SQL' 2>/dev/null || true
SELECT admin.oid = 10
   AND admin.rolcanlogin AND admin.rolsuper AND NOT admin.rolinherit
   AND NOT admin.rolcreatedb AND NOT admin.rolcreaterole
   AND NOT admin.rolreplication AND NOT admin.rolbypassrls
   AND admin.rolconnlimit = -1
   AND (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = admin.oid) IS NULL
   AND admin.rolpassword IS NULL
   AND shobj_description(admin.oid, 'pg_authid') = 'ascendany.cluster.bootstrap.v2'
   AND legacy.oid <> 10
   AND legacy.rolcanlogin AND NOT legacy.rolsuper AND legacy.rolinherit
   AND NOT legacy.rolcreatedb AND NOT legacy.rolcreaterole
   AND NOT legacy.rolreplication AND NOT legacy.rolbypassrls
   AND legacy.rolconnlimit = -1
   AND (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = legacy.oid) IS NULL
   AND legacy.rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'
   AND shobj_description(legacy.oid, 'pg_authid') = 'ascendany.legacy.runtime.v2'
   AND pg_get_userbyid(database.datdba) = 'ascendany_cluster_admin'
   AND pg_get_userbyid(task.relowner) = 'AscendAny'
   AND pg_get_userbyid(events.relowner) = 'AscendAny'
FROM pg_authid AS admin
CROSS JOIN pg_authid AS legacy
JOIN pg_database AS database ON database.datname = 'AscendAny'
JOIN pg_class AS task ON task.oid = 'ascendany.import_tasks'::regclass
JOIN pg_class AS events ON events.oid = 'ascendany.import_task_events'::regclass
WHERE admin.rolname = 'ascendany_cluster_admin'
  AND legacy.rolname = 'AscendAny';
SQL
)"
    [[ "$result" == t ]] && printf 'split\n' || printf 'invalid\n'
    return
  fi
  result="$(postgres_psql --dbname="$LEGACY_DATABASE" --tuples-only --no-align <<'SQL' 2>/dev/null || true
SELECT legacy.oid = 10
   AND legacy.rolcanlogin AND legacy.rolsuper AND legacy.rolinherit
   AND legacy.rolcreatedb AND legacy.rolcreaterole
   AND legacy.rolreplication AND legacy.rolbypassrls
   AND legacy.rolconnlimit = -1
   AND (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = legacy.oid) IS NULL
   AND legacy.rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'
   AND shobj_description(legacy.oid, 'pg_authid') IS NULL
   AND pg_get_userbyid(database.datdba) = 'AscendAny'
   AND pg_get_userbyid(task.relowner) = 'AscendAny'
   AND pg_get_userbyid(events.relowner) = 'AscendAny'
   AND NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_cluster_admin')
FROM pg_authid AS legacy
JOIN pg_database AS database ON database.datname = 'AscendAny'
JOIN pg_class AS task ON task.oid = 'ascendany.import_tasks'::regclass
JOIN pg_class AS events ON events.oid = 'ascendany.import_task_events'::regclass
WHERE legacy.rolname = 'AscendAny';
SQL
)"
  [[ "$result" == t ]] && printf 'bootstrap\n' || printf 'invalid\n'
}

restore_safe_postgres_access() {
  local role_state="$1"
  local hba_current ident_current hba_original='' ident_original=''
  local hba_bootstrap hba_final ident_bootstrap ident_final
  hba_current="$(postgres_file_hash "$POSTGRES_HBA_PATH" 2>/dev/null || true)"
  ident_current="$(postgres_file_hash "$POSTGRES_IDENT_PATH" 2>/dev/null || true)"
  hba_bootstrap="$(/usr/bin/sha256sum "$POSTGRES_HBA_BOOTSTRAP_SOURCE")"; hba_bootstrap="${hba_bootstrap%% *}"
  hba_final="$(/usr/bin/sha256sum "$POSTGRES_HBA_SOURCE")"; hba_final="${hba_final%% *}"
  ident_bootstrap="$(/usr/bin/sha256sum "$POSTGRES_IDENT_BOOTSTRAP_SOURCE")"; ident_bootstrap="${ident_bootstrap%% *}"
  ident_final="$(/usr/bin/sha256sum "$POSTGRES_IDENT_SOURCE")"; ident_final="${ident_final%% *}"

  if [[ -f "$hba_backup" && ! -L "$hba_backup" && -f "$hba_backup_sha" && ! -L "$hba_backup_sha" ]]; then
    hba_original="$(<"$hba_backup_sha")"
    [[ "$hba_original" =~ ^[0-9a-f]{64}$ &&
       "$(/usr/bin/sha256sum "$hba_backup" | /usr/bin/awk '{print $1}')" == "$hba_original" ]] || {
      recovery_failed=1
      return
    }
  fi
  if [[ -f "$ident_backup" && ! -L "$ident_backup" && -f "$ident_backup_sha" && ! -L "$ident_backup_sha" ]]; then
    ident_original="$(<"$ident_backup_sha")"
    [[ "$ident_original" =~ ^[0-9a-f]{64}$ &&
       "$(/usr/bin/sha256sum "$ident_backup" | /usr/bin/awk '{print $1}')" == "$ident_original" ]] || {
      recovery_failed=1
      return
    }
  fi
  if [[ "$role_state" == bootstrap && ( -z "$hba_original" || -z "$ident_original" ) ]]; then
    require_original_access_rules
    return
  fi
  [[ "$hba_current" == "$hba_bootstrap" || "$hba_current" == "$hba_final" ||
     ( -n "$hba_original" && "$hba_current" == "$hba_original" ) ]] || { recovery_failed=1; return; }
  [[ "$ident_current" == "$ident_bootstrap" || "$ident_current" == "$ident_final" ||
     ( -n "$ident_original" && "$ident_current" == "$ident_original" ) ]] || { recovery_failed=1; return; }

  if [[ "$role_state" == split ]]; then
    install_postgres_access final "$POSTGRES_HBA_SOURCE" "$POSTGRES_IDENT_SOURCE" || recovery_failed=1
    ((recovery_failed == 0)) && require_final_hba_rules || recovery_failed=1
    return
  fi
  install_postgres_access original "$hba_backup" "$ident_backup" || recovery_failed=1
  ((recovery_failed == 0)) && require_original_access_rules || recovery_failed=1
}

restore_old_pool_layout() {
  /usr/bin/systemctl stop "$POOL_UNIT" >/dev/null 2>&1 || recovery_failed=1
  /usr/bin/systemctl disable "$POOL_UNIT" >/dev/null 2>&1 || recovery_failed=1
  [[ "$(/usr/bin/systemctl is-active "$POOL_UNIT" 2>/dev/null || true)" == inactive ]] || recovery_failed=1
  [[ "$(/usr/bin/systemctl is-enabled "$POOL_UNIT" 2>/dev/null || true)" == disabled ]] || recovery_failed=1
  ((recovery_failed == 0)) || return
  stop_container_if_running "$old_pool_rollback" || recovery_failed=1
  stop_container_if_running "$OLD_POOL_CONTAINER" || recovery_failed=1
  ((recovery_failed == 0)) || return

  if [[ ! -d "$POOL_PARENT" || -L "$POOL_PARENT" ||
        "$POOL_PARENT" != "$(/usr/bin/realpath -e -- "$POOL_PARENT" 2>/dev/null)" ||
        "$(/usr/bin/stat -Lc '%u:%g' -- "$POOL_PARENT" 2>/dev/null)" != 0:0 ]]; then
    recovery_failed=1
    return
  fi
  /usr/bin/chmod 0700 "$POOL_PARENT" >/dev/null 2>&1 || recovery_failed=1
  ((recovery_failed == 0)) || return

  if [[ -e "$legacy_pool_manifest" || -L "$legacy_pool_manifest" ]]; then
    legacy_pool_backups_match || recovery_failed=1
    ((recovery_failed == 0)) || return
    if [[ -d "$config_rollback" && ! -L "$config_rollback" ]]; then
      restore_legacy_pool_bytes "$config_rollback" normalized || recovery_failed=1
      if [[ -e "$POOL_CONFIG_ROOT" || -L "$POOL_CONFIG_ROOT" ||
            -e "${POOL_CONFIG_ROOT}.deleting-${run_id}" || -L "${POOL_CONFIG_ROOT}.deleting-${run_id}" ]]; then
        remove_native_pool_tree "$POOL_CONFIG_ROOT" || recovery_failed=1
      fi
      if ((recovery_failed == 0)); then
        /usr/bin/mv "$config_rollback" "$POOL_CONFIG_ROOT" || recovery_failed=1
        /usr/bin/sync -f "$POOL_PARENT" || recovery_failed=1
      fi
    elif [[ -d "$POOL_CONFIG_ROOT" && ! -L "$POOL_CONFIG_ROOT" ]]; then
      restore_legacy_pool_bytes "$POOL_CONFIG_ROOT" legacy || recovery_failed=1
    else
      recovery_failed=1
    fi
    if ((recovery_failed == 0)); then
      restore_legacy_pool_bytes "$POOL_CONFIG_ROOT" legacy || recovery_failed=1
    fi
  elif [[ "$phase" == prepared && -d "$POOL_CONFIG_ROOT" && ! -L "$POOL_CONFIG_ROOT" ]]; then
    durably_verify_uncaptured_legacy_pool_tree || recovery_failed=1
  else
    recovery_failed=1
  fi

  if container_exists "$old_pool_rollback"; then
    if container_exists "$OLD_POOL_CONTAINER"; then
      recovery_failed=1
    else
      /usr/bin/podman rename "$old_pool_rollback" "$OLD_POOL_CONTAINER" >/dev/null 2>&1 || recovery_failed=1
    fi
  fi
  container_exists "$OLD_POOL_CONTAINER" || recovery_failed=1
  if ((recovery_failed == 0)); then
    set_container_restart_policy "$OLD_POOL_CONTAINER" always || recovery_failed=1
  fi
}

start_old_pool_after_recovery() {
  ((recovery_failed == 0)) || return
  start_legacy_pool_container "$OLD_POOL_CONTAINER" >/dev/null 2>&1 || recovery_failed=1
  wait_for_loopback_port 6432 || recovery_failed=1
  container_running "$OLD_POOL_CONTAINER" || recovery_failed=1
  durably_verify_recovered_legacy_pool_tree || recovery_failed=1
  if ((recovery_failed != 0)); then
    set_container_restart_policy "$OLD_POOL_CONTAINER" no >/dev/null 2>&1 || recovery_failed=1
    stop_container_if_running "$OLD_POOL_CONTAINER" >/dev/null 2>&1 || recovery_failed=1
    container_running "$OLD_POOL_CONTAINER" && recovery_failed=1
  fi
}

fence_failed_legacy_recovery() {
  stop_legacy_api_for_pool_switch >/dev/null 2>&1 || recovery_failed=1
  container_exists "$OLD_POOL_CONTAINER" || return 0
  set_container_restart_policy "$OLD_POOL_CONTAINER" no >/dev/null 2>&1 || recovery_failed=1
  stop_container_if_running "$OLD_POOL_CONTAINER" >/dev/null 2>&1 || recovery_failed=1
  container_running "$OLD_POOL_CONTAINER" && recovery_failed=1
}

journal_file_matches_run() {
  local path="$1" expected_run_id="$2"
  local owner_gid
  local -a lines=()
  owner_gid="$(/usr/bin/id -g)"
  [[ -f "$path" && ! -L "$path" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$path")" == "$EUID:$owner_gid:600:1" ]] || return 1
  mapfile -t lines <"$path"
  [[ "${#lines[@]}" == 4 &&
     "${lines[0]}" == 'schema=ascendany.postgres-pgbouncer.provision.v1' &&
     "${lines[1]}" == "run_id=$expected_run_id" &&
     "${lines[2]}" == phase=* &&
     "${lines[3]}" == "marker_role=ascendany_v2_marker_${expected_run_id}" ]] || return 1
  case "${lines[2]#phase=}" in
    prepared|bootstrap_access|legacy_quiesced|legacy_split|v2_database|credentials_published|old_pool_stopped|config_published|native_active|committed) ;;
    *) return 1 ;;
  esac
}

consume_recovered_state_root() {
  local state_run_id="$1" recovered_state state_parent entry name metadata entries
  local credential_directory credential_entry credential_name owner_gid
  [[ "$state_run_id" =~ ^[0-9a-f]{32}$ ]] || return 1
  owner_gid="$(/usr/bin/id -g)"
  recovered_state="${STATE_ROOT}.recovered-${state_run_id}"
  state_parent="$(/usr/bin/dirname "$STATE_ROOT")"
  [[ -d "$recovered_state" && ! -L "$recovered_state" &&
     "$recovered_state" == "$(/usr/bin/realpath -e -- "$recovered_state" 2>/dev/null)" &&
     "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$recovered_state")" == "$EUID:$owner_gid:700" ]] || return 1

  while IFS= read -r entry; do
    name="$(/usr/bin/basename "$entry")"
    case "$name" in
      "credentials-${state_run_id}")
        [[ -d "$entry" && ! -L "$entry" &&
           "$(/usr/bin/stat -Lc '%u:%g:%a' -- "$entry")" == "$EUID:$owner_gid:700" ]] || return 1
        ;;
      .journal.*)
        [[ "$name" =~ ^[.]journal[.][0-9]+$ ]] || return 1
        metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$entry" 2>/dev/null || true)"
        [[ -f "$entry" && ! -L "$entry" && "$metadata" == "$EUID:$owner_gid:600:1" ]] || return 1
        ;;
      journal|"credentials-${state_run_id}.manifest"|"credentials-${state_run_id}.manifest.tmp"|\
      "legacy-pgbouncer-${state_run_id}.manifest"|"legacy-pgbouncer-${state_run_id}.manifest.tmp"|\
      "legacy-pgbouncer-${state_run_id}.pgbouncer.ini"|"legacy-pgbouncer-${state_run_id}.pgbouncer.ini.tmp"|\
      "legacy-pgbouncer-${state_run_id}.userlist.txt"|"legacy-pgbouncer-${state_run_id}.userlist.txt.tmp"|\
      "postgresql-hba-${state_run_id}.original"|"postgresql-hba-${state_run_id}.original.tmp"|\
      "postgresql-hba-${state_run_id}.original.sha256"|"postgresql-hba-${state_run_id}.original.sha256.tmp"|\
      "postgresql-ident-${state_run_id}.original"|"postgresql-ident-${state_run_id}.original.tmp"|\
      "postgresql-ident-${state_run_id}.original.sha256"|"postgresql-ident-${state_run_id}.original.sha256.tmp"|\
      "pgbouncer-userlist-${state_run_id}")
        metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$entry" 2>/dev/null || true)"
        [[ -f "$entry" && ! -L "$entry" &&
           ( "$metadata" == "$EUID:$owner_gid:600:1" || "$metadata" == "$EUID:$owner_gid:400:1" ) ]] || return 1
        ;;
      *) return 1 ;;
    esac
  done < <(/usr/bin/find "$recovered_state" -mindepth 1 -maxdepth 1 -print | LC_ALL=C /usr/bin/sort)
  if [[ -e "$recovered_state/journal" || -L "$recovered_state/journal" ]]; then
    journal_file_matches_run "$recovered_state/journal" "$state_run_id" || return 1
  fi

  credential_directory="$recovered_state/credentials-${state_run_id}"
  if [[ -d "$credential_directory" && ! -L "$credential_directory" ]]; then
    while IFS= read -r credential_entry; do
      credential_name="$(/usr/bin/basename "$credential_entry")"
      case "$credential_name" in
        runtime_db_password.cred|migrator_db_password.cred|backup_db_password.cred|restore_db_password.cred|pgbouncer_userlist.cred) ;;
        *) return 1 ;;
      esac
      metadata="$(/usr/bin/stat -Lc '%u:%g:%a:%h' -- "$credential_entry" 2>/dev/null || true)"
      [[ -f "$credential_entry" && ! -L "$credential_entry" &&
         ( "$metadata" == "$EUID:$owner_gid:400:1" || "$metadata" == "$EUID:$owner_gid:600:1" ) ]] || return 1
    done < <(/usr/bin/find "$credential_directory" -mindepth 1 -maxdepth 1 -print | LC_ALL=C /usr/bin/sort)
    while IFS= read -r credential_entry; do
      /usr/bin/rm -f "$credential_entry" || return 1
      /usr/bin/sync -f "$credential_directory" || return 1
    done < <(/usr/bin/find "$credential_directory" -mindepth 1 -maxdepth 1 -type f -print | LC_ALL=C /usr/bin/sort)
    /usr/bin/rmdir "$credential_directory" || return 1
    /usr/bin/sync -f "$recovered_state" || return 1
  fi

  entries="$(/usr/bin/find "$recovered_state" -mindepth 1 -maxdepth 1 ! -name journal -type f -print | LC_ALL=C /usr/bin/sort)"
  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    /usr/bin/rm -f "$entry" || return 1
    /usr/bin/sync -f "$recovered_state" || return 1
  done <<<"$entries"
  if [[ -e "$recovered_state/journal" || -L "$recovered_state/journal" ]]; then
    /usr/bin/rm -f "$recovered_state/journal" || return 1
    /usr/bin/sync -f "$recovered_state" || return 1
  fi
  [[ -z "$(/usr/bin/find "$recovered_state" -mindepth 1 -maxdepth 1 -print -quit)" ]] || return 1
  /usr/bin/rmdir "$recovered_state" || return 1
  /usr/bin/sync -f "$state_parent"
}

tombstone_recovered_state_root() {
  local recovered_state="$1" state_parent
  state_parent="$(/usr/bin/dirname "$STATE_ROOT")"
  /usr/bin/sync -f "$STATE_ROOT" || return 1
  [[ ! -e "$recovered_state" && ! -L "$recovered_state" ]] || return 1
  /usr/bin/mv "$STATE_ROOT" "$recovered_state" || return 1
  /usr/bin/sync -f "$state_parent"
}

recover_precommit() {
  local role_state
  recovery_failed=0
  stop_legacy_api_for_pool_switch || {
    printf 'FAIL [recovery_api]: old API could not be quiesced before pool recovery\n' >&2
    return 1
  }
  role_state="$(catalog_role_state_for_recovery)"
  [[ "$role_state" == bootstrap || "$role_state" == split ]] || {
    printf 'FAIL [recovery_catalog]: PostgreSQL role state is mixed or foreign\n' >&2
    return 1
  }
  restore_old_pool_layout
  remove_published_credentials
  rollback_v2
  restore_safe_postgres_access "$role_state"
  start_old_pool_after_recovery
  if ((recovery_failed == 0)); then
    start_legacy_api_and_probe || recovery_failed=1
  fi
  if ((recovery_failed != 0)); then
    fence_failed_legacy_recovery
  fi
  if ((recovery_failed == 0)); then
    if [[ -e "$config_stage" || -L "$config_stage" ||
          -e "${config_stage}.deleting-${run_id}" || -L "${config_stage}.deleting-${run_id}" ]]; then
      remove_native_pool_stage_tree "$config_stage" || recovery_failed=1
    fi
  fi
  if ((recovery_failed == 0)); then
    local recovered_state="${STATE_ROOT}.recovered-${run_id}"
    tombstone_recovered_state_root "$recovered_state" || {
      printf 'FAIL [recovery_state]: recovery journal tombstone was not durably published\n' >&2
      return 1
    }
    consume_recovered_state_root "$run_id" || {
      printf 'FAIL [recovery_state]: recovered-state tombstone cleanup is incomplete\n' >&2
      return 1
    }
    pass recovered_precommit
    return 0
  fi
  printf 'FAIL [recovery_incomplete]: durable journal retained for operator recovery\n' >&2
  return 1
}

consume_committed_state_root() {
  local state_root="$1" state_run_id="$2" manifest_path="$3"
  local state_parent manifest_name committed_state expected_entries actual_entries entry metadata
  local -a manifest_lines=()

  [[ "$state_run_id" =~ ^[0-9a-f]{32}$ ]] || fail committed_state 'committed state run id is invalid'
  state_parent="$(/usr/bin/dirname "$state_root")"
  [[ "$state_parent" == "$(/usr/bin/realpath -e "$state_parent")" &&
     -d "$state_parent" && ! -L "$state_parent" &&
     "$(/usr/bin/stat -Lc '%u' "$state_parent")" == "$EUID" &&
     $((8#$(/usr/bin/stat -Lc '%a' "$state_parent") & 8#022)) == 0 ]] ||
    fail committed_state 'committed state parent is outside the protected owner boundary'
  manifest_name="credentials-${state_run_id}.manifest"
  committed_state="${state_root}.committed-${state_run_id}"
  [[ "$manifest_path" == "$state_root/$manifest_name" ]] ||
    fail committed_state 'committed credential manifest path is invalid'
  [[ ! ( ( -e "$state_root" || -L "$state_root" ) &&
           ( -e "$committed_state" || -L "$committed_state" ) ) ]] ||
    fail committed_state 'active and terminal committed state coexist'
  if [[ -e "$state_root" || -L "$state_root" ]]; then
    [[ "$state_root" == /* && "$state_root" == "$(/usr/bin/realpath -e "$state_root")" &&
       -d "$state_root" && ! -L "$state_root" &&
       "$(/usr/bin/stat -Lc '%u:%a' "$state_root")" == "$EUID:700" ]] ||
      fail committed_state 'committed state root is not a protected canonical directory'
    [[ -f "$manifest_path" && ! -L "$manifest_path" &&
       "$(/usr/bin/stat -Lc '%u:%a:%h' "$manifest_path")" == "$EUID:600:1" &&
       -f "$state_root/journal" && ! -L "$state_root/journal" &&
       "$(/usr/bin/stat -Lc '%u:%a:%h' "$state_root/journal")" == "$EUID:600:1" ]] ||
      fail committed_state 'committed state journal or credential manifest metadata is invalid'
    journal_file_matches_run "$state_root/journal" "$state_run_id" &&
      /usr/bin/grep -Fqx 'phase=committed' "$state_root/journal" ||
      fail committed_state 'committed journal does not contain the terminal phase'
    expected_entries="$(/usr/bin/printf '%s\n' "$manifest_name" journal | LC_ALL=C /usr/bin/sort)"
    actual_entries="$(/usr/bin/find "$state_root" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C /usr/bin/sort)"
    [[ "$actual_entries" == "$expected_entries" ]] ||
      fail committed_state 'committed state root contains an unexpected entry'
    /usr/bin/sync -f "$state_root"
    /usr/bin/mv "$state_root" "$committed_state"
    /usr/bin/sync -f "$state_parent"
  fi
  [[ -d "$committed_state" && ! -L "$committed_state" &&
     "$committed_state" == "$(/usr/bin/realpath -e "$committed_state" 2>/dev/null)" &&
     "$(/usr/bin/stat -Lc '%u:%a' "$committed_state")" == "$EUID:700" ]] ||
    fail committed_state 'committed-state tombstone identity is invalid'
  actual_entries="$(/usr/bin/find "$committed_state" -mindepth 1 -maxdepth 1 -printf '%f|%y\n' | LC_ALL=C /usr/bin/sort)"
  expected_entries="$(/usr/bin/printf '%s\n' "$manifest_name|f" 'journal|f' | LC_ALL=C /usr/bin/sort)"
  case "$actual_entries" in
    ''|"$manifest_name|f"|'journal|f'|"$expected_entries") ;;
    *) fail committed_state 'committed-state tombstone contains an unexpected entry' ;;
  esac
  for entry in "$committed_state/$manifest_name" "$committed_state/journal"; do
    [[ -e "$entry" || -L "$entry" ]] || continue
    metadata="$(/usr/bin/stat -Lc '%u:%a:%h' "$entry" 2>/dev/null || true)"
    [[ -f "$entry" && ! -L "$entry" && "$metadata" == "$EUID:600:1" ]] ||
      fail committed_state 'committed-state tombstone file metadata is invalid'
  done
  if [[ -f "$committed_state/$manifest_name" ]]; then
    mapfile -t manifest_lines <"$committed_state/$manifest_name"
    [[ "${#manifest_lines[@]}" == 5 ]] || fail committed_state 'committed credential manifest shape is invalid'
    for entry in "${manifest_lines[@]}"; do
      [[ "$entry" =~ ^(runtime_db_password|migrator_db_password|backup_db_password|restore_db_password|pgbouncer_userlist)[.]cred\|[0-9a-f]{64}$ ]] ||
        fail committed_state 'committed credential manifest record is invalid'
    done
    [[ "${manifest_lines[0]%%|*}" == runtime_db_password.cred &&
       "${manifest_lines[1]%%|*}" == migrator_db_password.cred &&
       "${manifest_lines[2]%%|*}" == backup_db_password.cred &&
       "${manifest_lines[3]%%|*}" == restore_db_password.cred &&
       "${manifest_lines[4]%%|*}" == pgbouncer_userlist.cred ]] ||
      fail committed_state 'committed credential manifest order is invalid'
    /usr/bin/rm -f "$committed_state/$manifest_name"
    /usr/bin/sync -f "$committed_state"
  fi
  if [[ -f "$committed_state/journal" ]]; then
    journal_file_matches_run "$committed_state/journal" "$state_run_id" &&
      /usr/bin/grep -Fqx 'phase=committed' "$committed_state/journal" ||
      fail committed_state 'committed tombstone journal is invalid'
    /usr/bin/rm -f "$committed_state/journal"
    /usr/bin/sync -f "$committed_state"
  fi
  /usr/bin/rmdir "$committed_state"
  /usr/bin/sync -f "$state_parent"
}

finalize_committed_state() {
  local marker_state
  detect_maintenance_role
  [[ "$maintenance_role" == "$CLUSTER_ADMIN_ROLE" ]] || fail committed_state 'cluster admin split is absent after commit'
  require_pool_unit_contract
  if container_exists "$old_pool_rollback"; then
    stop_container_if_running "$old_pool_rollback" ||
      fail committed_state 'rollback PgBouncer container could not be stopped'
    set_container_restart_policy "$old_pool_rollback" no ||
      fail committed_state 'rollback PgBouncer restart ownership could not be fenced'
    /usr/bin/podman rm "$old_pool_rollback" >/dev/null
  fi
  container_exists "$OLD_POOL_CONTAINER" && fail committed_state 'old PgBouncer container reappeared after commit'
  [[ "$(/usr/bin/systemctl is-active "$POOL_UNIT" 2>/dev/null || true)" == active ]] ||
    /usr/bin/systemctl start "$POOL_UNIT"
  /usr/bin/systemctl enable "$POOL_UNIT" >/dev/null ||
    fail committed_state 'native PgBouncer unit could not be enabled'
  [[ "$(/usr/bin/systemctl is-enabled "$POOL_UNIT" 2>/dev/null || true)" == enabled &&
     -L "/etc/systemd/system/multi-user.target.wants/$POOL_UNIT" &&
     "$(/usr/bin/readlink -e -- "/etc/systemd/system/multi-user.target.wants/$POOL_UNIT" 2>/dev/null || true)" == \
       "$POOL_UNIT_INSTALLED" ]] ||
    fail committed_state 'native PgBouncer boot ownership differs from the installed unit'
  /usr/bin/sync -f /etc/systemd/system/multi-user.target.wants ||
    fail committed_state 'native PgBouncer enablement directory could not be synchronized'
  /usr/bin/sync -f /etc/systemd/system ||
    fail committed_state 'systemd unit root could not be synchronized after enablement'
  if [[ -e "$config_rollback" || -L "$config_rollback" ||
        -e "${config_rollback}.deleting-${run_id}" || -L "${config_rollback}.deleting-${run_id}" ]]; then
    remove_legacy_pool_tree "$config_rollback" ||
      fail committed_state 'legacy PgBouncer rollback tree is foreign or changed'
  fi
  [[ ! -e "$config_stage" && ! -L "$config_stage" &&
     ! -e "$credential_stage" && ! -L "$credential_stage" ]] ||
    fail committed_state 'a committed staging tree remains unexpectedly'
  /usr/bin/rm -f "$hba_backup" "$hba_backup_sha" "$ident_backup" "$ident_backup_sha" \
    "$legacy_pool_manifest" "$legacy_pool_ini_backup" "$legacy_pool_userlist_backup"
  if ! marker_state="$(postgres_psql --dbname=postgres --tuples-only --no-align \
    --set=marker_role="$marker_role" --set=run_id="$run_id" <<'SQL' 2>/dev/null
SELECT CASE
  WHEN count(*) = 0 THEN 'absent'
  WHEN count(*) = 1 AND bool_and(shobj_description(oid, 'pg_authid') = 'ascendany.v2.provision:' || :'run_id') THEN 'owned'
  ELSE 'foreign'
END
FROM pg_roles
WHERE rolname = :'marker_role';
SQL
)"; then
    fail committed_state 'provision marker catalog query failed'
  fi
  if [[ "$marker_state" == owned ]]; then
    postgres_psql --dbname=postgres --set=marker_role="$marker_role" >/dev/null <<'SQL'
SELECT format('DROP ROLE %I', :'marker_role')
\gexec
SQL
  elif [[ "$marker_state" != absent ]]; then
    fail committed_state 'provision marker role is foreign or changed'
  fi
  /usr/bin/rm -f "$RUNTIME_PASSWORD_FILE" "$MIGRATOR_PASSWORD_FILE" "$BACKUP_PASSWORD_FILE" "$RESTORE_PASSWORD_FILE"
  [[ ! -d "$INPUT_ROOT" ]] || /usr/bin/rmdir "$INPUT_ROOT"
  probe_legacy_database || fail committed_state 'old API DB-backed probe failed during committed cleanup'
  consume_committed_state_root "$STATE_ROOT" "$run_id" "$credential_manifest"
  pass committed
}

on_exit() {
  local status="$?"
  trap - EXIT
  if ((status != 0)) && [[ -f "$JOURNAL_PATH" && "$phase" != committed ]]; then
    recover_precommit || true
  fi
  exit "$status"
}

preflight() {
  local package_nevra expected_nevra expected_binary_sha expected_binary_size
  local cluster_contract legacy_contract legacy_security_contract fresh_contract legacy_ddl_contract network_contract
  local durability_settings retained_pool_paths retained_pool_containers package_verify_output package_verify_status=0

  for file in "$ROLE_BOOTSTRAP" "$POOL_CONFIG_SOURCE" "$POOL_HBA_SOURCE" \
    "$POSTGRES_HBA_BOOTSTRAP_SOURCE" "$POSTGRES_HBA_SOURCE" \
    "$POSTGRES_IDENT_BOOTSTRAP_SOURCE" "$POSTGRES_IDENT_SOURCE" \
    "$PACKAGE_LOCK" "$POOL_UNIT_SOURCE"; do
    require_release_file "$file"
  done

  /usr/bin/jq -e '
    .schema == "ascendany.fedora-runtime-packages.v1" and
    .fedoraRelease == 44 and .architecture == "x86_64" and
    (.packages.pgbouncer | keys == ["files", "nevra", "rpmSHA256", "signingFingerprint"]) and
    .packages.pgbouncer.nevra == "pgbouncer-1.25.2-1.fc44.x86_64" and
    .packages.pgbouncer.rpmSHA256 == "ad409c6bef77aba14288cd2464128eb5a151d75d7c28aa0b66451febb0d978c2" and
    .packages.pgbouncer.signingFingerprint == "36f612dcf27f7d1a48a835e4dbfcf71c6d9f90a6" and
    .packages.pgbouncer.files == [{
      group: "root", mode: "0755", owner: "root", path: "/usr/bin/pgbouncer",
      sha256: "42c722ab7352ccbb1eaba8dcc6d7fb9d28df11fbe1a73aa8b177c88dcd0bb318",
      size: 467960
    }]
  ' "$PACKAGE_LOCK" >/dev/null || fail package 'Fedora runtime package lock is invalid'
  expected_nevra="$(/usr/bin/jq -r '.packages.pgbouncer.nevra' "$PACKAGE_LOCK")"
  expected_binary_sha="$(/usr/bin/jq -r '.packages.pgbouncer.files[0].sha256' "$PACKAGE_LOCK")"
  expected_binary_size="$(/usr/bin/jq -r '.packages.pgbouncer.files[0].size' "$PACKAGE_LOCK")"
  package_nevra="$(/usr/bin/rpm -q --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' pgbouncer 2>/dev/null || true)"
  [[ "$package_nevra" == "$expected_nevra" ]] || fail package 'installed PgBouncer NEVRA differs from the signed package lock'
  [[ -x /usr/bin/pgbouncer && ! -L /usr/bin/pgbouncer &&
     "$(stat -Lc '%u:%g:%a:%s:%h' /usr/bin/pgbouncer)" == "0:0:755:$expected_binary_size:1" ]] ||
    fail package 'installed PgBouncer binary metadata differs from the signed RPM'
  [[ "$(/usr/bin/sha256sum /usr/bin/pgbouncer | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]] ||
    fail package 'installed PgBouncer binary hash differs from the signed RPM'
  package_verify_output="$(/usr/bin/rpm --verify pgbouncer 2>&1)" || package_verify_status=$?
  [[ "$package_verify_status" == 0 && -z "$package_verify_output" ]] ||
    fail package 'installed PgBouncer package manifest verification failed'
  /usr/bin/pgbouncer --version 2>&1 | /usr/bin/head -n 1 | /usr/bin/grep -qx 'PgBouncer 1.25.2' ||
    fail package 'installed PgBouncer runtime version is not exactly 1.25.2'

  [[ "$(/usr/bin/systemctl is-enabled "$PACKAGE_POOL_UNIT" 2>/dev/null || true)" == masked ]] ||
    fail systemd 'package pgbouncer.service must be masked'
  [[ "$(/usr/bin/systemctl is-active "$PACKAGE_POOL_UNIT" 2>/dev/null || true)" == inactive ]] ||
    fail systemd 'package pgbouncer.service must be inactive'
  [[ "$(/usr/bin/systemctl is-enabled "$POOL_UNIT" 2>/dev/null || true)" == disabled ]] ||
    fail systemd 'release-owned PgBouncer unit must be disabled before provisioning'
  [[ "$(/usr/bin/systemctl is-active "$POOL_UNIT" 2>/dev/null || true)" == inactive ]] ||
    fail systemd 'release-owned PgBouncer unit must be inactive before provisioning'
  require_pool_unit_contract

  container_exists "$POSTGRES_CONTAINER" || fail postgres 'PostgreSQL container is missing'
  [[ "$(/usr/bin/podman inspect --format '{{.State.Running}}' "$POSTGRES_CONTAINER")" == true ]] ||
    fail postgres 'PostgreSQL container is inactive'
  postgres_image_id="$(/usr/bin/podman inspect --format '{{.Image}}' "$POSTGRES_CONTAINER")"
  network_contract="$(/usr/bin/podman inspect "$POSTGRES_CONTAINER" |
    /usr/bin/jq -r --arg network "$POSTGRES_NETWORK" --arg gateway "$POSTGRES_GATEWAY" --arg address "$POSTGRES_ADDRESS" '
      if type == "array" and length == 1 and
         (.[0].NetworkSettings.Networks | keys) == [$network] and
         .[0].NetworkSettings.Networks[$network].Gateway == $gateway and
         .[0].NetworkSettings.Networks[$network].IPAddress == $address and
         .[0].NetworkSettings.Networks[$network].IPPrefixLen == 16
      then "exact" else "" end
    ')"
  [[ "$network_contract" == exact ]] ||
    fail postgres 'PostgreSQL container attachment differs from the native service network contract'
  /usr/bin/podman network inspect "$POSTGRES_NETWORK" |
    /usr/bin/jq -e --arg network "$POSTGRES_NETWORK" --arg gateway "$POSTGRES_GATEWAY" --arg subnet "$POSTGRES_SUBNET" '
      type == "array" and length == 1 and
      .[0].name == $network and
      .[0].driver == "bridge" and
      .[0].network_interface == "podman0" and
      .[0].internal == false and
      .[0].ipv6_enabled == false and
      .[0].subnets == [{"subnet": $subnet, "gateway": $gateway}]
    ' >/dev/null || fail postgres 'Podman bridge differs from the native service network contract'

  detect_maintenance_role
  [[ "$(postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' \
    --command="SELECT current_setting('server_version_num')::int / 10000, current_setting('password_encryption'), current_setting('hba_file'), current_setting('ident_file'), current_setting('log_statement'), current_setting('log_min_duration_statement'), current_setting('log_duration')")" == \
    "17|scram-sha-256|$POSTGRES_HBA_PATH|$POSTGRES_IDENT_PATH|none|-1|off" ]] ||
    fail postgres 'PostgreSQL 17/SCRAM/access-file/statement-log contract differs'
  durability_settings="$(postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' \
    --command="SELECT current_setting('fsync'), current_setting('synchronous_commit'), current_setting('full_page_writes')")" ||
    fail postgres 'failed to read the PostgreSQL durability settings'
  postgres_durability_settings_are_enabled "$durability_settings" ||
    fail postgres 'PostgreSQL fsync/synchronous_commit/full_page_writes must all be on'
  cluster_contract="$(postgres_psql --dbname="$LEGACY_DATABASE" --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT (SELECT string_agg(rolname, ',' ORDER BY rolname) FROM pg_roles WHERE rolname !~ '^pg_'),
       (SELECT string_agg(datname, ',' ORDER BY datname) FROM pg_database),
       (SELECT string_agg(nspname, ',' ORDER BY nspname)
        FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname <> 'information_schema'),
       (SELECT count(*) FROM pg_db_role_setting),
       (SELECT count(*) FROM pg_replication_slots);
SQL
)"
  if [[ "$maintenance_role" == "$LEGACY_ROLE" ]]; then
    [[ "$cluster_contract" == 'AscendAny|AscendAny,postgres,template0,template1|ascendany,pgbouncer,public|0|0' ]] ||
      fail postgres 'PostgreSQL cluster entry shape differs from the reviewed bootstrap state'
  else
    [[ "$cluster_contract" == 'AscendAny,ascendany_cluster_admin|AscendAny,postgres,template0,template1|ascendany,public|0|0' ]] ||
      fail postgres 'PostgreSQL cluster entry shape differs from the reviewed split state'
  fi

  legacy_security_contract="$(postgres_psql --dbname="$LEGACY_DATABASE" --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT (SELECT count(*) FROM pg_namespace WHERE nspname = 'pgbouncer'),
       (SELECT count(*) FROM pg_proc WHERE pronamespace = to_regnamespace('pgbouncer')),
       (SELECT count(*) FROM pg_class WHERE relnamespace = to_regnamespace('pgbouncer')),
       (SELECT count(*) FROM pg_type WHERE typnamespace = to_regnamespace('pgbouncer') AND typrelid = 0),
       EXISTS (
         SELECT 1
         FROM pg_namespace AS namespace
         JOIN pg_proc AS routine ON routine.pronamespace = namespace.oid
         JOIN pg_language AS language ON language.oid = routine.prolang
         WHERE namespace.nspname = 'pgbouncer'
           AND namespace.nspowner = (SELECT oid FROM pg_roles WHERE rolname = 'AscendAny')
           AND namespace.nspacl IS NULL
           AND routine.proname = 'user_lookup'
           AND routine.proowner = namespace.nspowner
           AND routine.prosecdef AND NOT routine.proleakproof
           AND routine.provolatile = 'v' AND routine.proparallel = 'u'
           AND routine.proacl IS NULL
           AND routine.proconfig = ARRAY['search_path=pg_catalog']::text[]
           AND language.lanname = 'sql' AND routine.prokind = 'f'
           AND routine.pronargs = 1 AND routine.pronargdefaults = 0
           AND NOT routine.proretset AND NOT routine.proisstrict
           AND pg_get_function_identity_arguments(routine.oid) = 'i_username text, OUT uname text, OUT phash text'
           AND pg_get_function_result(routine.oid) = 'record'
           AND md5(pg_get_functiondef(routine.oid)) = '67abf4db5e9ae716417e7ba447f2e466'
       ),
       (SELECT count(*)
        FROM pg_proc AS routine
        JOIN pg_namespace AS namespace ON namespace.oid = routine.pronamespace
        WHERE namespace.nspname !~ '^pg_' AND namespace.nspname <> 'information_schema');
SQL
)"
  if [[ "$maintenance_role" == "$LEGACY_ROLE" ]]; then
    [[ "$legacy_security_contract" == '1|1|0|0|t|1' ]] ||
      fail postgres 'legacy PgBouncer definer function differs from the exact removable entry shape'
  else
    [[ "$legacy_security_contract" == '0|0|0|0|f|0' ]] ||
      fail postgres 'obsolete legacy PgBouncer schema or an ascendany routine remains after the role split'
  fi

  legacy_contract="$(postgres_psql --dbname="$LEGACY_DATABASE" --tuples-only --no-align <<'SQL'
SELECT CASE
  WHEN EXISTS (
    SELECT 1 FROM pg_authid AS legacy
    WHERE legacy.oid = 10 AND legacy.rolname = 'AscendAny'
      AND legacy.rolcanlogin AND legacy.rolsuper AND legacy.rolinherit
      AND legacy.rolcreatedb AND legacy.rolcreaterole
      AND legacy.rolreplication AND legacy.rolbypassrls
      AND legacy.rolconnlimit = -1
      AND (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = legacy.oid) IS NULL
      AND legacy.rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'
      AND shobj_description(legacy.oid, 'pg_authid') IS NULL
  )
  AND NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_cluster_admin')
  AND pg_get_userbyid((SELECT datdba FROM pg_database WHERE datname = 'AscendAny')) = 'AscendAny'
  AND pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = 'ascendany.import_tasks'::regclass)) = 'AscendAny'
  AND pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = 'ascendany.import_task_events'::regclass)) = 'AscendAny'
  THEN 'bootstrap'
  WHEN EXISTS (
    SELECT 1 FROM pg_authid AS admin
    WHERE admin.oid = 10 AND admin.rolname = 'ascendany_cluster_admin'
      AND admin.rolcanlogin AND admin.rolsuper AND NOT admin.rolinherit
      AND NOT admin.rolcreatedb AND NOT admin.rolcreaterole
      AND NOT admin.rolreplication AND NOT admin.rolbypassrls
      AND admin.rolconnlimit = -1
      AND (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = admin.oid) IS NULL
      AND admin.rolpassword IS NULL
      AND shobj_description(admin.oid, 'pg_authid') = 'ascendany.cluster.bootstrap.v2'
  )
  AND EXISTS (
    SELECT 1 FROM pg_authid AS legacy
    WHERE legacy.oid <> 10 AND legacy.rolname = 'AscendAny'
      AND legacy.rolcanlogin AND NOT legacy.rolsuper AND legacy.rolinherit
      AND NOT legacy.rolcreatedb AND NOT legacy.rolcreaterole
      AND NOT legacy.rolreplication AND NOT legacy.rolbypassrls
      AND legacy.rolconnlimit = -1
      AND (SELECT config.rolconfig FROM pg_roles AS config WHERE config.oid = legacy.oid) IS NULL
      AND legacy.rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$'
      AND shobj_description(legacy.oid, 'pg_authid') = 'ascendany.legacy.runtime.v2'
  )
  AND pg_get_userbyid((SELECT datdba FROM pg_database WHERE datname = 'AscendAny')) = 'ascendany_cluster_admin'
  AND pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = 'ascendany.import_tasks'::regclass)) = 'AscendAny'
  AND pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = 'ascendany.import_task_events'::regclass)) = 'AscendAny'
  AND NOT EXISTS (
    SELECT 1 FROM pg_auth_members AS edge
    WHERE edge.roleid IN (SELECT oid FROM pg_roles WHERE rolname IN ('AscendAny', 'ascendany_cluster_admin'))
       OR edge.member IN (SELECT oid FROM pg_roles WHERE rolname IN ('AscendAny', 'ascendany_cluster_admin'))
  )
  THEN 'split'
  ELSE 'invalid'
END;
SQL
)"
  [[ "$legacy_contract" == bootstrap || "$legacy_contract" == split ]] ||
    fail postgres 'legacy bootstrap role is not the exact expected entry state'
  entry_role_state="$legacy_contract"
  if [[ "$entry_role_state" == bootstrap ]]; then
    [[ "$(postgres_psql --dbname=postgres --tuples-only --no-align --command="SELECT count(*) FROM pg_roles WHERE rolname = '$splitter_role'")" == 0 ]] ||
      fail postgres 'splitter role already exists without a journal'
    require_original_access_rules
  else
    [[ "$maintenance_role" == "$CLUSTER_ADMIN_ROLE" ]] || fail postgres 'split role state lacks its peer maintenance channel'
    require_final_hba_rules
  fi

  fresh_contract="$(postgres_psql --dbname=postgres --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT count(*) FILTER (WHERE datname = 'ascendany_v2'),
       (SELECT count(*) FROM pg_roles WHERE rolname LIKE 'ascendany_v2_marker_%' OR rolname = ANY(ARRAY[
         'ascendany_database_owner', 'ascendany_owner', 'ascendany_runtime',
         'ascendany_migrator', 'ascendany_backup', 'ascendanyd_login',
         'ascendany_migrator_login', 'ascendany_backup_login', 'ascendany_restore_login'
       ]))
FROM pg_database;
SQL
)"
  [[ "$fresh_contract" == '0|0' ]] || fail postgres 'v2 database or managed role state is not fresh'

  retained_pool_paths="$(/usr/bin/find "$POOL_PARENT" -mindepth 1 -maxdepth 1 \
    \( -name '.pgbouncer.stage.*' -o -name '.pgbouncer.rollback.*' \
       -o -name 'pgbouncer.deleting-*' \) -printf '%f\n' | LC_ALL=C /usr/bin/sort)"
  [[ -z "$retained_pool_paths" ]] ||
    fail old_pool 'a prior PgBouncer stage, rollback, or deletion generation remains'
  retained_pool_containers="$(/usr/bin/podman ps -a --format '{{.Names}}' |
    /usr/bin/grep -E '^ascendany-pgbouncer-rollback-' || true)"
  [[ -z "$retained_pool_containers" ]] ||
    fail old_pool 'a prior PgBouncer rollback container remains'
  legacy_ddl_contract="$(postgres_psql --dbname="$LEGACY_DATABASE" --tuples-only --no-align --field-separator='|' <<'SQL'
SELECT to_regclass('ascendany.import_tasks') IS NOT NULL,
       to_regclass('ascendany.import_tasks_created_at_idx') IS NOT NULL,
       to_regclass('ascendany.import_task_events') IS NOT NULL,
       to_regclass('ascendany.import_task_events_run_id_event_id_idx') IS NOT NULL,
       pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid='ascendany.import_tasks'::regclass)),
       pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid='ascendany.import_task_events'::regclass));
SQL
)"
  [[ "$legacy_ddl_contract" == 't|t|t|t|AscendAny|AscendAny' ]] ||
    fail postgres 'legacy owner-only DDL objects do not match the reviewed split contract'

  container_exists "$OLD_POOL_CONTAINER" || fail old_pool 'old PgBouncer container is missing'
  capture_legacy_pool_mount_label ||
    fail old_pool 'old PgBouncer mount-label ownership is missing or ambiguous'
  [[ "$(/usr/bin/podman inspect --format '{{.State.Running}}' "$OLD_POOL_CONTAINER")" == true ]] ||
    fail old_pool 'old PgBouncer container is inactive'
  [[ "$(container_restart_policy "$OLD_POOL_CONTAINER")" == always ]] ||
    fail old_pool 'old PgBouncer container restart policy differs from the reviewed entry state'
  [[ -d "$POOL_PARENT" && ! -L "$POOL_PARENT" && "$(stat -Lc '%u:%g:%a' "$POOL_PARENT")" == 0:0:700 ]] ||
    fail old_pool 'old PgBouncer parent directory is not protected'
  legacy_pool_structure_matches "$POOL_CONFIG_ROOT" legacy ||
    fail old_pool 'old PgBouncer config tree metadata or SELinux label differs from the reviewed entry state'
  require_legacy_pool_semantics
  require_legacy_api_unit_contract
  legacy_api_runtime_matches 0 || fail legacy_probe 'old API runtime ownership differs from the reviewed entry state'
  probe_legacy_database || fail legacy_probe 'old API DB-backed preflight failed'

  [[ -d /etc/ascendany && ! -L /etc/ascendany ]] || fail credential 'credential parent is invalid'
  require_protected_ancestry "$CREDENTIAL_ROOT/pending"
  [[ -d "$CREDENTIAL_ROOT" && ! -L "$CREDENTIAL_ROOT" &&
     "$(stat -Lc '%u:%g:%a' "$CREDENTIAL_ROOT")" == 0:0:700 ]] ||
    fail credential 'credential root identity or mode differs'
  for output in runtime_db_password.cred migrator_db_password.cred backup_db_password.cred restore_db_password.cred pgbouncer_userlist.cred; do
    [[ ! -e "$CREDENTIAL_ROOT/$output" && ! -L "$CREDENTIAL_ROOT/$output" ]] || fail credential "credential output already exists: $output"
  done

  [[ -d "$INPUT_ROOT" && ! -L "$INPUT_ROOT" && "$(stat -Lc '%u:%g:%a' "$INPUT_ROOT")" == 0:0:700 ]] ||
    fail credential 'password input root must be root:root mode 0700'
  [[ "$(find "$INPUT_ROOT" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)" == \
     $'backup_db_password\nmigrator_db_password\nrestore_db_password\nruntime_db_password' ]] ||
    fail credential 'password input root contains an unexpected entry'
  require_password_file "$RUNTIME_PASSWORD_FILE"
  require_password_file "$MIGRATOR_PASSWORD_FILE"
  require_password_file "$BACKUP_PASSWORD_FILE"
  require_password_file "$RESTORE_PASSWORD_FILE"
  require_distinct_passwords "$RUNTIME_PASSWORD_FILE" "$MIGRATOR_PASSWORD_FILE" "$BACKUP_PASSWORD_FILE" "$RESTORE_PASSWORD_FILE"
}

main() {
  local terminal_state terminal_name terminal_run_id state_parent state_name recovery_legacy_pool_path
  local -a retained_tombstones=()
  if [[ "$#" == 1 && "$1" == --help ]]; then usage; exit 0; fi
  require_exact_args "$@"
  ((EUID == 0)) || fail identity 'provisioning must run as root'
  for command in podman systemd-creds systemctl systemd-run rpm jq curl ss sync sha256sum md5sum sed chcon; do
    command -v "$command" >/dev/null 2>&1 || fail dependency "required executable is missing: $command"
  done

  enter_provision_unit_boundary "$@"

  consume_initializing_state_roots
  state_parent="$(/usr/bin/dirname "$STATE_ROOT")"
  state_name="$(/usr/bin/basename "$STATE_ROOT")"
  mapfile -t retained_tombstones < <(/usr/bin/find "$state_parent" \
    -mindepth 1 -maxdepth 1 \
    \( -name "$(/usr/bin/basename "$STATE_ROOT").committed-*" \
       -o -name "$(/usr/bin/basename "$STATE_ROOT").recovered-*" \) \
    -print | LC_ALL=C /usr/bin/sort)
  ((${#retained_tombstones[@]} <= 1)) ||
    fail journal 'multiple terminal provisioning states coexist'
  if ((${#retained_tombstones[@]} == 1)); then
    terminal_state="${retained_tombstones[0]}"
    terminal_name="$(/usr/bin/basename "$terminal_state")"
    [[ ! -e "$STATE_ROOT" && ! -L "$STATE_ROOT" ]] ||
      fail journal 'active and terminal provisioning state coexist'
    case "$terminal_name" in
      "${state_name}.committed-"*)
        terminal_run_id="${terminal_name#${state_name}.committed-}"
        [[ "$terminal_run_id" =~ ^[0-9a-f]{32}$ ]] || fail journal 'committed tombstone run id is invalid'
        consume_committed_state_root "$STATE_ROOT" "$terminal_run_id" \
          "$STATE_ROOT/credentials-${terminal_run_id}.manifest"
        pass committed
        exit 0
        ;;
      "${state_name}.recovered-"*)
        terminal_run_id="${terminal_name#${state_name}.recovered-}"
        [[ "$terminal_run_id" =~ ^[0-9a-f]{32}$ ]] || fail journal 'recovered tombstone run id is invalid'
        consume_recovered_state_root "$terminal_run_id" ||
          fail journal 'recovered tombstone cleanup is incomplete'
        printf 'Provisioning recovery completed; invoke the exact command again.\n' >&2
        exit 2
        ;;
      *) fail journal 'terminal provisioning state name is invalid' ;;
    esac
  fi

  if [[ -e "$JOURNAL_PATH" || -L "$JOURNAL_PATH" ]]; then
    load_journal
    if [[ "$phase" == committed ]]; then
      finalize_committed_state
      exit 0
    fi
    capture_legacy_pool_mount_label ||
      fail old_pool 'recovery PgBouncer mount-label ownership is missing or ambiguous'
    recovery_legacy_pool_path="$POOL_CONFIG_ROOT"
    if [[ -e "$config_rollback" || -L "$config_rollback" ]]; then
      [[ -d "$config_rollback" && ! -L "$config_rollback" &&
         "$config_rollback" == "$(/usr/bin/realpath -e -- "$config_rollback" 2>/dev/null || true)" ]] ||
        fail old_pool 'recovery PgBouncer rollback configuration path is invalid'
      recovery_legacy_pool_path="$config_rollback"
    fi
    require_legacy_pool_semantics "$recovery_legacy_pool_path"
    require_legacy_api_unit_contract
    recover_precommit || exit 1
    printf 'Provisioning recovery completed; invoke the exact command again.\n' >&2
    exit 2
  fi
  [[ ! -e "$STATE_ROOT" && ! -L "$STATE_ROOT" ]] || fail journal 'state root exists without a journal'

  run_id="$(/usr/bin/tr -d '-' </proc/sys/kernel/random/uuid)"
  derive_run_paths
  preflight
  pass preflight

  initialize_state_root
  trap on_exit EXIT
  maintenance_role="$LEGACY_ROLE"
  capture_legacy_pool_manifest
  backup_original_access

  /usr/bin/install -d -o root -g root -m 0700 "$credential_stage"
  encrypt_credential db_password "$RUNTIME_PASSWORD_FILE" runtime_db_password.cred
  encrypt_credential migrator_db_password "$MIGRATOR_PASSWORD_FILE" migrator_db_password.cred
  encrypt_credential backup_db_password "$BACKUP_PASSWORD_FILE" backup_db_password.cred
  encrypt_credential restore_db_password "$RESTORE_PASSWORD_FILE" restore_db_password.cred
  /usr/bin/install -d -o root -g root -m 0755 "$config_stage"
  /usr/bin/install -o root -g root -m 0644 "$POOL_CONFIG_SOURCE" "$config_stage/.pgbouncer.ini.tmp"
  /usr/bin/sync -d "$config_stage/.pgbouncer.ini.tmp"
  /usr/bin/mv "$config_stage/.pgbouncer.ini.tmp" "$config_stage/pgbouncer.ini"
  /usr/bin/sync -f "$config_stage"
  /usr/bin/install -o root -g root -m 0644 "$POOL_HBA_SOURCE" "$config_stage/.pgbouncer-hba.conf.tmp"
  /usr/bin/sync -d "$config_stage/.pgbouncer-hba.conf.tmp"
  /usr/bin/mv "$config_stage/.pgbouncer-hba.conf.tmp" "$config_stage/pgbouncer-hba.conf"
  /usr/bin/sync -d "$config_stage/pgbouncer.ini" "$config_stage/pgbouncer-hba.conf"
  /usr/bin/sync -f "$config_stage"

  if [[ "$entry_role_state" == bootstrap ]]; then
    install_postgres_access bootstrap "$POSTGRES_HBA_BOOTSTRAP_SOURCE" "$POSTGRES_IDENT_BOOTSTRAP_SOURCE" ||
      fail postgres_access 'failed to publish the bootstrap PostgreSQL HBA/ident generation'
    require_bootstrap_hba_rules
    probe_legacy_database || fail postgres_access 'old API could not reconnect after closing PostgreSQL trust routes'
    write_journal bootstrap_access

    verify_legacy_pool_reconnect_before_split
    quiesce_legacy_pool
    split_legacy_role
    verify_legacy_split
    install_postgres_access final "$POSTGRES_HBA_SOURCE" "$POSTGRES_IDENT_SOURCE" ||
      fail postgres_access 'failed to publish the final PostgreSQL HBA/ident generation'
    require_final_hba_rules
    write_journal legacy_split
    restart_legacy_pool_after_split
    pass legacy_split
  else
    maintenance_role="$CLUSTER_ADMIN_ROLE"
    require_final_hba_rules
    write_journal legacy_split
    pass legacy_split_already_complete
  fi

  create_v2_database_and_roles
  verify_v2_ownership
  generate_pool_userlist_credential
  build_credential_manifest
  write_journal v2_database
  pass v2_database

  publish_credentials
  write_journal credentials_published
  switch_to_native_pool
  pass native_pool

  write_journal committed
  trap - EXIT
  finalize_committed_state
}

main "$@"
