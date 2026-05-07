from __future__ import annotations

import asyncio
import json
import httpx
from collections.abc import AsyncIterator

from apps.api.core.config import LLMProviderConfig, Settings
from apps.api.schemas.chat import ChatMessageRequest, ChatReplyRequest
from apps.api.services.llm import LLMService
from apps.api.services.tools import ToolExecutor


class _NullRepo:
    def __getattr__(self, name: str):
        raise AssertionError(f"Notes tool tests should not query repository: {name}")


def _build_service(settings: Settings) -> LLMService:
    return LLMService(
        settings=settings,
        http_client=httpx.AsyncClient(),
    )


def test_active_provider_uses_provider_config(monkeypatch) -> None:
    settings = Settings()
    settings.llm.active_provider = "openai"
    settings.llm.providers["openai"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="openai/gpt-5.4-high-thinking",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "secret")
    service = _build_service(settings)

    provider = service._resolve_active_profile()

    assert provider.base_url == "https://llm.example.com/v1"
    assert provider.model == "openai/gpt-5.4-high-thinking"
    assert provider.transport_model == "gpt-5.4-high-thinking"
    assert provider.api_key == "secret"


def test_prepare_messages_replays_reasoning_for_reasoning_passthrough_providers(
    monkeypatch,
) -> None:
    request = ChatReplyRequest(
        messages=[
            ChatMessageRequest(role="user", content="第一轮问题"),
            ChatMessageRequest(
                role="assistant",
                content="第一轮回答",
                reasoningContent="第一轮工具调用的思考内容",
            ),
            ChatMessageRequest(role="user", content="第二轮问题"),
        ],
        summary="",
    )

    deepseek_settings = Settings()
    deepseek_settings.llm.active_provider = "deepseek"
    deepseek_settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    openai_settings = Settings()
    openai_settings.llm.active_provider = "openai"
    openai_settings.llm.providers["openai"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="openai/gpt-5.4-medium-thinking",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    mimo_settings = Settings()
    mimo_settings.llm.active_provider = "mimo"
    mimo_settings.llm.providers["mimo"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="mimo-v2.5-pro",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "secret")

    deepseek_service = _build_service(deepseek_settings)
    deepseek_profile = deepseek_service._resolve_active_profile()
    deepseek_messages = deepseek_service._prepare_messages(
        request,
        profile=deepseek_profile,
    )
    assert deepseek_messages[1] == {
        "role": "assistant",
        "content": "第一轮回答",
        "reasoning_content": "第一轮工具调用的思考内容",
    }

    mimo_service = _build_service(mimo_settings)
    mimo_profile = mimo_service._resolve_active_profile()
    mimo_messages = mimo_service._prepare_messages(
        request,
        profile=mimo_profile,
    )
    assert mimo_messages[1] == {
        "role": "assistant",
        "content": "第一轮回答",
        "reasoning_content": "第一轮工具调用的思考内容",
    }

    openai_service = _build_service(openai_settings)
    openai_profile = openai_service._resolve_active_profile()
    openai_messages = openai_service._prepare_messages(
        request,
        profile=openai_profile,
    )
    assert openai_messages[1] == {
        "role": "assistant",
        "content": "第一轮回答",
    }


def test_public_tool_activity_labels_do_not_expose_tool_names() -> None:
    cases = {
        "get_student_learning_profile": "查看学习画像",
        "get_exam_submissions": "核对提交记录",
        "get_problem_recommendations": "获取题目推荐",
        "get_learning_path": "获取学习路径",
        "read_notes": "读取学习笔记",
        "update_notes": "更新学习笔记",
    }

    for tool_name, label in cases.items():
        assert LLMService._tool_activity_label(tool_name) == label
        assert tool_name not in label

    assert (
        LLMService._tool_activity_label(
            "read_notes",
            arguments={"mode": "search", "query": "图论"},
        )
        == "搜索学习笔记"
    )

    exam_result = json.dumps(
        {"exam": {"title": "数据结构第三次实验", "exam_id": 32}},
        ensure_ascii=False,
    )
    assert (
        LLMService._tool_activity_label(
            "get_exam_participant_metrics",
            result=exam_result,
        )
        == "查看《数据结构第三次实验》数据"
    )
    assert LLMService._tool_activity_label("unknown_tool") is None


class FakeToolExecutor:
    def __init__(self) -> None:
        self.calls: list[tuple[str, dict[str, object]]] = []

    async def execute(self, tool_name: str, arguments: dict[str, object]) -> str:
        self.calls.append((tool_name, arguments))
        return json.dumps(
            {
                "exam": {
                    "exam_id": 32,
                    "title": "数据结构第三次实验",
                },
                "exam_id": 32,
                "participants": [
                    {"student_no": "20231202019", "rank": 7},
                    {"student_no": "20231202001", "rank": 6},
                ],
            },
            ensure_ascii=False,
        )


class _SseStream(httpx.AsyncByteStream):
    def __init__(self, chunks: list[str]) -> None:
        self._chunks = chunks

    async def __aiter__(self) -> AsyncIterator[bytes]:
        for chunk in self._chunks:
            yield chunk.encode("utf-8")


def test_openai_path_executes_textual_dsml_tool_calls(monkeypatch) -> None:
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "content": (
                                    "让我获取你的能力指标详情。\n"
                                    "<｜DSML｜function_calls>\n"
                                    "<｜DSML｜invoke name=\"get_exam_participant_metrics\">\n"
                                    "<｜DSML｜parameter name=\"exam_id\" string=\"false\">32</｜DSML｜parameter>\n"
                                    "</｜DSML｜invoke>\n"
                                    "</｜DSML｜function_calls>"
                                ),
                            }
                        }
                    ]
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [
                    {
                        "message": {
                            "role": "assistant",
                            "content": "你在本场第 7 名，和上一名相差 1 名。",
                        }
                    }
                ]
            },
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "secret")

    async def run_case() -> tuple[str, FakeToolExecutor]:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            tool_executor = FakeToolExecutor()
            response = await service.generate_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="我第几名？")],
                    summary="",
                ),
                system_prompt="test prompt",
                tool_executor=tool_executor,  # type: ignore[arg-type]
            )
            return response.reply, tool_executor

    reply, tool_executor = asyncio.run(run_case())

    assert reply == "你在本场第 7 名，和上一名相差 1 名。"
    assert tool_executor.calls == [
        (
            "get_exam_participant_metrics",
            {
                "exam_id": 32,
            },
        )
    ]
    assert len(requests_payload) == 2
    second_messages = requests_payload[1]["messages"]
    assert isinstance(second_messages, list)
    assert any(
        isinstance(item, dict)
        and item.get("role") == "user"
        and "工具结果" in str(item.get("content"))
        for item in second_messages
    )
