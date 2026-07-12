import type {
  BrowserSession,
  ExamAnalysisGeneration,
  ExamAnalysisGenerationEvent,
  ObserveExamAnalysisGenerationOptions,
} from "@ascendany/sdk";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ExamAnalysisGenerationPanel } from "./ExamAnalysisGenerationPanel";

const sdk = vi.hoisted(() => ({
  observeExamAnalysisGeneration: vi.fn(),
}));

vi.mock("@ascendany/sdk", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@ascendany/sdk")>()),
  observeExamAnalysisGeneration: sdk.observeExamAnalysisGeneration,
}));

const examId = "123e4567-e89b-42d3-a456-426614174000";
const running: ExamAnalysisGeneration = {
  generationId: "41",
  status: "running",
  attemptCount: 1,
  createdAt: "2026-07-11T10:00:00Z",
  startedAt: "2026-07-11T10:00:01Z",
  eventHead: 2,
};
const succeeded: ExamAnalysisGeneration = {
  ...running,
  status: "succeeded",
  finishedAt: "2026-07-11T10:00:03Z",
};
const firstEvent: ExamAnalysisGenerationEvent = {
  sequence: 1,
  type: "running",
  payload: {},
  createdAt: "2026-07-11T10:00:01Z",
};

describe("web exam analysis generation panel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("keeps the durable cursor across an error retry and aborts on unmount", async () => {
    const attempts: ObserveExamAnalysisGenerationOptions[] = [];
    sdk.observeExamAnalysisGeneration.mockImplementation(async (
      options: ObserveExamAnalysisGenerationOptions,
    ) => {
      attempts.push(options);
      if (attempts.length === 1) {
        options.onGeneration(running, true);
        options.onEvent(firstEvent);
        options.onConnectionState("live");
        throw new Error("network interrupted");
      }
      options.onGeneration(succeeded, false);
      options.onConnectionState("closed");
    });
    const session = { client: {}, ensureAuthenticated: vi.fn() } as unknown as BrowserSession;

    const view = render(<ExamAnalysisGenerationPanel examId={examId} session={session} />);

    expect(await screen.findByText("network interrupted")).toBeInTheDocument();
    expect(screen.getByText("已保留至事件 1，重试会从该 Last-Event-ID 续传。")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "从事件 1 重试" }));

    await waitFor(() => expect(attempts).toHaveLength(2));
    expect(attempts[1]?.resume).toEqual({ generationId: "41", afterSequence: 1 });
    expect(await screen.findByText("生成成功")).toBeInTheDocument();
    expect(screen.getByText("生成已结束，事件流关闭于 1")).toBeInTheDocument();

    view.unmount();
    expect(attempts[1]?.signal.aborted).toBe(true);
  });
});
