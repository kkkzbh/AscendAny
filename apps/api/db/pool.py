from __future__ import annotations

from psycopg_pool import AsyncConnectionPool

from ..core.config import Settings


def build_pool(settings: Settings) -> AsyncConnectionPool:
    max_size = max(settings.db.pool_min_size, settings.db.pool_max_size)
    return AsyncConnectionPool(
        conninfo=settings.db.build_dsn(),
        min_size=settings.db.pool_min_size,
        max_size=max_size,
        timeout=settings.db.pool_timeout_seconds,
        open=False,
        kwargs={
            "autocommit": True,
            "prepare_threshold": None,
        },
    )
