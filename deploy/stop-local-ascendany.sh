#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

mapfile -t api_pids < <(pgrep -f "${repo_root}/.venv/bin/python -m uvicorn apps.api.main:app" || true)
if [ "${#api_pids[@]}" -gt 0 ]; then
  printf 'Stopping local AscendAny API PIDs: %s\n' "${api_pids[*]}"
  kill "${api_pids[@]}"
fi

if command -v podman >/dev/null 2>&1; then
  for container in postgres_pgbouncer_1 postgres_postgres_1; do
    if podman container exists "${container}"; then
      echo "Stopping local container: ${container}"
      podman stop "${container}"
    fi
  done
fi

ss -ltnp 2>/dev/null | grep -E ':(5432|6432|8000)\b' || true
