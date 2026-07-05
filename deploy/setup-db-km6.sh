#!/usr/bin/env bash
set -euo pipefail

remote="${ASCENDANY_DEPLOY_REMOTE:-km6}"
postgres_image="${ASCENDANY_POSTGRES_IMAGE:-docker.io/library/postgres:17}"
pgbouncer_image="${ASCENDANY_PGBOUNCER_IMAGE:-docker.io/pgbouncer/pgbouncer:latest}"

ssh -o BatchMode=yes "${remote}" "set -euo pipefail
  base=/opt/ascendany/infra
  mkdir -p \"\${base}/pgbouncer\"
  chmod 700 \"\${base}\"

  if [ ! -s \"\${base}/db_password\" ]; then
    umask 077
    python3 -c 'import secrets,string; print(\"\".join(secrets.choice(string.ascii_letters+string.digits) for _ in range(40)))' > \"\${base}/db_password\"
  fi

  db_password=\"\$(tr -d '\n' < \"\${base}/db_password\")\"
  if [ -z \"\${db_password}\" ]; then
    echo 'empty database password' >&2
    exit 1
  fi

  cat > \"\${base}/.env\" <<ENVEOF
DB_PASSWORD=\${db_password}
ENVEOF
  chmod 600 \"\${base}/.env\" \"\${base}/db_password\"

  pg_md5=\"\$(printf '%s%s' \"\${db_password}\" 'AscendAny' | md5sum | awk '{print \$1}')\"
  cat > \"\${base}/pgbouncer/userlist.txt\" <<USERSEOF
\"AscendAny\" \"md5\${pg_md5}\"
USERSEOF
  chown -R 70:70 \"\${base}/pgbouncer\"
  chmod 700 \"\${base}/pgbouncer\"
  chmod 600 \"\${base}/pgbouncer/userlist.txt\"

  podman pull '${postgres_image}' >/dev/null
  podman pull '${pgbouncer_image}' >/dev/null

  if podman container exists ascendany-postgres; then
    podman start ascendany-postgres >/dev/null
  else
    podman run -d --name ascendany-postgres \
      --restart=always \
      -e POSTGRES_DB=AscendAny \
      -e POSTGRES_USER=AscendAny \
      -e POSTGRES_PASSWORD=\"\${db_password}\" \
      -v ascendany-postgres-data:/var/lib/postgresql/data \
      -p 127.0.0.1:5432:5432 \
      '${postgres_image}' >/dev/null
  fi

  for i in \$(seq 1 60); do
    if podman exec ascendany-postgres pg_isready -h 127.0.0.1 -p 5432 -U AscendAny -d AscendAny >/dev/null 2>&1; then
      break
    fi
    if [ \"\${i}\" -eq 60 ]; then
      podman logs ascendany-postgres >&2 || true
      exit 1
    fi
    sleep 1
  done

  if podman container exists ascendany-pgbouncer; then
    podman rm -f ascendany-pgbouncer >/dev/null
  fi

  podman run -d --name ascendany-pgbouncer \
    --restart=always \
    --network host \
    -e DATABASES_CLIENT_SIDE_DBNAME=AscendAny \
    -e DATABASES_HOST=127.0.0.1 \
    -e DATABASES_PORT=5432 \
    -e DATABASES_DBNAME=AscendAny \
    -e DATABASES_USER=AscendAny \
    -e DATABASES_PASSWORD=\"\${db_password}\" \
    -e PGBOUNCER_LISTEN_ADDR=127.0.0.1 \
    -e PGBOUNCER_LISTEN_PORT=6432 \
    -e PGBOUNCER_AUTH_TYPE=md5 \
    -e PGBOUNCER_AUTH_FILE=/etc/pgbouncer/userlist.txt \
    -e PGBOUNCER_POOL_MODE=transaction \
    -e PGBOUNCER_MAX_CLIENT_CONN=100 \
    -e PGBOUNCER_DEFAULT_POOL_SIZE=20 \
    -e PGBOUNCER_ADMIN_USERS=AscendAny \
    -e QUIET=1 \
    -v \"\${base}/pgbouncer:/etc/pgbouncer:Z\" \
    '${pgbouncer_image}' >/dev/null

  for i in \$(seq 1 30); do
    if podman run --rm --network host -e PGPASSWORD=\"\${db_password}\" '${postgres_image}' \
      psql -h 127.0.0.1 -p 6432 -U AscendAny -d AscendAny -tAc 'select 1' >/dev/null 2>&1; then
      break
    fi
    if [ \"\${i}\" -eq 30 ]; then
      podman logs ascendany-pgbouncer >&2 || true
      exit 1
    fi
    sleep 1
  done

  cat > \"\${HOME}/.pgpass\" <<PGPASSEOF
127.0.0.1:5432:AscendAny:AscendAny:\${db_password}
127.0.0.1:6432:AscendAny:AscendAny:\${db_password}
PGPASSEOF
  chmod 600 \"\${HOME}/.pgpass\"

  ss -ltn | grep -E ':(5432|6432)' >/dev/null
"

echo "km6 PostgreSQL + PgBouncer are running."
