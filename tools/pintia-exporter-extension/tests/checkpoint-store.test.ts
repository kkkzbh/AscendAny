import { IDBFactory } from "fake-indexeddb";
import { beforeEach, describe, expect, it } from "vitest";
import {
  EXPORT_TASK_FORMAT_VERSION,
  SNAPSHOT_SCHEMA,
  type ExportBlobResourceJournal,
  type ExportDownloadResourceJournal,
  type ExportTask,
} from "../src/domain/types";
import { IndexedDbExportCoordinatorStore } from "../src/platform/checkpoint-store";

const DATABASE_NAME = "ascendany-pintia-exporter-v2";
const TASK_STORE = "tasks";
const COORDINATION_STORE = "coordination";

function openDatabase(version: number, upgrade?: (database: IDBDatabase) => void): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, version);
    request.addEventListener("upgradeneeded", () => upgrade?.(request.result));
    request.addEventListener("success", () => resolve(request.result), { once: true });
    request.addEventListener("error", () => reject(request.error), { once: true });
  });
}

function put(database: IDBDatabase, storeName: string, value: unknown): Promise<void> {
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(storeName, "readwrite");
    transaction.objectStore(storeName).put(value);
    transaction.addEventListener("complete", () => resolve(), { once: true });
    transaction.addEventListener("error", () => reject(transaction.error), { once: true });
    transaction.addEventListener("abort", () => reject(transaction.error), { once: true });
  });
}

function validTask(problemSetId: string, generation = "generation-a"): ExportTask {
  return {
    schema: SNAPSHOT_SCHEMA,
    taskFormatVersion: EXPORT_TASK_FORMAT_VERSION,
    taskId: `${problemSetId}-task`,
    generation,
    problemSetId,
    tabId: 7,
    origin: "https://pintia.cn",
    sourceUrl: `https://pintia.cn/problem-sets/${problemSetId}/problems`,
    originalUrl: `https://pintia.cn/problem-sets/${problemSetId}/problems`,
    status: "running",
    stage: "starting",
    error: null,
    createdAt: "2026-07-11T00:00:00.000Z",
    updatedAt: "2026-07-11T00:00:00.000Z",
    captureAttempt: 0,
    progress: {
      phase: "starting",
      totalSubmissions: 0,
      completedDetails: 0,
      pendingDetails: 0,
      detailPass: 0,
      percent: 4,
    },
    logs: [],
    failures: [],
    budget: { nodes: 0, stringBytes: 0, maximumDepth: 0 },
    parts: {
      problems: null,
      rankings: null,
      submissions: { collection: null, programmingItems: [], submissionDetailsById: {} },
    },
  };
}

async function acquire(store: IndexedDbExportCoordinatorStore): Promise<void> {
  await expect(store.acquire({
    generation: "generation-a",
    taskId: "900-task",
    problemSetId: "900",
    nowMs: 100,
  })).resolves.toMatchObject({ acquired: true });
}

beforeEach(() => {
  Object.defineProperty(globalThis, "indexedDB", {
    configurable: true,
    writable: true,
    value: new IDBFactory(),
  });
});

