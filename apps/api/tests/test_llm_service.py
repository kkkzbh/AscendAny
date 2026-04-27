from __future__ import annotations

import asyncio
import json
from typing import cast

import httpx

from apps.api.core.config import Settings
from apps.api.schemas.chat import ChatMessageRequest, ChatReplyRequest
from apps.api.services.llm import LLMService


def _build_service(settings: Settings) -> LLMService:
    return LLMService(
        settings=settings,
        http_client=cast(httpx.AsyncClient, object()),
    )


def test_server_default_provider_uses_dedicated_config(monkeypatch) -> None:
    settings = Settings()
    settings.llm.server_default.base_url = "https://llm.example.com/v1"
    settings.llm.server_default.model = "server-default-model"
    settings.llm.server_default.api_key_env = "SERVER_DEFAULT_TEST_KEY"
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "secret")
    service = _build_service(settings)

    provider = service._resolve_server_default_provider()

    assert provider.base_url == "https://llm.example.com/v1"
    assert provider.model == "server-default-model"
    assert provider.api_key == "secret"


class FakeToolExecutor:
    def __init__(self) -> None:
        self.calls: list[tuple[str, dict[str, object]]] = []

    async def execute(self, tool_name: str, arguments: dict[str, object]) -> str:
        self.calls.append((tool_name, arguments))
        return json.dumps(
            {
                "exam_id": 32,
                "me": {"rank": 7},
                "previous_ranker": {"rank": 6},
            },
            ensure_ascii=False,
        )


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
                                    "<｜DSML｜invoke name=\"get_student_ability_scores\">\n"
                                    "<｜DSML｜parameter name=\"exam_id\" string=\"false\">32</｜DSML｜parameter>\n"
                                    "<｜DSML｜parameter name=\"student_id\" string=\"false\">20231202019</｜DSML｜parameter>\n"
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
    settings.llm.server_default.base_url = "https://llm.example.com/v1"
    settings.llm.server_default.model = "mock-model"
    settings.llm.server_default.api_key_env = "SERVER_DEFAULT_TEST_KEY"
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
            "get_student_ability_scores",
            {
                "exam_id": 32,
                "student_id": 20231202019,
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


def test_gemini_path_executes_tool_call(monkeypatch) -> None:
    """Server-default Gemini path: function call round-trip produces final text reply."""
    requests_payload: list[dict[str, object]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content.decode("utf-8"))
        requests_payload.append(payload)
        if len(requests_payload) == 1:
            # First response: model requests a function call
            return httpx.Response(
                200,
                json={
                    "candidates": [
                        {
                            "content": {
                                "role": "model",
                                "parts": [
                                    {
                                        "functionCall": {
                                            "name": "get_student_ability_scores",
                                            "args": {
                                                "exam_id": 32,
                                                "student_id": "20231202019",
                                            },
                                        }
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
                "candidates": [
                    {
                        "content": {
                            "role": "model",
                            "parts": [{"text": "你的综合能力评分为 85 分。"}],
                        }
                    }
                ]
            },
        )

    transport = httpx.MockTransport(handler)
    settings = Settings()
    settings.llm.server_default.mode = "gemini"
    settings.llm.server_default.base_url = "https://generativelanguage.googleapis.com/v1beta"
    settings.llm.server_default.model = "gemini-2.0-flash"
    settings.llm.server_default.api_key_env = "SERVER_DEFAULT_TEST_KEY"
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
    assert tool_executor.calls[0][0] == "get_student_ability_scores"
    assert len(requests_payload) == 2

    # First request must include systemInstruction and tools
    first = requests_payload[0]
    assert "systemInstruction" in first
    assert "tools" in first

    # Second request must include function response in contents
    second_contents = requests_payload[1].get("contents")
    assert isinstance(second_contents, list)
    # Last user turn should have functionResponse parts
    user_turns = [c for c in second_contents if c.get("role") == "user"]
    assert any(
        isinstance(turn.get("parts"), list)
        and any("functionResponse" in p for p in turn["parts"])
        for turn in user_turns
    )
