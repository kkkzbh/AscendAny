import {
  ProgressRecoveryController,
  type ProgressRecoveryCoordination,
} from "./platform/progress-recovery";

interface ProgressSummary {
  totalSubmissions: number;
  completedDetails: number;
  pendingDetails: number;
  detailPass: number;
  percent: number;
  queueConcurrency?: number;
  requestSpacingMs?: number;
}

interface TaskSummary {
  status: "running" | "failed";
  active: boolean;
  stage: string;
  error: string | null;
  progress: ProgressSummary;
  logs: string[];
}

interface CoordinationSummary extends ProgressRecoveryCoordination {
  state: "active" | "recovering" | "completed" | "failed";
  waitingUntil: string | null;
  error: string | null;
}

interface Completeness {
  problems: { exportedCount: number };
  rankings: { exportedCount: number };
  submissions: { observedCount: number; exportedCount: number };
  participants: { exportedCount: number };
}

const statusNode = requireElement<HTMLElement>("status");
const logNode = requireElement<HTMLPreElement>("log");
const progressFill = requireElement<HTMLElement>("progressFill");
const progressTextNode = requireElement<HTMLElement>("progressText");
const progressBarNode = requireSelector<HTMLElement>(".progress-bar");
const retryButton = requireElement<HTMLButtonElement>("retryButton");
const restartButton = requireElement<HTMLButtonElement>("restartButton");

const parameters = new URLSearchParams(location.search);
const tabId = Number(parameters.get("tabId"));
const problemSetId = parameters.get("problemSetId") ?? "";
const sourceUrl = parameters.get("sourceUrl") ?? "";
const autoAction = parameters.get("auto") ?? "";
let activePort: chrome.runtime.Port | null = null;
let initialStateResolved = false;
let finalStateReached = false;
let lastLoggedWaitingUntil: string | null = null;

function requireElement<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (element === null) {
    throw new Error(`Missing UI element #${id}.`);
  }
  return element as T;
}

function requireSelector<T extends HTMLElement>(selector: string): T {
  const element = document.querySelector(selector);
  if (element === null) {
    throw new Error(`Missing UI element ${selector}.`);
  }
  return element as T;
}

function setStatus(text: string): void {
  statusNode.textContent = text;
}

function appendLog(text: string): void {
  logNode.textContent = `${logNode.textContent ?? ""}${text}\n`;
  logNode.scrollTop = logNode.scrollHeight;
}

function setProgress(percent: number): void {
  const value = Math.max(0, Math.min(100, Number.isFinite(percent) ? percent : 0));
  progressFill.style.width = `${value}%`;
  progressBarNode.setAttribute("aria-valuenow", String(value));
}

function command(type: "START_EXPORT" | "RETRY_EXPORT" | "RESTART_EXPORT"): void {
  if (activePort === null) {
    return;
  }
  retryButton.hidden = true;
  restartButton.hidden = true;
  setStatus(type === "RESTART_EXPORT" ? "Restarting" : "Exporting");
  activePort.postMessage({ type, tabId, problemSetId, sourceUrl });
}

function requestExportState(): void {
  activePort?.postMessage({ type: "GET_EXPORT_STATE", problemSetId });
}

function stageLabel(stage: string): string {
  const labels: Record<string, string> = {
    starting: "Preparing export",
    problems: "Collecting all problems",
    rankings: "Collecting all rankings",
    submissions: "Collecting all submissions",
    "submission-details": "Collecting submission code",
    restoring: "Restoring original page",
    validating: "Validating snapshot v2",
    downloading: "Downloading JSON",
    failed: "Export failed",
  };
  return labels[stage] ?? stage;
}

function renderTask(task: TaskSummary | null): void {
  if (task === null) {
    progressTextNode.textContent = "No saved checkpoint";
    setProgress(4);
    return;
  }
  const progress = task.progress;
  const queue = progress.queueConcurrency === undefined
    ? ""
    : `, batch concurrency ${progress.queueConcurrency}, service-worker pacing ${progress.requestSpacingMs ?? 0}ms/request`;
  const details = progress.totalSubmissions > 0
    ? `${progress.completedDetails}/${progress.totalSubmissions} code entries, ${progress.pendingDetails} pending${
      progress.detailPass > 0 ? `, round ${progress.detailPass}` : ""
    }${queue}`
    : stageLabel(task.stage);
  setProgress(progress.percent);
  progressTextNode.textContent = `${stageLabel(task.stage)} · ${details}`;
  logNode.textContent = task.logs.length > 0 ? `${task.logs.join("\n")}\n` : "";
  logNode.scrollTop = logNode.scrollHeight;
  if (task.active) {
    retryButton.hidden = true;
    restartButton.hidden = true;
    setStatus("Export ownership active");
    return;
  }
  if (task.status === "failed") {
    setStatus("Export failed");
    retryButton.hidden = false;
    restartButton.hidden = false;
    return;
  }
  retryButton.hidden = true;
  restartButton.hidden = true;
  setStatus("Exporting");
}

