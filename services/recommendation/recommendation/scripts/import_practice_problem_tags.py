from __future__ import annotations

import argparse
import csv
import hashlib
import json
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import psycopg

from ..config import load_db_config
from ..knowledge import RejectedKnowledgeTag, canonicalize_knowledge_tags


@dataclass(frozen=True)
class PracticeProblemTagsRow:
    practice_problem_id: str
    tags: list[str]
    rejected_tags: list[RejectedKnowledgeTag]
    source_hash: str
    meta: dict[str, Any]


def _clean(value: Any) -> str:
    if value is None:
        return ""
    return str(value).strip().rstrip("\t").strip()


def _parse_tags(value: Any) -> list[str]:
    return canonicalize_knowledge_tags(value).tags


def _parse_tags_with_rejections(value: Any) -> tuple[list[str], list[RejectedKnowledgeTag]]:
    normalized = canonicalize_knowledge_tags(value)
    return normalized.tags, normalized.rejected


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
        tags, rejected_tags = _parse_tags_with_rejections(raw_tags)
        rows.append(
            PracticeProblemTagsRow(
                practice_problem_id=problem_id,
                tags=tags,
                rejected_tags=rejected_tags,
                source_hash=_source_hash(problem_id, tags),
                meta={
                    "legacy_imported_at": imported_at,
                    "legacy_source": str(path),
                    "rejected_tags": [
                        {"value": item.value, "reason": item.reason}
                        for item in rejected_tags
                    ],
                },
            )
        )
    return rows


def _existing_practice_problem_ids(conn: psycopg.Connection) -> set[str]:
    with conn.cursor() as cur:
        cur.execute(
            """
            SELECT DISTINCT
                epl.source_platform || ':' || epl.external_problem_id AS practice_problem_id
            FROM ascendany.submissions s
            JOIN ascendany.exam_problem_external_links epl
              ON epl.exam_id = s.exam_id
             AND epl.problem_set_problem_id = s.problem_code
            WHERE s.student_id IS NOT NULL
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
                "rejected_tags": [
                    {
                        "practice_problem_id": row.practice_problem_id,
                        "value": item.value,
                        "reason": item.reason,
                    }
                    for row in rows
                    for item in row.rejected_tags
                ],
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
        "rejected_tag_rows": sum(len(row.rejected_tags) for row in rows),
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
