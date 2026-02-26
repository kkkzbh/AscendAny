from __future__ import annotations

from datetime import datetime, timezone
from decimal import Decimal

from fastapi.testclient import TestClient

from apps.api.db.repository import (
    LeaderboardEntryRow,
    StudentIdentityMatch,
    StudentNoMatch,
)
from apps.api.main import create_app
from apps.api.schemas.chat import ChatReplyResponse
from apps.api.schemas.model import ModelProvidersResponse, ProviderOptionResponse


class FakeRepo:
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
        return []

    async def exists_pta_submission_by_actor_name(self, actor_name: str):
        return False

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


class FakeLLM:
    def __init__(self) -> None:
        self.calls = 0

    def list_provider_options(self) -> ModelProvidersResponse:
        return ModelProvidersResponse(
            defaultProvider="server_default",
            serverDefaultTarget="server_default",
            providers=[
                ProviderOptionResponse(
                    type="server_default",
                    label="默认",
                    usesServerConfig=True,
                    enabled=True,
                )
            ],
        )

    async def generate_reply(self, payload, system_prompt=None, tool_executor=None) -> ChatReplyResponse:
        self.calls += 1
        return ChatReplyResponse(
            reply="ok",
            summary=payload.summary,
            provider="server_default",
        )


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
                "providerType": "server_default",
            },
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["reply"] == "ok"
    assert llm.calls == 1
