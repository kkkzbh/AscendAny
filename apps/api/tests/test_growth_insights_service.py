from __future__ import annotations

import asyncio
from datetime import datetime, timezone

from apps.api.db.repository import (
    ExamBandMediansRow,
    ExamMetricHistoryRow,
    ExamParticipantContextRow,
    ExamPreviousRankerRow,
    RatingHistoryRow,
)
from apps.api.services.growth_insights import GrowthInsightsService
from apps.api.services.identity import ResolvedIdentity


class FakeGrowthRepo:
    def __init__(self) -> None:
        self.participant_context: ExamParticipantContextRow | None = None
        self.band_medians: ExamBandMediansRow | None = None
        self.previous_ranker: ExamPreviousRankerRow | None = None

    async def fetch_exam_participant_context(
        self,
        exam_id: int,
        student_ids: list[int],
    ) -> ExamParticipantContextRow | None:
        del exam_id, student_ids
        return self.participant_context

    async def fetch_exam_band_medians(
        self,
        exam_id: int,
        pos_start: int,
        pos_end: int,
    ) -> ExamBandMediansRow | None:
        del exam_id, pos_start, pos_end
        return self.band_medians

    async def fetch_exam_previous_ranker(
        self,
        exam_id: int,
        my_pos: int,
    ) -> ExamPreviousRankerRow | None:
        del exam_id, my_pos
        return self.previous_ranker


IDENTITY = ResolvedIdentity(
    student_entity_id=101,
    student_entity_ids=(101,),
    student_id="20231202047",
    pta_nickname="王浩然",
    no_submission_records=False,
    matched_by="student_id",
)


def _rating_row(
    exam_id: int,
    delta: int,
    old_rating: int,
    new_rating: int,
    day: int,
) -> RatingHistoryRow:
    return RatingHistoryRow(
        exam_id=exam_id,
        exam_name=f"Exam {exam_id}",
        exam_time=datetime(2026, 2, day, tzinfo=timezone.utc),
        old_rating=old_rating,
        delta=delta,
        new_rating=new_rating,
    )


def _metric_row(
    exam_id: int,
    day: int,
    knowledge: int | None,
    accuracy: int | None,
    quality: int | None,
    flexibility: int | None,
    proficiency: int | None,
) -> ExamMetricHistoryRow:
    return ExamMetricHistoryRow(
        exam_id=exam_id,
        exam_name=f"Exam {exam_id}",
        exam_time=datetime(2026, 2, day, tzinfo=timezone.utc),
        knowledge=knowledge,
        accuracy=accuracy,
        quality=quality,
        flexibility=flexibility,
        proficiency=proficiency,
        computed_at=datetime(2026, 2, day, 10, 0, tzinfo=timezone.utc),
    )


def test_growth_progress_explanation_has_improvements_and_setbacks() -> None:
    repo = FakeGrowthRepo()
    service = GrowthInsightsService(repository=repo)

    rating_rows = [
        _rating_row(12, +12, 988, 1000, 12),
        _rating_row(11, -5, 993, 988, 9),
    ]
    exam_metric_rows = [
        _metric_row(12, 12, 75, 62, 80, 64, 83),
        _metric_row(11, 9, 70, 66, 76, 60, 80),
    ]

    result = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=rating_rows,
            exam_metric_rows=exam_metric_rows,
        )
    )

    assert result.progress_explanation.available is True
    assert result.progress_explanation.latestExamId == "12"
    assert result.progress_explanation.ratingDelta == 12
    assert any("知识 +5" in item for item in result.progress_explanation.keyImprovements)
    assert any("质量 +4" in item for item in result.progress_explanation.keyImprovements)
    assert any("准确 -4" in item for item in result.progress_explanation.keySetbacks)
    assert "Rating +12" in result.progress_explanation.summary


def test_growth_milestone_and_streak_rules() -> None:
    repo = FakeGrowthRepo()
    service = GrowthInsightsService(repository=repo)

    rating_rows = [
        _rating_row(15, +8, 995, 1003, 15),
        _rating_row(14, +3, 992, 995, 14),
        _rating_row(13, +4, 988, 992, 13),
        _rating_row(12, -5, 993, 988, 12),
        _rating_row(11, +6, 987, 993, 11),
        _rating_row(10, +7, 980, 987, 10),
    ]
    exam_metric_rows = [
        _metric_row(15, 15, 81, 69, 72, 75, 79),
        _metric_row(14, 14, 78, 70, 71, 73, 78),
    ]

    result = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=rating_rows,
            exam_metric_rows=exam_metric_rows,
        )
    )

    assert result.milestone_streak.currentPositiveStreak == 3
    assert result.milestone_streak.bestPositiveStreak == 3
    new_codes = {item.code for item in result.milestone_streak.newMilestones}
    assert "rating_1000" in new_codes
    assert "knowledge_80" in new_codes
    assert "streak_3" in new_codes
    assert len(result.milestone_streak.recentMilestones) <= 5
    assert any("rating" in item for item in result.milestone_streak.nextTargets)


