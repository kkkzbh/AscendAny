from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any


@dataclass(slots=True)
class SourceFile:
    file_role: str
    relative_path: str
    absolute_path: Path
    sha256: str
    size_bytes: int
    mtime: datetime | None


@dataclass(slots=True)
class ExamUnit:
    exam_type: str
    source_path: str
    absolute_path: Path
    files: list[SourceFile]
    fingerprint: str


@dataclass(slots=True)
class ProblemInfo:
    problem_code: str
    problem_title: str | None = None
    problem_kind: str | None = None
    group_code: str | None = None
    group_name: str | None = None
    points: float | None = None
    order_idx: int | None = None
    meta: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class ExamMeta:
    title: str | None
    starts_at: datetime | None
    ends_at: datetime | None
    duration_seconds: int | None
    total_points: float | None
    meta: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class ParticipantRow:
    identity_source: str
    external_id: str
    display_name: str | None
    user_group: str | None
    rank: int | None
    total_score: float | None
    time_used_seconds: int | None
    solved_count: int | None
    absent: bool
    problem_stats: dict[str, dict[str, Any]]
    raw: dict[str, Any] = field(default_factory=dict)
    student_id: int | None = None


@dataclass(slots=True)
class SubmissionRow:
    actor_source: str
    actor_external_id: str
    actor_name: str | None
    submitted_at: datetime | None
    verdict: str | None
    score: float | None
    problem_code: str | None
    language: str | None
    memory_kb: int | None
    time_ms: int | None
    row_hash: str
    raw: dict[str, Any] = field(default_factory=dict)
    student_id: int | None = None


@dataclass(slots=True)
class ExamBundle:
    unit: ExamUnit
    exam_meta: ExamMeta
    problems: list[ProblemInfo]
    participants: list[ParticipantRow]
    submissions: list[SubmissionRow]
