export {};

interface TaskSummary {
  status: "running" | "failed";
  active: boolean;
  error: string | null;
  progress: {
    totalSubmissions: number;
    completedDetails: number;
  };
}

interface CoordinationSummary {
  active: boolean;
  state: "active" | "recovering" | "completed" | "failed";
  waitingUntil: string | null;
  error: string | null;
}

interface ExportState {
  task: TaskSummary | null;
  coordination: CoordinationSummary | null;
}

const exportButton = requireElement<HTMLButtonElement>("exportButton");
const statusNode = requireElement<HTMLElement>("status");
const logNode = requireElement<HTMLPreElement>("log");
let activeTab: chrome.tabs.Tab | null = null;
let problemSetId = "";
let exportTask: TaskSummary | null = null;
let coordination: CoordinationSummary | null = null;

function requireElement<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (element === null) {
    throw new Error(`Missing UI element #${id}.`);
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

function parseProblemSetId(url: string): string | null {
  const parsed = new URL(url);
  if (parsed.origin !== "https://pintia.cn") {
    return null;
  }
  return parsed.pathname.match(/^\/problem-sets\/(\d+)(?:\/|$)/)?.[1] ?? null;
}

async function currentTab(): Promise<chrome.tabs.Tab | null> {
  return (await chrome.tabs.query({ active: true, currentWindow: true }))[0] ?? null;
}

async function openProgressTab(tab: chrome.tabs.Tab, id: string, auto: string): Promise<void> {
  if (tab.id === undefined || tab.url === undefined) {
    throw new Error("Current Pintia tab has no stable tab id or URL.");
  }
  const parameters = new URLSearchParams({
    tabId: String(tab.id),
    problemSetId: id,
    sourceUrl: tab.url,
  });
  if (auto.length > 0) {
    parameters.set("auto", auto);
  }
  await chrome.tabs.create({ url: chrome.runtime.getURL(`progress.html?${parameters.toString()}`), active: true });
}

function queryTaskState(id: string): Promise<ExportState> {
  return new Promise((resolve, reject) => {
    const port = chrome.runtime.connect({ name: "pintia-export-v2" });
    const timeout = setTimeout(() => {
      port.disconnect();
      reject(new Error("Timed out while checking the v2 export checkpoint."));
    }, 5_000);
    port.onMessage.addListener((message: unknown) => {
      if (typeof message !== "object" || message === null) {
        return;
      }
      const payload = message as Record<string, unknown>;
      if (payload.type !== "state") {
        return;
      }
      clearTimeout(timeout);
      port.disconnect();
      resolve({
        task: (payload.task as TaskSummary | null) ?? null,
        coordination: (payload.coordination as CoordinationSummary | null) ?? null,
      });
    });
    port.onDisconnect.addListener(() => {
      const error = chrome.runtime.lastError;
      if (error !== undefined) {
        clearTimeout(timeout);
        reject(new Error(error.message));
      }
    });
    port.postMessage({ type: "GET_EXPORT_STATE", problemSetId: id });
  });
}

function renderTaskState(state: ExportState): void {
  const { task } = state;
  exportTask = task;
  coordination = state.coordination;
  exportButton.disabled = false;
  logNode.textContent = "";
  if (coordination?.active === true) {
    setStatus(coordination.state === "recovering" ? "Exporter recovery in progress" : "Export ownership active");
    exportButton.textContent = "Open Progress";
    if (coordination.waitingUntil !== null) {
      appendLog(`Safety window: ${coordination.waitingUntil}`);
    }
    return;
  }
  if (task === null) {
    setStatus("Ready for Pintia snapshot v2");
    exportButton.textContent = "Export Current Problem Set";
    return;
  }
  const total = task.progress.totalSubmissions;
  const completed = task.progress.completedDetails;
  if (task.status === "failed" || (task.status === "running" && !task.active)) {
    setStatus("Saved v2 checkpoint found");
    exportButton.textContent = "Resume Export";
    appendLog(task.error ?? "Previous export stopped.");
    appendLog(`${completed}/${total} code entries collected.`);
    return;
  }
  setStatus("Export running");
  exportButton.textContent = "Open Progress";
  appendLog(`${completed}/${total} code entries collected.`);
}

async function initialize(): Promise<void> {
  exportButton.disabled = true;
  try {
    activeTab = await currentTab();
    problemSetId = activeTab?.url === undefined ? "" : (parseProblemSetId(activeTab.url) ?? "");
    if (activeTab?.id === undefined || problemSetId.length === 0) {
      throw new Error("Current tab is not inside a Pintia problem set.");
    }
    setStatus("Checking v2 checkpoint");
    renderTaskState(await queryTaskState(problemSetId));
  } catch (error: unknown) {
    setStatus("Unavailable");
    appendLog(error instanceof Error ? error.message : String(error));
  }
}

exportButton.addEventListener("click", () => {
  exportButton.disabled = true;
  void (async () => {
    try {
      if (activeTab?.id === undefined || activeTab.url === undefined || problemSetId.length === 0) {
        throw new Error("Current tab is not inside a Pintia problem set.");
      }
      const auto = exportTask !== null &&
        coordination?.active !== true &&
        (exportTask.status === "failed" || (exportTask.status === "running" && !exportTask.active))
        ? "retry"
        : "";
      setStatus(auto.length > 0 ? "Opening saved checkpoint" : "Opening progress page");
      await openProgressTab(activeTab, problemSetId, auto);
      window.close();
    } catch (error: unknown) {
      setStatus("Failed");
      appendLog(error instanceof Error ? error.message : String(error));
      exportButton.disabled = false;
    }
  })();
});

void initialize();
