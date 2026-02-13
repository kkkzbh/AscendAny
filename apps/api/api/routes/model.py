from __future__ import annotations

from fastapi import APIRouter, Depends

from ..deps import get_llm_service
from ...schemas.model import ModelProvidersResponse

router = APIRouter(tags=["model"])


@router.get("/model/providers", response_model=ModelProvidersResponse)
async def model_providers(
    llm_service=Depends(get_llm_service),
) -> ModelProvidersResponse:
    return llm_service.list_provider_options()
