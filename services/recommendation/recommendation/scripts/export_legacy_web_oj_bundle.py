from __future__ import annotations

import argparse
import hashlib
import json
import shutil
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from urllib.parse import quote

from ..knowledge import KNOWLEDGE_TREE_PATH
from .import_legacy_problem_bank import _to_problem
from .import_legacy_web_oj import (
    BUNDLE_SCHEMA_VERSION,
    _bool,
    _clean,
    _float,
    _int,
    _problem_id,
    _read_mysql_dump_tables,
    _testcase_hash,
)


def export_legacy_web_oj_bundle(
    *,
    mysql_dump: Path,
    output: Path,
    force: bool = False,
) -> dict[str, Any]:
    if output.exists():
        if not force and any(output.iterdir()):
            raise FileExistsError(f"output directory is not empty: {output}")
        if force:
            shutil.rmtree(output)
    problems_dir = output / "problems"
    testcases_dir = output / "testcases"
    problems_dir.mkdir(parents=True, exist_ok=True)
    testcases_dir.mkdir(parents=True, exist_ok=True)

    mysql_tables = _read_mysql_dump_tables(
        mysql_dump,
        {"problem_set", "problem_testcase", "pta_submit_record"},
    )
    problem_rows = mysql_tables.get("problem_set", [])
    testcase_rows = mysql_tables.get("problem_testcase", [])
    ignored_pta_rows = len(mysql_tables.get("pta_submit_record", []))

    problem_file_count = 0
    testcase_file_count = 0
    testcase_row_count = 0
    invalid_tags: list[dict[str, str]] = []
    problem_files: list[str] = []
    testcase_files: list[str] = []

    for row in sorted(problem_rows, key=lambda item: _clean(item.get("problem_id"))):
        problem, row_invalid_tags = _to_problem(row)
        invalid_tags.extend(row_invalid_tags)
        if problem is None:
            continue
        problem_file = problems_dir / f"{_safe_filename(problem.problem_id)}.json"
        raw_category_tags = row.get("category_tags")
        payload = {
            "problem_id": problem.problem_id,
            "title": problem.title,
            "description": problem.description,
            "solution_1": problem.solution_1,
            "solution_2": problem.solution_2,
            "link": problem.link,
            "active": problem.active,
            "submission_count": problem.submission_count,
            "pass_count": problem.pass_count,
            "created_at": _clean(row.get("created_at")) or None,
            "raw_category_tags": _clean(raw_category_tags)
            if raw_category_tags is not None
            else None,
            "canonical_tags": problem.tags,
            "rejected_tags": [
                {"value": item["value"], "reason": item["reason"]}
                for item in row_invalid_tags
            ],
            "source_hash": problem.source_hash,
            "source_meta": {
                "source_name": "legacy_x_web_oj",
                "source_table": "problem_set",
                "legacy_id": row.get("id"),
                "source_dump_path": str(mysql_dump),
            },
        }
        _write_json(problem_file, payload)
        problem_files.append(str(problem_file.relative_to(output)))
        problem_file_count += 1

    rows_by_problem: dict[str, list[dict[str, Any]]] = {}
    for row in testcase_rows:
        problem_id = _problem_id(row)
        if not problem_id:
            continue
        rows_by_problem.setdefault(problem_id, []).append(row)

    for problem_id, rows in sorted(rows_by_problem.items()):
        testcase_file = testcases_dir / f"{_safe_filename(problem_id)}.jsonl"
        with testcase_file.open("w", encoding="utf-8", newline="\n") as handle:
            for row in sorted(rows, key=lambda item: _int(item.get("id"), 0)):
                payload = {
                    "legacy_id": row.get("legacy_id") or row.get("id"),
                    "problem_id": problem_id,
                    "input_data": _clean(row.get("input_data")),
                    "output_data": _clean(row.get("output_data")),
                    "is_sample": _bool(row.get("is_sample")),
                    "weight": _float(row.get("weight"), 1.0),
                    "time_limit_ms": _int(row.get("time_limit_ms"), 1000),
                    "memory_limit_kb": _int(row.get("memory_limit_kb"), 262144),
                    "active": _bool(row.get("active")),
                    "created_at": _clean(row.get("created_at")) or None,
                    "source_hash": _testcase_hash(row),
                }
                handle.write(json.dumps(payload, ensure_ascii=False, sort_keys=True))
                handle.write("\n")
                testcase_row_count += 1
        testcase_files.append(str(testcase_file.relative_to(output)))
        testcase_file_count += 1

    manifest = {
        "schema_version": BUNDLE_SCHEMA_VERSION,
        "source_name": "legacy_x_web_oj",
        "source_dump_path": str(mysql_dump),
        "extracted_at": datetime.now(UTC).isoformat(),
        "problem_count": problem_file_count,
        "testcase_count": testcase_row_count,
        "ignored_tables": ["pta_submit_record"],
        "ignored_table_rows": {"pta_submit_record": ignored_pta_rows},
        "canonical_knowledge_tree_hash": _knowledge_tree_hash(),
        "problem_files": problem_files,
        "testcase_files": testcase_files,
    }
    _write_json(output / "manifest.json", manifest)
    _write_checksums(output)

    return {
        "output": str(output),
        "schema_version": BUNDLE_SCHEMA_VERSION,
        "input_problem_rows": len(problem_rows),
        "problem_count": problem_file_count,
        "input_testcase_rows": len(testcase_rows),
        "testcase_count": testcase_row_count,
        "testcase_file_count": testcase_file_count,
        "ignored_pta_submit_rows": ignored_pta_rows,
        "invalid_tags": invalid_tags,
        "generated_at": datetime.now(UTC).isoformat(),
    }


def _safe_filename(value: str) -> str:
    encoded = quote(value, safe="")
    return encoded or "_"


def _write_json(path: Path, payload: dict[str, Any]) -> None:
    path.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def _write_checksums(output: Path) -> None:
    paths = [
        output / "manifest.json",
        *sorted((output / "problems").glob("*.json")),
        *sorted((output / "testcases").glob("*.jsonl")),
    ]
    lines = []
    for path in paths:
        relative = str(path.relative_to(output))
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        lines.append(f"{digest}  {relative}")
    (output / "checksums.sha256").write_text(
        "\n".join(lines) + "\n",
        encoding="utf-8",
    )


def _knowledge_tree_hash() -> str:
    return hashlib.sha256(KNOWLEDGE_TREE_PATH.read_bytes()).hexdigest()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mysql-dump", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--force", action="store_true")
    args = parser.parse_args(argv)

    report = export_legacy_web_oj_bundle(
        mysql_dump=args.mysql_dump,
        output=args.output,
        force=args.force,
    )
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
