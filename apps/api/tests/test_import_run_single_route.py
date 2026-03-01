from __future__ import annotations

from fastapi.testclient import TestClient

from apps.api.api.deps import get_admin_account
from apps.api.main import create_app
from apps.api.services.auth import AuthenticatedAccount


class _StubRepo:
    pass


class _StubLLM:
    pass


def _client() -> TestClient:
    app = create_app(repository=_StubRepo(), llm_service=_StubLLM())
    app.dependency_overrides[get_admin_account] = (
        lambda: AuthenticatedAccount(
            account_id=1,
            username="admin",
            is_admin=True,
        )
    )
    return TestClient(app)


def test_run_single_rejects_invalid_exam_type() -> None:
    with _client() as client:
        response = client.post(
            "/api/v1/import/run-single",
            json={
                "examType": "invalid",
                "sourcePath": "foo/bar",
            },
        )
    assert response.status_code == 400
    assert "无效的考试类型" in response.json()["detail"]


def test_run_single_rejects_mismatched_source_path() -> None:
    with _client() as client:
        response = client.post(
            "/api/v1/import/run-single",
            json={
                "examType": "datastructure",
                "sourcePath": "pta_ioi/foo",
            },
        )
    assert response.status_code == 400
    assert "不匹配" in response.json()["detail"]
