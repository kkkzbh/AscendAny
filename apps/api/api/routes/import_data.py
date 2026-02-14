"""Import / preprocess management API routes.

All endpoints require admin authentication.
"""

from __future__ import annotations

import asyncio
import json
import logging
import sys
import threading
from pathlib import Path
from typing import Any

from fastapi import APIRouter, Depends, Query
from fastapi.responses import StreamingResponse
from psycopg.rows import dict_row

from ..deps import get_admin_account, get_repository, get_settings
from ...core.config import Settings
from ...schemas.import_data import (
    DiscoverExamItem,
    DiscoverFileItem,
    DiscoverResponse,
    ImportRunRequest,
    ImportRunResponse,
    IngestHistoryItem,
    IngestHistoryResponse,
    LinkActorsRequest,
    LinkActorsResponse,
)
from ...services.auth import AuthenticatedAccount
from ...services.import_task import ImportTaskManager, TaskEvent, get_task_manager

logger = logging.getLogger(__name__)

router = APIRouter(tags=["import"], prefix="/import")

# ── helpers ───────────────────────────────────────────────

# Add the project root to sys.path so we can import the preprocess package
_PROJECT_ROOT = Path(__file__).resolve().parents[4]
if str(_PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(_PROJECT_ROOT))


def _load_preprocess_settings() -> Any:
    """Load preprocess.config.Settings using the preprocess config."""
    from preprocess.config import load_settings as pp_load_settings

    return pp_load_settings()


def _create_preprocess_connection(pp_settings: Any) -> Any:
    """Create a synchronous psycopg connection for the preprocess module."""
    from preprocess.db import connect as pp_connect

    return pp_connect(pp_settings)


# ── GET /import/discover ──────────────────────────────────


