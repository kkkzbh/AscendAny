from __future__ import annotations

import csv
import io
from pathlib import Path
from typing import Any

from ..models import SourceFile, SubmissionRow
from ..utils import (
    clean_text,
    parse_datetime_text,
    parse_memory_kb,
    parse_optional_float,
    parse_time_ms,
    stable_json_hash,
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
    values: list[str], source: SourceFile, row_index: int, timezone_name: str
) -> SubmissionRow | None:
    if len(values) < 8:
        return None
    actor_external_id = clean_text(values[4])
    if not actor_external_id:
        return None

    payload: dict[str, Any] = {
        "submitted_at": clean_text(values[0]),
        "verdict": clean_text(values[1]),
        "score": parse_optional_float(values[2]),
        "problem_code": clean_text(values[3]) or None,
        "actor_source": "datastructure_nickname",
        "actor_external_id": actor_external_id,
        "actor_name": actor_external_id,
        "language": clean_text(values[5]) or None,
        "memory_kb": parse_memory_kb(values[6]),
        "time_ms": parse_time_ms(values[7]),
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
        },
    )


def _parse_pta_row(values: list[str], source: SourceFile, row_index: int, timezone_name: str, exam_type: str) -> SubmissionRow | None:
    if len(values) < 9:
        return None
    actor_external_id = clean_text(values[4])
    if not actor_external_id:
        return None
    actor_name = clean_text(values[5]) or None
    payload: dict[str, Any] = {
        "submitted_at": clean_text(values[0]),
        "verdict": clean_text(values[1]),
        "score": parse_optional_float(values[2]),
        "problem_code": clean_text(values[3]) or None,
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
        },
    )


def parse_submission_csv(
    file: SourceFile,
    exam_type: str,
    encodings: list[str],
    timezone_name: str,
    allowed_problem_codes: set[str] | None = None,
) -> list[SubmissionRow]:
    text = decode_csv(file.absolute_path, encodings=encodings)
    reader = csv.reader(io.StringIO(text))
    rows: list[SubmissionRow] = []
    for row_index, values in enumerate(reader, start=1):
        cleaned = [clean_text(item) for item in values]
        if not any(cleaned):
            continue
        if exam_type == "datastructure":
            parsed = _parse_datastructure_row(cleaned, source=file, row_index=row_index, timezone_name=timezone_name)
        else:
            parsed = _parse_pta_row(
                cleaned, source=file, row_index=row_index, timezone_name=timezone_name, exam_type=exam_type
            )
        if parsed is not None:
            problem_code = clean_text(parsed.problem_code)
            if allowed_problem_codes is not None and (
                not problem_code or problem_code not in allowed_problem_codes
            ):
                continue
            rows.append(parsed)
    return rows
