from __future__ import annotations

import asyncio
import json
import httpx

from apps.api.core.config import LLMProviderConfig, Settings
from apps.api.schemas.chat import ChatMessageRequest, ChatReplyRequest
from apps.api.services.llm import LLMService


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
                                            "name": "get_student_ability_scores",
                                            "arguments": json.dumps(
                                                {
                                                    "exam_id": 32,
                                                    "student_id": "20231202019",
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
    assert tool_executor.calls[0][0] == "get_student_ability_scores"
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
