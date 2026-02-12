from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Any

from ..config import RatingConfig
from ..models import ParticipantRow


@dataclass(slots=True)
class RatingResult:
    student_id: int
    old_rating: int
    delta: int
    new_rating: int
    rank: int
    seed: float
    performance: float
    details: dict[str, Any]


def _expected_rank(candidate_rating: float, ratings: list[int]) -> float:
    total = 1.0
    for rating in ratings:
        total += 1.0 / (1.0 + 10 ** ((candidate_rating - rating) / 400.0))
    return total


def _solve_performance(
    target_rank: float,
    opponent_ratings: list[int],
    cfg: RatingConfig,
) -> float:
    low = float(cfg.min_binary_search_rating)
    high = float(cfg.max_binary_search_rating)
    for _ in range(cfg.binary_search_steps):
        middle = (low + high) / 2.0
        seed = _expected_rank(middle, opponent_ratings)
        if seed < target_rank:
            high = middle
        else:
            low = middle
    return (low + high) / 2.0


def _competition_ranks(participants: list[tuple[int, float, int | None]]) -> dict[int, int]:
    ordered = sorted(participants, key=lambda item: (-item[1], item[2] if item[2] is not None else 10**9, item[0]))
    ranks: dict[int, int] = {}
    previous_score: float | None = None
    previous_time: int | None = None
    previous_rank = 0
    for index, (student_id, score, time_used) in enumerate(ordered, start=1):
        if previous_score is None:
            previous_rank = index
        elif previous_score != score or previous_time != time_used:
            previous_rank = index
        ranks[student_id] = previous_rank
        previous_score = score
        previous_time = time_used
    return ranks


def compute_exam_rating(
    participants: list[ParticipantRow],
    current_ratings: dict[int, int],
    cfg: RatingConfig,
) -> list[RatingResult]:
    rated = [item for item in participants if item.student_id is not None and not item.absent]
    if not rated:
        return []

    fallback_rank_inputs: list[tuple[int, float, int | None]] = []
    for participant in rated:
        score = participant.total_score if participant.total_score is not None else float(participant.solved_count or 0)
        fallback_rank_inputs.append((participant.student_id, float(score), participant.time_used_seconds))
    fallback_ranks = _competition_ranks(fallback_rank_inputs)

    competitors = []
    for participant in rated:
        student_id = participant.student_id
        if student_id is None:
            continue
        competitors.append(
            {
                "student_id": student_id,
                "rating": int(current_ratings.get(student_id, cfg.initial_rating)),
                "rank": int(participant.rank) if participant.rank is not None else fallback_ranks[student_id],
            }
        )

    deltas: dict[int, float] = {}
    seeds: dict[int, float] = {}
    performances: dict[int, float] = {}
    ratings = [item["rating"] for item in competitors]

    for entry in competitors:
        student_id = entry["student_id"]
        rating = entry["rating"]
        rank = entry["rank"]
        opponent_ratings = [value for idx, value in enumerate(ratings) if competitors[idx]["student_id"] != student_id]
        seed = _expected_rank(float(rating), opponent_ratings)
        target_rank = math.sqrt(seed * rank)
        performance = _solve_performance(target_rank, opponent_ratings, cfg=cfg)
        delta = (performance - rating) / 2.0
        deltas[student_id] = delta
        seeds[student_id] = seed
        performances[student_id] = performance

    total = len(competitors)
    inc_1 = (-1.0 - sum(deltas.values())) / total
    for student_id in deltas:
        deltas[student_id] += inc_1

    top_n = min(total, int(4 * math.sqrt(total)) or 1)
    sorted_by_rating = sorted(competitors, key=lambda item: item["rating"], reverse=True)
    top_sum = sum(deltas[item["student_id"]] for item in sorted_by_rating[:top_n])
    inc_2_raw = -top_sum / top_n
    inc_2 = min(max(inc_2_raw, -10.0), 0.0)
    for student_id in deltas:
        deltas[student_id] += inc_2

    results: list[RatingResult] = []
    for entry in competitors:
        student_id = entry["student_id"]
        old_rating = entry["rating"]
        delta_int = int(round(deltas[student_id]))
        new_rating = old_rating + delta_int
        results.append(
            RatingResult(
                student_id=student_id,
                old_rating=old_rating,
                delta=delta_int,
                new_rating=new_rating,
                rank=entry["rank"],
                seed=seeds[student_id],
                performance=performances[student_id],
                details={
                    "inc_1": inc_1,
                    "inc_2": inc_2,
                    "raw_delta": deltas[student_id],
                },
            )
        )
    return results
