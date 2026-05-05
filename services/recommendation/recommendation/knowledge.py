from __future__ import annotations

from functools import lru_cache
import math
from pathlib import Path
from typing import Any

import yaml


KNOWLEDGE_TREE_PATH = Path(__file__).resolve().parent / "data" / "knowledge_tree.yaml"


@lru_cache(maxsize=1)
def load_knowledge_tree() -> dict[str, Any]:
    if not KNOWLEDGE_TREE_PATH.exists():
        return {"knowledge_tree": {}, "prerequisites": []}
    with KNOWLEDGE_TREE_PATH.open("r", encoding="utf-8") as handle:
        payload = yaml.safe_load(handle) or {}
    return {
        "knowledge_tree": payload.get("knowledge_tree", {}) or {},
        "prerequisites": payload.get("prerequisites", []) or [],
    }


def get_all_knowledge_points() -> list[str]:
    points: list[str] = []

    def visit(node: object) -> None:
        if isinstance(node, dict):
            for key, value in node.items():
                if str(key) not in points:
                    points.append(str(key))
                visit(value)
        elif isinstance(node, list):
            for item in node:
                if isinstance(item, str):
                    if item not in points:
                        points.append(item)
                else:
                    visit(item)

    visit(load_knowledge_tree().get("knowledge_tree", {}))
    return points


def build_parent_edges(knowledge_to_idx: dict[str, int]) -> list[tuple[int, int]]:
    edges: list[tuple[int, int]] = []
    tree = load_knowledge_tree().get("knowledge_tree", {})

    def visit(node: object, parent: str | None = None) -> None:
        if isinstance(node, dict):
            for key, value in node.items():
                current = str(key)
                if parent is not None:
                    src = knowledge_to_idx.get(current)
                    dst = knowledge_to_idx.get(parent)
                    if src is not None and dst is not None:
                        edges.append((src, dst))
                visit(value, current)
        elif isinstance(node, list):
            for item in node:
                if isinstance(item, str):
                    if parent is None:
                        continue
                    src = knowledge_to_idx.get(item)
                    dst = knowledge_to_idx.get(parent)
                    if src is not None and dst is not None:
                        edges.append((src, dst))
                else:
                    visit(item, parent)

    visit(tree)
    return edges


def build_prerequisite_map() -> dict[str, set[str]]:
    prereq_map: dict[str, set[str]] = {}
    for row in load_knowledge_tree().get("prerequisites", []):
        if not isinstance(row, list) or len(row) != 2:
            continue
        target, prereq = str(row[0]), str(row[1])
        prereq_map.setdefault(target, set()).add(prereq)
    return prereq_map


def build_prerequisite_edges(knowledge_to_idx: dict[str, int]) -> list[tuple[int, int]]:
    edges: list[tuple[int, int]] = []
    for target, prereqs in build_prerequisite_map().items():
        target_idx = knowledge_to_idx.get(target)
        if target_idx is None:
            continue
        for prereq in prereqs:
            prereq_idx = knowledge_to_idx.get(prereq)
        if prereq_idx is not None:
            edges.append((target_idx, prereq_idx))
    return edges


class KnowledgeTree:
    def __init__(self, tree: dict[str, Any] | None = None) -> None:
        self._tree = tree if tree is not None else load_knowledge_tree().get("knowledge_tree", {})
        self._children: dict[str, list[str]] = {}
        self._parent: dict[str, str] = {}
        self._levels: dict[str, str] = {}
        self._roots: list[str] = []
        self._index(self._tree, parent=None, depth=0)

    def all_points(self) -> list[str]:
        return list(self._levels)

    def root_nodes(self) -> list[str]:
        return list(self._roots)

    def children(self, point: str) -> list[str]:
        return list(self._children.get(point, []))

    def is_leaf(self, point: str) -> bool:
        return not self._children.get(point)

    def level(self, point: str) -> str | None:
        return self._levels.get(point)

    def ancestors(self, point: str) -> list[str]:
        result: list[str] = []
        current = point
        while current in self._parent:
            current = self._parent[current]
            result.append(current)
        return result

    def descendants(self, point: str) -> list[str]:
        result: list[str] = []

        def visit(node: str) -> None:
            for child in self._children.get(node, []):
                result.append(child)
                visit(child)

        visit(point)
        return result

    def _index(self, node: object, *, parent: str | None, depth: int) -> None:
        if isinstance(node, dict):
            for key, value in node.items():
                current = str(key)
                self._register(current, parent=parent, depth=depth)
                self._index(value, parent=current, depth=depth + 1)
        elif isinstance(node, list):
            for item in node:
                if isinstance(item, str):
                    self._register(item, parent=parent, depth=depth)
                else:
                    self._index(item, parent=parent, depth=depth)

    def _register(self, point: str, *, parent: str | None, depth: int) -> None:
        if point not in self._levels:
            self._levels[point] = _level_name(depth)
        if parent is None:
            if point not in self._roots:
                self._roots.append(point)
            return
        if point not in self._children.setdefault(parent, []):
            self._children[parent].append(point)
        self._parent.setdefault(point, parent)


