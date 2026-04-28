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
from ...core.config import LLMModelTabConfig, LLMServerDefaultConfig
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
    AdminModelTabId,
    AdminModelTabConfig,
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

router = APIRouter(tags=["admin"], prefix="/admin")

_PROJECT_ROOT = Path(__file__).resolve().parents[4]
_PREPROCESS_CONFIG_ENV = "ASCENDANY_PREPROCESS_CONFIG"
_API_CONFIG_ENV = "ASCENDANY_API_CONFIG"
_ADMIN_ENV_FILE_ENV = "ASCENDANY_ADMIN_ENV_FILE"
_AUDIT_LIMIT = 200
_MODEL_TAB_ORDER: tuple[AdminModelTabId, ...] = (
    "siliconflow",
    "openai",
    "copilot",
    "deepseek",
)
_ENV_NAME_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
_OPENAI_GPT54_MODEL_OPTIONS = [
    ("openai/gpt-5.4", "GPT-5.4 (default thinking)"),
    ("openai/gpt-5.4-non-thinking", "GPT-5.4 (non-thinking)"),
    ("openai/gpt-5.4-minimal-thinking", "GPT-5.4 (minimal thinking)"),
    ("openai/gpt-5.4-low-thinking", "GPT-5.4 (low thinking)"),
    ("openai/gpt-5.4-medium-thinking", "GPT-5.4 (medium thinking)"),
    ("openai/gpt-5.4-high-thinking", "GPT-5.4 (high thinking)"),
    ("openai/gpt-5.4-xhigh-thinking", "GPT-5.4 (xhigh thinking)"),
    ("openai/gpt-5.4-thinking", "GPT-5.4 (thinking)"),
]
_COPILOT_MODEL_OPTIONS = [
    ("gpt-5.4", "GPT-5.4 (1x)", "responses", False),
    ("gpt-5.4-mini", "GPT-5.4 mini (0.33x)", "responses", False),
    ("gpt-5-mini", "GPT-5 mini (0x)", "chat_completions", False),
    ("gpt-4.1", "GPT-4.1 (0x)", "chat_completions", False),
    ("gpt-4o", "GPT-4o (0x, 可能弃用)", "chat_completions", True),
    ("claude-haiku-4.5", "Claude Haiku 4.5 (0.33x)", "chat_completions", False),
    ("claude-sonnet-4.5", "Claude Sonnet 4.5 (1x)", "chat_completions", False),
    ("gemini-3-flash-preview", "Gemini 3 Flash (0.33x)", "chat_completions", False),
    ("gemini-3.1-pro-preview", "Gemini 3.1 Pro (1x)", "chat_completions", False),
]
_DEEPSEEK_MODEL_OPTIONS = [
    ("deepseek-v4-flash", "deepseek-v4-flash", False),
    ("deepseek-v4-pro", "deepseek-v4-pro", False),
    ("deepseek-chat", "deepseek-chat (deprecated 2026-07-24)", True),
    ("deepseek-reasoner", "deepseek-reasoner (deprecated 2026-07-24)", True),
]
_MODEL_TAB_DEFAULTS: dict[AdminModelTabId, dict[str, str]] = {
    "siliconflow": {
        "title": "硅基流动",
        "provider": "siliconflow",
        "strategyId": "siliconflow-kimi-main-chat",
        "baseUrl": "https://api.siliconflow.cn/v1",
        "model": "Pro/moonshotai/Kimi-K2.5",
        "apiKeyEnv": "ASCENDANY_LLM_SILICONFLOW_API_KEY",
        "description": "固定走硅基流动 OpenAI 兼容接口，默认使用 Kimi-K2.5。",
        "modelHint": "当前仅支持 Pro/moonshotai/Kimi-K2.5。",
    },
    "openai": {
        "title": "OpenAI",
        "provider": "openai",
        "strategyId": "openai-gpt54-main-chat",
        "baseUrl": "https://shell.wyzai.top/v1",
        "model": "openai/gpt-5.4-medium-thinking",
        "apiKeyEnv": "ASCENDANY_LLM_OPENAI_API_KEY",
        "description": "按 OpenAI 兼容接口处理，默认预填 wyzai 与 GPT-5.4 medium thinking。",
        "modelHint": "推荐 openai/gpt-5.4-medium-thinking；运行时发送不带 openai/ 前缀的模型 ID。",
    },
    "copilot": {
        "title": "GitHub Copilot",
        "provider": "openai",
        "strategyId": "copilot-github-oauth-main-chat",
        "baseUrl": "http://127.0.0.1:5140/api/internal/copilot/v1",
        "model": "openai/gpt-5-mini",
        "apiKeyEnv": "ASCENDANY_LLM_COPILOT_API_KEY",
        "description": "通过本地 Copilot bridge 暴露的 OpenAI 兼容接口接入。",
        "modelHint": "第一版只启用 chat/completions 模型；Responses-only 模型暂不可选。",
    },
    "deepseek": {
        "title": "DeepSeek",
        "provider": "deepseek",
        "strategyId": "deepseek-official-main-chat",
        "baseUrl": "https://api.deepseek.com",
        "model": "deepseek-v4-flash",
        "apiKeyEnv": "ASCENDANY_LLM_DEEPSEEK_API_KEY",
        "description": "按 DeepSeek 官方 OpenAI 兼容接口接入，模型列表优先从官方 /models 刷新。",
        "modelHint": "运行时发送 DeepSeek 官方原始模型 ID。",
    },
}


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


