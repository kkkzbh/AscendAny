from __future__ import annotations

import asyncio
from dataclasses import dataclass

from apps.api.services.web_learning import WebLearningService
from services.recommendation.recommendation.pathing import PathPlanner, PracticeAttempt


def test_metrics_student_empty_response_matches_legacy_web_contract() -> None:
    service = WebLearningService(repository=None, settings=None)  # type: ignore[arg-type]

    result = asyncio.run(service.metrics_student({"student": "xixi"}))

    assert result["student"] == "xixi"
    assert isinstance(result["computed_at"], str)
    assert "metrics" in result
    assert "knowledge" not in result
    assert set(result["metrics"]) == {
        "knowledge_points",
        "proficiency",
        "accuracy",
        "flexibility",
        "quality",
    }
    assert result["metrics"]["knowledge_points"]["score"] == 0.0
    assert result["metrics"]["knowledge_points"]["error"] == "暂无指标数据"
    assert result["summary"]["overall_score"] == 0.0
    assert result["summary"]["strongest"] is None
    assert result["summary"]["weakest"] is None


def test_metric_score_is_normalized_for_legacy_percent_values() -> None:
    assert WebLearningService._score01(72) == 0.72
    assert WebLearningService._score01(0.83) == 0.83
    assert WebLearningService._score01(120) == 1.0
    assert WebLearningService._score01(-5) == 0.0


@dataclass(frozen=True)
class FakeIdentity:
    student_entity_id: int = 1
    student_entity_ids: tuple[int, ...] = (1,)


def test_path_plan_uses_live_path_planner(monkeypatch) -> None:
    service = WebLearningService(repository=None, settings=None)  # type: ignore[arg-type]
    planner = PathPlanner(
        [
            PracticeAttempt(1, "p_array", score_rate=1.0, is_correct=True),
            PracticeAttempt(1, "p_list", score_rate=0.0, is_correct=False),
        ],
        {"p_array": ["数组"], "p_list": ["链表"]},
        prereq_map={"链表": {"数组"}},
    )

    async def fake_identity(_student: str) -> FakeIdentity:
        return FakeIdentity()

    async def fake_planner(_student_entity_id: int, *, attempt_penalty_alpha: float) -> PathPlanner:
        assert attempt_penalty_alpha == 0.15
        return planner

    monkeypatch.setattr(service, "_resolve_identity_or_none", fake_identity)
    monkeypatch.setattr(service, "_build_path_planner", fake_planner)

    result = asyncio.run(
        service.path_plan(
            {
                "student": "xixi",
                "top_n_targets": 1,
                "include_mastered": True,
                "max_path_len": 2,
            }
        )
    )

    assert result["targets"] == ["链表"]
    assert result["path"] == ["数组", "链表"]
    assert result["mastery"]["链表"] == 0.0


def test_mastery_returns_hierarchy_from_live_estimate(monkeypatch) -> None:
    service = WebLearningService(repository=None, settings=None)  # type: ignore[arg-type]
    planner = PathPlanner(
        [
            PracticeAttempt(1, "p_array", score_rate=1.0, is_correct=True),
            PracticeAttempt(1, "p_list", score_rate=0.0, is_correct=False),
        ],
        {"p_array": ["数组"], "p_list": ["链表"]},
        prereq_map={},
    )

    async def fake_identity(_student: str) -> FakeIdentity:
        return FakeIdentity()

    async def fake_planner(_student_entity_id: int, *, attempt_penalty_alpha: float) -> PathPlanner:
        return planner

    async def fake_recommendations(*args, **kwargs):  # noqa: ANN002, ANN003
        return []

    monkeypatch.setattr(service, "_resolve_identity_or_none", fake_identity)
    monkeypatch.setattr(service, "_build_path_planner", fake_planner)
    monkeypatch.setattr(service, "_recommendations", fake_recommendations)

    result = asyncio.run(service.mastery({"student": "xixi", "top_k": 3}))

    assert "线性结构" in result["knowledge_mastery"]
    assert result["flat_mastery"]["数组"]["mastery"] == 1.0
    assert result["flat_mastery"]["链表"]["mastery"] == 0.0
    assert "链表" in result["weak_points"]
