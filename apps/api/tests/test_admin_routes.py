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
    base_url: https://llm.example.com/v1
    model: test-model
    api_key_env: TEST_ADMIN_MODEL_KEY
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
    monkeypatch.delenv("TEST_ADMIN_MODEL_KEY", raising=False)


def _build_client(tmp_path: Path, monkeypatch) -> TestClient:
    _write_configs(tmp_path, monkeypatch)
    settings = Settings()
    settings.llm.server_default.base_url = "https://llm.example.com/v1"
    settings.llm.server_default.model = "test-model"
    settings.llm.server_default.api_key_env = "TEST_ADMIN_MODEL_KEY"
    app = create_app(settings=settings, repository=_AdminRepo(), llm_service=_StubLLM())
    app.dependency_overrides[get_admin_account] = lambda: AuthenticatedAccount(
        account_id=1,
        username="admin",
        is_admin=True,
    )
    return TestClient(app)


def test_admin_config_does_not_expose_or_accept_model_config(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        initial = client.get("/api/v1/admin/config")
        assert initial.status_code == 200
        payload = initial.json()
        assert "model" not in payload
        assert "apiConfigPath" not in payload

        patched = client.patch(
            "/api/v1/admin/config",
            json={
                "model": {
                    "baseUrl": "https://new.example.com/v1",
                    "model": "new-model",
                    "apiKeyEnv": "NEW_ENV_KEY",
                    "requestTimeoutSeconds": 30,
                }
            },
        )

    assert patched.status_code == 422


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
