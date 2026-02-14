"""Function calling tools for the chat Agent.

Provides OpenAI-compatible tool definitions and an executor that maps
tool call names to DB queries.

Tools available:
  - get_student_rating_history   : Rating history for a student
  - get_student_metric_history   : Per-exam metric history for a student
  - get_exam_submissions         : Submission records for a student in an exam
  - get_exam_participants        : Ranked participant list for an exam
  - get_exam_info                : Exam metadata (title, date, duration, counts)
"""

from __future__ import annotations

import json
import logging
from typing import Any

from ..db.repository import ApiRepository
from .identity import ResolvedIdentity

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# OpenAI-compatible tool definitions
# ---------------------------------------------------------------------------

TOOL_DEFINITIONS: list[dict[str, Any]] = [
    {
        "type": "function",
        "function": {
            "name": "get_student_rating_history",
            "description": "获取该学生的 Rating 历史记录（按考试时间倒序），包含每场考试的 old_rating、delta、new_rating。",
            "parameters": {
                "type": "object",
                "properties": {
                    "limit": {
                        "type": "integer",
                        "description": "返回最近几场考试的记录，默认 10",
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_student_metric_history",
            "description": "获取该学生每场考试的五大能力指标历史记录（知识、准确、质量、灵活、熟练），按考试时间倒序。",
            "parameters": {
                "type": "object",
                "properties": {
                    "limit": {
                        "type": "integer",
                        "description": "返回最近几场考试的记录，默认 10",
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_exam_submissions",
            "description": "获取该学生在指定考试中的所有提交记录，包含题号、判定结果、得分、语言、用时等。",
            "parameters": {
                "type": "object",
                "properties": {
                    "exam_id": {
                        "type": "integer",
                        "description": "考试 ID（可从 rating_history 或 metric_history 获取）",
                    },
                },
                "required": ["exam_id"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_exam_participants",
            "description": "获取指定考试的参与者排名列表，包含排名、总分、解题数。可用于了解考试整体情况。",
            "parameters": {
                "type": "object",
                "properties": {
                    "exam_id": {
                        "type": "integer",
                        "description": "考试 ID",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "返回前几名参与者，默认 30",
                    },
                },
                "required": ["exam_id"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_exam_info",
            "description": "获取考试基本信息：标题、类型、时间、时长、题目数、参与人数。",
            "parameters": {
                "type": "object",
                "properties": {
                    "exam_id": {
                        "type": "integer",
                        "description": "考试 ID",
                    },
                },
                "required": ["exam_id"],
            },
        },
    },
]


# ---------------------------------------------------------------------------
# Tool executor
# ---------------------------------------------------------------------------


class ToolExecutor:
    """Executes tool calls by dispatching to the appropriate DB query."""

    def __init__(
        self,
        repository: ApiRepository,
        identity: ResolvedIdentity | None,
    ) -> None:
        self._repository = repository
        self._identity = identity

    async def execute(self, tool_name: str, arguments: dict[str, Any]) -> str:
        """Execute a tool call and return a JSON string result."""
        handler = _HANDLERS.get(tool_name)
        if handler is None:
            return json.dumps(
                {"error": f"Unknown tool: {tool_name}"}, ensure_ascii=False
            )
        try:
            result = await handler(self, arguments)
            return json.dumps(result, ensure_ascii=False, default=str)
        except Exception as exc:
            logger.warning(
                "Tool execution error: %s(%s): %s", tool_name, arguments, exc
            )
            return json.dumps(
                {"error": f"Tool execution failed: {exc}"}, ensure_ascii=False
            )

    # ---- Individual handlers ----

    async def _get_student_rating_history(self, args: dict[str, Any]) -> Any:
        if self._identity is None:
            return {"error": "当前未绑定学生身份，无法查询数据。"}
        limit = int(args.get("limit", 10))
        student_ids = self._identity.student_entity_ids or (
            self._identity.student_entity_id,
        )
        all_rows = []
        for sid in student_ids:
            all_rows.extend(
                await self._repository.fetch_rating_history(student_id=sid, limit=limit)
            )
        # Deduplicate by exam_id, keep newest
        seen: set[int] = set()
        deduplicated = []
        for r in sorted(all_rows, key=lambda r: (r.exam_time, r.exam_id), reverse=True):
            if r.exam_id not in seen:
                seen.add(r.exam_id)
                deduplicated.append(r)
            if len(deduplicated) >= limit:
                break
        return [
            {
                "exam_id": r.exam_id,
                "exam_name": r.exam_name,
                "exam_date": r.exam_time.strftime("%Y-%m-%d"),
                "old_rating": r.old_rating,
                "delta": r.delta,
                "new_rating": r.new_rating,
            }
            for r in deduplicated
        ]

    async def _get_student_metric_history(self, args: dict[str, Any]) -> Any:
        if self._identity is None:
            return {"error": "当前未绑定学生身份，无法查询数据。"}
        limit = int(args.get("limit", 10))
        student_ids = self._identity.student_entity_ids or (
            self._identity.student_entity_id,
        )
        all_rows = []
        for sid in student_ids:
            all_rows.extend(
                await self._repository.fetch_exam_metric_history(
                    student_id=sid, limit=limit
                )
            )
        seen: set[int] = set()
        deduplicated = []
        for r in sorted(all_rows, key=lambda r: (r.exam_time, r.exam_id), reverse=True):
            if r.exam_id not in seen:
                seen.add(r.exam_id)
                deduplicated.append(r)
            if len(deduplicated) >= limit:
                break
        return [
            {
                "exam_id": r.exam_id,
                "exam_name": r.exam_name,
                "exam_date": r.exam_time.strftime("%Y-%m-%d"),
                "knowledge": self._safe_int(r.knowledge),
                "accuracy": self._safe_int(r.accuracy),
                "quality": self._safe_int(r.quality),
                "flexibility": self._safe_int(r.flexibility),
                "proficiency": self._safe_int(r.proficiency),
            }
            for r in deduplicated
        ]

    async def _get_exam_submissions(self, args: dict[str, Any]) -> Any:
        if self._identity is None:
            return {"error": "当前未绑定学生身份，无法查询数据。"}
        exam_id = int(args["exam_id"])
        student_ids = self._identity.student_entity_ids or (
            self._identity.student_entity_id,
        )
        all_rows = []
        for sid in student_ids:
            all_rows.extend(
                await self._repository.fetch_exam_submissions_for_student(
                    exam_id=exam_id, student_id=sid
                )
            )
        return [
            {
                "problem_code": r.problem_code,
                "submitted_at": r.submitted_at.isoformat() if r.submitted_at else None,
                "verdict": r.verdict,
                "score": float(r.score) if r.score is not None else None,
                "language": r.language,
                "time_ms": r.time_ms,
                "memory_kb": r.memory_kb,
            }
            for r in all_rows
        ]

    async def _get_exam_participants(self, args: dict[str, Any]) -> Any:
        exam_id = int(args["exam_id"])
        limit = int(args.get("limit", 30))
        rows = await self._repository.fetch_exam_participants_ranked(
            exam_id=exam_id, limit=limit
        )
        return [
            {
                "rank": r.rank,
                "student_name": r.student_name,
                "total_score": float(r.total_score)
                if r.total_score is not None
                else None,
                "solved_count": r.solved_count,
            }
            for r in rows
        ]

    async def _get_exam_info(self, args: dict[str, Any]) -> Any:
        exam_id = int(args["exam_id"])
        row = await self._repository.fetch_exam_info(exam_id)
        if row is None:
            return {"error": f"考试 {exam_id} 不存在。"}
        return {
            "exam_id": row.exam_id,
            "exam_type": row.exam_type,
            "title": row.title,
            "starts_at": row.starts_at.isoformat() if row.starts_at else None,
            "ends_at": row.ends_at.isoformat() if row.ends_at else None,
            "duration_seconds": row.duration_seconds,
            "problem_count": row.problem_count,
            "participant_count": row.participant_count,
        }

    @staticmethod
    def _safe_int(value: Any) -> int | None:
        if value is None:
            return None
        return int(round(float(value)))


# Handler dispatch table
_HANDLERS: dict[str, Any] = {
    "get_student_rating_history": ToolExecutor._get_student_rating_history,
    "get_student_metric_history": ToolExecutor._get_student_metric_history,
    "get_exam_submissions": ToolExecutor._get_exam_submissions,
    "get_exam_participants": ToolExecutor._get_exam_participants,
    "get_exam_info": ToolExecutor._get_exam_info,
}
