from __future__ import annotations

import hashlib
import math
import re
from dataclasses import dataclass
from typing import Sequence

import numpy as np

from .db import BankProblem, StudentRecord, SubmissionRecord

TOKEN_RE = re.compile(r"[\w\u4e00-\u9fff]+")


@dataclass(frozen=True)
class BankRecommendationConfig:
    weight_knowledge: float = 0.45
    weight_text: float = 0.25
    weight_difficulty: float = 0.2
    weight_popularity: float = 0.1
    weight_gnn: float = 1.0
    unknown_mastery: float = 0.5
    unknown_knowledge_gap: float = 0.4
    difficulty_delta: float = 0.05
    difficulty_sigma: float = 0.25
    time_decay_half_life_days: int = 30
    attempt_penalty_alpha: float = 0.15
    dedup_similarity_threshold: float = 0.92
    mmr_lambda: float = 0.6
    min_profile_problems: int = 2
    target_knowledge_boost: float = 0.1
    default_target_difficulty: float = 0.5
    mastery_threshold: float = 0.8

    @classmethod
    def from_dict(cls, payload: dict[str, object] | None) -> "BankRecommendationConfig":
        payload = payload or {}
        weights = payload.get("weights", {})
        params = payload.get("params", {})
        weights = weights if isinstance(weights, dict) else {}
        params = params if isinstance(params, dict) else {}
        defaults = cls()

        def weight(name: str, flat_name: str, default: float) -> float:
            return float(weights.get(name, payload.get(flat_name, default)))

        def param(name: str, flat_name: str, default: float | int) -> float | int:
            return params.get(name, payload.get(flat_name, default))

        return cls(
            weight_knowledge=weight(
                "knowledge_gap", "target_knowledge_weight", defaults.weight_knowledge
            ),
            weight_text=weight("text_similarity", "similarity_weight", defaults.weight_text),
            weight_difficulty=weight(
                "difficulty_fit", "difficulty_weight", defaults.weight_difficulty
            ),
            weight_popularity=weight(
                "popularity",
                "popularity_weight",
                defaults.weight_popularity,
            ),
            weight_gnn=weight("gnn", "model_score_weight", defaults.weight_gnn),
            unknown_mastery=float(
                param("unknown_mastery", "unknown_mastery", defaults.unknown_mastery)
            ),
            unknown_knowledge_gap=float(
                param(
                    "unknown_knowledge_gap",
                    "unknown_knowledge_gap",
                    defaults.unknown_knowledge_gap,
                )
            ),
            difficulty_delta=float(
                param("difficulty_delta", "difficulty_delta", defaults.difficulty_delta)
            ),
            difficulty_sigma=float(
                param("difficulty_sigma", "difficulty_tolerance", defaults.difficulty_sigma)
            ),
            time_decay_half_life_days=int(
                param(
                    "time_decay_half_life_days",
                    "time_decay_half_life_days",
                    defaults.time_decay_half_life_days,
                )
            ),
            attempt_penalty_alpha=float(
                param(
                    "attempt_penalty_alpha",
                    "attempt_penalty_alpha",
                    defaults.attempt_penalty_alpha,
                )
            ),
            dedup_similarity_threshold=float(
                param(
                    "dedup_similarity_threshold",
                    "dedup_similarity_threshold",
                    defaults.dedup_similarity_threshold,
                )
            ),
            mmr_lambda=float(param("mmr_lambda", "mmr_lambda", defaults.mmr_lambda)),
            min_profile_problems=int(
                param(
                    "min_profile_problems",
                    "min_profile_problems",
                    defaults.min_profile_problems,
                )
            ),
            target_knowledge_boost=float(
                param(
                    "target_knowledge_boost",
                    "target_knowledge_boost",
                    defaults.target_knowledge_boost,
                )
            ),
            default_target_difficulty=float(
                param(
                    "default_target_difficulty",
                    "default_target_difficulty",
                    defaults.default_target_difficulty,
                )
            ),
            mastery_threshold=float(
                param("mastery_threshold", "mastery_threshold", defaults.mastery_threshold)
            ),
        )


@dataclass(frozen=True)
class StudentProfile:
    student_id: int
    knowledge_mastery: dict[str, float]
    knowledge_evidence: dict[str, int]
    interest_vector: np.ndarray
    target_difficulty: float
    solved_problem_ids: set[str]
    solved_embeddings: np.ndarray | None
    practiced_count: int


@dataclass(frozen=True)
class CandidateScore:
    problem: BankProblem
    score: float
    gnn_score: float
    knowledge_gap: float
    text_similarity: float
    difficulty_fit: float
    popularity: float


def build_profiles(
    students: Sequence[StudentRecord],
    submissions: Sequence[SubmissionRecord],
    practice_problem_tags: dict[str, list[str]],
    *,
    config: BankRecommendationConfig | None = None,
) -> dict[int, StudentProfile]:
    config = config or BankRecommendationConfig()
    by_student: dict[int, list[SubmissionRecord]] = {}
    for row in submissions:
        by_student.setdefault(row.student_id, []).append(row)
    practice_difficulty = _practice_difficulty(submissions)
    embeddings = {
        row.practice_problem_id: _text_embedding(
            f"{row.practice_problem_id} {row.problem_title or ''}"
        )
        for row in submissions
    }
    profiles: dict[int, StudentProfile] = {}
    for student in students:
        rows = by_student.get(student.student_id, [])
        grouped: dict[str, list[SubmissionRecord]] = {}
        for row in rows:
            grouped.setdefault(row.practice_problem_id, []).append(row)

        mastery_sum: dict[str, float] = {}
        evidence: dict[str, int] = {}
        interest_vectors: list[np.ndarray] = []
        interest_weights: list[float] = []
        solved_ids: set[str] = set()
        solved_embeddings: list[np.ndarray] = []
        difficulty_values: list[float] = []
        difficulty_weights: list[float] = []

        for problem_id, attempts in grouped.items():
            best = max(attempts, key=lambda item: item.score_rate)
            score_adj = best.score_rate * math.exp(
                -config.attempt_penalty_alpha * max(len(attempts) - 1, 0)
            )
            if best.score_rate > 0:
                solved_ids.add(problem_id)
            for tag in practice_problem_tags.get(problem_id, []):
                mastery_sum[tag] = mastery_sum.get(tag, 0.0) + score_adj
                evidence[tag] = evidence.get(tag, 0) + 1
            embedding = embeddings.get(problem_id)
            if embedding is not None and score_adj > 0:
                interest_vectors.append(embedding * score_adj)
                interest_weights.append(score_adj)
                if best.score_rate > 0:
                    solved_embeddings.append(embedding)
            difficulty = practice_difficulty.get(problem_id)
            if difficulty is not None and best.score_rate >= config.mastery_threshold:
                difficulty_values.append(difficulty)
                difficulty_weights.append(best.score_rate)

        mastery = {
            tag: mastery_sum[tag] / evidence[tag]
            for tag in mastery_sum
            if evidence.get(tag, 0) > 0
        }
        interest = _weighted_interest(interest_vectors, interest_weights)
        target_difficulty = config.default_target_difficulty
        if difficulty_values:
            weights = np.asarray(difficulty_weights, dtype=np.float32)
            if float(weights.sum()) > 0:
                target_difficulty = float(np.average(difficulty_values, weights=weights))
            else:
                target_difficulty = float(np.mean(difficulty_values))
            target_difficulty = float(
                np.clip(target_difficulty + config.difficulty_delta, 0.0, 1.0)
            )
        profiles[student.student_id] = StudentProfile(
            student_id=student.student_id,
            knowledge_mastery=mastery,
            knowledge_evidence=evidence,
            interest_vector=interest,
            target_difficulty=target_difficulty,
            solved_problem_ids=solved_ids,
            solved_embeddings=np.vstack(solved_embeddings)
            if solved_embeddings
            else None,
            practiced_count=len(grouped),
        )
    return profiles


def score_recommendations(
    profiles: dict[int, StudentProfile],
    bank_problems: list[BankProblem],
    *,
    model_scores: dict[int, dict[str, float]],
    top_k: int = 20,
    config: BankRecommendationConfig | None = None,
) -> dict[int, list[dict[str, object]]]:
    if model_scores is None:
        raise ValueError("R-GCN/HAN model scores are required for recommendations")
    config = config or BankRecommendationConfig()
    result: dict[int, list[dict[str, object]]] = {}
    for student_id, profile in profiles.items():
        scored = _score_candidates(
            profile,
            bank_problems,
            model_scores=model_scores.get(student_id, {}),
            config=config,
            top_k=top_k,
        )
        result[student_id] = [
            {
                "problemId": item.problem.problem_id,
                "title": item.problem.title or item.problem.problem_id,
                "url": item.problem.link,
                "knowledgePoints": item.problem.tags,
                "difficulty": _difficulty(item.problem),
                "score": round(float(item.score), 4),
                "reason": _reason(profile, item),
                "rank": rank,
                "meta": {
                    "source": "recommendation_problem_bank",
                    "model": "rgcn_han",
                    "gnnScore": round(item.gnn_score, 4),
                    "knowledgeGap": round(item.knowledge_gap, 4),
                    "textSimilarity": round(item.text_similarity, 4),
                    "difficultyFit": round(item.difficulty_fit, 4),
                    "popularity": round(item.popularity, 4),
                },
            }
            for rank, item in enumerate(scored, start=1)
        ]
    return result


