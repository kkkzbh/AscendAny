#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote="${ASCENDANY_DEPLOY_REMOTE:-km6}"
release_dir="${ASCENDANY_RELEASE_DIR:-/opt/ascendany/Release}"
venv_dir="${ASCENDANY_VENV_DIR:-/opt/ascendany/.venv}"
service="${ASCENDANY_API_SERVICE:-ascendany-api}"
api_env_file="${ASCENDANY_API_ENV_FILE:-/etc/ascendany/api.env}"
db_container="${ASCENDANY_POSTGRES_CONTAINER:-ascendany-postgres}"
db_name="${ASCENDANY_DB_NAME:-AscendAny}"
db_user="${ASCENDANY_DB_USER:-AscendAny}"
healthz_local="${ASCENDANY_LOCAL_HEALTHZ:-http://127.0.0.1:8000/api/v1/healthz}"
healthz_public="${ASCENDANY_PUBLIC_HEALTHZ:-https://ascendany.kkkzbh.cn/api/v1/healthz}"

require_clean_tree() {
  cd "${repo_root}"
  git diff --quiet
  git diff --cached --quiet
  if [ -n "$(git ls-files --others --exclude-standard)" ]; then
    echo "Working tree has untracked files. Commit or remove them before deploy." >&2
    git status --short >&2
    exit 1
  fi
}

require_clean_tree
command -v rsync >/dev/null

"${repo_root}/deploy/setup-db-km6.sh"
"${repo_root}/deploy/setup-cloudflare-tunnel-km6.sh"
"${repo_root}/deploy/init-km6.sh"

ssh -o BatchMode=yes "${remote}" "set -euo pipefail
  test -d '${release_dir}'
  test -d '${venv_dir}'
  test -r '${api_env_file}'
  test -x '${venv_dir}/bin/pip'
  command -v rsync >/dev/null
  podman container exists '${db_container}'
"

rsync -az --delete \
  --exclude '.git/' \
  --exclude '.venv/' \
  --exclude 'node_modules/' \
  --exclude '.pytest_cache/' \
  --exclude '.ruff_cache/' \
  --exclude '.playwright-cli/' \
  --exclude 'tmp/' \
  --exclude 'var/' \
  "${repo_root}/" "${remote}:${release_dir}/"

ssh -o BatchMode=yes "${remote}" "set -euo pipefail
  cd '${release_dir}'
  '${venv_dir}/bin/pip' install -r apps/api/requirements.txt
  '${venv_dir}/bin/pip' install -r preprocess/requirements.txt

  for f in db/schema/*.sql; do
    podman exec -i '${db_container}' psql -v ON_ERROR_STOP=1 -U '${db_user}' -d '${db_name}' < \"\${f}\"
  done

  systemctl restart '${service}'
  for i in \$(seq 1 30); do
    if systemctl is-active --quiet '${service}' && curl --max-time 2 -fsS '${healthz_local}' >/dev/null; then
      break
    fi
    if [ \"\${i}\" -eq 30 ]; then
      systemctl status '${service}' --no-pager -l >&2 || true
      journalctl -u '${service}' -n 120 --no-pager -o cat >&2 || true
      exit 1
    fi
    sleep 1
  done
"

curl --max-time 10 -fsS "${healthz_public}" >/dev/null

echo "AscendAny deployed to ${remote}:${release_dir}"
