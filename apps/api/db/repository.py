from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from decimal import Decimal
import json

import psycopg
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
class LeaderboardEntryRow:
    student_no: str
    username: str
    rating: int
    knowledge: Decimal | float | int | None
    accuracy: Decimal | float | int | None
    quality: Decimal | float | int | None
    flexibility: Decimal | float | int | None
    proficiency: Decimal | float | int | None


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
class ExamParticipantContextRow:
    student_id: int
    position: int
    rank: int | None
    total_score: Decimal | float | int | None
    solved_count: int | None
    total_participants: int


@dataclass(slots=True)
class ExamBandMediansRow:
    sample_size: int
    total_score_median: Decimal | float | int | None
    solved_count_median: Decimal | float | int | None
    knowledge_median: Decimal | float | int | None
    accuracy_median: Decimal | float | int | None
    quality_median: Decimal | float | int | None
    flexibility_median: Decimal | float | int | None
    proficiency_median: Decimal | float | int | None


@dataclass(slots=True)
class ExamPreviousRankerRow:
    student_id: int
    position: int
    rank: int | None
    total_score: Decimal | float | int | None
    solved_count: int | None
    knowledge: Decimal | float | int | None
    accuracy: Decimal | float | int | None
    quality: Decimal | float | int | None
    flexibility: Decimal | float | int | None
    proficiency: Decimal | float | int | None


@dataclass(slots=True)
class AccountRow:
    account_id: int
    username: str
    display_name: str
    password_hash: str
    is_active: bool
    is_admin: bool = False
    provision_source: str = "local"
    local_password_enabled: bool = True
    local_password_set_at: datetime | None = None


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
class ExamAutoAnalysisCacheRow:
    exam_id: int
    student_id: int
    role_id: str
    status: str
    provider_type: str | None
    reply: str
    source: str
    error_message: str | None
    generated_at: datetime | None
    updated_at: datetime | None


@dataclass(slots=True)
class ExamAnalysisExamRow:
    exam_id: int
    exam_name: str
    exam_type: str
    exam_date: datetime | None
    participant_count: int
    generated_count: int
    failed_count: int
    missing_count: int


@dataclass(slots=True)
class ExamAnalysisStudentRow:
    student_entity_id: int
    student_no: str | None
    student_name: str | None
    rank: int | None
    total_score: Decimal | float | int | None
    solved_count: int | None
    rating_delta: int | None
    knowledge: Decimal | float | int | None
    accuracy: Decimal | float | int | None
    quality: Decimal | float | int | None
    flexibility: Decimal | float | int | None
    proficiency: Decimal | float | int | None
    analysis_status: str
    analysis_reply: str
    generated_at: datetime | None
    error_message: str | None


@dataclass(slots=True)
class ExamAnalysisTargetRow:
    student_entity_id: int
    student_no: str | None
    student_name: str | None
    pta_nickname: str | None
    analysis_status: str | None


@dataclass(slots=True)
class AchievementDefinitionRow:
    achievement_code: str
    title: str
    description: str
    source: str
    progress_key: str
    bronze_target: Decimal | float | int
    silver_target: Decimal | float | int
    gold_target: Decimal | float | int
    sort_order: int


@dataclass(slots=True)
class AggregatedAchievementStateRow:
    achievement_code: str
    progress_value: Decimal | float | int
    tier: int


@dataclass(slots=True)
class RefreshTokenRow:
    token_id: int
    account_id: int
    token_hash: str
    expires_at: datetime
    revoked_at: datetime | None
    device_id: str | None


@dataclass(slots=True)
class ExternalIdentityLinkRow:
    link_id: int
    provider: str
    external_user_id: str
    account_id: int
    external_username: str
    external_display_name: str | None
    student_id_snapshot: str
    pta_nickname_snapshot: str | None
    raw_claims: dict[str, object]
    created_at: datetime | None
    updated_at: datetime | None
    last_login_at: datetime | None


