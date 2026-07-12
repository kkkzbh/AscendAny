import type { ExhaustiveCollection, JsonObject } from "./types";

const MAX_NUMBERED_PAGES = 10_000;
const MAX_BEFORE_CURSOR_PAGES = 100_000;

export interface NumberedPage<T> {
  items: T[];
  sourceReportedCount: number | null;
  hasNext: boolean | null;
}

export interface BeforeCursorPage<T> {
  items: T[];
  hasBefore: boolean;
}

export async function collectNumberedPages<T>(
  pageSize: number,
  fetchPage: (page: number, limit: number) => Promise<NumberedPage<T>>,
  identity: (item: T) => string,
): Promise<ExhaustiveCollection<T>> {
  if (!Number.isSafeInteger(pageSize) || pageSize <= 0) {
    throw new Error("pageSize must be a positive safe integer.");
  }

  const items: T[] = [];
  const identities = new Set<string>();
  let sourceReportedCount: number | null = null;

  for (let page = 0; page < MAX_NUMBERED_PAGES; page += 1) {
    const result = await fetchPage(page, pageSize);
    if (result.sourceReportedCount !== null) {
      if (
        !Number.isSafeInteger(result.sourceReportedCount) ||
        result.sourceReportedCount < 0 ||
        (sourceReportedCount !== null && sourceReportedCount !== result.sourceReportedCount)
      ) {
        throw new Error("Source-reported pagination count is invalid or changed between pages.");
      }
      sourceReportedCount = result.sourceReportedCount;
    }

    for (const item of result.items) {
      const id = identity(item);
      if (identities.has(id)) {
        throw new Error(`Numbered pagination repeated item ${id}.`);
      }
      identities.add(id);
      items.push(item);
    }

    if (sourceReportedCount !== null && items.length > sourceReportedCount) {
      throw new Error("Numbered pagination exceeded the source-reported count.");
    }
    if (
      result.hasNext === true &&
      sourceReportedCount !== null &&
      items.length === sourceReportedCount
    ) {
      throw new Error("Numbered pagination claims another page after reaching the source-reported count.");
    }

    const exhausted = result.hasNext === false ||
      (sourceReportedCount !== null && items.length === sourceReportedCount) ||
      (result.hasNext === null && sourceReportedCount === null && result.items.length < pageSize);

    if (exhausted) {
      if (sourceReportedCount !== null && items.length !== sourceReportedCount) {
        throw new Error("Numbered pagination ended before the source-reported count was collected.");
      }
      return {
        items,
        sourceReportedCount,
        observedCount: items.length,
        paginationExhausted: true,
      };
    }

    if (result.items.length === 0) {
      throw new Error("Numbered pagination did not make progress before exhaustion.");
    }
  }

  throw new Error(`Numbered pagination exceeded ${MAX_NUMBERED_PAGES} pages.`);
}

export async function collectBeforeCursorPages<T>(
  pageSize: number,
  fetchPage: (before: string | undefined, limit: number) => Promise<BeforeCursorPage<T>>,
  identity: (item: T) => string,
): Promise<ExhaustiveCollection<T>> {
  if (!Number.isSafeInteger(pageSize) || pageSize <= 0) {
    throw new Error("pageSize must be a positive safe integer.");
  }

  const items: T[] = [];
  const identities = new Set<string>();
  let before: string | undefined;

  for (let page = 0; page < MAX_BEFORE_CURSOR_PAGES; page += 1) {
    const result = await fetchPage(before, pageSize);
    if (typeof result.hasBefore !== "boolean") {
      throw new Error("Before-cursor pagination requires a boolean hasBefore value.");
    }

    for (const item of result.items) {
      const itemIdentity = identity(item);
      if (identities.has(itemIdentity)) {
        throw new Error(`Before-cursor pagination repeated item ${itemIdentity}.`);
      }
      identities.add(itemIdentity);
      items.push(item);
    }

    if (!result.hasBefore) {
      return {
        items,
        sourceReportedCount: null,
        observedCount: items.length,
        paginationExhausted: true,
      };
    }
    if (result.items.length === 0) {
      throw new Error("Before-cursor pagination did not make progress before exhaustion.");
    }

    const nextBefore = identity(result.items[result.items.length - 1] as T);
    if (nextBefore.length === 0 || nextBefore === before) {
      throw new Error("Before-cursor pagination did not advance its cursor.");
    }
    before = nextBefore;
  }

  throw new Error(`Before-cursor pagination exceeded ${MAX_BEFORE_CURSOR_PAGES} pages.`);
}

export function jsonObject(value: unknown, field: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${field} must be an object.`);
  }
  return value as JsonObject;
}

export function jsonObjectArray(value: unknown, field: string): JsonObject[] {
  if (!Array.isArray(value)) {
    throw new Error(`${field} must be an array.`);
  }
  return value.map((item, index) => jsonObject(item, `${field}[${index}]`));
}

export function sourceReportedCount(response: JsonObject): number | null {
  if (response.total === undefined || response.total === null) {
    return null;
  }
  if (typeof response.total !== "number" || !Number.isSafeInteger(response.total) || response.total < 0) {
    throw new Error("Pintia response total must be a non-negative safe integer.");
  }
  return response.total;
}

export function hasNextPage(response: JsonObject): boolean | null {
  if (response.hasNext === undefined || response.hasNext === null) {
    return null;
  }
  if (typeof response.hasNext !== "boolean") {
    throw new Error("Pintia response hasNext must be boolean when present.");
  }
  return response.hasNext;
}
