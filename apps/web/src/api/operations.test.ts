import type {
  Account,
  AccountSessionList,
  AgentRun,
  AgentRunEnqueueResult,
  AgentNote,
  AgentNoteMutationResult,
  AgentNotePage,
  BrowserSession,
  ChatMessagePage,
  ChatThread,
  ChatThreadPage,
  ExamDetail,
  ExamPage,
  OjCreateProblemVersionResult,
  OjCreateSubmissionResult,
  OjProblem,
  OjProblemPage,
  OjSubmissionDetail,
  SelfAchievements,
  SelfRecommendation,
  SelfStudentAnalytics,
  StudentLeaderboard,
} from "@ascendany/sdk";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  createStudentChatThread,
  changeStudentAgentNoteState,
  createStudentAgentNote,
  enqueueAutomaticAnalysis,
  enqueueChatReply,
  loadAgentRun,
  loadAgentNote,
  loadAgentNotes,
  loadChatMessages,
  loadChatThreads,
  loadExam,
  loadExams,
  loadOjProblem,
  loadOjProblems,
  loadOjSubmission,
  loadAccountSessions,
  loadSelfAchievements,
  loadSelfAnalytics,
  loadSelfRecommendation,
  loadStudentLeaderboard,
  openAgentRunEventStream,
  openOjJudgeEventStream,
  publishOjProblemVersion,
  replaceStudentAgentNote,
  revokeSession,
  saveDisplayName,
  sendAuthenticatedFeedback,
  submitOjSource,
} from "./operations";

