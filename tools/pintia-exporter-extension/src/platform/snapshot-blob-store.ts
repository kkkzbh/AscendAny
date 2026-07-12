import { CHECKPOINT_OPERATION_TIMEOUT_MS } from "../domain/limits";
import { withDeadline } from "./deadline";
import {
  reconcileSnapshotDirectory,
  removeSnapshotFile,
  writeSnapshotFile,
} from "./snapshot-opfs";

export interface SnapshotBlobHandle {
  requestId: string;
  fileName: string;
  url: string;
  size: number;
}

export interface SnapshotBlobStore {
  create(
    requestId: string,
    json: string,
    expectedBytes: number,
    signal: AbortSignal,
  ): Promise<SnapshotBlobHandle>;
  revoke(handle: SnapshotBlobHandle): Promise<void>;
}

interface BlobResponse {
  ok: boolean;
  requestId?: string;
  fileName?: string;
  url?: string;
  size?: number;
  revokedCount?: number;
  error?: string;
}

export interface ExtensionSnapshotBlobStoreDependencies {
  hasOffscreenDocument(): Promise<boolean>;
  createOffscreenDocument(): Promise<void>;
  closeOffscreenDocument(): Promise<void>;
  sendMessage(message: Record<string, unknown>): Promise<unknown>;
  writeSnapshotFile(
    requestId: string,
    json: string,
    expectedBytes: number,
    signal: AbortSignal,
  ): Promise<{ fileName: string; size: number; chunks: number }>;
  removeSnapshotFile(fileName: string): Promise<void>;
  reconcileSnapshotDirectory(): Promise<string[]>;
}

function responseObject(value: unknown, operation: string): BlobResponse {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`Offscreen ${operation} returned an invalid response.`);
  }
  return value as BlobResponse;
}

async function cleanupAfterFailure(
  primaryError: unknown,
  cleanup: Promise<void>,
  message: string,
): Promise<never> {
  try {
    await cleanup;
  } catch (cleanupError: unknown) {
    throw new AggregateError([primaryError, cleanupError], message);
  }
  throw primaryError;
}

export class ExtensionSnapshotBlobStore implements SnapshotBlobStore {
  private offscreenReady: Promise<void> | null = null;
  private readonly liveHandles = new Map<string, SnapshotBlobHandle>();

  constructor(private readonly dependencies: ExtensionSnapshotBlobStoreDependencies) {}

  private ensureOffscreenDocument(signal: AbortSignal): Promise<void> {
    if (this.offscreenReady !== null) {
      return withDeadline(
        this.offscreenReady,
        CHECKPOINT_OPERATION_TIMEOUT_MS,
        "Offscreen document readiness",
        signal,
      );
    }
    const readiness = (async (): Promise<void> => {
      const present = await withDeadline(
        this.dependencies.hasOffscreenDocument(),
        CHECKPOINT_OPERATION_TIMEOUT_MS,
        "Offscreen document lookup",
        signal,
      );
      if (!present) {
        await withDeadline(
          this.dependencies.createOffscreenDocument(),
          CHECKPOINT_OPERATION_TIMEOUT_MS,
          "Offscreen document creation",
          signal,
        );
      }
    })();
    this.offscreenReady = readiness;
    void readiness.catch(() => {
      if (this.offscreenReady === readiness) {
        this.offscreenReady = null;
      }
    });
    return readiness;
  }

  async create(
    requestId: string,
    json: string,
    expectedBytes: number,
    signal: AbortSignal,
  ): Promise<SnapshotBlobHandle> {
    const owned = this.liveHandles.get(requestId);
    if (owned !== undefined) {
      if (owned.size !== expectedBytes) {
        throw new Error("Snapshot Blob requestId was reused with a different byte contract.");
      }
      return owned;
    }
    const file = await this.dependencies.writeSnapshotFile(requestId, json, expectedBytes, signal);
    try {
      await this.ensureOffscreenDocument(signal);
      const response = responseObject(await withDeadline(
        this.dependencies.sendMessage({
          type: "ASCENDANY_CREATE_SNAPSHOT_BLOB_V2",
          requestId,
          fileName: file.fileName,
          expectedBytes,
        }),
        CHECKPOINT_OPERATION_TIMEOUT_MS,
        "Offscreen Blob creation",
        signal,
      ), "Blob creation");
      if (
        !response.ok ||
        response.requestId !== requestId ||
        response.fileName !== file.fileName ||
        typeof response.url !== "string" ||
        response.url.length === 0 ||
        response.size !== expectedBytes
      ) {
        throw new Error(response.error ?? "Offscreen Blob creation violated the snapshot byte contract.");
      }
      signal.throwIfAborted();
      const handle = { requestId, fileName: file.fileName, url: response.url, size: response.size };
      this.liveHandles.set(requestId, handle);
      return handle;
    } catch (error: unknown) {
      return cleanupAfterFailure(
        error,
        withDeadline(
          this.dependencies.removeSnapshotFile(file.fileName),
          CHECKPOINT_OPERATION_TIMEOUT_MS,
          "Failed Blob file cleanup",
        ),
        "Snapshot Blob creation and file cleanup failed.",
      );
    }
  }

