from __future__ import annotations

import json
import logging
import re
from dataclasses import dataclass
from typing import Any, AsyncIterator

import httpx

from ..core.config import Settings
from ..core.errors import AppError
from ..schemas.chat import ChatReplyRequest, ChatReplyResponse
from .llm_providers.registry import build_provider_profile, get_adapter
from .llm_providers.types import ProviderRequest
from .tools import TOOL_DEFINITIONS, ToolExecutor

logger = logging.getLogger(__name__)

_MAX_TOOL_ROUNDS = 5
_EXAM_TITLE_MAX_LENGTH = 32


@dataclass(slots=True)
class ParsedTextToolCall:
    tool_name: str
    arguments: dict[str, Any]


@dataclass(slots=True)
class ToolExecutionResult:
    tool_call_id: str
    activity_id: str
    tool_name: str
    arguments: dict[str, Any]
    result: str


class LLMService:
    def __init__(self, settings: Settings, http_client: httpx.AsyncClient) -> None:
        self._settings = settings
        self._http_client = http_client

    async def generate_reply(
        self,
        request: ChatReplyRequest,
        system_prompt: str | None = None,
        tool_executor: ToolExecutor | None = None,
    ) -> ChatReplyResponse:
        profile = self._resolve_active_profile()
        prepared_messages = self._prepare_messages(
            request,
            system_prompt=system_prompt,
            profile=profile,
        )
        reply = await self._complete_with_tools(
            profile=profile,
            messages=prepared_messages,
            tool_executor=tool_executor,
        )
        if not reply.strip():
            raise AppError(
                status_code=502,
                code="EMPTY_LLM_REPLY",
                message="LLM provider returned an empty reply.",
            )
        updated_notes = (
            getattr(tool_executor, "pending_notes_update", None)
            if tool_executor is not None
            else None
        )
        return ChatReplyResponse(
            reply=reply,
            summary=request.summary,
            provider=profile.id,
            model=profile.transport_model,
            requestMode=profile.request_mode,
            updatedNotes=updated_notes,
        )

    async def stream_reply(
        self,
        request: ChatReplyRequest,
        system_prompt: str | None = None,
        tool_executor: ToolExecutor | None = None,
    ) -> AsyncIterator[dict[str, Any]]:
        profile = self._resolve_active_profile()
        prepared_messages = self._prepare_messages(
            request,
            system_prompt=system_prompt,
            profile=profile,
        )
        yield {
            "type": "meta",
            "provider": profile.id,
            "model": profile.transport_model,
            "requestMode": profile.request_mode,
            "summary": request.summary,
        }
        reply = ""
        async for event in self._stream_with_tools(
            profile=profile,
            messages=prepared_messages,
            tool_executor=tool_executor,
        ):
            if event["type"] == "delta":
                reply += str(event.get("text", ""))
            yield event
        if not reply.strip():
            raise AppError(
                status_code=502,
                code="EMPTY_LLM_REPLY",
                message="LLM provider returned an empty reply.",
            )
        done_event: dict[str, Any] = {
            "type": "done",
            "reply": reply,
            "summary": request.summary,
            "provider": profile.id,
            "model": profile.transport_model,
            "requestMode": profile.request_mode,
        }
        pending_notes = (
            getattr(tool_executor, "pending_notes_update", None)
            if tool_executor is not None
            else None
        )
        if pending_notes is not None:
            done_event["updatedNotes"] = pending_notes
        yield done_event

    def _resolve_active_profile(self):
        profile = build_provider_profile(self._settings)
        if not profile.api_key:
            raise AppError(
                status_code=503,
                code="PROVIDER_KEY_MISSING",
                message=f"模型服务未配置 API Key：{profile.api_key_env}",
            )
        return profile

    def _prepare_messages(
        self,
        request: ChatReplyRequest,
        system_prompt: str | None = None,
        profile: Any | None = None,
    ) -> list[dict[str, Any]]:
        prepared: list[dict[str, Any]] = []
        if system_prompt:
            prepared.append({"role": "system", "content": system_prompt})

        summary = request.summary.strip()
        if summary:
            prepared.append(
                {"role": "system", "content": f"Conversation summary:\n{summary}"}
            )

        for message in request.messages:
            content = message.content.strip()
            if not content:
                continue
            prepared_message: dict[str, Any] = {"role": message.role, "content": content}
            reasoning_content = (message.reasoningContent or "").strip()
            if (
                message.role == "assistant"
                and reasoning_content
                and "reasoning_content"
                in getattr(profile, "assistant_passthrough_fields", ())
            ):
                prepared_message["reasoning_content"] = reasoning_content
            prepared.append(prepared_message)

        if not prepared:
            raise AppError(
                status_code=400,
                code="EMPTY_MESSAGES",
                message="messages cannot be empty.",
            )
        return prepared

    async def _complete_with_tools(
        self,
        profile: Any,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor | None,
    ) -> str:
        adapter = get_adapter(profile.adapter)
        for _ in range(_MAX_TOOL_ROUNDS):
            response = await adapter.complete(
                ProviderRequest(
                    profile=profile,
                    messages=messages,
                    tools=TOOL_DEFINITIONS if tool_executor is not None else None,
                    http_client=self._http_client,
                )
            )
            message = response.raw_message
            if tool_executor is None:
                return response.text.strip()

            tool_calls = message.get("tool_calls")
            if isinstance(tool_calls, list) and tool_calls:
                messages.append(self._assistant_replay_message(profile, message))
                await self._append_tool_results(messages, tool_executor, tool_calls)
                continue

            parsed_text_tool_calls = self._extract_text_tool_calls(response.text)
            if parsed_text_tool_calls:
                messages.append(
                    self._assistant_replay_message(
                        profile,
                        message,
                        fallback_content=response.text,
                    )
                )
                await self._append_text_tool_results(
                    messages, tool_executor, parsed_text_tool_calls
                )
                continue

            return response.text.strip()

        response = await adapter.complete(
            ProviderRequest(
                profile=profile,
                messages=messages,
                http_client=self._http_client,
            )
        )
        return response.text.strip()

    async def _stream_with_tools(
        self,
        profile: Any,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor | None,
    ) -> AsyncIterator[dict[str, Any]]:
        adapter = get_adapter(profile.adapter)
        for _ in range(_MAX_TOOL_ROUNDS):
            text_parts: list[str] = []
            reasoning_parts: list[str] = []
            tool_calls_by_index: dict[int, dict[str, Any]] = {}
            async for event in adapter.stream(
                ProviderRequest(
                    profile=profile,
                    messages=messages,
                    tools=TOOL_DEFINITIONS if tool_executor is not None else None,
                    http_client=self._http_client,
                )
            ):
                if event.kind == "delta":
                    text_parts.append(event.text)
                    yield {"type": "delta", "text": event.text}
                    continue
                if event.kind == "reasoning_delta":
                    reasoning_parts.append(event.text)
                    yield {"type": "reasoning_delta", "text": event.text}
                    continue
                if event.kind == "tool_call_delta":
                    self._merge_tool_call_deltas(tool_calls_by_index, event.tool_calls)

            if tool_executor is None or not tool_calls_by_index:
                return

            tool_calls = [
                tool_calls_by_index[index]
                for index in sorted(tool_calls_by_index)
                if tool_calls_by_index[index].get("function", {}).get("name")
            ]
            if not tool_calls:
                return

            assistant_message: dict[str, Any] = {
                "role": "assistant",
                "content": "".join(text_parts) or None,
                "tool_calls": tool_calls,
            }
            reasoning_content = "".join(reasoning_parts)
            if (
                reasoning_content
                and "reasoning_content" in profile.assistant_passthrough_fields
            ):
                assistant_message["reasoning_content"] = reasoning_content
            messages.append(assistant_message)
            async for tool_event in self._append_tool_results_with_activity(
                messages, tool_executor, tool_calls
            ):
                yield tool_event

        async for event in adapter.stream(
            ProviderRequest(
                profile=profile,
                messages=messages,
                http_client=self._http_client,
            )
        ):
            if event.kind == "delta":
                yield {"type": "delta", "text": event.text}
            elif event.kind == "reasoning_delta":
                yield {"type": "reasoning_delta", "text": event.text}

    def _assistant_replay_message(
        self,
        profile: Any,
        message: dict[str, Any],
        fallback_content: str | None = None,
    ) -> dict[str, Any]:
        content = message.get("content")
        if content is None and fallback_content is not None:
            content = fallback_content
        replay: dict[str, Any] = {
            "role": str(message.get("role") or "assistant"),
            "content": content,
        }
        tool_calls = message.get("tool_calls")
        if isinstance(tool_calls, list) and tool_calls:
            replay["tool_calls"] = tool_calls
        for field in getattr(profile, "assistant_passthrough_fields", ()):
            value = message.get(field)
            if value is not None and value != "":
                replay[field] = value
        return replay

    async def _append_tool_results(
        self,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor,
        tool_calls: list[Any],
    ) -> None:
        for tc in tool_calls:
            execution = await self._execute_tool_call(tool_executor, tc, len(messages))
            if execution is None:
                continue
            self._append_tool_message(messages, execution)

    async def _append_tool_results_with_activity(
        self,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor,
        tool_calls: list[Any],
    ) -> AsyncIterator[dict[str, Any]]:
        for index, tc in enumerate(tool_calls):
            parsed = self._parse_tool_call(tc, fallback_index=len(messages) + index)
            if parsed is None:
                continue
            activity_label = self._tool_activity_label(
                parsed.tool_name,
                arguments=parsed.arguments,
            )
            if activity_label is not None:
                yield {
                    "type": "tool_activity_start",
                    "activityId": parsed.activity_id,
                    "label": activity_label,
                    "status": "running",
                }

            execution = await self._execute_parsed_tool_call(tool_executor, parsed)
            self._append_tool_message(messages, execution)

            pending_notes_events = getattr(
                tool_executor, "notes_pending_events", None
            )
            if isinstance(pending_notes_events, list) and pending_notes_events:
                for note_event in pending_notes_events:
                    yield {"type": "notes_update", **note_event}
                pending_notes_events.clear()

            if activity_label is None:
                continue
            done_label = self._tool_activity_label(
                execution.tool_name,
                arguments=execution.arguments,
                result=execution.result,
            ) or activity_label
            if self._tool_result_has_error(execution.result):
                yield {
                    "type": "tool_activity_error",
                    "activityId": execution.activity_id,
                    "label": done_label,
                    "status": "error",
                }
            else:
                yield {
                    "type": "tool_activity_done",
                    "activityId": execution.activity_id,
                    "label": done_label,
                    "status": "done",
                }

    async def _execute_tool_call(
        self,
        tool_executor: ToolExecutor,
        tool_call: Any,
        fallback_index: int,
    ) -> ToolExecutionResult | None:
        parsed = self._parse_tool_call(tool_call, fallback_index=fallback_index)
        if parsed is None:
            return None
        return await self._execute_parsed_tool_call(tool_executor, parsed)

    async def _execute_parsed_tool_call(
        self,
        tool_executor: ToolExecutor,
        parsed: ToolExecutionResult,
    ) -> ToolExecutionResult:
        logger.info("Tool call: %s(%s)", parsed.tool_name, parsed.arguments)
        result = await tool_executor.execute(parsed.tool_name, parsed.arguments)
        return ToolExecutionResult(
            tool_call_id=parsed.tool_call_id,
            activity_id=parsed.activity_id,
            tool_name=parsed.tool_name,
            arguments=parsed.arguments,
            result=result,
        )

    def _parse_tool_call(
        self,
        tool_call: Any,
        *,
        fallback_index: int,
    ) -> ToolExecutionResult | None:
        if not isinstance(tool_call, dict):
            return None
        raw_id = str(tool_call.get("id", "")).strip()
        activity_id = raw_id or f"tool_activity_{fallback_index}"
        func = tool_call.get("function", {})
        tool_name = ""
        arguments: dict[str, Any] = {}
        if isinstance(func, dict):
            tool_name = str(func.get("name", "")).strip()
            arguments = self._parse_tool_arguments(func.get("arguments"))
        if not tool_name:
            return None
        return ToolExecutionResult(
            tool_call_id=raw_id,
            activity_id=activity_id,
            tool_name=tool_name,
            arguments=arguments,
            result="",
        )

    @staticmethod
    def _append_tool_message(
        messages: list[dict[str, Any]],
        execution: ToolExecutionResult,
    ) -> None:
        messages.append(
            {
                "role": "tool",
                "tool_call_id": execution.tool_call_id,
                "content": execution.result,
            }
        )

    @staticmethod
    def _tool_activity_label(
        tool_name: str,
        arguments: dict[str, Any] | None = None,
        result: str | None = None,
    ) -> str | None:
        if tool_name == "get_student_learning_profile":
            return "查看学习画像"
        if tool_name == "get_exam_participant_metrics":
            title = _extract_exam_title(result) if result else None
            return f"查看《{title}》数据" if title else "查看考试数据"
        if tool_name == "get_exam_submissions":
            return "核对提交记录"
        if tool_name == "read_notes":
            if arguments and arguments.get("mode") == "search":
                return "搜索学习笔记"
            return "读取学习笔记"
        if tool_name == "update_notes":
            return "更新学习笔记"
        return None

    @staticmethod
    def _tool_result_has_error(result: str) -> bool:
        try:
            parsed = json.loads(result)
        except json.JSONDecodeError:
            return False
        return isinstance(parsed, dict) and isinstance(parsed.get("error"), str)

    async def _append_text_tool_results(
        self,
        messages: list[dict[str, Any]],
        tool_executor: ToolExecutor,
        parsed_text_tool_calls: list[ParsedTextToolCall],
    ) -> None:
        tool_result_lines: list[str] = []
        for idx, call in enumerate(parsed_text_tool_calls, start=1):
            logger.info("Text tool call: %s(%s)", call.tool_name, call.arguments)
            result = await tool_executor.execute(call.tool_name, call.arguments)
            args_json = json.dumps(call.arguments, ensure_ascii=False)
            tool_result_lines.append(
                f"{idx}. {call.tool_name}({args_json}) => {result}"
            )
        messages.append(
            {
                "role": "user",
                "content": (
                    "以下是你请求的工具结果（JSON）。"
                    "请直接回答用户，不要输出任何函数/工具调用标记。\n"
                    + "\n".join(tool_result_lines)
                ),
            }
        )

    def _merge_tool_call_deltas(
        self,
        target: dict[int, dict[str, Any]],
        deltas: list[dict[str, Any]],
    ) -> None:
        for item in deltas:
            index = int(item.get("index", 0))
            current = target.setdefault(
                index,
                {
                    "id": "",
                    "type": "function",
                    "function": {"name": "", "arguments": ""},
                },
            )
            if item.get("id"):
                current["id"] = str(item["id"])
            if item.get("type"):
                current["type"] = str(item["type"])
            func_delta = item.get("function")
            if isinstance(func_delta, dict):
                func = current.setdefault("function", {"name": "", "arguments": ""})
                if func_delta.get("name"):
                    func["name"] = str(func_delta["name"])
                if func_delta.get("arguments"):
                    func["arguments"] = str(func.get("arguments", "")) + str(
                        func_delta["arguments"]
                    )

    @staticmethod
    def _parse_tool_arguments(raw: Any) -> dict[str, Any]:
        if isinstance(raw, dict):
            return raw
        if not isinstance(raw, str):
            return {}
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError:
            return {}
        if not isinstance(parsed, dict):
            return {}
        return parsed

    def _extract_text_tool_calls(self, content: str) -> list[ParsedTextToolCall]:
        text = content.strip()
        if not text:
            return []

        tag_prefix = r"(?:[|｜]DSML[|｜])?"
        invoke_pattern = re.compile(
            rf"<{tag_prefix}invoke\s+name=\"(?P<name>[A-Za-z0-9_.-]+)\"[^>]*>"
            rf"(?P<body>.*?)</{tag_prefix}invoke>",
            re.DOTALL,
        )
        parameter_pattern = re.compile(
            rf"<{tag_prefix}parameter\s+name=\"(?P<name>[^\"]+)\""
            rf"(?P<attrs>[^>]*)>(?P<value>.*?)</{tag_prefix}parameter>",
            re.DOTALL,
        )

        calls: list[ParsedTextToolCall] = []
        for invoke_match in invoke_pattern.finditer(text):
            tool_name = (invoke_match.group("name") or "").strip()
            if not tool_name:
                continue
            body = invoke_match.group("body") or ""
            arguments: dict[str, Any] = {}
            for param_match in parameter_pattern.finditer(body):
                key = (param_match.group("name") or "").strip()
                if not key:
                    continue
                value = (param_match.group("value") or "").strip()
                attrs = param_match.group("attrs") or ""
                arguments[key] = self._coerce_text_tool_parameter(value, attrs)
            calls.append(ParsedTextToolCall(tool_name=tool_name, arguments=arguments))
        return calls

    @staticmethod
    def _coerce_text_tool_parameter(value: str, attrs: str) -> Any:
        attrs_lower = attrs.lower()
        if 'string="true"' in attrs_lower:
            return value
        if value == "":
            return ""
        if value.startswith("{") or value.startswith("["):
            try:
                parsed = json.loads(value)
                if isinstance(parsed, (dict, list)):
                    return parsed
            except json.JSONDecodeError:
                pass
        lowered = value.lower()
        if lowered == "true":
            return True
        if lowered == "false":
            return False
        if re.fullmatch(r"[+-]?\d+", value):
            try:
                return int(value)
            except ValueError:
                return value
        if re.fullmatch(r"[+-]?\d+\.\d+", value):
            try:
                return float(value)
            except ValueError:
                return value
        return value


def _extract_exam_title(result: str | None) -> str | None:
    if not result:
        return None
    try:
        parsed = json.loads(result)
    except json.JSONDecodeError:
        return None
    if not isinstance(parsed, dict):
        return None
    exam = parsed.get("exam")
    if not isinstance(exam, dict):
        return None
    title = exam.get("title")
    if not isinstance(title, str):
        return None
    cleaned = re.sub(r"\s+", " ", title).strip(" 《》")
    if not cleaned:
        return None
    if len(cleaned) > _EXAM_TITLE_MAX_LENGTH:
        return f"{cleaned[:_EXAM_TITLE_MAX_LENGTH]}..."
    return cleaned
