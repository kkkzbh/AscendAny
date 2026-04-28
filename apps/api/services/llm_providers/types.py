from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Literal, Protocol

import httpx

RequestMode = Literal["chat_completions", "responses"]
AdapterKind = Literal["openai_compatible", "responses"]
ProviderStreamEventKind = Literal["delta", "tool_call_delta"]


@dataclass(slots=True)
class ProviderModelOption:
    model_id: str
    label: str
    request_mode: RequestMode = "chat_completions"
    deprecated: bool = False
    disabled: bool = False
    disabled_reason: str | None = None


@dataclass(slots=True)
class ProviderDefinition:
    id: str
    title: str
    provider: str
    strategy_id: str
    adapter: AdapterKind
    default_base_url: str
    default_model: str
    default_api_key_env: str
    default_request_mode: RequestMode
    description: str
    model_hint: str
    model_options: list[ProviderModelOption]
    supports_dynamic_models: bool = False


@dataclass(slots=True)
class ProviderProfile:
    id: str
    title: str
    provider: str
    strategy_id: str
    adapter: AdapterKind
    base_url: str
    model: str
    transport_model: str
    api_key_env: str
    api_key: str
    request_mode: RequestMode
    description: str
    model_hint: str
    model_options: list[ProviderModelOption]
    supports_dynamic_models: bool = False


@dataclass(slots=True)
class ProviderRequest:
    profile: ProviderProfile
    messages: list[dict[str, Any]]
    temperature: float = 0.2
    tools: list[dict[str, Any]] | None = None
    max_tokens: int | None = None
    http_client: httpx.AsyncClient | None = None


@dataclass(slots=True)
class ProviderResponse:
    text: str
    raw_message: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class ProviderStreamEvent:
    kind: ProviderStreamEventKind
    text: str = ""
    tool_calls: list[dict[str, Any]] = field(default_factory=list)


@dataclass(slots=True)
class ProviderConnectionTestResult:
    ok: bool
    status: str
    message: str
    elapsed_ms: int


@dataclass(slots=True)
class ProviderModelListResult:
    models: list[ProviderModelOption]
    source: Literal["dynamic", "static"]
    error: str | None = None


class ProviderAdapter(Protocol):
    async def complete(self, request: ProviderRequest) -> ProviderResponse:
        ...

    async def stream(
        self, request: ProviderRequest
    ) -> AsyncIterator[ProviderStreamEvent]:
        ...

    async def test_connection(
        self, profile: ProviderProfile, http_client: httpx.AsyncClient
    ) -> ProviderConnectionTestResult:
        ...

    async def list_models(
        self, profile: ProviderProfile, http_client: httpx.AsyncClient
    ) -> ProviderModelListResult:
        ...
