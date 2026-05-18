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
from .tools import TOOL_CONTRACTS_BY_NAME, TOOL_DEFINITIONS, ToolExecutor

logger = logging.getLogger(__name__)

_MAX_TOOL_ROUNDS = 5
_EXAM_TITLE_MAX_LENGTH = 32


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
    ) -> dict[str, Any]:
        content = message.get("content")
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
        contract = TOOL_CONTRACTS_BY_NAME.get(tool_name)
        if contract is not None:
            return contract.activity_label
        return None

    @staticmethod
    def _tool_result_has_error(result: str) -> bool:
        try:
            parsed = json.loads(result)
        except json.JSONDecodeError:
            return False
        return isinstance(parsed, dict) and isinstance(parsed.get("error"), str)

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
