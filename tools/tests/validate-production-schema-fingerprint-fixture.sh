#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly HELPER="$REPOSITORY_ROOT/deploy/v2/scripts/postgres-schema-fingerprint.sh"
readonly VALIDATOR="$REPOSITORY_ROOT/deploy/v2/scripts/validate-production.sh"
readonly ROLE_BOOTSTRAP="$REPOSITORY_ROOT/db/roles/001_v2_roles.sql"
readonly MIGRATION_ROOT="$REPOSITORY_ROOT/backend/internal/migrate/migrations"
readonly POSTGRES_IMAGE="${ASCENDANY_SCHEMA_FINGERPRINT_POSTGRES_IMAGE:-docker.io/library/postgres@sha256:5c855ad7b85e68e48a62f34662853f38b57c1c1d80f3a927ab58034fd6d31c5e}"
readonly POSTGRES_IMAGE_ID='07f76768a0c956d6e9bddbcdb3c2be7fd9fd45ee6174a26873f8219fccbad65d'
readonly POSTGRES_IMAGE_DIGEST='sha256:5c855ad7b85e68e48a62f34662853f38b57c1c1d80f3a927ab58034fd6d31c5e'
readonly CONTAINER="ascendany-schema-fingerprint-fixture-$RANDOM-$$"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-schema-fingerprint.XXXXXX")"

cleanup() {
  podman rm --force "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf -- "$WORK_ROOT"
}
trap cleanup EXIT

fail() {
  printf 'FAIL [schema fingerprint fixture]: %s\n' "$1" >&2
  exit 1
}

for command in awk basename cat cmp find grep mktemp podman sha256sum sleep sort stat; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is missing: $command"
done
[[ -x "$HELPER" && ! -L "$HELPER" ]] || fail 'schema fingerprint helper is not executable'
[[ -x "$VALIDATOR" && ! -L "$VALIDATOR" ]] || fail 'production validator is not executable'
[[ -f "$ROLE_BOOTSTRAP" && ! -L "$ROLE_BOOTSTRAP" ]] || fail 'role bootstrap is unavailable'
podman image exists "$POSTGRES_IMAGE" || fail "pinned PostgreSQL 17 image is unavailable: $POSTGRES_IMAGE"
image_identity="$(podman image inspect "$POSTGRES_IMAGE" --format '{{.Id}}|{{.Digest}}')"
[[ "$image_identity" == "$POSTGRES_IMAGE_ID|$POSTGRES_IMAGE_DIGEST" ]] ||
  fail "PostgreSQL fixture image identity differs from the production pin: $image_identity"

postgres_psql() {
  podman exec -i --user postgres "$CONTAINER" \
    /usr/bin/env -i \
      HOME=/var/lib/postgresql \
      PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
      LC_ALL=C \
      /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
        --username=postgres "$@"
}

schema_stream() {
  "$HELPER" --emit-sql |
    postgres_psql --dbname=ascendany_v2 --tuples-only --no-align --quiet |
    LC_ALL=C sort
}

production_validator_accepts_schema() (
  # shellcheck source=../../deploy/v2/scripts/validate-production.sh
  source "$VALIDATOR"
  release_root="$REPOSITORY_ROOT/deploy/v2"
  failures=0
  postgres_admin_psql() {
    postgres_psql "$@"
  }
  check_postgres_schema_fingerprint >/dev/null 2>&1
  (( failures == 0 ))
)

podman run --detach \
  --name "$CONTAINER" \
  --network none \
  --http-proxy=false \
  --env POSTGRES_HOST_AUTH_METHOD=trust \
  --tmpfs /var/lib/postgresql/data:rw,nosuid,nodev,size=1g \
  "$POSTGRES_IMAGE" \
  postgres -c password_encryption=scram-sha-256 >/dev/null

for attempt in {1..120}; do
  if podman exec --user postgres "$CONTAINER" /bin/sh -ceu '
      test "$(cat /proc/1/comm)" = postgres
      /usr/bin/pg_isready --username=postgres --dbname=postgres
    ' >/dev/null 2>&1; then
    break
  fi
  [[ "$attempt" != 120 ]] || fail 'PostgreSQL 17 fixture did not become ready'
  sleep 0.1
done

