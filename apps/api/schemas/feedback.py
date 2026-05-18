from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field, field_validator


class FeedbackImageRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str = Field(default="screenshot.png", max_length=160)
    dataUrl: str = Field(max_length=12 * 1024 * 1024)

    @field_validator("name", mode="before")
    @classmethod
    def clean_name(cls, value: object) -> str:
        if not isinstance(value, str):
            return "screenshot.png"
        return value.strip() or "screenshot.png"


class FeedbackSubmitRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    title: str = Field(min_length=1, max_length=200)
    content: str = Field(min_length=1, max_length=10000)
    images: list[FeedbackImageRequest] = Field(default_factory=list, max_length=8)
    platform: str | None = Field(default=None, max_length=80)
    appVersion: str | None = Field(default=None, max_length=80)
    userAgent: str | None = Field(default=None, max_length=512)

    @field_validator("title", "content", mode="before")
    @classmethod
    def clean_required_text(cls, value: object) -> str:
        if not isinstance(value, str):
            return ""
        return value.strip()

    @field_validator("platform", "appVersion", "userAgent", mode="before")
    @classmethod
    def clean_optional_text(cls, value: object) -> str | None:
        if not isinstance(value, str):
            return None
        trimmed = value.strip()
        return trimmed or None


class FeedbackSubmitResponse(BaseModel):
    success: bool
    message: str
