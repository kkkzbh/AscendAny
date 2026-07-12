import {
  EXPORT_COORDINATION_JOURNAL_ID,
  EXPORT_TASK_FORMAT_VERSION,
  SNAPSHOT_SCHEMA,
  type ExportBlobResourceJournal,
  type ExportCollectorOperationJournal,
  type ExportCoordinationJournal,
  type ExportDownloadResourceJournal,
  type ExportNavigationOperationJournal,
  type ExportTask,
} from "../domain/types";
import { MAX_TOTAL_NODES, MAX_TOTAL_STRING_BYTES } from "../domain/limits";

const DATABASE_NAME = "ascendany-pintia-exporter-v2";
const DATABASE_VERSION = EXPORT_TASK_FORMAT_VERSION;
const TASK_STORE = "tasks";
const COORDINATION_STORE = "coordination";

export interface LeaseClaim {
  generation: string;
  taskId: string;
  problemSetId: string;
  nowMs: number;
}

export type LeaseClaimResult =
  | { acquired: true; journal: ExportCoordinationJournal }
  | { acquired: false; journal: ExportCoordinationJournal };

export type RecoveryDecision =
  | { kind: "none" }
  | { kind: "blocked"; journal: ExportCoordinationJournal }
  | { kind: "recover"; journal: ExportCoordinationJournal };

export type FailureCommitResult = "failed" | "retained" | "completed";

export interface ExportCoordinatorStore {
  loadTask(problemSetId: string): Promise<ExportTask | null>;
  loadJournal(): Promise<ExportCoordinationJournal | null>;
  acquire(claim: LeaseClaim): Promise<LeaseClaimResult>;
  beginRecovery(nowMs: number, recoveryId: string, unsafeUntilMs: number): Promise<RecoveryDecision>;
  finishRecovery(generation: string, recoveryId: string, nowMs: number, error: string): Promise<void>;
  saveTask(generation: string, task: ExportTask): Promise<void>;
  beginNavigation(generation: string, operation: ExportNavigationOperationJournal): Promise<void>;
  settleNavigation(generation: string, operationId: string, nowMs: number, error: string | null): Promise<boolean>;
  beginCollector(generation: string, operation: ExportCollectorOperationJournal): Promise<void>;
  settleCollector(generation: string, operationId: string, nowMs: number, error: string | null): Promise<boolean>;
  claimBlob(generation: string, nowMs: number, blob: ExportBlobResourceJournal): Promise<boolean>;
  transitionBlob(
    generation: string,
    requestId: string,
    expectedStates: ExportBlobResourceJournal["state"][],
    nowMs: number,
    blob: ExportBlobResourceJournal | null,
  ): Promise<boolean>;
  claimDownload(generation: string, nowMs: number, download: ExportDownloadResourceJournal): Promise<boolean>;
  transitionDownload(
    generation: string,
    identity: string,
    expectedStates: ExportDownloadResourceJournal["state"][],
    nowMs: number,
    download: ExportDownloadResourceJournal,
  ): Promise<boolean>;
  complete(generation: string, problemSetId: string, nowMs: number): Promise<void>;
  fail(generation: string, nowMs: number, error: string): Promise<FailureCommitResult>;
}

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, DATABASE_VERSION);
    request.addEventListener("upgradeneeded", () => {
      const database = request.result;
      for (const store of [TASK_STORE, COORDINATION_STORE]) {
        if (database.objectStoreNames.contains(store)) {
          database.deleteObjectStore(store);
        }
      }
      database.createObjectStore(TASK_STORE, { keyPath: "problemSetId" });
      database.createObjectStore(COORDINATION_STORE, { keyPath: "id" });
    });
    request.addEventListener("success", () => resolve(request.result), { once: true });
    request.addEventListener("error", () => reject(request.error ?? new Error("Failed to open checkpoint database.")), {
      once: true,
    });
    request.addEventListener("blocked", () => reject(new Error("Checkpoint database upgrade is blocked.")), {
      once: true,
    });
  });
}

