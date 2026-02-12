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


def _get_value(row: dict[str, Any], candidates: list[str]) -> str:
    for candidate in candidates:
        for key, value in row.items():
            if candidate in key:
                return clean_text(value)
    return ""


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
) -> list[_ParsedSheetRow]:
    header_index = _find_header_row(df)
    if header_index is None:
        return []

    header_values = [clean_text(value) for value in df.iloc[header_index].tolist()]
    columns = _unique_column_names(header_values)
    problem_columns = [column for column in columns if _PROBLEM_CODE_RE.match(column)]

    results: list[_ParsedSheetRow] = []
    sheet_title = clean_text(df.iloc[0, 0]) if len(df) > 0 else ""
    if "成绩明细" not in sheet_title:
        sheet_title = ""

    for raw_index in range(header_index + 1, len(df)):
        row_series = df.iloc[raw_index].tolist()
        row_map: dict[str, Any] = {columns[idx]: row_series[idx] for idx in range(min(len(columns), len(row_series)))}
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
            for problem_code in problem_columns:
                maybe_points = parse_optional_float(row_map.get(problem_code))
                if maybe_points is not None:
                    points[problem_code] = maybe_points
            if points:
                results.append(_ParsedSheetRow(participant=None, problem_points=points, title=sheet_title or None))
            continue

        identity = identity_source
        if not external_id and display_name:
            external_id = f"name::{display_name}"
            identity = f"{identity_source}_name_fallback"

        problem_stats: dict[str, dict[str, Any]] = {}
        for problem_code in problem_columns:
            cell_data = _parse_problem_cell(row_map.get(problem_code), exam_type=exam_type)
            if cell_data.get("raw"):
                problem_stats[problem_code] = cell_data

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
            raw={"sheet_name": sheet_name, "total_score_raw": total_score_text},
        )
        results.append(_ParsedSheetRow(participant=participant, problem_points={}, title=sheet_title or None))
    return results


def parse_scoreboards(files: list[SourceFile], exam_type: str) -> tuple[list[ParticipantRow], list[ProblemInfo], dict[str, Any]]:
    identity_source = f"{exam_type}_student_no"
    participants_by_key: dict[tuple[str, str], ParticipantRow] = {}
    problem_points: dict[str, float] = {}
    all_problem_codes: set[str] = set()
    workbook_title: str | None = None

    for source in sorted(files, key=lambda item: item.relative_path):
        workbook = pd.read_excel(source.absolute_path, sheet_name=None, dtype=object)
        for sheet_name, frame in workbook.items():
            parsed_rows = _parse_sheet(frame, sheet_name=sheet_name, exam_type=exam_type, identity_source=identity_source)
            for parsed in parsed_rows:
                if parsed.title and workbook_title is None:
                    workbook_title = parsed.title
                problem_points.update(parsed.problem_points)
                if parsed.participant is None:
                    continue

                key = (parsed.participant.identity_source, parsed.participant.external_id)
                for problem_code in parsed.participant.problem_stats:
                    all_problem_codes.add(problem_code)

                existing = participants_by_key.get(key)
                if existing is None or _completeness(parsed.participant) > _completeness(existing):
                    participants_by_key[key] = parsed.participant

    for code in problem_points:
        all_problem_codes.add(code)

    problems = [
        ProblemInfo(problem_code=code, points=problem_points.get(code), order_idx=index + 1)
        for index, code in enumerate(sorted(all_problem_codes))
    ]
    participants = sorted(
        participants_by_key.values(),
        key=lambda item: (item.rank is None, item.rank if item.rank is not None else 10**9, item.external_id),
    )

    return participants, problems, {"title": workbook_title}
