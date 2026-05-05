from __future__ import annotations

import hmac
import json
import os
import re
import secrets
import string
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Any

from ..core.config import Settings
from ..core.errors import AppError
from ..core.security import (
    generate_refresh_token,
    hash_password,
    hash_refresh_token,
    sign_access_token,
    verify_hs256_token,
    verify_access_token,
    verify_password,
)
from ..db.repository import AccountProfileRow, AccountRow, ApiRepository
from ..schemas.auth import (
    AuthAccountResponse,
    AuthMeResponse,
    LocalPasswordBootstrapRequest,
    AuthPolicyResponse,
    AuthProfileResponse,
    AuthProfileUpdateRequest,
    AuthTokensResponse,
    LoginRequest,
    LogoutRequest,
    RefreshRequest,
    RegisterRequest,
    SSOExchangeRequest,
    SignupPolicy,
)

_USERNAME_RE = re.compile(r"^[A-Za-z0-9_]{3,32}$")
_DISPLAY_NAME_RE = re.compile(r"^[A-Za-z0-9_]{4,32}$")
_EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
_PHONE_RE = re.compile(r"^[0-9+][0-9\-\s]{5,31}$")
_DISPLAY_NAME_CHARS = string.ascii_lowercase + string.digits


@dataclass(slots=True)
class AuthenticatedAccount:
    account_id: int
    username: str
    is_admin: bool = False


@dataclass(slots=True)
class SSOClaims:
    provider: str
    external_user_id: str
    jti: str
    username: str
    student_id: str
    display_name: str | None
    pta_nickname: str | None
    expires_at: datetime
    raw_claims: dict[str, object]


