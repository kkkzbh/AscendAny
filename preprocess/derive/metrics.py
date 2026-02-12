from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import numpy as np

from ..models import ParticipantRow


@dataclass(slots=True)
class StudentMetricResult:
    student_id: int
    knowledge: int | None
    accuracy: int | None
    quality: int | None
    flexibility: int | None
    proficiency: int | None
    details: dict[str, Any]


def _winsorize(values: list[float], low: float, high: float) -> dict[float, float]:
    if not values:
        return {}
    if len(values) == 1:
        return {values[0]: values[0]}
    lower = float(np.quantile(values, low))
    upper = float(np.quantile(values, high))
    return {value: min(upper, max(lower, value)) for value in values}


def _percentile_scores(raw_by_student: dict[int, float], low: float, high: float) -> dict[int, int]:
    if not raw_by_student:
        return {}
    raw_values = list(raw_by_student.values())
    clipped_map = _winsorize(raw_values, low=low, high=high)
    clipped_values = [clipped_map[value] for value in raw_values]
    ordered = sorted(clipped_values)
    total = len(ordered)
    result: dict[int, int] = {}

    for student_id, original in raw_by_student.items():
        current = clipped_map[original]
        less_count = sum(1 for item in ordered if item < current)
        equal_count = sum(1 for item in ordered if item == current)
        percentile = (less_count + 0.5 * equal_count) / total
        result[student_id] = int(round(percentile * 100))
    return result


def _problem_runtime_medians(participants: list[ParticipantRow]) -> dict[str, float]:
    bucket: dict[str, list[int]] = {}
    for participant in participants:
        if participant.absent:
            continue
        for problem_code, stats in participant.problem_stats.items():
            runtime_ms = stats.get("runtime_ms")
            solved = stats.get("solved")
            if runtime_ms is None or not solved:
                continue
            bucket.setdefault(problem_code, []).append(int(runtime_ms))
    medians: dict[str, float] = {}
    for problem_code, runtimes in bucket.items():
        if runtimes:
            medians[problem_code] = float(np.median(runtimes))
    return medians