def _normalize_siliconflow_model(model: str) -> str:
    value = model.strip()
    if re.fullmatch(r"siliconflow/pro/moonshotai/kimi-k2\.5", value, re.I):
        return "Pro/moonshotai/Kimi-K2.5"
    if re.fullmatch(r"pro/moonshotai/kimi-k2\.5", value, re.I):
        return "Pro/moonshotai/Kimi-K2.5"
    raise AppError(
        status_code=422,
        code="INVALID_MODEL_CONFIG",
        message="硅基流动 Tab 仅支持 Pro/moonshotai/Kimi-K2.5。",
    )


def _normalize_openai_model(model: str) -> str:
    value = model.strip()
    if not value:
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="OpenAI Tab 默认模型不能为空。",
        )
    if "/" not in value:
        value = f"openai/{value}"
    if not value.startswith("openai/"):
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="OpenAI Tab 仅支持 openai/gpt-5.4 系列。",
        )
    raw_model = value.removeprefix("openai/")
    if not re.fullmatch(
        r"gpt-5\.4(?:-(?:non|minimal|low|medium|high|xhigh)-thinking|-thinking)?",
        raw_model,
        re.I,
    ):
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="OpenAI Tab 仅支持 openai/gpt-5.4 系列。",
        )
    return f"openai/{raw_model}"


def _normalize_copilot_model_id(model: str) -> str:
    value = model.strip()
    if value.startswith("openai/"):
        value = value.removeprefix("openai/").strip()
    elif value.startswith("github-copilot/"):
        value = value.removeprefix("github-copilot/").strip()
    return value


def _normalize_copilot_model(model: str) -> str:
    model_id = _normalize_copilot_model_id(model)
    option = next(
        (item for item in _COPILOT_MODEL_OPTIONS if item[0] == model_id),
        None,
    )
    if option is None:
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="GitHub Copilot Tab 只能从内置 Copilot 模型列表选择。",
        )
    if option[2] != "chat_completions":
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="AscendAny 第一版暂不支持 Copilot Responses-only 模型。",
        )
    return f"openai/{model_id}"


def _normalize_deepseek_model_id(model: str) -> str:
    value = model.strip()
    if value.startswith("deepseek/"):
        value = value.removeprefix("deepseek/").strip()
    if not value or "/" in value or not re.fullmatch(r"[A-Za-z0-9_.:-]+", value):
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="DeepSeek Tab 模型必须是官方原始模型 ID。",
        )
    return value


