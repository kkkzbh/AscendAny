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


@dataclass(slots=True)
class ExamMetricHistoryRow:
    exam_id: int
    exam_name: str
    exam_time: datetime
    knowledge: Decimal | float | int | None
    accuracy: Decimal | float | int | None
    quality: Decimal | float | int | None
    flexibility: Decimal | float | int | None
    proficiency: Decimal | float | int | None
    computed_at: datetime | None


@dataclass(slots=True)
class ExamInfoRow:
    exam_id: int
    exam_type: str
    source_path: str
    title: str | None
    starts_at: datetime | None
    ends_at: datetime | None
    duration_seconds: int | None
    problem_count: int
    participant_count: int


@dataclass(slots=True)
class ExamSubmissionRow:
    submission_id: int
    problem_code: str | None
    submitted_at: datetime | None
    verdict: str | None
    score: Decimal | float | int | None
    language: str | None
    time_ms: int | None
    memory_kb: int | None


@dataclass(slots=True)
class ExamParticipantRow:
    student_id: int
    student_name: str | None
    rank: int | None
    total_score: Decimal | float | int | None
    solved_count: int | None


@dataclass(slots=True)
class ExamStudentMetricRow:
    exam_id: int
    student_id: int
    knowledge: Decimal | float | int | None
    accuracy: Decimal | float | int | None
    quality: Decimal | float | int | None
    flexibility: Decimal | float | int | None
    proficiency: Decimal | float | int | None


@dataclass(slots=True)
class AccountRow:
    account_id: int
    username: str
    password_hash: str
    is_active: bool


@dataclass(slots=True)
class AccountProfileRow:
    student_id: str | None
    pta_nickname: str | None
    updated_at: datetime | None


@dataclass(slots=True)
class AutoAnalysisCacheRow:
    account_id: int
    exam_id: int
    role_id: str
    provider_type: str | None
    reply: str
    source: str
    generated_at: datetime | None
    delivered_at: datetime | None
    updated_at: datetime | None


@dataclass(slots=True)
class AutoAnalysisCandidateRow:
    account_id: int
    student_id: str | None
    pta_nickname: str | None


@dataclass(slots=True)
class RefreshTokenRow:
    token_id: int
    account_id: int
    token_hash: str
    expires_at: datetime
    revoked_at: datetime | None
    device_id: str | None


