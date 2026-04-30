from __future__ import annotations

from datetime import datetime, timezone
from decimal import Decimal

from fastapi.testclient import TestClient

from apps.api.db.repository import (
    AchievementDefinitionRow,
    AggregatedAchievementStateRow,
    LeaderboardEntryRow,
    StudentIdentityMatch,
    StudentNoMatch,
)
from apps.api.main import create_app
from apps.api.schemas.chat import ChatReplyResponse


class FakeRepo:
    def __init__(self) -> None:
        self.ai_counter_calls: list[tuple[int, ...]] = []

    async def fetch_latest_exam_imported_at(self):
        return datetime(2026, 2, 13, 9, 30, tzinfo=timezone.utc)

    async def find_student_nos_by_name(self, student_name: str):
        return [
            StudentNoMatch(student_id=1, student_no="20230001", student_name="Alice"),
            StudentNoMatch(student_id=2, student_no="20230002", student_name="Alice"),
        ]

    async def find_students_by_student_no(self, student_no: str):
        if student_no == "20230001":
            return [
                StudentIdentityMatch(
                    student_id=1,
                    student_no="20230001",
                    student_name="Alice",
                )
            ]
        if student_no == "20230088":
            return [
                StudentIdentityMatch(
                    student_id=88,
                    student_no="20230088",
                    student_name="MergeUser",
                ),
                StudentIdentityMatch(
                    student_id=99,
                    student_no="20230088",
                    student_name="MergeUser",
                ),
            ]
        return []

    async def exists_pta_submission_by_actor_name(self, actor_name: str):
        return False

    async def exists_learning_records_for_student_ids(
        self, student_ids: list[int]
    ) -> bool:
        return bool(student_ids)

    async def fetch_current_metrics(self, student_id: int):
        return None

    async def fetch_rating_history(self, student_id: int, limit: int = 50):
        return []

    async def fetch_exam_metric_history(self, student_id: int, limit: int = 50):
        return []

    async def fetch_student_leaderboard(self):
        return [
            LeaderboardEntryRow(
                student_no="20230001",
                username="alice",
                rating=1012,
                knowledge=Decimal("88.5"),
                accuracy=Decimal("81.0"),
                quality=Decimal("77.2"),
                flexibility=None,
                proficiency=Decimal("74.4"),
            )
        ]

    async def fetch_achievement_definitions(
        self,
        source: str | None = None,
        enabled_only: bool = True,
    ):
        _ = source, enabled_only
        return [
            AchievementDefinitionRow(
                achievement_code="exam_count_first",
                title="初试锋芒",
                description="累计参赛次数达到 1 / 3 / 8 场。",
                source="ingest",
                progress_key="exam_count",
                bronze_target=1,
                silver_target=3,
                gold_target=8,
                sort_order=1,
            ),
            AchievementDefinitionRow(
                achievement_code="ai_dialogue_count",
                title="AI陪练",
                description="与 AI 成功对话次数达到 3 / 15 / 40 次。",
                source="realtime",
                progress_key="ai_dialogue_count",
                bronze_target=3,
                silver_target=15,
                gold_target=40,
                sort_order=2,
            ),
        ]

    async def fetch_aggregated_achievement_states(
        self,
        student_ids: list[int],
    ):
        if set(student_ids) == {88, 99}:
            return [
                AggregatedAchievementStateRow(
                    achievement_code="exam_count_first",
                    progress_value=10,
                    tier=3,
                )
            ]
        if 1 in student_ids:
            return [
                AggregatedAchievementStateRow(
                    achievement_code="exam_count_first",
                    progress_value=2,
                    tier=1,
                )
            ]
        return []

    async def fetch_student_activity_counters(
        self,
        student_ids: list[int],
    ):
        if set(student_ids) == {88, 99}:
            return {88: 3, 99: 22}
        if 1 in student_ids:
            return {1: 5}
        return {}

    async def increment_ai_dialogue_count(
        self,
        student_ids: list[int],
        delta: int = 1,
    ):
        _ = delta
        self.ai_counter_calls.append(tuple(sorted(student_ids)))