def _normalize_model(tab_id: AdminModelTabId, model: str) -> str:
    if tab_id == "siliconflow":
        return _normalize_siliconflow_model(model)
    if tab_id == "openai":
        return _normalize_openai_model(model)
    if tab_id == "copilot":
        return _normalize_copilot_model(model)
    return _normalize_deepseek_model_id(model)


def _transport_model(tab_id: AdminModelTabId, model: str) -> str:
    if tab_id in {"openai", "copilot"}:
        if model.startswith("openai/"):
            return model.removeprefix("openai/").strip()
        if model.startswith("github-copilot/"):
            return model.removeprefix("github-copilot/").strip()
    if tab_id == "deepseek":
        return _normalize_deepseek_model_id(model)
    return model.strip()


def _model_options(tab_id: AdminModelTabId) -> list[AdminModelOption]:
    if tab_id == "siliconflow":
        return [
            AdminModelOption(
                modelId="Pro/moonshotai/Kimi-K2.5",
                label="Pro/moonshotai/Kimi-K2.5",
            )
        ]
    if tab_id == "openai":
        return [
            AdminModelOption(modelId=model_id, label=label)
            for model_id, label in _OPENAI_GPT54_MODEL_OPTIONS
        ]
    if tab_id == "copilot":
        return [
            AdminModelOption(
                modelId=f"openai/{model_id}",
                label=label,
                requestMode=request_mode,  # type: ignore[arg-type]
                deprecated=deprecated,
                disabled=request_mode != "chat_completions",
                disabledReason=(
                    "AscendAny 第一版暂不支持 Responses API"
                    if request_mode != "chat_completions"
                    else None
                ),
            )
            for model_id, label, request_mode, deprecated in _COPILOT_MODEL_OPTIONS
        ]
    return [
        AdminModelOption(modelId=model_id, label=label, deprecated=deprecated)
        for model_id, label, deprecated in _DEEPSEEK_MODEL_OPTIONS
    ]


def _infer_active_tab_from_server_default(server_default: dict[str, Any]) -> AdminModelTabId:
    base_url = str(server_default.get("base_url", "")).lower()
    model = str(server_default.get("model", "")).lower()
    if "siliconflow" in base_url or "kimi-k2.5" in model:
        return "siliconflow"
    if "copilot" in base_url:
        return "copilot"
    if "deepseek" in base_url or model.startswith("deepseek"):
        return "deepseek"
    return "openai"


def _coerce_tab_id(value: object, default: AdminModelTabId = "deepseek") -> AdminModelTabId:
    raw = str(value or "").strip()
    if raw in _MODEL_TAB_ORDER:
        return raw  # type: ignore[return-value]
    return default


def _default_tab_state() -> dict[AdminModelTabId, dict[str, str]]:
    return {
        tab_id: {
            "baseUrl": defaults["baseUrl"],
            "model": defaults["model"],
            "apiKeyEnv": defaults["apiKeyEnv"],
        }
        for tab_id, defaults in _MODEL_TAB_DEFAULTS.items()
    }


def _load_model_config_from_raw(
    raw: dict[str, Any],
) -> tuple[AdminModelTabId, dict[AdminModelTabId, dict[str, str]]]:
    llm = raw.get("llm", {}) or {}
    if not isinstance(llm, dict):
        llm = {}

    tabs = _default_tab_state()
    raw_tabs = llm.get("tabs", {}) or {}
    has_tabs = isinstance(raw_tabs, dict) and bool(raw_tabs)
    if isinstance(raw_tabs, dict):
        for tab_id in _MODEL_TAB_ORDER:
            raw_tab = raw_tabs.get(tab_id, {}) or {}
            if not isinstance(raw_tab, dict):
                continue
            tabs[tab_id] = {
                "baseUrl": str(raw_tab.get("base_url", tabs[tab_id]["baseUrl"])),
                "model": str(raw_tab.get("model", tabs[tab_id]["model"])),
                "apiKeyEnv": str(
                    raw_tab.get("api_key_env", tabs[tab_id]["apiKeyEnv"])
                ),
            }

    server_default = llm.get("server_default", {}) or {}
    if not isinstance(server_default, dict):
        server_default = {}
    active_tab = _coerce_tab_id(llm.get("active_tab"))
    if not has_tabs and server_default:
        active_tab = _infer_active_tab_from_server_default(server_default)
        tabs[active_tab] = {
            "baseUrl": str(server_default.get("base_url", tabs[active_tab]["baseUrl"])),
            "model": str(server_default.get("model", tabs[active_tab]["model"])),
            "apiKeyEnv": str(
                server_default.get("api_key_env", tabs[active_tab]["apiKeyEnv"])
            ),
        }
    return active_tab, tabs


