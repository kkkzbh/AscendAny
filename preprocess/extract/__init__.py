from __future__ import annotations

from dataclasses import replace

from ..models import ExamBundle, ExamMeta, ExamUnit
from .csv_reader import parse_submission_csv
from .html_reader import parse_exam_html
from .xlsx_reader import parse_scoreboards


def parse_exam_bundle(
    unit: ExamUnit, encodings: list[str], timezone_name: str
) -> ExamBundle:
    html_files = [item for item in unit.files if item.file_role == "answer_html"]
    csv_files = [item for item in unit.files if item.file_role == "submission_csv"]
    xlsx_files = [item for item in unit.files if item.file_role == "scoreboard_xlsx"]

    exam_meta: ExamMeta = parse_exam_html(
        files=html_files,
        timezone_name=timezone_name,
    )
    participants, problems, xlsx_meta = parse_scoreboards(
        files=xlsx_files,
        exam_type=unit.exam_type,
    )

    if not exam_meta.title and xlsx_meta.get("title"):
        exam_meta = replace(exam_meta, title=xlsx_meta["title"])

    submissions = []
    seen_hashes: set[str] = set()
    for source in csv_files:
        for row in parse_submission_csv(
            file=source,
            exam_type=unit.exam_type,
            encodings=encodings,
            timezone_name=timezone_name,
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
