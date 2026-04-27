from __future__ import annotations

import asyncio
import logging
import os
import sys
from contextlib import asynccontextmanager
from typing import Any

import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from .api.routes import (
    admin_router,
    auth_router,
    chat_router,
    exam_analysis_router,
    health_router,
    import_router,
    meta_router,
    students_router,
)
from .core.config import Settings, load_settings
from .core.errors import AppError
from .db.pool import build_pool
from .db.repository import ApiRepository
from .services.auth import AuthService
from .services.llm import LLMService

logger = logging.getLogger(__name__)


if sys.platform == "win32":
    # psycopg async connections require the selector event loop on Windows.
    # This is a no-op on non-Windows platforms.
    policy = getattr(asyncio, "WindowsSelectorEventLoopPolicy", None)
    if policy is not None:
        try:
            asyncio.set_event_loop_policy(policy())
        except Exception:
            pass


def create_app(
    settings: Settings | None = None,
    repository: Any | None = None,
    llm_service: Any | None = None,
) -> FastAPI:
    app_settings = settings or load_settings()

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        app.state.settings = app_settings
        managed_pool = None
        managed_http_client = None

        if repository is None:
            managed_pool = build_pool(app_settings)
            await managed_pool.open()
            pg_repo = ApiRepository(managed_pool)

            provider = (app_settings.auth.provider or "internal").strip().lower()
            if provider in {"", "internal"}:
                app.state.repository = pg_repo
            else:
                from .db.composite_repository import CompositeRepository
                from .db.mysql_user_repository import (
                    MySQLUserRepository,
                    load_app01_mysql_config,
                )

                mysql_cfg = load_app01_mysql_config(
                    explicit_path=app_settings.auth.app01_db_config_path,
                    env_get=os.getenv,
                )
                mysql_repo = MySQLUserRepository(mysql_cfg)
                app.state.repository = CompositeRepository(pg=pg_repo, mysql=mysql_repo)
        else:
            app.state.repository = repository

        if llm_service is None:
            managed_http_client = httpx.AsyncClient(
                timeout=app_settings.llm.request_timeout_seconds
            )
            app.state.llm_service = LLMService(
                settings=app_settings, http_client=managed_http_client
            )
        else:
            app.state.llm_service = llm_service
        app.state.auth_service = AuthService(
            settings=app_settings,
            repository=app.state.repository,
        )

        yield

        if managed_http_client is not None:
            await managed_http_client.aclose()
        if managed_pool is not None:
            await managed_pool.close()

    app = FastAPI(
        title="AscendAny API",
        version="0.1.0",
        lifespan=lifespan,
    )

    app.add_middleware(
        CORSMiddleware,
        allow_origins=app_settings.api.cors_origins,
        allow_credentials=False,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    @app.exception_handler(AppError)
    async def app_error_handler(_: Request, exc: AppError) -> JSONResponse:
        return JSONResponse(status_code=exc.status_code, content=exc.to_dict())

    @app.exception_handler(Exception)
    async def unhandled_error_handler(_: Request, exc: Exception) -> JSONResponse:
        logger.exception("Unhandled API exception", exc_info=exc)
        return JSONResponse(
            status_code=500,
            content={
                "error": {
                    "code": "INTERNAL_SERVER_ERROR",
                    "message": "An unexpected server error occurred.",
                }
            },
        )

    app.include_router(health_router, prefix="/api/v1")
    app.include_router(meta_router, prefix="/api/v1")
    app.include_router(auth_router, prefix="/api/v1")
    app.include_router(students_router, prefix="/api/v1")
    app.include_router(chat_router, prefix="/api/v1")
    app.include_router(exam_analysis_router, prefix="/api/v1")
    app.include_router(import_router, prefix="/api/v1")
    app.include_router(admin_router, prefix="/api/v1")

    return app


app = create_app()
