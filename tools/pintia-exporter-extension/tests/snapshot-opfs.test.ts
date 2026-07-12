import { afterEach, describe, expect, it, vi } from "vitest";
import { MAX_SNAPSHOT_BYTES, utf8ByteLength } from "../src/domain/limits";
import {
  SNAPSHOT_CHUNK_BYTES,
  SNAPSHOT_DIRECTORY,
  reconcileSnapshotDirectory,
  removeSnapshotFile,
  snapshotFileName,
  writeUtf8Chunks,
} from "../src/platform/snapshot-opfs";
import crashFixture from "./fixtures/synthetic-opfs-crash-state.json";

interface FakeEntry {
  name: string;
  kind: "file" | "directory";
}

class FakeSnapshotDirectory {
  readonly removed: string[] = [];

  constructor(private readonly sourceEntries: FakeEntry[]) {}

  async *entries(): AsyncIterableIterator<[string, FileSystemHandle]> {
    for (const entry of this.sourceEntries) {
      yield [entry.name, { kind: entry.kind } as FileSystemHandle];
    }
  }

  async removeEntry(name: string): Promise<void> {
    const index = this.sourceEntries.findIndex((entry) => entry.name === name);
    if (index < 0) {
      throw new DOMException(`Missing ${name}`, "NotFoundError");
    }
    this.sourceEntries.splice(index, 1);
    this.removed.push(name);
  }

  async getFileHandle(name: string): Promise<FileSystemFileHandle> {
    const entry = this.sourceEntries.find((candidate) => candidate.name === name && candidate.kind === "file");
    if (entry === undefined) {
      throw new DOMException(`Missing ${name}`, "NotFoundError");
    }
    return { kind: "file", name } as FileSystemFileHandle;
  }
}

function installOpfs(entries: FakeEntry[]): FakeSnapshotDirectory {
  const directory = new FakeSnapshotDirectory(entries);
  const getDirectoryHandle = vi.fn(async (name: string, options?: FileSystemGetDirectoryOptions) => {
    expect(name).toBe(SNAPSHOT_DIRECTORY);
    expect(options).toEqual({ create: true });
    return directory as unknown as FileSystemDirectoryHandle;
  });
  vi.stubGlobal("navigator", {
    storage: {
      getDirectory: vi.fn(async () => ({ getDirectoryHandle })),
    },
  });
  return directory;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("snapshot OPFS delivery", () => {
  it("accepts only canonical UUIDv4 request identifiers", () => {
    expect(snapshotFileName("550e8400-e29b-41d4-a716-446655440000")).toBe(
      "550e8400-e29b-41d4-a716-446655440000.json",
    );
    for (const invalid of [
      "550E8400-E29B-41D4-A716-446655440000",
      "550e8400-e29b-11d4-a716-446655440000",
      "550e8400-e29b-41d4-7716-446655440000",
      "550e8400-e29b-41d4-a716-44665544000",
    ]) {
      expect(() => snapshotFileName(invalid)).toThrow("requestId is invalid");
    }
  });

  it("removes every crash orphan in deterministic filename order", async () => {
    const directory = installOpfs(
      crashFixture.orphanedFiles.map((name) => ({ name, kind: "file" as const })),
    );

    const removed = await reconcileSnapshotDirectory();

    expect(removed).toEqual([...crashFixture.orphanedFiles].sort());
    expect(directory.removed).toEqual(removed);
  });

  it("treats an exact file already removed by recovery as idempotently clean", async () => {
    const fileName = crashFixture.orphanedFiles[0] as string;
    const directory = installOpfs([{ name: fileName, kind: "file" }]);

    await expect(reconcileSnapshotDirectory()).resolves.toEqual([fileName]);
    await expect(removeSnapshotFile(fileName)).resolves.toBeUndefined();
    await expect(removeSnapshotFile(fileName)).resolves.toBeUndefined();
    expect(directory.removed).toEqual([fileName]);
  });

  it.each(crashFixture.unexpectedEntries)(
    "rejects the unexpected crash entry $name ($kind) before removing any file",
    async (unexpected) => {
      const directory = installOpfs([
        { name: crashFixture.orphanedFiles[0] as string, kind: "file" },
        unexpected as FakeEntry,
      ]);

      await expect(reconcileSnapshotDirectory()).rejects.toThrow(
        `unexpected entry: ${unexpected.name}`,
      );
      expect(directory.removed).toEqual([]);
    },
  );

  it("streams code points without splitting a UTF-8 sequence", async () => {
    const chunks: Uint8Array<ArrayBuffer>[] = [];
    const value = "A😀Bé中";
    const result = await writeUtf8Chunks(value, {
      async write(bytes) {
        chunks.push(bytes);
      },
    }, 4);

    expect(chunks.map((chunk) => chunk.byteLength)).toEqual([1, 4, 3, 3]);
    expect(Buffer.concat(chunks).toString("utf8")).toBe(value);
    expect(result).toEqual({ bytes: utf8ByteLength(value), chunks: 4 });
  });

  it("uses the production chunk protocol for a snapshot one byte below the server limit", async () => {
    const value = "x".repeat(MAX_SNAPSHOT_BYTES - 1);
    const chunkSizes: number[] = [];

    const result = await writeUtf8Chunks(value, {
      async write(bytes) {
        chunkSizes.push(bytes.byteLength);
      },
    });

    expect(result.bytes).toBe(MAX_SNAPSHOT_BYTES - 1);
    expect(result.chunks).toBe(128);
    expect(chunkSizes).toHaveLength(128);
    expect(chunkSizes.slice(0, -1).every((size) => size === SNAPSHOT_CHUNK_BYTES)).toBe(true);
    expect(chunkSizes.at(-1)).toBe(SNAPSHOT_CHUNK_BYTES - 1);
  }, 30_000);
});
