#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly VALIDATOR="$REPOSITORY_ROOT/deploy/v2/scripts/validate-production.sh"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-generation-validator.XXXXXX")"
readonly REAL_STAT="$(command -v stat)"
trap 'rm -rf -- "$WORK_ROOT"' EXIT

(
  # shellcheck source=../../deploy/v2/scripts/validate-production.sh
  source "$VALIDATOR"
  trap - EXIT
  trap 'printf "generation fixture failed at line %s\n" "$LINENO" >&2' ERR
  pass() { :; }
  fail() { failures=$((failures + 1)); }

  fresh_database_snapshot() {
    local table sequence count
    while IFS= read -r table; do
      case "$table" in
        schema_migrations_v2) count=10 ;;
        achievement_rule_sets|achievement_rule_head|analytics_head) count=1 ;;
        achievement_rules) count=17 ;;
        *) count=0 ;;
      esac
      printf 'table:%s|%s\n' "$table" "$count"
    done < <(expected_initial_table_names)
    while IFS= read -r sequence; do
      if [[ "$sequence" == achievement_rule_sets_achievement_rule_set_id_seq ]]; then
        printf 'sequence:%s|1|true\n' "$sequence"
      else
        printf 'sequence:%s|1|false\n' "$sequence"
      fi
    done < <(expected_initial_sequence_names)
    expected_initial_migration_rows
    printf 'analytics-head|true||0\n'
    expected_initial_achievement_rows
  }

  deployment_transition=initial
  validation_phase=staged
  fixture_database_snapshot="$(fresh_database_snapshot)"
  initial_database_state_snapshot() { printf '%s\n' "$fixture_database_snapshot"; }

  failures=0
  check_initial_database_state
  [[ "$failures" == 0 ]]

  canonical_database_snapshot="$fixture_database_snapshot"
  fixture_database_snapshot="${canonical_database_snapshot/table:auth_accounts|0/table:auth_accounts|1}"
  failures=0
  check_initial_database_state
  [[ "$failures" -ge 1 ]]

  fixture_database_snapshot="$(grep -v '^sequence:recommendation_model_release_ids_seq|' <<<"$canonical_database_snapshot")"
  failures=0
  check_initial_database_state
  [[ "$failures" -ge 1 ]]

  fixture_database_snapshot="${canonical_database_snapshot/0cffdb00acefd37c049a654bad76d8fac79727ed7c54cc3fa9234d54964ce0cf/ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff}"
  failures=0
  check_initial_database_state
  [[ "$failures" -ge 1 ]]

  fixture_database_snapshot="${canonical_database_snapshot/analytics-head|true||0/analytics-head|true|1|1}"
  failures=0
  check_initial_database_state
  [[ "$failures" -ge 1 ]]

  fixture_database_snapshot="${canonical_database_snapshot/accuracy_max|准确进阶/accuracy_max|被篡改}"
  failures=0
  check_initial_database_state
  [[ "$failures" -ge 1 ]]

  fixture_database_snapshot='malformed retained state'
  deployment_transition=forward
  failures=0
  check_initial_database_state
  [[ "$failures" == 0 ]]
  deployment_transition=initial
  validation_phase=activation
  fixture_database_snapshot="${canonical_database_snapshot/table:recommendation_model_activation_events|0/table:recommendation_model_activation_events|1}"
  fixture_database_snapshot="${fixture_database_snapshot/table:recommendation_model_head|0/table:recommendation_model_head|1}"
  fixture_database_snapshot="${fixture_database_snapshot/table:recommendation_model_releases|0/table:recommendation_model_releases|1}"
  fixture_database_snapshot="${fixture_database_snapshot/sequence:recommendation_model_release_ids_seq|1|false/sequence:recommendation_model_release_ids_seq|1|true}"
  failures=0
  check_initial_database_state
  [[ "$failures" == 0 ]]
  fixture_database_snapshot="${fixture_database_snapshot/table:recommendation_model_releases|1/table:recommendation_model_releases|2}"
  failures=0
  check_initial_database_state
  [[ "$failures" -ge 1 ]]

  validation_phase=smoke
  fixture_database_snapshot="$canonical_database_snapshot"
  failures=0
  check_initial_database_state
  [[ "$failures" == 0 ]]

  artifact_root="$WORK_ROOT/durable/artifacts"
  catalog_publisher_state_root="$WORK_ROOT/durable/catalog-publisher"
  catalog_receipt_root="$catalog_publisher_state_root/receipts"
  catalog_publisher_pending_root="$catalog_publisher_state_root/pending"
  catalog_publication_request_source="$catalog_publisher_pending_root/catalog_publication_request.cred"
  catalog_publication_access_token_source="$catalog_publisher_pending_root/admin_access_token.cred"
  backup_root="$WORK_ROOT/durable/backups"
  acceptance_root="$WORK_ROOT/durable/acceptance"
  restore_evidence="$acceptance_root/restore-verify.json"
  install -d -m 0750 \
    "$artifact_root/sha256" "$artifact_root/incoming" "$artifact_root/.locks" \
    "$backup_root"
  install -d -m 0750 "$catalog_publisher_state_root" "$catalog_receipt_root"
  install -d -m 0700 "$catalog_publisher_pending_root"
  install -d -m 0700 "$acceptance_root"

  stat() {
    local format="${2-}" path="${!#}"
    case "$format|$path" in
      '%U:%G:%a|'"$backup_root") printf '%s\n' 'ascendany-backup:ascendany-backup-readers:750' ;;
      '%U:%G:%a|'"$catalog_publisher_state_root") printf '%s\n' 'ascendany-catalog-publisher:ascendany-catalog-readers:750' ;;
      '%U:%G:%a|'"$catalog_receipt_root") printf '%s\n' 'ascendany-catalog-publisher:ascendany-catalog-readers:750' ;;
      '%U:%G:%a|'"$catalog_publisher_pending_root") printf '%s\n' 'root:root:700' ;;
      '%U:%G:%a|'"$acceptance_root") printf '%s\n' 'root:root:700' ;;
      *) "$REAL_STAT" "$@" ;;
    esac
  }

  failures=0
  check_catalog_publisher_state_root
  [[ "$failures" == 0 ]]

  : >"$catalog_publisher_state_root/unexpected"
  failures=0
  check_catalog_publisher_state_root
  [[ "$failures" -ge 1 ]]
  rm -- "$catalog_publisher_state_root/unexpected"

  failures=0
  check_initial_empty_durable_state
  [[ "$failures" == 0 ]]

  validation_phase=activation
  failures=0
  check_initial_empty_durable_state
  [[ "$failures" == 0 ]]
  validation_phase=smoke

  : >"$artifact_root/sha256/unexpected"
  failures=0
  check_initial_empty_durable_state
  [[ "$failures" -ge 1 ]]
  rm -- "$artifact_root/sha256/unexpected"

  : >"$catalog_receipt_root/1.json"
  failures=0
  check_initial_empty_durable_state
  [[ "$failures" -ge 1 ]]
  rm -- "$catalog_receipt_root/1.json"

  mkdir "$backup_root/backup-old-generation"
  failures=0
  check_initial_empty_durable_state
  [[ "$failures" -ge 1 ]]
  rmdir "$backup_root/backup-old-generation"

  : >"$restore_evidence"
  failures=0
  check_initial_empty_durable_state
  [[ "$failures" -ge 1 ]]
  rm -- "$restore_evidence"

  deployment_transition=forward
  : >"$artifact_root/sha256/retained"
  failures=0
  check_initial_empty_durable_state
  [[ "$failures" == 0 ]]
  rm -- "$artifact_root/sha256/retained"

  deployment_transition=initial
  validation_phase=staged
  production_namespace_root="$WORK_ROOT/host/opt/ascendany"
  configuration_namespace_root="$WORK_ROOT/host/etc/ascendany"
  catalog_publisher_config_root="$WORK_ROOT/host/etc/ascendany-catalog-publisher"
  systemd_system_root="$WORK_ROOT/host/etc/systemd/system"
  systemd_runtime_root="$WORK_ROOT/host/run/systemd/system"
  systemd_local_root="$WORK_ROOT/host/usr/local/lib/systemd/system"
  systemd_vendor_root="$WORK_ROOT/host/usr/lib/systemd/system"
  retired_trainer_runtime_root="$WORK_ROOT/host/opt/ascendany-trainer-runtime"
  retired_trainer_state_root="$WORK_ROOT/host/var/lib/ascendany-trainer"
  retired_trainer_log_root="$WORK_ROOT/host/var/log/ascendany-trainer"
  retired_process_root="$WORK_ROOT/host/proc"
  pgbouncer_config_root="$production_namespace_root/infra/pgbouncer"
  runtime_provider_credential_ids=()

  install -d -m 0755 \
    "$production_namespace_root/v2" \
    "$production_namespace_root/infra/pgbouncer" \
    "$configuration_namespace_root" \
    "$systemd_system_root" "$systemd_runtime_root" "$systemd_local_root" "$systemd_vendor_root" \
    "$retired_process_root"
  install -d -m 0750 "$configuration_namespace_root/v2"
  install -d -m 0700 "$configuration_namespace_root/credentials"
  install -d -m 0750 "$catalog_publisher_config_root"
  install -d -m 0700 "$catalog_publisher_config_root/credentials"
  install -m 0600 /dev/null "$production_namespace_root/.install-v2-release.lock"
  for config in \
    analytics.json ascendanyd-read-only-smoke.env ascendanyd.env backup.env \
    judge.env migrate.env restore.env; do
    : >"$configuration_namespace_root/v2/$config"
  done
  for credential in \
    backup_db_password.cred cloudflare_tunnel_credentials.cred jwt_signing_private_key.cred \
    jwt_verification_public_key.cred \
    migrator_db_password.cred password_pepper.cred pgbouncer_userlist.cred \
    restore_db_password.cred runtime_db_password.cred; do
    : >"$configuration_namespace_root/credentials/$credential"
  done
  install -m 0640 /dev/null "$catalog_publisher_config_root/catalog-publish.env"
  for credential in catalog_publisher_db_password.cred; do
    printf '%s\n' fixture >"$catalog_publisher_config_root/credentials/$credential"
    chmod 0400 "$catalog_publisher_config_root/credentials/$credential"
  done
  ln -s /dev/null "$systemd_system_root/$retired_api_unit"
  ln -s /dev/null "$systemd_system_root/$retired_trainer_unit"

  stat() {
    local format="${2-}" path="${!#}"
    case "$format|$path" in
      '%U:%G:%a|'"$production_namespace_root"|\
      '%U:%G:%a|'"$production_namespace_root/infra"|\
      '%U:%G:%a|'"$configuration_namespace_root") printf '%s\n' 'root:root:755' ;;
      '%U:%G:%a|'"$configuration_namespace_root/v2") printf '%s\n' 'root:ascendany-runtime:750' ;;
      '%U:%G:%a|'"$configuration_namespace_root/credentials") printf '%s\n' 'root:root:700' ;;
      '%U:%G:%a|'"$catalog_publisher_config_root") printf '%s\n' 'root:ascendany-catalog-publisher:750' ;;
      '%U:%G:%a|'"$catalog_publisher_config_root/credentials") printf '%s\n' 'root:root:700' ;;
      '%U:%G:%a:%h|'"$catalog_publisher_config_root/catalog-publish.env") printf '%s\n' 'root:ascendany-catalog-publisher:640:1' ;;
      '%U:%G:%a:%h|'"$catalog_publisher_config_root/credentials/catalog_publisher_db_password.cred") printf '%s\n' 'root:root:400:1' ;;
      '%U:%G:%a:%h|'"$production_namespace_root/.install-v2-release.lock") printf '%s\n' 'root:root:600:1' ;;
      '%U:%G|'"$systemd_system_root/$retired_api_unit"|\
      '%U:%G|'"$systemd_system_root/$retired_trainer_unit") printf '%s\n' 'root:root' ;;
      *) "$REAL_STAT" "$@" ;;
    esac
  }

  fixture_passwd=$'root:x:0:0:root:/root:/bin/bash\nascendany:x:974:974::/var/lib/ascendany:/usr/sbin/nologin'
  fixture_group=$'root:x:0:\nascendany:x:974:'
  fixture_containers='ascendany-postgres'
  systemctl() {
    case "$1" in
      is-enabled) printf '%s\n' masked ;;
      is-active) printf '%s\n' inactive ;;
      *) return 1 ;;
    esac
  }
  unit_property() {
    case "$2" in
      MainPID) printf '%s\n' 0 ;;
      LoadState) printf '%s\n' masked ;;
      *) return 1 ;;
    esac
  }
  getent() {
    case "$1" in
      passwd) printf '%s\n' "$fixture_passwd" ;;
      group) printf '%s\n' "$fixture_group" ;;
      *) return 2 ;;
    esac
  }
  podman() {
    [[ "$1" == ps ]]
    printf '%s\n' "$fixture_containers"
  }

  failures=0
  check_retired_generation_closure
  [[ "$failures" == 0 ]]

  : >"$configuration_namespace_root/credentials/trainer_agent_rtx_01.cred"
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  rm -- "$configuration_namespace_root/credentials/trainer_agent_rtx_01.cred"

  rm -- "$systemd_system_root/$retired_trainer_unit"
  : >"$systemd_system_root/$retired_trainer_unit"
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  rm -- "$systemd_system_root/$retired_trainer_unit"
  ln -s /dev/null "$systemd_system_root/$retired_trainer_unit"

  mkdir -p "$retired_trainer_state_root"
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  rmdir "$retired_trainer_state_root"

  fixture_passwd+=$'\nascendany-trainer:x:971:971::/var/lib/ascendany-trainer:/usr/sbin/nologin'
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  fixture_passwd="${fixture_passwd%$'\nascendany-trainer:x:971:971::/var/lib/ascendany-trainer:/usr/sbin/nologin'}"

  fixture_containers=$'ascendany-postgres\nascendany-cloudflared'
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  fixture_containers='ascendany-postgres'

  install -d "$retired_process_root/123/fd"
  ln -s "$production_namespace_root/.venv/bin/python" "$retired_process_root/123/exe"
  ln -s / "$retired_process_root/123/cwd"
  : >"$retired_process_root/123/cmdline"
  : >"$retired_process_root/123/maps"
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  rm -rf -- "$retired_process_root/123"

  install -d "$retired_process_root/124/fd"
  ln -s /usr/bin/true "$retired_process_root/124/exe"
  ln -s / "$retired_process_root/124/cwd"
  ln -s "$production_namespace_root/Release/data.db" "$retired_process_root/124/fd/3"
  : >"$retired_process_root/124/cmdline"
  : >"$retired_process_root/124/maps"
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  rm -rf -- "$retired_process_root/124"

  install -d "$retired_process_root/125/fd"
  ln -s /usr/bin/true "$retired_process_root/125/exe"
  ln -s / "$retired_process_root/125/cwd"
  : >"$retired_process_root/125/cmdline"
  printf '%s\n' "7f000000-7f001000 r--p 00000000 00:00 0 $production_namespace_root/data/legacy.sqlite (deleted)" \
    >"$retired_process_root/125/maps"
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  rm -rf -- "$retired_process_root/125"

  : >"$production_namespace_root/legacy-extra"
  failures=0
  check_retired_generation_closure
  [[ "$failures" -ge 1 ]]
  rm -- "$production_namespace_root/legacy-extra"

  postgres_json="$(jq -n \
    --arg image "$postgres_image_id" \
    --arg reference "$postgres_image_reference" \
    --arg volume "$postgres_data_volume" '[{
      Image: $image,
      Config: {
        Image: $reference,
        Cmd: ["postgres", "-c", "password_encryption=scram-sha-256"],
        Env: [
          "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/lib/postgresql/17/bin",
          "GOSU_VERSION=1.19",
          "LANG=en_US.utf8",
          "PG_MAJOR=17",
          "PG_VERSION=17.10-1.pgdg13+1",
          "PGDATA=/var/lib/postgresql/data",
          "container=podman",
          "HOME=/root",
          "HOSTNAME=0123456789ab"
        ]
      },
      HostConfig: {
        RestartPolicy: {Name: "always", MaximumRetryCount: 0},
        PortBindings: {"5432/tcp": [{HostIp: "127.0.0.1", HostPort: "5432"}]}
      },
      Mounts: [{
        Type: "volume", Name: $volume, Destination: "/var/lib/postgresql/data",
        RW: true, Options: ["nosuid", "nodev", "rbind"]
      }]
    }]')"
  postgres_container_generation_contract <<<"$postgres_json"
  if postgres_container_generation_contract <<<"$(jq '.[0].Config.Env += ["POSTGRES_PASSWORD_FILE=/run/secret"]' <<<"$postgres_json")"; then
    printf 'generation fixture accepted a retained PostgreSQL bootstrap secret\n' >&2
    exit 1
  fi
  if postgres_container_generation_contract <<<"$(jq '.[0].Image = ("f" * 64)' <<<"$postgres_json")"; then
    printf 'generation fixture accepted an unpinned PostgreSQL image\n' >&2
    exit 1
  fi
  if postgres_container_generation_contract <<<"$(jq '.[0].Config.Env += ["HTTPS_PROXY=http://127.0.0.1:7897"]' <<<"$postgres_json")"; then
    printf 'generation fixture accepted a PostgreSQL proxy environment\n' >&2
    exit 1
  fi
  if postgres_container_generation_contract <<<"$(jq '.[0].Config.Env += ["ASCENDANY_TRAINER_ENDPOINT=https://trainer.invalid"]' <<<"$postgres_json")"; then
    printf 'generation fixture accepted a trainer environment\n' >&2
    exit 1
  fi
)

printf 'production generation retirement/fresh-state validator fixtures: PASS\n'
