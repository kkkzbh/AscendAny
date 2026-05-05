from __future__ import annotations

import asyncio
import json
import shutil
import subprocess
import tempfile
from pathlib import Path
from typing import Any

from fastapi import APIRouter, Depends, Query, Request, WebSocket, WebSocketDisconnect

from ..deps import (
    get_auth_service,
    get_admin_account,
    get_current_account,
    get_current_account_optional,
    get_llm_service,
    get_repository,
    get_settings,
)
from ...core.config import Settings
from ...core.errors import AppError
from ...services.auth import AuthService, AuthenticatedAccount
from ...services.oj import OjProblemService
from ...services.web_auth_adapter import WebAuthAdapter
from ...services.web_email import LegacyEmailService
from ...services.web_learning import WebLearningService
from ...services.web_qa import WebQaService

router = APIRouter(tags=["web"])
ws_router = APIRouter(tags=["websocket"])


async def _payload(request: Request) -> dict[str, Any]:
    ctype = (request.headers.get("content-type") or "").lower()
    if "application/json" in ctype:
        try:
            data = await request.json()
            return data if isinstance(data, dict) else {}
        except Exception:
            return {}
    form = await request.form()
    return dict(form)


def _web_auth(
    settings: Settings,
    repository: Any,
    auth_service: AuthService,
) -> WebAuthAdapter:
    email_service = LegacyEmailService(settings=settings, repository=repository)
    return WebAuthAdapter(
        settings=settings,
        repository=repository,
        auth_service=auth_service,
        email_service=email_service,
    )


