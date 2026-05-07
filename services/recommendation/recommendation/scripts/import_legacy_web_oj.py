from __future__ import annotations

import argparse
import csv
import hashlib
import json
import sqlite3
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from urllib.parse import unquote

import psycopg

from ..config import load_db_config
from .import_legacy_problem_bank import (
    _read_rows as _read_problem_bank_rows,
    _to_problem,
    _upsert as _upsert_problem_bank,
)

BUNDLE_SCHEMA_VERSION = "legacy-web-oj.problem-bank.v1"


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


def _read_json_object(path: Path) -> dict[str, Any]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return raw


def _read_jsonl_objects(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        text = line.strip()
        if not text:
            continue
        item = json.loads(text)
        if not isinstance(item, dict):
            raise ValueError(f"{path} must contain one JSON object per non-empty line")
        rows.append(item)
    return rows


def _read_bundle(
    bundle_path: Path,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], dict[str, Any]]:
    if not bundle_path.exists():
        raise FileNotFoundError(bundle_path)
    if not bundle_path.is_dir():
        raise ValueError(f"bundle path must be a directory: {bundle_path}")

    manifest_path = bundle_path / "manifest.json"
    checksums_path = bundle_path / "checksums.sha256"
    manifest = _read_json_object(manifest_path)
    if manifest.get("schema_version") != BUNDLE_SCHEMA_VERSION:
        raise ValueError(
            f"unsupported bundle schema_version: {manifest.get('schema_version')!r}"
        )
    _verify_bundle_checksums(bundle_path, checksums_path)

    problem_rows: list[dict[str, Any]] = []
    problems_dir = bundle_path / "problems"
    for path in sorted(problems_dir.glob("*.json")):
        item = _read_json_object(path)
        problem_id = _clean(item.get("problem_id")) or unquote(path.stem)
        problem_rows.append(
            {
                "problem_id": problem_id,
                "title": item.get("title"),
                "description": item.get("description"),
                "category_tags": item.get("raw_category_tags")
                if item.get("raw_category_tags") is not None
                else " ".join(str(tag) for tag in item.get("canonical_tags", [])),
                "solution_1": item.get("solution_1"),
                "solution_2": item.get("solution_2"),
                "link": item.get("link"),
                "active": item.get("active"),
                "submission_count": item.get("submission_count"),
                "pass_count": item.get("pass_count"),
                "created_at": item.get("created_at"),
                "__bundle_problem_path": str(path.relative_to(bundle_path)),
            }
        )

    testcase_rows: list[dict[str, Any]] = []
    testcases_dir = bundle_path / "testcases"
    for path in sorted(testcases_dir.glob("*.jsonl")):
        for item in _read_jsonl_objects(path):
            item["__bundle_testcase_path"] = str(path.relative_to(bundle_path))
            testcase_rows.append(item)

    expected_problem_count = manifest.get("problem_count")
    if expected_problem_count is not None and int(expected_problem_count) != len(problem_rows):
        raise ValueError(
            f"bundle problem_count mismatch: manifest={expected_problem_count}, files={len(problem_rows)}"
        )
    expected_testcase_count = manifest.get("testcase_count")
    if expected_testcase_count is not None and int(expected_testcase_count) != len(testcase_rows):
        raise ValueError(
            f"bundle testcase_count mismatch: manifest={expected_testcase_count}, rows={len(testcase_rows)}"
        )
    return problem_rows, testcase_rows, manifest


def _verify_bundle_checksums(bundle_path: Path, checksums_path: Path) -> None:
    if not checksums_path.exists():
        raise FileNotFoundError(checksums_path)

    declared: dict[str, str] = {}
    for line in checksums_path.read_text(encoding="utf-8").splitlines():
        text = line.strip()
        if not text:
            continue
        digest, relative = text.split(None, 1)
        declared[relative.strip()] = digest

    payload_paths = [
        path
        for path in [
            bundle_path / "manifest.json",
            *sorted((bundle_path / "problems").glob("*.json")),
            *sorted((bundle_path / "testcases").glob("*.jsonl")),
        ]
        if path.exists()
    ]
    expected = {str(path.relative_to(bundle_path)) for path in payload_paths}
    missing = sorted(expected - set(declared))
    if missing:
        raise ValueError(f"bundle checksums missing entries: {missing}")
    extra = sorted(set(declared) - expected)
    if extra:
        raise ValueError(f"bundle checksums contain unknown entries: {extra}")

    for relative, expected_digest in sorted(declared.items()):
        path = bundle_path / relative
        actual = hashlib.sha256(path.read_bytes()).hexdigest()
        if actual != expected_digest:
            raise ValueError(f"bundle checksum mismatch for {relative}")


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


