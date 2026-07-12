import {
  DOWNLOAD_CANCEL_TIMEOUT_MS,
  DOWNLOAD_CLEANUP_TIMEOUT_MS,
  DOWNLOAD_TERMINAL_TIMEOUT_MS,
} from "../domain/limits";
import { OperationDeadlineError, withDeadline } from "./deadline";

export interface DownloadChangedEvent {
  addListener(listener: (delta: chrome.downloads.DownloadDelta) => void): void;
  removeListener(listener: (delta: chrome.downloads.DownloadDelta) => void): void;
}

export interface DownloadsApi {
  download(options: chrome.downloads.DownloadOptions): Promise<number>;
  cancel(downloadId: number): Promise<void>;
  search(query: chrome.downloads.DownloadQuery): Promise<chrome.downloads.DownloadItem[]>;
  removeFile(downloadId: number): Promise<void>;
  erase(query: chrome.downloads.DownloadQuery): Promise<number[]>;
  onChanged: DownloadChangedEvent;
}

export interface TerminalDownloadState {
  state: "complete" | "interrupted";
  reason?: string;
}

export interface DownloadLifecycle {
  started(downloadId: number): Promise<boolean>;
  cancelling(downloadId: number): Promise<boolean>;
  terminal(downloadId: number, state: TerminalDownloadState): Promise<boolean>;
}

export interface OwnedDownloadIdentity {
  downloadId: number;
  identity: string;
  filename: string;
}

class DownloadCleanupError extends AggregateError {
  constructor(errors: unknown[], message: string) {
    super(errors, message);
    this.name = "DownloadCleanupError";
  }
}

const ownedDownloadCleanups = new Map<string, Promise<void>>();

function exactOwnedFilename(actual: string, expected: string, identity: string): boolean {
  const normalized = actual.replaceAll("\\", "/");
  const basename = normalized.slice(normalized.lastIndexOf("/") + 1);
  return basename === expected && expected.includes(identity);
}

function exactOwnedItem(
  matches: chrome.downloads.DownloadItem[],
  owned: OwnedDownloadIdentity,
): chrome.downloads.DownloadItem {
  if (
    matches.length !== 1 ||
    matches[0]?.id !== owned.downloadId ||
    !exactOwnedFilename(matches[0].filename, owned.filename, owned.identity)
  ) {
    throw new Error(`Chrome download ${owned.downloadId} does not match its exact journal identity.`);
  }
  return matches[0];
}

function terminalFileAbsent(item: chrome.downloads.DownloadItem): boolean {
  return item.state !== "in_progress" && !item.exists;
}

function terminalStateFromItem(item: chrome.downloads.DownloadItem): TerminalDownloadState | null {
  if (item.state === "complete") {
    return { state: "complete" };
  }
  if (item.state === "interrupted") {
    return {
      state: "interrupted",
      ...(item.error === undefined ? {} : { reason: item.error }),
    };
  }
  return null;
}

function cleanupKey(owned: OwnedDownloadIdentity): string {
  return JSON.stringify([owned.downloadId, owned.identity, owned.filename]);
}

function terminalAfterCancellation(
  downloads: DownloadsApi,
  downloadId: number,
): Promise<chrome.downloads.DownloadItem> {
  return new Promise((resolve, reject) => {
    let settled = false;
    const finish = (result: chrome.downloads.DownloadItem | Error): void => {
      if (settled) {
        return;
      }
      settled = true;
      downloads.onChanged.removeListener(listener);
      result instanceof Error ? reject(result) : resolve(result);
    };
    const inspect = async (): Promise<void> => {
      const matches = await downloads.search({ id: downloadId });
      if (matches.length !== 1 || matches[0]?.id !== downloadId) {
        throw new Error(`Chrome download ${downloadId} disappeared during cancellation.`);
      }
      if (matches[0].state !== "in_progress") {
        finish(matches[0]);
      }
    };
    const listener = (delta: chrome.downloads.DownloadDelta): void => {
      if (delta.id === downloadId && delta.state?.current !== "in_progress") {
        void inspect().catch((error: unknown) => finish(
          error instanceof Error ? error : new Error(String(error)),
        ));
      }
    };
    downloads.onChanged.addListener(listener);
    void downloads.cancel(downloadId).then(inspect, async (error: unknown) => {
      try {
        const matches = await downloads.search({ id: downloadId });
        if (matches.length === 1 && matches[0]?.id === downloadId && matches[0].state !== "in_progress") {
          finish(matches[0]);
          return;
        }
        finish(error instanceof Error ? error : new Error(String(error)));
      } catch (searchError: unknown) {
        finish(searchError instanceof Error ? searchError : new Error(String(searchError)));
      }
    });
  });
}

async function discardOwnedDownloadWithinDeadline(
  downloads: DownloadsApi,
  owned: OwnedDownloadIdentity,
): Promise<void> {
  const matches = await withDeadline(
    downloads.search({ id: owned.downloadId }),
    DOWNLOAD_CANCEL_TIMEOUT_MS,
    `Owned Chrome download ${owned.downloadId} lookup`,
  );
  let item = exactOwnedItem(matches, owned);
  if (item.state === "in_progress") {
    item = await withDeadline(
      terminalAfterCancellation(downloads, owned.downloadId),
      DOWNLOAD_CANCEL_TIMEOUT_MS,
      `Owned Chrome download ${owned.downloadId} terminal cancellation`,
    );
  }
  if (item.exists) {
    try {
      await withDeadline(
        downloads.removeFile(owned.downloadId),
        DOWNLOAD_CANCEL_TIMEOUT_MS,
        `Owned Chrome download ${owned.downloadId} file removal`,
      );
    } catch (removeError: unknown) {
      try {
        item = exactOwnedItem(await withDeadline(
          downloads.search({ id: owned.downloadId }),
          DOWNLOAD_CANCEL_TIMEOUT_MS,
          `Owned Chrome download ${owned.downloadId} post-removal lookup`,
        ), owned);
      } catch (recheckError: unknown) {
        throw new AggregateError(
          [removeError, recheckError],
          `Owned Chrome download ${owned.downloadId} removal could not be verified.`,
        );
      }
      if (!terminalFileAbsent(item)) {
        throw removeError;
      }
    }
  }
  let eraseError: unknown;
  try {
    const erased = await withDeadline(
      downloads.erase({ id: owned.downloadId }),
      DOWNLOAD_CANCEL_TIMEOUT_MS,
      `Owned Chrome download ${owned.downloadId} history erasure`,
    );
    if (erased.includes(owned.downloadId)) {
      return;
    }
    eraseError = new Error(`Owned Chrome download ${owned.downloadId} history was not erased.`);
  } catch (error: unknown) {
    eraseError = error;
  }
  const remaining = await withDeadline(
    downloads.search({ id: owned.downloadId }),
    DOWNLOAD_CANCEL_TIMEOUT_MS,
    `Owned Chrome download ${owned.downloadId} post-erasure lookup`,
  );
  if (remaining.length > 0) {
    exactOwnedItem(remaining, owned);
    throw eraseError;
  }
}

export function discardOwnedDownload(
  downloads: DownloadsApi,
  owned: OwnedDownloadIdentity,
): Promise<void> {
  const key = cleanupKey(owned);
  const existing = ownedDownloadCleanups.get(key);
  if (existing !== undefined) {
    return existing;
  }
  const cleanup = withDeadline(
    discardOwnedDownloadWithinDeadline(downloads, owned),
    DOWNLOAD_CLEANUP_TIMEOUT_MS,
    `Owned Chrome download ${owned.downloadId} cleanup`,
  );
  ownedDownloadCleanups.set(key, cleanup);
  void cleanup.finally(() => {
    if (ownedDownloadCleanups.get(key) === cleanup) {
      ownedDownloadCleanups.delete(key);
    }
  }).catch(() => undefined);
  return cleanup;
}

function abortReason(signal: AbortSignal): Error {
  return signal.reason instanceof Error
    ? signal.reason
    : new DOMException("Download aborted.", "AbortError");
}

function validDownloadId(value: number): number {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error("Chrome returned an invalid download id.");
  }
  return value;
}

