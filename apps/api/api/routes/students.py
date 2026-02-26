from __future__ import annotations

from fastapi import APIRouter, Depends, Query

from ..deps import get_current_account_optional, get_repository, get_settings
from ...core.errors import AppError
from ...schemas.students import (
    LeaderboardEntryResponse,
    MetricDeltaInfoResponse,
    MetricDeltaItemResponse,
    MetricMissingItemResponse,
    RatingInfoResponse,
    ResolvedIdentityResponse,
    StudentDashboardResponse,
    StudentLeaderboardResponse,
    StudentMetricsResponse,
)
from ...services.dashboard import DashboardService
from ...services.growth_insights import build_empty_growth_insights
from ...services.identity import StudentIdentityService

router = APIRouter(tags=["students"])


def _metric_number(value: object) -> float:
    if value is None:
        return 0.0
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0


def _build_not_found_fallback_dashboard(
    student_id: str | None,
    pta_nickname: str | None,
) -> StudentDashboardResponse:
    empty_growth = build_empty_growth_insights()
    return StudentDashboardResponse(
        metrics=StudentMetricsResponse(
            knowledge=50,
            accuracy=50,
            quality=50,
            flexibility=50,
            proficiency=50,
        ),
        metricMissing=MetricMissingItemResponse(
            knowledge=True,
            accuracy=True,
            quality=True,
            flexibility=True,
            proficiency=True,
        ),
        rating=RatingInfoResponse(current=800, lastDelta=None, history=[]),
        metricDelta=MetricDeltaInfoResponse(
            latestExamId=None,
            latestExamName=None,
            latestExamDate=None,
            baseline="zero",
            values=MetricDeltaItemResponse(
                knowledge=0,
                accuracy=0,
                quality=0,
                flexibility=0,
                proficiency=0,
            ),
        ),
        identity=ResolvedIdentityResponse(
            studentId=(student_id or "").strip(),
            ptaNickname=(pta_nickname or "").strip() or None,
            noSubmissionRecords=True,
        ),
        progressExplanation=empty_growth.progress_explanation,
        milestoneStreak=empty_growth.milestone_streak,
        peerComparison=empty_growth.peer_comparison,
        postExamSupport=empty_growth.post_exam_support,
    )


@router.get("/students/dashboard", response_model=StudentDashboardResponse)
async def students_dashboard(
    student_id: str | None = Query(default=None, alias="studentId"),
    pta_nickname: str | None = Query(default=None, alias="ptaNickname"),
    repository=Depends(get_repository),
    settings=Depends(get_settings),
    current_account=Depends(get_current_account_optional),
) -> StudentDashboardResponse:
    if (student_id is None or not student_id.strip()) and (
        pta_nickname is None or not pta_nickname.strip()
    ):
        if current_account is not None:
            profile = await repository.fetch_account_profile(current_account.account_id)
            if profile is not None:
                student_id = profile.student_id
                pta_nickname = profile.pta_nickname

    identity_service = StudentIdentityService(repository=repository)
    try:
        identity = await identity_service.resolve(
            student_id=student_id, pta_nickname=pta_nickname
        )
    except AppError as exc:
        if exc.code == "STUDENT_NOT_FOUND":
            return _build_not_found_fallback_dashboard(student_id, pta_nickname)
        raise

    dashboard_service = DashboardService(
        repository=repository,
        default_rating=settings.dashboard.default_rating,
        default_metric=settings.dashboard.default_metric,
        rating_history_limit=settings.dashboard.rating_history_limit,
    )
    return await dashboard_service.build(identity)


@router.get("/students/leaderboard", response_model=StudentLeaderboardResponse)
async def students_leaderboard(
    repository=Depends(get_repository),
) -> StudentLeaderboardResponse:
    rows = await repository.fetch_student_leaderboard()
    items = [
        LeaderboardEntryResponse(
            studentId=row.student_no,
            grade=row.student_no[:4],
            username=row.username,
            rating=row.rating,
            knowledge=_metric_number(row.knowledge),
            accuracy=_metric_number(row.accuracy),
            quality=_metric_number(row.quality),
            flexibility=_metric_number(row.flexibility),
            proficiency=_metric_number(row.proficiency),
        )
        for row in rows
    ]
    return StudentLeaderboardResponse(items=items)