function startExport(): void {
  if (!Number.isSafeInteger(tabId) || tabId <= 0 || problemSetId.length === 0 || sourceUrl.length === 0) {
    setStatus("Failed");
    appendLog("Missing Pintia tab id, problem set id, or source URL.");
    return;
  }
  command("START_EXPORT");
}

const recoveryController = new ProgressRecoveryController({
  requestState: requestExportState,
  retry: () => command("RETRY_EXPORT"),
});

function renderCoordination(
  task: TaskSummary | null,
  coordination: CoordinationSummary,
): boolean {
  if (!coordination.active) {
    recoveryController.observe(coordination);
    return false;
  }
  renderTask(task);
  retryButton.hidden = true;
  restartButton.hidden = true;
  if (coordination.live) {
    setStatus(coordination.state === "recovering" ? "Recovering export ownership" : "Export ownership active");
    recoveryController.observe(coordination);
    return true;
  }
  if (coordination.waitingUntil !== null) {
    setStatus("Waiting for recovery safety window");
    if (lastLoggedWaitingUntil !== coordination.waitingUntil) {
      appendLog(`Safety window: ${coordination.waitingUntil}`);
      lastLoggedWaitingUntil = coordination.waitingUntil;
    }
    recoveryController.observe(coordination);
    return true;
  }
  setStatus("Recovering interrupted export");
  recoveryController.observe(coordination);
  return true;
}

function connect(): void {
  activePort = chrome.runtime.connect({ name: "pintia-export-v2" });
  activePort.onMessage.addListener((message: unknown) => {
    if (typeof message !== "object" || message === null) {
      return;
    }
    const payload = message as Record<string, unknown>;
    if (payload.type === "state") {
      const task = (payload.task as TaskSummary | null) ?? null;
      const coordination = (payload.coordination as CoordinationSummary | null) ?? null;
      const firstState = !initialStateResolved;
      if (!initialStateResolved) {
        initialStateResolved = true;
      }
      if (coordination !== null && renderCoordination(task, coordination)) {
        return;
      }
      if (firstState) {
        if (task === null) {
          startExport();
          return;
        }
        if (task.status === "running" && !task.active) {
          renderTask(task);
          command("RETRY_EXPORT");
          return;
        }
        if (autoAction === "retry" && task.status === "failed") {
          renderTask(task);
          command("RETRY_EXPORT");
          return;
        }
        if (autoAction === "restart") {
          renderTask(task);
          command("RESTART_EXPORT");
          return;
        }
      }
      renderTask(task);
      return;
    }
    if (payload.type === "progress" || payload.type === "checkpoint") {
      renderTask((payload.task as TaskSummary | null) ?? null);
      return;
    }
    if (payload.type === "done") {
      finalStateReached = true;
      recoveryController.reset();
      setProgress(100);
      const completeness = payload.completeness as Completeness;
      appendLog(`Problems: ${completeness.problems.exportedCount}`);
      appendLog(`Rankings: ${completeness.rankings.exportedCount}`);
      appendLog(`Participants: ${completeness.participants.exportedCount}`);
      appendLog(`Observed submissions: ${completeness.submissions.observedCount}`);
      appendLog(`Programming submissions: ${completeness.submissions.exportedCount}`);
      setStatus("Export complete");
      progressTextNode.textContent = typeof payload.filenameHint === "string"
        ? `Ready: ${payload.filenameHint}`
        : "Download started";
      retryButton.hidden = true;
      restartButton.hidden = true;
      return;
    }
    if (payload.type === "error") {
      const task = (payload.task as TaskSummary | null) ?? null;
      renderTask(task);
      if (recoveryController.handleCommandError(task?.active === true)) {
        setStatus("Refreshing export ownership");
        return;
      }
      if (task === null) {
        appendLog(typeof payload.error === "string" ? payload.error : "Export failed.");
        setStatus("Export failed");
        retryButton.hidden = false;
        restartButton.hidden = false;
      }
    }
  });
  activePort.onDisconnect.addListener(() => {
    recoveryController.reset();
    activePort = null;
    if (!finalStateReached && statusNode.textContent === "Exporting") {
      appendLog("Exporter connection closed. Reopen this page to attach to the durable checkpoint.");
      setStatus("Export stopped");
    }
  });
  requestExportState();
}

retryButton.addEventListener("click", () => command("RETRY_EXPORT"));
restartButton.addEventListener("click", () => {
  logNode.textContent = "";
  command("RESTART_EXPORT");
});

setStatus("Loading v2 checkpoint");
connect();
export {};
