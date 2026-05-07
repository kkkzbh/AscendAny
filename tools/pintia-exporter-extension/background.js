const SCHEMA = "ascendany.pintia.unit.v1";
const EXPORTER_NAME = "ascendany-pintia-exporter";
const EXPORTER_VERSION = "0.1.0";

const ROUTES = [
  { collector: "problems", path: (id) => `/problem-sets/${id}/problems/type/7`, label: "Collecting problems" },
  { collector: "rankings", path: (id) => `/problem-sets/${id}/rankings`, label: "Collecting rankings" }
];

const SUBMISSIONS_ROUTE = {
  collector: "submissions",
  path: (id) => `/problem-sets/${id}/submissions`,
  label: "Collecting submission list"
};
const SUBMISSION_DETAIL_BATCH_SIZE = 24;
const SUBMISSION_DETAIL_CONCURRENCY = 4;
const SUBMISSION_DETAIL_REQUEST_DELAY_MS = 800;
const SUBMISSION_DETAIL_BATCH_COOLDOWN_MS = 3000;
const RATE_LIMIT_BACKOFF_MS = 120000;

const activeTasks = new Map();

function getProblemSetId(url) {
  const match = String(url || "").match(/\/problem-sets\/(\d+)/);
  return match ? match[1] : null;
}

function safeFileName(value) {
  return String(value || "pintia")
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/\s+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 120);
}

function buildExportFileName(bundle, withTimestamp) {
  const stamp = new Date().toISOString().replace(/[-:TZ.]/g, "").slice(0, 14);
  const title = safeFileName(bundle.exam.title) || "untitled";
  const pieces = ["AscendAny", "Pintia", title, bundle.exam.problemSetId];
  if (withTimestamp) {
    pieces.push(stamp);
  }
  return `${pieces.join("-")}.json`;
}

function send(port, message) {
  try {
    port.postMessage(message);
  } catch (_error) {
    // The popup may close while a long export is still running.
  }
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function getActiveTab() {
  return new Promise((resolve, reject) => {
    chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
      const error = chrome.runtime.lastError;
      if (error) {
        reject(new Error(error.message));
        return;
      }
      resolve(tabs && tabs[0] ? tabs[0] : null);
    });
  });
}

function getTab(tabId) {
  return new Promise((resolve, reject) => {
    chrome.tabs.get(tabId, (tab) => {
      const error = chrome.runtime.lastError;
      if (error) {
        reject(new Error(error.message));
        return;
      }
      resolve(tab);
    });
  });
}

function navigateTab(tabId, url) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      cleanup();
      reject(new Error(`Timed out while loading ${url}`));
    }, 60_000);

    function cleanup() {
      clearTimeout(timeout);
      chrome.tabs.onUpdated.removeListener(onUpdated);
    }

    function onUpdated(updatedTabId, changeInfo, tab) {
      if (updatedTabId !== tabId || changeInfo.status !== "complete") {
        return;
      }
      cleanup();
      resolve(tab);
    }

    function update() {
      chrome.tabs.onUpdated.addListener(onUpdated);
      chrome.tabs.update(tabId, { url }, () => {
        const error = chrome.runtime.lastError;
        if (error) {
          cleanup();
          reject(new Error(error.message));
        }
      });
    }

    chrome.tabs.get(tabId, (tab) => {
      const error = chrome.runtime.lastError;
      if (error) {
        cleanup();
        reject(new Error(error.message));
        return;
      }
      if (tab && tab.url === url && tab.status === "complete") {
        cleanup();
        resolve(tab);
        return;
      }
      update();
    });
  });
}

async function restoreTab(tabId, url) {
  const current = await getTab(tabId).catch(() => null);
  if (!current || current.url === url) {
    return;
  }
  await navigateTab(tabId, url).catch(() => undefined);
}

function sendMessage(tabId, message) {
  return new Promise((resolve, reject) => {
    chrome.tabs.sendMessage(tabId, message, (response) => {
      const error = chrome.runtime.lastError;
      if (error) {
        reject(new Error(error.message));
        return;
      }
      resolve(response);
    });
  });
}

function isRateLimitError(error) {
  const message = error && error.message ? error.message : String(error || "");
  return /访问得太快|休息一会|too fast|rate limit/i.test(message);
}

async function collectRoute(tabId, problemSetId, collector, payload, port) {
  let lastError = null;
  for (let attempt = 0; attempt < 30; attempt += 1) {
    try {
      const response = await sendMessage(tabId, {
        type: "ASCENDANY_COLLECT_PINTIA_ROUTE",
        problemSetId,
        collector,
        payload
      });
      if (!response || !response.ok) {
        throw new Error(response && response.error ? response.error : `Failed to collect ${collector}.`);
      }
      return response.result;
    } catch (error) {
      lastError = error;
      if (isRateLimitError(error)) {
        send(port, { type: "progress", message: "Pintia rate limit detected; pausing 120s" });
        await delay(RATE_LIMIT_BACKOFF_MS);
      } else {
        await delay(750);
      }
    }
  }
  throw lastError || new Error(`Failed to collect ${collector}.`);
}

function chunkItems(items, size) {
  const chunks = [];
  for (let index = 0; index < items.length; index += size) {
    chunks.push(items.slice(index, index + size));
  }
  return chunks;
}

async function collectSubmissions(tabId, origin, problemSetId, port) {
  const url = getRouteUrl(origin, problemSetId, SUBMISSIONS_ROUTE);
  send(port, { type: "progress", message: SUBMISSIONS_ROUTE.label });
  await navigateTab(tabId, url);
  await delay(1500);

  const submissionResult = await collectRoute(tabId, problemSetId, "submissions", undefined, port);
  const submissionList = submissionResult.submissionList || [];
  const submissionIndexes = submissionResult.submissionIndexes || {};
  const batches = chunkItems(submissionList, SUBMISSION_DETAIL_BATCH_SIZE);
  const submissionDetails = [];

  for (let index = 0; index < batches.length; index += 1) {
    const start = index * SUBMISSION_DETAIL_BATCH_SIZE + 1;
    const end = Math.min(start + batches[index].length - 1, submissionList.length);
    send(port, {
      type: "progress",
      message: `Collecting submission code ${start}-${end}/${submissionList.length}`
    });
    const batchResult = await collectRoute(tabId, problemSetId, "submission-details", {
      submissions: batches[index],
      indexes: submissionIndexes,
      concurrency: SUBMISSION_DETAIL_CONCURRENCY,
      requestDelayMs: SUBMISSION_DETAIL_REQUEST_DELAY_MS
    }, port);
    submissionDetails.push(...(batchResult.submissionDetails || []));
    if (index < batches.length - 1) {
      await delay(SUBMISSION_DETAIL_BATCH_COOLDOWN_MS);
    }
  }

  return {
    submissionList,
    submissionIndexes,
    submissionDetails
  };
}

function getRouteUrl(origin, problemSetId, route) {
  return `${origin}${route.path(problemSetId)}`;
}

function titleFromSummary(summary, problemSetId) {
  return (
    (summary && summary.problemSet && summary.problemSet.title) ||
    (summary && summary.title) ||
    `pintia-${problemSetId}`
  );
}

function normalizeRankings(rankingResponse) {
  if (!rankingResponse) {
    return [];
  }
  if (Array.isArray(rankingResponse)) {
    return rankingResponse;
  }
  return rankingResponse.commonRankings || rankingResponse.rankings || rankingResponse.items || [];
}

function buildBundle(problemSetId, originalUrl, parts) {
  const summary = parts.problems.summary || {};
  const problems = parts.problems.problems || [];
  const rankingResponse = parts.rankings.rankingResponse || {};
  const rankings = normalizeRankings(rankingResponse);
  const studentUserById = rankingResponse.studentUserById || {};
  const userById = rankingResponse.userById || {};
  const submissionList = parts.submissions.submissionList || [];
  const submissionDetails = parts.submissions.submissionDetails || [];
  const warnings = [];

  const bundle = {
    schema: SCHEMA,
    exporter: {
      name: EXPORTER_NAME,
      version: EXPORTER_VERSION,
      exportedAt: new Date().toISOString(),
      sourceUrl: originalUrl
    },
    exam: {
      platform: "pintia",
      problemSetId,
      title: titleFromSummary(summary, problemSetId),
      sourceUrl: originalUrl,
      raw: summary
    },
    problems,
    participants: Object.values(studentUserById),
    rankings,
    submissions: submissionDetails,
    rawIndexes: {
      userById,
      studentUserById,
      submissionIndexes: parts.submissions.submissionIndexes || {}
    },
    integrity: {
      problemCount: problems.length,
      participantCount: Object.keys(studentUserById).length || rankings.length,
      rankingCount: rankings.length,
      submissionCount: submissionList.length,
      submissionDetailCount: submissionDetails.length,
      codeCount: submissionDetails.filter((item) => item && item.code).length,
      warnings
    }
  };

  validateBundle(bundle);
  return bundle;
}

