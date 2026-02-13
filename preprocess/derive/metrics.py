from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any

import numpy as np

from ..models import ParticipantRow
from ..utils import clean_text


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


def _percentile_scores(
    raw_by_student: dict[int, float], low: float, high: float
) -> dict[int, int]:
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


def _verdict_text(value: Any) -> str:
    return clean_text(value).casefold()


def _is_accepted(verdict: Any) -> bool:
    text = _verdict_text(verdict)
    if not text:
        return False
    tokens = (
        "accepted",
        "答案正确",
        "通过",
        "correct",
        "ac",
    )
    return any(token in text for token in tokens)


def _is_wrong_attempt(verdict: Any) -> bool:
    text = _verdict_text(verdict)
    if not text:
        return False
    return not _is_accepted(text)


def _timeline_flexibility_signal(
    events: list[dict[str, Any]],
) -> tuple[float | None, dict[str, Any]]:
    ordered = [
        item for item in events if isinstance(item.get("submitted_at"), datetime)
    ]
    ordered.sort(key=lambda item: item["submitted_at"])
    if not ordered:
        return None, {
            "timeline_submission_count": 0,
            "timeline_reason": "missing_submitted_at",
        }

    ac_times = [
        item["submitted_at"] for item in ordered if _is_accepted(item.get("verdict"))
    ]
    if not ac_times:
        return None, {
            "timeline_submission_count": len(ordered),
            "ac_count": 0,
            "timeline_reason": "no_ac_submission",
        }

    if len(ac_times) >= 2:
        intervals = [
            max(0.0, (ac_times[index] - ac_times[index - 1]).total_seconds() / 60.0)
            for index in range(1, len(ac_times))
        ]
        ac_interval_minutes = float(np.mean(intervals)) if intervals else 0.0
    else:
        ac_interval_minutes = max(
            0.0, (ac_times[0] - ordered[0]["submitted_at"]).total_seconds() / 60.0
        )

    ac_pace = 1.0 / max(1.0, ac_interval_minutes)

    wa_total = 0
    switch_after_wa = 0
    for index in range(1, len(ordered)):
        previous = ordered[index - 1]
        current = ordered[index]
        if not _is_wrong_attempt(previous.get("verdict")):
            continue
        wa_total += 1
        prev_problem = clean_text(previous.get("problem_code"))
        current_problem = clean_text(current.get("problem_code"))
        if prev_problem and current_problem and prev_problem != current_problem:
            switch_after_wa += 1
    switch_after_wa_rate = (switch_after_wa / wa_total) if wa_total > 0 else 0.5

    stuck_durations: list[float] = []
    streak_problem: str | None = None
    streak_start: datetime | None = None
    streak_last: datetime | None = None

    for event in ordered:
        problem_code = clean_text(event.get("problem_code")) or "__unknown_problem__"
        event_time = event["submitted_at"]
        accepted = _is_accepted(event.get("verdict"))
        wrong_attempt = _is_wrong_attempt(event.get("verdict"))

        if streak_problem is None:
            if wrong_attempt:
                streak_problem = problem_code
                streak_start = event_time
                streak_last = event_time
            continue

        if problem_code != streak_problem:
            if streak_start is not None and streak_last is not None:
                stuck_durations.append(
                    max(0.0, (streak_last - streak_start).total_seconds() / 60.0)
                )
            if wrong_attempt:
                streak_problem = problem_code
                streak_start = event_time
                streak_last = event_time
            else:
                streak_problem = None
                streak_start = None
                streak_last = None
            continue

        if wrong_attempt and streak_start is not None:
            streak_last = event_time
        if accepted and streak_start is not None:
            stuck_durations.append(
                max(0.0, (event_time - streak_start).total_seconds() / 60.0)
            )
            streak_problem = None
            streak_start = None
            streak_last = None

    if (
        streak_problem is not None
        and streak_start is not None
        and streak_last is not None
    ):
        stuck_durations.append(
            max(0.0, (streak_last - streak_start).total_seconds() / 60.0)
        )

    max_stuck_minutes = max(stuck_durations) if stuck_durations else 0.0
    stuck_penalty = 1.0 + min(3.0, max_stuck_minutes / 30.0)

    signal = (ac_pace * (0.5 + 0.5 * switch_after_wa_rate)) / stuck_penalty
    return float(signal), {
        "timeline_submission_count": len(ordered),
        "ac_count": len(ac_times),
        "ac_pace": ac_pace,
        "switch_after_wa_rate": switch_after_wa_rate,
        "stuck_penalty": stuck_penalty,
        "stuck_max_minutes": max_stuck_minutes,
        "timeline_reason": "ok",
    }


