from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from decimal import Decimal

from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool


@dataclass(slots=True)
class StudentIdentityMatch:
    student_id: int
    student_no: str
    student_name: str | None


@dataclass(slots=True)
class StudentNoMatch:
    student_id: int
    student_no: str
    student_name: str | None


@dataclass(slots=True)
class DashboardMetricsRow:
    knowledge: Decimal | float | int | None
    accuracy: Decimal | float | int | None
    quality: Decimal | float | int | None
    flexibility: Decimal | float | int | None
    proficiency: Decimal | float | int | None
    rating: int
    updated_at: datetime | None = None


@dataclass(slots=True)
class RatingHistoryRow:
    exam_id: int
    exam_name: str
    exam_time: datetime
    old_rating: int
    delta: int
    new_rating: int


class ApiRepository:
    def __init__(self, pool: AsyncConnectionPool) -> None:
        self._pool = pool

    async def fetch_latest_exam_imported_at(self) -> datetime | None:
        query = """
            SELECT MAX(ir.finished_at) AS latest_exam_imported_at
            FROM ascendany.ingest_runs AS ir
            WHERE ir.status IN ('success', 'partial_success')
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query)
                row = await cursor.fetchone()
        if not row:
            return None
        latest = row.get("latest_exam_imported_at")
        return latest if isinstance(latest, datetime) else None

    async def find_students_by_student_no(
        self, student_no: str
    ) -> list[StudentIdentityMatch]:
        query = """
            SELECT
                s.student_id,
                si.external_id AS student_no,
                COALESCE(NULLIF(BTRIM(si.external_name), ''), NULLIF(BTRIM(s.canonical_name), '')) AS student_name,
                COALESCE(scm.updated_at, s.updated_at, s.created_at) AS sort_time
            FROM ascendany.student_identities AS si
            JOIN ascendany.students AS s
              ON s.student_id = si.student_id
            LEFT JOIN ascendany.student_current_metrics AS scm
              ON scm.student_id = s.student_id
            WHERE si.external_id = %s
              AND si.source LIKE %s
            ORDER BY sort_time DESC, s.student_id ASC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_no, "%student_no"))
                rows = await cursor.fetchall()

        return [
            StudentIdentityMatch(
                student_id=int(row["student_id"]),
                student_no=str(row["student_no"]),
                student_name=str(row["student_name"])
                if row.get("student_name")
                else None,
            )
            for row in rows
        ]

    async def find_student_nos_by_name(self, student_name: str) -> list[StudentNoMatch]:
        query = """
            WITH ranked AS (
                SELECT
                    s.student_id,
                    si.external_id AS student_no,
                    COALESCE(NULLIF(BTRIM(si.external_name), ''), NULLIF(BTRIM(s.canonical_name), '')) AS student_name,
                    COALESCE(scm.updated_at, s.updated_at, s.created_at) AS sort_time,
                    ROW_NUMBER() OVER (
                        PARTITION BY si.external_id
                        ORDER BY COALESCE(scm.updated_at, s.updated_at, s.created_at) DESC, s.student_id ASC
                    ) AS rank_no
                FROM ascendany.student_identities AS si
                JOIN ascendany.students AS s
                  ON s.student_id = si.student_id
                LEFT JOIN ascendany.student_current_metrics AS scm
                  ON scm.student_id = s.student_id
                WHERE si.source LIKE %s
                  AND (
                    COALESCE(NULLIF(BTRIM(si.external_name), ''), '') = %s
                    OR COALESCE(NULLIF(BTRIM(s.canonical_name), ''), '') = %s
                  )
            )
            SELECT student_id, student_no, student_name
            FROM ranked
            WHERE rank_no = 1
            ORDER BY sort_time DESC, student_id ASC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, ("%student_no", student_name, student_name))
                rows = await cursor.fetchall()

        return [
            StudentNoMatch(
                student_id=int(row["student_id"]),
                student_no=str(row["student_no"]),
                student_name=str(row["student_name"])
                if row.get("student_name")
                else None,
            )
            for row in rows
        ]

    async def exists_pta_submission_by_actor_name(self, actor_name: str) -> bool:
        query = """
            SELECT EXISTS (
                SELECT 1
                FROM ascendany.submissions AS s
                WHERE s.actor_source ~ '^pta_.*_account$'
                  AND COALESCE(NULLIF(BTRIM(s.actor_name), ''), '') = %s
                LIMIT 1
            ) AS has_match
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (actor_name,))
                row = await cursor.fetchone()
        if not row:
            return False
        return bool(row["has_match"])

    async def fetch_current_metrics(
        self, student_id: int
    ) -> DashboardMetricsRow | None:
        query = """
            SELECT
                knowledge,
                accuracy,
                quality,
                flexibility,
                proficiency,
                rating,
                updated_at
            FROM ascendany.student_current_metrics
            WHERE student_id = %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_id,))
                row = await cursor.fetchone()
        if not row:
            return None
        return DashboardMetricsRow(
            knowledge=row.get("knowledge"),
            accuracy=row.get("accuracy"),
            quality=row.get("quality"),
            flexibility=row.get("flexibility"),
            proficiency=row.get("proficiency"),
            rating=int(row.get("rating", 800)),
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def fetch_rating_history(
        self, student_id: int, limit: int = 50
    ) -> list[RatingHistoryRow]:
        query = """
            SELECT
                rh.exam_id,
                COALESCE(NULLIF(e.title, ''), e.source_path) AS exam_name,
                COALESCE(e.starts_at, rh.created_at) AS exam_time,
                rh.old_rating,
                rh.delta,
                rh.new_rating
            FROM ascendany.rating_history AS rh
            JOIN ascendany.exams AS e
              ON e.exam_id = rh.exam_id
            WHERE rh.student_id = %s
            ORDER BY COALESCE(e.starts_at, rh.created_at) DESC, rh.exam_id DESC
            LIMIT %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_id, limit))
                rows = await cursor.fetchall()

        history: list[RatingHistoryRow] = []
        for row in rows:
            exam_time = row.get("exam_time")
            if not isinstance(exam_time, datetime):
                continue
            history.append(
                RatingHistoryRow(
                    exam_id=int(row["exam_id"]),
                    exam_name=str(row["exam_name"]),
                    exam_time=exam_time,
                    old_rating=int(row["old_rating"]),
                    delta=int(row["delta"]),
                    new_rating=int(row["new_rating"]),
                )
            )
        return history
