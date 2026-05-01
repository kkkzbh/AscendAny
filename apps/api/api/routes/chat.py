from __future__ import annotations

import logging
import os
import json
from typing import Any

from fastapi import APIRouter, Depends, Header
from fastapi.responses import StreamingResponse

from ..deps import (
    get_current_account_optional,
    get_current_account,
    get_repository,
    get_llm_service,
)
from ...core.errors import AppError
from ...schemas.chat import (
    AutoAnalysisRequest,
    AutoAnalysisPrecomputeRequest,
    AutoAnalysisPrecomputeResponse,
    AutoAnalysisResponse,
    ChatMessageRequest,
    ChatReplyRequest,
    ChatReplyResponse,
)
from ...services.history_merge import merge_rating_history_rows
from ...services.identity import StudentIdentityService
from ...services.prompt import PromptService
from ...services.tools import ToolExecutor

logger = logging.getLogger(__name__)

router = APIRouter(tags=["chat"])

# Server-side role name mapping (mirrors frontend BUILT_IN_ROLES)
_ROLE_NAMES: dict[str, str] = {
    "xiaoD": "小D",
    "sakiko": "丰川祥子（Sakiko）",
}

_DEFAULT_ROLE_ID = "xiaoD"


def _sse(event_type: str, data: dict[str, Any]) -> str:
    payload = json.dumps(data, ensure_ascii=False, separators=(",", ":"))
    return f"event: {event_type}\ndata: {payload}\n\n"


def _resolve_role_name(role_id: str, role_name: str | None) -> str:
    normalized = (role_name or "").strip()
    if normalized:
        return normalized
    return _ROLE_NAMES.get(role_id, _ROLE_NAMES[_DEFAULT_ROLE_ID])


def _resolve_role_system_prompt(role_system_prompt: str | None) -> str:
    return (role_system_prompt or "").strip()


async def _resolve_latest_exam_id(repository: Any, identity: Any) -> int | None:
    student_entity_ids = identity.student_entity_ids or (identity.student_entity_id,)
    per_student_limit = max(20, 20 * len(student_entity_ids))
    rows = []
    for sid in student_entity_ids:
        rows.extend(
            await repository.fetch_rating_history(
                student_id=sid,
                limit=per_student_limit,
            )
        )
    latest = merge_rating_history_rows(rows, limit=1)
    if not latest:
        return None
    return int(latest[0].exam_id)


async def _fetch_auto_analysis_cache(
    repository: Any,
    account_id: int,
    exam_id: int,
    role_id: str,
) -> Any | None:
    fetcher = getattr(repository, "fetch_auto_analysis_cache", None)
    if not callable(fetcher):
        return None
    return await fetcher(account_id=account_id, exam_id=exam_id, role_id=role_id)


async def _upsert_auto_analysis_cache(
    repository: Any,
    account_id: int,
    exam_id: int,
    role_id: str,
    provider_type: str | None,
    reply: str,
    source: str,
) -> None:
    upserter = getattr(repository, "upsert_auto_analysis_cache", None)
    if not callable(upserter):
        return
    await upserter(
        account_id=account_id,
        exam_id=exam_id,
        role_id=role_id,
        provider_type=provider_type,
        reply=reply,
        source=source,
    )


async def _mark_auto_analysis_delivered(
    repository: Any,
    account_id: int,
    exam_id: int,
    role_id: str,
) -> None:
    marker = getattr(repository, "mark_auto_analysis_delivered", None)
    if not callable(marker):
        return
    await marker(account_id=account_id, exam_id=exam_id, role_id=role_id)


async def _increment_ai_dialogue_counter(repository: Any, identity: Any) -> None:
    if identity is None:
        return
    incrementer = getattr(repository, "increment_ai_dialogue_count", None)
    if not callable(incrementer):
        return
    student_entity_ids = getattr(identity, "student_entity_ids", None)
    student_entity_id = getattr(identity, "student_entity_id", None)
    raw_ids = student_entity_ids or (student_entity_id,)
    student_ids = sorted(
        {
            int(student_id)
            for student_id in raw_ids
            if student_id is not None and int(student_id) > 0
        }
    )
    if not student_ids:
        return
    await incrementer(student_ids, delta=1)


