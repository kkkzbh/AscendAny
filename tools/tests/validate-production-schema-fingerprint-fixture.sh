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
[[ "${#migrations[@]}" == 7 ]] || fail 'migration fixture requires exactly schema versions 1 through 7'
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

baseline_stream="$WORK_ROOT/baseline.stream"
schema_stream >"$baseline_stream"
for record_kind in contract column constraint index trigger routine; do
  grep -q "^${record_kind}|" "$baseline_stream" ||
    fail "canonical stream omits $record_kind records"
done
baseline_sha256="$(sha256sum -- "$baseline_stream" | awk '{print $1}')"
"$HELPER" --verify-sha256 "$baseline_sha256" ||
  fail "fresh schema-v7 digest differs from the embedded expected SHA-256: $baseline_sha256"
production_validator_accepts_schema ||
  fail 'production validator rejected the fresh canonical schema-v7 fingerprint'

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
  fail 'same-name column ALTER passed the embedded schema-v7 fingerprint gate'
fi
if production_validator_accepts_schema; then
  fail 'production validator accepted the same-name column ALTER'
fi

printf 'production PostgreSQL 17 schema fingerprint fixture: PASS\n'
