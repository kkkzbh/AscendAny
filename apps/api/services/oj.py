from __future__ import annotations

import asyncio
import hashlib
import html
import os
import re
import shutil
import subprocess
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from psycopg.rows import dict_row

from ..core.config import PROJECT_ROOT, Settings
from .auth import AuthenticatedAccount


def _normalize_output(output: str) -> str:
    lines = str(output or "").replace("\r\n", "\n").replace("\r", "\n").split("\n")
    normalized = [line.rstrip() for line in lines]
    while normalized and normalized[-1] == "":
        normalized.pop()
    return "\n".join(normalized)


def _status_label(code: str) -> str:
    return {
        "AC": "Accepted",
        "WA": "Wrong Answer",
        "CE": "Compile Error",
        "RE": "Runtime Error",
        "TLE": "Time Limit Exceeded",
        "MLE": "Memory Limit Exceeded",
        "SE": "System Error",
        "OK": "OK",
    }.get(code, code or "Unknown")


def _render_markdown_like(text: str) -> str:
    raw = text or ""
    try:
        import markdown  # type: ignore

        return markdown.markdown(raw, extensions=["fenced_code", "tables", "nl2br"])
    except Exception:
        pass

    parts: list[str] = []
    in_code = False
    code_lines: list[str] = []
    para: list[str] = []
    for line in raw.replace("\r\n", "\n").replace("\r", "\n").split("\n"):
        if line.strip().startswith("```"):
            if in_code:
                parts.append("<pre><code>" + html.escape("\n".join(code_lines)) + "</code></pre>")
                code_lines = []
                in_code = False
            else:
                if para:
                    parts.append("<p>" + "<br>".join(html.escape(x) for x in para) + "</p>")
                    para = []
                in_code = True
            continue
        if in_code:
            code_lines.append(line)
            continue
        if not line.strip():
            if para:
                parts.append("<p>" + "<br>".join(html.escape(x) for x in para) + "</p>")
                para = []
            continue
        para.append(line)
    if code_lines:
        parts.append("<pre><code>" + html.escape("\n".join(code_lines)) + "</code></pre>")
    if para:
        parts.append("<p>" + "<br>".join(html.escape(x) for x in para) + "</p>")
    return "\n".join(parts)


def _request_id(payload: dict[str, Any]) -> str:
    return str(payload.get("requestId") or "").strip()


def _admin_resp(
    request_id: str,
    code: int,
    data: Any,
    message: str,
    authentication: str = "",
) -> dict[str, Any]:
    return {
        "success": 200 <= int(code) < 300,
        "requestId": request_id or "",
        "code": int(code),
        "data": data,
        "message": message or "",
        "authentication": authentication or "",
    }


def _parse_int(
    value: Any,
    *,
    default: int,
    min_value: int | None = None,
    max_value: int | None = None,
) -> int:
    try:
        out = int(value)
    except (TypeError, ValueError):
        out = int(default)
    if min_value is not None:
        out = max(int(min_value), out)
    if max_value is not None:
        out = min(int(max_value), out)
    return out


def _parse_float(
    value: Any,
    *,
    default: float,
    min_value: float | None = None,
    max_value: float | None = None,
) -> float:
    try:
        out = float(value)
    except (TypeError, ValueError):
        out = float(default)
    if min_value is not None:
        out = max(float(min_value), out)
    if max_value is not None:
        out = min(float(max_value), out)
    return out


def _coerce_bool(value: Any, *, default: bool = False) -> bool:
    if value is None:
        return bool(default)
    if isinstance(value, bool):
        return bool(value)
    raw = str(value).strip().lower()
    if raw in {"1", "true", "yes", "on"}:
        return True
    if raw in {"0", "false", "no", "off"}:
        return False
    return bool(default)


def _preview(text: str, *, limit: int = 120) -> str:
    raw = (text or "").replace("\r\n", "\n").replace("\r", "\n").strip()
    if len(raw) <= limit:
        return raw
    return raw[:limit].rstrip() + "..."


def _iso(value: Any) -> str:
    return value.isoformat() if hasattr(value, "isoformat") else ""


def _blank_none(value: Any) -> str | None:
    raw = str(value or "").strip()
    return raw or None


def _parse_tags(payload: dict[str, Any]) -> list[str]:
    raw = payload.get("tags")
    if isinstance(raw, list):
        values = [str(item).strip() for item in raw]
    else:
        category_tags = str(payload.get("categoryTags") or "").strip()
        values = re.split(r"[\s,，;；]+", category_tags) if category_tags else []
    out: list[str] = []
    seen: set[str] = set()
    for value in values:
        if value and value not in seen:
            out.append(value)
            seen.add(value)
    return out


def _row_tags(row: dict[str, Any]) -> list[str]:
    tags = row.get("tags")
    if not isinstance(tags, list):
        return []
    return [str(item) for item in tags if str(item).strip()]


def _problem_source_hash(problem_id: str, description: str) -> str:
    raw = f"{problem_id}\0{description}"
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


@dataclass(slots=True)
class JudgeResult:
    status: str
    message: str
    stdout: str = ""
    stderr: str = ""
    runtime_ms: int = 0
    memory_kb: int = 0
    score: float = 0.0
    score_rate: float = 0.0
    truncated: bool = False
    is_correct: bool = False


