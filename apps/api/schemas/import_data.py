from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


# ── Upload ─────────────────────────────────────────────────

class UploadResponse(BaseModel):
    examType: str
    examName: str
    sourcePath: str
    fileCount: int
    message: str


# ── Import Run ────────────────────────────────────────────

class ImportRunRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    examTypes: list[str] | None = Field(
        default=None,
        description="Restrict to these exam types. null = all types.",
    )
    limit: int | None = Field(
        default=None,
        ge=1,
        description="Max number of changed exams to process.",
    )
    dryRun: bool = Field(
        default=False,
        description="If true, only scan without writing to DB.",
    )
    force: bool = Field(
        default=False,
        description="If true, reprocess all exams regardless of fingerprint.",
    )


class ImportRunResponse(BaseModel):
    runId: str
    message: str


class SingleImportRunRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    examType: str = Field(description="Exam type, e.g. datastructure")
    sourcePath: str = Field(
        min_length=1,
        description="Exam source_path, supports both full (datastructure/xxx) and short (xxx).",
    )
    dryRun: bool = Field(
        default=False,
        description="If true, only scan without writing to DB.",
    )
    force: bool = Field(
        default=True,
        description="If true, reprocess regardless of fingerprint.",
    )


# ── SSE Events (documented here for reference) ───────────

class SSELogEvent(BaseModel):
    """Sent as SSE event type 'log'."""
    level: Literal["info", "success", "warning", "error"]
    message: str
    timestamp: str


class SSEProgressEvent(BaseModel):
    """Sent as SSE event type 'progress'."""
    current: int
    total: int
    examType: str | None = None
    sourcePath: str | None = None
    phase: str | None = None


class SSEDoneEvent(BaseModel):
    """Sent as SSE event type 'done'."""
    ingestRunId: int | None = None
    scanned: int
    skipped: int
    succeeded: int
    failed: int
    submissionsBound: int = 0
    submissionsPendingClaim: int = 0
    nicknameConflicts: int = 0
    achievementsRecomputedStudents: int = 0
    errors: list[str] = Field(default_factory=list)


# ── History ───────────────────────────────────────────────

class IngestHistoryItem(BaseModel):
    ingestRunId: int
    status: str
    startedAt: datetime | None
    finishedAt: datetime | None
    scanned: int | None = None
    toProcess: int | None = None
    succeeded: int | None = None
    failed: int | None = None
    errors: list[str] = Field(default_factory=list)


class IngestHistoryResponse(BaseModel):
    items: list[IngestHistoryItem]
    total: int
