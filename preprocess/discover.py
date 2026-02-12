from __future__ import annotations

from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable

from .models import ExamUnit, SourceFile
from .utils import is_noise_file, sha256_file, sha256_text


def _detect_file_role(path: Path) -> str | None:
    normalized = path.as_posix()
    if "/提交记录/" in normalized and path.suffix.lower() == ".csv":
        return "submission_csv"
    if "/成绩单/" in normalized and path.suffix.lower() == ".xlsx":
        return "scoreboard_xlsx"
    if "/答卷/" in normalized and path.suffix.lower() == ".html":
        return "answer_html"
    return None


def _iter_exam_dirs(practice_root: Path, exam_types: Iterable[str] | None = None) -> list[tuple[str, Path]]:
    if exam_types is None:
        type_dirs = [p for p in practice_root.iterdir() if p.is_dir() and not p.name.startswith(".")]
    else:
        type_dirs = [practice_root / exam_type for exam_type in exam_types]

    pairs: list[tuple[str, Path]] = []
    for type_dir in sorted(type_dirs, key=lambda item: item.name):
        if not type_dir.exists() or not type_dir.is_dir():
            continue
        exam_type = type_dir.name
        for exam_dir in sorted(type_dir.iterdir(), key=lambda item: item.name):
            if not exam_dir.is_dir():
                continue
            if exam_dir.name == "tmp" or exam_dir.name.startswith("."):
                continue
            pairs.append((exam_type, exam_dir))
    return pairs


def discover_exam_units(
    practice_root: Path, exam_types: Iterable[str] | None = None, fingerprint_roles: set[str] | None = None
) -> list[ExamUnit]:
    units: list[ExamUnit] = []
    fingerprint_roles = fingerprint_roles or {"answer_html", "submission_csv", "scoreboard_xlsx"}

    for exam_type, exam_dir in _iter_exam_dirs(practice_root=practice_root, exam_types=exam_types):
        source_files: list[SourceFile] = []
        for file_path in sorted(exam_dir.rglob("*"), key=lambda item: item.as_posix()):
            if not file_path.is_file():
                continue
            if is_noise_file(file_path):
                continue
            role = _detect_file_role(file_path)
            if role is None:
                continue
            stat = file_path.stat()
            source_files.append(
                SourceFile(
                    file_role=role,
                    relative_path=file_path.relative_to(exam_dir).as_posix(),
                    absolute_path=file_path,
                    sha256=sha256_file(file_path),
                    size_bytes=stat.st_size,
                    mtime=datetime.fromtimestamp(stat.st_mtime, tz=timezone.utc),
                )
            )

        if not source_files:
            continue

        pieces = [
            f"{src.file_role}|{src.relative_path}|{src.sha256}"
            for src in source_files
            if src.file_role in fingerprint_roles
        ]
        fingerprint = sha256_text("\n".join(sorted(pieces)))
        units.append(
            ExamUnit(
                exam_type=exam_type,
                source_path=exam_dir.relative_to(practice_root).as_posix(),
                absolute_path=exam_dir,
                files=source_files,
                fingerprint=fingerprint,
            )
        )

    return units