def _read_mysql_dump_tables(
    path: Path, table_names: set[str]
) -> dict[str, list[dict[str, Any]]]:
    if not path.exists():
        raise FileNotFoundError(path)

    columns: dict[str, list[str]] = {name: [] for name in table_names}
    rows: dict[str, list[dict[str, Any]]] = {name: [] for name in table_names}
    current_table: str | None = None
    with path.open("r", encoding="utf-8", errors="replace") as handle:
        for raw_line in handle:
            line = raw_line.rstrip("\r\n")
            if line.startswith("CREATE TABLE `"):
                table_name = line.split("`", 2)[1]
                current_table = table_name if table_name in table_names else None
                continue
            if current_table is not None:
                stripped = line.strip()
                if stripped.startswith("`"):
                    columns[current_table].append(stripped.split("`", 2)[1])
                    continue
                if stripped.startswith(")"):
                    current_table = None
                    continue
            if not line.startswith("INSERT INTO `"):
                continue
            table_name = line.split("`", 2)[1]
            if table_name not in table_names:
                continue
            table_columns = columns.get(table_name) or []
            if not table_columns:
                raise ValueError(f"MySQL dump table {table_name!r} has no parsed columns")
            values_sql = line.split(" VALUES ", 1)[1].strip()
            for values in _parse_mysql_values(values_sql):
                if len(values) != len(table_columns):
                    raise ValueError(
                        f"MySQL dump row for {table_name!r} has {len(values)} values, "
                        f"expected {len(table_columns)}"
                    )
                row = dict(zip(table_columns, values, strict=True))
                row["__mysql_dump_table"] = table_name
                rows[table_name].append(row)
    return rows


def _parse_mysql_values(values_sql: str) -> list[list[Any]]:
    sql = values_sql.strip()
    if sql.endswith(";"):
        sql = sql[:-1]

    rows: list[list[Any]] = []
    index = 0
    length = len(sql)
    while index < length:
        index = _skip_mysql_ws(sql, index)
        if index >= length:
            break
        if sql[index] != "(":
            raise ValueError("MySQL INSERT VALUES must contain parenthesized rows")
        index += 1
        values: list[Any] = []
        while True:
            index = _skip_mysql_ws(sql, index)
            value, index = _parse_mysql_value(sql, index)
            values.append(value)
            index = _skip_mysql_ws(sql, index)
            if index >= length:
                raise ValueError("unterminated MySQL INSERT row")
            if sql[index] == ",":
                index += 1
                continue
            if sql[index] == ")":
                index += 1
                break
            raise ValueError(f"unexpected MySQL INSERT character: {sql[index]!r}")
        rows.append(values)
        index = _skip_mysql_ws(sql, index)
        if index < length and sql[index] == ",":
            index += 1
    return rows


def _skip_mysql_ws(sql: str, index: int) -> int:
    while index < len(sql) and sql[index].isspace():
        index += 1
    return index


def _parse_mysql_value(sql: str, index: int) -> tuple[Any, int]:
    if index < len(sql) and sql[index] == "'":
        return _parse_mysql_string(sql, index + 1)

    start = index
    while index < len(sql) and sql[index] not in ",)":
        index += 1
    token = sql[start:index].strip()
    upper = token.upper()
    if upper == "NULL":
        return None, index
    if upper in {"TRUE", "FALSE"}:
        return upper == "TRUE", index
    if token.startswith(("0x", "0X")):
        try:
            return bytes.fromhex(token[2:]).decode("utf-8"), index
        except (UnicodeDecodeError, ValueError):
            return token, index
    try:
        return int(token), index
    except ValueError:
        pass
    try:
        return float(token), index
    except ValueError:
        return token, index


def _parse_mysql_string(sql: str, index: int) -> tuple[str, int]:
    out: list[str] = []
    while index < len(sql):
        char = sql[index]
        if char == "'":
            return "".join(out), index + 1
        if char == "\\" and index + 1 < len(sql):
            escaped = sql[index + 1]
            out.append(
                {
                    "0": "\0",
                    "b": "\b",
                    "n": "\n",
                    "r": "\r",
                    "t": "\t",
                    "Z": "\x1a",
                }.get(escaped, escaped)
            )
            index += 2
            continue
        out.append(char)
        index += 1
    raise ValueError("unterminated MySQL string literal")


