from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


ChatRole = Literal["user", "assistant", "system"]
ProviderType = str


class ChatMessageRequest(BaseModel):
    role: ChatRole
    content: str = Field(min_length=1)


class ChatReplyRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    studentId: str | None = None
    ptaNickname: str | None = None
    messages: list[ChatMessageRequest] = Field(default_factory=list)
    summary: str = ""
    roleId: str | None = None
    roleName: str | None = None
    roleSystemPrompt: str | None = None


class ChatReplyResponse(BaseModel):
    reply: str
    summary: str
    provider: ProviderType
    model: str = ""
    requestMode: str = "chat_completions"


class AutoAnalysisRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    studentId: str | None = None
    ptaNickname: str | None = None
    latestExamId: str | None = None
    roleId: str | None = None
    roleName: str | None = None
    roleSystemPrompt: str | None = None


class AutoAnalysisResponse(BaseModel):
    reply: str
    provider: ProviderType
    model: str = ""
    requestMode: str = "chat_completions"


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
