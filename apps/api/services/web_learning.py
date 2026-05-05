from __future__ import annotations

from datetime import UTC, datetime
from typing import Any

from psycopg.rows import dict_row

from ..core.config import Settings
from .dashboard import DashboardService
from .identity import StudentIdentityService
from services.recommendation.recommendation.knowledge import (
    KnowledgeHierarchyAggregator,
    KnowledgeTree,
)
from services.recommendation.recommendation.pathing import PathPlanner, PracticeAttempt


class WebLearningService:
    def __init__(self, repository: Any, settings: Settings) -> None:
        self._repository = repository
        self._settings = settings

    async def metrics_student(self, payload: dict[str, Any]) -> dict[str, Any]:
        student = self._student_from_payload(payload)
        identity = await self._resolve_identity_or_none(student)
        if identity is None:
            return self._empty_metrics(student)
        dashboard = await DashboardService(
            repository=self._repository,
            default_rating=self._settings.dashboard.default_rating,
            default_metric=self._settings.dashboard.default_metric,
            rating_history_limit=self._settings.dashboard.rating_history_limit,
        ).build(identity)
        metrics = dashboard.metrics
        raw_values = {
            "knowledge_points": metrics.knowledge,
            "accuracy": metrics.accuracy,
            "quality": metrics.quality,
            "flexibility": metrics.flexibility,
            "proficiency": metrics.proficiency,
        }
        values = {key: self._score01(value) for key, value in raw_values.items()}
        overall = sum(values.values()) / max(1, len(values))
        strongest = max(values, key=lambda key: values[key])
        weakest = min(values, key=lambda key: values[key])
        return {
            "student": student,
            "student_id": student,
            "computed_at": datetime.now(UTC).isoformat(),
            "metrics": {
                key: self._metric_entry(score, raw_values.get(key))
                for key, score in values.items()
            },
            "summary": {
                "overall_score": overall,
                "rating": dashboard.rating.current,
                "strongest": strongest,
                "weakest": weakest,
            },
            "strongest": strongest,
            "weakest": weakest,
        }

    async def mastery(self, payload: dict[str, Any]) -> dict[str, Any]:
        student = self._student_from_payload(payload)
        identity = await self._resolve_identity_or_none(student)
        student_entity_id = identity.student_entity_id if identity is not None else None
        computed_at = datetime.now(UTC).isoformat()
        try:
            planner = await self._build_path_planner(
                student_entity_id,
                attempt_penalty_alpha=float(payload.get("attempt_penalty_alpha") or 0.15),
            )
            estimate = (
                planner.estimate_mastery(student_entity_id)
                if student_entity_id is not None
                else None
            )
        except Exception:
            planner = None
            estimate = None

        if estimate is not None:
            tree = KnowledgeTree()
            aggregator = KnowledgeHierarchyAggregator(
                tree,
                estimate.mastery,
                estimate.evidence,
            )
            weak_points = aggregator.identify_weak_points(
                threshold=float(payload.get("weak_point_threshold") or 0.6),
                top_k=max(1, min(int(payload.get("top_k") or 5), 50)),
            )
            mastery = aggregator.build_hierarchy()
            flat_mastery = {
                point: {
                    "mastery": round(score, 4),
                    "level": tree.level(point) or "knowledge",
                    "evidence": estimate.evidence.get(point, 0),
                }
                for point, score in sorted(estimate.mastery.items())
            }
            overall = (
                sum(float(v) for v in estimate.mastery.values())
                / max(1, len(estimate.mastery))
            )
        else:
            rows = await self._fetch_tag_stats(student_entity_id)
            flat_mastery = {}
            weak_points = []
            for row in rows:
                name = str(row["knowledge_point"])
                attempted = int(row.get("attempted") or 0)
                solved = int(row.get("solved") or 0)
                value = solved / attempted if attempted else 0.0
                flat_mastery[name] = {
                    "mastery": round(value, 4),
                    "level": "knowledge",
                    "evidence": attempted,
                }
                if value < float(payload.get("weak_point_threshold") or 0.6):
                    weak_points.append(name)
            mastery = flat_mastery
            overall = (
                sum(float(v["mastery"]) for v in flat_mastery.values())
                / max(1, len(flat_mastery))
            )
        recommendations = await self._recommendations(student_entity_id, top_k=6)
        return {
            "student": student,
            "student_id": student,
            "computed_at": computed_at,
            "knowledge_mastery": mastery,
            "flat_mastery": flat_mastery,
            "weak_points": weak_points,
            "recommendations": recommendations,
            "summary": {
                "overall_mastery": round(overall, 4),
                "evaluated_points": len(flat_mastery),
            },
        }

    async def path_plan(self, payload: dict[str, Any]) -> dict[str, Any]:
        student = self._student_from_payload(payload)
        identity = await self._resolve_identity_or_none(student)
        if identity is None:
            return {"student": student, "targets": [], "path": []}
        try:
            planner = await self._build_path_planner(
                identity.student_entity_id,
                attempt_penalty_alpha=float(payload.get("attempt_penalty_alpha") or 0.15),
            )
            plan = planner.plan(
                identity.student_entity_id,
                top_n_targets=max(
                    1,
                    min(
                        int(
                            payload.get("top_n_targets")
                            or payload.get("max_targets")
                            or payload.get("top_n")
                            or 5
                        ),
                        50,
                    ),
                ),
                min_evidence=max(1, int(payload.get("min_evidence") or 1)),
                include_mastered=self._bool(payload.get("include_mastered")),
                mastered_threshold=float(payload.get("mastered_threshold") or 0.8),
                max_path_len=self._optional_positive_int(payload.get("max_path_len")),
            )
            return {
                "student": student,
                "targets": plan.targets,
                "path": plan.path,
                "mastery": {
                    key: round(value, 4)
                    for key, value in (plan.mastery or {}).items()
                },
                "evidence": plan.evidence or {},
                "explanations": {
                    "mode": "prerequisite_topological",
                    "basis": (
                        "best score_rate with attempts penalty, weak target "
                        "selection, prerequisite closure and mastery-aware order"
                    ),
                },
            }
        except Exception:
            snapshot = await self._repository.fetch_latest_learning_path(
                list(identity.student_entity_ids or (identity.student_entity_id,))
            )
            if snapshot is None:
                return {"student": student, "targets": [], "path": []}
            return {"student": student, "targets": snapshot.targets, "path": snapshot.path}

    async def tag_detail(self, payload: dict[str, Any]) -> dict[str, Any]:
        student = self._student_from_payload(payload)
        knowledge_point = str(payload.get("knowledge_point") or "").strip()
        top_k = max(1, min(int(payload.get("top_k") or 6), 50))
        identity = await self._resolve_identity_or_none(student)
        student_entity_id = identity.student_entity_id if identity is not None else None
        stats = await self._fetch_one(
            """
            SELECT COUNT(DISTINCT r.problem_id) AS attempted,
                   COUNT(DISTINCT r.problem_id) FILTER (WHERE r.status = 'AC') AS solved
            FROM ascendany.oj_submit_records r
            JOIN ascendany.recommendation_problem_tags t ON t.problem_id = r.problem_id
            WHERE (%s::bigint IS NULL OR r.student_entity_id = %s)
              AND t.knowledge_point = %s
            """,
            (student_entity_id, student_entity_id, knowledge_point),
        )
        recs = await self._recommendations(
            student_entity_id,
            top_k=top_k,
            knowledge_point=knowledge_point,
        )
        return {
            "student": student,
            "knowledge_point": knowledge_point,
            "solved": int(stats.get("solved") or 0) if stats else 0,
            "attempted": int(stats.get("attempted") or 0) if stats else 0,
            "recommendations": recs,
        }

    async def _recommendations(
        self,
        student_entity_id: int | None,
        *,
        top_k: int,
        knowledge_point: str | None = None,
    ) -> list[dict[str, Any]]:
        if student_entity_id is not None:
            snapshot = await self._repository.fetch_latest_problem_recommendations(
                [student_entity_id], top_k=top_k * 2
            )
            if snapshot is not None:
                items: list[dict[str, Any]] = []
                for item in snapshot.items:
                    points = item.get("knowledgePoints") or item.get("knowledge_points") or []
                    if knowledge_point and knowledge_point not in points:
                        continue
                    pid = str(item.get("problemId") or item.get("problem_id") or "").strip()
                    if not pid:
                        continue
                    items.append(
                        {
                            "problem_id": pid,
                            "title": item.get("title") or pid,
                            "difficulty": item.get("difficulty") or 0,
                            "knowledge_points": points,
                            "score": item.get("score") or 0,
                            "reason": item.get("reason") or "",
                            "url": f"/oj/{pid}",
                        }
                    )
                    if len(items) >= top_k:
                        return items
        where = ["pb.active = TRUE"]
        params: list[Any] = []
        if knowledge_point:
            where.append(
                "EXISTS (SELECT 1 FROM ascendany.recommendation_problem_tags t WHERE t.problem_id = pb.problem_id AND t.knowledge_point = %s)"
            )
            params.append(knowledge_point)
        rows = await self._fetch_all(
            f"""
            SELECT pb.problem_id, pb.title,
                   COALESCE(jsonb_agg(t.knowledge_point ORDER BY t.knowledge_point)
                       FILTER (WHERE t.knowledge_point IS NOT NULL), '[]'::jsonb) AS tags
            FROM ascendany.recommendation_problem_bank pb
            LEFT JOIN ascendany.recommendation_problem_tags t ON t.problem_id = pb.problem_id
            WHERE {" AND ".join(where)}
            GROUP BY pb.problem_id
            ORDER BY pb.pass_count DESC, pb.problem_id
            LIMIT %s
            """,
            tuple(params + [top_k]),
        )
        return [
            {
                "problem_id": str(row["problem_id"]),
                "title": row.get("title") or str(row["problem_id"]),
                "difficulty": 0,
                "knowledge_points": row.get("tags") if isinstance(row.get("tags"), list) else [],
                "score": 0,
                "reason": "题库关联知识点推荐",
                "url": f"/oj/{row['problem_id']}",
            }
            for row in rows
        ]

    async def _fetch_tag_stats(self, student_entity_id: int | None) -> list[dict[str, Any]]:
        return await self._fetch_all(
            """
            SELECT t.knowledge_point,
                   COUNT(DISTINCT r.problem_id) AS attempted,
                   COUNT(DISTINCT r.problem_id) FILTER (WHERE r.status = 'AC') AS solved
            FROM ascendany.recommendation_problem_tags t
            LEFT JOIN ascendany.oj_submit_records r
              ON r.problem_id = t.problem_id
             AND (%s::bigint IS NULL OR r.student_entity_id = %s)
            GROUP BY t.knowledge_point
            ORDER BY t.knowledge_point
            """,
            (student_entity_id, student_entity_id),
        )

    async def _build_path_planner(
        self,
        student_entity_id: int | None,
        *,
        attempt_penalty_alpha: float,
    ) -> PathPlanner:
        if student_entity_id is None:
            return PathPlanner([], {}, attempt_penalty_alpha=attempt_penalty_alpha)
        attempts = [
            PracticeAttempt(
                student_id=int(row["student_entity_id"]),
                problem_id=str(row["problem_id"]),
                score_rate=float(row.get("score_rate") or 0.0),
                is_correct=bool(row.get("is_correct")),
            )
            for row in await self._fetch_attempt_rows(student_entity_id)
            if row.get("student_entity_id") is not None and row.get("problem_id")
        ]
        tags = await self._fetch_problem_tags()
        return PathPlanner(
            attempts,
            tags,
            attempt_penalty_alpha=attempt_penalty_alpha,
        )

    async def _fetch_attempt_rows(self, student_entity_id: int) -> list[dict[str, Any]]:
        return await self._fetch_all(
            """
            SELECT s.student_id AS student_entity_id,
                   COALESCE(
                       NULLIF(s.raw->>'global_problem_id', ''),
                       NULLIF(s.raw->>'problem_id', ''),
                       NULLIF(ep.meta->>'global_problem_id', ''),
                       NULLIF(ep.meta->>'problem_id', ''),
                       NULLIF(s.problem_code, ''),
                       ('exam:' || s.exam_id::text || ':' || COALESCE(s.problem_code, ''))
                   ) AS problem_id,
                   CASE
                       WHEN ep.points IS NOT NULL AND ep.points > 0 AND s.score IS NOT NULL
                           THEN LEAST(1.0, GREATEST(0.0, (s.score::numeric / ep.points::numeric)))::float
                       WHEN lower(COALESCE(s.verdict, '')) IN ('accepted', 'ac', '答案正确')
                           THEN 1.0
                       ELSE 0.0
                   END AS score_rate,
                   CASE
                       WHEN lower(COALESCE(s.verdict, '')) IN ('accepted', 'ac', '答案正确') THEN TRUE
                       WHEN ep.points IS NOT NULL AND ep.points > 0 AND s.score IS NOT NULL
                           THEN s.score >= ep.points
                       ELSE FALSE
                   END AS is_correct
            FROM ascendany.submissions s
            LEFT JOIN ascendany.exam_problems ep
              ON ep.exam_id = s.exam_id
             AND ep.problem_code = s.problem_code
            WHERE s.student_id = %s
              AND COALESCE(s.problem_code, '') <> ''
            UNION ALL
            SELECT r.student_entity_id,
                   r.problem_id,
                   LEAST(1.0, GREATEST(0.0, COALESCE(r.score_rate, 0)))::float AS score_rate,
                   COALESCE(r.is_correct, lower(COALESCE(r.status, '')) IN ('accepted', 'ac')) AS is_correct
            FROM ascendany.oj_submit_records r
            WHERE r.student_entity_id = %s
              AND COALESCE(r.problem_id, '') <> ''
            """,
            (student_entity_id, student_entity_id),
        )

    async def _fetch_problem_tags(self) -> dict[str, list[str]]:
        rows = await self._fetch_all(
            """
            SELECT problem_id, knowledge_point
            FROM ascendany.recommendation_problem_tags
            ORDER BY problem_id, knowledge_point
            """,
            (),
        )
        tags: dict[str, list[str]] = {}
        for row in rows:
            problem_id = str(row.get("problem_id") or "").strip()
            tag = str(row.get("knowledge_point") or "").strip()
            if not problem_id or not tag:
                continue
            tags.setdefault(problem_id, []).append(tag)
        return tags

    async def _resolve_identity_or_none(self, student: str):
        try:
            return await StudentIdentityService(self._repository).resolve(
                student_id=student,
                pta_nickname=student,
            )
        except Exception:
            return None

    @staticmethod
    def _student_from_payload(payload: dict[str, Any]) -> str:
        return str(
            payload.get("student_id")
            or payload.get("student")
            or payload.get("studentId")
            or ""
        ).strip()

    @staticmethod
    def _bool(value: Any) -> bool:
        if isinstance(value, bool):
            return value
        if value is None:
            return False
        return str(value).strip().lower() in {"1", "true", "yes", "on"}

    @staticmethod
    def _optional_positive_int(value: Any) -> int | None:
        if value in (None, ""):
            return None
        parsed = int(value)
        return parsed if parsed > 0 else None

    @staticmethod
    def _empty_metrics(student: str) -> dict[str, Any]:
        keys = (
            "knowledge_points",
            "proficiency",
            "accuracy",
            "flexibility",
            "quality",
        )
        return {
            "student": student,
            "student_id": student,
            "computed_at": "",
            "metrics": {
                key: WebLearningService._metric_entry(0.0, None, missing=True)
                for key in keys
            },
            "summary": {
                "overall_score": 0.0,
                "rating": 800,
                "strongest": None,
                "weakest": None,
            },
            "strongest": None,
            "weakest": None,
        }

    @staticmethod
    def _score01(value: Any) -> float:
        try:
            score = float(value or 0)
        except (TypeError, ValueError):
            score = 0.0
        if score > 1.0:
            score = score / 100.0
        return round(max(0.0, min(1.0, score)), 4)

    @staticmethod
    def _metric_entry(score: float, raw: Any, *, missing: bool = False) -> dict[str, Any]:
        return {
            "score": score,
            "details": {"raw_score": float(raw) if raw is not None else None},
            "error": "暂无指标数据" if missing else None,
        }

    async def _fetch_one(self, query: str, params: tuple[Any, ...]) -> dict[str, Any] | None:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                row = await cursor.fetchone()
        return dict(row) if row else None

    async def _fetch_all(self, query: str, params: tuple[Any, ...]) -> list[dict[str, Any]]:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                rows = await cursor.fetchall()
        return [dict(row) for row in rows]
