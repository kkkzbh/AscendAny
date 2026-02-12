from __future__ import annotations

import math
from datetime import datetime, timezone
from typing import Any


def _weighted_average(values: list[tuple[datetime, float]], half_life_days: float) -> float | None:
    if not values:
        return None
    now = datetime.now(timezone.utc)
    numerator = 0.0
    denominator = 0.0
    for event_time, score in values:
        age_days = max(0.0, (now - event_time).total_seconds() / 86400.0)
        weight = math.exp(-math.log(2.0) * age_days / max(0.1, half_life_days))
        numerator += weight * score
        denominator += weight
    if denominator == 0:
        return None
    return numerator / denominator


def compute_current_metrics(
    history_rows: list[dict[str, Any]],
    half_life_days: dict[str, float],
) -> dict[str, float | None]:
    bucket: dict[str, list[tuple[datetime, float]]] = {
        "knowledge": [],
        "accuracy": [],
        "quality": [],
        "flexibility": [],
        "proficiency": [],
    }
    for row in history_rows:
        event_time = row["event_time"]
        for key in bucket:
            value = row.get(key)
            if value is None:
                continue
            bucket[key].append((event_time, float(value)))

    return {
        "knowledge": _weighted_average(bucket["knowledge"], half_life_days["knowledge"]),
        "accuracy": _weighted_average(bucket["accuracy"], half_life_days["accuracy"]),
        "quality": _weighted_average(bucket["quality"], half_life_days["quality"]),
        "flexibility": _weighted_average(bucket["flexibility"], half_life_days["flexibility"]),
        "proficiency": _weighted_average(bucket["proficiency"], half_life_days["proficiency"]),
    }
