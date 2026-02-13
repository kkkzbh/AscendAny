from __future__ import annotations

import asyncio
from datetime import datetime, timezone
from decimal import Decimal

from apps.api.db.repository import DashboardMetricsRow, RatingHistoryRow
from apps.api.services.dashboard import DashboardService
from apps.api.services.identity import ResolvedIdentity


class FakeDashboardRepo:
    def __init__(self) -> None:
        self.metrics: DashboardMetricsRow | None = None
        self.history: list[RatingHistoryRow] = []
        self.metrics_by_student: dict[int, DashboardMetricsRow | None] = {}
        self.history_by_student: dict[int, list[RatingHistoryRow]] = {}

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
    assert result.rating.current == 912
    assert result.rating.history[0].examId == "2"
    assert result.rating.history[1].examId == "1"
