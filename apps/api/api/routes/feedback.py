from __future__ import annotations

import base64
import html
import logging
import os
import re
from datetime import UTC, datetime

from fastapi import APIRouter, Depends, Request

from ..deps import get_current_account_optional, get_repository, get_settings
from ...core.config import Settings
from ...core.errors import AppError
from ...schemas.feedback import FeedbackSubmitRequest, FeedbackSubmitResponse
from ...services.auth import AuthenticatedAccount
from ...services.web_email import EmailAttachment, LegacyEmailService

logger = logging.getLogger(__name__)

router = APIRouter(tags=["feedback"])

MAX_FEEDBACK_IMAGES = 8
MAX_FEEDBACK_IMAGE_BYTES = 8 * 1024 * 1024
DATA_URL_RE = re.compile(
    r"^data:(image/[a-zA-Z0-9.+-]+);base64,([A-Za-z0-9+/=\s]+)$"
)


def _safe_filename(name: str, index: int, content_type: str) -> str:
    extension = content_type.split("/", 1)[1].split("+", 1)[0].lower() or "png"
    cleaned = re.sub(r"[^a-zA-Z0-9._-]+", "_", name.strip())[:120].strip("._")
    if not cleaned:
        cleaned = f"screenshot_{index}"
    if not cleaned.lower().endswith(f".{extension}"):
        cleaned = f"{cleaned}.{extension}"
    return cleaned


def _decode_image(item_name: str, data_url: str, index: int) -> EmailAttachment:
    matched = DATA_URL_RE.fullmatch(data_url.strip())
    if not matched:
        raise AppError(
            status_code=400,
            code="FEEDBACK_IMAGE_INVALID",
            message="反馈截图格式不正确。",
        )
    content_type = matched.group(1).lower()
    encoded = re.sub(r"\s+", "", matched.group(2))
    try:
        content = base64.b64decode(encoded, validate=True)
    except Exception as exc:
        raise AppError(
            status_code=400,
            code="FEEDBACK_IMAGE_INVALID",
            message="反馈截图格式不正确。",
        ) from exc
    if len(content) > MAX_FEEDBACK_IMAGE_BYTES:
        raise AppError(
            status_code=400,
            code="FEEDBACK_IMAGE_TOO_LARGE",
            message="单张反馈截图不能超过 8MB。",
        )
    return EmailAttachment(
        filename=_safe_filename(item_name, index, content_type),
        content=content,
        content_type=content_type,
    )


def _line(label: str, value: str | None) -> str:
    return f"{label}: {value or '-'}"


def _account_lines(current: AuthenticatedAccount | None) -> list[str]:
    if current is None:
        return ["登录用户: 未登录或未携带令牌"]
    return [
        _line("账号ID", str(current.account_id)),
        _line("用户名", current.username),
        _line("显示名", current.display_name),
        _line("学生ID", current.student_id),
        _line("PTA昵称", current.pta_nickname),
        _line("是否管理员", "是" if current.is_admin else "否"),
    ]


@router.post("/feedback", response_model=FeedbackSubmitResponse)
async def submit_feedback(
    payload: FeedbackSubmitRequest,
    request: Request,
    current: AuthenticatedAccount | None = Depends(get_current_account_optional),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> FeedbackSubmitResponse:
    if len(payload.images) > MAX_FEEDBACK_IMAGES:
        raise AppError(
            status_code=400,
            code="FEEDBACK_TOO_MANY_IMAGES",
            message=f"最多上传 {MAX_FEEDBACK_IMAGES} 张反馈截图。",
        )

    attachments = [
        _decode_image(item.name, item.dataUrl, index + 1)
        for index, item in enumerate(payload.images)
    ]
    target_email = (
        os.getenv(settings.email.feedback_to_env, "").strip()
        or settings.email.feedback_to_default
    )
    sent_at = datetime.now(UTC).isoformat()
    client_host = request.client.host if request.client else "-"
    account_lines = _account_lines(current)

    text_lines = [
        "AscendAny 用户反馈",
        "",
        _line("标题", payload.title),
        "内容:",
        payload.content,
        "",
        "---",
        _line("发送时间", sent_at),
        _line("客户端IP", client_host),
        _line("平台", payload.platform),
        _line("应用版本", payload.appVersion),
        _line("User-Agent", payload.userAgent),
        _line("附件数量", str(len(attachments))),
        *account_lines,
    ]
    text_content = "\n".join(text_lines)

    metadata_html = "\n".join(
        f"<p><strong>{html.escape(line.split(':', 1)[0])}:</strong> "
        f"{html.escape(line.split(':', 1)[1].strip())}</p>"
        for line in [
            _line("发送时间", sent_at),
            _line("客户端IP", client_host),
            _line("平台", payload.platform),
            _line("应用版本", payload.appVersion),
            _line("User-Agent", payload.userAgent),
            _line("附件数量", str(len(attachments))),
            *account_lines,
        ]
        if ":" in line
    )
    html_content = f"""
    <!DOCTYPE html>
    <html>
    <head><meta charset="utf-8"><title>AscendAny 用户反馈</title></head>
    <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #222;">
      <h2>AscendAny 用户反馈</h2>
      <p><strong>标题：</strong>{html.escape(payload.title)}</p>
      <p><strong>内容：</strong><br/>{html.escape(payload.content).replace(chr(10), "<br/>")}</p>
      <hr/>
      {metadata_html}
    </body>
    </html>
    """

    email_service = LegacyEmailService(settings=settings, repository=repository)
    result = await email_service.send_feedback_email(
        to_email=target_email,
        subject=f"[AscendAny 反馈] {payload.title}",
        html_content=html_content,
        text_content=text_content,
        attachments=attachments,
    )
    if not result.success:
        logger.warning("Feedback email send failed: %s", result.message)
        raise AppError(
            status_code=503,
            code="FEEDBACK_SEND_FAILED",
            message="反馈发送失败，请稍后重试。",
        )

    return FeedbackSubmitResponse(success=True, message="反馈已发送，感谢你的反馈。")
