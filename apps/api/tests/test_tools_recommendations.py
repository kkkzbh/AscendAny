from __future__ import annotations

import asyncio
import json
from datetime import datetime, timezone

from apps.api.db.repository import (
    LearningPathSnapshotRow,
    ProblemRecommendationSnapshotRow,
)
from apps.api.services.identity import ResolvedIdentity
from apps.api.services.tools import ToolExecutor


GENERATED_AT = datetime(2026, 5, 6, 8, 30, tzinfo=timezone.utc)


def _identity() -> ResolvedIdentity:
    return ResolvedIdentity(
        student_entity_id=42,
        student_id="20240001",
        pta_nickname=None,
        no_submission_records=False,
        matched_by="student_id",
        student_entity_ids=(42,),
    )


def _execute(executor: ToolExecutor, tool: str, args: dict) -> dict:
    return json.loads(asyncio.run(executor.execute(tool, args)))


class _RecRepo:
    def __init__(self, items: list[dict]) -> None:
        self._items = items
        self.last_top_k_arg: object = "<unset>"

    async def fetch_latest_problem_recommendations(
        self, student_ids, top_k=10
    ):
        self.last_top_k_arg = top_k
        return ProblemRecommendationSnapshotRow(
            student_id=student_ids[0],
            model_run_id=7,
            items=list(self._items),
            generated_at=GENERATED_AT,
        )


class _PathRepo:
    def __init__(self, snapshot: LearningPathSnapshotRow | None) -> None:
        self._snapshot = snapshot

    async def fetch_latest_learning_path(self, student_ids):
        return self._snapshot


def _sample_items() -> list[dict]:
    return [
        {
            "problemId": "p001",
            "title": "最长上升子序列",
            "knowledgePoints": ["动态规划 DP", "线性 DP"],
            "difficulty": 1500,
            "rank": 1,
            "score": 0.91,
        },
        {
            "problemId": "p002",
            "title": "二分图最大匹配",
            "knowledgePoints": ["图论"],
            "difficulty": 1900,
            "rank": 2,
            "score": 0.85,
        },
        {
            "problemId": "p003",
            "title": "区间 DP 合并",
            "knowledgePoints": ["动态规划 DP", "区间 DP"],
            "difficulty": 1700,
            "rank": 3,
            "score": 0.80,
        },
        {
            "problemId": "p004",
            "title": "树的直径",
            "knowledgePoints": ["树形 DP", "图论"],
            "difficulty": None,
            "rank": 4,
            "score": 0.78,
        },
        {
            "problemId": "p005",
            "title": "网络流入门",
            "knowledgePoints": ["图论", "网络流"],
            "difficulty": 2100,
            "rank": 5,
            "score": 0.70,
        },
    ]


