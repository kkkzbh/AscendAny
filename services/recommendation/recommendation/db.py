from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from typing import Any

import psycopg
from psycopg.rows import dict_row


@dataclass(frozen=True)
class StudentRecord:
    student_id: int
    student_no: str | None
    student_name: str | None
    rating: int


@dataclass(frozen=True)
class SubmissionRecord:
    student_id: int
    student_key: str
    practice_problem_id: str
    problem_title: str | None
    submitted_at: datetime | None
    score: float
    max_score: float
    score_rate: float
    verdict: str | None
    is_correct: bool


@dataclass(frozen=True)
class BankProblem:
    problem_id: str
    title: str | None
    description: str
    link: str | None
    submission_count: float
    pass_count: float
    tags: list[str]
    active: bool


class RecommendationRepository:
    def __init__(self, dsn: str) -> None:
        self.dsn = dsn

    def connect(self):
        return psycopg.connect(self.dsn, row_factory=dict_row, prepare_threshold=None)

    def mark_run_running(self, run_id: int, artifact_path: str) -> None:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    UPDATE ascendany.recommendation_model_runs
                    SET status = 'running',
                        started_at = COALESCE(started_at, now()),
                        artifact_path = %s,
                        updated_at = now()
                    WHERE model_run_id = %s
                    """,
                    (artifact_path, run_id),
                )
                cur.execute(
                    """
                    INSERT INTO ascendany.recommendation_model_events
                        (model_run_id, level, message)
                    VALUES (%s, 'info', 'recommendation pipeline started')
                    """,
                    (run_id,),
                )

    def add_run_event(
        self,
        run_id: int,
        *,
        level: str,
        message: str,
        data: dict[str, Any] | None = None,
    ) -> None:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    INSERT INTO ascendany.recommendation_model_events
                        (model_run_id, level, message, data)
                    VALUES (%s, %s, %s, %s::jsonb)
                    """,
                    (
                        run_id,
                        level,
                        message,
                        json.dumps(data or {}, ensure_ascii=False),
                    ),
                )

    def mark_run_finished(
        self,
        run_id: int,
        *,
        status: str,
        metrics: dict[str, Any],
        error_message: str | None = None,
    ) -> None:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    UPDATE ascendany.recommendation_model_runs
                    SET status = %s,
                        metrics = %s::jsonb,
                        error_message = %s,
                        finished_at = now(),
                        updated_at = now()
                    WHERE model_run_id = %s
                    """,
                    (status, json.dumps(metrics), error_message, run_id),
                )

    def load_model_run_config(self, run_id: int) -> dict[str, Any]:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    SELECT model_type, config
                    FROM ascendany.recommendation_model_runs
                    WHERE model_run_id = %s
                    """,
                    (run_id,),
                )
                row = cur.fetchone()
        if not row:
            return {}
        model_type = row.get("model_type") if isinstance(row, dict) else row[0]
        raw = row.get("config") if isinstance(row, dict) else row[1]
        if raw is None:
            config = {}
        elif isinstance(raw, dict):
            config = raw
        elif isinstance(raw, str):
            try:
                value = json.loads(raw)
            except json.JSONDecodeError:
                config = {}
            else:
                config = value if isinstance(value, dict) else {}
        else:
            config = {}
        if model_type and "model_type" not in config and "model" not in config:
            config["model_type"] = str(model_type)
        return config

    def load_students(self) -> list[StudentRecord]:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    WITH resolved_submissions AS (
                        SELECT
                            COALESCE(s.student_id, resolved.student_id) AS student_id
                        FROM ascendany.submissions AS s
                        LEFT JOIN LATERAL (
                            SELECT MIN(ep.student_id) AS student_id
                            FROM ascendany.exam_participants AS ep
                            JOIN ascendany.student_identities AS si
                              ON si.student_id = ep.student_id
                             AND si.external_name = s.actor_name
                            WHERE ep.exam_id = s.exam_id
                            HAVING COUNT(DISTINCT ep.student_id) = 1
                        ) AS resolved ON TRUE
                        WHERE COALESCE(s.problem_code, '') <> ''
                    )
                    SELECT
                        s.student_id,
                        si.external_id AS student_no,
                        COALESCE(si.external_name, s.canonical_name) AS student_name,
                        COALESCE(scm.rating, 800) AS rating
                    FROM ascendany.students AS s
                    LEFT JOIN LATERAL (
                        SELECT external_id, external_name
                        FROM ascendany.student_identities
                        WHERE student_id = s.student_id
                          AND source LIKE %s
                        ORDER BY identity_id ASC
                        LIMIT 1
                    ) AS si ON TRUE
                    LEFT JOIN ascendany.student_current_metrics AS scm
                      ON scm.student_id = s.student_id
                    WHERE EXISTS (
                        SELECT 1
                        FROM resolved_submissions AS sub
                        WHERE sub.student_id = s.student_id
                    )
                    ORDER BY s.student_id
                    """,
                    ("%student_no",),
                )
                rows = cur.fetchall()
        return [
            StudentRecord(
                student_id=int(row["student_id"]),
                student_no=str(row["student_no"]) if row.get("student_no") else None,
                student_name=str(row["student_name"]) if row.get("student_name") else None,
                rating=int(row["rating"]),
            )
            for row in rows
        ]

    def load_submissions(self) -> list[SubmissionRecord]:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    WITH resolved_submissions AS (
                        SELECT
                            s.*,
                            COALESCE(s.student_id, resolved.student_id) AS resolved_student_id
                        FROM ascendany.submissions AS s
                        LEFT JOIN LATERAL (
                            SELECT MIN(ep.student_id) AS student_id
                            FROM ascendany.exam_participants AS ep
                            JOIN ascendany.student_identities AS si
                              ON si.student_id = ep.student_id
                             AND si.external_name = s.actor_name
                            WHERE ep.exam_id = s.exam_id
                            HAVING COUNT(DISTINCT ep.student_id) = 1
                        ) AS resolved ON TRUE
                    )
                    SELECT
                        s.resolved_student_id AS student_id,
                        COALESCE(
                            NULLIF(s.raw->>'global_problem_id', ''),
                            NULLIF(s.raw->>'problem_id', ''),
                            NULLIF(ep.meta->>'global_problem_id', ''),
                            NULLIF(ep.meta->>'problem_id', ''),
                            CASE
                                WHEN COALESCE(s.problem_code, '') <> ''
                                 AND e.source_path LIKE e.exam_type || '/%'
                                    THEN replace(e.source_path, '/', '_') || '_' || s.problem_code
                                WHEN COALESCE(s.problem_code, '') <> ''
                                    THEN e.exam_type || '_' || replace(e.source_path, '/', '_') || '_' || s.problem_code
                                ELSE NULL
                            END,
                            NULLIF(s.problem_code, ''),
                            ('exam:' || s.exam_id::text || ':' || COALESCE(s.problem_code, ''))
                        ) AS practice_problem_id,
                        COALESCE(ep.problem_title, s.problem_code) AS problem_title,
                        s.submitted_at,
                        COALESCE(s.score, 0)::float AS score,
                        COALESCE(NULLIF(ep.points, 0), 100)::float AS max_score,
                        CASE
                            WHEN ep.points IS NOT NULL AND ep.points > 0 AND s.score IS NOT NULL
                                THEN LEAST(1.0, GREATEST(0.0, (s.score::numeric / ep.points::numeric)))::float
                            WHEN lower(COALESCE(s.verdict, '')) IN ('accepted', 'ac', '答案正确')
                                THEN 1.0
                            ELSE 0.0
                        END AS score_rate,
                        s.verdict,
                        CASE
                            WHEN lower(COALESCE(s.verdict, '')) IN ('accepted', 'ac', '答案正确') THEN TRUE
                            WHEN ep.points IS NOT NULL AND ep.points > 0 AND s.score IS NOT NULL
                                THEN s.score >= ep.points
                            ELSE FALSE
                        END AS is_correct
                    FROM resolved_submissions AS s
                    JOIN ascendany.exams AS e
                      ON e.exam_id = s.exam_id
                    LEFT JOIN ascendany.exam_problems AS ep
                      ON ep.exam_id = s.exam_id
                     AND ep.problem_code = s.problem_code
                    WHERE s.resolved_student_id IS NOT NULL
                      AND COALESCE(s.problem_code, '') <> ''
                    """
                )
                rows = cur.fetchall()
        return [
            SubmissionRecord(
                student_id=int(row["student_id"]),
                student_key=str(row["student_id"]),
                practice_problem_id=str(row["practice_problem_id"]),
                problem_title=str(row["problem_title"]) if row.get("problem_title") else None,
                submitted_at=row.get("submitted_at")
                if isinstance(row.get("submitted_at"), datetime)
                else None,
                score=float(row.get("score") or 0),
                max_score=float(row.get("max_score") or 100),
                score_rate=float(row["score_rate"]),
                verdict=str(row["verdict"]) if row.get("verdict") else None,
                is_correct=bool(row["is_correct"]),
            )
            for row in rows
            ]

    def load_practice_problem_tags(self) -> dict[str, list[str]]:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    SELECT practice_problem_id,
                           knowledge_point
                    FROM ascendany.recommendation_practice_problem_tags
                    ORDER BY practice_problem_id, knowledge_point
                    """
                )
                rows = cur.fetchall()
        result: dict[str, list[str]] = {}
        for row in rows:
            result.setdefault(str(row["practice_problem_id"]), []).append(
                str(row["knowledge_point"])
            )
        return result

    def load_bank_problems(self) -> list[BankProblem]:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    SELECT
                        b.problem_id,
                        b.title,
                        b.description,
                        b.link,
                        b.submission_count,
                        b.pass_count,
                        b.active,
                        COALESCE(
                            jsonb_agg(t.knowledge_point ORDER BY t.knowledge_point)
                                FILTER (WHERE t.knowledge_point IS NOT NULL),
                            '[]'::jsonb
                        ) AS tags
                    FROM ascendany.recommendation_problem_bank AS b
                    LEFT JOIN ascendany.recommendation_problem_tags AS t
                      ON t.problem_id = b.problem_id
                    WHERE b.active = TRUE
                    GROUP BY b.problem_id
                    ORDER BY b.problem_id
                    """
                )
                rows = cur.fetchall()
        result: list[BankProblem] = []
        for row in rows:
            raw_tags = row.get("tags") or []
            if isinstance(raw_tags, str):
                raw_tags = json.loads(raw_tags)
            result.append(
                BankProblem(
                    problem_id=str(row["problem_id"]),
                    title=str(row["title"]) if row.get("title") else None,
                    description=str(row.get("description") or ""),
                    link=str(row["link"]) if row.get("link") else None,
                    submission_count=float(row.get("submission_count") or 0),
                    pass_count=float(row.get("pass_count") or 0),
                    tags=[str(item) for item in raw_tags if item],
                    active=bool(row["active"]),
                )
            )
        return result

    def replace_problem_recommendations(
        self,
        run_id: int,
        payloads: dict[int, list[dict[str, Any]]],
    ) -> None:
        with self.connect() as conn:
            with conn.cursor() as cur:
                for student_id, items in payloads.items():
                    cur.execute(
                        """
                        INSERT INTO ascendany.student_recommendation_snapshots
                            (student_id, model_run_id, items, generated_at)
                        VALUES (%s, %s, %s::jsonb, now())
                        ON CONFLICT (student_id, model_run_id)
                        DO UPDATE SET items = EXCLUDED.items,
                                      generated_at = EXCLUDED.generated_at
                        """,
                        (student_id, run_id, json.dumps(items, ensure_ascii=False)),
                    )

    def replace_learning_paths(
        self,
        run_id: int,
        payloads: dict[int, dict[str, Any]],
    ) -> None:
        with self.connect() as conn:
            with conn.cursor() as cur:
                for student_id, item in payloads.items():
                    cur.execute(
                        """
                        INSERT INTO ascendany.student_learning_path_snapshots
                            (student_id, model_run_id, targets, path, explanations, generated_at)
                        VALUES (%s, %s, %s::jsonb, %s::jsonb, %s::jsonb, now())
                        ON CONFLICT (student_id, model_run_id)
                        DO UPDATE SET targets = EXCLUDED.targets,
                                      path = EXCLUDED.path,
                                      explanations = EXCLUDED.explanations,
                                      generated_at = EXCLUDED.generated_at
                        """,
                        (
                            student_id,
                            run_id,
                            json.dumps(item.get("targets", []), ensure_ascii=False),
                            json.dumps(item.get("path", []), ensure_ascii=False),
                            json.dumps(item.get("explanations", {}), ensure_ascii=False),
                        ),
                    )
