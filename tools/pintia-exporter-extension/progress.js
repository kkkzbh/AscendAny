const statusNode = document.getElementById("status");
const logNode = document.getElementById("log");
const progressFill = document.getElementById("progressFill");

const params = new URLSearchParams(location.search);
const tabId = Number(params.get("tabId"));
const problemSetId = params.get("problemSetId") || "";

let activePort = null;

function setStatus(text) {
  statusNode.textContent = text;
}

function appendLog(text) {
  logNode.textContent = `${logNode.textContent}${text}\n`;
  logNode.scrollTop = logNode.scrollHeight;
}

function updateProgressFromMessage(message) {
  const match = String(message || "").match(/submission code\s+(\d+)-(\d+)\/(\d+)/i);
  if (!match) {
    return;
  }
  const end = Number(match[2]);
  const total = Number(match[3]);
  if (total > 0) {
    const pct = Math.max(8, Math.min(95, Math.round((end / total) * 90)));
    progressFill.style.width = `${pct}%`;
  }
}

function startExport() {
  if (!Number.isFinite(tabId) || !problemSetId) {
    setStatus("Failed");
    appendLog("Missing Pintia tab id or problem set id.");
    return;
  }

  setStatus("Exporting");
  appendLog(`Problem set ${problemSetId}`);
  activePort = chrome.runtime.connect({ name: "pintia-export" });

  activePort.onMessage.addListener((message) => {
    if (!message) {
      return;
    }
    if (message.type === "progress") {
      appendLog(message.message);
      updateProgressFromMessage(message.message);
      return;
    }
    if (message.type === "done") {
      progressFill.style.width = "100%";
      appendLog(`Problems: ${message.integrity.problemCount}`);
      appendLog(`Participants: ${message.integrity.participantCount}`);
      appendLog(`Submissions: ${message.integrity.submissionCount}`);
      appendLog(`Code entries: ${message.integrity.codeCount}`);
      setStatus("Export complete");
      return;
    }
    if (message.type === "error") {
      appendLog(message.error || "Export failed.");
      setStatus("Export failed");
      return;
    }
  });

  activePort.onDisconnect.addListener(() => {
    if (statusNode.textContent === "Exporting") {
      appendLog("Exporter connection closed.");
      setStatus("Export stopped");
    }
  });

  activePort.postMessage({
    type: "START_EXPORT",
    tabId,
    problemSetId
  });
}

startExport();
