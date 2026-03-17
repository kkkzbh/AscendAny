from __future__ import annotations

from datetime import datetime
from zoneinfo import ZoneInfo

from preprocess.config import RatingConfig
from preprocess.derive.metrics import compute_exam_metrics
from preprocess.derive.rating import compute_exam_rating
from preprocess.models import ParticipantRow


def _participant(
    student_id: int, rank: int, score: float, time_used: int, attempts: int
) -> ParticipantRow:
    return ParticipantRow(
        identity_source="test",
        external_id=f"s{student_id}",
        display_name=f"Student {student_id}",
        user_group="G1",
        rank=rank,
        total_score=score,
        time_used_seconds=time_used,
        solved_count=1,
        absent=False,
        problem_stats={
            "P1": {
                "solved": True,
                "score": score,
                "attempts": attempts,
                "runtime_ms": time_used * 2,
                "wrong_before_ac": max(0, attempts - 1),
            }
        },
        student_id=student_id,
    )


def test_compute_exam_metrics_outputs_range() -> None:
    participants = [
        _participant(1, rank=1, score=100.0, time_used=120, attempts=1),
        _participant(2, rank=2, score=80.0, time_used=240, attempts=2),
        _participant(3, rank=3, score=60.0, time_used=360, attempts=3),
    ]
    results = compute_exam_metrics(
        participants=participants,
        total_points=100.0,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
    )
    assert len(results) == 3
    for row in results:
        assert row.knowledge is not None
        assert 0 <= row.knowledge <= 100
        assert row.accuracy is not None
        assert 0 <= row.accuracy <= 100


def test_compute_exam_rating_direction() -> None:
    participants = [
        _participant(1, rank=1, score=100.0, time_used=120, attempts=1),
        _participant(2, rank=2, score=80.0, time_used=240, attempts=2),
        _participant(3, rank=3, score=60.0, time_used=360, attempts=3),
    ]
    ratings = {1: 800, 2: 800, 3: 800}
    results = compute_exam_rating(
        participants=participants, current_ratings=ratings, cfg=RatingConfig()
    )
    by_id = {item.student_id: item for item in results}
    assert by_id[1].delta >= by_id[2].delta
    assert by_id[2].delta >= by_id[3].delta


def test_compute_exam_metrics_prefers_timeline_flexibility() -> None:
    participants = [
        _participant(1, rank=1, score=100.0, time_used=120, attempts=2),
        _participant(2, rank=2, score=80.0, time_used=240, attempts=2),
    ]
    baseline = compute_exam_metrics(
        participants=participants,
        total_points=100.0,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
    )
    baseline_by_id = {item.student_id: item for item in baseline}
    assert baseline_by_id[1].details["flexibility_mode"] == "approx"

    timezone = ZoneInfo("Asia/Shanghai")
    timeline = {
        1: [
            {
                "submitted_at": datetime(2025, 3, 1, 10, 0, tzinfo=timezone),
                "problem_code": "P1",
                "verdict": "答案错误",
            },
            {
                "submitted_at": datetime(2025, 3, 1, 10, 3, tzinfo=timezone),
                "problem_code": "P2",
                "verdict": "答案正确",
            },
            {
                "submitted_at": datetime(2025, 3, 1, 10, 8, tzinfo=timezone),
                "problem_code": "P1",
                "verdict": "答案正确",
            },
        ]
    }
    with_timeline = compute_exam_metrics(
        participants=participants,
        total_points=100.0,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
        timeline_by_student=timeline,
    )
    by_id = {item.student_id: item for item in with_timeline}
    assert by_id[1].details["flexibility_mode"] == "timeline"
    assert by_id[1].details["ac_count"] == 2


def test_compute_exam_metrics_quality_avoids_division_by_zero() -> None:
    faster = _participant(1, rank=1, score=100.0, time_used=120, attempts=1)
    slower = _participant(2, rank=2, score=80.0, time_used=240, attempts=1)
    faster.problem_stats["P1"]["runtime_ms"] = 0
    slower.problem_stats["P1"]["runtime_ms"] = 100

    results = compute_exam_metrics(
        participants=[faster, slower],
        total_points=100.0,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
    )
    by_id = {item.student_id: item for item in results}

    assert by_id[1].quality is not None
    assert 0 <= by_id[1].quality <= 100
    assert by_id[2].quality is not None


