# AscendAny (学生能力分析平台) - Agent Instructions

## 项目现状（2026-02）
- 增量预处理链路已落地：`preprocess/` 可扫描、解析、幂等导入并计算指标与 rating。
- 后端已落地：`apps/api/`（FastAPI，含认证、学生画像、导入任务与 Agent 相关接口）。
- 桌面端已落地：`apps/desktop/`（Electron + Vite + React）。
- 导入控制台已落地：`apps/import-console/`（管理员 Web 控制台，SSE 进度流）。
- 官网已落地：`apps/site/`（产品介绍与下载入口）。
- `packages/shared/` 目前未落地；新增共享类型前先评估是否真的需要拆包。

## 目标与约束
- `practice/` 的**每一个单位数据 = 一场考试**（目录级别的一个子目录）。
- 输入数据是**增量**的；旧数据需要**永久保留**。
- 预处理/导入必须**可重复运行**：重复执行不会重复导入旧数据，只处理新增考试（或新增“快照”）。
- 数据库为 PostgreSQL，应用默认通过 **6432 的 PgBouncer** 访问；连接参数必须可配置（不要写死）。

## 数据源实践规范（必须遵守）
- `practice` 根目录必须来自配置（如 `PRACTICE_DATA_ROOT`），不要依赖机器固定路径。
- 解析时要处理多编码，默认按顺序尝试：`utf-8` -> `utf-8-sig` -> `gb18030`。
- 经验规则：
  - `datastructure/.../提交记录/*.csv` 多为 `utf-8`。
  - `pta_*` 提交记录 CSV 常见为 `gb18030`（检测可能显示 `iso-8859-1/unknown-8bit`）。
- 解析 CSV/XLSX 时字段值常带尾部 `\t`，入库前需要 `strip()`。

## 数据库与导入硬性要求
- 以“考试”为事务边界：单场考试导入要么全部成功，要么回滚。
- 通过唯一约束 + 指纹（hash）实现幂等：
  - 考试唯一键：`(exam_type, source_path)`。
  - 行级唯一键：如 `submissions(exam_id, row_hash)`。
- 增量状态必须可追踪：
  - 导入批次：`ingest_runs`。
  - 考试级结果：`ingest_exam_runs`。
  - 控制台任务与事件流：`import_tasks`、`import_task_events`。
- 公式与参数不要写死在代码里：半衰期、阈值、分位映射策略等应放配置或 DB，便于调参与回放。

## 配置与安全约束
- 预处理默认配置：`preprocess/config/default.yaml`，可被 `--config`、CLI 参数和环境变量覆盖。
- API 默认配置：`apps/api/config/default.yaml`，可由 `ASCENDANY_API_CONFIG` 覆盖。
- 数据库密码优先走 `~/.pgpass` 或环境变量（如 `ASCENDANY_DB_PASSWORD`），不要提交明文凭据。
- 模型 API Key 必须使用环境变量，不得写入仓库文件。

## 工程与测试
- Python 统一使用仓库内 `.venv`（`uv` 管理），禁止安装到系统全局环境。
- 运行测试时必须使用 `.venv` 解释器（如 `.venv/bin/pytest` 或 `uv run pytest`），不要直接用系统 `pytest`，避免出现 `ModuleNotFoundError`（如缺少 `fastapi`）等环境偏差问题。
- 后端/预处理新增功能必须补 `pytest`，重点覆盖：增量幂等、编码解析、指标/rating、导入任务流程。
- 前端使用 TypeScript 严格模式；关键交互（分栏拖拽、上下文清空、自动 compact）应有单测或 e2e 覆盖。

##
使用ssh ascend连接服务器
