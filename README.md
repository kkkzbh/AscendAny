# AscendAny（学生能力分析平台）

本仓库当前包含：
- `doc/`：设计与开发文档（从真实数据样例抽象出的规范）
- `db/schema/`：PostgreSQL DDL（每张表一个 SQL 文件）
- `preprocess/`：预处理与增量导入代码（已实现，含 `link-actors` 后处理映射）

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

## Python 运行环境（uv + .venv）

预处理相关命令统一使用仓库内 `.venv`（`uv` 管理），避免污染用户/系统 Python 环境。

```bash
# 首次初始化（若 .venv 不存在）
uv venv .venv

# 安装预处理依赖到项目 .venv
uv pip install --python .venv/bin/python -r preprocess/requirements-dev.txt

# 示例：运行预处理与测试
uv run --python .venv/bin/python -m preprocess.cli run --dry-run
uv run --python .venv/bin/python pytest -q
```

## FastAPI 后端（apps/api）

后端目录：`apps/api/`，默认配置：`apps/api/config/default.yaml`。

```bash
# 安装后端依赖
uv pip install --python .venv/bin/python -r apps/api/requirements-dev.txt

# 启动后端
uv run --python .venv/bin/python uvicorn apps.api.main:app --host 127.0.0.1 --port 8000 --reload
```

## Linux 桌面端 GPU 兼容模式（Electron）

在 Linux 下，桌面端默认使用 `ASCENDANY_LINUX_GPU_MODE=off`（关闭硬件加速）以规避
`gbm_pixmap_wayland.cc` / GPU 进程反复崩溃问题。

可按需切换：

```bash
# 默认（最稳妥）
ASCENDANY_LINUX_GPU_MODE=off pnpm --filter @ascendany/desktop dev

# 强制走 XWayland（某些驱动更稳定）
ASCENDANY_LINUX_GPU_MODE=x11 pnpm --filter @ascendany/desktop dev

# 使用软件渲染
ASCENDANY_LINUX_GPU_MODE=swiftshader pnpm --filter @ascendany/desktop dev

# 自动（不做降级处理）
ASCENDANY_LINUX_GPU_MODE=auto pnpm --filter @ascendany/desktop dev
```

## Linux Wayland 输入法（fcitx5）

桌面端在 Wayland 会话下默认启用 IME 兼容开关（`ASCENDANY_LINUX_IME_MODE=auto`），
会自动追加 Chromium 的 `enable-wayland-ime`。

可按需配置：

```bash
# 默认：仅在 Wayland 会话启用 IME 开关
ASCENDANY_LINUX_IME_MODE=auto pnpm --filter @ascendany/desktop dev

# 强制开启（用于排查）
ASCENDANY_LINUX_IME_MODE=on pnpm --filter @ascendany/desktop dev

# 关闭（用于回归对比）
ASCENDANY_LINUX_IME_MODE=off pnpm --filter @ascendany/desktop dev
```

若系统会话里没有设置输入法模块变量，可额外指定：

```bash
ASCENDANY_LINUX_IM_MODULE=fcitx ASCENDANY_LINUX_IME_MODE=on pnpm --filter @ascendany/desktop dev
```

说明：
- `ASCENDANY_LINUX_GPU_MODE=x11` 会强制 XWayland 路径，并跳过 Wayland IME 开关。
- 若你只关心稳定输入法，建议优先使用 `ASCENDANY_LINUX_IME_MODE=on` 在 Wayland 下验证。

## 产品官网（apps/web）

产品介绍网页已独立放在 `apps/web/`，与桌面端 `apps/desktop/` 隔离。

```bash
# 本地开发
pnpm --filter @ascendany/web dev

# 生产构建
pnpm --filter @ascendany/web build
```

### 下载中心 Release 源配置

`apps/web` 下载区会读取 GitHub 最新正式版 Release（`/releases/latest`），自动匹配：
- Windows：`.exe`
- Linux：`.rpm`（文件名需包含 `x64` 或 `amd64`）

可通过环境变量覆盖默认仓库（默认 `kkkzbh/AscendAny`）：

```bash
VITE_RELEASE_OWNER=kkkzbh
VITE_RELEASE_REPO=AscendAny
```

### GitHub Pages 自动发布

仓库已提供 GitHub Actions 工作流：`.github/workflows/deploy-web-pages.yml`。

- 发布触发：`main` 分支有前端相关变更时自动触发（也支持手动 `workflow_dispatch`）。
- 发布地址：`https://<GitHub 用户名>.github.io/AscendAny/`（仓库级 Pages）。
- 仓库设置：`Settings -> Pages -> Source` 选择 `GitHub Actions`。

## 桌面端 Release（Windows EXE + Linux RPM x64）

仓库已提供发布工作流：`.github/workflows/release-desktop.yml`。

- 触发方式：推送标签 `v*`（如 `v0.2.0`）。
- 打包 API 地址：工作流会注入 `VITE_API_BASE_URL`（优先读取仓库变量 `DESKTOP_API_BASE_URL`，未配置时回退 `https://ascendany.kkkzbh.cn`）。
- 产物：
  - Windows：`exe`（x64）
  - Linux：`rpm`（x64）
- 产物会自动上传到对应的 GitHub Release。

本地手动打包命令：

```bash
# Windows EXE x64
pnpm --filter @ascendany/desktop dist:win:x64

# Linux RPM x64
pnpm --filter @ascendany/desktop dist:linux:rpm:x64
```
