from __future__ import annotations

from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace

from fastapi.testclient import TestClient

from apps.api.api.deps import get_admin_account
from apps.api.core.config import Settings
from apps.api.main import create_app
from apps.api.services.auth import AuthenticatedAccount


class _StubLLM:
    pass


class _AdminRepo:
    async def fetch_admin_student_summaries(self, search=None, limit=200, role_id="xiaoD"):
        _ = search, limit, role_id
        return [
            SimpleNamespace(
                student_entity_id=101,
                student_no="20230001",
                student_name="Alice",
                username="alice",
                rating=1002,
                knowledge=90,
                accuracy=88,
                quality=80,
                flexibility=76,
                proficiency=79,
                latest_exam_at=datetime(2026, 2, 11, tzinfo=timezone.utc),
                exam_count=2,
                generated_reports=1,
                failed_reports=0,
                missing_reports=1,
            )
        ]

    async def fetch_admin_student_exam_reports(self, student_id: int, role_id="xiaoD"):
        assert student_id == 101
        _ = role_id
        return [
            SimpleNamespace(
                exam_id=11,
                exam_name="Contest 11",
                exam_type="datastructure",
                exam_date=datetime(2026, 2, 11, tzinfo=timezone.utc),
                rank=1,
                total_score=100,
                solved_count=4,
                rating_delta=22,
                old_rating=980,
                new_rating=1002,
                knowledge=90,
                accuracy=88,
                quality=80,
                flexibility=76,
                proficiency=79,
                analysis_status="success",
                analysis_reply="Alice report",
                generated_at=datetime(2026, 2, 11, tzinfo=timezone.utc),
                error_message=None,
            )
        ]

    async def fetch_admin_account_summaries(self, limit=200):
        _ = limit
        return [
            SimpleNamespace(
                account_id=1,
                username="admin",
                display_name="Admin",
                is_active=True,
                is_admin=True,
                provision_source="local",
                student_id=None,
                pta_nickname=None,
                created_at=datetime(2026, 1, 1, tzinfo=timezone.utc),
                updated_at=datetime(2026, 1, 2, tzinfo=timezone.utc),
                last_login_at=None,
            )
        ]

    async def fetch_admin_audit_logs(self, limit=100):
        _ = limit
        return []


def _write_configs(tmp_path: Path, monkeypatch) -> None:
    api_config = tmp_path / "api.yaml"
    api_config.write_text(
        """
llm:
  server_default:
    mode: openai_compatible
    base_url: https://api.deepseek.com
    model: deepseek-v4-flash
    api_key_env: TEST_DEEPSEEK_KEY
  active_tab: deepseek
  tabs:
    siliconflow:
      base_url: https://api.siliconflow.cn/v1
      model: Pro/moonshotai/Kimi-K2.5
      api_key_env: TEST_SILICONFLOW_KEY
    openai:
      base_url: https://shell.wyzai.top/v1
      model: openai/gpt-5.4-medium-thinking
      api_key_env: TEST_OPENAI_KEY
    copilot:
      base_url: http://127.0.0.1:5140/api/internal/copilot/v1
      model: openai/gpt-5-mini
      api_key_env: TEST_COPILOT_KEY
    deepseek:
      base_url: https://api.deepseek.com
      model: deepseek-v4-flash
      api_key_env: TEST_DEEPSEEK_KEY
  request_timeout_seconds: 60
""",
        encoding="utf-8",
    )
    preprocess_config = tmp_path / "preprocess.yaml"
    preprocess_config.write_text(
        """
practice_root: practice
ingest:
  encodings: [utf-8, utf-8-sig, gb18030]
  fingerprint_roles: [answer_html, submission_csv]
  timezone: Asia/Shanghai
""",
        encoding="utf-8",
    )
    monkeypatch.setenv("ASCENDANY_API_CONFIG", str(api_config))
    monkeypatch.setenv("ASCENDANY_PREPROCESS_CONFIG", str(preprocess_config))
    monkeypatch.setenv("ASCENDANY_ADMIN_ENV_FILE", str(tmp_path / ".env.local"))
    monkeypatch.delenv("TEST_DEEPSEEK_KEY", raising=False)


def _build_client(tmp_path: Path, monkeypatch) -> TestClient:
    _write_configs(tmp_path, monkeypatch)
    settings = Settings()
    app = create_app(settings=settings, repository=_AdminRepo(), llm_service=_StubLLM())
    app.dependency_overrides[get_admin_account] = lambda: AuthenticatedAccount(
        account_id=1,
        username="admin",
        is_admin=True,
    )
    return TestClient(app)


