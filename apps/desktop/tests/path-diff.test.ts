import { describe, expect, it } from "vitest";

import { diffPaths } from "@/lib/pathDiff";

describe("diffPaths", () => {
  it("returns all unchanged when paths are identical", () => {
    const result = diffPaths(["数组", "链表", "栈"], ["数组", "链表", "栈"]);
    expect(result.map((entry) => entry.kind)).toEqual([
      "unchanged",
      "unchanged",
      "unchanged",
    ]);
    expect(result.map((entry) => entry.point)).toEqual(["数组", "链表", "栈"]);
  });

  it("marks newly added trailing nodes", () => {
    const result = diffPaths(["数组"], ["数组", "链表"]);
    expect(result).toEqual([
      { kind: "unchanged", point: "数组", fromIndex: 0, toIndex: 0 },
      { kind: "added", point: "链表", fromIndex: null, toIndex: 1 },
    ]);
  });

  it("marks removed entries when next path is shorter", () => {
    const result = diffPaths(["数组", "链表", "栈"], ["数组", "栈"]);
    expect(result.map((entry) => entry.kind)).toEqual([
      "unchanged",
      "removed",
      "unchanged",
    ]);
  });

  it("handles full replacement", () => {
    const result = diffPaths(["数组"], ["DFS"]);
    expect(result).toHaveLength(2);
    expect(result.find((entry) => entry.kind === "removed")?.point).toBe("数组");
    expect(result.find((entry) => entry.kind === "added")?.point).toBe("DFS");
  });

  it("handles insertion in the middle while preserving common nodes", () => {
    const result = diffPaths(["数组", "栈"], ["数组", "链表", "栈"]);
    expect(result).toEqual([
      { kind: "unchanged", point: "数组", fromIndex: 0, toIndex: 0 },
      { kind: "added", point: "链表", fromIndex: null, toIndex: 1 },
      { kind: "unchanged", point: "栈", fromIndex: 1, toIndex: 2 },
    ]);
  });

  it("returns only added entries when previous path was empty", () => {
    const result = diffPaths([], ["数组", "链表"]);
    expect(result.map((entry) => entry.kind)).toEqual(["added", "added"]);
    expect(result.map((entry) => entry.toIndex)).toEqual([0, 1]);
  });
});