  async revoke(handle: SnapshotBlobHandle): Promise<void> {
    // Ownership ends before cleanup starts. A failed revoke can therefore be
    // repaired by the next exclusive reconciliation instead of poisoning the
    // in-memory handle set forever.
    this.liveHandles.delete(handle.requestId);
    let revocationError: unknown;
    try {
      const response = responseObject(await withDeadline(
        this.dependencies.sendMessage({
          type: "ASCENDANY_REVOKE_SNAPSHOT_BLOB_V2",
          requestId: handle.requestId,
          fileName: handle.fileName,
        }),
        CHECKPOINT_OPERATION_TIMEOUT_MS,
        "Offscreen Blob revocation",
      ), "Blob revocation");
      if (!response.ok || response.requestId !== handle.requestId || response.fileName !== handle.fileName) {
        throw new Error(response.error ?? "Offscreen Blob revocation failed.");
      }
    } catch (error: unknown) {
      revocationError = error;
    }
    let removalError: unknown;
    try {
      await withDeadline(
        this.dependencies.removeSnapshotFile(handle.fileName),
        CHECKPOINT_OPERATION_TIMEOUT_MS,
        "Snapshot Blob file removal",
      );
    } catch (error: unknown) {
      removalError = error;
    }
    if (revocationError !== undefined || removalError !== undefined) {
      throw new AggregateError(
        [revocationError, removalError].filter((error) => error !== undefined),
        "Snapshot Blob cleanup failed.",
      );
    }
  }

  async reconcile(signal?: AbortSignal): Promise<string[]> {
    if (this.liveHandles.size > 0) {
      throw new Error("Snapshot Blob reconciliation cannot run while a live delivery handle exists.");
    }
    const offscreenPresent = await withDeadline(
      this.dependencies.hasOffscreenDocument(),
      CHECKPOINT_OPERATION_TIMEOUT_MS,
      "Offscreen reconciliation lookup",
      signal,
    );
    if (offscreenPresent) {
      const response = responseObject(await withDeadline(
        this.dependencies.sendMessage({ type: "ASCENDANY_RECONCILE_SNAPSHOT_BLOBS_V2" }),
        CHECKPOINT_OPERATION_TIMEOUT_MS,
        "Offscreen Blob reconciliation",
        signal,
      ), "Blob reconciliation");
      if (
        !response.ok ||
        typeof response.revokedCount !== "number" ||
        !Number.isSafeInteger(response.revokedCount) ||
        response.revokedCount < 0
      ) {
        throw new Error(response.error ?? "Offscreen Blob reconciliation failed.");
      }
      await withDeadline(
        this.dependencies.closeOffscreenDocument(),
        CHECKPOINT_OPERATION_TIMEOUT_MS,
        "Offscreen document close",
        signal,
      );
      this.offscreenReady = null;
    }
    return withDeadline(
      this.dependencies.reconcileSnapshotDirectory(),
      CHECKPOINT_OPERATION_TIMEOUT_MS,
      "Snapshot directory reconciliation",
      signal,
    );
  }
}

const productionDependencies: ExtensionSnapshotBlobStoreDependencies = {
  hasOffscreenDocument: () => chrome.offscreen.hasDocument(),
  createOffscreenDocument: () => chrome.offscreen.createDocument({
    url: "offscreen.html",
    reasons: [chrome.offscreen.Reason.BLOBS],
    justification: "Create and retain a bounded snapshot Blob URL until Chrome finishes its download.",
  }),
  closeOffscreenDocument: () => chrome.offscreen.closeDocument(),
  sendMessage: (message) => chrome.runtime.sendMessage(message),
  writeSnapshotFile,
  removeSnapshotFile,
  reconcileSnapshotDirectory,
};

export const extensionSnapshotBlobStore = new ExtensionSnapshotBlobStore(productionDependencies);

export function reconcileExtensionSnapshotBlobStore(signal?: AbortSignal): Promise<string[]> {
  return extensionSnapshotBlobStore.reconcile(signal);
}
