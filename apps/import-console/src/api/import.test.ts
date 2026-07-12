import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ImportJob, ImportJobPage } from "@ascendany/sdk";
import { getImportHistory, readImportJob, uploadPintiaSnapshot } from "./import";

const sdk = vi.hoisted(() => ({
  createPintiaImport: vi.fn(),
  getImportJob: vi.fn(),
  listImportJobs: vi.fn(),
}));

const transport = vi.hoisted(() => ({
  ensureAuthenticated: vi.fn(),
  client: { kind: "browser-session-client" },
}));

vi.mock("@ascendany/sdk", () => sdk);

vi.mock("./v2Client", () => ({
  browserSession: { ensureAuthenticated: transport.ensureAuthenticated },
  v2Client: transport.client,
  apiFailureMessage: (error: unknown) => error instanceof Error ? error.message : "请求失败",
}));

const job: ImportJob = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  artifactSha256: "a".repeat(64),
  status: "queued",
  stage: "received",
  createdAt: "2026-07-11T04:00:00Z",
  updatedAt: "2026-07-11T04:00:00Z",
  examId: null,
  snapshotId: null,
  error: null,
};

describe("v2 import API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    transport.ensureAuthenticated.mockResolvedValue(undefined);
    sdk.createPintiaImport.mockResolvedValue({ data: job });
    sdk.getImportJob.mockResolvedValue({ data: job });
    sdk.listImportJobs.mockResolvedValue({ data: { items: [job], nextCursor: null } satisfies ImportJobPage });
  });

  it("refreshes the BrowserSession before uploading through its generated client", async () => {
    const progress = vi.fn();
    const file = new File(["{}"], "snapshot.json", { type: "application/json" });

    await expect(uploadPintiaSnapshot(file, progress)).resolves.toEqual(job);

    expect(progress.mock.calls.map(([value]) => value)).toEqual([0, 100]);
    expect(transport.ensureAuthenticated).toHaveBeenCalledTimes(1);
    expect(sdk.createPintiaImport).toHaveBeenCalledWith({
      client: transport.client,
      body: file,
      throwOnError: true,
    });
    expect(transport.ensureAuthenticated.mock.invocationCallOrder[0]).toBeLessThan(
      sdk.createPintiaImport.mock.invocationCallOrder[0] ?? 0,
    );
  });

  it("refreshes the same BrowserSession before history and job reads", async () => {
    await expect(getImportHistory(30, job.id)).resolves.toEqual({ items: [job], nextCursor: null });
    await expect(readImportJob(job.id)).resolves.toEqual(job);

    expect(transport.ensureAuthenticated).toHaveBeenCalledTimes(2);
    expect(sdk.listImportJobs).toHaveBeenCalledWith({
      client: transport.client,
      query: { limit: 30, cursor: job.id },
      throwOnError: true,
    });
    expect(sdk.getImportJob).toHaveBeenCalledWith({
      client: transport.client,
      path: { jobId: job.id },
      throwOnError: true,
    });
  });

  it("rejects files outside the snapshot JSON boundary before session access", async () => {
    const file = new File(["rows"], "submissions.csv", { type: "text/csv" });

    await expect(uploadPintiaSnapshot(file)).rejects.toThrow("仅接受浏览器插件导出的 Pintia JSON 快照");
    expect(transport.ensureAuthenticated).not.toHaveBeenCalled();
    expect(sdk.createPintiaImport).not.toHaveBeenCalled();
  });
});
