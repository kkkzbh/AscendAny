from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


ChatRole = Literal["user", "assistant", "system"]
ProviderType = Literal["server_default", "openai", "anthropic", "deepseek"]


class ChatMessageRequest(BaseModel):
    role: ChatRole
    content: str = Field(min_length=1)


class ClientProviderConfig(BaseModel):
    baseUrl: str = Field(min_length=1)
    model: str = Field(min_length=1)
    apiKey: str = Field(min_length=1)
    mode: Literal["openai_compatible", "anthropic"] | None = None


class ChatReplyRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    studentId: str | None = None
    ptaNickname: str | None = None
    messages: list[ChatMessageRequest] = Field(default_factory=list)
    summary: str = ""
    providerType: ProviderType = "server_default"
    providerConfig: ClientProviderConfig | None = None
    roleId: str | None = None


class ChatReplyResponse(BaseModel):
    reply: str
    summary: str
    provider: ProviderType


class AutoAnalysisRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    studentId: str | None = None
    ptaNickname: str | None = None
    latestExamId: str | None = None
    providerType: ProviderType = "server_default"
    providerConfig: ClientProviderConfig | None = None
    roleId: str | None = None


class AutoAnalysisResponse(BaseModel):
    reply: str
    provider: ProviderType


class AutoAnalysisPrecomputeRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    examId: int = Field(gt=0)
    roleId: str | None = None
    maxAccounts: int = Field(default=2000, ge=1, le=10000)


class AutoAnalysisPrecomputeResponse(BaseModel):
    examId: int
    roleId: str
    candidates: int
    generated: int
    skippedCached: int
    skippedNotLatest: int
    failed: int
