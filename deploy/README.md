# AscendAny deployment

> km6 是 AscendAny 唯一生产运行环境，并且是用户自有的本地服务器。PostgreSQL + PgBouncer 只部署在 km6；本电脑只做开发与测试，不常驻 API，不维护本地数据库实例。

## 1. Model

- 生产 API：km6 上唯一 systemd 服务 `ascendany-api`。
- 生产代码：km6 上唯一目录 `/opt/ascendany/Release`。
- Python 环境：km6 上唯一 virtualenv `/opt/ascendany/.venv`。
- 生产数据库：km6 本机 Podman 容器 `ascendany-postgres` + `ascendany-pgbouncer`。
  - PostgreSQL 管理端口：`127.0.0.1:5432`
  - PgBouncer 业务端口：`127.0.0.1:6432`
- 公网入口：km6 本机 Podman 容器 `ascendany-cloudflared`，通过 Cloudflare Tunnel 主动连接 Cloudflare，并把现有域名转发到 `127.0.0.1:8000`。不要求 km6 有公网地址，也不要求路由器转发 80/443。
- 本地开发：只临时启动 API 或前端；数据库访问必须指向 km6。
- 不维护 `current`、多 release、backup 目录、fallback 实例或 GitHub deploy workflow。

## 2. One-command deploy

外部前提：现有域名必须已经接入 Cloudflare，并且已创建 Cloudflare Tunnel 与 DNS route。例如在已登录 Cloudflare 的本机执行：`cloudflared tunnel create ascendany-km6`、`cloudflared tunnel route dns --overwrite-dns ascendany-km6 ascendany.kkkzbh.cn`。首次部署时通过 `CLOUDFLARE_TUNNEL_TOKEN="$(cloudflared tunnel token ascendany-km6)" ./deploy/deploy-km6.sh` 把 connector token 写入 km6；后续部署只复用 km6 上的 token 文件，不再依赖部署机保存 Cloudflare 登录状态或 tunnel credentials JSON。

部署前本地 working tree 必须干净。提交代码后执行：

```bash
./deploy/deploy-km6.sh
```

`deploy-km6.sh` 是唯一主入口，必须覆盖首次部署和后续部署。脚本行为：

1. 校验本地 working tree clean。
2. 自动初始化 km6 PostgreSQL + PgBouncer；首次运行会生成数据库密码，后续运行复用已有密码和数据卷。
3. 自动初始化 km6 Cloudflare Tunnel connector；首次运行需要 `CLOUDFLARE_TUNNEL_TOKEN`，后续运行复用 km6 上的 token 文件。
4. 自动初始化 km6 目录、virtualenv、`/etc/ascendany/api.env` 和 `ascendany-api.service`。
5. 通过 `rsync --delete` 同步到 km6 `/opt/ascendany/Release`。
6. 在 km6 `/opt/ascendany/.venv` 安装后端与预处理依赖。
7. 通过 km6 `ascendany-postgres` 容器应用 `db/schema/*.sql`。
8. 重启唯一服务 `ascendany-api`。
9. 校验 km6 本机与公网 healthz；如果 km6 本机 health 正常但公网失败，优先检查 Cloudflare Tunnel connector 是否 Up、DNS route 是否绑定到正确 tunnel，以及 connector 是否使用 `--url http://127.0.0.1:8000`。

`setup-db-km6.sh`、`setup-cloudflare-tunnel-km6.sh` 和 `init-km6.sh` 是幂等子步骤，可单独重跑用于诊断，但正常部署不要求手动调用它们。

## 3. First deploy with local database migration

如果要把旧本机数据库迁移到 km6，顺序是：

```bash
./deploy/setup-db-km6.sh
ASCENDANY_MIGRATE_CONFIRM=AscendAny-to-km6 ./deploy/migrate-db-to-km6.sh
./deploy/stop-local-ascendany.sh
./deploy/deploy-km6.sh
```

数据库迁移通过本机 `postgres_postgres_1` 容器的 `pg_dump` 和 km6 `ascendany-postgres` 容器的 `pg_restore` 完成。迁移完成后，本机 PostgreSQL/PgBouncer 和本地 API 都应停止。

不把一次性迁移产物固化为 backup 或 release 切换结构。

## 4. Files maintained by deploy

- `/opt/ascendany/Release`
- `/opt/ascendany/.venv`
- `/opt/ascendany/infra`
- `/opt/ascendany/infra/cloudflared`
- `/opt/ascendany/data/practice`
- `/etc/ascendany/api.env`
- `/etc/systemd/system/ascendany-api.service`

`ASCENDANY_DB_PASSWORD` 由 `setup-db-km6.sh` 首次生成并写入 km6 文件，不提交到仓库。

## 5. Local development

本地只做开发与测试。需要临时联调后端时，先把本地 `6432` 指向 km6 PgBouncer：

```bash
ssh -N -L 6432:127.0.0.1:6432 km6
```

另开终端临时启动 API：

```bash
uv pip install --python .venv/bin/python -r apps/api/requirements-dev.txt
uv run --python .venv/bin/python uvicorn apps.api.main:app --host 127.0.0.1 --port 8000 --reload
```

前端开发默认直连线上 API：

```bash
VITE_API_BASE_URL=https://ascendany.kkkzbh.cn pnpm --filter @ascendany/import-console dev
```

## 6. Verification

```bash
ssh km6 'systemctl is-active ascendany-api && curl -fsS http://127.0.0.1:8000/api/v1/healthz'
curl -fsS https://ascendany.kkkzbh.cn/api/v1/healthz
```

检查本机没有常驻 AscendAny API 或数据库端口：

```bash
ss -ltnp | grep -E ':(5432|6432|8000)\b' || true
```

## 7. GitHub Actions policy

仓库不再维护 deploy workflow。`.github/workflows/ci.yml` 只负责测试与构建；部署由本地 `deploy/` 脚本显式执行。
