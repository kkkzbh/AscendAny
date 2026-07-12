import { utf8ByteLength } from "../domain/limits";

export const SNAPSHOT_CHUNK_BYTES = 1024 * 1024;
export const SNAPSHOT_DIRECTORY = "ascendany-pintia-snapshot-delivery-v2";
const UUID_V4_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const SNAPSHOT_FILE_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}[.]json$/;

export interface ByteSink {
  write(bytes: Uint8Array<ArrayBuffer>): Promise<void>;
}

interface IterableDirectoryHandle extends FileSystemDirectoryHandle {
  entries(): AsyncIterableIterator<[string, FileSystemHandle]>;
}

function codePointWidth(value: string, index: number): { codeUnits: number; bytes: number } {
  const first = value.charCodeAt(index);
  if (first <= 0x7f) {
    return { codeUnits: 1, bytes: 1 };
  }
  if (first <= 0x7ff) {
    return { codeUnits: 1, bytes: 2 };
  }
  if (first >= 0xd800 && first <= 0xdbff) {
    const second = value.charCodeAt(index + 1);
    if (second >= 0xdc00 && second <= 0xdfff) {
      return { codeUnits: 2, bytes: 4 };
    }
  }
  return { codeUnits: 1, bytes: 3 };
}

export async function writeUtf8Chunks(
  value: string,
  sink: ByteSink,
  maximumChunkBytes = SNAPSHOT_CHUNK_BYTES,
  signal?: AbortSignal,
): Promise<{ bytes: number; chunks: number }> {
  if (!Number.isSafeInteger(maximumChunkBytes) || maximumChunkBytes < 4) {
    throw new Error("Snapshot chunk size must be a safe integer of at least four bytes.");
  }
  const encoder = new TextEncoder();
  let start = 0;
  let totalBytes = 0;
  let chunks = 0;
  while (start < value.length) {
    signal?.throwIfAborted();
    let end = start;
    let chunkBytes = 0;
    while (end < value.length) {
      const width = codePointWidth(value, end);
      if (chunkBytes > 0 && chunkBytes + width.bytes > maximumChunkBytes) {
        break;
      }
      chunkBytes += width.bytes;
      end += width.codeUnits;
    }
    const encoded = encoder.encode(value.slice(start, end)) as Uint8Array<ArrayBuffer>;
    if (encoded.byteLength !== chunkBytes || encoded.byteLength > maximumChunkBytes) {
      throw new Error("Snapshot UTF-8 chunk accounting is inconsistent.");
    }
    await sink.write(encoded);
    signal?.throwIfAborted();
    totalBytes += encoded.byteLength;
    chunks += 1;
    start = end;
  }
  if (totalBytes !== utf8ByteLength(value)) {
    throw new Error("Snapshot UTF-8 stream size differs from preflight accounting.");
  }
  return { bytes: totalBytes, chunks };
}

export function snapshotFileName(requestId: string): string {
  if (!UUID_V4_PATTERN.test(requestId)) {
    throw new Error("Snapshot OPFS requestId is invalid.");
  }
  return `${requestId}.json`;
}

function assertSnapshotFileName(fileName: string): void {
  if (!SNAPSHOT_FILE_PATTERN.test(fileName)) {
    throw new Error(`Snapshot OPFS filename is invalid: ${fileName}.`);
  }
}

async function snapshotDirectory(): Promise<FileSystemDirectoryHandle> {
  const root = await navigator.storage.getDirectory();
  return root.getDirectoryHandle(SNAPSHOT_DIRECTORY, { create: true });
}

export async function writeSnapshotFile(
  requestId: string,
  json: string,
  expectedBytes: number,
  signal: AbortSignal,
): Promise<{ fileName: string; size: number; chunks: number }> {
  signal.throwIfAborted();
  const fileName = snapshotFileName(requestId);
  if (!Number.isSafeInteger(expectedBytes) || expectedBytes <= 0 || utf8ByteLength(json) !== expectedBytes) {
    throw new Error("Snapshot OPFS preflight byte count is invalid.");
  }
  const directory = await snapshotDirectory();
  const handle = await directory.getFileHandle(fileName, { create: true });
  const writable = await handle.createWritable({ keepExistingData: false });
  let writableOpen = true;
  try {
    const streamed = await writeUtf8Chunks(json, {
      write: (bytes) => writable.write(bytes),
    }, SNAPSHOT_CHUNK_BYTES, signal);
    if (streamed.bytes !== expectedBytes) {
      throw new Error("Snapshot OPFS stream differs from the expected byte count.");
    }
    await writable.close();
    writableOpen = false;
    const file = await handle.getFile();
    if (file.size !== expectedBytes) {
      throw new Error("Snapshot OPFS file differs from the expected byte count.");
    }
    return { fileName, size: file.size, chunks: streamed.chunks };
  } catch (error: unknown) {
    const cleanupErrors: unknown[] = [];
    if (writableOpen) {
      try {
        await writable.abort();
      } catch (abortError: unknown) {
        cleanupErrors.push(abortError);
      }
    }
    try {
      await directory.removeEntry(fileName);
    } catch (removalError: unknown) {
      cleanupErrors.push(removalError);
    }
    if (cleanupErrors.length > 0) {
      throw new AggregateError(
        [error, ...cleanupErrors],
        "Snapshot OPFS write and cleanup failed.",
      );
    }
    throw error;
  }
}

export async function readSnapshotFile(fileName: string, expectedBytes: number): Promise<File> {
  assertSnapshotFileName(fileName);
  const directory = await snapshotDirectory();
  const handle = await directory.getFileHandle(fileName);
  const file = await handle.getFile();
  if (file.size !== expectedBytes) {
    throw new Error("Snapshot OPFS file size differs from its delivery handle.");
  }
  return file;
}

export async function removeSnapshotFile(fileName: string): Promise<void> {
  assertSnapshotFileName(fileName);
  const directory = await snapshotDirectory();
  try {
    const handle = await directory.getFileHandle(fileName);
    if (handle.kind !== "file" || handle.name !== fileName) {
      throw new Error(`Snapshot OPFS ownership mismatch for ${fileName}.`);
    }
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === "NotFoundError") {
      return;
    }
    throw error;
  }
  try {
    await directory.removeEntry(fileName);
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === "NotFoundError") {
      return;
    }
    throw error;
  }
}

export async function reconcileSnapshotDirectory(): Promise<string[]> {
  const directory = await snapshotDirectory();
  const orphanedFiles: string[] = [];
  for await (const [name, handle] of (directory as IterableDirectoryHandle).entries()) {
    if (handle.kind !== "file" || !SNAPSHOT_FILE_PATTERN.test(name)) {
      throw new Error(`Snapshot OPFS directory contains an unexpected entry: ${name}.`);
    }
    orphanedFiles.push(name);
  }
  orphanedFiles.sort();
  for (const fileName of orphanedFiles) {
    await directory.removeEntry(fileName);
  }
  return orphanedFiles;
}
