import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import fixture from "./fixtures/sanitized-source-shape.json";
import { DETAIL_BATCH_SIZE } from "../src/domain/limits";
import {
  EXPORT_TASK_FORMAT_VERSION,
  SNAPSHOT_SCHEMA,
  type CollectorRequest,
  type ExportTask,
  type SnapshotSource,
  type SubmissionDetailSource,
} from "../src/domain/types";
import type { ExportCoordinatorRunContext } from "../src/platform/export-coordinator";
import { PintiaCollectorError } from "../src/platform/chrome-export-runtime";

const source = structuredClone(fixture) as unknown as SnapshotSource;
const problemSetId = source.problemSetId;
const sourceUrl = `https://pintia.cn/problem-sets/${problemSetId}/problems/type/7`;
let runExportWithinCoordinator: typeof import("../src/background")["runExportWithinCoordinator"];

function chromeStub(): typeof chrome {
  const event = { addListener: vi.fn(), removeListener: vi.fn() };
  return {
    runtime: {
      id: "synthetic-extension",
      onConnect: event,
    },
    downloads: {
      download: vi.fn(),
      cancel: vi.fn(),
      search: vi.fn(),
      removeFile: vi.fn(),
      erase: vi.fn(),
      onChanged: event,
    },
  } as unknown as typeof chrome;
}

function port(): chrome.runtime.Port {
  return {
    name: "pintia-export-v2",
    postMessage: vi.fn(),
    onDisconnect: { addListener: vi.fn() },
    onMessage: { addListener: vi.fn() },
  } as unknown as chrome.runtime.Port;
}

function storedTask(): ExportTask {
  const task: ExportTask = {
    schema: SNAPSHOT_SCHEMA,
    taskFormatVersion: EXPORT_TASK_FORMAT_VERSION,
    taskId: "old-task",
    generation: "old-generation",
    problemSetId,
    tabId: 7,
    origin: "https://pintia.cn",
    sourceUrl,
    originalUrl: sourceUrl,
    status: "failed",
    stage: "submission-details",
    error: "service worker stopped",
    createdAt: "2026-07-12T00:00:00.000Z",
    updatedAt: "2026-07-12T00:01:00.000Z",
    captureAttempt: 1,
    progress: {
      phase: "submission-details",
      totalSubmissions: 2,
      completedDetails: 1,
      pendingDetails: 1,
      detailPass: 1,
      percent: 50,
    },
    logs: [],
    failures: [],
    budget: { nodes: 1, stringBytes: 1, maximumDepth: 1 },
    parts: {
      problems: structuredClone(source.problems),
      rankings: structuredClone(source.rankings),
      submissions: {
        collection: structuredClone(source.submissions),
        programmingItems: structuredClone(source.submissions.items.filter(
          (item) => item.problemType === "PROGRAMMING",
        )),
        submissionDetailsById: {
          "submission-alpha": structuredClone(source.submissionDetailsById["submission-alpha"] as NonNullable<
            SnapshotSource["submissionDetailsById"][string]
          >),
        },
      },
    },
  };
  return task;
}

function rawProblems(drift = false): unknown {
  return {
    items: structuredClone(source.problems.items).map((item, index) => index === 0 && drift
      ? { ...item, title: `${String(item.title)} drift` }
      : item),
    sourceReportedCount: source.problems.sourceReportedCount,
    observedCount: source.problems.observedCount,
    paginationExhausted: true,
    metadataResponse: {
      problemSet: {
        id: source.problems.metadata.problemSetId,
        name: source.problems.metadata.title,
        startAt: source.problems.metadata.startsAt,
        endAt: source.problems.metadata.endsAt,
      },
    },
  };
}

function rawRankings(): unknown {
  return structuredClone(source.rankings);
}

function rawSubmissions(): unknown {
  return structuredClone(source.submissions);
}

function context(
  task: ExportTask,
  detailCalls: string[][],
  checkpoints: ExportTask[],
  driftFirstFinalProblems: boolean,
): ExportCoordinatorRunContext {
  const controller = new AbortController();
  let problemsCalls = 0;
  return {
    generation: "new-generation",
    taskId: "new-task",
    problemSetId,
    signal: controller.signal,
    loadTask: vi.fn(async () => task),
    checkpoint: vi.fn(async (value) => { checkpoints.push(structuredClone(value)); }),
    getTab: vi.fn(async () => ({ id: 7, url: sourceUrl } as chrome.tabs.Tab)),
    navigateTab: vi.fn(async () => undefined),
    restoreTab: vi.fn(async () => undefined),
    collect: vi.fn(async (_tabId: number, request: CollectorRequest) => {
      if (request.collector === "problems") {
        problemsCalls += 1;
        return rawProblems(driftFirstFinalProblems && problemsCalls === 1);
      }
      if (request.collector === "rankings") {
        return rawRankings();
      }
      if (request.collector === "submissions") {
        return rawSubmissions();
      }
      const ids = request.submissionIds ?? [];
      detailCalls.push([...ids]);
      return {
        items: ids.map((submissionId) => ({
          submissionId,
          detail: structuredClone(source.submissionDetailsById[submissionId]),
        })),
      };
    }),
    deliver: vi.fn(async () => undefined),
  };
}

