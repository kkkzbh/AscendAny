import {
  CHECKPOINT_OPERATION_TIMEOUT_MS,
  COLLECTION_TIMEOUT_MS,
  DETAIL_BATCH_TIMEOUT_MS,
  DOWNLOAD_UNSAFE_TIMEOUT_MS,
  EXECUTE_SCRIPT_GRACE_MS,
  EXPORT_RECOVERY_TIMEOUT_MS,
  NAVIGATION_UNSAFE_TIMEOUT_MS,
  SNAPSHOT_BLOB_CREATE_TIMEOUT_MS,
  WHOLE_EXPORT_TIMEOUT_MS,
} from "../domain/limits";
import type {
  CollectorRequest,
  ExportBlobResourceJournal,
  ExportCollectorOperationJournal,
  ExportCoordinationJournal,
  ExportDownloadResourceJournal,
  ExportNavigationOperationJournal,
  ExportTask,
  PintiaSnapshotV2,
} from "../domain/types";
import { validateSnapshot } from "../domain/normalize";
import { validateSerializedSnapshotBytes } from "../domain/operational-preflight";
import type {
  ExportCoordinatorStore,
  FailureCommitResult,
} from "./checkpoint-store";
import { OperationDeadlineError, withDeadline } from "./deadline";
import type { ExportCoordinatorRuntime } from "./chrome-export-runtime";
import type { PintiaNavigationTarget } from "./navigation";
import { exactUrlNavigationTarget } from "./navigation";
import { snapshotFileName } from "./snapshot-opfs";

export interface ExportCoordinatorClock {
  now(): number;
  randomUUID(): string;
  deadline<T>(promise: Promise<T>, milliseconds: number, operation: string): Promise<T>;
}

export interface ExportCoordinatorDependencies {
  store: ExportCoordinatorStore;
  clock: ExportCoordinatorClock;
  runtime: ExportCoordinatorRuntime;
}

export interface ExportCoordinatorRunContext {
  readonly generation: string;
  readonly taskId: string;
  readonly problemSetId: string;
  readonly signal: AbortSignal;
  loadTask(problemSetId: string): Promise<ExportTask | null>;
  checkpoint(task: ExportTask): Promise<void>;
  getTab(tabId: number): Promise<chrome.tabs.Tab>;
  navigateTab(tabId: number, target: PintiaNavigationTarget, recoveryUrl: string): Promise<void>;
  restoreTab(tabId: number, url: string): Promise<void>;
  collect(tabId: number, request: CollectorRequest): Promise<unknown>;
  deliver(snapshot: PintiaSnapshotV2, filename: string): Promise<void>;
}

export interface ExportCoordinatorRun<T> {
  problemSetId: string;
  timeoutMs?: number;
  execute(context: ExportCoordinatorRunContext): Promise<T>;
}

interface CollectorOutcome {
  accepted: boolean;
  result?: unknown;
  error?: unknown;
}

export class PersistentExportLeaseError extends Error {
  constructor(readonly journal: ExportCoordinationJournal) {
    const waiting = journal.collector?.state === "running"
      ? ` Collector safety window ends at ${new Date(journal.collector.unsafeUntilMs).toISOString()}.`
      : " Recovery cleanup is still required.";
    super(`Problem set ${journal.problemSetId} owns the persistent export lease.${waiting}`);
    this.name = "PersistentExportLeaseError";
  }
}

export class UnsafeCollectorOperationError extends Error {
  constructor(readonly operation: ExportCollectorOperationJournal) {
    super(
      `Collector ${operation.collector} did not settle before its persistent safety window; ` +
      "the export lease remains owned for recovery.",
    );
    this.name = "UnsafeCollectorOperationError";
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function positiveSafe(value: number, field: string): number {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`${field} must be a positive safe integer.`);
  }
  return value;
}

function safeAdd(left: number, right: number, field: string): number {
  const result = left + right;
  if (!Number.isSafeInteger(result) || result < 0) {
    throw new Error(`${field} cannot be represented as a safe non-negative integer.`);
  }
  return result;
}

export const systemExportCoordinatorClock: ExportCoordinatorClock = {
  now: () => Date.now(),
  randomUUID: () => crypto.randomUUID(),
  deadline: (promise, milliseconds, operation) => withDeadline(promise, milliseconds, operation),
};

export class ExportCoordinator {
  private cachedJournal: ExportCoordinationJournal | null = null;
  private runningRecoveryId: string | null = null;
  private runningGeneration: string | null = null;

  constructor(private readonly dependencies: ExportCoordinatorDependencies) {}

  snapshot(): ExportCoordinationJournal | null {
    return this.cachedJournal === null ? null : structuredClone(this.cachedJournal);
  }

  ownsLiveRun(problemSetId: string): boolean {
    const journal = this.cachedJournal;
    return journal?.problemSetId === problemSetId && (
      (this.runningGeneration !== null && journal.generation === this.runningGeneration) ||
      (this.runningRecoveryId !== null && journal.recovery?.recoveryId === this.runningRecoveryId)
    );
  }

  async refresh(): Promise<ExportCoordinationJournal | null> {
    this.cachedJournal = await this.dependencies.store.loadJournal();
    return this.snapshot();
  }

  loadTask(problemSetId: string): Promise<ExportTask | null> {
    return this.dependencies.store.loadTask(problemSetId);
  }

  async recover(): Promise<void> {
    if (this.runningGeneration !== null || this.runningRecoveryId !== null) {
      const journal = await this.refresh();
      if (journal === null) {
        throw new Error("In-memory export ownership has no persistent coordination journal.");
      }
      throw new PersistentExportLeaseError(journal);
    }
    const nowMs = this.dependencies.clock.now();
    const recoveryId = this.dependencies.clock.randomUUID();
    const recoveryUnsafeUntilMs = safeAdd(
      nowMs,
      EXPORT_RECOVERY_TIMEOUT_MS,
      "Export recovery safety window",
    );
    const decision = await this.dependencies.store.beginRecovery(
      nowMs,
      recoveryId,
      recoveryUnsafeUntilMs,
    );
    if (decision.kind === "none") {
      await this.refresh();
      return;
    }
    this.cachedJournal = decision.journal;
    if (decision.kind === "blocked") {
      throw new PersistentExportLeaseError(decision.journal);
    }
    this.runningRecoveryId = recoveryId;
    try {
      await this.dependencies.clock.deadline(
        this.dependencies.runtime.recoverResources(decision.journal),
        EXPORT_RECOVERY_TIMEOUT_MS,
        `Export recovery ${recoveryId}`,
      );
      await this.dependencies.store.finishRecovery(
        decision.journal.generation,
        recoveryId,
        this.dependencies.clock.now(),
        "Interrupted export recovered before a new generation started.",
      );
      await this.refresh();
    } catch (error: unknown) {
      await this.refresh();
      throw new Error(`Persistent export recovery failed: ${errorMessage(error)}`);
    } finally {
      this.runningRecoveryId = null;
    }
  }

