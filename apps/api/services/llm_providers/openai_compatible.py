from __future__ import annotations

import json
import time
from typing import Any, AsyncIterator

import httpx

from ...core.errors import AppError
from .types import (
    ProviderConnectionTestResult,
    ProviderModelListResult,
    ProviderModelOption,
    ProviderProfile,
    ProviderRequest,
    ProviderResponse,
    ProviderStreamEvent,
)


class OpenAICompatibleAdapter:
    async def complete(self, request: ProviderRequest) -> ProviderResponse:
        data = await self._post_json(
            http_client=request.http_client,
            url=self._chat_url(request.profile),
            headers=self._headers(request.profile),
            payload=self._payload(request, stream=False),
        )
        message = self._extract_message(data)
        return ProviderResponse(
            text=self._extract_text(message.get("content")),
            raw_message=message,
        )

    async def stream(
        self, request: ProviderRequest
    ) -> AsyncIterator[ProviderStreamEvent]:
        url = self._chat_url(request.profile)
        headers = self._headers(request.profile)
        payload = self._payload(request, stream=True)
        try:
            external_client = request.http_client
            if external_client is None:
                external_client = httpx.AsyncClient(timeout=None)
                should_close = True
            else:
                should_close = False
            try:
                client = external_client
                async with client.stream(
                    "POST", url, headers=headers, json=payload
                ) as response:
                    if response.status_code >= 400:
                        body = (await response.aread()).decode("utf-8", "replace")
                        raise AppError(
                            status_code=502,
                            code="LLM_UPSTREAM_ERROR",
                            message=(
                                f"LLM upstream status {response.status_code}: "
                                f"{body.strip()[:500]}"
                            ),
                        )
                    async for line in response.aiter_lines():
                        line = line.strip()
                        if not line or line.startswith(":"):
                            continue
                        if not line.startswith("data:"):
                            continue
                        raw = line.removeprefix("data:").strip()
                        if raw == "[DONE]":
                            break
                        try:
                            chunk = json.loads(raw)
                        except json.JSONDecodeError:
                            continue
                        choices = chunk.get("choices")
                        if not isinstance(choices, list) or not choices:
                            continue
                        delta = choices[0].get("delta")
                        if not isinstance(delta, dict):
                            continue
                        reasoning_content = self._extract_text(
                            delta.get("reasoning_content")
                        )
                        if reasoning_content:
                            yield ProviderStreamEvent(
                                kind="reasoning_delta",
                                text=reasoning_content,
                            )
                        content = self._extract_text(delta.get("content"))
                        if content:
                            yield ProviderStreamEvent(kind="delta", text=content)
                        tool_calls = delta.get("tool_calls")
                        if isinstance(tool_calls, list) and tool_calls:
                            yield ProviderStreamEvent(
                                kind="tool_call_delta",
                                tool_calls=[
                                    item for item in tool_calls if isinstance(item, dict)
                                ],
                            )
            finally:
                if should_close:
                    await external_client.aclose()
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

    async def test_connection(
        self, profile: ProviderProfile, http_client: httpx.AsyncClient
    ) -> ProviderConnectionTestResult:
        started = time.perf_counter()
        if not profile.api_key:
            return ProviderConnectionTestResult(
                ok=False,
                status="missing_key",
                message=f"未配置 API Key：{profile.api_key_env}",
                elapsed_ms=int((time.perf_counter() - started) * 1000),
            )
        try:
            response = await http_client.post(
                self._chat_url(profile),
                headers=self._headers(profile),
                json={
                    "model": profile.transport_model,
                    "messages": [
                        {"role": "system", "content": "Connection test."},
                        {"role": "user", "content": "Reply with ok."},
                    ],
                    "temperature": 0,
                    "max_tokens": 8,
                },
            )
        except httpx.HTTPError as exc:
            return ProviderConnectionTestResult(
                ok=False,
                status="request_failed",
                message=str(exc),
                elapsed_ms=int((time.perf_counter() - started) * 1000),
            )
        elapsed_ms = int((time.perf_counter() - started) * 1000)
        if response.status_code >= 400:
            return ProviderConnectionTestResult(
                ok=False,
                status="upstream_error",
                message=response.text.strip()[:500] or f"HTTP {response.status_code}",
                elapsed_ms=elapsed_ms,
            )
        return ProviderConnectionTestResult(
            ok=True,
            status="success",
            message="连接成功",
            elapsed_ms=elapsed_ms,
        )

    async def list_models(
        self, profile: ProviderProfile, http_client: httpx.AsyncClient
    ) -> ProviderModelListResult:
        if not profile.supports_dynamic_models:
            return ProviderModelListResult(models=profile.model_options, source="static")
        if not profile.api_key:
            return ProviderModelListResult(
                models=profile.model_options,
                source="static",
                error=f"未配置 API Key：{profile.api_key_env}",
            )
        try:
            response = await http_client.get(
                f"{profile.base_url.rstrip('/')}/models",
                headers={"Authorization": f"Bearer {profile.api_key}"},
            )
            if response.status_code >= 400:
                return ProviderModelListResult(
                    models=profile.model_options,
                    source="static",
                    error=response.text.strip()[:500] or f"HTTP {response.status_code}",
                )
            data = response.json()
        except (httpx.HTTPError, ValueError) as exc:
            return ProviderModelListResult(
                models=profile.model_options,
                source="static",
                error=str(exc),
            )
        raw_models = data.get("data") if isinstance(data, dict) else None
        if not isinstance(raw_models, list):
            return ProviderModelListResult(
                models=profile.model_options,
                source="static",
                error=f"{profile.title} /models 返回格式异常",
            )
        models: list[ProviderModelOption] = []
        seen: set[str] = set()
        for item in raw_models:
            if not isinstance(item, dict):
                continue
            model_id = str(item.get("id", "")).strip()
            if not model_id or model_id in seen:
                continue
            seen.add(model_id)
            models.append(ProviderModelOption(model_id=model_id, label=model_id))
        if not models:
            return ProviderModelListResult(
                models=profile.model_options,
                source="static",
                error=f"{profile.title} /models 没有返回模型",
            )
        return ProviderModelListResult(models=models, source="dynamic")

    def _chat_url(self, profile: ProviderProfile) -> str:
        return f"{profile.base_url.rstrip('/')}/chat/completions"

    def _headers(self, profile: ProviderProfile) -> dict[str, str]:
        return {
            "Authorization": f"Bearer {profile.api_key}",
            "Content-Type": "application/json",
        }

    def _payload(self, request: ProviderRequest, *, stream: bool) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "model": request.profile.transport_model,
            "messages": request.messages,
            "temperature": request.temperature,
        }
        if request.max_tokens is not None:
            payload["max_tokens"] = request.max_tokens
        if request.tools is not None:
            payload["tools"] = request.tools
        if stream:
            payload["stream"] = True
        return payload

    async def _post_json(
        self,
        http_client: httpx.AsyncClient | None,
        url: str,
        headers: dict[str, str],
        payload: dict[str, Any],
    ) -> dict[str, Any]:
        client = http_client
        should_close = False
        if client is None:
            client = httpx.AsyncClient()
            should_close = True
        try:
            try:
                response = await client.post(url, headers=headers, json=payload)
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
                raise AppError(
                    status_code=502,
                    code="LLM_UPSTREAM_ERROR",
                    message=(
                        f"LLM upstream status {response.status_code}: "
                        f"{response.text.strip()[:500]}"
                    ),
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
        finally:
            if should_close:
                await client.aclose()

    def _extract_message(self, data: dict[str, Any]) -> dict[str, Any]:
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
        return message

    def _extract_text(self, content: Any) -> str:
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            chunks: list[str] = []
            for item in content:
                if isinstance(item, str):
                    chunks.append(item)
                elif isinstance(item, dict) and isinstance(item.get("text"), str):
                    chunks.append(item["text"])
            return "\n".join(chunks)
        return ""
