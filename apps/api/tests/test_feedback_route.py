from __future__ import annotations

import base64

from fastapi.testclient import TestClient

from apps.api.api.routes import feedback as feedback_route
from apps.api.core.config import Settings
from apps.api.main import create_app
from apps.api.services.web_email import LegacyEmailService


def _data_url(raw: bytes, content_type: str = "image/png") -> str:
    return f"data:{content_type};base64,{base64.b64encode(raw).decode('ascii')}"


def _client(monkeypatch, send_result: tuple[bool, str] = (True, "邮件发送成功")):
    calls: list[dict[str, object]] = []

    def fake_send_email(
        self,
        to_email,
        subject,
        html_content,
        text_content=None,
        priority="high",
        attachments=None,
    ):
        calls.append(
            {
                "to_email": to_email,
                "subject": subject,
                "html_content": html_content,
                "text_content": text_content,
                "priority": priority,
                "attachments": attachments or [],
            }
        )
        return send_result

    monkeypatch.setattr(LegacyEmailService, "_send_email", fake_send_email)
    monkeypatch.setenv("ASCENDANY_FEEDBACK_TO", "uika@foxmail.com")
    settings = Settings()
    app = create_app(settings=settings, repository=object(), llm_service=object())
    return TestClient(app), calls


def test_submit_feedback_sends_email_with_metadata_and_attachment(monkeypatch):
    client, calls = _client(monkeypatch)
    with client:
        response = client.post(
            "/api/v1/feedback",
            json={
                "title": "截图异常",
                "content": "打开设置页后截图区域异常。",
                "images": [{"name": "screen.png", "dataUrl": _data_url(b"png")}],
                "platform": "linux",
                "appVersion": "0.1.0",
                "userAgent": "Vitest",
            },
        )

    assert response.status_code == 200
    assert response.json() == {"success": True, "message": "反馈已发送，感谢你的反馈。"}
    assert len(calls) == 1
    call = calls[0]
    assert call["to_email"] == "uika@foxmail.com"
    assert call["subject"] == "[AscendAny 反馈] 截图异常"
    assert "打开设置页后截图区域异常。" in str(call["text_content"])
    assert "平台: linux" in str(call["text_content"])
    attachments = call["attachments"]
    assert len(attachments) == 1
    assert attachments[0].filename == "screen.png"
    assert attachments[0].content == b"png"
    assert attachments[0].content_type == "image/png"


def test_submit_feedback_requires_title_and_content(monkeypatch):
    client, _ = _client(monkeypatch)
    with client:
        response = client.post(
            "/api/v1/feedback",
            json={"title": " ", "content": "内容", "images": []},
        )

    assert response.status_code == 422


def test_submit_feedback_rejects_invalid_and_too_many_images(monkeypatch):
    client, _ = _client(monkeypatch)
    with client:
        invalid = client.post(
            "/api/v1/feedback",
            json={
                "title": "异常",
                "content": "内容",
                "images": [{"name": "bad.txt", "dataUrl": "not-a-data-url"}],
            },
        )
        too_many = client.post(
            "/api/v1/feedback",
            json={
                "title": "异常",
                "content": "内容",
                "images": [
                    {"name": f"{index}.png", "dataUrl": _data_url(b"x")}
                    for index in range(9)
                ],
            },
        )

    assert invalid.status_code == 400
    assert invalid.json()["error"]["code"] == "FEEDBACK_IMAGE_INVALID"
    assert too_many.status_code == 422


def test_submit_feedback_rejects_oversized_images(monkeypatch):
    monkeypatch.setattr(feedback_route, "MAX_FEEDBACK_IMAGE_BYTES", 4)
    client, _ = _client(monkeypatch)
    with client:
        response = client.post(
            "/api/v1/feedback",
            json={
                "title": "异常",
                "content": "内容",
                "images": [{"name": "large.png", "dataUrl": _data_url(b"12345")}],
            },
        )

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "FEEDBACK_IMAGE_TOO_LARGE"


def test_submit_feedback_hides_smtp_failure_details(monkeypatch):
    client, _ = _client(monkeypatch, send_result=(False, "SMTP认证失败: secret-token"))
    with client:
        response = client.post(
            "/api/v1/feedback",
            json={"title": "异常", "content": "内容", "images": []},
        )

    assert response.status_code == 503
    body = response.json()
    assert body["error"]["code"] == "FEEDBACK_SEND_FAILED"
    assert body["error"]["message"] == "反馈发送失败，请稍后重试。"
    assert "secret-token" not in str(body)