def _score_candidates(
    profile: StudentProfile,
    bank_problems: list[BankProblem],
    *,
    model_scores: dict[str, float],
    config: BankRecommendationConfig,
    top_k: int,
) -> list[CandidateScore]:
    candidates = [
        problem
        for problem in bank_problems
        if problem.active
        and problem.problem_id not in profile.solved_problem_ids
        and problem.problem_id in model_scores
    ]
    if not candidates:
        return []
    dedup_ids = _similar_to_solved(profile, candidates, config.dedup_similarity_threshold)
    remaining = [problem for problem in candidates if problem.problem_id not in dedup_ids]
    if len(remaining) >= max(top_k, 1):
        candidates = remaining

    knowledge_gap = np.asarray(
        [_knowledge_gap(profile, problem, config) for problem in candidates],
        dtype=np.float32,
    )
    text_similarity = np.asarray(
        [_text_similarity(profile, problem) for problem in candidates],
        dtype=np.float32,
    )
    difficulty_fit = np.asarray(
        [_difficulty_fit(profile, problem, config) for problem in candidates],
        dtype=np.float32,
    )
    popularity = np.asarray(
        [math.log1p(max(0.0, problem.submission_count)) for problem in candidates],
        dtype=np.float32,
    )
    gnn = np.asarray(
        [float(model_scores.get(problem.problem_id, 0.0)) for problem in candidates],
        dtype=np.float32,
    )

    weights = {
        "knowledge": 0.0
        if profile.practiced_count < config.min_profile_problems
        else config.weight_knowledge,
        "text": config.weight_text,
        "difficulty": config.weight_difficulty,
        "popularity": config.weight_popularity,
        "gnn": config.weight_gnn,
    }
    components = {
        "knowledge": _minmax(knowledge_gap),
        "text": _minmax(text_similarity),
        "difficulty": _minmax(difficulty_fit),
        "popularity": _minmax(popularity),
        "gnn": _minmax(gnn),
    }
    active = {
        key: value
        for key, value in weights.items()
        if value > 0 and bool(np.any(components[key] > 0))
    }
    if not active:
        combined = _minmax(gnn)
    else:
        total = float(sum(active.values()))
        combined = np.zeros(len(candidates), dtype=np.float32)
        for key, weight in active.items():
            combined += (weight / total) * components[key]
    selected = _mmr_rerank(
        candidates,
        combined,
        diversity_lambda=config.mmr_lambda,
        top_k=top_k,
    )
    return [
        CandidateScore(
            problem=candidates[idx],
            score=float(combined[idx]),
            gnn_score=float(gnn[idx]),
            knowledge_gap=float(knowledge_gap[idx]),
            text_similarity=float(text_similarity[idx]),
            difficulty_fit=float(difficulty_fit[idx]),
            popularity=float(popularity[idx]),
        )
        for idx in selected
    ]


def _practice_difficulty(submissions: Sequence[SubmissionRecord]) -> dict[str, float]:
    scores: dict[str, list[float]] = {}
    for row in submissions:
        scores.setdefault(row.practice_problem_id, []).append(row.score_rate)
    return {
        problem_id: 1.0 - (sum(values) / len(values))
        for problem_id, values in scores.items()
        if values
    }


def _difficulty(problem: BankProblem) -> float:
    total = max(problem.submission_count, 1.0)
    return round(1.0 - max(0.0, min(1.0, problem.pass_count / total)), 4)


def _knowledge_gap(
    profile: StudentProfile,
    problem: BankProblem,
    config: BankRecommendationConfig,
) -> float:
    if not problem.tags:
        return config.unknown_knowledge_gap
    values = [
        1.0 - float(profile.knowledge_mastery.get(tag, config.unknown_mastery))
        for tag in problem.tags
    ]
    return float(np.clip(np.mean(values), 0.0, 1.0))


def _difficulty_fit(
    profile: StudentProfile,
    problem: BankProblem,
    config: BankRecommendationConfig,
) -> float:
    diff = abs(_difficulty(problem) - profile.target_difficulty)
    return float(np.exp(-diff / max(config.difficulty_sigma, 1e-6)))


