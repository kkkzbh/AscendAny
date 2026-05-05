from __future__ import annotations

import argparse
import csv
import hashlib
import json
import sqlite3
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import psycopg

from ..config import load_db_config
from .import_legacy_problem_bank import (
    _read_rows as _read_problem_bank_rows,
    _to_problem,
    _upsert as _upsert_problem_bank,
)


def _clean(value: Any) -> str:
    if value is None:
        return ""
    return str(value).strip().rstrip("\t").strip()


def _bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    text = _clean(value).lower()
    if text in {"", "1", "true", "t", "yes", "y", "是"}:
        return True
    if text in {"0", "false", "f", "no", "n", "否"}:
        return False
    return True


def _int(value: Any, default: int) -> int:
    try:
        return int(float(_clean(value)))
    except ValueError:
        return default


def _float(value: Any, default: float = 0.0) -> float:
    try:
        return float(_clean(value))
    except ValueError:
        return default


def _rows(path: Path | None) -> list[dict[str, Any]]:
    if path is None:
        return []
    suffix = path.suffix.lower()
    if suffix == ".json":
        raw = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(raw, list):
            raise ValueError(f"{path} must contain a JSON list")
        return [item for item in raw if isinstance(item, dict)]
    if suffix == ".jsonl":
        out: list[dict[str, Any]] = []
        for line in path.read_text(encoding="utf-8").splitlines():
            text = line.strip()
            if text:
                item = json.loads(text)
                if isinstance(item, dict):
                    out.append(item)
        return out
    if suffix == ".csv":
        with path.open("r", encoding="utf-8-sig", newline="") as handle:
            return [dict(row) for row in csv.DictReader(handle)]
    raise ValueError(f"unsupported input format: {path}")


def _sqlite_table_exists(conn: sqlite3.Connection, table_name: str) -> bool:
    return (
        conn.execute(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?",
            (table_name,),
        ).fetchone()
        is not None
    )


def _sqlite_columns(conn: sqlite3.Connection, table_name: str) -> set[str]:
    return {str(row[1]) for row in conn.execute(f"PRAGMA table_info({table_name})")}


def _read_sqlite_table(path: Path, table_name: str) -> list[dict[str, Any]]:
    if not path.exists():
        raise FileNotFoundError(path)
    with sqlite3.connect(path) as conn:
        conn.row_factory = sqlite3.Row
        if not _sqlite_table_exists(conn, table_name):
            return []
        return [dict(row) for row in conn.execute(f"SELECT * FROM {table_name}")]


