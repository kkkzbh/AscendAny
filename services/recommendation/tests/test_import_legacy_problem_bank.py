from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

SERVICE_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SERVICE_ROOT))

from recommendation.scripts.import_legacy_problem_bank import import_problem_bank


def test_import_problem_bank_dry_run_reports_validation(tmp_path: Path) -> None:
    input_path = tmp_path / "problem_bank.json"
    report_path = tmp_path / "report.json"
    invalid_tag_report = tmp_path / "invalid_tags.csv"
    sql_report = tmp_path / "verify.sql"
    input_path.write_text(
        json.dumps(
            [
                {
                    "problem_id": "P1001",
                    "title": "最短路",
                    "description": "求最短路。",
                    "category_tags": "图论,最短路,,",
                    "solution_1": "Dijkstra",
                    "solution_2": "",
                    "link": "https://example.test/p/P1001",
                    "submission_count": "10",
                    "pass_count": "4",
                    "active": "true",
                },
                {
                    "problem_id": "P1001",
                    "description": "重复题号应出现在报告中。",
                    "category_tags": ["动态规划"],
                    "active": "false",
                },
            ],
            ensure_ascii=False,
        ),
        encoding="utf-8",
    )

    report = import_problem_bank(
        input_path,
        report_path=report_path,
        invalid_tag_report_path=invalid_tag_report,
        sql_report_path=sql_report,
        dry_run=True,
    )

    assert report["input_rows"] == 2
    assert report["valid_problem_rows"] == 2
    assert report["active_problem_rows"] == 1
    assert report["unique_problem_ids"] == 1
    assert report["duplicate_problem_ids"] == ["P1001"]
    assert report["tag_rows"] == 3
    assert report["link_present_rows"] == 1
    assert report_path.exists()
    assert invalid_tag_report.read_text(encoding="utf-8").startswith(
        "problem_id,value,reason"
    )
    assert "recommendation_problem_bank" in sql_report.read_text(encoding="utf-8")


def test_import_problem_bank_reads_legacy_sqlite_problem_set(tmp_path: Path) -> None:
    sqlite_path = tmp_path / "legacy.sqlite3"
    report_path = tmp_path / "sqlite_report.json"
    with sqlite3.connect(sqlite_path) as conn:
        conn.execute(
            """
            CREATE TABLE problem_set (
                problem_id TEXT PRIMARY KEY,
                description TEXT NOT NULL,
                category_tags TEXT,
                solution_1 TEXT,
                solution_2 TEXT,
                link TEXT,
                active INTEGER NOT NULL DEFAULT 1,
                submission_count REAL NOT NULL DEFAULT 0,
                pass_count REAL NOT NULL DEFAULT 0,
                created_at TEXT
            )
            """
        )
        conn.execute(
            """
            INSERT INTO problem_set (
                problem_id, description, category_tags, solution_1, solution_2,
                link, active, submission_count, pass_count, created_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "P2001",
                "旧 SQLite 题面",
                "数组;模拟",
                "sol1",
                "",
                "https://example.test/P2001",
                1,
                5.0,
                3.0,
                "2026-01-01T00:00:00Z",
            ),
        )

    report = import_problem_bank(
        sqlite_path,
        report_path=report_path,
        invalid_tag_report_path=None,
        sql_report_path=None,
        dry_run=True,
    )

    assert report["input_rows"] == 1
    assert report["valid_problem_rows"] == 1
    assert report["tag_rows"] == 2
    assert report["problems_without_tags"] == 0
