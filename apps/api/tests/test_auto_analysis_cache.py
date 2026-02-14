from __future__ import annotations

from datetime import datetime, timezone

from fastapi.testclient import TestClient

from apps.api.api.deps import get_current_account
from apps.api.db.repository import (
    AutoAnalysisCacheRow,
    RatingHistoryRow,
    StudentIdentityMatch,
)
from apps.api.main import create_app
from apps.api.schemas.chat import ChatReplyResponse
from apps.api.services.auth import AuthenticatedAccount


class FakeRepo:
    def __init__(self) -> None:
        self.cache: dict[tuple[int, int, str], AutoAnalysisCacheRow] = {}

    async def fetch_account_profile(self, account_id: int):
        return None

    async def find_students_by_student_no(self, student_no: str):
        return [
            StudentIdentityMatch(
                student_id=1,
                student_no=student_no,
                student_name="王浩然",
            )
        ]

    async def exists_pta_submission_by_actor_name(self, actor_name: str):
        return True

    async def fetch_current_metrics(self, student_id: int):
        return None

    async def fetch_exam_metric_history(self, student_id: int, limit: int = 50):
        return []

    async def fetch_rating_history(self, student_id: int, limit: int = 50):
        return [
            RatingHistoryRow(
                exam_id=9,
                exam_name="精选3",
                exam_time=datetime(2026, 2, 8, tzinfo=timezone.utc),
                old_rating=1016,
                delta=81,
                new_rating=1097,
            )
        ][:limit]

    async def fetch_auto_analysis_cache(self, account_id: int, exam_id: int, role_id: str):
        return self.cache.get((account_id, exam_id, role_id))

    async def upsert_auto_analysis_cache(
        self,
        account_id: int,
        exam_id: int,
        role_id: str,
        provider_type: str | None,
        reply: str,
        source: str,
    ):
        key = (account_id, exam_id, role_id)
        old = self.cache.get(key)
        delivered_at = old.delivered_at if old is not None else None
        now = datetime.now(timezone.utc)
        row = AutoAnalysisCacheRow(
            account_id=account_id,
            exam_id=exam_id,
            role_id=role_id,
            provider_type=provider_type,
            reply=reply,
            source=source,
            generated_at=now,
            delivered_at=delivered_at,
            updated_at=now,
        )
        self.cache[key] = row
        return row

    async def mark_auto_analysis_delivered(self, account_id: int, exam_id: int, role_id: str):
        key = (account_id, exam_id, role_id)
        row = self.cache.get(key)
        if row is None:
            return
        now = datetime.now(timezone.utc)
        self.cache[key] = AutoAnalysisCacheRow(
            account_id=row.account_id,
            exam_id=row.exam_id,
            role_id=row.role_id,
            provider_type=row.provider_type,
            reply=row.reply,
            source=row.source,
            generated_at=row.generated_at,
            delivered_at=row.delivered_at or now,
            updated_at=now,
        )


class FakeLLM:
    def __init__(self) -> None:
        self.calls = 0

    def list_provider_options(self):
        return None

    async def generate_reply(self, payload, system_prompt=None, tool_executor=None):
        self.calls += 1
        return ChatReplyResponse(
            reply="这是一条自动分析缓存。",
            summary="",
            provider=payload.providerType,
        )


def _build_client(repo: FakeRepo, llm: FakeLLM) -> TestClient:
    app = create_app(repository=repo, llm_service=llm)
    app.dependency_overrides[get_current_account] = lambda: AuthenticatedAccount(
        account_id=1,
        username="tester",
    )
    return TestClient(app)


def test_auto_analysis_delivered_only_once_per_latest_exam() -> None:
    repo = FakeRepo()
    llm = FakeLLM()
    with _build_client(repo, llm) as client:
        payload = {
            "studentId": "20231202047",
            "ptaNickname": "王浩然",
            "providerType": "server_default",
            "roleId": "xiaoD",
            "latestExamId": "9",
        }
        first = client.post("/api/v1/chat/auto-analysis", json=payload)
        second = client.post("/api/v1/chat/auto-analysis", json=payload)

    assert first.status_code == 200
    assert second.status_code == 200
    assert first.json()["reply"] == "这是一条自动分析缓存。"
    assert second.json()["reply"] == ""
    assert llm.calls == 1


def test_auto_analysis_uses_cached_reply_before_llm() -> None:
    repo = FakeRepo()
    llm = FakeLLM()
    now = datetime.now(timezone.utc)
    repo.cache[(1, 9, "xiaoD")] = AutoAnalysisCacheRow(
        account_id=1,
        exam_id=9,
        role_id="xiaoD",
        provider_type="server_default",
        reply="预热缓存命中",
        source="prewarm",
        generated_at=now,
        delivered_at=None,
        updated_at=now,
    )

    with _build_client(repo, llm) as client:
        response = client.post(
            "/api/v1/chat/auto-analysis",
            json={
                "studentId": "20231202047",
                "ptaNickname": "王浩然",
                "providerType": "server_default",
                "roleId": "xiaoD",
                "latestExamId": "9",
            },
        )

    assert response.status_code == 200
    assert response.json()["reply"] == "预热缓存命中"
    assert llm.calls == 0
