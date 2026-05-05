# AscendAny Recommendation Module

This module is intentionally separate from the FastAPI runtime. It owns model
training, offline scoring, learning-path snapshot generation, and legacy
problem-bank import.

Runtime boundary:

- The FastAPI app reads PostgreSQL snapshots only.
- This module uses its own Python 3.12 environment with PyTorch/PyG for training.
- The local training environment is `services/recommendation/.venv`, not the repo root `.venv`.
- CUDA training is pinned to PyTorch 2.10.0 `cu128` plus PyG wheels for `torch-2.10.0+cu128`.
- Training fails without `torch` and `torch-geometric`; there is no heuristic fallback.
- No code here reads the old `x/` project at runtime.

Typical commands:

```bash
cd services/recommendation
uv sync --extra dev
uv run ascendany-recommendation-check-cuda
uv run ascendany-import-legacy-problem-bank --input legacy_problem_bank.json --sql-report verify.sql --invalid-tag-report invalid_tags.csv
uv run ascendany-recommendation --run-id 1 --artifacts-dir ../../var/recommendation/artifacts --config config/rtx4060_8g.yaml
```

The checked-in `config/rtx4060_8g.yaml` is the local RTX 4060 8GB profile:
`device: cuda`, R-GCN, `hidden_dim: 64`, `batch_size: 128`, and
`num_negatives: 3`. Increase `batch_size` only after
`ascendany-recommendation-check-cuda` and a real training run show stable
memory headroom.

`ascendany-import-legacy-problem-bank` expects a one-time exported
JSON/JSONL/CSV file with these columns:

- `problem_id`, `title`, `description`, `category_tags`
- `solution_1`, `solution_2`, `link`
- `submission_count`, `pass_count`, `active`

The import is idempotent by `problem_id`: re-running it updates the same
AscendAny-owned rows and rewrites that problem's tag set.
