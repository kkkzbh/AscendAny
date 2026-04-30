from __future__ import annotations

import asyncio
from datetime import datetime, timezone
from decimal import Decimal

from fastapi.testclient import TestClient

from apps.api.api.deps import get_admin_account
from apps.api.db.repository import (
    DashboardMetricsRow,
    ExamAnalysisExamRow,
    ExamAnalysisStudentRow,
    ExamAnalysisTargetRow,
    ExamAutoAnalysisCacheRow,
    ExamInfoRow,
    ExamMetricHistoryRow,
    RatingHistoryRow,
    StudentIdentityMatch,
)
from apps.api.main import create_app
from apps.api.services.auth import AuthenticatedAccount
from apps.api.services.exam_analysis import ExamAnalysisService
from apps.api.schemas.chat import ChatReplyResponse


def _dt(day: int) -> datetime:
    return datetime(2026, 2, day, tzinfo=timezone.utc)


class FakeRepo:
    def __init__(self) -> None:
        self.cache: dict[tuple[int, int, str], ExamAutoAnalysisCacheRow] = {
            (11, 101, "xiaoD"): ExamAutoAnalysisCacheRow(
                exam_id=11,
                student_id=101,
                role_id="xiaoD",
                status="success",
                provider_type="server_default",
                reply="Alice exam 11",
                source="teacher_exam",
                error_message=None,
                generated_at=_dt(11),
                updated_at=_dt(11),
            )
        }

    async def list_exam_analysis_exams(self, role_id: str = "xiaoD"):
        _ = role_id
        return [
            ExamAnalysisExamRow(
                exam_id=11,
                exam_name="Contest 11",
                exam_type="datastructure",
                exam_date=_dt(11),
                participant_count=2,
                generated_count=1,
                failed_count=0,
                missing_count=1,
            )
        ]

    async def fetch_exam_info(self, exam_id: int):
        if exam_id != 11:
            return None
        return ExamInfoRow(
            exam_id=11,
            exam_type="datastructure",
            source_path="datastructure/contest-11",
            title="Contest 11",
            starts_at=_dt(11),
            ends_at=None,
            duration_seconds=7200,
            problem_count=4,
            participant_count=2,
        )

    async def fetch_exam_analysis_rows(self, exam_id: int, role_id: str = "xiaoD"):
        assert exam_id == 11
        _ = role_id
        second = self.cache.get((11, 202, "xiaoD"))
        return [
            ExamAnalysisStudentRow(
                student_entity_id=101,
                student_no="20230001",
                student_name="Alice",
                rank=1,
                total_score=Decimal("100"),
                solved_count=4,
                rating_delta=22,
                knowledge=90,
                accuracy=88,
                quality=80,
                flexibility=76,
                proficiency=79,
                analysis_status="success",
                analysis_reply="Alice exam 11",
                generated_at=_dt(11),
                error_message=None,
            ),
            ExamAnalysisStudentRow(
                student_entity_id=202,
                student_no=None,
                student_name="Bob",
                rank=2,
                total_score=Decimal("75"),
                solved_count=3,
                rating_delta=-5,
                knowledge=70,
                accuracy=62,
                quality=65,
                flexibility=60,
                proficiency=58,
                analysis_status=second.status if second is not None else "missing",
                analysis_reply=second.reply if second is not None else "",
                generated_at=second.generated_at if second is not None else None,
                error_message=second.error_message if second is not None else None,
            ),
        ]

    async def fetch_exam_analysis_targets(self, exam_id: int, role_id: str = "xiaoD"):
        assert exam_id == 11
        return [
            ExamAnalysisTargetRow(
                student_entity_id=101,
                student_no="20230001",
                student_name="Alice",
                pta_nickname="Alice",
                analysis_status=self.cache.get((11, 101, role_id)).status
                if self.cache.get((11, 101, role_id))
                else None,
            ),
            ExamAnalysisTargetRow(
                student_entity_id=202,
                student_no=None,
                student_name="Bob",
                pta_nickname="Bob",
                analysis_status=self.cache.get((11, 202, role_id)).status
                if self.cache.get((11, 202, role_id))
                else None,
            ),
        ]

    async def upsert_exam_auto_analysis_cache(
        self,
        exam_id: int,
        student_id: int,
        role_id: str,
        status: str,
        provider_type: str | None,
        reply: str,
        source: str,
        error_message: str | None,
    ):
        row = ExamAutoAnalysisCacheRow(
            exam_id=exam_id,
            student_id=student_id,
            role_id=role_id,
            status=status,
            provider_type=provider_type,
            reply=reply,
            source=source,
            error_message=error_message,
            generated_at=_dt(16),
            updated_at=_dt(16),
        )
        self.cache[(exam_id, student_id, role_id)] = row
        return row

    async def find_students_by_student_no(self, student_no: str):
        if student_no != "20230001":
            return []
        return [
            StudentIdentityMatch(
                student_id=101,
                student_no="20230001",
                student_name="Alice",
            )
        ]

    async def find_student_nos_by_name(self, student_name: str):
        _ = student_name
        return []

    async def exists_learning_records_for_student_ids(self, student_ids: list[int]) -> bool:
        return bool(student_ids)

    async def fetch_current_metrics(self, student_id: int):
        ratings = {
            101: 1002,
            202: 880,
        }
        return DashboardMetricsRow(
            knowledge=72,
            accuracy=68,
            quality=66,
            flexibility=61,
            proficiency=70,
            rating=ratings.get(student_id, 800),
            updated_at=_dt(16),
        )

    async def fetch_rating_history(self, student_id: int, limit: int = 50):
        rows = {
            101: [
                RatingHistoryRow(12, "Contest 12", _dt(12), 1002, -10, 992),
                RatingHistoryRow(11, "Contest 11", _dt(11), 980, 22, 1002),
                RatingHistoryRow(10, "Contest 10", _dt(10), 970, 10, 980),
            ],
            202: [
                RatingHistoryRow(11, "Contest 11", _dt(11), 885, -5, 880),
                RatingHistoryRow(10, "Contest 10", _dt(10), 890, -5, 885),
            ],
        }
        return rows.get(student_id, [])[:limit]

    async def fetch_exam_metric_history(self, student_id: int, limit: int = 50):
        rows = {
            101: [
                ExamMetricHistoryRow(12, "Contest 12", _dt(12), 85, 82, 78, 75, 80, _dt(12)),
                ExamMetricHistoryRow(11, "Contest 11", _dt(11), 90, 88, 80, 76, 79, _dt(11)),
                ExamMetricHistoryRow(10, "Contest 10", _dt(10), 84, 81, 77, 72, 75, _dt(10)),
            ],
            202: [
                ExamMetricHistoryRow(11, "Contest 11", _dt(11), 70, 62, 65, 60, 58, _dt(11)),
                ExamMetricHistoryRow(10, "Contest 10", _dt(10), 73, 66, 67, 63, 61, _dt(10)),
            ],
        }
        return rows.get(student_id, [])[:limit]


