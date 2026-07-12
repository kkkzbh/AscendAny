import type {
  BrowserSession,
  ExamAnalysisGeneration,
  ExamAnalysisGenerationEvent,
  ObserveExamAnalysisGenerationOptions,
} from "@ascendany/sdk";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ExamAnalysisGenerationPanel } from "./ExamAnalysisGenerationPanel";

const sdk = vi.hoisted(() => ({ observeExamAnalysisGeneration: vi.fn() }));

vi.mock("@ascendany/sdk", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@ascendany/sdk")>()),
  observeExamAnalysisGeneration: sdk.observeExamAnalysisGeneration,
}));

const examId = "123e4567-e89b-42d3-a456-426614174000";
const running: ExamAnalysisGeneration = {
  generationId: "51",
  status: "running",
  attemptCount: 1,
  createdAt: "2026-07-11T10:00:00Z",
  startedAt: "2026-07-11T10:00:01Z",
  eventHead: 2,
};
const firstEvent: ExamAnalysisGenerationEvent = {
  sequence: 1,
  type: "running",
  payload: {},
  createdAt: "2026-07-11T10:00:01Z",
};

describe("mobile exam analysis generation panel", () => {
  beforeEach(() => vi.clearAllMocks());

  it("resumes from the retained event cursor after a visible stream error", async () => {
    const attempts: ObserveExamAnalysisGenerationOptions[] = [];
    sdk.observeExamAnalysisGeneration.mockImplementation(async (
      options: ObserveExamAnalysisGenerationOptions,
    ) => {
      attempts.push(options);
      if (attempts.length === 1) {
        options.onGeneration(running, true);
        options.onEvent(firstEvent);
        throw new Error("mobile stream offline");
      }
      options.onGeneration({
        ...running,
        status: "failed",
        finishedAt: "2026-07-11T10:00:03Z",
        errorCode: "invalid_dataset",
      }, false);
      options.onConnectionState("closed");
    });
    const session = { client: {}, ensureAuthenticated: vi.fn() } as unknown as BrowserSession;

    const view = render(<ExamAnalysisGenerationPanel examId={examId} session={session} />);

    expect(await screen.findByText("mobile stream offline")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "从事件 1 重试" }));
    await waitFor(() => expect(attempts).toHaveLength(2));
    expect(attempts[1]?.resume).toEqual({ generationId: "51", afterSequence: 1 });
    expect(await screen.findByText("生成失败")).toBeTruthy();
    expect(screen.getByText("错误代码：invalid_dataset")).toBeTruthy();

    view.unmount();
    expect(attempts[1]?.signal.aborted).toBe(true);
  });
});
