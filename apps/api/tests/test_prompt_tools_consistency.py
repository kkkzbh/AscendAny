from __future__ import annotations

import asyncio
import json
from datetime import datetime, timezone
from decimal import Decimal

from apps.api.db.repository import (
    DashboardMetricsRow,
    ExamMetricHistoryRow,
    ExamParticipantRow,
    ExamStudentMetricRow,
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
        self.exam_participants_by_exam: dict[int, list[ExamParticipantRow]] = {}
        self.exam_metric_by_exam_student: dict[
            tuple[int, int], ExamStudentMetricRow
        ] = {}

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

    async def fetch_exam_participants_ranked(
        self, exam_id: int, limit: int = 200
    ) -> list[ExamParticipantRow]:
        return self.exam_participants_by_exam.get(exam_id, [])[:limit]

    async def fetch_exam_student_metrics_for_students(
        self, exam_id: int, student_ids: list[int]
    ) -> list[ExamStudentMetricRow]:
        rows: list[ExamStudentMetricRow] = []
        for sid in student_ids:
            row = self.exam_metric_by_exam_student.get((exam_id, sid))
            if row is not None:
                rows.append(row)
        return rows


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


def test_prompt_service_normal_mode_is_minimal_and_contextual() -> None:
    repo = _build_repo()
    prompt_service = PromptService(repository=repo)

    prompt = asyncio.run(prompt_service.build_system_prompt(identity=_build_identity()))

    assert "## 任务目标" in prompt
    assert "## 工具使用规则" in prompt
    assert "## 指标体系说明" not in prompt
    assert "寒暄/闲聊" not in prompt


def test_prompt_service_proactive_mode_contains_analysis_workflow() -> None:
    repo = _build_repo()
    prompt_service = PromptService(repository=repo)

    prompt = asyncio.run(
        prompt_service.build_proactive_analysis_system_prompt(identity=_build_identity())
    )

    assert "## 主动分析模式" in prompt
    assert "## 分析流程（必须执行）" in prompt
    assert "## 指标体系说明" in prompt


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


def test_tool_executor_student_ability_scores_returns_rank_gap() -> None:
    repo = _build_repo()
    repo.exam_participants_by_exam[3] = [
        ExamParticipantRow(
            student_id=303,
            student_name="前一名同学",
            rank=6,
            total_score=Decimal("380"),
            solved_count=5,
        ),
        ExamParticipantRow(
            student_id=101,
            student_name="王浩然",
            rank=7,
            total_score=Decimal("370"),
            solved_count=5,
        ),
    ]
    repo.exam_metric_by_exam_student[(3, 101)] = ExamStudentMetricRow(
        exam_id=3,
        student_id=101,
        knowledge=96,
        accuracy=22,
        quality=64,
        flexibility=28,
        proficiency=28,
    )
    repo.exam_metric_by_exam_student[(3, 303)] = ExamStudentMetricRow(
        exam_id=3,
        student_id=303,
        knowledge=90,
        accuracy=32,
        quality=50,
        flexibility=26,
        proficiency=31,
    )

    executor = ToolExecutor(repository=repo, identity=_build_identity())
    result = asyncio.run(
        executor.execute("get_student_ability_scores", {"exam_id": 3})
    )
    payload = json.loads(result)

    assert payload["me"]["rank"] == 7
    assert payload["previous_ranker"]["rank"] == 6
    assert payload["gap_vs_previous"]["score_gap"] == 10.0
    assert payload["rank_basis"]["source"] == "exam_participants.rank"
    assert payload["metric_diff_vs_previous"]["knowledge"]["delta_vs_previous"] == 6
    assert payload["metric_diff_vs_previous"]["knowledge"]["mine_is_missing"] is False
