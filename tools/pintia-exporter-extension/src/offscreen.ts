import { MAX_SNAPSHOT_BYTES } from "./domain/limits";
import { readSnapshotFile } from "./platform/snapshot-opfs";

const REQUEST_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const urls = new Map<string, { fileName: string; url: string; size: number }>();
const revoked = new Map<string, string>();
let operationChain = Promise.resolve();

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function enqueue<T>(operation: () => Promise<T> | T): Promise<T> {
  const next = operationChain.then(operation, operation);
  operationChain = next.then(() => undefined, () => undefined);
  return next;
}

function requestIdentity(requestId: unknown, fileName: unknown): { requestId: string; fileName: string } {
  if (
    typeof requestId !== "string" ||
    !REQUEST_ID_PATTERN.test(requestId) ||
    fileName !== `${requestId}.json`
  ) {
    throw new Error("Snapshot Blob request identity is invalid.");
  }
  return { requestId, fileName };
}

chrome.runtime.onMessage.addListener((request: unknown, sender, sendResponse) => {
  if (sender.id !== chrome.runtime.id || typeof request !== "object" || request === null || Array.isArray(request)) {
    return false;
  }
  const message = request as Record<string, unknown>;
  if (message.type === "ASCENDANY_CREATE_SNAPSHOT_BLOB_V2") {
    const requestId = message.requestId;
    void enqueue(async () => {
      const identity = requestIdentity(requestId, message.fileName);
      if (
        typeof message.expectedBytes !== "number" ||
        !Number.isSafeInteger(message.expectedBytes) ||
        message.expectedBytes <= 0 ||
        message.expectedBytes > MAX_SNAPSHOT_BYTES
      ) {
        throw new Error("Snapshot Blob request violates the v2 byte contract.");
      }
      if (revoked.get(identity.requestId) === identity.fileName) {
        throw new Error("Snapshot Blob requestId was already revoked.");
      }
      const existing = urls.get(identity.requestId);
      if (existing !== undefined) {
        if (existing.fileName !== identity.fileName || existing.size !== message.expectedBytes) {
          throw new Error("Snapshot Blob requestId was reused with a different byte contract.");
        }
        return {
          ok: true,
          requestId: identity.requestId,
          fileName: existing.fileName,
          url: existing.url,
          size: existing.size,
        };
      }
      const file = await readSnapshotFile(identity.fileName, message.expectedBytes);
      const url = URL.createObjectURL(file);
      urls.set(identity.requestId, { fileName: identity.fileName, url, size: file.size });
      return {
        ok: true,
        requestId: identity.requestId,
        fileName: identity.fileName,
        url,
        size: file.size,
      };
    }).then(sendResponse, (error: unknown) => sendResponse({ ok: false, requestId, error: errorMessage(error) }));
    return true;
  }
  if (message.type === "ASCENDANY_REVOKE_SNAPSHOT_BLOB_V2") {
    const requestId = message.requestId;
    void enqueue(() => {
      const identity = requestIdentity(requestId, message.fileName);
      const owned = urls.get(identity.requestId);
      if (owned !== undefined) {
        if (owned.fileName !== identity.fileName) {
          throw new Error("Snapshot Blob revocation fileName differs from its requestId.");
        }
        URL.revokeObjectURL(owned.url);
        urls.delete(identity.requestId);
      }
      revoked.set(identity.requestId, identity.fileName);
      return { ok: true, requestId: identity.requestId, fileName: identity.fileName };
    }).then(sendResponse, (error: unknown) => sendResponse({ ok: false, requestId, error: errorMessage(error) }));
    return true;
  }
  if (message.type === "ASCENDANY_RECONCILE_SNAPSHOT_BLOBS_V2") {
    void enqueue(() => {
      const revokedCount = urls.size;
      for (const owned of urls.values()) {
        URL.revokeObjectURL(owned.url);
      }
      urls.clear();
      revoked.clear();
      return { ok: true, revokedCount };
    }).then(sendResponse, (error: unknown) => sendResponse({ ok: false, error: errorMessage(error) }));
    return true;
  }
  return false;
});
