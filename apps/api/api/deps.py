from __future__ import annotations

from fastapi import Depends, Header, Request

from ..core.config import Settings
from ..core.errors import AppError
from ..services.auth import AuthenticatedAccount


def get_settings(request: Request) -> Settings:
    return request.app.state.settings


def get_repository(request: Request):
    return request.app.state.repository


def get_llm_service(request: Request):
    return request.app.state.llm_service


def get_auth_service(request: Request):
    return request.app.state.auth_service


def _extract_bearer_token(authorization: str | None) -> str | None:
    if authorization is None:
        return None
    value = authorization.strip()
    if not value or not value.lower().startswith("bearer "):
        return None
    token = value[7:].strip()
    return token or None


def get_current_account_optional(
    authorization: str | None = Header(default=None),
    auth_service=Depends(get_auth_service),
) -> AuthenticatedAccount | None:
    token = _extract_bearer_token(authorization)
    if token is None:
        return None
    return auth_service.authenticate_access_token(token)


def get_current_account(
    current: AuthenticatedAccount | None = Depends(get_current_account_optional),
) -> AuthenticatedAccount:
    if current is None:
        raise AppError(
            status_code=401,
            code="AUTH_UNAUTHORIZED",
            message="Authentication is required.",
        )
    return current
