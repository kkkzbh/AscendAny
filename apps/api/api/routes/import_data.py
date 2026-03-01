"""Import / preprocess management API routes.

All endpoints require admin authentication.

Flow:
  1. POST /import/upload  — Upload .zip, select examType → extract to practice_root
  2. POST /import/run     — Run incremental import (SSE progress)
  3. GET  /import/history  — Ingest run history
  4. GET  /import/tasks    — In-memory tasks (debug)
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import sys
import tempfile
import threading
import zipfile
from pathlib import Path
from typing import Any

from fastapi import APIRouter, Depends, Query, UploadFile, File, Form, HTTPException
from fastapi.responses import StreamingResponse
from psycopg.rows import dict_row

from ..deps import get_admin_account, get_repository
from ...schemas.import_data import (
    ImportRunRequest,
    ImportRunResponse,
    SingleImportRunRequest,
    IngestHistoryItem,
    IngestHistoryResponse,
    UploadResponse,
)
from ...services.auth import AuthenticatedAccount
from ...services.import_task import TaskEvent, get_task_manager

logger = logging.getLogger(__name__)

router = APIRouter(tags=["import"], prefix="/import")

# ── helpers ───────────────────────────────────────────────

_PROJECT_ROOT = Path(__file__).resolve().parents[4]
if str(_PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(_PROJECT_ROOT))

VALID_EXAM_TYPES = {"datastructure", "pta_icpc", "pta_ioi"}
PREPROCESS_CONFIG_ENV = "ASCENDANY_PREPROCESS_CONFIG"


def _resolve_preprocess_config_path() -> Path:
    env_path = os.getenv(PREPROCESS_CONFIG_ENV)
    if env_path:
        candidate = Path(env_path).expanduser()
        if not candidate.is_absolute():
            candidate = (_PROJECT_ROOT / candidate).resolve()
        return candidate
    return _PROJECT_ROOT / "preprocess/config/default.yaml"


def _load_preprocess_settings() -> Any:
    from preprocess.config import load_settings as pp_load_settings
    return pp_load_settings(config_path=_resolve_preprocess_config_path())


def _create_preprocess_connection(pp_settings: Any) -> Any:
    from preprocess.db import connect as pp_connect
    return pp_connect(pp_settings)


def _get_practice_root() -> Path:
    env_root = os.getenv("PRACTICE_DATA_ROOT")
    if env_root:
        root = Path(env_root).expanduser()
        if not root.is_absolute():
            root = (_PROJECT_ROOT / root).resolve()
        return root
    pp_settings = _load_preprocess_settings()
    root = Path(pp_settings.practice_root).expanduser()
    if not root.is_absolute():
        root = (_PROJECT_ROOT / root).resolve()
    return root


def _ensure_practice_root_writable(practice_root: Path) -> None:
    try:
        practice_root.mkdir(parents=True, exist_ok=True)
    except PermissionError as exc:
        raise HTTPException(
            status_code=500,
            detail=f"practice 目录不可写：{practice_root}",
        ) from exc
    except OSError as exc:
        raise HTTPException(
            status_code=500,
            detail=f"无法创建 practice 目录：{practice_root} ({exc})",
        ) from exc


def _normalize_single_source_path(exam_type: str, source_path: str) -> str:
    normalized = source_path.strip().replace("\\", "/").strip("/")
    if not normalized:
        raise HTTPException(status_code=400, detail="sourcePath 不能为空")
    if normalized.startswith(f"{exam_type}/"):
        return normalized
    top = normalized.split("/", 1)[0]
    if top in VALID_EXAM_TYPES and top != exam_type:
        raise HTTPException(
            status_code=400,
            detail=f"sourcePath 与 examType 不匹配：{source_path}",
        )
    return f"{exam_type}/{normalized}"


# ── POST /import/upload ───────────────────────────────────


@router.post("/upload", response_model=UploadResponse)
async def upload_exam_zip(
    file: UploadFile = File(...),
    examType: str = Form(...),
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> UploadResponse:
    """Upload a .zip containing exam data.

    Extracts to ``practice_root/{examType}/{zip_stem}/``.
    """
    if examType not in VALID_EXAM_TYPES:
        raise HTTPException(
            status_code=400,
            detail=f"无效的考试类型 '{examType}'，可选: {', '.join(sorted(VALID_EXAM_TYPES))}",
        )

    if not file.filename or not file.filename.lower().endswith(".zip"):
        raise HTTPException(status_code=400, detail="只支持 .zip 文件")

    exam_name = Path(file.filename).stem
    practice_root = await asyncio.to_thread(_get_practice_root)
    await asyncio.to_thread(_ensure_practice_root_writable, practice_root)
    target_dir = practice_root / examType / exam_name
    if target_dir.exists():
        raise HTTPException(
            status_code=409,
            detail="该zip名字已存在，请换一个",
        )

    tmp_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(suffix=".zip", delete=False) as tmp:
            tmp_path = Path(tmp.name)
            while True:
                chunk = await file.read(1024 * 1024)
                if not chunk:
                    break
                tmp.write(chunk)

        if not zipfile.is_zipfile(tmp_path):
            raise HTTPException(status_code=400, detail="上传的文件不是有效的 ZIP 文件")

        def _extract() -> tuple[str, int]:
            try:
                target_dir.mkdir(parents=True, exist_ok=False)
            except FileExistsError as exc:
                raise HTTPException(
                    status_code=409,
                    detail="该zip名字已存在，请换一个",
                ) from exc
            except PermissionError as exc:
                raise HTTPException(
                    status_code=500,
                    detail=f"上传目录不可写：{target_dir}",
                ) from exc

            with zipfile.ZipFile(tmp_path, "r") as zf:
                for member in zf.namelist():
                    member_path = (target_dir / member).resolve()
                    if not str(member_path).startswith(str(target_dir.resolve())):
                        raise HTTPException(
                            status_code=400,
                            detail=f"ZIP 中包含不安全的路径: {member}",
                        )
                zf.extractall(target_dir)
                file_count = len([n for n in zf.namelist() if not n.endswith("/")])

            return str(target_dir.relative_to(practice_root)), file_count

        source_path, file_count = await asyncio.to_thread(_extract)

    finally:
        if tmp_path:
            try:
                tmp_path.unlink(missing_ok=True)
            except Exception:
                pass

    return UploadResponse(
        examType=examType,
        examName=exam_name,
        sourcePath=source_path,
        fileCount=file_count,
        message=f"已上传并解压到 {source_path}，共 {file_count} 个文件",
    )


# ── POST /import/run ──────────────────────────────────────


@router.post("/run", response_model=ImportRunResponse)
async def start_import_run(
    body: ImportRunRequest,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> ImportRunResponse:
    tm = get_task_manager()
    run_id = tm.create_task("import")

    def _worker() -> None:
        try:
            tm.mark_running(run_id)
            tm.emit_log(run_id, "info", "正在启动增量导入 ...")

            pp_settings = _load_preprocess_settings()
            done_payload: dict[str, Any] | None = None
            with _create_preprocess_connection(pp_settings) as conn:
                from preprocess.load import IngestService, Repository

                repo = Repository(conn)
                service = IngestService(repo=repo, settings=pp_settings)

                def _on_progress(event_type: str, data: dict[str, Any]) -> None:
                    if event_type == "log":
                        tm.emit_log(
                            run_id,
                            data.get("level", "info"),
                            data.get("message", ""),
                        )
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

                summary = service.run(
                    exam_types=body.examTypes,
                    limit=body.limit,
                    dry_run=body.dryRun,
                    force=body.force,
                    on_progress=_on_progress,
                )
                done_payload = {
                    "ingestRunId": summary.ingest_run_id,
                    "scanned": summary.scanned,
                    "skipped": summary.skipped,
                    "succeeded": summary.succeeded,
                    "failed": summary.failed,
                    "submissionsBound": summary.submissions_bound,
                    "submissionsPendingClaim": summary.submissions_pending_claim,
                    "nicknameConflicts": summary.nickname_conflicts,
                    "achievementsRecomputedStudents": summary.achievements_recomputed_students,
                    "errors": summary.errors,
                }

            tm.emit_done(run_id, done_payload or {})
        except Exception as exc:
            logger.exception("Import task %s failed", run_id)
            tm.emit_error(run_id, str(exc))

    thread = threading.Thread(target=_worker, daemon=True, name=f"import-{run_id}")
    thread.start()

    return ImportRunResponse(
        runId=run_id,
        message="导入任务已启动",
    )


@router.post("/run-single", response_model=ImportRunResponse)
async def start_single_import_run(
    body: SingleImportRunRequest,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> ImportRunResponse:
    if body.examType not in VALID_EXAM_TYPES:
        raise HTTPException(
            status_code=400,
            detail=f"无效的考试类型 '{body.examType}'，可选: {', '.join(sorted(VALID_EXAM_TYPES))}",
        )
    normalized_source_path = _normalize_single_source_path(
        exam_type=body.examType,
        source_path=body.sourcePath,
    )

    tm = get_task_manager()
    run_id = tm.create_task("import")

    def _worker() -> None:
        try:
            tm.mark_running(run_id)
            tm.emit_log(
                run_id,
                "info",
                f"正在重跑单场考试: {normalized_source_path} ...",
            )

            pp_settings = _load_preprocess_settings()
            done_payload: dict[str, Any] | None = None
            with _create_preprocess_connection(pp_settings) as conn:
                from preprocess.load import IngestService, Repository

                repo = Repository(conn)
                service = IngestService(repo=repo, settings=pp_settings)

                def _on_progress(event_type: str, data: dict[str, Any]) -> None:
                    if event_type == "log":
                        tm.emit_log(
                            run_id,
                            data.get("level", "info"),
                            data.get("message", ""),
                        )
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

                summary = service.run(
                    exam_types=[body.examType],
                    source_paths=[normalized_source_path],
                    limit=1,
                    dry_run=body.dryRun,
                    force=body.force,
                    on_progress=_on_progress,
                )
                done_payload = {
                    "ingestRunId": summary.ingest_run_id,
                    "scanned": summary.scanned,
                    "skipped": summary.skipped,
                    "succeeded": summary.succeeded,
                    "failed": summary.failed,
                    "submissionsBound": summary.submissions_bound,
                    "submissionsPendingClaim": summary.submissions_pending_claim,
                    "nicknameConflicts": summary.nickname_conflicts,
                    "achievementsRecomputedStudents": summary.achievements_recomputed_students,
                    "errors": summary.errors,
                }

                if summary.scanned == 0:
                    tm.emit_error(
                        run_id,
                        f"未找到指定考试目录: {normalized_source_path}",
                    )
                    return

            tm.emit_done(run_id, done_payload or {})
        except Exception as exc:
            logger.exception("Single import task %s failed", run_id)
            tm.emit_error(run_id, str(exc))

    thread = threading.Thread(target=_worker, daemon=True, name=f"import-single-{run_id}")
    thread.start()

    return ImportRunResponse(
        runId=run_id,
        message="单场重跑任务已启动",
    )


# ── GET /import/run/{run_id}/stream ───────────────────────


@router.get("/run/{run_id}/stream")
async def stream_import_run(
    run_id: str,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
) -> StreamingResponse:
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
    tm = get_task_manager()
    return tm.list_tasks()
