from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .db import BankProblem, StudentRecord, SubmissionRecord
from .knowledge import build_parent_edges, build_prerequisite_edges, get_all_knowledge_points


@dataclass(frozen=True)
class TrainingGraph:
    nodes: list[dict[str, Any]]
    edges: list[dict[str, Any]]
    student_to_idx: dict[str, int]
    problem_to_idx: dict[str, int]
    bank_problem_ids: list[str]
    knowledge_to_idx: dict[str, int]

    @property
    def node_count(self) -> int:
        return len(self.nodes)

    @property
    def edge_count(self) -> int:
        return len(self.edges)


def build_training_graph(
    students: list[StudentRecord],
    submissions: list[SubmissionRecord],
    bank_problems: list[BankProblem],
    practice_problem_tags: dict[str, list[str]] | None = None,
) -> TrainingGraph:
    practice_problem_tags = practice_problem_tags or infer_practice_problem_tags(
        submissions, bank_problems
    )
    nodes: list[dict[str, Any]] = []
    edges: list[dict[str, Any]] = []
    seen_nodes: set[tuple[str, str]] = set()
    student_to_idx = {str(student.student_id): idx for idx, student in enumerate(students)}
    practice_ids = sorted({row.practice_problem_id for row in submissions})
    bank_ids = sorted({problem.problem_id for problem in bank_problems if problem.active})
    problem_ids = list(dict.fromkeys(practice_ids + bank_ids))
    problem_to_idx = {problem_id: idx for idx, problem_id in enumerate(problem_ids)}
    knowledge_points = list(
        dict.fromkeys(
            get_all_knowledge_points()
            + [
                tag
                for problem in bank_problems
                for tag in problem.tags
            ]
            + [
                tag
                for tags in practice_problem_tags.values()
                for tag in tags
            ]
        )
    )
    knowledge_to_idx = {knowledge: idx for idx, knowledge in enumerate(knowledge_points)}

    def add_node(kind: str, node_id: str, **attrs: Any) -> None:
        key = (kind, node_id)
        if key in seen_nodes:
            return
        seen_nodes.add(key)
        nodes.append({"kind": kind, "id": node_id, **attrs})

    for student in students:
        add_node(
            "student",
            str(student.student_id),
            student_no=student.student_no,
            rating=student.rating,
        )

    for submission in submissions:
        practice_id = submission.practice_problem_id
        add_node("problem", practice_id, title=submission.problem_title, source="practice")
        edges.append(
            {
                "type": "submitted",
                "src_kind": "student",
                "src": str(submission.student_id),
                "dst_kind": "problem",
                "dst": practice_id,
                "score": submission.score,
                "max_score": submission.max_score,
                "score_rate": submission.score_rate,
                "verdict": submission.verdict,
            }
        )
        for tag in practice_problem_tags.get(practice_id, []):
            add_node("knowledge", tag)
            edges.append(
                {
                    "type": "belongs_to",
                    "src_kind": "problem",
                    "src": practice_id,
                    "dst_kind": "knowledge",
                    "dst": tag,
                }
            )

    for problem in bank_problems:
        if not problem.active:
            continue
        add_node(
            "problem",
            problem.problem_id,
            title=problem.title,
            source="bank",
            submission_count=problem.submission_count,
            pass_count=problem.pass_count,
        )
        for tag in problem.tags:
            add_node("knowledge", tag)
            edges.append(
                {
                    "type": "belongs_to",
                    "src_kind": "problem",
                    "src": problem.problem_id,
                    "dst_kind": "knowledge",
                    "dst": tag,
                    "confidence": 1.0,
                }
            )

    for src, dst in build_parent_edges(knowledge_to_idx):
        edges.append(
            {
                "type": "parent",
                "src_kind": "knowledge",
                "src": knowledge_points[src],
                "dst_kind": "knowledge",
                "dst": knowledge_points[dst],
            }
        )
    for src, dst in build_prerequisite_edges(knowledge_to_idx):
        edges.append(
            {
                "type": "prerequisite",
                "src_kind": "knowledge",
                "src": knowledge_points[src],
                "dst_kind": "knowledge",
                "dst": knowledge_points[dst],
            }
        )

    return TrainingGraph(
        nodes=nodes,
        edges=edges,
        student_to_idx=student_to_idx,
        problem_to_idx=problem_to_idx,
        bank_problem_ids=bank_ids,
        knowledge_to_idx=knowledge_to_idx,
    )


def infer_practice_problem_tags(
    submissions: list[SubmissionRecord],
    bank_problems: list[BankProblem],
) -> dict[str, list[str]]:
    bank_tags = {problem.problem_id: problem.tags for problem in bank_problems}
    by_practice: dict[str, list[str]] = {}
    for row in submissions:
        tags = bank_tags.get(row.practice_problem_id, [])
        if tags:
            by_practice[row.practice_problem_id] = tags
    return by_practice
