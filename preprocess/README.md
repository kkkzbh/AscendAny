# preprocess（预处理与增量导入）

本目录已落地为可执行的增量导入流水线：
- 扫描 `practice` 考试目录，按 `exam_type + source_path` 识别考试单位。
- 以文件集合指纹（fingerprint）判断“新增考试”或“新增快照”。
- 解析 `答卷 HTML + 提交记录 CSV + 成绩单 XLSX`。
- 幂等写入 PostgreSQL（`(exam_id, row_hash)` 去重）。
- 每场计算五大指标（0-100）与 rating 变化，并维护 `student_current_metrics`。

## 目录结构

- `preprocess/cli.py`：CLI 入口。
- `preprocess/config.py`：配置加载（YAML + 环境变量覆盖）。
- `preprocess/discover.py`：增量扫描与 fingerprint。
- `preprocess/extract/`：CSV/XLSX/HTML 解析。
- `preprocess/load/`：入库仓储与导入服务。
- `preprocess/linking/`：提交 actor -> 学生实体后处理映射。
- `preprocess/derive/`：指标、rating、当前画像融合。
- `preprocess/tests/`：单元测试（编码、幂等 hash、指标与 rating）。

## 安装依赖

```bash
uv venv .venv
uv pip install --python .venv/bin/python -r preprocess/requirements-dev.txt
```

## 配置

默认配置文件：`preprocess/config/default.yaml`  
可通过以下方式覆盖：
- 环境变量：`PRACTICE_DATA_ROOT`
- CLI 参数：`--practice-root`、`--db-dsn`、`--db-host`、`--db-port`、`--db-name`、`--db-user`

`mapping` 配置支持：
- `primary_keys`：匹配优先键（如 `student_no`、`name`）。
- `actor_sources`：参与映射的 actor 来源（支持 `*` 通配符）。
- `strict_mode`：冲突时是否跳过（`true` 跳过并标记为 ambiguous）。

数据库默认走 PgBouncer `6432`，密码建议来自 `~/.pgpass` 或 `ASCENDANY_DB_PASSWORD`。

## 使用方式

仅扫描：

```bash
uv run --python .venv/bin/python -m preprocess.cli discover
```

执行增量导入：

```bash
uv run --python .venv/bin/python -m preprocess.cli run
```

试运行（只统计，不写库）：

```bash
uv run --python .venv/bin/python -m preprocess.cli run --dry-run
```

只处理特定考试类型：

```bash
uv run --python .venv/bin/python -m preprocess.cli run --exam-type datastructure --exam-type pta_icpc
```

限制处理数量（调试）：

```bash
uv run --python .venv/bin/python -m preprocess.cli run --limit 3
```

执行 actor 后处理映射：

```bash
uv run --python .venv/bin/python -m preprocess.cli link-actors
```

actor 映射试运行：

```bash
uv run --python .venv/bin/python -m preprocess.cli link-actors --dry-run
```

## 注意事项

- 解析编码按顺序尝试：`utf-8` → `utf-8-sig` → `gb18030`。
- CSV/XLSX 字段统一 `strip()`，去除尾部 `\t`。
- 提交记录默认保留 actor 信息（`actor_source/actor_external_id/actor_name`），不强制绑定 `students`。
- 每场考试导入以事务为边界；失败回滚并写入 `ingest_exam_runs`。
- 建议始终用 `uv run --python .venv/bin/python ...` 执行命令，不使用系统全局 Python 依赖。