def test_openai_path_executes_standard_tool_call(monkeypatch) -> None:
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "content": None,
                                "tool_calls": [
                                    {
                                        "id": "call_1",
                                        "type": "function",
                                        "function": {
                                            "name": "get_exam_participant_metrics",
                                            "arguments": json.dumps(
                                                {
                                                    "exam_id": 32,
                                                }
                                            ),
                                        },
                                    }
                                ],
                            }
                        }
                    ]
                },
            )
        # Second response: model produces final text
        return httpx.Response(
            200,
            json={
                "choices": [
                    {
                        "message": {
                            "role": "assistant",
                            "content": "你的综合能力评分为 85 分。",
                        }
                    }
                ]
            },
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> tuple[str, FakeToolExecutor]:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            tool_executor = FakeToolExecutor()
            response = await service.generate_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="我的能力评分是多少？")],
                    summary="",
                ),
                system_prompt="test system prompt",
                tool_executor=tool_executor,  # type: ignore[arg-type]
            )
            return response.reply, tool_executor

    reply, tool_executor = asyncio.run(run_case())

    assert reply == "你的综合能力评分为 85 分。"
    assert len(tool_executor.calls) == 1
    assert tool_executor.calls[0][0] == "get_exam_participant_metrics"
    assert len(requests_payload) == 2

    first = requests_payload[0]
    assert "tools" in first

    second_messages = requests_payload[1].get("messages")
    assert isinstance(second_messages, list)
    assert any(
        isinstance(message, dict)
        and message.get("role") == "tool"
        and message.get("tool_call_id") == "call_1"
        for message in second_messages
    )


