from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any

import pandas as pd

from ..models import ParticipantRow, ProblemInfo, SourceFile
from ..utils import clean_text, parse_optional_float, parse_optional_int

_PROBLEM_CODE_RE = re.compile(r"^(?:[A-Za-z]\d-\d+(?:-\d+)?|L\d-\d+|\d+-\d+(?:-\d+)?)$")


@dataclass(slots=True)
class _ParsedSheetRow:
    participant: ParticipantRow | None
    problem_points: dict[str, float]
    title: str | None


def _find_header_row(df: pd.DataFrame) -> int | None:
    hints = ("学号", "姓名", "排名", "总分", "通过数", "用时", "耗时")
    best_index: int | None = None
    best_score = 0
    limit = min(30, len(df))
    for idx in range(limit):
        row_values = [clean_text(value) for value in df.iloc[idx].tolist()]
        score = 0
        for value in row_values:
            if any(hint in value for hint in hints):
                score += 1
        if score > best_score:
            best_score = score
            best_index = idx
    if best_score < 2:
        return None
    return best_index


def _unique_column_names(values: list[str]) -> list[str]:
    seen: dict[str, int] = {}
    result: list[str] = []
    for index, value in enumerate(values):
        name = clean_text(value) or f"col_{index}"
        count = seen.get(name, 0)
        seen[name] = count + 1
        if count > 0:
            name = f"{name}__{count + 1}"
        result.append(name)
    return result


def _detect_problem_columns(
    df: pd.DataFrame,
    header_index: int,
    header_values: list[str],
) -> list[tuple[int, str]]:
    candidate_rows: list[list[str]] = []
    start = max(0, header_index - 3)
    for idx in range(start, header_index):
        row_values = [clean_text(value) for value in df.iloc[idx].tolist()]
        candidate_rows.append(row_values)

    columns: list[tuple[int, str]] = []
    seen_codes: set[str] = set()
    for col_idx, header_value in enumerate(header_values):
        problem_code = header_value if _PROBLEM_CODE_RE.match(header_value) else ""
        if not problem_code:
            for row_values in reversed(candidate_rows):
                if col_idx >= len(row_values):
                    continue
                candidate = clean_text(row_values[col_idx])
                if _PROBLEM_CODE_RE.match(candidate):
                    problem_code = candidate
                    break
        if not problem_code or problem_code in seen_codes:
            continue
        seen_codes.add(problem_code)
        columns.append((col_idx, problem_code))
    return columns


def _get_value(row: dict[str, Any], candidates: list[str]) -> str:
    for candidate in candidates:
        for key, value in row.items():
            if candidate in key:
                return clean_text(value)
    return ""


def _guess_datastructure_problem_kind(problem_code: str) -> str | None:
    code = clean_text(problem_code)
    if not code:
        return None
    first = code.split("-", 1)[0]
    if not first.isdigit():
        return None
    number = int(first)
    if number == 6:
        return "函数题"
    if number == 7:
        return "编程题"
    if number == 1:
        return "判断题"
    if number == 2:
        return "单选题"
    if number == 3:
        return "多选题"
    return None


def _resolve_problem_metadata(
    problem_code: str,
    exam_type: str,
    problem_metadata_by_code: dict[str, dict[str, Any]],
) -> dict[str, Any]:
    resolved = dict(problem_metadata_by_code.get(problem_code, {}))
    if not clean_text(resolved.get("problem_kind")) and exam_type == "datastructure":
        guessed = _guess_datastructure_problem_kind(problem_code)
        if guessed:
            resolved["problem_kind"] = guessed
    return resolved


def _include_problem(
    problem_code: str,
    exam_type: str,
    problem_metadata_by_code: dict[str, dict[str, Any]],
    included_problem_kinds: set[str] | None,
) -> tuple[bool, dict[str, Any]]:
    metadata = _resolve_problem_metadata(
        problem_code=problem_code,
        exam_type=exam_type,
        problem_metadata_by_code=problem_metadata_by_code,
    )
    if not included_problem_kinds:
        return True, metadata
    kind = clean_text(metadata.get("problem_kind"))
    if kind:
        return kind in included_problem_kinds, metadata
    # For non-datastructure or unknown-kind rows, keep data to avoid accidental drops.
    return True, metadata


