# AscendWeb

本目录是前端 SPA（React + TypeScript + Vite），用于展示知识图谱、OJ 题库、知识问答等页面。

当前 AscendAny 的生产 API 只部署在 km6，部署入口为 `deploy/README.md`。本目录只描述前端开发与构建；不要在本机常驻后端或数据库。

## 开发（本地）

默认直连 km6 API：

```bash
VITE_BACKEND_ORIGIN=https://ascendany.kkkzbh.cn pnpm --filter @ascendany/web dev
```

只在调试后端改动时，才按 `deploy/README.md` 建立到 km6 PgBouncer 的 SSH local forwarding，并临时启动本地 API。此时可以不设置 `VITE_BACKEND_ORIGIN`，由 Vite proxy 转发到本地临时 API。

默认：

- Vite 端口：5175（见 `vite.config.ts`）
- Vite proxy 仅用于临时后端联调，不作为默认运行方式

## 构建（静态站点）

```bash
pnpm --filter @ascendany/web build
```

产物输出到本应用的 `dist/`。

## 环境变量（Vite）

- `VITE_BACKEND_ORIGIN`
  - 推荐设置为 `https://ascendany.kkkzbh.cn`
  - 为空时使用相对路径请求，仅适合临时本地 API 联调
- `VITE_APP_BASENAME`
  - SPA Router basename，默认 `/`
  - 例如把站点部署到 `/app/` 下时可设为 `/app`

`VITE_*` 会在 build 时注入并打包进产物；独立部署时必须在构建阶段设置正确值。

## 备注

- 标签配置文件：`config/tags_config.json`
- 生产部署与数据库操作：`deploy/README.md`

## 相关文档

- API 集成说明：`docs/ascend-api-integration.md`
- SubTag InfoBox 后端契约：`docs/subtag-infobox-backend.md`
- 知识问答（QA）：`../docs/knowledge-qa.md`