export async function downloadAndWaitForCompletion(
  downloads: DownloadsApi,
  options: chrome.downloads.DownloadOptions,
  identity: string,
  signal: AbortSignal,
  lifecycle: DownloadLifecycle,
  timeoutMs = DOWNLOAD_TERMINAL_TIMEOUT_MS,
): Promise<number> {
  const startedAt = Date.now();
  let expectedId: number | undefined;
  let abandoned: unknown;
  let terminalObserved = false;
  let cleanupPromise: Promise<void> | null = null;
  let resolveTerminal: ((state: TerminalDownloadState) => void) | undefined;
  const terminal = new Promise<TerminalDownloadState>((resolve) => {
    resolveTerminal = resolve;
  });

  const listener = (delta: chrome.downloads.DownloadDelta): void => {
    const state = delta.state?.current;
    if (state !== "complete" && state !== "interrupted") {
      return;
    }
    const terminalState: TerminalDownloadState = state === "complete"
      ? { state }
      : { state, ...(delta.error?.current === undefined ? {} : { reason: delta.error.current }) };
    if (expectedId !== undefined && delta.id === expectedId) {
      resolveTerminal?.(terminalState);
    }
  };

  const discard = (
    downloadId: number,
    reason: "ASCENDANY_CANCELLED" | "ASCENDANY_SUPERSEDED" | "ASCENDANY_INTERRUPTED",
    markCancelling: boolean,
  ): Promise<void> => {
    if (cleanupPromise !== null) {
      return cleanupPromise;
    }
    cleanupPromise = (async () => {
      const errors: unknown[] = [];
      if (markCancelling) {
        try {
          await lifecycle.cancelling(downloadId);
        } catch (error: unknown) {
          errors.push(error);
        }
      }
      try {
        await discardOwnedDownload(downloads, {
          downloadId,
          identity,
          filename: options.filename ?? "",
        });
      } catch (error: unknown) {
        errors.push(error);
      }
      try {
        await lifecycle.terminal(downloadId, { state: "interrupted", reason });
      } catch (error: unknown) {
        errors.push(error);
      }
      if (errors.length > 0) {
        throw new DownloadCleanupError(errors, `Chrome download ${downloadId} cleanup failed.`);
      }
    })();
    return cleanupPromise;
  };

  downloads.onChanged.addListener(listener);
  const observedStart = downloads.download(options).then(async (rawId) => {
    const downloadId = validDownloadId(rawId);
    expectedId = downloadId;
    let accepted: boolean;
    try {
      accepted = await lifecycle.started(downloadId);
    } catch (startError: unknown) {
      try {
        await discard(downloadId, "ASCENDANY_SUPERSEDED", false);
      } catch (cleanupError: unknown) {
        throw new DownloadCleanupError(
          [startError, cleanupError],
          `Late Chrome download ${downloadId} could not be bound or cleaned.`,
        );
      }
      throw startError;
    }
    if (!accepted) {
      await discard(downloadId, "ASCENDANY_SUPERSEDED", false);
      throw new Error("Late Chrome download id belongs to a superseded export generation.");
    }
    if (abandoned !== undefined || signal.aborted) {
      await discard(downloadId, "ASCENDANY_CANCELLED", true);
      throw abandoned ?? (signal.aborted
        ? abortReason(signal)
        : new Error("Chrome download start was abandoned."));
    }
    const item = exactOwnedItem(await withDeadline(
      downloads.search({ id: downloadId }),
      DOWNLOAD_CANCEL_TIMEOUT_MS,
      `Chrome download ${downloadId} initial state lookup`,
      signal,
    ), {
      downloadId,
      identity,
      filename: options.filename ?? "",
    });
    const earlyTerminal = terminalStateFromItem(item);
    if (earlyTerminal !== null) {
      resolveTerminal?.(earlyTerminal);
    }
    return downloadId;
  });
  void observedStart.catch((error: unknown) => {
    if (abandoned !== undefined && error instanceof DownloadCleanupError) {
      console.error("Late Chrome download settlement failed after its caller exited.", error);
    }
  });

  try {
    const downloadId = await withDeadline(
      observedStart,
      timeoutMs,
      "Chrome download start",
      signal,
    );
    const remaining = timeoutMs - (Date.now() - startedAt);
    if (remaining <= 0) {
      throw new OperationDeadlineError("Chrome download terminal state", timeoutMs);
    }
    const terminalState = await withDeadline(
      terminal,
      remaining,
      "Chrome download terminal state",
      signal,
    );
    const accepted = await lifecycle.terminal(downloadId, terminalState);
    if (!accepted) {
      terminalObserved = true;
      await discard(downloadId, "ASCENDANY_SUPERSEDED", false);
      throw new Error("Chrome download terminal state belongs to a superseded export generation.");
    }
    terminalObserved = true;
    if (terminalState.state === "interrupted") {
      await discard(downloadId, "ASCENDANY_INTERRUPTED", false);
      throw new Error(
        `Download ${downloadId} was interrupted: ${terminalState.reason ?? "UNKNOWN"}.`,
      );
    }
    return downloadId;
  } catch (error: unknown) {
    abandoned = error;
    if (expectedId !== undefined && !terminalObserved) {
      try {
        await discard(expectedId, "ASCENDANY_CANCELLED", true);
      } catch (cancelError: unknown) {
        throw new AggregateError(
          [error, cancelError],
          `Chrome download ${expectedId} failed and cancellation failed.`,
        );
      }
    }
    throw error;
  } finally {
    downloads.onChanged.removeListener(listener);
  }
}
