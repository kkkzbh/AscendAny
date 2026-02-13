from __future__ import annotations

from fastapi import APIRouter

from ...schemas.common import HealthzResponse

router = APIRouter(tags=["health"])


@router.get("/healthz", response_model=HealthzResponse)
async def healthz() -> HealthzResponse:
    return HealthzResponse(ok=True)
