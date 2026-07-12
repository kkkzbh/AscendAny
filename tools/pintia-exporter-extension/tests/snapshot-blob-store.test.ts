import { describe, expect, it, vi } from "vitest";
import {
  ExtensionSnapshotBlobStore,
  type ExtensionSnapshotBlobStoreDependencies,
} from "../src/platform/snapshot-blob-store";

const REQUEST_ID = "550e8400-e29b-41d4-a716-446655440000";
const FILE_NAME = `${REQUEST_ID}.json`;

function dependencies(): ExtensionSnapshotBlobStoreDependencies {
  let offscreenPresent = false;
  return {
    hasOffscreenDocument: vi.fn(async () => offscreenPresent),
    createOffscreenDocument: vi.fn(async () => { offscreenPresent = true; }),
    closeOffscreenDocument: vi.fn(async () => { offscreenPresent = false; }),
    sendMessage: vi.fn(async (message) => {
      if (message.type === "ASCENDANY_CREATE_SNAPSHOT_BLOB_V2") {
        return {
          ok: true,
          requestId: REQUEST_ID,
          fileName: FILE_NAME,
          url: "blob:chrome-extension://test/snapshot",
          size: 2,
        };
      }
      if (message.type === "ASCENDANY_RECONCILE_SNAPSHOT_BLOBS_V2") {
        return { ok: true, revokedCount: 1 };
      }
      throw new Error("synthetic offscreen revoke failure");
    }),
    writeSnapshotFile: vi.fn(async () => ({ fileName: FILE_NAME, size: 2, chunks: 1 })),
    removeSnapshotFile: vi.fn(async () => undefined),
    reconcileSnapshotDirectory: vi.fn(async () => [FILE_NAME]),
  };
}

describe("extension snapshot Blob ownership", () => {
  it("removes ownership before a failed revoke so the next reconciliation can repair it", async () => {
    const platform = dependencies();
    const store = new ExtensionSnapshotBlobStore(platform);
    const handle = await store.create(REQUEST_ID, "{}", 2, new AbortController().signal);

    await expect(store.reconcile()).rejects.toThrow(
      "cannot run while a live delivery handle exists",
    );
    await expect(store.revoke(handle)).rejects.toThrow("Snapshot Blob cleanup failed");

    await expect(store.reconcile()).resolves.toEqual([FILE_NAME]);
    expect(platform.removeSnapshotFile).toHaveBeenCalledWith(FILE_NAME);
    expect(platform.reconcileSnapshotDirectory).toHaveBeenCalledOnce();
    expect(platform.closeOffscreenDocument).toHaveBeenCalledOnce();
  });

  it("reports both creation and file-cleanup failures", async () => {
    const platform = dependencies();
    vi.mocked(platform.sendMessage).mockRejectedValueOnce(new Error("create message failed"));
    vi.mocked(platform.removeSnapshotFile).mockRejectedValueOnce(new Error("remove failed"));
    const store = new ExtensionSnapshotBlobStore(platform);

    await expect(store.create(REQUEST_ID, "{}", 2, new AbortController().signal)).rejects.toThrow(
      "Snapshot Blob creation and file cleanup failed",
    );
  });

  it("allows recovery cleanup followed by repeated exact late-handle revocation", async () => {
    const platform = dependencies();
    vi.mocked(platform.sendMessage).mockImplementation(async (message) => {
      if (message.type === "ASCENDANY_CREATE_SNAPSHOT_BLOB_V2") {
        return {
          ok: true,
          requestId: REQUEST_ID,
          fileName: FILE_NAME,
          url: "blob:chrome-extension://test/snapshot",
          size: 2,
        };
      }
      if (message.type === "ASCENDANY_REVOKE_SNAPSHOT_BLOB_V2") {
        return { ok: true, requestId: REQUEST_ID, fileName: FILE_NAME };
      }
      return { ok: true, revokedCount: 1 };
    });
    const store = new ExtensionSnapshotBlobStore(platform);
    const handle = await store.create(REQUEST_ID, "{}", 2, new AbortController().signal);

    await expect(store.revoke(handle)).resolves.toBeUndefined();
    await expect(store.revoke(handle)).resolves.toBeUndefined();
    expect(platform.removeSnapshotFile).toHaveBeenCalledTimes(2);
  });
});
