import os
from pathlib import Path

from apps.api.core.config import load_settings


def test_load_settings_reads_local_env_file_without_overriding_process_env(
    tmp_path: Path,
    monkeypatch,
) -> None:
    config_path = tmp_path / "api.yaml"
    env_path = tmp_path / ".env.local"
    config_path.write_text(
        """
llm:
  active_provider: deepseek
  providers:
    deepseek:
      adapter: openai_compatible
      base_url: https://api.deepseek.com
      model: deepseek-v4-flash
      api_key_env: TEST_DEEPSEEK_KEY
      request_mode: chat_completions
""",
        encoding="utf-8",
    )
    env_path.write_text(
        """
# local API secrets
TEST_DEEPSEEK_KEY='from-local-env'
EXISTING_KEY=from-file
""",
        encoding="utf-8",
    )
    monkeypatch.setenv("ASCENDANY_ADMIN_ENV_FILE", str(env_path))
    monkeypatch.setenv("EXISTING_KEY", "from-process")
    monkeypatch.delenv("TEST_DEEPSEEK_KEY", raising=False)

    settings = load_settings(config_path)

    assert settings.llm.providers["deepseek"].api_key_env == "TEST_DEEPSEEK_KEY"
    assert os.getenv("TEST_DEEPSEEK_KEY") == "from-local-env"
    assert os.getenv("EXISTING_KEY") == "from-process"
