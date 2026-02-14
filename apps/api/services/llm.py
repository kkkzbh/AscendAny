from __future__ import annotations

import json
import logging
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
from .tools import TOOL_DEFINITIONS, ToolExecutor

logger = logging.getLogger(__name__)

_MAX_TOOL_ROUNDS = 5


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

    async def generate_reply(
        self,
        request: ChatReplyRequest,
        system_prompt: str | None = None,
        tool_executor: ToolExecutor | None = None,
    ) -> ChatReplyResponse:
        prepared_messages = self._prepare_messages(request, system_prompt=system_prompt)
        provider = self._resolve_provider(request.providerType, request.providerConfig)

        if provider.mode == "openai_compatible":
            reply = await self._openai_with_tools(
                provider, prepared_messages, tool_executor
            )
        elif provider.mode == "anthropic":
            reply = await self._anthropic_with_tools(
                provider, prepared_messages, tool_executor
            )
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

    # ------------------------------------------------------------------
    # Message preparation
    # ------------------------------------------------------------------

    def _prepare_messages(
        self,
        request: ChatReplyRequest,
        system_prompt: str | None = None,
    ) -> list[dict[str, Any]]:
        prepared: list[dict[str, Any]] = []

        # Injected system prompt from PromptService (layers 1-4)
        if system_prompt:
            prepared.append(
                {
                    "role": "system",
                    "content": system_prompt,
                }
            )

        # Conversation summary (if any)
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

    # ------------------------------------------------------------------
    # OpenAI-compatible path (with tool calling loop)
    # ------------------------------------------------------------------

    async def _openai_with_tools(
        self,
        provider: RuntimeProvider,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor | None,
    ) -> str:
        url = f"{provider.base_url.rstrip('/')}/chat/completions"
        headers = {
            "Authorization": f"Bearer {provider.api_key}",
            "Content-Type": "application/json",
        }

        for _ in range(_MAX_TOOL_ROUNDS):
            payload: dict[str, Any] = {
                "model": provider.model,
                "messages": messages,
                "temperature": 0.2,
            }
            if tool_executor is not None:
                payload["tools"] = TOOL_DEFINITIONS

            data = await self._post_json(url=url, headers=headers, payload=payload)

            choices = data.get("choices")
            if not isinstance(choices, list) or not choices:
                raise AppError(
                    status_code=502,
                    code="INVALID_OPENAI_RESPONSE",
                    message="Missing choices in OpenAI-compatible response.",
                )
            choice = choices[0]
            if not isinstance(choice, dict):
                raise AppError(
                    status_code=502,
                    code="INVALID_OPENAI_RESPONSE",
                    message="Invalid choice in OpenAI-compatible response.",
                )

            message = choice.get("message")
            if not isinstance(message, dict):
                raise AppError(
                    status_code=502,
                    code="INVALID_OPENAI_RESPONSE",
                    message="Missing message in OpenAI-compatible response.",
                )

            tool_calls = message.get("tool_calls")

            # If no tool calls or no executor, return text
            if not tool_calls or tool_executor is None:
                return self._extract_text(message.get("content"))

            # Process tool calls
            # Append the assistant message (with tool_calls) to conversation
            messages.append(message)

            for tc in tool_calls:
                if not isinstance(tc, dict):
                    continue
                tc_id = tc.get("id", "")
                func = tc.get("function", {})
                tool_name = func.get("name", "")
                try:
                    arguments = json.loads(func.get("arguments", "{}"))
                except json.JSONDecodeError:
                    arguments = {}

                logger.info("Tool call: %s(%s)", tool_name, arguments)
                result = await tool_executor.execute(tool_name, arguments)

                messages.append(
                    {
                        "role": "tool",
                        "tool_call_id": tc_id,
                        "content": result,
                    }
                )

            # Loop continues — LLM will process tool results

        # Exhausted rounds; do one final call without tools
        payload = {
            "model": provider.model,
            "messages": messages,
            "temperature": 0.2,
        }
        data = await self._post_json(url=url, headers=headers, payload=payload)
        choices = data.get("choices")
        if isinstance(choices, list) and choices:
            msg = choices[0].get("message") if isinstance(choices[0], dict) else None
            if isinstance(msg, dict):
                return self._extract_text(msg.get("content"))
        return ""

    # ------------------------------------------------------------------
    # Anthropic path (with tool calling loop)
    # ------------------------------------------------------------------

    async def _anthropic_with_tools(
        self,
        provider: RuntimeProvider,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor | None,
    ) -> str:
        if provider.base_url.rstrip("/").endswith("/v1"):
            url = f"{provider.base_url.rstrip('/')}/messages"
        else:
            url = f"{provider.base_url.rstrip('/')}/v1/messages"

        # Separate system messages
        system_parts: list[str] = []
        anthropic_messages: list[dict[str, Any]] = []
        for msg in messages:
            role = msg["role"]
            content = msg["content"]
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

        headers = {
            "x-api-key": provider.api_key,
            "anthropic-version": "2023-06-01",
            "Content-Type": "application/json",
        }

        # Convert tool definitions to Anthropic format
        anthropic_tools: list[dict[str, Any]] | None = None
        if tool_executor is not None:
            anthropic_tools = [
                {
                    "name": t["function"]["name"],
                    "description": t["function"]["description"],
                    "input_schema": t["function"]["parameters"],
                }
                for t in TOOL_DEFINITIONS
            ]

        for _ in range(_MAX_TOOL_ROUNDS):
            payload: dict[str, Any] = {
                "model": provider.model,
                "max_tokens": 4096,
                "messages": anthropic_messages,
            }
            if system_parts:
                payload["system"] = "\n\n".join(system_parts)
            if anthropic_tools is not None:
                payload["tools"] = anthropic_tools

            data = await self._post_json(url=url, headers=headers, payload=payload)

            content_blocks = data.get("content")
            if not isinstance(content_blocks, list):
                raise AppError(
                    status_code=502,
                    code="INVALID_ANTHROPIC_RESPONSE",
                    message="Missing content array in Anthropic response.",
                )

            # Collect text and tool_use blocks
            text_chunks: list[str] = []
            tool_uses: list[dict[str, Any]] = []
            for block in content_blocks:
                if not isinstance(block, dict):
                    continue
                if block.get("type") == "text":
                    text = block.get("text")
                    if isinstance(text, str):
                        text_chunks.append(text)
                elif block.get("type") == "tool_use":
                    tool_uses.append(block)

            # No tool calls or no executor — return text
            if not tool_uses or tool_executor is None:
                return "\n".join(text_chunks).strip()

            # Append assistant message with the full content (including tool_use blocks)
            anthropic_messages.append(
                {
                    "role": "assistant",
                    "content": content_blocks,
                }
            )

            # Execute tool calls and build tool_result response
            tool_results: list[dict[str, Any]] = []
            for tu in tool_uses:
                tool_name = tu.get("name", "")
                tool_use_id = tu.get("id", "")
                arguments = tu.get("input", {})
                if not isinstance(arguments, dict):
                    arguments = {}

                logger.info("Tool call (anthropic): %s(%s)", tool_name, arguments)
                result = await tool_executor.execute(tool_name, arguments)

                tool_results.append(
                    {
                        "type": "tool_result",
                        "tool_use_id": tool_use_id,
                        "content": result,
                    }
                )

            # Add user message with tool results (Anthropic convention)
            anthropic_messages.append(
                {
                    "role": "user",
                    "content": tool_results,
                }
            )

            # Loop continues

        # Exhausted rounds; final call without tools
        payload = {
            "model": provider.model,
            "max_tokens": 4096,
            "messages": anthropic_messages,
        }
        if system_parts:
            payload["system"] = "\n\n".join(system_parts)
        data = await self._post_json(url=url, headers=headers, payload=payload)
        content_blocks = data.get("content")
        if isinstance(content_blocks, list):
            chunks: list[str] = []
            for block in content_blocks:
                if isinstance(block, dict) and block.get("type") == "text":
                    text = block.get("text")
                    if isinstance(text, str):
                        chunks.append(text)
            return "\n".join(chunks).strip()
        return ""

    # ------------------------------------------------------------------
    # Provider resolution (unchanged)
    # ------------------------------------------------------------------

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

    # ------------------------------------------------------------------
    # HTTP & text helpers
    # ------------------------------------------------------------------

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
