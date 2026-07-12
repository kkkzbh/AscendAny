import { describe, expect, it } from "vitest";
import {
  type BeforeCursorPage,
  collectBeforeCursorPages,
  collectNumberedPages,
} from "../src/domain/pagination";

describe("exhaustive numbered pagination", () => {
  it("collects every page and preserves the source-reported count", async () => {
    const pages = [
      { items: [{ id: "a" }, { id: "b" }], sourceReportedCount: 3, hasNext: true },
      { items: [{ id: "c" }], sourceReportedCount: 3, hasNext: false },
    ];
    const result = await collectNumberedPages(
      2,
      async (page) => pages[page] ?? { items: [], sourceReportedCount: 3, hasNext: false },
      (item) => item.id,
    );

    expect(result).toEqual({
      items: [{ id: "a" }, { id: "b" }, { id: "c" }],
      sourceReportedCount: 3,
      observedCount: 3,
      paginationExhausted: true,
    });
  });

  it("rejects repeated records instead of silently accepting a stuck page", async () => {
    await expect(collectNumberedPages(
      1,
      async () => ({ items: [{ id: "a" }], sourceReportedCount: null, hasNext: true }),
      (item) => item.id,
    )).rejects.toThrow("repeated item a");
  });

  it("rejects an empty page that still claims another page", async () => {
    await expect(collectNumberedPages(
      10,
      async () => ({ items: [], sourceReportedCount: null, hasNext: true }),
      (item: { id: string }) => item.id,
    )).rejects.toThrow("did not make progress");
  });

  it("rejects early exhaustion before the reported count", async () => {
    await expect(collectNumberedPages(
      10,
      async () => ({ items: [{ id: "a" }], sourceReportedCount: 2, hasNext: false }),
      (item) => item.id,
    )).rejects.toThrow("ended before the source-reported count");
  });

  it("rejects contradictory hasNext after reaching the reported count", async () => {
    await expect(collectNumberedPages(
      1,
      async () => ({ items: [{ id: "a" }], sourceReportedCount: 1, hasNext: true }),
      (item) => item.id,
    )).rejects.toThrow("claims another page");
  });
});

describe("exhaustive before-cursor pagination", () => {
  it("uses hasBefore as the exhaustive contract and leaves the source count unknown", async () => {
    const calls: Array<{ before: string | undefined; limit: number }> = [];
    const pages = new Map<string | undefined, {
      items: Array<{ id: string }>;
      hasBefore: boolean;
      total: number;
    }>([
      [undefined, { items: [{ id: "c" }, { id: "b" }], hasBefore: true, total: 1 }],
      ["b", { items: [{ id: "a" }], hasBefore: false, total: 1 }],
    ]);

    const result = await collectBeforeCursorPages(
      2,
      async (before, limit) => {
        calls.push({ before, limit });
        const page = pages.get(before);
        if (page === undefined) {
          throw new Error(`Unexpected cursor ${before ?? "initial"}.`);
        }
        return page;
      },
      (item) => item.id,
    );

    expect(calls).toEqual([
      { before: undefined, limit: 2 },
      { before: "b", limit: 2 },
    ]);
    expect(result).toEqual({
      items: [{ id: "c" }, { id: "b" }, { id: "a" }],
      sourceReportedCount: null,
      observedCount: 3,
      paginationExhausted: true,
    });
  });

  it("rejects a repeated submission across cursor pages", async () => {
    await expect(collectBeforeCursorPages(
      1,
      async (before) => before === undefined
        ? { items: [{ id: "a" }], hasBefore: true }
        : { items: [{ id: "a" }], hasBefore: false },
      (item) => item.id,
    )).rejects.toThrow("repeated item a");
  });

  it("rejects an empty page that claims an older page", async () => {
    await expect(collectBeforeCursorPages(
      10,
      async () => ({ items: [], hasBefore: true }),
      (item: { id: string }) => item.id,
    )).rejects.toThrow("did not make progress");
  });

  it("requires an explicit boolean exhaustion signal", async () => {
    const invalidPage = {
      items: [],
      hasBefore: null,
    } as unknown as BeforeCursorPage<{ id: string }>;
    await expect(collectBeforeCursorPages(
      10,
      async () => invalidPage,
      (item: { id: string }) => item.id,
    )).rejects.toThrow("requires a boolean hasBefore");
  });
});