def test_compute_exam_metrics_quality_ignores_unsolved_runtime() -> None:
    participant = _participant(1, rank=1, score=0.0, time_used=120, attempts=1)
    participant.problem_stats["P1"]["score"] = 0.0
    participant.problem_stats["P1"]["solved"] = False
    participant.problem_stats["P1"]["runtime_ms"] = 0

    results = compute_exam_metrics(
        participants=[participant],
        total_points=100.0,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
    )

    assert results[0].quality is None


def test_compute_exam_metrics_quality_falls_back_to_submission_runtime() -> None:
    faster = _participant(1, rank=1, score=100.0, time_used=120, attempts=1)
    slower = _participant(2, rank=2, score=80.0, time_used=240, attempts=1)
    faster.problem_stats["P1"].pop("runtime_ms")
    slower.problem_stats["P1"].pop("runtime_ms")

    without_timeline = compute_exam_metrics(
        participants=[faster, slower],
        total_points=100.0,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
    )
    without_timeline_by_id = {item.student_id: item for item in without_timeline}
    assert without_timeline_by_id[1].quality is None
    assert without_timeline_by_id[2].quality is None

    timezone = ZoneInfo("Asia/Shanghai")
    with_timeline = compute_exam_metrics(
        participants=[faster, slower],
        total_points=100.0,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
        timeline_by_student={
            1: [
                {
                    "submitted_at": datetime(2026, 3, 6, 10, 0, tzinfo=timezone),
                    "problem_code": "P1",
                    "verdict": "答案正确",
                    "time_ms": 80,
                }
            ],
            2: [
                {
                    "submitted_at": datetime(2026, 3, 6, 10, 0, tzinfo=timezone),
                    "problem_code": "P1",
                    "verdict": "答案正确",
                    "time_ms": 160,
                }
            ],
        },
    )
    with_timeline_by_id = {item.student_id: item for item in with_timeline}
    assert with_timeline_by_id[1].quality is not None
    assert with_timeline_by_id[2].quality is not None
    assert with_timeline_by_id[1].quality > with_timeline_by_id[2].quality


def test_compute_exam_metrics_random_exam_fill_unanswered_slots() -> None:
    first = ParticipantRow(
        identity_source="test",
        external_id="s1",
        display_name="Student 1",
        user_group="G1",
        rank=1,
        total_score=40.0,
        time_used_seconds=120,
        solved_count=2,
        absent=False,
        problem_stats={
            "P1": {
                "raw": "20.0(1:10ms)",
                "solved": True,
                "score": 20.0,
                "attempts": 1,
                "runtime_ms": 10,
                "wrong_before_ac": 0,
            },
            "P2": {
                "raw": "20.0(1:10ms)",
                "solved": True,
                "score": 20.0,
                "attempts": 1,
                "runtime_ms": 10,
                "wrong_before_ac": 0,
            },
        },
        student_id=1,
    )
    second = ParticipantRow(
        identity_source="test",
        external_id="s2",
        display_name="Student 2",
        user_group="G1",
        rank=2,
        total_score=20.0,
        time_used_seconds=120,
        solved_count=1,
        absent=False,
        problem_stats={
            "P1": {
                "raw": "20.0(1:10ms)",
                "solved": True,
                "score": 20.0,
                "attempts": 1,
                "runtime_ms": 10,
                "wrong_before_ac": 0,
            },
        },
        student_id=2,
    )

    results = compute_exam_metrics(
        participants=[first, second],
        total_points=None,
        winsor_low=0.05,
        winsor_high=0.95,
        flexibility_mode="approx",
        timeline_by_student={
            1: [{"submitted_at": datetime(2025, 3, 1, 10, 0), "problem_code": "P1", "verdict": "答案正确"}],
            2: [{"submitted_at": datetime(2025, 3, 1, 10, 0), "problem_code": "P1", "verdict": "答案正确"}],
        },
        included_problem_kinds=["函数题", "编程题"],
        random_exam_mode=True,
        random_exam_slots_by_kind={},
        random_exam_missing_drawn_set_policy="max_passed_fill_unanswered",
        problem_kind_by_code={"P1": "编程题", "P2": "编程题"},
    )

    by_id = {item.student_id: item for item in results}
    assert by_id[1].details["slot_count_by_kind"]["编程题"] == 2
    assert by_id[2].details["slot_count_by_kind"]["编程题"] == 2
    assert by_id[1].details["knowledge_signal"] == 1.0
    assert by_id[2].details["knowledge_signal"] == 0.5
    assert by_id[2].details["filled_unanswered_count_by_kind"]["编程题"] == 1
    assert by_id[2].details["confidence"] == "degraded_missing_drawn_set"
