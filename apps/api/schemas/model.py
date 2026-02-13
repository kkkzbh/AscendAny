from __future__ import annotations

from pydantic import BaseModel


class ProviderOptionResponse(BaseModel):
    type: str
    label: str
    usesServerConfig: bool
    enabled: bool


class ModelProvidersResponse(BaseModel):
    defaultProvider: str
    serverDefaultTarget: str
    serverDefaultTargetLabel: str | None = None
    serverDefaultModel: str | None = None
    providers: list[ProviderOptionResponse]
