from __future__ import annotations

import asyncio
from datetime import datetime, timezone
from decimal import Decimal

from apps.api.db.repository import (
    DashboardMetricsRow,
    ExamMetricHistoryRow,
    RatingHistoryRow,
)
from apps.api.services.dashboard import DashboardService
from apps.api.services.identity import ResolvedIdentity


class FakeDashboardRepo:
    def __init__(self) -> None:
        self.metrics: DashboardMetricsRow | None = None
        self.history: list[RatingHistoryRow] = []
        self.exam_metric_history: list[ExamMetricHistoryRow] = []
        self.metrics_by_student: dict[int, DashboardMetricsRow | None] = {}
        self.history_by_student: dict[int, list[RatingHistoryRow]] = {}
        self.exam_metric_history_by_student: dict[int, list[ExamMetricHistoryRow]] = {}

    async def fetch_current_metrics(
        self, student_id: int
    ) -> DashboardMetricsRow | None:
        if student_id in self.metrics_by_student:
            return self.metrics_by_student[student_id]
        return self.metrics

    async def fetch_rating_history(
        self, student_id: int, limit: int = 50
    ) -> list[RatingHistoryRow]:
        if student_id in self.history_by_student:
            return self.history_by_student[student_id][:limit]
        return self.history[:limit]

    async def fetch_exam_metric_history(
        self, student_id: int, limit: int = 50
    ) -> list[ExamMetricHistoryRow]:
        if student_id in self.exam_metric_history_by_student:
            return self.exam_metric_history_by_student[student_id][:limit]
        return self.exam_metric_history[:limit]


def test_dashboard_returns_defaults_for_no_submission_records() -> None:
    repo = FakeDashboardRepo()
    service = DashboardService(
        repository=repo,
        default_rating=800,
        default_metric=0,
        rating_history_limit=50,
    )
    identity = ResolvedIdentity(
        student_entity_id=1,
        student_id="20230001",
        pta_nickname="Alice",
        no_submission_records=True,
        matched_by="student_id",
    )

    result = asyncio.run(service.build(identity))

    assert result.identity.noSubmissionRecords is True
    assert result.rating.current == 800
    assert result.rating.lastDelta is None
    assert result.rating.history == []
    assert result.metrics.knowledge == 0
    assert result.metricMissing.knowledge is True
    assert result.metricDelta.latestExamId is None
    assert result.metricDelta.baseline == "zero"
    assert result.metricDelta.values.knowledge == 0
    assert result.progressExplanation.available is False
    assert result.milestoneStreak.currentPositiveStreak == 0
    assert result.peerComparison.available is False
    assert result.postExamSupport.available is False


