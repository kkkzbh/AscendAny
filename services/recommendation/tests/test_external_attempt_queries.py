from __future__ import annotations

from pathlib import Path


def test_recommendation_training_uses_external_problem_links() -> None:
    source = Path("services/recommendation/recommendation/db.py").read_text(
        encoding="utf-8"
    )

    assert "exam_problem_external_links" in source
    assert "source_platform || ':' || epl.external_problem_id" in source
    assert "replace(e.source_path" not in source
    assert "oj_submit_records" not in source


def test_web_learning_default_attempts_do_not_union_internal_oj() -> None:
    source = Path("apps/api/services/web_learning.py").read_text(encoding="utf-8")

    assert "exam_problem_external_links" in source
    assert "source_platform || ':' || epl.external_problem_id" in source
    assert "UNION ALL" not in source