describe("durable export coordination journal", () => {
  it("atomically replaces every v3 store at the task-format v4 boundary", async () => {
    const legacy = await openDatabase(3, (database) => {
      database.createObjectStore(TASK_STORE, { keyPath: "problemSetId" });
      database.createObjectStore(COORDINATION_STORE, { keyPath: "id" });
    });
    await put(legacy, TASK_STORE, { problemSetId: "legacy" });
    await put(legacy, COORDINATION_STORE, { id: "global", state: "active" });
    legacy.close();

    const store = new IndexedDbExportCoordinatorStore();
    await expect(store.loadTask("legacy")).resolves.toBeNull();
    await expect(store.loadJournal()).resolves.toBeNull();
  });

  it("binds checkpoints and resource transitions to generation, identity, and prior state", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    await acquire(store);
    const task = validTask("900");
    await store.saveTask("generation-a", task);
    await expect(store.loadTask("900")).resolves.toEqual(task);

    const blob: ExportBlobResourceJournal = {
      requestId: "request-a",
      fileName: "request-a.json",
      expectedBytes: 2,
      unsafeUntilMs: 1_000,
      state: "writing",
    };
    await expect(store.claimBlob("generation-a", 110, blob)).resolves.toBe(true);
    await expect(store.transitionBlob(
      "generation-a",
      "request-b",
      ["writing"],
      120,
      { ...blob, state: "creating" },
    )).resolves.toBe(false);
    await expect(store.transitionBlob(
      "generation-a",
      blob.requestId,
      ["writing"],
      120,
      { ...blob, state: "creating" },
    )).resolves.toBe(true);

    const download: ExportDownloadResourceJournal = {
      identity: "download-a",
      filename: "snapshot-download-a.json",
      downloadId: null,
      unsafeUntilMs: 2_000,
      state: "starting",
    };
    await expect(store.claimDownload("generation-a", 130, download)).resolves.toBe(true);
    await expect(store.transitionDownload(
      "generation-b",
      download.identity,
      ["starting"],
      140,
      { ...download, downloadId: 41, state: "in_progress" },
    )).resolves.toBe(false);
    await expect(store.transitionDownload(
      "generation-a",
      download.identity,
      ["complete"],
      140,
      { ...download, downloadId: 41, state: "in_progress" },
    )).resolves.toBe(false);
    await expect(store.transitionDownload(
      "generation-a",
      download.identity,
      ["starting"],
      140,
      { ...download, downloadId: 41, state: "in_progress" },
    )).resolves.toBe(true);
  });

  it("serializes concurrent recovery claims and rejects a stale recovery completion", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    await acquire(store);
    await store.beginCollector("generation-a", {
      operationId: "collector-a",
      collector: "problems",
      startedAtMs: 100,
      absoluteDeadlineAtMs: 500,
      unsafeUntilMs: 1_000,
      state: "running",
      error: null,
    });

    await expect(store.beginRecovery(500, "recovery-a", 1_500)).resolves.toMatchObject({ kind: "blocked" });
    await expect(store.beginRecovery(1_001, "recovery-a", 1_500)).resolves.toMatchObject({ kind: "recover" });
    await expect(store.settleCollector(
      "generation-a",
      "collector-a",
      1_002,
      null,
    )).resolves.toBe(false);
    await expect(store.beginRecovery(1_100, "recovery-b", 2_000)).resolves.toMatchObject({ kind: "blocked" });
    await expect(store.beginRecovery(1_501, "recovery-b", 2_100)).resolves.toMatchObject({ kind: "recover" });
    await expect(store.finishRecovery("generation-a", "recovery-a", 1_600, "stale")).rejects.toThrow(
      "recovering lease state",
    );
    await expect(store.finishRecovery("generation-a", "recovery-b", 1_600, "recovered")).resolves.toBeUndefined();
    await expect(store.loadJournal()).resolves.toMatchObject({
      state: "failed",
      recovery: null,
      resources: { blob: null, download: null },
    });
  });

  it("freezes every old producer transition after recovery claims the generation", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    await acquire(store);
    await store.beginNavigation("generation-a", {
      operationId: "navigation-a",
      tabId: 7,
      targetUrl: "https://pintia.cn/problem-sets/900/rankings",
      recoveryUrl: "https://pintia.cn/problem-sets/900/problems",
      startedAtMs: 100,
      unsafeUntilMs: 1_000,
      state: "running",
      error: null,
    });
    const blob: ExportBlobResourceJournal = {
      requestId: "request-recovery",
      fileName: "request-recovery.json",
      expectedBytes: 2,
      unsafeUntilMs: 1_000,
      state: "writing",
    };
    await store.claimBlob("generation-a", 110, blob);
    await store.transitionBlob(
      "generation-a",
      blob.requestId,
      ["writing"],
      120,
      { ...blob, state: "creating" },
    );
    const download: ExportDownloadResourceJournal = {
      identity: "download-recovery",
      filename: "snapshot-download-recovery.json",
      downloadId: null,
      unsafeUntilMs: 1_000,
      state: "starting",
    };
    await store.claimDownload("generation-a", 130, download);

    await expect(store.beginRecovery(1_001, "recovery-freeze", 2_000)).resolves.toMatchObject({
      kind: "recover",
    });
    await expect(store.settleNavigation(
      "generation-a",
      "navigation-a",
      1_002,
      null,
    )).resolves.toBe(false);
    await expect(store.transitionBlob(
      "generation-a",
      blob.requestId,
      ["creating"],
      1_002,
      { ...blob, state: "live" },
    )).resolves.toBe(false);
    await expect(store.transitionDownload(
      "generation-a",
      download.identity,
      ["starting"],
      1_002,
      { ...download, downloadId: 88, state: "in_progress" },
    )).resolves.toBe(false);
    await expect(store.loadJournal()).resolves.toMatchObject({
      state: "recovering",
      navigation: { operationId: "navigation-a", state: "running" },
      resources: {
        blob: { requestId: "request-recovery", state: "creating" },
        download: { identity: "download-recovery", downloadId: null, state: "starting" },
      },
    });
  });

  it("commits completion and checkpoint deletion atomically so a late failure cannot overwrite it", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    await acquire(store);
    await store.saveTask("generation-a", validTask("900"));
    const download: ExportDownloadResourceJournal = {
      identity: "download-complete",
      filename: "snapshot-download-complete.json",
      downloadId: null,
      unsafeUntilMs: 2_000,
      state: "starting",
    };
    await store.claimDownload("generation-a", 110, download);
    await store.transitionDownload(
      "generation-a",
      download.identity,
      ["starting"],
      120,
      { ...download, downloadId: 91, state: "in_progress" },
    );
    await store.transitionDownload(
      "generation-a",
      download.identity,
      ["in_progress"],
      130,
      { ...download, downloadId: 91, state: "complete" },
    );

    await store.complete("generation-a", "900", 140);
    await expect(store.fail("generation-a", 150, "late failure")).resolves.toBe("completed");
    await expect(store.loadTask("900")).resolves.toBeNull();
    await expect(store.loadJournal()).resolves.toMatchObject({ state: "completed", finalError: null });
  });
});