def test_deepseek_standard_tool_call_replays_reasoning_content(monkeypatch) -> None:
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "content": None,
                                "reasoning_content": "需要先查本场考试全量榜单。",
                                "tool_calls": [
                                    {
                                        "id": "call_1",
                                        "type": "function",
                                        "function": {
                                            "name": "get_exam_participant_metrics",
                                            "arguments": json.dumps({"exam_id": 32}),
                                        },
                                    }
                                ],
                            }
                        }
                    ]
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [
                    {"message": {"role": "assistant", "content": "你在本场第 7 名。"}}
                ]
            },
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> str:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            response = await service.generate_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="分析这场考试")],
                    summary="",
                ),
                system_prompt="test system prompt",
                tool_executor=FakeToolExecutor(),  # type: ignore[arg-type]
            )
            return response.reply

    reply = asyncio.run(run_case())

    assert reply == "你在本场第 7 名。"
    assert len(requests_payload) == 2
    second_messages = requests_payload[1]["messages"]
    assert isinstance(second_messages, list)
    replayed_assistant = next(
        item
        for item in second_messages
        if isinstance(item, dict) and item.get("role") == "assistant"
    )
    assert replayed_assistant["reasoning_content"] == "需要先查本场考试全量榜单。"
    assert "tool_calls" in replayed_assistant


def test_non_deepseek_tool_replay_drops_reasoning_content(monkeypatch) -> None:
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "content": None,
                                "reasoning_content": "provider-specific hidden field",
                                "tool_calls": [
                                    {
                                        "id": "call_1",
                                        "type": "function",
                                        "function": {
                                            "name": "get_exam_participant_metrics",
                                            "arguments": json.dumps({"exam_id": 32}),
                                        },
                                    }
                                ],
                            }
                        }
                    ]
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [
                    {"message": {"role": "assistant", "content": "OpenAI 兼容路径正常。"}}
                ]
            },
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "openai"
    settings.llm.providers["openai"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="openai/gpt-5.4-medium-thinking",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> str:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            response = await service.generate_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="分析考试")],
                    summary="",
                ),
                system_prompt="test system prompt",
                tool_executor=FakeToolExecutor(),  # type: ignore[arg-type]
            )
            return response.reply

    reply = asyncio.run(run_case())

    assert reply == "OpenAI 兼容路径正常。"
    second_messages = requests_payload[1]["messages"]
    assert isinstance(second_messages, list)
    replayed_assistant = next(
        item
        for item in second_messages
        if isinstance(item, dict) and item.get("role") == "assistant"
    )
    assert "reasoning_content" not in replayed_assistant


def test_deepseek_textual_dsml_tool_call_replays_reasoning_content(monkeypatch) -> None:
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            return httpx.Response(
                200,
                json={
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "reasoning_content": "先转成文本工具调用。",
                                "content": (
                                    "<｜DSML｜invoke name=\"get_exam_participant_metrics\">"
                                    "<｜DSML｜parameter name=\"exam_id\" string=\"false\">32</｜DSML｜parameter>"
                                    "</｜DSML｜invoke>"
                                ),
                            }
                        }
                    ]
                },
            )
        return httpx.Response(
            200,
            json={
                "choices": [
                    {"message": {"role": "assistant", "content": "工具结果已收到。"}}
                ]
            },
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> str:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            response = await service.generate_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="查榜单")],
                    summary="",
                ),
                system_prompt="test prompt",
                tool_executor=FakeToolExecutor(),  # type: ignore[arg-type]
            )
            return response.reply

    reply = asyncio.run(run_case())

    assert reply == "工具结果已收到。"
    second_messages = requests_payload[1]["messages"]
    assert isinstance(second_messages, list)
    replayed_assistant = next(
        item
        for item in second_messages
        if isinstance(item, dict) and item.get("role") == "assistant"
    )
    assert replayed_assistant["reasoning_content"] == "先转成文本工具调用。"
    assert "DSML" in str(replayed_assistant["content"])


