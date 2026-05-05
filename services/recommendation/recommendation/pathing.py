from __future__ import annotations

import heapq
from dataclasses import dataclass
import math

from .knowledge import build_prerequisite_map
from .scoring import BankRecommendationConfig, StudentProfile


@dataclass(frozen=True)
class PathPlan:
    student_id: int
    targets: list[str]
    path: list[str]
    mastery: dict[str, float] | None = None
    evidence: dict[str, int] | None = None


@dataclass(frozen=True)
class PracticeAttempt:
    student_id: int
    problem_id: str
    score_rate: float | None = None
    is_correct: bool | None = None


@dataclass(frozen=True)
class MasteryEstimate:
    mastery: dict[str, float]
    evidence: dict[str, int]
    attempted_problem_count: int


class PathPlanner:
    def __init__(
        self,
        attempts: list[PracticeAttempt],
        problem_tags: dict[str, list[str]],
        *,
        prereq_map: dict[str, set[str]] | None = None,
        attempt_penalty_alpha: float = 0.15,
    ) -> None:
        if attempt_penalty_alpha < 0:
            raise ValueError("attempt_penalty_alpha must be >= 0")
        self.attempts = attempts
        self.problem_tags = {
            str(problem_id): [str(tag) for tag in tags]
            for problem_id, tags in problem_tags.items()
        }
        self.prereq_map = prereq_map if prereq_map is not None else build_prerequisite_map()
        self.attempt_penalty_alpha = attempt_penalty_alpha

    def estimate_mastery(self, student_id: int) -> MasteryEstimate:
        by_problem: dict[str, list[PracticeAttempt]] = {}
        for attempt in self.attempts:
            if attempt.student_id != student_id:
                continue
            by_problem.setdefault(str(attempt.problem_id), []).append(attempt)

        mastery_sum: dict[str, float] = {}
        evidence: dict[str, int] = {}
        for problem_id, attempts in by_problem.items():
            tags = self.problem_tags.get(problem_id, [])
            if not tags:
                continue
            best_score = max(_score_rate(attempt) for attempt in attempts)
            penalty = math.exp(
                -self.attempt_penalty_alpha * max(len(attempts) - 1, 0)
            )
            adjusted = best_score * penalty
            for tag in tags:
                mastery_sum[tag] = mastery_sum.get(tag, 0.0) + adjusted
                evidence[tag] = evidence.get(tag, 0) + 1

        mastery = {
            tag: mastery_sum[tag] / evidence[tag]
            for tag in mastery_sum
            if evidence.get(tag, 0) > 0
        }
        return MasteryEstimate(
            mastery=mastery,
            evidence=evidence,
            attempted_problem_count=len(by_problem),
        )

    def plan(
        self,
        student_id: int,
        *,
        top_n_targets: int = 5,
        min_evidence: int = 1,
        include_mastered: bool = False,
        mastered_threshold: float = 0.8,
        max_path_len: int | None = None,
    ) -> PathPlan:
        if top_n_targets <= 0:
            raise ValueError("top_n_targets must be > 0")
        if min_evidence <= 0:
            raise ValueError("min_evidence must be > 0")
        if not 0 <= mastered_threshold <= 1:
            raise ValueError("mastered_threshold must be in [0, 1]")

        estimate = self.estimate_mastery(student_id)
        profile = StudentProfile(
            student_id=student_id,
            knowledge_mastery=estimate.mastery,
            knowledge_evidence=estimate.evidence,
            interest_vector=_empty_interest_vector(),
            target_difficulty=0.5,
            solved_problem_ids=set(),
            solved_embeddings=None,
            practiced_count=estimate.attempted_problem_count,
        )
        plan = plan_learning_path(
            profile,
            prereq_map=self.prereq_map,
            top_n_targets=top_n_targets,
            min_evidence=min_evidence,
            include_mastered=include_mastered,
            mastered_threshold=mastered_threshold,
            max_path_len=max_path_len,
        )
        return PathPlan(
            student_id=student_id,
            targets=plan.targets,
            path=plan.path,
            mastery=estimate.mastery,
            evidence=estimate.evidence,
        )


