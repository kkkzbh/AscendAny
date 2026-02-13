from __future__ import annotations

from fastapi import APIRouter, Depends

from ..deps import get_repository
from ...schemas.meta import LatestExamImportedAtResponse

router = APIRouter(tags=["meta"])


@router.get(
    "/meta/latest_exam_imported_at", response_model=LatestExamImportedAtResponse
)
async def latest_exam_imported_at(
    repository=Depends(get_repository),
) -> LatestExamImportedAtResponse:
    latest = await repository.fetch_latest_exam_imported_at()
    return LatestExamImportedAtResponse(latestExamImportedAt=latest)
