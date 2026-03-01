from __future__ import annotations

import json
from typing import Any

import psycopg
from psycopg.rows import dict_row

from ..models import ExamMeta, ParticipantRow, ProblemInfo, SourceFile, SubmissionRow
from .achievements import AchievementDefinition, build_state_rows


def _to_sql_like_pattern(pattern: str) -> str:
    escaped = pattern.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
    return escaped.replace("*", "%")


def _normalize_nickname(value: str | None) -> str:
    if value is None:
        return ""
    return value.strip().casefold()


class Repository:
    def __init__(self, conn: psycopg.Connection[Any]) -> None:
        self.conn = conn

    def create_ingest_run(self, meta: dict[str, Any]) -> int:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.ingest_runs (status, meta)
                VALUES ('running', %s::jsonb)
                RETURNING ingest_run_id
                """,
                (json.dumps(meta, ensure_ascii=False),),
            )
            row = cursor.fetchone()
            if row is None:
                raise RuntimeError("failed to create ingest run")
            return int(row[0])

    def finish_ingest_run(
        self, ingest_run_id: int, status: str, meta: dict[str, Any]
    ) -> None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                UPDATE ascendany.ingest_runs
                SET status = %s,
                    finished_at = now(),
                    meta = meta || %s::jsonb
                WHERE ingest_run_id = %s
                """,
                (status, json.dumps(meta, ensure_ascii=False), ingest_run_id),
            )

    def get_latest_success_fingerprint(
        self, exam_type: str, source_path: str
    ) -> str | None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                SELECT ier.fingerprint
                FROM ascendany.ingest_exam_runs AS ier
                JOIN ascendany.exams AS e
                  ON e.exam_id = ier.exam_id
                WHERE e.exam_type = %s
                  AND e.source_path = %s
                  AND ier.status = 'success'
                ORDER BY ier.created_at DESC
                LIMIT 1
                """,
                (exam_type, source_path),
            )
            row = cursor.fetchone()
            return row[0] if row else None

    def upsert_exam(self, exam_type: str, source_path: str, meta: ExamMeta) -> int:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.exams (
                    exam_type,
                    source_path,
                    title,
                    starts_at,
                    ends_at,
                    duration_seconds,
                    total_points,
                    meta
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s::jsonb)
                ON CONFLICT (exam_type, source_path)
                DO UPDATE SET
                    title = EXCLUDED.title,
                    starts_at = EXCLUDED.starts_at,
                    ends_at = EXCLUDED.ends_at,
                    duration_seconds = EXCLUDED.duration_seconds,
                    total_points = EXCLUDED.total_points,
                    meta = ascendany.exams.meta || EXCLUDED.meta
                RETURNING exam_id
                """,
                (
                    exam_type,
                    source_path,
                    meta.title,
                    meta.starts_at,
                    meta.ends_at,
                    meta.duration_seconds,
                    meta.total_points,
                    json.dumps(meta.meta, ensure_ascii=False),
                ),
            )
            row = cursor.fetchone()
            if row is None:
                raise RuntimeError("failed to upsert exam")
            return int(row[0])

    def insert_exam_files(self, exam_id: int, files: list[SourceFile]) -> None:
        if not files:
            return
        with self.conn.cursor() as cursor:
            cursor.executemany(
                """
                INSERT INTO ascendany.exam_files (
                    exam_id,
                    file_role,
                    relative_path,
                    sha256,
                    size_bytes,
                    mtime
                )
                VALUES (%s, %s, %s, %s, %s, %s)
                ON CONFLICT (exam_id, relative_path, sha256) DO NOTHING
                """,
                [
                    (
                        exam_id,
                        item.file_role,
                        item.relative_path,
                        item.sha256,
                        item.size_bytes,
                        item.mtime,
                    )
                    for item in files
                ],
            )

    def upsert_student_identity(
        self,
        source: str,
        external_id: str,
        external_name: str | None,
    ) -> int:
        normalized_external_id = external_id.strip()
        is_student_no_source = source.endswith("_student_no")
        is_name_fallback_id = normalized_external_id.startswith("name::")

        canonical_key = f"{source}:{normalized_external_id}"
        if is_student_no_source and not is_name_fallback_id:
            canonical_key = f"student_no:{normalized_external_id}"

        with self.conn.cursor() as cursor:
            if is_student_no_source and not is_name_fallback_id:
                cursor.execute(
                    """
                    SELECT si.student_id
                    FROM ascendany.student_identities AS si
                    WHERE si.external_id = %s
                      AND si.source LIKE %s
                    ORDER BY si.identity_id ASC
                    LIMIT 1
                    """,
                    (normalized_external_id, "%student_no"),
                )
                existing = cursor.fetchone()
                if existing:
                    student_id = int(existing[0])
                    cursor.execute(
                        """
                        UPDATE ascendany.students
                        SET canonical_name = COALESCE(%s, canonical_name),
                            updated_at = now()
                        WHERE student_id = %s
                        """,
                        (external_name, student_id),
                    )
                else:
                    cursor.execute(
                        """
                        INSERT INTO ascendany.students (canonical_key, canonical_name)
                        VALUES (%s, %s)
                        ON CONFLICT (canonical_key)
                        DO UPDATE SET
                            canonical_name = COALESCE(EXCLUDED.canonical_name, ascendany.students.canonical_name),
                            updated_at = now()
                        RETURNING student_id
                        """,
                        (canonical_key, external_name),
                    )
                    row = cursor.fetchone()
                    if row is None:
                        raise RuntimeError("failed to upsert student")
                    student_id = int(row[0])
            else:
                cursor.execute(
                    """
                    INSERT INTO ascendany.students (canonical_key, canonical_name)
                    VALUES (%s, %s)
                    ON CONFLICT (canonical_key)
                    DO UPDATE SET
                        canonical_name = COALESCE(EXCLUDED.canonical_name, ascendany.students.canonical_name),
                        updated_at = now()
                    RETURNING student_id
                    """,
                    (canonical_key, external_name),
                )
                row = cursor.fetchone()
                if row is None:
                    raise RuntimeError("failed to upsert student")
                student_id = int(row[0])

            cursor.execute(
                """
                INSERT INTO ascendany.student_identities (
                    student_id,
                    source,
                    external_id,
                    external_name
                )
                VALUES (%s, %s, %s, %s)
                ON CONFLICT (source, external_id)
                DO UPDATE SET
                    student_id = EXCLUDED.student_id,
                    external_name = COALESCE(EXCLUDED.external_name, ascendany.student_identities.external_name)
                """,
                (student_id, source, normalized_external_id, external_name),
            )
            return student_id

    def upsert_exam_problem(self, exam_id: int, problem: ProblemInfo) -> None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.exam_problems (
                    exam_id,
                    problem_code,
                    problem_title,
                    problem_kind,
                    group_code,
                    group_name,
                    points,
                    order_idx,
                    meta
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb)
                ON CONFLICT (exam_id, problem_code)
                DO UPDATE SET
                    problem_title = COALESCE(EXCLUDED.problem_title, ascendany.exam_problems.problem_title),
                    problem_kind = COALESCE(EXCLUDED.problem_kind, ascendany.exam_problems.problem_kind),
                    group_code = COALESCE(EXCLUDED.group_code, ascendany.exam_problems.group_code),
                    group_name = COALESCE(EXCLUDED.group_name, ascendany.exam_problems.group_name),
                    points = COALESCE(EXCLUDED.points, ascendany.exam_problems.points),
                    order_idx = COALESCE(EXCLUDED.order_idx, ascendany.exam_problems.order_idx),
                    meta = ascendany.exam_problems.meta || EXCLUDED.meta
                """,
                (
                    exam_id,
                    problem.problem_code,
                    problem.problem_title,
                    problem.problem_kind,
                    problem.group_code,
                    problem.group_name,
                    problem.points,
                    problem.order_idx,
                    json.dumps(problem.meta, ensure_ascii=False),
                ),
            )

    def upsert_exam_participant(
        self, exam_id: int, participant: ParticipantRow
    ) -> None:
        if participant.student_id is None:
            return
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.exam_participants (
                    exam_id,
                    student_id,
                    user_group,
                    rank,
                    total_score,
                    time_used_seconds,
                    solved_count,
                    absent,
                    meta
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb)
                ON CONFLICT (exam_id, student_id)
                DO UPDATE SET
                    user_group = EXCLUDED.user_group,
                    rank = EXCLUDED.rank,
                    total_score = EXCLUDED.total_score,
                    time_used_seconds = EXCLUDED.time_used_seconds,
                    solved_count = EXCLUDED.solved_count,
                    absent = EXCLUDED.absent,
                    meta = EXCLUDED.meta
                """,
                (
                    exam_id,
                    participant.student_id,
                    participant.user_group,
                    participant.rank,
                    participant.total_score,
                    participant.time_used_seconds,
                    participant.solved_count,
                    participant.absent,
                    json.dumps(participant.raw, ensure_ascii=False),
                ),
            )

    def insert_submission(self, exam_id: int, row: SubmissionRow) -> None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.submissions (
                    exam_id,
                    student_id,
                    actor_source,
                    actor_external_id,
                    actor_name,
                    submitted_at,
                    problem_code,
                    verdict,
                    score,
                    language,
                    time_ms,
                    memory_kb,
                    row_hash,
                    raw
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb)
                ON CONFLICT (exam_id, row_hash) DO NOTHING
                """,
                (
                    exam_id,
                    row.student_id,
                    row.actor_source,
                    row.actor_external_id,
                    row.actor_name,
                    row.submitted_at,
                    row.problem_code,
                    row.verdict,
                    row.score,
                    row.language,
                    row.time_ms,
                    row.memory_kb,
                    row.row_hash,
                    json.dumps(row.raw, ensure_ascii=False),
                ),
            )

    def fetch_active_nickname_claims(self, nicknames: list[str]) -> dict[str, int]:
        keys = sorted({_normalize_nickname(item) for item in nicknames if _normalize_nickname(item)})
        if not keys:
            return {}
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                SELECT lower(BTRIM(nickname)) AS nickname_key, student_id
                FROM ascendany.student_nickname_claims
                WHERE is_active = TRUE
                  AND lower(BTRIM(nickname)) = ANY(%s)
                """,
                (keys,),
            )
            rows = cursor.fetchall()
        return {
            str(row["nickname_key"]): int(row["student_id"])
            for row in rows
            if row.get("nickname_key") is not None
        }

    def upsert_exam_student_metric(
        self,
        exam_id: int,
        student_id: int,
        knowledge: int | None,
        accuracy: int | None,
        quality: int | None,
        flexibility: int | None,
        proficiency: int | None,
        details: dict[str, Any],
    ) -> None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.exam_student_metrics (
                    exam_id,
                    student_id,
                    knowledge,
                    accuracy,
                    quality,
                    flexibility,
                    proficiency,
                    details
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s::jsonb)
                ON CONFLICT (exam_id, student_id)
                DO UPDATE SET
                    knowledge = EXCLUDED.knowledge,
                    accuracy = EXCLUDED.accuracy,
                    quality = EXCLUDED.quality,
                    flexibility = EXCLUDED.flexibility,
                    proficiency = EXCLUDED.proficiency,
                    details = EXCLUDED.details,
                    computed_at = now()
                """,
                (
                    exam_id,
                    student_id,
                    knowledge,
                    accuracy,
                    quality,
                    flexibility,
                    proficiency,
                    json.dumps(details, ensure_ascii=False),
                ),
            )

    def fetch_current_ratings(self, student_ids: list[int]) -> dict[int, int]:
        if not student_ids:
            return {}
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                SELECT student_id, rating
                FROM ascendany.student_current_metrics
                WHERE student_id = ANY(%s)
                """,
                (student_ids,),
            )
            rows = cursor.fetchall()
            return {int(row["student_id"]): int(row["rating"]) for row in rows}

    def fetch_ratings_before_exam(
        self,
        exam_id: int,
        student_ids: list[int],
        default_rating: int,
    ) -> dict[int, int]:
        unique_student_ids = sorted(set(student_ids))
        if not unique_student_ids:
            return {}

        ratings = {student_id: default_rating for student_id in unique_student_ids}
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                WITH target_exam AS (
                    SELECT
                        e.exam_id,
                        COALESCE(e.starts_at, e.created_at) AS event_time
                    FROM ascendany.exams AS e
                    WHERE e.exam_id = %s
                ),
                ranked AS (
                    SELECT
                        rh.student_id,
                        rh.new_rating,
                        ROW_NUMBER() OVER (
                            PARTITION BY rh.student_id
                            ORDER BY
                                COALESCE(e.starts_at, e.created_at) DESC,
                                rh.exam_id DESC
                        ) AS rn
                    FROM ascendany.rating_history AS rh
                    JOIN ascendany.exams AS e
                      ON e.exam_id = rh.exam_id
                    CROSS JOIN target_exam AS target
                    WHERE rh.student_id = ANY(%s)
                      AND (
                            COALESCE(e.starts_at, e.created_at) < target.event_time
                            OR (
                                COALESCE(e.starts_at, e.created_at) = target.event_time
                                AND rh.exam_id < target.exam_id
                            )
                        )
                )
                SELECT student_id, new_rating
                FROM ranked
                WHERE rn = 1
                """,
                (exam_id, unique_student_ids),
            )
            rows = cursor.fetchall()

        for row in rows:
            ratings[int(row["student_id"])] = int(row["new_rating"])
        return ratings

    def upsert_rating_history(
        self,
        exam_id: int,
        student_id: int,
        old_rating: int,
        delta: int,
        new_rating: int,
        rank: int,
        seed: float,
        performance: float,
        details: dict[str, Any],
    ) -> None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.rating_history (
                    exam_id,
                    student_id,
                    old_rating,
                    delta,
                    new_rating,
                    rank,
                    seed,
                    performance,
                    details
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s::jsonb)
                ON CONFLICT (exam_id, student_id)
                DO UPDATE SET
                    old_rating = EXCLUDED.old_rating,
                    delta = EXCLUDED.delta,
                    new_rating = EXCLUDED.new_rating,
                    rank = EXCLUDED.rank,
                    seed = EXCLUDED.seed,
                    performance = EXCLUDED.performance,
                    details = EXCLUDED.details,
                    created_at = now()
                """,
                (
                    exam_id,
                    student_id,
                    old_rating,
                    delta,
                    new_rating,
                    rank,
                    seed,
                    performance,
                    json.dumps(details, ensure_ascii=False),
                ),
            )

    def fetch_metric_history(self, student_id: int) -> list[dict[str, Any]]:
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                SELECT
                    COALESCE(e.starts_at, esm.computed_at) AS event_time,
                    esm.knowledge,
                    esm.accuracy,
                    esm.quality,
                    esm.flexibility,
                    esm.proficiency
                FROM ascendany.exam_student_metrics AS esm
                JOIN ascendany.exams AS e
                  ON e.exam_id = esm.exam_id
                WHERE esm.student_id = %s
                ORDER BY event_time ASC, esm.exam_id ASC
                """,
                (student_id,),
            )
            rows = cursor.fetchall()
            return [dict(row) for row in rows]

    def fetch_latest_rating(self, student_id: int, default_rating: int) -> int:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                SELECT rh.new_rating
                FROM ascendany.rating_history AS rh
                JOIN ascendany.exams AS e
                  ON e.exam_id = rh.exam_id
                WHERE rh.student_id = %s
                ORDER BY COALESCE(e.starts_at, e.created_at) DESC, rh.exam_id DESC
                LIMIT 1
                """,
                (student_id,),
            )
            row = cursor.fetchone()
            return int(row[0]) if row else default_rating

    def upsert_student_current_metrics(
        self,
        student_id: int,
        metrics: dict[str, float | None],
        rating: int,
    ) -> None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.student_current_metrics (
                    student_id,
                    knowledge,
                    accuracy,
                    quality,
                    flexibility,
                    proficiency,
                    rating
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (student_id)
                DO UPDATE SET
                    knowledge = EXCLUDED.knowledge,
                    accuracy = EXCLUDED.accuracy,
                    quality = EXCLUDED.quality,
                    flexibility = EXCLUDED.flexibility,
                    proficiency = EXCLUDED.proficiency,
                    rating = EXCLUDED.rating,
                    updated_at = now()
                """,
                (
                    student_id,
                    metrics.get("knowledge"),
                    metrics.get("accuracy"),
                    metrics.get("quality"),
                    metrics.get("flexibility"),
                    metrics.get("proficiency"),
                    rating,
                ),
            )

    def record_ingest_exam_run(
        self,
        ingest_run_id: int,
        exam_id: int,
        fingerprint: str,
        status: str,
        error_message: str | None,
    ) -> None:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                INSERT INTO ascendany.ingest_exam_runs (
                    ingest_run_id,
                    exam_id,
                    fingerprint,
                    status,
                    error_message
                )
                VALUES (%s, %s, %s, %s, %s)
                ON CONFLICT (ingest_run_id, exam_id)
                DO UPDATE SET
                    fingerprint = EXCLUDED.fingerprint,
                    status = EXCLUDED.status,
                    error_message = EXCLUDED.error_message,
                    created_at = now()
                """,
                (ingest_run_id, exam_id, fingerprint, status, error_message),
            )

    def fetch_achievement_definitions(
        self,
        source: str,
        enabled_only: bool = True,
    ) -> list[AchievementDefinition]:
        query = """
            SELECT
                achievement_code,
                progress_key,
                bronze_target,
                silver_target,
                gold_target
            FROM ascendany.achievement_definitions
            WHERE source = %s
        """
        params: list[Any] = [source]
        if enabled_only:
            query += " AND is_enabled = TRUE"
        query += " ORDER BY sort_order ASC, achievement_code ASC"
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(query, params)
            rows = cursor.fetchall()
        return [
            AchievementDefinition(
                achievement_code=str(row["achievement_code"]),
                progress_key=str(row["progress_key"]),
                bronze_target=float(row["bronze_target"]),
                silver_target=float(row["silver_target"]),
                gold_target=float(row["gold_target"]),
            )
            for row in rows
        ]

    def fetch_student_ingest_achievement_progress(
        self, student_ids: list[int]
    ) -> dict[int, dict[str, float]]:
        unique_student_ids = sorted(set(student_ids))
        if not unique_student_ids:
            return {}
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                WITH target_students AS (
                    SELECT DISTINCT unnest(%s::bigint[]) AS student_id
                ),
                exam_stats AS (
                    SELECT
                        ts.student_id,
                        COUNT(*) FILTER (WHERE ep.exam_id IS NOT NULL)::numeric AS exam_count,
                        COUNT(*) FILTER (
                            WHERE ep.exam_id IS NOT NULL
                              AND COALESCE(rh.delta, 0) > 0
                        )::numeric AS positive_delta_count,
                        COALESCE(MAX(rh.new_rating), 0)::numeric AS max_rating,
                        GREATEST(COALESCE(MAX(rh.delta), 0), 0)::numeric AS max_rating_delta,
                        COUNT(*) FILTER (
                            WHERE ep.rank IS NOT NULL
                              AND ep.rank <= 10
                        )::numeric AS top10_count,
                        COUNT(*) FILTER (
                            WHERE ep.rank IS NOT NULL
                              AND ep.rank <= 3
                        )::numeric AS top3_count,
                        COUNT(*) FILTER (WHERE ep.rank = 1)::numeric AS rank1_count
                    FROM target_students AS ts
                    LEFT JOIN ascendany.exam_participants AS ep
                      ON ep.student_id = ts.student_id
                     AND ep.absent = FALSE
                    LEFT JOIN ascendany.rating_history AS rh
                      ON rh.student_id = ep.student_id
                     AND rh.exam_id = ep.exam_id
                    GROUP BY ts.student_id
                ),
                ordered_rating AS (
                    SELECT
                        ts.student_id,
                        rh.delta,
                        SUM(
                            CASE
                                WHEN COALESCE(rh.delta, 0) > 0 THEN 0
                                ELSE 1
                            END
                        ) OVER (
                            PARTITION BY ts.student_id
                            ORDER BY COALESCE(e.starts_at, e.created_at, rh.created_at), rh.exam_id
                        ) AS streak_group
                    FROM target_students AS ts
                    LEFT JOIN ascendany.rating_history AS rh
                      ON rh.student_id = ts.student_id
                    LEFT JOIN ascendany.exams AS e
                      ON e.exam_id = rh.exam_id
                ),
                streak_stats AS (
                    SELECT
                        ts.student_id,
                        COALESCE(MAX(streak_len), 0)::numeric AS best_positive_streak
                    FROM target_students AS ts
                    LEFT JOIN (
                        SELECT
                            student_id,
                            streak_group,
                            COUNT(*)::numeric AS streak_len
                        FROM ordered_rating
                        WHERE delta > 0
                        GROUP BY student_id, streak_group
                    ) AS s
                      ON s.student_id = ts.student_id
                    GROUP BY ts.student_id
                ),
                metric_max_stats AS (
                    SELECT
                        ts.student_id,
                        COALESCE(MAX(esm.knowledge), 0)::numeric AS knowledge_max,
                        COALESCE(MAX(esm.accuracy), 0)::numeric AS accuracy_max,
                        COALESCE(MAX(esm.quality), 0)::numeric AS quality_max,
                        COALESCE(MAX(esm.flexibility), 0)::numeric AS flexibility_max,
                        COALESCE(MAX(esm.proficiency), 0)::numeric AS proficiency_max
                    FROM target_students AS ts
                    LEFT JOIN ascendany.exam_student_metrics AS esm
                      ON esm.student_id = ts.student_id
                    GROUP BY ts.student_id
                ),
                balanced_stats AS (
                    SELECT
                        ts.student_id,
                        COALESCE(MAX(
                            CASE
                                WHEN esm.knowledge IS NULL
                                  OR esm.accuracy IS NULL
                                  OR esm.quality IS NULL
                                  OR esm.flexibility IS NULL
                                  OR esm.proficiency IS NULL
                                THEN NULL
                                ELSE LEAST(
                                    esm.knowledge,
                                    esm.accuracy,
                                    esm.quality,
                                    esm.flexibility,
                                    esm.proficiency
                                )::numeric
                            END
                        ), 0)::numeric AS max_of_exam_min_metric
                    FROM target_students AS ts
                    LEFT JOIN ascendany.exam_student_metrics AS esm
                      ON esm.student_id = ts.student_id
                    GROUP BY ts.student_id
                ),
                per_exam_metric_max AS (
                    SELECT
                        esm.exam_id,
                        MAX(esm.knowledge) AS max_knowledge,
                        MAX(esm.accuracy) AS max_accuracy,
                        MAX(esm.quality) AS max_quality,
                        MAX(esm.flexibility) AS max_flexibility,
                        MAX(esm.proficiency) AS max_proficiency
                    FROM ascendany.exam_student_metrics AS esm
                    JOIN ascendany.exam_participants AS ep
                      ON ep.exam_id = esm.exam_id
                     AND ep.student_id = esm.student_id
                     AND ep.absent = FALSE
                    GROUP BY esm.exam_id
                ),
                metric_top1_hits AS (
                    SELECT DISTINCT
                        ts.student_id,
                        esm.exam_id
                    FROM target_students AS ts
                    JOIN ascendany.exam_student_metrics AS esm
                      ON esm.student_id = ts.student_id
                    JOIN ascendany.exam_participants AS ep
                      ON ep.exam_id = esm.exam_id
                     AND ep.student_id = esm.student_id
                     AND ep.absent = FALSE
                    JOIN per_exam_metric_max AS pem
                      ON pem.exam_id = esm.exam_id
                    WHERE (
                        esm.knowledge IS NOT NULL
                        AND pem.max_knowledge IS NOT NULL
                        AND esm.knowledge = pem.max_knowledge
                    ) OR (
                        esm.accuracy IS NOT NULL
                        AND pem.max_accuracy IS NOT NULL
                        AND esm.accuracy = pem.max_accuracy
                    ) OR (
                        esm.quality IS NOT NULL
                        AND pem.max_quality IS NOT NULL
                        AND esm.quality = pem.max_quality
                    ) OR (
                        esm.flexibility IS NOT NULL
                        AND pem.max_flexibility IS NOT NULL
                        AND esm.flexibility = pem.max_flexibility
                    ) OR (
                        esm.proficiency IS NOT NULL
                        AND pem.max_proficiency IS NOT NULL
                        AND esm.proficiency = pem.max_proficiency
                    )
                ),
                metric_top1_stats AS (
                    SELECT
                        ts.student_id,
                        COALESCE(COUNT(mh.exam_id), 0)::numeric AS any_metric_top1_count
                    FROM target_students AS ts
                    LEFT JOIN metric_top1_hits AS mh
                      ON mh.student_id = ts.student_id
                    GROUP BY ts.student_id
                ),
                current_stats AS (
                    SELECT
                        ts.student_id,
                        COALESCE(
                            CASE
                                WHEN scm.knowledge IS NULL
                                  OR scm.accuracy IS NULL
                                  OR scm.quality IS NULL
                                  OR scm.flexibility IS NULL
                                  OR scm.proficiency IS NULL
                                THEN 0
                                ELSE LEAST(
                                    scm.knowledge,
                                    scm.accuracy,
                                    scm.quality,
                                    scm.flexibility,
                                    scm.proficiency
                                )
                            END,
                            0
                        )::numeric AS current_min_metric
                    FROM target_students AS ts
                    LEFT JOIN ascendany.student_current_metrics AS scm
                      ON scm.student_id = ts.student_id
                )
                SELECT
                    ts.student_id,
                    COALESCE(es.exam_count, 0)::numeric AS exam_count,
                    COALESCE(es.positive_delta_count, 0)::numeric AS positive_delta_count,
                    COALESCE(ss.best_positive_streak, 0)::numeric AS best_positive_streak,
                    COALESCE(mm.knowledge_max, 0)::numeric AS knowledge_max,
                    COALESCE(mm.accuracy_max, 0)::numeric AS accuracy_max,
                    COALESCE(mm.quality_max, 0)::numeric AS quality_max,
                    COALESCE(mm.flexibility_max, 0)::numeric AS flexibility_max,
                    COALESCE(mm.proficiency_max, 0)::numeric AS proficiency_max,
                    COALESCE(es.max_rating, 0)::numeric AS max_rating,
                    COALESCE(es.max_rating_delta, 0)::numeric AS max_rating_delta,
                    COALESCE(es.top10_count, 0)::numeric AS top10_count,
                    COALESCE(es.top3_count, 0)::numeric AS top3_count,
                    COALESCE(bs.max_of_exam_min_metric, 0)::numeric AS max_of_exam_min_metric,
                    COALESCE(mt.any_metric_top1_count, 0)::numeric AS any_metric_top1_count,
                    COALESCE(es.rank1_count, 0)::numeric AS rank1_count,
                    COALESCE(cs.current_min_metric, 0)::numeric AS current_min_metric
                FROM target_students AS ts
                LEFT JOIN exam_stats AS es
                  ON es.student_id = ts.student_id
                LEFT JOIN streak_stats AS ss
                  ON ss.student_id = ts.student_id
                LEFT JOIN metric_max_stats AS mm
                  ON mm.student_id = ts.student_id
                LEFT JOIN balanced_stats AS bs
                  ON bs.student_id = ts.student_id
                LEFT JOIN metric_top1_stats AS mt
                  ON mt.student_id = ts.student_id
                LEFT JOIN current_stats AS cs
                  ON cs.student_id = ts.student_id
                ORDER BY ts.student_id ASC
                """,
                (unique_student_ids,),
            )
            rows = cursor.fetchall()
        progress_by_student: dict[int, dict[str, float]] = {}
        for row in rows:
            student_id = int(row["student_id"])
            progress_by_student[student_id] = {
                key: float(row.get(key) or 0.0)
                for key in (
                    "exam_count",
                    "positive_delta_count",
                    "best_positive_streak",
                    "knowledge_max",
                    "accuracy_max",
                    "quality_max",
                    "flexibility_max",
                    "proficiency_max",
                    "max_rating",
                    "max_rating_delta",
                    "top10_count",
                    "top3_count",
                    "max_of_exam_min_metric",
                    "any_metric_top1_count",
                    "rank1_count",
                    "current_min_metric",
                )
            }
        return progress_by_student

    def upsert_student_achievement_states(
        self,
        rows: list[tuple[int, str, float, int, int]],
    ) -> None:
        if not rows:
            return
        with self.conn.cursor() as cursor:
            cursor.executemany(
                """
                INSERT INTO ascendany.student_achievement_states (
                    student_id,
                    achievement_code,
                    progress_value,
                    tier,
                    achieved_at
                )
                VALUES (
                    %s,
                    %s,
                    %s,
                    %s,
                    CASE
                        WHEN %s > 0 THEN now()
                        ELSE NULL
                    END
                )
                ON CONFLICT (student_id, achievement_code)
                DO UPDATE SET
                    progress_value = GREATEST(
                        ascendany.student_achievement_states.progress_value,
                        EXCLUDED.progress_value
                    ),
                    tier = GREATEST(
                        ascendany.student_achievement_states.tier,
                        EXCLUDED.tier
                    ),
                    achieved_at = CASE
                        WHEN ascendany.student_achievement_states.achieved_at IS NOT NULL
                            THEN ascendany.student_achievement_states.achieved_at
                        WHEN GREATEST(
                            ascendany.student_achievement_states.tier,
                            EXCLUDED.tier
                        ) > 0
                            THEN COALESCE(EXCLUDED.achieved_at, now())
                        ELSE NULL
                    END,
                    updated_at = now()
                """,
                rows,
            )

    def recompute_achievements_for_students(
        self,
        student_ids: list[int],
        source: str = "ingest",
    ) -> int:
        unique_student_ids = sorted(set(student_ids))
        if not unique_student_ids:
            return 0
        definitions = self.fetch_achievement_definitions(source=source, enabled_only=True)
        if not definitions:
            return 0
        progress_by_student: dict[int, dict[str, float]] = {}
        if source == "ingest":
            progress_by_student = self.fetch_student_ingest_achievement_progress(
                unique_student_ids
            )
        rows = build_state_rows(
            student_ids=unique_student_ids,
            definitions=definitions,
            progress_by_student=progress_by_student,
        )
        self.upsert_student_achievement_states(rows)
        return len(unique_student_ids)

    def cleanup_orphan_students(self) -> dict[str, int]:
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                UPDATE ascendany.submissions AS s
                SET student_id = NULL
                WHERE s.student_id IS NOT NULL
                  AND NOT EXISTS (
                      SELECT 1
                      FROM ascendany.student_identities AS si
                      WHERE si.student_id = s.student_id
                  )
                """
            )
            submissions_unlinked = cursor.rowcount

            cursor.execute(
                """
                DELETE FROM ascendany.exam_participants AS ep
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM ascendany.student_identities AS si
                    WHERE si.student_id = ep.student_id
                )
                """
            )
            participants_deleted = cursor.rowcount

            cursor.execute(
                """
                DELETE FROM ascendany.exam_student_metrics AS esm
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM ascendany.student_identities AS si
                    WHERE si.student_id = esm.student_id
                )
                """
            )
            metrics_deleted = cursor.rowcount

            cursor.execute(
                """
                DELETE FROM ascendany.rating_history AS rh
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM ascendany.student_identities AS si
                    WHERE si.student_id = rh.student_id
                )
                """
            )
            ratings_deleted = cursor.rowcount

            cursor.execute(
                """
                DELETE FROM ascendany.student_current_metrics AS scm
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM ascendany.student_identities AS si
                    WHERE si.student_id = scm.student_id
                )
                """
            )
            current_metrics_deleted = cursor.rowcount

            cursor.execute(
                """
                DELETE FROM ascendany.students AS s
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM ascendany.student_identities AS si
                    WHERE si.student_id = s.student_id
                )
                """
            )
            students_deleted = cursor.rowcount

        return {
            "submissions_unlinked": submissions_unlinked,
            "participants_deleted": participants_deleted,
            "metrics_deleted": metrics_deleted,
            "ratings_deleted": ratings_deleted,
            "current_metrics_deleted": current_metrics_deleted,
            "students_deleted": students_deleted,
        }

    def list_exams_with_unlinked_submissions(
        self,
        exam_types: list[str] | None,
        actor_sources: list[str],
        limit: int | None,
    ) -> list[dict[str, Any]]:
        source_conditions = (
            " OR ".join(["s.actor_source LIKE %s ESCAPE '\\'" for _ in actor_sources])
            or "TRUE"
        )
        params: list[Any] = [_to_sql_like_pattern(item) for item in actor_sources]
        query = f"""
            SELECT DISTINCT e.exam_id, e.exam_type, e.source_path
            FROM ascendany.exams AS e
            JOIN ascendany.submissions AS s
              ON s.exam_id = e.exam_id
            WHERE s.student_id IS NULL
              AND ({source_conditions})
        """
        if exam_types:
            query += " AND e.exam_type = ANY(%s)"
            params.append(exam_types)
        query += " ORDER BY e.exam_type ASC, e.source_path ASC"
        if limit is not None:
            query += " LIMIT %s"
            params.append(limit)
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(query, params)
            return [dict(row) for row in cursor.fetchall()]

    def fetch_exam_link_candidates(self, exam_id: int) -> list[dict[str, Any]]:
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                SELECT
                    ep.student_id,
                    s.canonical_name,
                    si.source AS identity_source,
                    si.external_id,
                    si.external_name
                FROM ascendany.exam_participants AS ep
                JOIN ascendany.students AS s
                  ON s.student_id = ep.student_id
                LEFT JOIN ascendany.student_identities AS si
                  ON si.student_id = ep.student_id
                WHERE ep.exam_id = %s
                ORDER BY ep.student_id ASC, si.identity_id ASC
                """,
                (exam_id,),
            )
            return [dict(row) for row in cursor.fetchall()]

    def fetch_exam_unlinked_submissions(
        self, exam_id: int, actor_sources: list[str]
    ) -> list[dict[str, Any]]:
        source_conditions = (
            " OR ".join(["actor_source LIKE %s ESCAPE '\\'" for _ in actor_sources])
            or "TRUE"
        )
        params: list[Any] = [
            exam_id,
            *[_to_sql_like_pattern(item) for item in actor_sources],
        ]
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                f"""
                SELECT
                    submission_id,
                    student_id,
                    actor_source,
                    actor_external_id,
                    actor_name,
                    raw
                FROM ascendany.submissions
                WHERE exam_id = %s
                  AND student_id IS NULL
                  AND ({source_conditions})
                ORDER BY submission_id ASC
                """,
                params,
            )
            return [dict(row) for row in cursor.fetchall()]

    def update_submission_linking(
        self,
        submission_id: int,
        student_id: int | None,
        linking_payload: dict[str, Any],
    ) -> bool:
        payload = json.dumps(linking_payload, ensure_ascii=False)
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                UPDATE ascendany.submissions
                SET student_id = %s,
                    raw = jsonb_set(
                        COALESCE(raw, '{}'::jsonb),
                        '{linking}',
                        %s::jsonb,
                        true
                    )
                WHERE submission_id = %s
                  AND (
                    student_id IS DISTINCT FROM %s
                    OR COALESCE(raw -> 'linking', 'null'::jsonb) IS DISTINCT FROM %s::jsonb
                  )
                """,
                (
                    student_id,
                    payload,
                    submission_id,
                    student_id,
                    payload,
                ),
            )
            return cursor.rowcount > 0

    def fetch_exam_metric_rows(self, exam_id: int) -> list[dict[str, Any]]:
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                SELECT student_id, flexibility, details
                FROM ascendany.exam_student_metrics
                WHERE exam_id = %s
                ORDER BY student_id ASC
                """,
                (exam_id,),
            )
            return [dict(row) for row in cursor.fetchall()]

    def fetch_exam_submission_timeline(
        self, exam_id: int
    ) -> dict[int, list[dict[str, Any]]]:
        with self.conn.cursor(row_factory=dict_row) as cursor:
            cursor.execute(
                """
                SELECT
                    student_id,
                    submitted_at,
                    problem_code,
                    verdict,
                    score
                FROM ascendany.submissions
                WHERE exam_id = %s
                  AND student_id IS NOT NULL
                  AND submitted_at IS NOT NULL
                ORDER BY student_id ASC, submitted_at ASC, submission_id ASC
                """,
                (exam_id,),
            )
            rows = cursor.fetchall()
        timeline: dict[int, list[dict[str, Any]]] = {}
        for row in rows:
            student_id = int(row["student_id"])
            timeline.setdefault(student_id, []).append(dict(row))
        return timeline

    def update_exam_metric_flexibility(
        self,
        exam_id: int,
        student_id: int,
        flexibility: int | None,
        details: dict[str, Any],
    ) -> bool:
        payload = json.dumps(details, ensure_ascii=False)
        with self.conn.cursor() as cursor:
            cursor.execute(
                """
                UPDATE ascendany.exam_student_metrics
                SET flexibility = %s,
                    details = %s::jsonb,
                    computed_at = now()
                WHERE exam_id = %s
                  AND student_id = %s
                  AND (
                    flexibility IS DISTINCT FROM %s
                    OR details IS DISTINCT FROM %s::jsonb
                  )
                """,
                (
                    flexibility,
                    payload,
                    exam_id,
                    student_id,
                    flexibility,
                    payload,
                ),
            )
            return cursor.rowcount > 0

    def count_unlinked_submissions(
        self, exam_types: list[str] | None, actor_sources: list[str]
    ) -> int:
        source_conditions = (
            " OR ".join(["s.actor_source LIKE %s ESCAPE '\\'" for _ in actor_sources])
            or "TRUE"
        )
        params: list[Any] = [_to_sql_like_pattern(item) for item in actor_sources]
        query = f"""
            SELECT COUNT(1)
            FROM ascendany.submissions AS s
            JOIN ascendany.exams AS e
              ON e.exam_id = s.exam_id
            WHERE s.student_id IS NULL
              AND ({source_conditions})
        """
        if exam_types:
            query += " AND e.exam_type = ANY(%s)"
            params.append(exam_types)
        with self.conn.cursor() as cursor:
            cursor.execute(query, params)
            row = cursor.fetchone()
            return int(row[0]) if row else 0
