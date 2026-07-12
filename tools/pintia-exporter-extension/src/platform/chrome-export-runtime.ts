import type {
  CollectorFailure,
  CollectorName,
  CollectorRequest,
  CollectorResponse,
  ExportCoordinationJournal,
} from "../domain/types";
import { collectPintiaRouteInMainWorld } from "../main-world-collector";
import {
  discardOwnedDownload,
  downloadAndWaitForCompletion,
  type DownloadLifecycle,
  type DownloadsApi,
} from "./download";
import {
  exactUrlNavigationTarget,
  navigationReached,
  type PintiaNavigationTarget,
} from "./navigation";
import {
  extensionSnapshotBlobStore,
  reconcileExtensionSnapshotBlobStore,
  type SnapshotBlobHandle,
} from "./snapshot-blob-store";

export interface ExportCoordinatorRuntime {
  getTab(tabId: number): Promise<chrome.tabs.Tab>;
  navigateTab(tabId: number, target: PintiaNavigationTarget): Promise<void>;
  restoreTab(tabId: number, url: string): Promise<void>;
  executeCollector(tabId: number, request: CollectorRequest): Promise<unknown>;
  createBlob(
    requestId: string,
    json: string,
    expectedBytes: number,
    signal: AbortSignal,
  ): Promise<SnapshotBlobHandle>;
  revokeBlob(handle: SnapshotBlobHandle): Promise<void>;
  download(
    identity: string,
    options: chrome.downloads.DownloadOptions,
    signal: AbortSignal,
    lifecycle: DownloadLifecycle,
  ): Promise<number>;
  recoverResources(journal: ExportCoordinationJournal): Promise<void>;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function validFailureMessage(value: unknown): value is string {
  return typeof value === "string" &&
    value.length > 0 &&
    value.length <= 512 &&
    !/[\u0000-\u001f\u007f]/.test(value);
}

function collectorFailure(value: unknown): CollectorFailure | null {
  const candidate = record(value);
  if (candidate === null || !validFailureMessage(candidate.message)) {
    return null;
  }
  if (candidate.kind === "rate_limited" && candidate.status === 429) {
    return {
      kind: "rate_limited",
      status: 429,
      message: candidate.message,
    };
  }
  if (
    candidate.kind === "http" &&
    typeof candidate.status === "number" &&
    Number.isSafeInteger(candidate.status) &&
    candidate.status >= 100 &&
    candidate.status <= 599 &&
    candidate.status !== 429
  ) {
    return {
      kind: "http",
      status: candidate.status,
      message: candidate.message,
    };
  }
  return candidate.kind === "collector"
    ? { kind: "collector", message: candidate.message }
    : null;
}

function collectorResponse(value: unknown, collector: CollectorName): CollectorResponse | null {
  const response = record(value);
  if (response === null || response.collector !== collector || typeof response.ok !== "boolean") {
    return null;
  }
  if (response.ok) {
    return Object.hasOwn(response, "result")
      ? { ok: true, collector, result: response.result }
      : null;
  }
  const failure = collectorFailure(response.failure);
  return failure === null ? null : { ok: false, collector, failure };
}

export class PintiaCollectorError extends Error {
  constructor(
    readonly collector: CollectorName,
    readonly failure: CollectorFailure,
  ) {
    super(failure.message);
    this.name = "PintiaCollectorError";
  }
}

function keepAliveTick(): Promise<void> {
  return chrome.runtime.getPlatformInfo().then(() => undefined);
}

function scheduleKeepAliveTick(): void {
  void keepAliveTick().catch((error: unknown) => {
    console.error("AscendAny exporter keepalive tick failed.", error);
  });
}

function withKeepAlive<T>(promise: Promise<T>): Promise<T> {
  const interval = setInterval(scheduleKeepAliveTick, 20_000);
  scheduleKeepAliveTick();
  return promise.finally(() => clearInterval(interval));
}

function navigateChromeTab(
  tabId: number,
  target: PintiaNavigationTarget,
): Promise<void> {
  return new Promise((resolve, reject) => {
    let settled = false;
    const finish = (error?: Error): void => {
      if (settled) {
        return;
      }
      settled = true;
      chrome.tabs.onUpdated.removeListener(onUpdated);
      error === undefined ? resolve() : reject(error);
    };
    const onUpdated = (updatedTabId: number, change: { status?: string }, tab: chrome.tabs.Tab): void => {
      if (updatedTabId !== tabId || change.status !== "complete") {
        return;
      }
      if (!navigationReached(target, tab.url)) {
        finish(new Error(`Pintia tab completed at an unexpected URL: ${tab.url ?? "unknown"}.`));
        return;
      }
      finish();
    };
    chrome.tabs.onUpdated.addListener(onUpdated);
    void chrome.tabs.update(tabId, { url: target.requestedUrl }).then(
      (tab) => {
        if (tab?.status !== "complete") {
          return;
        }
        if (navigationReached(target, tab.url)) {
          finish();
        } else {
          finish(new Error(`Pintia tab completed at an unexpected URL: ${tab.url ?? "unknown"}.`));
        }
      },
      (error: unknown) => finish(new Error(`Failed to navigate Pintia tab: ${errorMessage(error)}`)),
    );
  });
}

function downloadIdentityMatches(item: chrome.downloads.DownloadItem, identity: string): boolean {
  return item.filename.replaceAll("\\", "/").split("/").at(-1)?.includes(identity) === true;
}

async function recoverDownload(
  downloads: DownloadsApi,
  journal: NonNullable<ExportCoordinationJournal["resources"]["download"]>,
): Promise<void> {
  if (journal.state === "complete") {
    return;
  }
  let downloadId = journal.downloadId;
  if (downloadId === null) {
    const matches = (await downloads.search({ query: [journal.identity] })).filter((item) => {
      const basename = item.filename.replaceAll("\\", "/").split("/").at(-1);
      return basename === journal.filename && downloadIdentityMatches(item, journal.identity);
    });
    if (matches.length > 1) {
      throw new Error(`Download recovery identity ${journal.identity} matched multiple Chrome downloads.`);
    }
    downloadId = matches[0]?.id ?? null;
  }
  if (downloadId !== null) {
    await discardOwnedDownload(downloads, {
      downloadId,
      identity: journal.identity,
      filename: journal.filename,
    });
  }
}

export class ChromeExportCoordinatorRuntime implements ExportCoordinatorRuntime {
  constructor(
    private readonly downloads: DownloadsApi,
  ) {}

