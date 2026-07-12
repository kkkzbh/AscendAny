import { cleanup, render, screen } from "@testing-library/react";
import type { SelfAchievements, SelfRecommendation } from "@ascendany/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AchievementPanel } from "./AchievementPanel";
import { RecommendationPanel } from "./RecommendationPanel";

const mocks = vi.hoisted(() => ({ loadSelfAchievements: vi.fn(), loadSelfRecommendation: vi.fn(), useSession: vi.fn() }));
vi.mock("../api/operations", () => ({ loadSelfAchievements: mocks.loadSelfAchievements, loadSelfRecommendation: mocks.loadSelfRecommendation }));
vi.mock("../session/SessionContext", () => ({ useSession: mocks.useSession }));

const achievements = {
  state: "no_observations",
  analyticsHeadRevision: 6,
  ruleSetVersion: 2,
  ruleHeadRevision: 8,
  summary: { total: 2, locked: 1, bronze: 1, silver: 0, gold: 0 },
  items: [
    { code: "first_exam", title: "初次登场", description: "完成考试。", progressKey: "exam_count", tier: 0, progress: 0, bronzeTarget: 1, silverTarget: 5, goldTarget: 10, sortOrder: 1 },
    { code: "dialogue", title: "勤学善问", description: "完成对话。", progressKey: "ai_dialogue_count", tier: 1, progress: 3, bronzeTarget: 1, silverTarget: 10, goldTarget: 30, sortOrder: 2 },
  ],
} satisfies SelfAchievements;

const recommendation = {
  state: "stale",
  currentAnalyticsGenerationId: "19",
  currentAnalyticsHeadRevision: 6,
  recommendationHeadRevision: 3,
  model: {
    modelId: "123e4567-e89b-42d3-a456-426614174000", trainingRunId: "223e4567-e89b-42d3-a456-426614174000", analyticsGenerationId: "18", analyticsHeadRevision: 5,
    inputManifestSha256: "a".repeat(64), trainingConfigurationVersionId: "20", trainingConfigurationKey: "recommendation.training.mobile", trainingConfigurationVersion: 2,
    trainingConfigurationSchema: "ascendany.training.recommendation.v2", trainingConfigurationSha256: "b".repeat(64),
    knowledgeCatalogVersionId: "30", knowledgeCatalogKey: "recommendation.knowledge.mobile", knowledgeCatalogVersion: 1,
    knowledgeCatalogSchema: "ascendany.knowledge_catalog.recommendation.v1", knowledgeCatalogSha256: "f".repeat(64),
    outputArtifactSha256: "c".repeat(64), modelSchema: "ascendany.recommendation.model.v2",
    modelManifest: {}, modelManifestSha256: "d".repeat(64), metrics: {}, createdAt: "2026-07-11T08:00:00Z",
  },
  result: {
    schema: "ascendany.recommendation.result.v2", sha256: "e".repeat(64), status: "insufficient", sourceRating: 980,
    evidence: { trainInteractionCount: 5, validationInteractionCount: 1, distinctProblemCount: 4, passedProblemCount: 1 },
    knowledgeMastery: [{ knowledgePointId: "arrays", label: "数组", description: "数组基础。", prerequisiteIds: [], mastery: 0.35, trainInteractionCount: 5 }],
    insufficiency: { reasonCode: "problem_candidates_below_minimum", minimumPathSteps: 2, candidatePathSteps: 2, problemsPerStep: 2, eligibleProblemCount: 1, blockedKnowledgePointIds: ["arrays"] },
  },
} satisfies SelfRecommendation;
const readyRecommendation = {
  state: "fresh",
  currentAnalyticsGenerationId: "19",
  currentAnalyticsHeadRevision: 6,
  recommendationHeadRevision: 4,
  model: {
    modelId: "123e4567-e89b-42d3-a456-426614174000",
    trainingRunId: "223e4567-e89b-42d3-a456-426614174000",
    analyticsGenerationId: "19",
    analyticsHeadRevision: 6,
    inputManifestSha256: "a".repeat(64),
    trainingConfigurationVersionId: "20",
    trainingConfigurationKey: "recommendation.training.mobile",
    trainingConfigurationVersion: 2,
    trainingConfigurationSchema: "ascendany.training.recommendation.v2",
    trainingConfigurationSha256: "b".repeat(64),
    knowledgeCatalogVersionId: "30",
    knowledgeCatalogKey: "recommendation.knowledge.mobile",
    knowledgeCatalogVersion: 1,
    knowledgeCatalogSchema: "ascendany.knowledge_catalog.recommendation.v1",
    knowledgeCatalogSha256: "f".repeat(64),
    outputArtifactSha256: "c".repeat(64),
    modelSchema: "ascendany.recommendation.model.v2",
    modelManifest: {},
    modelManifestSha256: "d".repeat(64),
    metrics: {},
    createdAt: "2026-07-11T08:00:00Z",
  },
  result: {
    schema: "ascendany.recommendation.result.v2",
    sha256: "e".repeat(64),
    status: "ready",
    sourceRating: 980,
    evidence: { trainInteractionCount: 5, validationInteractionCount: 1, distinctProblemCount: 4, passedProblemCount: 1 },
    knowledgeMastery: [
      { knowledgePointId: "arrays", label: "数组", description: "数组基础。", prerequisiteIds: [], mastery: 0.35, trainInteractionCount: 5 },
    ],
    learningPath: [
      {
        order: 1,
        knowledgePointId: "arrays",
        label: "数组",
        description: "数组基础。",
        prerequisiteIds: [],
        mastery: 0.35,
        targetMastery: 0.8,
        reasonCode: "knowledge_gap",
        recommendedProblems: [
          {
            problemKey: "pintia:501:fact",
            sourceProblemKey: "pintia:501",
            platform: "pintia",
            problemId: "501",
            title: "数组练习",
            sourceProblemSets: [
              { problemSetId: "1001", sourceUrl: "https://pintia.cn/problem-sets/1001" },
              { problemSetId: "1001", sourceUrl: "https://pintia.cn/problem-sets/1001/problems/type/7" },
            ],
            predictedSuccessProbability: 0.67,
            recommendationScore: 0.2,
            rankingEvidence: { knowledgeGap: 0.45, successDistance: 0.03, stepKnowledgeWeight: 1 },
          },
        ],
      },
    ],
  },
} satisfies SelfRecommendation;

