import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ImportJob, ImportJobPage } from "@ascendany/sdk";
import { ImportPage } from "./ImportPage";

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

const importMocks = vi.hoisted(() => ({
  uploadPintiaSnapshot: vi.fn<(file: File, onProgress?: (percent: number) => void) => Promise<ImportJob>>(),
  getImportHistory: vi.fn<(limit?: number, cursor?: string) => Promise<ImportJobPage>>(),
}));

const streamState = vi.hoisted(() => ({
  logs: [] as Array<{ level: "info" | "success" | "warning" | "error"; message: string; timestamp: string }>,
  progress: null as { current: number; total: number; phase: string } | null,
  status: "idle" as "idle" | "connecting" | "streaming" | "done" | "error",
  result: null as ImportJob | null,
  errorMessage: null as string | null,
  connect: vi.fn<(jobId: string) => void>(),
  disconnect: vi.fn<() => void>(),
  clearLogs: vi.fn<() => void>(),
}));

vi.mock("../api/import", () => importMocks);

vi.mock("../hooks/useImportJobStream", () => ({
  useImportJobStream: () => streamState,
}));

describe("ImportPage", () => {
  beforeEach(() => {
    importMocks.uploadPintiaSnapshot.mockReset();
    importMocks.getImportHistory.mockReset();
    streamState.logs = [];
    streamState.progress = null;
    streamState.status = "idle";
    streamState.result = null;
    streamState.errorMessage = null;
    streamState.connect.mockReset();
    streamState.disconnect.mockReset();
    streamState.clearLogs.mockReset();
    importMocks.getImportHistory.mockResolvedValue({ items: [], nextCursor: null });
  });

  it("renders v2 snapshot upload and the standalone task terminal", async () => {
    const { container } = render(<ImportPage />);

    expect(await screen.findByText("上传快照")).toBeInTheDocument();
    expect(screen.getByText("拖入 Pintia snapshot v2 JSON")).toBeInTheDocument();
    expect(screen.getByText("每次上传一份浏览器插件生成的完整 JSON 快照")).toBeInTheDocument();
    const terminal = container.querySelector(".import-terminal");
    expect(terminal).not.toBeNull();
    expect(terminal).toHaveTextContent("实时日志");
    expect(terminal).toHaveTextContent("任务日志会在快照上传后显示。");
  });

  it("queues one JSON snapshot and connects the durable v2 job stream", async () => {
    importMocks.uploadPintiaSnapshot.mockImplementation(async (_file, onProgress) => {
      onProgress?.(100);
      return job;
    });
    const { container } = render(<ImportPage />);
    await screen.findByText("上传快照");

    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    expect(input).not.toBeNull();
    const file = new File(["{}"], "pintia-snapshot.json", { type: "application/json" });
    fireEvent.change(input as HTMLInputElement, { target: { files: [file] } });

    await waitFor(() => {
      expect(importMocks.uploadPintiaSnapshot).toHaveBeenCalledWith(file, expect.any(Function));
      expect(streamState.connect).toHaveBeenCalledWith(job.id);
      expect(screen.getByText("123e4567")).toBeInTheDocument();
    });
    expect(importMocks.getImportHistory).toHaveBeenCalledWith(30, undefined);
    expect(screen.queryByRole("button", { name: "开始增量导入" })).not.toBeInTheDocument();
  });

  it("rejects a non-JSON file before calling the v2 upload operation", async () => {
    const { container } = render(<ImportPage />);
    await screen.findByText("上传快照");
    const input = container.querySelector<HTMLInputElement>('input[type="file"]');
    const file = new File(["rows"], "submissions.csv", { type: "text/csv" });
    fireEvent.change(input as HTMLInputElement, { target: { files: [file] } });

    expect(await screen.findByText("每次请选择一个浏览器插件导出的 Pintia JSON 快照")).toBeInTheDocument();
    expect(importMocks.uploadPintiaSnapshot).not.toHaveBeenCalled();
  });
});
