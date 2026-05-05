from __future__ import annotations

import argparse
import csv
import hashlib
import json
import re
import sqlite3
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Iterable

import psycopg

from ..config import load_db_config

_SPLIT_RE = re.compile(r"[,，;；|/]+")
_SPACE_RE = re.compile(r"\s+")


@dataclass(frozen=True)
class ProblemBankRow:
    problem_id: str
    title: str | None
    description: str
    solution_1: str | None
    solution_2: str | None
    link: str | None
    submission_count: float
    pass_count: float
    active: bool
    tags: list[str]
    source_hash: str
    meta: dict[str, Any]


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    return str(value).strip().rstrip("\t").strip()


def _optional_text(value: Any) -> str | None:
    text = _clean_text(value)
    return text or None


def _float_value(value: Any) -> float:
    text = _clean_text(value)
    if not text:
        return 0.0
    try:
        return float(text)
    except ValueError:
        return 0.0


def _bool_value(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    text = _clean_text(value).lower()
    if text in {"", "1", "true", "t", "yes", "y", "启用", "正常"}:
        return True
    if text in {"0", "false", "f", "no", "n", "停用", "禁用"}:
        return False
    return True


def _normalize_tag(value: Any) -> tuple[str | None, str | None]:
    tag = _SPACE_RE.sub(" ", _clean_text(value))
    if not tag:
        return None, "empty"
    if len(tag) > 64:
        return None, "too_long"
    if any(ord(ch) < 32 for ch in tag):
        return None, "control_character"
    return tag, None


def _parse_tags(value: Any) -> tuple[list[str], list[dict[str, str]]]:
    raw_items: Iterable[Any]
    if isinstance(value, list):
        raw_items = value
    else:
        text = _clean_text(value)
        if not text:
            raw_items = []
        elif text.startswith("["):
            try:
                parsed = json.loads(text)
            except json.JSONDecodeError:
                raw_items = _SPLIT_RE.split(text)
            else:
                raw_items = parsed if isinstance(parsed, list) else [parsed]
        else:
            raw_items = _SPLIT_RE.split(text)

    tags: list[str] = []
    invalid: list[dict[str, str]] = []
    seen: set[str] = set()
    for item in raw_items:
        tag, reason = _normalize_tag(item)
        if tag is None:
            invalid.append({"value": _clean_text(item), "reason": reason or "invalid"})
            continue
        if tag in seen:
            continue
        seen.add(tag)
        tags.append(tag)
    return tags, invalid


def _source_hash(row: dict[str, Any]) -> str:
    payload = json.dumps(row, ensure_ascii=False, sort_keys=True, default=str)
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def _read_rows(path: Path) -> list[dict[str, Any]]:
    suffix = path.suffix.lower()
    if suffix in {".db", ".sqlite", ".sqlite3"}:
        return _read_sqlite_problem_set(path)
    if suffix == ".json":
        raw = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(raw, list):
            raise ValueError("JSON input must be a list of problem objects.")
        return [item for item in raw if isinstance(item, dict)]
    if suffix == ".jsonl":
        rows: list[dict[str, Any]] = []
        for line in path.read_text(encoding="utf-8").splitlines():
            text = line.strip()
            if not text:
                continue
            item = json.loads(text)
            if isinstance(item, dict):
                rows.append(item)
        return rows
    if suffix == ".csv":
        with path.open("r", encoding="utf-8-sig", newline="") as handle:
            return [dict(row) for row in csv.DictReader(handle)]
    raise ValueError("Input must be .json, .jsonl, .csv, .db, .sqlite, or .sqlite3.")


def _read_sqlite_problem_set(path: Path, table_name: str = "problem_set") -> list[dict[str, Any]]:
    if not path.exists():
        raise FileNotFoundError(path)
    with sqlite3.connect(path) as conn:
        conn.row_factory = sqlite3.Row
        table_exists = conn.execute(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?",
            (table_name,),
        ).fetchone()
        if table_exists is None:
            raise ValueError(f"SQLite database {path} does not contain table {table_name!r}")
        rows = conn.execute(
            f"""
            SELECT
                problem_id,
                description,
                category_tags,
                solution_1,
                solution_2,
                link,
                active,
                submission_count,
                pass_count,
                created_at
            FROM {table_name}
            ORDER BY problem_id
            """
        ).fetchall()
    return [dict(row) for row in rows]


def _to_problem(row: dict[str, Any]) -> tuple[ProblemBankRow | None, list[dict[str, str]]]:
    problem_id = _clean_text(row.get("problem_id"))
    if not problem_id:
        return None, [{"problem_id": "", "value": "", "reason": "missing_problem_id"}]

    tags, invalid_tags = _parse_tags(row.get("category_tags"))
    source_hash = _source_hash(row)
    meta = {
        "legacy_imported_at": datetime.now(UTC).isoformat(),
        "legacy_source": "problem_set_export",
    }
    problem = ProblemBankRow(
        problem_id=problem_id,
        title=_optional_text(row.get("title")),
        description=_clean_text(row.get("description")),
        solution_1=_optional_text(row.get("solution_1")),
        solution_2=_optional_text(row.get("solution_2")),
        link=_optional_text(row.get("link")),
        submission_count=_float_value(row.get("submission_count")),
        pass_count=_float_value(row.get("pass_count")),
        active=_bool_value(row.get("active")),
        tags=tags,
        source_hash=source_hash,
        meta=meta,
    )
    return problem, [
        {"problem_id": problem_id, "value": item["value"], "reason": item["reason"]}
        for item in invalid_tags
    ]


def _write_invalid_tag_csv(path: Path, invalid_tags: list[dict[str, str]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=["problem_id", "value", "reason"])
        writer.writeheader()
        writer.writerows(invalid_tags)


def _sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _write_sql_report(path: Path, problem_ids: list[str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    values = ", ".join(f"({_sql_literal(item)})" for item in problem_ids)
    if not values:
        values = "(NULL)"
    path.write_text(
        "\n".join(
            [
                "-- AscendAny recommendation problem-bank import verification",
                "WITH imported(problem_id) AS (",
                f"    VALUES {values}",
                ")",
                "SELECT 'imported_problem_count' AS check_name, count(*) AS value",
                "FROM ascendany.recommendation_problem_bank b",
                "JOIN imported i USING (problem_id);",
                "",
                "WITH imported(problem_id) AS (",
                f"    VALUES {values}",
                ")",
                "SELECT 'tag_count' AS check_name, count(*) AS value",
                "FROM ascendany.recommendation_problem_tags t",
                "JOIN imported i USING (problem_id);",
                "",
                "WITH imported(problem_id) AS (",
                f"    VALUES {values}",
                ")",
                "SELECT b.problem_id",
                "FROM ascendany.recommendation_problem_bank b",
                "JOIN imported i USING (problem_id)",
                "WHERE b.active = TRUE",
                "  AND NOT EXISTS (",
                "      SELECT 1",
                "      FROM ascendany.recommendation_problem_tags t",
                "      WHERE t.problem_id = b.problem_id",
                "  )",
                "ORDER BY b.problem_id;",
                "",
            ]
        ),
        encoding="utf-8",
    )


def _upsert(conn: psycopg.Connection, problems: list[ProblemBankRow]) -> None:
    with conn.cursor() as cur:
        for problem in problems:
            cur.execute(
                """
                INSERT INTO ascendany.recommendation_problem_bank (
                    problem_id,
                    title,
                    description,
                    solution_1,
                    solution_2,
                    link,
                    submission_count,
                    pass_count,
                    active,
                    source_hash,
                    meta,
                    imported_at,
                    updated_at
                )
                VALUES (
                    %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb, now(), now()
                )
                ON CONFLICT (problem_id)
                DO UPDATE SET
                    title = EXCLUDED.title,
                    description = EXCLUDED.description,
                    solution_1 = EXCLUDED.solution_1,
                    solution_2 = EXCLUDED.solution_2,
                    link = EXCLUDED.link,
                    submission_count = EXCLUDED.submission_count,
                    pass_count = EXCLUDED.pass_count,
                    active = EXCLUDED.active,
                    source_hash = EXCLUDED.source_hash,
                    meta = EXCLUDED.meta,
                    updated_at = now()
                """,
                (
                    problem.problem_id,
                    problem.title,
                    problem.description,
                    problem.solution_1,
                    problem.solution_2,
                    problem.link,
                    problem.submission_count,
                    problem.pass_count,
                    problem.active,
                    problem.source_hash,
                    json.dumps(problem.meta, ensure_ascii=False),
                ),
            )
            cur.execute(
                """
                DELETE FROM ascendany.recommendation_problem_tags
                WHERE problem_id = %s
                """,
                (problem.problem_id,),
            )
            for tag in problem.tags:
                cur.execute(
                    """
                    INSERT INTO ascendany.recommendation_problem_tags (
                        problem_id, knowledge_point, source, confidence
                    )
                    VALUES (%s, %s, 'legacy_problem_bank', 1.0)
                    ON CONFLICT (problem_id, knowledge_point)
                    DO UPDATE SET source = EXCLUDED.source,
                                  confidence = EXCLUDED.confidence
                    """,
                    (problem.problem_id, tag),
                )


def import_problem_bank(
    input_path: Path,
    *,
    report_path: Path,
    invalid_tag_report_path: Path | None,
    sql_report_path: Path | None,
    dry_run: bool,
) -> dict[str, Any]:
    raw_rows = _read_rows(input_path)
    problems: list[ProblemBankRow] = []
    invalid_tags: list[dict[str, str]] = []
    duplicate_problem_ids: list[str] = []
    seen_ids: set[str] = set()

    for row in raw_rows:
        problem, row_invalid_tags = _to_problem(row)
        invalid_tags.extend(row_invalid_tags)
        if problem is None:
            continue
        if problem.problem_id in seen_ids:
            duplicate_problem_ids.append(problem.problem_id)
        seen_ids.add(problem.problem_id)
        problems.append(problem)

    if not dry_run:
        with psycopg.connect(load_db_config().dsn, prepare_threshold=None) as conn:
            _upsert(conn, problems)

    if invalid_tag_report_path is not None:
        _write_invalid_tag_csv(invalid_tag_report_path, invalid_tags)
    if sql_report_path is not None:
        _write_sql_report(
            sql_report_path,
            sorted({item.problem_id for item in problems}),
        )

    report = {
        "input_path": str(input_path),
        "dry_run": dry_run,
        "input_rows": len(raw_rows),
        "valid_problem_rows": len(problems),
        "active_problem_rows": sum(1 for item in problems if item.active),
        "unique_problem_ids": len({item.problem_id for item in problems}),
        "duplicate_problem_ids": sorted(set(duplicate_problem_ids)),
        "tag_rows": sum(len(item.tags) for item in problems),
        "problems_without_tags": sum(1 for item in problems if not item.tags),
        "invalid_tags": invalid_tags,
        "link_present_rows": sum(1 for item in problems if item.link),
        "generated_at": datetime.now(UTC).isoformat(),
    }
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(
        json.dumps(report, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )
    return report


def _default_report_path() -> Path:
    stamp = datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ")
    return Path("var/recommendation/import_reports") / f"problem_bank_import_{stamp}.json"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--report", type=Path, default=_default_report_path())
    parser.add_argument("--invalid-tag-report", type=Path)
    parser.add_argument("--sql-report", type=Path)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)

    report = import_problem_bank(
        args.input,
        report_path=args.report,
        invalid_tag_report_path=args.invalid_tag_report,
        sql_report_path=args.sql_report,
        dry_run=args.dry_run,
    )
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
