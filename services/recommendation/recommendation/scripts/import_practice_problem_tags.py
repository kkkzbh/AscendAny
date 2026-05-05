from __future__ import annotations

import argparse
import csv
import hashlib
import json
import re
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import psycopg

from ..config import load_db_config

_SPLIT_RE = re.compile(r"[,，;；|/]+")
_SPACE_RE = re.compile(r"\s+")


@dataclass(frozen=True)
class PracticeProblemTagsRow:
    practice_problem_id: str
    tags: list[str]
    source_hash: str
    meta: dict[str, Any]


def _clean(value: Any) -> str:
    if value is None:
        return ""
    return str(value).strip().rstrip("\t").strip()


def _normalize_tag(value: Any) -> str | None:
    tag = _SPACE_RE.sub(" ", _clean(value))
    if not tag or len(tag) > 64:
        return None
    if any(ord(ch) < 32 for ch in tag):
        return None
    return tag


def _parse_tags(value: Any) -> list[str]:
    if isinstance(value, list):
        raw_items = value
    else:
        text = _clean(value)
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
    seen: set[str] = set()
    for item in raw_items:
        tag = _normalize_tag(item)
        if tag is None or tag in seen:
            continue
        tags.append(tag)
        seen.add(tag)
    return tags


def _source_hash(problem_id: str, tags: list[str]) -> str:
    payload = json.dumps(
        {"practice_problem_id": problem_id, "tags": tags},
        ensure_ascii=False,
        sort_keys=True,
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def _read_mapping(path: Path) -> list[PracticeProblemTagsRow]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        raise ValueError("practice problem mapping JSON must be an object")
    rows: list[PracticeProblemTagsRow] = []
    imported_at = datetime.now(UTC).isoformat()
    for raw_problem_id, raw_tags in sorted(raw.items(), key=lambda item: str(item[0])):
        problem_id = _clean(raw_problem_id)
        if not problem_id:
            continue
        tags = _parse_tags(raw_tags)
        rows.append(
            PracticeProblemTagsRow(
                practice_problem_id=problem_id,
                tags=tags,
                source_hash=_source_hash(problem_id, tags),
                meta={
                    "legacy_imported_at": imported_at,
                    "legacy_source": str(path),
                },
            )
        )
    return rows


def _existing_practice_problem_ids(conn: psycopg.Connection) -> set[str]:
    with conn.cursor() as cur:
        cur.execute(
            """
            SELECT DISTINCT
                COALESCE(
                    NULLIF(s.raw->>'global_problem_id', ''),
                    NULLIF(s.raw->>'problem_id', ''),
                    NULLIF(ep.meta->>'global_problem_id', ''),
                    NULLIF(ep.meta->>'problem_id', ''),
                    CASE
                        WHEN COALESCE(s.problem_code, '') <> ''
                         AND e.source_path LIKE e.exam_type || '/%'
                        THEN replace(e.source_path, '/', '_') || '_' || s.problem_code
                        WHEN COALESCE(s.problem_code, '') <> ''
                        THEN e.exam_type || '_' || replace(e.source_path, '/', '_') || '_' || s.problem_code
                        ELSE NULL
                    END,
                    NULLIF(s.problem_code, '')
                ) AS practice_problem_id
            FROM ascendany.submissions s
            JOIN ascendany.exams e ON e.exam_id = s.exam_id
            LEFT JOIN ascendany.exam_problems ep
              ON ep.exam_id = s.exam_id
             AND ep.problem_code = s.problem_code
            WHERE COALESCE(s.problem_code, '') <> ''
            """
        )
        return {str(row[0]) for row in cur.fetchall() if row[0]}


def _upsert(conn: psycopg.Connection, rows: list[PracticeProblemTagsRow]) -> int:
    count = 0
    with conn.cursor() as cur:
        for row in rows:
            cur.execute(
                """
                DELETE FROM ascendany.recommendation_practice_problem_tags
                WHERE practice_problem_id = %s
                """,
                (row.practice_problem_id,),
            )
            for tag in row.tags:
                cur.execute(
                    """
                    INSERT INTO ascendany.recommendation_practice_problem_tags (
                        practice_problem_id, knowledge_point, source_hash, meta, updated_at
                    )
                    VALUES (%s, %s, %s, %s::jsonb, now())
                    ON CONFLICT (practice_problem_id, knowledge_point)
                    DO UPDATE SET source_hash = EXCLUDED.source_hash,
                                  meta = EXCLUDED.meta,
                                  updated_at = now()
                    """,
                    (
                        row.practice_problem_id,
                        tag,
                        row.source_hash,
                        json.dumps(row.meta, ensure_ascii=False),
                    ),
                )
                count += 1
    return count


def _write_report(
    path: Path,
    *,
    rows: list[PracticeProblemTagsRow],
    matched: list[str],
    unmatched: list[str],
    tag_count: int,
) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(
            {
                "problem_rows": len(rows),
                "tag_rows": tag_count,
                "matched_problem_rows": len(matched),
                "unmatched_problem_rows": len(unmatched),
                "unmatched_problem_ids": unmatched[:200],
            },
            ensure_ascii=False,
            indent=2,
        ),
        encoding="utf-8",
    )


def _write_unmatched_csv(path: Path, unmatched: list[str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=["practice_problem_id"])
        writer.writeheader()
        for item in unmatched:
            writer.writerow({"practice_problem_id": item})


def import_practice_problem_tags(
    input_path: Path,
    *,
    report: Path | None = None,
    unmatched_report: Path | None = None,
    dry_run: bool = False,
) -> dict[str, Any]:
    rows = _read_mapping(input_path)
    with psycopg.connect(load_db_config().dsn, prepare_threshold=None) as conn:
        existing_ids = _existing_practice_problem_ids(conn)
        matched = [row.practice_problem_id for row in rows if row.practice_problem_id in existing_ids]
        unmatched = [
            row.practice_problem_id
            for row in rows
            if row.practice_problem_id not in existing_ids
        ]
        tag_count = sum(len(row.tags) for row in rows)
        if not dry_run:
            _upsert(conn, rows)
    payload = {
        "problem_rows": len(rows),
        "tag_rows": tag_count,
        "matched_problem_rows": len(matched),
        "unmatched_problem_rows": len(unmatched),
    }
    if report:
        _write_report(
            report,
            rows=rows,
            matched=matched,
            unmatched=unmatched,
            tag_count=tag_count,
        )
    if unmatched_report:
        _write_unmatched_csv(unmatched_report, unmatched)
    return payload


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--report", type=Path)
    parser.add_argument("--unmatched-report", type=Path)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    result = import_practice_problem_tags(
        args.input,
        report=args.report,
        unmatched_report=args.unmatched_report,
        dry_run=args.dry_run,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
