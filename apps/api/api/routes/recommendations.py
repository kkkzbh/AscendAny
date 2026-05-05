from __future__ import annotations

import os
import subprocess
from pathlib import Path
from typing import Any

from fastapi import APIRouter, Depends, Query

from ..deps import get_admin_account, get_current_account, get_repository, get_settings
from ...core.config import PROJECT_ROOT, Settings
from ...core.errors import AppError
from ...schemas.recommendations import (
    LearningPathResponse,
    ProblemRecommendationItem,
    ProblemRecommendationsResponse,
    RecommendationRunItem,
    RecommendationRunsResponse,
    RecommendationTrainRequest,
    RecommendationTrainResponse,
)
from ...services.auth import AuthenticatedAccount
from ...services.identity import StudentIdentityService

router = APIRouter(tags=["recommendations"])


def _bounded_top_k(value: int) -> int:
    return max(1, min(int(value), 50))


async def _identity_for_account(repository: Any, account: AuthenticatedAccount):
    profile = await repository.fetch_account_profile(account.account_id)
    if profile is None:
        raise AppError(
            status_code=400,
            code="STUDENT_PROFILE_NOT_BOUND",
            message="Current account is not bound to a student profile.",
        )
    return await StudentIdentityService(repository=repository).resolve(
        student_id=profile.student_id,
        pta_nickname=profile.pta_nickname,
    )


async def _identity_for_student_no(repository: Any, student_id: str):
    return await StudentIdentityService(repository=repository).resolve(
        student_id=student_id,
        pta_nickname=None,
    )


def _student_ids(identity: Any) -> list[int]:
    return list(identity.student_entity_ids or (identity.student_entity_id,))


def _item_from_raw(raw: dict[str, Any], index: int) -> ProblemRecommendationItem:
    knowledge_points = raw.get("knowledgePoints", raw.get("knowledge_points", []))
    if not isinstance(knowledge_points, list):
        knowledge_points = []
    return ProblemRecommendationItem(
        problemId=str(raw.get("problemId", raw.get("problem_id", ""))),
        title=str(raw["title"]) if raw.get("title") is not None else None,
        url=str(raw.get("url", raw.get("link")))
        if raw.get("url", raw.get("link")) is not None
        else None,
        knowledgePoints=[str(item) for item in knowledge_points],
        difficulty=float(raw["difficulty"]) if raw.get("difficulty") is not None else None,
        score=float(raw["score"]) if raw.get("score") is not None else None,
        reason=str(raw["reason"]) if raw.get("reason") is not None else None,
        rank=int(raw.get("rank", index + 1)),
        meta=raw.get("meta", {}) if isinstance(raw.get("meta"), dict) else {},
    )


async def _build_problem_response(repository: Any, identity: Any, top_k: int):
    student_ids = _student_ids(identity)
    snapshot = await repository.fetch_latest_problem_recommendations(
        student_ids, top_k=_bounded_top_k(top_k)
    )
    if snapshot is None:
        return ProblemRecommendationsResponse(
            studentEntityId=identity.student_entity_id,
            studentEntityIds=student_ids,
            items=[],
        )
    return ProblemRecommendationsResponse(
        studentEntityId=snapshot.student_id,
        studentEntityIds=student_ids,
        modelRunId=snapshot.model_run_id,
        generatedAt=snapshot.generated_at,
        items=[
            _item_from_raw(item, index)
            for index, item in enumerate(snapshot.items)
            if item.get("problemId") or item.get("problem_id")
        ],
    )


async def _build_path_response(repository: Any, identity: Any):
    student_ids = _student_ids(identity)
    snapshot = await repository.fetch_latest_learning_path(student_ids)
    if snapshot is None:
        return LearningPathResponse(
            studentEntityId=identity.student_entity_id,
            studentEntityIds=student_ids,
        )
    return LearningPathResponse(
        studentEntityId=snapshot.student_id,
        studentEntityIds=student_ids,
        modelRunId=snapshot.model_run_id,
        generatedAt=snapshot.generated_at,
        targets=snapshot.targets,
        path=snapshot.path,
        explanations=snapshot.explanations,
    )


@router.get(
    "/recommendations/problems/me",
    response_model=ProblemRecommendationsResponse,
)
async def recommendation_problems_me(
    top_k: int = Query(default=10, alias="topK", ge=1, le=50),
    repository=Depends(get_repository),
    current_account: AuthenticatedAccount = Depends(get_current_account),
) -> ProblemRecommendationsResponse:
    identity = await _identity_for_account(repository, current_account)
    return await _build_problem_response(repository, identity, top_k)