class FakeLLM:
    def __init__(self) -> None:
        self.calls = 0
        self.payloads = []

    async def generate_reply(self, payload, system_prompt=None, tool_executor=None) -> ChatReplyResponse:
        self.calls += 1
        self.payloads.append(payload)
        return ChatReplyResponse(
            reply="ok",
            summary=payload.summary,
            provider="server_default",
        )

    async def stream_reply(self, payload, system_prompt=None, tool_executor=None):
        self.calls += 1
        self.payloads.append(payload)
        yield {
            "type": "meta",
            "provider": "deepseek",
            "model": "deepseek-v4-flash",
            "requestMode": "chat_completions",
            "summary": payload.summary,
        }
        yield {"type": "delta", "text": "o"}
        yield {"type": "delta", "text": "k"}
        yield {
            "type": "done",
            "reply": "ok",
            "summary": payload.summary,
            "provider": "deepseek",
            "model": "deepseek-v4-flash",
            "requestMode": "chat_completions",
        }


def test_healthz_route() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get("/api/v1/healthz")
    assert response.status_code == 200
    assert response.json() == {"ok": True}


def test_meta_latest_exam_route() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get("/api/v1/meta/latest_exam_imported_at")
    assert response.status_code == 200
    payload = response.json()
    assert payload["latestExamImportedAt"].startswith("2026-02-13T09:30:00")


def test_auth_policy_route() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get("/api/v1/auth/policy")
    assert response.status_code == 200
    payload = response.json()
    assert payload["signupPolicy"] == "username_password_only"
    assert payload["requirePhone"] is False
    assert payload["requireEmail"] is False


def test_students_dashboard_ambiguous_pta_nickname_returns_409() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get(
            "/api/v1/students/dashboard", params={"ptaNickname": "Alice"}
        )

    assert response.status_code == 409
    payload = response.json()
    assert payload["error"]["code"] == "MULTIPLE_STUDENT_IDS_FOR_NICKNAME"


