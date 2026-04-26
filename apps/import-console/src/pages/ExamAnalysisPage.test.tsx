import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ExamAnalysisPage } from "./ExamAnalysisPage";
import type {
  ExamAnalysisExamDetail,
  ExamAnalysisExamListResponse,
} from "../api/examAnalysis";

const apiMocks = vi.hoisted(() => ({
  listExamAnalysisExams: vi.fn<() => Promise<ExamAnalysisExamListResponse>>(),
  getExamAnalysisDetail: vi.fn<(examId: string) => Promise<ExamAnalysisExamDetail>>(),
  generateExamAnalysis: vi.fn<(examId: string, payload?: { force?: boolean }) => Promise<{ runId: string; message: string }>>(),
}));

const streamState = vi.hoisted(() => ({
  logs: [] as Array<{ level: "info" | "success" | "warning" | "error"; message: string; timestamp: string }>,
  progress: null as { current: number; total: number; phase?: string | null } | null,
  status: "idle" as "idle" | "connecting" | "streaming" | "done" | "error",
  result: null as Record<string, unknown> | null,
  errorMessage: null as string | null,
  connect: vi.fn<(runId: string, path: string) => void>(),
  disconnect: vi.fn<() => void>(),
  clearLogs: vi.fn<() => void>(),
}));

vi.mock("../api/examAnalysis", () => ({
  ...apiMocks,
  startExamAnalysisRun: apiMocks.generateExamAnalysis,
}));
vi.mock("../hooks/useSSEStream", () => ({
  useSSEStream: () => streamState,
}));

const EXAM_LIST: ExamAnalysisExamListResponse = {
  items: [
    {
      examId: "11",
      examName: "Contest 11",
      examType: "datastructure",
      examDate: "2026-02-11T00:00:00+00:00",
      participantCount: 2,
      generatedCount: 1,
      failedCount: 0,
      missingCount: 1,
    },
  ],
};

const DETAIL_MISSING: ExamAnalysisExamDetail = {
  examId: "11",
  examName: "Contest 11",
  examType: "datastructure",
  examDate: "2026-02-11T00:00:00+00:00",
  participantCount: 2,
  generatedCount: 1,
  failedCount: 0,
  missingCount: 1,
  items: [
    {
      studentEntityId: "101",
      studentId: "20230001",
      studentName: "Alice",
      rank: 1,
      totalScore: 100,
      solvedCount: 4,
      ratingDelta: 22,
      knowledge: 90,
      accuracy: 88,
      quality: 80,
      flexibility: 76,
      proficiency: 79,
      analysisStatus: "success",
      analysisReply: "Alice summary text",
      generatedAt: "2026-02-11T08:00:00+00:00",
      errorMessage: null,
    },
    {
      studentEntityId: "202",
      studentId: "20230002",
      studentName: "Bob",
      rank: 2,
      totalScore: 75,
      solvedCount: 3,
      ratingDelta: -5,
      knowledge: 70,
      accuracy: 62,
      quality: 65,
      flexibility: 60,
      proficiency: 58,
      analysisStatus: "missing",
      analysisReply: "",
      generatedAt: null,
      errorMessage: null,
    },
  ],
};

const ALICE_ROW = DETAIL_MISSING.items[0]!;
const BOB_ROW = DETAIL_MISSING.items[1]!;

const DETAIL_DONE: ExamAnalysisExamDetail = {
  ...DETAIL_MISSING,
  generatedCount: 2,
  missingCount: 0,
  items: [
    ALICE_ROW,
    {
      ...BOB_ROW,
      analysisStatus: "success",
      analysisReply: "Bob generated text",
      generatedAt: "2026-02-16T10:00:00+00:00",
    },
  ],
};

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/exam-analysis"]}>
      <ExamAnalysisPage
        account={{ accountId: "1", username: "admin", isAdmin: true }}
        onLogout={() => undefined}
      />
    </MemoryRouter>,
  );
}

