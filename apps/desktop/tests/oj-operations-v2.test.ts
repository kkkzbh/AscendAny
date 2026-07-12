import type {
  Account,
  BrowserSession,
  OjCreateProblemVersionResult,
  OjCreateSubmissionResult,
  OjProblem,
  OjSubmissionDetail,
} from "@ascendany/sdk";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  loadOjProblem,
  loadOjProblems,
  loadOjSubmission,
  openOjJudgeEventStream,
  publishOjProblemVersion,
  submitOjSource,
} from "../src/api/operations";

const sdk = vi.hoisted(() => ({
  getOjProblem: vi.fn(),
  getOjSubmission: vi.fn(),
  listOjProblems: vi.fn(),
  streamOjJudgeEvents: vi.fn(),
  uploadOjProblemVersion: vi.fn(),
  uploadOjSubmission: vi.fn(),
}));

vi.mock("@ascendany/sdk", () => sdk);

const account: Account = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  username: "student-1",
  displayName: "学生一",
  studentNumber: "20260001",
  role: "student",
  authRevision: 1,
};
const problem: OjProblem = {
  id: "223e4567-e89b-42d3-a456-426614174000",
  slug: "two_sum",
  headRevision: 3,
  currentVersion: {
    number: 3,
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
const submission: OjCreateSubmissionResult = {
  submission: {
    id: "323e4567-e89b-42d3-a456-426614174000",
    judgeJobId: "423e4567-e89b-42d3-a456-426614174000",
    problemId: problem.id,
    problemVersion: 3,
    mode: "run",
    languageId: "cpp20",
    createdAt: "2026-07-11T10:01:00Z",
  },
  created: true,
};
const detail: OjSubmissionDetail = {
  ...submission.submission,
  status: "queued",
  attemptCount: 0,
  updatedAt: "2026-07-11T10:01:00Z",
};
const problemVersionResult: OjCreateProblemVersionResult = {
  problem,
  idempotent: false,
};
const client = { kind: "desktop-client" };
const ensureAuthenticated = vi.fn();
const session = { client, ensureAuthenticated } as unknown as BrowserSession;

describe("desktop OJ generated operations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    ensureAuthenticated.mockResolvedValue(account);
    sdk.listOjProblems.mockResolvedValue({ data: { items: [problem], nextCursor: null } });
    sdk.getOjProblem.mockResolvedValue({ data: problem });
    sdk.getOjSubmission.mockResolvedValue({ data: detail });
    sdk.uploadOjSubmission.mockResolvedValue({ data: submission });
    sdk.uploadOjProblemVersion.mockResolvedValue({ data: problemVersionResult });
    sdk.streamOjJudgeEvents.mockResolvedValue({
      stream: (async function* () {
        yield { sequence: 10, type: "completed", payload: {}, createdAt: "2026-07-11T10:01:01Z" };
      })(),
    });
  });

  it("uses generated reads and exact run/submit multipart shapes", async () => {
    await expect(loadOjProblems(session, 25, "one_sum", true)).resolves.toEqual({ items: [problem], nextCursor: null });
    await expect(loadOjProblem(session, problem.id)).resolves.toEqual(problem);
    await expect(submitOjSource(session, problem, "run", "int main() {}\n", "42\n")).resolves.toEqual(submission);
    await expect(submitOjSource(session, problem, "submit", "int main() {}\n", "ignored")).resolves.toEqual(submission);
    await expect(loadOjSubmission(session, submission.submission.id)).resolves.toEqual(detail);

    expect(sdk.listOjProblems).toHaveBeenCalledWith({
      client,
      query: { limit: 25, includeArchived: true, afterSlug: "one_sum" },
      throwOnError: true,
    });
    expect(sdk.getOjProblem).toHaveBeenCalledWith({ client, path: { problemId: problem.id }, throwOnError: true });
    const runUpload = sdk.uploadOjSubmission.mock.calls[0]?.[0];
    const submitUpload = sdk.uploadOjSubmission.mock.calls[1]?.[0];
    expect(runUpload).toEqual(expect.objectContaining({
      client,
      metadata: expect.objectContaining({
        clientRequestId: expect.any(String),
        problemId: problem.id,
        expectedProblemHeadRevision: 3,
        mode: "run",
        languageId: "cpp20",
      }),
      source: expect.objectContaining({ filename: "main.cpp", data: expect.any(Blob) }),
      stdin: expect.objectContaining({ filename: "stdin.txt", data: expect.any(Blob) }),
    }));
    expect(runUpload.source.data.type).toBe("text/x-c++src; charset=utf-8");
    expect(runUpload.stdin.data.type).toBe("text/plain; charset=utf-8");
    expect(submitUpload).not.toHaveProperty("stdin");
    expect(sdk.getOjSubmission).toHaveBeenCalledWith({
      client,
      path: { submissionId: submission.submission.id },
      throwOnError: true,
    });
  });

  it("resumes durable SSE from the exact cursor and publishes through the multipart helper", async () => {
    const abort = new AbortController();
    const eventStream = await openOjJudgeEventStream(session, submission.submission.id, 9, abort.signal);
    const events = [];
    for await (const event of eventStream.stream) events.push(event);
    expect(events).toHaveLength(1);
    expect(sdk.streamOjJudgeEvents).toHaveBeenCalledWith(expect.objectContaining({
      client,
      path: { submissionId: submission.submission.id },
      headers: { "Last-Event-ID": "9" },
      signal: abort.signal,
      sseMaxRetryAttempts: 1,
      onSseError: expect.any(Function),
    }));

    const metadata = {
      slug: problem.slug,
      expectedHeadRevision: problem.headRevision,
      lifecycle: "active" as const,
      title: "Two Sum",
      statementMarkdown: "Find two values.",
      solutionMarkdown: null,
      knowledgeTags: ["array"],
      timeLimitMs: 1000,
      memoryLimitBytes: 268435456,
      outputLimitBytes: 1048576,
      problemSpec: { checker: "tokens", schema: "ascendany.oj.problem-spec.v1" },
    };
    const bundle = new File(["bundle"], "tests.tar", { type: "application/x-tar" });
    await expect(publishOjProblemVersion(session, metadata, bundle)).resolves.toEqual(problemVersionResult);
    expect(sdk.uploadOjProblemVersion).toHaveBeenCalledWith({
      client,
      metadata,
      testBundle: { data: bundle, filename: "tests.tar" },
    });
  });
});
