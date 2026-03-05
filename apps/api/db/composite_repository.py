from __future__ import annotations

from typing import Any

from ..core.errors import AppError
from .mysql_user_repository import MySQLUserRepository
from .repository import ApiRepository


class CompositeRepository:
    """Delegates user/auth-related methods to MySQL; others to PostgreSQL."""

    def __init__(self, *, pg: ApiRepository, mysql: MySQLUserRepository) -> None:
        self._pg = pg
        self._mysql = mysql

    def __getattr__(self, name: str) -> Any:
        # Fallback for all non-user methods.
        return getattr(self._pg, name)

    # --- Auth/account/profile ---
    async def fetch_account_by_username(self, username: str):
        return await self._mysql.fetch_account_by_username(username)

    async def fetch_account_by_id(self, account_id: int):
        return await self._mysql.fetch_account_by_id(account_id)

    async def fetch_account_profile(self, account_id: int):
        return await self._mysql.fetch_account_profile(account_id)

    async def upsert_account_profile(
        self, account_id: int, student_id: str | None, pta_nickname: str | None
    ):
        return await self._mysql.upsert_account_profile(
            account_id, student_id, pta_nickname
        )

    async def touch_account_login(self, account_id: int) -> None:
        return await self._mysql.touch_account_login(account_id)

    async def update_account_display_name(self, account_id: int, display_name: str):
        return await self._mysql.update_account_display_name(account_id, display_name)

    # --- Refresh tokens ---
    async def insert_refresh_token(
        self, account_id: int, token_hash: str, expires_at, device_id: str | None
    ):
        return await self._mysql.insert_refresh_token(
            account_id=account_id,
            token_hash=token_hash,
            expires_at=expires_at,
            device_id=device_id,
        )

    async def fetch_refresh_token_by_hash(self, token_hash: str):
        return await self._mysql.fetch_refresh_token_by_hash(token_hash)

    async def revoke_refresh_token_by_id(self, token_id: int) -> None:
        return await self._mysql.revoke_refresh_token_by_id(token_id)

    async def revoke_refresh_token_by_hash(
        self, account_id: int, token_hash: str
    ) -> bool:
        return await self._mysql.revoke_refresh_token_by_hash(account_id, token_hash)

    async def revoke_all_refresh_tokens(self, account_id: int) -> int:
        return await self._mysql.revoke_all_refresh_tokens(account_id)

    # --- Auto-analysis cache (per user) ---
    async def fetch_auto_analysis_cache(
        self, account_id: int, exam_id: int, role_id: str
    ):
        return await self._mysql.fetch_auto_analysis_cache(account_id, exam_id, role_id)

    async def upsert_auto_analysis_cache(
        self,
        account_id: int,
        exam_id: int,
        role_id: str,
        provider_type: str | None,
        reply: str,
        source: str,
    ):
        return await self._mysql.upsert_auto_analysis_cache(
            account_id=account_id,
            exam_id=exam_id,
            role_id=role_id,
            provider_type=provider_type,
            reply=reply,
            source=source,
        )

    async def mark_auto_analysis_delivered(
        self, account_id: int, exam_id: int, role_id: str
    ) -> None:
        return await self._mysql.mark_auto_analysis_delivered(
            account_id, exam_id, role_id
        )

    async def fetch_auto_analysis_candidates_by_exam(
        self, exam_id: int, limit: int = 2000
    ):
        # Derive candidate identity filters from PostgreSQL, then pull accounts from MySQL.
        if limit <= 0:
            return []

        student_entity_ids = await self._pg._fetch_distinct_student_entity_ids_by_exam(
            exam_id
        )
        if not student_entity_ids:
            return []

        student_nos = await self._pg._fetch_student_nos_for_student_ids(
            student_entity_ids
        )
        nicknames = await self._pg._fetch_active_nicknames_for_student_ids(
            student_entity_ids
        )
        if not student_nos and not nicknames:
            return []

        return await self._mysql.fetch_users_by_student_ids_or_full_names(
            student_ids=student_nos,
            full_names_normalized=nicknames,
            limit=limit,
        )

    # --- Unsupported operations in external auth mode ---
    async def create_account(self, *args, **kwargs):
        raise AppError(
            status_code=503,
            code="AUTH_EXTERNAL_PROVIDER_UNSUPPORTED",
            message="Account creation is disabled when using external auth provider.",
        )

    async def delete_account(self, *args, **kwargs):
        raise AppError(
            status_code=503,
            code="AUTH_EXTERNAL_PROVIDER_UNSUPPORTED",
            message="Account deletion is disabled when using external auth provider.",
        )
