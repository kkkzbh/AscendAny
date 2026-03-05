from __future__ import annotations

import json
import asyncio
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, Callable, Iterable


@dataclass(frozen=True, slots=True)
class App01MySQLConfig:
    host: str
    port: int
    user: str
    password: str
    database: str
    charset: str = "utf8mb4"


def _resolve_db_config_path(explicit_path: str | None) -> Path | None:
    if explicit_path:
        path = Path(explicit_path).expanduser()
        return path

    cwd = Path.cwd().resolve()
    for base in (cwd, *cwd.parents[:6]):
        candidate = base / "config" / "db_config.json"
        if candidate.exists():
            return candidate
    return None


def load_app01_mysql_config(
    *,
    explicit_path: str | None,
    env_get: Callable[[str, str], str],
) -> App01MySQLConfig:
    # Allow overriding via individual env vars (useful for containers/systemd).
    env_host = env_get("ASCENDANY_APP01_DB_HOST", "").strip()
    env_user = env_get("ASCENDANY_APP01_DB_USER", "").strip()
    env_password = env_get("ASCENDANY_APP01_DB_PASSWORD", "")
    env_name = env_get("ASCENDANY_APP01_DB_NAME", "").strip()
    env_port_raw = env_get("ASCENDANY_APP01_DB_PORT", "").strip()
    env_charset = env_get("ASCENDANY_APP01_DB_CHARSET", "").strip()
    if env_host and env_user and env_name:
        port = int(env_port_raw) if env_port_raw else 3306
        return App01MySQLConfig(
            host=env_host,
            port=port,
            user=env_user,
            password=str(env_password or ""),
            database=env_name,
            charset=env_charset or "utf8mb4",
        )

    config_path = _resolve_db_config_path(explicit_path)
    if config_path is None:
        raise RuntimeError(
            "MySQL db_config.json not found. Set ASCENDANY_APP01_DB_HOST/... or ASCENDANY_APP01_DB_CONFIG_PATH."
        )
    raw = json.loads(config_path.read_text(encoding="utf-8"))
    cfg = App01MySQLConfig(
        host=str(raw.get("host", "127.0.0.1")),
        port=int(raw.get("port", 3306)),
        user=str(raw.get("user", "")),
        password=str(raw.get("password", "")),
        database=str(raw.get("database", "")),
        charset=str(raw.get("charset", "utf8mb4")),
    )

    if not cfg.user or not cfg.database:
        raise RuntimeError(
            f"Invalid MySQL db_config.json: missing user/database at {config_path}"
        )
    return cfg


