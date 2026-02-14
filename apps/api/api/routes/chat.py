from __future__ import annotations

import logging

from fastapi import APIRouter, Depends

from ..deps import (
    get_current_account_optional,
    get_current_account,
    get_repository,
    get_llm_service,
)
from ...schemas.chat import (
    AutoAnalysisRequest,
    AutoAnalysisResponse,
    ChatMessageRequest,
    ChatReplyRequest,
    ChatReplyResponse,
)
from ...services.identity import StudentIdentityService
from ...services.prompt import PromptService
from ...services.tools import ToolExecutor

logger = logging.getLogger(__name__)

router = APIRouter(tags=["chat"])

# Server-side role name mapping (mirrors frontend BUILT_IN_ROLES)
_ROLE_NAMES: dict[str, str] = {
    "xiaoD": "小D",
}

_DEFAULT_ROLE_ID = "xiaoD"


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
    role_id = payload.roleId or _DEFAULT_ROLE_ID
    role_name = _ROLE_NAMES.get(role_id, _ROLE_NAMES[_DEFAULT_ROLE_ID])
    prompt_service = PromptService(repository)
    system_prompt = await prompt_service.build_system_prompt(
        identity=identity,
        role_id=role_id,
        role_name=role_name,
    )

    # ── 4. Create tool executor ──
    tool_executor = ToolExecutor(repository=repository, identity=identity)

    # ── 5. Generate LLM reply ──
    return await llm_service.generate_reply(
        effective_payload,
        system_prompt=system_prompt,
        tool_executor=tool_executor,
    )


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
            provider=payload.providerType,
        )

    # ── 2. Build system prompt (layers 1-4) ──
    role_id = payload.roleId or _DEFAULT_ROLE_ID
    role_name = _ROLE_NAMES.get(role_id, _ROLE_NAMES[_DEFAULT_ROLE_ID])
    prompt_service = PromptService(repository)
    system_prompt = await prompt_service.build_system_prompt(
        identity=identity,
        role_id=role_id,
        role_name=role_name,
    )

    # ── 3. Build the hidden user trigger message ──
    user_message = prompt_service.build_auto_analysis_user_message()

    # ── 4. Create a synthetic ChatReplyRequest ──
    synthetic_request = ChatReplyRequest(
        studentId=student_id,
        ptaNickname=pta_nickname,
        messages=[ChatMessageRequest(role="user", content=user_message)],
        summary="",
        providerType=payload.providerType,
        providerConfig=payload.providerConfig,
        roleId=role_id,
    )

    # ── 5. Generate reply with tools ──
    tool_executor = ToolExecutor(repository=repository, identity=identity)
    result = await llm_service.generate_reply(
        synthetic_request,
        system_prompt=system_prompt,
        tool_executor=tool_executor,
    )

    return AutoAnalysisResponse(
        reply=result.reply,
        provider=result.provider,
    )