function multiBatchTask(count: number): {
  task: ExportTask;
  detailsById: Record<string, SubmissionDetailSource>;
} {
  const task = storedTask();
  const templateItem = source.submissions.items[0];
  const templateDetail = source.submissionDetailsById["submission-alpha"];
  if (templateItem === undefined || templateDetail === undefined) {
    throw new Error("Synthetic source fixture is missing its submission template.");
  }
  const programmingItems = Array.from({ length: count }, (_, index) => ({
    ...structuredClone(templateItem),
    id: `submission-${index.toString().padStart(2, "0")}`,
  }));
  const detailsById = Object.fromEntries(programmingItems.map((item) => {
    const submissionId = String(item.id);
    return [submissionId, {
      ...structuredClone(templateDetail),
      submissionId,
      code: `SYNTHETIC_CODE_${submissionId}`,
    }];
  })) as Record<string, SubmissionDetailSource>;
  task.parts.submissions.collection = {
    ...structuredClone(source.submissions),
    items: structuredClone(programmingItems),
    sourceReportedCount: count,
    observedCount: count,
  };
  task.parts.submissions.programmingItems = structuredClone(programmingItems);
  task.parts.submissions.submissionDetailsById = {};
  task.progress.totalSubmissions = count;
  task.progress.completedDetails = 0;
  task.progress.pendingDetails = count;
  task.failures = [];
  return { task, detailsById };
}

function rateLimitedMultiBatchContext(
  task: ExportTask,
  detailsById: Readonly<Record<string, SubmissionDetailSource>>,
  detailCalls: string[][],
  checkpoints: ExportTask[],
  rateLimitedDetailCall = 2,
): ExportCoordinatorRunContext {
  const controller = new AbortController();
  let detailCall = 0;
  return {
    generation: "new-generation",
    taskId: "new-task",
    problemSetId,
    signal: controller.signal,
    loadTask: vi.fn(async () => task),
    checkpoint: vi.fn(async (value) => { checkpoints.push(structuredClone(value)); }),
    getTab: vi.fn(async () => ({ id: 7, url: sourceUrl } as chrome.tabs.Tab)),
    navigateTab: vi.fn(async () => undefined),
    restoreTab: vi.fn(async () => undefined),
    collect: vi.fn(async (_tabId: number, request: CollectorRequest) => {
      if (request.collector === "problems") {
        return rawProblems();
      }
      if (request.collector === "rankings") {
        return rawRankings();
      }
      if (request.collector === "submissions") {
        return structuredClone(task.parts.submissions.collection);
      }
      const ids = request.submissionIds ?? [];
      detailCalls.push([...ids]);
      detailCall += 1;
      if (detailCall === rateLimitedDetailCall) {
        throw new PintiaCollectorError("submission-details", {
          kind: "rate_limited",
          status: 429,
          message: "Pintia API request was rate limited (HTTP 429).",
        });
      }
      return {
        items: ids.map((submissionId) => {
          const detail = detailsById[submissionId];
          if (detail === undefined) {
            throw new Error(`Synthetic detail ${submissionId} is missing.`);
          }
          return { submissionId, detail: structuredClone(detail) };
        }),
      };
    }),
    deliver: vi.fn(async () => undefined),
  };
}

async function settleWithFakeDelays<T>(promise: Promise<T>): Promise<T> {
  let settled = false;
  void promise.then(
    () => { settled = true; },
    () => { settled = true; },
  );
  const deadline = performance.now() + 3_000;
  while (!settled && performance.now() < deadline) {
    await vi.runAllTimersAsync();
    await new Promise<void>((resolve) => setImmediate(resolve));
  }
  if (!settled) {
    throw new Error("Background workflow did not settle after all deterministic delays advanced.");
  }
  return promise;
}

beforeAll(async () => {
  vi.stubGlobal("chrome", chromeStub());
  ({ runExportWithinCoordinator } = await import("../src/background"));
});

afterAll(() => vi.unstubAllGlobals());
afterEach(() => vi.useRealTimers());