@router.get(
    "/recommendations/problems/student",
    response_model=ProblemRecommendationsResponse,
)
async def recommendation_problems_student(
    student_id: str = Query(alias="studentId", min_length=1),
    top_k: int = Query(default=10, alias="topK", ge=1, le=50),
    repository=Depends(get_repository),
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> ProblemRecommendationsResponse:
    identity = await _identity_for_student_no(repository, student_id)
    return await _build_problem_response(repository, identity, top_k)


@router.get("/recommendations/path/me", response_model=LearningPathResponse)
async def recommendation_path_me(
    repository=Depends(get_repository),
    current_account: AuthenticatedAccount = Depends(get_current_account),
) -> LearningPathResponse:
    identity = await _identity_for_account(repository, current_account)
    return await _build_path_response(repository, identity)


@router.get("/recommendations/path/student", response_model=LearningPathResponse)
async def recommendation_path_student(
    student_id: str = Query(alias="studentId", min_length=1),
    repository=Depends(get_repository),
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> LearningPathResponse:
    identity = await _identity_for_student_no(repository, student_id)
    return await _build_path_response(repository, identity)


@router.post(
    "/admin/recommendation/train",
    response_model=RecommendationTrainResponse,
)
async def start_recommendation_train(
    body: RecommendationTrainRequest,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    admin: AuthenticatedAccount = Depends(get_admin_account),
) -> RecommendationTrainResponse:
    model_run_id = await repository.create_recommendation_model_run(
        model_type=body.modelType,
        config=body.config,
        created_by_account_id=admin.account_id,
    )
    await repository.add_recommendation_model_event(
        model_run_id=model_run_id,
        level="info",
        message="recommendation training queued by API",
        data={"model_type": body.modelType},
    )

    python_path = _resolve_project_path(settings.recommendation.python_path)
    artifacts_dir = _resolve_project_path(settings.recommendation.artifacts_dir)
    command = [
        str(python_path),
        "-m",
        settings.recommendation.module,
        "--run-id",
        str(model_run_id),
        "--artifacts-dir",
        str(artifacts_dir),
    ]

    if not python_path.exists():
        await repository.add_recommendation_model_event(
            model_run_id=model_run_id,
            level="error",
            message="recommendation training python executable not found",
            data={"python_path": str(python_path)},
        )
        raise AppError(
            status_code=500,
            code="RECOMMENDATION_TRAINING_ENV_MISSING",
            message="Recommendation training Python environment is not available.",
        )

    env = os.environ.copy()
    env["ASCENDANY_RECOMMENDATION_RUN_ID"] = str(model_run_id)
    env["ASCENDANY_RECOMMENDATION_ARTIFACTS_DIR"] = str(artifacts_dir)
    try:
        subprocess.Popen(
            command,
            cwd=str(PROJECT_ROOT),
            env=env,
            start_new_session=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except OSError as exc:
        await repository.add_recommendation_model_event(
            model_run_id=model_run_id,
            level="error",
            message="failed to start recommendation training process",
            data={"error": str(exc)},
        )
        raise AppError(
            status_code=500,
            code="RECOMMENDATION_TRAINING_START_FAILED",
            message="Failed to start recommendation training process.",
        ) from exc

    return RecommendationTrainResponse(
        modelRunId=model_run_id,
        status="queued",
        command=command,
    )


@router.get(
    "/admin/recommendation/runs",
    response_model=RecommendationRunsResponse,
)
async def recommendation_runs(
    limit: int = Query(default=50, ge=1, le=200),
    repository=Depends(get_repository),
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> RecommendationRunsResponse:
    rows = await repository.fetch_recommendation_model_runs(limit=limit)
    return RecommendationRunsResponse(
        items=[
            RecommendationRunItem(
                modelRunId=row.model_run_id,
                status=row.status,
                modelType=row.model_type,
                metrics=row.metrics,
                artifactPath=row.artifact_path,
                errorMessage=row.error_message,
                createdAt=row.created_at,
                startedAt=row.started_at,
                finishedAt=row.finished_at,
            )
            for row in rows
        ]
    )


def _resolve_project_path(path: str) -> Path:
    candidate = Path(path).expanduser()
    if not candidate.is_absolute():
        candidate = (PROJECT_ROOT / candidate).resolve()
    return candidate