async def _prepare_chat_runtime(
    payload: ChatReplyRequest,
    repository: Any,
    current_account: Any,
) -> tuple[ChatReplyRequest, Any, str, ToolExecutor]:
    effective_payload = payload
    student_id = payload.studentId
    pta_nickname = payload.ptaNickname

    if (
        (student_id is None or not student_id.strip())
        and (pta_nickname is None or not pta_nickname.strip())
        and current_account is not None
    ):
        profile = await repository.fetch_account_profile(current_account.account_id)
        if profile is not None:
            student_id = profile.student_id
            pta_nickname = profile.pta_nickname
            effective_payload = payload.model_copy(
                update={
                    "studentId": student_id,
                    "ptaNickname": pta_nickname,
                }
            )

    role_id = payload.roleId or _DEFAULT_ROLE_ID
    role_name = _resolve_role_name(role_id, payload.roleName)
    role_system_prompt = _resolve_role_system_prompt(payload.roleSystemPrompt)

    identity = None
    if student_id or pta_nickname:
        try:
            identity_service = StudentIdentityService(repository)
            identity = await identity_service.resolve(student_id, pta_nickname)
        except Exception:
            logger.debug(
                "Student identity resolution failed for chat prompt", exc_info=True
            )

    prompt_service = PromptService(repository)
    system_prompt = await prompt_service.build_system_prompt(
        identity=identity,
        role_id=role_id,
        role_name=role_name,
        custom_role_style_prompt=role_system_prompt,
        notes=payload.notes,
        notes_title=payload.notesTitle,
    )
    tool_executor = ToolExecutor(
        repository=repository,
        identity=identity,
        notes_content=payload.notes,
        notes_title=payload.notesTitle,
    )
    return effective_payload, identity, system_prompt, tool_executor


@router.post("/chat/reply", response_model=ChatReplyResponse)
async def chat_reply(
    payload: ChatReplyRequest,
    repository=Depends(get_repository),
    current_account=Depends(get_current_account_optional),
    llm_service=Depends(get_llm_service),
) -> ChatReplyResponse:
    # ── 1. Resolve effective student identity ──
    effective_payload = payload
    student_id = payload.studentId
    pta_nickname = payload.ptaNickname

    if (
        (student_id is None or not student_id.strip())
        and (pta_nickname is None or not pta_nickname.strip())
        and current_account is not None
    ):
        profile = await repository.fetch_account_profile(current_account.account_id)
        if profile is not None:
            student_id = profile.student_id
            pta_nickname = profile.pta_nickname
            effective_payload = payload.model_copy(
                update={
                    "studentId": student_id,
                    "ptaNickname": pta_nickname,
                }
            )

    role_id = payload.roleId or _DEFAULT_ROLE_ID
    role_name = _resolve_role_name(role_id, payload.roleName)
    role_system_prompt = _resolve_role_system_prompt(payload.roleSystemPrompt)

    # ── 2. Resolve student identity for prompt context ──
    identity = None
    if student_id or pta_nickname:
        try:
            identity_service = StudentIdentityService(repository)
            identity = await identity_service.resolve(student_id, pta_nickname)
        except Exception:
            # Identity resolution failure is non-fatal for chat;
            # the prompt will fall back to "no student context".
            logger.debug(
                "Student identity resolution failed for chat prompt", exc_info=True
            )

    # ── 3. Build system prompt ──
    prompt_service = PromptService(repository)
    system_prompt = await prompt_service.build_system_prompt(
        identity=identity,
        role_id=role_id,
        role_name=role_name,
        custom_role_style_prompt=role_system_prompt,
        notes=payload.notes,
        notes_title=payload.notesTitle,
    )

    # ── 4. Create tool executor ──
    tool_executor = ToolExecutor(
        repository=repository,
        identity=identity,
        notes_content=payload.notes,
        notes_title=payload.notesTitle,
    )

    # ── 5. Generate LLM reply ──
    result = await llm_service.generate_reply(
        effective_payload,
        system_prompt=system_prompt,
        tool_executor=tool_executor,
    )
    await _increment_ai_dialogue_counter(repository, identity)
    return result