server_major="$(postgres_psql --dbname=postgres --tuples-only --no-align --quiet \
  --command="SELECT current_setting('server_version_num')::integer / 10000")"
[[ "$server_major" == 17 ]] || fail "fixture server major is $server_major, expected 17"

postgres_psql --dbname=postgres >/dev/null <<'SQL'
CREATE ROLE ascendany_database_owner NOLOGIN;
CREATE DATABASE ascendany_v2 OWNER ascendany_database_owner TEMPLATE template0;
SQL
postgres_psql --dbname=ascendany_v2 <"$ROLE_BOOTSTRAP" >/dev/null

mapfile -t migrations < <(find "$MIGRATION_ROOT" -mindepth 1 -maxdepth 1 \
  -type f -name '[0-9][0-9][0-9][0-9]_*.sql' -print | LC_ALL=C sort)
[[ "${#migrations[@]}" == 10 ]] || fail 'migration fixture requires exactly schema versions 1 through 10'
expected_version=1
for migration in "${migrations[@]}"; do
  filename="$(basename -- "$migration")"
  version_text="${filename%%_*}"
  version="$((10#$version_text))"
  [[ "$version" == "$expected_version" ]] ||
    fail "migration fixture is not contiguous at version $expected_version: $filename"
  name="${filename#*_}"
  name="${name%.sql}"
  sha256="$(sha256sum -- "$migration" | awk '{print $1}')"
  {
    printf '%s\n' 'BEGIN;' 'SET LOCAL ROLE ascendany_owner;'
    cat -- "$migration"
    printf "INSERT INTO ascendany.schema_migrations_v2 (version, name, sha256) VALUES (%d, '%s', '%s');\n" \
      "$version" "$name" "$sha256"
    printf '%s\n' 'COMMIT;'
  } | postgres_psql --dbname=ascendany_v2 >/dev/null
  (( expected_version += 1 ))
done

# The production role bootstrap is deliberately rerunnable only for the exact
# embedded migration history. Reapply it here so hash/name/version drift in its
# schema-v10 boundary fails the real PostgreSQL fixture.
postgres_psql --dbname=ascendany_v2 <"$ROLE_BOOTSTRAP" >/dev/null

fresh_inventory="$(postgres_psql --dbname=ascendany_v2 \
  --tuples-only --no-align --quiet <<'SQL'
SELECT
  (
    SELECT count(*)::text
    FROM pg_class AS relation
    JOIN pg_namespace AS namespace
      ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany'
      AND relation.relkind IN ('r', 'p')
  ) || '|' || (
    SELECT count(*)::text
    FROM pg_class AS relation
    JOIN pg_namespace AS namespace
      ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'ascendany'
      AND relation.relkind = 'S'
  ) || '|' ||
  (SELECT count(*)::text FROM ascendany.schema_migrations_v2) || '|' ||
  (SELECT max(version)::text FROM ascendany.schema_migrations_v2);
SQL
)"
[[ "$fresh_inventory" == '54|31|10|10' ]] ||
  fail "fresh schema-v10 inventory differs from 54 base tables, 31 sequences, and 10 migrations: $fresh_inventory"

auto_analysis_column_contract_count="$(postgres_psql --dbname=ascendany_v2 \
  --tuples-only --no-align --quiet <<'SQL'
SELECT count(*)
FROM pg_attribute AS attribute
WHERE attribute.attrelid = 'ascendany.agent_runs'::regclass
  AND attribute.attnum > 0
  AND NOT attribute.attisdropped
  AND (
    (
      attribute.attname = 'auto_analysis_exam_id'
      AND attribute.atttypid = 'uuid'::regtype
      AND attribute.atttypmod = -1
      AND NOT attribute.attnotnull
    )
    OR (
      attribute.attname = 'auto_analysis_role_id'
      AND attribute.atttypid = 'text'::regtype
      AND attribute.atttypmod = -1
      AND NOT attribute.attnotnull
    )
  );
SQL
)"
[[ "$auto_analysis_column_contract_count" == 2 ]] ||
  fail 'schema v10 omits the exact automatic-analysis identity columns'

auto_analysis_constraint_contract_count="$(postgres_psql --dbname=ascendany_v2 \
  --tuples-only --no-align --quiet <<'SQL'
