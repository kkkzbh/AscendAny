#!/usr/bin/env bash
set -euo pipefail

remote="${ASCENDANY_DEPLOY_REMOTE:-km6}"
cloudflared_image="${ASCENDANY_CLOUDFLARED_IMAGE:-docker.io/cloudflare/cloudflared:latest}"
public_host="${ASCENDANY_PUBLIC_HOST:-ascendany.kkkzbh.cn}"
origin_url="${ASCENDANY_CLOUDFLARE_TUNNEL_ORIGIN_URL:-http://127.0.0.1:8000}"
token_file="${ASCENDANY_CLOUDFLARE_TUNNEL_TOKEN_FILE:-/opt/ascendany/infra/cloudflared/tunnel_token}"
remote_dir="$(dirname "${token_file}")"
cloudflared_uid="${ASCENDANY_CLOUDFLARED_UID:-65532}"
cloudflared_gid="${ASCENDANY_CLOUDFLARED_GID:-65532}"

if [ -n "${CLOUDFLARE_TUNNEL_TOKEN:-}" ]; then
  tmp_token="$(mktemp)"
  printf '%s\n' "${CLOUDFLARE_TUNNEL_TOKEN}" > "${tmp_token}"
  chmod 600 "${tmp_token}"
  ssh -o BatchMode=yes "${remote}" "install -d -m 700 '${remote_dir}'"
  scp -q "${tmp_token}" "${remote}:${token_file}"
  rm -f "${tmp_token}"
  ssh -o BatchMode=yes "${remote}" "chown '${cloudflared_uid}:${cloudflared_gid}' '${token_file}' && chmod 600 '${token_file}'"
fi

ssh -o BatchMode=yes "${remote}" "set -euo pipefail
  install -d -m 700 '${remote_dir}'

  if [ ! -s '${token_file}' ]; then
    cat >&2 <<EOF
Cloudflare Tunnel token is missing on km6: ${token_file}
Create and route the tunnel from a Cloudflare-authenticated machine, then install the connector token once:

  cloudflared tunnel create ascendany-km6
  cloudflared tunnel route dns --overwrite-dns ascendany-km6 ${public_host}
  CLOUDFLARE_TUNNEL_TOKEN=\"\$(cloudflared tunnel token ascendany-km6)\" ./deploy/deploy-km6.sh

After the token is written to km6, later deploys do not require Cloudflare credentials on the deploy machine.
EOF
    exit 1
  fi

  chown '${cloudflared_uid}:${cloudflared_gid}' '${token_file}'
  chmod 600 '${token_file}'

  podman pull '${cloudflared_image}' >/dev/null

  if podman container exists ascendany-caddy; then
    podman rm -f ascendany-caddy >/dev/null
  fi

  if podman container exists ascendany-cloudflared; then
    podman rm -f ascendany-cloudflared >/dev/null
  fi

  podman run -d --name ascendany-cloudflared \
    --restart=always \
    --network host \
    -v '${token_file}:/etc/cloudflared/tunnel_token:ro,Z' \
    '${cloudflared_image}' \
    tunnel --no-autoupdate run --token-file /etc/cloudflared/tunnel_token --url '${origin_url}' >/dev/null

  for i in \$(seq 1 30); do
    if podman ps --format '{{.Names}} {{.Status}}' | grep -q '^ascendany-cloudflared .*Up'; then
      break
    fi
    if [ \"\${i}\" -eq 30 ]; then
      podman logs ascendany-cloudflared >&2 || true
      exit 1
    fi
    sleep 1
  done
"

echo "km6 Cloudflare Tunnel connector is running for ${public_host} -> ${origin_url}."