def _parse_problem_cell(value: Any, exam_type: str) -> dict[str, Any]:
    text = clean_text(value)
    if not text:
        return {"raw": text}
    if text == "缺考":
        return {"raw": text, "absent": True}
    if text == "-":
        return {"raw": text, "solved": False, "attempts": 0, "wrong_before_ac": 0}

    if exam_type == "pta_icpc":
        if text.startswith("+"):
            numbers = [int(item) for item in re.findall(r"\d+", text)]
            wrong_before_ac = 0
            penalty = None
            if "\n" in text or "\r" in text:
                if len(numbers) == 1:
                    penalty = numbers[0]
                elif len(numbers) >= 2:
                    wrong_before_ac = numbers[0]
                    penalty = numbers[1]
            elif numbers:
                penalty = numbers[-1]
            return {
                "raw": text,
                "solved": True,
                "attempts": wrong_before_ac + 1,
                "wrong_before_ac": wrong_before_ac,
                "penalty": penalty,
            }
        if text.startswith("-"):
            wrong = abs(parse_optional_int(text) or 0)
            return {
                "raw": text,
                "solved": False,
                "attempts": wrong,
                "wrong_before_ac": wrong,
            }

    matched = re.match(r"^(-?\d+(?:\.\d+)?)\((\d+):(\d+)ms\)$", text)
    if matched:
        score = float(matched.group(1))
        attempts = int(matched.group(2))
        runtime_ms = int(matched.group(3))
        solved = score > 0
        return {
            "raw": text,
            "score": score,
            "attempts": attempts,
            "runtime_ms": runtime_ms,
            "solved": solved,
            "wrong_before_ac": max(0, attempts - 1 if solved else attempts),
        }

    numeric_score = parse_optional_float(text)
    if numeric_score is not None:
        solved = numeric_score > 0
        return {
            "raw": text,
            "score": numeric_score,
            "solved": solved,
            "attempts": 1 if solved else 0,
            "wrong_before_ac": 0 if solved else 1,
        }

    return {"raw": text}


def _completeness(participant: ParticipantRow) -> int:
    score = 0
    score += 1 if participant.rank is not None else 0
    score += 1 if participant.total_score is not None else 0
    score += 1 if participant.time_used_seconds is not None else 0
    score += 1 if participant.solved_count is not None else 0
    score += len(participant.problem_stats)
    return score