SELECT count(*)
FROM pg_constraint AS constraint_row
WHERE constraint_row.conrelid = 'ascendany.agent_runs'::regclass
  AND (
    (
      constraint_row.conname = 'agent_runs_auto_analysis_exam_fk'
      AND constraint_row.contype = 'f'
      AND constraint_row.confrelid = 'ascendany.logical_exams'::regclass
      AND constraint_row.confdeltype = 'r'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE
        'FOREIGN KEY (auto_analysis_exam_id) REFERENCES ascendany.logical_exams(public_id) ON DELETE RESTRICT%'
    )
    OR (
      constraint_row.conname = 'agent_runs_kind_input_consistent'
      AND constraint_row.contype = 'c'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE '%auto_analysis_exam_id IS NULL%'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE '%auto_analysis_role_id IS NULL%'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE '%analytics_generation_id IS NOT NULL%'
    )
    OR (
      constraint_row.conname = 'agent_runs_auto_analysis_identity_consistent'
      AND constraint_row.contype = 'c'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE '%auto_analysis_exam_id IS NOT NULL%'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE '%auto_analysis_role_id = btrim(auto_analysis_role_id)%'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE '%octet_length(auto_analysis_role_id) >= 1%'
      AND pg_get_constraintdef(constraint_row.oid, true) LIKE '%octet_length(auto_analysis_role_id) <= 256%'
    )
  );
SQL
)"
[[ "$auto_analysis_constraint_contract_count" == 3 ]] ||
  fail 'schema v10 omits the exact automatic-analysis identity constraints'

auto_analysis_index_contract_count="$(postgres_psql --dbname=ascendany_v2 \
  --tuples-only --no-align --quiet <<'SQL'
SELECT count(*)
FROM pg_index AS index_row
JOIN pg_class AS index_relation
  ON index_relation.oid = index_row.indexrelid
WHERE index_row.indrelid = 'ascendany.agent_runs'::regclass
  AND index_relation.relname = 'agent_runs_owner_exam_role_auto_analysis_unique'
  AND index_row.indisunique
  AND NOT index_row.indisprimary
  AND index_row.indisvalid
  AND index_row.indisready
  AND index_row.indnkeyatts = 3
  AND pg_get_indexdef(index_row.indexrelid, 1, true) = 'owner_account_id'
  AND pg_get_indexdef(index_row.indexrelid, 2, true) = 'auto_analysis_exam_id'
  AND pg_get_indexdef(index_row.indexrelid, 3, true) = 'auto_analysis_role_id'
  AND pg_get_expr(index_row.indpred, index_row.indrelid, true) = 'run_kind = ''auto_analysis''::text';
SQL
)"
[[ "$auto_analysis_index_contract_count" == 1 ]] ||
  fail 'schema v10 omits the exact automatic-analysis owner/exam/role uniqueness index'

old_auto_analysis_index_count="$(postgres_psql --dbname=ascendany_v2 \
  --tuples-only --no-align --quiet \
  --command="SELECT count(*) FROM pg_class WHERE oid = to_regclass('ascendany.agent_runs_owner_analytics_auto_analysis_unique')")"
[[ "$old_auto_analysis_index_count" == 0 ]] ||
  fail 'schema v10 retained the analytics-generation automatic-analysis uniqueness index'

feedback_attachment_digest_constraint_count="$(postgres_psql --dbname=ascendany_v2 \
  --tuples-only --no-align --quiet \
  --command="SELECT count(*) FROM pg_constraint WHERE conrelid = 'ascendany.feedback_attachments'::regclass AND conname = 'feedback_attachments_feedback_artifact_unique'")"
[[ "$feedback_attachment_digest_constraint_count" == 0 ]] ||
  fail 'schema v10 retained the feedback attachment digest uniqueness constraint'

feedback_attachment_digest_index_count="$(postgres_psql --dbname=ascendany_v2 \
  --tuples-only --no-align --quiet \
  --command="SELECT count(*) FROM pg_class WHERE oid = to_regclass('ascendany.feedback_attachments_feedback_artifact_unique')")"
[[ "$feedback_attachment_digest_index_count" == 0 ]] ||
  fail 'schema v10 retained the feedback attachment digest uniqueness index'

