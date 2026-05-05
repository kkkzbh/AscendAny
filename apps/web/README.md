# AscendWeb

本目录是前端 SPA（React + TypeScript + Vite），用于展示知识图谱、OJ 题库、知识问答等页面。

后端默认是同仓库的 Django 服务（`code/django_project/`），API 前缀为 `/api`，WebSocket 前缀为 `/ws`。

## 开发（本地）

推荐直接在 `code/django_project/` 下用一键脚本（同时起后端 + worker + 前端）：

```bash
python start.py
```

Windows 也可以继续用 `start.bat`（旧版，多窗口）。

只运行前端（需要你自己先启动后端）：

```bat
cd AscendWeb
npm install
npm run dev
```

默认：

- Vite 端口：5175（见 `vite.config.ts`）
- Vite 会将 `/api` 与 `/ws` 代理到 `http://127.0.0.1:8000`

## 构建（静态站点）

```bat
cd AscendWeb
npm install
npm run build
```

产物输出到：`AscendWeb/dist/`。

## 环境变量（Vite）

前端使用的环境变量：

- `VITE_BACKEND_ORIGIN`
  - 为空：使用相对路径请求（本地开发通常依赖 Vite proxy）
  - 非空：把 `/api/*`、`/ws/*` 拼到该 Origin（独立部署/跨域时需要）
- `VITE_APP_BASENAME`
  - SPA Router basename，默认 `/`
  - 例如把站点部署到 `/app/` 下时可设为 `/app`

说明：`VITE_*` 会在 build 时注入并打包进产物；独立部署时务必在构建阶段设置正确值。

## 备注

- 标签配置文件：`config/tags_config.json`
- Windows 终端：如需在命令行临时设置环境变量，`cmd`/PowerShell 语法不同；推荐使用 `start.py`（或 `start.bat`）或编辑 `.env` 文件

## 相关文档

- API 集成说明：`docs/ascend-api-integration.md`
- SubTag InfoBox 后端契约：`docs/subtag-infobox-backend.md`
- 知识问答（QA）：`../docs/knowledge-qa.md`
