import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { OjProblem, OjSubmissionDetail } from "@ascendany/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OjView } from "./OjView";

const operations = vi.hoisted(() => ({
  loadOjProblem: vi.fn(),
  loadOjProblems: vi.fn(),
  loadOjSubmission: vi.fn(),
  openOjJudgeEventStream: vi.fn(),
  publishOjProblemVersion: vi.fn(),
  submitOjSource: vi.fn(),
}));
const sessionState = vi.hoisted(() => ({
  account: {
    id: "123e4567-e89b-42d3-a456-426614174000",
    username: "student-1",
    displayName: "学生一",
    studentNumber: "20260001" as string | null,
    role: "student" as "student" | "admin",
    authRevision: 1,
  },
  session: { kind: "mobile-browser-session" },
}));

vi.mock("../api/operations", () => operations);
vi.mock("../session/SessionContext", () => ({ useSession: () => sessionState }));

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
    problemSpec: { checker: "tokens", schema: "ascendany.oj.problem-spec.v1" },
    contentSha256: "a".repeat(64),
    createdByAccountId: sessionState.account.id,
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
const running: OjSubmissionDetail = { ...queued, status: "running", attemptCount: 1 };
const completed: OjSubmissionDetail = {
  ...running,
  status: "completed",
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
};

describe("mobile OjView", () => {
  afterEach(cleanup);

  beforeEach(() => {
    vi.clearAllMocks();
    sessionState.account.role = "student";
    operations.loadOjProblems.mockResolvedValue({ items: [problem], nextCursor: null });
    operations.loadOjProblem.mockResolvedValue(problem);
    operations.submitOjSource.mockResolvedValue({ submission: queued, created: true });
    operations.loadOjSubmission.mockResolvedValue(completed);
    operations.openOjJudgeEventStream.mockResolvedValue({
      stream: (async function* () {
        yield { sequence: 1, type: "completed", payload: {}, createdAt: "2026-07-11T10:01:01Z" };
      })(),
      failure: () => undefined,
    });
    operations.publishOjProblemVersion.mockResolvedValue({ problem, idempotent: false });
  });

  it("submits C++20 and follows the durable event stream to a terminal result", async () => {
    operations.loadOjSubmission.mockResolvedValueOnce(queued).mockResolvedValue(completed);
    render(<OjView />);

    expect(await screen.findByRole("heading", { name: "Two Sum" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "发布不可变题目版本" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "提交" }));
    fireEvent.change(screen.getByLabelText("C++20 源码"), { target: { value: "int main() { return 0; }\n" } });
    fireEvent.click(screen.getByRole("button", { name: "提交评测" }));

    expect(await screen.findByRole("heading", { name: "Accepted" })).toBeTruthy();
    expect(operations.submitOjSource).toHaveBeenCalledWith(
      sessionState.session,
      problem,
      "submit",
      "int main() { return 0; }\n",
      "\n",
    );
    expect(operations.openOjJudgeEventStream).toHaveBeenCalledWith(
      sessionState.session,
      queued.id,
      0,
      expect.any(AbortSignal),
    );
    expect(screen.getByText("4/4")).toBeTruthy();
  });

  it("resumes a broken durable stream from the last observed event sequence", async () => {
    operations.loadOjSubmission.mockReset();
    operations.loadOjSubmission
      .mockResolvedValueOnce(queued)
      .mockResolvedValueOnce(running)
      .mockResolvedValue(completed);
    operations.openOjJudgeEventStream.mockReset();
    operations.openOjJudgeEventStream
      .mockResolvedValueOnce({
        stream: (async function* () {
          yield { sequence: 7, type: "running", payload: {}, createdAt: "2026-07-11T10:01:01Z" };
        })(),
        failure: () => new Error("connection interrupted"),
      })
      .mockResolvedValueOnce({
        stream: (async function* () {
          yield { sequence: 8, type: "completed", payload: {}, createdAt: "2026-07-11T10:01:02Z" };
        })(),
        failure: () => undefined,
      });
    render(<OjView />);
    await screen.findByRole("heading", { name: "Two Sum" });
    fireEvent.click(screen.getByRole("button", { name: "提交" }));
    fireEvent.click(screen.getByRole("button", { name: "提交评测" }));

    await screen.findByText("connection interrupted");
    fireEvent.click(screen.getByRole("button", { name: "续接事件流" }));
    await waitFor(() => expect(operations.openOjJudgeEventStream).toHaveBeenNthCalledWith(
      2,
      sessionState.session,
      queued.id,
      7,
      expect.any(AbortSignal),
    ));
    expect(await screen.findByRole("heading", { name: "Accepted" })).toBeTruthy();
  });

  it("shows immutable version publishing only to admins", async () => {
    sessionState.account.role = "admin";
    render(<OjView />);
    expect(await screen.findByRole("heading", { name: "发布不可变题目版本" })).toBeTruthy();
    const bundle = new File(["bundle"], "tests.tar", { type: "application/x-tar" });
    fireEvent.change(screen.getByLabelText("测试包 TAR"), { target: { files: [bundle] } });
    fireEvent.click(screen.getByRole("button", { name: "发布版本" }));

    await waitFor(() => expect(operations.publishOjProblemVersion).toHaveBeenCalledWith(
      sessionState.session,
      expect.objectContaining({
        slug: problem.slug,
        expectedHeadRevision: problem.headRevision,
        problemSpec: { checker: "tokens", schema: "ascendany.oj.problem-spec.v1" },
      }),
      bundle,
    ));
    expect(await screen.findByText("已发布 two_sum v1。")).toBeTruthy();
  });
});
