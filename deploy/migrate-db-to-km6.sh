#!/usr/bin/env bash
set -euo pipefail

if [ "${ASCENDANY_MIGRATE_CONFIRM:-}" != "AscendAny-to-km6" ]; then
  echo "Set ASCENDANY_MIGRATE_CONFIRM=AscendAny-to-km6 before running this migration." >&2
  exit 1
fi

remote="${ASCENDANY_DEPLOY_REMOTE:-km6}"
local_container="${ASCENDANY_LOCAL_POSTGRES_CONTAINER:-postgres_postgres_1}"
remote_container="${ASCENDANY_REMOTE_POSTGRES_CONTAINER:-ascendany-postgres}"
local_db="${ASCENDANY_LOCAL_DB_NAME:-AscendAny}"
local_user="${ASCENDANY_LOCAL_DB_USER:-AscendAny}"
remote_db="${ASCENDANY_REMOTE_DB_NAME:-AscendAny}"
remote_user="${ASCENDANY_REMOTE_DB_USER:-AscendAny}"

podman start "${local_container}" >/dev/null
podman container exists "${local_container}"
ssh -o BatchMode=yes "${remote}" "podman container exists '${remote_container}'"

echo "Migrating ${local_container}:${local_db} to ${remote}:${remote_container}:${remote_db}" >&2

podman exec "${local_container}" \
  pg_dump -Fc --no-owner --no-privileges -U "${local_user}" -d "${local_db}" \
  | ssh -o BatchMode=yes "${remote}" \
      "podman exec -i '${remote_container}' pg_restore --clean --if-exists --no-owner --no-privileges -U '${remote_user}' -d '${remote_db}'"

echo "Database migration finished."
