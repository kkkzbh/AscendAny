from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

SERVICE_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SERVICE_ROOT))

from recommendation.scripts.export_legacy_web_oj_bundle import export_legacy_web_oj_bundle
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


def test_import_legacy_web_oj_mysql_dump_dry_run_ignores_pta_rows(
    tmp_path: Path,
) -> None:
    dump_path = tmp_path / "legacy_x.sql"
    report_path = tmp_path / "mysql_report.json"
    dump_path.write_text(
        "\n".join(
            [
                "CREATE TABLE `problem_set`  (",
                "  `id` bigint NOT NULL AUTO_INCREMENT,",
                "  `problem_id` varchar(50) NOT NULL,",
                "  `description` longtext NOT NULL,",
                "  `category_tags` varchar(255) NULL DEFAULT NULL,",
                "  `solution_1` longtext NULL,",
                "  `solution_2` longtext NULL,",
                "  `link` varchar(200) NULL DEFAULT NULL,",
                "  `submission_count` double NOT NULL,",
                "  `pass_count` double NOT NULL,",
                "  `created_at` datetime(6) NOT NULL,",
                "  PRIMARY KEY (`id`) USING BTREE",
                ") ENGINE = InnoDB;",
                "INSERT INTO `problem_set` VALUES (1, 'bank_P1', '题面\\n含换行和\\'引号', '图论;最短路', NULL, '', 'https://example.test/P1', 12.5, 7, '2026-01-01 00:00:00.000000');",
                "CREATE TABLE `problem_testcase`  (",
                "  `id` bigint NOT NULL AUTO_INCREMENT,",
                "  `input_data` longtext NOT NULL,",
                "  `output_data` longtext NOT NULL,",
                "  `is_sample` tinyint(1) NOT NULL,",
                "  `weight` double NOT NULL,",
                "  `time_limit_ms` int NOT NULL,",
                "  `memory_limit_kb` int NOT NULL,",
                "  `active` tinyint(1) NOT NULL,",
                "  `created_at` datetime(6) NOT NULL,",
                "  `problem_id` varchar(50) NOT NULL,",
                "  PRIMARY KEY (`id`) USING BTREE",
                ") ENGINE = InnoDB;",
                "INSERT INTO `problem_testcase` VALUES (10, '1 2\\n', '3\\n', 1, 1, 1000, 262144, 1, '2026-01-01 00:00:00.000000', 'bank_P1');",
                "INSERT INTO `problem_testcase` VALUES (11, '1 2\\n', '3\\n', 1, 1, 1000, 262144, 1, '2026-01-01 00:00:00.000000', 'bank_P1');",
                "CREATE TABLE `pta_submit_record`  (",
                "  `id` bigint NOT NULL AUTO_INCREMENT,",
                "  `code_content` longtext NOT NULL,",
                "  `problem_id` varchar(50) NOT NULL,",
                "  PRIMARY KEY (`id`) USING BTREE",
                ") ENGINE = InnoDB;",
                "INSERT INTO `pta_submit_record` VALUES (1, 'int main(){}', '7-1');",
                "",
            ]
        ),
        encoding="utf-8",
    )

    report = import_legacy_web_oj(
        mysql_dump_path=dump_path,
        sqlite_path=None,
        problem_bank_path=None,
        testcases_path=None,
        submits_path=None,
        report_path=report_path,
        dry_run=True,
    )

    assert report["mysql_dump_path"] == str(dump_path)
    assert report["input_problem_rows"] == 1
    assert report["valid_problem_rows"] == 1
    assert report["input_testcase_rows"] == 2
    assert report["unique_testcase_rows"] == 2
    assert report["valid_testcase_rows"] == 2
    assert report["ignored_pta_submit_rows"] == 1
    assert report["input_submit_rows"] == 0
    assert report_path.exists()


