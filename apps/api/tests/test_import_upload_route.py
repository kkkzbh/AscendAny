from __future__ import annotations

import io
import json
import zipfile
from pathlib import Path

from fastapi.testclient import TestClient

from apps.api.api.deps import get_admin_account
from apps.api.main import create_app
from apps.api.services.auth import AuthenticatedAccount


class _StubRepo:
    pass


class _StubLLM:
    pass


def _build_zip_bytes() -> bytes:
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, mode="w", compression=zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("sample.txt", "hello")
    return buffer.getvalue()


def _build_pintia_json_bytes() -> bytes:
    return json.dumps(
        {
            "schema": "ascendany.pintia.unit.v1",
            "exam": {"problemSetId": "ps-1"},
            "problems": [],
            "participants": [],
            "rankings": [],
            "submissions": [],
            "integrity": {
                "problemCount": 0,
                "participantCount": 0,
                "rankingCount": 0,
                "submissionCount": 0,
                "submissionDetailCount": 0,
                "codeCount": 0,
            },
        },
        ensure_ascii=False,
    ).encode("utf-8")


def test_upload_zip_rejects_existing_exam_directory(
    tmp_path: Path, monkeypatch
) -> None:
    practice_root = tmp_path / "practice"
    existing_exam_dir = practice_root / "datastructure" / "exam-1"
    existing_exam_dir.mkdir(parents=True, exist_ok=True)
    marker_file = existing_exam_dir / "keep.txt"
    marker_file.write_text("keep", encoding="utf-8")

    monkeypatch.setenv("PRACTICE_DATA_ROOT", str(practice_root))

    app = create_app(repository=_StubRepo(), llm_service=_StubLLM())
    app.dependency_overrides[get_admin_account] = (
        lambda: AuthenticatedAccount(
            account_id=1,
            username="admin",
            is_admin=True,
        )
    )

    with TestClient(app) as client:
        response = client.post(
            "/api/v1/import/upload",
            data={"examType": "datastructure"},
            files={"file": ("exam-1.zip", _build_zip_bytes(), "application/zip")},
        )

    assert response.status_code == 409
    assert response.json() == {"detail": "该zip名字已存在，请换一个"}
    assert marker_file.read_text(encoding="utf-8") == "keep"


def test_upload_pintia_json_does_not_require_exam_type(
    tmp_path: Path, monkeypatch
) -> None:
    practice_root = tmp_path / "practice"
    monkeypatch.setenv("PRACTICE_DATA_ROOT", str(practice_root))

    app = create_app(repository=_StubRepo(), llm_service=_StubLLM())
    app.dependency_overrides[get_admin_account] = (
        lambda: AuthenticatedAccount(
            account_id=1,
            username="admin",
            is_admin=True,
        )
    )

    with TestClient(app) as client:
        response = client.post(
            "/api/v1/import/upload",
            files={
                "file": (
                    "unit.json",
                    _build_pintia_json_bytes(),
                    "application/json",
                )
            },
        )

    assert response.status_code == 200
    payload = response.json()
    assert payload["examType"] == "pintia"
    assert payload["sourcePath"] == "pintia/unit.json"
    assert (practice_root / "pintia" / "unit.json").exists()
