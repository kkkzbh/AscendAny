from __future__ import annotations

import json
from pathlib import Path

from recommendation.scripts.import_practice_problem_tags import _read_mapping


def test_read_practice_problem_mapping_normalizes_tags(tmp_path: Path) -> None:
    source = tmp_path / "problem_knowledge_mapping.json"
    source.write_text(
        json.dumps(
            {
                "datastructure_2021第一次月测_6-1-1": ["数组", "数组", " 线性结构\t"],
                "datastructure_2021第一次月测_6-1-2": "链表，双指针",
            },
            ensure_ascii=False,
        ),
        encoding="utf-8",
    )

    rows = _read_mapping(source)

    assert rows[0].practice_problem_id == "datastructure_2021第一次月测_6-1-1"
    assert rows[0].tags == ["数组", "线性结构"]
    assert rows[1].tags == ["链表", "双指针"]
