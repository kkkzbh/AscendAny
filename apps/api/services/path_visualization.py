from __future__ import annotations

from datetime import datetime
from typing import Any

from psycopg.errors import UndefinedTable
from psycopg.rows import dict_row

from ..core.config import Settings
from .web_learning import WebLearningService

from services.recommendation.recommendation.knowledge import (
    KnowledgeTree,
    build_prerequisite_map,
    load_knowledge_tree,
)


class PathVisualizationService:
    """Read-only helpers for the desktop path/star-map UI.

    Reuses the same SQL surface as ``WebLearningService`` so stats stay
    consistent between the web mastery page and the desktop star map.
    """

    def __init__(self, repository: Any, settings: Settings) -> None:
        self._repository = repository
        self._settings = settings
        self._learning = WebLearningService(repository, settings)

    async def path_status(
        self, student_entity_id: int, points: list[str]
    ) -> list[dict[str, Any]]:
        if not points:
            return []
        try:
            rows = await self._fetch_all(
                """
                SELECT t.knowledge_point,
                       COUNT(DISTINCT attempts.problem_id) AS attempted,
                       COUNT(DISTINCT attempts.problem_id)
                           FILTER (WHERE attempts.is_correct) AS correct,
                       MAX(attempts.submitted_at) AS last_tried_at
                FROM ascendany.external_problem_tags t
                LEFT JOIN (
                    SELECT s.student_id AS student_entity_id,
                           epl.source_platform,
                           epl.external_problem_id,
                           epl.source_platform || ':' || epl.external_problem_id AS problem_id,
                           s.submitted_at AS submitted_at,
                           CASE
                               WHEN lower(COALESCE(s.verdict, '')) IN ('accepted', 'ac', '答案正确') THEN TRUE
                               WHEN ep.points IS NOT NULL AND ep.points > 0 AND s.score IS NOT NULL
                                   THEN s.score >= ep.points
                               ELSE FALSE
                           END AS is_correct
                    FROM ascendany.submissions s
                    JOIN ascendany.exam_problem_external_links epl
                      ON epl.exam_id = s.exam_id
                     AND epl.problem_set_problem_id = s.problem_code
                    LEFT JOIN ascendany.exam_problems ep
                      ON ep.exam_id = s.exam_id
                     AND ep.problem_code = s.problem_code
                    WHERE s.student_id = %s
                ) AS attempts
                  ON attempts.source_platform = t.source_platform
                 AND attempts.external_problem_id = t.external_problem_id
                WHERE t.knowledge_point = ANY(%s::text[])
                GROUP BY t.knowledge_point
                """,
                (student_entity_id, points),
            )
        except UndefinedTable as exc:
            if not _is_external_schema_missing(exc):
                raise
            rows = []
        stats_by_point = {str(row["knowledge_point"]): row for row in rows}

        mastery_by_point = await self._mastery_by_point(student_entity_id)

        items: list[dict[str, Any]] = []
        for point in points:
            row = stats_by_point.get(point)
            attempted = int(row.get("attempted") or 0) if row else 0
            correct = int(row.get("correct") or 0) if row else 0
            last_at = row.get("last_tried_at") if row else None
            items.append(
                {
                    "point": point,
                    "mastery": round(
                        float(mastery_by_point.get(point, _ratio(correct, attempted))),
                        4,
                    ),
                    "attempted": attempted,
                    "correct": correct,
                    "lastTriedAt": last_at if isinstance(last_at, datetime) else None,
                }
            )
        return items

    async def node_detail(
        self,
        student_entity_id: int | None,
        point: str,
        top_k: int = 5,
    ) -> dict[str, Any]:
        tree = KnowledgeTree()
        prereq_map = build_prerequisite_map()
        successors_map: dict[str, list[str]] = {}
        for target, prereqs in prereq_map.items():
            for prereq in prereqs:
                successors_map.setdefault(prereq, []).append(target)

        parents = tree.ancestors(point)
        children = tree.children(point)
        prerequisites = sorted(prereq_map.get(point, set()))
        successors = sorted(set(successors_map.get(point, [])))

        stats = await self._point_stats_with_series(student_entity_id, point)
        mastery_by_point = (
            await self._mastery_by_point(student_entity_id)
            if student_entity_id is not None
            else {}
        )
        mastery = float(
            mastery_by_point.get(
                point, _ratio(stats["correct"], stats["attempted"])
            )
        )

        recs = await self._learning._recommendations(  # noqa: SLF001
            student_entity_id, top_k=top_k, knowledge_point=point
        )
        problems = [
            {
                "problemId": str(rec.get("problem_id") or ""),
                "title": rec.get("title"),
                "difficulty": rec.get("difficulty"),
                "knowledgePoints": rec.get("knowledge_points") or [],
                "score": rec.get("score"),
                "reason": rec.get("reason"),
            }
            for rec in recs
            if rec.get("problem_id")
        ]

        description = _describe_point(tree, point, parents, prerequisites, successors)
        return {
            "point": point,
            "level": tree.level(point),
            "parents": parents,
            "children": children,
            "prerequisites": prerequisites,
            "successors": successors,
            "description": description,
            "mastery": round(mastery, 4),
            "stats": stats,
            "problems": problems,
        }

    async def _mastery_by_point(self, student_entity_id: int | None) -> dict[str, float]:
        if student_entity_id is None:
            return {}
        try:
            planner = await self._learning._build_path_planner(  # noqa: SLF001
                student_entity_id, attempt_penalty_alpha=0.15
            )
            estimate = planner.estimate_mastery(student_entity_id)
        except Exception:
            return {}
        return {str(k): float(v) for k, v in (estimate.mastery or {}).items()}

    async def _point_stats_with_series(
        self, student_entity_id: int | None, point: str
    ) -> dict[str, Any]:
        if student_entity_id is None or not point:
            return {
                "attempted": 0,
                "correct": 0,
                "accuracy": 0.0,
                "lastTriedAt": None,
                "recentSeries": [],
            }
        try:
            agg = await self._fetch_one(
                """
                SELECT COUNT(DISTINCT attempts.problem_id) AS attempted,
                       COUNT(DISTINCT attempts.problem_id)
                           FILTER (WHERE attempts.is_correct) AS correct,
                       MAX(attempts.submitted_at) AS last_tried_at
                FROM (
                    SELECT s.student_id AS student_entity_id,
                           epl.source_platform || ':' || epl.external_problem_id AS problem_id,
                           s.submitted_at AS submitted_at,
                           CASE
                               WHEN lower(COALESCE(s.verdict, '')) IN ('accepted', 'ac', '答案正确') THEN TRUE
                               WHEN ep.points IS NOT NULL AND ep.points > 0 AND s.score IS NOT NULL
                                   THEN s.score >= ep.points
                               ELSE FALSE
                           END AS is_correct
                    FROM ascendany.submissions s
                    JOIN ascendany.exam_problem_external_links epl
                      ON epl.exam_id = s.exam_id
                     AND epl.problem_set_problem_id = s.problem_code
                    LEFT JOIN ascendany.exam_problems ep
                      ON ep.exam_id = s.exam_id
                     AND ep.problem_code = s.problem_code
                    WHERE s.student_id = %s
                ) AS attempts
                JOIN ascendany.external_problem_tags t
                  ON t.source_platform || ':' || t.external_problem_id = attempts.problem_id
                WHERE t.knowledge_point = %s
                """,
                (student_entity_id, point),
            )
        except UndefinedTable as exc:
            if not _is_external_schema_missing(exc):
                raise
            return _empty_point_stats()
        attempted = int((agg or {}).get("attempted") or 0)
        correct = int((agg or {}).get("correct") or 0)
        last_at = (agg or {}).get("last_tried_at")

        try:
            series_rows = await self._fetch_all(
                """
                SELECT to_char(date_trunc('day', attempts.submitted_at), 'YYYY-MM-DD') AS day,
                       COUNT(*) AS attempted,
                       COUNT(*) FILTER (WHERE attempts.is_correct) AS correct
                FROM (
                    SELECT s.submitted_at,
                           epl.source_platform || ':' || epl.external_problem_id AS problem_id,
                           CASE
                               WHEN lower(COALESCE(s.verdict, '')) IN ('accepted', 'ac', '答案正确') THEN TRUE
                               WHEN ep.points IS NOT NULL AND ep.points > 0 AND s.score IS NOT NULL
                                   THEN s.score >= ep.points
                               ELSE FALSE
                           END AS is_correct
                    FROM ascendany.submissions s
                    JOIN ascendany.exam_problem_external_links epl
                      ON epl.exam_id = s.exam_id
                     AND epl.problem_set_problem_id = s.problem_code
                    LEFT JOIN ascendany.exam_problems ep
                      ON ep.exam_id = s.exam_id
                     AND ep.problem_code = s.problem_code
                    WHERE s.student_id = %s
                      AND s.submitted_at >= now() - interval '7 days'
                ) AS attempts
                JOIN ascendany.external_problem_tags t
                  ON t.source_platform || ':' || t.external_problem_id = attempts.problem_id
                WHERE t.knowledge_point = %s
                GROUP BY day
                ORDER BY day
                """,
                (student_entity_id, point),
            )
        except UndefinedTable as exc:
            if not _is_external_schema_missing(exc):
                raise
            series_rows = []
        recent = [
            {
                "date": str(row.get("day") or ""),
                "attempted": int(row.get("attempted") or 0),
                "correct": int(row.get("correct") or 0),
            }
            for row in series_rows
        ]
        return {
            "attempted": attempted,
            "correct": correct,
            "accuracy": round(_ratio(correct, attempted), 4),
            "lastTriedAt": last_at if isinstance(last_at, datetime) else None,
            "recentSeries": recent,
        }

    async def _fetch_one(self, query: str, params: tuple[Any, ...]) -> dict[str, Any] | None:
        async with self._repository._pool.connection() as conn:  # noqa: SLF001
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                row = await cursor.fetchone()
        return dict(row) if row else None

    async def _fetch_all(self, query: str, params: tuple[Any, ...]) -> list[dict[str, Any]]:
        async with self._repository._pool.connection() as conn:  # noqa: SLF001
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                rows = await cursor.fetchall()
        return [dict(row) for row in rows]


