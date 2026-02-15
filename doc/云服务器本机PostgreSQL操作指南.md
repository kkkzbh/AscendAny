# 云服务器本机 PostgreSQL 操作指南（AscendAny）

> 目标：在云服务器上“本机操作”数据库，优先通过 PgBouncer `127.0.0.1:6432` 访问业务库；仅在需要数据库管理能力时直连 PostgreSQL `127.0.0.1:5432`。

## 1. 连接原则

- 业务查询/导入/执行项目 DDL：走 PgBouncer `6432`。
- 管理类操作（建库、建用户、扩展、全库恢复、重度维护）：直连 PostgreSQL `5432`。
- 不对公网暴露数据库端口，所有命令在服务器本机执行。

## 2. 登录与准备

```bash
ssh ascend
cd /opt/ascendany/infra
sudo docker compose ps
```

确认数据库组件在线：
- `postgres`（5432）
- `pgbouncer`（6432）

## 3. 准备连接参数（推荐 `~/.pgpass`）

建议在服务器登录用户下配置 `~/.pgpass`（权限必须 `600`）：

```text
127.0.0.1:6432:AscendAny:AscendAny:<your_password>
127.0.0.1:5432:AscendAny:AscendAny:<your_password>
```

```bash
chmod 600 ~/.pgpass
```

如果暂时未配置 `~/.pgpass`，可从 `/opt/ascendany/infra/.env` 临时读取密码（避免明文写到命令里）：

```bash
DB_PASSWORD="$(sudo awk -F= '/^DB_PASSWORD=/{print $2}' /opt/ascendany/infra/.env | tail -n1)"
```

## 4. 常用连接命令

通过 PgBouncer（业务默认）：

```bash
psql -w -h 127.0.0.1 -p 6432 -U AscendAny -d AscendAny
```

未配置 `~/.pgpass` 时：

```bash
PGPASSWORD="$DB_PASSWORD" psql -h 127.0.0.1 -p 6432 -U AscendAny -d AscendAny
```

直连 PostgreSQL（管理场景）：

```bash
psql -w -h 127.0.0.1 -p 5432 -U AscendAny -d AscendAny
```

## 5. psql 内常用操作

```sql
\dn                     -- 查看 schema
\dt ascendany.*         -- 查看业务表
\d ascendany.exams      -- 查看表结构
SELECT now();           -- 查看数据库时间
SELECT count(*) FROM ascendany.exams;
```

## 6. 执行项目 DDL（幂等重放）

在仓库目录执行（推荐通过 6432）：

```bash
cd /opt/ascendany/api/current
for f in db/schema/*.sql; do
  psql -w -v ON_ERROR_STOP=1 -h 127.0.0.1 -p 6432 -U AscendAny -d AscendAny -f "$f"
done
```

若未配置 `~/.pgpass`，把 `psql -w` 替换为：

```bash
PGPASSWORD="$DB_PASSWORD" psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -p 6432 -U AscendAny -d AscendAny -f "$f"
```

## 7. 备份与恢复

逻辑备份（建议直连 5432）：

```bash
mkdir -p /opt/ascendany/backup
pg_dump -w -h 127.0.0.1 -p 5432 -U AscendAny -d AscendAny -F c \
  -f /opt/ascendany/backup/AscendAny_$(date +%F_%H%M).dump
```

恢复（先确认目标库可写、并评估是否需要先清库）：

```bash
pg_restore -w -h 127.0.0.1 -p 5432 -U AscendAny -d AscendAny --clean --if-exists \
  /opt/ascendany/backup/<backup_file>.dump
```

## 8. 运行状态与排障

查看容器状态与日志：

```bash
cd /opt/ascendany/infra
sudo docker compose ps
sudo docker compose logs -f postgres
sudo docker compose logs -f pgbouncer
```

重启数据库相关服务：

```bash
cd /opt/ascendany/infra
sudo docker compose restart postgres pgbouncer
```

查看当前连接与会话状态（在 psql 中执行）：

```sql
SELECT datname, usename, state, count(*)
FROM pg_stat_activity
GROUP BY datname, usename, state
ORDER BY datname, usename, state;
```

## 9. 安全与操作建议

- 不要把密码写入仓库；优先使用 `~/.pgpass` 或环境变量。
- 高风险操作（`DROP/TRUNCATE/--clean` 恢复）先做备份。
- 导入场景保持“单场考试一个事务边界”，失败要回滚重试。
- 业务应用连接参数保持可配置，不在代码中写死。
