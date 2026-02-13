from __future__ import annotations

import os
import re
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

from ..core.config import Settings
from ..core.errors import AppError
from ..core.security import (
    generate_refresh_token,
    hash_password,
    hash_refresh_token,
    sign_access_token,
    verify_access_token,
    verify_password,
)
from ..db.repository import AccountProfileRow, AccountRow, ApiRepository
from ..schemas.auth import (
    AuthAccountResponse,
    AuthMeResponse,
    AuthPolicyResponse,
    AuthProfileResponse,
    AuthProfileUpdateRequest,
    AuthTokensResponse,
    LoginRequest,
    LogoutRequest,
    RefreshRequest,
    RegisterRequest,
    SignupPolicy,
)

_USERNAME_RE = re.compile(r"^[A-Za-z0-9_]{4,32}$")
_EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
_PHONE_RE = re.compile(r"^[0-9+][0-9\-\s]{5,31}$")


@dataclass(slots=True)
class AuthenticatedAccount:
    account_id: int
    username: str


class AuthService:
    def __init__(self, settings: Settings, repository: ApiRepository) -> None:
        self._settings = settings
        self._repository = repository

    def get_policy(self) -> AuthPolicyResponse:
        policy = self._resolve_signup_policy()
        require_phone = policy == "require_phone_and_email"
        require_email = policy == "require_phone_and_email"
        return AuthPolicyResponse(
            signupPolicy=policy,
            requirePhone=require_phone,
            requireEmail=require_email,
        )

    async def register(self, payload: RegisterRequest) -> AuthTokensResponse:
        self._ensure_enabled()

        username = self._normalize_username(payload.username)
        password = payload.password
        if len(password.strip()) < 8:
            raise AppError(
                status_code=400,
                code="AUTH_PASSWORD_TOO_SHORT",
                message="Password must be at least 8 characters.",
            )

        phone = self._normalize_phone(payload.phone)
        email = self._normalize_email(payload.email)
        self._enforce_signup_policy(phone=phone, email=email)

        pepper = self._load_password_pepper()
        password_hash = hash_password(password, pepper=pepper)

        account = await self._repository.create_account(
            username=username,
            password_hash=password_hash,
        )
        if account is None:
            raise AppError(
                status_code=409,
                code="AUTH_USERNAME_TAKEN",
                message="Username is already in use.",
            )

        if phone:
            inserted = await self._repository.add_account_contact(
                account_id=account.account_id,
                contact_type="phone",
                value=phone,
            )
            if not inserted:
                await self._repository.delete_account(account.account_id)
                raise AppError(
                    status_code=409,
                    code="AUTH_CONTACT_ALREADY_IN_USE",
                    message="Phone number is already in use.",
                )

        if email:
            inserted = await self._repository.add_account_contact(
                account_id=account.account_id,
                contact_type="email",
                value=email,
            )
            if not inserted:
                await self._repository.delete_account(account.account_id)
                raise AppError(
                    status_code=409,
                    code="AUTH_CONTACT_ALREADY_IN_USE",
                    message="Email is already in use.",
                )

        profile = await self._repository.fetch_account_profile(account.account_id)
        return await self._issue_tokens(
            account=account,
            profile=profile,
            device_id=self._clean(payload.deviceId),
        )

    async def login(self, payload: LoginRequest) -> AuthTokensResponse:
        self._ensure_enabled()

        username = self._normalize_username(payload.username)
        account = await self._repository.fetch_account_by_username(username)
        if account is None:
            raise AppError(
                status_code=401,
                code="AUTH_INVALID_CREDENTIALS",
                message="Invalid username or password.",
            )
        if not account.is_active:
            raise AppError(
                status_code=403,
                code="AUTH_FORBIDDEN",
                message="Account is disabled.",
            )

        pepper = self._load_password_pepper()
        if not verify_password(payload.password, account.password_hash, pepper=pepper):
            raise AppError(
                status_code=401,
                code="AUTH_INVALID_CREDENTIALS",
                message="Invalid username or password.",
            )

        profile = await self._repository.fetch_account_profile(account.account_id)
        return await self._issue_tokens(
            account=account,
            profile=profile,
            device_id=self._clean(payload.deviceId),
        )

    async def refresh(self, payload: RefreshRequest) -> AuthTokensResponse:
        self._ensure_enabled()

        pepper = self._load_password_pepper()
        token_hash = hash_refresh_token(payload.refreshToken, pepper=pepper)
        token_row = await self._repository.fetch_refresh_token_by_hash(token_hash)
        if token_row is None:
            raise AppError(
                status_code=401,
                code="AUTH_TOKEN_INVALID",
                message="Refresh token is invalid.",
            )
        if token_row.revoked_at is not None:
            raise AppError(
                status_code=401,
                code="AUTH_TOKEN_INVALID",
                message="Refresh token is revoked.",
            )
        if token_row.expires_at <= datetime.now(UTC):
            raise AppError(
                status_code=401,
                code="AUTH_TOKEN_EXPIRED",
                message="Refresh token has expired.",
            )

        account = await self._repository.fetch_account_by_id(token_row.account_id)
        if account is None or not account.is_active:
            raise AppError(
                status_code=403,
                code="AUTH_FORBIDDEN",
                message="Account is unavailable.",
            )

        await self._repository.revoke_refresh_token_by_id(token_row.token_id)
        profile = await self._repository.fetch_account_profile(account.account_id)
        return await self._issue_tokens(
            account=account,
            profile=profile,
            device_id=self._clean(payload.deviceId) or token_row.device_id,
        )

    async def logout(
        self, current: AuthenticatedAccount, payload: LogoutRequest
    ) -> dict[str, bool]:
        self._ensure_enabled()
        refresh_token = self._clean(payload.refreshToken)
        if refresh_token:
            pepper = self._load_password_pepper()
            token_hash = hash_refresh_token(refresh_token, pepper=pepper)
            await self._repository.revoke_refresh_token_by_hash(
                account_id=current.account_id,
                token_hash=token_hash,
            )
            return {"ok": True}

        await self._repository.revoke_all_refresh_tokens(current.account_id)
        return {"ok": True}

    async def me(self, current: AuthenticatedAccount) -> AuthMeResponse:
        self._ensure_enabled()
        account = await self._repository.fetch_account_by_id(current.account_id)
        if account is None:
            raise AppError(
                status_code=404,
                code="AUTH_ACCOUNT_NOT_FOUND",
                message="Account was not found.",
            )
        profile = await self._repository.fetch_account_profile(account.account_id)
        return AuthMeResponse(account=self._build_account_response(account, profile))

    async def update_profile(
        self,
        current: AuthenticatedAccount,
        payload: AuthProfileUpdateRequest,
    ) -> AuthProfileResponse:
        self._ensure_enabled()
        account = await self._repository.fetch_account_by_id(current.account_id)
        if account is None or not account.is_active:
            raise AppError(
                status_code=404,
                code="AUTH_ACCOUNT_NOT_FOUND",
                message="Account was not found.",
            )

        student_id = self._clean(payload.studentId)
        pta_nickname = self._clean(payload.ptaNickname)
        profile = await self._repository.upsert_account_profile(
            account_id=current.account_id,
            student_id=student_id,
            pta_nickname=pta_nickname,
        )
        return AuthProfileResponse(
            studentId=profile.student_id,
            ptaNickname=profile.pta_nickname,
        )

    def authenticate_access_token(self, token: str) -> AuthenticatedAccount:
        self._ensure_enabled()
        secret = self._load_jwt_secret()
        try:
            payload = verify_access_token(token, secret=secret)
        except ValueError as exc:
            reason = str(exc)
            if reason == "token_expired":
                raise AppError(
                    status_code=401,
                    code="AUTH_TOKEN_EXPIRED",
                    message="Access token has expired.",
                ) from exc
            raise AppError(
                status_code=401,
                code="AUTH_TOKEN_INVALID",
                message="Access token is invalid.",
            ) from exc

        if payload.get("typ") != "access":
            raise AppError(
                status_code=401,
                code="AUTH_TOKEN_INVALID",
                message="Access token is invalid.",
            )

        sub = payload.get("sub")
        username = payload.get("username")
        if not isinstance(sub, str) or not sub.isdigit() or not isinstance(username, str):
            raise AppError(
                status_code=401,
                code="AUTH_TOKEN_INVALID",
                message="Access token is invalid.",
            )

        return AuthenticatedAccount(account_id=int(sub), username=username)

    async def _issue_tokens(
        self,
        account: AccountRow,
        profile: AccountProfileRow | None,
        device_id: str | None,
    ) -> AuthTokensResponse:
        secret = self._load_jwt_secret()
        access_token, access_expires_at = sign_access_token(
            payload={
                "sub": str(account.account_id),
                "username": account.username,
                "typ": "access",
            },
            secret=secret,
            expires_in_seconds=self._settings.auth.access_ttl_minutes * 60,
        )

        refresh_token = generate_refresh_token()
        refresh_expires_at = datetime.now(UTC) + timedelta(
            days=self._settings.auth.refresh_ttl_days
        )
        pepper = self._load_password_pepper()
        await self._repository.insert_refresh_token(
            account_id=account.account_id,
            token_hash=hash_refresh_token(refresh_token, pepper=pepper),
            expires_at=refresh_expires_at,
            device_id=device_id,
        )
        await self._repository.touch_account_login(account.account_id)

        return AuthTokensResponse(
            accessToken=access_token,
            accessTokenExpiresAt=access_expires_at,
            refreshToken=refresh_token,
            refreshTokenExpiresAt=refresh_expires_at,
            account=self._build_account_response(account, profile),
        )

    def _build_account_response(
        self,
        account: AccountRow,
        profile: AccountProfileRow | None,
    ) -> AuthAccountResponse:
        student_id = profile.student_id if profile else None
        pta_nickname = profile.pta_nickname if profile else None
        return AuthAccountResponse(
            accountId=str(account.account_id),
            username=account.username,
            studentId=student_id,
            ptaNickname=pta_nickname,
        )

    def _ensure_enabled(self) -> None:
        if self._settings.auth.enabled:
            return
        raise AppError(
            status_code=503,
            code="AUTH_DISABLED",
            message="Authentication is disabled on this server.",
        )

    def _resolve_signup_policy(self) -> SignupPolicy:
        raw = self._settings.auth.signup_policy.strip()
        if raw in {
            "username_password_only",
            "require_phone_or_email",
            "require_phone_and_email",
        }:
            return raw
        return "username_password_only"

    def _enforce_signup_policy(self, phone: str | None, email: str | None) -> None:
        policy = self._resolve_signup_policy()
        if policy == "username_password_only":
            return
        if policy == "require_phone_or_email" and (phone or email):
            return
        if policy == "require_phone_and_email" and phone and email:
            return
        raise AppError(
            status_code=400,
            code="AUTH_SIGNUP_POLICY_VIOLATION",
            message="Signup payload does not satisfy current signup policy.",
        )

    def _normalize_username(self, username: str) -> str:
        value = username.strip()
        if not _USERNAME_RE.fullmatch(value):
            raise AppError(
                status_code=400,
                code="AUTH_INVALID_USERNAME",
                message="Username must be 4-32 chars and contain only letters, digits, underscore.",
            )
        return value

    def _normalize_phone(self, phone: str | None) -> str | None:
        value = self._clean(phone)
        if value is None:
            return None
        if not _PHONE_RE.fullmatch(value):
            raise AppError(
                status_code=400,
                code="AUTH_INVALID_PHONE",
                message="Phone format is invalid.",
            )
        return value

    def _normalize_email(self, email: str | None) -> str | None:
        value = self._clean(email)
        if value is None:
            return None
        normalized = value.lower()
        if not _EMAIL_RE.fullmatch(normalized):
            raise AppError(
                status_code=400,
                code="AUTH_INVALID_EMAIL",
                message="Email format is invalid.",
            )
        return normalized

    def _load_jwt_secret(self) -> str:
        secret = os.getenv(self._settings.auth.jwt_secret_env, "").strip()
        if secret:
            return secret
        raise AppError(
            status_code=503,
            code="AUTH_CONFIG_ERROR",
            message=f"Missing auth secret env var: {self._settings.auth.jwt_secret_env}",
        )

    def _load_password_pepper(self) -> str:
        return os.getenv(self._settings.auth.password_pepper_env, "").strip()

    @staticmethod
    def _clean(value: str | None) -> str | None:
        if value is None:
            return None
        trimmed = value.strip()
        return trimmed or None