def test_streaming_deepseek_reasoning_delta_and_tool_replay(monkeypatch) -> None:
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            tool_arguments = json.dumps({"exam_id": 32})
            return httpx.Response(
                200,
                headers={"Content-Type": "text/event-stream"},
                stream=_SseStream(
                    [
                        'data: {"choices":[{"delta":{"reasoning_content":"先看"}}]}\n\n',
                        'data: {"choices":[{"delta":{"reasoning_content":"榜单。"}}]}\n\n',
                        (
                            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,'
                            '"id":"call_1","type":"function","function":{'
                            '"name":"get_exam_participant_metrics","arguments":'
                            f'{json.dumps(tool_arguments)}'
                            "}}]}}]}\n\n"
                        ),
                        "data: [DONE]\n\n",
                    ]
                ),
            )
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=_SseStream(
                [
                    'data: {"choices":[{"delta":{"content":"最终结论"}}]}\n\n',
                    "data: [DONE]\n\n",
                ]
            ),
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> list[dict[str, object]]:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            events: list[dict[str, object]] = []
            async for event in service.stream_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="分析考试")],
                    summary="",
                ),
                system_prompt="test prompt",
                tool_executor=FakeToolExecutor(),  # type: ignore[arg-type]
            ):
                events.append(event)
            return events

    events = asyncio.run(run_case())

    assert {"type": "reasoning_delta", "text": "先看"} in events
    assert {"type": "reasoning_delta", "text": "榜单。"} in events
    assert {
        "type": "tool_activity_start",
        "activityId": "call_1",
        "label": "查看考试数据",
        "status": "running",
    } in events
    assert {
        "type": "tool_activity_done",
        "activityId": "call_1",
        "label": "查看《数据结构第三次实验》数据",
        "status": "done",
    } in events
    assert {"type": "delta", "text": "最终结论"} in events
    assert events[-1]["type"] == "done"
    assert events[-1]["reply"] == "最终结论"
    assert not any(event.get("type") in {"tool_start", "tool_done"} for event in events)
    assert not any(
        "get_exam_participant_metrics" in json.dumps(event, ensure_ascii=False)
        for event in events
    )
    second_messages = requests_payload[1]["messages"]
    assert isinstance(second_messages, list)
    replayed_assistant = next(
        item
        for item in second_messages
        if isinstance(item, dict) and item.get("role") == "assistant"
    )
    assert replayed_assistant["reasoning_content"] == "先看榜单。"
    assert replayed_assistant["tool_calls"]


def test_streaming_problem_recommendation_tool_activity(monkeypatch) -> None:
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            tool_arguments = json.dumps({"top_k": 5})
            return httpx.Response(
                200,
                headers={"Content-Type": "text/event-stream"},
                stream=_SseStream(
                    [
                        (
                            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,'
                            '"id":"call_rec","type":"function","function":{'
                            '"name":"get_problem_recommendations","arguments":'
                            f'{json.dumps(tool_arguments)}'
                            "}}]}}]}\n\n"
                        ),
                        "data: [DONE]\n\n",
                    ]
                ),
            )
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=_SseStream(
                [
                    'data: {"choices":[{"delta":{"content":"已给出推荐。"}}]}\n\n',
                    "data: [DONE]\n\n",
                ]
            ),
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> tuple[list[dict[str, object]], FakeToolExecutor]:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            tool_executor = FakeToolExecutor()
            events: list[dict[str, object]] = []
            async for event in service.stream_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="推荐几道题")],
                    summary="",
                ),
                system_prompt="test prompt",
                tool_executor=tool_executor,  # type: ignore[arg-type]
            ):
                events.append(event)
            return events, tool_executor

    events, tool_executor = asyncio.run(run_case())

    assert tool_executor.calls == [("get_problem_recommendations", {"top_k": 5})]
    assert {
        "type": "tool_activity_start",
        "activityId": "call_rec",
        "label": "获取题目推荐",
        "status": "running",
    } in events
    assert {
        "type": "tool_activity_done",
        "activityId": "call_rec",
        "label": "获取题目推荐",
        "status": "done",
    } in events
    assert {"type": "delta", "text": "已给出推荐。"} in events
    assert events[-1]["type"] == "done"
    assert events[-1]["reply"] == "已给出推荐。"
    assert len(requests_payload) == 2