def compute_flexibility_scores(
    approx_signals: dict[int, float],
    timeline_by_student: dict[int, list[dict[str, Any]]] | None,
    winsor_low: float,
    winsor_high: float,
    fallback_mode: str,
) -> tuple[dict[int, int], dict[int, dict[str, Any]]]:
    timeline = timeline_by_student or {}
    raw_flexibility: dict[int, float] = {}
    details_by_student: dict[int, dict[str, Any]] = {}

    student_ids = set(approx_signals) | set(timeline)
    for student_id in student_ids:
        approx_signal = approx_signals.get(student_id)
        timeline_signal, timeline_details = _timeline_flexibility_signal(
            timeline.get(student_id, [])
        )
        if timeline_signal is not None:
            raw_flexibility[student_id] = timeline_signal
            details_by_student[student_id] = {
                "flexibility_mode": "timeline",
                "flexibility_signal": timeline_signal,
                **timeline_details,
            }
            continue

        if approx_signal is not None:
            raw_flexibility[student_id] = float(approx_signal)
        details_by_student[student_id] = {
            "flexibility_mode": fallback_mode if approx_signal is not None else "none",
            "flexibility_signal": float(approx_signal)
            if approx_signal is not None
            else None,
            **timeline_details,
        }

    flexibility_scores = _percentile_scores(
        raw_flexibility, low=winsor_low, high=winsor_high
    )
    return flexibility_scores, details_by_student


def compute_exam_metrics(
    participants: list[ParticipantRow],
    total_points: float | None,
    winsor_low: float,
    winsor_high: float,
    flexibility_mode: str,
    timeline_by_student: dict[int, list[dict[str, Any]]] | None = None,
) -> list[StudentMetricResult]:
    runtime_medians = _problem_runtime_medians(participants)

    raw_knowledge: dict[int, float] = {}
    raw_accuracy: dict[int, float] = {}
    raw_quality: dict[int, float] = {}
    approx_flexibility: dict[int, float] = {}
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
            solved_count = sum(
                1 for stats in participant.problem_stats.values() if stats.get("solved")
            )
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
        avg_attempts = (
            (sum(solved_attempts) / len(solved_attempts)) if solved_attempts else None
        )
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
            if runtime_ms is None or not stats.get("solved"):
                continue
            median_runtime = runtime_medians.get(problem_code)
            if median_runtime is None or median_runtime <= 0:
                continue
            runtime_ratios.append(max(1e-6, float(runtime_ms) / median_runtime))
        quality_signal = None
        if runtime_ratios:
            median_runtime_ratio = float(np.median(runtime_ratios))
            if median_runtime_ratio > 0:
                quality_signal = 1.0 / median_runtime_ratio

        time_used_seconds = participant.time_used_seconds
        pace_signal = None
        if time_used_seconds and time_used_seconds > 0:
            pace_signal = solved_count / (time_used_seconds / 60.0)
        total_attempts = sum(attempts)
        waste = max(0.0, (total_attempts - solved_count) / max(1, solved_count))
        flexibility_signal = None
        if pace_signal is not None:
            flexibility_signal = pace_signal / (1.0 + waste)

        proficiency_base = (
            total_score if total_score is not None else float(solved_count)
        )
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
            approx_flexibility[student_id] = float(flexibility_signal)
        if proficiency_signal is not None:
            raw_proficiency[student_id] = float(proficiency_signal)

        raw_details[student_id] = {
            "knowledge_signal": knowledge_signal,
            "accuracy_signal": accuracy_signal,
            "quality_signal": quality_signal,
            "flexibility_signal_approx": flexibility_signal,
            "proficiency_signal": proficiency_signal,
            "solved_count": solved_count,
            "total_score": total_score,
            "time_used_seconds": time_used_seconds,
        }

    knowledge_scores = _percentile_scores(
        raw_knowledge, low=winsor_low, high=winsor_high
    )
    accuracy_scores = _percentile_scores(raw_accuracy, low=winsor_low, high=winsor_high)
    quality_scores = _percentile_scores(raw_quality, low=winsor_low, high=winsor_high)
    flexibility_scores, flexibility_details = compute_flexibility_scores(
        approx_signals=approx_flexibility,
        timeline_by_student=timeline_by_student,
        winsor_low=winsor_low,
        winsor_high=winsor_high,
        fallback_mode=flexibility_mode,
    )
    proficiency_scores = _percentile_scores(
        raw_proficiency, low=winsor_low, high=winsor_high
    )

    results: list[StudentMetricResult] = []
    for participant in participants:
        if participant.student_id is None:
            continue
        student_id = participant.student_id
        details = dict(raw_details.get(student_id, {"absent": participant.absent}))
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
        details.update(
            flexibility_details.get(student_id, {"flexibility_mode": "none"})
        )
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