def compute_exam_metrics(
    participants: list[ParticipantRow],
    total_points: float | None,
    winsor_low: float,
    winsor_high: float,
    flexibility_mode: str,
) -> list[StudentMetricResult]:
    runtime_medians = _problem_runtime_medians(participants)

    raw_knowledge: dict[int, float] = {}
    raw_accuracy: dict[int, float] = {}
    raw_quality: dict[int, float] = {}
    raw_flexibility: dict[int, float] = {}
    raw_proficiency: dict[int, float] = {}
    raw_details: dict[int, dict[str, Any]] = {}

    for participant in participants:
        if participant.student_id is None:
            continue
        if participant.absent:
            raw_details[participant.student_id] = {"absent": True}
            continue

        solved_count = participant.solved_count
        if solved_count is None:
            solved_count = sum(1 for stats in participant.problem_stats.values() if stats.get("solved"))
        solved_count = max(0, solved_count)

        total_score = participant.total_score
        if total_score is None:
            sum_score = 0.0
            has_score = False
            for stats in participant.problem_stats.values():
                score = stats.get("score")
                if score is not None:
                    sum_score += float(score)
                    has_score = True
            total_score = sum_score if has_score else None

        problem_count = max(1, len(participant.problem_stats))
        knowledge_signal = None
        if total_points and total_points > 0 and total_score is not None:
            knowledge_signal = total_score / total_points
        elif problem_count > 0:
            knowledge_signal = solved_count / problem_count

        attempts = [
            int(stats["attempts"])
            for stats in participant.problem_stats.values()
            if stats.get("attempts") is not None and int(stats["attempts"]) > 0
        ]
        solved_attempts = [
            int(stats["attempts"])
            for stats in participant.problem_stats.values()
            if stats.get("solved") and stats.get("attempts") is not None
        ]
        avg_attempts = (sum(solved_attempts) / len(solved_attempts)) if solved_attempts else None
        total_wrong_before_ac = sum(
            int(stats.get("wrong_before_ac", 0))
            for stats in participant.problem_stats.values()
            if stats.get("wrong_before_ac") is not None
        )
        wa_rate = total_wrong_before_ac / max(1, solved_count)
        accuracy_signal = None
        if avg_attempts is not None:
            denominator = max(0.1, 1.0 + avg_attempts + wa_rate)
            accuracy_signal = 1.0 / denominator

        runtime_ratios: list[float] = []
        for problem_code, stats in participant.problem_stats.items():
            runtime_ms = stats.get("runtime_ms")
            if runtime_ms is None:
                continue
            median_runtime = runtime_medians.get(problem_code)
            if not median_runtime:
                continue
            runtime_ratios.append(float(runtime_ms) / median_runtime)
        quality_signal = None
        if runtime_ratios:
            quality_signal = 1.0 / float(np.median(runtime_ratios))

        time_used_seconds = participant.time_used_seconds
        pace_signal = None
        if time_used_seconds and time_used_seconds > 0:
            pace_signal = solved_count / (time_used_seconds / 60.0)
        total_attempts = sum(attempts)
        waste = max(0.0, (total_attempts - solved_count) / max(1, solved_count))
        flexibility_signal = None
        if pace_signal is not None:
            flexibility_signal = pace_signal / (1.0 + waste)

        proficiency_base = total_score if total_score is not None else float(solved_count)
        proficiency_signal = None
        if time_used_seconds and time_used_seconds > 0:
            proficiency_signal = proficiency_base / max(1.0, (time_used_seconds / 60.0))

        student_id = participant.student_id
        if knowledge_signal is not None:
            raw_knowledge[student_id] = float(knowledge_signal)
        if accuracy_signal is not None:
            raw_accuracy[student_id] = float(accuracy_signal)
        if quality_signal is not None:
            raw_quality[student_id] = float(quality_signal)
        if flexibility_signal is not None:
            raw_flexibility[student_id] = float(flexibility_signal)
        if proficiency_signal is not None:
            raw_proficiency[student_id] = float(proficiency_signal)

        raw_details[student_id] = {
            "knowledge_signal": knowledge_signal,
            "accuracy_signal": accuracy_signal,
            "quality_signal": quality_signal,
            "flexibility_signal": flexibility_signal,
            "proficiency_signal": proficiency_signal,
            "solved_count": solved_count,
            "total_score": total_score,
            "time_used_seconds": time_used_seconds,
            "flexibility_mode": flexibility_mode,
        }

    knowledge_scores = _percentile_scores(raw_knowledge, low=winsor_low, high=winsor_high)
    accuracy_scores = _percentile_scores(raw_accuracy, low=winsor_low, high=winsor_high)
    quality_scores = _percentile_scores(raw_quality, low=winsor_low, high=winsor_high)
    flexibility_scores = _percentile_scores(raw_flexibility, low=winsor_low, high=winsor_high)
    proficiency_scores = _percentile_scores(raw_proficiency, low=winsor_low, high=winsor_high)

    results: list[StudentMetricResult] = []
    for participant in participants:
        if participant.student_id is None:
            continue
        student_id = participant.student_id
        details = raw_details.get(student_id, {"absent": participant.absent})
        if participant.absent:
            results.append(
                StudentMetricResult(
                    student_id=student_id,
                    knowledge=None,
                    accuracy=None,
                    quality=None,
                    flexibility=None,
                    proficiency=None,
                    details=details,
                )
            )
            continue
        results.append(
            StudentMetricResult(
                student_id=student_id,
                knowledge=knowledge_scores.get(student_id),
                accuracy=accuracy_scores.get(student_id),
                quality=quality_scores.get(student_id),
                flexibility=flexibility_scores.get(student_id),
                proficiency=proficiency_scores.get(student_id),
                details=details,
            )
        )
    return results