describe("durable background capture resume", () => {
  it("resumes a mid-detail checkpoint, fetches only pending detail, then runs full digest verification", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const task = storedTask();
    const detailCalls: string[][] = [];
    const checkpoints: ExportTask[] = [];
    const runContext = context(task, detailCalls, checkpoints, false);

    const running = runExportWithinCoordinator(port(), {
      type: "RETRY_EXPORT",
      tabId: 7,
      problemSetId,
      sourceUrl,
    }, runContext);
    const completed = settleWithFakeDelays(running);
    await expect(completed).resolves.toMatchObject({
      completeness: { submissions: { exportedCount: 2 } },
    });

    expect(detailCalls[0]).toEqual(["submission-beta"]);
    expect(detailCalls.at(-1)).toEqual(["submission-alpha", "submission-beta"]);
    expect(task.captureAttempt).toBe(1);
    expect(runContext.deliver).toHaveBeenCalledOnce();
    expect(checkpoints.some((checkpoint) => checkpoint.progress.completedDetails === 1)).toBe(true);
    expect(checkpoints.some((checkpoint) => checkpoint.budget.nodes > 1)).toBe(true);
  });

  it("clears every captured part after drift and starts a fresh attempt", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const task = storedTask();
    const detailCalls: string[][] = [];
    const checkpoints: ExportTask[] = [];
    const runContext = context(task, detailCalls, checkpoints, true);

    const running = runExportWithinCoordinator(port(), {
      type: "RETRY_EXPORT",
      tabId: 7,
      problemSetId,
      sourceUrl,
    }, runContext);
    await expect(settleWithFakeDelays(running)).resolves.toBeDefined();

    expect(task.captureAttempt).toBe(2);
    expect(checkpoints).toContainEqual(expect.objectContaining({
      captureAttempt: 2,
      parts: {
        problems: null,
        rankings: null,
        submissions: {
          collection: null,
          programmingItems: [],
          submissionDetailsById: {},
        },
      },
    }));
    expect(detailCalls).toContainEqual(["submission-alpha", "submission-beta"]);
    expect(runContext.deliver).toHaveBeenCalledOnce();
  });

  it("stops a rate-limited pass, backs off once, and retries every uncommitted id in bounded batches", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const timeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const { task, detailsById } = multiBatchTask(DETAIL_BATCH_SIZE * 2 + 1);
    const detailCalls: string[][] = [];
    const checkpoints: ExportTask[] = [];
    const runContext = rateLimitedMultiBatchContext(
      task,
      detailsById,
      detailCalls,
      checkpoints,
    );

    const running = runExportWithinCoordinator(port(), {
      type: "RETRY_EXPORT",
      tabId: 7,
      problemSetId,
      sourceUrl,
    }, runContext);
    await expect(settleWithFakeDelays(running)).resolves.toMatchObject({
      completeness: { submissions: { exportedCount: DETAIL_BATCH_SIZE * 2 + 1 } },
    });

    const ids = Object.keys(detailsById);
    expect(detailCalls).toEqual([
      ids.slice(0, DETAIL_BATCH_SIZE),
      ids.slice(DETAIL_BATCH_SIZE, DETAIL_BATCH_SIZE * 2),
      ids.slice(DETAIL_BATCH_SIZE, DETAIL_BATCH_SIZE * 2),
      ids.slice(DETAIL_BATCH_SIZE * 2),
      ids.slice(0, DETAIL_BATCH_SIZE),
      ids.slice(DETAIL_BATCH_SIZE, DETAIL_BATCH_SIZE * 2),
      ids.slice(DETAIL_BATCH_SIZE * 2),
    ]);
    expect(detailCalls.every((batch) => batch.length <= DETAIL_BATCH_SIZE)).toBe(true);
    expect(detailCalls[2]?.[0]).toBe(ids[DETAIL_BATCH_SIZE]);
    expect(detailCalls.flat().filter((id) => id === ids.at(-1))).toHaveLength(2);
    expect(timeoutSpy.mock.calls.filter((call) => call[1] === 120_000)).toHaveLength(1);
    expect(task.failures).toEqual([]);
    expect(Object.keys(task.parts.submissions.submissionDetailsById)).toEqual(ids);
    expect(checkpoints.some((checkpoint) => checkpoint.progress.detailPass === 2)).toBe(true);
    timeoutSpy.mockRestore();
  });

  it("retains the current and later batches when digest verification is rate-limited", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const timeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const { task, detailsById } = multiBatchTask(DETAIL_BATCH_SIZE * 2 + 1);
    const detailCalls: string[][] = [];
    const checkpoints: ExportTask[] = [];
    const runContext = rateLimitedMultiBatchContext(
      task,
      detailsById,
      detailCalls,
      checkpoints,
      5,
    );

    const running = runExportWithinCoordinator(port(), {
      type: "RETRY_EXPORT",
      tabId: 7,
      problemSetId,
      sourceUrl,
    }, runContext);
    await expect(settleWithFakeDelays(running)).resolves.toBeDefined();

    const ids = Object.keys(detailsById);
    expect(detailCalls).toEqual([
      ids.slice(0, DETAIL_BATCH_SIZE),
      ids.slice(DETAIL_BATCH_SIZE, DETAIL_BATCH_SIZE * 2),
      ids.slice(DETAIL_BATCH_SIZE * 2),
      ids.slice(0, DETAIL_BATCH_SIZE),
      ids.slice(DETAIL_BATCH_SIZE, DETAIL_BATCH_SIZE * 2),
      ids.slice(DETAIL_BATCH_SIZE, DETAIL_BATCH_SIZE * 2),
      ids.slice(DETAIL_BATCH_SIZE * 2),
    ]);
    expect(detailCalls.every((batch) => batch.length <= DETAIL_BATCH_SIZE)).toBe(true);
    expect(timeoutSpy.mock.calls.filter((call) => call[1] === 120_000)).toHaveLength(1);
    expect(runContext.deliver).toHaveBeenCalledOnce();
    timeoutSpy.mockRestore();
  });
});
