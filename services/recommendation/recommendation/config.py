from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class DatabaseConfig:
    dsn: str


def load_db_config() -> DatabaseConfig:
    dsn = os.getenv("ASCENDANY_DB_DSN", "").strip()
    if dsn:
        return DatabaseConfig(dsn=dsn)

    host = os.getenv("ASCENDANY_DB_HOST", "127.0.0.1")
    port = os.getenv("ASCENDANY_DB_PORT", "6432")
    dbname = os.getenv("ASCENDANY_DB_NAME", "AscendAny")
    user = os.getenv("ASCENDANY_DB_USER", "AscendAny")
    password_env = os.getenv("ASCENDANY_DB_PASSWORD_ENV", "ASCENDANY_DB_PASSWORD")
    password = os.getenv(password_env, "").strip()
    parts = [
        f"host={host}",
        f"port={port}",
        f"dbname={dbname}",
        f"user={user}",
    ]
    if password:
        parts.append(f"password={password}")
    return DatabaseConfig(dsn=" ".join(parts))
