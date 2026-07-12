import { buildSnapshot, selectProgrammingSubmissionItems, sha256Utf8 } from "./domain/normalize";
import {
  canonicalCaptureJson,
  captureCollectionCanonical,
  CaptureDriftError,
} from "./domain/capture-attempt";
import {
  adaptProblemCollection,
  adaptRankingCollection,
  adaptSubmissionCollection,
} from "./domain/collector-adapters";
import {
  CHECKPOINT_OPERATION_TIMEOUT_MS,
  DETAIL_BATCH_CONCURRENCY,
  DETAIL_BATCH_SIZE,
  DETAIL_REQUEST_SPACING_MS,
  EXPORT_LIMITS,
} from "./domain/limits";
import {
  consumeExportBudget,
  emptyExportBudget,
} from "./domain/operational-preflight";
import {
  EXPORT_TASK_FORMAT_VERSION,
  SNAPSHOT_SCHEMA,
  type CollectorName,
  type CollectorRequest,
  type ExportFailure,
  type ExportCoordinationJournal,
  type ExportStage,
  type ExportTask,
  type JsonObject,
  type PintiaSnapshotV2,
  type ProblemCollection,
  type RankingCollection,
  type SnapshotSource,
  type SubmissionCollection,
  type SubmissionDetailSource,
} from "./domain/types";
import {
  indexedDbExportCoordinatorStore,
} from "./platform/checkpoint-store";
import { abortableDelay, withDeadline } from "./platform/deadline";
import {
  ExportCoordinator,
  type ExportCoordinatorRunContext,
  systemExportCoordinatorClock,
} from "./platform/export-coordinator";
import { PintiaCollectorError } from "./platform/chrome-export-runtime";
import { chromeExportCoordinatorRuntime } from "./platform/chrome-export-runtime-production";
import {
  exactPintiaNavigationTarget,
  programmingProblemsNavigationTarget,
  type PintiaNavigationTarget,
} from "./platform/navigation";

const PINTIA_ORIGIN = "https://pintia.cn" as const;
const submissionsNavigationTarget = (problemSetId: string): PintiaNavigationTarget =>
  exactPintiaNavigationTarget(`/problem-sets/${problemSetId}/submissions`);
const CHECKPOINT_INTERVAL = 40;
const CHECKPOINT_MILLISECONDS = 5_000;
const MAX_DETAIL_PASSES = 5;
const RETRY_BASE_MILLISECONDS = 1_000;
const RATE_LIMIT_BACKOFF_MILLISECONDS = 120_000;
const MAX_LOG_ENTRIES = 600;
const COLLECTOR_ATTEMPTS = 3;
const MAX_CAPTURE_ATTEMPTS = 3;

function submissionDetail(value: unknown, expectedId: string): SubmissionDetailSource {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`Projected submission detail ${expectedId} is invalid.`);
  }
  const detail = value as Record<string, unknown>;
  if (
    detail.submissionId !== expectedId ||
    typeof detail.code !== "string" ||
    detail.code.length === 0 ||
    (detail.compileLog !== null && typeof detail.compileLog !== "string") ||
    typeof detail.testcaseJudgeResults !== "object" ||
    detail.testcaseJudgeResults === null ||
    Array.isArray(detail.testcaseJudgeResults)
  ) {
    throw new Error(`Projected submission detail ${expectedId} violates its typed contract.`);
  }
  return detail as unknown as SubmissionDetailSource;
}

function adaptSubmissionDetailBatch(
  value: unknown,
  expectedIds: string[],
): Array<{ submissionId: string; detail: SubmissionDetailSource }> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Submission detail batch returned an invalid object.");
  }
  const items = (value as Record<string, unknown>).items;
  if (!Array.isArray(items) || items.length !== expectedIds.length) {
    throw new Error("Submission detail batch result count does not match its request.");
  }
  return items.map((rawItem, index) => {
    if (typeof rawItem !== "object" || rawItem === null || Array.isArray(rawItem)) {
      throw new Error(`Submission detail batch item ${index} is invalid.`);
    }
    const item = rawItem as Record<string, unknown>;
    const expectedId = expectedIds[index] as string;
    if (item.submissionId !== expectedId) {
      throw new Error(`Submission detail batch item ${index} does not bind to ${expectedId}.`);
    }
    return {
      submissionId: expectedId,
      detail: submissionDetail(item.detail, expectedId),
    };
  });
}

const taskPorts = new Map<string, Set<chrome.runtime.Port>>();
const exportCoordinator = new ExportCoordinator({
  store: indexedDbExportCoordinatorStore,
  clock: systemExportCoordinatorClock,
  runtime: chromeExportCoordinatorRuntime,
});

