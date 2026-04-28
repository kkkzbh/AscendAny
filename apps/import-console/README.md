# AscendAny 数据导入控制台

> 面向管理员的 Web 应用，用于图形化管理增量数据导入。

## 功能概览

- **考试发现**：自动扫描 `practice/` 目录，检测新增 / 变更的考试数据
- **增量导入**：一键执行增量导入，SSE 实时推送进度与日志
- **Actor 关联**：将匿名提交记录关联到学生身份
- **导入历史**：查看所有历史导入记录
- **内置帮助**：打开右侧帮助面板即可了解完整操作流程

---

## 本地开发（连接云服务器）

### 前置条件

- Node.js 20+, pnpm 9+
- 云端 FastAPI 已部署在 `https://ascendany.kkkzbh.cn`

### 启动方式

**方式 A — 连接本地 API（Vite 自动代理）**

```bash
# 1. 先启动本地 FastAPI（VS Code "AscendAny API" 配置，或手动）
# 2. 启动前端
pnpm --filter @ascendany/import-console dev
```

浏览器打开 **http://localhost:6748**。Vite dev server 自动将 `/api` 代理到 `http://127.0.0.1:8000`。

**方式 B — 直连云服务器 API**

```bash
VITE_API_BASE_URL=https://ascendany.kkkzbh.cn pnpm --filter @ascendany/import-console dev
```

浏览器打开 **http://localhost:6748**。前端直接请求云端 API（后端 CORS 已配置 `allow_origins: ["*"]`）。

### 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `VITE_API_BASE_URL` | 后端 API 地址（不含尾 `/`） | `""` (同源，走 Vite proxy) |
| `VITE_BASE_PATH` | 部署子路径 | `/` |
| `VITE_TOKEN_HANDOFF` | `"true"` → 允许从 Action 传入登录 token（仅本地开发建议开启） | dev server 默认可用，生产构建默认关闭 |
| `VITE_HASH_ROUTER` | `"true"` → 启用 HashRouter（静态托管场景） | 不设置 → BrowserRouter |

### Codex Action 自动登录

`启动管理平台` Action 默认只启动并打开页面。若当前终端环境同时存在以下变量，Action 会先调用 `/api/v1/auth/login`，再打开已写入 token 的管理页：

```bash
export ASCENDANY_ADMIN_USERNAME=Admin
export ASCENDANY_ADMIN_PASSWORD='你的本地管理员密码'
```

也可以写入 git 已忽略的本地文件 `.env.local` 或 `.env`：

```bash
ASCENDANY_ADMIN_USERNAME=Admin
ASCENDANY_ADMIN_PASSWORD=你的本地管理员密码
```

账号密码不会写入 `.codex/environments/environment.toml`；前端收到 token 后会立即清理地址栏中的 token 参数。

---

## 首次设置

1. **应用数据库迁移**（在服务器上执行一次）

   ```sql
   -- 已包含在 db/schema/040_user_accounts.sql 的 DO 块中，
   -- 也可单独执行：
   ALTER TABLE ascendany.user_accounts
     ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;
   ```

2. **提权管理员账号**

   ```sql
   UPDATE ascendany.user_accounts
   SET is_admin = TRUE
   WHERE username = '你的用户名';
   ```

3. **部署后端新代码**到服务器（`git pull` + `systemctl restart ascendany-api`），使 import 路由生效。

4. 打开 `http://localhost:6748`，用管理员账号登录。

---

## 使用流程

1. **登录** → 进入主控制台
2. **左侧面板** — 点击"扫描考试数据"，自动检测 `practice/` 下的考试
   - 🟢 **new** = 新考试（从未导入）
   - 🟡 **changed** = 数据内容有变更
   - ⚪ **unchanged** = 无变化
3. **勾选**要导入的考试（或全选）
4. **点击"开始导入"** — 右侧控制台实时显示进度条 + 终端日志
5. 导入完成后，点击 **"Link Actors"** 执行学生身份关联
6. 切换 **"历史记录"** 标签查看所有导入记录

### 选项说明

| 选项 | 说明 |
| --- | --- |
| **Dry Run** | 试运行，不写入数据库，仅预览 |
| **Force** | 强制重新导入（忽略 fingerprint） |

点击右上角 **❓ 帮助** 打开内置操作指引。

---

## 技术架构

```
浏览器 (React SPA, localhost:6748)
  ├── JWT 认证 → /api/v1/auth/*
  ├── 发现考试 → GET /api/v1/import/discover
  ├── 启动导入 → POST /api/v1/import/run → 返回 runId
  ├── SSE 流   → GET /api/v1/import/run/{runId}/stream
  └── 导入历史 → GET /api/v1/import/history

FastAPI 后端 (ascendany.kkkzbh.cn)
  ├── Admin 鉴权 (JWT is_admin claim)
  ├── TaskManager (内存任务队列 + SSE 事件流)
  └── threading.Thread → preprocess.IngestService / LinkingService
```