def _build_model_config_response_from_raw(raw: dict[str, Any]) -> AdminModelConfigResponse:
    active_tab, tabs = _load_model_config_from_raw(raw)
    tab_items: list[AdminModelTabConfig] = []
    for tab_id in _MODEL_TAB_ORDER:
        defaults = _MODEL_TAB_DEFAULTS[tab_id]
        tab = tabs[tab_id]
        api_key_env = tab["apiKeyEnv"].strip()
        model = tab["model"].strip()
        try:
            transport_model = _transport_model(tab_id, model)
        except AppError:
            transport_model = model
        tab_items.append(
            AdminModelTabConfig(
                id=tab_id,
                title=defaults["title"],
                provider=defaults["provider"],
                strategyId=defaults["strategyId"],
                baseUrl=tab["baseUrl"],
                model=model,
                transportModel=transport_model,
                apiKeyEnv=api_key_env,
                apiKeyConfigured=bool(os.getenv(api_key_env, "").strip()),
                active=tab_id == active_tab,
                requestMode="chat_completions",
                modelOptions=_model_options(tab_id),
                description=defaults["description"],
                modelHint=defaults["modelHint"],
            )
        )

    active_config = tabs[active_tab]
    try:
        active_transport_model = _transport_model(active_tab, active_config["model"])
    except AppError:
        active_transport_model = active_config["model"].strip()
    server_default = AdminModelServerDefault(
        mode="openai_compatible",
        baseUrl=active_config["baseUrl"],
        model=active_transport_model,
        apiKeyEnv=active_config["apiKeyEnv"],
    )
    return AdminModelConfigResponse(
        configPath=str(_resolve_api_config_path()),
        envFilePath=str(_resolve_admin_env_file_path()),
        activeTab=active_tab,
        tabs=tab_items,
        serverDefault=server_default,
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
    active_tab: AdminModelTabId,
    tabs: dict[AdminModelTabId, dict[str, str]],
) -> None:
    settings = getattr(request.app.state, "settings", None)
    if settings is None:
        return
    settings.llm.active_tab = active_tab
    for tab_id, tab in tabs.items():
        settings.llm.tabs[tab_id] = LLMModelTabConfig(
            base_url=tab["baseUrl"],
            model=tab["model"],
            api_key_env=tab["apiKeyEnv"],
        )
    active = tabs[active_tab]
    settings.llm.server_default = LLMServerDefaultConfig(
        mode="openai_compatible",
        base_url=active["baseUrl"],
        model=_transport_model(active_tab, active["model"]),
        api_key_env=active["apiKeyEnv"],
    )
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
    active_tab, tabs = _load_model_config_from_raw(raw)

    if payload.activeTab is not None:
        active_tab = payload.activeTab

    api_key_changed = False
    changed_tab_id: AdminModelTabId | None = None
    if payload.tab is not None:
        tab_patch = payload.tab
        changed_tab_id = tab_patch.id
        current = dict(tabs[tab_patch.id])
        if tab_patch.baseUrl is not None:
            current["baseUrl"] = _validate_url(tab_patch.baseUrl)
        if tab_patch.model is not None:
            current["model"] = _normalize_model(tab_patch.id, tab_patch.model)
        else:
            current["model"] = _normalize_model(tab_patch.id, current["model"])
        if tab_patch.apiKeyEnv is not None:
            current["apiKeyEnv"] = _validate_env_name(tab_patch.apiKeyEnv)
        else:
            current["apiKeyEnv"] = _validate_env_name(current["apiKeyEnv"])

        api_key = tab_patch.apiKey.strip() if tab_patch.apiKey is not None else ""
        if api_key:
            _write_env_value(
                _resolve_admin_env_file_path(),
                current["apiKeyEnv"],
                api_key,
            )
            os.environ[current["apiKeyEnv"]] = api_key
            api_key_changed = True
        tabs[tab_patch.id] = current

    active = tabs[active_tab]
    active["baseUrl"] = _validate_url(active["baseUrl"])
    active["model"] = _normalize_model(active_tab, active["model"])
    active["apiKeyEnv"] = _validate_env_name(active["apiKeyEnv"])
    tabs[active_tab] = active

    llm = raw.setdefault("llm", {})
    if not isinstance(llm, dict):
        llm = {}
        raw["llm"] = llm
    raw_tabs = llm.setdefault("tabs", {})
    if not isinstance(raw_tabs, dict):
        raw_tabs = {}
        llm["tabs"] = raw_tabs
    for tab_id in _MODEL_TAB_ORDER:
        tab = tabs[tab_id]
        raw_tabs[tab_id] = {
            "base_url": tab["baseUrl"],
            "model": tab["model"],
            "api_key_env": tab["apiKeyEnv"],
        }
    llm["active_tab"] = active_tab
    llm["server_default"] = {
        "mode": "openai_compatible",
        "base_url": active["baseUrl"],
        "model": _transport_model(active_tab, active["model"]),
        "api_key_env": active["apiKeyEnv"],
    }
    _write_yaml(config_path, raw)
    _patch_runtime_model_settings(request, active_tab, tabs)

    _remember_audit_event(
        request,
        kind="model_config_update",
        status="success",
        title="模型配置已保存",
        detail=f"当前模型 Tab：{_MODEL_TAB_DEFAULTS[active_tab]['title']}",
        payload={
            "activeTab": active_tab,
            "changedTab": changed_tab_id,
            "baseUrl": active["baseUrl"],
            "model": active["model"],
            "apiKeyEnv": active["apiKeyEnv"],
            "apiKeyChanged": api_key_changed,
        },
    )
    return _build_model_config_response_from_raw(raw)


