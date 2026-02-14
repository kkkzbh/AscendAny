from __future__ import annotations

from pathlib import Path

import pandas as pd

from preprocess.extract.xlsx_reader import parse_scoreboards
from preprocess.models import SourceFile


def _build_source_file(path: Path) -> SourceFile:
    return SourceFile(
        file_role="scoreboard_xlsx",
        relative_path=path.name,
        absolute_path=path,
        sha256="test",
        size_bytes=path.stat().st_size,
        mtime=None,
    )


def test_parse_scoreboards_reads_problem_codes_from_previous_header_row(
    tmp_path: Path,
) -> None:
    rows = [
        [
            "题目池",
            "",
            "",
            "",
            "",
            "",
            "",
            "题目池1",
            "",
            "",
            "",
            "函数题得分",
            "题目池1",
            "",
            "",
            "",
            "编程题得分",
            "",
        ],
        [
            "题目标号",
            "",
            "",
            "",
            "",
            "",
            "",
            "6-1-1",
            "6-1-2",
            "6-1-3",
            "题目池得分",
            "",
            "7-1-1",
            "7-1-2",
            "7-1-3",
            "题目池得分",
            "",
            "",
        ],
        [
            "用户组",
            "学号/邮箱、电话",
            "姓名/昵称",
            "MOOCID",
            "总分(50.0)",
            "排名",
            "耗时(秒)",
            "15.0",
            "15.0",
            "15.0",
            "",
            "",
            "20.0",
            "20.0",
            "20.0",
            "",
            "",
            "",
        ],
        [
            "GroupA",
            "20241202001",
            "孟庆凯",
            "user-1",
            "2",
            "126",
            "3554",
            "2.0(1:11ms)",
            "-",
            "0.0(1:0ms)",
            "2",
            "2",
            "-",
            "-",
            "0.0(1:0ms)",
            "0",
            "0",
            "0",
        ],
    ]
    file_path = tmp_path / "datastructure.xlsx"
    pd.DataFrame(rows).to_excel(file_path, index=False, header=False)

    participants, problems, _ = parse_scoreboards(
        files=[_build_source_file(file_path)],
        exam_type="datastructure",
    )

    assert len(participants) == 1
    first = participants[0]
    assert first.external_id == "20241202001"
    assert first.problem_stats["6-1-1"]["runtime_ms"] == 11
    assert first.problem_stats["6-1-2"]["attempts"] == 0
    assert first.problem_stats["7-1-3"]["runtime_ms"] == 0

    problem_codes = {item.problem_code for item in problems}
    assert {"6-1-1", "6-1-2", "6-1-3", "7-1-1", "7-1-2", "7-1-3"}.issubset(
        problem_codes
    )


def test_parse_scoreboards_reads_problem_codes_from_top_row(tmp_path: Path) -> None:
    rows = [
        [
            "sheet-title",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
        ],
        [
            "题目标号",
            "",
            "",
            "",
            "",
            "",
            "",
            "7-1",
            "7-2",
            "7-3",
            "7-4",
            "7-5",
            "",
        ],
        [
            "用户组",
            "学号/邮箱、电话",
            "姓名/昵称",
            "MOOCID",
            "总分(70.0)",
            "排名",
            "耗时(秒)",
            "10.0",
            "10.0",
            "25.0",
            "25.0",
            "0.0",
            "",
        ],
        [
            "GroupB",
            "20231202019",
            "王若冰",
            "",
            "12",
            "24",
            "6229",
            "-",
            "10.0(2:3ms)",
            "-",
            "2.0(8:5ms)",
            "-",
            "12",
        ],
    ]
    file_path = tmp_path / "pta_ioi.xlsx"
    pd.DataFrame(rows).to_excel(file_path, index=False, header=False)

    participants, problems, _ = parse_scoreboards(
        files=[_build_source_file(file_path)],
        exam_type="pta_ioi",
    )

    assert len(participants) == 1
    first = participants[0]
    assert first.problem_stats["7-2"]["runtime_ms"] == 3
    assert first.problem_stats["7-4"]["attempts"] == 8

    problem_codes = {item.problem_code for item in problems}
    assert {"7-1", "7-2", "7-3", "7-4", "7-5"}.issubset(problem_codes)


def test_parse_scoreboards_filters_non_function_programming_kinds(
    tmp_path: Path,
) -> None:
    rows = [
        [
            "sheet-title",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
            "",
        ],
        [
            "题目标号",
            "",
            "",
            "",
            "",
            "",
            "",
            "1-1-1",
            "7-1-1",
            "",
        ],
        [
            "用户组",
            "学号/邮箱、电话",
            "姓名/昵称",
            "MOOCID",
            "总分(30.0)",
            "排名",
            "耗时(秒)",
            "2.0",
            "20.0",
            "",
        ],
        [
            "G1",
            "20241202001",
            "孟庆凯",
            "",
            "22",
            "1",
            "100",
            "F(2.0)",
            "20.0(2:20ms)",
            "",
        ],
    ]
    file_path = tmp_path / "mixed.xlsx"
    pd.DataFrame(rows).to_excel(file_path, index=False, header=False)

    participants, problems, meta = parse_scoreboards(
        files=[_build_source_file(file_path)],
        exam_type="datastructure",
        problem_metadata_by_code={
            "1-1-1": {"problem_kind": "判断题"},
            "7-1-1": {"problem_kind": "编程题"},
        },
        included_problem_kinds={"函数题", "编程题"},
    )

    assert len(participants) == 1
    first = participants[0]
    assert "1-1-1" not in first.problem_stats
    assert "7-1-1" in first.problem_stats
    assert first.total_score == 20.0
    assert first.solved_count == 1

    assert [item.problem_code for item in problems] == ["7-1-1"]
    assert meta["filtered_total_points"] is None
