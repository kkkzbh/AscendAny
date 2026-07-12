# AscendAny Web

学生与管理员使用的浏览器应用。当前产品面严格来自 v2 OpenAPI 合同：

- 用户名与密码登录
- 一次性 enrollment claim 激活
- 学生个人 analytics 与 leaderboard
- 显示名称与账户 session 管理

客户端通过 `@ascendany/sdk` 的 `BrowserSession` 管理认证。refresh credential 位于 HttpOnly cookie，短期 access credential 只驻留运行内存；浏览器存储中仅保存轮换 CSRF token。

## 本地开发

```bash
pnpm --filter @ascendany/web dev
```

Vite 默认监听 5175，页面入口固定为 `/app/`，并把 `/api` 转发到 `VITE_API_PROXY_TARGET`（默认 `http://127.0.0.1:18000`）。Production build 固定使用同源 API，不设置 `VITE_API_BASE_URL`。

## 验证

```bash
pnpm --filter @ascendany/web typecheck
pnpm --filter @ascendany/web test
pnpm --filter @ascendany/web build
```