  async run<T>(run: ExportCoordinatorRun<T>): Promise<T> {
    positiveSafe(run.timeoutMs ?? WHOLE_EXPORT_TIMEOUT_MS, "Whole export timeout");
    await this.recover();
    const generation = this.dependencies.clock.randomUUID();
    const acquiredAtMs = this.dependencies.clock.now();
    const taskId = `${run.problemSetId}-${acquiredAtMs}-${generation}`;
    const claim = await this.dependencies.store.acquire({
      generation,
      taskId,
      problemSetId: run.problemSetId,
      nowMs: acquiredAtMs,
    });
    this.cachedJournal = claim.journal;
    if (!claim.acquired) {
      throw new PersistentExportLeaseError(claim.journal);
    }
    this.runningGeneration = generation;

    const controller = new AbortController();
    const context: ExportCoordinatorRunContext = {
      generation,
      taskId,
      problemSetId: run.problemSetId,
      signal: controller.signal,
      loadTask: async (problemSetId) => {
        controller.signal.throwIfAborted();
        return this.dependencies.store.loadTask(problemSetId);
      },
      checkpoint: async (task) => {
        controller.signal.throwIfAborted();
        await this.dependencies.store.saveTask(generation, task);
      },
      getTab: async (tabId) => {
        controller.signal.throwIfAborted();
        return this.dependencies.runtime.getTab(tabId);
      },
      navigateTab: (tabId, target, recoveryUrl) => this.navigate(
        generation,
        controller.signal,
        tabId,
        target,
        recoveryUrl,
      ),
      restoreTab: async (tabId, url) => {
        controller.signal.throwIfAborted();
        const tab = await this.dependencies.runtime.getTab(tabId);
        if (tab.url !== url) {
          await this.navigate(
            generation,
            controller.signal,
            tabId,
            exactUrlNavigationTarget(url),
            url,
          );
        }
      },
      collect: (tabId, request) => this.collect(generation, controller.signal, tabId, request),
      deliver: (snapshot, filename) => this.deliver(
        generation,
        controller.signal,
        snapshot,
        filename,
      ),
    };

    const execution = Promise.resolve().then(() => run.execute(context));
    const timeoutMs = run.timeoutMs ?? WHOLE_EXPORT_TIMEOUT_MS;
    const outcome = execution.then(
      (value) => ({ kind: "value" as const, value }),
      (error: unknown) => ({ kind: "error" as const, error }),
    );
    try {
      let selected: Awaited<typeof outcome>;
      try {
        selected = await this.dependencies.clock.deadline(
          outcome,
          timeoutMs,
          `Export ${run.problemSetId}`,
        );
      } catch (error: unknown) {
        const timeout = error instanceof OperationDeadlineError
          ? error
          : new OperationDeadlineError(`Export ${run.problemSetId}`, timeoutMs);
        controller.abort(timeout);
        await this.commitFailure(generation, timeout);
        throw timeout;
      }
      if (selected.kind === "error") {
        controller.abort(selected.error);
        await this.commitFailure(generation, selected.error);
        throw selected.error;
      }
      controller.signal.throwIfAborted();
      await this.dependencies.store.complete(generation, run.problemSetId, this.dependencies.clock.now());
      await this.refresh();
      controller.abort(new DOMException("Export completed.", "AbortError"));
      return selected.value;
    } finally {
      this.runningGeneration = null;
    }
  }

  private async commitFailure(
    generation: string,
    error: unknown,
  ): Promise<FailureCommitResult> {
    const result = await this.dependencies.store.fail(
      generation,
      this.dependencies.clock.now(),
      errorMessage(error),
    );
    await this.refresh();
    return result;
  }

  private async collect(
    generation: string,
    signal: AbortSignal,
    tabId: number,
    request: CollectorRequest,
  ): Promise<unknown> {
    signal.throwIfAborted();
    const startedAtMs = this.dependencies.clock.now();
    const collectorTimeout = request.collector === "submission-details"
      ? DETAIL_BATCH_TIMEOUT_MS
      : COLLECTION_TIMEOUT_MS;
    const absoluteDeadlineAtMs = safeAdd(startedAtMs, collectorTimeout, "Collector absolute deadline");
    const unsafeUntilMs = safeAdd(
      absoluteDeadlineAtMs,
      EXECUTE_SCRIPT_GRACE_MS,
      "Collector safety window",
    );
    const operation: ExportCollectorOperationJournal = {
      operationId: this.dependencies.clock.randomUUID(),
      collector: request.collector,
      startedAtMs,
      absoluteDeadlineAtMs,
      unsafeUntilMs,
      state: "running",
      error: null,
    };
    await this.dependencies.store.beginCollector(generation, operation);
    await this.refresh();

    const settlement = this.dependencies.runtime.executeCollector(tabId, request).then(
      async (result): Promise<CollectorOutcome> => ({
        accepted: await this.dependencies.store.settleCollector(
          generation,
          operation.operationId,
          this.dependencies.clock.now(),
          null,
        ),
        result,
      }),
      async (error: unknown): Promise<CollectorOutcome> => ({
        accepted: await this.dependencies.store.settleCollector(
          generation,
          operation.operationId,
          this.dependencies.clock.now(),
          errorMessage(error),
        ),
        error,
      }),
    );
    const remaining = Math.max(1, unsafeUntilMs - this.dependencies.clock.now());
    let outcome: CollectorOutcome;
    try {
      outcome = await this.dependencies.clock.deadline(
        settlement,
        remaining,
        `Collector ${request.collector} settle`,
      );
    } catch (error: unknown) {
      if (error instanceof OperationDeadlineError) {
        await this.refresh();
        throw new UnsafeCollectorOperationError(operation);
      }
      throw error;
    }
    await this.refresh();
    if (!outcome.accepted) {
      throw new Error("Collector result belongs to a superseded export generation.");
    }
    signal.throwIfAborted();
    if (outcome.error !== undefined) {
      throw outcome.error;
    }
    return outcome.result;
  }

