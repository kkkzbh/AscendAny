import { afterEach, describe, expect, it, vi } from "vitest";
import { DOWNLOAD_CANCEL_TIMEOUT_MS } from "../src/domain/limits";
import {
  discardOwnedDownload,
  downloadAndWaitForCompletion,
  type DownloadChangedEvent,
  type DownloadLifecycle,
  type DownloadsApi,
} from "../src/platform/download";

class FakeChangedEvent implements DownloadChangedEvent {
  readonly listeners = new Set<(delta: chrome.downloads.DownloadDelta) => void>();

  addListener(listener: (delta: chrome.downloads.DownloadDelta) => void): void {
    this.listeners.add(listener);
  }

  removeListener(listener: (delta: chrome.downloads.DownloadDelta) => void): void {
    this.listeners.delete(listener);
  }

  emit(delta: chrome.downloads.DownloadDelta): void {
    this.listeners.forEach((listener) => listener(delta));
  }
}

function options(): chrome.downloads.DownloadOptions {
  return { url: "blob:test", filename: "snapshot-identity.json", saveAs: false };
}

function lifecycle(startAccepted = true): DownloadLifecycle {
  return {
    started: vi.fn(async () => startAccepted),
    cancelling: vi.fn(async () => startAccepted),
    terminal: vi.fn(async () => startAccepted),
  };
}

function api(
  changed: FakeChangedEvent,
  download: DownloadsApi["download"],
  cancel: DownloadsApi["cancel"] = vi.fn(async () => undefined),
): DownloadsApi {
  return {
    download,
    cancel,
    search: vi.fn(async (query) => typeof query.id === "number" ? [{
      id: query.id,
      filename: "/tmp/snapshot-identity.json",
      state: "in_progress",
      exists: true,
    } as chrome.downloads.DownloadItem] : []),
    removeFile: vi.fn(async () => undefined),
    erase: vi.fn(async (query) => typeof query.id === "number" ? [query.id] : []),
    onChanged: changed,
  };
}

afterEach(() => vi.useRealTimers());

