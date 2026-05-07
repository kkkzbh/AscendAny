from __future__ import annotations

import csv
import io
import re
from pathlib import Path
from typing import Any

from ...models import SourceFile, SubmissionRow
from ...utils import (
    clean_text,
    normalize_verdict,
    parse_datetime_text,
    parse_memory_kb,
    parse_optional_float,
    parse_time_ms,
    stable_json_hash,
)

_STUDENT_NO_RE = re.compile(r"^\d{6,}$")


def _canonical_problem_code(
    value: str,
    problem_code_aliases: dict[str, str] | None,
) -> str | None:
    problem_code = clean_text(value)
    if not problem_code:
        return None
    if problem_code_aliases and problem_code in problem_code_aliases:
        return problem_code_aliases[problem_code]
    return problem_code


def _is_datastructure_student_no_layout(values: list[str]) -> bool:
    if len(values) < 9:
        return False
    student_no = clean_text(values[4])
    display_name = clean_text(values[5])
    language = clean_text(values[6])
    return bool(
        _STUDENT_NO_RE.fullmatch(student_no)
        and display_name
        and language
    )


def decode_csv(path: Path, encodings: list[str]) -> str:
    payload = path.read_bytes()
    for encoding in encodings:
        try:
            return payload.decode(encoding)
        except UnicodeDecodeError:
            continue
    return payload.decode(encodings[-1], errors="ignore")


def _parse_datastructure_row(
    values: list[str],
    source: SourceFile,
    row_index: int,
    timezone_name: str,
    problem_code_aliases: dict[str, str] | None,
) -> SubmissionRow | None:
    if len(values) < 8:
        return None
    uses_student_no_layout = _is_datastructure_student_no_layout(values)
    actor_external_id = clean_text(values[4])
    if not actor_external_id:
        return None
    actor_name = clean_text(values[5]) if uses_student_no_layout else actor_external_id
    problem_code = _canonical_problem_code(values[3], problem_code_aliases)
    verdict = normalize_verdict(values[1])

    payload: dict[str, Any] = {
        "submitted_at": clean_text(values[0]),
        "verdict": verdict,
        "score": parse_optional_float(values[2]),
        "problem_code": problem_code,
        "actor_source": "datastructure_student_no"
        if uses_student_no_layout
        else "datastructure_nickname",
        "actor_external_id": actor_external_id,
        "actor_name": actor_name,
        "language": clean_text(values[6] if uses_student_no_layout else values[5]) or None,
        "memory_kb": parse_memory_kb(values[7] if uses_student_no_layout else values[6]),
        "time_ms": parse_time_ms(values[8] if uses_student_no_layout else values[7]),
    }
    row_hash = stable_json_hash(payload)
    return SubmissionRow(
        actor_source=payload["actor_source"],
        actor_external_id=payload["actor_external_id"],
        actor_name=payload["actor_name"],
        submitted_at=parse_datetime_text(payload["submitted_at"], timezone_name=timezone_name),
        verdict=payload["verdict"],
        score=payload["score"],
        problem_code=payload["problem_code"],
        language=payload["language"],
        memory_kb=payload["memory_kb"],
        time_ms=payload["time_ms"],
        row_hash=row_hash,
        raw={
            "source_file": source.relative_path,
            "row_index": row_index,
            "raw_score": clean_text(values[2]),
            "raw_verdict": clean_text(values[1]),
            "datastructure_layout": "student_no"
            if uses_student_no_layout
            else "nickname",
        },
    )


def _parse_pta_row(
    values: list[str],
    source: SourceFile,
    row_index: int,
    timezone_name: str,
    exam_type: str,
    problem_code_aliases: dict[str, str] | None,
) -> SubmissionRow | None:
    if len(values) < 9:
        return None
    actor_external_id = clean_text(values[4])
    if not actor_external_id:
        return None
    actor_name = clean_text(values[5]) or None
    problem_code = _canonical_problem_code(values[3], problem_code_aliases)
    verdict = normalize_verdict(values[1])
    payload: dict[str, Any] = {
        "submitted_at": clean_text(values[0]),
        "verdict": verdict,
        "score": parse_optional_float(values[2]),
        "problem_code": problem_code,
        "actor_source": f"{exam_type}_account",
        "actor_external_id": actor_external_id,
        "actor_name": actor_name,
        "language": clean_text(values[6]) or None,
        "memory_kb": parse_memory_kb(values[7]),
        "time_ms": parse_time_ms(values[8]),
    }
    row_hash = stable_json_hash(payload)
    return SubmissionRow(
        actor_source=payload["actor_source"],
        actor_external_id=payload["actor_external_id"],
        actor_name=payload["actor_name"],
        submitted_at=parse_datetime_text(payload["submitted_at"], timezone_name=timezone_name),
        verdict=payload["verdict"],
        score=payload["score"],
        problem_code=payload["problem_code"],
        language=payload["language"],
        memory_kb=payload["memory_kb"],
        time_ms=payload["time_ms"],
        row_hash=row_hash,
        raw={
            "source_file": source.relative_path,
            "row_index": row_index,
            "raw_score": clean_text(values[2]),
            "raw_verdict": clean_text(values[1]),
        },
    )


def parse_submission_csv(
    file: SourceFile,
    exam_type: str,
    encodings: list[str],
    timezone_name: str,
    allowed_problem_codes: set[str] | None = None,
    problem_code_aliases: dict[str, str] | None = None,
) -> list[SubmissionRow]:
    text = decode_csv(file.absolute_path, encodings=encodings)
    reader = csv.reader(io.StringIO(text))
    rows: list[SubmissionRow] = []
    for row_index, values in enumerate(reader, start=1):
        cleaned = [clean_text(item) for item in values]
        if not any(cleaned):
            continue
        if exam_type == "datastructure":
            parsed = _parse_datastructure_row(
                cleaned,
                source=file,
                row_index=row_index,
                timezone_name=timezone_name,
                problem_code_aliases=problem_code_aliases,
            )
        else:
            parsed = _parse_pta_row(
                cleaned,
                source=file,
                row_index=row_index,
                timezone_name=timezone_name,
                exam_type=exam_type,
                problem_code_aliases=problem_code_aliases,
            )
        if parsed is not None:
            problem_code = clean_text(parsed.problem_code)
            if allowed_problem_codes is not None and (
                not problem_code or problem_code not in allowed_problem_codes
            ):
                continue
            rows.append(parsed)
    return rows
