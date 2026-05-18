from __future__ import annotations

import asyncio
import json
from datetime import UTC, datetime
from typing import AsyncGenerator

from fastapi import APIRouter, Depends
from fastapi.responses import StreamingResponse

from ..deps import get_repository
from ...schemas.meta import LatestExamImportedAtResponse

router = APIRouter(tags=["meta"])

DATA_EVENT_POLL_SECONDS = 5.0
DATA_EVENT_HEARTBEAT_SECONDS = 15.0


@router.get(
    "/meta/latest_exam_imported_at", response_model=LatestExamImportedAtResponse
)
async def latest_exam_imported_at(
    repository=Depends(get_repository),
) -> LatestExamImportedAtResponse:
    latest = await repository.fetch_latest_exam_imported_at()
    return LatestExamImportedAtResponse(latestExamImportedAt=latest)


def _iso(value: datetime | None) -> str | None:
    if value is None:
        return None
    if value.tzinfo is None:
        value = value.replace(tzinfo=UTC)
    return value.isoformat()


def _sse(event_type: str, payload: dict[str, object]) -> str:
    return f"event: {event_type}\ndata: {json.dumps(payload, ensure_ascii=False, default=str)}\n\n"


async def data_event_generator(
    repository,
    *,
    poll_seconds: float = DATA_EVENT_POLL_SECONDS,
    heartbeat_seconds: float = DATA_EVENT_HEARTBEAT_SECONDS,
) -> AsyncGenerator[str, None]:
    latest = await repository.fetch_latest_exam_imported_at()
    latest_iso = _iso(latest)
    yield _sse(
        "snapshot",
        {
            "type": "snapshot",
            "latestExamImportedAt": latest_iso,
        },
    )

    last_heartbeat_at = datetime.now(UTC)
    while True:
        await asyncio.sleep(poll_seconds)
        current = await repository.fetch_latest_exam_imported_at()
        current_iso = _iso(current)
        if current_iso != latest_iso:
            latest_iso = current_iso
            yield _sse(
                "data_changed",
                {
                    "type": "data_changed",
                    "latestExamImportedAt": current_iso,
                },
            )

        now = datetime.now(UTC)
        if (now - last_heartbeat_at).total_seconds() >= heartbeat_seconds:
            yield _sse(
                "heartbeat",
                {
                    "type": "heartbeat",
                    "ts": now.isoformat(),
                },
            )
            last_heartbeat_at = now


@router.get("/meta/data-events/stream")
async def data_events_stream(
    repository=Depends(get_repository),
) -> StreamingResponse:
    return StreamingResponse(
        data_event_generator(repository),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )
