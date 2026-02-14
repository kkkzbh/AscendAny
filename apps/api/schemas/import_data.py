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


# ── Link Actors ───────────────────────────────────────────

class LinkActorsRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    examTypes: list[str] | None = Field(
        default=None,
        description="Restrict to these exam types. null = all types.",
    )
    limit: int | None = Field(
        default=None,
        ge=1,
        description="Max number of exams to process.",
    )
    dryRun: bool = Field(
        default=False,
        description="If true, only plan without writing to DB.",
    )


class LinkActorsResponse(BaseModel):
    runId: str
    message: str


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
    errors: list[str] = Field(default_factory=list)


class SSELinkDoneEvent(BaseModel):
    """Sent as SSE event type 'done' for link-actors."""
    scannedExams: int
    processedExams: int
    matched: int
    ambiguous: int
    unmatched: int
    updated: int
    metricsUpdated: int
    remainingUnmatched: int


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
