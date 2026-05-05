from __future__ import annotations

import pytest

from recommendation.knowledge import KnowledgeHierarchyAggregator, KnowledgeTree
from recommendation.pathing import PathPlanner, PracticeAttempt


def test_path_planner_estimates_mastery_with_best_score_and_attempt_penalty() -> None:
    planner = PathPlanner(
        [
            PracticeAttempt(1, "p1", score_rate=0.0, is_correct=False),
            PracticeAttempt(1, "p1", score_rate=1.0, is_correct=True),
            PracticeAttempt(1, "p1", score_rate=0.5, is_correct=False),
        ],
        {"p1": ["数组"]},
        prereq_map={},
        attempt_penalty_alpha=0.15,
    )

    estimate = planner.estimate_mastery(1)

    assert estimate.evidence["数组"] == 1
    assert estimate.mastery["数组"] == pytest.approx(0.740818, rel=1e-4)


def test_path_planner_uses_closure_filters_mastered_and_respects_limit() -> None:
    planner = PathPlanner(
        [
            PracticeAttempt(1, "p_array", score_rate=1.0, is_correct=True),
            PracticeAttempt(1, "p_list", score_rate=0.0, is_correct=False),
            PracticeAttempt(1, "p_dp", score_rate=0.1, is_correct=False),
        ],
        {
            "p_array": ["数组"],
            "p_list": ["链表"],
            "p_dp": ["动态规划"],
        },
        prereq_map={"链表": {"数组"}, "动态规划": {"递归"}},
    )

    filtered = planner.plan(1, top_n_targets=2, include_mastered=False)
    included = planner.plan(1, top_n_targets=2, include_mastered=True, max_path_len=2)

    assert filtered.targets == ["链表", "动态规划"]
    assert "数组" not in filtered.path
    assert filtered.path.index("递归") < filtered.path.index("动态规划")
    assert len(included.path) == 2
    if "动态规划" in included.path:
        assert included.path.index("递归") < included.path.index("动态规划")


def test_knowledge_hierarchy_aggregates_children_and_prefers_evidence_weakness() -> None:
    tree = KnowledgeTree({"线性结构": {"数组": ["矩阵"], "链表": None}})
    aggregator = KnowledgeHierarchyAggregator(
        tree,
        {"矩阵": 1.0, "链表": 0.2},
        {"矩阵": 3, "链表": 2},
    )

    hierarchy = aggregator.build_hierarchy()

    assert hierarchy["线性结构"]["children"]["链表"]["mastery"] == 0.2
    assert aggregator.get_mastery("线性结构") < 1.0
    assert aggregator.identify_weak_points(threshold=0.6, top_k=1) == ["链表"]
