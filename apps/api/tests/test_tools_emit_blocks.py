from __future__ import annotations

import asyncio
import json

from apps.api.services.tools import ToolExecutor


class _NullRepo:
    def __getattr__(self, name: str):  # pragma: no cover - emitters never query the DB
        raise AssertionError(f"Emit tools should not query repository: {name}")


def _execute(executor: ToolExecutor, tool_name: str, args: dict) -> dict:
    return json.loads(asyncio.run(executor.execute(tool_name, args)))


def _make_executor() -> ToolExecutor:
    return ToolExecutor(
        repository=_NullRepo(),  # type: ignore[arg-type]
        identity=None,
        notes_content="",
        notes_title="",
    )


def test_emit_problem_card_queues_block_event() -> None:
    executor = _make_executor()

    payload = _execute(
        executor,
        "emit_problem_card",
        {
            "problem_id": "PTA:1234",
            "title": "二项分布期望",
            "difficulty": 1.7,
            "knowledge_points": ["概率论", "二项分布"],
            "reason": "巩固掌握度",
        },
    )

    assert payload == {"ok": True, "kind": "problem", "problemId": "PTA:1234"}
    assert executor.chat_block_events == [
        {
            "kind": "problem",
            "problem": {
                "problemId": "PTA:1234",
                "title": "二项分布期望",
                "difficulty": 1.7,
                "knowledgePoints": ["概率论", "二项分布"],
                "reason": "巩固掌握度",
            },
        }
    ]


def test_emit_choice_validates_options_and_queues_event() -> None:
    executor = _make_executor()

    short = _execute(
        executor,
        "emit_choice",
        {
            "question": "X 是？",
            "options": [{"id": "A", "label": "1"}],
        },
    )
    assert "error" in short
    assert executor.chat_block_events == []

    payload = _execute(
        executor,
        "emit_choice",
        {
            "question": "二项分布期望是？",
            "options": [
                {"id": "A", "label": "np"},
                {"id": "B", "label": "n(n+1)/2"},
            ],
            "answer_idx": 0,
            "explanation": "X~B(n,p) 的期望为 np。",
        },
    )

    assert payload == {"ok": True, "kind": "choice"}
    assert executor.chat_block_events == [
        {
            "kind": "choice",
            "question": "二项分布期望是？",
            "options": [
                {"id": "A", "label": "np"},
                {"id": "B", "label": "n(n+1)/2"},
            ],
            "answerIdx": 0,
            "explanation": "X~B(n,p) 的期望为 np。",
        }
    ]


def test_emit_math_steps_filters_empty_steps() -> None:
    executor = _make_executor()

    payload = _execute(
        executor,
        "emit_math_steps",
        {
            "steps": [
                {"title": "展开", "tex": "(a+b)^n = ...", "note": "二项展开式"},
                {"tex": "  "},  # 只有空白，应被过滤
                {"tex": "E[X] = np"},
            ]
        },
    )

    assert payload == {"ok": True, "kind": "math_steps", "stepCount": 2}
    assert executor.chat_block_events == [
        {
            "kind": "math_steps",
            "steps": [
                {"title": "展开", "tex": "(a+b)^n = ...", "note": "二项展开式"},
                {"tex": "E[X] = np"},
            ],
        }
    ]


def test_emit_code_requires_lang_and_code() -> None:
    executor = _make_executor()

    err = _execute(executor, "emit_code", {"lang": "", "code": "print(1)"})
    assert "error" in err

    payload = _execute(
        executor, "emit_code", {"lang": "python", "code": "print(1)\n"}
    )
    assert payload == {"ok": True, "kind": "code"}
    assert executor.chat_block_events == [
        {"kind": "code", "lang": "python", "code": "print(1)\n"}
    ]


def test_emit_node_ref_and_focus_knowledge_node_split_queues() -> None:
    executor = _make_executor()

    _execute(executor, "emit_node_ref", {"point": "二项分布", "label": "二项分布 B(n,p)"})
    _execute(executor, "focus_knowledge_node", {"point": "概率论"})

    assert executor.chat_block_events == [
        {"kind": "node_ref", "point": "二项分布", "label": "二项分布 B(n,p)"}
    ]
    assert executor.path_visualization_events == [
        {"event": "node_focus", "point": "概率论"}
    ]


def test_emit_callout_validates_tone() -> None:
    executor = _make_executor()

    err = _execute(
        executor, "emit_callout", {"tone": "danger", "markdown": "x"}
    )
    assert "error" in err
    assert executor.chat_block_events == []

    payload = _execute(
        executor, "emit_callout", {"tone": "tip", "markdown": "**记住**：np。"}
    )
    assert payload == {"ok": True, "kind": "callout", "tone": "tip"}
    assert executor.chat_block_events == [
        {"kind": "callout", "tone": "tip", "markdown": "**记住**：np。"}
    ]
