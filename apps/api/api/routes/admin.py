from __future__ import annotations

import os
import time
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from typing import Any

import yaml
from fastapi import APIRouter, Depends, Query, Request

from ..deps import get_admin_account, get_repository
from ...core.errors import AppError
from ...schemas.admin import (
    AdminAccountSummary,
    AdminAccountsResponse,
    AdminAuditLogItem,
    AdminAuditLogResponse,
    AdminConfigPatch,
    AdminConfigResponse,
    AdminFusionHalfLifeConfig,
    AdminMappingConfig,
    AdminMetricsConfig,
    AdminPreprocessConfig,
    AdminRatingConfig,
    AdminStudentExamReport,
    AdminStudentExamReportsResponse,
    AdminStudentListResponse,
    AdminStudentSummary,
    AdminWarmupConfig,
)

router = APIRouter(tags=["admin"], prefix="/admin")

_PROJECT_ROOT = Path(__file__).resolve().parents[4]
_PREPROCESS_CONFIG_ENV = "ASCENDANY_PREPROCESS_CONFIG"
_AUDIT_LIMIT = 200


def _resolve_preprocess_config_path() -> Path:
    raw_path = os.getenv(_PREPROCESS_CONFIG_ENV, "").strip()
    candidate = (
        Path(raw_path)
        if raw_path
        else _PROJECT_ROOT / "preprocess/config/default.yaml"
    )
    if not candidate.is_absolute():
        candidate = (_PROJECT_ROOT / candidate).resolve()
    return candidate


def _read_yaml(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {}
    loaded = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    if not isinstance(loaded, dict):
        raise AppError(
            status_code=500,
            code="INVALID_CONFIG_FILE",
            message=f"Config file root must be an object: {path}",
        )
    return loaded


def _write_yaml(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = path.with_suffix(path.suffix + ".tmp")
    tmp_path.write_text(
        yaml.safe_dump(data, allow_unicode=True, sort_keys=False),
        encoding="utf-8",
    )
    tmp_path.replace(path)


def _load_preprocess_settings() -> Any:
    from preprocess.config import load_settings as load_preprocess_settings

    return load_preprocess_settings(config_path=_resolve_preprocess_config_path())


def _to_float(value: object) -> float | None:
    if value is None:
        return None
    if isinstance(value, Decimal):
        return float(value)
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def _clean_strings(values: list[str]) -> list[str]:
    return [item.strip() for item in values if item.strip()]


def _preprocess_config_response(settings: Any) -> AdminPreprocessConfig:
    return AdminPreprocessConfig(
        practiceRoot=str(settings.practice_root),
        encodings=list(settings.ingest.encodings),
        fingerprintRoles=list(settings.ingest.fingerprint_roles),
        timezone=settings.ingest.timezone,
        metrics=AdminMetricsConfig(
            winsorLow=float(settings.metrics.winsor_low),
            winsorHigh=float(settings.metrics.winsor_high),
            flexibilityModeDefault=settings.metrics.flexibility_mode_default,
            includedProblemKinds=list(settings.metrics.included_problem_kinds),
            randomExamMissingDrawnSetPolicy=(
                settings.metrics.random_exam_missing_drawn_set_policy
            ),
            randomExamSlotSourcePriority=list(
                settings.metrics.random_exam_slot_source_priority
            ),
        ),
        mapping=AdminMappingConfig(
            primaryKeys=list(settings.mapping.primary_keys),
            actorSources=list(settings.mapping.actor_sources),
            strictMode=bool(settings.mapping.strict_mode),
            autoBindOnIngest=bool(settings.mapping.auto_bind_on_ingest),
            claimIdentitySource=settings.mapping.claim_identity_source,
        ),
        fusionHalfLifeDays=AdminFusionHalfLifeConfig(
            knowledge=float(settings.fusion.half_life_days.knowledge),
            accuracy=float(settings.fusion.half_life_days.accuracy),
            quality=float(settings.fusion.half_life_days.quality),
            flexibility=float(settings.fusion.half_life_days.flexibility),
            proficiency=float(settings.fusion.half_life_days.proficiency),
        ),
        rating=AdminRatingConfig(
            initialRating=int(settings.rating.initial_rating),
            maxBinarySearchRating=int(settings.rating.max_binary_search_rating),
            minBinarySearchRating=int(settings.rating.min_binary_search_rating),
            binarySearchSteps=int(settings.rating.binary_search_steps),
        ),
        warmup=AdminWarmupConfig(
            enabled=bool(settings.warmup.enabled),
            apiBaseUrl=settings.warmup.api_base_url,
            tokenEnv=settings.warmup.token_env,
            timeoutSeconds=float(settings.warmup.timeout_seconds),
            roleId=settings.warmup.role_id,
        ),
    )


def _build_config_response() -> AdminConfigResponse:
    preprocess_settings = _load_preprocess_settings()
    return AdminConfigResponse(
        preprocessConfigPath=str(_resolve_preprocess_config_path()),
        preprocess=_preprocess_config_response(preprocess_settings),
        restartRequiredKeys=["db", "auth", "sso"],
    )


def _remember_audit_event(
    request: Request,
    kind: str,
    status: str,
    title: str,
    detail: str,
    payload: dict[str, object] | None = None,
) -> None:
    events = getattr(request.app.state, "admin_audit_events", None)
    if not isinstance(events, list):
        events = []
        request.app.state.admin_audit_events = events
    events.insert(
        0,
        {
            "id": f"memory:{int(time.time() * 1000)}",
            "kind": kind,
            "status": status,
            "title": title,
            "detail": detail,
            "createdAt": datetime.now(timezone.utc),
            "payload": payload or {},
        },
    )
    del events[_AUDIT_LIMIT:]


@router.get("/config", response_model=AdminConfigResponse)
async def get_admin_config(
    _admin=Depends(get_admin_account),
) -> AdminConfigResponse:
    return _build_config_response()


@router.patch("/config", response_model=AdminConfigResponse)
async def patch_admin_config(
    payload: AdminConfigPatch,
    request: Request,
    _admin=Depends(get_admin_account),
) -> AdminConfigResponse:
    preprocess_config_path = _resolve_preprocess_config_path()

    if payload.preprocess is not None:
        raw = _read_yaml(preprocess_config_path)
        pp = payload.preprocess
        if pp.practiceRoot is not None:
            raw["practice_root"] = pp.practiceRoot.strip()
        ingest = raw.setdefault("ingest", {})
        if not isinstance(ingest, dict):
            ingest = {}
            raw["ingest"] = ingest
        if pp.encodings is not None:
            ingest["encodings"] = _clean_strings(pp.encodings)
        if pp.fingerprintRoles is not None:
            ingest["fingerprint_roles"] = _clean_strings(pp.fingerprintRoles)
        if pp.timezone is not None:
            ingest["timezone"] = pp.timezone.strip()

        if pp.metrics is not None:
            metrics = raw.setdefault("metrics", {})
            if not isinstance(metrics, dict):
                metrics = {}
                raw["metrics"] = metrics
            metric_patch = pp.metrics
            if metric_patch.winsorLow is not None:
                metrics["winsor_low"] = float(metric_patch.winsorLow)
            if metric_patch.winsorHigh is not None:
                metrics["winsor_high"] = float(metric_patch.winsorHigh)
            if metric_patch.flexibilityModeDefault is not None:
                metrics["flexibility_mode_default"] = (
                    metric_patch.flexibilityModeDefault.strip()
                )
            if metric_patch.includedProblemKinds is not None:
                metrics["included_problem_kinds"] = _clean_strings(
                    metric_patch.includedProblemKinds
                )
            if metric_patch.randomExamMissingDrawnSetPolicy is not None:
                metrics["random_exam_missing_drawn_set_policy"] = (
                    metric_patch.randomExamMissingDrawnSetPolicy.strip()
                )
            if metric_patch.randomExamSlotSourcePriority is not None:
                metrics["random_exam_slot_source_priority"] = _clean_strings(
                    metric_patch.randomExamSlotSourcePriority
                )

        if pp.mapping is not None:
            mapping = raw.setdefault("mapping", {})
            if not isinstance(mapping, dict):
                mapping = {}
                raw["mapping"] = mapping
            mapping_patch = pp.mapping
            if mapping_patch.primaryKeys is not None:
                mapping["primary_keys"] = _clean_strings(mapping_patch.primaryKeys)
            if mapping_patch.actorSources is not None:
                mapping["actor_sources"] = _clean_strings(mapping_patch.actorSources)
            if mapping_patch.strictMode is not None:
                mapping["strict_mode"] = bool(mapping_patch.strictMode)
            if mapping_patch.autoBindOnIngest is not None:
                mapping["auto_bind_on_ingest"] = bool(mapping_patch.autoBindOnIngest)
            if mapping_patch.claimIdentitySource is not None:
                mapping["claim_identity_source"] = (
                    mapping_patch.claimIdentitySource.strip()
                )

        if pp.fusionHalfLifeDays is not None:
            fusion = raw.setdefault("fusion", {})
            if not isinstance(fusion, dict):
                fusion = {}
                raw["fusion"] = fusion
            half_life = fusion.setdefault("half_life_days", {})
            if not isinstance(half_life, dict):
                half_life = {}
                fusion["half_life_days"] = half_life
            for key, value in pp.fusionHalfLifeDays.model_dump(
                exclude_none=True
            ).items():
                half_life[key] = float(value)

        if pp.rating is not None:
            rating = raw.setdefault("rating", {})
            if not isinstance(rating, dict):
                rating = {}
                raw["rating"] = rating
            rating_patch = pp.rating
            if rating_patch.initialRating is not None:
                rating["initial_rating"] = int(rating_patch.initialRating)
            if rating_patch.maxBinarySearchRating is not None:
                rating["max_binary_search_rating"] = int(
                    rating_patch.maxBinarySearchRating
                )
            if rating_patch.minBinarySearchRating is not None:
                rating["min_binary_search_rating"] = int(
                    rating_patch.minBinarySearchRating
                )
            if rating_patch.binarySearchSteps is not None:
                rating["binary_search_steps"] = int(rating_patch.binarySearchSteps)

        if pp.warmup is not None:
            warmup = raw.setdefault("warmup", {})
            if not isinstance(warmup, dict):
                warmup = {}
                raw["warmup"] = warmup
            warmup_patch = pp.warmup
            if warmup_patch.enabled is not None:
                warmup["enabled"] = bool(warmup_patch.enabled)
            if warmup_patch.apiBaseUrl is not None:
                warmup["api_base_url"] = warmup_patch.apiBaseUrl.strip() or None
            if warmup_patch.tokenEnv is not None:
                warmup["token_env"] = warmup_patch.tokenEnv.strip()
            if warmup_patch.timeoutSeconds is not None:
                warmup["timeout_seconds"] = float(warmup_patch.timeoutSeconds)
            if warmup_patch.roleId is not None:
                warmup["role_id"] = warmup_patch.roleId.strip()

        _write_yaml(preprocess_config_path, raw)

    _remember_audit_event(
        request,
        kind="config_update",
        status="success",
        title="管理员配置已保存",
        detail="预处理参数发生变更",
        payload=payload.model_dump(exclude_none=True),
    )
    return _build_config_response()


def _student_summary(row: Any) -> AdminStudentSummary:
    exam_count = int(getattr(row, "exam_count", 0))
    generated = int(getattr(row, "generated_reports", 0))
    failed = int(getattr(row, "failed_reports", 0))
    missing = int(getattr(row, "missing_reports", 0))
    return AdminStudentSummary(
        studentEntityId=str(row.student_entity_id),
        studentId=row.student_no,
        studentName=row.student_name,
        grade=row.student_no[:4] if row.student_no and len(row.student_no) >= 4 else None,
        username=row.username,
        rating=int(row.rating),
        knowledge=_to_float(row.knowledge),
        accuracy=_to_float(row.accuracy),
        quality=_to_float(row.quality),
        flexibility=_to_float(row.flexibility),
        proficiency=_to_float(row.proficiency),
        latestExamAt=row.latest_exam_at,
        examCount=exam_count,
        generatedReports=generated,
        failedReports=failed,
        missingReports=missing,
        reportCompletionRate=(generated / exam_count if exam_count else 0),
    )


@router.get("/students", response_model=AdminStudentListResponse)
async def list_admin_students(
    search: str | None = Query(default=None),
    limit: int = Query(default=200, ge=1, le=1000),
    _admin=Depends(get_admin_account),
    repository=Depends(get_repository),
) -> AdminStudentListResponse:
    fetcher = getattr(repository, "fetch_admin_student_summaries", None)
    if not callable(fetcher):
        raise AppError(
            status_code=500,
            code="ADMIN_STUDENTS_UNSUPPORTED",
            message="Repository does not support admin student summaries.",
        )
    rows = await fetcher(search=search, limit=limit)
    items = [_student_summary(row) for row in rows]
    return AdminStudentListResponse(items=items, total=len(items))


@router.get(
    "/students/{student_entity_id}/exam-reports",
    response_model=AdminStudentExamReportsResponse,
)
async def get_admin_student_exam_reports(
    student_entity_id: int,
    _admin=Depends(get_admin_account),
    repository=Depends(get_repository),
) -> AdminStudentExamReportsResponse:
    summary_fetcher = getattr(repository, "fetch_admin_student_summaries", None)
    report_fetcher = getattr(repository, "fetch_admin_student_exam_reports", None)
    if not callable(report_fetcher):
        raise AppError(
            status_code=500,
            code="ADMIN_STUDENT_REPORTS_UNSUPPORTED",
            message="Repository does not support admin student report lookup.",
        )
    student = None
    if callable(summary_fetcher):
        summaries = await summary_fetcher(search=None, limit=1000)
        for row in summaries:
            if int(row.student_entity_id) == student_entity_id:
                student = _student_summary(row)
                break
    rows = await report_fetcher(student_id=student_entity_id)
    items = [
        AdminStudentExamReport(
            examId=str(row.exam_id),
            examName=row.exam_name,
            examType=row.exam_type,
            examDate=row.exam_date,
            rank=row.rank,
            totalScore=_to_float(row.total_score),
            solvedCount=row.solved_count,
            ratingDelta=row.rating_delta,
            oldRating=row.old_rating,
            newRating=row.new_rating,
            knowledge=_to_float(row.knowledge),
            accuracy=_to_float(row.accuracy),
            quality=_to_float(row.quality),
            flexibility=_to_float(row.flexibility),
            proficiency=_to_float(row.proficiency),
            analysisStatus=row.analysis_status,
            analysisReply=row.analysis_reply,
            generatedAt=row.generated_at,
            errorMessage=row.error_message,
        )
        for row in rows
    ]
    return AdminStudentExamReportsResponse(student=student, items=items)


@router.get("/accounts", response_model=AdminAccountsResponse)
async def list_admin_accounts(
    limit: int = Query(default=200, ge=1, le=1000),
    _admin=Depends(get_admin_account),
    repository=Depends(get_repository),
) -> AdminAccountsResponse:
    fetcher = getattr(repository, "fetch_admin_account_summaries", None)
    if not callable(fetcher):
        raise AppError(
            status_code=500,
            code="ADMIN_ACCOUNTS_UNSUPPORTED",
            message="Repository does not support admin account summaries.",
        )
    rows = await fetcher(limit=limit)
    items = [
        AdminAccountSummary(
            accountId=str(row.account_id),
            username=row.username,
            displayName=row.display_name,
            isActive=row.is_active,
            isAdmin=row.is_admin,
            provisionSource=row.provision_source,
            studentId=row.student_id,
            ptaNickname=row.pta_nickname,
            createdAt=row.created_at,
            updatedAt=row.updated_at,
            lastLoginAt=row.last_login_at,
        )
        for row in rows
    ]
    return AdminAccountsResponse(items=items, total=len(items))


@router.get("/audit-log", response_model=AdminAuditLogResponse)
async def list_admin_audit_log(
    request: Request,
    limit: int = Query(default=100, ge=1, le=500),
    _admin=Depends(get_admin_account),
    repository=Depends(get_repository),
) -> AdminAuditLogResponse:
    items: list[AdminAuditLogItem] = []
    memory_events = getattr(request.app.state, "admin_audit_events", [])
    if isinstance(memory_events, list):
        for event in memory_events:
            if not isinstance(event, dict):
                continue
            created_at = event.get("createdAt")
            if not isinstance(created_at, datetime):
                created_at = datetime.now(timezone.utc)
            payload = event.get("payload")
            items.append(
                AdminAuditLogItem(
                    id=str(event.get("id", "")),
                    kind=str(event.get("kind", "event")),
                    status=str(event.get("status", "info")),
                    title=str(event.get("title", "事件")),
                    detail=str(event.get("detail", "")),
                    createdAt=created_at,
                    payload=payload if isinstance(payload, dict) else {},
                )
            )

    fetcher = getattr(repository, "fetch_admin_audit_logs", None)
    if callable(fetcher):
        rows = await fetcher(limit=limit)
        items.extend(
            AdminAuditLogItem(
                id=row.row_id,
                kind=row.kind,
                status=row.status,
                title=row.title,
                detail=row.detail,
                createdAt=row.created_at,
                payload=row.payload,
            )
            for row in rows
        )

    items.sort(key=lambda item: item.createdAt, reverse=True)
    items = items[:limit]
    return AdminAuditLogResponse(items=items, total=len(items))
