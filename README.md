# AscendAny（学生能力分析平台）

本仓库当前包含：
- `doc/`：设计与开发文档（从真实数据样例抽象出的规范）
- `db/schema/`：PostgreSQL DDL（每张表一个 SQL 文件）
- `preprocess/`：预处理与增量导入代码（预留）

更多入口见：`doc/文档索引.md`。

## 本地数据库（PostgreSQL + PgBouncer）

本地开发推荐使用 `~/services/postgres`（Podman Compose）：
- PostgreSQL：`127.0.0.1:5432`（管理/排障直连）
- PgBouncer：`127.0.0.1:6432`（应用默认入口）

> 默认使用 **PgBouncer（6432）+ `~/.pgpass`** 管理密码；不要把数据库密码写进仓库。

### 1) 创建数据库与用户（示例）

按 `~/services/postgres/README.md` 在 PostgreSQL 中创建角色与数据库（示例使用本项目默认命名，注意大小写需双引号）：

```sql
CREATE ROLE "AscendAny" LOGIN PASSWORD '<your_password>';
CREATE DATABASE "AscendAny" OWNER "AscendAny";
```

### 2) 配置 `~/.pgpass`（推荐）

将密码写入本机 `~/.pgpass`（权限必须为 `600`，否则会被忽略）：

```text
127.0.0.1:6432:AscendAny:AscendAny:<your_password>
```

可选：保留直连 PostgreSQL（5432）用于管理/排障：

```text
127.0.0.1:5432:AscendAny:AscendAny:<your_password>
```

### 3) 应用 DDL（`db/schema/*.sql`）

从仓库根目录执行（默认连接 PgBouncer:6432；`-w` 表示只用 `~/.pgpass`，不进行交互式密码提示）：

```bash
for f in db/schema/*.sql; do
  psql -w -v ON_ERROR_STOP=1 -h 127.0.0.1 -p 6432 -U AscendAny -d AscendAny -f "$f"
done
```

### 4) 应用连接参数（通过 PgBouncer）

```text
host=127.0.0.1
port=6432
dbname=AscendAny
user=AscendAny
# password: from ~/.pgpass
```
