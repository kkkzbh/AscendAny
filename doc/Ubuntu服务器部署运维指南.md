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

首次或长期运维前，先用私钥登录（已知登录用户：`xyz`，服务器 IP：`52.147.120.86`）。

为避免私钥权限过宽导致 `Permission denied (publickey)`，建议先复制并收紧权限：

```bash
install -m 600 /home/kkkzbh/data/下载/Ascend_key.pem /tmp/ascend_key.pem
ssh -o StrictHostKeyChecking=accept-new -i /tmp/ascend_key.pem xyz@52.147.120.86
```

可直接执行单条远程命令检查连通性：

```bash
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

## 8. 桌面端 Release 对接线上 API

- 工作流：`.github/workflows/release-desktop.yml`
- 构建时环境变量：`VITE_API_BASE_URL`
- 建议在 GitHub 仓库 Variables 设置：
  - `DESKTOP_API_BASE_URL=https://ascendany.kkkzbh.cn`

说明：
- Release 打包会注入线上 API 地址。
- 本地 `pnpm --filter @ascendany/desktop dev` 仍默认使用 `http://127.0.0.1:8000`，便于本地调试。

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
