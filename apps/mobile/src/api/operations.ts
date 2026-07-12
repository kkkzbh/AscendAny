import {
  createChatThread,
  enqueueSelfAutoAnalysis,
  enqueueAgentRun,
  getAgentRun,
  getExam,
  getOjProblem,
  getOjSubmission,
  getSelfAchievements,
  getSelfRecommendation,
  getSelfStudentAnalytics,
  getStudentLeaderboard,
  listExams,
  listAccountSessions,
  listChatMessages,
  listChatThreads,
  listOjProblems,
  revokeAccountSession,
  updateAccountProfile,
  streamAgentRunEvents,
  streamOjJudgeEvents,
  uploadOjProblemVersion,
  uploadOjSubmission,
  type Account,
  type AccountSessionList,
  type AgentRun,
  type AgentRunEnqueueResult,
  type AgentRunEvent,
  type BrowserSession,
  type ChatMessagePage,
  type ChatThread,
  type ChatThreadPage,
  type ExamDetail,
  type ExamPage,
  type OjCreateProblemVersionResult,
  type OjCreateSubmissionResult,
  type OjJudgeEvent,
  type OjProblem,
  type OjProblemPage,
  type OjProblemVersionMetadata,
  type OjSubmissionDetail,
  type OjSubmissionMode,
  type SelfAchievements,
  type SelfRecommendation,
  type SelfStudentAnalytics,
  type StudentLeaderboard,
} from "@ascendany/sdk";

export interface AgentRunEventStream {
  stream: AsyncGenerator<AgentRunEvent>;
  failure: () => unknown;
}

export interface OjJudgeEventStream {
  stream: AsyncGenerator<OjJudgeEvent>;
  failure: () => unknown;
}

export async function loadExams(
  session: BrowserSession,
  limit = 20,
  cursor?: string,
): Promise<ExamPage> {
  await session.ensureAuthenticated();
  const result = await listExams({
    client: session.client,
    query: { limit, ...(cursor === undefined ? {} : { cursor }) },
    throwOnError: true,
  });
  return result.data;
}

export async function loadExam(session: BrowserSession, examId: string): Promise<ExamDetail> {
  await session.ensureAuthenticated();
  const result = await getExam({
    client: session.client,
    path: { examId },
    throwOnError: true,
  });
  return result.data;
}

export async function loadSelfAnalytics(
  session: BrowserSession,
  limit = 50,
): Promise<SelfStudentAnalytics> {
  await session.ensureAuthenticated();
  const result = await getSelfStudentAnalytics({
    client: session.client,
    query: { limit },
    throwOnError: true,
  });
  return result.data;
}

export async function loadSelfRecommendation(
  session: BrowserSession,
): Promise<SelfRecommendation> {
  await session.ensureAuthenticated();
  const result = await getSelfRecommendation({
    client: session.client,
    throwOnError: true,
  });
  return result.data;
}

export async function loadSelfAchievements(
  session: BrowserSession,
): Promise<SelfAchievements> {
  await session.ensureAuthenticated();
  const result = await getSelfAchievements({
    client: session.client,
    throwOnError: true,
  });
  return result.data;
}

export async function loadStudentLeaderboard(
  session: BrowserSession,
  limit = 100,
): Promise<StudentLeaderboard> {
  await session.ensureAuthenticated();
  const result = await getStudentLeaderboard({
    client: session.client,
    query: { limit },
    throwOnError: true,
  });
  return result.data;
}

export async function saveDisplayName(
  session: BrowserSession,
  displayName: string,
): Promise<Account> {
  await session.ensureAuthenticated();
  const result = await updateAccountProfile({
    client: session.client,
    body: { displayName },
    throwOnError: true,
  });
  return result.data;
}

export async function loadAccountSessions(session: BrowserSession): Promise<AccountSessionList> {
  await session.ensureAuthenticated();
  const result = await listAccountSessions({
    client: session.client,
    throwOnError: true,
  });
  return result.data;
}

export async function revokeSession(
  session: BrowserSession,
  sessionId: string,
  current: boolean,
): Promise<void> {
  await session.ensureAuthenticated();
  await revokeAccountSession({
    client: session.client,
    path: { sessionId },
    throwOnError: true,
  });
  if (current) {
    await session.forgetLocalSession();
  }
}

export async function loadChatThreads(
  session: BrowserSession,
  limit = 50,
  cursor?: string,
): Promise<ChatThreadPage> {
  await session.ensureAuthenticated();
  const result = await listChatThreads({
    client: session.client,
    query: { limit, ...(cursor === undefined ? {} : { cursor }) },
    throwOnError: true,
  });
  return result.data;
}

export async function createStudentChatThread(session: BrowserSession): Promise<ChatThread> {
  await session.ensureAuthenticated();
  const result = await createChatThread({ client: session.client, throwOnError: true });
  return result.data;
}

export async function loadChatMessages(
  session: BrowserSession,
  threadId: string,
  afterSequence = 0,
  limit = 200,
): Promise<ChatMessagePage> {
  await session.ensureAuthenticated();
  const result = await listChatMessages({
    client: session.client,
    path: { threadId },
    query: { afterSequence, limit },
    throwOnError: true,
  });
  return result.data;
}