def build_learning_paths(
    profiles: dict[int, StudentProfile],
    *,
    config: BankRecommendationConfig | None = None,
    max_targets: int = 5,
    max_path_len: int | None = 8,
    min_evidence: int = 1,
    include_mastered: bool = False,
    mastered_threshold: float = 0.8,
) -> dict[int, dict[str, object]]:
    config = config or BankRecommendationConfig()
    prereq_map = build_prerequisite_map()
    payloads: dict[int, dict[str, object]] = {}
    for student_id, profile in profiles.items():
        plan = plan_learning_path(
            profile,
            prereq_map=prereq_map,
            top_n_targets=max_targets,
            min_evidence=min_evidence,
            include_mastered=include_mastered,
            mastered_threshold=mastered_threshold,
            max_path_len=max_path_len,
        )
        payloads[student_id] = {
            "targets": plan.targets,
            "path": plan.path,
            "explanations": {
                "mode": "prerequisite_topological",
                "basis": (
                    "best score_rate with attempt penalty, then prerequisite "
                    "closure and mastery-aware topological ordering"
                ),
                "practiced_count": profile.practiced_count,
                "attempt_penalty_alpha": config.attempt_penalty_alpha,
                "mastered_threshold": mastered_threshold,
            },
        }
    return payloads


def plan_learning_path(
    profile: StudentProfile,
    *,
    prereq_map: dict[str, set[str]],
    top_n_targets: int = 5,
    min_evidence: int = 1,
    include_mastered: bool = False,
    mastered_threshold: float = 0.8,
    max_path_len: int | None = None,
) -> PathPlan:
    targets = _select_weak_targets(
        profile,
        top_n=top_n_targets,
        min_evidence=min_evidence,
    )
    if not targets:
        return PathPlan(student_id=profile.student_id, targets=[], path=[])

    closure = _prerequisite_closure(targets, prereq_map)
    if not include_mastered:
        closure = {
            tag
            for tag in closure
            if tag in targets
            or tag not in profile.knowledge_mastery
            or profile.knowledge_mastery.get(tag, 0.0) < mastered_threshold
        }
    path = topological_sort(closure, prereq_map, profile.knowledge_mastery)
    if max_path_len is not None and max_path_len > 0:
        path = path[:max_path_len]
    return PathPlan(
        student_id=profile.student_id,
        targets=targets,
        path=path,
        mastery=profile.knowledge_mastery,
        evidence=profile.knowledge_evidence,
    )


def topological_sort(
    nodes: set[str],
    prereq_map: dict[str, set[str]],
    mastery: dict[str, float],
) -> list[str]:
    indegree = {node: 0 for node in nodes}
    children: dict[str, list[str]] = {node: [] for node in nodes}
    for target in nodes:
        for prereq in prereq_map.get(target, set()):
            if prereq not in nodes:
                continue
            indegree[target] += 1
            children.setdefault(prereq, []).append(target)

    heap: list[tuple[float, str]] = []
    for node, degree in indegree.items():
        if degree == 0:
            heapq.heappush(heap, (mastery.get(node, 0.0), node))

    ordered: list[str] = []
    while heap:
        _, node = heapq.heappop(heap)
        ordered.append(node)
        for child in children.get(node, []):
            indegree[child] -= 1
            if indegree[child] == 0:
                heapq.heappush(heap, (mastery.get(child, 0.0), child))

    if len(ordered) < len(nodes):
        remaining = sorted(nodes - set(ordered), key=lambda item: (mastery.get(item, 0.0), item))
        ordered.extend(remaining)
    return ordered


def _select_weak_targets(
    profile: StudentProfile,
    *,
    top_n: int,
    min_evidence: int,
) -> list[str]:
    candidates = [
        tag
        for tag, evidence in profile.knowledge_evidence.items()
        if evidence >= min_evidence
    ]
    candidates.sort(
        key=lambda tag: (
            profile.knowledge_mastery.get(tag, 0.0),
            -profile.knowledge_evidence.get(tag, 0),
            tag,
        )
    )
    return candidates[:top_n]


def _prerequisite_closure(
    targets: list[str],
    prereq_map: dict[str, set[str]],
) -> set[str]:
    closure: set[str] = set()

    def visit(tag: str) -> None:
        if tag in closure:
            return
        closure.add(tag)
        for prereq in prereq_map.get(tag, set()):
            visit(prereq)

    for target in targets:
        visit(target)
    return closure


def _score_rate(attempt: PracticeAttempt) -> float:
    if attempt.score_rate is not None:
        return max(0.0, min(1.0, float(attempt.score_rate)))
    return 1.0 if attempt.is_correct else 0.0


def _empty_interest_vector():
    try:
        import numpy as np

        return np.zeros(32, dtype=np.float32)
    except Exception:  # pragma: no cover
        return []  # type: ignore[return-value]