  private async navigate(
    generation: string,
    signal: AbortSignal,
    tabId: number,
    target: PintiaNavigationTarget,
    recoveryUrl: string,
  ): Promise<void> {
    signal.throwIfAborted();
    const startedAtMs = this.dependencies.clock.now();
    const unsafeUntilMs = safeAdd(
      startedAtMs,
      NAVIGATION_UNSAFE_TIMEOUT_MS,
      "Chrome navigation safety window",
    );
    const operation: ExportNavigationOperationJournal = {
      operationId: this.dependencies.clock.randomUUID(),
      tabId,
      targetUrl: target.requestedUrl,
      recoveryUrl,
      startedAtMs,
      unsafeUntilMs,
      state: "running",
      error: null,
    };
    await this.dependencies.store.beginNavigation(generation, operation);
    await this.refresh();
    const settlement = this.dependencies.runtime.navigateTab(
      tabId,
      target,
    ).then(
      async (): Promise<CollectorOutcome> => ({
        accepted: await this.dependencies.store.settleNavigation(
          generation,
          operation.operationId,
          this.dependencies.clock.now(),
          null,
        ),
      }),
      async (error: unknown): Promise<CollectorOutcome> => ({
        accepted: await this.dependencies.store.settleNavigation(
          generation,
          operation.operationId,
          this.dependencies.clock.now(),
          errorMessage(error),
        ),
        error,
      }),
    );
    let outcome: CollectorOutcome;
    try {
      outcome = await this.dependencies.clock.deadline(
        settlement,
        Math.max(1, unsafeUntilMs - this.dependencies.clock.now()),
        `Chrome navigation ${target.requestedUrl}`,
      );
    } catch (error: unknown) {
      if (error instanceof OperationDeadlineError) {
        await this.refresh();
        throw new Error(
          `Chrome navigation to ${target.requestedUrl} did not settle before its persistent safety window.`,
        );
      }
      throw error;
    }
    await this.refresh();
    if (!outcome.accepted) {
      throw new Error("Chrome navigation result belongs to a superseded export generation.");
    }
    signal.throwIfAborted();
    if (outcome.error !== undefined) {
      throw outcome.error;
    }
  }

  private async claimBlob(
    generation: string,
    blob: ExportBlobResourceJournal,
  ): Promise<boolean> {
    const accepted = await this.dependencies.store.claimBlob(
      generation,
      this.dependencies.clock.now(),
      blob,
    );
    await this.refresh();
    return accepted;
  }

  private async transitionBlob(
    generation: string,
    requestId: string,
    expectedStates: ExportBlobResourceJournal["state"][],
    blob: ExportBlobResourceJournal | null,
  ): Promise<boolean> {
    const accepted = await this.dependencies.store.transitionBlob(
      generation,
      requestId,
      expectedStates,
      this.dependencies.clock.now(),
      blob,
    );
    await this.refresh();
    return accepted;
  }

  private async claimDownload(
    generation: string,
    download: ExportDownloadResourceJournal,
  ): Promise<boolean> {
    const accepted = await this.dependencies.store.claimDownload(
      generation,
      this.dependencies.clock.now(),
      download,
    );
    await this.refresh();
    return accepted;
  }

  private async transitionDownload(
    generation: string,
    identity: string,
    expectedStates: ExportDownloadResourceJournal["state"][],
    download: ExportDownloadResourceJournal,
  ): Promise<boolean> {
    const accepted = await this.dependencies.store.transitionDownload(
      generation,
      identity,
      expectedStates,
      this.dependencies.clock.now(),
      download,
    );
    await this.refresh();
    return accepted;
  }

