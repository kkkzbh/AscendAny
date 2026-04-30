"""Fact-only function calling tools for the chat Agent."""

from __future__ import annotations

import json
import logging
from datetime import datetime
from typing import Any

from ..db.repository import ApiRepository
from .history_merge import (
    latest_metrics_row,
    merge_exam_metric_rows,
    merge_rating_history_rows,
    metric_from_rows,
)
from .identity import ResolvedIdentity

logger = logging.getLogger(__name__)

_METRIC_KEYS = ("knowledge", "accuracy", "quality", "flexibility", "proficiency")


TOOL_DEFINITIONS: list[dict[str, Any]] = [
    {
        "type": "function",
        "function": {
            "name": "get_student_learning_profile",
            "description": (
                "获取当前绑定学生的事实型学习画像：身份、当前 Rating/五维指标、"
                "最近考试 Rating 与五维指标历史。只返回数据，不生成分析结论。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "history_limit": {
                        "type": "integer",
                        "description": "返回最近几场考试的历史记录，默认 10。",
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_exam_participant_metrics",
            "description": (
                "获取指定考试的事实型全量榜单与指标：考试信息、未缺考学生的"
                "学号、姓名、排名、总分、解题数、Rating 变化和五维指标。"
                "用于模型自行对比和分析。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "exam_id": {
                        "type": "integer",
                        "description": "考试 ID。",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "最多返回多少名未缺考学生，默认 10000，硬上限 20000。",
                    },
                },
                "required": ["exam_id"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_exam_submissions",
            "description": (
                "获取指定考试中的提交记录。默认查询当前绑定学生；也可用 "
                "student_no 或 student_entity_id 查询指定学生。只返回提交事实。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "exam_id": {
                        "type": "integer",
                        "description": "考试 ID。",
                    },
                    "student_no": {
                        "type": "string",
                        "description": "可选。指定要查询的学生学号。",
                    },
                    "student_entity_id": {
                        "type": "integer",
                        "description": "可选。指定要查询的内部学生实体 ID。",
                    },
                    "limit": {
                        "type": "integer",
                        "description": "最多返回多少条提交记录，默认 100。",
                    },
                },
                "required": ["exam_id"],
            },
        },
    },
]