function requestValue<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.addEventListener("success", () => resolve(request.result), { once: true });
    request.addEventListener("error", () => reject(request.error ?? new Error("IndexedDB request failed.")), {
      once: true,
    });
  });
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.addEventListener("complete", () => resolve(), { once: true });
    transaction.addEventListener(
      "abort",
      () => reject(transaction.error ?? new Error("Checkpoint transaction was aborted.")),
      { once: true },
    );
    transaction.addEventListener(
      "error",
      () => reject(transaction.error ?? new Error("Checkpoint transaction failed.")),
      { once: true },
    );
  });
}

function nonempty(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

function safeTime(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function validNavigation(value: unknown): boolean {
  if (value === null) {
    return true;
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const operation = value as Partial<ExportNavigationOperationJournal>;
  return nonempty(operation.operationId) &&
    typeof operation.tabId === "number" && Number.isSafeInteger(operation.tabId) && operation.tabId >= 0 &&
    nonempty(operation.targetUrl) && nonempty(operation.recoveryUrl) &&
    safeTime(operation.startedAtMs) && safeTime(operation.unsafeUntilMs) &&
    (operation.state === "running" || operation.state === "settled") &&
    (operation.error === null || typeof operation.error === "string");
}

function validCollector(value: unknown): boolean {
  if (value === null) {
    return true;
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const operation = value as Partial<ExportCollectorOperationJournal>;
  return nonempty(operation.operationId) &&
    ["problems", "rankings", "submissions", "submission-details"].includes(operation.collector ?? "") &&
    safeTime(operation.startedAtMs) && safeTime(operation.absoluteDeadlineAtMs) &&
    safeTime(operation.unsafeUntilMs) &&
    (operation.state === "running" || operation.state === "settled") &&
    (operation.error === null || typeof operation.error === "string");
}

function validBlob(value: unknown): boolean {
  if (value === null) {
    return true;
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const blob = value as Partial<ExportBlobResourceJournal>;
  return nonempty(blob.requestId) && nonempty(blob.fileName) &&
    typeof blob.expectedBytes === "number" && Number.isSafeInteger(blob.expectedBytes) && blob.expectedBytes > 0 &&
    safeTime(blob.unsafeUntilMs) &&
    ["writing", "creating", "live", "revoking"].includes(blob.state ?? "");
}

function validDownload(value: unknown): boolean {
  if (value === null) {
    return true;
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const download = value as Partial<ExportDownloadResourceJournal>;
  return nonempty(download.identity) && nonempty(download.filename) &&
    (download.downloadId === null || (
      typeof download.downloadId === "number" && Number.isSafeInteger(download.downloadId) && download.downloadId >= 0
    )) && safeTime(download.unsafeUntilMs) &&
    ["starting", "in_progress", "cancelling", "complete", "interrupted"].includes(download.state ?? "");
}

function validateTask(value: unknown): ExportTask {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Stored checkpoint is not a v2 export task.");
  }
  const task = value as Partial<ExportTask>;
  if (
    task.schema !== SNAPSHOT_SCHEMA ||
    task.taskFormatVersion !== EXPORT_TASK_FORMAT_VERSION ||
    !nonempty(task.problemSetId) ||
    !nonempty(task.taskId) ||
    !nonempty(task.generation) ||
    task.origin !== "https://pintia.cn" ||
    typeof task.tabId !== "number" ||
    !Number.isSafeInteger(task.tabId) ||
    task.tabId < 0 ||
    typeof task.parts !== "object" ||
    task.parts === null ||
    typeof task.budget !== "object" || task.budget === null ||
    !safeTime(task.budget.nodes) || task.budget.nodes > MAX_TOTAL_NODES ||
    !safeTime(task.budget.stringBytes) || task.budget.stringBytes > MAX_TOTAL_STRING_BYTES ||
    !safeTime(task.budget.maximumDepth)
  ) {
    throw new Error("Stored checkpoint does not satisfy the Pintia snapshot v2 task contract.");
  }
  return task as ExportTask;
}

function validateJournal(value: unknown): ExportCoordinationJournal {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Stored export coordination journal is invalid.");
  }
  const journal = value as Partial<ExportCoordinationJournal>;
  if (
    journal.id !== EXPORT_COORDINATION_JOURNAL_ID ||
    journal.formatVersion !== EXPORT_TASK_FORMAT_VERSION ||
    !nonempty(journal.generation) ||
    !nonempty(journal.taskId) ||
    !nonempty(journal.problemSetId) ||
    !safeTime(journal.acquiredAtMs) ||
    !safeTime(journal.updatedAtMs) ||
    !["active", "recovering", "completed", "failed"].includes(journal.state ?? "") ||
    typeof journal.resources !== "object" ||
    journal.resources === null ||
    !validNavigation(journal.navigation) ||
    !validCollector(journal.collector) ||
    !validBlob(journal.resources.blob) ||
    !validDownload(journal.resources.download) ||
    (journal.state === "recovering" && (
      typeof journal.recovery !== "object" ||
      journal.recovery === null ||
      !nonempty(journal.recovery.recoveryId) ||
      !safeTime(journal.recovery.claimedAtMs) ||
      !safeTime(journal.recovery.unsafeUntilMs)
    )) ||
    (journal.state !== "recovering" && journal.recovery !== null)
  ) {
    throw new Error("Stored export coordination journal is invalid.");
  }
  return journal as ExportCoordinationJournal;
}

function activeJournal(value: unknown, generation?: string): ExportCoordinationJournal {
  const journal = validateJournal(value);
  if (
    (journal.state !== "active" && journal.state !== "recovering") ||
    (generation !== undefined && journal.generation !== generation)
  ) {
    throw new Error("Export coordination generation does not own the persistent lease.");
  }
  return journal;
}

async function readStoreValue(storeName: string, key: IDBValidKey): Promise<unknown> {
  const database = await openDatabase();
  try {
    const transaction = database.transaction(storeName, "readonly");
    const value = await requestValue(transaction.objectStore(storeName).get(key));
    await transactionDone(transaction);
    return value;
  } finally {
    database.close();
  }
}

interface JournalMutation<T> {
  result: T;
  journal?: ExportCoordinationJournal;
  putTask?: ExportTask;
  deleteTaskProblemSetId?: string;
}

async function mutateJournal<T>(
  mutate: (current: unknown) => JournalMutation<T>,
): Promise<T> {
  const database = await openDatabase();
  try {
    const transaction = database.transaction([COORDINATION_STORE, TASK_STORE], "readwrite");
    const journalStore = transaction.objectStore(COORDINATION_STORE);
    const taskStore = transaction.objectStore(TASK_STORE);
    let mutation: JournalMutation<T> | undefined;
    let mutationError: unknown;
    const request = journalStore.get(EXPORT_COORDINATION_JOURNAL_ID);
    request.addEventListener("success", () => {
      try {
        mutation = mutate(request.result);
        if (mutation.journal !== undefined) {
          journalStore.put(mutation.journal);
        }
        if (mutation.putTask !== undefined) {
          taskStore.put(mutation.putTask);
        }
        if (mutation.deleteTaskProblemSetId !== undefined) {
          taskStore.delete(mutation.deleteTaskProblemSetId);
        }
      } catch (error: unknown) {
        mutationError = error;
        transaction.abort();
      }
    }, { once: true });
    try {
      await transactionDone(transaction);
    } catch (error: unknown) {
      if (mutationError !== undefined) {
        throw mutationError;
      }
      throw error;
    }
    if (mutation === undefined) {
      throw new Error("Export coordination transaction produced no result.");
    }
    return mutation.result;
  } finally {
    database.close();
  }
}

async function failExportGeneration(
  generation: string,
  nowMs: number,
  error: string,
): Promise<FailureCommitResult> {
  const database = await openDatabase();
  try {
    const transaction = database.transaction([COORDINATION_STORE, TASK_STORE], "readwrite");
    const journalStore = transaction.objectStore(COORDINATION_STORE);
    const taskStore = transaction.objectStore(TASK_STORE);
    let result: FailureCommitResult | undefined;
    let mutationError: unknown;
    const journalRequest = journalStore.get(EXPORT_COORDINATION_JOURNAL_ID);
    journalRequest.addEventListener("success", () => {
      try {
        if (journalRequest.result === undefined) {
          throw new Error("Export failure has no persistent coordination journal.");
        }
        const journal = validateJournal(journalRequest.result);
        if (journal.generation !== generation) {
          throw new Error("Export failure generation lost persistent lease ownership.");
        }
        if (journal.state === "completed") {
          result = "completed";
          return;
        }
        const taskRequest = taskStore.get(journal.problemSetId);
        taskRequest.addEventListener("success", () => {
          try {
            if (taskRequest.result !== undefined) {
              const task = validateTask(taskRequest.result);
              if (task.generation === generation) {
                task.status = "failed";
                task.stage = "failed";
                task.error = error;
                task.updatedAt = new Date(nowMs).toISOString();
                task.progress = { ...task.progress, phase: "failed" };
                task.logs = [...task.logs, error].slice(-600);
                taskStore.put(task);
              }
            }
            const unsafe = journal.state === "recovering" ||
              journal.collector?.state === "running" ||
              journal.navigation?.state === "running" ||
              journal.resources.blob !== null ||
              (journal.resources.download !== null && journal.resources.download.state !== "complete");
            const failed: ExportCoordinationJournal = {
              ...journal,
              ...(unsafe ? {} : { state: "failed" as const, recovery: null }),
              updatedAtMs: nowMs,
              finalError: error,
            };
            journalStore.put(failed);
            result = unsafe ? "retained" : "failed";
          } catch (caught: unknown) {
            mutationError = caught;
            transaction.abort();
          }
        }, { once: true });
      } catch (caught: unknown) {
        mutationError = caught;
        transaction.abort();
      }
    }, { once: true });
    try {
      await transactionDone(transaction);
    } catch (caught: unknown) {
      if (mutationError !== undefined) {
        throw mutationError;
      }
      throw caught;
    }
    if (result === undefined) {
      throw new Error("Export failure transaction produced no result.");
    }
    return result;
  } finally {
    database.close();
  }
}

export class IndexedDbExportCoordinatorStore implements ExportCoordinatorStore {
  async loadTask(problemSetId: string): Promise<ExportTask | null> {
    const value = await readStoreValue(TASK_STORE, problemSetId);
    return value === undefined ? null : validateTask(value);
  }

  async loadJournal(): Promise<ExportCoordinationJournal | null> {
    const value = await readStoreValue(COORDINATION_STORE, EXPORT_COORDINATION_JOURNAL_ID);
    return value === undefined ? null : validateJournal(value);
  }

  acquire(claim: LeaseClaim): Promise<LeaseClaimResult> {
    return mutateJournal<LeaseClaimResult>((current) => {
      if (current !== undefined) {
        const existing = validateJournal(current);
        if (existing.state === "active" || existing.state === "recovering") {
          return { result: { acquired: false, journal: existing } };
        }
      }
      const journal: ExportCoordinationJournal = {
        id: EXPORT_COORDINATION_JOURNAL_ID,
        formatVersion: EXPORT_TASK_FORMAT_VERSION,
        generation: claim.generation,
        taskId: claim.taskId,
        problemSetId: claim.problemSetId,
        state: "active",
        acquiredAtMs: claim.nowMs,
        updatedAtMs: claim.nowMs,
        navigation: null,
        collector: null,
        resources: { blob: null, download: null },
        recovery: null,
        finalError: null,
      };
      return { result: { acquired: true, journal }, journal };
    });
  }

  beginRecovery(nowMs: number, recoveryId: string, unsafeUntilMs: number): Promise<RecoveryDecision> {
    return mutateJournal<RecoveryDecision>((current) => {
      if (current === undefined) {
        return { result: { kind: "none" } };
      }
      const journal = validateJournal(current);
      if (journal.state === "completed" || journal.state === "failed") {
        return { result: { kind: "none" } };
      }
      if (
        journal.state === "recovering" &&
        journal.recovery !== null &&
        journal.recovery.unsafeUntilMs > nowMs
      ) {
        return { result: { kind: "blocked", journal } };
      }
      if (
        journal.navigation?.state === "running" &&
        journal.navigation.unsafeUntilMs > nowMs
      ) {
        return { result: { kind: "blocked", journal } };
      }
      if (
        journal.collector?.state === "running" &&
        journal.collector.unsafeUntilMs > nowMs
      ) {
        return { result: { kind: "blocked", journal } };
      }
      if (
        journal.resources.blob !== null &&
        journal.resources.blob.unsafeUntilMs > nowMs
      ) {
        return { result: { kind: "blocked", journal } };
      }
      if (
        journal.resources.download !== null &&
        journal.resources.download.state !== "complete" &&
        journal.resources.download.unsafeUntilMs > nowMs
      ) {
        return { result: { kind: "blocked", journal } };
      }
      const recovering: ExportCoordinationJournal = {
        ...journal,
        state: "recovering",
        updatedAtMs: nowMs,
        recovery: { recoveryId, claimedAtMs: nowMs, unsafeUntilMs },
      };
      return { result: { kind: "recover", journal: recovering }, journal: recovering };
    });
  }

  finishRecovery(generation: string, recoveryId: string, nowMs: number, error: string): Promise<void> {
    return mutateJournal((current) => {
      const journal = activeJournal(current, generation);
      if (journal.state !== "recovering" || journal.recovery?.recoveryId !== recoveryId) {
        throw new Error("Export recovery completion requires the recovering lease state.");
      }
      const failed: ExportCoordinationJournal = {
        ...journal,
        state: "failed",
        updatedAtMs: nowMs,
        navigation: journal.navigation === null ? null : { ...journal.navigation, state: "settled" },
        collector: journal.collector === null ? null : { ...journal.collector, state: "settled" },
        resources: { blob: null, download: null },
        recovery: null,
        finalError: error,
      };
      return { result: undefined, journal: failed };
    });
  }

  saveTask(generation: string, task: ExportTask): Promise<void> {
    return mutateJournal((current) => {
      const journal = activeJournal(current, generation);
      if (journal.state !== "active" || task.generation !== generation || task.taskId !== journal.taskId) {
        throw new Error("Checkpoint save does not match the active export generation.");
      }
      return { result: undefined, putTask: task };
    });
  }

  beginNavigation(generation: string, operation: ExportNavigationOperationJournal): Promise<void> {
    return mutateJournal((current) => {
      const journal = activeJournal(current, generation);
      if (
        journal.state !== "active" ||
        journal.navigation?.state === "running" ||
        journal.collector?.state === "running"
      ) {
        throw new Error("A Chrome operation already owns the persistent export lease.");
      }
      return {
        result: undefined,
        journal: { ...journal, updatedAtMs: operation.startedAtMs, navigation: operation },
      };
    });
  }

  settleNavigation(
    generation: string,
    operationId: string,
    nowMs: number,
    error: string | null,
  ): Promise<boolean> {
    return mutateJournal((current) => {
      if (current === undefined) {
        return { result: false };
      }
      const journal = validateJournal(current);
      if (
        journal.generation !== generation ||
        journal.state !== "active" ||
        journal.navigation?.operationId !== operationId ||
        journal.navigation.state !== "running"
      ) {
        return { result: false };
      }
      return {
        result: true,
        journal: {
          ...journal,
          updatedAtMs: nowMs,
          navigation: { ...journal.navigation, state: "settled", error },
        },
      };
    });
  }

  beginCollector(generation: string, operation: ExportCollectorOperationJournal): Promise<void> {
    return mutateJournal((current) => {
      const journal = activeJournal(current, generation);
      if (
        journal.state !== "active" ||
        journal.navigation?.state === "running" ||
        journal.collector?.state === "running"
      ) {
        throw new Error("A collector operation already owns the persistent export lease.");
      }
      return {
        result: undefined,
        journal: { ...journal, updatedAtMs: operation.startedAtMs, collector: operation },
      };
    });
  }

  settleCollector(
    generation: string,
    operationId: string,
    nowMs: number,
    error: string | null,
  ): Promise<boolean> {
    return mutateJournal((current) => {
      if (current === undefined) {
        return { result: false };
      }
      const journal = validateJournal(current);
      if (
        journal.generation !== generation ||
        journal.state !== "active" ||
        journal.collector?.operationId !== operationId ||
        journal.collector.state !== "running"
      ) {
        return { result: false };
      }
      return {
        result: true,
        journal: {
          ...journal,
          updatedAtMs: nowMs,
          collector: { ...journal.collector, state: "settled", error },
        },
      };
    });
  }

  claimBlob(
    generation: string,
    nowMs: number,
    blob: ExportBlobResourceJournal,
  ): Promise<boolean> {
    return mutateJournal((current) => {
      if (current === undefined) {
        return { result: false };
      }
      const journal = validateJournal(current);
      if (
        journal.generation !== generation ||
        journal.state !== "active" ||
        journal.resources.blob !== null
      ) {
        return { result: false };
      }
      return {
        result: true,
        journal: { ...journal, updatedAtMs: nowMs, resources: { ...journal.resources, blob } },
      };
    });
  }

  transitionBlob(
    generation: string,
    requestId: string,
    expectedStates: ExportBlobResourceJournal["state"][],
    nowMs: number,
    blob: ExportBlobResourceJournal | null,
  ): Promise<boolean> {
    return mutateJournal((current) => {
      if (current === undefined) {
        return { result: false };
      }
      const journal = validateJournal(current);
      const currentBlob = journal.resources.blob;
      if (
        journal.generation !== generation ||
        journal.state !== "active" ||
        currentBlob?.requestId !== requestId ||
        !expectedStates.includes(currentBlob.state) ||
        (blob !== null && blob.requestId !== requestId)
      ) {
        return { result: false };
      }
      return {
        result: true,
        journal: { ...journal, updatedAtMs: nowMs, resources: { ...journal.resources, blob } },
      };
    });
  }

  claimDownload(
    generation: string,
    nowMs: number,
    download: ExportDownloadResourceJournal,
  ): Promise<boolean> {
    return mutateJournal((current) => {
      if (current === undefined) {
        return { result: false };
      }
      const journal = validateJournal(current);
      if (
        journal.generation !== generation ||
        journal.state !== "active" ||
        journal.resources.download !== null
      ) {
        return { result: false };
      }
      return {
        result: true,
        journal: { ...journal, updatedAtMs: nowMs, resources: { ...journal.resources, download } },
      };
    });
  }

  transitionDownload(
    generation: string,
    identity: string,
    expectedStates: ExportDownloadResourceJournal["state"][],
    nowMs: number,
    download: ExportDownloadResourceJournal,
  ): Promise<boolean> {
    return mutateJournal((current) => {
      if (current === undefined) {
        return { result: false };
      }
      const journal = validateJournal(current);
      const currentDownload = journal.resources.download;
      if (
        journal.generation !== generation ||
        journal.state !== "active" ||
        currentDownload?.identity !== identity ||
        !expectedStates.includes(currentDownload.state) ||
        download.identity !== identity
      ) {
        return { result: false };
      }
      return {
        result: true,
        journal: { ...journal, updatedAtMs: nowMs, resources: { ...journal.resources, download } },
      };
    });
  }

  complete(generation: string, problemSetId: string, nowMs: number): Promise<void> {
    return mutateJournal((current) => {
      const journal = activeJournal(current, generation);
      if (
        journal.state !== "active" ||
        journal.problemSetId !== problemSetId ||
        journal.navigation?.state === "running" ||
        journal.collector?.state === "running" ||
        journal.resources.blob !== null ||
        (journal.resources.download !== null && journal.resources.download.state !== "complete")
      ) {
        throw new Error("Export completion requires settled operations and terminal resources.");
      }
      const completed: ExportCoordinationJournal = {
        ...journal,
        state: "completed",
        updatedAtMs: nowMs,
        recovery: null,
        finalError: null,
      };
      return {
        result: undefined,
        journal: completed,
        deleteTaskProblemSetId: problemSetId,
      };
    });
  }

  fail(generation: string, nowMs: number, error: string): Promise<FailureCommitResult> {
    return failExportGeneration(generation, nowMs, error);
  }
}

export const indexedDbExportCoordinatorStore = new IndexedDbExportCoordinatorStore();

export function loadTask(problemSetId: string): Promise<ExportTask | null> {
  return indexedDbExportCoordinatorStore.loadTask(problemSetId);
}