def test_growth_peer_percentile_mode_is_anonymous_and_has_gap() -> None:
    repo = FakeGrowthRepo()
    repo.participant_context = ExamParticipantContextRow(
        student_id=101,
        position=23,
        rank=23,
        total_score=356,
        solved_count=4,
        total_participants=100,
    )
    repo.band_medians = ExamBandMediansRow(
        sample_size=20,
        total_score_median=340,
        solved_count_median=4,
        knowledge_median=70,
        accuracy_median=67,
        quality_median=66,
        flexibility_median=68,
        proficiency_median=75,
    )
    repo.previous_ranker = ExamPreviousRankerRow(
        student_id=88,
        position=22,
        rank=22,
        total_score=360,
        solved_count=4,
        knowledge=76,
        accuracy=70,
        quality=67,
        flexibility=70,
        proficiency=76,
    )

    service = GrowthInsightsService(repository=repo)
    rating_rows = [_rating_row(20, +5, 1040, 1045, 20)]
    exam_metric_rows = [_metric_row(20, 20, 74, 68, 64, 69, 76)]

    result = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=rating_rows,
            exam_metric_rows=exam_metric_rows,
        )
    )

    assert result.peer_comparison.available is True
    assert result.peer_comparison.percentileBand.bandCode == "top_30"
    assert result.peer_comparison.percentileBand.totalParticipants == 100
    assert result.peer_comparison.percentileBand.gapVsBandMedian.score == 16.0
    serialized = result.peer_comparison.model_dump(mode="json")
    serialized_text = str(serialized)
    assert "student_name" not in serialized_text
    assert "姓名" not in serialized_text


def test_growth_previous_ranker_mode_with_and_without_previous() -> None:
    repo = FakeGrowthRepo()
    repo.participant_context = ExamParticipantContextRow(
        student_id=101,
        position=1,
        rank=1,
        total_score=400,
        solved_count=5,
        total_participants=64,
    )
    service = GrowthInsightsService(repository=repo)

    rating_rows = [_rating_row(21, +2, 1098, 1100, 21)]
    exam_metric_rows = [_metric_row(21, 21, 82, 78, 74, 76, 80)]

    no_previous = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=rating_rows,
            exam_metric_rows=exam_metric_rows,
        )
    )
    assert no_previous.peer_comparison.previousRanker.available is False

    repo.participant_context = ExamParticipantContextRow(
        student_id=101,
        position=8,
        rank=8,
        total_score=352,
        solved_count=4,
        total_participants=64,
    )
    repo.previous_ranker = ExamPreviousRankerRow(
        student_id=79,
        position=7,
        rank=7,
        total_score=360,
        solved_count=4,
        knowledge=85,
        accuracy=80,
        quality=76,
        flexibility=77,
        proficiency=83,
    )

    with_previous = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=rating_rows,
            exam_metric_rows=exam_metric_rows,
        )
    )
    assert with_previous.peer_comparison.previousRanker.available is True
    assert with_previous.peer_comparison.previousRanker.rankGap == 1
    assert with_previous.peer_comparison.previousRanker.scoreGap == 8.0
    assert (
        with_previous.peer_comparison.previousRanker.metricGapVsPrevious.knowledge
        == 3
    )


def test_growth_post_exam_support_recovery_mode() -> None:
    repo = FakeGrowthRepo()
    service = GrowthInsightsService(repository=repo)

    result = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=[_rating_row(30, -12, 1020, 1008, 20)],
            exam_metric_rows=[
                _metric_row(30, 20, 63, 58, 54, 51, 52),
                _metric_row(29, 18, 70, 66, 60, 59, 58),
            ],
        )
    )

    assert result.post_exam_support.mode == "recovery"


def test_growth_post_exam_support_reinforce_mode() -> None:
    repo = FakeGrowthRepo()
    service = GrowthInsightsService(repository=repo)

    result = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=[_rating_row(31, +15, 1020, 1035, 21)],
            exam_metric_rows=[
                _metric_row(31, 21, 80, 74, 78, 77, 82),
                _metric_row(30, 19, 70, 68, 70, 70, 74),
            ],
        )
    )

    assert result.post_exam_support.mode == "reinforce"


def test_growth_post_exam_support_steady_mode() -> None:
    repo = FakeGrowthRepo()
    service = GrowthInsightsService(repository=repo)

    result = asyncio.run(
        service.build(
            identity=IDENTITY,
            rating_rows=[_rating_row(32, +3, 1035, 1038, 22)],
            exam_metric_rows=[
                _metric_row(32, 22, 72, 71, 69, 70, 74),
                _metric_row(31, 21, 70, 69, 68, 69, 73),
            ],
        )
    )

    assert result.post_exam_support.mode == "steady"