class JudgeService:
    def __init__(self, settings: Settings) -> None:
        self._settings = settings

    async def run_once(
        self,
        *,
        code: str,
        input_data: str,
        language: str,
        time_limit_ms: int,
        memory_limit_kb: int,
    ) -> JudgeResult:
        return await asyncio.to_thread(
            self._run_once_sync,
            code,
            input_data,
            language,
            time_limit_ms,
            memory_limit_kb,
        )

    async def judge(
        self, *, code: str, language: str, testcases: list[dict[str, Any]]
    ) -> JudgeResult:
        return await asyncio.to_thread(self._judge_sync, code, language, testcases)

    def _judge_sync(
        self, code: str, language: str, testcases: list[dict[str, Any]]
    ) -> JudgeResult:
        total_weight = sum(float(tc.get("weight", 1.0) or 1.0) for tc in testcases)
        if total_weight <= 0:
            total_weight = float(len(testcases) or 1)
        passed_weight = 0.0
        max_runtime = 0
        max_memory = 0
        if not testcases:
            compile_result = self._compile_or_prepare(code, language)
            if compile_result.status != "OK":
                return compile_result
            return JudgeResult(
                status="AC",
                message="编译通过（未配置测试点，未判题）",
                score=100.0,
                score_rate=1.0,
                is_correct=True,
            )

        for idx, testcase in enumerate(testcases, start=1):
            result = self._run_once_sync(
                code,
                str(testcase.get("input_data") or ""),
                language,
                int(testcase.get("time_limit_ms") or 1000),
                int(testcase.get("memory_limit_kb") or 262144),
            )
            max_runtime = max(max_runtime, result.runtime_ms)
            max_memory = max(max_memory, result.memory_kb)
            if result.status in {"CE", "TLE", "RE", "MLE", "SE"}:
                result.score = round((passed_weight / total_weight) * 100.0, 2)
                result.score_rate = round(passed_weight / total_weight, 4)
                result.runtime_ms = max_runtime
                result.memory_kb = max_memory
                return result
            actual = _normalize_output(result.stdout)
            expected = _normalize_output(str(testcase.get("output_data") or ""))
            if actual != expected:
                return JudgeResult(
                    status="WA",
                    message=f"第 {idx} 个测试点输出不匹配",
                    stdout=result.stdout,
                    stderr=result.stderr,
                    runtime_ms=max_runtime,
                    memory_kb=max_memory,
                    score=round((passed_weight / total_weight) * 100.0, 2),
                    score_rate=round(passed_weight / total_weight, 4),
                )
            passed_weight += float(testcase.get("weight", 1.0) or 1.0)

        return JudgeResult(
            status="AC",
            message="全部测试点通过",
            runtime_ms=max_runtime,
            memory_kb=max_memory,
            score=100.0,
            score_rate=1.0,
            is_correct=True,
        )

    def _run_once_sync(
        self,
        code: str,
        input_data: str,
        language: str,
        time_limit_ms: int,
        memory_limit_kb: int,
    ) -> JudgeResult:
        runtime = (self._settings.oj.runtime or "podman").strip().lower()
        if runtime in {"podman", "docker"} and shutil.which(runtime):
            result = self._run_once_oci(
                runtime, code, input_data, language, time_limit_ms, memory_limit_kb
            )
            if result.status != "SE":
                return result
        return self._run_once_local(
            code, input_data, language, time_limit_ms, memory_limit_kb
        )

    def _compile_or_prepare(self, code: str, language: str) -> JudgeResult:
        with tempfile.TemporaryDirectory(dir=self._work_dir()) as raw_dir:
            work_dir = Path(raw_dir)
            source, command = self._write_source(work_dir, code, language)
            if command:
                proc = subprocess.run(
                    command,
                    cwd=work_dir,
                    capture_output=True,
                    text=True,
                    timeout=self._settings.oj.compile_timeout_seconds,
                )
                if proc.returncode != 0:
                    return JudgeResult(
                        status="CE",
                        message=f"Compilation Error:\n{proc.stderr}",
                        stderr=proc.stderr,
                    )
            return JudgeResult(status="OK", message="OK", stdout=str(source))

    def _run_once_local(
        self,
        code: str,
        input_data: str,
        language: str,
        time_limit_ms: int,
        memory_limit_kb: int,
    ) -> JudgeResult:
        try:
            with tempfile.TemporaryDirectory(dir=self._work_dir()) as raw_dir:
                work_dir = Path(raw_dir)
                source, compile_cmd = self._write_source(work_dir, code, language)
                if compile_cmd:
                    compile_proc = subprocess.run(
                        compile_cmd,
                        cwd=work_dir,
                        capture_output=True,
                        text=True,
                        timeout=self._settings.oj.compile_timeout_seconds,
                    )
                    if compile_proc.returncode != 0:
                        return JudgeResult(
                            status="CE",
                            message=f"Compilation Error:\n{compile_proc.stderr}",
                            stderr=compile_proc.stderr,
                        )
                    run_cmd = [str(work_dir / "main")]
                else:
                    run_cmd = ["python3", str(source)]

                start = time.perf_counter()
                proc = subprocess.run(
                    run_cmd,
                    input=input_data,
                    cwd=work_dir,
                    capture_output=True,
                    text=True,
                    timeout=(max(1, time_limit_ms) / 1000.0)
                    + self._settings.oj.time_buffer_seconds,
                )
                elapsed = max(0, int((time.perf_counter() - start) * 1000))
                stdout = self._limit_output(proc.stdout or "")
                stderr = self._limit_output(proc.stderr or "")
                if proc.returncode != 0:
                    return JudgeResult(
                        status="RE",
                        message=stderr or f"Runtime Error: exit code {proc.returncode}",
                        stdout=stdout,
                        stderr=stderr,
                        runtime_ms=elapsed,
                    )
                return JudgeResult(
                    status="OK",
                    message="运行成功",
                    stdout=stdout,
                    stderr=stderr,
                    runtime_ms=elapsed,
                    truncated=len(proc.stdout or "") > self._settings.oj.output_limit_bytes,
                )
        except subprocess.TimeoutExpired:
            return JudgeResult(status="TLE", message=f"运行超时 (>{time_limit_ms} ms)")
        except Exception as exc:
            return JudgeResult(status="SE", message=f"System Error: {exc}")

    def _run_once_oci(
        self,
        runtime: str,
        code: str,
        input_data: str,
        language: str,
        time_limit_ms: int,
        memory_limit_kb: int,
    ) -> JudgeResult:
        try:
            with tempfile.TemporaryDirectory(dir=self._work_dir()) as raw_dir:
                work_dir = Path(raw_dir)
                source, compile_cmd = self._write_source(work_dir, code, language)
                script = self._oci_script(source.name, bool(compile_cmd), time_limit_ms, memory_limit_kb)
                (work_dir / "runner.sh").write_text(script, encoding="utf-8")
                (work_dir / "stdin.txt").write_text(input_data or "", encoding="utf-8")
                cmd = [
                    runtime,
                    "run",
                    "--rm",
                    "-i",
                    "--network",
                    "none",
                    "--pids-limit",
                    "128",
                    "--memory",
                    f"{max(self._settings.oj.memory_mb, 64)}m",
                    "--memory-swap",
                    f"{max(self._settings.oj.memory_mb, 64)}m",
                    "--cpus",
                    str(self._settings.oj.cpus),
                    "-v",
                    f"{work_dir.resolve()}:/work:Z",
                    "-w",
                    "/work",
                    self._settings.oj.image,
                    "bash",
                    "runner.sh",
                ]
                start = time.perf_counter()
                proc = subprocess.run(
                    cmd,
                    capture_output=True,
                    text=True,
                    timeout=(time_limit_ms / 1000.0)
                    + self._settings.oj.compile_timeout_seconds
                    + 5,
                )
                elapsed = max(0, int((time.perf_counter() - start) * 1000))
                status = self._read_file(work_dir / "status.txt") or "SE"
                stdout = self._limit_output(self._read_file(work_dir / "stdout.txt"))
                stderr = self._limit_output(self._read_file(work_dir / "stderr.txt"))
                message = self._read_file(work_dir / "message.txt") or stderr or _status_label(status)
                if proc.returncode != 0 and status == "SE":
                    message = proc.stderr or message
                return JudgeResult(
                    status=status,
                    message=message,
                    stdout=stdout,
                    stderr=stderr,
                    runtime_ms=elapsed,
                    truncated=len(stdout) > self._settings.oj.output_limit_bytes,
                )
        except subprocess.TimeoutExpired:
            return JudgeResult(status="TLE", message=f"运行超时 (>{time_limit_ms} ms)")
        except Exception as exc:
            return JudgeResult(status="SE", message=f"System Error: {exc}")

    def _write_source(
        self, work_dir: Path, code: str, language: str
    ) -> tuple[Path, list[str] | None]:
        lang = (language or "C++").strip().lower()
        if lang in {"python", "python3", "py"}:
            source = work_dir / "main.py"
            source.write_text(code, encoding="utf-8")
            return source, None
        source = work_dir / "main.cpp"
        source.write_text(code, encoding="utf-8")
        return source, [
            "g++",
            str(source),
            "-O2",
            "-std=gnu++23",
            "-pipe",
            "-o",
            str(work_dir / "main"),
        ]

    def _oci_script(
        self, source_name: str, needs_compile: bool, time_limit_ms: int, memory_limit_kb: int
    ) -> str:
        timeout_sec = (max(1, time_limit_ms) / 1000.0) + self._settings.oj.time_buffer_seconds
        mem_line = (
            f"ulimit -v {int(memory_limit_kb)};"
            if int(memory_limit_kb or 0) > 0
            else ""
        )
        if needs_compile:
            run_line = "./main"
            compile_block = f"""
timeout -k 1s {self._settings.oj.compile_timeout_seconds:.3f}s g++ {source_name} -O2 -std=gnu++23 -pipe -o main 2>stderr.txt
rc=$?
if [ "$rc" -ne 0 ]; then
  echo CE > status.txt
  cp stderr.txt message.txt
  exit 0
fi
"""
        else:
            run_line = f"python3 {source_name}"
            compile_block = ""
        return f"""#!/usr/bin/env bash
set +e
{compile_block}
timeout -k 1s {timeout_sec:.3f}s bash -lc '{mem_line} {run_line}' <stdin.txt >stdout.txt 2>stderr.txt
rc=$?
if [ "$rc" -eq 124 ] || [ "$rc" -eq 137 ]; then
  echo TLE > status.txt
  echo "运行超时 (>{int(time_limit_ms)} ms)" > message.txt
elif [ "$rc" -ne 0 ]; then
  echo RE > status.txt
  cp stderr.txt message.txt
else
  echo OK > status.txt
  echo "运行成功" > message.txt
fi
"""

    def _work_dir(self) -> str:
        raw = Path(self._settings.oj.work_dir)
        path = raw if raw.is_absolute() else PROJECT_ROOT / raw
        path.mkdir(parents=True, exist_ok=True)
        return str(path)

    def _limit_output(self, text: str) -> str:
        data = (text or "").encode("utf-8", errors="replace")
        limit = self._settings.oj.output_limit_bytes
        if len(data) <= limit:
            return text or ""
        return data[:limit].decode("utf-8", errors="replace")

    @staticmethod
    def _read_file(path: Path) -> str:
        try:
            return path.read_text(encoding="utf-8", errors="replace")
        except Exception:
            return ""


