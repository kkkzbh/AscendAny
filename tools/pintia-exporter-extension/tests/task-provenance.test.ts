import { readFile } from "node:fs/promises";
import { describe, expect, it } from "vitest";

describe("export task provenance", () => {
  it("uses completion time and never derives exam metadata from the tab", async () => {
    const source = await readFile(new URL("../src/background.ts", import.meta.url), "utf8");
    expect(source).toMatch(
      /buildSnapshot\(\s*snapshotSource\(completedTask\),\s*nowIso\(\),\s*\{\s*signal: context\.signal,?\s*\}\s*\)/,
    );
    expect(source).not.toContain("completedTask.createdAt");
    expect(source).not.toContain("tab.title");
  });
});
