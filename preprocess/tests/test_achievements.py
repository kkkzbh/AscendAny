from __future__ import annotations

from preprocess.load.achievements import (
    AchievementDefinition,
    build_state_rows,
    evaluate_tier,
)


def test_evaluate_tier_boundary_values() -> None:
    assert evaluate_tier(progress=0, bronze_target=1, silver_target=3, gold_target=8) == 0
    assert evaluate_tier(progress=1, bronze_target=1, silver_target=3, gold_target=8) == 1
    assert evaluate_tier(progress=3, bronze_target=1, silver_target=3, gold_target=8) == 2
    assert evaluate_tier(progress=8, bronze_target=1, silver_target=3, gold_target=8) == 3


def test_build_state_rows_is_deterministic_for_repeated_inputs() -> None:
    definitions = [
        AchievementDefinition(
            achievement_code="exam_count_first",
            progress_key="exam_count",
            bronze_target=1,
            silver_target=3,
            gold_target=8,
        ),
        AchievementDefinition(
            achievement_code="ai_dialogue_count",
            progress_key="ai_dialogue_count",
            bronze_target=3,
            silver_target=15,
            gold_target=40,
        ),
    ]
    progress_by_student = {
        101: {"exam_count": 4, "ai_dialogue_count": 2},
        102: {"exam_count": 10, "ai_dialogue_count": 50},
    }
    student_ids = [101, 102]

    first = build_state_rows(
        student_ids=student_ids,
        definitions=definitions,
        progress_by_student=progress_by_student,
    )
    second = build_state_rows(
        student_ids=student_ids,
        definitions=definitions,
        progress_by_student=progress_by_student,
    )

    assert first == second
    assert first[0] == (101, "exam_count_first", 4.0, 2, 2)
    assert first[1] == (101, "ai_dialogue_count", 2.0, 0, 0)
    assert first[2] == (102, "exam_count_first", 10.0, 3, 3)
    assert first[3] == (102, "ai_dialogue_count", 50.0, 3, 3)