const sdk = vi.hoisted(() => ({
  archiveAgentNote: vi.fn(),
  createAgentNote: vi.fn(),
  createChatThread: vi.fn(),
  enqueueSelfAutoAnalysis: vi.fn(),
  enqueueAgentRun: vi.fn(),
  getAgentRun: vi.fn(),
  getAgentNote: vi.fn(),
  getExam: vi.fn(),
  getOjProblem: vi.fn(),
  getOjSubmission: vi.fn(),
  getSelfAchievements: vi.fn(),
  getSelfRecommendation: vi.fn(),
  getSelfStudentAnalytics: vi.fn(),
  getStudentLeaderboard: vi.fn(),
  listExams: vi.fn(),
  listAccountSessions: vi.fn(),
  listAgentNotes: vi.fn(),
  listChatMessages: vi.fn(),
  listChatThreads: vi.fn(),
  listOjProblems: vi.fn(),
  revokeAccountSession: vi.fn(),
  replaceAgentNote: vi.fn(),
  restoreAgentNote: vi.fn(),
  submitAuthenticatedFeedback: vi.fn(),
  updateAccountProfile: vi.fn(),
  streamAgentRunEvents: vi.fn(),
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
const analytics: SelfStudentAnalytics = {
  state: "not_generated",
  headRevision: 0,
};
const achievements: SelfAchievements = {
  state: "not_generated",
  analyticsHeadRevision: 0,
  ruleSetVersion: 3,
  ruleHeadRevision: 7,
  summary: { total: 1, locked: 1, bronze: 0, silver: 0, gold: 0 },
  items: [{
    code: "first_exam",
    title: "初次登场",
    description: "完成第一场考试。",
    progressKey: "exam_count",
    tier: 0,
    progress: 0,
    bronzeTarget: 1,
    silverTarget: 5,
    goldTarget: 10,
    sortOrder: 1,
  }],
};
const recommendation: SelfRecommendation = {
  state: "unavailable",
  unavailableReason: "no_active_model",
  currentAnalyticsHeadRevision: 0,
  recommendationHeadRevision: 0,
};
const leaderboard: StudentLeaderboard = {
  state: "not_generated",
  headRevision: 0,
  population: 0,
  items: [],
};
const sessionList: AccountSessionList = { items: [] };
const examPage: ExamPage = { items: [], nextCursor: null };
const examDetail = { id: "123e4567-e89b-42d3-a456-426614174010" } as ExamDetail;
const thread: ChatThread = {
  id: "123e4567-e89b-42d3-a456-426614174020",
  kind: "conversation",
  headRevision: 0,
  createdAt: "2026-07-11T10:00:00Z",
  updatedAt: "2026-07-11T10:00:00Z",
};
const threadPage: ChatThreadPage = { items: [thread], nextCursor: null };
const messagePage: ChatMessagePage = { items: [], lastSequence: 0 };
const agentRun = {
  id: "123e4567-e89b-42d3-a456-426614174021",
  threadId: thread.id,
  clientRequestId: "123e4567-e89b-42d3-a456-426614174022",
  kind: "reply",
  inputMessageId: "123e4567-e89b-42d3-a456-426614174023",
  status: "queued",
  attemptCount: 0,
  createdAt: "2026-07-11T10:00:01Z",
  updatedAt: "2026-07-11T10:00:01Z",
} satisfies AgentRun;
const enqueueResult = {
  run: agentRun,
  message: {
    id: agentRun.inputMessageId,
    threadId: thread.id,
    sequence: 1,
    kind: "user",
    content: "Explain this result.",
    createdAt: "2026-07-11T10:00:01Z",
  },
  created: true,
} satisfies AgentRunEnqueueResult;
const agentNote: AgentNote = {
  id: "623e4567-e89b-42d3-a456-426614174020",
  headRevision: 1,
  state: "active",
  title: "学习计划",
  contentSha256: "a".repeat(64),
  currentMutationId: "723e4567-e89b-42d3-a456-426614174020",
  currentOperation: "create",
  currentRevisionCreatedAt: "2026-07-11T11:00:00Z",
  createdAt: "2026-07-11T11:00:00Z",
  updatedAt: "2026-07-11T11:00:00Z",
  content: "复习图论",
};
const agentNotePage: AgentNotePage = { items: [agentNote], nextCursor: null };
const agentNoteMutation: AgentNoteMutationResult = { note: agentNote, idempotent: false };
const ojProblem: OjProblem = {
  id: "a23e4567-e89b-42d3-a456-426614174020",
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
    contentSha256: "b".repeat(64),
    createdByAccountId: account.id,
    createdAt: "2026-07-11T11:00:00Z",
  },
  createdAt: "2026-07-11T11:00:00Z",
  updatedAt: "2026-07-11T11:00:00Z",
};
const ojProblemPage: OjProblemPage = { items: [ojProblem], nextCursor: null };
const ojSubmissionResult: OjCreateSubmissionResult = {
  submission: {
    id: "b23e4567-e89b-42d3-a456-426614174020",
    judgeJobId: "c23e4567-e89b-42d3-a456-426614174020",
    problemId: ojProblem.id,
    problemVersion: 1,
    mode: "submit",
    languageId: "cpp20",
    createdAt: "2026-07-11T11:01:00Z",
  },
  created: true,
};
const ojSubmissionDetail: OjSubmissionDetail = {
  ...ojSubmissionResult.submission,
  status: "queued",
  attemptCount: 0,
  updatedAt: "2026-07-11T11:01:00Z",
};
const ojProblemVersionResult: OjCreateProblemVersionResult = {
  problem: ojProblem,
  idempotent: false,
};
const client = { kind: "browser-session-client" };
const ensureAuthenticated = vi.fn();
const forgetLocalSession = vi.fn();
const session = {
  client,
  ensureAuthenticated,
  forgetLocalSession,
} as unknown as BrowserSession;

describe("web v2 operations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    ensureAuthenticated.mockResolvedValue(account);
    forgetLocalSession.mockResolvedValue(undefined);
    sdk.getSelfStudentAnalytics.mockResolvedValue({ data: analytics });
    sdk.getSelfAchievements.mockResolvedValue({ data: achievements });
    sdk.getSelfRecommendation.mockResolvedValue({ data: recommendation });
    sdk.getStudentLeaderboard.mockResolvedValue({ data: leaderboard });
    sdk.listExams.mockResolvedValue({ data: examPage });
    sdk.getExam.mockResolvedValue({ data: examDetail });
    sdk.updateAccountProfile.mockResolvedValue({ data: account });
    sdk.listAccountSessions.mockResolvedValue({ data: sessionList });
    sdk.revokeAccountSession.mockResolvedValue({ data: undefined });
    sdk.listChatThreads.mockResolvedValue({ data: threadPage });
    sdk.createChatThread.mockResolvedValue({ data: thread });
    sdk.listChatMessages.mockResolvedValue({ data: messagePage });
    sdk.enqueueAgentRun.mockResolvedValue({ data: enqueueResult });
    sdk.enqueueSelfAutoAnalysis.mockResolvedValue({ data: enqueueResult });
    sdk.getAgentRun.mockResolvedValue({ data: agentRun });
    sdk.streamAgentRunEvents.mockResolvedValue({
      stream: (async function* () {
        yield {
          sequence: 1,
          type: "queued",
          payload: {},
          createdAt: "2026-07-11T10:00:01Z",
        };
      })(),
    });
    sdk.listAgentNotes.mockResolvedValue({ data: agentNotePage });
    sdk.getAgentNote.mockResolvedValue({ data: agentNote });
    sdk.createAgentNote.mockResolvedValue({ data: agentNoteMutation });
    sdk.replaceAgentNote.mockResolvedValue({ data: agentNoteMutation });
    sdk.archiveAgentNote.mockResolvedValue({ data: agentNoteMutation });
    sdk.restoreAgentNote.mockResolvedValue({ data: agentNoteMutation });
    sdk.submitAuthenticatedFeedback.mockResolvedValue({
      data: {
        submission: {
          id: "823e4567-e89b-42d3-a456-426614174020",
          deliveryJobId: "923e4567-e89b-42d3-a456-426614174020",
          createdAt: "2026-07-11T11:00:00Z",
        },
        created: true,
      },
    });
    sdk.listOjProblems.mockResolvedValue({ data: ojProblemPage });
    sdk.getOjProblem.mockResolvedValue({ data: ojProblem });
    sdk.uploadOjSubmission.mockResolvedValue({ data: ojSubmissionResult });
    sdk.getOjSubmission.mockResolvedValue({ data: ojSubmissionDetail });
    sdk.streamOjJudgeEvents.mockResolvedValue({
      stream: (async function* () {
        yield {
          sequence: 1,
          type: "queued",
          payload: {},
          createdAt: "2026-07-11T11:01:00Z",
        };
      })(),
    });
    sdk.uploadOjProblemVersion.mockResolvedValue({ data: ojProblemVersionResult });
  });

  it("reads the exam catalog through generated operations", async () => {
    await expect(loadExams(session, 12, "123e4567-e89b-42d3-a456-426614174099")).resolves.toEqual(examPage);
    await expect(loadExam(session, examDetail.id)).resolves.toEqual(examDetail);

    expect(sdk.listExams).toHaveBeenCalledWith({
      client,
      query: { limit: 12, cursor: "123e4567-e89b-42d3-a456-426614174099" },
      throwOnError: true,
    });
    expect(sdk.getExam).toHaveBeenCalledWith({
      client,
      path: { examId: examDetail.id },
      throwOnError: true,
    });
  });

  it("reads student insights through authenticated generated operations", async () => {
    await expect(loadSelfAnalytics(session, 25)).resolves.toEqual(analytics);
    await expect(loadSelfAchievements(session)).resolves.toEqual(achievements);
    await expect(loadSelfRecommendation(session)).resolves.toEqual(recommendation);
    await expect(loadStudentLeaderboard(session, 60)).resolves.toEqual(leaderboard);

    expect(ensureAuthenticated).toHaveBeenCalledTimes(4);
    expect(sdk.getSelfStudentAnalytics).toHaveBeenCalledWith({
      client,
      query: { limit: 25 },
      throwOnError: true,
    });
    expect(sdk.getSelfAchievements).toHaveBeenCalledWith({ client, throwOnError: true });
    expect(sdk.getSelfRecommendation).toHaveBeenCalledWith({ client, throwOnError: true });
    expect(sdk.getStudentLeaderboard).toHaveBeenCalledWith({
      client,
      query: { limit: 60 },
      throwOnError: true,
    });
  });

  it("uses generated account operations for profile and sessions", async () => {
    await expect(saveDisplayName(session, "新名字")).resolves.toEqual(account);
    await expect(loadAccountSessions(session)).resolves.toEqual(sessionList);

    expect(sdk.updateAccountProfile).toHaveBeenCalledWith({
      client,
      body: { displayName: "新名字" },
      throwOnError: true,
    });
    expect(sdk.listAccountSessions).toHaveBeenCalledWith({
      client,
      throwOnError: true,
    });
  });

  it("forgets local state only after revoking the current session", async () => {
    const otherId = "223e4567-e89b-42d3-a456-426614174001";
    const currentId = "323e4567-e89b-42d3-a456-426614174002";

    await revokeSession(session, otherId, false);
    expect(forgetLocalSession).not.toHaveBeenCalled();
    await revokeSession(session, currentId, true);

    expect(sdk.revokeAccountSession).toHaveBeenNthCalledWith(1, {
      client,
      path: { sessionId: otherId },
      throwOnError: true,
    });
    expect(sdk.revokeAccountSession).toHaveBeenNthCalledWith(2, {
      client,
      path: { sessionId: currentId },
      throwOnError: true,
    });
    expect(forgetLocalSession).toHaveBeenCalledTimes(1);
  });

  it("uses generated durable Chat and Agent contracts", async () => {
    await expect(loadChatThreads(session, 20, thread.id)).resolves.toEqual(threadPage);
    await expect(createStudentChatThread(session)).resolves.toEqual(thread);
    await expect(loadChatMessages(session, thread.id, 4, 100)).resolves.toEqual(messagePage);
    await expect(enqueueChatReply(session, thread.id, {
      clientRequestId: agentRun.clientRequestId,
      content: enqueueResult.message.content,
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
    })).resolves.toEqual(enqueueResult);
    await expect(enqueueAutomaticAnalysis(session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 12,
    })).resolves.toEqual(enqueueResult);
    await expect(loadAgentRun(session, agentRun.id)).resolves.toEqual(agentRun);

    expect(sdk.listChatThreads).toHaveBeenCalledWith({
      client,
      query: { limit: 20, cursor: thread.id },
      throwOnError: true,
    });
    expect(sdk.createChatThread).toHaveBeenCalledWith({ client, throwOnError: true });
    expect(sdk.listChatMessages).toHaveBeenCalledWith({
      client,
      path: { threadId: thread.id },
      query: { afterSequence: 4, limit: 100 },
      throwOnError: true,
    });
    expect(sdk.enqueueAgentRun).toHaveBeenCalledWith({
      client,
      path: { threadId: thread.id },
      body: {
        clientRequestId: agentRun.clientRequestId,
        kind: "reply",
        content: enqueueResult.message.content,
        promptConfigurationKey: "agent.prompt.default",
        modelConfigurationKey: "agent.model.default",
        expectedAnalyticsHeadRevision: null,
      },
      throwOnError: true,
    });
    expect(sdk.enqueueSelfAutoAnalysis).toHaveBeenCalledWith({
      client,
      body: {
        promptConfigurationKey: "agent.prompt.default",
        modelConfigurationKey: "agent.model.default",
        expectedAnalyticsHeadRevision: 12,
      },
      throwOnError: true,
    });
    expect(sdk.getAgentRun).toHaveBeenCalledWith({
      client,
      path: { runId: agentRun.id },
      throwOnError: true,
    });
  });

  it("opens one resumable authenticated Agent event stream without implicit retries", async () => {
    const abort = new AbortController();
    const result = await openAgentRunEventStream(session, agentRun.id, 7, abort.signal);
    const events = [];
    for await (const event of result.stream) events.push(event);

    expect(events).toHaveLength(1);
    expect(result.failure()).toBeUndefined();
    expect(sdk.streamAgentRunEvents).toHaveBeenCalledWith(expect.objectContaining({
      client,
      path: { runId: agentRun.id },
      headers: { "Last-Event-ID": "7" },
      signal: abort.signal,
      sseMaxRetryAttempts: 1,
    }));
  });

  it("uses generated CAS operations for versioned Agent notes", async () => {
    await expect(loadAgentNotes(session, 20, "cursor-1")).resolves.toEqual(agentNotePage);
    await expect(loadAgentNote(session, agentNote.id)).resolves.toEqual(agentNote);
    await expect(createStudentAgentNote(session, "学习计划", "复习图论")).resolves.toEqual(agentNoteMutation);
    await expect(replaceStudentAgentNote(session, agentNote, "更新", "复习树")).resolves.toEqual(agentNoteMutation);
    await expect(changeStudentAgentNoteState(session, agentNote, "archived")).resolves.toEqual(agentNoteMutation);

    expect(sdk.listAgentNotes).toHaveBeenCalledWith({
      client,
      query: { limit: 20, cursor: "cursor-1" },
      throwOnError: true,
    });
    expect(sdk.getAgentNote).toHaveBeenCalledWith({
      client,
      path: { noteId: agentNote.id },
      throwOnError: true,
    });
    expect(sdk.createAgentNote).toHaveBeenCalledWith(expect.objectContaining({
      client,
      body: expect.objectContaining({
        expectedHeadRevision: 0,
        title: "学习计划",
        content: "复习图论",
        mutationId: expect.any(String),
      }),
      throwOnError: true,
    }));
    expect(sdk.replaceAgentNote).toHaveBeenCalledWith(expect.objectContaining({
      client,
      path: { noteId: agentNote.id },
      body: expect.objectContaining({ expectedHeadRevision: 1, title: "更新", content: "复习树" }),
      throwOnError: true,
    }));
    expect(sdk.archiveAgentNote).toHaveBeenCalledWith(expect.objectContaining({
      client,
      path: { noteId: agentNote.id },
      body: expect.objectContaining({ expectedHeadRevision: 1 }),
      throwOnError: true,
    }));
  });

  it("submits authenticated feedback through the generated idempotent contract", async () => {
    await sendAuthenticatedFeedback(session, {
      title: "建议",
      content: "希望增加知识图谱。",
      platform: "web",
    });

    expect(sdk.submitAuthenticatedFeedback).toHaveBeenCalledWith(expect.objectContaining({
      client,
      body: expect.objectContaining({
        clientRequestId: expect.any(String),
        title: "建议",
        content: "希望增加知识图谱。",
        platform: "web",
        appVersion: null,
        userAgent: expect.any(String),
      }),
      throwOnError: true,
    }));
  });

  it("uses the generated OJ reads and exact multipart upload helper", async () => {
    await expect(loadOjProblems(session, 25, "one_sum", true)).resolves.toEqual(ojProblemPage);
    await expect(loadOjProblem(session, ojProblem.id)).resolves.toEqual(ojProblem);
    await expect(submitOjSource(session, ojProblem, "submit", "int main() {}\n", "")).resolves.toEqual(ojSubmissionResult);
    await expect(loadOjSubmission(session, ojSubmissionResult.submission.id)).resolves.toEqual(ojSubmissionDetail);

    expect(sdk.listOjProblems).toHaveBeenCalledWith({
      client,
      query: { limit: 25, includeArchived: true, afterSlug: "one_sum" },
      throwOnError: true,
    });
    expect(sdk.getOjProblem).toHaveBeenCalledWith({
      client,
      path: { problemId: ojProblem.id },
      throwOnError: true,
    });
    expect(sdk.uploadOjSubmission).toHaveBeenCalledWith(expect.objectContaining({
      client,
      metadata: expect.objectContaining({
        clientRequestId: expect.any(String),
        problemId: ojProblem.id,
        expectedProblemHeadRevision: 1,
        mode: "submit",
        languageId: "cpp20",
      }),
      source: expect.objectContaining({ filename: "main.cpp", data: expect.any(Blob) }),
    }));
    expect(sdk.getOjSubmission).toHaveBeenCalledWith({
      client,
      path: { submissionId: ojSubmissionResult.submission.id },
      throwOnError: true,
    });
  });

  it("resumes OJ judge events and publishes admin problem versions", async () => {
    const abort = new AbortController();
    const stream = await openOjJudgeEventStream(
      session,
      ojSubmissionResult.submission.id,
      9,
      abort.signal,
    );
    const events = [];
    for await (const event of stream.stream) events.push(event);
    expect(events).toHaveLength(1);
    expect(sdk.streamOjJudgeEvents).toHaveBeenCalledWith(expect.objectContaining({
      client,
      path: { submissionId: ojSubmissionResult.submission.id },
      headers: { "Last-Event-ID": "9" },
      signal: abort.signal,
      sseMaxRetryAttempts: 1,
    }));

    const metadata = {
      slug: ojProblem.slug,
      expectedHeadRevision: 1,
      lifecycle: "active" as const,
      title: "Two Sum",
      statementMarkdown: "Find two values.",
      solutionMarkdown: null,
      knowledgeTags: ["array"],
      timeLimitMs: 1000,
      memoryLimitBytes: 268435456,
      outputLimitBytes: 1048576,
      problemSpec: { comparison: "tokens" },
    };
    const bundle = new File(["bundle"], "tests.tar", { type: "application/x-tar" });
    await expect(publishOjProblemVersion(session, metadata, bundle)).resolves.toEqual(ojProblemVersionResult);
    expect(sdk.uploadOjProblemVersion).toHaveBeenCalledWith({
      client,
      metadata,
      testBundle: { data: bundle, filename: "tests.tar" },
    });
  });
});
