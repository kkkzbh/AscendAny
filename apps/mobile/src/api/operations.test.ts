import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Account, AccountSessionList, BrowserSession, ExamDetail, ExamPage, RecommendationModelProvenance, SelfAchievements, SelfRecommendation, SelfStudentAnalytics, StudentLeaderboard } from "@ascendany/sdk";
import {
  enqueueAutomaticAnalysis,
  loadExam,
  loadExams,
  loadAccountSessions,
  loadSelfAchievements,
  loadSelfAnalytics,
  loadSelfRecommendation,
  loadStudentLeaderboard,
  revokeSession,
  saveDisplayName,
} from "./operations";

const sdk = vi.hoisted(() => ({
  enqueueSelfAutoAnalysis: vi.fn(),
  getExam: vi.fn(),
  getSelfAchievements: vi.fn(),
  getSelfRecommendation: vi.fn(),
  getSelfStudentAnalytics: vi.fn(),
  getStudentLeaderboard: vi.fn(),
  listExams: vi.fn(),
  listAccountSessions: vi.fn(),
  revokeAccountSession: vi.fn(),
  updateAccountProfile: vi.fn(),
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

const analytics: SelfStudentAnalytics = { state: "not_generated", headRevision: 0 };
const achievements: SelfAchievements = {
  state: "not_generated",
  analyticsHeadRevision: 0,
  ruleSetVersion: 3,
  ruleHeadRevision: 7,
  summary: { total: 1, locked: 1, bronze: 0, silver: 0, gold: 0 },
  items: [{ code: "first_exam", title: "初次登场", description: "完成第一场考试。", progressKey: "exam_count", tier: 0, progress: 0, bronzeTarget: 1, silverTarget: 5, goldTarget: 10, sortOrder: 1 }],
};
const recommendationModel: RecommendationModelProvenance = {
  modelId: "123e4567-e89b-42d3-a456-426614174000",
  purpose: "acceptance_test",
  artifactSha256: "a".repeat(64), artifactSizeBytes: 4096, artifactMode: 420,
  modelSchema: "ascendany.recommendation.inference-model.v1",
  algorithm: "knowledge_mirt_feature_v1", inferenceContract: "ascendany.recommendation.inference.v1",
  trainedAt: "2026-07-11T08:00:00Z", trainingProvenanceSha256: "b".repeat(64),
  featureSchemaSha256: "c".repeat(64), knowledgeCatalogSha256: "d".repeat(64),
  parameterSha256: "e".repeat(64), goldenVectorsSha256: "f".repeat(64),
  modelHeadRevision: 5, applicationVersion: "0.2.0", applicationCommit: "1".repeat(40),
  applicationBuildTime: "2026-07-11T08:05:00Z",
};
const recommendation: SelfRecommendation = { state: "unavailable", unavailableReason: "analytics_unavailable", currentAnalyticsHeadRevision: 0, modelHeadRevision: 5, model: recommendationModel };
const leaderboard: StudentLeaderboard = { state: "not_generated", headRevision: 0, population: 0, items: [] };
const sessionList: AccountSessionList = { items: [] };
const examPage: ExamPage = { items: [], nextCursor: null };
const examDetail = { id: "123e4567-e89b-42d3-a456-426614174010" } as ExamDetail;
const client = { kind: "browser-session-client" };
const ensureAuthenticated = vi.fn();
const forgetLocalSession = vi.fn();
const session = { client, ensureAuthenticated, forgetLocalSession } as unknown as BrowserSession;

describe("mobile v2 operations", () => {
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
    sdk.enqueueSelfAutoAnalysis.mockResolvedValue({ data: { created: false } });
  });

  it("reads exams through generated operations", async () => {
    const cursor = "123e4567-e89b-42d3-a456-426614174099";
    await expect(loadExams(session, 12, cursor)).resolves.toEqual(examPage);
    await expect(loadExam(session, examDetail.id)).resolves.toEqual(examDetail);

    expect(sdk.listExams).toHaveBeenCalledWith({ client, query: { limit: 12, cursor }, throwOnError: true });
    expect(sdk.getExam).toHaveBeenCalledWith({ client, path: { examId: examDetail.id }, throwOnError: true });
  });

  it("reads student insights through authenticated generated operations", async () => {
    await expect(loadSelfAnalytics(session, 25)).resolves.toEqual(analytics);
    await expect(loadSelfAchievements(session)).resolves.toEqual(achievements);
    await expect(loadSelfRecommendation(session)).resolves.toEqual(recommendation);
    await expect(loadStudentLeaderboard(session, 60)).resolves.toEqual(leaderboard);

    expect(ensureAuthenticated).toHaveBeenCalledTimes(4);
    expect(sdk.getSelfStudentAnalytics).toHaveBeenCalledWith({ client, query: { limit: 25 }, throwOnError: true });
    expect(sdk.getSelfAchievements).toHaveBeenCalledWith({ client, throwOnError: true });
    expect(sdk.getSelfRecommendation).toHaveBeenCalledWith({ client, throwOnError: true });
    expect(sdk.getStudentLeaderboard).toHaveBeenCalledWith({ client, query: { limit: 60 }, throwOnError: true });
  });

  it("updates profile and lists sessions through v2 account operations", async () => {
    await expect(saveDisplayName(session, "新名字")).resolves.toEqual(account);
    await expect(loadAccountSessions(session)).resolves.toEqual(sessionList);

    expect(sdk.updateAccountProfile).toHaveBeenCalledWith({ client, body: { displayName: "新名字" }, throwOnError: true });
    expect(sdk.listAccountSessions).toHaveBeenCalledWith({ client, throwOnError: true });
  });

  it("enqueues automatic analysis with the exact analytics head", async () => {
    await expect(enqueueAutomaticAnalysis(session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 9,
    })).resolves.toEqual({ created: false });

    expect(sdk.enqueueSelfAutoAnalysis).toHaveBeenCalledWith({
      client,
      body: {
        promptConfigurationKey: "agent.prompt.default",
        modelConfigurationKey: "agent.model.default",
        expectedAnalyticsHeadRevision: 9,
      },
      throwOnError: true,
    });
  });

  it("forgets BrowserSession state only when revoking the current session", async () => {
    const otherId = "223e4567-e89b-42d3-a456-426614174001";
    const currentId = "323e4567-e89b-42d3-a456-426614174002";

    await revokeSession(session, otherId, false);
    expect(forgetLocalSession).not.toHaveBeenCalled();
    await revokeSession(session, currentId, true);

    expect(sdk.revokeAccountSession).toHaveBeenNthCalledWith(1, { client, path: { sessionId: otherId }, throwOnError: true });
    expect(sdk.revokeAccountSession).toHaveBeenNthCalledWith(2, { client, path: { sessionId: currentId }, throwOnError: true });
    expect(forgetLocalSession).toHaveBeenCalledTimes(1);
  });
});
