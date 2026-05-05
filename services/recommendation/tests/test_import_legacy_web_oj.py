from __future__ import annotations

import sqlite3
import sys
from pathlib import Path

SERVICE_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SERVICE_ROOT))

from recommendation.scripts.import_legacy_web_oj import import_legacy_web_oj


def test_import_legacy_web_oj_sqlite_dry_run_reports_validation(tmp_path: Path) -> None:
    sqlite_path = tmp_path / "legacy.sqlite3"
    report_path = tmp_path / "web_oj_report.json"
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
            CREATE TABLE problem_testcase (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                problem_id TEXT NOT NULL,
                input_data TEXT NOT NULL,
                output_data TEXT NOT NULL,
                is_sample INTEGER NOT NULL DEFAULT 0,
                weight REAL NOT NULL DEFAULT 1.0,
                time_limit_ms INTEGER NOT NULL DEFAULT 1000,
                memory_limit_kb INTEGER NOT NULL DEFAULT 262144,
                active INTEGER NOT NULL DEFAULT 1,
                created_at TEXT
            )
            """
        )
        conn.executemany(
            """
            INSERT INTO problem_set (
                problem_id, description, category_tags, solution_1, solution_2,
                link, active, submission_count, pass_count, created_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            [
                ("P3001", "有标签题", "图论,最短路", "", "", "", 1, 10, 7, ""),
                ("P3002", "无标签题", "", "", "", "", 1, 2, 1, ""),
            ],
        )
        conn.executemany(
            """
            INSERT INTO problem_testcase (
                problem_id, input_data, output_data, is_sample, weight,
                time_limit_ms, memory_limit_kb, active, created_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            [
                ("P3001", "1 2\n", "3\n", 1, 1.0, 1000, 262144, 1, ""),
                ("P3001", "1 2\n", "3\n", 1, 1.0, 1000, 262144, 1, ""),
                ("MISSING", "x\n", "y\n", 0, 1.0, 1000, 262144, 1, ""),
            ],
        )

    report = import_legacy_web_oj(
        sqlite_path=sqlite_path,
        problem_bank_path=None,
        testcases_path=None,
        submits_path=None,
        report_path=report_path,
        dry_run=True,
    )

    assert report["input_problem_rows"] == 2
    assert report["valid_problem_rows"] == 2
    assert report["problems_without_tags"] == 1
    assert report["input_testcase_rows"] == 3
    assert report["unique_testcase_rows"] == 2
    assert len(report["duplicate_testcases"]) == 1
    assert report["valid_testcase_rows"] == 1
    assert report["missing_problem_testcase_rows"] == 1
    assert report["missing_problem_testcases"][0]["problem_id"] == "MISSING"
    assert report_path.exists()