describe("ExamAnalysisPage", () => {
  beforeEach(() => {
    apiMocks.listExamAnalysisExams.mockReset();
    apiMocks.getExamAnalysisDetail.mockReset();
    apiMocks.generateExamAnalysis.mockReset();
    window.sessionStorage.clear();
    streamState.logs = [];
    streamState.progress = null;
    streamState.status = "idle";
    streamState.result = null;
    streamState.errorMessage = null;
    streamState.connect.mockReset();
    streamState.disconnect.mockReset();
    streamState.clearLogs.mockReset();
  });

  it("renders exam data and filters students by keyword and status", async () => {
    apiMocks.listExamAnalysisExams.mockResolvedValue(EXAM_LIST);
    apiMocks.getExamAnalysisDetail.mockResolvedValue(DETAIL_MISSING);

    renderPage();

    await screen.findAllByText("Contest 11");
    const table = await screen.findByRole("table");
    expect(within(table).getByText("Alice")).toBeInTheDocument();
    expect(within(table).getByText("Bob")).toBeInTheDocument();
    expect(within(table).getByText("Alice summary text")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("搜索学生"), {
      target: { value: "Bob" },
    });

    await waitFor(() => {
      expect(within(table).queryByText("Alice")).not.toBeInTheDocument();
      expect(within(table).getByText("Bob")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByLabelText("状态筛选"), {
      target: { value: "missing" },
    });

    await waitFor(() => {
      expect(screen.getAllByText("缺失").length).toBeGreaterThan(0);
      expect(within(table).getByText("Bob")).toBeInTheDocument();
    });
  });

  it("shows the selected student's full analysis text", async () => {
    apiMocks.listExamAnalysisExams.mockResolvedValue(EXAM_LIST);
    apiMocks.getExamAnalysisDetail.mockResolvedValue(DETAIL_MISSING);

    renderPage();

    const table = await screen.findByRole("table");
    fireEvent.click(within(table).getByText("Bob"));

    await waitFor(() => {
      expect(screen.getByText("当前没有可展示的分析结果")).toBeInTheDocument();
      expect(screen.getByText("学号 20230002 · 实体 202")).toBeInTheDocument();
    });
  });

  it("starts a generation run and refreshes detail after stream completion", async () => {
    apiMocks.listExamAnalysisExams.mockResolvedValue(EXAM_LIST);
    apiMocks.getExamAnalysisDetail
      .mockResolvedValueOnce(DETAIL_MISSING)
      .mockResolvedValueOnce(DETAIL_DONE);
    apiMocks.generateExamAnalysis.mockResolvedValue({
      runId: "run-11",
      message: "ok",
    });

    const { rerender } = renderPage();

    await screen.findAllByText("Contest 11");
    fireEvent.click(screen.getByRole("button", { name: "生成本场分析" }));

    await waitFor(() => {
      expect(apiMocks.generateExamAnalysis).toHaveBeenCalledWith("11", { force: false });
      expect(streamState.clearLogs).toHaveBeenCalled();
      expect(streamState.connect).toHaveBeenCalledWith("run-11", "/api/v1/exam-analysis/runs/{run_id}/stream");
      expect(window.sessionStorage.getItem("ascendany.exam-analysis.run.11")).toBe("run-11");
    });

    streamState.status = "done";
    streamState.result = {
      participants: 2,
      generated: 1,
      skipped: 0,
      failed: 0,
    };
    streamState.logs = [
      {
        level: "success",
        message: "done",
        timestamp: "2026-02-16T10:00:00+00:00",
      },
    ];

    rerender(
      <MemoryRouter initialEntries={["/exam-analysis"]}>
        <ExamAnalysisPage
          account={{ accountId: "1", username: "admin", isAdmin: true }}
          onLogout={() => undefined}
        />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(apiMocks.getExamAnalysisDetail).toHaveBeenCalledTimes(2);
      expect(screen.getByText("成功 1")).toBeInTheDocument();
      expect(screen.getByText("Bob generated text")).toBeInTheDocument();
      expect(window.sessionStorage.getItem("ascendany.exam-analysis.run.11")).toBeNull();
    });
  });

  it("reconnects to a stored run after page reload", async () => {
    apiMocks.listExamAnalysisExams.mockResolvedValue(EXAM_LIST);
    apiMocks.getExamAnalysisDetail.mockResolvedValue(DETAIL_MISSING);
    window.sessionStorage.setItem("ascendany.exam-analysis.run.11", "run-11");

    renderPage();

    await screen.findAllByText("Contest 11");

    await waitFor(() => {
      expect(streamState.clearLogs).toHaveBeenCalled();
      expect(streamState.connect).toHaveBeenCalledWith("run-11", "/api/v1/exam-analysis/runs/{run_id}/stream");
    });
  });
});
