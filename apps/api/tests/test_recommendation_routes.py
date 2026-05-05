from __future__ import annotations

from datetime import datetime, timezone

from fastapi.testclient import TestClient

from apps.api.api.deps import get_admin_account, get_current_account
from apps.api.db.repository import (
    AccountProfileRow,
    LearningPathSnapshotRow,
    ProblemRecommendationSnapshotRow,
    StudentIdentityMatch,
)
from apps.api.main import create_app
from apps.api.services.auth import AuthenticatedAccount


class FakeRecommendationRepo:
    def __init__(self) -> None:
        self.problem_snapshot = ProblemRecommendationSnapshotRow(
            student_id=1,
            model_run_id=11,
            items=[
                {
                    "problemId": "HDU-1001",
                    "title": "A + B Problem",
                    "url": "https://example.test/problem/1001",
                    "knowledgePoints": ["输入输出"],
                    "score": 0.98,
                    "rank": 1,
                    "reason": "基础题型命中当前薄弱点。",
                }
            ],
            generated_at=datetime(2026, 2, 14, 8, 0, tzinfo=timezone.utc),
        )
        self.path_snapshot = LearningPathSnapshotRow(
            student_id=1,
            model_run_id=12,
            targets=["图论"],
            path=["图遍历", "最短路"],
            explanations={"图遍历": "近期相关题得分率偏低。"},
            generated_at=datetime(2026, 2, 14, 9, 0, tzinfo=timezone.utc),
        )

    async def fetch_account_profile(self, account_id: int):
        _ = account_id
        return AccountProfileRow(
            student_id="20230001",
            pta_nickname="Alice",
            updated_at=datetime(2026, 2, 14, tzinfo=timezone.utc),
        )

    async def find_students_by_student_no(
        self, student_no: str
    ) -> list[StudentIdentityMatch]:
        if student_no != "20230001":
            return []
        return [
            StudentIdentityMatch(
                student_id=1,
                student_no="20230001",
                student_name="Alice",
            )
        ]

    async def exists_pta_submission_by_actor_name(self, actor_name: str):
        _ = actor_name
        return False

    async def exists_learning_records_for_student_ids(
        self, student_ids: list[int]
    ) -> bool:
        return student_ids == [1]

    async def fetch_latest_problem_recommendations(
        self, student_ids: list[int], top_k: int = 10
    ):
        if student_ids != [1]:
            return None
        return ProblemRecommendationSnapshotRow(
            student_id=self.problem_snapshot.student_id,
            model_run_id=self.problem_snapshot.model_run_id,
            items=self.problem_snapshot.items[:top_k],
            generated_at=self.problem_snapshot.generated_at,
        )

    async def fetch_latest_learning_path(self, student_ids: list[int]):
        if student_ids != [1]:
            return None
        return self.path_snapshot


def _account() -> AuthenticatedAccount:
    return AuthenticatedAccount(account_id=7, username="student", is_admin=False)


def _admin() -> AuthenticatedAccount:
    return AuthenticatedAccount(account_id=1, username="admin", is_admin=True)


def test_problem_recommendations_me_returns_only_problem_items() -> None:
    app = create_app(repository=FakeRecommendationRepo(), llm_service=object())
    app.dependency_overrides[get_current_account] = _account

    with TestClient(app) as client:
        response = client.get("/api/v1/recommendations/problems/me?topK=1")

    assert response.status_code == 200
    payload = response.json()
    assert payload["studentEntityId"] == 1
    assert payload["modelRunId"] == 11
    assert payload["items"][0]["problemId"] == "HDU-1001"
    assert "path" not in payload
    assert "targets" not in payload


def test_learning_path_me_returns_only_path_data() -> None:
    app = create_app(repository=FakeRecommendationRepo(), llm_service=object())
    app.dependency_overrides[get_current_account] = _account

    with TestClient(app) as client:
        response = client.get("/api/v1/recommendations/path/me")

    assert response.status_code == 200
    payload = response.json()
    assert payload["studentEntityId"] == 1
    assert payload["modelRunId"] == 12
    assert payload["path"] == ["图遍历", "最短路"]
    assert "items" not in payload


def test_admin_can_fetch_problem_recommendations_for_student_id() -> None:
    app = create_app(repository=FakeRecommendationRepo(), llm_service=object())
    app.dependency_overrides[get_admin_account] = _admin

    with TestClient(app) as client:
        response = client.get(
            "/api/v1/recommendations/problems/student",
            params={"studentId": "20230001", "topK": 1},
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["studentEntityIds"] == [1]
    assert payload["items"][0]["knowledgePoints"] == ["输入输出"]