def test_students_dashboard_response_contains_metric_delta() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get(
            "/api/v1/students/dashboard", params={"studentId": "20230001"}
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["identity"]["studentId"] == "20230001"
    assert payload["metricDelta"]["baseline"] == "zero"
    assert payload["metricDelta"]["latestExamId"] is None
    assert payload["metricDelta"]["values"]["knowledge"] == 0
    assert payload["progressExplanation"]["available"] is False
    assert payload["milestoneStreak"]["newMilestones"] == []
    assert payload["peerComparison"]["defaultMode"] == "percentile_band"
    assert payload["postExamSupport"]["mode"] == "steady"


def test_students_dashboard_returns_fallback_when_student_id_not_found() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get(
            "/api/v1/students/dashboard", params={"studentId": "20999999"}
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["identity"]["studentId"] == "20999999"
    assert payload["identity"]["noSubmissionRecords"] is True
    assert payload["rating"]["current"] == 800
    assert payload["rating"]["history"] == []
    assert payload["metrics"]["knowledge"] == 50
    assert payload["metrics"]["accuracy"] == 50
    assert payload["metrics"]["quality"] == 50
    assert payload["metrics"]["flexibility"] == 50
    assert payload["metrics"]["proficiency"] == 50
    assert payload["progressExplanation"]["available"] is False
    assert payload["milestoneStreak"]["available"] is False
    assert payload["peerComparison"]["available"] is False
    assert payload["postExamSupport"]["available"] is False


def test_students_achievements_route_returns_items() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get(
            "/api/v1/students/achievements",
            params={"studentId": "20230001"},
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["identity"]["studentId"] == "20230001"
    assert payload["summary"]["total"] == 2
    assert payload["summary"]["bronze"] == 2
    assert payload["summary"]["silver"] == 0
    assert payload["summary"]["gold"] == 0
    assert payload["items"][0]["code"] == "exam_count_first"
    assert payload["items"][0]["tier"] == 1
    assert payload["items"][1]["code"] == "ai_dialogue_count"
    assert payload["items"][1]["tier"] == 1


def test_students_achievements_route_merges_identity_entities() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get(
            "/api/v1/students/achievements",
            params={"studentId": "20230088"},
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["identity"]["studentId"] == "20230088"
    assert payload["items"][0]["progress"] == 10
    assert payload["items"][0]["tier"] == 3
    assert payload["items"][1]["progress"] == 22
    assert payload["items"][1]["tier"] == 2


def test_students_achievements_route_handles_not_found_student() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get(
            "/api/v1/students/achievements",
            params={"studentId": "20999999"},
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["identity"]["studentId"] == "20999999"
    assert payload["identity"]["noSubmissionRecords"] is True
    assert payload["summary"]["locked"] == 2
    assert payload["items"][0]["tier"] == 0
    assert payload["items"][1]["tier"] == 0


def test_students_leaderboard_route_returns_rows() -> None:
    app = create_app(repository=FakeRepo(), llm_service=FakeLLM())
    with TestClient(app) as client:
        response = client.get("/api/v1/students/leaderboard")

    assert response.status_code == 200
    payload = response.json()
    assert len(payload["items"]) == 1
    assert payload["items"][0]["studentId"] == "20230001"
    assert payload["items"][0]["grade"] == "2023"
    assert payload["items"][0]["username"] == "alice"
    assert payload["items"][0]["rating"] == 1012
    assert payload["items"][0]["knowledge"] == 88.5
    assert payload["items"][0]["flexibility"] == 0


def test_chat_reply_hello_still_goes_to_llm() -> None:
    repo = FakeRepo()
    llm = FakeLLM()
    app = create_app(repository=repo, llm_service=llm)
    with TestClient(app) as client:
        response = client.post(
            "/api/v1/chat/reply",
            json={
                "studentId": "20230001",
                "messages": [{"role": "user", "content": "你好"}],
                "summary": "",
            },
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["reply"] == "ok"
    assert llm.calls == 1
    assert repo.ai_counter_calls == [(1,)]


def test_chat_reply_stream_emits_meta_delta_done() -> None:
    repo = FakeRepo()
    llm = FakeLLM()
    app = create_app(repository=repo, llm_service=llm)
    with TestClient(app) as client:
        response = client.post(
            "/api/v1/chat/reply/stream",
            json={
                "studentId": "20230001",
                "messages": [{"role": "user", "content": "你好"}],
                "summary": "",
            },
        )

    assert response.status_code == 200
    body = response.text
    assert "event: meta" in body
    assert 'data: {"type":"delta","text":"o"}' in body
    assert "event: done" in body
    assert '"reply":"ok"' in body
    assert llm.calls == 1
    assert repo.ai_counter_calls == [(1,)]


def test_chat_reply_stream_preserves_assistant_reasoning_content() -> None:
    repo = FakeRepo()
    llm = FakeLLM()
    app = create_app(repository=repo, llm_service=llm)
    with TestClient(app) as client:
        response = client.post(
            "/api/v1/chat/reply/stream",
            json={
                "studentId": "20230001",
                "messages": [
                    {"role": "user", "content": "第一轮"},
                    {
                        "role": "assistant",
                        "content": "第一轮回答",
                        "reasoningContent": "第一轮工具调用 reasoning",
                    },
                    {"role": "user", "content": "第二轮"},
                ],
                "summary": "",
            },
        )

    assert response.status_code == 200
    assert llm.calls == 1
    assert llm.payloads[0].messages[1].reasoningContent == "第一轮工具调用 reasoning"


def test_chat_reply_rejects_legacy_client_provider_config() -> None:
    repo = FakeRepo()
    llm = FakeLLM()
    app = create_app(repository=repo, llm_service=llm)
    with TestClient(app) as client:
        response = client.post(
            "/api/v1/chat/reply",
            json={
                "studentId": "20230001",
                "messages": [{"role": "user", "content": "你好"}],
                "summary": "",
                "providerType": "deepseek",
                "providerConfig": {
                    "baseUrl": "https://api.deepseek.com/v1",
                    "model": "deepseek-chat",
                    "apiKey": "secret",
                    "mode": "openai_compatible",
                },
            },
        )

    assert response.status_code == 422
    assert llm.calls == 0
