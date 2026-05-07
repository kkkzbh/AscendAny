const BRIDGE_READY_EVENT = "ASCENDANY_PINTIA_BRIDGE_READY";
const BRIDGE_REQUEST_EVENT = "ASCENDANY_PINTIA_EXPORT_REQUEST";
const BRIDGE_RESPONSE_EVENT = "ASCENDANY_PINTIA_EXPORT_RESPONSE";

let bridgeInjected = false;

function injectBridge() {
  if (bridgeInjected) {
    return Promise.resolve();
  }

  bridgeInjected = true;
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("Timed out while loading page bridge.")), 10_000);
    const onReady = () => {
      clearTimeout(timeout);
      window.removeEventListener(BRIDGE_READY_EVENT, onReady);
      resolve();
    };

    window.addEventListener(BRIDGE_READY_EVENT, onReady);
    const script = document.createElement("script");
    script.src = chrome.runtime.getURL("page-bridge.js");
    script.async = false;
    script.onload = () => script.remove();
    script.onerror = () => {
      clearTimeout(timeout);
      window.removeEventListener(BRIDGE_READY_EVENT, onReady);
      reject(new Error("Failed to inject page bridge."));
    };
    (document.head || document.documentElement).appendChild(script);
  });
}

function requestCollector(problemSetId, collector, payload) {
  const requestId = `${Date.now()}-${Math.random().toString(16).slice(2)}`;

  return new Promise((resolve) => {
    const timeout = setTimeout(() => {
      window.removeEventListener("message", onMessage);
      resolve({ ok: false, error: `Timed out while collecting ${collector}.` });
    }, 15 * 60 * 1000);

    function onMessage(event) {
      if (event.source !== window) {
        return;
      }
      const data = event.data || {};
      if (data.type !== BRIDGE_RESPONSE_EVENT || data.requestId !== requestId) {
        return;
      }
      clearTimeout(timeout);
      window.removeEventListener("message", onMessage);
      resolve(data.payload);
    }

    window.addEventListener("message", onMessage);
    window.postMessage(
      {
        type: BRIDGE_REQUEST_EVENT,
        requestId,
        problemSetId,
        collector,
        payload
      },
      "*"
    );
  });
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (!message || message.type !== "ASCENDANY_COLLECT_PINTIA_ROUTE") {
    return false;
  }

  injectBridge()
    .then(() => requestCollector(message.problemSetId, message.collector, message.payload))
    .then((response) => sendResponse(response))
    .catch((error) => sendResponse({ ok: false, error: error && error.message ? error.message : String(error) }));

  return true;
});
