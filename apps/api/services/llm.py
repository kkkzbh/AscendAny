from __future__ import annotations

import json
import logging
import os
import re
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


@dataclass(slots=True)
class ParsedTextToolCall:
    tool_name: str
    arguments: dict[str, Any]


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
        elif provider.mode == "gemini":
            reply = await self._gemini_with_tools(
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
            if tool_executor is None:
                return self._extract_text(message.get("content"))

            # Standard OpenAI-style tool calls
            if isinstance(tool_calls, list) and tool_calls:
                messages.append(message)
                for tc in tool_calls:
                    if not isinstance(tc, dict):
                        continue
                    tc_id = str(tc.get("id", ""))
                    func = tc.get("function", {})
                    tool_name = ""
                    arguments: dict[str, Any] = {}
                    if isinstance(func, dict):
                        tool_name = str(func.get("name", ""))
                        arguments = self._parse_tool_arguments(func.get("arguments"))

                    logger.info("Tool call: %s(%s)", tool_name, arguments)
                    result = await tool_executor.execute(tool_name, arguments)
                    messages.append(
                        {
                            "role": "tool",
                            "tool_call_id": tc_id,
                            "content": result,
                        }
                    )
                continue

            # Fallback: some models emit XML/DSML-like textual function calls.
            # Parse and execute them server-side, then ask the model for final
            # natural-language answer.
            content_text = self._extract_text(message.get("content"))
            parsed_text_tool_calls = self._extract_text_tool_calls(content_text)
            if parsed_text_tool_calls:
                messages.append({"role": "assistant", "content": content_text})
                tool_result_lines: list[str] = []
                for idx, call in enumerate(parsed_text_tool_calls, start=1):
                    logger.info(
                        "Text tool call: %s(%s)", call.tool_name, call.arguments
                    )
                    result = await tool_executor.execute(call.tool_name, call.arguments)
                    args_json = json.dumps(call.arguments, ensure_ascii=False)
                    tool_result_lines.append(
                        f"{idx}. {call.tool_name}({args_json}) => {result}"
                    )
                messages.append(
                    {
                        "role": "user",
                        "content": (
                            "以下是你请求的工具结果（JSON）。"
                            "请直接回答用户，不要输出任何函数/工具调用标记。\n"
                            + "\n".join(tool_result_lines)
                        ),
                    }
                )
                continue

            return content_text

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
    # Gemini path (Google Generative Language REST API)
    # ------------------------------------------------------------------

    async def _gemini_with_tools(
        self,
        provider: RuntimeProvider,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor | None,
    ) -> str:
        base = provider.base_url.rstrip("/")
        url = f"{base}/models/{provider.model}:generateContent?key={provider.api_key}"
        headers = {"Content-Type": "application/json"}

        # Convert messages to Gemini contents format.
        # System messages are merged into systemInstruction.
        system_parts: list[str] = []
        contents: list[dict[str, Any]] = []
        for msg in messages:
            role = msg["role"]
            content = msg["content"]
            if role == "system":
                system_parts.append(content)
                continue
            # Gemini uses "user" / "model" roles
            gemini_role = "model" if role == "assistant" else "user"
            # content can be a string or list of parts (tool results)
            if isinstance(content, list):
                # Already a list of parts (tool result round)
                contents.append({"role": gemini_role, "parts": content})
            else:
                contents.append({"role": gemini_role, "parts": [{"text": content}]})

        if not contents:
            raise AppError(
                status_code=400,
                code="INVALID_GEMINI_MESSAGES",
                message="Gemini requests need at least one user or model message.",
            )

        # Build tool declarations (Gemini function calling format)
        gemini_tools: list[dict[str, Any]] | None = None
        if tool_executor is not None:
            function_declarations = [
                {
                    "name": t["function"]["name"],
                    "description": t["function"]["description"],
                    "parameters": t["function"]["parameters"],
                }
                for t in TOOL_DEFINITIONS
            ]
            gemini_tools = [{"functionDeclarations": function_declarations}]

        for _ in range(_MAX_TOOL_ROUNDS):
            payload: dict[str, Any] = {"contents": contents}
            if system_parts:
                payload["systemInstruction"] = {
                    "parts": [{"text": "\n\n".join(system_parts)}]
                }
            if gemini_tools is not None:
                payload["tools"] = gemini_tools
            payload["generationConfig"] = {"temperature": 0.2}

            data = await self._post_json(url=url, headers=headers, payload=payload)

            candidates = data.get("candidates")
            if not isinstance(candidates, list) or not candidates:
                raise AppError(
                    status_code=502,
                    code="INVALID_GEMINI_RESPONSE",
                    message="Missing candidates in Gemini response.",
                )
            candidate = candidates[0]
            if not isinstance(candidate, dict):
                raise AppError(
                    status_code=502,
                    code="INVALID_GEMINI_RESPONSE",
                    message="Invalid candidate in Gemini response.",
                )

            content_obj = candidate.get("content")
            if not isinstance(content_obj, dict):
                raise AppError(
                    status_code=502,
                    code="INVALID_GEMINI_RESPONSE",
                    message="Missing content in Gemini candidate.",
                )

            parts = content_obj.get("parts")
            if not isinstance(parts, list):
                raise AppError(
                    status_code=502,
                    code="INVALID_GEMINI_RESPONSE",
                    message="Missing parts in Gemini content.",
                )

            # Separate text parts and function call parts
            text_chunks: list[str] = []
            function_calls: list[dict[str, Any]] = []
            for part in parts:
                if not isinstance(part, dict):
                    continue
                if "text" in part:
                    text = part["text"]
                    if isinstance(text, str):
                        text_chunks.append(text)
                elif "functionCall" in part:
                    function_calls.append(part["functionCall"])

            # No function calls or no executor — return text
            if not function_calls or tool_executor is None:
                return "\n".join(text_chunks).strip()

            # Append assistant turn (model role with function call parts)
            contents.append({"role": "model", "parts": parts})

            # Execute each function call and collect results
            function_response_parts: list[dict[str, Any]] = []
            for fc in function_calls:
                tool_name = fc.get("name", "")
                arguments = fc.get("args", {})
                if not isinstance(arguments, dict):
                    arguments = {}

                logger.info("Tool call (gemini): %s(%s)", tool_name, arguments)
                result = await tool_executor.execute(tool_name, arguments)

                # Try to parse result as JSON for structured response
                try:
                    result_data = json.loads(result)
                except (json.JSONDecodeError, ValueError):
                    result_data = {"result": result}

                function_response_parts.append(
                    {
                        "functionResponse": {
                            "name": tool_name,
                            "response": result_data,
                        }
                    }
                )

            # Append tool results as user turn
            contents.append({"role": "user", "parts": function_response_parts})

        # Exhausted rounds; final call without tools
        payload = {
            "contents": contents,
            "generationConfig": {"temperature": 0.2},
        }
        if system_parts:
            payload["systemInstruction"] = {
                "parts": [{"text": "\n\n".join(system_parts)}]
            }
        data = await self._post_json(url=url, headers=headers, payload=payload)
        candidates = data.get("candidates")
        if isinstance(candidates, list) and candidates:
            cand = candidates[0]
            if isinstance(cand, dict):
                content_obj = cand.get("content")
                if isinstance(content_obj, dict):
                    parts = content_obj.get("parts", [])
                    if isinstance(parts, list):
                        chunks: list[str] = []
                        for part in parts:
                            if isinstance(part, dict) and "text" in part:
                                text = part["text"]
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
        if provider_type in {"openai", "anthropic", "deepseek", "gemini"}:
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
        if provider_type == "gemini":
            return "gemini"
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

    @staticmethod
    def _parse_tool_arguments(raw: Any) -> dict[str, Any]:
        if isinstance(raw, dict):
            return raw
        if not isinstance(raw, str):
            return {}
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError:
            return {}
        if not isinstance(parsed, dict):
            return {}
        return parsed

    def _extract_text_tool_calls(self, content: str) -> list[ParsedTextToolCall]:
        text = content.strip()
        if not text:
            return []

        tag_prefix = r"(?:[|｜]DSML[|｜])?"
        invoke_pattern = re.compile(
            rf"<{tag_prefix}invoke\s+name=\"(?P<name>[A-Za-z0-9_.-]+)\"[^>]*>"
            rf"(?P<body>.*?)</{tag_prefix}invoke>",
            re.DOTALL,
        )
        parameter_pattern = re.compile(
            rf"<{tag_prefix}parameter\s+name=\"(?P<name>[^\"]+)\""
            rf"(?P<attrs>[^>]*)>(?P<value>.*?)</{tag_prefix}parameter>",
            re.DOTALL,
        )

        calls: list[ParsedTextToolCall] = []
        for invoke_match in invoke_pattern.finditer(text):
            tool_name = (invoke_match.group("name") or "").strip()
            if not tool_name:
                continue
            body = invoke_match.group("body") or ""
            arguments: dict[str, Any] = {}
            for param_match in parameter_pattern.finditer(body):
                key = (param_match.group("name") or "").strip()
                if not key:
                    continue
                value = (param_match.group("value") or "").strip()
                attrs = param_match.group("attrs") or ""
                arguments[key] = self._coerce_text_tool_parameter(value, attrs)
            calls.append(ParsedTextToolCall(tool_name=tool_name, arguments=arguments))
        return calls

    @staticmethod
    def _coerce_text_tool_parameter(value: str, attrs: str) -> Any:
        attrs_lower = attrs.lower()
        if 'string="true"' in attrs_lower:
            return value

        if value == "":
            return ""

        if value.startswith("{") or value.startswith("["):
            try:
                parsed = json.loads(value)
                if isinstance(parsed, (dict, list)):
                    return parsed
            except json.JSONDecodeError:
                pass

        lowered = value.lower()
        if lowered == "true":
            return True
        if lowered == "false":
            return False
        if re.fullmatch(r"[+-]?\d+", value):
            try:
                return int(value)
            except ValueError:
                return value
        if re.fullmatch(r"[+-]?\d+\.\d+", value):
            try:
                return float(value)
            except ValueError:
                return value
        return value
