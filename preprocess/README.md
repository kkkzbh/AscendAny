# preprocess（预处理与增量导入）

本目录预留给“practice 增量数据 -> PostgreSQL -> 指标/rating”的代码实现。

设计约束（必须满足）：
- 输入为增量：重复运行不会重复导入旧数据。
- 旧数据永久保留：不得通过“清库重算”实现增量。
- 以“考试单位目录”为事务边界导入。

建议后续实现的入口：
- `preprocess/discover.py`: 扫描 `PRACTICE_DATA_ROOT`，识别新增考试单位/快照
- `preprocess/extract_*`: 按类型解析 CSV/XLSX/HTML
- `preprocess/load.py`: 幂等写入 DB（`row_hash` 去重）
- `preprocess/derive_metrics.py`: 五大指标相对打分
- `preprocess/derive_rating.py`: rating 回放计算（参考 Codeforces 框架）

更多规范见：
- `doc/增量输入数据规范.md`
- `doc/增量预处理与幂等导入.md`
- `doc/数据库设计.md`
- `doc/五大能力指标与综合能力分设计.md`
