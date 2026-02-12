from __future__ import annotations

import re
from datetime import datetime

from bs4 import BeautifulSoup

from ..models import ExamMeta, SourceFile
from ..utils import clean_text, parse_datetime_text, parse_duration_seconds, parse_optional_float


def _extract_label_value(raw_html: str, label: str) -> str | None:
    pattern = rf"{re.escape(label)}</span><span[^>]*>([^<]+)</span>"
    match = re.search(pattern, raw_html)
    if not match:
        return None
    return clean_text(match.group(1))


def parse_exam_html(files: list[SourceFile], timezone_name: str) -> ExamMeta:
    if not files:
        return ExamMeta(
            title=None,
            starts_at=None,
            ends_at=None,
            duration_seconds=None,
            total_points=None,
            meta={},
        )

    source = sorted(files, key=lambda item: item.relative_path)[0]
    raw_html = source.absolute_path.read_text(encoding="utf-8", errors="ignore")
    soup = BeautifulSoup(raw_html, "lxml")
    title = clean_text(soup.title.get_text()) if soup.title else None

    starts_at_text = _extract_label_value(raw_html, "开始时间")
    ends_at_text = _extract_label_value(raw_html, "结束时间")
    duration_text = _extract_label_value(raw_html, "答题时长")
    total_points_text = _extract_label_value(raw_html, "试卷总分")

    starts_at: datetime | None = parse_datetime_text(starts_at_text, timezone_name=timezone_name)
    ends_at: datetime | None = parse_datetime_text(ends_at_text, timezone_name=timezone_name)

    return ExamMeta(
        title=title or None,
        starts_at=starts_at,
        ends_at=ends_at,
        duration_seconds=parse_duration_seconds(duration_text),
        total_points=parse_optional_float(total_points_text),
        meta={
            "answer_file": source.relative_path,
            "starts_at_raw": starts_at_text,
            "ends_at_raw": ends_at_text,
            "duration_raw": duration_text,
            "total_points_raw": total_points_text,
        },
    )