baseline_stream="$WORK_ROOT/baseline.stream"
schema_stream >"$baseline_stream"
for record_kind in contract column constraint index trigger routine; do
  grep -q "^${record_kind}|" "$baseline_stream" ||
    fail "canonical stream omits $record_kind records"
done
baseline_sha256="$(sha256sum -- "$baseline_stream" | awk '{print $1}')"
"$HELPER" --verify-sha256 "$baseline_sha256" ||
	  fail "fresh schema-v10 digest differs from the embedded expected SHA-256: $baseline_sha256"
production_validator_accepts_schema ||
	  fail 'production validator rejected the fresh canonical schema-v10 fingerprint'

postgres_psql --dbname=ascendany_v2 >/dev/null <<'SQL'
BEGIN;
SET LOCAL ROLE ascendany_owner;

INSERT INTO ascendany.pintia_actors (user_id)
VALUES ('schema-v10-auto-analysis-fixture');

INSERT INTO ascendany.pintia_actor_identifiers (
  identifier_kind,
  identifier_value,
  actor_id
)
SELECT 'student_number', 'schema-v10-student', actor_id
FROM ascendany.pintia_actors
WHERE user_id = 'schema-v10-auto-analysis-fixture';

INSERT INTO ascendany.auth_accounts (
  public_id,
  username,
  password_phc,
  display_name,
  student_number,
  pta_nickname,
  actor_id,
  role,
  auth_revision,
  created_at,
  updated_at
)
SELECT
  '00000000-0000-4000-8000-000000000801'::uuid,
	  'schema_v10_student',
  'fixture-password-phc',
	  'Schema V10 Student',
	  'schema-v10-student',
  'pta-fixture',
  actor_id,
  'student',
  1,
  clock_timestamp(),
  clock_timestamp()
FROM ascendany.pintia_actors
WHERE user_id = 'schema-v10-auto-analysis-fixture';

INSERT INTO ascendany.auth_sessions (
  public_id,
  account_id,
  auth_revision,
  created_at,
  expires_at,
  last_seen_at
)
SELECT
  '00000000-0000-4000-8000-000000000802'::uuid,
  account_id,
  1,
  clock_timestamp(),
  clock_timestamp() + interval '1 hour',
  clock_timestamp()
FROM ascendany.auth_accounts
WHERE public_id = '00000000-0000-4000-8000-000000000801'::uuid;

INSERT INTO ascendany.chat_threads (public_id, owner_account_id, thread_kind)
SELECT '00000000-0000-4000-8000-000000000803'::uuid, account_id, 'auto_analysis'
FROM ascendany.auth_accounts
WHERE public_id = '00000000-0000-4000-8000-000000000801'::uuid;

INSERT INTO ascendany.chat_threads (public_id, owner_account_id, thread_kind)
SELECT '00000000-0000-4000-8000-000000000804'::uuid, account_id, 'conversation'
FROM ascendany.auth_accounts
WHERE public_id = '00000000-0000-4000-8000-000000000801'::uuid;

INSERT INTO ascendany.chat_messages (
  public_id,
  chat_thread_id,
  owner_account_id,
  message_sequence,
  message_kind,
  content,
  author_session_id
)
SELECT
  '00000000-0000-4000-8000-000000000805'::uuid,
  thread.chat_thread_id,
  account.account_id,
  1,
  'auto_analysis_request',
	  $json${"context":{"latestExamId":"00000000-0000-4000-8000-000000000809","notes":"","notesLocked":false,"notesTitle":"","ptaNickname":"pta-fixture","roleId":"coach","roleName":"Coach","roleSystemPrompt":"Be concise.","studentId":"schema-v10-student"},"instruction":"Analyze the student's current published analytics snapshot and provide a concise, actionable progress review.","schema":"ascendany.agent.auto-analysis.frontend-context.v1"}$json$,
  session.session_id
FROM ascendany.chat_threads AS thread
JOIN ascendany.auth_accounts AS account
  ON account.account_id = thread.owner_account_id
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
WHERE thread.public_id = '00000000-0000-4000-8000-000000000803'::uuid;

INSERT INTO ascendany.chat_messages (
  public_id,
  chat_thread_id,
  owner_account_id,
  message_sequence,
  message_kind,
  content,
  author_session_id
)
SELECT
  '00000000-0000-4000-8000-000000000806'::uuid,
  thread.chat_thread_id,
  account.account_id,
  1,
  'user',
  'ordinary non-JSON chat content remains valid',
  session.session_id