describe("journaled Chrome download lifecycle", () => {
  it("binds an early terminal event to the exact late start id", async () => {
    const changed = new FakeChangedEvent();
    let resolveStart: ((id: number) => void) | undefined;
    const downloads = api(changed, vi.fn(() => new Promise<number>((resolve) => { resolveStart = resolve; })));
    vi.mocked(downloads.search).mockResolvedValue([{
      id: 41,
      filename: "/tmp/snapshot-identity.json",
      state: "complete",
      exists: true,
    } as chrome.downloads.DownloadItem]);
    const journal = lifecycle();
    const completed = downloadAndWaitForCompletion(
      downloads,
      options(),
      "identity",
      new AbortController().signal,
      journal,
    );

    changed.emit({ id: 41, state: { current: "complete" } });
    resolveStart?.(41);

    await expect(completed).resolves.toBe(41);
    expect(journal.started).toHaveBeenCalledWith(41);
    expect(journal.terminal).toHaveBeenCalledWith(41, { state: "complete" });
    expect(downloads.cancel).not.toHaveBeenCalled();
  });

  it("physically cancels a late id even when its generation CAS is rejected", async () => {
    const changed = new FakeChangedEvent();
    let resolveStart: ((id: number) => void) | undefined;
    const downloads = api(changed, vi.fn(() => new Promise<number>((resolve) => { resolveStart = resolve; })));
    vi.mocked(downloads.search).mockResolvedValueOnce([{
      id: 52,
      filename: "/tmp/snapshot-identity.json",
      state: "in_progress",
      exists: true,
    } as chrome.downloads.DownloadItem]).mockResolvedValueOnce([{
      id: 52,
      filename: "/tmp/snapshot-identity.json",
      state: "interrupted",
      exists: false,
    } as chrome.downloads.DownloadItem]);
    const journal = lifecycle(false);
    const controller = new AbortController();
    const completed = downloadAndWaitForCompletion(
      downloads,
      options(),
      "identity",
      controller.signal,
      journal,
      1_000,
    );
    controller.abort(new Error("generation recovered"));
    await expect(completed).rejects.toThrow("generation recovered");

    resolveStart?.(52);
    await vi.waitFor(() => expect(downloads.cancel).toHaveBeenCalledWith(52));
    expect(journal.started).toHaveBeenCalledWith(52);
  });

  it("removes an already-complete late file and erases its Chrome history", async () => {
    const changed = new FakeChangedEvent();
    let resolveStart: ((id: number) => void) | undefined;
    const downloads = api(changed, vi.fn(() => new Promise<number>((resolve) => { resolveStart = resolve; })));
    vi.mocked(downloads.search).mockResolvedValue([{
      id: 53,
      filename: "/tmp/snapshot-identity.json",
      state: "complete",
      exists: true,
    } as chrome.downloads.DownloadItem]);
    const controller = new AbortController();
    const completed = downloadAndWaitForCompletion(
      downloads,
      options(),
      "identity",
      controller.signal,
      lifecycle(false),
      1_000,
    );
    controller.abort(new Error("generation recovered"));
    await expect(completed).rejects.toThrow("generation recovered");

    resolveStart?.(53);
    await vi.waitFor(() => expect(downloads.removeFile).toHaveBeenCalledWith(53));
    expect(downloads.erase).toHaveBeenCalledWith({ id: 53 });
    expect(downloads.cancel).not.toHaveBeenCalled();
  });

  it("re-searches and removes a file that completes while cancellation is racing", async () => {
    const changed = new FakeChangedEvent();
    const downloads = api(
      changed,
      vi.fn(async () => 54),
      vi.fn(async () => { throw new Error("Download is not in progress"); }),
    );
    vi.mocked(downloads.search).mockResolvedValueOnce([{
      id: 54,
      filename: "/tmp/snapshot-identity.json",
      state: "in_progress",
      exists: true,
    } as chrome.downloads.DownloadItem]).mockResolvedValueOnce([{
      id: 54,
      filename: "/tmp/snapshot-identity.json",
      state: "complete",
      exists: true,
    } as chrome.downloads.DownloadItem]);

    await discardOwnedDownload(downloads, {
      downloadId: 54,
      identity: "identity",
      filename: "snapshot-identity.json",
    });

    expect(downloads.removeFile).toHaveBeenCalledWith(54);
    expect(downloads.erase).toHaveBeenCalledWith({ id: 54 });
  });

  it("accepts a removeFile race only after the exact terminal item reports no file", async () => {
    const changed = new FakeChangedEvent();
    const downloads = api(changed, vi.fn(async () => 56));
    vi.mocked(downloads.search).mockResolvedValueOnce([{
      id: 56,
      filename: "/tmp/snapshot-identity.json",
      state: "complete",
      exists: true,
    } as chrome.downloads.DownloadItem]).mockResolvedValueOnce([{
      id: 56,
      filename: "/tmp/snapshot-identity.json",
      state: "complete",
      exists: false,
    } as chrome.downloads.DownloadItem]);
    vi.mocked(downloads.removeFile).mockRejectedValue(new Error("FILE_NOT_FOUND"));

    await expect(discardOwnedDownload(downloads, {
      downloadId: 56,
      identity: "identity",
      filename: "snapshot-identity.json",
    })).resolves.toBeUndefined();

    expect(downloads.search).toHaveBeenCalledTimes(2);
    expect(downloads.erase).toHaveBeenCalledWith({ id: 56 });
  });

  it("singleflights concurrent cleanup of the same exact download identity", async () => {
    const changed = new FakeChangedEvent();
    let finishRemoval: (() => void) | undefined;
    const downloads = api(changed, vi.fn(async () => 57));
    vi.mocked(downloads.search).mockResolvedValue([{
      id: 57,
      filename: "/tmp/snapshot-identity-single.json",
      state: "complete",
      exists: true,
    } as chrome.downloads.DownloadItem]);
    vi.mocked(downloads.removeFile).mockImplementation(() => new Promise<void>((resolve) => {
      finishRemoval = resolve;
    }));
    const owned = {
      downloadId: 57,
      identity: "identity-single",
      filename: "snapshot-identity-single.json",
    };

    const first = discardOwnedDownload(downloads, owned);
    const second = discardOwnedDownload(downloads, owned);
    expect(second).toBe(first);
    await vi.waitFor(() => expect(downloads.removeFile).toHaveBeenCalledOnce());
    finishRemoval?.();
    await Promise.all([first, second]);

    expect(downloads.search).toHaveBeenCalledOnce();
    expect(downloads.removeFile).toHaveBeenCalledOnce();
    expect(downloads.erase).toHaveBeenCalledOnce();
  });

  it("removes a completed file when its terminal generation transition is rejected", async () => {
    const changed = new FakeChangedEvent();
    const downloads = api(changed, vi.fn(async () => 55));
    vi.mocked(downloads.search).mockResolvedValueOnce([{
      id: 55,
      filename: "/tmp/snapshot-identity.json",
      state: "in_progress",
      exists: true,
    } as chrome.downloads.DownloadItem]).mockResolvedValue([{
      id: 55,
      filename: "/tmp/snapshot-identity.json",
      state: "complete",
      exists: true,
    } as chrome.downloads.DownloadItem]);
    const journal = lifecycle();
    vi.mocked(journal.terminal).mockResolvedValue(false);
    const completed = downloadAndWaitForCompletion(
      downloads,
      options(),
      "identity",
      new AbortController().signal,
      journal,
    );
    await vi.waitFor(() => expect(journal.started).toHaveBeenCalledWith(55));
    changed.emit({ id: 55, state: { current: "complete" } });

    await expect(completed).rejects.toThrow("terminal state belongs to a superseded export generation");
    expect(downloads.removeFile).toHaveBeenCalledWith(55);
    expect(downloads.erase).toHaveBeenCalledWith({ id: 55 });
    expect(downloads.cancel).not.toHaveBeenCalled();
  });

  it("records a natural interruption without issuing a second cancellation", async () => {
    const changed = new FakeChangedEvent();
    const downloads = api(changed, vi.fn(async () => 63));
    vi.mocked(downloads.search).mockResolvedValueOnce([{
      id: 63,
      filename: "/tmp/snapshot-identity.json",
      state: "in_progress",
      exists: true,
    } as chrome.downloads.DownloadItem]).mockResolvedValue([{
      id: 63,
      filename: "/tmp/snapshot-identity.json",
      state: "interrupted",
      exists: false,
    } as chrome.downloads.DownloadItem]);
    const journal = lifecycle();
    const completed = downloadAndWaitForCompletion(
      downloads,
      options(),
      "identity",
      new AbortController().signal,
      journal,
    );
    await Promise.resolve();
    changed.emit({
      id: 63,
      state: { current: "interrupted" },
      error: { current: "NETWORK_FAILED" },
    });

    await expect(completed).rejects.toThrow("Download 63 was interrupted: NETWORK_FAILED.");
    expect(downloads.cancel).not.toHaveBeenCalled();
    expect(downloads.erase).toHaveBeenCalledWith({ id: 63 });
  });

  it("leaves a never-settling start without inventing an id", async () => {
    vi.useFakeTimers();
    const changed = new FakeChangedEvent();
    const downloads = api(changed, vi.fn(async () => new Promise<number>(() => undefined)));
    const completed = downloadAndWaitForCompletion(
      downloads,
      options(),
      "identity",
      new AbortController().signal,
      lifecycle(),
      1_000,
    );
    const rejected = expect(completed).rejects.toThrow("Chrome download start exceeded");
    await vi.advanceTimersByTimeAsync(1_000);
    await rejected;
    expect(downloads.cancel).not.toHaveBeenCalled();
  });

  it("bounds physical cancellation after a terminal deadline", async () => {
    vi.useFakeTimers();
    const changed = new FakeChangedEvent();
    const downloads = api(
      changed,
      vi.fn(async () => 75),
      vi.fn(async () => new Promise<void>(() => undefined)),
    );
    vi.mocked(downloads.search).mockResolvedValue([{
      id: 75,
      filename: "/tmp/snapshot-identity.json",
      state: "in_progress",
      exists: true,
    } as chrome.downloads.DownloadItem]);
    const completed = downloadAndWaitForCompletion(
      downloads,
      options(),
      "identity",
      new AbortController().signal,
      lifecycle(),
      1_000,
    );
    const rejected = expect(completed).rejects.toThrow("failed and cancellation failed");
    await vi.advanceTimersByTimeAsync(1_000 + DOWNLOAD_CANCEL_TIMEOUT_MS);
    await rejected;
  });
});
