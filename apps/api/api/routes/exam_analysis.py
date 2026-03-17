from __future__ import annotations

import asyncio
import logging
from decimal import Decimal

from fastapi import APIRouter, Depends
from fastapi.responses import StreamingResponse

from ..deps import get_admin_account, get_llm_service, get_repository
from ...schemas.exam_analysis import (
    ExamAnalysisExamDetailResponse,
    ExamAnalysisExamListItemResponse,
    ExamAnalysisExamListResponse,
    ExamAnalysisGenerateRequest,
    ExamAnalysisGenerateResponse,
    ExamAnalysisStudentRowResponse,
)
from ...services.auth import AuthenticatedAccount
from ...services.exam_analysis import ExamAnalysisService
from ...services.import_task import TaskEvent, get_task_manager

logger = logging.getLogger(__name__)

router = APIRouter(tags=["exam-analysis"], prefix="/exam-analysis")


def _safe_float(value: Decimal | float | int | None) -> float | None:
    if value is None:
        return None
    return float(value)


@router.get("/exams", response_model=ExamAnalysisExamListResponse)
async def list_exam_analysis_exams(
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    llm_service=Depends(get_llm_service),
) -> ExamAnalysisExamListResponse:
    service = ExamAnalysisService(repository=repository, llm_service=llm_service)
    rows = await service.list_exams()
    return ExamAnalysisExamListResponse(
        items=[
            ExamAnalysisExamListItemResponse(
                examId=str(row.exam_id),
                examName=row.exam_name,
                examType=row.exam_type,
                examDate=row.exam_date,
                participantCount=row.participant_count,
                generatedCount=row.generated_count,
                failedCount=row.failed_count,
                missingCount=row.missing_count,
            )
            for row in rows
        ]
    )


@router.get("/exams/{exam_id}", response_model=ExamAnalysisExamDetailResponse)
async def get_exam_analysis_detail(
    exam_id: int,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    llm_service=Depends(get_llm_service),
) -> ExamAnalysisExamDetailResponse:
    service = ExamAnalysisService(repository=repository, llm_service=llm_service)
    payload = await service.build_exam_detail(exam_id=exam_id)
    return ExamAnalysisExamDetailResponse(
        examId=str(payload.exam.exam_id),
        examName=payload.exam.title or payload.exam.source_path,
        examType=payload.exam.exam_type,
        examDate=payload.exam.starts_at,
        participantCount=payload.exam.participant_count,
        generatedCount=payload.generated_count,
        failedCount=payload.failed_count,
        missingCount=payload.missing_count,
        items=[
            ExamAnalysisStudentRowResponse(
                studentEntityId=str(row.student_entity_id),
                studentId=row.student_no,
                studentName=row.student_name,
                rank=row.rank,
                totalScore=_safe_float(row.total_score),
                solvedCount=row.solved_count,
                ratingDelta=row.rating_delta,
                knowledge=_safe_float(row.knowledge),
                accuracy=_safe_float(row.accuracy),
                quality=_safe_float(row.quality),
                flexibility=_safe_float(row.flexibility),
                proficiency=_safe_float(row.proficiency),
                analysisStatus=row.analysis_status,
                analysisReply=row.analysis_reply,
                generatedAt=row.generated_at,
                errorMessage=row.error_message,
            )
            for row in payload.items
        ],
    )


@router.post("/exams/{exam_id}/generate", response_model=ExamAnalysisGenerateResponse)
async def generate_exam_analysis(
    exam_id: int,
    body: ExamAnalysisGenerateRequest,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    llm_service=Depends(get_llm_service),
) -> ExamAnalysisGenerateResponse:
    tm = get_task_manager()
    run_id = tm.create_task("exam_analysis")
    service = ExamAnalysisService(repository=repository, llm_service=llm_service)

    def _on_progress(event_type: str, data: dict[str, object]) -> None:
        if event_type == "log":
            tm.emit_log(
                run_id,
                str(data.get("level", "info")),
                str(data.get("message", "")),
            )
            return
        if event_type == "progress":
            tm.emit_progress(
                run_id,
                int(data.get("current", 0)),
                int(data.get("total", 0)),
                phase=str(data.get("phase", "exam_analysis")),
            )
            return
        tm.emit(run_id, TaskEvent(event_type=event_type, data=dict(data)))

    async def _worker() -> None:
        try:
            tm.mark_running(run_id)
            summary = await service.generate_exam_analysis(
                exam_id=exam_id,
                force=body.force,
                on_progress=_on_progress,
            )
            tm.emit_done(
                run_id,
                {
                    "examId": summary.exam.exam_id,
                    "examName": summary.exam.title or summary.exam.source_path,
                    "participants": summary.participants,
                    "generated": summary.generated,
                    "skipped": summary.skipped,
                    "failed": summary.failed,
                },
            )
        except Exception as exc:
            logger.exception("Exam analysis task %s failed", run_id)
            tm.emit_error(run_id, str(exc))

    asyncio.create_task(_worker())

    return ExamAnalysisGenerateResponse(
        runId=run_id,
        message="考试分析任务已启动",
    )


@router.get("/runs/{run_id}/stream")
async def stream_exam_analysis_run(
    run_id: str,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> StreamingResponse:
    tm = get_task_manager()
    return StreamingResponse(
        tm.event_stream(run_id),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )
