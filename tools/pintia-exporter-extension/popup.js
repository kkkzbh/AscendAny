const exportButton = document.getElementById("exportButton");
const statusNode = document.getElementById("status");
const logNode = document.getElementById("log");

function setStatus(text) {
  statusNode.textContent = text;
}

function appendLog(text) {
  logNode.textContent = `${logNode.textContent}${text}\n`;
  logNode.scrollTop = logNode.scrollHeight;
}

function getProblemSetId(url) {
  const match = String(url || "").match(/\/problem-sets\/(\d+)/);
  return match ? match[1] : null;
}

async function getActiveTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return tab || null;
}

function openProgressTab(tab, problemSetId) {
  const params = new URLSearchParams({
    tabId: String(tab.id),
    problemSetId,
    sourceUrl: tab.url || ""
  });
  const url = chrome.runtime.getURL(`progress.html?${params.toString()}`);
  return chrome.tabs.create({ url, active: true });
}

exportButton.addEventListener("click", async () => {
  exportButton.disabled = true;
  logNode.textContent = "";

  try {
    const tab = await getActiveTab();
    const problemSetId = getProblemSetId(tab && tab.url);
    if (!tab || !tab.id || !problemSetId) {
      throw new Error("Current tab is not inside a Pintia problem set.");
    }

    setStatus("Opening progress page");
    appendLog(`Problem set ${problemSetId}`);
    await openProgressTab(tab, problemSetId);
    window.close();
  } catch (error) {
    setStatus("Failed");
    appendLog(error && error.message ? error.message : String(error));
    exportButton.disabled = false;
  }
});