@router.post("/chat/reply/stream")
async def chat_reply_stream(
    payload: ChatReplyRequest,
    repository=Depends(get_repository),
    current_account=Depends(get_current_account_optional),
    llm_service=Depends(get_llm_service),
) -> StreamingResponse:
    async def generate():
        identity = None
        try:
            effective_payload, identity, system_prompt, tool_executor = (
                await _prepare_chat_runtime(payload, repository, current_account)
            )
            async for event in llm_service.stream_reply(
                effective_payload,
                system_prompt=system_prompt,
                tool_executor=tool_executor,
            ):
                event_type = str(event.get("type", "delta"))
                yield _sse(event_type, event)
            await _increment_ai_dialogue_counter(repository, identity)
        except AppError as exc:
            yield _sse("error", {"type": "error", "code": exc.code, "message": exc.message})
        except Exception as exc:
            logger.warning("Chat stream failed", exc_info=True)
            yield _sse(
                "error",
                {
                    "type": "error",
                    "code": "CHAT_STREAM_FAILED",
                    "message": str(exc) or "Chat stream failed.",
                },
            )

    return StreamingResponse(generate(), media_type="text/event-stream")


@router.post("/chat/auto-analysis", response_model=AutoAnalysisResponse)
async def chat_auto_analysis(
    payload: AutoAnalysisRequest,
    repository=Depends(get_repository),
    current_account=Depends(get_current_account),
    llm_service=Depends(get_llm_service),
) -> AutoAnalysisResponse:
    """Trigger an automatic analysis of the student's latest exam.

    The frontend hides the trigger message and only displays the assistant reply,
    making it appear as if the assistant initiated the conversation.
    """
    # ── 1. Resolve student identity ──
    student_id = payload.studentId
    pta_nickname = payload.ptaNickname

    if (student_id is None or not student_id.strip()) and (
        pta_nickname is None or not pta_nickname.strip()
    ):
        profile = await repository.fetch_account_profile(current_account.account_id)
        if profile is not None:
            student_id = profile.student_id
            pta_nickname = profile.pta_nickname

    identity = None
    if student_id or pta_nickname:
        try:
            identity_service = StudentIdentityService(repository)
            identity = await identity_service.resolve(student_id, pta_nickname)
        except Exception:
            logger.debug(
                "Student identity resolution failed for auto-analysis", exc_info=True
            )

    if identity is None or identity.no_submission_records:
        return AutoAnalysisResponse(
            reply="",
            provider="server_default",
        )

    latest_exam_id = await _resolve_latest_exam_id(repository, identity)
    if latest_exam_id is None:
        return AutoAnalysisResponse(
            reply="",
            provider="server_default",
        )
    latest_exam_id_str = str(latest_exam_id)
    expected_latest_exam_id = (payload.latestExamId or "").strip()
    if expected_latest_exam_id and expected_latest_exam_id != latest_exam_id_str:
        return AutoAnalysisResponse(
            reply="",
            provider="server_default",
        )

    # ── 2. Build dedicated proactive-analysis prompt ──
    role_id = payload.roleId or _DEFAULT_ROLE_ID
    role_name = _resolve_role_name(role_id, payload.roleName)
    role_system_prompt = _resolve_role_system_prompt(payload.roleSystemPrompt)

    cache_row = await _fetch_auto_analysis_cache(
        repository=repository,
        account_id=current_account.account_id,
        exam_id=latest_exam_id,
        role_id=role_id,
    )
    if cache_row is not None and getattr(cache_row, "delivered_at", None) is not None:
        return AutoAnalysisResponse(
            reply="",
            provider="server_default",
        )
    cached_reply = (
        str(getattr(cache_row, "reply", "")).strip() if cache_row is not None else ""
    )
    if cached_reply:
        await _increment_ai_dialogue_counter(repository, identity)
        await _mark_auto_analysis_delivered(
            repository=repository,
            account_id=current_account.account_id,
            exam_id=latest_exam_id,
            role_id=role_id,
        )
        return AutoAnalysisResponse(
            reply=cached_reply,
            provider="server_default",
        )

    prompt_service = PromptService(repository)
    system_prompt = await prompt_service.build_proactive_analysis_system_prompt(
        identity=identity,
        role_id=role_id,
        role_name=role_name,
        custom_role_style_prompt=role_system_prompt,
        notes=payload.notes,
        notes_title=payload.notesTitle,
    )

    # ── 3. Build the hidden user trigger message ──
    user_message = await prompt_service.build_auto_analysis_user_message(latest_exam_id)

    # ── 4. Create a synthetic ChatReplyRequest ──
    synthetic_request = ChatReplyRequest(
        studentId=student_id,
        ptaNickname=pta_nickname,
        messages=[ChatMessageRequest(role="user", content=user_message)],
        summary="",
        roleId=role_id,
        roleName=role_name,
        roleSystemPrompt=role_system_prompt,
        notes=payload.notes,
        notesTitle=payload.notesTitle,
    )

    # ── 5. Generate reply with tools ──
    tool_executor = ToolExecutor(
        repository=repository,
        identity=identity,
        notes_content=payload.notes,
        notes_title=payload.notesTitle,
    )
    result = await llm_service.generate_reply(
        synthetic_request,
        system_prompt=system_prompt,
        tool_executor=tool_executor,
    )
    reply = result.reply.strip()
    if reply:
        await _upsert_auto_analysis_cache(
            repository=repository,
            account_id=current_account.account_id,
            exam_id=latest_exam_id,
            role_id=role_id,
            provider_type="server_default",
            reply=reply,
            source="online",
        )
        await _mark_auto_analysis_delivered(
            repository=repository,
            account_id=current_account.account_id,
            exam_id=latest_exam_id,
            role_id=role_id,
        )

    await _increment_ai_dialogue_counter(repository, identity)
    return AutoAnalysisResponse(
        reply=reply,
        provider=result.provider,
        model=result.model,
        requestMode=result.requestMode,
        updatedNotes=getattr(tool_executor, "pending_notes_update", None),
    )


