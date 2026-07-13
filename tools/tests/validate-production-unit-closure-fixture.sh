#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
validator="$repository_root/deploy/v2/scripts/validate-production.sh"
ascendanyd_unit="$repository_root/deploy/v2/systemd/ascendanyd.service"
catalog_publisher_unit="$repository_root/deploy/v2/systemd/ascendany-catalog-publish.service"
cloudflared_unit="$repository_root/deploy/v2/systemd/ascendany-cloudflared.service"
pgbouncer_unit_source="$repository_root/deploy/v2/systemd/ascendany-pgbouncer.service"
go_binary="$(realpath -e -- "$(command -v go)")"
fixture_root="$(mktemp -d)"
pgbouncer_fixture_pid=""

cleanup_fixture() {
  if [[ "$pgbouncer_fixture_pid" =~ ^[1-9][0-9]*$ ]]; then
    kill "$pgbouncer_fixture_pid" 2>/dev/null || true
    wait "$pgbouncer_fixture_pid" 2>/dev/null || true
  fi
  rm -rf -- "$fixture_root"
}
trap cleanup_fixture EXIT

grep -F 'test("^[0-9A-Za-z][0-9A-Za-z._@/-]*$")' "$validator" >/dev/null || {
  printf 'production release path contract rejects systemd template unit names\n' >&2
  exit 1
}
for template_unit_path in \
  systemd/ascendany-judge@.service \
  systemd/ascendany-lsp@.service \
  systemd/ascendany-restore-verify@.service; do
  [[ "$template_unit_path" =~ ^[0-9A-Za-z][0-9A-Za-z._@/-]*$ ]] || {
    printf 'production release path contract rejects %s\n' "$template_unit_path" >&2
    exit 1
  }
done

[[ "$(head -n 1 "$validator")" == '#!/usr/bin/bash -p' ]]
[[ "$(sed -n '2p' "$validator")" == 'set +x' ]]
printf '%s\n' 'touch "$BASH_ENV_MARKER"' >"$fixture_root/bash-env"
if env \
    BASH_ENV="$fixture_root/bash-env" \
    BASH_ENV_MARKER="$fixture_root/bash-env-executed" \
    SHELLOPTS=xtrace \
    ASCENDANY_VALIDATION_PHASE=invalid \
    ASCENDANY_EXPECTED_RUNTIME_FEEDBACK_CREDENTIAL_BINDINGS=DO_NOT_TRACE_THIS_VALUE \
    "$validator" >"$fixture_root/clean-env.out" 2>"$fixture_root/clean-env.err"; then
  printf 'invalid clean-environment validation unexpectedly passed\n' >&2
  exit 1
fi
[[ ! -e "$fixture_root/bash-env-executed" ]]
if grep -F 'DO_NOT_TRACE_THIS_VALUE' "$fixture_root/clean-env.out" "$fixture_root/clean-env.err" >/dev/null; then
  printf 'validator leaked a whitelisted input through inherited tracing\n' >&2
  exit 1
fi
if grep -E '^(release_root|artifact_root|backup_root|restore_evidence|expected_db_user)=.*\$\{|^ *export PG(HOST|PORT|DATABASE|USER|CONNECT_TIMEOUT)=|PG(HOST|PORT|DATABASE|USER|CONNECT_TIMEOUT)="?\$\{' "$validator" >/dev/null; then
  printf 'validator still accepts a caller-controlled runtime path or PostgreSQL identity\n' >&2
  exit 1
fi

if ASCENDANY_VALIDATION_PHASE=invalid \
    "$validator" >"$fixture_root/phase.out" 2>"$fixture_root/phase.err"; then
  printf 'invalid ASCENDANY_VALIDATION_PHASE unexpectedly passed\n' >&2
  exit 1
fi
grep -Fx 'FAIL ASCENDANY_VALIDATION_PHASE must be exactly staged, smoke, activation, catalog, or production' \
  "$fixture_root/phase.err" >/dev/null
if ASCENDANY_VALIDATION_PHASE='' \
    "$validator" >"$fixture_root/phase-empty.out" 2>"$fixture_root/phase-empty.err"; then
  printf 'empty ASCENDANY_VALIDATION_PHASE unexpectedly passed\n' >&2
  exit 1
fi
grep -Fx 'FAIL ASCENDANY_VALIDATION_PHASE must be exactly staged, smoke, activation, catalog, or production' \
  "$fixture_root/phase-empty.err" >/dev/null
if grep -F 'ASCENDANY_CLOUDFLARE_ACCOUNT_ID' "$validator" >/dev/null ||
   grep -F 'ASCENDANY_CLOUDFLARE_TUNNEL_ID' "$validator" >/dev/null; then
  printf 'production validator retains a remotely managed Cloudflare input\n' >&2
  exit 1
fi
grep -F -- \
  "curl --disable --fail --silent --show-error --max-time 5 --noproxy '*' --proto '=http' http://127.0.0.1:18000/version" \
  "$validator" >/dev/null
grep -F -- \
  "curl --disable --fail --silent --show-error --max-time 5 --noproxy '*' --proto '=http' --header 'CF-Connecting-IP: 127.0.0.1' http://127.0.0.1:18000/api/v2/capabilities" \
  "$validator" >/dev/null
for endpoint in livez readyz; do
  grep -F -- \
    "curl --disable --fail --silent --show-error --max-time 5 --noproxy '*' --proto '=http' http://127.0.0.1:18000/$endpoint" \
    "$validator" >/dev/null
done
for model_binding_fragment in \
  'FROM ascendany.recommendation_model_head AS head' \
  'JOIN ascendany.recommendation_model_releases AS model' \
  'JOIN ascendany.recommendation_model_activation_events AS event' \
  'model.model_purpose' \
  'event.application_version' \
  'event.application_commit' \
  'event.application_build_time'; do
  grep -F -- "$model_binding_fragment" "$validator" >/dev/null
done
for preactivation_fragment in \
  'initial preactivation database contains no recommendation model release, head, or activation state' \
  '(SELECT count(*) FROM ascendany.recommendation_model_releases)' \
  '(SELECT count(*) FROM ascendany.recommendation_model_head)' \
  '(SELECT count(*) FROM ascendany.recommendation_model_activation_events)'; do
  grep -F -- "$preactivation_fragment" "$validator" >/dev/null
done
if grep -F 'model.application_' "$validator" >/dev/null; then
  printf 'production model binding reads application identity from the model release instead of its activation event\n' >&2
  exit 1
fi
for directive in \
  'StandardOutput=journal' \
  'StandardError=journal' \
  'MemoryPressureWatch=yes' \
  'MemoryPressureThresholdSec=200ms'; do
  [[ "$(grep -Fxc -- "$directive" "$ascendanyd_unit")" == "1" ]]
done
[[ "$(grep -Fxc -- 'LoadCredentialEncrypted=jwt_signing_private_key:/etc/ascendany/credentials/jwt_signing_private_key.cred' "$ascendanyd_unit")" == 1 ]]
[[ "$(grep -Fxc -- 'Environment=ASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE=%d/jwt_signing_private_key' "$ascendanyd_unit")" == 1 ]]
[[ "$(grep -Fxc -- 'LoadCredentialEncrypted=jwt_verification_public_key:/etc/ascendany/credentials/jwt_verification_public_key.cred' "$catalog_publisher_unit")" == 1 ]]
[[ "$(grep -Fxc -- 'Environment=ASCENDANY_JWT_VERIFICATION_PUBLIC_KEY_FILE=%d/jwt_verification_public_key' "$catalog_publisher_unit")" == 1 ]]
if grep -F 'jwt_signing_private_key' "$catalog_publisher_unit" >/dev/null ||
   grep -F 'jwt_verification_public_key' "$ascendanyd_unit" >/dev/null; then
  printf 'JWT signing and verification capabilities cross the systemd unit boundary\n' >&2
  exit 1
fi

