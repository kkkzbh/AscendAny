from __future__ import annotations

import logging
from dataclasses import dataclass
from decimal import Decimal
from typing import Any, Callable

from ..core.errors import AppError
from ..db.repository import (
    ApiRepository,
    ExamAnalysisExamRow,
    ExamAnalysisStudentRow,
    ExamAnalysisTargetRow,
    ExamInfoRow,
)
from ..schemas.chat import ChatMessageRequest, ChatReplyRequest
from .identity import ResolvedIdentity, StudentIdentityService
from .prompt import PromptService
from .tools import ToolExecutor

logger = logging.getLogger(__name__)

_DEFAULT_ROLE_ID = "xiaoD"
_ROLE_NAMES: dict[str, str] = {
    "xiaoD": "小D",
    "sakiko": "丰川祥子（Sakiko）",
}


def _safe_float(value: Decimal | float | int | None) -> float | None:
    if value is None:
        return None
    return float(value)


@dataclass(slots=True)
class ExamAnalysisDetailPayload:
    exam: ExamInfoRow
    generated_count: int
    failed_count: int
    missing_count: int
    items: list[ExamAnalysisStudentRow]


@dataclass(slots=True)
class ExamAnalysisRunSummary:
    exam: ExamInfoRow
    participants: int
    generated: int
    skipped: int
    failed: int


ProgressEmitter = Callable[[str, dict[str, Any]], None]


