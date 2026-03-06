# Ubuntu 服务器部署运维指南（AscendAny）

> 目标：在 Ubuntu 服务器部署生产 FastAPI，数据库通过本机 PostgreSQL + PgBouncer（6432）访问，桌面端 Release 默认连接线上 API。

## 1. 线上地址

- API 域名：`https://ascendany.kkkzbh.cn`
- 健康检查：`https://ascendany.kkkzbh.cn/api/v1/healthz`

## 2. 服务器组件

- 操作系统：Ubuntu 24.04 LTS
- 反向代理：Nginx（80/443）
- 证书：Let's Encrypt（certbot 自动续期）
- API 进程：systemd + Uvicorn
- 数据库：Docker Compose
  - PostgreSQL：`127.0.0.1:5432`
  - PgBouncer：`127.0.0.1:6432`

## 3. 关键文件路径

- 基础目录：`/opt/ascendany`
- 数据库编排：`/opt/ascendany/infra/docker-compose.yml`
- 数据库密钥：`/opt/ascendany/infra/.env`
- PgBouncer 用户文件：`/opt/ascendany/infra/pgbouncer/userlist.txt`
- API 代码：`/opt/ascendany/api/current`
- API 虚拟环境：`/opt/ascendany/api/.venv`
- API 环境变量：`/etc/ascendany/api.env`
- API systemd：`/etc/systemd/system/ascendany-api.service`
- Nginx 站点：`/etc/nginx/sites-available/ascendany-api.conf`

## 4. SSH 登录服务器

当前推荐直接使用本机 SSH 别名：

```bash
ssh ascend
```

如需在新机器初始化，可在 `~/.ssh/config` 添加：

```sshconfig
Host ascend
  HostName 52.147.120.86
  User xyz
  IdentityFile /home/kkkzbh/data/下载/Ascend_key.pem
  IdentitiesOnly yes
  StrictHostKeyChecking accept-new
```

兼容方式（不依赖别名，临时私钥）：

```bash
install -m 600 /home/kkkzbh/data/下载/Ascend_key.pem /tmp/ascend_key.pem
ssh -i /tmp/ascend_key.pem xyz@52.147.120.86 'whoami && hostnamectl --static'
```

## 5. 服务状态检查

```bash
sudo systemctl status ascendany-api --no-pager
sudo systemctl status nginx --no-pager
sudo systemctl status docker --no-pager
sudo docker ps
curl -fsS https://ascendany.kkkzbh.cn/api/v1/healthz
```

## 6. 数据库维护

```bash
cd /opt/ascendany/infra
sudo docker compose ps
sudo docker compose logs -f postgres
sudo docker compose logs -f pgbouncer
sudo docker compose restart postgres pgbouncer
```

如需重新应用 DDL（幂等）：

```bash
DB_PASSWORD="$(sudo awk -F= '/^DB_PASSWORD=/{print $2}' /opt/ascendany/infra/.env | tail -n1)"
for f in /opt/ascendany/api/current/db/schema/*.sql; do
  PGPASSWORD="$DB_PASSWORD" psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -p 6432 -U AscendAny -d AscendAny -f "$f"
done
```

## 7. API 发布/更新

```bash
cd /opt/ascendany/api/current
git pull --ff-only
/opt/ascendany/api/.venv/bin/pip install -r apps/api/requirements.txt
sudo systemctl restart ascendany-api
```

查看日志：

```bash
journalctl -u ascendany-api -f
```

导入控制台依赖的关键环境变量（建议写入 `/etc/ascendany/api.env`）：

```bash
PRACTICE_DATA_ROOT=/opt/ascendany/data/practice
# 可选：覆盖预处理配置文件路径（默认会自动使用仓库内 preprocess/config/default.yaml）
# ASCENDANY_PREPROCESS_CONFIG=/opt/ascendany/api/current/preprocess/config/default.yaml
```

应用变量并重启：

```bash
sudo systemctl daemon-reload
sudo systemctl restart ascendany-api
```

## 8. 桌面端 Release 对接线上 API

- 工作流：`.github/workflows/release-desktop.yml`
- 构建时环境变量：`VITE_API_BASE_URL`
- 建议在 GitHub 仓库 Variables 设置：
  - `DESKTOP_API_BASE_URL=https://ascendany.kkkzbh.cn`

说明：
- Release 打包会注入线上 API 地址。
- 本地 `pnpm --filter @ascendany/desktop dev` 仍默认使用 `http://127.0.0.1:8000`，便于本地调试。
- 自动更新发布自检（Windows + Linux）：
  - GitHub Release 必须包含：`latest.yml`、`latest-linux.yml`、`AscendAny-win-*.exe.blockmap`、安装包本体（`.exe/.rpm`）。
  - Pages 构建后应存在目录：`apps/site/public/desktop-updates/`，并包含上述更新元数据与安装包。