FROM ascendany.chat_threads AS thread
JOIN ascendany.auth_accounts AS account
  ON account.account_id = thread.owner_account_id
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
WHERE thread.public_id = '00000000-0000-4000-8000-000000000804'::uuid;

DO $constraint$
DECLARE
  auto_thread_id bigint;
  account_id bigint;
  session_id bigint;
BEGIN
  SELECT thread.chat_thread_id, account.account_id, session.session_id
  INTO STRICT auto_thread_id, account_id, session_id
  FROM ascendany.chat_threads AS thread
  JOIN ascendany.auth_accounts AS account
    ON account.account_id = thread.owner_account_id
  JOIN ascendany.auth_sessions AS session
    ON session.account_id = account.account_id
  WHERE thread.public_id = '00000000-0000-4000-8000-000000000803'::uuid;

  BEGIN
    INSERT INTO ascendany.chat_messages (
      public_id, chat_thread_id, owner_account_id, message_sequence,
      message_kind, content, author_session_id
    ) VALUES (
      '00000000-0000-4000-8000-000000000807'::uuid,
      auto_thread_id,
      account_id,
      2,
      'auto_analysis_request',
      $legacy$Analyze the student's current published analytics snapshot and provide a concise, actionable progress review.$legacy$,
      session_id
    );
    RAISE EXCEPTION 'legacy automatic analysis content was accepted';
  EXCEPTION WHEN check_violation THEN
    NULL;
  END;

  BEGIN
    INSERT INTO ascendany.chat_messages (
      public_id, chat_thread_id, owner_account_id, message_sequence,
      message_kind, content, author_session_id
    ) VALUES (
      '00000000-0000-4000-8000-000000000808'::uuid,
      auto_thread_id,
      account_id,
      2,
      'auto_analysis_request',
	      $typed${"context":{"latestExamId":"00000000-0000-4000-8000-000000000809","notes":"","notesLocked":"false","notesTitle":"","ptaNickname":"pta-fixture","roleId":"coach","roleName":"Coach","roleSystemPrompt":"Be concise.","studentId":"schema-v10-student"},"instruction":"Analyze the student's current published analytics snapshot and provide a concise, actionable progress review.","schema":"ascendany.agent.auto-analysis.frontend-context.v1"}$typed$,
      session_id
    );
    RAISE EXCEPTION 'ill-typed automatic analysis context was accepted';
  EXCEPTION WHEN check_violation THEN
    NULL;
  END;
END
$constraint$;

ROLLBACK;
SQL

names_before="$WORK_ROOT/names.before"
postgres_psql --dbname=ascendany_v2 --tuples-only --no-align --quiet <<'SQL' \
  | LC_ALL=C sort >"$names_before"
SELECT namespace.nspname || '.' || relation.relname
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany'
  AND relation.relkind IN ('r', 'p');
SQL

postgres_psql --dbname=ascendany_v2 >/dev/null <<'SQL'
BEGIN;
SET LOCAL ROLE ascendany_owner;
ALTER TABLE ascendany.auth_accounts
ALTER COLUMN display_name DROP NOT NULL;
COMMIT;
SQL

names_after="$WORK_ROOT/names.after"
postgres_psql --dbname=ascendany_v2 --tuples-only --no-align --quiet <<'SQL' \
  | LC_ALL=C sort >"$names_after"
SELECT namespace.nspname || '.' || relation.relname
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany'
  AND relation.relkind IN ('r', 'p');
SQL
cmp --silent -- "$names_before" "$names_after" ||
  fail 'same-name ALTER unexpectedly changed the base-table name set'

mutated_sha256="$(schema_stream | sha256sum | awk '{print $1}')"
[[ "$mutated_sha256" != "$baseline_sha256" ]] ||
  fail 'same-name column ALTER did not change the canonical schema fingerprint'
if "$HELPER" --verify-sha256 "$mutated_sha256"; then
	  fail 'same-name column ALTER passed the embedded schema-v10 fingerprint gate'
fi
if production_validator_accepts_schema; then
  fail 'production validator accepted the same-name column ALTER'
fi

printf 'production PostgreSQL 17 schema fingerprint fixture: PASS\n'