class ApiRepository:
    def __init__(self, pool: AsyncConnectionPool) -> None:
        self._pool = pool

    @staticmethod
    def _is_missing_relation_error(exc: Exception) -> bool:
        return getattr(exc, "sqlstate", None) == "42P01"

    @staticmethod
    def _row_to_account(row: dict[str, object]) -> AccountRow:
        return AccountRow(
            account_id=int(row["account_id"]),
            username=str(row["username"]),
            display_name=str(row["display_name"]),
            password_hash=str(row["password_hash"]),
            is_active=bool(row["is_active"]),
            is_admin=bool(row.get("is_admin", False)),
            provision_source=str(row.get("provision_source", "local") or "local"),
            local_password_enabled=bool(
                row.get("local_password_enabled", True)
            ),
            local_password_set_at=row.get("local_password_set_at")
            if isinstance(row.get("local_password_set_at"), datetime)
            else None,
        )

    @staticmethod
    def _row_to_external_identity_link(
        row: dict[str, object],
    ) -> ExternalIdentityLinkRow:
        raw_claims = row.get("raw_claims")
        normalized_claims: dict[str, object] = {}
        if isinstance(raw_claims, dict):
            normalized_claims = raw_claims
        return ExternalIdentityLinkRow(
            link_id=int(row["link_id"]),
            provider=str(row["provider"]),
            external_user_id=str(row["external_user_id"]),
            account_id=int(row["account_id"]),
            external_username=str(row["external_username"]),
            external_display_name=(
                str(row["external_display_name"])
                if row.get("external_display_name") is not None
                else None
            ),
            student_id_snapshot=str(row["student_id_snapshot"]),
            pta_nickname_snapshot=(
                str(row["pta_nickname_snapshot"])
                if row.get("pta_nickname_snapshot") is not None
                else None
            ),
            raw_claims=normalized_claims,
            created_at=row.get("created_at")
            if isinstance(row.get("created_at"), datetime)
            else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
            last_login_at=row.get("last_login_at")
            if isinstance(row.get("last_login_at"), datetime)
            else None,
        )

    async def create_account(
        self,
        username: str,
        display_name: str,
        password_hash: str,
        provision_source: str = "local",
        local_password_enabled: bool = True,
        local_password_set_at: datetime | None = None,
    ) -> AccountRow | None:
        query = """
            INSERT INTO ascendany.user_accounts (
                username,
                display_name,
                password_hash,
                provision_source,
                local_password_enabled,
                local_password_set_at
            )
            VALUES (%s, %s, %s, %s, %s, %s)
            ON CONFLICT DO NOTHING
            RETURNING
                account_id,
                username,
                display_name,
                password_hash,
                is_active,
                is_admin,
                provision_source,
                local_password_enabled,
                local_password_set_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (
                        username,
                        display_name,
                        password_hash,
                        provision_source,
                        local_password_enabled,
                        local_password_set_at,
                    ),
                )
                row = await cursor.fetchone()
        if not row:
            return None
        return self._row_to_account(row)

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
            SELECT
                account_id,
                username,
                display_name,
                password_hash,
                is_active,
                is_admin,
                provision_source,
                local_password_enabled,
                local_password_set_at
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
        return self._row_to_account(row)

    async def fetch_account_by_id(self, account_id: int) -> AccountRow | None:
        query = """
            SELECT
                account_id,
                username,
                display_name,
                password_hash,
                is_active,
                is_admin,
                provision_source,
                local_password_enabled,
                local_password_set_at
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
        return self._row_to_account(row)

    async def update_account_display_name(
        self, account_id: int, display_name: str
    ) -> AccountRow | None:
        query = """
            UPDATE ascendany.user_accounts
            SET display_name = %s, updated_at = now()
            WHERE account_id = %s
              AND NOT EXISTS (
                SELECT 1
                FROM ascendany.user_accounts AS ua
                WHERE ua.account_id <> %s
                  AND ua.display_name_normalized = lower(BTRIM(%s))
              )
            RETURNING
                account_id,
                username,
                display_name,
                password_hash,
                is_active,
                is_admin,
                provision_source,
                local_password_enabled,
                local_password_set_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (display_name, account_id, account_id, display_name),
                )
                row = await cursor.fetchone()
        if not row:
            return None
        return self._row_to_account(row)

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
            pta_nickname=str(row["pta_nickname"]) if row.get("pta_nickname") else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def fetch_account_by_student_id(self, student_id: str) -> AccountRow | None:
        query = """
            SELECT
                ua.account_id,
                ua.username,
                ua.display_name,
                ua.password_hash,
                ua.is_active,
                ua.is_admin,
                ua.provision_source,
                ua.local_password_enabled,
                ua.local_password_set_at
            FROM ascendany.user_accounts AS ua
            INNER JOIN ascendany.user_profiles AS up
                ON up.account_id = ua.account_id
            WHERE BTRIM(COALESCE(up.student_id, '')) = BTRIM(%s)
            ORDER BY ua.account_id ASC
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_id,))
                row = await cursor.fetchone()
        if not row:
            return None
        return self._row_to_account(row)

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
            return AccountProfileRow(
                student_id=None, pta_nickname=None, updated_at=None
            )
        return AccountProfileRow(
            student_id=str(row["student_id"]) if row.get("student_id") else None,
            pta_nickname=str(row["pta_nickname"]) if row.get("pta_nickname") else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def bootstrap_local_password(
        self,
        account_id: int,
        password_hash: str,
    ) -> AccountRow | None:
        query = """
            UPDATE ascendany.user_accounts
            SET password_hash = %s,
                local_password_enabled = TRUE,
                local_password_set_at = now(),
                updated_at = now()
            WHERE account_id = %s
            RETURNING
                account_id,
                username,
                display_name,
                password_hash,
                is_active,
                is_admin,
                provision_source,
                local_password_enabled,
                local_password_set_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (password_hash, account_id))
                row = await cursor.fetchone()
        if not row:
            return None
        return self._row_to_account(row)

    async def fetch_external_identity_link(
        self,
        provider: str,
        external_user_id: str,
    ) -> ExternalIdentityLinkRow | None:
        query = """
            SELECT
                link_id,
                provider,
                external_user_id,
                account_id,
                external_username,
                external_display_name,
                student_id_snapshot,
                pta_nickname_snapshot,
                raw_claims,
                created_at,
                updated_at,
                last_login_at
            FROM ascendany.external_identity_links
            WHERE provider = %s
              AND external_user_id = %s
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (provider, external_user_id))
                row = await cursor.fetchone()
        if not row:
            return None
        return self._row_to_external_identity_link(row)

    async def create_external_identity_link(
        self,
        provider: str,
        external_user_id: str,
        account_id: int,
        external_username: str,
        external_display_name: str | None,
        student_id_snapshot: str,
        pta_nickname_snapshot: str | None,
        raw_claims: dict[str, object],
    ) -> ExternalIdentityLinkRow | None:
        query = """
            INSERT INTO ascendany.external_identity_links (
                provider,
                external_user_id,
                account_id,
                external_username,
                external_display_name,
                student_id_snapshot,
                pta_nickname_snapshot,
                raw_claims,
                last_login_at
            )
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s::jsonb, now())
            ON CONFLICT (provider, external_user_id) DO NOTHING
            RETURNING
                link_id,
                provider,
                external_user_id,
                account_id,
                external_username,
                external_display_name,
                student_id_snapshot,
                pta_nickname_snapshot,
                raw_claims,
                created_at,
                updated_at,
                last_login_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (
                        provider,
                        external_user_id,
                        account_id,
                        external_username,
                        external_display_name,
                        student_id_snapshot,
                        pta_nickname_snapshot,
                        json.dumps(raw_claims),
                    ),
                )
                row = await cursor.fetchone()
        if not row:
            return None
        return self._row_to_external_identity_link(row)

    async def update_external_identity_snapshot(
        self,
        link_id: int,
        external_username: str,
        external_display_name: str | None,
        student_id_snapshot: str,
        pta_nickname_snapshot: str | None,
        raw_claims: dict[str, object],
    ) -> ExternalIdentityLinkRow | None:
        query = """
            UPDATE ascendany.external_identity_links
            SET external_username = %s,
                external_display_name = %s,
                student_id_snapshot = %s,
                pta_nickname_snapshot = %s,
                raw_claims = %s::jsonb,
                updated_at = now(),
                last_login_at = now()
            WHERE link_id = %s
            RETURNING
                link_id,
                provider,
                external_user_id,
                account_id,
                external_username,
                external_display_name,
                student_id_snapshot,
                pta_nickname_snapshot,
                raw_claims,
                created_at,
                updated_at,
                last_login_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (
                        external_username,
                        external_display_name,
                        student_id_snapshot,
                        pta_nickname_snapshot,
                        json.dumps(raw_claims),
                        link_id,
                    ),
                )
                row = await cursor.fetchone()
        if not row:
            return None
        return self._row_to_external_identity_link(row)

    async def cleanup_expired_sso_jtis(self) -> int:
        query = """
            DELETE FROM ascendany.external_sso_jti_consumptions
            WHERE expires_at < now() - interval '1 day'
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query)
                return cursor.rowcount or 0

    async def consume_sso_jti(
        self,
        provider: str,
        jti: str,
        external_user_id: str,
        expires_at: datetime,
        account_id: int | None = None,
    ) -> bool:
        query = """
            INSERT INTO ascendany.external_sso_jti_consumptions (
                provider,
                jti,
                external_user_id,
                account_id,
                expires_at
            )
            VALUES (%s, %s, %s, %s, %s)
            ON CONFLICT (provider, jti) DO NOTHING
            RETURNING provider
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (provider, jti, external_user_id, account_id, expires_at),
                )
                row = await cursor.fetchone()
        return row is not None

    async def attach_consumed_sso_jti_account(
        self,
        provider: str,
        jti: str,
        account_id: int,
    ) -> None:
        query = """
            UPDATE ascendany.external_sso_jti_consumptions
            SET account_id = %s
            WHERE provider = %s
              AND jti = %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, (account_id, provider, jti))

    async def ensure_student_by_student_no(
        self,
        student_no: str,
        student_name: str | None,
        identity_source: str = "user_profile_student_no",
    ) -> int:
        normalized_student_no = student_no.strip()
        if not normalized_student_no:
            raise RuntimeError("student_no is empty")
        canonical_key = f"student_no:{normalized_student_no}"
        async with self._pool.connection() as conn:
            async with conn.transaction():
                async with conn.cursor(row_factory=dict_row) as cursor:
                    await cursor.execute(
                        """
                        SELECT si.student_id
                        FROM ascendany.student_identities AS si
                        WHERE si.external_id = %s
                          AND si.source LIKE %s
                        ORDER BY si.identity_id ASC
                        LIMIT 1
                        """,
                        (normalized_student_no, "%student_no"),
                    )
                    existing = await cursor.fetchone()
                    if existing:
                        student_id = int(existing["student_id"])
                        await cursor.execute(
                            """
                            UPDATE ascendany.students
                            SET canonical_name = COALESCE(%s, canonical_name),
                                updated_at = now()
                            WHERE student_id = %s
                            """,
                            (student_name, student_id),
                        )
                    else:
                        await cursor.execute(
                            """
                            INSERT INTO ascendany.students (canonical_key, canonical_name)
                            VALUES (%s, %s)
                            ON CONFLICT (canonical_key)
                            DO UPDATE SET
                                canonical_name = COALESCE(EXCLUDED.canonical_name, ascendany.students.canonical_name),
                                updated_at = now()
                            RETURNING student_id
                            """,
                            (canonical_key, student_name),
                        )
                        row = await cursor.fetchone()
                        if not row:
                            raise RuntimeError("failed to upsert student")
                        student_id = int(row["student_id"])

                    await cursor.execute(
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
                        (
                            student_id,
                            identity_source,
                            normalized_student_no,
                            student_name,
                        ),
                    )
                    return student_id

    async def upsert_student_identity(
        self,
        student_id: int,
        source: str,
        external_id: str,
        external_name: str | None,
    ) -> None:
        normalized_external_id = external_id.strip()
        if not normalized_external_id:
            raise RuntimeError("external_id is empty")
        query = """
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
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(
                    query,
                    (student_id, source, normalized_external_id, external_name),
                )

    async def claim_student_nickname(
        self,
        student_id: int,
        nickname: str,
        account_id: int | None = None,
    ) -> bool:
        normalized_nickname = nickname.strip()
        if not normalized_nickname:
            raise RuntimeError("nickname is empty")
        async with self._pool.connection() as conn:
            async with conn.transaction():
                async with conn.cursor(row_factory=dict_row) as cursor:
                    await cursor.execute(
                        """
                        SELECT claim_id, student_id
                        FROM ascendany.student_nickname_claims
                        WHERE is_active = TRUE
                          AND lower(BTRIM(nickname)) = lower(BTRIM(%s))
                        FOR UPDATE
                        """,
                        (normalized_nickname,),
                    )
                    current = await cursor.fetchone()
                    if current and int(current["student_id"]) != student_id:
                        return False

                    await cursor.execute(
                        """
                        UPDATE ascendany.student_nickname_claims
                        SET is_active = FALSE,
                            claimed_to = now()
                        WHERE student_id = %s
                          AND is_active = TRUE
                          AND lower(BTRIM(nickname)) <> lower(BTRIM(%s))
                        """,
                        (student_id, normalized_nickname),
                    )

                    if current:
                        await cursor.execute(
                            """
                            UPDATE ascendany.student_nickname_claims
                            SET claimed_by_account_id = COALESCE(%s, claimed_by_account_id),
                                meta = COALESCE(meta, '{}'::jsonb)
                            WHERE claim_id = %s
                            """,
                            (account_id, int(current["claim_id"])),
                        )
                    else:
                        await cursor.execute(
                            """
                            INSERT INTO ascendany.student_nickname_claims (
                                student_id,
                                nickname,
                                is_active,
                                claimed_by_account_id
                            )
                            VALUES (%s, %s, TRUE, %s)
                            """,
                            (student_id, normalized_nickname, account_id),
                        )
        return True

    async def reassign_submissions_by_nicknames(
        self,
        student_id: int,
        nicknames: list[str],
        reason: str = "claim_backfill",
    ) -> int:
        normalized = sorted(
            {item.strip().casefold() for item in nicknames if item.strip()}
        )
        if not normalized:
            return 0
        payload = json.dumps(
            {
                "status": "bound_by_claim",
                "reason": reason,
                "student_id": student_id,
            },
            ensure_ascii=False,
        )
        query = """
            UPDATE ascendany.submissions AS s
            SET student_id = %s,
                raw = jsonb_set(
                    COALESCE(s.raw, '{}'::jsonb),
                    '{linking}',
                    %s::jsonb,
                    true
                )
            WHERE (
                (
                    s.actor_source = 'datastructure_nickname'
                    AND (
                        lower(BTRIM(COALESCE(s.actor_name, ''))) = ANY(%s)
                        OR lower(BTRIM(COALESCE(s.actor_external_id, ''))) = ANY(%s)
                    )
                )
                OR (
                    s.actor_source ~ '^pta_.*_account$'
                    AND lower(BTRIM(COALESCE(s.actor_name, ''))) = ANY(%s)
                )
            )
              AND (
                s.student_id IS DISTINCT FROM %s
                OR COALESCE(s.raw -> 'linking', 'null'::jsonb) IS DISTINCT FROM %s::jsonb
              )
        """
        async with self._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(
                    query,
                    (
                        student_id,
                        payload,
                        normalized,
                        normalized,
                        normalized,
                        student_id,
                        payload,
                    ),
                )
                return cursor.rowcount

    async def find_student_ids_by_submission_nicknames(
        self,
        nicknames: list[str],
    ) -> list[int]:
        normalized = sorted(
            {item.strip().casefold() for item in nicknames if item.strip()}
        )
        if not normalized:
            return []
        query = """
            SELECT DISTINCT s.student_id
            FROM ascendany.submissions AS s
            WHERE (
                (
                    s.actor_source = 'datastructure_nickname'
                    AND (
                        lower(BTRIM(COALESCE(s.actor_name, ''))) = ANY(%s)
                        OR lower(BTRIM(COALESCE(s.actor_external_id, ''))) = ANY(%s)
                    )
                )
                OR (
                    s.actor_source ~ '^pta_.*_account$'
                    AND lower(BTRIM(COALESCE(s.actor_name, ''))) = ANY(%s)
                )
            )
              AND s.student_id IS NOT NULL
            ORDER BY s.student_id ASC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (
                        normalized,
                        normalized,
                        normalized,
                    ),
                )
                rows = await cursor.fetchall()
        return [
            int(row["student_id"]) for row in rows if row.get("student_id") is not None
        ]

    async def merge_achievement_states_for_student(
        self,
        target_student_id: int,
        source_student_ids: list[int],
    ) -> int:
        unique_student_ids = sorted(
            {
                int(student_id)
                for student_id in source_student_ids
                if int(student_id) > 0
            }
        )
        if target_student_id > 0 and target_student_id not in unique_student_ids:
            unique_student_ids.append(target_student_id)
        if not unique_student_ids:
            return 0

        query = """
            WITH aggregated AS (
                SELECT
                    achievement_code,
                    MAX(progress_value) AS progress_value,
                    MAX(tier) AS tier,
                    MIN(achieved_at) FILTER (WHERE achieved_at IS NOT NULL) AS achieved_at
                FROM ascendany.student_achievement_states
                WHERE student_id = ANY(%s::bigint[])
                GROUP BY achievement_code
            )
            INSERT INTO ascendany.student_achievement_states (
                student_id,
                achievement_code,
                progress_value,
                tier,
                achieved_at,
                updated_at
            )
            SELECT
                %s::bigint AS student_id,
                aggregated.achievement_code,
                aggregated.progress_value,
                aggregated.tier,
                CASE
                    WHEN aggregated.tier > 0
                        THEN COALESCE(aggregated.achieved_at, now())
                    ELSE NULL
                END AS achieved_at,
                now() AS updated_at
            FROM aggregated
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
        """
        try:
            async with self._pool.connection() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        query,
                        (unique_student_ids, target_student_id),
                    )
                    return cursor.rowcount or 0
        except psycopg.Error as exc:
            if self._is_missing_relation_error(exc):
                return 0
            raise

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
                        FROM ascendany.student_nickname_claims AS snc
                        JOIN target_students AS ts
                          ON ts.student_id = snc.student_id
                        WHERE snc.is_active = TRUE
                          AND lower(BTRIM(snc.nickname))
                              = lower(NULLIF(BTRIM(up.pta_nickname), ''))
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

    async def fetch_exam_auto_analysis_cache(
        self,
        exam_id: int,
        student_id: int,
        role_id: str,
    ) -> ExamAutoAnalysisCacheRow | None:
        query = """
            SELECT
                exam_id,
                student_id,
                role_id,
                status,
                provider_type,
                reply,
                source,
                error_message,
                generated_at,
                updated_at
            FROM ascendany.exam_auto_analysis_cache
            WHERE exam_id = %s
              AND student_id = %s
              AND role_id = %s
            LIMIT 1
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, student_id, role_id))
                row = await cursor.fetchone()
        if not row:
            return None
        return ExamAutoAnalysisCacheRow(
            exam_id=int(row["exam_id"]),
            student_id=int(row["student_id"]),
            role_id=str(row["role_id"]),
            status=str(row["status"]),
            provider_type=str(row["provider_type"])
            if row.get("provider_type")
            else None,
            reply=str(row["reply"]) if row.get("reply") else "",
            source=str(row["source"]) if row.get("source") else "teacher_exam",
            error_message=str(row["error_message"])
            if row.get("error_message")
            else None,
            generated_at=row.get("generated_at")
            if isinstance(row.get("generated_at"), datetime)
            else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

    async def upsert_exam_auto_analysis_cache(
        self,
        exam_id: int,
        student_id: int,
        role_id: str,
        status: str,
        provider_type: str | None,
        reply: str,
        source: str,
        error_message: str | None,
    ) -> ExamAutoAnalysisCacheRow:
        query = """
            INSERT INTO ascendany.exam_auto_analysis_cache (
                exam_id,
                student_id,
                role_id,
                status,
                provider_type,
                reply,
                source,
                error_message,
                generated_at,
                updated_at
            )
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s, now(), now())
            ON CONFLICT (exam_id, student_id, role_id)
            DO UPDATE SET
                status = EXCLUDED.status,
                provider_type = EXCLUDED.provider_type,
                reply = EXCLUDED.reply,
                source = EXCLUDED.source,
                error_message = EXCLUDED.error_message,
                generated_at = now(),
                updated_at = now()
            RETURNING
                exam_id,
                student_id,
                role_id,
                status,
                provider_type,
                reply,
                source,
                error_message,
                generated_at,
                updated_at
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(
                    query,
                    (
                        exam_id,
                        student_id,
                        role_id,
                        status,
                        provider_type,
                        reply,
                        source,
                        error_message,
                    ),
                )
                row = await cursor.fetchone()
        if not row:
            raise RuntimeError("Failed to upsert exam auto-analysis cache row")
        return ExamAutoAnalysisCacheRow(
            exam_id=int(row["exam_id"]),
            student_id=int(row["student_id"]),
            role_id=str(row["role_id"]),
            status=str(row["status"]),
            provider_type=str(row["provider_type"])
            if row.get("provider_type")
            else None,
            reply=str(row["reply"]) if row.get("reply") else "",
            source=str(row["source"]) if row.get("source") else "teacher_exam",
            error_message=str(row["error_message"])
            if row.get("error_message")
            else None,
            generated_at=row.get("generated_at")
            if isinstance(row.get("generated_at"), datetime)
            else None,
            updated_at=row.get("updated_at")
            if isinstance(row.get("updated_at"), datetime)
            else None,
        )

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
                await cursor.execute(
                    query, (account_id, token_hash, expires_at, device_id)
                )
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
                      AND ep2.absent = FALSE
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

    async def list_exam_analysis_exams(
        self,
        role_id: str = "xiaoD",
    ) -> list[ExamAnalysisExamRow]:
        query = """
            WITH participant_stats AS (
                SELECT
                    ep.exam_id,
                    COUNT(*) FILTER (WHERE ep.absent = FALSE) AS participant_count
                FROM ascendany.exam_participants AS ep
                GROUP BY ep.exam_id
            ),
            cache_stats AS (
                SELECT
                    c.exam_id,
                    COUNT(*) FILTER (WHERE c.status = 'success') AS generated_count,
                    COUNT(*) FILTER (WHERE c.status = 'failed') AS failed_count
                FROM ascendany.exam_auto_analysis_cache AS c
                WHERE c.role_id = %s
                GROUP BY c.exam_id
            )
            SELECT
                e.exam_id,
                COALESCE(NULLIF(BTRIM(e.title), ''), e.source_path) AS exam_name,
                e.exam_type,
                e.starts_at AS exam_date,
                COALESCE(ps.participant_count, 0) AS participant_count,
                COALESCE(cs.generated_count, 0) AS generated_count,
                COALESCE(cs.failed_count, 0) AS failed_count,
                GREATEST(
                    COALESCE(ps.participant_count, 0)
                    - COALESCE(cs.generated_count, 0)
                    - COALESCE(cs.failed_count, 0),
                    0
                ) AS missing_count
            FROM ascendany.exams AS e
            LEFT JOIN participant_stats AS ps
              ON ps.exam_id = e.exam_id
            LEFT JOIN cache_stats AS cs
              ON cs.exam_id = e.exam_id
            WHERE COALESCE(ps.participant_count, 0) > 0
            ORDER BY COALESCE(e.starts_at, e.created_at) DESC, e.exam_id DESC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (role_id,))
                rows = await cursor.fetchall()
        return [
            ExamAnalysisExamRow(
                exam_id=int(row["exam_id"]),
                exam_name=str(row["exam_name"]),
                exam_type=str(row["exam_type"]),
                exam_date=row.get("exam_date")
                if isinstance(row.get("exam_date"), datetime)
                else None,
                participant_count=int(row.get("participant_count", 0)),
                generated_count=int(row.get("generated_count", 0)),
                failed_count=int(row.get("failed_count", 0)),
                missing_count=int(row.get("missing_count", 0)),
            )
            for row in rows
        ]

    async def fetch_exam_analysis_rows(
        self,
        exam_id: int,
        role_id: str = "xiaoD",
    ) -> list[ExamAnalysisStudentRow]:
        query = """
            SELECT
                ep.student_id AS student_entity_id,
                NULLIF(BTRIM(student_no.external_id), '') AS student_no,
                COALESCE(
                    NULLIF(BTRIM(s.canonical_name), ''),
                    NULLIF(BTRIM(active_nickname.nickname), ''),
                    NULLIF(BTRIM(latest_name.external_name), '')
                ) AS student_name,
                ep.rank,
                ep.total_score,
                ep.solved_count,
                rh.delta AS rating_delta,
                esm.knowledge,
                esm.accuracy,
                esm.quality,
                esm.flexibility,
                esm.proficiency,
                COALESCE(cache.status, 'missing') AS analysis_status,
                COALESCE(cache.reply, '') AS analysis_reply,
                cache.generated_at,
                cache.error_message
            FROM ascendany.exam_participants AS ep
            JOIN ascendany.students AS s
              ON s.student_id = ep.student_id
            LEFT JOIN ascendany.exam_student_metrics AS esm
              ON esm.exam_id = ep.exam_id
             AND esm.student_id = ep.student_id
            LEFT JOIN ascendany.rating_history AS rh
              ON rh.exam_id = ep.exam_id
             AND rh.student_id = ep.student_id
            LEFT JOIN ascendany.exam_auto_analysis_cache AS cache
              ON cache.exam_id = ep.exam_id
             AND cache.student_id = ep.student_id
             AND cache.role_id = %s
            LEFT JOIN LATERAL (
                SELECT si.external_id
                FROM ascendany.student_identities AS si
                WHERE si.student_id = ep.student_id
                  AND si.source LIKE %s
                ORDER BY si.identity_id ASC
                LIMIT 1
            ) AS student_no ON TRUE
            LEFT JOIN LATERAL (
                SELECT snc.nickname
                FROM ascendany.student_nickname_claims AS snc
                WHERE snc.student_id = ep.student_id
                  AND snc.is_active = TRUE
                ORDER BY snc.claimed_from DESC, snc.claim_id DESC
                LIMIT 1
            ) AS active_nickname ON TRUE
            LEFT JOIN LATERAL (
                SELECT si.external_name
                FROM ascendany.student_identities AS si
                WHERE si.student_id = ep.student_id
                  AND si.external_name IS NOT NULL
                  AND BTRIM(si.external_name) <> ''
                ORDER BY si.identity_id DESC
                LIMIT 1
            ) AS latest_name ON TRUE
            WHERE ep.exam_id = %s
              AND ep.absent = FALSE
            ORDER BY ep.rank ASC NULLS LAST, ep.total_score DESC NULLS LAST, ep.student_id ASC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (role_id, "%student_no", exam_id))
                rows = await cursor.fetchall()
        return [
            ExamAnalysisStudentRow(
                student_entity_id=int(row["student_entity_id"]),
                student_no=str(row["student_no"]) if row.get("student_no") else None,
                student_name=str(row["student_name"])
                if row.get("student_name")
                else None,
                rank=int(row["rank"]) if row.get("rank") is not None else None,
                total_score=row.get("total_score"),
                solved_count=int(row["solved_count"])
                if row.get("solved_count") is not None
                else None,
                rating_delta=int(row["rating_delta"])
                if row.get("rating_delta") is not None
                else None,
                knowledge=row.get("knowledge"),
                accuracy=row.get("accuracy"),
                quality=row.get("quality"),
                flexibility=row.get("flexibility"),
                proficiency=row.get("proficiency"),
                analysis_status=str(row["analysis_status"]),
                analysis_reply=str(row["analysis_reply"])
                if row.get("analysis_reply")
                else "",
                generated_at=row.get("generated_at")
                if isinstance(row.get("generated_at"), datetime)
                else None,
                error_message=str(row["error_message"])
                if row.get("error_message")
                else None,
            )
            for row in rows
        ]

    async def fetch_exam_analysis_targets(
        self,
        exam_id: int,
        role_id: str = "xiaoD",
    ) -> list[ExamAnalysisTargetRow]:
        query = """
            SELECT
                ep.student_id AS student_entity_id,
                NULLIF(BTRIM(student_no.external_id), '') AS student_no,
                COALESCE(
                    NULLIF(BTRIM(s.canonical_name), ''),
                    NULLIF(BTRIM(active_nickname.nickname), ''),
                    NULLIF(BTRIM(latest_name.external_name), '')
                ) AS student_name,
                COALESCE(
                    NULLIF(BTRIM(active_nickname.nickname), ''),
                    NULLIF(BTRIM(latest_name.external_name), ''),
                    NULLIF(BTRIM(s.canonical_name), '')
                ) AS pta_nickname,
                cache.status AS analysis_status
            FROM ascendany.exam_participants AS ep
            JOIN ascendany.students AS s
              ON s.student_id = ep.student_id
            LEFT JOIN ascendany.exam_auto_analysis_cache AS cache
              ON cache.exam_id = ep.exam_id
             AND cache.student_id = ep.student_id
             AND cache.role_id = %s
            LEFT JOIN LATERAL (
                SELECT si.external_id
                FROM ascendany.student_identities AS si
                WHERE si.student_id = ep.student_id
                  AND si.source LIKE %s
                ORDER BY si.identity_id ASC
                LIMIT 1
            ) AS student_no ON TRUE
            LEFT JOIN LATERAL (
                SELECT snc.nickname
                FROM ascendany.student_nickname_claims AS snc
                WHERE snc.student_id = ep.student_id
                  AND snc.is_active = TRUE
                ORDER BY snc.claimed_from DESC, snc.claim_id DESC
                LIMIT 1
            ) AS active_nickname ON TRUE
            LEFT JOIN LATERAL (
                SELECT si.external_name
                FROM ascendany.student_identities AS si
                WHERE si.student_id = ep.student_id
                  AND si.external_name IS NOT NULL
                  AND BTRIM(si.external_name) <> ''
                ORDER BY si.identity_id DESC
                LIMIT 1
            ) AS latest_name ON TRUE
            WHERE ep.exam_id = %s
              AND ep.absent = FALSE
            ORDER BY ep.rank ASC NULLS LAST, ep.total_score DESC NULLS LAST, ep.student_id ASC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (role_id, "%student_no", exam_id))
                rows = await cursor.fetchall()
        return [
            ExamAnalysisTargetRow(
                student_entity_id=int(row["student_entity_id"]),
                student_no=str(row["student_no"]) if row.get("student_no") else None,
                student_name=str(row["student_name"])
                if row.get("student_name")
                else None,
                pta_nickname=str(row["pta_nickname"])
                if row.get("pta_nickname")
                else None,
                analysis_status=str(row["analysis_status"])
                if row.get("analysis_status")
                else None,
            )
            for row in rows
        ]

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

    async def fetch_exam_participant_context(
        self, exam_id: int, student_ids: list[int]
    ) -> ExamParticipantContextRow | None:
        if not student_ids:
            return None

        query = """
            WITH ranked AS (
                SELECT
                    ep.student_id,
                    ep.rank,
                    ep.total_score,
                    ep.solved_count,
                    row_number() OVER (
                        ORDER BY ep.rank ASC NULLS LAST, ep.total_score DESC NULLS LAST, ep.student_id ASC
                    ) AS pos,
                    count(*) OVER () AS total_participants
                FROM ascendany.exam_participants AS ep
                WHERE ep.exam_id = %s
                  AND ep.absent = FALSE
            )
            SELECT
                student_id,
                rank,
                total_score,
                solved_count,
                pos,
                total_participants
            FROM ranked
            WHERE student_id = ANY(%s::bigint[])
            ORDER BY pos ASC
            LIMIT 1
        """

        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, student_ids))
                row = await cursor.fetchone()
        if not row:
            return None
        return ExamParticipantContextRow(
            student_id=int(row["student_id"]),
            position=int(row["pos"]),
            rank=int(row["rank"]) if row.get("rank") is not None else None,
            total_score=row.get("total_score"),
            solved_count=int(row["solved_count"])
            if row.get("solved_count") is not None
            else None,
            total_participants=int(row.get("total_participants", 0)),
        )

    async def fetch_exam_band_medians(
        self, exam_id: int, pos_start: int, pos_end: int
    ) -> ExamBandMediansRow | None:
        if pos_start <= 0 or pos_end <= 0 or pos_start > pos_end:
            return None

        query = """
            WITH ranked AS (
                SELECT
                    ep.student_id,
                    ep.total_score,
                    ep.solved_count,
                    row_number() OVER (
                        ORDER BY ep.rank ASC NULLS LAST, ep.total_score DESC NULLS LAST, ep.student_id ASC
                    ) AS pos
                FROM ascendany.exam_participants AS ep
                WHERE ep.exam_id = %s
                  AND ep.absent = FALSE
            ),
            band AS (
                SELECT
                    r.student_id,
                    r.total_score,
                    r.solved_count,
                    esm.knowledge,
                    esm.accuracy,
                    esm.quality,
                    esm.flexibility,
                    esm.proficiency
                FROM ranked AS r
                LEFT JOIN ascendany.exam_student_metrics AS esm
                  ON esm.exam_id = %s
                 AND esm.student_id = r.student_id
                WHERE r.pos BETWEEN %s AND %s
            )
            SELECT
                COUNT(*) AS sample_size,
                percentile_cont(0.5) WITHIN GROUP (ORDER BY total_score)
                    FILTER (WHERE total_score IS NOT NULL) AS total_score_median,
                percentile_cont(0.5) WITHIN GROUP (ORDER BY solved_count)
                    FILTER (WHERE solved_count IS NOT NULL) AS solved_count_median,
                percentile_cont(0.5) WITHIN GROUP (ORDER BY knowledge)
                    FILTER (WHERE knowledge IS NOT NULL) AS knowledge_median,
                percentile_cont(0.5) WITHIN GROUP (ORDER BY accuracy)
                    FILTER (WHERE accuracy IS NOT NULL) AS accuracy_median,
                percentile_cont(0.5) WITHIN GROUP (ORDER BY quality)
                    FILTER (WHERE quality IS NOT NULL) AS quality_median,
                percentile_cont(0.5) WITHIN GROUP (ORDER BY flexibility)
                    FILTER (WHERE flexibility IS NOT NULL) AS flexibility_median,
                percentile_cont(0.5) WITHIN GROUP (ORDER BY proficiency)
                    FILTER (WHERE proficiency IS NOT NULL) AS proficiency_median
            FROM band
        """

        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, exam_id, pos_start, pos_end))
                row = await cursor.fetchone()
        if not row or int(row.get("sample_size", 0)) <= 0:
            return None

        return ExamBandMediansRow(
            sample_size=int(row["sample_size"]),
            total_score_median=row.get("total_score_median"),
            solved_count_median=row.get("solved_count_median"),
            knowledge_median=row.get("knowledge_median"),
            accuracy_median=row.get("accuracy_median"),
            quality_median=row.get("quality_median"),
            flexibility_median=row.get("flexibility_median"),
            proficiency_median=row.get("proficiency_median"),
        )

    async def fetch_exam_previous_ranker(
        self, exam_id: int, my_pos: int
    ) -> ExamPreviousRankerRow | None:
        if my_pos <= 1:
            return None

        query = """
            WITH ranked AS (
                SELECT
                    ep.student_id,
                    ep.rank,
                    ep.total_score,
                    ep.solved_count,
                    row_number() OVER (
                        ORDER BY ep.rank ASC NULLS LAST, ep.total_score DESC NULLS LAST, ep.student_id ASC
                    ) AS pos
                FROM ascendany.exam_participants AS ep
                WHERE ep.exam_id = %s
                  AND ep.absent = FALSE
            )
            SELECT
                r.student_id,
                r.rank,
                r.total_score,
                r.solved_count,
                r.pos,
                esm.knowledge,
                esm.accuracy,
                esm.quality,
                esm.flexibility,
                esm.proficiency
            FROM ranked AS r
            LEFT JOIN ascendany.exam_student_metrics AS esm
              ON esm.exam_id = %s
             AND esm.student_id = r.student_id
            WHERE r.pos = %s
            LIMIT 1
        """

        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id, exam_id, my_pos - 1))
                row = await cursor.fetchone()
        if not row:
            return None

        return ExamPreviousRankerRow(
            student_id=int(row["student_id"]),
            position=int(row["pos"]),
            rank=int(row["rank"]) if row.get("rank") is not None else None,
            total_score=row.get("total_score"),
            solved_count=int(row["solved_count"])
            if row.get("solved_count") is not None
            else None,
            knowledge=row.get("knowledge"),
            accuracy=row.get("accuracy"),
            quality=row.get("quality"),
            flexibility=row.get("flexibility"),
            proficiency=row.get("proficiency"),
        )

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
            SELECT
                s.student_id,
                si.external_id AS student_no,
                COALESCE(NULLIF(BTRIM(si.external_name), ''), NULLIF(BTRIM(s.canonical_name), '')) AS student_name
            FROM ascendany.student_nickname_claims AS snc
            JOIN ascendany.students AS s
              ON s.student_id = snc.student_id
            JOIN LATERAL (
                SELECT external_id, external_name
                FROM ascendany.student_identities
                WHERE student_id = s.student_id
                  AND source LIKE %s
                ORDER BY identity_id ASC
                LIMIT 1
            ) AS si ON TRUE
            WHERE snc.is_active = TRUE
              AND lower(BTRIM(snc.nickname)) = lower(BTRIM(%s))
            ORDER BY s.student_id ASC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, ("%student_no", student_name))
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

    async def exists_learning_records_for_student_ids(
        self, student_ids: list[int]
    ) -> bool:
        if not student_ids:
            return False
        query = """
            SELECT (
                EXISTS (
                    SELECT 1
                    FROM ascendany.rating_history AS rh
                    WHERE rh.student_id = ANY(%s::bigint[])
                    LIMIT 1
                )
                OR EXISTS (
                    SELECT 1
                    FROM ascendany.exam_student_metrics AS esm
                    WHERE esm.student_id = ANY(%s::bigint[])
                    LIMIT 1
                )
                OR EXISTS (
                    SELECT 1
                    FROM ascendany.submissions AS s
                    WHERE s.student_id = ANY(%s::bigint[])
                    LIMIT 1
                )
            ) AS has_records
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_ids, student_ids, student_ids))
                row = await cursor.fetchone()
        if not row:
            return False
        return bool(row["has_records"])

    async def _fetch_distinct_student_entity_ids_by_exam(
        self, exam_id: int
    ) -> list[int]:
        query = """
            SELECT DISTINCT rh.student_id
            FROM ascendany.rating_history AS rh
            WHERE rh.exam_id = %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (exam_id,))
                rows = await cursor.fetchall()
        return [int(r["student_id"]) for r in rows if r.get("student_id") is not None]

    async def _fetch_student_nos_for_student_ids(
        self, student_ids: list[int]
    ) -> list[str]:
        if not student_ids:
            return []
        query = """
            SELECT DISTINCT si.external_id
            FROM ascendany.student_identities AS si
            WHERE si.student_id = ANY(%s::bigint[])
              AND si.source LIKE %s
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_ids, "%student_no"))
                rows = await cursor.fetchall()
        out: list[str] = []
        for r in rows:
            v = (r.get("external_id") or "").strip()
            if v:
                out.append(v)
        return out

    async def _fetch_active_nicknames_for_student_ids(
        self, student_ids: list[int]
    ) -> list[str]:
        if not student_ids:
            return []
        query = """
            SELECT DISTINCT lower(BTRIM(snc.nickname)) AS nickname
            FROM ascendany.student_nickname_claims AS snc
            WHERE snc.is_active = TRUE
              AND snc.student_id = ANY(%s::bigint[])
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, (student_ids,))
                rows = await cursor.fetchall()
        out: list[str] = []
        for r in rows:
            v = (r.get("nickname") or "").strip().lower()
            if v:
                out.append(v)
        return out

    async def fetch_achievement_definitions(
        self,
        source: str | None = None,
        enabled_only: bool = True,
    ) -> list[AchievementDefinitionRow]:
        query = """
            SELECT
                achievement_code,
                title,
                description,
                source,
                progress_key,
                bronze_target,
                silver_target,
                gold_target,
                sort_order
            FROM ascendany.achievement_definitions
            WHERE 1 = 1
        """
        params: list[object] = []
        if source:
            query += " AND source = %s"
            params.append(source)
        if enabled_only:
            query += " AND is_enabled = TRUE"
        query += " ORDER BY sort_order ASC, achievement_code ASC"

        try:
            async with self._pool.connection() as conn:
                async with conn.cursor(row_factory=dict_row) as cursor:
                    await cursor.execute(query, params)
                    rows = await cursor.fetchall()
        except psycopg.Error as exc:
            if self._is_missing_relation_error(exc):
                return []
            raise
        return [
            AchievementDefinitionRow(
                achievement_code=str(row["achievement_code"]),
                title=str(row["title"]),
                description=str(row["description"]),
                source=str(row["source"]),
                progress_key=str(row["progress_key"]),
                bronze_target=row["bronze_target"],
                silver_target=row["silver_target"],
                gold_target=row["gold_target"],
                sort_order=int(row["sort_order"]),
            )
            for row in rows
        ]

    async def fetch_aggregated_achievement_states(
        self,
        student_ids: list[int],
    ) -> list[AggregatedAchievementStateRow]:
        unique_student_ids = sorted(set(student_ids))
        if not unique_student_ids:
            return []
        query = """
            SELECT
                achievement_code,
                MAX(progress_value) AS progress_value,
                MAX(tier) AS tier
            FROM ascendany.student_achievement_states
            WHERE student_id = ANY(%s::bigint[])
            GROUP BY achievement_code
        """
        try:
            async with self._pool.connection() as conn:
                async with conn.cursor(row_factory=dict_row) as cursor:
                    await cursor.execute(query, (unique_student_ids,))
                    rows = await cursor.fetchall()
        except psycopg.Error as exc:
            if self._is_missing_relation_error(exc):
                return []
            raise
        return [
            AggregatedAchievementStateRow(
                achievement_code=str(row["achievement_code"]),
                progress_value=row["progress_value"],
                tier=int(row.get("tier", 0)),
            )
            for row in rows
        ]

    async def fetch_student_activity_counters(
        self,
        student_ids: list[int],
    ) -> dict[int, int]:
        unique_student_ids = sorted(set(student_ids))
        if not unique_student_ids:
            return {}
        query = """
            SELECT student_id, ai_dialogue_count
            FROM ascendany.student_activity_counters
            WHERE student_id = ANY(%s::bigint[])
        """
        try:
            async with self._pool.connection() as conn:
                async with conn.cursor(row_factory=dict_row) as cursor:
                    await cursor.execute(query, (unique_student_ids,))
                    rows = await cursor.fetchall()
        except psycopg.Error as exc:
            if self._is_missing_relation_error(exc):
                return {}
            raise
        return {
            int(row["student_id"]): int(row.get("ai_dialogue_count", 0)) for row in rows
        }

    async def increment_ai_dialogue_count(
        self,
        student_ids: list[int],
        delta: int = 1,
    ) -> None:
        unique_student_ids = sorted(set(student_ids))
        if not unique_student_ids or delta <= 0:
            return
        query = """
            WITH target_students AS (
                SELECT DISTINCT unnest(%s::bigint[]) AS student_id
            )
            INSERT INTO ascendany.student_activity_counters (
                student_id,
                ai_dialogue_count,
                updated_at
            )
            SELECT student_id, %s, now()
            FROM target_students
            ON CONFLICT (student_id)
            DO UPDATE SET
                ai_dialogue_count =
                    ascendany.student_activity_counters.ai_dialogue_count
                    + EXCLUDED.ai_dialogue_count,
                updated_at = now()
        """
        try:
            async with self._pool.connection() as conn:
                async with conn.cursor() as cursor:
                    await cursor.execute(query, (unique_student_ids, delta))
        except psycopg.Error as exc:
            if self._is_missing_relation_error(exc):
                return
            raise

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

    async def fetch_student_leaderboard(self) -> list[LeaderboardEntryRow]:
        query = """
            SELECT
                profile.student_no,
                profile.username,
                scm.rating,
                scm.knowledge,
                scm.accuracy,
                scm.quality,
                scm.flexibility,
                scm.proficiency
            FROM (
                SELECT
                    ua.account_id,
                    ua.display_name AS username,
                    NULLIF(BTRIM(up.student_id), '') AS student_no
                FROM ascendany.user_accounts AS ua
                JOIN ascendany.user_profiles AS up
                  ON up.account_id = ua.account_id
                WHERE ua.is_active = TRUE
                  AND LEFT(lower(ua.username), 5) <> 'test_'
                  AND LEFT(lower(ua.display_name), 5) <> 'test_'
                  AND NULLIF(BTRIM(up.student_id), '') ~ '^[0-9]{4,}$'
            ) AS profile
            JOIN LATERAL (
                SELECT si.student_id
                FROM ascendany.student_identities AS si
                LEFT JOIN ascendany.student_current_metrics AS scm2
                  ON scm2.student_id = si.student_id
                WHERE si.external_id = profile.student_no
                  AND si.source LIKE %s
                ORDER BY
                    COALESCE(scm2.updated_at, TIMESTAMPTZ 'epoch') DESC,
                    si.identity_id ASC
                LIMIT 1
            ) AS identity ON TRUE
            JOIN ascendany.student_current_metrics AS scm
              ON scm.student_id = identity.student_id
            ORDER BY
                scm.rating DESC,
                COALESCE(scm.knowledge, 0) DESC,
                profile.username ASC,
                profile.account_id ASC
        """
        async with self._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, ("%student_no",))
                rows = await cursor.fetchall()
        return [
            LeaderboardEntryRow(
                student_no=str(row["student_no"]),
                username=str(row["username"]),
                rating=int(row["rating"]),
                knowledge=row.get("knowledge"),
                accuracy=row.get("accuracy"),
                quality=row.get("quality"),
                flexibility=row.get("flexibility"),
                proficiency=row.get("proficiency"),
            )
            for row in rows
        ]

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
