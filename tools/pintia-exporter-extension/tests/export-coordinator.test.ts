import { IDBFactory } from "fake-indexeddb";
import { beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "./fixtures/sanitized-source-shape.json";
import { buildSnapshot } from "../src/domain/normalize";
import type {
  CollectorRequest,
  ExportCoordinationJournal,
  PintiaSnapshotV2,
  SnapshotSource,
} from "../src/domain/types";
import { IndexedDbExportCoordinatorStore } from "../src/platform/checkpoint-store";
import type { ExportCoordinatorRuntime } from "../src/platform/chrome-export-runtime";
import {
  downloadAndWaitForCompletion,
  type DownloadChangedEvent,
  type DownloadLifecycle,
  type DownloadsApi,
} from "../src/platform/download";
import {
  ExportCoordinator,
  type ExportCoordinatorClock,
} from "../src/platform/export-coordinator";
import { OperationDeadlineError } from "../src/platform/deadline";
import type { PintiaNavigationTarget } from "../src/platform/navigation";
import type { SnapshotBlobHandle } from "../src/platform/snapshot-blob-store";

const UUIDS = [
  "00000000-0000-4000-8000-000000000001",
  "00000000-0000-4000-8000-000000000002",
  "00000000-0000-4000-8000-000000000003",
  "00000000-0000-4000-8000-000000000004",
  "00000000-0000-4000-8000-000000000005",
  "00000000-0000-4000-8000-000000000006",
];

function deferred<T>(): {
  promise: Promise<T>;
  resolve(value: T): void;
  reject(error: unknown): void;
} {
  let resolvePromise: ((value: T) => void) | undefined;
  let rejectPromise: ((error: unknown) => void) | undefined;
  const promise = new Promise<T>((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  return {
    promise,
    resolve: (value) => resolvePromise?.(value),
    reject: (error) => rejectPromise?.(error),
  };
}

class ChangedEvent implements DownloadChangedEvent {
  readonly listeners = new Set<(delta: chrome.downloads.DownloadDelta) => void>();
  addListener(listener: (delta: chrome.downloads.DownloadDelta) => void): void {
    this.listeners.add(listener);
  }
  removeListener(listener: (delta: chrome.downloads.DownloadDelta) => void): void {
    this.listeners.delete(listener);
  }
}

class MutableClock implements ExportCoordinatorClock {
  nowMs = 100;
  private uuidIndex = 0;

  now(): number {
    return this.nowMs;
  }

  randomUUID(): string {
    const value = UUIDS[this.uuidIndex];
    this.uuidIndex += 1;
    if (value === undefined) {
      throw new Error("Synthetic UUID sequence exhausted.");
    }
    return value;
  }

  deadline<T>(promise: Promise<T>): Promise<T> {
    return promise;
  }
}

class FakeRuntime implements ExportCoordinatorRuntime {
  readonly blob = deferred<SnapshotBlobHandle>();
  readonly navigation = deferred<void>();
  readonly collector = deferred<unknown>();
  readonly downloadStart = deferred<number>();
  readonly revokeBlob = vi.fn(async () => undefined);
  readonly cancel = vi.fn(async () => undefined);
  readonly recoverResources = vi.fn(async (_journal: ExportCoordinationJournal) => undefined);
  readonly changed = new ChangedEvent();
  private searchCount = 0;
  readonly downloads: DownloadsApi = {
    download: vi.fn(() => this.downloadStart.promise),
    cancel: this.cancel,
    search: vi.fn(async (query) => typeof query.id === "number" ? [{
      id: query.id,
      filename: `/tmp/snapshot-${UUIDS[3]}.json`,
      state: this.searchCount++ === 0 ? "in_progress" : "interrupted",
      exists: this.searchCount === 1,
    } as chrome.downloads.DownloadItem] : []),
    removeFile: vi.fn(async () => undefined),
    erase: vi.fn(async (query) => typeof query.id === "number" ? [query.id] : []),
    onChanged: this.changed,
  };
  blobImmediate = false;

  getTab = vi.fn(async (tabId: number): Promise<chrome.tabs.Tab> => ({
    id: tabId,
    url: "https://pintia.cn/problem-sets/900/problems",
  } as chrome.tabs.Tab));

  navigateTab(
    _tabId: number,
    _target: PintiaNavigationTarget,
  ): Promise<void> {
    return this.navigation.promise;
  }

  restoreTab = vi.fn(async () => undefined);

  executeCollector(_tabId: number, _request: CollectorRequest): Promise<unknown> {
    return this.collector.promise;
  }

  createBlob(
    requestId: string,
    _json: string,
    expectedBytes: number,
    _signal: AbortSignal,
  ): Promise<SnapshotBlobHandle> {
    if (this.blobImmediate) {
      return Promise.resolve({
        requestId,
        fileName: `${requestId}.json`,
        url: "blob:synthetic",
        size: expectedBytes,
      });
    }
    return this.blob.promise;
  }

  download(
    identity: string,
    options: chrome.downloads.DownloadOptions,
    signal: AbortSignal,
    lifecycle: DownloadLifecycle,
  ): Promise<number> {
    return downloadAndWaitForCompletion(this.downloads, options, identity, signal, lifecycle);
  }
}

async function snapshot(): Promise<PintiaSnapshotV2> {
  return buildSnapshot(
    structuredClone(fixture) as unknown as SnapshotSource,
    "2026-07-12T00:00:00.000Z",
  );
}

async function waitForJournal(
  store: IndexedDbExportCoordinatorStore,
  predicate: (journal: ExportCoordinationJournal) => boolean,
): Promise<ExportCoordinationJournal> {
  let current: ExportCoordinationJournal | null = null;
  await vi.waitFor(async () => {
    current = await store.loadJournal();
    expect(current).not.toBeNull();
    expect(predicate(current as ExportCoordinationJournal)).toBe(true);
  });
  if (current === null) {
    throw new Error("Expected a persistent export journal.");
  }
  return current;
}

beforeEach(() => {
  vi.useRealTimers();
  Object.defineProperty(globalThis, "indexedDB", {
    configurable: true,
    writable: true,
    value: new IDBFactory(),
  });
});

describe("persistent ExportCoordinator late-result ownership", () => {
  it("revokes a late Blob after recovery finished and leaves the new generation unchanged", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    const clock = new MutableClock();
    const runtime = new FakeRuntime();
    const coordinator = new ExportCoordinator({ store, clock, runtime });
    const delivery = coordinator.run({
      problemSetId: "900",
      execute: async (context) => context.deliver(await snapshot(), "snapshot.json"),
    });
    const creating = await waitForJournal(
      store,
      (journal) => journal.resources.blob?.state === "creating",
    );
    const oldGeneration = creating.generation;
    clock.nowMs = (creating.resources.blob?.unsafeUntilMs ?? 0) + 1;
    await expect(store.beginRecovery(clock.nowMs, "recovery-late-blob", clock.nowMs + 1_000)).resolves.toMatchObject({
      kind: "recover",
    });
    await store.finishRecovery(oldGeneration, "recovery-late-blob", clock.nowMs, "recovered");
    await expect(store.acquire({
      generation: "new-generation",
      taskId: "new-task",
      problemSetId: "901",
      nowMs: clock.nowMs + 1,
    })).resolves.toMatchObject({ acquired: true });

    const requestId = creating.resources.blob?.requestId as string;
    const handle = {
      requestId,
      fileName: `${requestId}.json`,
      url: "blob:late",
      size: creating.resources.blob?.expectedBytes as number,
    };
    runtime.blob.resolve(handle);
    await expect(delivery).rejects.toThrow();
    await vi.waitFor(() => expect(runtime.revokeBlob).toHaveBeenCalledWith(handle));
    await expect(store.loadJournal()).resolves.toMatchObject({
      generation: "new-generation",
      taskId: "new-task",
      resources: { blob: null, download: null },
    });
  });

  it("discards an id that appears after recovery searched the frozen starting journal", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    const clock = new MutableClock();
    const runtime = new FakeRuntime();
    runtime.blobImmediate = true;
    const coordinator = new ExportCoordinator({ store, clock, runtime });
    const delivery = coordinator.run({
      problemSetId: "900",
      execute: async (context) => context.deliver(await snapshot(), "snapshot.json"),
    });
    const starting = await waitForJournal(
      store,
      (journal) => journal.resources.download?.state === "starting",
    );
    expect(coordinator.ownsLiveRun("900")).toBe(true);
    expect(coordinator.ownsLiveRun("901")).toBe(false);
    const oldGeneration = starting.generation;
    const unsafe = Math.max(
      starting.resources.blob?.unsafeUntilMs ?? 0,
      starting.resources.download?.unsafeUntilMs ?? 0,
    );
    clock.nowMs = unsafe + 1;
    const recoverySearchFinished = deferred<void>();
    const allowRecoveryFinish = deferred<void>();
    const finishRecoveryEntered = deferred<void>();
    const allowPersistentFinish = deferred<void>();
    const realFinishRecovery = store.finishRecovery.bind(store);
    vi.spyOn(store, "finishRecovery").mockImplementation(async (
      generation,
      recoveryId,
      finishedAtMs,
      reason,
    ) => {
      finishRecoveryEntered.resolve();
      await allowPersistentFinish.promise;
      return realFinishRecovery(generation, recoveryId, finishedAtMs, reason);
    });
    runtime.recoverResources.mockImplementationOnce(async (journal) => {
      const identity = journal.resources.download?.identity;
      if (identity === undefined) {
        throw new Error("Expected a starting download recovery journal.");
      }
      await expect(runtime.downloads.search({ query: [identity] })).resolves.toEqual([]);
      recoverySearchFinished.resolve();
      await allowRecoveryFinish.promise;
    });
    const recoveryCoordinator = new ExportCoordinator({ store, clock, runtime });
    const recovery = recoveryCoordinator.recover();
    await recoverySearchFinished.promise;
    expect(recoveryCoordinator.ownsLiveRun("900")).toBe(true);
    expect(recoveryCoordinator.ownsLiveRun("901")).toBe(false);
    await expect(store.loadJournal()).resolves.toMatchObject({
      generation: oldGeneration,
      state: "recovering",
      resources: { download: { state: "starting", downloadId: null } },
    });

    runtime.downloadStart.resolve(88);
    await expect(delivery).rejects.toThrow();
    await vi.waitFor(() => expect(runtime.cancel).toHaveBeenCalledWith(88));
    allowRecoveryFinish.resolve();
    await finishRecoveryEntered.promise;
    expect(recoveryCoordinator.ownsLiveRun("900")).toBe(true);
    allowPersistentFinish.resolve();
    await recovery;
    expect(recoveryCoordinator.ownsLiveRun("900")).toBe(false);
    await expect(store.acquire({
      generation: "new-generation",
      taskId: "new-task",
      problemSetId: "901",
      nowMs: clock.nowMs + 1,
    })).resolves.toMatchObject({ acquired: true });
    await expect(store.loadJournal()).resolves.toMatchObject({
      generation: "new-generation",
      taskId: "new-task",
      resources: { blob: null, download: null },
    });
  });

  it("keeps a timed-out pending navigation journaled until the raw Chrome operation settles", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    let uuidIndex = 0;
    let nowMs = 0;
    const wholeDeadline = deferred<never>();
    const clock: ExportCoordinatorClock = {
      now: () => nowMs,
      randomUUID: () => UUIDS[uuidIndex++] as string,
      deadline: (promise, milliseconds, _operation) => milliseconds === 100
        ? Promise.race([promise, wholeDeadline.promise])
        : promise,
    };
    const runtime = new FakeRuntime();
    const coordinator = new ExportCoordinator({ store, clock, runtime });
    const running = coordinator.run({
      problemSetId: "900",
      timeoutMs: 100,
      execute: (context) => context.navigateTab(
        7,
        { requestedUrl: "https://pintia.cn/problem-sets/900/rankings", accepts: () => true },
        "https://pintia.cn/problem-sets/900/problems",
      ),
    });
    await waitForJournal(store, (journal) => journal.navigation?.state === "running");
    const rejected = expect(running).rejects.toThrow("Export 900 exceeded its 100ms deadline");
    nowMs = 100;
    wholeDeadline.reject(new OperationDeadlineError("Export 900", 100));
    await rejected;
    await expect(store.loadJournal()).resolves.toMatchObject({
      state: "active",
      navigation: { state: "running" },
    });
    await expect(store.acquire({
      generation: "blocked-generation",
      taskId: "blocked-task",
      problemSetId: "901",
      nowMs,
    })).resolves.toMatchObject({ acquired: false });

    runtime.navigation.resolve();
    await waitForJournal(store, (journal) => journal.navigation?.state === "settled");
  });

  it("keeps a timed-out MAIN collector lease until its exact late operation settles", async () => {
    const store = new IndexedDbExportCoordinatorStore();
    let uuidIndex = 0;
    let nowMs = 0;
    const wholeDeadline = deferred<never>();
    const clock: ExportCoordinatorClock = {
      now: () => nowMs,
      randomUUID: () => UUIDS[uuidIndex++] as string,
      deadline: (promise, milliseconds, _operation) => milliseconds === 100
        ? Promise.race([promise, wholeDeadline.promise])
        : promise,
    };
    const runtime = new FakeRuntime();
    const coordinator = new ExportCoordinator({ store, clock, runtime });
    const running = coordinator.run({
      problemSetId: "900",
      timeoutMs: 100,
      execute: (context) => context.collect(7, {
        type: "ASCENDANY_COLLECT_PINTIA_ROUTE_V2",
        problemSetId: "900",
        collector: "problems",
        limits: {
          maxProblems: 1,
          maxParticipants: 1,
          maxProblemResultsPerRanking: 1,
          maxSubmissions: 1,
          maxCaseResultsPerSubmission: 1,
          maxDetailBatchSize: 1,
          maxCodeBytes: 1,
          maxStringBytes: 1,
          maxTotalStringBytes: 1,
          maxTotalNodes: 1,
          maxJsonDepth: 1,
          apiCallTimeoutMs: 1,
          collectionTimeoutMs: 1,
          detailBatchTimeoutMs: 1,
          detailBatchConcurrency: 1,
        },
      }),
    });
    await waitForJournal(store, (journal) => journal.collector?.state === "running");
    const rejected = expect(running).rejects.toThrow("Export 900 exceeded its 100ms deadline");
    nowMs = 100;
    wholeDeadline.reject(new OperationDeadlineError("Export 900", 100));
    await rejected;
    await expect(store.acquire({
      generation: "blocked-generation",
      taskId: "blocked-task",
      problemSetId: "901",
      nowMs,
    })).resolves.toMatchObject({ acquired: false });

    runtime.collector.resolve({ items: [] });
    await waitForJournal(store, (journal) => journal.collector?.state === "settled");
  });
});