def _parse_sheet(
    df: pd.DataFrame,
    sheet_name: str,
    exam_type: str,
    identity_source: str,
    problem_metadata_by_code: dict[str, dict[str, Any]],
    included_problem_kinds: set[str] | None,
) -> list[_ParsedSheetRow]:
    header_index = _find_header_row(df)
    if header_index is None:
        return []

    header_values = [clean_text(value) for value in df.iloc[header_index].tolist()]
    columns = _unique_column_names(header_values)
    problem_columns = _detect_problem_columns(
        df=df,
        header_index=header_index,
        header_values=header_values,
    )

    results: list[_ParsedSheetRow] = []
    sheet_title = clean_text(df.iloc[0, 0]) if len(df) > 0 else ""
    if "成绩明细" not in sheet_title:
        sheet_title = ""

    for raw_index in range(header_index + 1, len(df)):
        row_series = df.iloc[raw_index].tolist()
        row_map: dict[str, Any] = {
            columns[idx]: row_series[idx]
            for idx in range(min(len(columns), len(row_series)))
        }
        cleaned_values = [clean_text(item) for item in row_map.values()]
        if not any(cleaned_values):
            continue

        external_id = _get_value(row_map, ["学号/邮箱、电话", "学号/账号", "学号"])
        display_name = _get_value(row_map, ["姓名/昵称", "姓名"])
        rank = parse_optional_int(_get_value(row_map, ["排名"]))
        total_score_text = _get_value(row_map, ["总分"])
        total_score = parse_optional_float(total_score_text)
        time_used = parse_optional_int(_get_value(row_map, ["耗时(秒)", "用时"]))
        solved_count = parse_optional_int(_get_value(row_map, ["通过数"]))
        user_group = _get_value(row_map, ["用户组"]) or None
        absent = "缺考" in "".join(cleaned_values)

        if not external_id and not display_name:
            points: dict[str, float] = {}
            for column_index, problem_code in problem_columns:
                include_problem, _ = _include_problem(
                    problem_code=problem_code,
                    exam_type=exam_type,
                    problem_metadata_by_code=problem_metadata_by_code,
                    included_problem_kinds=included_problem_kinds,
                )
                if not include_problem:
                    continue
                raw_value = (
                    row_series[column_index] if column_index < len(row_series) else None
                )
                maybe_points = parse_optional_float(raw_value)
                if maybe_points is not None:
                    points[problem_code] = maybe_points
            if points:
                results.append(
                    _ParsedSheetRow(
                        participant=None,
                        problem_points=points,
                        title=sheet_title or None,
                    )
                )
            continue

        identity = identity_source
        if not external_id and display_name:
            external_id = f"name::{display_name}"
            identity = f"{identity_source}_name_fallback"

        problem_stats: dict[str, dict[str, Any]] = {}
        problem_raw_cells: dict[str, str] = {}
        filtered_problem_cells = 0
        for column_index, problem_code in problem_columns:
            include_problem, metadata = _include_problem(
                problem_code=problem_code,
                exam_type=exam_type,
                problem_metadata_by_code=problem_metadata_by_code,
                included_problem_kinds=included_problem_kinds,
            )
            raw_value = (
                row_series[column_index] if column_index < len(row_series) else None
            )
            raw_text = clean_text(raw_value)
            if not include_problem:
                if raw_text:
                    filtered_problem_cells += 1
                continue
            cell_data = _parse_problem_cell(raw_value, exam_type=exam_type)
            if cell_data.get("raw"):
                problem_kind = clean_text(metadata.get("problem_kind"))
                if problem_kind:
                    cell_data["problem_kind"] = problem_kind
                problem_stats[problem_code] = cell_data
                problem_raw_cells[problem_code] = clean_text(cell_data.get("raw"))

        solved_from_stats = sum(
            1 for stats in problem_stats.values() if bool(stats.get("solved"))
        )
        score_values = [
            float(stats.get("score"))
            for stats in problem_stats.values()
            if stats.get("score") is not None
        ]
        if score_values:
            total_score = sum(score_values)
        if solved_count is None or included_problem_kinds:
            solved_count = solved_from_stats

        participant = ParticipantRow(
            identity_source=identity,
            external_id=external_id,
            display_name=display_name or None,
            user_group=user_group,
            rank=rank,
            total_score=total_score,
            time_used_seconds=time_used,
            solved_count=solved_count,
            absent=absent,
            problem_stats=problem_stats,
            raw={
                "sheet_name": sheet_name,
                "total_score_raw": total_score_text,
                "problem_raw_cells": problem_raw_cells,
                "filtering": {
                    "included_problem_kinds": sorted(included_problem_kinds)
                    if included_problem_kinds
                    else None,
                    "filtered_problem_cells": filtered_problem_cells,
                },
            },
        )
        results.append(
            _ParsedSheetRow(
                participant=participant, problem_points={}, title=sheet_title or None
            )
        )
    return results