def _testcase_hash(row: dict[str, Any]) -> str:
    source_hash = _clean(row.get("source_hash"))
    if source_hash:
        return source_hash
    parts = [
        _problem_id(row),
        _clean(row.get("input_data")),
        _clean(row.get("output_data")),
        "1" if _bool(row.get("is_sample")) else "0",
    ]
    if row.get("__mysql_dump_table") == "problem_testcase":
        parts.append(_clean(row.get("legacy_id") or row.get("id")))
    raw = "\0".join(parts)
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
    bundle_path: Path | None = None,
    problem_bank_path: Path | None = None,
    testcases_path: Path | None,
    submits_path: Path | None,
    sqlite_path: Path | None = None,
    mysql_dump_path: Path | None = None,
    report_path: Path,
    dry_run: bool,
) -> dict[str, Any]:
    bundle_manifest: dict[str, Any] | None = None
    bundle_problem_rows: list[dict[str, Any]] = []
    bundle_testcase_rows: list[dict[str, Any]] = []
    if bundle_path is not None:
        bundle_problem_rows, bundle_testcase_rows, bundle_manifest = _read_bundle(
            bundle_path
        )

    mysql_tables = (
        _read_mysql_dump_tables(
            mysql_dump_path,
            {"problem_set", "problem_testcase", "pta_submit_record"},
        )
        if mysql_dump_path is not None
        else {}
    )
    if bundle_path is not None:
        problem_rows = bundle_problem_rows
    elif problem_bank_path is not None:
        problem_rows = _read_problem_bank_rows(problem_bank_path)
    elif mysql_dump_path is not None:
        problem_rows = mysql_tables.get("problem_set", [])
    elif sqlite_path is not None:
        problem_rows = _read_sqlite_table(sqlite_path, "problem_set")
    else:
        problem_rows = []
    problem_validation_available = (
        bundle_path is not None
        or problem_bank_path is not None
        or mysql_dump_path is not None
        or sqlite_path is not None
    )

    if bundle_path is not None:
        testcase_rows = bundle_testcase_rows
    elif testcases_path is not None:
        testcase_rows = _rows(testcases_path)
    elif mysql_dump_path is not None:
        testcase_rows = mysql_tables.get("problem_testcase", [])
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
        "bundle_path": str(bundle_path) if bundle_path else None,
        "mysql_dump_path": str(mysql_dump_path) if mysql_dump_path else None,
        "sqlite_path": str(sqlite_path) if sqlite_path else None,
        "problem_bank_path": str(problem_bank_path) if problem_bank_path else None,
        "testcases_path": str(testcases_path) if testcases_path else None,
        "submits_path": str(submits_path) if submits_path else None,
        "dry_run": dry_run,
        "ignored_pta_submit_rows": _bundle_ignored_table_rows(
            bundle_manifest, "pta_submit_record"
        )
        if bundle_manifest is not None
        else len(mysql_tables.get("pta_submit_record", [])),
        "bundle_schema_version": bundle_manifest.get("schema_version")
        if bundle_manifest is not None
        else None,
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
        "processed_testcase_rows": testcase_count
        if not dry_run
        else len(valid_testcase_rows),
        "processed_submit_rows": submit_count if not dry_run else len(submit_rows),
        "generated_at": datetime.now(UTC).isoformat(),
    }
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--bundle",
        type=Path,
        help="AscendAny legacy Web OJ 题库备份包目录，读取 manifest/problems/testcases。",
    )
    parser.add_argument(
        "--mysql-dump",
        type=Path,
        help="旧 x MySQL dump；只读取旧 OJ 的 problem_set/problem_testcase，并统计但不导入 pta_submit_record。",
    )
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
        args.bundle is None
        and
        args.sqlite_db is None
        and args.mysql_dump is None
        and args.problem_bank is None
        and args.testcases is None
        and args.submits is None
    ):
        parser.error(
            "at least one of --mysql-dump, --sqlite-db, --problem-bank, --testcases or --submits is required"
        )
    if args.bundle is not None and any(
        item is not None
        for item in (args.sqlite_db, args.mysql_dump, args.problem_bank, args.testcases)
    ):
        parser.error(
            "--bundle cannot be combined with --mysql-dump, --sqlite-db, "
            "--problem-bank or --testcases"
        )
    report = import_legacy_web_oj(
        bundle_path=args.bundle,
        problem_bank_path=args.problem_bank,
        testcases_path=args.testcases,
        submits_path=args.submits,
        sqlite_path=args.sqlite_db,
        mysql_dump_path=args.mysql_dump,
        report_path=args.report,
        dry_run=args.dry_run,
    )
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


def _bundle_ignored_table_rows(manifest: dict[str, Any] | None, table_name: str) -> int:
    if not isinstance(manifest, dict):
        return 0
    rows = manifest.get("ignored_table_rows")
    if not isinstance(rows, dict):
        return 0
    try:
        return int(rows.get(table_name) or 0)
    except (TypeError, ValueError):
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