function validateBundle(bundle) {
  if (!bundle.schema || bundle.schema !== SCHEMA) {
    throw new Error("Exported bundle has an invalid schema.");
  }
  if (!bundle.exam || !bundle.exam.problemSetId) {
    throw new Error("Exported bundle is missing problemSetId.");
  }
  if (bundle.integrity.problemCount <= 0) {
    throw new Error("No problems were exported.");
  }
  if (bundle.integrity.submissionCount !== bundle.integrity.submissionDetailCount) {
    throw new Error(
      `Incomplete submission details: ${bundle.integrity.submissionDetailCount}/${bundle.integrity.submissionCount}.`
    );
  }
  if (bundle.integrity.codeCount < bundle.integrity.submissionDetailCount) {
    throw new Error(`Incomplete code export: ${bundle.integrity.codeCount}/${bundle.integrity.submissionDetailCount}.`);
  }
}

function downloadBundle(bundle) {
  return new Promise((resolve, reject) => {
    const json = JSON.stringify(bundle, null, 2);
    const dataUrl = `data:application/json;charset=utf-8,${encodeURIComponent(json)}`;
    const filename = buildExportFileName(bundle, true);

    chrome.downloads.download({ url: dataUrl, filename, saveAs: true }, (downloadId) => {
      const error = chrome.runtime.lastError;
      if (error) {
        reject(new Error(error.message));
        return;
      }
      resolve(downloadId);
    });
  });
}

async function runExport(port, startOptions) {
  const tab = startOptions && startOptions.tabId ? await getTab(startOptions.tabId) : await getActiveTab();
  if (!tab || !tab.id || !tab.url) {
    throw new Error("No active Pintia tab was found.");
  }

  const problemSetId = getProblemSetId(tab.url);
  if (!problemSetId) {
    throw new Error("Current tab is not inside a Pintia problem set.");
  }
  if (activeTasks.has(tab.id)) {
    throw new Error("This tab is already exporting.");
  }

  activeTasks.set(tab.id, true);
  const originalUrl = tab.url;
  const origin = new URL(originalUrl).origin;
  const parts = {};

  try {
    send(port, { type: "progress", message: `Problem set ${problemSetId}` });

    for (const route of ROUTES) {
      const url = getRouteUrl(origin, problemSetId, route);
      send(port, { type: "progress", message: route.label });
      await navigateTab(tab.id, url);
      await delay(1500);
      parts[route.collector] = await collectRoute(tab.id, problemSetId, route.collector, undefined, port);
    }

    parts.submissions = await collectSubmissions(tab.id, origin, problemSetId, port);

    send(port, { type: "progress", message: "Restoring original page" });
    await restoreTab(tab.id, originalUrl);

    send(port, { type: "progress", message: "Validating bundle" });
    const bundle = buildBundle(problemSetId, originalUrl, parts);

    send(port, { type: "progress", message: "Downloading JSON" });
    await downloadBundle(bundle);

    send(port, {
      type: "done",
      integrity: bundle.integrity,
      filenameHint: buildExportFileName(bundle, false)
    });
  } catch (error) {
    await restoreTab(tab.id, originalUrl);
    throw error;
  } finally {
    activeTasks.delete(tab.id);
  }
}

chrome.runtime.onConnect.addListener((port) => {
  if (port.name !== "pintia-export") {
    return;
  }

  port.onMessage.addListener((message) => {
    if (!message || message.type !== "START_EXPORT") {
      return;
    }

    runExport(port, message).catch((error) => {
      send(port, { type: "error", error: error && error.message ? error.message : String(error) });
    });
  });
});
