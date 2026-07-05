#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote="${ASCENDANY_DEPLOY_REMOTE:-km6}"
release_dir="${ASCENDANY_RELEASE_DIR:-/opt/ascendany/Release}"
venv_dir="${ASCENDANY_VENV_DIR:-/opt/ascendany/.venv}"
api_env_file="${ASCENDANY_API_ENV_FILE:-/etc/ascendany/api.env}"
service="${ASCENDANY_API_SERVICE:-ascendany-api}"

if ! ssh -o BatchMode=yes "${remote}" "test -r /opt/ascendany/infra/.env"; then
  "${repo_root}/deploy/setup-db-km6.sh"
fi

ssh -o BatchMode=yes "${remote}" "set -euo pipefail
  mkdir -p /opt/ascendany /etc/ascendany '${release_dir}' /opt/ascendany/data/practice
  if [ ! -d '${venv_dir}' ]; then
    python3 -m venv '${venv_dir}'
  fi
  '${venv_dir}/bin/pip' install -U pip
  test -r /opt/ascendany/infra/.env
"

if ssh -o BatchMode=yes "${remote}" "test ! -r '${api_env_file}'"; then
  db_password="$(ssh -o BatchMode=yes "${remote}" "grep '^DB_PASSWORD=' /opt/ascendany/infra/.env | tail -n1 | cut -d= -f2-")"
  if [ -z "${db_password}" ]; then
    echo "DB_PASSWORD is missing from km6:/opt/ascendany/infra/.env" >&2
    exit 1
  fi
  tmp_env="$(mktemp)"
  sed "s/^ASCENDANY_DB_PASSWORD=.*/ASCENDANY_DB_PASSWORD=${db_password}/" "${repo_root}/deploy/api.env.example" > "${tmp_env}"
  scp -q "${tmp_env}" "${remote}:${api_env_file}"
  rm -f "${tmp_env}"
  ssh -o BatchMode=yes "${remote}" "chmod 600 '${api_env_file}'"
fi

scp -q "${repo_root}/deploy/ascendany-api.service" "${remote}:/etc/systemd/system/${service}.service"
ssh -o BatchMode=yes "${remote}" "set -euo pipefail
  systemctl daemon-reload
  systemctl enable '${service}'
"

echo "km6 API service is prepared."
