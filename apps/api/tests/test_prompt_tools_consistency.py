from __future__ import annotations

import asyncio
import json
from datetime import datetime, timezone
from decimal import Decimal

from apps.api.db.repository import (
    DashboardMetricsRow,
    ExamMetricHistoryRow,
    RatingHistoryRow,
)
from apps.api.services.identity import ResolvedIdentity
from apps.api.services.prompt import PromptService
from apps.api.services.tools import ToolExecutor


class FakeMergedHistoryRepo:
    def __init__(self) -> None:
        self.metrics_by_student: dict[int, DashboardMetricsRow | None] = {}
        self.rating_by_student: dict[int, list[RatingHistoryRow]] = {}
        self.exam_metrics_by_student: dict[int, list[ExamMetricHistoryRow]] = {}

    async def fetch_current_metrics(
        self, student_id: int
    ) -> DashboardMetricsRow | None:
        return self.metrics_by_student.get(student_id)

    async def fetch_rating_history(
        self, student_id: int, limit: int = 50
    ) -> list[RatingHistoryRow]:
        return self.rating_by_student.get(student_id, [])[:limit]

    async def fetch_exam_metric_history(
        self, student_id: int, limit: int = 50
    ) -> list[ExamMetricHistoryRow]:
        return self.exam_metrics_by_student.get(student_id, [])[:limit]


def _build_identity() -> ResolvedIdentity:
    return ResolvedIdentity(
        student_entity_id=101,
        student_entity_ids=(101, 202),
        student_id="20231202047",
        pta_nickname="王浩然",
        no_submission_records=False,
        matched_by="student_id",
    )


def _build_repo() -> FakeMergedHistoryRepo:
    repo = FakeMergedHistoryRepo()

    repo.metrics_by_student[101] = DashboardMetricsRow(
        knowledge=Decimal("96"),
        accuracy=Decimal("22"),
        quality=None,
        flexibility=Decimal("28"),
        proficiency=Decimal("28"),
        rating=1097,
        updated_at=datetime(2026, 2, 8, 10, 0, tzinfo=timezone.utc),
    )
    repo.metrics_by_student[202] = DashboardMetricsRow(
        knowledge=Decimal("75"),
        accuracy=Decimal("32"),
        quality=Decimal("64"),
        flexibility=Decimal("40"),
        proficiency=Decimal("53"),
        rating=1097,
        updated_at=datetime(2026, 2, 8, 10, 5, tzinfo=timezone.utc),
    )

    repo.exam_metrics_by_student[101] = [
        ExamMetricHistoryRow(
            exam_id=3,
            exam_name="精选3",
            exam_time=datetime(2026, 2, 8, 9, 0, tzinfo=timezone.utc),
            knowledge=96,
            accuracy=22,
            quality=None,
            flexibility=28,
            proficiency=28,
            computed_at=datetime(2026, 2, 8, 10, 0, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            exam_id=2,
            exam_name="精选2",
            exam_time=datetime(2026, 2, 1, 9, 0, tzinfo=timezone.utc),
            knowledge=48,
            accuracy=40,
            quality=64,
            flexibility=31,
            proficiency=60,
            computed_at=datetime(2026, 2, 1, 10, 0, tzinfo=timezone.utc),
        ),
    ]
    repo.exam_metrics_by_student[202] = [
        ExamMetricHistoryRow(
            exam_id=29,
            exam_name="精进3",
            exam_time=datetime(2026, 2, 9, 9, 0, tzinfo=timezone.utc),
            knowledge=None,
            accuracy=None,
            quality=None,
            flexibility=None,
            proficiency=None,
            computed_at=datetime(2026, 2, 9, 10, 5, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            exam_id=3,
            exam_name="精选3",
            exam_time=datetime(2026, 2, 8, 9, 0, tzinfo=timezone.utc),
            knowledge=75,
            accuracy=None,
            quality=64,
            flexibility=40,
            proficiency=53,
            computed_at=datetime(2026, 2, 8, 10, 5, tzinfo=timezone.utc),
        ),
        ExamMetricHistoryRow(
            exam_id=2,
            exam_name="精选2",
            exam_time=datetime(2026, 2, 1, 9, 0, tzinfo=timezone.utc),
            knowledge=None,
            accuracy=32,
            quality=None,
            flexibility=None,
            proficiency=None,
            computed_at=datetime(2026, 2, 1, 10, 5, tzinfo=timezone.utc),
        ),
    ]
    return repo


def test_prompt_service_uses_merged_exam_metric_rows() -> None:
    repo = _build_repo()
    prompt_service = PromptService(repository=repo)

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))

    assert "知识: 75 / 100" in prompt
    assert "- 知识: 75（+27，上一场: 48）" in prompt
    assert "- 质量: 64（+0，上一场: 64）" in prompt
    assert "精进3" not in prompt


def test_tool_executor_merges_exam_metric_rows() -> None:
    repo = _build_repo()
    executor = ToolExecutor(repository=repo, identity=_build_identity())

    result = asyncio.run(
        executor.execute("get_student_metric_history", {"limit": 2})
    )
    payload = json.loads(result)

    assert payload[0]["exam_id"] == 3
    assert payload[0]["knowledge"] == 75
    assert payload[0]["accuracy"] == 22
    assert payload[0]["quality"] == 64
    assert payload[1]["exam_id"] == 2
    assert payload[1]["accuracy"] == 32
    assert all(item["exam_id"] != 29 for item in payload)