def test_export_legacy_web_oj_bundle_and_import_dry_run(tmp_path: Path) -> None:
    dump_path = tmp_path / "legacy_x.sql"
    bundle_path = tmp_path / "bundle"
    report_path = tmp_path / "bundle_report.json"
    dump_path.write_text(
        "\n".join(
            [
                "CREATE TABLE `problem_set`  (",
                "  `id` bigint NOT NULL AUTO_INCREMENT,",
                "  `problem_id` varchar(50) NOT NULL,",
                "  `description` longtext NOT NULL,",
                "  `category_tags` varchar(255) NULL DEFAULT NULL,",
                "  `solution_1` longtext NULL,",
                "  `solution_2` longtext NULL,",
                "  `link` varchar(200) NULL DEFAULT NULL,",
                "  `submission_count` double NOT NULL,",
                "  `pass_count` double NOT NULL,",
                "  `created_at` datetime(6) NOT NULL,",
                "  PRIMARY KEY (`id`) USING BTREE",
                ") ENGINE = InnoDB;",
                "INSERT INTO `problem_set` VALUES (1, 'bank_P1', '题面', '图论;最短路', NULL, '', 'https://example.test/P1', 12.5, 7, '2026-01-01 00:00:00.000000');",
                "CREATE TABLE `problem_testcase`  (",
                "  `id` bigint NOT NULL AUTO_INCREMENT,",
                "  `input_data` longtext NOT NULL,",
                "  `output_data` longtext NOT NULL,",
                "  `is_sample` tinyint(1) NOT NULL,",
                "  `weight` double NOT NULL,",
                "  `time_limit_ms` int NOT NULL,",
                "  `memory_limit_kb` int NOT NULL,",
                "  `active` tinyint(1) NOT NULL,",
                "  `created_at` datetime(6) NOT NULL,",
                "  `problem_id` varchar(50) NOT NULL,",
                "  PRIMARY KEY (`id`) USING BTREE",
                ") ENGINE = InnoDB;",
                "INSERT INTO `problem_testcase` VALUES (10, '1 2\\n', '3\\n', 1, 1, 1000, 262144, 1, '2026-01-01 00:00:00.000000', 'bank_P1');",
                "CREATE TABLE `pta_submit_record`  (",
                "  `id` bigint NOT NULL AUTO_INCREMENT,",
                "  `code_content` longtext NOT NULL,",
                "  `problem_id` varchar(50) NOT NULL,",
                "  PRIMARY KEY (`id`) USING BTREE",
                ") ENGINE = InnoDB;",
                "INSERT INTO `pta_submit_record` VALUES (1, 'int main(){}', '7-1');",
                "",
            ]
        ),
        encoding="utf-8",
    )

    export_report = export_legacy_web_oj_bundle(
        mysql_dump=dump_path,
        output=bundle_path,
    )

    manifest = json.loads((bundle_path / "manifest.json").read_text(encoding="utf-8"))
    problem = json.loads((bundle_path / "problems" / "bank_P1.json").read_text(encoding="utf-8"))
    assert export_report["problem_count"] == 1
    assert export_report["testcase_count"] == 1
    assert manifest["schema_version"] == "legacy-web-oj.problem-bank.v1"
    assert manifest["ignored_tables"] == ["pta_submit_record"]
    assert manifest["ignored_table_rows"]["pta_submit_record"] == 1
    assert problem["raw_category_tags"] == "图论;最短路"
    assert problem["canonical_tags"] == ["图", "最短路"]
    assert not any("pta_submit_record" in item.name for item in bundle_path.rglob("*"))
    assert (bundle_path / "checksums.sha256").exists()

    import_report = import_legacy_web_oj(
        bundle_path=bundle_path,
        mysql_dump_path=None,
        sqlite_path=None,
        problem_bank_path=None,
        testcases_path=None,
        submits_path=None,
        report_path=report_path,
        dry_run=True,
    )

    assert import_report["bundle_path"] == str(bundle_path)
    assert import_report["input_problem_rows"] == 1
    assert import_report["valid_problem_rows"] == 1
    assert import_report["input_testcase_rows"] == 1
    assert import_report["valid_testcase_rows"] == 1
    assert import_report["ignored_pta_submit_rows"] == 1
