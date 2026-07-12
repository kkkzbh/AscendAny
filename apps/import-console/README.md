# AscendAny Pintia 数据导入控制台

面向管理员的 React Web 应用，负责接收 AscendAny 浏览器插件导出的 Pintia snapshot v2，并查看 Go runtime 中的持久导入任务。

## 功能

- 上传一份完整的 `ascendany.pintia.snapshot.v2` JSON 快照
- 上传成功后直接创建或复用按 artifact SHA-256 幂等的持久任务
- 通过 durable event sequence 恢复 SSE 任务流
- 使用游标分页查看导入历史
- 通过 `@ascendany/sdk` 的 `BrowserSession` 管理 v2 管理员会话

## 本地开发

前置条件：Node.js 22.18+、pnpm 9+，以及已启动的 AscendAny Go v2 runtime。

连接本地 runtime：

```bash
pnpm --filter @ascendany/import-console dev
```

Vite 在 `http://localhost:6748/admin/` 提供页面，并将 `/api` 代理到 `http://127.0.0.1:18000`。

连接已部署的 runtime：

```bash
VITE_API_BASE_URL=https://ascendany.kkkzbh.cn \
  pnpm --filter @ascendany/import-console dev
```

### 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `VITE_API_BASE_URL` | Go v2 API 的 canonical origin，不含尾部 `/` | 当前页面 origin |

Production public base固定为 `/admin/`，router basename固定为 `/admin`。

`VITE_API_BASE_URL` 必须是 HTTPS origin；本地开发允许 canonical loopback HTTP origin。

## 认证边界

控制台直接使用 `@ascendany/sdk` 的 `BrowserSession`：

- access token 只保存在当前页面内存中；
- refresh credential 由 Go runtime 写入 HttpOnly cookie；
- 浏览器存储只保存与 API origin 绑定的旋转 CSRF token；
- 页面启动时使用 refresh cookie 和 CSRF token 恢复会话；
- 每次上传、读取任务和建立 SSE 连接前都会确保 access token 仍有足够有效期。

管理员在页面中输入账号密码登录。应用不接受 URL token，也不持久化 access token 或 refresh credential。

## 使用流程

1. 在 Pintia 题目集页面使用 AscendAny 浏览器插件导出完整 snapshot v2 JSON。
2. 登录控制台并将单个 `.json` 文件拖入上传区域。
3. 服务端持久化快照字节并立即创建任务；控制台自动连接任务事件流。
4. 在当前任务、实时日志和导入历史中查看进度与最终状态。

上传入口只接受完整 snapshot v2。服务端完成严格结构校验、跨记录语义校验，并以整场考试为事务边界提交业务数据。

## 技术架构

```text
React Import Console
  ├── BrowserSession → 内存 access token + HttpOnly refresh cookie + CSRF rotation
  ├── POST /api/v2/imports/pintia
  ├── GET  /api/v2/imports/{jobId}/events
  ├── GET  /api/v2/imports/{jobId}
  └── GET  /api/v2/imports?cursor=&limit=

Go v2 runtime
  ├── 管理员 session authorization
  ├── immutable artifact 与 PostgreSQL durable job/event
  ├── strict snapshot v2 validation
  └── leased import/analytics worker 与考试事务
```

## 验证

```bash
pnpm --filter @ascendany/import-console exec tsc -b
pnpm --filter @ascendany/import-console test
pnpm --filter @ascendany/import-console build
```
