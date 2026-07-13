import type { BrowserSession, RecommendationModelProvenance, SelfRecommendation } from "@ascendany/sdk";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sdk = vi.hoisted(() => ({
  enqueueSelfAutoAnalysis: vi.fn(),
  getExam: vi.fn(),
  getSelfStudentAnalytics: vi.fn(),
  getSelfAchievements: vi.fn(),
  getSelfRecommendation: vi.fn(),
  getStudentLeaderboard: vi.fn(),
  listExams: vi.fn(),
  listAccountSessions: vi.fn(),
  revokeAccountSession: vi.fn(),
  updateAccountProfile: vi.fn(),
}));

vi.mock("@ascendany/sdk", () => sdk);

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
} from "../src/api/operations";

function fakeSession() {
  return {
    client: { marker: "desktop-generated-client" },
    ensureAuthenticated: vi.fn().mockResolvedValue({}),
    forgetLocalSession: vi.fn().mockResolvedValue(undefined),
  } as unknown as BrowserSession;
}

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
const recommendation: SelfRecommendation = {
  state: "unavailable", unavailableReason: "analytics_unavailable",
  currentAnalyticsHeadRevision: 0, modelHeadRevision: 5, model: recommendationModel,
};

describe("desktop generated v2 operations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sdk.getSelfStudentAnalytics.mockResolvedValue({ data: { state: "not_generated", headRevision: 0 } });
    sdk.getSelfAchievements.mockResolvedValue({
      data: {
        state: "not_generated",
        analyticsHeadRevision: 0,
        ruleSetVersion: 3,
        ruleHeadRevision: 7,
        summary: { total: 1, locked: 1, bronze: 0, silver: 0, gold: 0 },
        items: [{ code: "first_exam", title: "初次登场", description: "完成第一场考试。", progressKey: "exam_count", tier: 0, progress: 0, bronzeTarget: 1, silverTarget: 5, goldTarget: 10, sortOrder: 1 }],
      },
    });
    sdk.getSelfRecommendation.mockResolvedValue({
      data: recommendation,
    });
    sdk.getStudentLeaderboard.mockResolvedValue({
      data: { state: "not_generated", headRevision: 0, population: 0, items: [] },
    });
    sdk.listExams.mockResolvedValue({ data: { items: [], nextCursor: null } });
    sdk.getExam.mockResolvedValue({ data: { id: "123e4567-e89b-42d3-a456-426614174010" } });
    sdk.updateAccountProfile.mockResolvedValue({ data: { id: "account" } });
    sdk.listAccountSessions.mockResolvedValue({ data: { items: [] } });
    sdk.revokeAccountSession.mockResolvedValue({ data: undefined });
    sdk.enqueueSelfAutoAnalysis.mockResolvedValue({ data: { created: false } });
  });

  it("reads exam catalog and details through generated operations", async () => {
    const session = fakeSession();
    const cursor = "123e4567-e89b-42d3-a456-426614174099";

    await expect(loadExams(session, 12, cursor)).resolves.toEqual({ items: [], nextCursor: null });
    await expect(loadExam(session, "123e4567-e89b-42d3-a456-426614174010")).resolves.toMatchObject({
      id: "123e4567-e89b-42d3-a456-426614174010",
    });

    expect(sdk.listExams).toHaveBeenCalledWith({
      client: session.client,
      query: { limit: 12, cursor },
      throwOnError: true,
    });
    expect(sdk.getExam).toHaveBeenCalledWith({
      client: session.client,
      path: { examId: "123e4567-e89b-42d3-a456-426614174010" },
      throwOnError: true,
    });
  });

  it("authorizes before generated student insight reads", async () => {
    const session = fakeSession();

    await expect(loadSelfAnalytics(session, 25)).resolves.toMatchObject({
      state: "not_generated",
    });
    await expect(loadSelfAchievements(session)).resolves.toMatchObject({
      ruleSetVersion: 3,
    });
    await expect(loadSelfRecommendation(session)).resolves.toMatchObject({
      state: "unavailable",
    });
    await expect(loadStudentLeaderboard(session, 40)).resolves.toMatchObject({
      state: "not_generated",
    });

    expect(session.ensureAuthenticated).toHaveBeenCalledTimes(4);
    expect(sdk.getSelfStudentAnalytics).toHaveBeenCalledWith({
      client: session.client,
      query: { limit: 25 },
      throwOnError: true,
    });
    expect(sdk.getSelfAchievements).toHaveBeenCalledWith({
      client: session.client,
      throwOnError: true,
    });
    expect(sdk.getSelfRecommendation).toHaveBeenCalledWith({
      client: session.client,
      throwOnError: true,
    });
    expect(sdk.getStudentLeaderboard).toHaveBeenCalledWith({
      client: session.client,
      query: { limit: 40 },
      throwOnError: true,
    });
  });

  it("uses generated profile and session operations", async () => {
    const session = fakeSession();

    await saveDisplayName(session, "新名称");
    await loadAccountSessions(session);

    expect(sdk.updateAccountProfile).toHaveBeenCalledWith({
      client: session.client,
      body: { displayName: "新名称" },
      throwOnError: true,
    });
    expect(sdk.listAccountSessions).toHaveBeenCalledWith({
      client: session.client,
      throwOnError: true,
    });
  });

  it("enqueues automatic analysis with the exact analytics head", async () => {
    const session = fakeSession();

    await expect(enqueueAutomaticAnalysis(session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 9,
    })).resolves.toEqual({ created: false });

    expect(sdk.enqueueSelfAutoAnalysis).toHaveBeenCalledWith({
      client: session.client,
      body: {
        promptConfigurationKey: "agent.prompt.default",
        modelConfigurationKey: "agent.model.default",
        expectedAnalyticsHeadRevision: 9,
      },
      throwOnError: true,
    });
  });

  it("forgets only the locally revoked current session", async () => {
    const session = fakeSession();

    await revokeSession(session, "123e4567-e89b-42d3-a456-426614174000", false);
    expect(session.forgetLocalSession).not.toHaveBeenCalled();

    await revokeSession(session, "123e4567-e89b-42d3-a456-426614174001", true);
    expect(sdk.revokeAccountSession).toHaveBeenLastCalledWith({
      client: session.client,
      path: { sessionId: "123e4567-e89b-42d3-a456-426614174001" },
      throwOnError: true,
    });
    expect(session.forgetLocalSession).toHaveBeenCalledTimes(1);
  });
});