def test_streaming_interleaves_text_and_tool_across_rounds(monkeypatch) -> None:
    """每一轮 LLM 调用都先吐 content delta 再发 tool_calls，最终事件流按时序交错。"""
    request_count = {"value": 0}

    def handler(_request: httpx.Request) -> httpx.Response:
        request_count["value"] += 1
        round_index = request_count["value"]
        if round_index == 1:
            tool_args = json.dumps({"exam_id": 32})
            return httpx.Response(
                200,
                headers={"Content-Type": "text/event-stream"},
                stream=_SseStream(
                    [
                        'data: {"choices":[{"delta":{"content":"先瞅瞅"}}]}\n\n',
                        'data: {"choices":[{"delta":{"content":"画像哈"}}]}\n\n',
                        (
                            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,'
                            '"id":"call_1","type":"function","function":{'
                            '"name":"get_exam_participant_metrics","arguments":'
                            f"{json.dumps(tool_args)}"
                            "}}]}}]}\n\n"
                        ),
                        "data: [DONE]\n\n",
                    ]
                ),
            )
        if round_index == 2:
            tool_args = json.dumps({"exam_id": 32})
            return httpx.Response(
                200,
                headers={"Content-Type": "text/event-stream"},
                stream=_SseStream(
                    [
                        'data: {"choices":[{"delta":{"content":"再翻下"}}]}\n\n',
                        (
                            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,'
                            '"id":"call_2","type":"function","function":{'
                            '"name":"get_exam_submissions","arguments":'
                            f"{json.dumps(tool_args)}"
                            "}}]}}]}\n\n"
                        ),
                        "data: [DONE]\n\n",
                    ]
                ),
            )
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=_SseStream(
                [
                    'data: {"choices":[{"delta":{"content":"分析完了"}}]}\n\n',
                    "data: [DONE]\n\n",
                ]
            ),
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> list[dict[str, object]]:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            events: list[dict[str, object]] = []
            async for event in service.stream_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="看看考试")],
                    summary="",
                ),
                system_prompt="test prompt",
                tool_executor=FakeToolExecutor(),  # type: ignore[arg-type]
            ):
                events.append(event)
            return events

    events = asyncio.run(run_case())

    # Filter out meta/done so we can assert on the streaming-segment ordering.
    timeline = [
        event["type"]
        for event in events
        if event.get("type")
        in {"delta", "tool_activity_start", "tool_activity_done"}
    ]

    # First round: two text deltas, then a tool start+done.
    # Second round: one text delta, then a tool start+done.
    # Final round: one text delta.
    expected_prefix = [
        "delta",
        "delta",
        "tool_activity_start",
        "tool_activity_done",
        "delta",
        "tool_activity_start",
        "tool_activity_done",
        "delta",
    ]
    assert timeline == expected_prefix, timeline

    delta_texts = [event["text"] for event in events if event.get("type") == "delta"]
    assert delta_texts == ["先瞅瞅", "画像哈", "再翻下", "分析完了"]
    assert events[-1]["type"] == "done"
    assert events[-1]["reply"] == "先瞅瞅画像哈再翻下分析完了"


