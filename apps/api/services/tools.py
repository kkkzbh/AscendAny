"""Fact-only function calling tools for the chat Agent."""

from __future__ import annotations

import json
import logging
import re
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

NOTES_MAX_LENGTH = 32_768


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
    {
        "type": "function",
        "function": {
            "name": "read_notes",
            "description": (
                "读取当前激活的长期笔记内容（跨会话持久化、由用户与你共同维护）。"
                "可整篇读取，也可按关键词搜索。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "mode": {
                        "type": "string",
                        "enum": ["full", "search"],
                        "description": "读取模式：full 整篇读取；search 按关键词搜索。默认 full。",
                    },
                    "query": {
                        "type": "string",
                        "description": "搜索关键词。mode 为 search 时必填。",
                    },
                    "max_matches": {
                        "type": "integer",
                        "description": "最多返回多少条命中，默认 10，硬上限 30。",
                    },
                    "context_lines": {
                        "type": "integer",
                        "description": "每条命中前后各返回多少行上下文，默认 2，硬上限 5。",
                    },
                },
                "required": [],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "update_notes",
            "description": (
                "修改当前激活长期笔记。小范围修改使用 unified diff patch；"
                "大幅重构或全文改写使用完整 Markdown 替换。上限 32 KB。"
            ),
            "parameters": {
                "type": "object",
                "properties": {
                    "mode": {
                        "type": "string",
                        "enum": ["patch", "replace"],
                        "description": "写入模式：patch 局部修改；replace 整段替换。",
                    },
                    "patch": {
                        "type": "string",
                        "description": (
                            "mode 为 patch 时必填。标准 unified diff，文件名固定为 notes.md，"
                            "必须包含 --- notes.md、+++ notes.md 和 @@ hunk。"
                        ),
                    },
                    "content": {
                        "type": "string",
                        "description": "mode 为 replace 时必填。笔记的完整 Markdown 文本。",
                    },
                },
                "required": ["mode"],
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
        notes_content: str = "",
        notes_title: str = "",
    ) -> None:
        self._repository = repository
        self._identity = identity
        self.notes_content = notes_content
        self.notes_title = notes_title
        self.pending_notes_update: str | None = None

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

    async def _read_notes(self, args: dict[str, Any]) -> Any:
        mode = args.get("mode", "full")
        if mode not in ("full", "search"):
            return {"error": "参数 mode 必须是 full 或 search。"}
        if mode == "search":
            return _search_notes(self.notes_title, self.notes_content, args)
        return {
            "title": self.notes_title,
            "content": self.notes_content,
            "line_count": len(self.notes_content.splitlines()),
        }

    async def _update_notes(self, args: dict[str, Any]) -> Any:
        mode = args.get("mode")
        if mode == "replace":
            content = args.get("content")
            if not isinstance(content, str):
                return {"error": "replace 模式下参数 content 必须是字符串。"}
            next_content = content
        elif mode == "patch":
            patch = args.get("patch")
            if not isinstance(patch, str):
                return {"error": "patch 模式下参数 patch 必须是字符串。"}
            patch_result = _apply_notes_patch(self.notes_content, patch)
            if isinstance(patch_result, dict):
                return patch_result
            next_content = patch_result
        else:
            return {"error": "参数 mode 必须是 patch 或 replace。"}

        if len(next_content) > NOTES_MAX_LENGTH:
            return {
                "error": (
                    f"笔记内容过长（{len(next_content)} 字符）。"
                    f"上限为 {NOTES_MAX_LENGTH} 字符，请精简后再写入。"
                ),
            }
        self.notes_content = next_content
        self.pending_notes_update = next_content
        return {"ok": True, "mode": mode, "length": len(next_content)}

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


def _search_notes(
    title: str,
    content: str,
    args: dict[str, Any],
) -> dict[str, Any]:
    query = args.get("query")
    if not isinstance(query, str) or not query.strip():
        return {"error": "search 模式下参数 query 必须是非空字符串。"}

    max_matches = _bounded_int(
        args.get("max_matches"),
        default=10,
        min_value=1,
        max_value=30,
    )
    context_lines = _bounded_int(
        args.get("context_lines"),
        default=2,
        min_value=0,
        max_value=5,
    )
    lines = content.splitlines()
    needle = query.strip().casefold()
    matches = []

    for index, line in enumerate(lines):
        if needle not in line.casefold():
            continue
        start = max(0, index - context_lines)
        end = min(len(lines), index + context_lines + 1)
        matches.append(
            {
                "line": index + 1,
                "text": line,
                "before": lines[start:index],
                "after": lines[index + 1 : end],
            }
        )
        if len(matches) >= max_matches:
            break

    return {
        "title": title,
        "query": query.strip(),
        "matches": matches,
        "truncated": len(matches) >= max_matches
        and any(needle in line.casefold() for line in lines[matches[-1]["line"] :]),
    }


_HUNK_HEADER_RE = re.compile(
    r"^@@ -(?P<old_start>\d+)(?:,(?P<old_count>\d+))? "
    r"\+(?P<new_start>\d+)(?:,(?P<new_count>\d+))? @@"
)


def _apply_notes_patch(content: str, patch: str) -> str | dict[str, str]:
    patch_lines = patch.splitlines()
    if len(patch_lines) < 3:
        return {"error": "patch 必须包含文件头和至少一个 hunk。"}
    header_index = next(
        (
            index
            for index, line in enumerate(patch_lines[:-1])
            if line.startswith("--- notes.md")
            and patch_lines[index + 1].startswith("+++ notes.md")
        ),
        -1,
    )
    if header_index < 0:
        return {"error": "patch 必须包含 --- notes.md 和 +++ notes.md 文件头。"}
    if not any(line.startswith("@@") for line in patch_lines[header_index + 2 :]):
        return {"error": "patch 必须包含 @@ hunk。"}

    base_lines = content.splitlines()
    output: list[str] = []
    source_index = 0
    patch_index = header_index + 2

    while patch_index < len(patch_lines):
        header = patch_lines[patch_index]
        match = _HUNK_HEADER_RE.match(header)
        if match is None:
            return {"error": f"无效 hunk 头：{header}"}

        old_start = int(match.group("old_start"))
        target_index = max(0, old_start - 1)
        if target_index < source_index:
            return {"error": "patch hunk 顺序无效。"}
        if target_index > len(base_lines):
            return {"error": "patch hunk 行号超出笔记范围。"}

        output.extend(base_lines[source_index:target_index])
        source_index = target_index
        patch_index += 1

        while patch_index < len(patch_lines) and not patch_lines[
            patch_index
        ].startswith("@@"):
            line = patch_lines[patch_index]
            patch_index += 1
            if line == r"\ No newline at end of file":
                continue
            if not line:
                return {"error": "patch hunk 行缺少操作前缀。"}

            prefix = line[0]
            value = line[1:]
            if prefix == " ":
                if source_index >= len(base_lines) or base_lines[source_index] != value:
                    return {"error": "patch context 不匹配，笔记未修改。"}
                output.append(value)
                source_index += 1
            elif prefix == "-":
                if source_index >= len(base_lines) or base_lines[source_index] != value:
                    return {"error": "patch 删除行不匹配，笔记未修改。"}
                source_index += 1
            elif prefix == "+":
                output.append(value)
            else:
                return {"error": f"无效 patch 行前缀：{prefix}"}

    output.extend(base_lines[source_index:])
    result = "\n".join(output)
    if content.endswith("\n") and output:
        result += "\n"
    return result


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
    "read_notes": ToolExecutor._read_notes,
    "update_notes": ToolExecutor._update_notes,
}