@router.get("/discover", response_model=DiscoverResponse)
async def discover_exams(
    exam_type: list[str] | None = Query(default=None, alias="examType"),
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> DiscoverResponse:
    """Scan the practice directory and return all exam units with change status."""

    def _run() -> DiscoverResponse:
        from preprocess.discover import discover_exam_units

        pp_settings = _load_preprocess_settings()
        fingerprint_roles = set(pp_settings.ingest.fingerprint_roles)
        discovered = discover_exam_units(
            practice_root=pp_settings.practice_root,
            exam_types=exam_type,
            fingerprint_roles=fingerprint_roles,
        )

        # Compare with DB to determine change status
        conn = _create_preprocess_connection(pp_settings)
        try:
            from preprocess.load import Repository

            repo = Repository(conn)
            exam_types_set: set[str] = set()
            items: list[DiscoverExamItem] = []
            changed_count = 0

            for unit in discovered:
                exam_types_set.add(unit.exam_type)
                previous_fp = repo.get_latest_success_fingerprint(
                    unit.exam_type, unit.source_path
                )
                has_changed = previous_fp != unit.fingerprint
                if has_changed:
                    changed_count += 1

                files = [
                    DiscoverFileItem(
                        fileRole=f.file_role,
                        relativePath=f.relative_path,
                        sha256=f.sha256,
                    )
                    for f in unit.files
                ]
                items.append(
                    DiscoverExamItem(
                        examType=unit.exam_type,
                        sourcePath=unit.source_path,
                        fingerprint=unit.fingerprint,
                        fileCount=len(unit.files),
                        hasChanged=has_changed,
                        files=files,
                    )
                )
        finally:
            conn.close()

        return DiscoverResponse(
            examTypes=sorted(exam_types_set),
            exams=items,
            totalCount=len(items),
            changedCount=changed_count,
        )

    return await asyncio.to_thread(_run)


# ── POST /import/run ──────────────────────────────────────


@router.post("/run", response_model=ImportRunResponse)
async def start_import_run(
    body: ImportRunRequest,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> ImportRunResponse:
    """Start an incremental import task. Returns a run_id for SSE streaming."""
    tm = get_task_manager()
    run_id = tm.create_task("import")

    def _worker() -> None:
        try:
            tm.mark_running(run_id)
            tm.emit_log(run_id, "info", "Starting incremental import ...")

            pp_settings = _load_preprocess_settings()
            conn = _create_preprocess_connection(pp_settings)
            try:
                from preprocess.load import IngestService, Repository

                repo = Repository(conn)
                service = IngestService(repo=repo, settings=pp_settings)

                def _on_progress(event_type: str, data: dict[str, Any]) -> None:
                    if event_type == "done":
                        tm.emit_done(run_id, data)
                    elif event_type == "log":
                        tm.emit_log(run_id, data.get("level", "info"), data.get("message", ""))
                    elif event_type == "progress":
                        tm.emit_progress(
                            run_id,
                            data.get("current", 0),
                            data.get("total", 0),
                            exam_type=data.get("examType"),
                            source_path=data.get("sourcePath"),
                            phase=data.get("phase"),
                        )
                    else:
                        tm.emit(run_id, TaskEvent(event_type=event_type, data=data))

                service.run(
                    exam_types=body.examTypes,
                    limit=body.limit,
                    dry_run=body.dryRun,
                    force=body.force,
                    on_progress=_on_progress,
                )
            finally:
                conn.close()
        except Exception as exc:
            logger.exception("Import task %s failed", run_id)
            tm.emit_error(run_id, str(exc))

    thread = threading.Thread(target=_worker, daemon=True, name=f"import-{run_id}")
    thread.start()
    task = tm.get_task(run_id)
    if task:
        task.thread = thread

    return ImportRunResponse(
        runId=run_id,
        message="Import task started. Use SSE endpoint to monitor progress.",
    )


# ── GET /import/run/{run_id}/stream ───────────────────────


@router.get("/run/{run_id}/stream")
async def stream_import_run(
    run_id: str,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> StreamingResponse:
    """SSE stream for an import task."""
    tm = get_task_manager()
    return StreamingResponse(
        tm.event_stream(run_id),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )


# ── POST /import/link-actors ─────────────────────────────


@router.post("/link-actors", response_model=LinkActorsResponse)
async def start_link_actors(
    body: LinkActorsRequest,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> LinkActorsResponse:
    """Start an actor-linking task. Returns a run_id for SSE streaming."""
    tm = get_task_manager()
    run_id = tm.create_task("link-actors")

    def _worker() -> None:
        try:
            tm.mark_running(run_id)
            tm.emit_log(run_id, "info", "Starting actor linking ...")

            pp_settings = _load_preprocess_settings()
            conn = _create_preprocess_connection(pp_settings)
            try:
                from preprocess.linking import LinkActorsService
                from preprocess.load import Repository

                repo = Repository(conn)
                service = LinkActorsService(repo=repo, settings=pp_settings)

                def _on_progress(event_type: str, data: dict[str, Any]) -> None:
                    if event_type == "done":
                        tm.emit_done(run_id, data)
                    elif event_type == "log":
                        tm.emit_log(run_id, data.get("level", "info"), data.get("message", ""))
                    elif event_type == "progress":
                        tm.emit_progress(
                            run_id,
                            data.get("current", 0),
                            data.get("total", 0),
                            phase=data.get("phase"),
                        )

                service.run(
                    exam_types=body.examTypes,
                    limit=body.limit,
                    dry_run=body.dryRun,
                    on_progress=_on_progress,
                )
            finally:
                conn.close()
        except Exception as exc:
            logger.exception("Link-actors task %s failed", run_id)
            tm.emit_error(run_id, str(exc))

    thread = threading.Thread(target=_worker, daemon=True, name=f"link-{run_id}")
    thread.start()
    task = tm.get_task(run_id)
    if task:
        task.thread = thread

    return LinkActorsResponse(
        runId=run_id,
        message="Actor linking task started. Use SSE endpoint to monitor progress.",
    )


# ── GET /import/link-actors/{run_id}/stream ───────────────


@router.get("/link-actors/{run_id}/stream")
async def stream_link_actors(
    run_id: str,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> StreamingResponse:
    """SSE stream for a link-actors task."""
    tm = get_task_manager()
    return StreamingResponse(
        tm.event_stream(run_id),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )


# ── GET /import/history ───────────────────────────────────


@router.get("/history", response_model=IngestHistoryResponse)
async def get_ingest_history(
    limit: int = Query(default=20, ge=1, le=100),
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
) -> IngestHistoryResponse:
    """Return recent ingest run history."""
    query = """
        SELECT ingest_run_id, status, started_at, finished_at, meta
        FROM ascendany.ingest_runs
        ORDER BY started_at DESC
        LIMIT %s
    """
    count_query = "SELECT COUNT(*) FROM ascendany.ingest_runs"

    async with repository._pool.connection() as conn:
        async with conn.cursor(row_factory=dict_row) as cur:
            await cur.execute(count_query)
            total_row = await cur.fetchone()
            total = int(total_row["count"]) if total_row else 0

            await cur.execute(query, (limit,))
            rows = await cur.fetchall()

    items: list[IngestHistoryItem] = []
    for row in rows:
        meta = row.get("meta") or {}
        if isinstance(meta, str):
            try:
                meta = json.loads(meta)
            except (json.JSONDecodeError, TypeError):
                meta = {}
        items.append(
            IngestHistoryItem(
                ingestRunId=int(row["ingest_run_id"]),
                status=str(row["status"]),
                startedAt=row.get("started_at"),
                finishedAt=row.get("finished_at"),
                scanned=meta.get("scanned"),
                toProcess=meta.get("to_process"),
                succeeded=meta.get("succeeded"),
                failed=meta.get("failed"),
                errors=meta.get("errors", []),
            )
        )

    return IngestHistoryResponse(items=items, total=total)


# ── GET /import/tasks ─────────────────────────────────────


@router.get("/tasks")
async def list_active_tasks(
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> list[dict[str, Any]]:
    """List recent in-memory tasks (for debugging/monitoring)."""
    tm = get_task_manager()
    return tm.list_tasks()
