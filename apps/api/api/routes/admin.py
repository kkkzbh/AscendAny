from __future__ import annotations

import os
import re
import time
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

import httpx
import yaml
from fastapi import APIRouter, Depends, Query, Request

from ..deps import get_admin_account, get_repository
from ...core.config import LLMProviderConfig
from ...core.errors import AppError
from ...schemas.admin import (
    AdminAccountSummary,
    AdminAccountsResponse,
    AdminAuditLogItem,
    AdminAuditLogResponse,
    AdminConfigPatch,
    AdminConfigResponse,
    AdminDeepSeekModelsRequest,
    AdminDeepSeekModelsResponse,
    AdminFusionHalfLifeConfig,
    AdminMappingConfig,
    AdminModelConfigPatch,
    AdminModelConfigResponse,
    AdminModelConnectionTestRequest,
    AdminModelConnectionTestResponse,
    AdminModelOption,
    AdminModelProviderId,
    AdminModelProviderConfig,
    AdminMetricsConfig,
    AdminPreprocessConfig,
    AdminRatingConfig,
    AdminModelServerDefault,
    AdminStudentExamReport,
    AdminStudentExamReportsResponse,
    AdminStudentListResponse,
    AdminStudentSummary,
    AdminWarmupConfig,
)
from ...services.llm_providers.registry import (
    PROVIDER_DEFINITIONS,
    PROVIDER_ORDER,
    build_provider_profile,
    get_adapter,
    model_option_request_mode,
    normalize_adapter,
    normalize_model,
    normalize_request_mode,
    provider_config_to_raw,
    transport_model,
)
from ...services.llm_providers.types import ProviderModelOption

router = APIRouter(tags=["admin"], prefix="/admin")

_PROJECT_ROOT = Path(__file__).resolve().parents[4]
_PREPROCESS_CONFIG_ENV = "ASCENDANY_PREPROCESS_CONFIG"
_API_CONFIG_ENV = "ASCENDANY_API_CONFIG"
_ADMIN_ENV_FILE_ENV = "ASCENDANY_ADMIN_ENV_FILE"
_AUDIT_LIMIT = 200
_ENV_NAME_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def _resolve_api_config_path() -> Path:
    raw_path = os.getenv(_API_CONFIG_ENV, "").strip()
    candidate = (
        Path(raw_path) if raw_path else _PROJECT_ROOT / "apps/api/config/default.yaml"
    )
    if not candidate.is_absolute():
        candidate = (_PROJECT_ROOT / candidate).resolve()
    return candidate


def _resolve_admin_env_file_path() -> Path:
    raw_path = os.getenv(_ADMIN_ENV_FILE_ENV, "").strip()
    candidate = Path(raw_path) if raw_path else _PROJECT_ROOT / ".env.local"
    if not candidate.is_absolute():
        candidate = (_PROJECT_ROOT / candidate).resolve()
    return candidate


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


def _validate_url(value: str, field: str = "Base URL") -> str:
    normalized = value.strip().rstrip("/")
    parsed = urlparse(normalized)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message=f"{field} must be an http(s) URL.",
        )
    return normalized


def _validate_env_name(value: str) -> str:
    normalized = value.strip()
    if not _ENV_NAME_RE.fullmatch(normalized):
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="API Key Env must be a valid environment variable name.",
        )
    return normalized


def _admin_model_options(options: list[ProviderModelOption]) -> list[AdminModelOption]:
    return [
        AdminModelOption(
            modelId=option.model_id,
            label=option.label,
            requestMode=option.request_mode,
            deprecated=option.deprecated,
            disabled=option.disabled,
            disabledReason=option.disabled_reason,
        )
        for option in options
    ]


def _coerce_provider_id(
    value: object,
    default: AdminModelProviderId = "deepseek",
) -> AdminModelProviderId:
    raw = str(value or "").strip()
    if raw in PROVIDER_ORDER:
        return raw  # type: ignore[return-value]
    return default