  keepServiceWorkerAlive<T>(operation: Promise<T>): Promise<T> {
    return withKeepAlive(operation);
  }

  getTab(tabId: number): Promise<chrome.tabs.Tab> {
    return chrome.tabs.get(tabId);
  }

  navigateTab(tabId: number, target: PintiaNavigationTarget): Promise<void> {
    return navigateChromeTab(tabId, target);
  }

  async restoreTab(tabId: number, url: string): Promise<void> {
    const tab = await this.getTab(tabId);
    if (tab.url !== url) {
      await this.navigateTab(tabId, exactUrlNavigationTarget(url));
    }
  }

  async executeCollector(tabId: number, request: CollectorRequest): Promise<unknown> {
    const injections = await withKeepAlive(chrome.scripting.executeScript({
      target: { tabId, frameIds: [0] },
      world: "MAIN",
      func: collectPintiaRouteInMainWorld,
      args: [request],
    }));
    if (injections.length !== 1 || injections[0]?.frameId !== 0 || injections[0].result === undefined) {
      throw new Error(`Pintia ${request.collector} collector returned an invalid MAIN-world execution result.`);
    }
    const response = collectorResponse(injections[0].result, request.collector);
    if (response === null) {
      throw new Error(`Pintia ${request.collector} collector returned an invalid typed response.`);
    }
    if (!response.ok) {
      throw new PintiaCollectorError(request.collector, response.failure);
    }
    return response.result;
  }

  createBlob(
    requestId: string,
    json: string,
    expectedBytes: number,
    signal: AbortSignal,
  ): Promise<SnapshotBlobHandle> {
    return extensionSnapshotBlobStore.create(requestId, json, expectedBytes, signal);
  }

  revokeBlob(handle: SnapshotBlobHandle): Promise<void> {
    return extensionSnapshotBlobStore.revoke(handle);
  }

  download(
    identity: string,
    options: chrome.downloads.DownloadOptions,
    signal: AbortSignal,
    lifecycle: DownloadLifecycle,
  ): Promise<number> {
    return downloadAndWaitForCompletion(this.downloads, options, identity, signal, lifecycle);
  }

  async recoverResources(journal: ExportCoordinationJournal): Promise<void> {
    if (journal.resources.download !== null) {
      await recoverDownload(this.downloads, journal.resources.download);
    }
    if (journal.resources.blob !== null) {
      await reconcileExtensionSnapshotBlobStore();
    }
    if (journal.navigation !== null) {
      await this.restoreTab(
        journal.navigation.tabId,
        journal.navigation.recoveryUrl,
      );
    }
  }
}
