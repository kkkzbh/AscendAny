from __future__ import annotations

import os
import re
from typing import Any

from psycopg.rows import dict_row

from ..core.config import Settings
from ..core.errors import AppError
from ..core.security import hash_password, verify_password
from ..schemas.auth import (
    AuthProfileUpdateRequest,
    LoginRequest,
    LogoutRequest,
    RefreshRequest,
    RegisterRequest,
)
from .auth import AuthService, AuthenticatedAccount
from .web_email import LegacyEmailService

EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")


class WebAuthAdapter:
    def __init__(
        self,
        *,
        settings: Settings,
        repository: Any,
        auth_service: AuthService,
        email_service: LegacyEmailService,
    ) -> None:
        self._settings = settings
        self._repository = repository
        self._auth_service = auth_service
        self._email_service = email_service

    async def me(self, current: AuthenticatedAccount) -> dict[str, Any]:
        account = await self._repository.fetch_account_by_id(current.account_id)
        if account is None:
            return {"success": False, "message": "用户不存在"}
        profile = await self._repository.fetch_account_profile(current.account_id)
        email = await self._fetch_email_for_account(current.account_id)
        return {
            "success": True,
            "user": {
                "id": account.account_id,
                "username": account.username,
                "email": email or "",
                "full_name": profile.pta_nickname if profile else "",
                "student_id": profile.student_id if profile else "",
                "school": "",
            },
        }

    async def login(self, payload: dict[str, Any]) -> dict[str, Any]:
        username_or_email = (payload.get("username_or_email") or "").strip()
        password = str(payload.get("password") or "")
        if not username_or_email or not password:
            return {"success": False, "message": "请输入账号和密码"}

        username = username_or_email
        if "@" in username_or_email:
            account_row = await self._fetch_account_by_email(username_or_email)
            if account_row is None:
                return {"success": False, "message": "用户名或密码错误"}
            username = str(account_row["username"])

        try:
            tokens = await self._auth_service.login(
                LoginRequest(username=username, password=password)
            )
        except AppError:
            return {"success": False, "message": "用户名或密码错误"}

        return {
            "success": True,
            "message": "登录成功",
            "access_token": tokens.accessToken,
            "refresh_token": tokens.refreshToken,
            "user": {
                "id": tokens.account.accountId,
                "username": tokens.account.username,
                "full_name": tokens.account.ptaNickname or "",
                "student_id": tokens.account.studentId or "",
            },
        }

    async def refresh(self, payload: dict[str, Any]) -> dict[str, Any]:
        refresh_token = (
            payload.get("refresh_token")
            or payload.get("refreshToken")
            or payload.get("token")
            or ""
        )
        try:
            tokens = await self._auth_service.refresh(
                RefreshRequest(refreshToken=str(refresh_token))
            )
        except AppError:
            return {"success": False, "message": "登录已过期"}
        return {
            "success": True,
            "access_token": tokens.accessToken,
            "refresh_token": tokens.refreshToken,
        }

    async def logout(
        self, current: AuthenticatedAccount | None, payload: dict[str, Any]
    ) -> dict[str, Any]:
        if current is None:
            return {"success": True}
        refresh_token = payload.get("refresh_token") or payload.get("refreshToken")
        await self._auth_service.logout(
            current, LogoutRequest(refreshToken=str(refresh_token) if refresh_token else None)
        )
        return {"success": True}

    async def register(self, payload: dict[str, Any]) -> dict[str, Any]:
        username = (payload.get("username") or "").strip()
        email = (payload.get("email") or "").strip()
        full_name = (payload.get("full_name") or "").strip()
        student_id = (payload.get("student_id") or "").strip()
        school = (payload.get("school") or "").strip()
        password = str(payload.get("password") or "")
        confirm_password = str(payload.get("confirm_password") or "")
        code = (payload.get("verification_code") or "").strip()

        if not re.match(r"^[A-Za-z0-9_]{3,15}$", username):
            return {
                "success": False,
                "message": "用户名长度为3-15个字符，只能包含字母、数字和下划线",
            }
        if not EMAIL_RE.match(email):
            return {"success": False, "message": "邮箱格式不正确"}
        if not full_name or not student_id or not school:
            return {"success": False, "message": "请完整填写注册信息"}
        if password != confirm_password:
            return {"success": False, "message": "两次输入的密码不一致"}
        if not code:
            return {"success": False, "message": "请填写邮箱验证码"}
        if await self.email_exists(email):
            return {"success": False, "message": "该邮箱已被注册"}

        code_ok, code_message = await self._email_service.verify_registration_code(
            email, code
        )
        if not code_ok:
            return {"success": False, "message": f"验证码错误: {code_message}"}

        try:
            await self._auth_service.register(
                RegisterRequest(
                    username=username,
                    password=password,
                    studentId=student_id,
                    ptaNickname=full_name,
                    email=email,
                )
            )
        except AppError as exc:
            return {"success": False, "message": exc.message}

        created = await self._fetch_account_by_email(email)
        if created is not None:
            await self._upsert_email_contact(
                int(created["account_id"]), email, is_verified=True
            )
        await self._email_service.clear_registration_code(email)
        return {"success": True, "message": "用户注册成功"}

    async def forgot_password(self, payload: dict[str, Any]) -> dict[str, Any]:
        action = (payload.get("action") or "").strip()
        email = (payload.get("email") or "").strip()
        if action == "send_reset_email":
            if not email:
                return {"success": False, "message": "请填写邮箱"}
            account = await self._fetch_account_by_email(email)
            if account is None:
                return {
                    "success": True,
                    "message": "如果该邮箱已注册，您将收到重置验证码",
                }
            result = await self._email_service.send_password_reset_email(
                email, str(account.get("username") or "用户")
            )
            if result.success:
                return {"success": True, "message": "如果该邮箱已注册，您将收到重置验证码"}
            return {"success": False, "message": f"邮件发送失败: {result.message}"}

        if action == "reset_password":
            code = (payload.get("reset_code") or "").strip()
            new_password = str(payload.get("new_password") or "")
            if not email or not code or not new_password:
                return {"success": False, "message": "请填写邮箱、验证码和新密码"}
            account = await self._fetch_account_by_email(email)
            if account is None:
                return {"success": False, "message": "验证码错误或邮箱不存在"}
            ok, message = await self._email_service.verify_reset_code(
                email, code, mark_used=True
            )
            if not ok:
                return {"success": False, "message": message}
            await self._update_password(int(account["account_id"]), new_password)
            await self._email_service.clear_reset_code(email)
            await self._email_service.send_password_change_notification(
                email, str(account.get("username") or "用户")
            )
            return {"success": True, "message": "密码重置成功"}

        return {"success": False, "message": "无效的操作"}

    async def update_profile(
        self, current: AuthenticatedAccount, payload: dict[str, Any]
    ) -> dict[str, Any]:
        email = (payload.get("email") or "").strip()
        student_id = (payload.get("student_id") or "").strip()
        pta_nickname = (payload.get("full_name") or payload.get("pta_nickname") or "").strip()
        if not email or not student_id:
            return {"success": False, "message": "请填写邮箱和学号"}
        if not EMAIL_RE.match(email):
            return {"success": False, "message": "邮箱格式不正确"}

        existing = await self._fetch_account_by_email(email)
        if existing is not None and int(existing["account_id"]) != current.account_id:
            return {"success": False, "message": "该邮箱已被注册"}

        try:
            await self._auth_service.update_profile(
                current,
                AuthProfileUpdateRequest(
                    studentId=student_id,
                    ptaNickname=pta_nickname or None,
                ),
            )
        except AppError as exc:
            if exc.code not in {"AUTH_PROFILE_NOT_FOUND", "AUTH_STUDENT_ID_IMMUTABLE"}:
                return {"success": False, "message": exc.message}

        await self._upsert_email_contact(current.account_id, email, is_verified=True)
        return {"success": True, "message": "资料已更新"}

    async def change_password(
        self, current: AuthenticatedAccount, payload: dict[str, Any]
    ) -> dict[str, Any]:
        current_password = str(payload.get("current_password") or "")
        new_password = str(payload.get("new_password") or "")
        email = (payload.get("email") or "").strip()
        reset_code = (payload.get("reset_code") or "").strip()
        if not current_password or not new_password or not email or not reset_code:
            return {"success": False, "message": "请填写完整信息"}

        account = await self._repository.fetch_account_by_id(current.account_id)
        if account is None:
            return {"success": False, "message": "用户不存在"}
        known_email = await self._fetch_email_for_account(current.account_id)
        if (known_email or "").lower() != email.lower():
            return {"success": False, "message": "邮箱与当前账号不一致"}
        pepper = os.getenv(self._settings.auth.password_pepper_env, "")
        if not verify_password(current_password, account.password_hash, pepper=pepper):
            return {"success": False, "message": "当前密码错误"}
        ok, message = await self._email_service.verify_reset_code(
            email, reset_code, mark_used=True
        )
        if not ok:
            return {"success": False, "message": message}

        await self._update_password(current.account_id, new_password)
        await self._email_service.clear_reset_code(email)
        await self._email_service.send_password_change_notification(
            email, account.username
        )
        return {"success": True, "message": "密码修改成功"}

    async def email_exists(self, email: str) -> bool:
        return await self._fetch_account_by_email(email) is not None

    async def _update_password(self, account_id: int, new_password: str) -> None:
        if len(new_password.strip()) < 8:
            raise AppError(
                status_code=400,
                code="AUTH_PASSWORD_TOO_SHORT",
                message="Password must be at least 8 characters.",
            )
        pepper = os.getenv(self._settings.auth.password_pepper_env, "")
        password_hash = hash_password(new_password, pepper=pepper)
        await self._execute(
            """
            UPDATE ascendany.user_accounts
            SET password_hash = %s,
                local_password_enabled = TRUE,
                local_password_set_at = now(),
                updated_at = now()
            WHERE account_id = %s
            """,
            (password_hash, account_id),
        )

    async def _fetch_account_by_email(self, email: str) -> dict[str, Any] | None:
        return await self._fetch_one(
            """
            SELECT ua.account_id, ua.username, ua.display_name, ua.password_hash
            FROM ascendany.user_contacts AS uc
            JOIN ascendany.user_accounts AS ua ON ua.account_id = uc.account_id
            WHERE uc.type = 'email'
              AND uc.value_normalized = lower(BTRIM(%s))
              AND ua.is_active = TRUE
            LIMIT 1
            """,
            (email,),
        )

    async def _fetch_email_for_account(self, account_id: int) -> str | None:
        row = await self._fetch_one(
            """
            SELECT value
            FROM ascendany.user_contacts
            WHERE account_id = %s AND type = 'email'
            ORDER BY is_verified DESC, contact_id ASC
            LIMIT 1
            """,
            (account_id,),
        )
        return str(row["value"]) if row and row.get("value") is not None else None

    async def _upsert_email_contact(
        self, account_id: int, email: str, *, is_verified: bool
    ) -> None:
        await self._execute(
            """
            INSERT INTO ascendany.user_contacts (account_id, type, value, is_verified)
            VALUES (%s, 'email', %s, %s)
            ON CONFLICT (type, value_normalized)
            DO UPDATE SET
                account_id = EXCLUDED.account_id,
                value = EXCLUDED.value,
                is_verified = EXCLUDED.is_verified,
                updated_at = now()
            """,
            (account_id, email, is_verified),
        )

    async def _fetch_one(self, query: str, params: tuple[Any, ...]) -> dict[str, Any] | None:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                row = await cursor.fetchone()
        return dict(row) if row else None

    async def _execute(self, query: str, params: tuple[Any, ...]) -> None:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, params)
