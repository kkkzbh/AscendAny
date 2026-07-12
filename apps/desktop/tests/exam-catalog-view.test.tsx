import type { BrowserSession, ExamDetail, ExamPage } from "@ascendany/sdk";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ExamCatalogView } from "../src/components/ExamCatalogView";

const operations = vi.hoisted(() => ({ loadExam: vi.fn(), loadExams: vi.fn() }));

vi.mock("../src/api/operations", () => operations);
vi.mock("../src/components/ExamAnalysisGenerationPanel", () => ({
  ExamAnalysisGenerationPanel: ({ examId }: { examId: string }) => <p>generation-for-{examId}</p>,
}));

const examId = "123e4567-e89b-42d3-a456-426614174010";
const detail: ExamDetail = {
  id: examId,
  snapshotId: "223e4567-e89b-42d3-a456-426614174010",
  platform: "pintia",
  problemSetId: "2039341868571590656",
  title: "2026 清明节集训",
  sourceUrl: "https://pintia.cn/problem-sets/2039341868571590656",
  startsAt: null,
  endsAt: null,
  totalScore: "100",
  problemCount: 1,
  participantCount: 35,
  rankingCount: 211,
  submissionCount: 624,
  snapshotSequence: 1,
  headRevision: 1,
  exporterVersion: "2.0.5",
  exportedAt: "2026-07-11T10:00:00Z",
  updatedAt: "2026-07-11T10:00:00Z",
  problems: [{
    id: "problem-row-1",
    problemId: "problem-1",
    label: "7-1",
    title: "样例题目",
    maxScore: "100",
    timeLimitMs: 1000,
    memoryLimitBytes: 268435456,
    submissionCount: 624,
    submittingParticipantCount: 35,
    passedParticipantCount: 20,
  }],
};
const page: ExamPage = { items: [{ ...detail }], nextCursor: null };

describe("desktop exam catalog", () => {
  afterEach(cleanup);
  beforeEach(() => {
    vi.clearAllMocks();
    operations.loadExams.mockResolvedValue(page);
    operations.loadExam.mockResolvedValue(detail);
  });

  it("opens an exam detail with its current generation surface", async () => {
    const session = { marker: "desktop-session" } as unknown as BrowserSession;
    render(<ExamCatalogView session={session} />);

    fireEvent.click(await screen.findByRole("button", { name: /2026 清明节集训/ }));
    await waitFor(() => expect(operations.loadExam).toHaveBeenCalledWith(session, examId));
    expect(await screen.findByText(`generation-for-${examId}`)).toBeTruthy();
    expect(screen.getByText(/624 次提交/)).toBeTruthy();
    expect(screen.getByText(/样例题目/)).toBeTruthy();
  });
});