@router.get("/auth/me/")
async def web_auth_me(
    current: AuthenticatedAccount = Depends(get_current_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).me(current)


@router.post("/auth/token/login/")
async def web_auth_login(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).login(await _payload(request))


@router.post("/auth/token/refresh/")
async def web_auth_refresh(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).refresh(await _payload(request))


@router.post("/auth/token/logout/")
async def web_auth_logout(
    request: Request,
    current: AuthenticatedAccount | None = Depends(get_current_account_optional),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).logout(
        current, await _payload(request)
    )


@router.post("/auth/register/")
async def web_auth_register(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).register(await _payload(request))


@router.post("/auth/forgot-password/")
async def web_auth_forgot_password(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).forgot_password(
        await _payload(request)
    )


@router.post("/send-verification-code/")
async def web_send_verification_code(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    data = await _payload(request)
    email = (data.get("email") or "").strip()
    code_type = (data.get("type") or "registration").strip().lower()
    if not email:
        return {"success": False, "message": "请输入邮箱地址"}

    adapter = _web_auth(settings, repository, auth_service)
    email_service = LegacyEmailService(settings=settings, repository=repository)
    if code_type == "registration":
        if await adapter.email_exists(email):
            return {"success": False, "message": "该邮箱已被注册"}
        result = await email_service.send_registration_verification(email, "用户")
        if result.success:
            return {"success": True, "message": "验证码已发送到您的邮箱，请稍等片刻查收"}
        return {"success": False, "message": f"验证码发送失败: {result.message}"}

    if code_type == "reset":
        account = await adapter._fetch_account_by_email(email)
        if account is None:
            return {"success": True, "message": "如果该邮箱已注册，您将收到重置验证码"}
        result = await email_service.send_password_reset_email(
            email, str(account.get("username") or "用户")
        )
        if result.success:
            return {"success": True, "message": "密码重置验证码已发送到您的邮箱"}
        return {"success": False, "message": f"验证码发送失败: {result.message}"}

    return {"success": False, "message": "无效的验证码类型"}


@router.post("/verify-reset-code/")
async def web_verify_reset_code(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    data = await _payload(request)
    email = (data.get("email") or "").strip()
    code = (data.get("code") or "").strip()
    if not email or not code:
        return {"success": False, "message": "请填写邮箱和验证码"}
    ok, message = await LegacyEmailService(settings, repository).verify_reset_code(email, code)
    return {"success": bool(ok), "message": message}


@router.post("/profile/update/")
async def web_profile_update(
    request: Request,
    current: AuthenticatedAccount = Depends(get_current_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).update_profile(
        current, await _payload(request)
    )


@router.post("/profile/change-password/")
async def web_profile_change_password(
    request: Request,
    current: AuthenticatedAccount = Depends(get_current_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
    auth_service: AuthService = Depends(get_auth_service),
) -> dict[str, Any]:
    return await _web_auth(settings, repository, auth_service).change_password(
        current, await _payload(request)
    )


@router.post("/metrics/student")
async def web_metrics_student(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await WebLearningService(repository, settings).metrics_student(await _payload(request))


@router.post("/mastery/knowledge-points")
async def web_mastery(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await WebLearningService(repository, settings).mastery(await _payload(request))


@router.post("/path/plan")
async def web_path_plan(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await WebLearningService(repository, settings).path_plan(await _payload(request))


@router.post("/knowledge/tag-detail")
async def web_tag_detail(
    request: Request,
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await WebLearningService(repository, settings).tag_detail(await _payload(request))


@router.post("/qa/search")
async def web_qa_search(
    request: Request,
    repository=Depends(get_repository),
    llm_service=Depends(get_llm_service),
) -> dict[str, Any]:
    return await WebQaService(repository, llm_service).search(await _payload(request))


@router.get("/records/")
async def web_records(
    q: str = "",
    status: str = "",
    page: int = Query(default=1, ge=1),
    page_size: int = Query(default=50, ge=1, le=200),
    current: AuthenticatedAccount | None = Depends(get_current_account_optional),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).records(
        account=current, q=q, status=status, page=page, page_size=page_size
    )


@router.get("/oj/problems/")
async def web_oj_problems(
    q: str = "",
    tag: str = "",
    page: int = Query(default=1, ge=1),
    page_size: int = Query(default=50, ge=1, le=200),
    include_recommended: bool = False,
    limit: int | None = None,
    current: AuthenticatedAccount | None = Depends(get_current_account_optional),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).list_problems(
        account=current,
        q=q,
        tag=tag,
        page=page,
        page_size=page_size,
        include_recommended=include_recommended,
        limit=limit,
    )


@router.get("/oj/problems/{problem_id}/")
async def web_oj_problem_detail(
    problem_id: str,
    current: AuthenticatedAccount = Depends(get_current_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).problem_detail(
        account=current, problem_id=problem_id
    )


@router.post("/oj/run/")
async def web_oj_run(
    request: Request,
    current: AuthenticatedAccount = Depends(get_current_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).run_code(
        account=current, payload=await _payload(request)
    )


@router.post("/oj/submit/")
async def web_oj_submit(
    request: Request,
    current: AuthenticatedAccount = Depends(get_current_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).submit_code(
        account=current, payload=await _payload(request)
    )


@router.post("/admin/oj/problems/list/")
async def web_admin_oj_problems_list(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_problems_list(
        await _payload(request)
    )


@router.post("/admin/oj/problems/get/")
async def web_admin_oj_problems_get(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_problems_get(
        await _payload(request)
    )


@router.post("/admin/oj/problems/create/")
async def web_admin_oj_problems_create(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_problems_create(
        await _payload(request)
    )


@router.post("/admin/oj/problems/update/")
async def web_admin_oj_problems_update(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_problems_update(
        await _payload(request)
    )


@router.post("/admin/oj/problems/delete/")
async def web_admin_oj_problems_delete(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_problems_delete(
        await _payload(request)
    )


@router.post("/admin/oj/testcases/list/")
async def web_admin_oj_testcases_list(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_testcases_list(
        await _payload(request)
    )


@router.post("/admin/oj/testcases/get/")
async def web_admin_oj_testcases_get(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_testcases_get(
        await _payload(request)
    )


@router.post("/admin/oj/testcases/create/")
async def web_admin_oj_testcases_create(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_testcases_create(
        await _payload(request)
    )


@router.post("/admin/oj/testcases/update/")
async def web_admin_oj_testcases_update(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_testcases_update(
        await _payload(request)
    )


@router.post("/admin/oj/testcases/delete/")
async def web_admin_oj_testcases_delete(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_testcases_delete(
        await _payload(request)
    )


@router.post("/admin/oj/submissions/list/")
async def web_admin_oj_submissions_list(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_submissions_list(
        await _payload(request)
    )


@router.post("/admin/oj/submissions/get/")
async def web_admin_oj_submissions_get(
    request: Request,
    _admin: AuthenticatedAccount = Depends(get_admin_account),
    repository=Depends(get_repository),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    return await OjProblemService(repository, settings).admin_submissions_get(
        await _payload(request)
    )


async def _read_lsp_message(stream: asyncio.StreamReader) -> str | None:
    headers: dict[str, str] = {}
    while True:
        line = await stream.readline()
        if not line:
            return None
        if line in {b"\r\n", b"\n"}:
            break
        raw = line.decode("ascii", errors="ignore").strip()
        if ":" in raw:
            key, value = raw.split(":", 1)
            headers[key.lower()] = value.strip()
    length = int(headers.get("content-length") or 0)
    if length <= 0:
        return None
    data = await stream.readexactly(length)
    return data.decode("utf-8", errors="replace")


def _lsp_frame(message: str) -> bytes:
    data = message.encode("utf-8")
    return f"Content-Length: {len(data)}\r\n\r\n".encode("ascii") + data


@ws_router.websocket("/ws/oj/lsp/cpp/")
async def web_oj_lsp_cpp(
    websocket: WebSocket,
) -> None:
    await websocket.accept()
    app = websocket.app
    settings: Settings = app.state.settings
    auth_service: AuthService = app.state.auth_service
    token = websocket.query_params.get("access_token") or ""
    try:
        auth_service.authenticate_access_token(token)
    except AppError:
        await websocket.close(code=4401)
        return

    clangd = shutil.which(settings.oj.clangd_path) or shutil.which("clangd")
    if not clangd:
        await websocket.send_text(json.dumps({"error": "clangd is not configured"}))
        await websocket.close(code=1011)
        return

    with tempfile.TemporaryDirectory() as raw_dir:
        work_dir = Path(raw_dir)
        (work_dir / "compile_flags.txt").write_text(
            "-std=gnu++23\n-I/usr/include\n", encoding="utf-8"
        )
        proc = await asyncio.create_subprocess_exec(
            clangd,
            "--compile-commands-dir=" + str(work_dir),
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL,
            cwd=str(work_dir),
        )

        async def ws_to_proc() -> None:
            try:
                while True:
                    msg = await websocket.receive_text()
                    if proc.stdin is None:
                        return
                    proc.stdin.write(_lsp_frame(msg))
                    await proc.stdin.drain()
            except WebSocketDisconnect:
                return

        async def proc_to_ws() -> None:
            if proc.stdout is None:
                return
            while True:
                msg = await _read_lsp_message(proc.stdout)
                if msg is None:
                    return
                await websocket.send_text(msg)

        tasks = [
            asyncio.create_task(ws_to_proc()),
            asyncio.create_task(proc_to_ws()),
        ]
        done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        for task in pending:
            task.cancel()
        for task in done:
            try:
                task.result()
            except Exception:
                pass
        try:
            proc.terminate()
            await asyncio.wait_for(proc.wait(), timeout=3)
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass
