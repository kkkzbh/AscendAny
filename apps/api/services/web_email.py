from __future__ import annotations

import asyncio
import logging
import os
import random
import re
import smtplib
import string
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from email.header import Header
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from email.utils import formataddr
from typing import Any

from psycopg.rows import dict_row

from ..core.config import Settings

logger = logging.getLogger(__name__)

EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
PURPOSE_REGISTRATION = "registration"
PURPOSE_RESET = "reset"


@dataclass(slots=True)
class EmailSendResult:
    success: bool
    code: str | None
    message: str


class LegacyEmailService:
    """AscendAny-native port of the old AscendWeb SMTP verification service."""

    def __init__(self, settings: Settings, repository: Any) -> None:
        self._settings = settings
        self._repository = repository

    async def send_registration_verification(
        self, email: str, username: str = "用户"
    ) -> EmailSendResult:
        email = self._clean_email(email)
        can_send, message = await self._check_send_frequency(
            email,
            PURPOSE_REGISTRATION,
            self._settings.email.registration_cooldown_seconds,
        )
        if not can_send:
            return EmailSendResult(False, None, message)

        code = self._generate_code()
        subject = "【验证码】用户注册验证"
        html_content = f"""
        <html>
        <body style="font-family: Arial; color: #333;">
            <h3>注册验证码</h3>
            <p>您的验证码是：<strong style="font-size: 24px; color: #007bff;">{code}</strong></p>
            <p>验证码有效期10分钟，请及时使用。</p>
            <p style="color: #666; font-size: 12px;">此邮件由系统自动发送，请勿回复。</p>
        </body>
        </html>
        """
        text_content = f"注册验证码：{code}\n有效期10分钟，请及时使用。"
        sent, send_message = await asyncio.to_thread(
            self._send_email,
            email,
            subject,
            html_content,
            text_content,
            "high",
        )
        if not sent:
            return EmailSendResult(False, None, send_message)

        await self._store_code(
            email,
            PURPOSE_REGISTRATION,
            code,
            self._settings.email.registration_ttl_seconds,
        )
        return EmailSendResult(True, code, "验证邮件发送成功")

    async def send_password_reset_email(
        self, email: str, username: str
    ) -> EmailSendResult:
        email = self._clean_email(email)
        can_send, message = await self._check_send_frequency(
            email,
            PURPOSE_RESET,
            self._settings.email.reset_cooldown_seconds,
        )
        if not can_send:
            return EmailSendResult(False, None, message)

        code = self._generate_code()
        subject = "【验证码】密码重置验证"
        html_content = f"""
        <!DOCTYPE html>
        <html>
        <head><meta charset="utf-8"><title>密码重置验证码</title></head>
        <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
            <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
                <h2 style="color: #dc3545;">密码重置验证码</h2>
                <p>亲爱的 <strong>{username}</strong>，</p>
                <p>我们收到了您的密码重置请求。您的验证码是：</p>
                <div style="text-align: center; margin: 30px 0;">
                    <span style="background-color: #f8f9fa; color: #dc3545; padding: 15px 30px; font-size: 32px; font-weight: bold; border-radius: 8px; border: 2px dashed #dc3545; display: inline-block; letter-spacing: 5px;">{code}</span>
                </div>
                <p><strong>安全提醒：</strong></p>
                <ul>
                    <li>验证码有效期为15分钟</li>
                    <li>请在密码重置页面输入此验证码</li>
                    <li>如果您没有请求重置密码，请忽略此邮件</li>
                    <li>为了账户安全，请不要将验证码分享给他人</li>
                </ul>
                <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
                <p style="color: #666; font-size: 12px;">此邮件由系统自动发送，请勿回复。</p>
            </div>
        </body>
        </html>
        """
        text_content = f"""
        密码重置验证码

        亲爱的 {username}，

        我们收到了您的密码重置请求。您的验证码是：{code}

        验证码有效期为15分钟，请及时使用。

        如果您没有请求重置密码，请忽略此邮件。
        """
        sent, send_message = await asyncio.to_thread(
            self._send_email,
            email,
            subject,
            html_content,
            text_content,
            "high",
        )
        if not sent:
            return EmailSendResult(False, None, send_message)

        await self._store_code(
            email,
            PURPOSE_RESET,
            code,
            self._settings.email.reset_ttl_seconds,
        )
        return EmailSendResult(True, code, "密码重置验证码发送成功")

    async def verify_registration_code(self, email: str, code: str) -> tuple[bool, str]:
        return await self._verify_code(email, PURPOSE_REGISTRATION, code, mark_used=True)

    async def verify_reset_code(
        self, email: str, code: str, *, mark_used: bool = False
    ) -> tuple[bool, str]:
        return await self._verify_code(email, PURPOSE_RESET, code, mark_used=mark_used)

    async def clear_registration_code(self, email: str) -> None:
        await self._clear_code(email, PURPOSE_REGISTRATION)

    async def clear_reset_code(self, email: str) -> None:
        await self._clear_code(email, PURPOSE_RESET)

    async def send_password_change_notification(self, email: str, username: str) -> None:
        subject = "密码修改通知"
        now_text = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        html_content = f"""
        <!DOCTYPE html>
        <html>
        <head><meta charset="utf-8"><title>密码修改通知</title></head>
        <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
            <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
                <h2 style="color: #28a745;">密码修改成功</h2>
                <p>亲爱的 <strong>{username}</strong>，</p>
                <p>您的账户密码已成功修改。</p>
                <div style="background-color: #d4edda; border: 1px solid #c3e6cb; padding: 15px; border-radius: 5px; margin: 20px 0;">
                    <p style="margin: 0;"><strong>修改时间：</strong>{now_text}</p>
                </div>
                <p><strong>安全提醒：</strong></p>
                <ul>
                    <li>如果这不是您本人操作，请立即联系管理员</li>
                    <li>建议定期更换密码以保护账户安全</li>
                    <li>请妥善保管您的新密码</li>
                </ul>
                <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
                <p style="color: #666; font-size: 12px;">此邮件由系统自动发送，请勿回复。</p>
            </div>
        </body>
        </html>
        """
        text_content = f"密码修改成功\n\n亲爱的 {username}，您的账户密码已成功修改。\n修改时间：{now_text}"
        await asyncio.to_thread(
            self._send_email,
            self._clean_email(email),
            subject,
            html_content,
            text_content,
            "normal",
        )

    def _send_email(
        self,
        to_email: str,
        subject: str,
        html_content: str,
        text_content: str | None = None,
        priority: str = "high",
    ) -> tuple[bool, str]:
        cfg = self._settings.email
        host = (cfg.host or "").strip()
        port = int(cfg.port or 0)
        user = os.getenv(cfg.user_env, "").strip()
        password = os.getenv(cfg.password_env, "").strip()
        from_email = os.getenv(cfg.from_env, "").strip()
        from_name = cfg.from_name or ""
        if not host or not port or not user or not password or not from_email:
            return (
                False,
                "邮件配置缺失(EMAIL_HOST/EMAIL_PORT/EMAIL_USER/EMAIL_PASS/EMAIL_FROM)",
            )
        if not self._is_valid_email(to_email):
            return False, "收件人邮箱格式不正确"

        try:
            msg = MIMEMultipart("alternative")
            msg["From"] = formataddr((from_name, from_email))
            msg["To"] = to_email
            msg["Subject"] = str(Header(subject, "utf-8"))
            if priority == "high":
                msg["X-Priority"] = "1"
                msg["X-MSMail-Priority"] = "High"
                msg["Importance"] = "High"
            msg["X-Mailer"] = "Python EmailService"
            msg["Message-ID"] = f"<{random.randint(1000000, 9999999)}@{host}>"
            if text_content:
                msg.attach(MIMEText(text_content, "plain", "utf-8"))
            msg.attach(MIMEText(html_content, "html", "utf-8"))

            use_ssl = cfg.use_ssl if cfg.use_ssl is not None else port == 465
            use_tls = cfg.use_tls if cfg.use_tls is not None else port == 587
            server = None
            try:
                if use_ssl:
                    server = smtplib.SMTP_SSL(host, port, timeout=cfg.timeout_seconds)
                    server.ehlo()
                    server.set_debuglevel(0)
                else:
                    server = smtplib.SMTP(host, port, timeout=cfg.timeout_seconds)
                    server.ehlo()
                    if use_tls:
                        server.starttls()
                        server.ehlo()
                    server.set_debuglevel(0)
                server.login(user, password)
                server.send_message(msg, from_email, [to_email])
            finally:
                if server is not None:
                    try:
                        server.quit()
                    except Exception:
                        pass
            return True, "邮件发送成功"
        except smtplib.SMTPAuthenticationError as exc:
            return False, f"SMTP认证失败: {exc}"
        except smtplib.SMTPConnectError as exc:
            return False, f"SMTP连接失败: {exc}"
        except smtplib.SMTPServerDisconnected as exc:
            return False, f"SMTP服务器断开连接: {exc}"
        except smtplib.SMTPException as exc:
            return False, f"SMTP错误: {exc}"
        except ConnectionError as exc:
            return False, f"网络连接错误: {exc}"
        except TimeoutError as exc:
            return False, f"连接超时: {exc}"
        except Exception as exc:
            logger.exception("Email send failed")
            return False, f"邮件发送失败: {exc}"

    async def _check_send_frequency(
        self, email: str, purpose: str, limit_seconds: int
    ) -> tuple[bool, str]:
        query = """
            SELECT sent_at
            FROM ascendany.email_verification_codes
            WHERE email_normalized = lower(BTRIM(%s))
              AND purpose = %s
            ORDER BY sent_at DESC
            LIMIT 1
        """
        row = await self._fetch_one(query, (email, purpose))
        if not row or not isinstance(row.get("sent_at"), datetime):
            return True, "可以发送"
        delta = datetime.now(UTC) - row["sent_at"]
        if delta.total_seconds() < limit_seconds:
            return False, f"请等待{limit_seconds}秒后再次发送"
        return True, "可以发送"

    async def _store_code(
        self, email: str, purpose: str, code: str, expires_seconds: int
    ) -> None:
        query = """
            WITH latest AS (
                SELECT code_id
                FROM ascendany.email_verification_codes
                WHERE email_normalized = lower(BTRIM(%s))
                  AND purpose = %s
                  AND used_at IS NULL
                  AND expires_at > now()
                ORDER BY sent_at DESC
                LIMIT 1
            )
            UPDATE ascendany.email_verification_codes AS c
            SET code = %s,
                sent_at = now(),
                expires_at = now() + (%s || ' seconds')::interval,
                send_count = c.send_count + 1,
                updated_at = now()
            FROM latest
            WHERE c.code_id = latest.code_id
            RETURNING c.code_id
        """
        row = await self._fetch_one(query, (email, purpose, code, int(expires_seconds)))
        if row:
            return
        insert = """
            INSERT INTO ascendany.email_verification_codes
                (email, purpose, code, sent_at, expires_at, send_count)
            VALUES (%s, %s, %s, now(), now() + (%s || ' seconds')::interval, 1)
        """
        await self._execute(insert, (email, purpose, code, int(expires_seconds)))

    async def _verify_code(
        self, email: str, purpose: str, code: str, *, mark_used: bool
    ) -> tuple[bool, str]:
        query = """
            SELECT code_id, code
            FROM ascendany.email_verification_codes
            WHERE email_normalized = lower(BTRIM(%s))
              AND purpose = %s
              AND used_at IS NULL
              AND expires_at > now()
            ORDER BY sent_at DESC
            LIMIT 1
        """
        row = await self._fetch_one(query, (self._clean_email(email), purpose))
        if not row:
            return False, "验证码已过期或不存在"
        if str(row.get("code") or "") != str(code or "").strip():
            return False, "验证码错误"
        if mark_used:
            await self._execute(
                """
                UPDATE ascendany.email_verification_codes
                SET used_at = now(), updated_at = now()
                WHERE code_id = %s
                """,
                (row["code_id"],),
            )
        return True, "验证成功" if purpose == PURPOSE_REGISTRATION else "验证码验证成功"

    async def _clear_code(self, email: str, purpose: str) -> None:
        await self._execute(
            """
            DELETE FROM ascendany.email_verification_codes
            WHERE email_normalized = lower(BTRIM(%s))
              AND purpose = %s
            """,
            (self._clean_email(email), purpose),
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

    @staticmethod
    def _generate_code(length: int = 6) -> str:
        return "".join(random.choices(string.digits, k=length))

    @staticmethod
    def _clean_email(email: str) -> str:
        return (email or "").strip()

    @staticmethod
    def _is_valid_email(email: str) -> bool:
        if not email or "\n" in email or "\r" in email:
            return False
        return bool(EMAIL_RE.match(email))
