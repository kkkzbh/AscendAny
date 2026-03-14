from __future__ import annotations

from dataclasses import replace

from ..models import ExamBundle, ExamMeta, ExamUnit
from ..utils import clean_text
from .csv_reader import parse_submission_csv
from .html_reader import parse_exam_html
from .xlsx_reader import parse_scoreboards


def _problem_code_tail(problem_code: str) -> str | None:
    parts = [item for item in clean_text(problem_code).split("-") if item]
    if len(parts) < 2:
        return None
    return "-".join(parts[1:])


def _build_problem_code_aliases(
    exam_type: str,
    allowed_problem_codes: set[str] | None,
    problem_metadata_by_code: dict[str, dict[str, object]],
) -> dict[str, str]:
    if exam_type != "datastructure" or not allowed_problem_codes or not problem_metadata_by_code:
        return {}

    html_codes = {
        clean_text(code)
        for code in problem_metadata_by_code
        if clean_text(code)
    }
    if not html_codes or html_codes & allowed_problem_codes:
        return {}

    xlsx_by_tail: dict[str, str] = {}
    for code in allowed_problem_codes:
        tail = _problem_code_tail(code)
        if tail is None or tail in xlsx_by_tail:
            return {}
        xlsx_by_tail[tail] = code

    aliases: dict[str, str] = {}
    for code in sorted(html_codes):
        tail = _problem_code_tail(code)
        if tail is None:
            return {}
        target_code = xlsx_by_tail.get(tail)
        if target_code is None:
            return {}
        aliases[code] = target_code

    if set(aliases.values()) != allowed_problem_codes:
        return {}
    return aliases


def parse_exam_bundle(
    unit: ExamUnit,
    encodings: list[str],
    timezone_name: str,
    included_problem_kinds: set[str] | None = None,
) -> ExamBundle:
    html_files = [item for item in unit.files if item.file_role == "answer_html"]
    csv_files = [item for item in unit.files if item.file_role == "submission_csv"]
    xlsx_files = [item for item in unit.files if item.file_role == "scoreboard_xlsx"]

    exam_meta: ExamMeta = parse_exam_html(
        files=html_files,
        timezone_name=timezone_name,
    )
    problem_catalog = exam_meta.meta.get("problem_catalog")
    problem_metadata_by_code: dict[str, dict[str, object]] = {}
    if isinstance(problem_catalog, dict):
        by_code = problem_catalog.get("by_code")
        if isinstance(by_code, dict):
            problem_metadata_by_code = {
                str(code): dict(meta) if isinstance(meta, dict) else {}
                for code, meta in by_code.items()
            }
    participants, problems, xlsx_meta = parse_scoreboards(
        files=xlsx_files,
        exam_type=unit.exam_type,
        problem_metadata_by_code=problem_metadata_by_code,
        included_problem_kinds=included_problem_kinds,
    )

    resolved_title = exam_meta.title
    if not resolved_title and xlsx_meta.get("title"):
        resolved_title = xlsx_meta["title"]

    total_points = exam_meta.total_points
    filtered_total_points = xlsx_meta.get("filtered_total_points")
    if filtered_total_points is not None:
        total_points = filtered_total_points

    merged_meta = dict(exam_meta.meta)
    merged_meta.update(
        {
            "metric_scope": "function_programming_only"
            if included_problem_kinds
            else "all_problem_kinds",
            "included_problem_kinds": sorted(included_problem_kinds)
            if included_problem_kinds
            else None,
            "filtered_problem_count": xlsx_meta.get("filtered_problem_count"),
            "filtered_problem_kinds": xlsx_meta.get("filtered_problem_kinds"),
            "filtered_problem_cells": xlsx_meta.get("filtered_problem_cells"),
        }
    )
    problem_kind_by_code = xlsx_meta.get("problem_kind_by_code")
    if isinstance(problem_kind_by_code, dict):
        merged_meta["problem_kind_by_code"] = problem_kind_by_code
    exam_meta = replace(
        exam_meta,
        title=resolved_title,
        total_points=total_points,
        meta=merged_meta,
    )

    submissions = []
    seen_hashes: set[str] = set()
    allowed_problem_codes = {item.problem_code for item in problems if item.problem_code}
    if not allowed_problem_codes:
        allowed_problem_codes = None
    problem_code_aliases = _build_problem_code_aliases(
        exam_type=unit.exam_type,
        allowed_problem_codes=allowed_problem_codes,
        problem_metadata_by_code=problem_metadata_by_code,
    )
    if problem_code_aliases:
        exam_meta.meta["submission_problem_code_aliases"] = problem_code_aliases
    for source in csv_files:
        for row in parse_submission_csv(
            file=source,
            exam_type=unit.exam_type,
            encodings=encodings,
            timezone_name=timezone_name,
            allowed_problem_codes=allowed_problem_codes,
            problem_code_aliases=problem_code_aliases,
        ):
            if row.row_hash in seen_hashes:
                continue
            seen_hashes.add(row.row_hash)
            submissions.append(row)

    return ExamBundle(
        unit=unit,
        exam_meta=exam_meta,
        problems=problems,
        participants=participants,
        submissions=submissions,
    )
