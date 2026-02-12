from __future__ import annotations

from preprocess.config import RatingConfig
from preprocess.derive.metrics import compute_exam_metrics
from preprocess.derive.rating import compute_exam_rating
from preprocess.models import ParticipantRow


def _participant(student_id: int, rank: int, score: float, time_used: int, attempts: int) -> ParticipantRow:
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
    results = compute_exam_rating(participants=participants, current_ratings=ratings, cfg=RatingConfig())
    by_id = {item.student_id: item for item in results}
    assert by_id[1].delta >= by_id[2].delta
    assert by_id[2].delta >= by_id[3].delta
