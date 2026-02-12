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


def clean_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, float) and math.isnan(value):
        return ""
    text = str(value).replace("\u00a0", " ").replace("\ufeff", "")
    return text.strip().rstrip("\t").strip()


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
