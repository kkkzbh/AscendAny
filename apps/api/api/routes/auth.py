from __future__ import annotations

from fastapi import APIRouter, Depends

from ..deps import (
    get_auth_service,
    get_current_account,
)
from ...schemas.auth import (
    AuthMeResponse,
    AuthPolicyResponse,
    AuthProfileResponse,
    AuthProfileUpdateRequest,
    AuthTokensResponse,
    LoginRequest,
    LogoutRequest,
    RefreshRequest,
    RegisterRequest,
)

router = APIRouter(tags=["auth"])


@router.get("/auth/policy", response_model=AuthPolicyResponse)
async def auth_policy(
    auth_service=Depends(get_auth_service),
) -> AuthPolicyResponse:
    return auth_service.get_policy()


@router.post("/auth/register", response_model=AuthTokensResponse)
async def auth_register(
    payload: RegisterRequest,
    auth_service=Depends(get_auth_service),
) -> AuthTokensResponse:
    return await auth_service.register(payload)


@router.post("/auth/login", response_model=AuthTokensResponse)
async def auth_login(
    payload: LoginRequest,
    auth_service=Depends(get_auth_service),
) -> AuthTokensResponse:
    return await auth_service.login(payload)


@router.post("/auth/refresh", response_model=AuthTokensResponse)
async def auth_refresh(
    payload: RefreshRequest,
    auth_service=Depends(get_auth_service),
) -> AuthTokensResponse:
    return await auth_service.refresh(payload)


@router.post("/auth/logout")
async def auth_logout(
    payload: LogoutRequest,
    current_account=Depends(get_current_account),
    auth_service=Depends(get_auth_service),
) -> dict[str, bool]:
    return await auth_service.logout(current_account, payload)


@router.get("/auth/me", response_model=AuthMeResponse)
async def auth_me(
    current_account=Depends(get_current_account),
    auth_service=Depends(get_auth_service),
) -> AuthMeResponse:
    return await auth_service.me(current_account)


@router.put("/auth/profile", response_model=AuthProfileResponse)
async def auth_update_profile(
    payload: AuthProfileUpdateRequest,
    current_account=Depends(get_current_account),
    auth_service=Depends(get_auth_service),
) -> AuthProfileResponse:
    return await auth_service.update_profile(current_account, payload)
