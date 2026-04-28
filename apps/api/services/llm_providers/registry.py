from __future__ import annotations

import os
import re
from typing import Any

from ...core.config import LLMProviderConfig, Settings
from ...core.errors import AppError
from .openai_compatible import OpenAICompatibleAdapter
from .responses import ResponsesAdapter
from .types import (
    AdapterKind,
    ProviderAdapter,
    ProviderDefinition,
    ProviderModelOption,
    ProviderProfile,
    RequestMode,
)

PROVIDER_ORDER: tuple[str, ...] = ("siliconflow", "openai", "copilot", "deepseek")

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


def _provider_options(provider_id: str) -> list[ProviderModelOption]:
    if provider_id == "siliconflow":
        return [
            ProviderModelOption(
                model_id="Pro/moonshotai/Kimi-K2.5",
                label="Pro/moonshotai/Kimi-K2.5",
            )
        ]
    if provider_id == "openai":
        return [
            ProviderModelOption(model_id=model_id, label=label)
            for model_id, label in _OPENAI_GPT54_MODEL_OPTIONS
        ]
    if provider_id == "copilot":
        return [
            ProviderModelOption(
                model_id=f"openai/{model_id}",
                label=label,
                request_mode=request_mode,  # type: ignore[arg-type]
                deprecated=deprecated,
            )
            for model_id, label, request_mode, deprecated in _COPILOT_MODEL_OPTIONS
        ]
    return [
        ProviderModelOption(model_id=model_id, label=label, deprecated=deprecated)
        for model_id, label, deprecated in _DEEPSEEK_MODEL_OPTIONS
    ]


PROVIDER_DEFINITIONS: dict[str, ProviderDefinition] = {
    "siliconflow": ProviderDefinition(
        id="siliconflow",
        title="硅基流动",
        provider="siliconflow",
        strategy_id="siliconflow-kimi-main-chat",
        adapter="openai_compatible",
        default_base_url="https://api.siliconflow.cn/v1",
        default_model="Pro/moonshotai/Kimi-K2.5",
        default_api_key_env="ASCENDANY_LLM_SILICONFLOW_API_KEY",
        default_request_mode="chat_completions",
        description="固定走硅基流动 OpenAI 兼容接口，默认使用 Kimi-K2.5。",
        model_hint="当前仅支持 Pro/moonshotai/Kimi-K2.5。",
        model_options=_provider_options("siliconflow"),
    ),
    "openai": ProviderDefinition(
        id="openai",
        title="OpenAI",
        provider="openai",
        strategy_id="openai-gpt54-main-chat",
        adapter="openai_compatible",
        default_base_url="https://shell.wyzai.top/v1",
        default_model="openai/gpt-5.4-medium-thinking",
        default_api_key_env="ASCENDANY_LLM_OPENAI_API_KEY",
        default_request_mode="chat_completions",
        description="按 OpenAI 兼容接口处理，默认预填 wyzai 与 GPT-5.4 medium thinking。",
        model_hint="推荐 openai/gpt-5.4-medium-thinking；运行时发送不带 openai/ 前缀的模型 ID。",
        model_options=_provider_options("openai"),
    ),
    "copilot": ProviderDefinition(
        id="copilot",
        title="GitHub Copilot",
        provider="openai",
        strategy_id="copilot-github-oauth-main-chat",
        adapter="openai_compatible",
        default_base_url="http://127.0.0.1:5140/api/internal/copilot/v1",
        default_model="openai/gpt-5-mini",
        default_api_key_env="ASCENDANY_LLM_COPILOT_API_KEY",
        default_request_mode="chat_completions",
        description="通过本地 Copilot bridge 暴露的 OpenAI 兼容接口接入。",
        model_hint="chat/completions 模型即时可用；Responses 模型由 Responses adapter 承载。",
        model_options=_provider_options("copilot"),
    ),
    "deepseek": ProviderDefinition(
        id="deepseek",
        title="DeepSeek",
        provider="deepseek",
        strategy_id="deepseek-official-main-chat",
        adapter="openai_compatible",
        default_base_url="https://api.deepseek.com",
        default_model="deepseek-v4-flash",
        default_api_key_env="ASCENDANY_LLM_DEEPSEEK_API_KEY",
        default_request_mode="chat_completions",
        description="按 DeepSeek 官方 OpenAI 兼容接口接入，模型列表优先从官方 /models 刷新。",
        model_hint="运行时发送 DeepSeek 官方原始模型 ID。",
        model_options=_provider_options("deepseek"),
        supports_dynamic_models=True,
    ),
}

_ADAPTERS: dict[AdapterKind, ProviderAdapter] = {
    "openai_compatible": OpenAICompatibleAdapter(),
    "responses": ResponsesAdapter(),
}


def get_adapter(adapter: AdapterKind) -> ProviderAdapter:
    return _ADAPTERS[adapter]


