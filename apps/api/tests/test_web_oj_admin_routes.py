from __future__ import annotations

from typing import Any

from fastapi.testclient import TestClient

from apps.api.api.deps import get_admin_account
from apps.api.main import create_app
from apps.api.services.auth import AuthenticatedAccount
from apps.api.services.oj import OjProblemService


class FakeRepo:
    pass


def _admin() -> AuthenticatedAccount:
    return AuthenticatedAccount(account_id=1, username="admin", is_admin=True)


def test_web_admin_oj_problem_list_uses_admin_auth_and_legacy_path(monkeypatch) -> None:
    seen: dict[str, Any] = {}

    async def fake_list(self: OjProblemService, payload: dict[str, Any]) -> dict[str, Any]:
        seen["payload"] = payload
        return {
            "success": True,
            "requestId": payload["requestId"],
            "code": 200,
            "data": {"total": 1, "list": [{"problemId": "P1001"}]},
            "message": "获取题目列表成功",
            "authentication": "",
        }

    monkeypatch.setattr(OjProblemService, "admin_problems_list", fake_list)
    app = create_app(repository=FakeRepo(), llm_service=object())
    app.dependency_overrides[get_admin_account] = _admin

    with TestClient(app) as client:
        response = client.post(
            "/api/admin/oj/problems/list/",
            json={"requestId": "r1", "q": "P1001", "page": 20, "offset": 0},
        )

    assert response.status_code == 200
    payload = response.json()
    assert seen["payload"]["q"] == "P1001"
    assert payload["success"] is True
    assert payload["code"] == 200
    assert payload["requestId"] == "r1"
    assert payload["data"]["list"][0]["problemId"] == "P1001"


def test_web_admin_oj_routes_require_admin_dependency() -> None:
    app = create_app(repository=FakeRepo(), llm_service=object())

    with TestClient(app) as client:
        response = client.post(
            "/api/admin/oj/submissions/list/",
            json={"requestId": "r2"},
        )

    assert response.status_code == 401


def test_web_admin_oj_testcase_create_path(monkeypatch) -> None:
    async def fake_create(
        self: OjProblemService, payload: dict[str, Any]
    ) -> dict[str, Any]:
        return {
            "success": True,
            "requestId": payload["requestId"],
            "code": 200,
            "data": {"id": 9},
            "message": "创建测试点成功",
            "authentication": "",
        }

    monkeypatch.setattr(OjProblemService, "admin_testcases_create", fake_create)
    app = create_app(repository=FakeRepo(), llm_service=object())
    app.dependency_overrides[get_admin_account] = _admin

    with TestClient(app) as client:
        response = client.post(
            "/api/admin/oj/testcases/create/",
            json={
                "requestId": "r3",
                "problemId": "P1001",
                "inputData": "1 2\n",
                "outputData": "3\n",
            },
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["success"] is True
    assert payload["data"] == {"id": 9}