(
  # shellcheck source=../../deploy/v2/scripts/validate-production.sh
  source "$validator"
  trap - EXIT
  pass() { :; }
  fail() { failures=$((failures + 1)); }
  validation_phase=production
  deployment_transition=initial
  expected_write_mode=enabled
  ascendanyd_active=1

  ascendanyd_active=0
  check_active_ascendanyd_process
  check_active_ascendanyd_health
  ascendanyd_active=1

  render_read_only_smoke_dropin >"$fixture_root/read-only-smoke.conf"
  failures=0
  check_read_only_smoke_dropin_bytes "$fixture_root/read-only-smoke.conf"
  [[ "$failures" == "0" ]]
  printf '# drift\n' >>"$fixture_root/read-only-smoke.conf"
  check_read_only_smoke_dropin_bytes "$fixture_root/read-only-smoke.conf"
  [[ "$failures" == "1" ]]

  health_fixture_mode=valid
  curl() {
    local endpoint="${!#}"
    case "$endpoint" in
      */livez) printf '%s\n' '{"status":"alive"}' ;;
      */readyz)
        if [[ "$health_fixture_mode" == "valid" ]]; then
          printf '%s\n' '{"status":"ready","checks":{"database":{"status":"pass"},"migrations":{"status":"pass","currentVersion":7,"expectedVersion":7}}}'
        else
          printf '%s\n' '{"status":"ready","checks":{"database":{"status":"pass"},"migrations":{"status":"pass","currentVersion":6,"expectedVersion":7}}}'
        fi
        ;;
      *) return 1 ;;
    esac
  }
  failures=0
  check_active_ascendanyd_health
  [[ "$failures" == "0" ]]
  health_fixture_mode=drift
  check_active_ascendanyd_health
  [[ "$failures" == "1" ]]

  expected_runtime_feedback_credential_bindings='ASCENDANY_CREDENTIAL_FILE_REF_HEX_42_AUTHORITY_HEX_44=feedback_second ASCENDANY_CREDENTIAL_FILE_REF_HEX_41_AUTHORITY_HEX_43=feedback_first'
  failures=0
  parse_runtime_feedback_bindings
  [[ "$failures" == "0" ]]
  render_runtime_feedback_dropin >"$fixture_root/canonical.conf"
  printf '%s\n' \
    '[Service]' \
    'LoadCredentialEncrypted=feedback_first:/etc/ascendany/credentials/feedback_first.cred' \
    'Environment=ASCENDANY_CREDENTIAL_FILE_REF_HEX_41_AUTHORITY_HEX_43=%d/feedback_first' \
    'LoadCredentialEncrypted=feedback_second:/etc/ascendany/credentials/feedback_second.cred' \
    'Environment=ASCENDANY_CREDENTIAL_FILE_REF_HEX_42_AUTHORITY_HEX_44=%d/feedback_second' \
    >"$fixture_root/expected.conf"
  cmp --silent -- "$fixture_root/expected.conf" "$fixture_root/canonical.conf"

  failures=0
  check_runtime_feedback_dropin_bytes "$fixture_root/canonical.conf"
  [[ "$failures" == "0" ]]
  printf '# unauthorized directive\n' >>"$fixture_root/canonical.conf"
  check_runtime_feedback_dropin_bytes "$fixture_root/canonical.conf"
  [[ "$failures" == "1" ]]

  printf '%s\n' \
    '# Fedora package comment is non-effective.' \
    '' \
    '[Service]' \
    'TimeoutStopFailureMode=abort' \
    >"$fixture_root/global-service.conf"
  failures=0
  check_fedora_global_service_dropin_bytes "$fixture_root/global-service.conf"
  [[ "$failures" == "0" ]]
  printf 'ExecStart=/usr/bin/false\n' >>"$fixture_root/global-service.conf"
  check_fedora_global_service_dropin_bytes "$fixture_root/global-service.conf"
  [[ "$failures" == "1" ]]

  expected_runtime_feedback_credential_bindings='ASCENDANY_CREDENTIAL_FILE_REF_HEX_41_AUTHORITY_HEX_43=db_password'
  failures=0
  parse_runtime_feedback_bindings || true
  [[ "$failures" == "1" ]]

  expected_runtime_feedback_credential_bindings='ASCENDANY_CREDENTIAL_FILE_REF_HEX_41_AUTHORITY_HEX_43=feedback_one ASCENDANY_CREDENTIAL_FILE_REF_HEX_42_AUTHORITY_HEX_44=feedback_one'
  failures=0
  parse_runtime_feedback_bindings || true
  [[ "$failures" == "1" ]]

  fixture_fragment='/etc/systemd/system/example.service'
  fixture_dropins=''
  fixture_working_directory='/var/lib/example'
  example_unit_text=$'[Service]\nExecStart=/opt/ascendany/v2/bin/example run\nExecStartPre=/usr/bin/test -s %d/example\nEnvironment=ASCENDANY_FEEDBACK_FILE=%d/feedback_one'
  manager_environment_extra=''
  systemctl() {
    case "$1" in
      show-environment)
        printf '%s\n' 'PATH=/usr/local/bin:/usr/bin' 'LANG=zh_CN.UTF-8'
        [[ -z "$manager_environment_extra" ]] || printf '%s\n' "$manager_environment_extra"
        ;;
      cat)
        [[ "$2" == "example.service" ]] || return 1
        printf '%s\n' "$example_unit_text"
        ;;
      *) return 1 ;;
    esac
  }
  unit_property() {
    local unit="$1" property="$2"
    [[ "$unit" == "example.service" ]] || return 1
    case "$property" in
      FragmentPath) printf '%s\n' "$fixture_fragment" ;;
      DropInPaths) printf '%s\n' "$fixture_dropins" ;;
      WorkingDirectory) printf '%s\n' "$fixture_working_directory" ;;
      ExecStart)
        : >"$fixture_root/show-representation-read"
        printf '%s\n' '{ path=/opt/ascendany/v2/bin/example ; argv[]=/opt/ascendany/v2/bin/example expanded ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }'
        ;;
      ExecStartPre)
        : >"$fixture_root/show-representation-read"
        printf '%s\n' '{ path=/usr/bin/test ; argv[]=/usr/bin/test -s /run/credentials/example.service/example ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }'
        ;;
      Environment)
        : >"$fixture_root/show-representation-read"
        printf '%s\n' 'ASCENDANY_FEEDBACK_FILE=/run/credentials/example.service/feedback_one'
        ;;
      *) return 1 ;;
    esac
  }

  expected_start='/opt/ascendany/v2/bin/example run'
  expected_pre='/usr/bin/test -s %d/example'
  expected_environment='ASCENDANY_FEEDBACK_FILE=%d/feedback_one'
  failures=0
  check_unit_effective_shape \
    example.service "$fixture_fragment" "$fixture_working_directory" "" \
    "$expected_start" "$expected_pre" "$expected_environment"
  [[ "$failures" == "0" ]]
  [[ ! -e "$fixture_root/show-representation-read" ]]

  example_unit_text=$'[Service]\nExecStart=/opt/ascendany/v2/bin/example bypass\nExecStartPre=/usr/bin/test -s %d/example\nEnvironment=ASCENDANY_FEEDBACK_FILE=%d/feedback_one'
  check_effective_directive_sequence example.service ExecStart "$expected_start"
  [[ "$failures" == "1" ]]

  failures=0
  example_unit_text=$'[Service]\nExecStart=/opt/ascendany/v2/bin/example run\nExecStartPre=/usr/bin/test -s %d/example\nEnvironment=ASCENDANY_FEEDBACK_FILE=%d/feedback_one'
  fixture_dropins='/etc/systemd/system/example.service.d/99-override.conf'
  check_unit_effective_shape \
    example.service "$fixture_fragment" "$fixture_working_directory" "" \
    "$expected_start" "$expected_pre" "$expected_environment"
  [[ "$failures" == "1" ]]

  fixture_dropins=''
  failures=0
  check_effective_directive_sequence example.service Environment \
    'ASCENDANY_FEEDBACK_FILE=%d/feedback_two'
  [[ "$failures" == "1" ]]

  failures=0
  check_system_manager_environment
  [[ "$failures" == "0" ]]
  manager_environment_extra='DATABASE_URL=postgres://attacker.example/override'
  check_system_manager_environment
  [[ "$failures" == "1" ]]
  failures=0
  manager_environment_extra='LD_PRELOAD=/tmp/attacker.so'
  check_system_manager_environment
  [[ "$failures" == "1" ]]
  manager_environment_extra=''

  unit_exec_drift=0
  unit_environment_drift=0
  backup_runtime_environment_drift=0
  timer_schedule_drift=0
  standard_output_drift=0
  standard_error_drift=0
  memory_pressure_watch_drift=0
  memory_pressure_threshold_drift=0
  fixture_ascendanyd_active_state=active
  fixture_ascendanyd_enabled_state=enabled
  fixture_model_activation_active_state=inactive
  fixture_model_activation_enabled_state=static
  fixture_model_activation_result=success
  fixture_model_activation_main_code=exited
  fixture_model_activation_main_status=0
  fixture_model_registration_active_state=inactive
  fixture_model_registration_enabled_state=static
  fixture_model_registration_result=success
  fixture_model_registration_main_code=exited
  fixture_model_registration_main_status=0
  fixture_timer_active_state=inactive
  fixture_timer_enabled_state=disabled
  render_fixture_unit() {
    local unit="$1"
    case "$unit" in
      ascendanyd.service)
        printf '%s\n' \
          '[Service]' \
          'EnvironmentFile=-/etc/ascendany/v2/ascendanyd.env' \
          'Environment=SHELL=/usr/sbin/nologin' \
          'Environment=ASCENDANY_DATABASE_PASSWORD_FILE=%d/db_password' \
          'Environment=ASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE=%d/jwt_signing_private_key' \
          'Environment=ASCENDANY_PASSWORD_PEPPER_FILE=%d/password_pepper' \
          'ExecStartPre=/usr/bin/test -s %d/db_password' \
          'ExecStartPre=/usr/bin/test -s %d/jwt_signing_private_key' \
          'ExecStartPre=/usr/bin/test -s %d/password_pepper' \
          'ExecStartPre=/opt/ascendany/v2/bin/ascendany-model verify-catalog --catalog /opt/ascendany/v2/models/recommendation-knowledge-catalog.json --catalog-sha256 ${ASCENDANY_KNOWLEDGE_CATALOG_SHA256} --model /opt/ascendany/v2/models/recommendation-model.json --model-sha256 ${ASCENDANY_RECOMMENDATION_MODEL_SHA256} --expected-purpose ${ASCENDANY_RECOMMENDATION_MODEL_PURPOSE}' \
          'ExecStart=/opt/ascendany/v2/bin/ascendanyd serve' \
          'StandardOutput=journal' \
          'StandardError=journal' \
          'MemoryPressureWatch=yes' \
          'MemoryPressureThresholdSec=200ms'
        if smoke_dropin_required; then
          printf '%s\n' \
            '[Service]' \
            'EnvironmentFile=' \
            'EnvironmentFile=/etc/ascendany/v2/ascendanyd.env' \
            'EnvironmentFile=/etc/ascendany/v2/ascendanyd-read-only-smoke.env'
        fi
        ;;
      ascendany-model-activate.service)
        printf '%s\n' \
          '[Service]' \
          'EnvironmentFile=/etc/ascendany/v2/ascendanyd.env' \
          'Environment=SHELL=/usr/sbin/nologin' \
          'Environment=ASCENDANY_DATABASE_PASSWORD_FILE=%d/db_password' \
          'ExecStartPre=/usr/bin/test -s %d/db_password' \
          'ExecStartPre=/opt/ascendany/v2/bin/ascendany-model verify-catalog --catalog /opt/ascendany/v2/models/recommendation-knowledge-catalog.json --catalog-sha256 ${ASCENDANY_KNOWLEDGE_CATALOG_SHA256} --model /opt/ascendany/v2/models/recommendation-model.json --model-sha256 ${ASCENDANY_RECOMMENDATION_MODEL_SHA256} --expected-purpose ${ASCENDANY_RECOMMENDATION_MODEL_PURPOSE}' \
          'ExecStart=/opt/ascendany/v2/bin/ascendanyd activate-model'
        ;;
      ascendany-model-register.service)
        printf '%s\n' \
          '[Service]' \
          'EnvironmentFile=/etc/ascendany/v2/ascendanyd.env' \
          'Environment=SHELL=/usr/sbin/nologin' \
          'Environment=ASCENDANY_DATABASE_PASSWORD_FILE=%d/db_password' \
          'ExecStartPre=/usr/bin/test -s %d/db_password' \
          'ExecStartPre=/opt/ascendany/v2/bin/ascendany-model verify-catalog --catalog /opt/ascendany/v2/models/recommendation-knowledge-catalog.json --catalog-sha256 ${ASCENDANY_KNOWLEDGE_CATALOG_SHA256} --model /opt/ascendany/v2/models/recommendation-model.json --model-sha256 ${ASCENDANY_RECOMMENDATION_MODEL_SHA256} --expected-purpose ${ASCENDANY_RECOMMENDATION_MODEL_PURPOSE}' \
          'ExecStart=/opt/ascendany/v2/bin/ascendanyd register-model'
        ;;
      ascendany-catalog-publish.service)
        printf '%s\n' \
          '[Service]' \
          'EnvironmentFile=/etc/ascendany-catalog-publisher/catalog-publish.env' \
          'Environment=SHELL=/usr/sbin/nologin' \
          'Environment=ASCENDANY_DATABASE_PASSWORD_FILE=%d/catalog_publisher_db_password' \
          'Environment=ASCENDANY_JWT_VERIFICATION_PUBLIC_KEY_FILE=%d/jwt_verification_public_key' \
          'ExecStartPre=+/usr/bin/test -x /opt/ascendany/v2/bin/ascendany-catalog-publish' \
          'ExecStartPre=+/usr/bin/test -r /etc/ascendany-catalog-publisher/catalog-publish.env' \
          'ExecStartPre=+/usr/bin/test -f /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred' \
          'ExecStartPre=+/usr/bin/test ! -L /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred' \
          'ExecStartPre=+/usr/bin/test -s /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred' \
          'ExecStartPre=+/usr/bin/test -f /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred' \
          'ExecStartPre=+/usr/bin/test ! -L /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred' \
          'ExecStartPre=+/usr/bin/test -s /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred' \
          'ExecStartPre=/usr/bin/test -s %d/catalog_publisher_db_password' \
          'ExecStartPre=/usr/bin/test -s %d/jwt_verification_public_key' \
          'ExecStartPre=/usr/bin/test -s %d/catalog_publication_request' \
          'ExecStartPre=/usr/bin/test -s %d/admin_access_token' \
          'ExecStart=/opt/ascendany/v2/bin/ascendany-catalog-publish publish' \
          'ExecStartPost=/usr/bin/test -d /var/lib/ascendany-catalog-publisher/receipts' \
          'ExecStartPost=+/usr/bin/rm -f -- /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred'
        ;;
      ascendany-judge@validation.service)
        printf '%s\n' \
          '[Service]' \
          'Environment=HOME=/var/lib/ascendany-judge' \
          'Environment=XDG_RUNTIME_DIR=/run/ascendany-judge-podman/%i' \
          'Environment=XDG_DATA_HOME=/var/lib/ascendany-judge/.local/share' \
          'Environment=XDG_CONFIG_HOME=/var/lib/ascendany-judge/.config' \
          'Environment=XDG_CACHE_HOME=/var/lib/ascendany-judge/.cache' \
          'ExecStart=/opt/ascendany/v2/bin/ascendany-judge run --job-id %i --control-socket /run/ascendany-judge/%i.sock --work-root /var/lib/ascendany-judge/jobs/%i --allowed-client-user ascendany --compiler-image ${ASCENDANY_JUDGE_COMPILER_IMAGE} --runtime-image ${ASCENDANY_JUDGE_RUNTIME_IMAGE} --podman-binary /usr/bin/podman --delegated-cgroup-root /sys/fs/cgroup'
        ;;
      ascendany-lsp@validation.service)
        printf '%s\n' \
          '[Service]' \
          'ExecStart=/opt/ascendany/v2/bin/ascendany-lsp serve --session-id %i --control-socket /run/ascendany-lsp-control/control.sock --workspace /tmp/ascendany-lsp-sessions/%i'
        if [[ "$unit_environment_drift" == "1" ]]; then
          printf '%s\n' 'Environment=DATABASE_URL=postgres://attacker.example/lsp'
        fi
        ;;
      ascendany-admin-bootstrap.service)
        printf '%s\n' \
          '[Service]' \
          'Environment=ASCENDANY_DATABASE_PASSWORD_FILE=%d/db_password' \
          'Environment=ASCENDANY_PASSWORD_PEPPER_FILE=%d/password_pepper' \
          'ExecStartPre=+/usr/bin/test -x /opt/ascendany/v2/bin/ascendany-admin-bootstrap' \
          'ExecStartPre=+/usr/bin/test -r /etc/ascendany/v2/ascendanyd.env' \
          'ExecStartPre=+/usr/bin/test -s /run/ascendany-admin-bootstrap-input/admin_password.cred' \
          'ExecStartPre=/usr/bin/test -s %d/db_password' \
          'ExecStartPre=/usr/bin/test -s %d/password_pepper' \
          'ExecStartPre=/usr/bin/test -s %d/admin_password' \
          'ExecStart=/opt/ascendany/v2/bin/ascendany-admin-bootstrap create --username admin --display-name admin' \
          'ExecStopPost=+/usr/bin/rm -f -- /run/ascendany-admin-bootstrap-input/admin_password.cred'
        ;;
      ascendany-backup.service)
        printf '%s\n' \
          '[Service]' \
          'Environment=ASCENDANY_DATABASE_PASSWORD_FILE=%d/backup_db_password'
        if [[ "$backup_runtime_environment_drift" == "1" ]]; then
          printf '%s\n' 'Environment=ASCENDANY_BACKUP_RUNTIME_ROOT=/var/backups/ascendany/runtime'
        else
          printf '%s\n' 'Environment=ASCENDANY_BACKUP_RUNTIME_ROOT=/run/ascendany-backup'
        fi
        printf '%s\n' 'ExecStartPre=/usr/bin/test -s %d/backup_db_password'
        if [[ "$unit_exec_drift" == "1" ]]; then
          printf '%s\n' 'ExecStart=/opt/ascendany/v2/bin/ascendany-backup verify'
        else
          printf '%s\n' 'ExecStart=/opt/ascendany/v2/bin/ascendany-backup create'
        fi
        ;;
      ascendany-migrate.service)
        printf '%s\n' \
          '[Service]' \
          'Environment=ASCENDANY_DATABASE_PASSWORD_FILE=%d/migrator_db_password' \
          'ExecStartPre=/usr/bin/test -s %d/migrator_db_password' \
          'ExecStart=/opt/ascendany/v2/bin/ascendany-migrate up'
        ;;
      ascendany-restore-verify@validation.service)
        printf '%s\n' \
          '[Service]' \
          'Environment=ASCENDANY_RESTORE_DATABASE_PASSWORD_FILE=%d/restore_db_password' \
          'Environment=ASCENDANY_RESTORE_RUNTIME_ROOT=%t/ascendany-restore-verify-%i' \
          'ExecStartPre=/usr/bin/test -s %d/restore_db_password' \
          'ExecStartPre=/usr/bin/test -f /run/ascendany-restore-operator/operator.lock' \
          'ExecStartPre=/usr/bin/test -f /run/ascendany-restore-operator/publication.lock' \
          'ExecStart=/opt/ascendany/v2/scripts/restore-verify-operator.sh run %i' \
          'ExecStartPost=+/opt/ascendany/v2/scripts/publish-restore-evidence.sh %i'
        ;;
      *) return 1 ;;
    esac
    printf '%s\n' '[Service]' 'TimeoutStopFailureMode=abort'
  }
  systemctl() {
    case "$1" in
      show-environment)
        printf '%s\n' 'PATH=/usr/local/bin:/usr/bin' 'LANG=zh_CN.UTF-8'
        ;;
      cat) render_fixture_unit "$2" ;;
      is-enabled)
        case "$2" in
          ascendanyd.service) printf '%s\n' "$fixture_ascendanyd_enabled_state" ;;
          ascendany-model-register.service) printf '%s\n' "$fixture_model_registration_enabled_state" ;;
          ascendany-model-activate.service) printf '%s\n' "$fixture_model_activation_enabled_state" ;;
          ascendany-catalog-publish.service) printf '%s\n' static ;;
          ascendany-backup.timer) printf '%s\n' "$fixture_timer_enabled_state" ;;
          *) return 1 ;;
        esac
        ;;
      *) return 1 ;;
    esac
  }
  unit_property() {
    local unit="$1" property="$2"
    case "$property" in
      FragmentPath)
        case "$unit" in
          ascendanyd.service|ascendany-model-register.service|ascendany-model-activate.service|ascendany-catalog-publish.service|ascendany-admin-bootstrap.service|ascendany-backup.service|ascendany-backup.timer|ascendany-migrate.service)
            printf '/etc/systemd/system/%s\n' "$unit"
            ;;
          ascendany-judge@validation.service) printf '/etc/systemd/system/ascendany-judge@.service\n' ;;
          ascendany-lsp@validation.service) printf '/etc/systemd/system/ascendany-lsp@.service\n' ;;
          ascendany-restore-verify@validation.service) printf '/etc/systemd/system/ascendany-restore-verify@.service\n' ;;
          *) return 1 ;;
        esac
        ;;
      LoadState)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        printf 'loaded\n'
        ;;
      NeedDaemonReload)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        printf 'no\n'
        ;;
      ActiveState)
        case "$unit" in
          ascendanyd.service) printf '%s\n' "$fixture_ascendanyd_active_state" ;;
          ascendany-model-register.service) printf '%s\n' "$fixture_model_registration_active_state" ;;
          ascendany-model-activate.service) printf '%s\n' "$fixture_model_activation_active_state" ;;
          ascendany-catalog-publish.service) printf '%s\n' inactive ;;
          ascendany-backup.timer) printf '%s\n' "$fixture_timer_active_state" ;;
          *) return 1 ;;
        esac
        ;;
      DropInPaths)
        if [[ "$unit" != "ascendany-backup.timer" ]]; then
          printf '/usr/lib/systemd/system/service.d/10-timeout-abort.conf\n'
          if [[ "$unit" == "ascendanyd.service" ]] && smoke_dropin_required; then
            printf '%s\n' "$smoke_dropin"
          fi
        fi
        ;;
      WorkingDirectory)
        case "$unit" in
          ascendanyd.service|ascendany-model-register.service|ascendany-model-activate.service) printf '/var/lib/ascendany\n' ;;
          ascendany-catalog-publish.service) printf '/var/lib/ascendany-catalog-publisher\n' ;;
          ascendany-judge@validation.service) printf '/var/lib/ascendany-judge\n' ;;
          ascendany-lsp@validation.service) printf '/tmp\n' ;;
          ascendany-admin-bootstrap.service) printf '/var/lib/ascendany\n' ;;
          ascendany-backup.service) printf '/var/backups/ascendany\n' ;;
          ascendany-migrate.service) printf '/var/lib/ascendany-migrate\n' ;;
          ascendany-restore-verify@validation.service) printf '/var/lib/ascendany-restore\n' ;;
          *) return 1 ;;
        esac
        ;;
      ExecStart|ExecStartPre|Environment)
        : >"$fixture_root/show-representation-read"
        printf '%s\n' 'expanded runtime representation intentionally differs'
        ;;
      StandardOutput)
        [[ "$unit" == "ascendanyd.service" ]] || return 1
        if [[ "$standard_output_drift" == "1" ]]; then printf 'null\n'; else printf 'journal\n'; fi
        ;;
      StandardError)
        [[ "$unit" == "ascendanyd.service" ]] || return 1
        if [[ "$standard_error_drift" == "1" ]]; then printf 'inherit\n'; else printf 'journal\n'; fi
        ;;
      MemoryPressureWatch)
        [[ "$unit" == "ascendanyd.service" ]] || return 1
        if [[ "$memory_pressure_watch_drift" == "1" ]]; then printf 'auto\n'; else printf 'yes\n'; fi
        ;;
      MemoryPressureThresholdUSec)
        [[ "$unit" == "ascendanyd.service" ]] || return 1
        if [[ "$memory_pressure_threshold_drift" == "1" ]]; then printf '1s\n'; else printf '200ms\n'; fi
        ;;
      Result)
        case "$unit" in
          ascendany-model-register.service) printf '%s\n' "$fixture_model_registration_result" ;;
          ascendany-model-activate.service) printf '%s\n' "$fixture_model_activation_result" ;;
          *) return 1 ;;
        esac
        ;;
      ExecMainCode)
        case "$unit" in
          ascendany-model-register.service) printf '%s\n' "$fixture_model_registration_main_code" ;;
          ascendany-model-activate.service) printf '%s\n' "$fixture_model_activation_main_code" ;;
          *) return 1 ;;
        esac
        ;;
      ExecMainStatus)
        case "$unit" in
          ascendany-model-register.service) printf '%s\n' "$fixture_model_registration_main_status" ;;
          ascendany-model-activate.service) printf '%s\n' "$fixture_model_activation_main_status" ;;
          *) return 1 ;;
        esac
        ;;
      Type)
        [[ "$unit" == "ascendany-model-register.service" || "$unit" == "ascendany-model-activate.service" || "$unit" == "ascendany-catalog-publish.service" ]] || return 1
        printf 'oneshot\n'
        ;;
      NoNewPrivileges|ProtectHome|PrivateDevices|RestrictNamespaces|MemoryDenyWriteExecute)
        [[ "$unit" == "ascendany-model-register.service" || "$unit" == "ascendany-model-activate.service" || "$unit" == "ascendany-catalog-publish.service" ]] || return 1
        printf 'yes\n'
        ;;
      ProtectSystem)
        [[ "$unit" == "ascendany-model-register.service" || "$unit" == "ascendany-model-activate.service" || "$unit" == "ascendany-catalog-publish.service" ]] || return 1
        printf 'strict\n'
        ;;
      ProtectProc)
        [[ "$unit" == "ascendany-model-register.service" || "$unit" == "ascendany-model-activate.service" || "$unit" == "ascendany-catalog-publish.service" ]] || return 1
        printf 'invisible\n'
        ;;
      ProcSubset)
        [[ "$unit" == "ascendany-model-register.service" || "$unit" == "ascendany-model-activate.service" || "$unit" == "ascendany-catalog-publish.service" ]] || return 1
        printf 'pid\n'
        ;;
      DevicePolicy)
        [[ "$unit" == "ascendany-model-register.service" || "$unit" == "ascendany-model-activate.service" || "$unit" == "ascendany-catalog-publish.service" ]] || return 1
        printf 'closed\n'
        ;;
      RestrictAddressFamilies)
        [[ "$unit" == "ascendany-model-register.service" || "$unit" == "ascendany-model-activate.service" || "$unit" == "ascendany-catalog-publish.service" ]] || return 1
        printf '%s\n' 'AF_UNIX AF_INET AF_INET6'
        ;;
      ReadWritePaths)
        case "$unit" in
          ascendany-model-register.service) printf '/var/lib/ascendany\n' ;;
          ascendany-model-activate.service) printf '/var/lib/ascendany\n' ;;
          ascendany-catalog-publish.service) printf '%s\n' '/var/lib/ascendany-catalog-publisher/receipts /var/lib/ascendany-catalog-publisher/pending' ;;
          *) return 1 ;;
        esac
        ;;
      InaccessiblePaths)
        case "$unit" in
          ascendanyd.service)
            printf '%s\n' '/opt/ascendany/Release /var/lib/ascendany-catalog-publisher'
            ;;
          ascendany-model-register.service)
            printf '%s\n' '/opt/ascendany/Release /var/lib/ascendany/artifacts /var/backups/ascendany /var/lib/ascendany-catalog-publisher'
            ;;
          ascendany-model-activate.service)
            printf '%s\n' '/opt/ascendany/Release /var/lib/ascendany/artifacts /var/backups/ascendany /var/lib/ascendany-catalog-publisher'
            ;;
          ascendany-catalog-publish.service)
            printf '%s\n' '/etc/ascendany/v2 /etc/ascendany/credentials /opt/ascendany/Release /var/lib/ascendany /var/backups/ascendany'
            ;;
          *) return 1 ;;
        esac
        ;;
      Unit)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        printf 'ascendany-backup.service\n'
        ;;
      AccuracyUSec)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        printf '1min\n'
        ;;
      RandomizedDelayUSec)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        if [[ "$timer_schedule_drift" == "1" ]]; then printf '2h\n'; else printf '20min\n'; fi
        ;;
      FixedRandomDelay)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        printf 'no\n'
        ;;
      Persistent)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        printf 'yes\n'
        ;;
      TimersCalendar)
        [[ "$unit" == "ascendany-backup.timer" ]] || return 1
        printf '{ OnCalendar=*-*-* 03:20:00 ; next_elapse=Sun 2026-07-12 03:20:00 +08 }\n'
        ;;
      TimeoutStopFailureMode)
        [[ "$unit" != "ascendany-backup.timer" ]] || return 1
        printf 'abort\n'
        ;;
      *) return 1 ;;
    esac
  }

  runtime_feedback_bindings=()
  runtime_feedback_credential_ids=()
  runtime_feedback_environment=()
  release_model_sha256='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
  release_catalog_sha256='cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc'
  release_manifest_purpose=production

  printf '%s\n' \
    '# reviewed fixture config' \
    'ASCENDANY_HTTP_LISTEN=127.0.0.1:18000' \
    'ASCENDANY_RECOMMENDATION_MODEL_PATH=/opt/ascendany/v2/models/recommendation-model.json' \
    'ASCENDANY_RECOMMENDATION_MODEL_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
    'ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=production' \
    'ASCENDANY_KNOWLEDGE_CATALOG_PATH=/opt/ascendany/v2/models/recommendation-knowledge-catalog.json' \
    'ASCENDANY_KNOWLEDGE_CATALOG_SHA256=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' \
    'ASCENDANY_WRITE_MODE=enabled' \
    >"$fixture_root/ascendanyd.env"
  printf '%s\n' \
    '# reviewed fixture smoke override' \
    'ASCENDANY_WRITE_MODE=disabled' \
    >"$fixture_root/ascendanyd-read-only-smoke.env"
  failures=0
  check_ascendanyd_config_contract \
    "$fixture_root/ascendanyd.env" \
    "$fixture_root/ascendanyd-read-only-smoke.env"
  [[ "$failures" == "0" ]]
  sed -i 's/ASCENDANY_WRITE_MODE=enabled/ASCENDANY_WRITE_MODE=disabled/' \
    "$fixture_root/ascendanyd.env"
  check_ascendanyd_config_contract \
    "$fixture_root/ascendanyd.env" \
    "$fixture_root/ascendanyd-read-only-smoke.env"
  [[ "$failures" == "1" ]]
  sed -i 's/ASCENDANY_WRITE_MODE=disabled/ASCENDANY_WRITE_MODE=enabled/' \
    "$fixture_root/ascendanyd.env"
  sed -i 's/ASCENDANY_RECOMMENDATION_MODEL_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/ASCENDANY_RECOMMENDATION_MODEL_SHA256=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/' \
    "$fixture_root/ascendanyd.env"
  failures=0
  check_ascendanyd_config_contract \
    "$fixture_root/ascendanyd.env" \
    "$fixture_root/ascendanyd-read-only-smoke.env"
  [[ "$failures" == "1" ]]
  sed -i 's/ASCENDANY_RECOMMENDATION_MODEL_SHA256=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/ASCENDANY_RECOMMENDATION_MODEL_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/' \
    "$fixture_root/ascendanyd.env"
  sed -i 's/ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=production/ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=acceptance_test/' \
    "$fixture_root/ascendanyd.env"
  failures=0
  check_ascendanyd_config_contract \
    "$fixture_root/ascendanyd.env" \
    "$fixture_root/ascendanyd-read-only-smoke.env"
  [[ "$failures" == "1" ]]
  sed -i 's/ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=acceptance_test/ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=production/' \
    "$fixture_root/ascendanyd.env"
  write_process_environment() {
    local extra="${1:-}"
    printf '%s\0' \
      'LANG=zh_CN.UTF-8' \
      'PATH=/usr/local/bin:/usr/bin' \
      'USER=ascendany' \
      'LOGNAME=ascendany' \
      'HOME=/var/lib/ascendany' \
      'SHELL=/usr/sbin/nologin' \
      'ASCENDANY_HTTP_LISTEN=127.0.0.1:18000' \
      'ASCENDANY_RECOMMENDATION_MODEL_PATH=/opt/ascendany/v2/models/recommendation-model.json' \
      'ASCENDANY_RECOMMENDATION_MODEL_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
      'ASCENDANY_RECOMMENDATION_MODEL_PURPOSE=production' \
      'ASCENDANY_KNOWLEDGE_CATALOG_PATH=/opt/ascendany/v2/models/recommendation-knowledge-catalog.json' \
      'ASCENDANY_KNOWLEDGE_CATALOG_SHA256=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' \
      "ASCENDANY_WRITE_MODE=$expected_write_mode" \
      'ASCENDANY_DATABASE_PASSWORD_FILE=/run/credentials/ascendanyd.service/db_password' \
      'ASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE=/run/credentials/ascendanyd.service/jwt_signing_private_key' \
      'ASCENDANY_PASSWORD_PEPPER_FILE=/run/credentials/ascendanyd.service/password_pepper' \
      'INVOCATION_ID=0123456789abcdef0123456789abcdef' \
      'JOURNAL_STREAM=8:9' \
      'SYSTEMD_EXEC_PID=4242' \
      'MEMORY_PRESSURE_WATCH=/sys/fs/cgroup/system.slice/ascendanyd.service/memory.pressure' \
      'MEMORY_PRESSURE_WRITE=c29tZSAyMDAwMDAgMjAwMDAwMAA=' \
      'CREDENTIALS_DIRECTORY=/run/credentials/ascendanyd.service' \
      'RUNTIME_DIRECTORY=/run/ascendany' \
      'STATE_DIRECTORY=/var/lib/ascendany' \
      'LOGS_DIRECTORY=/var/log/ascendany' \
      >"$fixture_root/process.environ"
    [[ -z "$extra" ]] || printf '%s\0' "$extra" >>"$fixture_root/process.environ"
  }
  write_process_environment
  failures=0
  check_active_ascendanyd_environment \
    4242 "$fixture_root/process.environ" "$fixture_root/ascendanyd.env"
  [[ "$failures" == "0" ]]
  failures=0
  check_system_manager_environment
  write_process_environment 'LD_PRELOAD=/tmp/stale-manager-injection.so'
  check_active_ascendanyd_environment \
    4242 "$fixture_root/process.environ" "$fixture_root/ascendanyd.env"
  [[ "$failures" == "1" ]]

  validation_phase=smoke
  expected_write_mode=disabled
  write_process_environment
  failures=0
  check_active_ascendanyd_environment \
    4242 "$fixture_root/process.environ" "$fixture_root/ascendanyd.env"
  [[ "$failures" == "0" ]]
  validation_phase=production
  expected_write_mode=enabled

  check_fedora_global_service_dropin() { :; }
  check_read_only_smoke_dropin() { :; }
  rm -f -- "$fixture_root/show-representation-read"
  failures=0
  check_all_unit_effective_shapes
  [[ "$failures" == "0" ]]
  [[ ! -e "$fixture_root/show-representation-read" ]]

  validation_phase=smoke
  expected_write_mode=disabled
  failures=0
  check_all_unit_effective_shapes
  [[ "$failures" == "0" ]]
  validation_phase=production
  expected_write_mode=enabled

  for drift in \
    standard_output_drift \
    standard_error_drift \
    memory_pressure_watch_drift \
    memory_pressure_threshold_drift; do
    failures=0
    printf -v "$drift" '%s' 1
    check_all_unit_effective_shapes
    [[ "$failures" == "1" ]]
    printf -v "$drift" '%s' 0
  done

  failures=0
  unit_exec_drift=1
  check_all_unit_effective_shapes
  [[ "$failures" == "1" ]]
  unit_exec_drift=0

  failures=0
  unit_environment_drift=1
  check_all_unit_effective_shapes
  [[ "$failures" == "1" ]]
  unit_environment_drift=0

  failures=0
  backup_runtime_environment_drift=1
  check_all_unit_effective_shapes
  [[ "$failures" == "1" ]]
  backup_runtime_environment_drift=0

  timer_schedule_drift=1
  failures=0
  check_backup_timer_effective_shape
  [[ "$failures" == "1" ]]

  timer_schedule_drift=0
  validation_phase=production
  fixture_ascendanyd_active_state=active
  fixture_ascendanyd_enabled_state=enabled
  failures=0
  check_ascendanyd_phase_state
  [[ "$failures" == "0" ]]
  validation_phase=smoke
  fixture_ascendanyd_enabled_state=disabled
  check_ascendanyd_phase_state
  [[ "$failures" == "0" ]]
  validation_phase=staged
  fixture_ascendanyd_active_state=inactive
  check_ascendanyd_phase_state
  [[ "$failures" == "0" ]]
  fixture_ascendanyd_active_state=active
  check_ascendanyd_phase_state
  [[ "$failures" == "1" ]]

  validation_phase=activation
  failures=0
  check_model_registration_unit_state
  [[ "$failures" == "0" ]]
  fixture_model_registration_main_status=1
  check_model_registration_unit_state
  [[ "$failures" == "1" ]]
  fixture_model_registration_main_status=0
  fixture_model_registration_enabled_state=enabled
  failures=0
  check_model_registration_unit_state
  [[ "$failures" == "1" ]]
  fixture_model_registration_enabled_state=static

  failures=0
  check_model_activation_unit_state
  [[ "$failures" == "0" ]]
  fixture_model_activation_main_status=1
  check_model_activation_unit_state
  [[ "$failures" == "1" ]]
  fixture_model_activation_main_status=0
  fixture_model_activation_enabled_state=enabled
  failures=0
  check_model_activation_unit_state
  [[ "$failures" == "1" ]]
  fixture_model_activation_enabled_state=static

  failures=0
  fixture_timer_active_state=inactive
  fixture_timer_enabled_state=disabled
  check_inactive_backup_timer
  [[ "$failures" == "0" ]]
  fixture_timer_enabled_state=enabled
  check_inactive_backup_timer
  [[ "$failures" == "1" ]]

  systemctl() {
    [[ "$1" == "cat" && "$2" == "example.service" ]] || return 1
    printf '%s\n' \
      '[Service]' \
      'LoadCredentialEncrypted=db_password:/etc/ascendany/credentials/db_password.cred'
  }
  check_credential_source() { return 0; }
  failures=0
  check_unit_credentials example.service feedback_one
  [[ "$failures" == "1" ]]

  mkdir -p "$fixture_root/release/db/roles"
  printf '%s\n' "SELECT 'peer-admin-verifier';" >"$fixture_root/release/db/roles/verify_v2_roles.sql"
  : >"$fixture_root/runtime.pgpass"
  release_root="$fixture_root/release"
  release_payload_verified=1
  validation_phase=smoke
  PGPASSFILE="$fixture_root/runtime.pgpass"
  run_runtime_psql() {
    [[ " $* " == *' -c '* ]] || return 1
    : >"$fixture_root/runtime-role-query"
    printf '%s\n' 'ascendanyd_login|f|f|f|f|f|f|t'
  }
  postgres_admin_psql() {
    [[ " $* " == *' --dbname=ascendany_v2 '* ]] || return 1
    command cat >"$fixture_root/release-role-verifier"
  }
  failures=0
  check_database_role
  [[ "$failures" == "0" ]]
  [[ -e "$fixture_root/runtime-role-query" ]]
  cmp -s "$fixture_root/release/db/roles/verify_v2_roles.sql" \
    "$fixture_root/release-role-verifier"

  run_runtime_psql() {
    if [[ " $* " == *' -c '* ]]; then
      printf '%s\n' 'ascendanyd_login|f|f|f|f|f|t|t'
      return
    fi
    return 0
  }
  failures=0
  check_database_role
  [[ "$failures" == "1" ]]

  run_runtime_psql() {
    printf '%s\n' "$admin_bootstrap_result"
  }
  for admin_phase_case in \
    'staged:0|0|0|0|0' \
    'smoke:0|0|0|0|0' \
    'production:2|2|1|1|1'; do
    validation_phase="${admin_phase_case%%:*}"
    admin_bootstrap_result="${admin_phase_case#*:}"
    failures=0
    check_admin_bootstrap_database
    [[ "$failures" == "0" ]]
  done
  validation_phase=smoke
  admin_bootstrap_result='1|1|1|1|1'
  failures=0
  check_admin_bootstrap_database
  [[ "$failures" == "1" ]]

  unit_property() {
    [[ "$1" == "ascendany-admin-bootstrap.service" && "$2" == "EnvironmentFiles" ]] || return 1
    printf '%s\n' '/etc/ascendany/v2/ascendanyd.env (ignore_errors=yes)'
  }
  check_environment_file() {
    [[ "$1" == "ascendany-admin-bootstrap.service" && "$2" == "/etc/ascendany/v2/ascendanyd.env" ]]
  }
  failures=0
  check_unit_optional_environment_files ascendany-admin-bootstrap.service /etc/ascendany/v2/ascendanyd.env
  [[ "$failures" == "0" ]]

  psql() {
    : >"$fixture_root/staged-psql-called"
  }
  release_payload_verified=0
  validation_phase=staged
  unset PGPASSFILE
  failures=0
  check_database_role
  [[ "$failures" == "1" ]]
  [[ ! -e "$fixture_root/staged-psql-called" ]]

  grep -F 'if ! check_release_directory_metadata "$release_root" "release root"; then' \
    "$validator" >/dev/null
  grep -F 'check_release_directory_metadata "$release_root/$relative" "release directory $relative" || true' \
    "$validator" >/dev/null

  release_directory_fixture="$fixture_root/release-directory-real/root"
  mkdir -p "$release_directory_fixture/bin"
  chmod 0755 "$fixture_root/release-directory-real" "$release_directory_fixture" \
    "$release_directory_fixture/bin"
  release_directory_owner_group='0:0'
  stat() {
    if [[ "$1" == "-Lc" && "$2" == "%u:%g:%a" ]]; then
      printf '%s:%s\n' "$release_directory_owner_group" \
        "$(command stat -Lc '%a' -- "${!#}")"
      return
    fi
    command stat "$@"
  }

  failures=0
  check_release_directory_metadata "$release_directory_fixture" 'release root fixture'
  check_release_directory_metadata "$release_directory_fixture/bin" 'release bin fixture'
  [[ "$failures" == "0" ]]

  chmod 0700 "$release_directory_fixture"
  check_release_directory_metadata "$release_directory_fixture" 'release root fixture' || true
  [[ "$failures" == "1" ]]
  chmod 0755 "$release_directory_fixture"

  failures=0
  chmod 0700 "$release_directory_fixture/bin"
  check_release_directory_metadata "$release_directory_fixture/bin" 'release bin fixture' || true
  [[ "$failures" == "1" ]]
  chmod 0755 "$release_directory_fixture/bin"

  failures=0
  release_directory_owner_group='1:0'
  check_release_directory_metadata "$release_directory_fixture" 'release root fixture' || true
  [[ "$failures" == "1" ]]

  failures=0
  release_directory_owner_group='0:1'
  check_release_directory_metadata "$release_directory_fixture/bin" 'release bin fixture' || true
  [[ "$failures" == "1" ]]
  release_directory_owner_group='0:0'

  ln -s "$fixture_root/release-directory-real" "$fixture_root/release-directory-ancestor-link"
  failures=0
  check_release_directory_metadata \
    "$fixture_root/release-directory-ancestor-link/root" 'release root fixture' || true
  [[ "$failures" == "1" ]]

  mv "$release_directory_fixture/bin" "$release_directory_fixture/real-bin"
  ln -s real-bin "$release_directory_fixture/bin"
  failures=0
  check_release_directory_metadata "$release_directory_fixture/bin" 'release bin fixture' || true
  [[ "$failures" == "1" ]]

  for native_unit_contract in \
    "$cloudflared_unit|AssertFileIsExecutable=/usr/bin/cloudflared|AssertPathExists=/opt/ascendany/v2/config/cloudflared.yaml|LoadCredentialEncrypted=tunnel_credentials:/etc/ascendany/credentials/cloudflare_tunnel_credentials.cred" \
    "$pgbouncer_unit_source|AssertFileIsExecutable=/usr/bin/pgbouncer|AssertPathExists=/opt/ascendany/infra/pgbouncer/pgbouncer.ini|LoadCredentialEncrypted=pgbouncer_userlist:/etc/ascendany/credentials/pgbouncer_userlist.cred"; do
    IFS='|' read -r native_unit executable_assert config_assert credential_directive \
      <<<"$native_unit_contract"
    [[ "$(grep -Fxc -- "$executable_assert" "$native_unit")" == "1" ]]
    [[ "$(grep -Fxc -- "$config_assert" "$native_unit")" == "1" ]]
    [[ "$(grep -Fxc -- "$credential_directive" "$native_unit")" == "1" ]]
  done
  [[ "$(grep -Fxc -- 'AssertPathExists=/opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf' \
    "$pgbouncer_unit_source")" == "1" ]]

  pgbouncer_config_root="$fixture_root/pgbouncer"
  pgbouncer_runtime_credential="$fixture_root/pgbouncer-userlist.runtime"
  pgbouncer_binary="$fixture_root/native-pgbouncer-fixture"
  printf '%s\n' \
    'package main' \
    '' \
    'import (' \
    '  "fmt"' \
    '  "os"' \
    '  "os/signal"' \
    '  "syscall"' \
    ')' \
    '' \
    'func main() {' \
    '  if len(os.Args) == 2 && os.Args[1] == "--version" {' \
    '    fmt.Println("PgBouncer 1.25.2")' \
    '    return' \
    '  }' \
    '  if len(os.Args) != 3 || os.Args[1] != "-q" {' \
    '    os.Exit(64)' \
    '  }' \
    '  signals := make(chan os.Signal, 1)' \
    '  signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)' \
    '  <-signals' \
    '}' \
    >"$fixture_root/native-pgbouncer-fixture.go"
  env -i \
    PATH=/usr/bin:/bin \
    HOME="${HOME:-/tmp}" \
    GOTOOLCHAIN=local \
    GOENV=off \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOAMD64=v1 \
    "$go_binary" build -buildvcs=false -trimpath \
      -o "$pgbouncer_binary" "$fixture_root/native-pgbouncer-fixture.go"
  chmod 0755 "$pgbouncer_binary"
  pgbouncer_binary_size="$(command stat -Lc '%s' "$pgbouncer_binary")"
  pgbouncer_binary_sha256="$(sha256sum "$pgbouncer_binary" | command awk '{print $1}')"
  mkdir -p "$pgbouncer_config_root"
  printf '%s\n' \
    '"ascendanyd_login" "SCRAM-SHA-256$4096:c2FsdDI=$c3RvcmVkMg==:c2VydmVyMg=="' \
    >"$pgbouncer_runtime_credential"
  printf '%s\n' \
    'host ascendany_v2 ascendanyd_login 127.0.0.1/32 scram-sha-256' \
    'host all all 0.0.0.0/0 reject' \
    >"$pgbouncer_config_root/pgbouncer-hba.conf"
  printf '%s\n' \
    '[databases]' \
    'ascendany_v2 = host=127.0.0.1 port=5432 dbname=ascendany_v2' \
    '' \
    '[pgbouncer]' \
    'listen_addr = 127.0.0.1' \
    'listen_port = 0' \
    'unix_socket_dir =' \
    'auth_type = hba' \
    "auth_file = $pgbouncer_runtime_credential" \
    "auth_hba_file = $pgbouncer_config_root/pgbouncer-hba.conf" \
    'pool_mode = transaction' \
    'logfile =' \
    'pidfile =' \
    >"$pgbouncer_config_root/pgbouncer.ini"
  chmod 0755 "$pgbouncer_config_root"
  chmod 0644 "$pgbouncer_config_root/pgbouncer.ini" \
    "$pgbouncer_config_root/pgbouncer-hba.conf"
  chmod 0440 "$pgbouncer_runtime_credential"

  env -i PATH=/usr/bin:/bin LC_ALL=C \
    "$pgbouncer_binary" -q "$pgbouncer_config_root/pgbouncer.ini" &
  pgbouncer_fixture_pid=$!
  cleanup_pgbouncer_process() {
    kill "$pgbouncer_fixture_pid" 2>/dev/null || true
    wait "$pgbouncer_fixture_pid" 2>/dev/null || true
    pgbouncer_fixture_pid=""
  }
  trap cleanup_pgbouncer_process EXIT
  for _ in {1..50}; do
    if [[ "$(readlink -e -- "/proc/$pgbouncer_fixture_pid/exe" 2>/dev/null || true)" == \
          "$pgbouncer_binary" ]]; then
      break
    fi
    kill -0 "$pgbouncer_fixture_pid" 2>/dev/null
    sleep 0.02
  done
  [[ "$(readlink -e -- "/proc/$pgbouncer_fixture_pid/exe" 2>/dev/null || true)" == \
    "$pgbouncer_binary" ]]

  pgbouncer_metadata_drift=0
  pgbouncer_package_drift=0
  pgbouncer_container_conflict=0
  pgbouncer_unit_state_drift=0
  pgbouncer_fragment_drift=0
  pgbouncer_dropin_drift=0
  pgbouncer_process_security_drift=0
  pgbouncer_credential_source_drift=0
  stat() {
    local target="${!#}"
    if [[ "$target" == "$pgbouncer_config_root" ]]; then
      if [[ "$pgbouncer_metadata_drift" == 1 ]]; then
        printf '%s\n' '0:0:775'
      else
        printf '%s\n' '0:0:755'
      fi
      return
    fi
    if [[ "$target" == "$pgbouncer_config_root/"* ]]; then
      printf '%s\n' '0:0:644:1'
      return
    fi
    if [[ "$target" == "$pgbouncer_runtime_credential" ]]; then
      printf '%s\n' '0:0:440:1'
      return
    fi
    if [[ "$target" == "$pgbouncer_binary" ]]; then
      printf '0:0:755:%s:1\n' "$pgbouncer_binary_size"
      return
    fi
    command stat "$@"
  }
  check_root_owned_ancestry() { return 0; }
  check_credential_source() {
    if [[ "$pgbouncer_credential_source_drift" == 1 ]]; then
      fail 'fixture encrypted credential source drifted'
      return 1
    fi
    return 0
  }
  rpm() {
    if [[ "$1" == -q ]]; then
      if [[ "$pgbouncer_package_drift" == 1 ]]; then
        printf '%s\n' 'pgbouncer-1.25.1-1.fc44.x86_64'
      else
        printf '%s\n' "$pgbouncer_nevra"
      fi
      return
    fi
    [[ "$1" == --verify && "$2" == pgbouncer ]]
  }
  podman() {
    [[ "$1" == ps && "$2" == -a ]] || return 1
    if [[ "$pgbouncer_container_conflict" == 1 ]]; then
      printf '%s\n' 'ascendany-pgbouncer-conflict-fixture'
    fi
  }
  render_pgbouncer_unit() {
    if [[ "$pgbouncer_unit_state_drift" == 1 ]]; then
      sed 's|^DynamicUser=yes$|DynamicUser=no|' "$pgbouncer_unit_source"
    else
      cat "$pgbouncer_unit_source"
    fi
    printf '%s\n' '[Service]' 'TimeoutStopFailureMode=abort'
  }
  systemctl() {
    case "$1:$2" in
      is-enabled:pgbouncer.service) printf '%s\n' masked ;;
      is-active:pgbouncer.service) printf '%s\n' inactive ;;
      is-enabled:ascendany-pgbouncer.service) printf '%s\n' enabled ;;
      is-active:ascendany-pgbouncer.service) printf '%s\n' active ;;
      cat:ascendany-pgbouncer.service) render_pgbouncer_unit ;;
      *) return 1 ;;
    esac
  }
  unit_property() {
    local unit="$1" property="$2"
    if [[ "$unit" == pgbouncer.service && "$property" == MainPID ]]; then
      printf '%s\n' 0
      return
    fi
    [[ "$unit" == ascendany-pgbouncer.service ]] || return 1
    case "$property" in
      FragmentPath)
        if [[ "$pgbouncer_fragment_drift" == 1 ]]; then
          printf '%s\n' /run/systemd/system/ascendany-pgbouncer.service
        else
          printf '%s\n' /etc/systemd/system/ascendany-pgbouncer.service
        fi
        ;;
      DropInPaths)
        if [[ "$pgbouncer_dropin_drift" == 1 ]]; then
          printf '%s\n' \
            /usr/lib/systemd/system/service.d/10-timeout-abort.conf \
            /run/systemd/system/ascendany-pgbouncer.service.d/90-override.conf
        else
          printf '%s\n' /usr/lib/systemd/system/service.d/10-timeout-abort.conf
        fi
        ;;
      NeedDaemonReload) printf '%s\n' no ;;
      DynamicUser)
        if [[ "$pgbouncer_unit_state_drift" == 1 ]]; then printf '%s\n' no; else printf '%s\n' yes; fi
        ;;
      User|Group) printf '%s\n' ascendany-pgbouncer ;;
      MainPID) printf '%s\n' "$pgbouncer_fixture_pid" ;;
      WorkingDirectory|EnvironmentFiles|CapabilityBoundingSet|AmbientCapabilities) printf '%s' '' ;;
      Type) printf '%s\n' notify-reload ;;
      KillSignal) printf '%s\n' 2 ;;
      NoNewPrivileges|PrivateTmp|PrivateDevices|ProtectSystem|ProtectHome|ProtectControlGroups|ProtectKernelTunables|ProtectKernelModules|ProtectKernelLogs|ProtectClock|ProtectHostname|RestrictNamespaces|RestrictRealtime|RestrictSUIDSGID|LockPersonality|MemoryDenyWriteExecute|RemoveIPC)
        case "$property" in
          ProtectSystem) printf '%s\n' strict ;;
          *) printf '%s\n' yes ;;
        esac
        ;;
      ProtectProc) printf '%s\n' invisible ;;
      ProcSubset) printf '%s\n' pid ;;
      KeyringMode) printf '%s\n' private ;;
      DevicePolicy) printf '%s\n' closed ;;
      RestrictAddressFamilies) printf '%s\n' 'AF_UNIX AF_INET' ;;
      *) return 1 ;;
    esac
  }
  awk() {
    local arguments="$*" target="${!#}"
    if [[ "$target" == "/proc/$pgbouncer_fixture_pid/status" ]]; then
      case "$arguments" in
        *'Uid:'*) printf '%s\n' '1000 1000 1000 1000' ;;
        *'Gid:'*) printf '%s\n' '1000 1000 1000 1000' ;;
        *'NoNewPrivs:'*)
          if [[ "$pgbouncer_process_security_drift" == 1 ]]; then printf '%s\n' 0; else printf '%s\n' 1; fi
          ;;
        *'Seccomp:'*) printf '%s\n' 2 ;;
        *'CapInh:'*|*'CapPrm:'*|*'CapEff:'*|*'CapBnd:'*|*'CapAmb:'*)
          printf '%s\n' 0000000000000000
          ;;
        *) command awk "$@" ;;
      esac
      return
    fi
    command awk "$@"
  }
  pgbouncer_probe_mode=valid
  run_pgbouncer_rejection_psql() {
    if [[ "$pgbouncer_probe_mode" == valid ]]; then
      printf '%s\n' 'psql: error: connection to server at "127.0.0.1", port 6432 failed: FATAL:  login rejected' >&2
    else
      printf '%s\n' 'psql: error: connection failed: no password supplied' >&2
    fi
    return 2
  }

  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "0" ]]

  cp "$pgbouncer_runtime_credential" "$fixture_root/userlist.valid"
  chmod 0640 "$pgbouncer_runtime_credential"
  printf '%s\n' '"ascendanyd_login" "plaintext"' \
    >"$pgbouncer_runtime_credential"
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  cp "$fixture_root/userlist.valid" "$pgbouncer_runtime_credential"
  chmod 0440 "$pgbouncer_runtime_credential"

  pgbouncer_package_drift=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_package_drift=0

  pgbouncer_container_conflict=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_container_conflict=0

  pgbouncer_metadata_drift=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_metadata_drift=0

  pgbouncer_unit_state_drift=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_unit_state_drift=0

  pgbouncer_fragment_drift=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_fragment_drift=0

  pgbouncer_dropin_drift=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_dropin_drift=0

  pgbouncer_credential_source_drift=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_credential_source_drift=0

  pgbouncer_process_security_drift=1
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "1" ]]
  pgbouncer_process_security_drift=0

  pgbouncer_probe_mode=wrong
  failures=0
  check_pgbouncer_contract
  [[ "$failures" == "2" ]]

  grep -F 'config/pgbouncer-hba.conf' "$validator" >/dev/null
  grep -F '/opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf' "$validator" >/dev/null
  grep -F 'config/pgbouncer.ini' "$validator" >/dev/null
  grep -F '/opt/ascendany/infra/pgbouncer/pgbouncer.ini' "$validator" >/dev/null

  release_copy_fixture="$fixture_root/release-copy"
  install -d -m 0755 \
    "$release_copy_fixture/release/config" \
    "$release_copy_fixture/installed"
  printf '%s\n' 'reviewed native PgBouncer config bytes' \
    >"$release_copy_fixture/release/config/pgbouncer.ini"
  cp "$release_copy_fixture/release/config/pgbouncer.ini" \
    "$release_copy_fixture/installed/pgbouncer.ini"
  chmod 0644 \
    "$release_copy_fixture/release/config/pgbouncer.ini" \
    "$release_copy_fixture/installed/pgbouncer.ini"
  release_root="$release_copy_fixture/release"
  stat() {
    local target="${!#}"
    if [[ "$1" == -Lc && "$2" == '%u:%g:%a' && \
          "$target" == "$release_copy_fixture/"* ]]; then
      printf '%s\n' '0:0:644'
      return
    fi
    command stat "$@"
  }
  failures=0
  check_installed_release_copy \
    config/pgbouncer.ini "$release_copy_fixture/installed/pgbouncer.ini" 0
  [[ "$failures" == "0" ]]
  printf '%s\n' 'drifted native PgBouncer config bytes' \
    >>"$release_copy_fixture/installed/pgbouncer.ini"
  check_installed_release_copy \
    config/pgbouncer.ini "$release_copy_fixture/installed/pgbouncer.ini" 0
  [[ "$failures" == "1" ]]

  installed_release_copy_contract="$fixture_root/installed-release-copy-contract"
  check_installed_release_copy() {
    printf '%s|%s|%s\n' "$1" "$2" "$3" >>"$installed_release_copy_contract"
  }
  validation_phase=production
  : >"$installed_release_copy_contract"
  check_installed_release_inputs
  cat >"$fixture_root/expected-installed-release-copy-contract" <<'CONTRACT'
