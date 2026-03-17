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


def _accepted_runtime_ms_by_student_problem(
    timeline_by_student: dict[int, list[dict[str, Any]]] | None,
) -> dict[int, dict[str, int]]:
    if not timeline_by_student:
        return {}
    result: dict[int, dict[str, int]] = {}
    for student_id, events in timeline_by_student.items():
        accepted_by_problem: dict[str, int] = {}
        for event in events:
            problem_code = clean_text(event.get("problem_code"))
            if not problem_code or not _is_accepted(event.get("verdict")):
                continue
            raw_time_ms = event.get("time_ms")
            if raw_time_ms is None:
                continue
            try:
                time_ms = int(raw_time_ms)
            except (TypeError, ValueError):
                continue
            current = accepted_by_problem.get(problem_code)
            if current is None or time_ms < current:
                accepted_by_problem[problem_code] = time_ms
        if accepted_by_problem:
            result[int(student_id)] = accepted_by_problem
    return result


def _resolved_quality_runtime_ms(
    student_id: int | None,
    problem_code: str,
    stats: dict[str, Any],
    accepted_runtime_by_student_problem: dict[int, dict[str, int]],
) -> int | None:
    if not stats.get("solved"):
        return None
    raw_runtime_ms = stats.get("runtime_ms")
    if raw_runtime_ms is not None:
        try:
            return int(raw_runtime_ms)
        except (TypeError, ValueError):
            return None
    if student_id is None:
        return None
    return accepted_runtime_by_student_problem.get(student_id, {}).get(
        clean_text(problem_code)
    )


def _problem_runtime_medians(
    participants: list[ParticipantRow],
    accepted_runtime_by_student_problem: dict[int, dict[str, int]] | None = None,
) -> dict[str, float]:
    bucket: dict[str, list[int]] = {}
    runtime_fallbacks = accepted_runtime_by_student_problem or {}
    for participant in participants:
        if participant.absent:
            continue
        for problem_code, stats in participant.problem_stats.items():
            runtime_ms = _resolved_quality_runtime_ms(
                student_id=participant.student_id,
                problem_code=problem_code,
                stats=stats,
                accepted_runtime_by_student_problem=runtime_fallbacks,
            )
            if runtime_ms is None:
                continue
            bucket.setdefault(problem_code, []).append(int(runtime_ms))
    medians: dict[str, float] = {}
    for problem_code, runtimes in bucket.items():
        if runtimes:
            medians[problem_code] = float(np.median(runtimes))
    return medians


def _normalize_kind(value: Any) -> str:
    return clean_text(value)


def _resolve_problem_kind(
    problem_code: str,
    stats: dict[str, Any],
    problem_kind_by_code: dict[str, str],
) -> str | None:
    kind = _normalize_kind(stats.get("problem_kind"))
    if not kind:
        kind = _normalize_kind(problem_kind_by_code.get(problem_code))
    return kind or None


def _timeline_problem_codes_by_student(
    timeline_by_student: dict[int, list[dict[str, Any]]] | None,
) -> dict[int, set[str]]:
    if not timeline_by_student:
        return {}
    result: dict[int, set[str]] = {}
    for student_id, events in timeline_by_student.items():
        codes: set[str] = set()
        for event in events:
            code = clean_text(event.get("problem_code"))
            if code:
                codes.add(code)
        if codes:
            result[int(student_id)] = codes
    return result


def _infer_slot_count_by_kind(
    participants: list[ParticipantRow],
    included_problem_kinds: set[str],
    problem_kind_by_code: dict[str, str],
    random_exam_slots_by_kind: dict[str, int],
) -> tuple[dict[str, int], dict[str, str]]:
    slots: dict[str, int] = {}
    source_by_kind: dict[str, str] = {}

    for kind in included_problem_kinds:
        value = int(random_exam_slots_by_kind.get(kind, 0))
        if value > 0:
            slots[kind] = value
            source_by_kind[kind] = "html_pool_choose_k"

    max_solved_by_kind: dict[str, int] = {kind: 0 for kind in included_problem_kinds}
    for participant in participants:
        solved_counter: dict[str, int] = {kind: 0 for kind in included_problem_kinds}
        for problem_code, stats in participant.problem_stats.items():
            kind = _resolve_problem_kind(
                problem_code=problem_code,
                stats=stats,
                problem_kind_by_code=problem_kind_by_code,
            )
            if not kind or kind not in included_problem_kinds:
                continue
            if stats.get("solved"):
                solved_counter[kind] += 1
        for kind in included_problem_kinds:
            max_solved_by_kind[kind] = max(max_solved_by_kind[kind], solved_counter[kind])

    for kind in included_problem_kinds:
        if slots.get(kind, 0) > 0:
            continue
        fallback = max_solved_by_kind.get(kind, 0)
        if fallback > 0:
            slots[kind] = fallback
            source_by_kind[kind] = "max_passed_count"

    return slots, source_by_kind