@router.post("/model-config/test", response_model=AdminModelConnectionTestResponse)
async def test_admin_model_config(
    payload: AdminModelConnectionTestRequest,
    request: Request,
    _admin=Depends(get_admin_account),
) -> AdminModelConnectionTestResponse:
    base_url = _validate_url(payload.baseUrl)
    api_key_env = _validate_env_name(payload.apiKeyEnv)
    canonical_model = _normalize_model(payload.tabId, payload.model)
    model = _transport_model(payload.tabId, canonical_model)
    api_key = (payload.apiKey or "").strip() or os.getenv(api_key_env, "").strip()
    started = time.perf_counter()
    if not api_key:
        elapsed_ms = int((time.perf_counter() - started) * 1000)
        return AdminModelConnectionTestResponse(
            ok=False,
            status="missing_key",
            message=f"未配置 API Key：{api_key_env}",
            provider=_MODEL_TAB_DEFAULTS[payload.tabId]["title"],
            model=model,
            elapsedMs=elapsed_ms,
        )

    url = f"{base_url}/chat/completions"
    try:
        async with httpx.AsyncClient(timeout=15) as client:
            response = await client.post(
                url,
                headers={
                    "Authorization": f"Bearer {api_key}",
                    "Content-Type": "application/json",
                },
                json={
                    "model": model,
                    "messages": [
                        {"role": "system", "content": "Connection test."},
                        {"role": "user", "content": "Reply with ok."},
                    ],
                    "temperature": 0,
                    "max_tokens": 8,
                },
            )
        elapsed_ms = int((time.perf_counter() - started) * 1000)
        if response.status_code >= 400:
            message = response.text.strip()[:500] or f"HTTP {response.status_code}"
            _remember_audit_event(
                request,
                kind="model_connection_test",
                status="failed",
                title="模型连接测试失败",
                detail=f"{_MODEL_TAB_DEFAULTS[payload.tabId]['title']} 返回 {response.status_code}",
                payload={"tabId": payload.tabId, "model": canonical_model},
            )
            return AdminModelConnectionTestResponse(
                ok=False,
                status="upstream_error",
                message=message,
                provider=_MODEL_TAB_DEFAULTS[payload.tabId]["title"],
                model=model,
                elapsedMs=elapsed_ms,
            )
        _remember_audit_event(
            request,
            kind="model_connection_test",
            status="success",
            title="模型连接测试成功",
            detail=f"{_MODEL_TAB_DEFAULTS[payload.tabId]['title']} 可用",
            payload={"tabId": payload.tabId, "model": canonical_model},
        )
        return AdminModelConnectionTestResponse(
            ok=True,
            status="success",
            message="连接成功",
            provider=_MODEL_TAB_DEFAULTS[payload.tabId]["title"],
            model=model,
            elapsedMs=elapsed_ms,
        )
    except httpx.HTTPError as exc:
        elapsed_ms = int((time.perf_counter() - started) * 1000)
        _remember_audit_event(
            request,
            kind="model_connection_test",
            status="failed",
            title="模型连接测试失败",
            detail=str(exc),
            payload={"tabId": payload.tabId, "model": canonical_model},
        )
        return AdminModelConnectionTestResponse(
            ok=False,
            status="request_failed",
            message=str(exc),
            provider=_MODEL_TAB_DEFAULTS[payload.tabId]["title"],
            model=model,
            elapsedMs=elapsed_ms,
        )