export async function enqueueChatReply(
  session: BrowserSession,
  threadId: string,
  input: {
    clientRequestId: string;
    content: string;
    promptConfigurationKey: string;
    modelConfigurationKey: string;
  },
): Promise<AgentRunEnqueueResult> {
  await session.ensureAuthenticated();
  const result = await enqueueAgentRun({
    client: session.client,
    path: { threadId },
    body: {
      clientRequestId: input.clientRequestId,
      kind: "reply",
      content: input.content,
      promptConfigurationKey: input.promptConfigurationKey,
      modelConfigurationKey: input.modelConfigurationKey,
      expectedAnalyticsHeadRevision: null,
    },
    throwOnError: true,
  });
  return result.data;
}

export async function enqueueAutomaticAnalysis(
  session: BrowserSession,
  input: {
    promptConfigurationKey: string;
    modelConfigurationKey: string;
    expectedAnalyticsHeadRevision: number;
  },
): Promise<AgentRunEnqueueResult> {
  await session.ensureAuthenticated();
  const result = await enqueueSelfAutoAnalysis({
    client: session.client,
    body: input,
    throwOnError: true,
  });
  return result.data;
}

export async function loadAgentRun(session: BrowserSession, runId: string): Promise<AgentRun> {
  await session.ensureAuthenticated();
  const result = await getAgentRun({
    client: session.client,
    path: { runId },
    throwOnError: true,
  });
  return result.data;
}

export async function openAgentRunEventStream(
  session: BrowserSession,
  runId: string,
  afterSequence: number,
  signal: AbortSignal,
): Promise<AgentRunEventStream> {
  await session.ensureAuthenticated();
  let streamFailure: unknown;
  const result = await streamAgentRunEvents({
    client: session.client,
    path: { runId },
    headers: { "Last-Event-ID": String(afterSequence) },
    signal,
    sseMaxRetryAttempts: 1,
    onSseError: (error) => {
      streamFailure = error;
    },
  });
  return { stream: result.stream, failure: () => streamFailure };
}

export async function loadOjProblems(
  session: BrowserSession,
  limit = 50,
  cursor?: string,
  includeArchived = false,
): Promise<OjProblemPage> {
  await session.ensureAuthenticated();
  const result = await listOjProblems({
    client: session.client,
    query: {
      limit,
      ...(includeArchived ? { includeArchived: true as const } : {}),
      ...(cursor === undefined ? {} : { afterSlug: cursor }),
    },
    throwOnError: true,
  });
  return result.data;
}

export async function loadOjProblem(
  session: BrowserSession,
  problemId: string,
): Promise<OjProblem> {
  await session.ensureAuthenticated();
  const result = await getOjProblem({
    client: session.client,
    path: { problemId },
    throwOnError: true,
  });
  return result.data;
}

export async function submitOjSource(
  session: BrowserSession,
  problem: OjProblem,
  mode: OjSubmissionMode,
  source: string,
  stdin: string,
): Promise<OjCreateSubmissionResult> {
  await session.ensureAuthenticated();
  const metadata = {
    clientRequestId: crypto.randomUUID(),
    problemId: problem.id,
    expectedProblemHeadRevision: problem.headRevision,
    mode,
    languageId: "cpp20" as const,
  };
  const sourceUpload = {
    data: new Blob([source], { type: "text/x-c++src; charset=utf-8" }),
    filename: "main.cpp",
  };
  const result = mode === "run"
    ? await uploadOjSubmission({
        client: session.client,
        metadata: { ...metadata, mode: "run" },
        source: sourceUpload,
        stdin: {
          data: new Blob([stdin], { type: "text/plain; charset=utf-8" }),
          filename: "stdin.txt",
        },
      })
    : await uploadOjSubmission({
        client: session.client,
        metadata: { ...metadata, mode: "submit" },
        source: sourceUpload,
      });
  if (result.error !== undefined) throw result.error;
  if (result.data === undefined) throw new Error("OJ submission response is missing.");
  return result.data;
}

export async function loadOjSubmission(
  session: BrowserSession,
  submissionId: string,
): Promise<OjSubmissionDetail> {
  await session.ensureAuthenticated();
  const result = await getOjSubmission({
    client: session.client,
    path: { submissionId },
    throwOnError: true,
  });
  return result.data;
}

export async function openOjJudgeEventStream(
  session: BrowserSession,
  submissionId: string,
  afterSequence: number,
  signal: AbortSignal,
): Promise<OjJudgeEventStream> {
  await session.ensureAuthenticated();
  let streamFailure: unknown;
  const result = await streamOjJudgeEvents({
    client: session.client,
    path: { submissionId },
    headers: { "Last-Event-ID": String(afterSequence) },
    signal,
    sseMaxRetryAttempts: 1,
    onSseError: (error) => {
      streamFailure = error;
    },
  });
  return { stream: result.stream, failure: () => streamFailure };
}

export async function publishOjProblemVersion(
  session: BrowserSession,
  metadata: OjProblemVersionMetadata,
  testBundle: File,
): Promise<OjCreateProblemVersionResult> {
  await session.ensureAuthenticated();
  const result = await uploadOjProblemVersion({
    client: session.client,
    metadata,
    testBundle: { data: testBundle, filename: testBundle.name },
  });
  if (result.error !== undefined) throw result.error;
  if (result.data === undefined) throw new Error("OJ problem response is missing.");
  return result.data;
}
