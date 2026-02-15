from __future__ import annotations

from pathlib import Path

from preprocess.config import load_settings


def test_load_settings_uses_repo_default_config_when_cwd_changes(
    monkeypatch, tmp_path: Path
) -> None:
    monkeypatch.chdir(tmp_path)

    settings = load_settings()

    assert settings.practice_root == Path("practice")
