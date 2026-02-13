from __future__ import annotations

import json
from typing import Any

import psycopg
from psycopg.rows import dict_row

from ..models import ExamMeta, ParticipantRow, ProblemInfo, SourceFile, SubmissionRow


def _to_sql_like_pattern(pattern: str) -> str:
    escaped = pattern.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
    return escaped.replace("*", "%")


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
            return int(cursor.fetchone()[0])

    def finish_ingest_run(self, ingest_run_id: int, status: str, meta: dict[str, Any]) -> None:
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

    def get_latest_success_fingerprint(self, exam_type: str, source_path: str) -> str | None:
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
            return int(cursor.fetchone()[0])

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
        canonical_key = f"{source}:{external_id}"
        with self.conn.cursor() as cursor:
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
            student_id = int(cursor.fetchone()[0])
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
                (student_id, source, external_id, external_name),
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

    def upsert_exam_participant(self, exam_id: int, participant: ParticipantRow) -> None:
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

    def list_exams_with_unlinked_submissions(
        self,
        exam_types: list[str] | None,
        actor_sources: list[str],
        limit: int | None,
    ) -> list[dict[str, Any]]:
        source_conditions = " OR ".join(["s.actor_source LIKE %s ESCAPE '\\'" for _ in actor_sources]) or "TRUE"
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

    def fetch_exam_unlinked_submissions(self, exam_id: int, actor_sources: list[str]) -> list[dict[str, Any]]:
        source_conditions = " OR ".join(["actor_source LIKE %s ESCAPE '\\'" for _ in actor_sources]) or "TRUE"
        params: list[Any] = [exam_id, *[_to_sql_like_pattern(item) for item in actor_sources]]
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

    def fetch_exam_submission_timeline(self, exam_id: int) -> dict[int, list[dict[str, Any]]]:
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

    def count_unlinked_submissions(self, exam_types: list[str] | None, actor_sources: list[str]) -> int:
        source_conditions = " OR ".join(["s.actor_source LIKE %s ESCAPE '\\'" for _ in actor_sources]) or "TRUE"
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
