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
    def __init__(self) -> None:
        self.prompts: dict[str, SimpleNamespace] = {}
        self.prompt_versions: dict[str, list[SimpleNamespace]] = {}

    async def ensure_ai_prompt_template_default(
        self,
        *,
        prompt_key: str,
        title: str,
        description: str,
        category: str,
        default_content: str,
        allowed_variables: list[str],
        required_variables: list[str],
    ) -> None:
        now = datetime(2026, 2, 1, tzinfo=timezone.utc)
        if prompt_key not in self.prompts:
            self.prompts[prompt_key] = SimpleNamespace(
                prompt_key=prompt_key,
                title=title,
                description=description,
                category=category,
                content=default_content,
                default_content=default_content,
                allowed_variables=allowed_variables,
                required_variables=required_variables,
                version=1,
                updated_by="system",
                created_at=now,
                updated_at=now,
            )
            self.prompt_versions[prompt_key] = [
                SimpleNamespace(
                    version_id=1,
                    prompt_key=prompt_key,
                    version=1,
                    content=default_content,
                    change_note="系统默认版本",
                    updated_by="system",
                    created_at=now,
                )
            ]
            return
        row = self.prompts[prompt_key]
        row.title = title
        row.description = description
        row.category = category
        row.default_content = default_content
        row.allowed_variables = allowed_variables
        row.required_variables = required_variables

    async def list_ai_prompt_templates(self):
        return list(self.prompts.values())

    async def fetch_ai_prompt_template(self, prompt_key: str):
        return self.prompts.get(prompt_key)

    async def fetch_ai_prompt_template_versions(self, prompt_key: str):
        return list(reversed(self.prompt_versions.get(prompt_key, [])))

    async def update_ai_prompt_template(
        self,
        *,
        prompt_key: str,
        content: str,
        change_note: str | None,
        updated_by: str | None,
    ):
        row = self.prompts.get(prompt_key)
        if row is None:
            return None
        row.version += 1
        row.content = content
        row.updated_by = updated_by
        row.updated_at = datetime(2026, 2, row.version, tzinfo=timezone.utc)
        self.prompt_versions.setdefault(prompt_key, []).append(
            SimpleNamespace(
                version_id=row.version,
                prompt_key=prompt_key,
                version=row.version,
                content=content,
                change_note=change_note,
                updated_by=updated_by,
                created_at=row.updated_at,
            )
        )
        return row

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
  active_provider: deepseek
  providers:
    siliconflow:
      adapter: openai_compatible
      base_url: https://api.siliconflow.cn/v1
      model: Pro/moonshotai/Kimi-K2.5
      api_key_env: TEST_SILICONFLOW_KEY
      request_mode: chat_completions
    openai:
      adapter: openai_compatible
      base_url: https://shell.wyzai.top/v1
      model: openai/gpt-5.4-medium-thinking
      api_key_env: TEST_OPENAI_KEY
      request_mode: chat_completions
    copilot:
      adapter: openai_compatible
      base_url: http://127.0.0.1:5140/api/internal/copilot/v1
      model: openai/gpt-5-mini
      api_key_env: TEST_COPILOT_KEY
      request_mode: chat_completions
    deepseek:
      adapter: openai_compatible
      base_url: https://api.deepseek.com
      model: deepseek-v4-flash
      api_key_env: TEST_DEEPSEEK_KEY
      request_mode: chat_completions
    mimo:
      adapter: openai_compatible
      base_url: https://token-plan-cn.xiaomimimo.com/v1
      model: mimo-v2.5-pro
      api_key_env: TEST_MIMO_KEY
      request_mode: chat_completions
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
        assert payload["activeProvider"] == "deepseek"
        assert "sk-secret-value" not in str(payload)
        assert payload["activeRuntime"]["model"] == "deepseek-v4-flash"
        mimo = next(item for item in payload["providers"] if item["id"] == "mimo")
        assert mimo["baseUrl"] == "https://token-plan-cn.xiaomimimo.com/v1"
        assert mimo["model"] == "mimo-v2.5-pro"
        assert mimo["apiKeyEnv"] == "TEST_MIMO_KEY"
        assert mimo["requestMode"] == "chat_completions"

        patched = client.patch(
            "/api/v1/admin/model-config",
            json={
                "activeProvider": "openai",
                "provider": {
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
        assert patched_payload["activeProvider"] == "openai"
        assert patched_payload["activeRuntime"]["model"] == "gpt-5.4-high-thinking"
        assert patched_payload["activeRuntime"]["apiKeyEnv"] == "NEW_OPENAI_KEY"
        assert "sk-secret-value" not in str(patched_payload)
        assert (tmp_path / ".env.local").read_text(encoding="utf-8").count(
            "sk-secret-value"
        ) == 1
        assert client.app.state.settings.llm.providers["openai"].base_url == (
            "https://new.example.com/v1"
        )
        assert client.app.state.settings.llm.providers["openai"].model == (
            "openai/gpt-5.4-high-thinking"
        )


def test_admin_model_config_accepts_responses_copilot_model_as_responses_adapter(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        response = client.patch(
                "/api/v1/admin/model-config",
                json={
                "activeProvider": "copilot",
                "provider": {
                    "id": "copilot",
                    "model": "openai/gpt-5.4-mini",
                },
            },
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["activeProvider"] == "copilot"
    copilot = next(item for item in payload["providers"] if item["id"] == "copilot")
    assert copilot["adapter"] == "responses"
    assert copilot["requestMode"] == "responses"


def test_admin_model_config_saves_mimo_and_rejects_tts_model(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        response = client.patch(
            "/api/v1/admin/model-config",
            json={
                "activeProvider": "mimo",
                "provider": {
                    "id": "mimo",
                    "model": "mimo-v2.5-pro",
                },
            },
        )
        invalid = client.patch(
            "/api/v1/admin/model-config",
            json={
                "activeProvider": "mimo",
                "provider": {
                    "id": "mimo",
                    "model": "mimo-v2.5-tts",
                },
            },
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["activeProvider"] == "mimo"
    assert payload["activeRuntime"]["model"] == "mimo-v2.5-pro"
    mimo = next(item for item in payload["providers"] if item["id"] == "mimo")
    assert mimo["adapter"] == "openai_compatible"
    assert mimo["requestMode"] == "chat_completions"
    assert invalid.status_code == 422


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
                "providerId": "openai",
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


def test_admin_mimo_model_list_filters_dynamic_tts_models(
    tmp_path: Path,
    monkeypatch,
) -> None:
    class _FakeResponse:
        status_code = 200
        text = ""

        def json(self):
            return {
                "data": [
                    {"id": "mimo-v2.5-pro"},
                    {"id": "mimo-v2.5-tts"},
                    {"id": "mimo-v2-omni"},
                    {"id": "mimo-v2.5-tts-voiceclone"},
                ]
            }

    class _FakeAsyncClient:
        def __init__(self, *args, **kwargs):
            _ = args, kwargs

        async def __aenter__(self):
            return self

        async def __aexit__(self, exc_type, exc, tb):
            _ = exc_type, exc, tb
            return False

        async def get(self, url, headers=None):
            assert url == "https://token-plan-cn.xiaomimimo.com/v1/models"
            assert headers["Authorization"] == "Bearer sk-mimo"
            return _FakeResponse()

    monkeypatch.setenv("TEST_MIMO_KEY", "sk-mimo")
    monkeypatch.setattr(
        "apps.api.api.routes.admin.httpx.AsyncClient",
        _FakeAsyncClient,
    )
    with _build_client(tmp_path, monkeypatch) as client:
        response = client.post(
            "/api/v1/admin/model-config/mimo/models",
            json={
                "baseUrl": "https://token-plan-cn.xiaomimimo.com/v1",
                "apiKeyEnv": "TEST_MIMO_KEY",
            },
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["source"] == "dynamic"
    assert [item["modelId"] for item in payload["models"]] == [
        "mimo-v2.5-pro",
        "mimo-v2-omni",
    ]


def test_admin_prompt_config_saves_previews_and_restores(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        prompts = client.get("/api/v1/admin/prompts")
        assert prompts.status_code == 200
        assert any(item["key"] == "chat.normal_system" for item in prompts.json()["items"])

        patched = client.patch(
            "/api/v1/admin/prompts/chat.normal_system",
            json={
                "content": "角色：{role_name}\n\n{tool_rules}\n\n请先给结论。",
                "changeNote": "测试版本",
            },
        )
        assert patched.status_code == 200
        patched_payload = patched.json()
        assert patched_payload["version"] == 2
        assert patched_payload["content"].startswith("角色：")

        preview = client.post(
            "/api/v1/admin/prompts/chat.normal_system/preview",
            json={"content": "你好 {role_name}\n{tool_rules}"},
        )
        assert preview.status_code == 200
        assert "你好 小D" in preview.json()["rendered"]

        restored = client.post(
            "/api/v1/admin/prompts/chat.normal_system/restore",
            json={"version": 1},
        )
        assert restored.status_code == 200
        restored_payload = restored.json()
        assert restored_payload["version"] == 3
        assert "任务目标" in restored_payload["content"]


def test_admin_prompt_config_rejects_unknown_and_missing_variables(
    tmp_path: Path,
    monkeypatch,
) -> None:
    with _build_client(tmp_path, monkeypatch) as client:
        unknown = client.patch(
            "/api/v1/admin/prompts/chat.normal_system",
            json={"content": "你好 {role_name} {unknown}\n{tool_rules}"},
        )
        missing = client.patch(
            "/api/v1/admin/prompts/chat.normal_system",
            json={"content": "没有角色变量\n{tool_rules}"},
        )

    assert unknown.status_code == 422
    assert unknown.json()["error"]["code"] == "INVALID_PROMPT_TEMPLATE"
    assert missing.status_code == 422
    assert "role_name" in missing.json()["error"]["message"]


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