## 9. 预处理导入（后续图形化触发）

当前线上仅部署 API，不做定时导入。后续图形化导入 App 完成后，建议在导入成功后触发一次：

```bash
/usr/local/bin/ascendany-preprocess-run
```

该包装命令等价于在服务器执行：

```bash
cd /opt/ascendany/api/current
/opt/ascendany/api/.venv/bin/python -m preprocess.cli run
```

可用于预检查：

```bash
/usr/local/bin/ascendany-preprocess-run --dry-run
```

如果线上运行该命令，需先保证：
- `PRACTICE_DATA_ROOT` 指向服务器上的数据目录；
- `preprocess` 依赖已安装到对应虚拟环境；
- DB 连接参数仍指向本机 `127.0.0.1:6432`。

## 10. CI/CD 自动部署（push 后自动同步）

仓库已提供工作流：`.github/workflows/deploy-api-server.yml`  
触发条件：
- push 到 `main/master` 且命中后端相关路径（`apps/api/**`、`preprocess/**`、`db/schema/**`）；
- 手动触发 `workflow_dispatch`。

### 10.1 GitHub Secrets（必需）

- `ASCENDANY_SERVER_HOST`：服务器地址（如 `52.147.120.86`）
- `ASCENDANY_SERVER_USER`：SSH 用户（如 `xyz`）
- `ASCENDANY_SSH_PRIVATE_KEY`：部署私钥（PEM 全内容）

可选：
- `ASCENDANY_SSH_KNOWN_HOSTS`：固定 `known_hosts`（推荐配置，避免首次 `ssh-keyscan`）

配置状态（当前仓库）：
- 已配置上述 Secrets/Variables（具体值不写入仓库文档）。

### 10.2 GitHub Variables（可选，均有默认值）

- `ASCENDANY_SERVER_PORT`（默认 `22`）
- `ASCENDANY_SERVER_APP_DIR`（默认 `/opt/ascendany/api/current`）
- `ASCENDANY_API_VENV`（默认 `/opt/ascendany/api/.venv`）
- `ASCENDANY_API_SERVICE`（默认 `ascendany-api`）
- `ASCENDANY_API_ENV_FILE`（默认 `/etc/ascendany/api.env`）
- `ASCENDANY_API_HEALTHZ`（默认 `https://ascendany.kkkzbh.cn/api/v1/healthz`）
- `ASCENDANY_SECONDARY_API_ENABLED`（默认 `false`）
- `ASCENDANY_SECONDARY_API_SERVICE`（默认 `ascendany-api-external-auth`）
- `ASCENDANY_SECONDARY_API_HOST`（默认 `0.0.0.0`）
- `ASCENDANY_SECONDARY_API_PORT`（默认 `8010`）
- `ASCENDANY_SECONDARY_API_LOG_LEVEL`（默认 `info`）
- `ASCENDANY_SECONDARY_API_ENV_FILE`（默认 `/etc/ascendany/api-external-auth.env`）
- `ASCENDANY_SECONDARY_API_AUTH_PROVIDER`（默认 `app01_mysql`）
- `ASCENDANY_AUTH_ALLOW_STORED_PASSWORD_DIRECT_LOGIN`（第二 API 环境文件内固定写入 `true`，用于 Integration Web 传数据库存储密码值直登）
- `ASCENDANY_SECONDARY_APP01_DB_CONFIG_PATH`（默认空）
- `ASCENDANY_SECONDARY_API_INTERNAL_HEALTHZ`（默认空，空时自动按端口推导）
- `ASCENDANY_SECONDARY_API_HEALTHZ`（默认空，空时跳过外部 URL 探测）

### 10.3 服务器前置条件

- 目标目录是一个可 `git pull --ff-only` 的仓库工作副本；
- API 运行用户对仓库目录与虚拟环境有读写权限；
- `sudo systemctl restart ascendany-api` 不需要交互密码（或部署用户具备对应权限）。

### 10.4 部署行为

每次触发会在服务器执行：
1. 切到部署分支并 `git pull --ff-only`；
2. 安装/同步 Python 依赖（`apps/api/requirements.txt` + `preprocess/requirements.txt`）；
3. 重启 `ascendany-api`；
4. 校验主 API 的 `is-active` 与 `healthz`；
5. 若启用 `ASCENDANY_SECONDARY_API_ENABLED=true`，会额外创建/更新第二 API systemd 服务（新端口 + 新启动参数），并做本机 healthz 校验。

### 10.5 外部项目子功能接入（第二 API 实例）

建议最小配置：
- `ASCENDANY_SECONDARY_API_ENABLED=true`
- `ASCENDANY_SECONDARY_API_PORT=8010`
- `ASCENDANY_SECONDARY_API_AUTH_PROVIDER=app01_mysql`
- 第二 API 环境文件会额外写入 `ASCENDANY_AUTH_ALLOW_STORED_PASSWORD_DIRECT_LOGIN=true`
- `ASCENDANY_SECONDARY_APP01_DB_CONFIG_PATH=/path/to/db_config.json`（或在 `api.env` 提供 `ASCENDANY_APP01_DB_*`）

