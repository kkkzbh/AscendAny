from __future__ import annotations

from datetime import datetime, timezone
from pathlib import Path
import json
from typing import Iterable

from .models import ExamUnit, SourceFile
from .utils import is_noise_file, sha256_file, sha256_text

PINTIA_UNIT_SCHEMA = "ascendany.pintia.unit.v1"
PINTIA_UNIT_ROLE = "ascendany_pintia_unit_json"


def _read_json_schema(path: Path) -> str | None:
    try:
        with path.open("r", encoding="utf-8") as file_obj:
            payload = json.load(file_obj)
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        return None
    if not isinstance(payload, dict):
        return None
    schema = payload.get("schema")
    return schema if isinstance(schema, str) else None


def _detect_file_role(path: Path) -> str | None:
    if path.suffix.lower() != ".json":
        return None
    if _read_json_schema(path) == PINTIA_UNIT_SCHEMA:
        return PINTIA_UNIT_ROLE
    return None


def _schema_to_exam_type(schema: str) -> str | None:
    if schema == PINTIA_UNIT_SCHEMA:
        return "pintia"
    return None


def discover_exam_units(
    practice_root: Path,
    exam_types: Iterable[str] | None = None,
    fingerprint_roles: set[str] | None = None,
) -> list[ExamUnit]:
    units: list[ExamUnit] = []
    fingerprint_roles = fingerprint_roles or {PINTIA_UNIT_ROLE}
    exam_type_filter = set(exam_types) if exam_types is not None else None

    if not practice_root.exists() or not practice_root.is_dir():
        return []

    for file_path in sorted(practice_root.rglob("*.json"), key=lambda item: item.as_posix()):
        if not file_path.is_file() or is_noise_file(file_path):
            continue
        schema = _read_json_schema(file_path)
        if schema is None:
            continue
        exam_type = _schema_to_exam_type(schema)
        if exam_type is None:
            continue
        if exam_type_filter is not None and exam_type not in exam_type_filter:
            continue
        role = _detect_file_role(file_path)
        if role is None:
            continue
        relative_path = file_path.relative_to(practice_root).as_posix()
        stat = file_path.stat()
        source_file = SourceFile(
            file_role=role,
            relative_path=relative_path,
            absolute_path=file_path,
            sha256=sha256_file(file_path),
            size_bytes=stat.st_size,
            mtime=datetime.fromtimestamp(stat.st_mtime, tz=timezone.utc),
        )
        pieces = [
            f"{source_file.file_role}|{source_file.relative_path}|{source_file.sha256}"
            if source_file.file_role in fingerprint_roles
            else ""
        ]
        if not any(pieces):
            pieces = [f"{source_file.file_role}|{source_file.relative_path}|{source_file.sha256}"]
        fingerprint = sha256_text("\n".join(sorted(item for item in pieces if item)))
        units.append(
            ExamUnit(
                exam_type=exam_type,
                source_path=relative_path,
                absolute_path=file_path.parent,
                files=[source_file],
                fingerprint=fingerprint,
            )
        )

    return units