def _text_similarity(profile: StudentProfile, problem: BankProblem) -> float:
    if profile.interest_vector.size == 0:
        return 0.0
    problem_vec = _text_embedding(
        " ".join([problem.problem_id, problem.title or "", problem.description or ""])
    )
    return _cosine(profile.interest_vector, problem_vec)


def _similar_to_solved(
    profile: StudentProfile,
    problems: list[BankProblem],
    threshold: float,
) -> set[str]:
    if threshold <= 0 or profile.solved_embeddings is None:
        return set()
    excluded: set[str] = set()
    for problem in problems:
        vec = _text_embedding(
            " ".join([problem.problem_id, problem.title or "", problem.description or ""])
        )
        sims = [_cosine(vec, solved) for solved in profile.solved_embeddings]
        if sims and max(sims) >= threshold:
            excluded.add(problem.problem_id)
    return excluded


def _reason(profile: StudentProfile, item: CandidateScore) -> str:
    if item.problem.tags:
        weakest = min(
            item.problem.tags,
            key=lambda tag: profile.knowledge_mastery.get(tag, 0.5),
        )
        gap = 1.0 - profile.knowledge_mastery.get(weakest, 0.5)
        if gap >= 0.6:
            reason = f"focus on weak knowledge '{weakest}'"
        elif gap >= 0.3:
            reason = f"reinforce knowledge '{weakest}'"
        else:
            reason = f"extend knowledge '{weakest}'"
    else:
        reason = "match current level"
    if item.text_similarity >= 0.6:
        reason += "; similar to recent practice"
    return reason


def _text_embedding(text: str, dim: int = 64) -> np.ndarray:
    vec = np.zeros(dim, dtype=np.float32)
    for token in TOKEN_RE.findall(text.lower()):
        digest = hashlib.md5(token.encode("utf-8")).digest()
        idx = int.from_bytes(digest[:4], "little") % dim
        sign = 1.0 if digest[4] % 2 == 0 else -1.0
        vec[idx] += sign
    norm = float(np.linalg.norm(vec))
    if norm > 0:
        vec = vec / norm
    return vec


def _weighted_interest(vectors: list[np.ndarray], weights: list[float]) -> np.ndarray:
    if not vectors or not weights:
        return np.zeros(64, dtype=np.float32)
    stacked = np.vstack(vectors)
    weight_arr = np.asarray(weights, dtype=np.float32).reshape(-1, 1)
    total = float(weight_arr.sum())
    if total <= 0:
        return np.zeros(stacked.shape[1], dtype=np.float32)
    vec = (stacked * weight_arr).sum(axis=0) / total
    norm = float(np.linalg.norm(vec))
    if norm > 0:
        vec = vec / norm
    return vec.astype(np.float32)


def _minmax(values: np.ndarray) -> np.ndarray:
    if values.size == 0:
        return values
    min_value = float(values.min())
    max_value = float(values.max())
    if max_value - min_value <= 1e-8:
        return np.zeros_like(values)
    return (values - min_value) / (max_value - min_value)


def _mmr_rerank(
    candidates: list[BankProblem],
    scores: np.ndarray,
    *,
    diversity_lambda: float,
    top_k: int,
) -> list[int]:
    top_k = max(int(top_k), 1)
    if len(candidates) <= top_k:
        return [int(i) for i in np.argsort(-scores)]
    selected: list[int] = []
    remaining = set(range(len(candidates)))
    while remaining and len(selected) < top_k:
        if not selected:
            best = int(np.argmax(scores))
            selected.append(best)
            remaining.remove(best)
            continue
        best_idx = None
        best_score = -1e9
        for idx in list(remaining):
            sim = _max_problem_similarity(candidates[idx], [candidates[i] for i in selected])
            mmr_score = diversity_lambda * scores[idx] - (1 - diversity_lambda) * sim
            if mmr_score > best_score:
                best_score = float(mmr_score)
                best_idx = idx
        if best_idx is None:
            break
        selected.append(best_idx)
        remaining.remove(best_idx)
    return selected


def _max_problem_similarity(problem: BankProblem, selected: list[BankProblem]) -> float:
    tags = set(problem.tags)
    if not tags:
        return 0.0
    max_sim = 0.0
    for item in selected:
        other = set(item.tags)
        if not other:
            continue
        union = tags | other
        sim = len(tags & other) / len(union) if union else 0.0
        max_sim = max(max_sim, sim)
    return float(max_sim)


def _cosine(left: np.ndarray, right: np.ndarray) -> float:
    denom = float(np.linalg.norm(left) * np.linalg.norm(right))
    if denom <= 0:
        return 0.0
    return float(np.dot(left, right) / denom)
