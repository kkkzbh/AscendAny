from __future__ import annotations

from pathlib import Path

from preprocess.extract import parse_exam_bundle
from preprocess.models import ExamMeta, ExamUnit, ProblemInfo, SourceFile


def _build_source_file(tmp_path: Path, relative_path: str, file_role: str) -> SourceFile:
    absolute_path = tmp_path / relative_path.replace("/", "_")
    absolute_path.write_text("test", encoding="utf-8")
    return SourceFile(
        file_role=file_role,
        relative_path=relative_path,
        absolute_path=absolute_path,
        sha256="x",
        size_bytes=absolute_path.stat().st_size,
        mtime=None,
    )


def test_parse_exam_bundle_maps_datastructure_submission_problem_codes(
    monkeypatch,
    tmp_path: Path,
) -> None:
    unit = ExamUnit(
        exam_type="datastructure",
        source_path="datastructure/test-exam",
        absolute_path=tmp_path,
        files=[
            _build_source_file(tmp_path, "答卷/0-ANSWER.html", "answer_html"),
            _build_source_file(tmp_path, "提交记录/提交记录.csv", "submission_csv"),
            _build_source_file(tmp_path, "成绩单/成绩单.xlsx", "scoreboard_xlsx"),
        ],
        fingerprint="fp",
    )
    captured: dict[str, object] = {}

    def fake_parse_exam_html(files, timezone_name):  # type: ignore[no-untyped-def]
        _ = files, timezone_name
        return ExamMeta(
            title="test-exam",
            starts_at=None,
            ends_at=None,
            duration_seconds=None,
            total_points=20.0,
            meta={
                "problem_catalog": {
                    "by_code": {
                        "1-1": {"problem_kind": "编程题"},
                        "1-2": {"problem_kind": "编程题"},
                    }
                }
            },
        )

    def fake_parse_scoreboards(  # type: ignore[no-untyped-def]
        files,
        exam_type,
        problem_metadata_by_code,
        included_problem_kinds,
    ):
        _ = files, exam_type, problem_metadata_by_code, included_problem_kinds
        return (
            [],
            [
                ProblemInfo(problem_code="7-1", problem_kind="编程题"),
                ProblemInfo(problem_code="7-2", problem_kind="编程题"),
            ],
            {},
        )

    def fake_parse_submission_csv(**kwargs):  # type: ignore[no-untyped-def]
        captured["allowed_problem_codes"] = kwargs["allowed_problem_codes"]
        captured["problem_code_aliases"] = kwargs["problem_code_aliases"]
        return []

    monkeypatch.setattr("preprocess.extract.parse_exam_html", fake_parse_exam_html)
    monkeypatch.setattr("preprocess.extract.parse_scoreboards", fake_parse_scoreboards)
    monkeypatch.setattr("preprocess.extract.parse_submission_csv", fake_parse_submission_csv)

    parse_exam_bundle(
        unit=unit,
        encodings=["utf-8"],
        timezone_name="Asia/Shanghai",
    )

    assert captured["allowed_problem_codes"] == {"7-1", "7-2"}
    assert captured["problem_code_aliases"] == {"1-1": "7-1", "1-2": "7-2"}