class MySQLUserRepository:
    """User/auth related data backed by MySQL (Django app01_user + AscendAny ext tables)."""

    def __init__(self, cfg: App01MySQLConfig) -> None:
        self._cfg = cfg

    def _connect(self):
        import pymysql

        return pymysql.connect(
            host=self._cfg.host,
            port=self._cfg.port,
            user=self._cfg.user,
            password=self._cfg.password,
            database=self._cfg.database,
            charset=self._cfg.charset,
            autocommit=True,
            cursorclass=pymysql.cursors.DictCursor,
        )

    async def _run(self, fn, *args, **kwargs):
        return await asyncio.to_thread(fn, *args, **kwargs)

    def _fetchone(self, query: str, params: tuple[Any, ...]) -> dict[str, Any] | None:
        conn = self._connect()
        try:
            with conn.cursor() as cur:
                cur.execute(query, params)
                return cur.fetchone()
        finally:
            conn.close()

    def _fetchall(self, query: str, params: tuple[Any, ...]) -> list[dict[str, Any]]:
        conn = self._connect()
        try:
            with conn.cursor() as cur:
                cur.execute(query, params)
                return list(cur.fetchall() or [])
        finally:
            conn.close()

    def _execute(self, query: str, params: tuple[Any, ...]) -> tuple[int, int]:
        """Returns (rowcount, lastrowid)."""
        conn = self._connect()
        try:
            with conn.cursor() as cur:
                cur.execute(query, params)
                lastrowid = int(getattr(cur, "lastrowid", 0) or 0)
                rowcount = int(getattr(cur, "rowcount", 0) or 0)
                return rowcount, lastrowid
        finally:
            conn.close()

    async def fetch_account_by_username(self, username: str):
        from .repository import AccountRow

        row = await self._run(
            self._fetchone,
            """
            SELECT
                u.id AS account_id,
                u.username AS username,
                u.password AS password_hash,
                u.full_name AS full_name,
                u.student_id AS student_id,
                COALESCE(NULLIF(e.display_name_override, ''), u.full_name, u.username) AS display_name,
                COALESCE(e.is_active, 1) AS is_active,
                COALESCE(e.is_admin, 0) AS is_admin
            FROM app01_user AS u
            LEFT JOIN ascendany_user_ext AS e
              ON e.user_id = u.id
            WHERE u.username = %s
            LIMIT 1
            """,
            (username,),
        )
        if not row:
            return None
        return AccountRow(
            account_id=int(row["account_id"]),
            username=str(row["username"]),
            display_name=str(
                row.get("display_name") or row.get("full_name") or row["username"]
            ),
            password_hash=str(row["password_hash"]),
            is_active=bool(row.get("is_active", True)),
            is_admin=bool(row.get("is_admin", False)),
        )

    async def fetch_account_by_id(self, account_id: int):
        from .repository import AccountRow

        row = await self._run(
            self._fetchone,
            """
            SELECT
                u.id AS account_id,
                u.username AS username,
                u.password AS password_hash,
                u.full_name AS full_name,
                u.student_id AS student_id,
                COALESCE(NULLIF(e.display_name_override, ''), u.full_name, u.username) AS display_name,
                COALESCE(e.is_active, 1) AS is_active,
                COALESCE(e.is_admin, 0) AS is_admin
            FROM app01_user AS u
            LEFT JOIN ascendany_user_ext AS e
              ON e.user_id = u.id
            WHERE u.id = %s
            LIMIT 1
            """,
            (account_id,),
        )
        if not row:
            return None
        return AccountRow(
            account_id=int(row["account_id"]),
            username=str(row["username"]),
            display_name=str(
                row.get("display_name") or row.get("full_name") or row["username"]
            ),
            password_hash=str(row["password_hash"]),
            is_active=bool(row.get("is_active", True)),
            is_admin=bool(row.get("is_admin", False)),
        )

    async def fetch_account_profile(self, account_id: int):
        from .repository import AccountProfileRow

        row = await self._run(
            self._fetchone,
            """
            SELECT
                u.student_id AS student_id,
                u.full_name AS pta_nickname,
                e.updated_at AS updated_at
            FROM app01_user AS u
            LEFT JOIN ascendany_user_ext AS e
              ON e.user_id = u.id
            WHERE u.id = %s
            LIMIT 1
            """,
            (account_id,),
        )
        if not row:
            return None
        updated_at = row.get("updated_at")
        return AccountProfileRow(
            student_id=str(row["student_id"]) if row.get("student_id") else None,
            pta_nickname=str(row["pta_nickname"]) if row.get("pta_nickname") else None,
            updated_at=updated_at if isinstance(updated_at, datetime) else None,
        )

    async def upsert_account_profile(
        self, account_id: int, student_id: str | None, pta_nickname: str | None
    ):
        # External mode: student_id/full_name are owned by Django; treat profile as read-only.
        profile = await self.fetch_account_profile(account_id)
        if profile is None:
            from .repository import AccountProfileRow

            return AccountProfileRow(
                student_id=None, pta_nickname=None, updated_at=None
            )
        return profile

    async def touch_account_login(self, account_id: int) -> None:
        await self._run(
            self._execute,
            """
            INSERT INTO ascendany_user_ext (
                user_id, display_name_override, is_active, is_admin, last_login_at, created_at, updated_at
            )
            VALUES (%s, '', 1, 0, NOW(6), NOW(6), NOW(6))
            ON DUPLICATE KEY UPDATE
                last_login_at = NOW(6),
                updated_at = NOW(6)
            """,
            (account_id,),
        )

    async def update_account_display_name(self, account_id: int, display_name: str):
        # Store display name override in the ext table.
        await self._run(
            self._execute,
            """
            INSERT INTO ascendany_user_ext (
                user_id, display_name_override, is_active, is_admin, last_login_at, created_at, updated_at
            )
            VALUES (%s, %s, 1, 0, NULL, NOW(6), NOW(6))
            ON DUPLICATE KEY UPDATE
                display_name_override = VALUES(display_name_override),
                updated_at = NOW(6)
            """,
            (account_id, display_name),
        )
        return await self.fetch_account_by_id(account_id)

    async def insert_refresh_token(
        self,
        account_id: int,
        token_hash: str,
        expires_at: datetime,
        device_id: str | None,
    ):
        from .repository import RefreshTokenRow

        _rowcount, token_id = await self._run(
            self._execute,
            """
            INSERT INTO ascendany_user_refresh_tokens (
                user_id, token_hash, expires_at, revoked_at, device_id, created_at
            )
            VALUES (%s, %s, %s, NULL, %s, NOW(6))
            """,
            (account_id, token_hash, expires_at, (device_id or "")),
        )
        return RefreshTokenRow(
            token_id=int(token_id),
            account_id=int(account_id),
            token_hash=str(token_hash),
            expires_at=expires_at,
            revoked_at=None,
            device_id=str(device_id) if device_id else None,
        )

    async def fetch_refresh_token_by_hash(self, token_hash: str):
        from .repository import RefreshTokenRow

        row = await self._run(
            self._fetchone,
            """
            SELECT
                id AS token_id,
                user_id AS account_id,
                token_hash,
                expires_at,
                revoked_at,
                device_id
            FROM ascendany_user_refresh_tokens
            WHERE token_hash = %s
            LIMIT 1
            """,
            (token_hash,),
        )
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
            device_id=str(row.get("device_id")) if row.get("device_id") else None,
        )

    async def revoke_refresh_token_by_id(self, token_id: int) -> None:
        await self._run(
            self._execute,
            """
            UPDATE ascendany_user_refresh_tokens
            SET revoked_at = NOW(6)
            WHERE id = %s
              AND revoked_at IS NULL
            """,
            (token_id,),
        )

    async def revoke_refresh_token_by_hash(
        self, account_id: int, token_hash: str
    ) -> bool:
        rowcount, _ = await self._run(
            self._execute,
            """
            UPDATE ascendany_user_refresh_tokens
            SET revoked_at = NOW(6)
            WHERE user_id = %s
              AND token_hash = %s
              AND revoked_at IS NULL
            """,
            (account_id, token_hash),
        )
        return rowcount > 0

    async def revoke_all_refresh_tokens(self, account_id: int) -> int:
        rowcount, _ = await self._run(
            self._execute,
            """
            UPDATE ascendany_user_refresh_tokens
            SET revoked_at = NOW(6)
            WHERE user_id = %s
              AND revoked_at IS NULL
            """,
            (account_id,),
        )
        return int(rowcount)

    async def fetch_auto_analysis_cache(
        self, account_id: int, exam_id: int, role_id: str
    ):
        from .repository import AutoAnalysisCacheRow

        row = await self._run(
            self._fetchone,
            """
            SELECT
                user_id AS account_id,
                exam_id,
                role_id,
                NULLIF(provider_type, '') AS provider_type,
                reply,
                source,
                generated_at,
                delivered_at,
                updated_at
            FROM ascendany_user_auto_analysis_cache
            WHERE user_id = %s
              AND exam_id = %s
              AND role_id = %s
            LIMIT 1
            """,
            (account_id, exam_id, role_id),
        )
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
            source=str(row.get("source") or "online"),
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
    ):
        from .repository import AutoAnalysisCacheRow

        await self._run(
            self._execute,
            """
            INSERT INTO ascendany_user_auto_analysis_cache (
                user_id, exam_id, role_id, provider_type, reply, source,
                generated_at, delivered_at, updated_at
            )
            VALUES (%s, %s, %s, %s, %s, %s, NOW(6), NULL, NOW(6))
            ON DUPLICATE KEY UPDATE
                provider_type = VALUES(provider_type),
                reply = VALUES(reply),
                source = VALUES(source),
                generated_at = NOW(6),
                updated_at = NOW(6)
            """,
            (
                account_id,
                exam_id,
                role_id,
                (provider_type or ""),
                reply,
                source,
            ),
        )
        row = await self.fetch_auto_analysis_cache(account_id, exam_id, role_id)
        if row is None:
            raise RuntimeError("Failed to upsert auto-analysis cache row")
        return row

    async def mark_auto_analysis_delivered(
        self, account_id: int, exam_id: int, role_id: str
    ) -> None:
        await self._run(
            self._execute,
            """
            UPDATE ascendany_user_auto_analysis_cache
            SET delivered_at = COALESCE(delivered_at, NOW(6)),
                updated_at = NOW(6)
            WHERE user_id = %s
              AND exam_id = %s
              AND role_id = %s
            """,
            (account_id, exam_id, role_id),
        )

    async def fetch_users_by_student_ids_or_full_names(
        self,
        student_ids: list[str],
        full_names_normalized: list[str],
        limit: int,
    ):
        from .repository import AutoAnalysisCandidateRow

        student_ids = [s.strip() for s in student_ids if str(s or "").strip()]
        full_names_normalized = [
            s.strip().lower() for s in full_names_normalized if str(s or "").strip()
        ]
        if limit <= 0:
            return []
        if not student_ids and not full_names_normalized:
            return []

        # Chunk to keep SQL size reasonable.
        max_chunk = 500
        results: list[AutoAnalysisCandidateRow] = []

        def _query_chunk(
            sids: list[str], nicks: list[str], remaining: int
        ) -> list[dict[str, Any]]:
            clauses: list[str] = []
            params: list[Any] = []
            if sids:
                clauses.append("u.student_id IN (%s)" % (",".join(["%s"] * len(sids))))
                params.extend(sids)
            if nicks:
                clauses.append(
                    "LOWER(TRIM(u.full_name)) IN (%s)" % (",".join(["%s"] * len(nicks)))
                )
                params.extend(nicks)
            where = " OR ".join(clauses)
            sql = f"""
                SELECT
                    u.id AS account_id,
                    u.student_id AS student_id,
                    u.full_name AS pta_nickname
                FROM app01_user AS u
                LEFT JOIN ascendany_user_ext AS e
                  ON e.user_id = u.id
                WHERE COALESCE(e.is_active, 1) = 1
                  AND ({where})
                ORDER BY u.id ASC
                LIMIT %s
            """
            params.append(int(remaining))
            return self._fetchall(sql, tuple(params))

        i = 0
        j = 0
        while len(results) < limit and (
            i < len(student_ids) or j < len(full_names_normalized)
        ):
            sid_chunk = student_ids[i : i + max_chunk]
            nick_chunk = full_names_normalized[j : j + max_chunk]
            i += len(sid_chunk)
            j += len(nick_chunk)

            rows = await self._run(
                _query_chunk, sid_chunk, nick_chunk, limit - len(results)
            )
            for row in rows:
                results.append(
                    AutoAnalysisCandidateRow(
                        account_id=int(row["account_id"]),
                        student_id=str(row["student_id"])
                        if row.get("student_id")
                        else None,
                        pta_nickname=str(row["pta_nickname"])
                        if row.get("pta_nickname")
                        else None,
                    )
                )

        # De-dup by account_id (in case both clauses match).
        seen: set[int] = set()
        deduped: list[AutoAnalysisCandidateRow] = []
        for item in results:
            if item.account_id in seen:
                continue
            seen.add(item.account_id)
            deduped.append(item)
            if len(deduped) >= limit:
                break
        return deduped
