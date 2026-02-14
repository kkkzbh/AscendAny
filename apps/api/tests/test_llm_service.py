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


def test_list_provider_options_includes_server_default_metadata() -> None:
    settings = Settings()
    service = _build_service(settings)

    payload = service.list_provider_options()

    assert payload.defaultProvider == "server_default"
    assert payload.serverDefaultTarget == "server_default"
    assert payload.serverDefaultTargetLabel is None
    assert payload.serverDefaultModel is None
    assert any(
        option.type == "server_default" and option.usesServerConfig
        for option in payload.providers
    )


def test_server_default_provider_uses_dedicated_config(monkeypatch) -> None:
    settings = Settings()
    settings.llm.server_default.base_url = "https://llm.example.com/v1"
    settings.llm.server_default.model = "server-default-model"
    settings.llm.server_default.api_key_env = "SERVER_DEFAULT_TEST_KEY"
    settings.llm.providers["openai"].base_url = "https://api.openai.com/v1"
    settings.llm.providers["openai"].model = "provider-model"
    monkeypatch.setenv("SERVER_DEFAULT_TEST_KEY", "secret")
    service = _build_service(settings)

    provider = service._resolve_provider("server_default", None)

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
                    providerType="server_default",
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
