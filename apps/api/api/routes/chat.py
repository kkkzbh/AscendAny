from __future__ import annotations

from fastapi import APIRouter, Depends

from ..deps import get_llm_service
from ...schemas.chat import ChatReplyRequest, ChatReplyResponse

router = APIRouter(tags=["chat"])


@router.post("/chat/reply", response_model=ChatReplyResponse)
async def chat_reply(
    payload: ChatReplyRequest,
    llm_service=Depends(get_llm_service),
) -> ChatReplyResponse:
    return await llm_service.generate_reply(payload)
