import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { BrowserSession, OjProblem, OjSubmissionDetail } from "@ascendany/sdk";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { OjPage } from "./OjPage";

const operations = vi.hoisted(() => ({
  loadOjProblem: vi.fn(),
  loadOjProblems: vi.fn(),
  loadOjSubmission: vi.fn(),
  openOjJudgeEventStream: vi.fn(),
  publishOjProblemVersion: vi.fn(),
  submitOjSource: vi.fn(),
}));

vi.mock("../api/operations", () => operations);

const session = { kind: "browser-session" } as unknown as BrowserSession;
const account = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  username: "student-1",
  displayName: "学生一",
  studentNumber: "20260001",
  role: "student" as const,
  authRevision: 1,
};

vi.mock("../session/context", () => ({
  useSession: () => ({ account, session }),
}));

const problem: OjProblem = {
  id: "223e4567-e89b-42d3-a456-426614174000",
  slug: "two_sum",
  headRevision: 1,
  currentVersion: {
    number: 1,
    lifecycle: "active",
    title: "Two Sum",
    statementMarkdown: "Find two values.",
    knowledgeTags: ["array"],
    timeLimitMs: 1000,
    memoryLimitBytes: 268435456,
    outputLimitBytes: 1048576,
    problemSchema: "ascendany.oj.problem.v1",
    contentSha256: "a".repeat(64),
    createdByAccountId: account.id,
    createdAt: "2026-07-11T10:00:00Z",
  },
  createdAt: "2026-07-11T10:00:00Z",
  updatedAt: "2026-07-11T10:00:00Z",
};

const queued: OjSubmissionDetail = {
  id: "323e4567-e89b-42d3-a456-426614174000",
  judgeJobId: "423e4567-e89b-42d3-a456-426614174000",
  problemId: problem.id,
  problemVersion: 1,
  mode: "submit",
  languageId: "cpp20",
  createdAt: "2026-07-11T10:01:00Z",
  status: "queued",
  attemptCount: 0,
  updatedAt: "2026-07-11T10:01:00Z",
};

const completed: OjSubmissionDetail = {
  ...queued,
  status: "completed",
  attemptCount: 1,
  result: {
    verdict: "accepted",
    scoreFraction: 1,
    passedCaseCount: 4,
    totalCaseCount: 4,
    maxTimeMs: 12,
    maxMemoryBytes: 4096,
    resultSchema: "ascendany.oj.judge-result.v1",
    resultManifest: {},
    resultSha256: "b".repeat(64),
    createdAt: "2026-07-11T10:01:01Z",
  },
  updatedAt: "2026-07-11T10:01:01Z",
};

describe("OjPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    operations.loadOjProblems.mockResolvedValue({ items: [problem], nextCursor: null });
    operations.loadOjProblem.mockResolvedValue(problem);
    operations.submitOjSource.mockResolvedValue({
      submission: queued,
      created: true,
    });
    operations.loadOjSubmission
      .mockResolvedValueOnce(queued)
      .mockResolvedValue(completed);
    operations.openOjJudgeEventStream.mockResolvedValue({
      stream: (async function* () {
        yield {
          sequence: 1,
          type: "completed",
          payload: {},
          createdAt: "2026-07-11T10:01:01Z",
        };
      })(),
      failure: () => undefined,
    });
  });

  it("loads a problem and follows one durable submit through its terminal event", async () => {
    render(<OjPage />);

    expect(await screen.findByRole("heading", { name: "Two Sum" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "提交" }));
    fireEvent.change(screen.getByLabelText("C++20 源码"), {
      target: { value: "int main() { return 0; }\n" },
    });
    fireEvent.click(screen.getByRole("button", { name: "提交评测" }));

    await waitFor(() => expect(screen.getByRole("heading", { name: "Accepted" })).toBeInTheDocument());
    expect(operations.submitOjSource).toHaveBeenCalledWith(
      session,
      problem,
      "submit",
      "int main() { return 0; }\n",
      "\n",
    );
    expect(operations.openOjJudgeEventStream).toHaveBeenCalledWith(
      session,
      queued.id,
      0,
      expect.any(AbortSignal),
    );
    expect(screen.getByText("4/4")).toBeInTheDocument();
  });
});