export interface StartCommand {
  type: "START_EXPORT" | "RETRY_EXPORT" | "RESTART_EXPORT";
  tabId: number;
  problemSetId: string;
  sourceUrl: string;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function problemSetIdFromUrl(url: string): string | null {
  const parsed = new URL(url);
  if (parsed.origin !== PINTIA_ORIGIN) {
    return null;
  }
  const match = parsed.pathname.match(/^\/problem-sets\/(\d+)(?:\/|$)/);
  return match?.[1] ?? null;
}

function safeFileName(value: string): string {
  return value
    .replace(/[\\/:*?"<>|\u0000-\u001f]+/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 80) || "untitled";
}

function exportFileName(snapshot: PintiaSnapshotV2, timestamp: boolean): string {
  const pieces = [
    "AscendAny",
    "Pintia",
    safeFileName(snapshot.exam.title),
    snapshot.exam.problemSetId,
  ];
  if (timestamp) {
    pieces.push(snapshot.exporter.exportedAt.replace(/[-:TZ.]/g, "").slice(0, 14));
  }
  return `${pieces.join("-")}.json`;
}

function nowIso(): string {
  return new Date().toISOString();
}

function send(port: chrome.runtime.Port, message: unknown): void {
  try {
    port.postMessage(message);
  } catch {
    // A progress page can disconnect while its durable task keeps running.
  }
}

function subscribe(problemSetId: string, port: chrome.runtime.Port): void {
  const ports = taskPorts.get(problemSetId) ?? new Set<chrome.runtime.Port>();
  ports.add(port);
  taskPorts.set(problemSetId, ports);
}

function unsubscribe(port: chrome.runtime.Port): void {
  for (const [problemSetId, ports] of taskPorts) {
    ports.delete(port);
    if (ports.size === 0) {
      taskPorts.delete(problemSetId);
    }
  }
}

function broadcast(task: ExportTask, message: unknown): boolean {
  const ports = taskPorts.get(task.problemSetId);
  if (ports === undefined || ports.size === 0) {
    return false;
  }
  ports.forEach((port) => send(port, message));
  return true;
}

function delay(milliseconds: number, signal: AbortSignal): Promise<void> {
  return abortableDelay(milliseconds, signal);
}

function appendLog(task: ExportTask, message: string): void {
  task.logs.push(message);
  if (task.logs.length > MAX_LOG_ENTRIES) {
    task.logs.splice(0, task.logs.length - MAX_LOG_ENTRIES);
  }
  task.updatedAt = nowIso();
}

function consumeTaskBudget(task: ExportTask, value: unknown, field: string): void {
  task.budget = consumeExportBudget(task.budget, value, field);
}

function recomputeTaskBudget(task: ExportTask): void {
  task.budget = emptyExportBudget();
  if (task.parts.problems !== null) {
    consumeTaskBudget(task, task.parts.problems, "stored problem collection");
  }
  if (task.parts.rankings !== null) {
    consumeTaskBudget(task, task.parts.rankings, "stored ranking collection");
  }
  if (task.parts.submissions.collection !== null) {
    consumeTaskBudget(task, task.parts.submissions.collection, "stored submission collection");
  }
  for (const [submissionId, detail] of Object.entries(
    task.parts.submissions.submissionDetailsById,
  )) {
    if (detail.submissionId !== submissionId) {
      throw new Error(`Stored submission detail ${submissionId} has a different identity.`);
    }
    consumeTaskBudget(task, detail, `stored submission detail ${submissionId}`);
  }
}

function completedDetailCount(task: ExportTask): number {
  return Object.keys(task.parts.submissions.submissionDetailsById).length;
}

function submissionTotal(task: ExportTask): number {
  return task.parts.submissions.programmingItems.length;
}

function computePercent(task: ExportTask): number {
  const total = submissionTotal(task);
  if (total > 0) {
    const roundedProgress = Math.round((completedDetailCount(task) / total) * 88);
    if (!Number.isSafeInteger(roundedProgress) || roundedProgress < 0) {
      throw new Error("Export progress cannot be represented as a safe non-negative integer.");
    }
    return Math.min(98, Math.max(8, roundedProgress + 8));
  }
  const stagePercent: Partial<Record<ExportStage, number>> = {
    starting: 4,
    problems: 12,
    rankings: 20,
    submissions: 30,
    validating: 98,
  };
  return stagePercent[task.stage] ?? task.progress.percent;
}

function journalWaitingUntil(journal: ExportCoordinationJournal | null): string | null {
  if (journal === null) {
    return null;
  }
  const deadlines = [
    journal.navigation?.state === "running" ? journal.navigation.unsafeUntilMs : 0,
    journal.collector?.state === "running" ? journal.collector.unsafeUntilMs : 0,
    journal.resources.blob?.unsafeUntilMs ?? 0,
    journal.resources.download !== null && journal.resources.download.state !== "complete"
      ? journal.resources.download.unsafeUntilMs
      : 0,
    journal.recovery?.unsafeUntilMs ?? 0,
  ];
  const deadline = Math.max(...deadlines);
  return deadline > 0 ? new Date(deadline).toISOString() : null;
}

function coordinationSummary(
  problemSetId: string,
  journal: ExportCoordinationJournal | null,
): unknown {
  if (journal === null || journal.problemSetId !== problemSetId) {
    return null;
  }
  return {
    state: journal.state,
    active: journal.state === "active" || journal.state === "recovering",
    live: exportCoordinator.ownsLiveRun(problemSetId),
    waitingUntil: journalWaitingUntil(journal),
    error: journal.finalError,
  };
}

function taskSummary(
  task: ExportTask | null,
  journal: ExportCoordinationJournal | null = exportCoordinator.snapshot(),
): unknown {
  if (task === null) {
    return null;
  }
  const total = submissionTotal(task);
  const completed = completedDetailCount(task);
  const active = journal !== null &&
    journal.problemSetId === task.problemSetId &&
    (journal.state === "active" || journal.state === "recovering");
  return {
    taskId: task.taskId,
    problemSetId: task.problemSetId,
    status: task.status,
    active,
    coordinationState: journal?.problemSetId === task.problemSetId ? journal.state : null,
    waitingUntil: active ? journalWaitingUntil(journal) : null,
    stage: task.stage,
    error: task.error,
    createdAt: task.createdAt,
    updatedAt: task.updatedAt,
    progress: {
      ...task.progress,
      totalSubmissions: total,
      completedDetails: completed,
      pendingDetails: Math.max(0, total - completed),
      percent: computePercent(task),
    },
    logs: task.logs,
    failures: task.failures.slice(0, 20),
    captureAttempt: task.captureAttempt,
  };
}

function publish(task: ExportTask, port: chrome.runtime.Port, message: string, progress: Partial<ExportTask["progress"]> = {}): void {
  appendLog(task, message);
  task.progress = { ...task.progress, ...progress, percent: computePercent(task) };
  const payload = { type: "progress", message, task: taskSummary(task) };
  if (!broadcast(task, payload)) {
    send(port, payload);
  }
}

function setStage(task: ExportTask, stage: ExportStage): void {
  task.stage = stage;
  task.progress = { ...task.progress, phase: stage, percent: computePercent(task) };
}

async function persistTask(task: ExportTask, context: ExportCoordinatorRunContext): Promise<void> {
  context.signal.throwIfAborted();
  task.updatedAt = nowIso();
  await context.checkpoint(task);
}

function rateLimited(error: unknown): boolean {
  return error instanceof PintiaCollectorError && error.failure.kind === "rate_limited";
}

async function sendCollector(
  tabId: number,
  task: ExportTask,
  collector: CollectorName,
  submissionIds: string[] | undefined,
  port: chrome.runtime.Port,
  attempts: number,
  context: ExportCoordinatorRunContext,
): Promise<unknown> {
  let lastError: unknown = new Error(`Failed to collect ${collector}.`);
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    context.signal.throwIfAborted();
    try {
      const request: CollectorRequest = {
        type: "ASCENDANY_COLLECT_PINTIA_ROUTE_V2",
        problemSetId: task.problemSetId,
        collector,
        limits: EXPORT_LIMITS,
        ...(submissionIds === undefined ? {} : { submissionIds }),
      };
      return await context.collect(tabId, request);
    } catch (error: unknown) {
      context.signal.throwIfAborted();
      lastError = error;
      if (attempt === attempts) {
        break;
      }
      if (rateLimited(error)) {
        publish(task, port, "Pintia rate limit detected; pausing 120 seconds");
        await delay(RATE_LIMIT_BACKOFF_MILLISECONDS, context.signal);
      } else {
        await delay(750, context.signal);
      }
    }
  }
  throw lastError;
}

function createTask(
  tab: chrome.tabs.Tab,
  problemSetId: string,
  sourceUrl: string,
  context: ExportCoordinatorRunContext,
): ExportTask {
  if (tab.id === undefined || tab.url === undefined || new URL(tab.url).origin !== PINTIA_ORIGIN) {
    throw new Error("Cannot create an export task without an exact Pintia source tab.");
  }
  if (problemSetIdFromUrl(sourceUrl) !== problemSetId) {
    throw new Error("Export source URL does not identify the selected Pintia problem set.");
  }
  const createdAt = nowIso();
  return {
    schema: SNAPSHOT_SCHEMA,
    taskFormatVersion: EXPORT_TASK_FORMAT_VERSION,
    taskId: context.taskId,
    generation: context.generation,
    problemSetId,
    tabId: tab.id,
    origin: PINTIA_ORIGIN,
    sourceUrl,
    originalUrl: tab.url,
    status: "running",
    stage: "starting",
    error: null,
    createdAt,
    updatedAt: createdAt,
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
    budget: emptyExportBudget(),
    parts: {
      problems: null,
      rankings: null,
      submissions: {
        collection: null,
        programmingItems: [],
        submissionDetailsById: {},
      },
    },
  };
}

function beginCaptureAttempt(task: ExportTask): void {
  task.captureAttempt += 1;
  task.status = "running";
  task.stage = "starting";
  task.error = null;
  task.failures = [];
  task.budget = emptyExportBudget();
  task.progress = {
    phase: "starting",
    totalSubmissions: 0,
    completedDetails: 0,
    pendingDetails: 0,
    detailPass: 0,
    percent: 4,
  };
  task.parts = {
    problems: null,
    rankings: null,
    submissions: {
      collection: null,
      programmingItems: [],
      submissionDetailsById: {},
    },
  };
}

async function fetchProblemCollection(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<ProblemCollection> {
  await context.navigateTab(
    task.tabId,
    programmingProblemsNavigationTarget(task.problemSetId),
    task.originalUrl,
  );
  await delay(1_500, context.signal);
  return adaptProblemCollection(
    await sendCollector(task.tabId, task, "problems", undefined, port, COLLECTOR_ATTEMPTS, context),
    task.problemSetId,
  );
}

async function fetchRankingCollection(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<RankingCollection> {
  await context.navigateTab(
    task.tabId,
    exactPintiaNavigationTarget(`/problem-sets/${task.problemSetId}/rankings`),
    task.originalUrl,
  );
  await delay(1_500, context.signal);
  return adaptRankingCollection(
    await sendCollector(task.tabId, task, "rankings", undefined, port, COLLECTOR_ATTEMPTS, context),
  );
}

async function collectStaticRoutes(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<void> {
  if (task.parts.problems === null) {
    setStage(task, "problems");
    publish(task, port, "Collecting all programming problems");
    await persistTask(task, context);
    const problems = await fetchProblemCollection(task, port, context);
    consumeTaskBudget(task, problems, "problem collection");
    task.parts.problems = problems;
  }
  if (task.parts.rankings === null) {
    setStage(task, "rankings");
    publish(task, port, "Collecting all rankings");
    await persistTask(task, context);
    const rankings = await fetchRankingCollection(task, port, context);
    consumeTaskBudget(task, rankings, "ranking collection");
    task.parts.rankings = rankings;
  }
  await persistTask(task, context);
}

function pendingSubmissions(task: ExportTask): JsonObject[] {
  const details = task.parts.submissions.submissionDetailsById;
  return task.parts.submissions.programmingItems.filter((item) => {
    const id = typeof item.id === "string" ? item.id : "";
    return id.length > 0 && details[id] === undefined;
  });
}

async function fetchSubmissionCollection(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<SubmissionCollection> {
  await context.navigateTab(task.tabId, submissionsNavigationTarget(task.problemSetId), task.originalUrl);
  await delay(1_500, context.signal);
  return adaptSubmissionCollection(
    await sendCollector(task.tabId, task, "submissions", undefined, port, COLLECTOR_ATTEMPTS, context),
  );
}

function selectAttemptProgrammingItems(
  problems: ProblemCollection,
  submissions: SubmissionCollection,
): JsonObject[] {
  const problemIds = new Set(problems.items.map((problem) => {
    if (typeof problem.id !== "string") {
      throw new Error("Collected problem is missing id.");
    }
    return problem.id;
  }));
  return selectProgrammingSubmissionItems(submissions.items, problemIds);
}

async function collectSubmissionList(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<void> {
  if (task.parts.problems === null) {
    throw new Error("Problems must be collected before submissions.");
  }
  if (task.parts.submissions.collection === null) {
    setStage(task, "submissions");
    publish(task, port, "Collecting the complete submission list");
    await persistTask(task, context);
    const collection = await fetchSubmissionCollection(task, port, context);
    consumeTaskBudget(task, collection, "submission collection");
    task.parts.submissions.collection = collection;
  }
  const collection = task.parts.submissions.collection;
  const programmingItems = selectAttemptProgrammingItems(task.parts.problems, collection);
  task.parts.submissions.programmingItems = programmingItems;
  const programmingIds = new Set(programmingItems.map((item) => item.id));
  for (const submissionId of Object.keys(task.parts.submissions.submissionDetailsById)) {
    if (!programmingIds.has(submissionId)) {
      throw new Error(`Stored detail ${submissionId} does not belong to a programming submission.`);
    }
  }
  task.progress.totalSubmissions = programmingItems.length;
  task.progress.pendingDetails = pendingSubmissions(task).length;
  publish(
    task,
    port,
    `Programming submissions selected ${programmingItems.length}/${collection.observedCount}`,
  );
  await persistTask(task, context);
}

function rememberFailure(task: ExportTask, failure: ExportFailure | null, successfulId: string | null): void {
  const failures = new Map(task.failures.map((item) => [item.submissionId, item]));
  if (successfulId !== null) {
    failures.delete(successfulId);
  }
  if (failure !== null) {
    failures.set(failure.submissionId, failure);
  }
  task.failures = [...failures.values()];
}

async function detailCheckpoint(
  task: ExportTask,
  port: chrome.runtime.Port,
  force: boolean,
  context: ExportCoordinatorRunContext,
): Promise<void> {
  const now = Date.now();
  const completed = completedDetailCount(task);
  const shouldSave = force ||
    completed - (task.progress.lastCheckpointCompleted ?? 0) >= CHECKPOINT_INTERVAL ||
    now - (task.progress.lastCheckpointAtMs ?? 0) >= CHECKPOINT_MILLISECONDS;
  if (!shouldSave) {
    return;
  }
  task.progress.completedDetails = completed;
  task.progress.pendingDetails = Math.max(0, submissionTotal(task) - completed);
  task.progress.lastCheckpointCompleted = completed;
  task.progress.lastCheckpointAtMs = now;
  task.progress.percent = computePercent(task);
  await persistTask(task, context);
  const payload = { type: "checkpoint", task: taskSummary(task) };
  if (!broadcast(task, payload)) {
    send(port, payload);
  }
}

function retryDelay(pass: number): number {
  return Math.min(RATE_LIMIT_BACKOFF_MILLISECONDS, RETRY_BASE_MILLISECONDS * 2 ** Math.max(0, pass - 1));
}

async function paceDetailBatch(itemCount: number, signal: AbortSignal): Promise<void> {
  if (!Number.isSafeInteger(itemCount) || itemCount <= 0 || itemCount > DETAIL_BATCH_SIZE) {
    throw new Error("Detail pacing requires one bounded production batch.");
  }
  await delay(itemCount * DETAIL_REQUEST_SPACING_MS, signal);
}

async function collectSubmissionDetails(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<void> {
  let pending = pendingSubmissions(task);
  const total = submissionTotal(task);
  let previousPassRateLimited = false;

  for (let pass = 1; pending.length > 0 && pass <= MAX_DETAIL_PASSES; pass += 1) {
    context.signal.throwIfAborted();
    setStage(task, "submission-details");
    task.progress.detailPass = pass;
    task.progress.totalSubmissions = total;
    task.progress.queueConcurrency = DETAIL_BATCH_CONCURRENCY;
    task.progress.requestSpacingMs = DETAIL_REQUEST_SPACING_MS;
    await persistTask(task, context);
    if (pass > 1) {
      const milliseconds = previousPassRateLimited
        ? RATE_LIMIT_BACKOFF_MILLISECONDS
        : retryDelay(pass);
      publish(task, port, `Retrying ${pending.length} code entries after ${milliseconds}ms`, { detailPass: pass });
      await delay(milliseconds, context.signal);
    }
    previousPassRateLimited = false;

    publish(
      task,
      port,
      `Collecting code ${completedDetailCount(task)}/${total} (round ${pass}, service-worker-paced MAIN batches of ${DETAIL_BATCH_SIZE})`,
      {
        detailPass: pass,
        queueConcurrency: DETAIL_BATCH_CONCURRENCY,
        requestSpacingMs: DETAIL_REQUEST_SPACING_MS,
      },
    );

    for (let start = 0; start < pending.length; start += DETAIL_BATCH_SIZE) {
      context.signal.throwIfAborted();
      const batch = pending.slice(start, start + DETAIL_BATCH_SIZE);
      const submissionIds = batch.map((item) => {
        if (typeof item.id !== "string" || item.id.length === 0) {
          throw new Error("Programming submission is missing id.");
        }
        return item.id;
      });
      try {
        const details = adaptSubmissionDetailBatch(
          await sendCollector(
            task.tabId,
            task,
            "submission-details",
            submissionIds,
            port,
            1,
            context,
          ),
          submissionIds,
        );
        for (const { submissionId, detail } of details) {
          consumeTaskBudget(task, detail, `submission detail ${submissionId}`);
          task.parts.submissions.submissionDetailsById[submissionId] = detail;
          rememberFailure(task, null, submissionId);
        }
        await paceDetailBatch(submissionIds.length, context.signal);
      } catch (error: unknown) {
        context.signal.throwIfAborted();
        const text = errorMessage(error);
        for (const submissionId of submissionIds) {
          rememberFailure(task, { submissionId, error: text, attempts: pass }, null);
        }
        if (rateLimited(error)) {
          previousPassRateLimited = true;
          publish(task, port, "Pintia rate limit detected; stopping this detail round before one 120 second retry backoff", {
            detailPass: pass,
          });
          await detailCheckpoint(task, port, true, context);
          break;
        }
        await paceDetailBatch(submissionIds.length, context.signal);
      }
      await detailCheckpoint(task, port, false, context);
    }
    await detailCheckpoint(task, port, true, context);
    pending = pendingSubmissions(task);
    if (previousPassRateLimited && pending.length > 0) {
      publish(task, port, `Rate-limited detail round stopped; ${pending.length} entries remain`, {
        detailPass: pass,
        pendingDetails: pending.length,
      });
    }
  }

  if (pending.length > 0) {
    throw new Error(`Incomplete code export after retries: ${completedDetailCount(task)}/${total}.`);
  }
}

async function detailDigests(
  details: Record<string, SubmissionDetailSource>,
  signal: AbortSignal,
): Promise<Map<string, string>> {
  const digests = new Map<string, string>();
  for (const submissionId of Object.keys(details).sort()) {
    signal.throwIfAborted();
    const detail = details[submissionId];
    if (detail === undefined || detail.submissionId !== submissionId) {
      throw new Error(`Submission detail ${submissionId} does not bind to its checkpoint identity.`);
    }
    digests.set(submissionId, await sha256Utf8(canonicalCaptureJson(detail), signal));
  }
  return digests;
}

async function verifySubmissionDetailDigests(
  task: ExportTask,
  port: chrome.runtime.Port,
  expected: ReadonlyMap<string, string>,
  context: ExportCoordinatorRunContext,
): Promise<void> {
  let pending = task.parts.submissions.programmingItems.map((item) => {
    if (typeof item.id !== "string" || item.id.length === 0) {
      throw new Error("Programming submission is missing id during digest verification.");
    }
    return item.id;
  });
  if (expected.size !== pending.length || pending.some((submissionId) => !expected.has(submissionId))) {
    throw new CaptureDriftError("submission-details");
  }

  let previousPassRateLimited = false;
  for (let pass = 1; pending.length > 0 && pass <= MAX_DETAIL_PASSES; pass += 1) {
    context.signal.throwIfAborted();
    if (pass > 1) {
      const milliseconds = previousPassRateLimited
        ? RATE_LIMIT_BACKOFF_MILLISECONDS
        : retryDelay(pass);
      publish(
        task,
        port,
        `Retrying ${pending.length} submission detail digests after ${milliseconds}ms`,
      );
      await delay(milliseconds, context.signal);
    }
    previousPassRateLimited = false;
    const failed = new Set<string>();
    publish(task, port, `Verifying ${pending.length} submission detail digests (round ${pass})`);
    for (let start = 0; start < pending.length; start += DETAIL_BATCH_SIZE) {
      const submissionIds = pending.slice(start, start + DETAIL_BATCH_SIZE);
      try {
        const details = adaptSubmissionDetailBatch(
          await sendCollector(
            task.tabId,
            task,
            "submission-details",
            submissionIds,
            port,
            1,
            context,
          ),
          submissionIds,
        );
        for (const { submissionId, detail } of details) {
          const actual = await sha256Utf8(canonicalCaptureJson(detail), context.signal);
          if (actual !== expected.get(submissionId)) {
            throw new CaptureDriftError("submission-details");
          }
        }
        await paceDetailBatch(submissionIds.length, context.signal);
      } catch (error: unknown) {
        context.signal.throwIfAborted();
        if (error instanceof CaptureDriftError) {
          throw error;
        }
        submissionIds.forEach((submissionId) => failed.add(submissionId));
        if (rateLimited(error)) {
          pending.slice(start + submissionIds.length).forEach((submissionId) => failed.add(submissionId));
          previousPassRateLimited = true;
          publish(
            task,
            port,
            "Pintia rate limit detected; stopping this digest round before one 120 second retry backoff",
          );
          break;
        }
        await paceDetailBatch(submissionIds.length, context.signal);
      }
    }
    pending = [...failed];
  }
  if (pending.length > 0) {
    throw new Error(`Incomplete submission detail digest verification: ${pending.length} entries remain.`);
  }
}

async function verifyCaptureAttempt(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<void> {
  const initialProblems = task.parts.problems;
  const initialRankings = task.parts.rankings;
  const initialSubmissions = task.parts.submissions.collection;
  if (initialProblems === null || initialRankings === null || initialSubmissions === null) {
    throw new Error("Capture verification requires all initial collections.");
  }

  setStage(task, "validating");
  publish(task, port, "Re-scanning Pintia collections to prove a stable capture attempt");
  await persistTask(task, context);
  const expectedProblemDigest = await sha256Utf8(
    captureCollectionCanonical("problems", initialProblems),
    context.signal,
  );
  const expectedRankingDigest = await sha256Utf8(
    captureCollectionCanonical("rankings", initialRankings),
    context.signal,
  );
  const expectedSubmissionDigest = await sha256Utf8(
    captureCollectionCanonical("submissions", initialSubmissions),
    context.signal,
  );
  {
    const finalProblems = await fetchProblemCollection(task, port, context);
    if (
      await sha256Utf8(captureCollectionCanonical("problems", finalProblems), context.signal) !==
      expectedProblemDigest
    ) {
      throw new CaptureDriftError("problems");
    }
  }
  {
    const finalRankings = await fetchRankingCollection(task, port, context);
    if (
      await sha256Utf8(captureCollectionCanonical("rankings", finalRankings), context.signal) !==
      expectedRankingDigest
    ) {
      throw new CaptureDriftError("rankings");
    }
  }
  {
    const finalSubmissions = await fetchSubmissionCollection(task, port, context);
    if (
      await sha256Utf8(captureCollectionCanonical("submissions", finalSubmissions), context.signal) !==
      expectedSubmissionDigest
    ) {
      throw new CaptureDriftError("submissions");
    }
  }

  const expectedDigests = await detailDigests(
    task.parts.submissions.submissionDetailsById,
    context.signal,
  );
  publish(task, port, "Re-collecting submission details as digest-only verification");
  await persistTask(task, context);
  await verifySubmissionDetailDigests(task, port, expectedDigests, context);
  publish(task, port, `Capture attempt ${task.captureAttempt} is stable across the final re-scan`);
  await persistTask(task, context);
}

async function captureStableSource(
  task: ExportTask,
  port: chrome.runtime.Port,
  context: ExportCoordinatorRunContext,
): Promise<void> {
  if (task.captureAttempt === 0) {
    beginCaptureAttempt(task);
  } else {
    recomputeTaskBudget(task);
    publish(
      task,
      port,
      `Resuming deterministic capture attempt ${task.captureAttempt}/${MAX_CAPTURE_ATTEMPTS}`,
    );
  }
  while (task.captureAttempt <= MAX_CAPTURE_ATTEMPTS) {
    context.signal.throwIfAborted();
    publish(
      task,
      port,
      `Continuing deterministic capture attempt ${task.captureAttempt}/${MAX_CAPTURE_ATTEMPTS}`,
    );
    await persistTask(task, context);
    try {
      await collectStaticRoutes(task, port, context);
      await collectSubmissionList(task, port, context);
      await collectSubmissionDetails(task, port, context);
      await verifyCaptureAttempt(task, port, context);
      return;
    } catch (error: unknown) {
      if (!(error instanceof CaptureDriftError)) {
        throw error;
      }
      if (task.captureAttempt === MAX_CAPTURE_ATTEMPTS) {
        throw new Error(
          `${error.message} Exhausted ${MAX_CAPTURE_ATTEMPTS} deterministic capture attempts.`,
        );
      }
      publish(
        task,
        port,
        `${error.message} Discarding every captured part before the next attempt`,
      );
      beginCaptureAttempt(task);
    }
  }
  throw new Error("Deterministic capture attempts ended unexpectedly.");
}

function snapshotSource(task: ExportTask): SnapshotSource {
  if (
    task.parts.problems === null ||
    task.parts.rankings === null ||
    task.parts.submissions.collection === null
  ) {
    throw new Error("Export task is missing a fully collected source part.");
  }
  return {
    problemSetId: task.problemSetId,
    sourceUrl: task.sourceUrl,
    problems: task.parts.problems,
    rankings: task.parts.rankings,
    submissions: task.parts.submissions.collection,
    submissionDetailsById: task.parts.submissions.submissionDetailsById,
  };
}

async function resolveStart(
  command: StartCommand,
  context: ExportCoordinatorRunContext,
): Promise<{ tab: chrome.tabs.Tab; problemSetId: string }> {
  const tab = await withDeadline(
    context.getTab(command.tabId),
    CHECKPOINT_OPERATION_TIMEOUT_MS,
    "Resolve Pintia source tab",
    context.signal,
  );
  if (tab.id === undefined || tab.url === undefined || new URL(tab.url).origin !== PINTIA_ORIGIN) {
    throw new Error("Saved export tab is unavailable or left Pintia.");
  }
  const tabProblemSetId = problemSetIdFromUrl(tab.url);
  if (tabProblemSetId === null || tabProblemSetId !== command.problemSetId) {
    throw new Error("Pintia tab no longer matches the export problem set.");
  }
  return { tab, problemSetId: command.problemSetId };
}

async function prepareTask(
  command: StartCommand,
  restart: boolean,
  context: ExportCoordinatorRunContext,
): Promise<ExportTask> {
  const { tab, problemSetId } = await resolveStart(command, context);
  const stored = restart
    ? null
    : await withDeadline(
      context.loadTask(problemSetId),
      CHECKPOINT_OPERATION_TIMEOUT_MS,
      `Checkpoint load ${problemSetId}`,
      context.signal,
    );
  const task = stored ?? createTask(tab, problemSetId, command.sourceUrl, context);
  task.taskFormatVersion = EXPORT_TASK_FORMAT_VERSION;
  task.taskId = context.taskId;
  task.generation = context.generation;
  task.tabId = tab.id as number;
  task.sourceUrl = command.sourceUrl;
  if (stored === null) {
    task.originalUrl = tab.url as string;
  }
  task.status = "running";
  task.error = null;
  return task;
}

interface CompletedExport {
  completeness: PintiaSnapshotV2["completeness"];
  filenameHint: string;
}

export async function runExportWithinCoordinator(
  port: chrome.runtime.Port,
  command: StartCommand,
  context: ExportCoordinatorRunContext,
): Promise<CompletedExport> {
  const restart = command.type === "RESTART_EXPORT";
  let task: ExportTask | null = null;
  try {
    task = await prepareTask(command, restart, context);
    send(port, { type: "state", task: taskSummary(task) });
    await persistTask(task, context);
    publish(task, port, `Problem set ${task.problemSetId}`);

    await captureStableSource(task, port, context);

    setStage(task, "restoring");
    publish(task, port, "Restoring the original Pintia page");
    await persistTask(task, context);
    await context.restoreTab(task.tabId, task.originalUrl);

    setStage(task, "validating");
    publish(task, port, "Validating strict Pintia snapshot v2 invariants");
    await persistTask(task, context);
    const completedTask = task;
    const snapshot = await buildSnapshot(snapshotSource(completedTask), nowIso(), {
      signal: context.signal,
    });

    setStage(task, "downloading");
    publish(task, port, "Downloading Pintia snapshot v2 JSON");
    await persistTask(task, context);
    await context.deliver(snapshot, exportFileName(snapshot, true));
    return {
      completeness: snapshot.completeness,
      filenameHint: exportFileName(snapshot, false),
    };
  } catch (error: unknown) {
    if (task !== null && !context.signal.aborted) {
      let failure = errorMessage(error);
      try {
        await context.restoreTab(task.tabId, task.originalUrl);
      } catch (restoreError: unknown) {
        failure = `${failure} Original tab restore also failed: ${errorMessage(restoreError)}`;
      }
      throw new Error(failure);
    }
    throw error;
  }
}

export async function runExport(port: chrome.runtime.Port, command: StartCommand): Promise<void> {
  try {
    const completed = await chromeExportCoordinatorRuntime.keepServiceWorkerAlive(
      exportCoordinator.run({
        problemSetId: command.problemSetId,
        execute: (context) => runExportWithinCoordinator(port, command, context),
      }),
    );
    const payload = { type: "done", ...completed };
    const ports = taskPorts.get(command.problemSetId);
    if (ports === undefined || ports.size === 0) {
      send(port, payload);
    } else {
      ports.forEach((subscriber) => send(subscriber, payload));
    }
  } catch (error: unknown) {
    const journal = await exportCoordinator.refresh();
    const task = await exportCoordinator.loadTask(command.problemSetId);
    const payload = {
      type: "error",
      error: errorMessage(error),
      task: taskSummary(task, journal),
    };
    if (task === null || !broadcast(task, payload)) {
      send(port, payload);
    }
  }
}

function validStartCommand(value: unknown): value is StartCommand {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const command = value as Partial<StartCommand>;
  return ["START_EXPORT", "RETRY_EXPORT", "RESTART_EXPORT"].includes(command.type ?? "") &&
    typeof command.tabId === "number" &&
    Number.isSafeInteger(command.tabId) &&
    command.tabId >= 0 &&
    typeof command.problemSetId === "string" &&
    typeof command.sourceUrl === "string";
}

chrome.runtime.onConnect.addListener((port) => {
  if (port.name !== "pintia-export-v2") {
    return;
  }
  port.onDisconnect.addListener(() => unsubscribe(port));
  port.onMessage.addListener((message: unknown) => {
    if (typeof message !== "object" || message === null) {
      return;
    }
    const input = message as Record<string, unknown>;
    if (input.type === "GET_EXPORT_STATE" && typeof input.problemSetId === "string") {
      subscribe(input.problemSetId, port);
      void exportCoordinator.refresh().then(async (journal) => ({
        journal,
        task: await exportCoordinator.loadTask(input.problemSetId as string),
      })).then(
        ({ journal, task }) => send(port, {
          type: "state",
          task: taskSummary(task, journal),
          coordination: coordinationSummary(input.problemSetId as string, journal),
        }),
        (error: unknown) => send(port, { type: "error", error: errorMessage(error) }),
      );
      return;
    }
    if (validStartCommand(message)) {
      subscribe(message.problemSetId, port);
      void runExport(port, message).catch((error: unknown) => {
        send(port, { type: "error", error: errorMessage(error) });
      });
    }
  });
});
