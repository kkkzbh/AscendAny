from __future__ import annotations

from recommendation.knowledge import (
    canonical_knowledge_point_set,
    canonicalize_knowledge_tags,
    load_knowledge_tree,
)


def test_canonicalize_splits_combined_tags_to_known_points() -> None:
    result = canonicalize_knowledge_tags("BFS DFS STL 数组 搜索 模拟")

    assert result.tags == ["BFS", "DFS", "STL", "数组", "搜索", "模拟"]
    assert result.rejected == []


def test_canonicalize_aliases_observed_noncanonical_tags() -> None:
    result = canonicalize_knowledge_tags(["循环数组", "中缀表达式", "二叉搜索树"])

    assert result.tags == ["数组", "栈", "二叉树"]
    assert result.rejected == []


def test_canonicalize_rejects_unknown_or_out_of_scope_tags() -> None:
    result = canonicalize_knowledge_tags(["C程序设计", "输入输出", "未定义知识点"])

    assert result.tags == []
    assert [(item.value, item.reason) for item in result.rejected] == [
        ("C程序设计", "out_of_scope"),
        ("输入输出", "out_of_scope"),
        ("未定义知识点", "unknown"),
    ]


def test_descriptions_cover_all_canonical_points() -> None:
    payload = load_knowledge_tree()
    descriptions = payload.get("descriptions")

    assert isinstance(descriptions, dict)
    assert canonical_knowledge_point_set() - set(descriptions) == set()
