import { cleanup, render, screen } from "@testing-library/react";
import type { RecommendationModelProvenance, SelfAchievements, SelfRecommendation } from "@ascendany/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AchievementPanel } from "../src/components/AchievementPanel";
import { RecommendationPanel } from "../src/components/RecommendationPanel";

const mocks = vi.hoisted(() => ({ loadSelfAchievements: vi.fn(), loadSelfRecommendation: vi.fn(), useSession: vi.fn() }));
vi.mock("../src/api/operations", () => ({ loadSelfAchievements: mocks.loadSelfAchievements, loadSelfRecommendation: mocks.loadSelfRecommendation }));
vi.mock("../src/session/context", () => ({ useSession: mocks.useSession }));

const achievements = {
  state: "not_generated",
  analyticsHeadRevision: 0,
  ruleSetVersion: 1,
  ruleHeadRevision: 2,
  summary: { total: 2, locked: 2, bronze: 0, silver: 0, gold: 0 },
  items: [
    { code: "first_exam", title: "初次登场", description: "完成考试。", progressKey: "exam_count", tier: 0, progress: 0, bronzeTarget: 1, silverTarget: 5, goldTarget: 10, sortOrder: 1 },
    { code: "dialogue", title: "勤学善问", description: "完成对话。", progressKey: "ai_dialogue_count", tier: 0, progress: 0, bronzeTarget: 1, silverTarget: 10, goldTarget: 30, sortOrder: 2 },
  ],
} satisfies SelfAchievements;
const model = {
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
} satisfies RecommendationModelProvenance;
const recommendation = { state: "unavailable", unavailableReason: "analytics_unavailable", currentAnalyticsHeadRevision: 0, modelHeadRevision: 5, model } satisfies SelfRecommendation;
const readyRecommendation = {
  state: "fresh",
  currentAnalyticsGenerationId: "28",
  currentAnalyticsHeadRevision: 12,
  modelHeadRevision: 5,
  model,
  result: {
    schema: "ascendany.recommendation.inference-result.v1",
    sha256: "9".repeat(64),
    status: "ready",
    sourceRating: 1810,
    evidence: { observationCount: 10, distinctProblemCount: 6, passedProblemCount: 3 },
    knowledgeMastery: [
      { knowledgePointId: "arrays", label: "数组", description: "数组基础与索引。", prerequisiteIds: [], mastery: 0.42, observationCount: 4 },
    ],
    learningPath: [
      {
        order: 1,
        knowledgePointId: "arrays",
        label: "数组",
        description: "数组基础与索引。",
        prerequisiteIds: [],
        mastery: 0.42,
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
            rankingEvidence: { knowledgeGap: 0.38, successDistance: 0.03, stepKnowledgeWeight: 1 },
          },
        ],
      },
    ],
  },
} satisfies SelfRecommendation;

describe("desktop student insight panels", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useSession.mockReturnValue({ session: { marker: "desktop-session" } });
    mocks.loadSelfAchievements.mockResolvedValue(achievements);
    mocks.loadSelfRecommendation.mockResolvedValue(recommendation);
  });
  afterEach(cleanup);

  it("shows the complete rule set before analytics generation and the unavailable recommendation state", async () => {
    const { container } = render(<><AchievementPanel /><RecommendationPanel /></>);
    await screen.findByText("等待首轮分析");
    await screen.findByText("学习建议暂不可用");

    const content = container.textContent ?? "";
    expect(content).toContain("初次登场");
    expect(content).toContain("勤学善问");
    expect(content).toContain("规则集v1");
    expect(content).toContain("系统尚未发布分析结果");
    expect(content).toContain("分析 head r0");
    expect(content).toContain("模型 head r5");
    expect(content).toContain(model.modelId);
    expect(content).toContain(model.artifactSha256);
    expect(mocks.loadSelfAchievements).toHaveBeenCalledWith({ marker: "desktop-session" });
    expect(mocks.loadSelfRecommendation).toHaveBeenCalledWith({ marker: "desktop-session" });
  });

  it("shows problem IDs and preserves every canonical source problem-set URL", async () => {
    mocks.loadSelfRecommendation.mockResolvedValueOnce(readyRecommendation);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const { container } = render(<RecommendationPanel />);
      await screen.findByText("题目 ID 501");

      expect(container.textContent).toContain("数组练习");
      expect(container.textContent).toContain("预计通过 67%");
      expect(container.textContent).toContain("ascendany.recommendation.inference-result.v1");
      expect(container.textContent).toContain("9".repeat(64));
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