def test_streaming_emits_notes_update_before_tool_activity_done(monkeypatch) -> None:
    request_count = {"value": 0}

    def handler(_request: httpx.Request) -> httpx.Response:
        request_count["value"] += 1
        if request_count["value"] == 1:
            tool_args = json.dumps({"mode": "replace", "content": "新版笔记"})
            return httpx.Response(
                200,
                headers={"Content-Type": "text/event-stream"},
                stream=_SseStream(
                    [
                        (
                            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,'
                            '"id":"call_notes_1","type":"function","function":{'
                            '"name":"update_notes","arguments":'
                            f"{json.dumps(tool_args)}"
                            "}}]}}]}\n\n"
                        ),
                        "data: [DONE]\n\n",
                    ]
                ),
            )
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=_SseStream(
                [
                    'data: {"choices":[{"delta":{"content":"已更新"}}]}\n\n',
                    "data: [DONE]\n\n",
                ]
            ),
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> list[dict[str, object]]:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            tool_executor = ToolExecutor(
                repository=_NullRepo(),  # type: ignore[arg-type]
                identity=None,
                notes_content="旧版笔记",
                notes_title="学习笔记",
            )
            events: list[dict[str, object]] = []
            async for event in service.stream_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="更新笔记")],
                    summary="",
                ),
                system_prompt="test prompt",
                tool_executor=tool_executor,
            ):
                events.append(event)
            return events

    events = asyncio.run(run_case())

    types_in_order = [event.get("type") for event in events]
    notes_index = types_in_order.index("notes_update")
    done_index = types_in_order.index("tool_activity_done")
    assert notes_index < done_index, types_in_order

    notes_event = events[notes_index]
    assert notes_event["mode"] == "replace"
    assert notes_event["previous"] == "旧版笔记"
    assert notes_event["next"] == "新版笔记"
    assert notes_event["patch"] is None

    final = events[-1]
    assert final["type"] == "done"
    assert final.get("updatedNotes") == "新版笔记"


def test_streaming_skips_notes_update_when_locked(monkeypatch) -> None:
    request_count = {"value": 0}

    def handler(_request: httpx.Request) -> httpx.Response:
        request_count["value"] += 1
        if request_count["value"] == 1:
            tool_args = json.dumps({"mode": "replace", "content": "新版笔记"})
            return httpx.Response(
                200,
                headers={"Content-Type": "text/event-stream"},
                stream=_SseStream(
                    [
                        (
                            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,'
                            '"id":"call_notes_1","type":"function","function":{'
                            '"name":"update_notes","arguments":'
                            f"{json.dumps(tool_args)}"
                            "}}]}}]}\n\n"
                        ),
                        "data: [DONE]\n\n",
                    ]
                ),
            )
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=_SseStream(
                [
                    'data: {"choices":[{"delta":{"content":"好的"}}]}\n\n',
                    "data: [DONE]\n\n",
                ]
            ),
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.active_provider = "deepseek"
    settings.llm.providers["deepseek"] = LLMProviderConfig(
        adapter="openai_compatible",
        base_url="https://llm.example.com/v1",
        model="deepseek-v4-flash",
        api_key_env="SERVER_DEFAULT_TEST_KEY",
    )
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "test-key")

    async def run_case() -> tuple[list[dict[str, object]], ToolExecutor]:
        async with httpx.AsyncClient(transport=transport) as client:
            service = LLMService(settings=settings, http_client=client)
            tool_executor = ToolExecutor(
                repository=_NullRepo(),  # type: ignore[arg-type]
                identity=None,
                notes_content="旧版笔记",
                notes_locked=True,
            )
            events: list[dict[str, object]] = []
            async for event in service.stream_reply(
                ChatReplyRequest(
                    messages=[ChatMessageRequest(role="user", content="更新笔记")],
                    summary="",
                ),
                system_prompt="test prompt",
                tool_executor=tool_executor,
            ):
                events.append(event)
            return events, tool_executor

    events, executor = asyncio.run(run_case())

    assert not any(event.get("type") == "notes_update" for event in events)
    assert any(event.get("type") == "tool_activity_error" for event in events)
    assert executor.notes_content == "旧版笔记"
    assert executor.pending_notes_update is None
    assert events[-1].get("updatedNotes") is None
