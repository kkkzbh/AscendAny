import { describe, expect, it } from "vitest";

import { diffLines, summarizeDiff } from "@/lib/notesDiff";

describe("diffLines", () => {
  it("returns a single unchanged segment when before equals after", () => {
    const segments = diffLines("A\nB\nC", "A\nB\nC");
    expect(segments).toEqual([
      { kind: "unchanged", lines: ["A", "B", "C"] },
    ]);
  });

  it("identifies a single replaced line in the middle", () => {
    const segments = diffLines("A\nB\nC", "A\nB2\nC");
    expect(segments).toEqual([
      { kind: "unchanged", lines: ["A"] },
      { kind: "removed", lines: ["B"] },
      { kind: "added", lines: ["B2"] },
      { kind: "unchanged", lines: ["C"] },
    ]);
  });

  it("treats a full rewrite as one removed segment plus one added segment", () => {
    const segments = diffLines("alpha\nbeta", "gamma\ndelta");
    expect(segments).toEqual([
      { kind: "removed", lines: ["alpha", "beta"] },
      { kind: "added", lines: ["gamma", "delta"] },
    ]);
  });

  it("handles inserts at the end without dropping unchanged prefix", () => {
    const segments = diffLines("A\nB", "A\nB\nC\nD");
    expect(segments).toEqual([
      { kind: "unchanged", lines: ["A", "B"] },
      { kind: "added", lines: ["C", "D"] },
    ]);
  });

  it("handles deletes from the front without dropping unchanged suffix", () => {
    const segments = diffLines("A\nB\nC", "C");
    expect(segments).toEqual([
      { kind: "removed", lines: ["A", "B"] },
      { kind: "unchanged", lines: ["C"] },
    ]);
  });

  it("returns empty for two empty strings", () => {
    expect(diffLines("", "")).toEqual([]);
  });

  it("counts segment line totals with summarizeDiff", () => {
    const segments = diffLines("A\nB\nC", "A\nB2\nC\nD");
    expect(summarizeDiff(segments)).toEqual({
      unchanged: 2,
      removed: 1,
      added: 2,
    });
  });
});
