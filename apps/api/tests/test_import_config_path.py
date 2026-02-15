from __future__ import annotations

from pathlib import Path

from apps.api.api.routes import import_data


def test_resolve_preprocess_config_default_is_project_absolute(
    monkeypatch, tmp_path: Path
) -> None:
    monkeypatch.delenv(import_data.PREPROCESS_CONFIG_ENV, raising=False)
    monkeypatch.setattr(import_data, "_PROJECT_ROOT", tmp_path)

    path = import_data._resolve_preprocess_config_path()

    assert path == tmp_path / "preprocess/config/default.yaml"


def test_resolve_preprocess_config_env_relative_is_project_absolute(
    monkeypatch, tmp_path: Path
) -> None:
    monkeypatch.setattr(import_data, "_PROJECT_ROOT", tmp_path)
    monkeypatch.setenv(import_data.PREPROCESS_CONFIG_ENV, "configs/preprocess.yaml")

    path = import_data._resolve_preprocess_config_path()

    assert path == (tmp_path / "configs/preprocess.yaml").resolve()


def test_get_practice_root_uses_project_root_for_relative_env(
    monkeypatch, tmp_path: Path
) -> None:
    monkeypatch.setattr(import_data, "_PROJECT_ROOT", tmp_path)
    monkeypatch.setenv("PRACTICE_DATA_ROOT", "data/practice")

    path = import_data._get_practice_root()

    assert path == (tmp_path / "data/practice").resolve()


def test_ensure_practice_root_writable_creates_missing_dirs(tmp_path: Path) -> None:
    target = tmp_path / "practice" / "nested"

    import_data._ensure_practice_root_writable(target)

    assert target.exists()
    assert target.is_dir()
