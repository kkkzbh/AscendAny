from __future__ import annotations

import re
from datetime import datetime
from typing import Any

from bs4 import BeautifulSoup

from ...models import ExamMeta, SourceFile
from ...utils import clean_text, parse_datetime_text, parse_duration_seconds, parse_optional_float

_KIND_HEADER_RE = re.compile(
    r'<span class="text-base text-normal font-bold flex-1">([^<]+)</span>'
)
_POOL_RE = re.compile(
    r"<div class=\"flex-none font-medium\">([A-Za-z]?\d+-\d+)</div>"
    r"<div style=\"font-weight:bold\">([^<]+)</div><div>\((\d+)\s*选\s*(\d+)\)</div>"
)
_PROBLEM_ENTRY_RE = re.compile(
    r"<div class=\"flex-none font-medium\">([A-Za-z]?\d+-\d+(?:-\d+)?)</div><div class=\"flex-1"
)


def _extract_label_value(raw_html: str, label: str) -> str | None:
    pattern = rf"{re.escape(label)}</span><span[^>]*>([^<]+)</span>"
    match = re.search(pattern, raw_html)
    if not match:
        return None
    return clean_text(match.group(1))


def _parse_optional_positive_int(text: str | None) -> int | None:
    value = clean_text(text)
    if not value:
        return None
    match = re.search(r"\d+", value)
    if not match:
        return None
    parsed = int(match.group())
    if parsed <= 0:
        return None
    return parsed


def _extract_problem_catalog(raw_html: str) -> dict[str, Any]:
    kind_matches = list(_KIND_HEADER_RE.finditer(raw_html))
    if not kind_matches:
        return {
            "by_code": {},
            "pools": [],
            "slot_count_by_kind": {},
            "has_random_pool": False,
        }

    by_code: dict[str, dict[str, Any]] = {}
    pools: list[dict[str, Any]] = []
    slot_count_by_kind: dict[str, int] = {}

    for idx, match in enumerate(kind_matches):
        kind = clean_text(match.group(1))
        if not kind:
            continue
        start = match.end()
        end = kind_matches[idx + 1].start() if idx + 1 < len(kind_matches) else len(raw_html)
        segment = raw_html[start:end]

        pool_entries: list[dict[str, Any]] = []
        for pool_match in _POOL_RE.finditer(segment):
            pool_code = clean_text(pool_match.group(1))
            pool_name = clean_text(pool_match.group(2))
            pool_size_n = _parse_optional_positive_int(pool_match.group(3))
            pool_choose_k = _parse_optional_positive_int(pool_match.group(4))
            if not pool_code:
                continue
            entry = {
                "problem_kind": kind,
                "pool_code": pool_code,
                "pool_name": pool_name or None,
                "pool_size_n": pool_size_n,
                "pool_choose_k": pool_choose_k,
            }
            pools.append(entry)
            pool_entries.append(entry)
            if pool_choose_k is not None:
                slot_count_by_kind[kind] = slot_count_by_kind.get(kind, 0) + pool_choose_k

        for problem_match in _PROBLEM_ENTRY_RE.finditer(segment):
            problem_code = clean_text(problem_match.group(1))
            if not problem_code:
                continue
            payload = by_code.setdefault(problem_code, {})
            payload["problem_kind"] = kind
            for pool in pool_entries:
                pool_code = clean_text(pool.get("pool_code"))
                if not pool_code:
                    continue
                if problem_code.startswith(f"{pool_code}-"):
                    payload["pool_code"] = pool_code
                    payload["pool_name"] = pool.get("pool_name")
                    payload["pool_size_n"] = pool.get("pool_size_n")
                    payload["pool_choose_k"] = pool.get("pool_choose_k")
                    break

    return {
        "by_code": by_code,
        "pools": pools,
        "slot_count_by_kind": slot_count_by_kind,
        "has_random_pool": bool(pools),
    }


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
    exam_type_text = _extract_label_value(raw_html, "试卷类型")
    problem_catalog = _extract_problem_catalog(raw_html)

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
            "exam_paper_type": exam_type_text,
            "is_random_exam": "随机" in clean_text(exam_type_text),
            "problem_catalog": problem_catalog,
            "problem_kind_by_code": {
                code: clean_text(payload.get("problem_kind")) or None
                for code, payload in problem_catalog.get("by_code", {}).items()
                if clean_text(code)
            },
            "slot_count_by_kind": dict(problem_catalog.get("slot_count_by_kind", {})),
        },
    )
