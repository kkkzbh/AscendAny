from __future__ import annotations

from fastapi import Request

from ..core.config import Settings


def get_settings(request: Request) -> Settings:
    return request.app.state.settings


def get_repository(request: Request):
    return request.app.state.repository


def get_llm_service(request: Request):
    return request.app.state.llm_service