class ExamAnalysisService:
    def __init__(self, repository: ApiRepository, llm_service: Any) -> None:
        self._repository = repository
        self._llm_service = llm_service
        self._prompt_service = PromptService(repository)
        self._identity_service = StudentIdentityService(repository)

    async def list_exams(
        self,
        role_id: str = _DEFAULT_ROLE_ID,
    ) -> list[ExamAnalysisExamRow]:
        return await self._repository.list_exam_analysis_exams(role_id=role_id)

    async def build_exam_detail(
        self,
        exam_id: int,
        role_id: str = _DEFAULT_ROLE_ID,
    ) -> ExamAnalysisDetailPayload:
        exam = await self._require_exam(exam_id)
        items = await self._repository.fetch_exam_analysis_rows(
            exam_id=exam_id,
            role_id=role_id,
        )
        generated_count = sum(1 for item in items if item.analysis_status == "success")
        failed_count = sum(1 for item in items if item.analysis_status == "failed")
        missing_count = sum(1 for item in items if item.analysis_status == "missing")
        return ExamAnalysisDetailPayload(
            exam=exam,
            generated_count=generated_count,
            failed_count=failed_count,
            missing_count=missing_count,
            items=items,
        )

    async def generate_exam_analysis(
        self,
        exam_id: int,
        role_id: str = _DEFAULT_ROLE_ID,
        force: bool = False,
        on_progress: ProgressEmitter | None = None,
    ) -> ExamAnalysisRunSummary:
        exam = await self._require_exam(exam_id)
        targets = await self._repository.fetch_exam_analysis_targets(
            exam_id=exam_id,
            role_id=role_id,
        )
        total = len(targets)
        generated = 0
        skipped = 0
        failed = 0

        self._emit_log(
            on_progress,
            "info",
            f"开始为考试《{self._exam_name(exam)}》生成 AI 分析，共 {total} 名学生。",
        )

        for idx, target in enumerate(targets, start=1):
            display_name = target.student_name or target.student_no or str(
                target.student_entity_id
            )
            if not force and target.analysis_status == "success":
                skipped += 1
                self._emit_progress(on_progress, idx, total, phase="exam_analysis")
                continue

            self._emit_log(
                on_progress,
                "info",
                f"[{idx}/{total}] 生成 {display_name} 的考试分析 ...",
            )
            self._emit_progress(on_progress, idx, total, phase="exam_analysis")

            try:
                identity = await self._resolve_identity(target)
                system_prompt = (
                    await self._prompt_service.build_exam_analysis_system_prompt(
                        identity=identity,
                        target_exam_id=exam_id,
                        role_id=role_id,
                        role_name=_ROLE_NAMES.get(role_id, _ROLE_NAMES[_DEFAULT_ROLE_ID]),
                    )
                )
                synthetic_request = ChatReplyRequest(
                    studentId=target.student_no,
                    ptaNickname=target.pta_nickname,
                    messages=[
                        ChatMessageRequest(
                            role="user",
                            content=self._prompt_service.build_auto_analysis_user_message(),
                        )
                    ],
                    summary="",
                    providerType="server_default",
                    providerConfig=None,
                    roleId=role_id,
                    roleName=_ROLE_NAMES.get(role_id, _ROLE_NAMES[_DEFAULT_ROLE_ID]),
                    roleSystemPrompt=None,
                )
                tool_executor = ToolExecutor(
                    repository=self._repository,
                    identity=identity,
                )
                result = await self._llm_service.generate_reply(
                    synthetic_request,
                    system_prompt=system_prompt,
                    tool_executor=tool_executor,
                )
                reply = result.reply.strip()
                if not reply:
                    raise RuntimeError("LLM returned empty exam analysis.")

                await self._repository.upsert_exam_auto_analysis_cache(
                    exam_id=exam_id,
                    student_id=target.student_entity_id,
                    role_id=role_id,
                    status="success",
                    provider_type=result.provider,
                    reply=reply,
                    source="teacher_exam",
                    error_message=None,
                )
                generated += 1
            except Exception as exc:
                failed += 1
                error_message = str(exc).strip() or exc.__class__.__name__
                logger.warning(
                    "Exam analysis generation failed: exam_id=%s student_id=%s",
                    exam_id,
                    target.student_entity_id,
                    exc_info=True,
                )
                await self._repository.upsert_exam_auto_analysis_cache(
                    exam_id=exam_id,
                    student_id=target.student_entity_id,
                    role_id=role_id,
                    status="failed",
                    provider_type=None,
                    reply="",
                    source="teacher_exam",
                    error_message=error_message[:500],
                )
                self._emit_log(
                    on_progress,
                    "warning",
                    f"{display_name} 生成失败：{error_message}",
                )

        self._emit_log(
            on_progress,
            "success",
            f"考试《{self._exam_name(exam)}》分析生成完成：成功 {generated}，跳过 {skipped}，失败 {failed}。",
        )
        return ExamAnalysisRunSummary(
            exam=exam,
            participants=total,
            generated=generated,
            skipped=skipped,
            failed=failed,
        )

    async def _require_exam(self, exam_id: int) -> ExamInfoRow:
        exam = await self._repository.fetch_exam_info(exam_id)
        if exam is None:
            raise AppError(
                status_code=404,
                code="EXAM_NOT_FOUND",
                message="Exam was not found.",
            )
        return exam

    async def _resolve_identity(
        self,
        target: ExamAnalysisTargetRow,
    ) -> ResolvedIdentity:
        if target.student_no or target.pta_nickname:
            try:
                return await self._identity_service.resolve(
                    student_id=target.student_no,
                    pta_nickname=target.pta_nickname,
                )
            except AppError:
                logger.debug(
                    "Falling back to direct exam-participant identity: student=%s",
                    target.student_entity_id,
                    exc_info=True,
                )

        return ResolvedIdentity(
            student_entity_id=target.student_entity_id,
            student_entity_ids=(target.student_entity_id,),
            student_id=target.student_no or str(target.student_entity_id),
            pta_nickname=target.pta_nickname or target.student_name,
            no_submission_records=False,
            matched_by="exam_participant",
        )

    def _emit_log(
        self,
        on_progress: ProgressEmitter | None,
        level: str,
        message: str,
    ) -> None:
        if on_progress is None:
            return
        on_progress("log", {"level": level, "message": message})

    def _emit_progress(
        self,
        on_progress: ProgressEmitter | None,
        current: int,
        total: int,
        phase: str,
    ) -> None:
        if on_progress is None:
            return
        on_progress(
            "progress",
            {
                "current": current,
                "total": total,
                "phase": phase,
            },
        )

    def _exam_name(self, exam: ExamInfoRow) -> str:
        return exam.title or exam.source_path
