from __future__ import annotations

from pathlib import Path

import preprocess.load.ingest_service as ingest_module
from preprocess.config import Settings
from preprocess.load.ingest_service import IngestService
from preprocess.models import ExamUnit


class _Repo:
    def get_latest_success_fingerprint(self, exam_type: str, source_path: str) -> str | None:
        _ = (exam_type, source_path)
        return None


def _unit(exam_type: str, source_path: str, fingerprint: str) -> ExamUnit:
    return ExamUnit(
        exam_type=exam_type,
        source_path=source_path,
        absolute_path=Path(f"/tmp/{source_path}"),
        files=[],
        fingerprint=fingerprint,
    )


def test_discover_changed_units_filters_by_source_paths(monkeypatch) -> None:
    monkeypatch.setattr(
        ingest_module,
        "discover_exam_units",
        lambda **kwargs: [
            _unit("datastructure", "datastructure/2023秋学期第1次月测", "fp-1"),
            _unit("datastructure", "datastructure/2024春学期第2次月测", "fp-2"),
        ],
    )
    service = IngestService(repo=_Repo(), settings=Settings())

    units = service.discover_changed_units(
        exam_types=["datastructure"],
        source_paths=["datastructure\\2023秋学期第1次月测"],
    )

    assert len(units) == 1
    assert units[0].source_path == "datastructure/2023秋学期第1次月测"