@router.post("/chat/auto-analysis/stream")
async def chat_auto_analysis_stream(
    payload: AutoAnalysisRequest,
    repository=Depends(get_repository),
    current_account=Depends(get_current_account),
    llm_service=Depends(get_llm_service),
) -> StreamingResponse:
    async def generate():
        identity = None
        try:
            student_id = payload.studentId
            pta_nickname = payload.ptaNickname
            if (student_id is None or not student_id.strip()) and (
                pta_nickname is None or not pta_nickname.strip()
            ):
                profile = await repository.fetch_account_profile(current_account.account_id)
                if profile is not None:
                    student_id = profile.student_id
                    pta_nickname = profile.pta_nickname

            if student_id or pta_nickname:
                try:
                    identity_service = StudentIdentityService(repository)
                    identity = await identity_service.resolve(student_id, pta_nickname)
                except Exception:
                    logger.debug(
                        "Student identity resolution failed for auto-analysis stream",
                        exc_info=True,
                    )

            if identity is None or identity.no_submission_records:
                yield _sse("done", {"type": "done", "reply": "", "provider": "none"})
                return

            latest_exam_id = await _resolve_latest_exam_id(repository, identity)
            if latest_exam_id is None:
                yield _sse("done", {"type": "done", "reply": "", "provider": "none"})
                return
            latest_exam_id_str = str(latest_exam_id)
            expected_latest_exam_id = (payload.latestExamId or "").strip()
            if expected_latest_exam_id and expected_latest_exam_id != latest_exam_id_str:
                yield _sse("done", {"type": "done", "reply": "", "provider": "none"})
                return

            role_id = payload.roleId or _DEFAULT_ROLE_ID
            role_name = _resolve_role_name(role_id, payload.roleName)
            role_system_prompt = _resolve_role_system_prompt(payload.roleSystemPrompt)
            cache_row = await _fetch_auto_analysis_cache(
                repository=repository,
                account_id=current_account.account_id,
                exam_id=latest_exam_id,
                role_id=role_id,
            )
            if cache_row is not None and getattr(cache_row, "delivered_at", None) is not None:
                yield _sse("done", {"type": "done", "reply": "", "provider": "none"})
                return
            cached_reply = (
                str(getattr(cache_row, "reply", "")).strip()
                if cache_row is not None
                else ""
            )
            if cached_reply:
                await _mark_auto_analysis_delivered(
                    repository=repository,
                    account_id=current_account.account_id,
                    exam_id=latest_exam_id,
                    role_id=role_id,
                )
                await _increment_ai_dialogue_counter(repository, identity)
                yield _sse("meta", {"type": "meta", "provider": "cache"})
                yield _sse("delta", {"type": "delta", "text": cached_reply})
                yield _sse(
                    "done",
                    {"type": "done", "reply": cached_reply, "provider": "cache"},
                )
                return

            prompt_service = PromptService(repository)
            system_prompt = await prompt_service.build_proactive_analysis_system_prompt(
                identity=identity,
                role_id=role_id,
                role_name=role_name,
                custom_role_style_prompt=role_system_prompt,
                notes=payload.notes,
                notes_title=payload.notesTitle,
            )
            synthetic_request = ChatReplyRequest(
                studentId=student_id,
                ptaNickname=pta_nickname,
                messages=[
                    ChatMessageRequest(
                        role="user",
                        content=await prompt_service.build_auto_analysis_user_message(
                            latest_exam_id
                        ),
                    )
                ],
                summary="",
                roleId=role_id,
                roleName=role_name,
                roleSystemPrompt=role_system_prompt,
                notes=payload.notes,
                notesTitle=payload.notesTitle,
            )
            tool_executor = ToolExecutor(
                repository=repository,
                identity=identity,
                notes_content=payload.notes,
                notes_title=payload.notesTitle,
            )
            final_reply = ""
            final_provider = "server_default"
            async for event in llm_service.stream_reply(
                synthetic_request,
                system_prompt=system_prompt,
                tool_executor=tool_executor,
            ):
                if event.get("type") == "done":
                    final_reply = str(event.get("reply", "")).strip()
                    final_provider = str(event.get("provider", "server_default"))
                yield _sse(str(event.get("type", "delta")), event)
            if final_reply:
                await _upsert_auto_analysis_cache(
                    repository=repository,
                    account_id=current_account.account_id,
                    exam_id=latest_exam_id,
                    role_id=role_id,
                    provider_type=final_provider,
                    reply=final_reply,
                    source="online",
                )
                await _mark_auto_analysis_delivered(
                    repository=repository,
                    account_id=current_account.account_id,
                    exam_id=latest_exam_id,
                    role_id=role_id,
                )
                await _increment_ai_dialogue_counter(repository, identity)
        except AppError as exc:
            yield _sse("error", {"type": "error", "code": exc.code, "message": exc.message})
        except Exception as exc:
            logger.warning("Auto-analysis stream failed", exc_info=True)
            yield _sse(
                "error",
                {
                    "type": "error",
                    "code": "AUTO_ANALYSIS_STREAM_FAILED",
                    "message": str(exc) or "Auto-analysis stream failed.",
                },
            )

    return StreamingResponse(generate(), media_type="text/event-stream")