def default_provider_configs() -> dict[str, LLMProviderConfig]:
    return {
        provider_id: LLMProviderConfig(
            adapter=definition.adapter,
            base_url=definition.default_base_url,
            model=definition.default_model,
            api_key_env=definition.default_api_key_env,
            request_mode=definition.default_request_mode,
        )
        for provider_id, definition in PROVIDER_DEFINITIONS.items()
    }


def normalize_request_mode(value: str) -> RequestMode:
    normalized = value.strip()
    if normalized == "responses":
        return "responses"
    return "chat_completions"


def normalize_adapter(value: str) -> AdapterKind:
    normalized = value.strip()
    if normalized == "responses":
        return "responses"
    return "openai_compatible"


def normalize_model(provider_id: str, model: str) -> str:
    if provider_id == "siliconflow":
        return _normalize_siliconflow_model(model)
    if provider_id == "openai":
        return _normalize_openai_model(model)
    if provider_id == "copilot":
        return _normalize_copilot_model(model)
    if provider_id == "deepseek":
        return _normalize_deepseek_model_id(model)
    raise AppError(
        status_code=422,
        code="INVALID_MODEL_CONFIG",
        message=f"Unsupported provider: {provider_id}",
    )


def transport_model(provider_id: str, model: str) -> str:
    if provider_id in {"openai", "copilot"}:
        if model.startswith("openai/"):
            return model.removeprefix("openai/").strip()
        if model.startswith("github-copilot/"):
            return model.removeprefix("github-copilot/").strip()
    if provider_id == "deepseek":
        return _normalize_deepseek_model_id(model)
    return model.strip()


def build_provider_profile(
    settings: Settings,
    provider_id: str | None = None,
    api_key_override: str | None = None,
) -> ProviderProfile:
    active_provider = provider_id or settings.llm.active_provider
    if active_provider not in PROVIDER_DEFINITIONS:
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message=f"Unsupported provider: {active_provider}",
        )
    definition = PROVIDER_DEFINITIONS[active_provider]
    config = settings.llm.providers.get(active_provider)
    if config is None:
        config = LLMProviderConfig(
            adapter=definition.adapter,
            base_url=definition.default_base_url,
            model=definition.default_model,
            api_key_env=definition.default_api_key_env,
            request_mode=definition.default_request_mode,
        )
    model = normalize_model(active_provider, config.model)
    api_key_env = config.api_key_env.strip()
    api_key = (api_key_override or "").strip() or os.getenv(api_key_env, "").strip()
    adapter = normalize_adapter(config.adapter)
    request_mode = normalize_request_mode(config.request_mode)
    if request_mode == "responses":
        adapter = "responses"
    return ProviderProfile(
        id=active_provider,
        title=definition.title,
        provider=definition.provider,
        strategy_id=definition.strategy_id,
        adapter=adapter,
        base_url=config.base_url.strip().rstrip("/"),
        model=model,
        transport_model=transport_model(active_provider, model),
        api_key_env=api_key_env,
        api_key=api_key,
        request_mode=request_mode,
        description=definition.description,
        model_hint=definition.model_hint,
        model_options=definition.model_options,
        supports_dynamic_models=definition.supports_dynamic_models,
    )


def model_option_request_mode(provider_id: str, model: str) -> RequestMode:
    normalized = normalize_model(provider_id, model)
    for option in PROVIDER_DEFINITIONS[provider_id].model_options:
        if option.model_id == normalized:
            return option.request_mode
    return PROVIDER_DEFINITIONS[provider_id].default_request_mode


def _normalize_siliconflow_model(model: str) -> str:
    value = model.strip()
    if re.fullmatch(r"siliconflow/pro/moonshotai/kimi-k2\.5", value, re.I):
        return "Pro/moonshotai/Kimi-K2.5"
    if re.fullmatch(r"pro/moonshotai/kimi-k2\.5", value, re.I):
        return "Pro/moonshotai/Kimi-K2.5"
    raise AppError(
        status_code=422,
        code="INVALID_MODEL_CONFIG",
        message="硅基流动 Provider 仅支持 Pro/moonshotai/Kimi-K2.5。",
    )


def _normalize_openai_model(model: str) -> str:
    value = model.strip()
    if not value:
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="OpenAI Provider 默认模型不能为空。",
        )
    if "/" not in value:
        value = f"openai/{value}"
    if not value.startswith("openai/"):
        raise AppError(
            status_code=422,
            code="INVALID_MODEL_CONFIG",
            message="OpenAI Provider 仅支持 openai/gpt-5.4 系列。",
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
            message="OpenAI Provider 仅支持 openai/gpt-5.4 系列。",
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
            message="GitHub Copilot Provider 只能从内置 Copilot 模型列表选择。",
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
            message="DeepSeek Provider 模型必须是官方原始模型 ID。",
        )
    return value


def provider_config_to_raw(config: LLMProviderConfig) -> dict[str, Any]:
    return {
        "adapter": config.adapter,
        "base_url": config.base_url,
        "model": config.model,
        "api_key_env": config.api_key_env,
        "request_mode": config.request_mode,
    }