def parse_scoreboards(
    files: list[SourceFile],
    exam_type: str,
    problem_metadata_by_code: dict[str, dict[str, Any]] | None = None,
    included_problem_kinds: set[str] | None = None,
) -> tuple[list[ParticipantRow], list[ProblemInfo], dict[str, Any]]:
    identity_source = f"{exam_type}_student_no"
    participants_by_key: dict[tuple[str, str], ParticipantRow] = {}
    problem_points: dict[str, float] = {}
    all_problem_codes: set[str] = set()
    workbook_title: str | None = None
    metadata_by_code = problem_metadata_by_code or {}
    filtered_cells_total = 0

    for source in sorted(files, key=lambda item: item.relative_path):
        workbook = pd.read_excel(source.absolute_path, sheet_name=None, dtype=object)
        for sheet_name, frame in workbook.items():
            parsed_rows = _parse_sheet(
                frame,
                sheet_name=sheet_name,
                exam_type=exam_type,
                identity_source=identity_source,
                problem_metadata_by_code=metadata_by_code,
                included_problem_kinds=included_problem_kinds,
            )
            for parsed in parsed_rows:
                if parsed.title and workbook_title is None:
                    workbook_title = parsed.title
                problem_points.update(parsed.problem_points)
                if parsed.participant is None:
                    continue
                filtering = parsed.participant.raw.get("filtering")
                if isinstance(filtering, dict):
                    filtered_cells_total += int(
                        filtering.get("filtered_problem_cells") or 0
                    )

                key = (
                    parsed.participant.identity_source,
                    parsed.participant.external_id,
                )
                for problem_code in parsed.participant.problem_stats:
                    all_problem_codes.add(problem_code)

                existing = participants_by_key.get(key)
                if existing is None or _completeness(
                    parsed.participant
                ) > _completeness(existing):
                    participants_by_key[key] = parsed.participant

    for code in problem_points:
        all_problem_codes.add(code)

    filtered_codes: list[str] = []
    for code in sorted(all_problem_codes):
        include_problem, _ = _include_problem(
            problem_code=code,
            exam_type=exam_type,
            problem_metadata_by_code=metadata_by_code,
            included_problem_kinds=included_problem_kinds,
        )
        if include_problem:
            filtered_codes.append(code)

    problems = [
        ProblemInfo(
            problem_code=code,
            problem_kind=clean_text(
                _resolve_problem_metadata(
                    problem_code=code,
                    exam_type=exam_type,
                    problem_metadata_by_code=metadata_by_code,
                ).get("problem_kind")
            )
            or None,
            group_code=clean_text(
                _resolve_problem_metadata(
                    problem_code=code,
                    exam_type=exam_type,
                    problem_metadata_by_code=metadata_by_code,
                ).get("pool_code")
            )
            or None,
            group_name=clean_text(
                _resolve_problem_metadata(
                    problem_code=code,
                    exam_type=exam_type,
                    problem_metadata_by_code=metadata_by_code,
                ).get("pool_name")
            )
            or None,
            points=problem_points.get(code),
            order_idx=index + 1,
            meta={
                key: value
                for key, value in _resolve_problem_metadata(
                    problem_code=code,
                    exam_type=exam_type,
                    problem_metadata_by_code=metadata_by_code,
                ).items()
                if key not in {"problem_kind", "pool_code", "pool_name"}
            },
        )
        for index, code in enumerate(filtered_codes)
    ]
    participants = sorted(
        participants_by_key.values(),
        key=lambda item: (
            item.rank is None,
            item.rank if item.rank is not None else 10**9,
            item.external_id,
        ),
    )

    filtered_total_points: float | None = None
    points_values = [float(item.points) for item in problems if item.points is not None]
    if points_values:
        filtered_total_points = float(sum(points_values))

    kinds = sorted(
        {
            clean_text(item.problem_kind)
            for item in problems
            if clean_text(item.problem_kind)
        }
    )

    problem_kind_by_code = {
        item.problem_code: item.problem_kind for item in problems if item.problem_kind
    }

    return participants, problems, {
        "title": workbook_title,
        "filtered_total_points": filtered_total_points,
        "filtered_problem_count": len(problems),
        "filtered_problem_kinds": kinds,
        "filtered_problem_cells": filtered_cells_total,
        "problem_kind_by_code": problem_kind_by_code,
    }
