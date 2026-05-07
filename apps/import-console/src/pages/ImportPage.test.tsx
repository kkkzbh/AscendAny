import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ImportPage } from "./ImportPage";
import type { ImportRunResponse, IngestHistoryResponse } from "../api/import";

const importMocks = vi.hoisted(() => ({
  uploadExamZip: vi.fn(),
  startImportRun: vi.fn<() => Promise<ImportRunResponse>>(),
  getIngestHistory: vi.fn<() => Promise<IngestHistoryResponse>>(),
}));

const streamState = vi.hoisted(() => ({
  logs: [] as Array<{ level: "info" | "success" | "warning" | "error"; message: string; timestamp: string }>,
  progress: null as { current: number; total: number; examType?: string | null } | null,
  status: "idle" as "idle" | "connecting" | "streaming" | "done" | "error",
  result: null as Record<string, unknown> | null,
  errorMessage: null as string | null,
  connect: vi.fn<(runId: string, path: string) => void>(),
  disconnect: vi.fn<() => void>(),
  clearLogs: vi.fn<() => void>(),
}));

vi.mock("../api/import", () => ({
  EXAM_TYPES: [
    { value: "pintia", label: "Pintia JSON" },
    { value: "datastructure", label: "数据结构" },
    { value: "pta_icpc", label: "PTA ICPC" },
  ],
  ...importMocks,
}));

vi.mock("../hooks/useSSEStream", () => ({
  useSSEStream: () => streamState,
}));

describe("ImportPage", () => {
  beforeEach(() => {
    importMocks.uploadExamZip.mockReset();
    importMocks.startImportRun.mockReset();
    importMocks.getIngestHistory.mockReset();
    streamState.logs = [];
    streamState.progress = null;
    streamState.status = "idle";
    streamState.result = null;
    streamState.errorMessage = null;
    streamState.connect.mockReset();
    streamState.disconnect.mockReset();
    streamState.clearLogs.mockReset();
    importMocks.getIngestHistory.mockResolvedValue({ items: [], total: 0 });
  });

  it("renders realtime logs in a standalone bottom terminal", async () => {
    const { container } = render(<ImportPage />);

    expect(await screen.findByText("上传队列")).toBeInTheDocument();
    const terminal = container.querySelector(".import-terminal");
    expect(terminal).not.toBeNull();
    expect(terminal).toHaveTextContent("实时日志");
    expect(terminal).toHaveTextContent("任务日志会在导入开始后显示。");
    expect(container.querySelector(".import-workspace .log-panel")).toBeNull();
  });

  it("starts import and keeps terminal log controls wired to the stream", async () => {
    streamState.logs = [
      {
        level: "info",
        message: "scan started",
        timestamp: "2026-04-28T10:00:00+00:00",
      },
    ];
    importMocks.startImportRun.mockResolvedValue({
      runId: "run-1",
      message: "started",
    });

    const { container } = render(<ImportPage />);

    expect(await screen.findByText("scan started")).toBeInTheDocument();
    expect(container.querySelector(".import-terminal")).toHaveTextContent("scan started");

    fireEvent.click(screen.getByRole("button", { name: "开始增量导入" }));

    await waitFor(() => {
      expect(importMocks.startImportRun).toHaveBeenCalledWith({
        examTypes: ["pintia"],
        dryRun: false,
        force: false,
      });
      expect(streamState.clearLogs).toHaveBeenCalled();
      expect(streamState.connect).toHaveBeenCalledWith(
        "run-1",
        "/api/v1/import/run/{run_id}/stream",
      );
    });

    fireEvent.click(screen.getByRole("button", { name: "清除" }));
    expect(streamState.clearLogs).toHaveBeenCalledTimes(2);
  });
});