class OjProblemService:
    def __init__(self, repository: Any, settings: Settings) -> None:
        self._repository = repository
        self._settings = settings
        self._judge = JudgeService(settings)

    async def list_problems(
        self,
        *,
        account: AuthenticatedAccount | None,
        q: str = "",
        tag: str = "",
        page: int = 1,
        page_size: int = 50,
        include_recommended: bool = False,
        limit: int | None = None,
    ) -> dict[str, Any]:
        page = max(1, int(page or 1))
        page_size = max(1, min(int(page_size or 50), 200))
        q = (q or "").strip()
        tag = (tag or "").strip()
        where = ["pb.active = TRUE"]
        params: list[Any] = []
        if q:
            where.append("(pb.problem_id ILIKE %s OR pb.title ILIKE %s OR pb.description ILIKE %s)")
            like = f"%{q}%"
            params.extend([like, like, like])
        if tag:
            where.append(
                "EXISTS (SELECT 1 FROM ascendany.recommendation_problem_tags t WHERE t.problem_id = pb.problem_id AND t.knowledge_point = %s)"
            )
            params.append(tag)
        where_sql = " AND ".join(where)
        total_row = await self._fetch_one(
            f"SELECT COUNT(*) AS total FROM ascendany.recommendation_problem_bank pb WHERE {where_sql}",
            tuple(params),
        )
        total = int(total_row.get("total") or 0) if total_row else 0
        fetch_limit = int(limit) if limit is not None and int(limit) > 0 else page_size
        offset = 0 if limit else (page - 1) * page_size
        rows = await self._fetch_all(
            f"""
            SELECT pb.problem_id, pb.title, pb.description, pb.link,
                   pb.submission_count, pb.pass_count,
                   COALESCE(jsonb_agg(t.knowledge_point ORDER BY t.knowledge_point)
                       FILTER (WHERE t.knowledge_point IS NOT NULL), '[]'::jsonb) AS tags
            FROM ascendany.recommendation_problem_bank pb
            LEFT JOIN ascendany.recommendation_problem_tags t ON t.problem_id = pb.problem_id
            WHERE {where_sql}
            GROUP BY pb.problem_id
            ORDER BY pb.problem_id
            LIMIT %s OFFSET %s
            """,
            tuple(params + [fetch_limit, offset]),
        )
        attempts = await self._fetch_attempts(account, [str(r["problem_id"]) for r in rows])
        items = [self._problem_item(row, attempts.get(str(row["problem_id"]), {})) for row in rows]
        tags = await self._available_tags()
        total_pages = max(1, (total + page_size - 1) // page_size)
        data: dict[str, Any] = {
            "items": items,
            "available_tags": tags,
            "pagination": {
                "page": page,
                "page_size": page_size,
                "total": total,
                "total_pages": total_pages,
                "has_prev": page > 1,
                "has_next": page < total_pages,
            },
        }
        if include_recommended:
            data["recommended"] = await self._recommended_for_account(account)
        return {"success": True, "data": data}

    async def problem_detail(
        self, *, account: AuthenticatedAccount, problem_id: str
    ) -> dict[str, Any]:
        row = await self._fetch_problem(problem_id)
        if not row:
            return {"success": False, "message": "题目不存在或已停用"}
        sample = await self._fetch_sample(problem_id)
        latest = await self._fetch_latest_submit(account.account_id, problem_id)
        tags = row.get("tags") if isinstance(row.get("tags"), list) else []
        return {
            "success": True,
            "data": {
                "problem_id": str(row["problem_id"]),
                "tags": [str(item) for item in tags],
                "link": row.get("link") or "",
                "pass_count": float(row.get("pass_count") or 0),
                "submission_count": float(row.get("submission_count") or 0),
                "description_html": _render_markdown_like(str(row.get("description") or "")),
                "solution_1_html": _render_markdown_like(str(row.get("solution_1") or "")),
                "solution_2_html": _render_markdown_like(str(row.get("solution_2") or "")),
                "sample_input": sample.get("input_data", "") if sample else "",
                "sample_output": sample.get("output_data", "") if sample else "",
                "latest_code": latest.get("code_content", "") if latest else "",
                "latest_language": latest.get("language", "") if latest else "",
                "latest_submit_time": latest.get("submit_time").isoformat()
                if latest and latest.get("submit_time") is not None
                else None,
                "run_url": "/api/oj/run/",
                "submit_url": "/api/oj/submit/",
            },
        }

    async def run_code(
        self, *, account: AuthenticatedAccount, payload: dict[str, Any]
    ) -> dict[str, Any]:
        problem_id = (payload.get("problem_id") or "").strip()
        code = str(payload.get("code") or "")
        stdin = payload.get("input")
        language = str(payload.get("language") or "C++")
        if not code.strip():
            return {"success": False, "message": "代码不能为空"}
        row = await self._fetch_problem(problem_id)
        if not row:
            return {"success": False, "message": "题目不存在或已停用"}
        sample = await self._fetch_sample(problem_id)
        used_sample_input = stdin is None
        input_data = str(stdin if stdin is not None else (sample.get("input_data", "") if sample else ""))
        result = await self._judge.run_once(
            code=code,
            input_data=input_data,
            language=language,
            time_limit_ms=int(sample.get("time_limit_ms") or 1000) if sample else 1000,
            memory_limit_kb=int(sample.get("memory_limit_kb") or 262144) if sample else 262144,
        )
        await self._insert_run_event(account, problem_id, language, result)
        return {
            "success": result.status == "OK",
            "status": result.status,
            "message": result.message,
            "stdout": result.stdout,
            "stderr": result.stderr,
            "runtime_ms": result.runtime_ms,
            "truncated": result.truncated,
            "sample_input": sample.get("input_data", "") if sample else "",
            "expected_output": sample.get("output_data", "") if sample else "",
            "used_sample_input": used_sample_input,
        }

    async def submit_code(
        self, *, account: AuthenticatedAccount, payload: dict[str, Any]
    ) -> dict[str, Any]:
        problem_id = (payload.get("problem_id") or "").strip()
        code = str(payload.get("code") or "")
        language = str(payload.get("language") or "C++")
        if not code.strip():
            return {"success": False, "message": "代码不能为空"}
        row = await self._fetch_problem(problem_id)
        if not row:
            return {"success": False, "message": "题目不存在或已停用"}
        testcases = await self._fetch_testcases(problem_id)
        result = await self._judge.judge(code=code, language=language, testcases=testcases)
        profile = await self._repository.fetch_account_profile(account.account_id)
        student_entity_id = None
        if profile and profile.student_id:
            student_entity_id = await self._fetch_student_entity_id(profile.student_id)
        await self._insert_submit_record(
            account=account,
            student_entity_id=student_entity_id,
            student_id=profile.student_id if profile else None,
            student_name=profile.pta_nickname if profile else account.username,
            problem_id=problem_id,
            code=code,
            language=language,
            result=result,
        )
        await self._execute(
            """
            UPDATE ascendany.recommendation_problem_bank
            SET submission_count = submission_count + 1,
                pass_count = pass_count + CASE WHEN %s THEN 1 ELSE 0 END,
                updated_at = now()
            WHERE problem_id = %s
            """,
            (result.status == "AC", problem_id),
        )
        return {"success": True, "result": result.status, "message": result.message}

    async def records(
        self,
        *,
        account: AuthenticatedAccount | None,
        q: str = "",
        status: str = "",
        page: int = 1,
        page_size: int = 50,
    ) -> dict[str, Any]:
        page = max(1, int(page or 1))
        page_size = max(1, min(int(page_size or 50), 200))
        where = []
        params: list[Any] = []
        if account is not None:
            where.append("r.account_id = %s")
            params.append(account.account_id)
        q = (q or "").strip()
        if q:
            where.append("(r.problem_id ILIKE %s OR r.student_name ILIKE %s)")
            like = f"%{q}%"
            params.extend([like, like])
        status = (status or "").strip().upper()
        if status:
            where.append("r.status = %s")
            params.append(status)
        where_sql = "WHERE " + " AND ".join(where) if where else ""
        total_row = await self._fetch_one(
            f"SELECT COUNT(*) AS total FROM ascendany.oj_submit_records r {where_sql}",
            tuple(params),
        )
        total = int(total_row.get("total") or 0) if total_row else 0
        rows = await self._fetch_all(
            f"""
            SELECT r.*, pb.title
            FROM ascendany.oj_submit_records r
            LEFT JOIN ascendany.recommendation_problem_bank pb ON pb.problem_id = r.problem_id
            {where_sql}
            ORDER BY r.submit_time DESC, r.submit_id DESC
            LIMIT %s OFFSET %s
            """,
            tuple(params + [page_size, (page - 1) * page_size]),
        )
        items = [
            {
                "exam": "OJ",
                "student_id": row.get("student_id") or "",
                "name": row.get("student_name") or "",
                "submit_time": row["submit_time"].isoformat()
                if row.get("submit_time") is not None
                else "",
                "status_code": row.get("status") or "",
                "status": _status_label(str(row.get("status") or "")),
                "score": float(row.get("score") or 0),
                "problem": row.get("problem_id") or "",
                "language": row.get("language") or "",
                "memory": f"{int(row.get('memory_kb') or 0)} KB",
                "time": int(row.get("runtime_ms") or 0),
            }
            for row in rows
        ]
        total_pages = max(1, (total + page_size - 1) // page_size)
        return {
            "success": True,
            "data": {
                "items": items,
                "pagination": {
                    "page": page,
                    "page_size": page_size,
                    "total": total,
                    "total_pages": total_pages,
                    "has_prev": page > 1,
                    "has_next": page < total_pages,
                },
            },
        }

    async def admin_problems_list(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        q = str(payload.get("q") or "").strip()
        tag = str(payload.get("tag") or "").strip()
        limit = _parse_int(payload.get("page"), default=50, min_value=1, max_value=500)
        offset = _parse_int(payload.get("offset"), default=0, min_value=0)

        where = []
        params: list[Any] = []
        if q:
            where.append(
                "(pb.problem_id ILIKE %s OR pb.title ILIKE %s OR pb.description ILIKE %s)"
            )
            like = f"%{q}%"
            params.extend([like, like, like])
        if tag:
            where.append(
                "EXISTS (SELECT 1 FROM ascendany.recommendation_problem_tags t WHERE t.problem_id = pb.problem_id AND t.knowledge_point ILIKE %s)"
            )
            params.append(f"%{tag}%")
        where_sql = "WHERE " + " AND ".join(where) if where else ""
        total_row = await self._fetch_one(
            f"SELECT COUNT(*) AS total FROM ascendany.recommendation_problem_bank pb {where_sql}",
            tuple(params),
        )
        rows = await self._fetch_all(
            f"""
            SELECT pb.problem_id, pb.title, pb.description, pb.link, pb.submission_count,
                   pb.pass_count, pb.active, pb.imported_at AS created_at,
                   COALESCE(jsonb_agg(t.knowledge_point ORDER BY t.knowledge_point)
                       FILTER (WHERE t.knowledge_point IS NOT NULL), '[]'::jsonb) AS tags,
                   COUNT(DISTINCT tc.testcase_id) AS testcase_count,
                   COUNT(DISTINCT tc.testcase_id) FILTER (WHERE tc.active = TRUE) AS active_testcase_count
            FROM ascendany.recommendation_problem_bank pb
            LEFT JOIN ascendany.recommendation_problem_tags t ON t.problem_id = pb.problem_id
            LEFT JOIN ascendany.oj_problem_testcases tc ON tc.problem_id = pb.problem_id
            {where_sql}
            GROUP BY pb.problem_id
            ORDER BY pb.problem_id
            LIMIT %s OFFSET %s
            """,
            tuple(params + [limit, offset]),
        )
        items = []
        for row in rows:
            tags = _row_tags(row)
            items.append(
                {
                    "problemId": str(row["problem_id"]),
                    "active": bool(row.get("active")),
                    "categoryTags": " ".join(tags),
                    "tags": tags,
                    "link": row.get("link") or "",
                    "submissionCount": float(row.get("submission_count") or 0),
                    "passCount": float(row.get("pass_count") or 0),
                    "createdAt": _iso(row.get("created_at")),
                    "testcaseCount": int(row.get("testcase_count") or 0),
                    "activeTestcaseCount": int(row.get("active_testcase_count") or 0),
                    "descriptionPreview": _preview(str(row.get("description") or "")),
                }
            )
        return _admin_resp(
            request_id,
            200,
            {"total": int(total_row.get("total") or 0) if total_row else 0, "list": items},
            "获取题目列表成功",
        )

    async def admin_problems_get(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        problem_id = str(payload.get("problemId") or "").strip()
        if not problem_id:
            return _admin_resp(request_id, 400, None, "缺少problemId")
        row = await self._fetch_problem_admin(problem_id)
        if not row:
            return _admin_resp(request_id, 404, None, "题目不存在")
        counts = await self._fetch_problem_admin_counts(problem_id)
        tags = _row_tags(row)
        return _admin_resp(
            request_id,
            200,
            {
                "problemId": str(row["problem_id"]),
                "active": bool(row.get("active")),
                "description": row.get("description") or "",
                "categoryTags": " ".join(tags),
                "tags": tags,
                "solution1": row.get("solution_1") or "",
                "solution2": row.get("solution_2") or "",
                "link": row.get("link") or "",
                "submissionCount": float(row.get("submission_count") or 0),
                "passCount": float(row.get("pass_count") or 0),
                "createdAt": _iso(row.get("imported_at")),
                "testcaseCount": int(counts.get("testcase_count") or 0),
                "activeTestcaseCount": int(counts.get("active_testcase_count") or 0),
                "submitRecordCount": int(counts.get("submit_record_count") or 0),
            },
            "获取题目成功",
        )

    async def admin_problems_create(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        problem_id = str(payload.get("problemId") or "").strip()
        description = str(payload.get("description") or "")
        if not problem_id:
            return _admin_resp(request_id, 400, None, "请输入题号")
        if not description.strip():
            return _admin_resp(request_id, 400, None, "请输入题目描述")
        if len(problem_id) > 50:
            return _admin_resp(request_id, 400, None, "题号过长")
        if await self._fetch_problem_admin(problem_id):
            return _admin_resp(request_id, 400, None, "题号已存在")

        title = str(payload.get("title") or "").strip() or problem_id
        active = _coerce_bool(payload.get("active"), default=True)
        source_hash = _problem_source_hash(problem_id, description)
        tags = _parse_tags(payload)
        async with self._repository._pool.connection() as conn:
            async with conn.transaction():
                async with conn.cursor() as cursor:
                    await cursor.execute(
                        """
                        INSERT INTO ascendany.recommendation_problem_bank
                            (problem_id, title, description, solution_1, solution_2,
                             link, active, source_hash, updated_at)
                        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, now())
                        """,
                        (
                            problem_id,
                            title,
                            description,
                            _blank_none(payload.get("solution1")),
                            _blank_none(payload.get("solution2")),
                            _blank_none(payload.get("link")),
                            active,
                            source_hash,
                        ),
                    )
                    await self._replace_problem_tags(cursor, problem_id, tags)
        return _admin_resp(request_id, 200, {"problemId": problem_id}, "创建题目成功")

    async def admin_problems_update(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        problem_id = str(payload.get("problemId") or "").strip()
        if not problem_id:
            return _admin_resp(request_id, 400, None, "缺少problemId")
        current = await self._fetch_problem_admin(problem_id)
        if not current:
            return _admin_resp(request_id, 404, None, "题目不存在")

        fields: list[str] = []
        params: list[Any] = []
        if "title" in payload:
            fields.append("title = %s")
            params.append(_blank_none(payload.get("title")) or problem_id)
        if "description" in payload:
            fields.append("description = %s")
            params.append(str(payload.get("description") or ""))
        if "solution1" in payload:
            fields.append("solution_1 = %s")
            params.append(_blank_none(payload.get("solution1")))
        if "solution2" in payload:
            fields.append("solution_2 = %s")
            params.append(_blank_none(payload.get("solution2")))
        if "link" in payload:
            fields.append("link = %s")
            params.append(_blank_none(payload.get("link")))
        if "active" in payload:
            fields.append("active = %s")
            params.append(_coerce_bool(payload.get("active"), default=bool(current.get("active"))))
        if "description" in payload:
            fields.append("source_hash = %s")
            params.append(_problem_source_hash(problem_id, str(payload.get("description") or "")))
        tags_changed = "categoryTags" in payload or "tags" in payload
        if not fields and not tags_changed:
            return _admin_resp(request_id, 400, None, "没有需要更新的字段")

        async with self._repository._pool.connection() as conn:
            async with conn.transaction():
                async with conn.cursor() as cursor:
                    if fields:
                        params.append(problem_id)
                        await cursor.execute(
                            f"""
                            UPDATE ascendany.recommendation_problem_bank
                            SET {", ".join(fields)}, updated_at = now()
                            WHERE problem_id = %s
                            """,
                            tuple(params),
                        )
                    if tags_changed:
                        await self._replace_problem_tags(
                            cursor, problem_id, _parse_tags(payload)
                        )
        return _admin_resp(request_id, 200, None, "更新题目成功")

    async def admin_problems_delete(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        problem_id = str(payload.get("problemId") or "").strip()
        if not problem_id:
            return _admin_resp(request_id, 400, None, "缺少problemId")
        if not await self._fetch_problem_admin(problem_id):
            return _admin_resp(request_id, 404, None, "题目不存在")
        counts = await self._fetch_problem_admin_counts(problem_id)
        tc_count = int(counts.get("testcase_count") or 0)
        sub_count = int(counts.get("submit_record_count") or 0)
        if tc_count or sub_count:
            return _admin_resp(
                request_id,
                400,
                {"testcaseCount": tc_count, "submitRecordCount": sub_count},
                "该题目存在测试点或提交记录，禁止删除",
            )
        await self._execute(
            "DELETE FROM ascendany.recommendation_problem_bank WHERE problem_id = %s",
            (problem_id,),
        )
        return _admin_resp(request_id, 200, None, "删除题目成功")

    async def admin_testcases_list(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        problem_id = str(payload.get("problemId") or "").strip()
        if not problem_id:
            return _admin_resp(request_id, 400, None, "缺少problemId")
        if not await self._fetch_problem_admin(problem_id):
            return _admin_resp(request_id, 404, None, "题目不存在")
        limit = _parse_int(payload.get("page"), default=100, min_value=1, max_value=500)
        offset = _parse_int(payload.get("offset"), default=0, min_value=0)
        total_row = await self._fetch_one(
            "SELECT COUNT(*) AS total FROM ascendany.oj_problem_testcases WHERE problem_id = %s",
            (problem_id,),
        )
        rows = await self._fetch_all(
            """
            SELECT testcase_id, problem_id, input_data, output_data, is_sample,
                   weight, time_limit_ms, memory_limit_kb, active, created_at
            FROM ascendany.oj_problem_testcases
            WHERE problem_id = %s
            ORDER BY testcase_id
            LIMIT %s OFFSET %s
            """,
            (problem_id, limit, offset),
        )
        items = []
        for row in rows:
            input_data = str(row.get("input_data") or "")
            output_data = str(row.get("output_data") or "")
            items.append(
                {
                    "id": int(row["testcase_id"]),
                    "problemId": row["problem_id"],
                    "isSample": bool(row.get("is_sample")),
                    "weight": float(row.get("weight") or 0),
                    "timeLimitMs": int(row.get("time_limit_ms") or 0),
                    "memoryLimitKb": int(row.get("memory_limit_kb") or 0),
                    "active": bool(row.get("active")),
                    "createdAt": _iso(row.get("created_at")),
                    "inputSize": len(input_data),
                    "outputSize": len(output_data),
                    "inputPreview": _preview(input_data, limit=120),
                    "outputPreview": _preview(output_data, limit=120),
                }
            )
        return _admin_resp(
            request_id,
            200,
            {"total": int(total_row.get("total") or 0) if total_row else 0, "list": items},
            "获取测试点列表成功",
        )

    async def admin_testcases_get(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        testcase_id = _parse_int(payload.get("id"), default=0, min_value=1)
        if not testcase_id:
            return _admin_resp(request_id, 400, None, "缺少id")
        row = await self._fetch_testcase_admin(testcase_id)
        if not row:
            return _admin_resp(request_id, 404, None, "测试点不存在")
        return _admin_resp(
            request_id,
            200,
            {
                "id": int(row["testcase_id"]),
                "problemId": row["problem_id"],
                "isSample": bool(row.get("is_sample")),
                "weight": float(row.get("weight") or 0),
                "timeLimitMs": int(row.get("time_limit_ms") or 0),
                "memoryLimitKb": int(row.get("memory_limit_kb") or 0),
                "active": bool(row.get("active")),
                "createdAt": _iso(row.get("created_at")),
                "inputData": row.get("input_data") or "",
                "outputData": row.get("output_data") or "",
            },
            "获取测试点成功",
        )

    async def admin_testcases_create(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        problem_id = str(payload.get("problemId") or "").strip()
        input_data = str(payload.get("inputData") or "")
        output_data = str(payload.get("outputData") or "")
        if not problem_id:
            return _admin_resp(request_id, 400, None, "缺少problemId")
        if not input_data.strip() and not output_data.strip():
            return _admin_resp(request_id, 400, None, "请输入测试点输入/输出")
        if not await self._fetch_problem_admin(problem_id):
            return _admin_resp(request_id, 404, None, "题目不存在")

        is_sample = _coerce_bool(payload.get("isSample"), default=False)
        active = _coerce_bool(payload.get("active"), default=True)
        weight = _parse_float(
            payload.get("weight"), default=1.0, min_value=0.0, max_value=1000000.0
        )
        time_limit_ms = _parse_int(
            payload.get("timeLimitMs"), default=1000, min_value=1, max_value=600000
        )
        memory_limit_kb = _parse_int(
            payload.get("memoryLimitKb"),
            default=262144,
            min_value=1,
            max_value=1024 * 1024 * 16,
        )
        source_hash = testcase_source_hash(problem_id, input_data, output_data, is_sample)
        async with self._repository._pool.connection() as conn:
            async with conn.transaction():
                async with conn.cursor(row_factory=dict_row) as cursor:
                    await cursor.execute(
                        """
                        INSERT INTO ascendany.oj_problem_testcases
                            (problem_id, input_data, output_data, is_sample, weight,
                             time_limit_ms, memory_limit_kb, active, source_hash,
                             updated_at)
                        VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, now())
                        ON CONFLICT (problem_id, source_hash)
                        DO UPDATE SET input_data = EXCLUDED.input_data,
                                      output_data = EXCLUDED.output_data,
                                      weight = EXCLUDED.weight,
                                      time_limit_ms = EXCLUDED.time_limit_ms,
                                      memory_limit_kb = EXCLUDED.memory_limit_kb,
                                      active = EXCLUDED.active,
                                      updated_at = now()
                        RETURNING testcase_id
                        """,
                        (
                            problem_id,
                            input_data,
                            output_data,
                            is_sample,
                            weight,
                            time_limit_ms,
                            memory_limit_kb,
                            active,
                            source_hash,
                        ),
                    )
                    row = await cursor.fetchone()
                    testcase_id = int(row["testcase_id"])
                    if is_sample:
                        await cursor.execute(
                            """
                            UPDATE ascendany.oj_problem_testcases
                            SET is_sample = FALSE, updated_at = now()
                            WHERE problem_id = %s AND testcase_id <> %s AND is_sample = TRUE
                            """,
                            (problem_id, testcase_id),
                        )
        return _admin_resp(request_id, 200, {"id": testcase_id}, "创建测试点成功")

    async def admin_testcases_update(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        testcase_id = _parse_int(payload.get("id"), default=0, min_value=1)
        if not testcase_id:
            return _admin_resp(request_id, 400, None, "缺少id")
        current = await self._fetch_testcase_admin(testcase_id)
        if not current:
            return _admin_resp(request_id, 404, None, "测试点不存在")

        values = {
            "input_data": current.get("input_data") or "",
            "output_data": current.get("output_data") or "",
            "is_sample": bool(current.get("is_sample")),
        }
        fields: list[str] = []
        params: list[Any] = []
        if "inputData" in payload:
            values["input_data"] = str(payload.get("inputData") or "")
            fields.append("input_data = %s")
            params.append(values["input_data"])
        if "outputData" in payload:
            values["output_data"] = str(payload.get("outputData") or "")
            fields.append("output_data = %s")
            params.append(values["output_data"])
        if "isSample" in payload:
            values["is_sample"] = _coerce_bool(
                payload.get("isSample"), default=bool(current.get("is_sample"))
            )
            fields.append("is_sample = %s")
            params.append(values["is_sample"])
        if "weight" in payload:
            fields.append("weight = %s")
            params.append(
                _parse_float(
                    payload.get("weight"),
                    default=float(current.get("weight") or 1.0),
                    min_value=0.0,
                    max_value=1000000.0,
                )
            )
        if "timeLimitMs" in payload:
            fields.append("time_limit_ms = %s")
            params.append(
                _parse_int(
                    payload.get("timeLimitMs"),
                    default=int(current.get("time_limit_ms") or 1000),
                    min_value=1,
                    max_value=600000,
                )
            )
        if "memoryLimitKb" in payload:
            fields.append("memory_limit_kb = %s")
            params.append(
                _parse_int(
                    payload.get("memoryLimitKb"),
                    default=int(current.get("memory_limit_kb") or 262144),
                    min_value=1,
                    max_value=1024 * 1024 * 16,
                )
            )
        if "active" in payload:
            fields.append("active = %s")
            params.append(_coerce_bool(payload.get("active"), default=bool(current.get("active"))))
        if not fields:
            return _admin_resp(request_id, 400, None, "没有需要更新的字段")

        fields.append("source_hash = %s")
        params.append(
            testcase_source_hash(
                str(current["problem_id"]),
                str(values["input_data"]),
                str(values["output_data"]),
                bool(values["is_sample"]),
            )
        )
        async with self._repository._pool.connection() as conn:
            async with conn.transaction():
                async with conn.cursor() as cursor:
                    params.append(testcase_id)
                    await cursor.execute(
                        f"""
                        UPDATE ascendany.oj_problem_testcases
                        SET {", ".join(fields)}, updated_at = now()
                        WHERE testcase_id = %s
                        """,
                        tuple(params),
                    )
                    if values["is_sample"]:
                        await cursor.execute(
                            """
                            UPDATE ascendany.oj_problem_testcases
                            SET is_sample = FALSE, updated_at = now()
                            WHERE problem_id = %s AND testcase_id <> %s AND is_sample = TRUE
                            """,
                            (current["problem_id"], testcase_id),
                        )
        return _admin_resp(request_id, 200, None, "更新测试点成功")

    async def admin_testcases_delete(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        testcase_id = _parse_int(payload.get("id"), default=0, min_value=1)
        if not testcase_id:
            return _admin_resp(request_id, 400, None, "缺少id")
        if not await self._fetch_testcase_admin(testcase_id):
            return _admin_resp(request_id, 404, None, "测试点不存在")
        await self._execute(
            "DELETE FROM ascendany.oj_problem_testcases WHERE testcase_id = %s",
            (testcase_id,),
        )
        return _admin_resp(request_id, 200, None, "删除测试点成功")

    async def admin_submissions_list(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        limit = _parse_int(payload.get("page"), default=50, min_value=1, max_value=200)
        offset = _parse_int(payload.get("offset"), default=0, min_value=0)
        where = []
        params: list[Any] = []
        for key, column in (
            ("problemId", "r.problem_id"),
            ("studentName", "r.student_name"),
            ("dataset", "r.dataset"),
            ("session", "r.session"),
        ):
            value = str(payload.get(key) or "").strip()
            if value:
                where.append(f"{column} ILIKE %s")
                params.append(f"%{value}%")
        status = str(payload.get("status") or "").strip()
        if status:
            where.append("r.status = %s")
            params.append(status)
        if "isCorrect" in payload and str(payload.get("isCorrect") or "").strip() != "":
            where.append("r.is_correct = %s")
            params.append(_coerce_bool(payload.get("isCorrect"), default=False))
        where_sql = "WHERE " + " AND ".join(where) if where else ""
        total_row = await self._fetch_one(
            f"SELECT COUNT(*) AS total FROM ascendany.oj_submit_records r {where_sql}",
            tuple(params),
        )
        rows = await self._fetch_all(
            f"""
            SELECT r.submit_id, r.problem_id, r.student_name, r.status, r.language,
                   r.submit_time, r.score, r.score_rate, r.runtime_ms, r.memory_kb,
                   r.dataset, r.session, r.is_correct, r.error_message
            FROM ascendany.oj_submit_records r
            {where_sql}
            ORDER BY r.submit_time DESC, r.submit_id DESC
            LIMIT %s OFFSET %s
            """,
            tuple(params + [limit, offset]),
        )
        items = [
            {
                "id": int(row["submit_id"]),
                "problemId": row.get("problem_id") or "",
                "studentName": row.get("student_name") or "",
                "status": row.get("status") or "",
                "language": row.get("language") or "",
                "submitTime": _iso(row.get("submit_time")),
                "score": float(row.get("score") or 0),
                "scoreRate": float(row.get("score_rate") or 0),
                "runtimeMs": int(row.get("runtime_ms") or 0),
                "memoryKb": int(row.get("memory_kb") or 0),
                "dataset": row.get("dataset") or "",
                "session": row.get("session") or "",
                "isCorrect": bool(row.get("is_correct")),
                "errorPreview": _preview(str(row.get("error_message") or ""), limit=120),
            }
            for row in rows
        ]
        return _admin_resp(
            request_id,
            200,
            {"total": int(total_row.get("total") or 0) if total_row else 0, "list": items},
            "获取提交记录成功",
        )

    async def admin_submissions_get(self, payload: dict[str, Any]) -> dict[str, Any]:
        request_id = _request_id(payload)
        submit_id = _parse_int(payload.get("id"), default=0, min_value=1)
        if not submit_id:
            return _admin_resp(request_id, 400, None, "缺少id")
        row = await self._fetch_one(
            """
            SELECT submit_id, problem_id, student_name, status, language, submit_time,
                   score, score_rate, runtime_ms, memory_kb, dataset, session,
                   is_correct, code_content, error_message, extra, global_problem_id
            FROM ascendany.oj_submit_records
            WHERE submit_id = %s
            LIMIT 1
            """,
            (submit_id,),
        )
        if not row:
            return _admin_resp(request_id, 404, None, "提交记录不存在")
        return _admin_resp(
            request_id,
            200,
            {
                "id": int(row["submit_id"]),
                "problemId": row.get("problem_id") or "",
                "studentName": row.get("student_name") or "",
                "status": row.get("status") or "",
                "language": row.get("language") or "",
                "submitTime": _iso(row.get("submit_time")),
                "score": float(row.get("score") or 0),
                "scoreRate": float(row.get("score_rate") or 0),
                "runtimeMs": int(row.get("runtime_ms") or 0),
                "memoryKb": int(row.get("memory_kb") or 0),
                "dataset": row.get("dataset") or "",
                "session": row.get("session") or "",
                "isCorrect": bool(row.get("is_correct")),
                "codeContent": row.get("code_content") or "",
                "errorMessage": row.get("error_message") or "",
                "extra": row.get("extra") or {},
                "globalProblemId": row.get("global_problem_id") or "",
            },
            "获取提交记录成功",
        )

    async def _fetch_problem(self, problem_id: str) -> dict[str, Any] | None:
        return await self._fetch_one(
            """
            SELECT pb.*,
                   COALESCE(jsonb_agg(t.knowledge_point ORDER BY t.knowledge_point)
                       FILTER (WHERE t.knowledge_point IS NOT NULL), '[]'::jsonb) AS tags
            FROM ascendany.recommendation_problem_bank pb
            LEFT JOIN ascendany.recommendation_problem_tags t ON t.problem_id = pb.problem_id
            WHERE pb.problem_id = %s AND pb.active = TRUE
            GROUP BY pb.problem_id
            LIMIT 1
            """,
            (problem_id,),
        )

    async def _fetch_problem_admin(self, problem_id: str) -> dict[str, Any] | None:
        return await self._fetch_one(
            """
            SELECT pb.*,
                   COALESCE(jsonb_agg(t.knowledge_point ORDER BY t.knowledge_point)
                       FILTER (WHERE t.knowledge_point IS NOT NULL), '[]'::jsonb) AS tags
            FROM ascendany.recommendation_problem_bank pb
            LEFT JOIN ascendany.recommendation_problem_tags t ON t.problem_id = pb.problem_id
            WHERE pb.problem_id = %s
            GROUP BY pb.problem_id
            LIMIT 1
            """,
            (problem_id,),
        )

    async def _fetch_problem_admin_counts(self, problem_id: str) -> dict[str, Any]:
        row = await self._fetch_one(
            """
            SELECT
                (SELECT COUNT(*) FROM ascendany.oj_problem_testcases WHERE problem_id = %s) AS testcase_count,
                (SELECT COUNT(*) FROM ascendany.oj_problem_testcases WHERE problem_id = %s AND active = TRUE) AS active_testcase_count,
                (SELECT COUNT(*) FROM ascendany.oj_submit_records WHERE problem_id = %s) AS submit_record_count
            """,
            (problem_id, problem_id, problem_id),
        )
        return row or {}

    async def _fetch_testcase_admin(self, testcase_id: int) -> dict[str, Any] | None:
        return await self._fetch_one(
            """
            SELECT testcase_id, problem_id, input_data, output_data, is_sample,
                   weight, time_limit_ms, memory_limit_kb, active, created_at
            FROM ascendany.oj_problem_testcases
            WHERE testcase_id = %s
            LIMIT 1
            """,
            (testcase_id,),
        )

    async def _replace_problem_tags(
        self, cursor: Any, problem_id: str, tags: list[str]
    ) -> None:
        await cursor.execute(
            "DELETE FROM ascendany.recommendation_problem_tags WHERE problem_id = %s",
            (problem_id,),
        )
        for tag in tags:
            await cursor.execute(
                """
                INSERT INTO ascendany.recommendation_problem_tags
                    (problem_id, knowledge_point, source, confidence)
                VALUES (%s, %s, 'admin', 1.0)
                ON CONFLICT (problem_id, knowledge_point) DO NOTHING
                """,
                (problem_id, tag),
            )

    async def _fetch_sample(self, problem_id: str) -> dict[str, Any] | None:
        return await self._fetch_one(
            """
            SELECT input_data, output_data, time_limit_ms, memory_limit_kb
            FROM ascendany.oj_problem_testcases
            WHERE problem_id = %s AND active = TRUE AND is_sample = TRUE
            ORDER BY testcase_id
            LIMIT 1
            """,
            (problem_id,),
        )

    async def _fetch_testcases(self, problem_id: str) -> list[dict[str, Any]]:
        return await self._fetch_all(
            """
            SELECT input_data, output_data, weight, time_limit_ms, memory_limit_kb
            FROM ascendany.oj_problem_testcases
            WHERE problem_id = %s AND active = TRUE
            ORDER BY is_sample DESC, testcase_id
            """,
            (problem_id,),
        )

    async def _fetch_latest_submit(
        self, account_id: int, problem_id: str
    ) -> dict[str, Any] | None:
        return await self._fetch_one(
            """
            SELECT code_content, language, submit_time
            FROM ascendany.oj_submit_records
            WHERE account_id = %s AND problem_id = %s
            ORDER BY submit_time DESC, submit_id DESC
            LIMIT 1
            """,
            (account_id, problem_id),
        )

    async def _available_tags(self) -> list[str]:
        rows = await self._fetch_all(
            """
            SELECT knowledge_point
            FROM ascendany.recommendation_problem_tags
            GROUP BY knowledge_point
            ORDER BY COUNT(*) DESC, knowledge_point
            LIMIT 100
            """,
            (),
        )
        return [str(row["knowledge_point"]) for row in rows]

    async def _fetch_attempts(
        self, account: AuthenticatedAccount | None, problem_ids: list[str]
    ) -> dict[str, dict[str, bool]]:
        if account is None or not problem_ids:
            return {}
        rows = await self._fetch_all(
            """
            SELECT problem_id,
                   COUNT(*) > 0 AS attempted,
                   BOOL_OR(status = 'AC') AS solved
            FROM ascendany.oj_submit_records
            WHERE account_id = %s AND problem_id = ANY(%s::text[])
            GROUP BY problem_id
            """,
            (account.account_id, problem_ids),
        )
        return {
            str(row["problem_id"]): {
                "attempted": bool(row.get("attempted")),
                "solved": bool(row.get("solved")),
            }
            for row in rows
        }

    async def _recommended_for_account(
        self, account: AuthenticatedAccount | None
    ) -> list[dict[str, Any]]:
        if account is None:
            return []
        profile = await self._repository.fetch_account_profile(account.account_id)
        if not profile or not profile.student_id:
            return []
        student_id = await self._fetch_student_entity_id(profile.student_id)
        if student_id is None:
            return []
        snapshot = await self._repository.fetch_latest_problem_recommendations(
            [student_id], top_k=6
        )
        if snapshot is None:
            return []
        out: list[dict[str, Any]] = []
        for item in snapshot.items:
            pid = str(item.get("problemId") or item.get("problem_id") or "").strip()
            if not pid:
                continue
            out.append(
                {
                    "problem_id": pid,
                    "title": item.get("title") or pid,
                    "difficulty": item.get("difficulty") or 0,
                    "knowledge_points": item.get("knowledgePoints")
                    or item.get("knowledge_points")
                    or [],
                    "score": item.get("score") or 0,
                    "reason": item.get("reason") or "",
                    "url": f"/oj/{pid}",
                }
            )
        return out

    def _problem_item(self, row: dict[str, Any], attempts: dict[str, bool]) -> dict[str, Any]:
        desc = re.sub(r"<[^>]+>", "", str(row.get("description") or ""))
        desc = re.sub(r"\s+", " ", desc).strip()
        return {
            "problem_id": str(row["problem_id"]),
            "title": row.get("title") or str(row["problem_id"]),
            "tags": row.get("tags") if isinstance(row.get("tags"), list) else [],
            "pass_count": float(row.get("pass_count") or 0),
            "submission_count": float(row.get("submission_count") or 0),
            "description_preview": desc[:240],
            "link": row.get("link") or "",
            "attempted": bool(attempts.get("attempted")),
            "solved": bool(attempts.get("solved")),
        }

    async def _fetch_student_entity_id(self, student_no: str | None) -> int | None:
        if not student_no:
            return None
        row = await self._fetch_one(
            """
            SELECT student_id
            FROM ascendany.students
            WHERE canonical_key = %s
            LIMIT 1
            """,
            (student_no,),
        )
        return int(row["student_id"]) if row else None

    async def _insert_run_event(
        self,
        account: AuthenticatedAccount,
        problem_id: str,
        language: str,
        result: JudgeResult,
    ) -> None:
        await self._execute(
            """
            INSERT INTO ascendany.oj_run_events
                (account_id, problem_id, language, status, runtime_ms, memory_kb)
            VALUES (%s, %s, %s, %s, %s, %s)
            """,
            (
                account.account_id,
                problem_id,
                language,
                result.status,
                result.runtime_ms,
                result.memory_kb,
            ),
        )

    async def _insert_submit_record(
        self,
        *,
        account: AuthenticatedAccount,
        student_entity_id: int | None,
        student_id: str | None,
        student_name: str | None,
        problem_id: str,
        code: str,
        language: str,
        result: JudgeResult,
    ) -> None:
        dataset, session = self._split_problem_id(problem_id)
        await self._execute(
            """
            INSERT INTO ascendany.oj_submit_records (
                account_id, student_entity_id, student_id, student_name,
                problem_id, code_content, language, status, error_message,
                score, score_rate, memory_kb, runtime_ms, session, dataset,
                global_problem_id, is_correct
            )
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
            """,
            (
                account.account_id,
                student_entity_id,
                student_id,
                student_name,
                problem_id,
                code,
                language,
                result.status,
                result.message,
                result.score,
                result.score_rate,
                result.memory_kb,
                result.runtime_ms,
                session,
                dataset,
                problem_id,
                result.status == "AC",
            ),
        )

    @staticmethod
    def _split_problem_id(problem_id: str) -> tuple[str, str]:
        parts = (problem_id or "").split("_")
        if len(parts) >= 2:
            return parts[0], parts[1]
        return "", ""

    async def _fetch_one(self, query: str, params: tuple[Any, ...]) -> dict[str, Any] | None:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                row = await cursor.fetchone()
        return dict(row) if row else None

    async def _fetch_all(self, query: str, params: tuple[Any, ...]) -> list[dict[str, Any]]:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor(row_factory=dict_row) as cursor:
                await cursor.execute(query, params)
                rows = await cursor.fetchall()
        return [dict(row) for row in rows]

    async def _execute(self, query: str, params: tuple[Any, ...]) -> None:
        async with self._repository._pool.connection() as conn:
            async with conn.cursor() as cursor:
                await cursor.execute(query, params)


def testcase_source_hash(
    problem_id: str, input_data: str, output_data: str, is_sample: bool
) -> str:
    raw = f"{problem_id}\0{input_data}\0{output_data}\0{int(is_sample)}"
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()