systemd/ascendany-cloudflared.service|/etc/systemd/system/ascendany-cloudflared.service|1
systemd/ascendanyd.service|/etc/systemd/system/ascendanyd.service|1
systemd/ascendany-model-register.service|/etc/systemd/system/ascendany-model-register.service|1
systemd/ascendany-model-activate.service|/etc/systemd/system/ascendany-model-activate.service|1
systemd/ascendany-catalog-publish.service|/etc/systemd/system/ascendany-catalog-publish.service|1
systemd/ascendany-admin-bootstrap.service|/etc/systemd/system/ascendany-admin-bootstrap.service|1
systemd/ascendany-backup.service|/etc/systemd/system/ascendany-backup.service|1
systemd/ascendany-backup.timer|/etc/systemd/system/ascendany-backup.timer|1
systemd/ascendany-judge@.service|/etc/systemd/system/ascendany-judge@.service|1
systemd/ascendany-lsp@.service|/etc/systemd/system/ascendany-lsp@.service|1
systemd/ascendany-migrate.service|/etc/systemd/system/ascendany-migrate.service|1
systemd/ascendany-pgbouncer.service|/etc/systemd/system/ascendany-pgbouncer.service|1
systemd/ascendany-restore-verify@.service|/etc/systemd/system/ascendany-restore-verify@.service|1
polkit-1/rules.d/60-ascendany-judge.rules|/etc/polkit-1/rules.d/60-ascendany-judge.rules|1
polkit-1/rules.d/61-ascendany-lsp.rules|/etc/polkit-1/rules.d/61-ascendany-lsp.rules|1
sysusers.d/ascendany-v2.conf|/etc/sysusers.d/ascendany-v2.conf|1
tmpfiles.d/ascendany-v2.conf|/etc/tmpfiles.d/ascendany-v2.conf|1
config/analytics.json|/etc/ascendany/v2/analytics.json|0
config/ascendanyd.env|/etc/ascendany/v2/ascendanyd.env|0
config/ascendanyd-read-only-smoke.env|/etc/ascendany/v2/ascendanyd-read-only-smoke.env|0
config/backup.env|/etc/ascendany/v2/backup.env|0
config/catalog-publish.env|/etc/ascendany-catalog-publisher/catalog-publish.env|0
config/judge.env|/etc/ascendany/v2/judge.env|0
config/migrate.env|/etc/ascendany/v2/migrate.env|0
config/pgbouncer-hba.conf|/opt/ascendany/infra/pgbouncer/pgbouncer-hba.conf|0
config/pgbouncer.ini|/opt/ascendany/infra/pgbouncer/pgbouncer.ini|0
config/restore.env|/etc/ascendany/v2/restore.env|0
CONTRACT
  cmp --silent -- \
    "$fixture_root/expected-installed-release-copy-contract" \
    "$installed_release_copy_contract"
)

printf 'PASS: phase validator closes write mode, raw unit directives, health, environments, credentials, and drop-ins\n'
