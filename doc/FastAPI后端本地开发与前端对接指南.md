# FastAPI 后端本地开发与前端对接指南（v0）

> 目标：本电脑只做开发与测试；需要后端联调时临时启动 FastAPI，并通过 SSH tunnel 使用 km6 上的 PostgreSQL + PgBouncer `6432`。生产部署入口见 `deploy/README.md`。

## 1. 前端当前对后端的真实需求

### 1.1 设置页输入（用户侧）
- 通用设置目前仅保留两项：
  - `studentId`（学号）
  - `ptaNickname`（PTA 账号昵称）
- 参考：`apps/desktop/src/components/settings/SettingsDialog.tsx`
- 存储：`apps/desktop/src/stores/settingsStore.ts`

### 1.2 能力与 rating 数据结构（必须兼容）
- 前端指标类型定义：`apps/desktop/src/types/metrics.ts`
- 核心字段：

```json
{
  "metrics": {
    "knowledge": 72,
    "accuracy": 65,
    "quality": 58,
    "flexibility": 44,
    "proficiency": 81
  },
  "rating": {
    "current": 888,
    "lastDelta": -8,
    "history": [
      {
        "examId": "5",
        "examName": "ICPC训练赛 d5",
        "date": "2025-05-01",
        "oldRating": 896,
        "delta": -8,
        "newRating": 888
      }
    ]
  }
}
```

### 1.3 对话数据结构（后续替换 mock 回复）
- 对话消息定义：`apps/desktop/src/types/chat.ts`
- 当前 `ChatInput` 里仍是本地模拟回复，需要后端接口替换：
  - 参考：`apps/desktop/src/components/chat/ChatInput.tsx`

### 1.4 自动分析触发依赖
- 前端已预留接口检查逻辑（未接线）：`apps/desktop/src/hooks/useAutoAnalysis.ts`
- 需要后端提供：`latest_exam_imported_at`（或等价字段）。

## 2. 建议先实现的本地联调 API（MVP）

建议统一前缀：`/api/v1`

### 2.1 健康检查
- `GET /api/v1/healthz`
- 返回：

```json
{ "ok": true }
```

### 2.2 最新导入考试时间（支持自动分析触发）
- `GET /api/v1/meta/latest_exam_imported_at`
- 返回：

```json
{ "latestExamImportedAt": "2026-02-13T09:30:00Z" }
```

无数据时：

```json
{ "latestExamImportedAt": null }
```

### 2.3 学生仪表盘（指标 + rating）
- `GET /api/v1/students/dashboard?studentId=...&ptaNickname=...`
- 说明：
  - 优先 `studentId` 精确定位。
  - 若 `studentId` 为空，可用 `ptaNickname` 尝试匹配。
  - 两者都为空返回 400。
  - 找不到学生返回 404。
- 返回体建议直接兼容前端 `metrics + rating` 结构（见 1.2）。

### 2.4 聊天回复（后续）
- `POST /api/v1/chat/reply`
- 请求建议：

```json
{
  "studentId": "20230001",
  "ptaNickname": "alice",
  "messages": [
    { "role": "user", "content": "请分析我最近的能力变化" }
  ],
  "summary": ""
}
```

- 响应建议：

```json
{
  "reply": "你的知识与熟练度较高，但灵活性偏弱...",
  "summary": "可选：后端压缩后的上下文摘要"
}
```

## 3. 与现有数据库表的映射建议

DDL 参考：`db/schema/*.sql`

- 学生定位：
  - 主表：`ascendany.students`
  - 身份映射：`ascendany.student_identities`
- 当前画像：`ascendany.student_current_metrics`
- rating 轨迹：`ascendany.rating_history` + `ascendany.exams`
- 最新导入时间：`ascendany.ingest_runs` / `ascendany.ingest_exam_runs`

建议查询口径：
- `metrics`：取 `student_current_metrics` 最新值。
- `rating.current`：取 `student_current_metrics.rating`。
- `rating.history`：按考试时间倒序（建议用 `exams.starts_at`，为空时回退 `rating_history.created_at`）。

## 4. 本地启动建议（临时联调）

本地不维护 PostgreSQL 实例，也不常驻 API。需要后端联调时：

1. 建立到 km6 PgBouncer 的 SSH local forwarding，详见 `deploy/README.md`。
2. 使用仓库 `.venv`（`uv` 管理，不用系统 Python）。
3. 安装后端开发依赖。
4. 临时启动 `127.0.0.1:8000`。

```bash
uv pip install --python .venv/bin/python -r apps/api/requirements-dev.txt
uv run --python .venv/bin/python uvicorn apps.api.main:app --host 127.0.0.1 --port 8000 --reload
```

数据库连接约束：
- 默认走 PgBouncer：`127.0.0.1:6432`
- 本地 `127.0.0.1:6432` 必须指向 km6 PgBouncer。
- 不要把密码写进仓库；优先 `~/.pgpass` 或环境变量。

## 5. 前后端联调落地顺序（建议）

1. 先实现 `healthz` + `students/dashboard`，替换 `metricsStore.loadMockData()`。
2. 再实现 `meta/latest_exam_imported_at`，接通 `useAutoAnalysis`。
3. 最后实现 `chat/reply`，替换 `ChatInput` 模拟回复。

## 6. 验收清单

- 桌面端不依赖 mock 也能渲染右侧指标与 rating。
- `Rating 历史` 展开后仅历史列表区域滚动。
- 学号/PTA 昵称任一可用于定位学生（有明确 400/404 返回）。
- API 临时启动后可稳定连接 km6 数据库。
