from __future__ import annotations

from typing import cast

import httpx

from apps.api.core.config import Settings
from apps.api.services.llm import LLMService


def _build_service(settings: Settings) -> LLMService:
    return LLMService(
        settings=settings,
        http_client=cast(httpx.AsyncClient, object()),
    )


def test_list_provider_options_includes_server_default_metadata() -> None:
    settings = Settings()
    service = _build_service(settings)

    payload = service.list_provider_options()

    assert payload.defaultProvider == "server_default"
    assert payload.serverDefaultTarget == "server_default"
    assert payload.serverDefaultTargetLabel is None
    assert payload.serverDefaultModel is None
    assert any(
        option.type == "server_default" and option.usesServerConfig
        for option in payload.providers
    )


def test_server_default_provider_uses_dedicated_config(monkeypatch) -> None:
    settings = Settings()
    settings.llm.server_default.base_url = "https://llm.example.com/v1"
    settings.llm.server_default.model = "server-default-model"
    settings.llm.server_default.api_key_env = "SERVER_DEFAULT_TEST_KEY"
    settings.llm.providers["openai"].base_url = "https://api.openai.com/v1"
    settings.llm.providers["openai"].model = "provider-model"
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "secret")
    service = _build_service(settings)

    provider = service._resolve_provider("server_default", None)

    assert provider.base_url == "https://llm.example.com/v1"
    assert provider.model == "server-default-model"
    assert provider.api_key == "secret"