class FakeLLM:
    def __init__(self) -> None:
        self.calls: list[tuple[str | None, str]] = []

    async def generate_reply(self, payload, system_prompt=None, tool_executor=None):
        _ = tool_executor
        self.calls.append((payload.ptaNickname, system_prompt or ""))
        if payload.ptaNickname == "Bob":
            raise RuntimeError("boom")
        return ChatReplyResponse(
            reply=f"{payload.ptaNickname or payload.studentId} analysis",
            summary=payload.summary,
            provider="server_default",
        )


def _build_client(repo: FakeRepo, llm: FakeLLM, *, admin: bool) -> TestClient:
    app = create_app(repository=repo, llm_service=llm)
    if admin:
        app.dependency_overrides[get_admin_account] = lambda: AuthenticatedAccount(
            account_id=1,
            username="admin",
            is_admin=True,
        )
    return TestClient(app)


def test_exam_analysis_routes_require_admin() -> None:
    with _build_client(FakeRepo(), FakeLLM(), admin=False) as client:
        response = client.get("/api/v1/exam-analysis/exams")
    assert response.status_code == 401


def test_exam_analysis_routes_return_exam_list_and_detail() -> None:
    with _build_client(FakeRepo(), FakeLLM(), admin=True) as client:
        exams = client.get("/api/v1/exam-analysis/exams")
        detail = client.get("/api/v1/exam-analysis/exams/11")

    assert exams.status_code == 200
    exams_payload = exams.json()
    assert exams_payload["items"][0]["examId"] == "11"
    assert exams_payload["items"][0]["participantCount"] == 2
    assert exams_payload["items"][0]["missingCount"] == 1

    assert detail.status_code == 200
    detail_payload = detail.json()
    assert detail_payload["examId"] == "11"
    assert len(detail_payload["items"]) == 2
    assert detail_payload["items"][1]["studentName"] == "Bob"
    assert detail_payload["items"][1]["analysisStatus"] == "missing"


def test_exam_analysis_service_targets_selected_exam_and_includes_students_without_accounts() -> None:
    repo = FakeRepo()
    repo.cache.clear()
    llm = FakeLLM()
    service = ExamAnalysisService(repository=repo, llm_service=llm)

    summary = asyncio.run(service.generate_exam_analysis(exam_id=11))

    assert summary.participants == 2
    assert summary.generated == 1
    assert summary.failed == 1

    success_row = repo.cache[(11, 101, "xiaoD")]
    failed_row = repo.cache[(11, 202, "xiaoD")]
    assert success_row.status == "success"
    assert success_row.reply == "Alice analysis"
    assert failed_row.status == "failed"
    assert failed_row.error_message == "boom"

    alice_prompt = llm.calls[0][1]
    assert "目标考试 ID 为 `11`" in alice_prompt
    assert "目标考试 ID: 11" in alice_prompt
    assert "Contest 11" not in alice_prompt
    assert "系统主动分析最新考试表现" not in alice_prompt


def test_exam_analysis_service_force_recomputes_success_rows() -> None:
    repo = FakeRepo()
    llm = FakeLLM()
    service = ExamAnalysisService(repository=repo, llm_service=llm)

    first = asyncio.run(service.generate_exam_analysis(exam_id=11, force=False))
    calls_after_first = len(llm.calls)
    second = asyncio.run(service.generate_exam_analysis(exam_id=11, force=False))
    calls_after_second = len(llm.calls)
    third = asyncio.run(service.generate_exam_analysis(exam_id=11, force=True))

    assert first.skipped == 1
    assert calls_after_first == 1
    assert second.skipped == 1
    assert calls_after_second == 2
    assert third.generated == 1
    assert len(llm.calls) == 4