class ApiRepository:
    def __init__(self, pool: AsyncConnectionPool) -> None:
        self._pool = pool

    async def create_account(
        self, username: str, password_hash: str
    ) -> AccountRow | None:
        query = """
            INSERT INTO ascendany.user_accounts (username, password_hash)
            VALUES (%s, %s)
            ON CONFLICT (username_normalized) DO NOTHING
            RETURNING account_id, username, password_hash, is_active
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (username, password_hash))
                row = await cursor.fetchone()
        if not row:
            return None
        return AccountRow(
            account_id=int(row["account_id"]),
            username=str(row["username"]),
            password_hash=str(row["password_hash"]),
            is_active=bool(row["is_active"]),
        )

    async def add_account_contact(
        self, account_id: int, contact_type: str, value: str
    ) -> bool:
        query = """
            INSERT INTO ascendany.user_contacts (account_id, type, value)
            VALUES (%s, %s, %s)
            ON CONFLICT (type, value_normalized) DO NOTHING
            RETURNING contact_id
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (account_id, contact_type, value))
                row = await cursor.fetchone()
        return row is not None

    async def fetch_account_by_username(self, username: str) -> AccountRow | None:
        query = """
            SELECT account_id, username, password_hash, is_active
            FROM ascendany.user_accounts
            WHERE username_normalized = lower(BTRIM(%s))
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (username,))
                row = await cursor.fetchone()
        if not row:
            return None
        return AccountRow(
            account_id=int(row["account_id"]),
            username=str(row["username"]),
            password_hash=str(row["password_hash"]),
            is_active=bool(row["is_active"]),
        )

    async def fetch_account_by_id(self, account_id: int) -> AccountRow | None:
        query = """
            SELECT account_id, username, password_hash, is_active
            FROM ascendany.user_accounts
            WHERE account_id = %s
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (account_id,))
                row = await cursor.fetchone()
        if not row:
            return None
        return AccountRow(
            account_id=int(row["account_id"]),
            username=str(row["username"]),
            password_hash=str(row["password_hash"]),
            is_active=bool(row["is_active"]),
        )

    async def touch_account_login(self, account_id: int) -> None:
        query = """
            UPDATE ascendany.user_accounts
            SET last_login_at = now(), updated_at = now()
            WHERE account_id = %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, (account_id,))

    async def delete_account(self, account_id: int) -> None:
        query = """
            DELETE FROM ascendany.user_accounts
            WHERE account_id = %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, (account_id,))

    async def fetch_account_profile(self, account_id: int) -> AccountProfileRow | None:
        query = """
            SELECT student_id, pta_nickname, updated_at
            FROM ascendany.user_profiles
            WHERE account_id = %s
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (account_id,))
                row = await cursor.fetchone()
        if not row:
            return None
        return AccountProfileRow(
            student_id=str(row["student_id"]) if row.get("student_id") else None,
            pta_nickname=str(row["pta_nickname"])
            if row.get("pta_nickname")
            else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def upsert_account_profile(
        self, account_id: int, student_id: str | None, pta_nickname: str | None
    ) -> AccountProfileRow:
        query = """
            INSERT INTO ascendany.user_profiles (account_id, student_id, pta_nickname, updated_at)
            VALUES (%s, %s, %s, now())
            ON CONFLICT (account_id)
            DO UPDATE SET
                student_id = EXCLUDED.student_id,
                pta_nickname = EXCLUDED.pta_nickname,
                updated_at = now()
            RETURNING student_id, pta_nickname, updated_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (account_id, student_id, pta_nickname))
                row = await cursor.fetchone()
        if not row:
            return AccountProfileRow(student_id=None, pta_nickname=None, updated_at=None)
        return AccountProfileRow(
            student_id=str(row["student_id"]) if row.get("student_id") else None,
            pta_nickname=str(row["pta_nickname"])
            if row.get("pta_nickname")
            else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def fetch_auto_analysis_cache(
        self,
        account_id: int,
        exam_id: int,
        role_id: str,
    ) -> AutoAnalysisCacheRow | None:
        query = """
            SELECT
                account_id,
                exam_id,
                role_id,
                provider_type,
                reply,
                source,
                generated_at,
                delivered_at,
                updated_at
            FROM ascendany.user_auto_analysis_cache
            WHERE account_id = %s
              AND exam_id = %s
              AND role_id = %s
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (account_id, exam_id, role_id))
                row = await cursor.fetchone()
        if not row:
            return None
        return AutoAnalysisCacheRow(
            account_id=int(row["account_id"]),
            exam_id=int(row["exam_id"]),
            role_id=str(row["role_id"]),
            provider_type=str(row["provider_type"])
            if row.get("provider_type")
            else None,
            reply=str(row["reply"]),
            source=str(row["source"]) if row.get("source") else "online",
            generated_at=row.get("generated_at")
            if isinstance(row.get("generated_at"), datetime)
            else None,
            delivered_at=row.get("delivered_at")
            if isinstance(row.get("delivered_at"), datetime)
            else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def upsert_auto_analysis_cache(
        self,
        account_id: int,
        exam_id: int,
        role_id: str,
        provider_type: str | None,
        reply: str,
        source: str,
    ) -> AutoAnalysisCacheRow:
        query = """
            INSERT INTO ascendany.user_auto_analysis_cache (
                account_id,
                exam_id,
                role_id,
                provider_type,
                reply,
                source,
                generated_at,
                updated_at
            )
            VALUES (%s, %s, %s, %s, %s, %s, now(), now())
            ON CONFLICT (account_id, exam_id, role_id)
            DO UPDATE SET
                provider_type = EXCLUDED.provider_type,
                reply = EXCLUDED.reply,
                source = EXCLUDED.source,
                generated_at = now(),
                updated_at = now()
            RETURNING
                account_id,
                exam_id,
                role_id,
                provider_type,
                reply,
                source,
                generated_at,
                delivered_at,
                updated_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (account_id, exam_id, role_id, provider_type, reply, source),
                )
                row = await cursor.fetchone()
        if not row:
            raise RuntimeError("Failed to upsert auto-analysis cache row")
        return AutoAnalysisCacheRow(
            account_id=int(row["account_id"]),
            exam_id=int(row["exam_id"]),
            role_id=str(row["role_id"]),
            provider_type=str(row["provider_type"])
            if row.get("provider_type")
            else None,
            reply=str(row["reply"]),
            source=str(row["source"]) if row.get("source") else "online",
            generated_at=row.get("generated_at")
            if isinstance(row.get("generated_at"), datetime)
            else None,
            delivered_at=row.get("delivered_at")
            if isinstance(row.get("delivered_at"), datetime)
            else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def mark_auto_analysis_delivered(
        self,
        account_id: int,
        exam_id: int,
        role_id: str,
    ) -> None:
        query = """
            UPDATE ascendany.user_auto_analysis_cache
            SET delivered_at = COALESCE(delivered_at, now()),
                updated_at = now()
            WHERE account_id = %s
              AND exam_id = %s
              AND role_id = %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, (account_id, exam_id, role_id))

    async def fetch_auto_analysis_candidates_by_exam(
        self,
        exam_id: int,
        limit: int = 2000,
    ) -> list[AutoAnalysisCandidateRow]:
        query = """
            WITH target_students AS (
                SELECT DISTINCT rh.student_id
                FROM ascendany.rating_history AS rh
                WHERE rh.exam_id = %s
            )
            SELECT DISTINCT
                up.account_id,
                NULLIF(BTRIM(up.student_id), '') AS student_id,
                NULLIF(BTRIM(up.pta_nickname), '') AS pta_nickname
            FROM ascendany.user_profiles AS up
            WHERE
                (
                    NULLIF(BTRIM(up.student_id), '') IS NOT NULL
                    AND EXISTS (
                        SELECT 1
                        FROM ascendany.student_identities AS si
                        JOIN target_students AS ts
                          ON ts.student_id = si.student_id
                        WHERE si.source LIKE %s
                          AND si.external_id = NULLIF(BTRIM(up.student_id), '')
                    )
                )
                OR (
                    NULLIF(BTRIM(up.pta_nickname), '') IS NOT NULL
                    AND EXISTS (
                        SELECT 1
                        FROM ascendany.student_identities AS si
                        JOIN target_students AS ts
                          ON ts.student_id = si.student_id
                        WHERE COALESCE(NULLIF(BTRIM(si.external_name), ''), '')
                              = NULLIF(BTRIM(up.pta_nickname), '')
                    )
                )
            ORDER BY up.account_id ASC
            LIMIT %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, "%student_no", limit))
                rows = await cursor.fetchall()
        return [
            AutoAnalysisCandidateRow(
                account_id=int(row["account_id"]),
                student_id=str(row["student_id"]) if row.get("student_id") else None,
                pta_nickname=str(row["pta_nickname"])
                if row.get("pta_nickname")
                else None,
            )
            for row in rows
        ]

    async def insert_refresh_token(
        self,
        account_id: int,
        token_hash: str,
        expires_at: datetime,
        device_id: str | None,
    ) -> RefreshTokenRow:
        query = """
            INSERT INTO ascendany.user_refresh_tokens (
                account_id, token_hash, expires_at, device_id
            )
            VALUES (%s, %s, %s, %s)
            RETURNING token_id, account_id, token_hash, expires_at, revoked_at, device_id
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (account_id, token_hash, expires_at, device_id))
                row = await cursor.fetchone()
        if not row:
            raise RuntimeError("Failed to insert refresh token row")
        return RefreshTokenRow(
            token_id=int(row["token_id"]),
            account_id=int(row["account_id"]),
            token_hash=str(row["token_hash"]),
            expires_at=row["expires_at"],
            revoked_at=row.get("revoked_at"),
            device_id=str(row["device_id"]) if row.get("device_id") else None,
        )

    async def fetch_refresh_token_by_hash(
        self, token_hash: str
    ) -> RefreshTokenRow | None:
        query = """
            SELECT token_id, account_id, token_hash, expires_at, revoked_at, device_id
            FROM ascendany.user_refresh_tokens
            WHERE token_hash = %s
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (token_hash,))
                row = await cursor.fetchone()
        if not row:
            return None
        expires_at = row.get("expires_at")
        if not isinstance(expires_at, datetime):
            return None
        revoked_at = row.get("revoked_at")
        return RefreshTokenRow(
            token_id=int(row["token_id"]),
            account_id=int(row["account_id"]),
            token_hash=str(row["token_hash"]),
            expires_at=expires_at,
            revoked_at=revoked_at if isinstance(revoked_at, datetime) else None,
            device_id=str(row["device_id"]) if row.get("device_id") else None,
        )

    async def revoke_refresh_token_by_id(self, token_id: int) -> None:
        query = """
            UPDATE ascendany.user_refresh_tokens
            SET revoked_at = now()
            WHERE token_id = %s
              AND revoked_at IS NULL
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, (token_id,))

    async def revoke_refresh_token_by_hash(
        self, account_id: int, token_hash: str
    ) -> bool:
        query = """
            UPDATE ascendany.user_refresh_tokens
            SET revoked_at = now()
            WHERE account_id = %s
              AND token_hash = %s
              AND revoked_at IS NULL
            RETURNING token_id
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (account_id, token_hash))
                row = await cursor.fetchone()
        return row is not None

    async def revoke_all_refresh_tokens(self, account_id: int) -> int:
        query = """
            UPDATE ascendany.user_refresh_tokens
            SET revoked_at = now()
            WHERE account_id = %s
              AND revoked_at IS NULL
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, (account_id,))
                return cursor.rowcount or 0

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

    async def fetch_exam_info(self, exam_id: int) -> ExamInfoRow | None:
        query = """
            SELECT
                e.exam_id,
                e.exam_type,
                e.source_path,
                e.title,
                e.starts_at,
                e.ends_at,
                e.duration_seconds,
                (
                    SELECT COUNT(*)
                    FROM ascendany.exam_problems ep
                    WHERE ep.exam_id = e.exam_id
                ) AS problem_count,
                (
                    SELECT COUNT(*)
                    FROM ascendany.exam_participants ep2
                    WHERE ep2.exam_id = e.exam_id
                ) AS participant_count
            FROM ascendany.exams AS e
            WHERE e.exam_id = %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id,))
                row = await cursor.fetchone()
        if not row:
            return None
        starts_at = row.get("starts_at")
        ends_at = row.get("ends_at")
        return ExamInfoRow(
            exam_id=int(row["exam_id"]),
            exam_type=str(row["exam_type"]),
            source_path=str(row["source_path"]),
            title=str(row["title"]) if row.get("title") else None,
            starts_at=starts_at if isinstance(starts_at, datetime) else None,
            ends_at=ends_at if isinstance(ends_at, datetime) else None,
            duration_seconds=int(row["duration_seconds"])
            if row.get("duration_seconds") is not None
            else None,
            problem_count=int(row.get("problem_count", 0)),
            participant_count=int(row.get("participant_count", 0)),
        )

    async def fetch_exam_submissions_for_student(
        self, exam_id: int, student_id: int, limit: int = 100
    ) -> list[ExamSubmissionRow]:
        query = """
            SELECT
                s.submission_id,
                s.problem_code,
                s.submitted_at,
                s.verdict,
                s.score,
                s.language,
                s.time_ms,
                s.memory_kb
            FROM ascendany.submissions AS s
            WHERE s.exam_id = %s AND s.student_id = %s
            ORDER BY s.submitted_at ASC
            LIMIT %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, student_id, limit))
                rows = await cursor.fetchall()

        return [
            ExamSubmissionRow(
                submission_id=int(row["submission_id"]),
                problem_code=str(row["problem_code"])
                if row.get("problem_code")
                else None,
                submitted_at=row.get("submitted_at")
                if isinstance(row.get("submitted_at"), datetime)
                else None,
                verdict=str(row["verdict"]) if row.get("verdict") else None,
                score=row.get("score"),
                language=str(row["language"]) if row.get("language") else None,
                time_ms=int(row["time_ms"]) if row.get("time_ms") is not None else None,
                memory_kb=int(row["memory_kb"])
                if row.get("memory_kb") is not None
                else None,
            )
            for row in rows
        ]

    async def fetch_exam_participants_ranked(
        self, exam_id: int, limit: int = 200
    ) -> list[ExamParticipantRow]:
        query = """
            SELECT
                ep.student_id,
                COALESCE(NULLIF(BTRIM(s.canonical_name), ''), si_name.external_name) AS student_name,
                ep.rank,
                ep.total_score,
                ep.solved_count
            FROM ascendany.exam_participants AS ep
            JOIN ascendany.students AS s
              ON s.student_id = ep.student_id
            LEFT JOIN LATERAL (
                SELECT si.external_name
                FROM ascendany.student_identities si
                WHERE si.student_id = ep.student_id
                  AND si.external_name IS NOT NULL
                  AND BTRIM(si.external_name) <> ''
                ORDER BY si.identity_id DESC
                LIMIT 1
            ) si_name ON TRUE
            WHERE ep.exam_id = %s
              AND ep.absent = FALSE
            ORDER BY ep.rank ASC NULLS LAST, ep.total_score DESC NULLS LAST
            LIMIT %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, limit))
                rows = await cursor.fetchall()

        return [
            ExamParticipantRow(
                student_id=int(row["student_id"]),
                student_name=str(row["student_name"])
                if row.get("student_name")
                else None,
                rank=int(row["rank"]) if row.get("rank") is not None else None,
                total_score=row.get("total_score"),
                solved_count=int(row["solved_count"])
                if row.get("solved_count") is not None
                else None,
            )
            for row in rows
        ]

    async def fetch_exam_student_metrics_for_students(
        self, exam_id: int, student_ids: list[int]
    ) -> list[ExamStudentMetricRow]:
        if not student_ids:
            return []
        query = """
            SELECT
                exam_id,
                student_id,
                knowledge,
                accuracy,
                quality,
                flexibility,
                proficiency
            FROM ascendany.exam_student_metrics
            WHERE exam_id = %s
              AND student_id = ANY(%s::bigint[])
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, student_ids))
                rows = await cursor.fetchall()
        return [
            ExamStudentMetricRow(
                exam_id=int(row["exam_id"]),
                student_id=int(row["student_id"]),
                knowledge=row.get("knowledge"),
                accuracy=row.get("accuracy"),
                quality=row.get("quality"),
                flexibility=row.get("flexibility"),
                proficiency=row.get("proficiency"),
            )
            for row in rows
        ]

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

    async def fetch_exam_metric_history(
        self, student_id: int, limit: int = 50
    ) -> list[ExamMetricHistoryRow]:
        query = """
            SELECT
                esm.exam_id,
                COALESCE(NULLIF(e.title, ''), e.source_path) AS exam_name,
                COALESCE(e.starts_at, esm.computed_at) AS exam_time,
                esm.knowledge,
                esm.accuracy,
                esm.quality,
                esm.flexibility,
                esm.proficiency,
                esm.computed_at
            FROM ascendany.exam_student_metrics AS esm
            JOIN ascendany.exams AS e
              ON e.exam_id = esm.exam_id
            WHERE esm.student_id = %s
            ORDER BY
                COALESCE(e.starts_at, esm.computed_at) DESC,
                esm.exam_id DESC,
                esm.computed_at DESC
            LIMIT %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_id, limit))
                rows = await cursor.fetchall()

        history: list[ExamMetricHistoryRow] = []
        for row in rows:
            exam_time = row.get("exam_time")
            if not isinstance(exam_time, datetime):
                continue
            computed_at = row.get("computed_at")
            history.append(
                ExamMetricHistoryRow(
                    exam_id=int(row["exam_id"]),
                    exam_name=str(row["exam_name"]),
                    exam_time=exam_time,
                    knowledge=row.get("knowledge"),
                    accuracy=row.get("accuracy"),
                    quality=row.get("quality"),
                    flexibility=row.get("flexibility"),
                    proficiency=row.get("proficiency"),
                    computed_at=computed_at
                    if isinstance(computed_at, datetime)
                    else None,
                )
            )
        return history