def test_dashboard_uses_metrics_and_rating_history() -> None:
    repo = FakeDashboardRepo()
    repo.metrics = DashboardMetricsRow(
        knowledge=Decimal("71.2"),
        accuracy=Decimal("68.5"),
        quality=Decimal("62.0"),
        flexibility=Decimal("55.1"),
        proficiency=Decimal("79.8"),
        rating=888,
    )
    repo.history = [
        RatingHistoryRow(
            exam_id=9,
            exam_name="Contest 9",
            exam_time=datetime(2026, 2, 12, tzinfo=timezone.utc),
            old_rating=880,
            delta=8,
            new_rating=888,
        )
    ]
    repo.exam_metric_history = [
        ExamMetricHistoryRow(
            exam_id=9,
            exam_name="Contest 9",
            exam_time=datetime(2026, 2, 12, tzinfo=timezone.utc),
            knowledge=72,
            accuracy=68,
            quality=62,
            flexibility=55,
            proficiency=80,
            computed_at=datetime(2026, 2, 12, 10, 0, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            exam_id=8,
            exam_name="Contest 8",
            exam_time=datetime(2026, 2, 10, tzinfo=timezone.utc),
            knowledge=69,
            accuracy=70,
            quality=60,
            flexibility=57,
            proficiency=78,
            computed_at=datetime(2026, 2, 10, 10, 0, tzinfo=timezone.utc),
        ),
    ]

    service = DashboardService(
        repository=repo,
        default_rating=800,
        default_metric=0,
        rating_history_limit=50,
    )
    identity = ResolvedIdentity(
        student_entity_id=1,
        student_id="20230001",
        pta_nickname="Alice",
        no_submission_records=False,
        matched_by="student_id",
    )

    result = asyncio.run(service.build(identity))

    assert result.identity.noSubmissionRecords is False
    assert result.rating.current == 888
    assert result.rating.lastDelta == 8
    assert result.rating.history[0].examId == "9"
    assert result.rating.history[0].date == "2026-02-12"
    assert result.metrics.knowledge == 71.2
    assert result.metricDelta.latestExamId == "9"
    assert result.metricDelta.baseline == "previous_exam"
    assert result.metricDelta.values.knowledge == 3
    assert result.metricDelta.values.accuracy == -2
    assert result.metricDelta.values.quality == 2
    assert result.metricDelta.values.flexibility == -2
    assert result.metricDelta.values.proficiency == 2
    assert result.progressExplanation.available is True
    assert result.milestoneStreak.available is True
    assert result.peerComparison.defaultMode == "percentile_band"
    assert result.postExamSupport.mode in {"steady", "reinforce", "recovery"}


def test_dashboard_merges_duplicate_student_entities() -> None:
    repo = FakeDashboardRepo()
    repo.metrics_by_student[101] = DashboardMetricsRow(
        knowledge=Decimal("60.0"),
        accuracy=None,
        quality=None,
        flexibility=Decimal("50.0"),
        proficiency=Decimal("49.0"),
        rating=901,
        updated_at=datetime(2026, 2, 12, 8, 0, tzinfo=timezone.utc),
    )
    repo.metrics_by_student[202] = DashboardMetricsRow(
        knowledge=Decimal("72.0"),
        accuracy=Decimal("31.0"),
        quality=None,
        flexibility=Decimal("29.0"),
        proficiency=Decimal("43.0"),
        rating=912,
        updated_at=datetime(2026, 2, 12, 9, 0, tzinfo=timezone.utc),
    )
    repo.history_by_student[101] = [
        RatingHistoryRow(
            exam_id=1,
            exam_name="A",
            exam_time=datetime(2026, 2, 1, tzinfo=timezone.utc),
            old_rating=900,
            delta=1,
            new_rating=901,
        )
    ]
    repo.history_by_student[202] = [
        RatingHistoryRow(
            exam_id=2,
            exam_name="B",
            exam_time=datetime(2026, 2, 10, tzinfo=timezone.utc),
            old_rating=890,
            delta=22,
            new_rating=912,
        )
    ]
    repo.exam_metric_history_by_student[101] = [
        ExamMetricHistoryRow(
            exam_id=3,
            exam_name="C-old",
            exam_time=datetime(2026, 2, 11, tzinfo=timezone.utc),
            knowledge=64,
            accuracy=20,
            quality=None,
            flexibility=None,
            proficiency=None,
            computed_at=datetime(2026, 2, 11, 9, 0, tzinfo=timezone.utc),
        )
    ]
    repo.exam_metric_history_by_student[202] = [
        ExamMetricHistoryRow(
            exam_id=3,
            exam_name="C-new",
            exam_time=datetime(2026, 2, 11, tzinfo=timezone.utc),
            knowledge=66,
            accuracy=None,
            quality=55,
            flexibility=44,
            proficiency=33,
            computed_at=datetime(2026, 2, 11, 10, 0, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            exam_id=2,
            exam_name="B",
            exam_time=datetime(2026, 2, 10, tzinfo=timezone.utc),
            knowledge=62,
            accuracy=18,
            quality=50,
            flexibility=40,
            proficiency=30,
            computed_at=datetime(2026, 2, 10, 10, 0, tzinfo=timezone.utc),
        ),
    ]

    service = DashboardService(
        repository=repo,
        default_rating=800,
        default_metric=0,
        rating_history_limit=50,
    )
    identity = ResolvedIdentity(
        student_entity_id=101,
        student_id="20231202047",
        pta_nickname="Alice",
        no_submission_records=False,
        matched_by="student_id",
        student_entity_ids=(101, 202),
    )

    result = asyncio.run(service.build(identity))

    assert result.metrics.accuracy == 31.0
    assert result.metrics.quality == 0
    assert result.metricMissing.accuracy is False
    assert result.metricMissing.quality is True
    assert result.rating.current == 912
    assert result.rating.history[0].examId == "2"
    assert result.rating.history[1].examId == "1"
    assert result.metricDelta.latestExamId == "3"
    assert result.metricDelta.latestExamName == "C-new"
    assert result.metricDelta.values.knowledge == 4
    assert result.metricDelta.values.accuracy == 2
    assert result.metricDelta.values.quality == 5
    assert result.metricDelta.values.flexibility == 4
    assert result.metricDelta.values.proficiency == 3
    assert result.progressExplanation.available is True


def test_dashboard_metric_delta_uses_zero_baseline_for_first_exam() -> None:
    repo = FakeDashboardRepo()
    repo.exam_metric_history = [
        ExamMetricHistoryRow(
            exam_id=11,
            exam_name="Contest 11",
            exam_time=datetime(2026, 2, 13, tzinfo=timezone.utc),
            knowledge=55,
            accuracy=48,
            quality=36,
            flexibility=22,
            proficiency=61,
            computed_at=datetime(2026, 2, 13, 10, 0, tzinfo=timezone.utc),
        )
    ]

    service = DashboardService(
        repository=repo,
        default_rating=800,
        default_metric=0,
        rating_history_limit=50,
    )
    identity = ResolvedIdentity(
        student_entity_id=1,
        student_id="20230001",
        pta_nickname="Alice",
        no_submission_records=False,
        matched_by="student_id",
    )

    result = asyncio.run(service.build(identity))

    assert result.metricDelta.latestExamId == "11"
    assert result.metricDelta.baseline == "zero"
    assert result.metricDelta.values.knowledge == 55
    assert result.metricDelta.values.accuracy == 48
    assert result.metricDelta.values.quality == 36
    assert result.metricDelta.values.flexibility == 22
    assert result.metricDelta.values.proficiency == 61
    assert result.progressExplanation.latestExamId == "11"


def test_dashboard_metric_delta_skips_all_none_exam_rows() -> None:
    repo = FakeDashboardRepo()
    repo.exam_metric_history = [
        ExamMetricHistoryRow(
            exam_id=29,
            exam_name="精进3",
            exam_time=datetime(2025, 2, 8, tzinfo=timezone.utc),
            knowledge=None,
            accuracy=None,
            quality=None,
            flexibility=None,
            proficiency=None,
            computed_at=datetime(2025, 2, 8, 10, 0, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            exam_id=32,
            exam_name="精进营第二次竞赛",
            exam_time=datetime(2025, 2, 6, tzinfo=timezone.utc),
            knowledge=6,
            accuracy=31,
            quality=None,
            flexibility=96,
            proficiency=69,
            computed_at=datetime(2025, 2, 6, 10, 0, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            exam_id=34,
            exam_name="精进营开营测试",
            exam_time=datetime(2025, 1, 12, tzinfo=timezone.utc),
            knowledge=40,
            accuracy=28,
            quality=16,
            flexibility=43,
            proficiency=35,
            computed_at=datetime(2025, 1, 12, 10, 0, tzinfo=timezone.utc),
        ),
    ]

    service = DashboardService(
        repository=repo,
        default_rating=800,
        default_metric=0,
        rating_history_limit=50,
    )
    identity = ResolvedIdentity(
        student_entity_id=1,
        student_id="20230001",
        pta_nickname="Alice",
        no_submission_records=False,
        matched_by="student_id",
    )

    result = asyncio.run(service.build(identity))

    assert result.metricDelta.latestExamId == "32"
    assert result.metricDelta.latestExamName == "精进营第二次竞赛"
    assert result.metricDelta.values.knowledge == -34
    assert result.progressExplanation.latestExamId == "32"
    assert result.metricDelta.values.accuracy == 3
    assert result.metricDelta.values.quality == -16
    assert result.metricDelta.values.flexibility == 53
    assert result.metricDelta.values.proficiency == 34