def _ratio(numerator: int, denominator: int) -> float:
    if not denominator:
        return 0.0
    return max(0.0, min(1.0, float(numerator) / float(denominator)))


def _empty_point_stats() -> dict[str, Any]:
    return {
        "attempted": 0,
        "correct": 0,
        "accuracy": 0.0,
        "lastTriedAt": None,
        "recentSeries": [],
    }


def _is_external_schema_missing(exc: UndefinedTable) -> bool:
    table_name = getattr(getattr(exc, "diag", None), "table_name", None)
    if table_name in {
        "external_problem_tags",
        "exam_problem_external_links",
        "external_problems",
    }:
        return True
    message = str(exc)
    return any(
        name in message
        for name in (
            "external_problem_tags",
            "exam_problem_external_links",
            "external_problems",
        )
    )


def _describe_point(
    tree: KnowledgeTree,
    point: str,
    parents: list[str],
    prerequisites: list[str],
    successors: list[str],
) -> str | None:
    del tree, parents, prerequisites, successors

    payload = load_knowledge_tree()
    descriptions = payload.get("descriptions") if isinstance(payload, dict) else None
    if isinstance(descriptions, dict):
        text = descriptions.get(point)
        if isinstance(text, str) and text.strip():
            return text.strip()
    text = _raw_knowledge_descriptions().get(point)
    if isinstance(text, str) and text.strip():
        return text.strip()
    return None


def _raw_knowledge_descriptions() -> dict[str, Any]:
    payload = load_knowledge_tree()
    descriptions = payload.get("descriptions") if isinstance(payload, dict) else None
    return descriptions if isinstance(descriptions, dict) else {}
