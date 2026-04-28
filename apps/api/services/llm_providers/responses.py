from __future__ import annotations

from typing import AsyncIterator

import httpx

from ...core.errors import AppError
from .types import (
    ProviderConnectionTestResult,
    ProviderModelListResult,
    ProviderProfile,
    ProviderRequest,
    ProviderResponse,
    ProviderStreamEvent,
)


class ResponsesAdapter:
    async def complete(self, request: ProviderRequest) -> ProviderResponse:
        raise AppError(
            status_code=501,
            code="RESPONSES_ADAPTER_NOT_IMPLEMENTED",
            message="Responses adapter is not implemented yet.",
        )

    async def stream(
        self, request: ProviderRequest
    ) -> AsyncIterator[ProviderStreamEvent]:
        raise AppError(
            status_code=501,
            code="RESPONSES_ADAPTER_NOT_IMPLEMENTED",
            message="Responses adapter is not implemented yet.",
        )
        yield ProviderStreamEvent(kind="delta", text="")

    async def test_connection(
        self, profile: ProviderProfile, http_client: httpx.AsyncClient
    ) -> ProviderConnectionTestResult:
        _ = http_client
        return ProviderConnectionTestResult(
            ok=False,
            status="unsupported",
            message="Responses adapter is not implemented yet.",
            elapsed_ms=0,
        )

    async def list_models(
        self, profile: ProviderProfile, http_client: httpx.AsyncClient
    ) -> ProviderModelListResult:
        _ = http_client
        return ProviderModelListResult(models=profile.model_options, source="static")