@router.post(
    "/chat/auto-analysis/precompute-exam",
    response_model=AutoAnalysisPrecomputeResponse,
)
async def chat_auto_analysis_precompute_exam(
    payload: AutoAnalysisPrecomputeRequest,
    prewarm_token: str | None = Header(default=None, alias="X-AscendAny-Prewarm-Token"),
    repository=Depends(get_repository),
    llm_service=Depends(get_llm_service),
) -> AutoAnalysisPrecomputeResponse:
    expected_token = os.getenv("ASCENDANY_AUTO_ANALYSIS_PREWARM_TOKEN", "").strip()
    if not expected_token:
        raise AppError(
            status_code=503,
            code="AUTO_ANALYSIS_PREWARM_DISABLED",
            message="Auto-analysis prewarm is not configured.",
        )
    if (prewarm_token or "").strip() != expected_token:
        raise AppError(
            status_code=401,
            code="AUTO_ANALYSIS_PREWARM_UNAUTHORIZED",
            message="Invalid prewarm token.",
        )

    fetcher = getattr(repository, "fetch_auto_analysis_candidates_by_exam", None)
    if not callable(fetcher):
        raise AppError(
            status_code=500,
            code="AUTO_ANALYSIS_PREWARM_UNSUPPORTED",
            message="Repository does not support prewarm candidates query.",
        )

    role_id = payload.roleId or _DEFAULT_ROLE_ID
    role_name = _ROLE_NAMES.get(role_id, _ROLE_NAMES[_DEFAULT_ROLE_ID])
    prompt_service = PromptService(repository)
    identity_service = StudentIdentityService(repository)

    candidates = await fetcher(exam_id=payload.examId, limit=payload.maxAccounts)
    generated = 0
    skipped_cached = 0
    skipped_not_latest = 0
    failed = 0

    for candidate in candidates:
        student_id = getattr(candidate, "student_id", None)
        pta_nickname = getattr(candidate, "pta_nickname", None)
        account_id = getattr(candidate, "account_id", None)
        if account_id is None:
            failed += 1
            continue

        try:
            identity = await identity_service.resolve(student_id, pta_nickname)
        except Exception:
            failed += 1
            continue
        if identity.no_submission_records:
            skipped_not_latest += 1
            continue

        latest_exam_id = await _resolve_latest_exam_id(repository, identity)
        if latest_exam_id is None or latest_exam_id != payload.examId:
            skipped_not_latest += 1
            continue

        cache_row = await _fetch_auto_analysis_cache(
            repository=repository,
            account_id=int(account_id),
            exam_id=payload.examId,
            role_id=role_id,
        )
        if cache_row is not None and str(getattr(cache_row, "reply", "")).strip():
            skipped_cached += 1
            continue

        try:
            system_prompt = await prompt_service.build_proactive_analysis_system_prompt(
                identity=identity,
                role_id=role_id,
                role_name=role_name,
            )
            user_message = await prompt_service.build_auto_analysis_user_message(
                payload.examId
            )
            synthetic_request = ChatReplyRequest(
                studentId=student_id,
                ptaNickname=pta_nickname,
                messages=[ChatMessageRequest(role="user", content=user_message)],
                summary="",
                roleId=role_id,
            )
            tool_executor = ToolExecutor(repository=repository, identity=identity)
            result = await llm_service.generate_reply(
                synthetic_request,
                system_prompt=system_prompt,
                tool_executor=tool_executor,
            )
            reply = result.reply.strip()
            if not reply:
                failed += 1
                continue
            await _upsert_auto_analysis_cache(
                repository=repository,
                account_id=int(account_id),
                exam_id=payload.examId,
                role_id=role_id,
                provider_type="server_default",
                reply=reply,
                source="prewarm",
            )
            generated += 1
        except Exception:
            logger.warning(
                "Auto-analysis prewarm failed: account_id=%s exam_id=%s",
                account_id,
                payload.examId,
                exc_info=True,
            )
            failed += 1

    return AutoAnalysisPrecomputeResponse(
        examId=payload.examId,
        roleId=role_id,
        candidates=len(candidates),
        generated=generated,
        skippedCached=skipped_cached,
        skippedNotLatest=skipped_not_latest,
        failed=failed,
    )