class AuthService:
    def __init__(self, settings: Settings, repository: ApiRepository) -> None:
        self._settings = settings
        self._repository = repository

    def _is_external_provider(self) -> bool:
        provider = getattr(self._settings.auth, "provider", "internal") or "internal"
        return provider.strip().lower() not in {"", "internal"}

    async def _fetch_account_by_username(self, username: str) -> AccountRow | None:
        fetcher = getattr(self._repository, "fetch_account_by_username", None)
        if callable(fetcher):
            return await fetcher(username)

        pool = getattr(self._repository, "_pool", None)
        if pool is not None:
            return await ApiRepository(pool).fetch_account_by_username(username)

        raise AppError(
            status_code=500,
            code="AUTH_REPOSITORY_INCOMPATIBLE",
            message="Auth repository does not support username lookup.",
        )

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

        if self._is_external_provider():
            raise AppError(
                status_code=503,
                code="AUTH_REGISTER_DISABLED",
                message="Registration is disabled when using an external auth provider.",
            )

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
        student_id = self._normalize_student_id(payload.studentId)
        pta_nickname = self._normalize_pta_nickname(payload.ptaNickname)

        pepper = self._load_password_pepper()
        password_hash = hash_password(password, pepper=pepper)

        existing = await self._fetch_account_by_username(username)
        if existing is not None:
            raise AppError(
                status_code=409,
                code="AUTH_USERNAME_TAKEN",
                message="Username is already in use.",
            )
        account = None
        for _ in range(12):
            candidate_display_name = self._generate_random_display_name()
            account = await self._repository.create_account(
                username=username,
                display_name=candidate_display_name,
                password_hash=password_hash,
            )
            if account is not None:
                break
        if account is None:
            if await self._fetch_account_by_username(username) is not None:
                raise AppError(
                    status_code=409,
                    code="AUTH_USERNAME_TAKEN",
                    message="Username is already in use.",
                )
            raise AppError(
                status_code=503,
                code="AUTH_DISPLAY_NAME_ALLOCATE_FAILED",
                message="Failed to allocate display name, please retry.",
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

        try:
            student_entity_id = await self._repository.ensure_student_by_student_no(
                student_no=student_id,
                student_name=pta_nickname,
            )
            claimed = await self._repository.claim_student_nickname(
                student_id=student_entity_id,
                nickname=pta_nickname,
                account_id=account.account_id,
            )
            if not claimed:
                raise AppError(
                    status_code=409,
                    code="AUTH_PTA_NICKNAME_TAKEN",
                    message="ptaNickname is already claimed by another student.",
                )
            await self._repository.upsert_student_identity(
                student_id=student_entity_id,
                source="pta_nickname",
                external_id=pta_nickname,
                external_name=pta_nickname,
            )
            related_student_ids = await self._find_related_student_ids_by_nicknames(
                [pta_nickname]
            )
            await self._repository.reassign_submissions_by_nicknames(
                student_id=student_entity_id,
                nicknames=[pta_nickname],
                reason="register_claim",
            )
            await self._merge_achievement_states_for_student(
                target_student_id=student_entity_id,
                source_student_ids=related_student_ids + [student_entity_id],
            )
            profile = await self._repository.upsert_account_profile(
                account_id=account.account_id,
                student_id=student_id,
                pta_nickname=pta_nickname,
            )
        except Exception:
            await self._repository.delete_account(account.account_id)
            raise

        return await self._issue_tokens(
            account=account,
            profile=profile,
            device_id=self._clean(payload.deviceId),
        )

    async def login(self, payload: LoginRequest) -> AuthTokensResponse:
        self._ensure_enabled()

        username = self._normalize_username(payload.username)
        account = await self._fetch_account_by_username(username)
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

        mode = payload.passwordMode or "plain"
        authenticated = False
        if mode == "stored_value":
            if (
                self._settings.auth.allow_stored_password_direct_login
                and self._is_external_provider()
            ):
                authenticated = hmac.compare_digest(
                    payload.password,
                    account.password_hash,
                )
        else:
            if not getattr(account, "local_password_enabled", True):
                raise AppError(
                    status_code=403,
                    code="AUTH_LOCAL_LOGIN_DISABLED",
                    message="Local password login is not enabled for this account.",
                )
            pepper = self._load_password_pepper()
            authenticated = verify_password(
                payload.password,
                account.password_hash,
                pepper=pepper,
            )

        if not authenticated:
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

    async def exchange_sso(self, payload: SSOExchangeRequest) -> AuthTokensResponse:
        self._ensure_enabled()
        if not self._settings.sso.enabled:
            raise AppError(
                status_code=503,
                code="AUTH_SSO_DISABLED",
                message="SSO is disabled on this server.",
            )

        claims = self._verify_sso_token(payload.token)
        await self._repository.cleanup_expired_sso_jtis()
        consumed = await self._repository.consume_sso_jti(
            provider=claims.provider,
            jti=claims.jti,
            external_user_id=claims.external_user_id,
            expires_at=claims.expires_at,
            account_id=None,
        )
        if not consumed:
            raise AppError(
                status_code=409,
                code="AUTH_SSO_TOKEN_REPLAYED",
                message="SSO token has already been consumed.",
            )

        account, profile = await self._resolve_sso_account(claims)
        await self._repository.attach_consumed_sso_jti_account(
            provider=claims.provider,
            jti=claims.jti,
            account_id=account.account_id,
        )
        return await self._issue_tokens(
            account=account,
            profile=profile,
            device_id=f"sso:{claims.provider}",
        )

    async def bootstrap_local_password(
        self,
        current: AuthenticatedAccount,
        payload: LocalPasswordBootstrapRequest,
    ) -> dict[str, bool]:
        self._ensure_enabled()
        account = await self._repository.fetch_account_by_id(current.account_id)
        if account is None:
            raise AppError(
                status_code=404,
                code="AUTH_ACCOUNT_NOT_FOUND",
                message="Account was not found.",
            )
        if getattr(account, "local_password_enabled", True):
            raise AppError(
                status_code=409,
                code="AUTH_LOCAL_PASSWORD_ALREADY_ENABLED",
                message="Local password login is already enabled for this account.",
            )

        password = payload.newPassword
        if len(password.strip()) < 8:
            raise AppError(
                status_code=400,
                code="AUTH_PASSWORD_TOO_SHORT",
                message="Password must be at least 8 characters.",
            )

        pepper = self._load_password_pepper()
        password_hash = hash_password(password, pepper=pepper)
        updated = await self._repository.bootstrap_local_password(
            account_id=current.account_id,
            password_hash=password_hash,
        )
        if updated is None:
            raise AppError(
                status_code=404,
                code="AUTH_ACCOUNT_NOT_FOUND",
                message="Account was not found.",
            )
        return {"ok": True}

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

        if self._is_external_provider():
            if payload.studentId is not None or payload.ptaNickname is not None:
                raise AppError(
                    status_code=403,
                    code="AUTH_PROFILE_IMMUTABLE",
                    message="studentId/ptaNickname are managed by the external provider.",
                )

            account = await self._repository.fetch_account_by_id(current.account_id)
            if account is None:
                raise AppError(
                    status_code=404,
                    code="AUTH_ACCOUNT_NOT_FOUND",
                    message="Account was not found.",
                )

            if payload.displayName is not None:
                next_display_name = payload.displayName.strip()
                if not next_display_name:
                    raise AppError(
                        status_code=400,
                        code="AUTH_INVALID_DISPLAY_NAME",
                        message="displayName must not be blank.",
                    )
                updated = await self._repository.update_account_display_name(
                    account_id=current.account_id,
                    display_name=next_display_name,
                )
                if updated is not None:
                    account = updated

            profile = await self._repository.fetch_account_profile(current.account_id)
            return AuthProfileResponse(
                displayName=account.display_name,
                studentId=profile.student_id if profile else None,
                ptaNickname=profile.pta_nickname if profile else None,
            )
        account = await self._repository.fetch_account_by_id(current.account_id)
        if account is None:
            raise AppError(
                status_code=404,
                code="AUTH_ACCOUNT_NOT_FOUND",
                message="Account was not found.",
            )
        profile = await self._repository.fetch_account_profile(current.account_id)
        if payload.displayName is not None:
            next_display_name = self._normalize_display_name(payload.displayName)
            if next_display_name != account.display_name:
                updated_account = await self._repository.update_account_display_name(
                    account_id=current.account_id,
                    display_name=next_display_name,
                )
                if updated_account is None:
                    raise AppError(
                        status_code=409,
                        code="AUTH_DISPLAY_NAME_TAKEN",
                        message="displayName is already in use.",
                    )
                account = updated_account

        should_update_profile = (
            payload.studentId is not None or payload.ptaNickname is not None
        )
        if not should_update_profile:
            return AuthProfileResponse(
                displayName=account.display_name,
                studentId=profile.student_id if profile else None,
                ptaNickname=profile.pta_nickname if profile else None,
            )

        if profile is None or not self._clean(profile.student_id):
            raise AppError(
                status_code=404,
                code="AUTH_PROFILE_NOT_FOUND",
                message="Account profile was not found.",
            )
        current_student_id = self._clean(profile.student_id)
        if current_student_id is None:
            raise AppError(
                status_code=400,
                code="AUTH_STUDENT_ID_REQUIRED",
                message="studentId is required.",
            )

        requested_student_id = self._clean(payload.studentId)
        if (
            requested_student_id is not None
            and requested_student_id != current_student_id
        ):
            raise AppError(
                status_code=403,
                code="AUTH_STUDENT_ID_IMMUTABLE",
                message="studentId is immutable after registration.",
            )

        if payload.ptaNickname is None:
            next_nickname = self._clean(profile.pta_nickname)
            if next_nickname is None:
                raise AppError(
                    status_code=400,
                    code="AUTH_PTA_NICKNAME_REQUIRED",
                    message="ptaNickname is required.",
                )
        else:
            next_nickname = self._normalize_pta_nickname(payload.ptaNickname)
        previous_nickname = self._clean(profile.pta_nickname)

        student_entity_id = await self._repository.ensure_student_by_student_no(
            student_no=current_student_id,
            student_name=next_nickname,
        )
        claimed = await self._repository.claim_student_nickname(
            student_id=student_entity_id,
            nickname=next_nickname,
            account_id=current.account_id,
        )
        if not claimed:
            raise AppError(
                status_code=409,
                code="AUTH_PTA_NICKNAME_TAKEN",
                message="ptaNickname is already claimed by another student.",
            )
        await self._repository.upsert_student_identity(
            student_id=student_entity_id,
            source="pta_nickname",
            external_id=next_nickname,
            external_name=next_nickname,
        )
        reassigned_nicknames = [next_nickname]
        if previous_nickname and previous_nickname != next_nickname:
            reassigned_nicknames.append(previous_nickname)
        related_student_ids = await self._find_related_student_ids_by_nicknames(
            reassigned_nicknames
        )
        await self._repository.reassign_submissions_by_nicknames(
            student_id=student_entity_id,
            nicknames=reassigned_nicknames,
            reason="profile_nickname_update",
        )
        await self._merge_achievement_states_for_student(
            target_student_id=student_entity_id,
            source_student_ids=related_student_ids + [student_entity_id],
        )

        updated = await self._repository.upsert_account_profile(
            account_id=current.account_id,
            student_id=current_student_id,
            pta_nickname=next_nickname,
        )
        return AuthProfileResponse(
            displayName=account.display_name,
            studentId=updated.student_id,
            ptaNickname=updated.pta_nickname,
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
        if (
            not isinstance(sub, str)
            or not sub.isdigit()
            or not isinstance(username, str)
        ):
            raise AppError(
                status_code=401,
                code="AUTH_TOKEN_INVALID",
                message="Access token is invalid.",
            )

        return AuthenticatedAccount(
            account_id=int(sub),
            username=username,
            is_admin=bool(payload.get("adm", False)),
        )

    async def _resolve_sso_account(
        self,
        claims: SSOClaims,
    ) -> tuple[AccountRow, AccountProfileRow | None]:
        link = await self._repository.fetch_external_identity_link(
            claims.provider,
            claims.external_user_id,
        )
        if link is not None:
            account = await self._repository.fetch_account_by_id(link.account_id)
            if account is None:
                raise AppError(
                    status_code=404,
                    code="AUTH_ACCOUNT_NOT_FOUND",
                    message="Account was not found.",
                )
            profile = await self._repository.fetch_account_profile(account.account_id)
            if profile is None or self._clean(profile.student_id) != claims.student_id:
                raise AppError(
                    status_code=409,
                    code="AUTH_SSO_STUDENT_ID_MISMATCH",
                    message="Linked SSO identity does not match the account student ID.",
                )
            profile = await self._sync_sso_profile(
                account=account,
                profile=profile,
                claims=claims,
                student_mismatch_code="AUTH_SSO_STUDENT_ID_MISMATCH",
                pta_mismatch_code="AUTH_SSO_PTA_NICKNAME_MISMATCH",
            )
            await self._repository.update_external_identity_snapshot(
                link_id=link.link_id,
                external_username=claims.username,
                external_display_name=claims.display_name,
                student_id_snapshot=claims.student_id,
                pta_nickname_snapshot=claims.pta_nickname,
                raw_claims=claims.raw_claims,
            )
            return account, profile

        account = await self._repository.fetch_account_by_student_id(claims.student_id)
        if account is not None:
            profile = await self._repository.fetch_account_profile(account.account_id)
            profile = await self._sync_sso_profile(
                account=account,
                profile=profile,
                claims=claims,
                student_mismatch_code="AUTH_SSO_STUDENT_ID_MISMATCH",
                pta_mismatch_code="AUTH_SSO_PTA_NICKNAME_MISMATCH",
            )
            created_link = await self._repository.create_external_identity_link(
                provider=claims.provider,
                external_user_id=claims.external_user_id,
                account_id=account.account_id,
                external_username=claims.username,
                external_display_name=claims.display_name,
                student_id_snapshot=claims.student_id,
                pta_nickname_snapshot=claims.pta_nickname,
                raw_claims=claims.raw_claims,
            )
            if created_link is None:
                raise AppError(
                    status_code=409,
                    code="AUTH_SSO_TOKEN_REPLAYED",
                    message="SSO identity is already linked by another request.",
                )
            return account, profile

        username = self._normalize_username(claims.username)
        existing = await self._fetch_account_by_username(username)
        if existing is not None:
            raise AppError(
                status_code=409,
                code="AUTH_SSO_USERNAME_CONFLICT",
                message="Username is already in use.",
            )

        display_name = claims.display_name or username
        pepper = self._load_password_pepper()
        random_password_hash = hash_password(secrets.token_urlsafe(32), pepper=pepper)
        account = None
        for candidate_display_name in [display_name, username]:
            if account is not None:
                break
            account = await self._repository.create_account(
                username=username,
                display_name=candidate_display_name,
                password_hash=random_password_hash,
                provision_source="external_sso",
                local_password_enabled=False,
                local_password_set_at=None,
            )
        if account is None:
            raise AppError(
                status_code=409,
                code="AUTH_SSO_USERNAME_CONFLICT",
                message="Username is already in use.",
            )
        try:
            profile = await self._repository.upsert_account_profile(
                account_id=account.account_id,
                student_id=claims.student_id,
                pta_nickname=None,
            )
            profile = await self._sync_sso_profile(
                account=account,
                profile=profile,
                claims=claims,
                student_mismatch_code="AUTH_SSO_STUDENT_ID_MISMATCH",
                pta_mismatch_code="AUTH_SSO_PTA_NICKNAME_CONFLICT",
            )
            created_link = await self._repository.create_external_identity_link(
                provider=claims.provider,
                external_user_id=claims.external_user_id,
                account_id=account.account_id,
                external_username=claims.username,
                external_display_name=claims.display_name,
                student_id_snapshot=claims.student_id,
                pta_nickname_snapshot=claims.pta_nickname,
                raw_claims=claims.raw_claims,
            )
            if created_link is None:
                raise AppError(
                    status_code=409,
                    code="AUTH_SSO_TOKEN_REPLAYED",
                    message="SSO identity is already linked by another request.",
                )
            return account, profile
        except Exception:
            await self._repository.delete_account(account.account_id)
            raise

    async def _sync_sso_profile(
        self,
        account: AccountRow,
        profile: AccountProfileRow | None,
        claims: SSOClaims,
        student_mismatch_code: str,
        pta_mismatch_code: str,
    ) -> AccountProfileRow:
        current_student_id = self._clean(profile.student_id if profile else None)
        if current_student_id is None:
            profile = await self._repository.upsert_account_profile(
                account_id=account.account_id,
                student_id=claims.student_id,
                pta_nickname=profile.pta_nickname if profile else None,
            )
            current_student_id = claims.student_id
        if current_student_id != claims.student_id:
            raise AppError(
                status_code=409,
                code=student_mismatch_code,
                message="SSO payload does not match the linked student profile.",
            )

        next_pta_nickname = self._clean(profile.pta_nickname if profile else None)
        incoming_pta_nickname = claims.pta_nickname
        if (
            next_pta_nickname is not None
            and incoming_pta_nickname is not None
            and next_pta_nickname != incoming_pta_nickname
        ):
            raise AppError(
                status_code=409,
                code=pta_mismatch_code,
                message="SSO payload does not match the linked PTA nickname.",
            )

        student_entity_id = await self._repository.ensure_student_by_student_no(
            student_no=claims.student_id,
            student_name=incoming_pta_nickname,
        )
        if incoming_pta_nickname:
            claimed = await self._repository.claim_student_nickname(
                student_id=student_entity_id,
                nickname=incoming_pta_nickname,
                account_id=account.account_id,
            )
            if not claimed:
                raise AppError(
                    status_code=409,
                    code="AUTH_SSO_PTA_NICKNAME_CONFLICT",
                    message="ptaNickname is already claimed by another student.",
                )
            await self._repository.upsert_student_identity(
                student_id=student_entity_id,
                source="pta_nickname",
                external_id=incoming_pta_nickname,
                external_name=incoming_pta_nickname,
            )
            if next_pta_nickname != incoming_pta_nickname:
                related_student_ids = await self._find_related_student_ids_by_nicknames(
                    [incoming_pta_nickname]
                )
                await self._repository.reassign_submissions_by_nicknames(
                    student_id=student_entity_id,
                    nicknames=[incoming_pta_nickname],
                    reason="sso_claim",
                )
                await self._merge_achievement_states_for_student(
                    target_student_id=student_entity_id,
                    source_student_ids=related_student_ids + [student_entity_id],
                )
                profile = await self._repository.upsert_account_profile(
                    account_id=account.account_id,
                    student_id=claims.student_id,
                    pta_nickname=incoming_pta_nickname,
                )
        if profile is None:
            profile = await self._repository.upsert_account_profile(
                account_id=account.account_id,
                student_id=claims.student_id,
                pta_nickname=incoming_pta_nickname,
            )
        return profile

    def _verify_sso_token(self, token: str) -> SSOClaims:
        secret = self._load_sso_secret()
        try:
            _, payload = verify_hs256_token(token, secret=secret)
        except ValueError as exc:
            raise AppError(
                status_code=401,
                code="AUTH_SSO_TOKEN_INVALID",
                message="SSO token is invalid.",
            ) from exc

        provider = self._settings.sso.provider.strip() or "external_app"
        issuer = self._settings.sso.issuer.strip() or provider
        audience = self._settings.sso.audience.strip()
        clock_skew = max(0, self._settings.sso.clock_skew_seconds)
        max_token_ttl = max(1, self._settings.sso.max_token_ttl_seconds)

        iss = self._claim_as_string(payload, "iss")
        aud = payload.get("aud")
        if iss != issuer or not self._claim_matches_audience(aud, audience):
            raise AppError(
                status_code=401,
                code="AUTH_SSO_TOKEN_INVALID",
                message="SSO token is invalid.",
            )

        external_user_id = self._claim_as_string(payload, "sub")
        jti = self._claim_as_string(payload, "jti")
        username = self._claim_as_string(payload, "username")
        student_id = self._claim_as_string(payload, "student_id")
        display_name = self._optional_string_claim(payload, "display_name")
        pta_nickname = self._optional_string_claim(payload, "pta_nickname")

        iat = self._claim_as_int(payload, "iat")
        exp = self._claim_as_int(payload, "exp")
        if exp <= iat or exp - iat > max_token_ttl:
            raise AppError(
                status_code=401,
                code="AUTH_SSO_TOKEN_INVALID",
                message="SSO token is invalid.",
            )

        now = int(datetime.now(UTC).timestamp())
        if iat > now + clock_skew:
            raise AppError(
                status_code=401,
                code="AUTH_SSO_TOKEN_INVALID",
                message="SSO token is invalid.",
            )
        if exp < now - clock_skew:
            raise AppError(
                status_code=401,
                code="AUTH_SSO_TOKEN_EXPIRED",
                message="SSO token has expired.",
            )

        raw_claims = json.loads(json.dumps(payload))
        return SSOClaims(
            provider=provider,
            external_user_id=external_user_id,
            jti=jti,
            username=username,
            student_id=student_id,
            display_name=display_name,
            pta_nickname=pta_nickname,
            expires_at=datetime.fromtimestamp(exp, tz=UTC),
            raw_claims=raw_claims,
        )

    @staticmethod
    def _claim_matches_audience(value: Any, expected: str) -> bool:
        if not expected:
            return True
        if isinstance(value, str):
            return value == expected
        if isinstance(value, list):
            return any(isinstance(item, str) and item == expected for item in value)
        return False

    def _claim_as_string(self, payload: dict[str, Any], key: str) -> str:
        value = self._clean(payload.get(key))
        if value is None:
            raise AppError(
                status_code=401,
                code="AUTH_SSO_TOKEN_INVALID",
                message="SSO token is invalid.",
            )
        return value

    @staticmethod
    def _claim_as_int(payload: dict[str, Any], key: str) -> int:
        value = payload.get(key)
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise AppError(
                status_code=401,
                code="AUTH_SSO_TOKEN_INVALID",
                message="SSO token is invalid.",
            )
        return int(value)

    def _optional_string_claim(
        self,
        payload: dict[str, Any],
        key: str,
    ) -> str | None:
        return self._clean(payload.get(key))

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
                "adm": bool(getattr(account, "is_admin", False)),
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
            displayName=account.display_name,
            isAdmin=bool(getattr(account, "is_admin", False)),
            studentId=student_id,
            ptaNickname=pta_nickname,
            provisionSource=str(getattr(account, "provision_source", "local")),
            localPasswordEnabled=bool(
                getattr(account, "local_password_enabled", True)
            ),
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
        if self._is_external_provider():
            if not value or len(value) > 50:
                raise AppError(
                    status_code=400,
                    code="AUTH_INVALID_USERNAME",
                    message="Username must not be blank.",
                )
            return value

        if not _USERNAME_RE.fullmatch(value):
            raise AppError(
                status_code=400,
                code="AUTH_INVALID_USERNAME",
                message="Username must be 4-32 chars and contain only letters, digits, underscore.",
            )
        return value

    def _normalize_display_name(self, display_name: str) -> str:
        value = display_name.strip()
        if self._is_external_provider():
            if not value or len(value) > 128:
                raise AppError(
                    status_code=400,
                    code="AUTH_INVALID_DISPLAY_NAME",
                    message="displayName must not be blank.",
                )
            return value
        if not _DISPLAY_NAME_RE.fullmatch(value):
            raise AppError(
                status_code=400,
                code="AUTH_INVALID_DISPLAY_NAME",
                message="displayName must be 4-32 chars and contain only letters, digits, underscore.",
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

    def _normalize_student_id(self, student_id: str | None) -> str:
        value = self._clean(student_id)
        if value is None:
            raise AppError(
                status_code=400,
                code="AUTH_STUDENT_ID_REQUIRED",
                message="studentId is required for registration.",
            )
        return value

    def _normalize_pta_nickname(self, pta_nickname: str | None) -> str:
        value = self._clean(pta_nickname)
        if value is None:
            raise AppError(
                status_code=400,
                code="AUTH_PTA_NICKNAME_REQUIRED",
                message="ptaNickname is required for registration.",
            )
        return value

    def _load_jwt_secret(self) -> str:
        secret = os.getenv(self._settings.auth.jwt_secret_env, "").strip()
        if secret:
            return secret
        fallback = self._settings.auth.jwt_secret.strip()
        if fallback:
            return fallback
        raise AppError(
            status_code=503,
            code="AUTH_CONFIG_ERROR",
            message=f"Missing auth secret env var: {self._settings.auth.jwt_secret_env}",
        )

    def _load_password_pepper(self) -> str:
        return os.getenv(self._settings.auth.password_pepper_env, "").strip()

    def _load_sso_secret(self) -> str:
        secret = os.getenv(self._settings.sso.hs256_secret_env, "").strip()
        if secret:
            return secret
        raise AppError(
            status_code=503,
            code="AUTH_CONFIG_ERROR",
            message=f"Missing SSO secret env var: {self._settings.sso.hs256_secret_env}",
        )

    async def _find_related_student_ids_by_nicknames(
        self,
        nicknames: list[str],
    ) -> list[int]:
        finder = getattr(
            self._repository,
            "find_student_ids_by_submission_nicknames",
            None,
        )
        if not callable(finder):
            return []
        rows = await finder(nicknames)
        return [int(student_id) for student_id in rows if int(student_id) > 0]

    async def _merge_achievement_states_for_student(
        self,
        target_student_id: int,
        source_student_ids: list[int],
    ) -> None:
        merger = getattr(
            self._repository,
            "merge_achievement_states_for_student",
            None,
        )
        if not callable(merger):
            return
        await merger(
            target_student_id=target_student_id,
            source_student_ids=source_student_ids,
        )

    @staticmethod
    def _generate_random_display_name() -> str:
        suffix = "".join(secrets.choice(_DISPLAY_NAME_CHARS) for _ in range(8))
        return f"user_{suffix}"

    @staticmethod
    def _clean(value: str | None) -> str | None:
        if value is None:
            return None
        trimmed = value.strip()
        return trimmed or None
