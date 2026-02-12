from __future__ import annotations

from typing import Any

import psycopg

from .config import Settings


def connect(settings: Settings) -> psycopg.Connection[Any]:
    dsn = settings.db.build_dsn()
    return psycopg.connect(dsn, prepare_threshold=None)