def test_admin_model_config_does_not_expose_plaintext_key_and_saves_runtime(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        initial = client.get("/api/v1/admin/model-config")
        assert initial.status_code == 200
        payload = initial.json()
        assert payload["activeTab"] == "deepseek"
        assert "sk-secret-value" not in str(payload)
        assert payload["serverDefault"]["model"] == "deepseek-v4-flash"

        patched = client.patch(
            "/api/v1/admin/model-config",
            json={
                "activeTab": "openai",
                "tab": {
                    "id": "openai",
                    "baseUrl": "https://new.example.com/v1",
                    "model": "openai/gpt-5.4-high-thinking",
                    "apiKeyEnv": "NEW_OPENAI_KEY",
                    "apiKey": "sk-secret-value",
                }
            },
        )

        assert patched.status_code == 200
        patched_payload = patched.json()
        assert patched_payload["activeTab"] == "openai"
        assert patched_payload["serverDefault"]["model"] == "gpt-5.4-high-thinking"
        assert patched_payload["serverDefault"]["apiKeyEnv"] == "NEW_OPENAI_KEY"
        assert "sk-secret-value" not in str(patched_payload)
        assert (tmp_path / ".env.local").read_text(encoding="utf-8").count(
            "sk-secret-value"
        ) == 1
        assert client.app.state.settings.llm.server_default.base_url == (
            "https://new.example.com/v1"
        )
        assert client.app.state.settings.llm.server_default.model == (
            "gpt-5.4-high-thinking"
        )


def test_admin_model_config_rejects_responses_only_copilot_model(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        response = client.patch(
            "/api/v1/admin/model-config",
            json={
                "activeTab": "copilot",
                "tab": {
                    "id": "copilot",
                    "model": "openai/gpt-5.4-mini",
                },
            },
        )

    assert response.status_code == 422
    assert "Responses-only" in response.json()["error"]["message"]


def test_admin_model_connection_test_and_deepseek_static_fallback(
    tmp_path: Path,
    monkeypatch,
) -> None:
    class _FakeResponse:
        status_code = 200
        text = ""

        def json(self):
            return {"choices": [{"message": {"content": "ok"}}]}

    class _FakeAsyncClient:
        def __init__(self, *args, **kwargs):
            _ = args, kwargs

        async def __aenter__(self):
            return self

        async def __aexit__(self, exc_type, exc, tb):
            _ = exc_type, exc, tb
            return False

        async def post(self, url, headers=None, json=None):
            assert url == "https://new.example.com/v1/chat/completions"
            assert headers["Authorization"] == "Bearer sk-test"
            assert json["model"] == "gpt-5.4-medium-thinking"
            return _FakeResponse()

    monkeypatch.setenv("TEST_OPENAI_KEY", "sk-test")
    monkeypatch.setattr(
        "apps.api.api.routes.admin.httpx.AsyncClient",
        _FakeAsyncClient,
    )
    with _build_client(tmp_path, monkeypatch) as client:
        result = client.post(
            "/api/v1/admin/model-config/test",
            json={
                "tabId": "openai",
                "baseUrl": "https://new.example.com/v1",
                "model": "openai/gpt-5.4-medium-thinking",
                "apiKeyEnv": "TEST_OPENAI_KEY",
            },
        )
        fallback = client.post(
            "/api/v1/admin/model-config/deepseek/models",
            json={
                "baseUrl": "https://api.deepseek.com",
                "apiKeyEnv": "TEST_DEEPSEEK_KEY",
            },
        )

    assert result.status_code == 200
    assert result.json()["ok"] is True
    assert fallback.status_code == 200
    assert fallback.json()["source"] == "static"
    assert fallback.json()["models"][0]["modelId"] == "deepseek-v4-flash"


def test_admin_student_reports_aggregate_by_student(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        students = client.get("/api/v1/admin/students")
        reports = client.get("/api/v1/admin/students/101/exam-reports")

    assert students.status_code == 200
    students_payload = students.json()
    assert students_payload["items"][0]["studentEntityId"] == "101"
    assert students_payload["items"][0]["reportCompletionRate"] == 0.5

    assert reports.status_code == 200
    reports_payload = reports.json()
    assert reports_payload["student"]["studentName"] == "Alice"
    assert reports_payload["items"][0]["analysisReply"] == "Alice report"