  private async deliver(
    generation: string,
    signal: AbortSignal,
    snapshot: PintiaSnapshotV2,
    filename: string,
  ): Promise<void> {
    signal.throwIfAborted();
    await validateSnapshot(snapshot, signal);
    const json = JSON.stringify(snapshot);
    const expectedBytes = validateSerializedSnapshotBytes(json);
    const requestId = this.dependencies.clock.randomUUID();
    const fileName = snapshotFileName(requestId);
    const blobUnsafeUntilMs = safeAdd(
      this.dependencies.clock.now(),
      SNAPSHOT_BLOB_CREATE_TIMEOUT_MS + CHECKPOINT_OPERATION_TIMEOUT_MS,
      "Snapshot Blob safety window",
    );
    const blobBase: ExportBlobResourceJournal = {
      requestId,
      fileName,
      expectedBytes,
      unsafeUntilMs: blobUnsafeUntilMs,
      state: "writing",
    };
    if (!await this.claimBlob(generation, blobBase)) {
      throw new Error("Snapshot Blob claim belongs to a superseded export generation.");
    }
    if (!await this.transitionBlob(
      generation,
      requestId,
      ["writing"],
      { ...blobBase, state: "creating" },
    )) {
      throw new Error("Snapshot Blob creation belongs to a superseded export generation.");
    }

    let handle: Awaited<ReturnType<ExportCoordinatorRuntime["createBlob"]>> | null = null;
    try {
      handle = await this.dependencies.runtime.createBlob(
        requestId,
        json,
        expectedBytes,
        AbortSignal.timeout(SNAPSHOT_BLOB_CREATE_TIMEOUT_MS),
      );
      if (!await this.transitionBlob(
        generation,
        requestId,
        ["creating"],
        { ...blobBase, state: "live" },
      )) {
        await this.dependencies.runtime.revokeBlob(handle);
        handle = null;
        throw new Error("Late Snapshot Blob result was revoked after its generation ended.");
      }
      signal.throwIfAborted();

      const identity = this.dependencies.clock.randomUUID();
      const dot = filename.toLowerCase().endsWith(".json") ? filename.length - 5 : filename.length;
      const uniqueFilename = `${filename.slice(0, dot)}-${identity}.json`;
      const downloadUnsafeUntilMs = safeAdd(
        this.dependencies.clock.now(),
        DOWNLOAD_UNSAFE_TIMEOUT_MS,
        "Snapshot download safety window",
      );
      const downloadBase: ExportDownloadResourceJournal = {
        identity,
        filename: uniqueFilename,
        downloadId: null,
        unsafeUntilMs: downloadUnsafeUntilMs,
        state: "starting",
      };
      if (!await this.claimDownload(generation, downloadBase)) {
        throw new Error("Snapshot download claim belongs to a superseded export generation.");
      }
      await this.dependencies.runtime.download(identity, {
        url: handle.url,
        filename: uniqueFilename,
        conflictAction: "overwrite",
        saveAs: false,
      }, signal, {
        started: (downloadId) => this.transitionDownload(
          generation,
          identity,
          ["starting"],
          { ...downloadBase, downloadId, state: "in_progress" },
        ),
        cancelling: (downloadId) => this.transitionDownload(
          generation,
          identity,
          ["starting", "in_progress"],
          { ...downloadBase, downloadId, state: "cancelling" },
        ),
        terminal: (downloadId, state) => this.transitionDownload(
          generation,
          identity,
          ["in_progress", "cancelling"],
          { ...downloadBase, downloadId, state: state.state },
        ),
      });
    } finally {
      if (handle !== null) {
        const accepted = await this.transitionBlob(
          generation,
          requestId,
          ["live"],
          { ...blobBase, state: "revoking" },
        );
        await this.dependencies.runtime.revokeBlob(handle);
        if (accepted) {
          await this.transitionBlob(generation, requestId, ["revoking"], null);
        }
      }
    }
  }
}
