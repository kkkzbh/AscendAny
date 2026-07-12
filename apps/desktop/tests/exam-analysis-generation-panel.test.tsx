import type {
  BrowserSession,
  ExamAnalysisGeneration,
  ExamAnalysisGenerationEvent,
  ObserveExamAnalysisGenerationOptions,
} from "@ascendany/sdk";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ExamAnalysisGenerationPanel } from "../src/components/ExamAnalysisGenerationPanel";

const sdk = vi.hoisted(() => ({ observeExamAnalysisGeneration: vi.fn() }));

vi.mock("@ascendany/sdk", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@ascendany/sdk")>()),
  observeExamAnalysisGeneration: sdk.observeExamAnalysisGeneration,
}));

const examId = "123e4567-e89b-42d3-a456-426614174000";
const running: ExamAnalysisGeneration = {
  generationId: "61",
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

describe("desktop exam analysis generation panel", () => {
  afterEach(cleanup);
  beforeEach(() => vi.clearAllMocks());

  it("shows a retry boundary and resumes the retained durable cursor", async () => {
    const attempts: ObserveExamAnalysisGenerationOptions[] = [];
    sdk.observeExamAnalysisGeneration.mockImplementation(async (
      options: ObserveExamAnalysisGenerationOptions,
    ) => {
      attempts.push(options);
      if (attempts.length === 1) {
        options.onGeneration(running, true);
        options.onEvent(firstEvent);
        throw new Error("desktop connection closed");
      }
      options.onGeneration({
        ...running,
        status: "superseded",
        finishedAt: "2026-07-11T10:00:03Z",
      }, false);
      options.onConnectionState("closed");
    });
    const session = { client: {}, ensureAuthenticated: vi.fn() } as unknown as BrowserSession;

    const view = render(<ExamAnalysisGenerationPanel examId={examId} session={session} />);

    expect(await screen.findByText("desktop connection closed")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "从事件 1 重试" }));
    await waitFor(() => expect(attempts).toHaveLength(2));
    expect(attempts[1]?.resume).toEqual({ generationId: "61", afterSequence: 1 });
    expect(await screen.findByText("已被新版本取代")).toBeTruthy();
    expect(screen.getByText("生成已结束，事件流关闭于 1")).toBeTruthy();

    view.unmount();
    expect(attempts[1]?.signal.aborted).toBe(true);
  });
});