def _read_sqlite_testcases(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        raise FileNotFoundError(path)
    table_name = "problem_testcase"
    with sqlite3.connect(path) as conn:
        conn.row_factory = sqlite3.Row
        if not _sqlite_table_exists(conn, table_name):
            return []
        columns = _sqlite_columns(conn, table_name)
        problem_column = "problem_id" if "problem_id" in columns else "problem_id_id"
        if problem_column not in columns:
            raise ValueError(
                f"SQLite table {table_name!r} must contain problem_id or problem_id_id"
            )
        rows = conn.execute(
            f"""
            SELECT
                id AS legacy_id,
                {problem_column} AS problem_id,
                input_data,
                output_data,
                is_sample,
                weight,
                time_limit_ms,
                memory_limit_kb,
                active,
                created_at
            FROM {table_name}
            ORDER BY {problem_column}, id
            """
        ).fetchall()
    return [dict(row) for row in rows]


def _testcase_hash(row: dict[str, Any]) -> str:
    raw = "\0".join(
        [
            _problem_id(row),
            _clean(row.get("input_data")),
            _clean(row.get("output_data")),
            "1" if _bool(row.get("is_sample")) else "0",
        ]
    )
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


def _problem_id(row: dict[str, Any]) -> str:
    return _clean(row.get("problem_id") or row.get("problem") or row.get("problem_id_id"))


def _testcase_identity(row: dict[str, Any]) -> dict[str, Any]:
    return {
        "legacy_id": row.get("legacy_id") or row.get("id"),
        "problem_id": _problem_id(row),
        "source_hash": _testcase_hash(row),
        "is_sample": _bool(row.get("is_sample")),
    }


def _existing_problem_ids(conn: psycopg.Connection, problem_ids: set[str]) -> set[str]:
    if not problem_ids:
        return set()
    existing: set[str] = set()
    with conn.cursor() as cur:
        ids = sorted(problem_ids)
        for start in range(0, len(ids), 1000):
            chunk = ids[start : start + 1000]
            cur.execute(
                """
                SELECT problem_id
                FROM ascendany.recommendation_problem_bank
                WHERE problem_id = ANY(%s)
                """,
                (chunk,),
            )
            existing.update(str(row[0]) for row in cur.fetchall())
    return existing


def _filter_missing_testcases(
    rows: list[dict[str, Any]], known_problem_ids: set[str]
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    valid: list[dict[str, Any]] = []
    missing: list[dict[str, Any]] = []
    for row in rows:
        problem_id = _problem_id(row)
        if not problem_id:
            missing.append({**_testcase_identity(row), "reason": "missing_problem_id"})
            continue
        if problem_id not in known_problem_ids:
            missing.append({**_testcase_identity(row), "reason": "problem_not_found"})
            continue
        valid.append(row)
    return valid, missing


def _dedupe_testcases(
    rows: list[dict[str, Any]]
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    deduped: list[dict[str, Any]] = []
    duplicates: list[dict[str, Any]] = []
    seen: set[tuple[str, str]] = set()
    for row in rows:
        key = (_problem_id(row), _testcase_hash(row))
        if key in seen:
            duplicates.append({**_testcase_identity(row), "reason": "duplicate_source_hash"})
            continue
        seen.add(key)
        deduped.append(row)
    return deduped, duplicates


def _parse_problem_bank_rows(
    rows: list[dict[str, Any]]
) -> tuple[list[Any], list[dict[str, str]], list[str]]:
    problems: list[Any] = []
    invalid_tags: list[dict[str, str]] = []
    duplicate_problem_ids: list[str] = []
    seen: set[str] = set()
    for row in rows:
        problem, row_invalid_tags = _to_problem(row)
        invalid_tags.extend(row_invalid_tags)
        if problem is None:
            continue
        if problem.problem_id in seen:
            duplicate_problem_ids.append(problem.problem_id)
        seen.add(problem.problem_id)
        problems.append(problem)
    return problems, invalid_tags, sorted(set(duplicate_problem_ids))


def _import_testcases(conn: psycopg.Connection, rows: list[dict[str, Any]]) -> int:
    count = 0
    with conn.cursor() as cur:
        for row in rows:
            problem_id = _problem_id(row)
            if not problem_id:
                continue
            source_hash = _testcase_hash(row)
            cur.execute(
                """
                INSERT INTO ascendany.oj_problem_testcases (
                    problem_id, input_data, output_data, is_sample, weight,
                    time_limit_ms, memory_limit_kb, active, source_hash, updated_at
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, now())
                ON CONFLICT (problem_id, source_hash)
                DO UPDATE SET
                    input_data = EXCLUDED.input_data,
                    output_data = EXCLUDED.output_data,
                    is_sample = EXCLUDED.is_sample,
                    weight = EXCLUDED.weight,
                    time_limit_ms = EXCLUDED.time_limit_ms,
                    memory_limit_kb = EXCLUDED.memory_limit_kb,
                    active = EXCLUDED.active,
                    updated_at = now()
                """,
                (
                    problem_id,
                    _clean(row.get("input_data")),
                    _clean(row.get("output_data")),
                    _bool(row.get("is_sample")),
                    _float(row.get("weight"), 1.0),
                    _int(row.get("time_limit_ms"), 1000),
                    _int(row.get("memory_limit_kb"), 262144),
                    _bool(row.get("active")),
                    source_hash,
                ),
            )
            count += 1
    return count


def _submit_hash(row: dict[str, Any]) -> str:
    raw = json.dumps(row, ensure_ascii=False, sort_keys=True, default=str)
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


def _import_submits(conn: psycopg.Connection, rows: list[dict[str, Any]]) -> int:
    count = 0
    with conn.cursor() as cur:
        for row in rows:
            problem_id = _problem_id(row)
            code = _clean(row.get("code_content"))
            if not problem_id or not code:
                continue
            legacy_hash = _submit_hash(row)
            extra = {"legacy_import_hash": legacy_hash}
            cur.execute(
                """
                INSERT INTO ascendany.oj_submit_records (
                    student_id, student_name, problem_id, code_content, language,
                    submit_time, status, error_message, score, score_rate,
                    memory_kb, runtime_ms, session, dataset, global_problem_id,
                    extra, is_correct
                )
                SELECT %s, %s, %s, %s, %s, COALESCE(%s::timestamptz, now()), %s, %s,
                       %s, %s, %s, %s, %s, %s, %s, %s::jsonb, %s
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM ascendany.oj_submit_records
                    WHERE extra->>'legacy_import_hash' = %s
                )
                """,
                (
                    _clean(row.get("student_id")),
                    _clean(row.get("student_name")),
                    problem_id,
                    code,
                    _clean(row.get("language")) or "C++",
                    _clean(row.get("submit_time")) or None,
                    _clean(row.get("status")) or "Pending",
                    _clean(row.get("error_message")) or None,
                    _float(row.get("score")),
                    _float(row.get("score_rate")),
                    _int(row.get("memory_kb"), 0),
                    _int(row.get("runtime_ms"), 0),
                    _clean(row.get("session")),
                    _clean(row.get("dataset")),
                    _clean(row.get("global_problem_id")) or problem_id,
                    json.dumps(extra, ensure_ascii=False),
                    _bool(row.get("is_correct")),
                    legacy_hash,
                ),
            )
            count += 1
    return count


def import_legacy_web_oj(
    *,
    problem_bank_path: Path | None = None,
    testcases_path: Path | None,
    submits_path: Path | None,
    sqlite_path: Path | None = None,
    report_path: Path,
    dry_run: bool,
) -> dict[str, Any]:
    if problem_bank_path is not None:
        problem_rows = _read_problem_bank_rows(problem_bank_path)
    elif sqlite_path is not None:
        problem_rows = _read_sqlite_table(sqlite_path, "problem_set")
    else:
        problem_rows = []
    problem_validation_available = problem_bank_path is not None or sqlite_path is not None

    if testcases_path is not None:
        testcase_rows = _rows(testcases_path)
    elif sqlite_path is not None:
        testcase_rows = _read_sqlite_testcases(sqlite_path)
    else:
        testcase_rows = []
    submit_rows = _rows(submits_path)

    problems, invalid_tags, duplicate_problem_ids = _parse_problem_bank_rows(problem_rows)
    imported_problem_ids = {item.problem_id for item in problems}
    deduped_testcase_rows, duplicate_testcases = _dedupe_testcases(testcase_rows)

    testcase_count = 0
    submit_count = 0
    valid_testcase_rows: list[dict[str, Any]] = []
    missing_problem_testcases: list[dict[str, Any]] = []
    if not dry_run:
        with psycopg.connect(load_db_config().dsn, prepare_threshold=None) as conn:
            if problems:
                _upsert_problem_bank(conn, problems)
            testcase_problem_ids = {
                _problem_id(row) for row in deduped_testcase_rows if _problem_id(row)
            }
            known_problem_ids = _existing_problem_ids(conn, testcase_problem_ids)
            valid_testcase_rows, missing_problem_testcases = _filter_missing_testcases(
                deduped_testcase_rows, known_problem_ids
            )
            testcase_count = _import_testcases(conn, valid_testcase_rows)
            submit_count = _import_submits(conn, submit_rows)
    elif problem_validation_available:
        valid_testcase_rows, missing_problem_testcases = _filter_missing_testcases(
            deduped_testcase_rows, imported_problem_ids
        )
    else:
        valid_testcase_rows = [
            row for row in deduped_testcase_rows if _problem_id(row)
        ]
        missing_problem_testcases = [
            {**_testcase_identity(row), "reason": "missing_problem_id"}
            for row in deduped_testcase_rows
            if not _problem_id(row)
        ]

    report = {
        "sqlite_path": str(sqlite_path) if sqlite_path else None,
        "problem_bank_path": str(problem_bank_path) if problem_bank_path else None,
        "testcases_path": str(testcases_path) if testcases_path else None,
        "submits_path": str(submits_path) if submits_path else None,
        "dry_run": dry_run,
        "input_problem_rows": len(problem_rows),
        "valid_problem_rows": len(problems),
        "unique_problem_ids": len(imported_problem_ids),
        "duplicate_problem_ids": duplicate_problem_ids,
        "problems_without_tags": sum(1 for item in problems if not item.tags),
        "invalid_tags": invalid_tags,
        "input_testcase_rows": len(testcase_rows),
        "unique_testcase_rows": len(deduped_testcase_rows),
        "duplicate_testcases": duplicate_testcases,
        "valid_testcase_rows": len(valid_testcase_rows),
        "missing_problem_testcases": missing_problem_testcases,
        "missing_problem_testcase_rows": len(missing_problem_testcases),
        "input_submit_rows": len(submit_rows),
        "processed_testcase_rows": testcase_count if not dry_run else len(valid_testcase_rows),
        "processed_submit_rows": submit_count if not dry_run else len(submit_rows),
        "generated_at": datetime.now(UTC).isoformat(),
    }
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--sqlite-db",
        type=Path,
        help="旧 x Django SQLite DB；读取 problem_set 和 problem_testcase。",
    )
    parser.add_argument(
        "--problem-bank",
        type=Path,
        help="旧题库 CSV/JSON/JSONL/SQLite 导出；不传时可由 --sqlite-db 读取 problem_set。",
    )
    parser.add_argument("--testcases", type=Path)
    parser.add_argument("--submits", type=Path)
    parser.add_argument(
        "--report",
        type=Path,
        default=Path("var/recommendation/import_reports/web_oj_import.json"),
    )
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    if (
        args.sqlite_db is None
        and args.problem_bank is None
        and args.testcases is None
        and args.submits is None
    ):
        parser.error(
            "at least one of --sqlite-db, --problem-bank, --testcases or --submits is required"
        )
    report = import_legacy_web_oj(
        problem_bank_path=args.problem_bank,
        testcases_path=args.testcases,
        submits_path=args.submits,
        sqlite_path=args.sqlite_db,
        report_path=args.report,
        dry_run=args.dry_run,
    )
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
