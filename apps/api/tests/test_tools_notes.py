from __future__ import annotations

import asyncio
import json

from apps.api.services.tools import NOTES_MAX_LENGTH, ToolExecutor


class _NullRepo:
    def __getattr__(self, name: str):  # pragma: no cover - notes tools never touch repo
        raise AssertionError(f"Notes tools should not query repository: {name}")


def _execute(executor: ToolExecutor, tool_name: str, args: dict) -> dict:
    return json.loads(asyncio.run(executor.execute(tool_name, args)))


def test_read_notes_full_returns_title_content_and_line_count() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="第一行\n第二行",
        notes_title="刷题策略",
    )

    payload = _execute(executor, "read_notes", {})

    assert payload == {
        "title": "刷题策略",
        "content": "第一行\n第二行",
        "line_count": 2,
    }


def test_read_notes_search_returns_matches_with_context() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="图论\n最短路容易漏边界\n动态规划\n图论复盘",
        notes_title="刷题策略",
    )

    payload = _execute(
        executor,
        "read_notes",
        {"mode": "search", "query": "图论", "max_matches": 1, "context_lines": 1},
    )

    assert payload["title"] == "刷题策略"
    assert payload["query"] == "图论"
    assert payload["truncated"] is True
    assert payload["matches"] == [
        {
            "line": 1,
            "text": "图论",
            "before": [],
            "after": ["最短路容易漏边界"],
        }
    ]


def test_read_notes_search_requires_query() -> None:
    executor = ToolExecutor(repository=_NullRepo(), identity=None)  # type: ignore[arg-type]

    payload = _execute(executor, "read_notes", {"mode": "search"})

    assert "error" in payload


def test_update_notes_patch_replaces_a_line() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A\nB\nC",
    )

    payload = _execute(
        executor,
        "update_notes",
        {
            "mode": "patch",
            "patch": "--- notes.md\n+++ notes.md\n@@ -1,3 +1,3 @@\n A\n-B\n+B2\n C",
        },
    )

    assert payload == {"ok": True, "mode": "patch", "length": 6}
    assert executor.notes_content == "A\nB2\nC"
    assert executor.pending_notes_update == "A\nB2\nC"


def test_update_notes_patch_adds_an_item() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A\nB",
    )

    payload = _execute(
        executor,
        "update_notes",
        {
            "mode": "patch",
            "patch": "--- notes.md\n+++ notes.md\n@@ -1,2 +1,3 @@\n A\n+X\n B",
        },
    )

    assert payload["ok"] is True
    assert executor.notes_content == "A\nX\nB"


def test_update_notes_patch_deletes_a_paragraph() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A\nB\nC",
    )

    payload = _execute(
        executor,
        "update_notes",
        {
            "mode": "patch",
            "patch": "--- notes.md\n+++ notes.md\n@@ -1,3 +1,2 @@\n A\n-B\n C",
        },
    )

    assert payload["ok"] is True
    assert executor.notes_content == "A\nC"


def test_update_notes_patch_supports_multiple_hunks() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A\nB\nC\nD\nF",
    )

    payload = _execute(
        executor,
        "update_notes",
        {
            "mode": "patch",
            "patch": (
                "--- notes.md\n+++ notes.md\n"
                "@@ -1,2 +1,2 @@\n A\n-B\n+B2\n"
                "@@ -4,2 +4,3 @@\n D\n+E\n F"
            ),
        },
    )

    assert payload["ok"] is True
    assert executor.notes_content == "A\nB2\nC\nD\nE\nF"


def test_update_notes_patch_context_mismatch_fails_without_changing_content() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A\nB\nC",
    )

    payload = _execute(
        executor,
        "update_notes",
        {
            "mode": "patch",
            "patch": "--- notes.md\n+++ notes.md\n@@ -1,3 +1,3 @@\n A\n-X\n+B2\n C",
        },
    )

    assert "error" in payload
    assert executor.notes_content == "A\nB\nC"
    assert executor.pending_notes_update is None


def test_update_notes_replace_overwrites_in_memory() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A",
    )

    payload = _execute(
        executor,
        "update_notes",
        {"mode": "replace", "content": "A\nB"},
    )

    assert payload == {"ok": True, "mode": "replace", "length": 3}
    assert executor.pending_notes_update == "A\nB"
    assert executor.notes_content == "A\nB"


def test_update_notes_rejects_oversized_patch_result() -> None:
    executor = ToolExecutor(repository=_NullRepo(), identity=None)  # type: ignore[arg-type]
    too_big = "x" * (NOTES_MAX_LENGTH + 1)

    payload = _execute(
        executor,
        "update_notes",
        {
            "mode": "patch",
            "patch": f"--- notes.md\n+++ notes.md\n@@ -0,0 +1 @@\n+{too_big}",
        },
    )

    assert "error" in payload
    assert executor.pending_notes_update is None
    assert executor.notes_content == ""


def test_update_notes_rejects_legacy_content_without_mode() -> None:
    executor = ToolExecutor(repository=_NullRepo(), identity=None)  # type: ignore[arg-type]

    payload = _execute(executor, "update_notes", {"content": "A\nB"})

    assert "error" in payload
    assert executor.pending_notes_update is None


def test_pending_notes_update_starts_none() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="seed",
    )

    _execute(executor, "read_notes", {})

    assert executor.pending_notes_update is None


def test_update_notes_emits_pending_event_for_replace() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="old",
    )

    _execute(executor, "update_notes", {"mode": "replace", "content": "new"})

    assert executor.notes_pending_events == [
        {
            "mode": "replace",
            "previous": "old",
            "next": "new",
            "patch": None,
        }
    ]


def test_update_notes_emits_pending_event_for_patch_with_diff_text() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A\nB\nC",
    )
    patch_text = "--- notes.md\n+++ notes.md\n@@ -1,3 +1,3 @@\n A\n-B\n+B2\n C"

    _execute(executor, "update_notes", {"mode": "patch", "patch": patch_text})

    assert executor.notes_pending_events == [
        {
            "mode": "patch",
            "previous": "A\nB\nC",
            "next": "A\nB2\nC",
            "patch": patch_text,
        }
    ]


def test_update_notes_when_locked_returns_error_and_no_event() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="seed",
        notes_locked=True,
    )

    payload = _execute(executor, "update_notes", {"mode": "replace", "content": "x"})

    assert "error" in payload
    assert "正在编辑" in payload["error"]
    assert executor.notes_content == "seed"
    assert executor.pending_notes_update is None
    assert executor.notes_pending_events == []


def test_read_notes_still_works_when_locked() -> None:
    executor = ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="A\nB",
        notes_title="t",
        notes_locked=True,
    )

    payload = _execute(executor, "read_notes", {})

    assert payload["content"] == "A\nB"
    assert payload["title"] == "t"