效果：
- 主 API（原有项目）保持不变；
- 第二 API 在新端口启动，供其他项目使用外部 user 表登录。

## 11. 桌面端 Web 版自动部署（开放端口给浏览器访问）

仓库已新增工作流：`.github/workflows/deploy-desktop-web.yml`  
触发条件：
- push 到 `main/master` 且命中 `apps/desktop/**` 或相关前端依赖文件；
- 手动触发 `workflow_dispatch`。

### 11.1 功能说明

- CI 会构建两个 Web 产物：
  - 主 Web（原有部署）
  - Integration Web（给其他项目嵌入，默认开启直登参数能力）
- 将两个产物分别同步到服务器目录（默认主：`/opt/ascendany/desktop-web`，Integration：`/opt/ascendany/desktop-web-integration`）；
- 自动写入/更新对应 systemd 服务（默认主：`ascendany-desktop-web`，Integration：`ascendany-desktop-web-integration`）；
- 主服务默认监听 `0.0.0.0:4173`；Integration 默认监听 `0.0.0.0:4174`；
- 如果服务器启用了 `ufw`，自动执行 `ufw allow <port>/tcp` 放行端口；
- 部署后执行本机与外部可用性探测。

### 11.2 GitHub Variables（可选，均有默认值）

- `DESKTOP_WEB_API_BASE_URL`（默认回退 `DESKTOP_API_BASE_URL`，再回退 `https://ascendany.kkkzbh.cn`）
- `DESKTOP_WEB_DIRECT_LOGIN_ENABLED`（主 Web 直登开关，默认 `false`）
- `ASCENDANY_DESKTOP_WEB_DIR`（默认 `/opt/ascendany/desktop-web`）
- `ASCENDANY_DESKTOP_WEB_SERVICE`（默认 `ascendany-desktop-web`）
- `ASCENDANY_DESKTOP_WEB_PORT`（默认 `4173`）
- `ASCENDANY_DESKTOP_WEB_URL`（默认空，空时按 `http://<server_host>:<port>/` 校验）
- `ASCENDANY_DESKTOP_WEB_INTEGRATION_ENABLED`（默认 `false`）
- `DESKTOP_WEB_INTEGRATION_API_BASE_URL`（Integration Web 的 API 地址）
- `DESKTOP_WEB_INTEGRATION_DIRECT_LOGIN_ENABLED`（默认 `true`）
- `ASCENDANY_DESKTOP_WEB_INTEGRATION_DIR`（默认 `/opt/ascendany/desktop-web-integration`）
- `ASCENDANY_DESKTOP_WEB_INTEGRATION_SERVICE`（默认 `ascendany-desktop-web-integration`）
- `ASCENDANY_DESKTOP_WEB_INTEGRATION_PORT`（默认 `4174`）
- `ASCENDANY_DESKTOP_WEB_INTEGRATION_URL`（默认空，空时仅做本机探测）

### 11.3 服务器查看与排障

```bash
sudo systemctl status ascendany-desktop-web --no-pager
sudo journalctl -u ascendany-desktop-web -n 120 --no-pager
sudo systemctl status ascendany-desktop-web-integration --no-pager
sudo journalctl -u ascendany-desktop-web-integration -n 120 --no-pager
ss -ltnp | grep 4173
ss -ltnp | grep 4174
curl -fsS http://127.0.0.1:4173/
curl -fsS http://127.0.0.1:4174/
```

若使用云厂商安全组，还需在安全组层面额外放行 `4173/tcp`。

### 11.4 Integration Web 直登参数

当 `DESKTOP_WEB_INTEGRATION_DIRECT_LOGIN_ENABLED=true` 时，可通过 URL 传参自动登录：

```text
https://<integration-host>/?username=<账号>&password=<密码>&autoLogin=true&rememberPassword=false
```

若第二 API 同时启用了 `ASCENDANY_AUTH_ALLOW_STORED_PASSWORD_DIRECT_LOGIN=true`，也可直接传数据库 `password` 字段中的存储值：

```text
https://<integration-host>/?username=<账号>&storedPassword=<数据库存储值>&autoLogin=true&rememberPassword=false
```

说明：
- 别名参数也支持：`user`/`pass`；
- 存储值参数也支持别名：`aa_stored_password`；
- 可选传 `deviceId`；
- 当同时存在 `password` 与 `storedPassword` 时，优先使用 `storedPassword`；
- `storedPassword` 仅用于“第二 API + 集成 Web”接入场景，主 API / 主 Web 不支持；
- 前端尝试登录后会自动清理 URL 中的敏感参数。