def _random_exam_knowledge_signal(
    participant: ParticipantRow,
    included_problem_kinds: set[str],
    problem_kind_by_code: dict[str, str],
    slot_count_by_kind: dict[str, int],
    slot_source_by_kind: dict[str, str],
    submission_problem_codes: set[str],
) -> tuple[float | None, dict[str, Any]]:
    solved_count_by_kind: dict[str, int] = {kind: 0 for kind in included_problem_kinds}
    visible_codes_by_kind: dict[str, set[str]] = {
        kind: set() for kind in included_problem_kinds
    }

    for problem_code, stats in participant.problem_stats.items():
        kind = _resolve_problem_kind(
            problem_code=problem_code,
            stats=stats,
            problem_kind_by_code=problem_kind_by_code,
        )
        if not kind or kind not in included_problem_kinds:
            continue
        if stats.get("solved"):
            solved_count_by_kind[kind] += 1
        raw_text = clean_text(stats.get("raw"))
        if raw_text and raw_text != "-":
            visible_codes_by_kind[kind].add(problem_code)

    for problem_code in submission_problem_codes:
        kind = _normalize_kind(problem_kind_by_code.get(problem_code))
        if kind and kind in included_problem_kinds:
            visible_codes_by_kind[kind].add(problem_code)

    visible_count_by_kind = {
        kind: len(visible_codes_by_kind.get(kind, set())) for kind in included_problem_kinds
    }
    filled_unanswered_count_by_kind = {
        kind: max(0, int(slot_count_by_kind.get(kind, 0)) - visible_count_by_kind.get(kind, 0))
        for kind in included_problem_kinds
    }

    total_slot_count = sum(max(0, int(slot_count_by_kind.get(kind, 0))) for kind in included_problem_kinds)
    if total_slot_count <= 0:
        return None, {
            "random_exam_mode": True,
            "knowledge_mode": "fallback_default",
            "slot_count_by_kind": {
                kind: int(slot_count_by_kind.get(kind, 0)) for kind in included_problem_kinds
            },
            "slot_source_by_kind": {
                kind: slot_source_by_kind.get(kind, "unknown")
                for kind in included_problem_kinds
            },
        }

    solved_total = sum(solved_count_by_kind.values())
    confidence = "high"
    if any(value > 0 for value in filled_unanswered_count_by_kind.values()):
        confidence = "degraded_missing_drawn_set"
    if any(
        slot_source_by_kind.get(kind) == "max_passed_count"
        for kind in included_problem_kinds
    ):
        confidence = "degraded_missing_drawn_set"

    return (solved_total / total_slot_count), {
        "random_exam_mode": True,
        "knowledge_mode": "max_passed_fill_unanswered",
        "slot_count_by_kind": {
            kind: int(slot_count_by_kind.get(kind, 0)) for kind in included_problem_kinds
        },
        "slot_source_by_kind": {
            kind: slot_source_by_kind.get(kind, "unknown")
            for kind in included_problem_kinds
        },
        "solved_count_by_kind": solved_count_by_kind,
        "visible_count_by_kind": visible_count_by_kind,
        "filled_unanswered_count_by_kind": filled_unanswered_count_by_kind,
        "confidence": confidence,
    }


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
    included_problem_kinds: list[str] | None = None,
    random_exam_mode: bool = False,
    random_exam_slots_by_kind: dict[str, int] | None = None,
    random_exam_missing_drawn_set_policy: str = "max_passed_fill_unanswered",
    problem_kind_by_code: dict[str, str] | None = None,
) -> list[StudentMetricResult]:
    accepted_runtime_by_student_problem = _accepted_runtime_ms_by_student_problem(
        timeline_by_student
    )
    runtime_medians = _problem_runtime_medians(
        participants,
        accepted_runtime_by_student_problem=accepted_runtime_by_student_problem,
    )
    included_kind_set = {
        clean_text(item) for item in (included_problem_kinds or []) if clean_text(item)
    }
    normalized_problem_kind_by_code = {
        clean_text(code): clean_text(kind)
        for code, kind in (problem_kind_by_code or {}).items()
        if clean_text(code) and clean_text(kind)
    }
    timeline_problem_codes = _timeline_problem_codes_by_student(timeline_by_student)
    slot_count_by_kind: dict[str, int] = {}
    slot_source_by_kind: dict[str, str] = {}
    if (
        random_exam_mode
        and included_kind_set
        and random_exam_missing_drawn_set_policy == "max_passed_fill_unanswered"
    ):
        slot_count_by_kind, slot_source_by_kind = _infer_slot_count_by_kind(
            participants=participants,
            included_problem_kinds=included_kind_set,
            problem_kind_by_code=normalized_problem_kind_by_code,
            random_exam_slots_by_kind=random_exam_slots_by_kind or {},
        )

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
            raw_details[participant.student_id] = {
                "absent": True,
                "metric_scope": "function_programming_only"
                if included_kind_set
                else "all_problem_kinds",
                "random_exam_mode": random_exam_mode,
            }
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

        knowledge_signal = None
        knowledge_details: dict[str, Any] = {}
        if (
            random_exam_mode
            and included_kind_set
            and random_exam_missing_drawn_set_policy == "max_passed_fill_unanswered"
        ):
            knowledge_signal, knowledge_details = _random_exam_knowledge_signal(
                participant=participant,
                included_problem_kinds=included_kind_set,
                problem_kind_by_code=normalized_problem_kind_by_code,
                slot_count_by_kind=slot_count_by_kind,
                slot_source_by_kind=slot_source_by_kind,
                submission_problem_codes=timeline_problem_codes.get(participant.student_id, set()),
            )

        if knowledge_signal is None:
            problem_count = max(1, len(participant.problem_stats))
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
            runtime_ms = _resolved_quality_runtime_ms(
                student_id=participant.student_id,
                problem_code=problem_code,
                stats=stats,
                accepted_runtime_by_student_problem=accepted_runtime_by_student_problem,
            )
            if runtime_ms is None:
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
            "metric_scope": "function_programming_only"
            if included_kind_set
            else "all_problem_kinds",
            "random_exam_mode": random_exam_mode,
            **knowledge_details,
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
