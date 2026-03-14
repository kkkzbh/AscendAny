from __future__ import annotations

from datetime import datetime
from pathlib import Path

from preprocess.extract.csv_reader import decode_csv, parse_submission_csv
from preprocess.models import SourceFile


def test_decode_csv_fallback_gb18030(tmp_path: Path) -> None:
    file_path = tmp_path / "submission.csv"
    payload = '"2025/02/03 21:24:53\\t","答案错误\\t","详情\\t","L2-9\\t","20230513032\\t","付丹怡\\t","Python (python3)\\t","2856 KB\\t","14 ms\\t"\n'
    file_path.write_bytes(payload.encode("gb18030"))

    decoded = decode_csv(file_path, encodings=["utf-8", "gb18030"])
    assert "付丹怡" in decoded


def test_parse_submission_csv_generates_stable_hash(tmp_path: Path) -> None:
    file_path = tmp_path / "submission.csv"
    file_path.write_text(
        "2025/03/26 11:09:31\t,答案正确\t,15\t,6-1-2\t,kami\t,C (gcc)\t,572 KB\t,1 ms\t,\n",
        encoding="utf-8",
    )
    source = SourceFile(
        file_role="submission_csv",
        relative_path="提交记录/提交记录.csv",
        absolute_path=file_path,
        sha256="x",
        size_bytes=file_path.stat().st_size,
        mtime=datetime.now().astimezone(),
    )
    rows_a = parse_submission_csv(
        file=source,
        exam_type="datastructure",
        encodings=["utf-8", "gb18030"],
        timezone_name="Asia/Shanghai",
    )
    rows_b = parse_submission_csv(
        file=source,
        exam_type="datastructure",
        encodings=["utf-8", "gb18030"],
        timezone_name="Asia/Shanghai",
    )
    assert len(rows_a) == 1
    assert len(rows_b) == 1
    assert rows_a[0].row_hash == rows_b[0].row_hash


def test_parse_submission_csv_supports_datastructure_student_no_layout(
    tmp_path: Path,
) -> None:
    file_path = tmp_path / "submission.csv"
    file_path.write_text(
        "2026/03/06 10:29:31\t,Accepted\t,5\t,1-1\t,20251202099\t,常根全\t,C (gcc)\t,564 KB\t,1 ms\t,\n",
        encoding="utf-8",
    )
    source = SourceFile(
        file_role="submission_csv",
        relative_path="提交记录/提交记录.csv",
        absolute_path=file_path,
        sha256="x",
        size_bytes=file_path.stat().st_size,
        mtime=datetime.now().astimezone(),
    )

    rows = parse_submission_csv(
        file=source,
        exam_type="datastructure",
        encodings=["utf-8", "gb18030"],
        timezone_name="Asia/Shanghai",
    )

    assert len(rows) == 1
    row = rows[0]
    assert row.actor_source == "datastructure_student_no"
    assert row.actor_external_id == "20251202099"
    assert row.actor_name == "常根全"
    assert row.language == "C (gcc)"
    assert row.memory_kb == 564
    assert row.time_ms == 1
    assert row.verdict == "答案正确"
    assert row.raw["datastructure_layout"] == "student_no"


def test_parse_submission_csv_normalizes_verdict_language(tmp_path: Path) -> None:
    english_file = tmp_path / "english.csv"
    chinese_file = tmp_path / "chinese.csv"
    english_file.write_text(
        "2026/03/06 10:29:31\t,Accepted\t,5\t,1-1\t,20251202099\t,常根全\t,C (gcc)\t,564 KB\t,1 ms\t,\n",
        encoding="utf-8",
    )
    chinese_file.write_text(
        "2026/03/06 10:29:31\t,答案正确\t,5\t,1-1\t,20251202099\t,常根全\t,C (gcc)\t,564 KB\t,1 ms\t,\n",
        encoding="utf-8",
    )
    source_a = SourceFile(
        file_role="submission_csv",
        relative_path="提交记录/english.csv",
        absolute_path=english_file,
        sha256="x",
        size_bytes=english_file.stat().st_size,
        mtime=datetime.now().astimezone(),
    )
    source_b = SourceFile(
        file_role="submission_csv",
        relative_path="提交记录/chinese.csv",
        absolute_path=chinese_file,
        sha256="y",
        size_bytes=chinese_file.stat().st_size,
        mtime=datetime.now().astimezone(),
    )

    row_a = parse_submission_csv(
        file=source_a,
        exam_type="datastructure",
        encodings=["utf-8", "gb18030"],
        timezone_name="Asia/Shanghai",
    )[0]
    row_b = parse_submission_csv(
        file=source_b,
        exam_type="datastructure",
        encodings=["utf-8", "gb18030"],
        timezone_name="Asia/Shanghai",
    )[0]

    assert row_a.verdict == "答案正确"
    assert row_b.verdict == "答案正确"
    assert row_a.row_hash == row_b.row_hash


def test_parse_submission_csv_filters_problem_codes(tmp_path: Path) -> None:
    file_path = tmp_path / "submission.csv"
    file_path.write_text(
        "\n".join(
            [
                "2025/03/26 11:09:31\t,答案正确\t,15\t,6-1-2\t,kami\t,C (gcc)\t,572 KB\t,1 ms\t,",
                "2025/03/26 11:09:30\t,部分正确\t,1\t,单选题\t,kami\t,-\t,0 KB\t,0 ms\t,",
            ]
        ),
        encoding="utf-8",
    )
    source = SourceFile(
        file_role="submission_csv",
        relative_path="提交记录/提交记录.csv",
        absolute_path=file_path,
        sha256="x",
        size_bytes=file_path.stat().st_size,
        mtime=datetime.now().astimezone(),
    )
    rows = parse_submission_csv(
        file=source,
        exam_type="datastructure",
        encodings=["utf-8", "gb18030"],
        timezone_name="Asia/Shanghai",
        allowed_problem_codes={"6-1-2"},
    )

    assert len(rows) == 1
    assert rows[0].problem_code == "6-1-2"
