import { afterEach, describe, expect, it, vi } from "vitest";
import { EXPORT_LIMITS } from "../src/domain/limits";
import {
  EXPORT_COORDINATION_JOURNAL_ID,
  EXPORT_TASK_FORMAT_VERSION,
  type CollectorRequest,
  type ExportCoordinationJournal,
} from "../src/domain/types";
import {
  ChromeExportCoordinatorRuntime,
  PintiaCollectorError,
} from "../src/platform/chrome-export-runtime";
import type { DownloadChangedEvent, DownloadsApi } from "../src/platform/download";

class ChangedEvent implements DownloadChangedEvent {
  addListener(_listener: (delta: chrome.downloads.DownloadDelta) => void): void {}
  removeListener(_listener: (delta: chrome.downloads.DownloadDelta) => void): void {}
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function journal(state: "in_progress" | "complete"): ExportCoordinationJournal {
  return {
    id: EXPORT_COORDINATION_JOURNAL_ID,
    formatVersion: EXPORT_TASK_FORMAT_VERSION,
    generation: "generation-a",
    taskId: "task-a",
    problemSetId: "900",
    state: "recovering",
    acquiredAtMs: 1,
    updatedAtMs: 2,
    navigation: null,
    collector: null,
    resources: {
      blob: null,
      download: {
        identity: "identity-a",
        filename: "snapshot-identity-a.json",
        downloadId: 77,
        unsafeUntilMs: 3,
        state,
      },
    },
    recovery: { recoveryId: "recovery-a", claimedAtMs: 2, unsafeUntilMs: 100 },
    finalError: null,
  };
}

function downloadsApi(): DownloadsApi {
  return {
    download: vi.fn(async () => 77),
    cancel: vi.fn(async () => undefined),
    search: vi.fn(async () => []),
    removeFile: vi.fn(async () => undefined),
    erase: vi.fn(async () => []),
    onChanged: new ChangedEvent(),
  };
}

function collectorRequest(): CollectorRequest {
  return {
    type: "ASCENDANY_COLLECT_PINTIA_ROUTE_V2",
    problemSetId: "900",
    collector: "submission-details",
    submissionIds: ["submission-alpha"],
    limits: EXPORT_LIMITS,
  };
}

function installCollectorExecution(result: unknown): void {
  vi.stubGlobal("chrome", {
    runtime: { getPlatformInfo: vi.fn(async () => ({ os: "linux" })) },
    scripting: {
      executeScript: vi.fn(async () => [{ frameId: 0, result }]),
    },
  });
}

describe("Chrome MAIN-world collector contract", () => {
  it("returns a result only from a valid success response", async () => {
    installCollectorExecution({
      ok: true,
      collector: "submission-details",
      result: { items: [] },
    });
    const runtime = new ChromeExportCoordinatorRuntime(downloadsApi());

    await expect(runtime.executeCollector(7, collectorRequest())).resolves.toEqual({ items: [] });
  });

  it("throws a typed error for a structured rate-limit failure", async () => {
    installCollectorExecution({
      ok: false,
      collector: "submission-details",
      failure: {
        kind: "rate_limited",
        status: 429,
        message: "Pintia API request was rate limited (HTTP 429).",
      },
    });
    const runtime = new ChromeExportCoordinatorRuntime(downloadsApi());

    const failure = await runtime.executeCollector(7, collectorRequest()).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(PintiaCollectorError);
    expect(failure).toMatchObject({
      name: "PintiaCollectorError",
      collector: "submission-details",
      failure: {
        kind: "rate_limited",
        status: 429,
        message: "Pintia API request was rate limited (HTTP 429).",
      },
    });
  });

  it("throws the same typed boundary error for an ordinary collector failure", async () => {
    installCollectorExecution({
      ok: false,
      collector: "submission-details",
      failure: {
        kind: "collector",
        message: "synthetic ordinary failure",
      },
    });
    const runtime = new ChromeExportCoordinatorRuntime(downloadsApi());

    await expect(runtime.executeCollector(7, collectorRequest())).rejects.toMatchObject({
      name: "PintiaCollectorError",
      failure: {
        kind: "collector",
        message: "synthetic ordinary failure",
      },
    });
  });

  it("rejects malformed or mismatched failure responses before they cross into background", async () => {
    installCollectorExecution({
      ok: false,
      collector: "submission-details",
      failure: {
        kind: "rate_limited",
        status: 503,
        message: "invalid discriminator and status pair",
      },
    });
    const runtime = new ChromeExportCoordinatorRuntime(downloadsApi());

    await expect(runtime.executeCollector(7, collectorRequest())).rejects.toThrow(
      "Pintia submission-details collector returned an invalid typed response.",
    );
  });
});

describe("Chrome resource recovery", () => {
  it("ticks for the whole owned operation and stops immediately after it settles", async () => {
    vi.useFakeTimers();
    const getPlatformInfo = vi.fn(async () => ({ os: "linux" }));
    vi.stubGlobal("chrome", { runtime: { getPlatformInfo } });
    const downloads: DownloadsApi = {
      download: vi.fn(async () => 77),
      cancel: vi.fn(async () => undefined),
      search: vi.fn(async () => []),
      removeFile: vi.fn(async () => undefined),
      erase: vi.fn(async () => []),
      onChanged: new ChangedEvent(),
    };
    const runtime = new ChromeExportCoordinatorRuntime(downloads);
    let finishOperation: (() => void) | undefined;
    const operation = new Promise<void>((resolve) => { finishOperation = resolve; });

    const keptAlive = runtime.keepServiceWorkerAlive(operation);
    expect(getPlatformInfo).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(getPlatformInfo).toHaveBeenCalledTimes(4);

    finishOperation?.();
    await keptAlive;
    await vi.advanceTimersByTimeAsync(40_000);
    expect(getPlatformInfo).toHaveBeenCalledTimes(4);
  });

  it("deletes a completed file/history when its durable journal was nonterminal", async () => {
    const downloads: DownloadsApi = {
      download: vi.fn(async () => 77),
      cancel: vi.fn(async () => undefined),
      search: vi.fn(async () => [{
        id: 77,
        filename: "/downloads/snapshot-identity-a.json",
        state: "complete",
        exists: true,
      } as chrome.downloads.DownloadItem]),
      removeFile: vi.fn(async () => undefined),
      erase: vi.fn(async () => [77]),
      onChanged: new ChangedEvent(),
    };
    const runtime = new ChromeExportCoordinatorRuntime(downloads);

    await runtime.recoverResources(journal("in_progress"));

    expect(downloads.removeFile).toHaveBeenCalledWith(77);
    expect(downloads.erase).toHaveBeenCalledWith({ id: 77 });
    expect(downloads.cancel).not.toHaveBeenCalled();
  });

  it("preserves a normally completed owned download", async () => {
    const downloads: DownloadsApi = {
      download: vi.fn(async () => 77),
      cancel: vi.fn(async () => undefined),
      search: vi.fn(async () => []),
      removeFile: vi.fn(async () => undefined),
      erase: vi.fn(async () => []),
      onChanged: new ChangedEvent(),
    };
    const runtime = new ChromeExportCoordinatorRuntime(downloads);

    await runtime.recoverResources(journal("complete"));

    expect(downloads.search).not.toHaveBeenCalled();
    expect(downloads.removeFile).not.toHaveBeenCalled();
    expect(downloads.erase).not.toHaveBeenCalled();
  });
});