def _default_provider_state() -> dict[AdminModelProviderId, LLMProviderConfig]:
    return {
        provider_id: LLMProviderConfig(
            adapter=definition.adapter,
            base_url=definition.default_base_url,
            model=definition.default_model,
            api_key_env=definition.default_api_key_env,
            request_mode=definition.default_request_mode,
        )
        for provider_id, definition in PROVIDER_DEFINITIONS.items()
    }  # type: ignore[return-value]


def _load_model_config_from_raw(
    raw: dict[str, Any],
) -> tuple[AdminModelProviderId, dict[AdminModelProviderId, LLMProviderConfig]]:
    llm = raw.get("llm", {}) or {}
    if not isinstance(llm, dict):
        llm = {}
    providers = _default_provider_state()
    raw_providers = llm.get("providers", {}) or {}
    if isinstance(raw_providers, dict):
        for provider_id in PROVIDER_ORDER:
            raw_provider = raw_providers.get(provider_id, {}) or {}
            if not isinstance(raw_provider, dict):
                continue
            baseline = providers[provider_id]  # type: ignore[index]
            providers[provider_id] = LLMProviderConfig(
                adapter=str(raw_provider.get("adapter", baseline.adapter)),
                base_url=str(raw_provider.get("base_url", baseline.base_url)),
                model=str(raw_provider.get("model", baseline.model)),
                api_key_env=str(
                    raw_provider.get("api_key_env", baseline.api_key_env)
                ),
                request_mode=str(
                    raw_provider.get("request_mode", baseline.request_mode)
                ),
            )
    active_provider = _coerce_provider_id(llm.get("active_provider"))
    return active_provider, providers


def _build_model_config_response_from_raw(raw: dict[str, Any]) -> AdminModelConfigResponse:
    active_provider, providers = _load_model_config_from_raw(raw)
    settings = getattr(_build_model_config_response_from_raw, "_settings", None)
    provider_items: list[AdminModelProviderConfig] = []
    for provider_id in PROVIDER_ORDER:
        definition = PROVIDER_DEFINITIONS[provider_id]
        provider = providers[provider_id]  # type: ignore[index]
        model = provider.model.strip()
        try:
            transport = transport_model(provider_id, normalize_model(provider_id, model))
        except AppError:
            transport = model
        provider_items.append(
            AdminModelProviderConfig(
                id=provider_id,  # type: ignore[arg-type]
                title=definition.title,
                provider=definition.provider,
                strategyId=definition.strategy_id,
                adapter=provider.adapter,
                baseUrl=provider.base_url,
                model=model,
                transportModel=transport,
                apiKeyEnv=provider.api_key_env.strip(),
                apiKeyConfigured=bool(os.getenv(provider.api_key_env.strip(), "").strip()),
                active=provider_id == active_provider,
                requestMode=normalize_request_mode(provider.request_mode),
                modelOptions=_admin_model_options(definition.model_options),
                description=definition.description,
                modelHint=definition.model_hint,
            )
        )

    active = providers[active_provider]
    active_runtime = AdminModelServerDefault(
        mode=active.adapter,
        baseUrl=active.base_url,
        model=transport_model(active_provider, normalize_model(active_provider, active.model)),
        apiKeyEnv=active.api_key_env,
    )
    _ = settings
    return AdminModelConfigResponse(
        configPath=str(_resolve_api_config_path()),
        envFilePath=str(_resolve_admin_env_file_path()),
        activeProvider=active_provider,
        providers=provider_items,
        activeRuntime=active_runtime,
    )


def _shell_quote(value: str) -> str:
    return "'" + value.replace("'", "'\"'\"'") + "'"