describe("mobile student insight panels", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useSession.mockReturnValue({ session: { marker: "mobile-session" } });
    mocks.loadSelfAchievements.mockResolvedValue(achievements);
    mocks.loadSelfRecommendation.mockResolvedValue(recommendation);
  });
  afterEach(cleanup);

  it("keeps all rules visible without observations and exposes stale recommendation provenance", async () => {
    const { container } = render(<><AchievementPanel /><RecommendationPanel /></>);
    await screen.findByText("暂无考试观测");
    await screen.findByText("个性化学习建议");

    const content = container.textContent ?? "";
    expect(content).toContain("初次登场");
    expect(content).toContain("勤学善问");
    expect(content).toContain("青铜");
    expect(content).toContain("规则集v2");
    expect(content).toContain("待更新");
    expect(content).toContain("分析数据已更新");
    expect(content).toContain("部分知识点缺少足量且适合的练习题");
    expect(content).toContain("recommendation.training.mobile v2");
    expect(content).toContain("recommendation.knowledge.mobile v1");
    expect(mocks.loadSelfAchievements).toHaveBeenCalledWith({ marker: "mobile-session" });
    expect(mocks.loadSelfRecommendation).toHaveBeenCalledWith({ marker: "mobile-session" });
  });

  it("shows problem IDs and preserves duplicate-set canonical source URLs", async () => {
    mocks.loadSelfRecommendation.mockResolvedValueOnce(readyRecommendation);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const { container } = render(<RecommendationPanel />);
      await screen.findByText("题目 ID 501");

      expect(container.textContent).toContain("数组练习");
      expect(container.textContent).toContain("预计通过 67%");
      const links = screen.getAllByRole("link", { name: "打开题目集 1001" });
      expect(links).toHaveLength(2);
      expect(links.map((link) => link.getAttribute("href"))).toEqual([
        "https://pintia.cn/problem-sets/1001",
        "https://pintia.cn/problem-sets/1001/problems/type/7",
      ]);
      for (const link of links) {
        expect(link.getAttribute("target")).toBe("_blank");
        expect(link.getAttribute("rel")).toBe("noopener noreferrer");
        expect(link.getAttribute("href")).not.toContain("/problems/501");
      }
      expect(consoleError).not.toHaveBeenCalled();
    } finally {
      consoleError.mockRestore();
    }
  });
});
