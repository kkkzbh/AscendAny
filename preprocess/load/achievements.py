from __future__ import annotations

from dataclasses import dataclass
from typing import Any


@dataclass(slots=True)
class AchievementDefinition:
    achievement_code: str
    progress_key: str
    bronze_target: float
    silver_target: float
    gold_target: float


def coerce_numeric(value: Any) -> float:
    if value is None:
        return 0.0
    if isinstance(value, bool):
        return float(int(value))
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0


def evaluate_tier(
    progress: float,
    bronze_target: float,
    silver_target: float,
    gold_target: float,
) -> int:
    if progress >= gold_target:
        return 3
    if progress >= silver_target:
        return 2
    if progress >= bronze_target:
        return 1
    return 0


def build_state_rows(
    student_ids: list[int],
    definitions: list[AchievementDefinition],
    progress_by_student: dict[int, dict[str, Any]],
) -> list[tuple[int, str, float, int, int]]:
    rows: list[tuple[int, str, float, int, int]] = []
    for student_id in student_ids:
        progress_map = progress_by_student.get(student_id, {})
        for definition in definitions:
            progress = coerce_numeric(progress_map.get(definition.progress_key))
            tier = evaluate_tier(
                progress=progress,
                bronze_target=definition.bronze_target,
                silver_target=definition.silver_target,
                gold_target=definition.gold_target,
            )
            rows.append(
                (
                    student_id,
                    definition.achievement_code,
                    progress,
                    tier,
                    tier,
                )
            )
    return rows