def _write_env_value(path: Path, key: str, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = path.read_text(encoding="utf-8").splitlines() if path.exists() else []
    pattern = re.compile(rf"^\s*(?:export\s+)?{re.escape(key)}\s*=")
    new_line = f"{key}={_shell_quote(value)}"
    replaced = False
    updated: list[str] = []
    for line in lines:
        if pattern.match(line):
            if not replaced:
                updated.append(new_line)
                replaced = True
            continue
        updated.append(line)
    if not replaced:
        if updated and updated[-1].strip():
            updated.append("")
        updated.append(new_line)
    path.write_text("\n".join(updated) + "\n", encoding="utf-8")


def _patch_runtime_model_settings(
    request: Request,
    active_provider: AdminModelProviderId,
    providers: dict[AdminModelProviderId, LLMProviderConfig],
) -> None:
    settings = getattr(request.app.state, "settings", None)
    if settings is None:
        return
    settings.llm.active_provider = active_provider
    settings.llm.providers = dict(providers)
    llm_service = getattr(request.app.state, "llm_service", None)
    if hasattr(llm_service, "_settings"):
        llm_service._settings = settings


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


@router.get("/model-config", response_model=AdminModelConfigResponse)
async def get_admin_model_config(
    _admin=Depends(get_admin_account),
) -> AdminModelConfigResponse:
    return _build_model_config_response_from_raw(_read_yaml(_resolve_api_config_path()))


@router.patch("/model-config", response_model=AdminModelConfigResponse)
async def patch_admin_model_config(
    payload: AdminModelConfigPatch,
    request: Request,
    _admin=Depends(get_admin_account),
) -> AdminModelConfigResponse:
    config_path = _resolve_api_config_path()
    raw = _read_yaml(config_path)
    active_provider, providers = _load_model_config_from_raw(raw)

    if payload.activeProvider is not None:
        active_provider = payload.activeProvider

    api_key_changed = False
    changed_provider_id: AdminModelProviderId | None = None
    if payload.provider is not None:
        provider_patch = payload.provider
        changed_provider_id = provider_patch.id
        current = providers[provider_patch.id]
        base_url = current.base_url
        model = current.model
        api_key_env = current.api_key_env
        adapter = current.adapter
        request_mode = current.request_mode
        if provider_patch.baseUrl is not None:
            base_url = _validate_url(provider_patch.baseUrl)
        if provider_patch.model is not None:
            model = normalize_model(provider_patch.id, provider_patch.model)
        else:
            model = normalize_model(provider_patch.id, model)
        if provider_patch.apiKeyEnv is not None:
            api_key_env = _validate_env_name(provider_patch.apiKeyEnv)
        else:
            api_key_env = _validate_env_name(api_key_env)
        if provider_patch.requestMode is not None:
            request_mode = provider_patch.requestMode
        else:
            request_mode = model_option_request_mode(provider_patch.id, model)
        if provider_patch.adapter is not None:
            adapter = normalize_adapter(provider_patch.adapter)
        elif request_mode == "responses":
            adapter = "responses"

        api_key = (
            provider_patch.apiKey.strip() if provider_patch.apiKey is not None else ""
        )
        if api_key:
            _write_env_value(
                _resolve_admin_env_file_path(),
                api_key_env,
                api_key,
            )
            os.environ[api_key_env] = api_key
            api_key_changed = True
        providers[provider_patch.id] = LLMProviderConfig(
            adapter=adapter,
            base_url=base_url,
            model=model,
            api_key_env=api_key_env,
            request_mode=request_mode,
        )

    active = providers[active_provider]
    providers[active_provider] = LLMProviderConfig(
        adapter=normalize_adapter(active.adapter),
        base_url=_validate_url(active.base_url),
        model=normalize_model(active_provider, active.model),
        api_key_env=_validate_env_name(active.api_key_env),
        request_mode=normalize_request_mode(active.request_mode),
    )

    llm = raw.setdefault("llm", {})
    if not isinstance(llm, dict):
        llm = {}
        raw["llm"] = llm
    raw_providers = {}
    for provider_id in PROVIDER_ORDER:
        raw_providers[provider_id] = provider_config_to_raw(providers[provider_id])  # type: ignore[index]
    llm["active_provider"] = active_provider
    llm["providers"] = raw_providers
    llm.pop("active_tab", None)
    llm.pop("tabs", None)
    llm.pop("server_default", None)
    _write_yaml(config_path, raw)
    _patch_runtime_model_settings(request, active_provider, providers)

    active = providers[active_provider]
    _remember_audit_event(
        request,
        kind="model_config_update",
        status="success",
        title="模型配置已保存",
        detail=f"当前 Provider：{PROVIDER_DEFINITIONS[active_provider].title}",
        payload={
            "activeProvider": active_provider,
            "changedProvider": changed_provider_id,
            "baseUrl": active.base_url,
            "model": active.model,
            "apiKeyEnv": active.api_key_env,
            "requestMode": active.request_mode,
            "apiKeyChanged": api_key_changed,
        },
    )
    return _build_model_config_response_from_raw(raw)


def _profile_from_admin_payload(
    provider_id: AdminModelProviderId,
    *,
    base_url: str,
    model: str,
    api_key_env: str,
    request_mode: str | None,
    adapter: str | None,
    api_key: str | None,
):
    settings = getattr(_profile_from_admin_payload, "_settings", None)
    _ = settings
    definition = PROVIDER_DEFINITIONS[provider_id]
    normalized_model = normalize_model(provider_id, model)
    normalized_request_mode = (
        normalize_request_mode(request_mode)
        if request_mode
        else model_option_request_mode(provider_id, normalized_model)
    )
    normalized_adapter = normalize_adapter(adapter or definition.adapter)
    if normalized_request_mode == "responses":
        normalized_adapter = "responses"
    temp_settings = type("_TempSettings", (), {})()
    temp_llm = type("_TempLLM", (), {})()
    temp_settings.llm = temp_llm
    temp_llm.active_provider = provider_id
    temp_llm.providers = {
        provider_id: LLMProviderConfig(
            adapter=normalized_adapter,
            base_url=_validate_url(base_url),
            model=normalized_model,
            api_key_env=_validate_env_name(api_key_env),
            request_mode=normalized_request_mode,
        )
    }
    return build_provider_profile(temp_settings, provider_id, api_key_override=api_key)  # type: ignore[arg-type]


@router.post("/model-config/test", response_model=AdminModelConnectionTestResponse)
async def test_admin_model_config(
    payload: AdminModelConnectionTestRequest,
    request: Request,
    _admin=Depends(get_admin_account),
) -> AdminModelConnectionTestResponse:
    profile = _profile_from_admin_payload(
        payload.providerId,
        base_url=payload.baseUrl,
        model=payload.model,
        api_key_env=payload.apiKeyEnv,
        request_mode=payload.requestMode,
        adapter=payload.adapter,
        api_key=payload.apiKey,
    )
    async with httpx.AsyncClient(timeout=15) as client:
        result = await get_adapter(profile.adapter).test_connection(profile, client)
    _remember_audit_event(
        request,
        kind="model_connection_test",
        status="success" if result.ok else "failed",
        title="模型连接测试成功" if result.ok else "模型连接测试失败",
        detail=f"{profile.title}：{result.message}",
        payload={"providerId": payload.providerId, "model": profile.model},
    )
    return AdminModelConnectionTestResponse(
        ok=result.ok,
        status=result.status,
        message=result.message,
        provider=profile.title,
        model=profile.transport_model,
        elapsedMs=result.elapsed_ms,
    )


def _static_deepseek_model_response(error: str | None = None) -> AdminDeepSeekModelsResponse:
    return AdminDeepSeekModelsResponse(
        models=_admin_model_options(PROVIDER_DEFINITIONS["deepseek"].model_options),
        source="static",
        error=error,
    )


@router.post(
    "/model-config/{provider_id}/models",
    response_model=AdminDeepSeekModelsResponse,
)
async def list_admin_provider_models(
    provider_id: AdminModelProviderId,
    payload: AdminDeepSeekModelsRequest,
    _admin=Depends(get_admin_account),
) -> AdminDeepSeekModelsResponse:
    effective_provider_id = provider_id
    profile = _profile_from_admin_payload(
        effective_provider_id,
        base_url=payload.baseUrl,
        model=payload.model or PROVIDER_DEFINITIONS[effective_provider_id].default_model,
        api_key_env=payload.apiKeyEnv,
        request_mode=payload.requestMode,
        adapter=payload.adapter,
        api_key=payload.apiKey,
    )
    async with httpx.AsyncClient(timeout=15) as client:
        result = await get_adapter(profile.adapter).list_models(profile, client)
    return AdminDeepSeekModelsResponse(
        models=_admin_model_options(result.models),
        source=result.source,
        error=result.error,
    )


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
