from __future__ import annotations

import hashlib
import json
import math
import re
from datetime import datetime
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo


_DATETIME_FORMATS = (
    "%Y/%m/%d %H:%M:%S",
    "%Y-%m-%d %H:%M:%S",
    "%m/%d/%Y %I:%M:%S %p",
    "%Y-%m-%dT%H:%M:%S",
)

_VERDICT_ALIASES = {
    "accepted": "答案正确",
    "答案正确": "答案正确",
    "correct": "答案正确",
    "ac": "答案正确",
    "wrong answer": "答案错误",
    "答案错误": "答案错误",
    "partially accepted": "部分正确",
    "partial accepted": "部分正确",
    "部分正确": "部分正确",
    "compile error": "编译错误",
    "编译错误": "编译错误",
    "time limit exceeded": "运行超时",
    "运行超时": "运行超时",
    "memory limit exceeded": "内存超限",
    "内存超限": "内存超限",
    "runtime error": "运行错误",
    "运行错误": "运行错误",
    "presentation error": "格式错误",
    "格式错误": "格式错误",
    "segmentation fault": "段错误",
    "段错误": "段错误",
    "non-zero exit code": "非零退出",
    "非零退出": "非零退出",
    "multiple errors": "多个错误",
    "多个错误": "多个错误",
}


def clean_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, float) and math.isnan(value):
        return ""
    text = str(value).replace("\u00a0", " ").replace("\ufeff", "")
    return text.strip().rstrip("\t").strip()


def normalize_verdict(value: Any) -> str:
    text = clean_text(value)
    if not text:
        return ""
    folded = re.sub(r"\s+", " ", text).strip().casefold()
    return _VERDICT_ALIASES.get(folded, text)


def parse_optional_int(value: Any) -> int | None:
    text = clean_text(value)
    if not text:
        return None
    match = re.search(r"-?\d+", text)
    if not match:
        return None
    return int(match.group())


def parse_optional_float(value: Any) -> float | None:
    text = clean_text(value)
    if not text:
        return None
    match = re.search(r"-?\d+(?:\.\d+)?", text)
    if not match:
        return None
    return float(match.group())


def parse_memory_kb(value: Any) -> int | None:
    return parse_optional_int(value)


def parse_time_ms(value: Any) -> int | None:
    return parse_optional_int(value)


def parse_duration_seconds(value: Any) -> int | None:
    text = clean_text(value)
    if not text:
        return None
    if "分钟" in text:
        minutes = parse_optional_float(text)
        if minutes is None:
            return None
        return int(minutes * 60)
    if re.match(r"^\d+:\d+:\d+$", text):
        hours, minutes, seconds = text.split(":")
        return int(hours) * 3600 + int(minutes) * 60 + int(seconds)
    if re.match(r"^\d+:\d+$", text):
        minutes, seconds = text.split(":")
        return int(minutes) * 60 + int(seconds)
    return parse_optional_int(text)


def parse_datetime_text(value: Any, timezone_name: str) -> datetime | None:
    text = clean_text(value)
    if not text:
        return None
    timezone = ZoneInfo(timezone_name)
    for fmt in _DATETIME_FORMATS:
        try:
            parsed = datetime.strptime(text, fmt)
            return parsed.replace(tzinfo=timezone)
        except ValueError:
            continue
    return None


def sha256_bytes(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


def sha256_text(payload: str) -> str:
    return sha256_bytes(payload.encode("utf-8"))


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file_obj:
        while True:
            chunk = file_obj.read(1024 * 1024)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def stable_json_hash(data: dict[str, Any]) -> str:
    normalized = json.dumps(data, sort_keys=True, ensure_ascii=False, separators=(",", ":"))
    return sha256_text(normalized)


def is_noise_file(path: Path) -> bool:
    name = path.name
    return name.startswith(".") or name.endswith(":Zone.Identifier")
