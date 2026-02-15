"""In-memory task manager for long-running import operations.

Each task runs synchronously in a thread and publishes events via a queue
that can be consumed as an SSE stream.
"""

from __future__ import annotations

import asyncio
import json
import logging
import threading
import uuid
from dataclasses import dataclass, field
from datetime import UTC, datetime
from enum import Enum
from queue import Empty, Queue
from typing import Any, AsyncGenerator

logger = logging.getLogger(__name__)


class TaskStatus(str, Enum):
    PENDING = "pending"
    RUNNING = "running"
    DONE = "done"
    FAILED = "failed"


@dataclass
class TaskEvent:
    event_type: str  # "log" | "progress" | "done" | "error" | "heartbeat"
    data: dict[str, Any]
    timestamp: str = field(default_factory=lambda: datetime.now(UTC).isoformat())


@dataclass
class ImportTask:
    run_id: str
    task_type: str  # "import"
    status: TaskStatus = TaskStatus.PENDING
    queue: Queue[TaskEvent | None] = field(default_factory=Queue)
    thread: threading.Thread | None = None
    result: dict[str, Any] | None = None
    created_at: str = field(default_factory=lambda: datetime.now(UTC).isoformat())


class ImportTaskManager:
    """Manages in-memory import tasks."""

    def __init__(self) -> None:
        self._tasks: dict[str, ImportTask] = {}
        self._lock = threading.Lock()

    def create_task(self, task_type: str) -> str:
        run_id = uuid.uuid4().hex[:12]
        task = ImportTask(run_id=run_id, task_type=task_type)
        with self._lock:
            self._tasks[run_id] = task
        return run_id

    def get_task(self, run_id: str) -> ImportTask | None:
        with self._lock:
            return self._tasks.get(run_id)

    def emit(self, run_id: str, event: TaskEvent) -> None:
        task = self.get_task(run_id)
        if task is None:
            return
        task.queue.put(event)

    def emit_log(
        self,
        run_id: str,
        level: str,
        message: str,
    ) -> None:
        self.emit(
            run_id,
            TaskEvent(
                event_type="log",
                data={
                    "level": level,
                    "message": message,
                },
            ),
        )

    def emit_progress(
        self,
        run_id: str,
        current: int,
        total: int,
        exam_type: str | None = None,
        source_path: str | None = None,
        phase: str | None = None,
    ) -> None:
        self.emit(
            run_id,
            TaskEvent(
                event_type="progress",
                data={
                    "current": current,
                    "total": total,
                    "examType": exam_type,
                    "sourcePath": source_path,
                    "phase": phase,
                },
            ),
        )

    def emit_done(self, run_id: str, data: dict[str, Any]) -> None:
        task = self.get_task(run_id)
        if task is None:
            return
        task.status = TaskStatus.DONE
        task.result = data
        task.queue.put(TaskEvent(event_type="done", data=data))
        task.queue.put(None)  # sentinel to close stream

    def emit_error(self, run_id: str, message: str) -> None:
        task = self.get_task(run_id)
        if task is None:
            return
        task.status = TaskStatus.FAILED
        task.result = {"error": message}
        task.queue.put(
            TaskEvent(event_type="error", data={"message": message})
        )
        task.queue.put(None)  # sentinel

    def mark_running(self, run_id: str) -> None:
        task = self.get_task(run_id)
        if task:
            task.status = TaskStatus.RUNNING

    async def event_stream(
        self, run_id: str, heartbeat_seconds: float = 15.0
    ) -> AsyncGenerator[str, None]:
        """Yields SSE-formatted strings from the task's event queue."""
        task = self.get_task(run_id)
        if task is None:
            yield _sse_format("error", {"message": f"Task {run_id} not found"})
            return

        loop = asyncio.get_running_loop()
        while True:
            try:
                event = await asyncio.wait_for(
                    loop.run_in_executor(None, lambda: task.queue.get(timeout=heartbeat_seconds)),
                    timeout=heartbeat_seconds + 1,
                )
            except (asyncio.TimeoutError, Empty):
                yield _sse_format("heartbeat", {"ts": datetime.now(UTC).isoformat()})
                continue

            if event is None:
                # stream closed
                return

            yield _sse_format(event.event_type, {**event.data, "timestamp": event.timestamp})

    def cleanup_old_tasks(self, max_age_seconds: int = 3600) -> int:
        """Remove finished tasks older than max_age_seconds."""
        now = datetime.now(UTC)
        to_remove: list[str] = []
        with self._lock:
            for run_id, task in self._tasks.items():
                if task.status in (TaskStatus.DONE, TaskStatus.FAILED):
                    try:
                        created = datetime.fromisoformat(task.created_at)
                        if (now - created).total_seconds() > max_age_seconds:
                            to_remove.append(run_id)
                    except (TypeError, ValueError):
                        to_remove.append(run_id)
            for run_id in to_remove:
                del self._tasks[run_id]
        return len(to_remove)

    def list_tasks(self, limit: int = 20) -> list[dict[str, Any]]:
        with self._lock:
            items = sorted(
                self._tasks.values(),
                key=lambda t: t.created_at,
                reverse=True,
            )[:limit]
        return [
            {
                "runId": t.run_id,
                "taskType": t.task_type,
                "status": t.status.value,
                "createdAt": t.created_at,
                "result": t.result,
            }
            for t in items
        ]


# ── Global singleton ─────────────────────────────────────

_task_manager: ImportTaskManager | None = None
_manager_lock = threading.Lock()


def get_task_manager() -> ImportTaskManager:
    global _task_manager
    if _task_manager is None:
        with _manager_lock:
            if _task_manager is None:
                _task_manager = ImportTaskManager()
    return _task_manager


# ── SSE formatting ────────────────────────────────────────

def _sse_format(event_type: str, data: dict[str, Any]) -> str:
    payload = json.dumps(data, ensure_ascii=False, default=str)
    return f"event: {event_type}\ndata: {payload}\n\n"
