from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Any

import httpx

from ..core.config import Settings
from ..core.errors import AppError
from ..schemas.chat import (
    ChatReplyRequest,
    ChatReplyResponse,
    ClientProviderConfig,
    ProviderType,
)
from ..schemas.model import ModelProvidersResponse, ProviderOptionResponse


@dataclass(slots=True)
class RuntimeProvider:
    key: str
    mode: str
    base_url: str
    model: str
    api_key: str
    uses_server_config: bool


class LLMService:
    def __init__(self, settings: Settings, http_client: httpx.AsyncClient) -> None:
        self._settings = settings
        self._http_client = http_client

    def list_provider_options(self) -> ModelProvidersResponse:
        options = [
            ProviderOptionResponse(
                type="server_default",
                label="默认",
                usesServerConfig=True,
                enabled=True,
            )
        ]
        for provider_type, provider_config in self._settings.llm.providers.items():
            options.append(
                ProviderOptionResponse(
                    type=provider_type,
                    label=provider_config.label,
                    usesServerConfig=False,
                    enabled=provider_config.enabled,
                )
            )
        return ModelProvidersResponse(
            defaultProvider="server_default",
            serverDefaultTarget="server_default",
            serverDefaultTargetLabel=None,
            serverDefaultModel=None,
            providers=options,
        )

    async def generate_reply(self, request: ChatReplyRequest) -> ChatReplyResponse:
        prepared_messages = self._prepare_messages(request)
        provider = self._resolve_provider(request.providerType, request.providerConfig)

        if provider.mode == "openai_compatible":
            reply = await self._request_openai_compatible(provider, prepared_messages)
        elif provider.mode == "anthropic":
            reply = await self._request_anthropic(provider, prepared_messages)
        else:
            raise AppError(
                status_code=400,
                code="UNSUPPORTED_PROVIDER_MODE",
                message=f"Unsupported provider mode: {provider.mode}",
            )

        if not reply.strip():
            raise AppError(
                status_code=502,
                code="EMPTY_LLM_REPLY",
                message="LLM provider returned an empty reply.",
            )

        return ChatReplyResponse(
            reply=reply,
            summary=request.summary,
            provider=request.providerType,
        )

    def _prepare_messages(self, request: ChatReplyRequest) -> list[dict[str, str]]:
        prepared: list[dict[str, str]] = []
        summary = request.summary.strip()
        if summary:
            prepared.append(
                {
                    "role": "system",
                    "content": f"Conversation summary:\n{summary}",
                }
            )

        for message in request.messages:
            content = message.content.strip()
            if not content:
                continue
            prepared.append(
                {
                    "role": message.role,
                    "content": content,
                }
            )

        if not prepared:
            raise AppError(
                status_code=400,
                code="EMPTY_MESSAGES",
                message="messages cannot be empty.",
            )

        return prepared

    def _resolve_provider(
        self,
        provider_type: ProviderType,
        provider_config: ClientProviderConfig | None,
    ) -> RuntimeProvider:
        if provider_type == "server_default":
            return self._resolve_server_default_provider()

        if provider_config is not None:
            mode = provider_config.mode or self._default_mode(provider_type)
            return RuntimeProvider(
                key=provider_type,
                mode=mode,
                base_url=provider_config.baseUrl.strip(),
                model=provider_config.model.strip(),
                api_key=provider_config.apiKey.strip(),
                uses_server_config=False,
            )

        return self._resolve_server_provider(provider_type)

    def _resolve_server_default_provider(self) -> RuntimeProvider:
        config = self._settings.llm.server_default
        api_key = os.getenv(config.api_key_env, "").strip()
        if not api_key:
            raise AppError(
                status_code=503,
                code="SERVER_DEFAULT_PROVIDER_KEY_MISSING",
                message=f"Missing API key env var: {config.api_key_env}",
            )
        return RuntimeProvider(
            key="server_default",
            mode=config.mode,
            base_url=config.base_url,
            model=config.model,
            api_key=api_key,
            uses_server_config=True,
        )

    def _resolve_server_provider(self, provider_type: str) -> RuntimeProvider:
        config = self._settings.llm.providers.get(provider_type)
        if config is None:
            raise AppError(
                status_code=503,
                code="SERVER_DEFAULT_PROVIDER_NOT_FOUND",
                message=f"Provider not found in server config: {provider_type}",
            )
        if not config.enabled:
            raise AppError(
                status_code=503,
                code="SERVER_DEFAULT_PROVIDER_DISABLED",
                message=f"Provider disabled in server config: {provider_type}",
            )
        api_key = os.getenv(config.api_key_env, "").strip()
        if not api_key:
            raise AppError(
                status_code=503,
                code="SERVER_DEFAULT_PROVIDER_KEY_MISSING",
                message=f"Missing API key env var: {config.api_key_env}",
            )
        if provider_type in {"openai", "anthropic", "deepseek"}:
            provider_key = provider_type
        else:
            provider_key = "openai"
        return RuntimeProvider(
            key=provider_key,
            mode=config.mode,
            base_url=config.base_url,
            model=config.model,
            api_key=api_key,
            uses_server_config=True,
        )

    @staticmethod
    def _default_mode(provider_type: ProviderType) -> str:
        if provider_type == "anthropic":
            return "anthropic"
        return "openai_compatible"

    async def _request_openai_compatible(
        self, provider: RuntimeProvider, messages: list[dict[str, str]]
    ) -> str:
        url = f"{provider.base_url.rstrip('/')}/chat/completions"
        payload = {
            "model": provider.model,
            "messages": messages,
            "temperature": 0.2,
        }
        headers = {
            "Authorization": f"Bearer {provider.api_key}",
            "Content-Type": "application/json",
        }
        data = await self._post_json(url=url, headers=headers, payload=payload)
        choices = data.get("choices")
        if not isinstance(choices, list) or not choices:
            raise AppError(
                status_code=502,
                code="INVALID_OPENAI_RESPONSE",
                message="Missing choices in OpenAI-compatible response.",
            )
        message = choices[0].get("message") if isinstance(choices[0], dict) else None
        if not isinstance(message, dict):
            raise AppError(
                status_code=502,
                code="INVALID_OPENAI_RESPONSE",
                message="Missing message in OpenAI-compatible response.",
            )
        return self._extract_text(message.get("content"))

    async def _request_anthropic(
        self, provider: RuntimeProvider, messages: list[dict[str, str]]
    ) -> str:
        if provider.base_url.rstrip("/").endswith("/v1"):
            url = f"{provider.base_url.rstrip('/')}/messages"
        else:
            url = f"{provider.base_url.rstrip('/')}/v1/messages"

        system_parts: list[str] = []
        anthropic_messages: list[dict[str, str]] = []
        for message in messages:
            role = message["role"]
            content = message["content"]
            if role == "system":
                system_parts.append(content)
                continue
            if role not in {"user", "assistant"}:
                continue
            anthropic_messages.append(
                {
                    "role": role,
                    "content": content,
                }
            )

        if not anthropic_messages:
            raise AppError(
                status_code=400,
                code="INVALID_ANTHROPIC_MESSAGES",
                message="Anthropic requests need at least one user or assistant message.",
            )

        payload: dict[str, Any] = {
            "model": provider.model,
            "max_tokens": 1024,
            "messages": anthropic_messages,
        }
        if system_parts:
            payload["system"] = "\n\n".join(system_parts)

        headers = {
            "x-api-key": provider.api_key,
            "anthropic-version": "2023-06-01",
            "Content-Type": "application/json",
        }
        data = await self._post_json(url=url, headers=headers, payload=payload)
        content = data.get("content")
        if not isinstance(content, list):
            raise AppError(
                status_code=502,
                code="INVALID_ANTHROPIC_RESPONSE",
                message="Missing content array in Anthropic response.",
            )

        chunks: list[str] = []
        for item in content:
            if not isinstance(item, dict):
                continue
            if item.get("type") != "text":
                continue
            text = item.get("text")
            if isinstance(text, str):
                chunks.append(text)
        return "\n".join(chunks).strip()

    async def _post_json(
        self, url: str, headers: dict[str, str], payload: dict[str, Any]
    ) -> dict[str, Any]:
        try:
            response = await self._http_client.post(url, headers=headers, json=payload)
        except httpx.TimeoutException as exc:
            raise AppError(
                status_code=504,
                code="LLM_REQUEST_TIMEOUT",
                message="LLM request timed out.",
            ) from exc
        except httpx.HTTPError as exc:
            raise AppError(
                status_code=502,
                code="LLM_REQUEST_FAILED",
                message=f"LLM request failed: {exc}",
            ) from exc

        if response.status_code >= 400:
            body = response.text.strip()
            raise AppError(
                status_code=502,
                code="LLM_UPSTREAM_ERROR",
                message=f"LLM upstream status {response.status_code}: {body[:500]}",
            )

        try:
            data = response.json()
        except ValueError as exc:
            raise AppError(
                status_code=502,
                code="INVALID_LLM_JSON",
                message="LLM response is not valid JSON.",
            ) from exc

        if not isinstance(data, dict):
            raise AppError(
                status_code=502,
                code="INVALID_LLM_JSON",
                message="LLM response JSON root is not an object.",
            )
        return data

    def _extract_text(self, content: Any) -> str:
        if isinstance(content, str):
            return content.strip()
        if isinstance(content, list):
            chunks: list[str] = []
            for item in content:
                if isinstance(item, str):
                    chunks.append(item)
                    continue
                if not isinstance(item, dict):
                    continue
                text = item.get("text")
                if isinstance(text, str):
                    chunks.append(text)
            return "\n".join(chunks).strip()
        return ""