class KnowledgeHierarchyAggregator:
    direct_evidence_weight = 0.3
    child_aggregate_weight = 0.7
    default_mastery = 0.0

    def __init__(
        self,
        tree: KnowledgeTree | None,
        leaf_mastery: dict[str, float],
        leaf_evidence: dict[str, int] | None = None,
    ) -> None:
        self.tree = tree or KnowledgeTree()
        self.leaf_mastery = leaf_mastery
        self.leaf_evidence = leaf_evidence or {}
        self._mastery_cache: dict[str, float] = {}
        self._evidence_cache: dict[str, int] = {}

    def get_mastery(self, point: str) -> float:
        if point in self._mastery_cache:
            return self._mastery_cache[point]
        if point not in self.tree.all_points():
            return self.default_mastery
        value = self._compute_mastery(point)
        self._mastery_cache[point] = value
        return value

    def build_hierarchy(self, points: list[str] | None = None) -> dict[str, Any]:
        if points is None:
            return {root: self._build_node(root) for root in self.tree.root_nodes()}

        requested: set[str] = set()
        for point in points:
            requested.add(point)
            requested.update(self.tree.descendants(point))

        result: dict[str, Any] = {}
        for point in points:
            root = self._root_for(point)
            if root in result:
                continue
            result[root] = self._build_filtered_node(root, requested)
        return result

    def identify_weak_points(
        self,
        *,
        points: list[str] | None = None,
        threshold: float = 0.6,
        top_k: int = 5,
    ) -> list[str]:
        candidates = points or [
            point
            for point in self.tree.all_points()
            if self.tree.level(point) != "root"
        ]
        prereq_map = build_prerequisite_map()
        depth_memo: dict[str, int] = {}
        weak: list[tuple[int, float, int, str]] = []
        for point in candidates:
            mastery = self.get_mastery(point)
            if mastery >= threshold:
                continue
            evidence = self.subtree_evidence(point)
            depth = _dependency_depth(point, prereq_map, depth_memo, set())
            evidence_flag = 0 if evidence > 0 else 1
            weak.append((evidence_flag, mastery, depth, point))
        weak.sort()
        return [point for _, _, _, point in weak[:top_k]]

    def subtree_evidence(self, point: str) -> int:
        if point in self._evidence_cache:
            return self._evidence_cache[point]
        total = int(self.leaf_evidence.get(point, 0))
        for child in self.tree.children(point):
            total += self.subtree_evidence(child)
        self._evidence_cache[point] = total
        return total

    def _compute_mastery(self, point: str) -> float:
        children = self.tree.children(point)
        if not children:
            return float(self.leaf_mastery.get(point, self.default_mastery))

        weighted: list[tuple[float, float]] = []
        for child in children:
            evidence = self.subtree_evidence(child)
            weight = math.log1p(evidence) if evidence > 0 else 0.1
            weighted.append((self.get_mastery(child), weight))
        total_weight = sum(weight for _, weight in weighted)
        child_value = (
            sum(value * weight for value, weight in weighted) / total_weight
            if total_weight > 0
            else self.default_mastery
        )
        direct = self.leaf_mastery.get(point)
        if direct is None:
            return child_value
        return (
            self.direct_evidence_weight * float(direct)
            + self.child_aggregate_weight * child_value
        )

    def _build_node(self, point: str) -> dict[str, Any]:
        node: dict[str, Any] = {
            "mastery": round(self.get_mastery(point), 4),
            "level": self.tree.level(point) or "unknown",
            "evidence": self.subtree_evidence(point),
        }
        children = self.tree.children(point)
        if children:
            node["children"] = {child: self._build_node(child) for child in children}
        return node

    def _build_filtered_node(self, point: str, requested: set[str]) -> dict[str, Any]:
        node = {
            "mastery": round(self.get_mastery(point), 4),
            "level": self.tree.level(point) or "unknown",
            "evidence": self.subtree_evidence(point),
        }
        children = {
            child: self._build_filtered_node(child, requested)
            for child in self.tree.children(point)
            if child in requested or any(desc in requested for desc in self.tree.descendants(child))
        }
        if children:
            node["children"] = children
        return node

    def _root_for(self, point: str) -> str:
        ancestors = self.tree.ancestors(point)
        return ancestors[-1] if ancestors else point


def _level_name(depth: int) -> str:
    if depth <= 0:
        return "root"
    if depth == 1:
        return "category"
    return "knowledge"


def _dependency_depth(
    node: str,
    prereq_map: dict[str, set[str]],
    memo: dict[str, int],
    visiting: set[str],
) -> int:
    if node in memo:
        return memo[node]
    if node in visiting:
        return 0
    visiting.add(node)
    prereqs = prereq_map.get(node, set())
    depth = 0 if not prereqs else 1 + max(
        _dependency_depth(prereq, prereq_map, memo, visiting)
        for prereq in prereqs
    )
    visiting.remove(node)
    memo[node] = depth
    return depth