def _static_deepseek_model_response(error: str | None = None) -> AdminDeepSeekModelsResponse:
    return AdminDeepSeekModelsResponse(
        models=_model_options("deepseek"),
        source="static",
        error=error,
    )


@router.post(
    "/model-config/deepseek/models",
    response_model=AdminDeepSeekModelsResponse,
)
async def list_admin_deepseek_models(
    payload: AdminDeepSeekModelsRequest,
    _admin=Depends(get_admin_account),
) -> AdminDeepSeekModelsResponse:
    base_url = _validate_url(payload.baseUrl)
    api_key_env = _validate_env_name(payload.apiKeyEnv)
    api_key = (payload.apiKey or "").strip() or os.getenv(api_key_env, "").strip()
    if not api_key:
        return _static_deepseek_model_response(f"未配置 API Key：{api_key_env}")

    try:
        async with httpx.AsyncClient(timeout=15) as client:
            response = await client.get(
                f"{base_url}/models",
                headers={"Authorization": f"Bearer {api_key}"},
            )
        if response.status_code >= 400:
            return _static_deepseek_model_response(
                response.text.strip()[:500] or f"HTTP {response.status_code}"
            )
        data = response.json()
    except (httpx.HTTPError, ValueError) as exc:
        return _static_deepseek_model_response(str(exc))

    raw_models = data.get("data") if isinstance(data, dict) else None
    if not isinstance(raw_models, list):
        return _static_deepseek_model_response("DeepSeek /models 返回格式异常")

    models: list[AdminModelOption] = []
    seen: set[str] = set()
    for item in raw_models:
        if not isinstance(item, dict):
            continue
        model_id = str(item.get("id", "")).strip()
        if not model_id or model_id in seen:
            continue
        seen.add(model_id)
        models.append(AdminModelOption(modelId=model_id, label=model_id))
    if not models:
        return _static_deepseek_model_response("DeepSeek /models 没有返回模型")
    return AdminDeepSeekModelsResponse(models=models, source="dynamic")


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