class ToolExecutor:
    """Executes fact-only tool calls by dispatching to DB queries."""

    def __init__(
        self,
        repository: ApiRepository,
        identity: ResolvedIdentity | None,
    ) -> None:
        self._repository = repository
        self._identity = identity

    async def execute(self, tool_name: str, arguments: dict[str, Any]) -> str:
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

    async def _load_merged_histories(
        self,
        rating_limit: int,
        exam_metric_limit: int,
    ) -> tuple[list[Any], list[Any]]:
        if self._identity is None:
            return [], []
        student_ids = self._identity.student_entity_ids or (
            self._identity.student_entity_id,
        )
        per_student_rating_limit = max(rating_limit, rating_limit * len(student_ids))
        per_student_exam_metric_limit = max(
            exam_metric_limit, exam_metric_limit * len(student_ids)
        )
        rating_rows = []
        exam_metric_rows = []
        for sid in student_ids:
            rating_rows.extend(
                await self._repository.fetch_rating_history(
                    student_id=sid,
                    limit=per_student_rating_limit,
                )
            )
            exam_metric_rows.extend(
                await self._repository.fetch_exam_metric_history(
                    student_id=sid,
                    limit=per_student_exam_metric_limit,
                )
            )
        return (
            merge_rating_history_rows(rating_rows, limit=rating_limit),
            merge_exam_metric_rows(exam_metric_rows, limit=exam_metric_limit),
        )

    async def _get_student_learning_profile(self, args: dict[str, Any]) -> Any:
        if self._identity is None:
            return {"error": "当前未绑定学生身份，无法查询数据。"}

        history_limit = _bounded_int(
            args.get("history_limit"),
            default=10,
            min_value=1,
            max_value=50,
        )
        student_ids = self._identity.student_entity_ids or (
            self._identity.student_entity_id,
        )
        current_metric_rows = []
        for sid in student_ids:
            row = await self._repository.fetch_current_metrics(sid)
            if row is not None:
                current_metric_rows.append(row)

        rating_rows, metric_rows = await self._load_merged_histories(
            rating_limit=history_limit,
            exam_metric_limit=history_limit,
        )
        latest_metrics = latest_metrics_row(current_metric_rows)
        history_by_exam: dict[int, dict[str, Any]] = {}

        for row in rating_rows:
            history_by_exam[row.exam_id] = {
                "exam_id": row.exam_id,
                "exam_name": row.exam_name,
                "exam_date": _date_str(row.exam_time),
                "old_rating": row.old_rating,
                "delta": row.delta,
                "new_rating": row.new_rating,
                "knowledge": None,
                "accuracy": None,
                "quality": None,
                "flexibility": None,
                "proficiency": None,
                "_sort_time": row.exam_time,
            }

        for row in metric_rows:
            item = history_by_exam.setdefault(
                row.exam_id,
                {
                    "exam_id": row.exam_id,
                    "exam_name": row.exam_name,
                    "exam_date": _date_str(row.exam_time),
                    "old_rating": None,
                    "delta": None,
                    "new_rating": None,
                    "_sort_time": row.exam_time,
                },
            )
            item["exam_name"] = row.exam_name
            item["exam_date"] = _date_str(row.exam_time)
            item["_sort_time"] = max(
                item.get("_sort_time", row.exam_time),
                row.exam_time,
            )
            for key in _METRIC_KEYS:
                item[key] = _safe_int(getattr(row, key))

        history = sorted(
            history_by_exam.values(),
            key=lambda item: (item.get("_sort_time") or datetime.min, item["exam_id"]),
            reverse=True,
        )[:history_limit]
        for item in history:
            item.pop("_sort_time", None)

        return {
            "student": {
                "student_entity_id": self._identity.student_entity_id,
                "student_entity_ids": list(student_ids),
                "student_no": self._identity.student_id,
                "pta_nickname": self._identity.pta_nickname,
                "matched_by": self._identity.matched_by,
            },
            "current": {
                "rating": latest_metrics.rating if latest_metrics else 800,
                "knowledge": _safe_int(
                    metric_from_rows(current_metric_rows, "knowledge")
                ),
                "accuracy": _safe_int(
                    metric_from_rows(current_metric_rows, "accuracy")
                ),
                "quality": _safe_int(metric_from_rows(current_metric_rows, "quality")),
                "flexibility": _safe_int(
                    metric_from_rows(current_metric_rows, "flexibility")
                ),
                "proficiency": _safe_int(
                    metric_from_rows(current_metric_rows, "proficiency")
                ),
            },
            "history": history,
        }

    async def _get_exam_participant_metrics(self, args: dict[str, Any]) -> Any:
        exam_id = int(args["exam_id"])
        limit = _bounded_int(
            args.get("limit"),
            default=10000,
            min_value=1,
            max_value=20000,
        )
        exam = await self._repository.fetch_exam_info(exam_id)
        if exam is None:
            return {"error": f"考试 {exam_id} 不存在。"}

        rows = await self._repository.fetch_exam_participant_metrics(
            exam_id=exam_id,
            limit=limit,
        )
        current_student_ids = set()
        if self._identity is not None:
            current_student_ids = set(
                self._identity.student_entity_ids
                or (self._identity.student_entity_id,)
            )
        participant_count = (
            rows[0].total_participants if rows else exam.participant_count
        )

        return {
            "exam": {
                "exam_id": exam.exam_id,
                "exam_type": exam.exam_type,
                "title": exam.title,
                "starts_at": exam.starts_at.isoformat() if exam.starts_at else None,
                "ends_at": exam.ends_at.isoformat() if exam.ends_at else None,
                "duration_seconds": exam.duration_seconds,
                "problem_count": exam.problem_count,
                "participant_count": participant_count,
            },
            "participant_count": participant_count,
            "returned_count": len(rows),
            "truncated": len(rows) < participant_count,
            "participants": [
                {
                    "student_entity_id": row.student_entity_id,
                    "student_no": row.student_no,
                    "student_name": row.student_name,
                    "rank": row.rank,
                    "total_score": _safe_float(row.total_score),
                    "solved_count": row.solved_count,
                    "old_rating": row.old_rating,
                    "new_rating": row.new_rating,
                    "rating_delta": row.rating_delta,
                    "knowledge": _safe_int(row.knowledge),
                    "accuracy": _safe_int(row.accuracy),
                    "quality": _safe_int(row.quality),
                    "flexibility": _safe_int(row.flexibility),
                    "proficiency": _safe_int(row.proficiency),
                    "is_current_student": row.student_entity_id in current_student_ids,
                }
                for row in rows
            ],
        }

    async def _get_exam_submissions(self, args: dict[str, Any]) -> Any:
        exam_id = int(args["exam_id"])
        limit = _bounded_int(
            args.get("limit"),
            default=100,
            min_value=1,
            max_value=1000,
        )
        student_ids_result = await self._resolve_submission_student_ids(args)
        if isinstance(student_ids_result, dict):
            return student_ids_result

        all_rows = []
        for sid in student_ids_result:
            all_rows.extend(
                await self._repository.fetch_exam_submissions_for_student(
                    exam_id=exam_id,
                    student_id=sid,
                    limit=limit,
                )
            )

        all_rows = sorted(
            all_rows,
            key=lambda row: (
                row.submitted_at.isoformat() if row.submitted_at else "",
                row.submission_id,
            ),
        )[:limit]
        return {
            "exam_id": exam_id,
            "student_entity_ids": student_ids_result,
            "returned_count": len(all_rows),
            "submissions": [
                {
                    "problem_code": row.problem_code,
                    "submitted_at": row.submitted_at.isoformat()
                    if row.submitted_at
                    else None,
                    "verdict": row.verdict,
                    "score": _safe_float(row.score),
                    "language": row.language,
                    "time_ms": row.time_ms,
                    "memory_kb": row.memory_kb,
                }
                for row in all_rows
            ],
        }

    async def _resolve_submission_student_ids(
        self, args: dict[str, Any]
    ) -> list[int] | dict[str, str]:
        if args.get("student_entity_id") is not None:
            return [int(args["student_entity_id"])]

        student_no = str(args.get("student_no", "")).strip()
        if student_no:
            matches = await self._repository.find_students_by_student_no(student_no)
            if not matches:
                return {"error": f"未找到学号 {student_no} 对应的学生。"}
            return list(dict.fromkeys(match.student_id for match in matches))

        if self._identity is None:
            return {"error": "当前未绑定学生身份，无法查询数据。"}
        return list(
            self._identity.student_entity_ids or (self._identity.student_entity_id,)
        )


def _bounded_int(
    value: Any,
    *,
    default: int,
    min_value: int,
    max_value: int,
) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        parsed = default
    return max(min_value, min(parsed, max_value))


def _date_str(value: datetime) -> str:
    return value.strftime("%Y-%m-%d")


def _safe_int(value: Any) -> int | None:
    if value is None:
        return None
    return int(round(float(value)))


def _safe_float(value: Any) -> float | None:
    if value is None:
        return None
    return float(value)


_HANDLERS: dict[str, Any] = {
    "get_student_learning_profile": ToolExecutor._get_student_learning_profile,
    "get_exam_participant_metrics": ToolExecutor._get_exam_participant_metrics,
    "get_exam_submissions": ToolExecutor._get_exam_submissions,
}