def test_recommendations_no_filters_returns_top_k_with_summary() -> None:
    repo = _RecRepo(_sample_items())
    executor = ToolExecutor(
        repository=repo,  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(executor, "get_problem_recommendations", {"top_k": 3})

    assert repo.last_top_k_arg is None  # handler now drives filtering itself
    assert [item["problemId"] for item in payload["items"]] == ["p001", "p002", "p003"]
    assert payload["filter_summary"] == {
        "snapshot_total": 5,
        "matched_after_filters": 5,
        "returned": 3,
        "filters_active": False,
    }
    assert payload["model_run_id"] == 7


def test_recommendations_filter_by_knowledge_point_substring_case_insensitive() -> None:
    executor = ToolExecutor(
        repository=_RecRepo(_sample_items()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(
        executor,
        "get_problem_recommendations",
        {"knowledge_point": "dp"},
    )

    ids = [item["problemId"] for item in payload["items"]]
    assert ids == ["p001", "p003", "p004"]
    assert payload["filter_summary"]["filters_active"] is True
    assert payload["filter_summary"]["matched_after_filters"] == 3


def test_recommendations_difficulty_range_excludes_missing_difficulty() -> None:
    executor = ToolExecutor(
        repository=_RecRepo(_sample_items()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(
        executor,
        "get_problem_recommendations",
        {"min_difficulty": 1600, "max_difficulty": 2000},
    )

    ids = [item["problemId"] for item in payload["items"]]
    # p001=1500 too low, p004=None excluded, p005=2100 too high
    assert ids == ["p002", "p003"]


def test_recommendations_exclude_problem_ids() -> None:
    executor = ToolExecutor(
        repository=_RecRepo(_sample_items()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(
        executor,
        "get_problem_recommendations",
        {"exclude_problem_ids": ["p001", "p005"]},
    )

    ids = [item["problemId"] for item in payload["items"]]
    assert ids == ["p002", "p003", "p004"]
    assert payload["filter_summary"]["matched_after_filters"] == 3


def test_recommendations_combined_filters_then_top_k() -> None:
    executor = ToolExecutor(
        repository=_RecRepo(_sample_items()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(
        executor,
        "get_problem_recommendations",
        {
            "knowledge_point": "图论",
            "exclude_problem_ids": ["p005"],
            "top_k": 1,
        },
    )

    ids = [item["problemId"] for item in payload["items"]]
    assert ids == ["p002"]
    assert payload["filter_summary"]["matched_after_filters"] == 2
    assert payload["filter_summary"]["returned"] == 1


def test_recommendations_no_snapshot_returns_error_payload() -> None:
    class _EmptyRepo:
        async def fetch_latest_problem_recommendations(self, student_ids, top_k=10):
            return None

    executor = ToolExecutor(
        repository=_EmptyRepo(),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(executor, "get_problem_recommendations", {})

    assert payload["items"] == []
    assert "error" in payload


def test_recommendations_without_identity_returns_error() -> None:
    executor = ToolExecutor(
        repository=_RecRepo(_sample_items()),  # type: ignore[arg-type]
        identity=None,
    )

    payload = _execute(executor, "get_problem_recommendations", {})

    assert "error" in payload
    assert "items" not in payload


def _path_snapshot() -> LearningPathSnapshotRow:
    return LearningPathSnapshotRow(
        student_id=42,
        model_run_id=11,
        targets=["编程能力提升"],
        path=["基础数据结构", "动态规划 DP", "图论", "网络流"],
        explanations={
            "基础数据结构": "栈/队列/链表的常见模式",
            "动态规划 DP": "状态转移方程的设计套路",
            "图论": "建图与最短路问题",
            "网络流": "最大流与费用流入门",
        },
        generated_at=GENERATED_AT,
    )


def test_learning_path_default_returns_full_snapshot() -> None:
    executor = ToolExecutor(
        repository=_PathRepo(_path_snapshot()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(executor, "get_learning_path", {})

    assert payload["path"] == [
        "基础数据结构",
        "动态规划 DP",
        "图论",
        "网络流",
    ]
    assert payload["path_total"] == 4
    assert "explanations" in payload
    assert payload["explanations"]["图论"] == "建图与最短路问题"
    assert "note" not in payload


def test_learning_path_topic_filter_returns_only_matching_step() -> None:
    executor = ToolExecutor(
        repository=_PathRepo(_path_snapshot()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(executor, "get_learning_path", {"topic": "dp"})

    assert payload["path"] == ["动态规划 DP"]
    assert payload["explanations"] == {"动态规划 DP": "状态转移方程的设计套路"}
    assert payload["path_total"] == 4


def test_learning_path_topic_no_match_returns_empty_path_with_note() -> None:
    executor = ToolExecutor(
        repository=_PathRepo(_path_snapshot()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(executor, "get_learning_path", {"topic": "量子力学"})

    assert payload["path"] == []
    assert "note" in payload
    assert "量子力学" in payload["note"]


def test_learning_path_limit_truncates_path_and_explanations() -> None:
    executor = ToolExecutor(
        repository=_PathRepo(_path_snapshot()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(executor, "get_learning_path", {"limit": 2})

    assert payload["path"] == ["基础数据结构", "动态规划 DP"]
    assert set(payload["explanations"].keys()) == {
        "基础数据结构",
        "动态规划 DP",
    }
    assert payload["path_total"] == 4


def test_learning_path_topic_takes_precedence_over_limit() -> None:
    executor = ToolExecutor(
        repository=_PathRepo(_path_snapshot()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(
        executor,
        "get_learning_path",
        {"topic": "网络流", "limit": 1},
    )

    assert payload["path"] == ["网络流"]


def test_learning_path_include_explanations_false_drops_field() -> None:
    executor = ToolExecutor(
        repository=_PathRepo(_path_snapshot()),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(
        executor,
        "get_learning_path",
        {"include_explanations": False},
    )

    assert "explanations" not in payload
    assert payload["path_total"] == 4


def test_learning_path_no_snapshot_returns_error_payload() -> None:
    executor = ToolExecutor(
        repository=_PathRepo(None),  # type: ignore[arg-type]
        identity=_identity(),
    )

    payload = _execute(executor, "get_learning_path", {})

    assert payload["path"] == []
    assert "error" in payload
