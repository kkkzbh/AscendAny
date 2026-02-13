from __future__ import annotations

from fastapi import APIRouter, Depends

from ..deps import get_current_account_optional, get_repository, get_llm_service
from ...schemas.chat import ChatReplyRequest, ChatReplyResponse

router = APIRouter(tags=["chat"])


@router.post("/chat/reply", response_model=ChatReplyResponse)
async def chat_reply(
    payload: ChatReplyRequest,
    repository=Depends(get_repository),
    current_account=Depends(get_current_account_optional),
    llm_service=Depends(get_llm_service),
) -> ChatReplyResponse:
    effective_payload = payload
    if (
        (payload.studentId is None or not payload.studentId.strip())
        and (payload.ptaNickname is None or not payload.ptaNickname.strip())
        and current_account is not None
    ):
        profile = await repository.fetch_account_profile(current_account.account_id)
        if profile is not None:
            effective_payload = payload.model_copy(
                update={
                    "studentId": profile.student_id,
                    "ptaNickname": profile.pta_nickname,
                }
            )
    return await llm_service.generate_reply(effective_payload)
