from __future__ import annotations

from fastapi import APIRouter, Depends, Query

from ..deps import get_repository, get_settings
from ...schemas.students import StudentDashboardResponse
from ...services.dashboard import DashboardService
from ...services.identity import StudentIdentityService

router = APIRouter(tags=["students"])


@router.get("/students/dashboard", response_model=StudentDashboardResponse)
async def students_dashboard(
    student_id: str | None = Query(default=None, alias="studentId"),
    pta_nickname: str | None = Query(default=None, alias="ptaNickname"),
    repository=Depends(get_repository),
    settings=Depends(get_settings),
) -> StudentDashboardResponse:
    identity_service = StudentIdentityService(repository=repository)
    identity = await identity_service.resolve(
        student_id=student_id, pta_nickname=pta_nickname
    )

    dashboard_service = DashboardService(
        repository=repository,
        default_rating=settings.dashboard.default_rating,
        default_metric=settings.dashboard.default_metric,
        rating_history_limit=settings.dashboard.rating_history_limit,
    )
    return await dashboard_service.build(identity)
