from __future__ import annotations

import sys
from pathlib import Path

import pytest

SERVICE_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SERVICE_ROOT))

from recommendation.db import BankProblem, StudentRecord, SubmissionRecord
from recommendation.pathing import plan_learning_path
from recommendation.scoring import (
    BankRecommendationConfig,
    build_profiles,
    score_recommendations,
)


def test_recommendation_config_accepts_flat_runtime_aliases() -> None:
    config = BankRecommendationConfig.from_dict(
        {
            "model_score_weight": 0.5,
            "target_knowledge_weight": 0.35,
            "similarity_weight": 0.2,
            "difficulty_weight": 0.15,
            "popularity_weight": 0.1,
            "difficulty_tolerance": 0.4,
            "mmr_lambda": 0.72,
        }
    )

    assert config.weight_gnn == 0.5
    assert config.weight_knowledge == 0.35
    assert config.weight_text == 0.2
    assert config.weight_difficulty == 0.15
    assert config.weight_popularity == 0.1
    assert config.difficulty_sigma == 0.4
    assert config.mmr_lambda == 0.72


def test_bank_recommender_requires_gnn_scores_and_excludes_done() -> None:
    students = [StudentRecord(1, "20230001", "Alice", 900)]
    submissions = [
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="P_DONE",
            problem_title="已做题",
            submitted_at=None,
            score=100,
            max_score=100,
            score_rate=1.0,
            verdict="Accepted",
            is_correct=True,
        ),
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="P_WEAK",
            problem_title="最短路练习",
            submitted_at=None,
            score=20,
            max_score=100,
            score_rate=0.2,
            verdict="Wrong Answer",
            is_correct=False,
        ),
    ]
    bank = [
        BankProblem("P_DONE", "已做题", "", None, 100, 100, ["最短路"], True),
        BankProblem("P_NEXT", "Dijkstra", "最短路", None, 100, 40, ["最短路"], True),
    ]
    profiles = build_profiles(
        students,
        submissions,
        {"P_DONE": ["最短路"], "P_WEAK": ["最短路"]},
        config=BankRecommendationConfig(min_profile_problems=1),
    )

    payload = score_recommendations(
        profiles,
        bank,
        model_scores={1: {"P_DONE": 0.99, "P_NEXT": 0.88}},
        top_k=10,
        config=BankRecommendationConfig(min_profile_problems=1),
    )

    assert [item["problemId"] for item in payload[1]] == ["P_NEXT"]
    assert payload[1][0]["meta"]["gnnScore"] == 0.88

    with pytest.raises(ValueError, match="model scores are required"):
        score_recommendations(
            profiles,
            bank,
            model_scores=None,  # type: ignore[arg-type]
            top_k=10,
            config=BankRecommendationConfig(min_profile_problems=1),
        )


def test_path_planner_uses_prerequisite_closure_and_mastery_order() -> None:
    students = [StudentRecord(1, "20230001", "Alice", 900)]
    submissions = [
        SubmissionRecord(
            student_id=1,
            student_key="1",
            practice_problem_id="P_DP",
            problem_title="动态规划",
            submitted_at=None,
            score=10,
            max_score=100,
            score_rate=0.1,
            verdict="Wrong Answer",
            is_correct=False,
        ),
    ]
    profile = build_profiles(
        students,
        submissions,
        {"P_DP": ["动态规划"]},
        config=BankRecommendationConfig(min_profile_problems=1),
    )[1]

    plan = plan_learning_path(
        profile,
        prereq_map={"动态规划": {"递归"}},
        top_n_targets=1,
        min_evidence=1,
        include_mastered=True,
    )

    assert plan.targets == ["动态规划"]
    assert plan.path == ["递归", "动态规划"]
