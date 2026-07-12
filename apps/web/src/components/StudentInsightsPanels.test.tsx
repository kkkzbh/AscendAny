import { cleanup, render, screen, waitFor } from "@testing-library/react";
import type { SelfAchievements, SelfRecommendation } from "@ascendany/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AchievementPanel } from "./AchievementPanel";
import { RecommendationPanel } from "./RecommendationPanel";

const mocks = vi.hoisted(() => ({
  loadSelfAchievements: vi.fn(),
  loadSelfRecommendation: vi.fn(),
  useSession: vi.fn(),
}));

vi.mock("../api/operations", () => ({
  loadSelfAchievements: mocks.loadSelfAchievements,
  loadSelfRecommendation: mocks.loadSelfRecommendation,
}));
vi.mock("../session/context", () => ({ useSession: mocks.useSession }));

const achievements = {
  state: "ready",
  analyticsHeadRevision: 12,
  ruleSetVersion: 4,
  ruleHeadRevision: 9,
  summary: { total: 2, locked: 1, bronze: 0, silver: 0, gold: 1 },
  items: [
    { code: "first_exam", title: "初次登场", description: "完成考试。", progressKey: "exam_count", tier: 0, progress: 0, bronzeTarget: 1, silverTarget: 5, goldTarget: 10, sortOrder: 1 },
    { code: "rating_peak", title: "登峰造极", description: "提升 Rating。", progressKey: "max_rating", tier: 3, progress: 1810, bronzeTarget: 1200, silverTarget: 1500, goldTarget: 1800, sortOrder: 2 },
  ],
} satisfies SelfAchievements;

const recommendation = {
  state: "fresh",
  currentAnalyticsGenerationId: "28",
  currentAnalyticsHeadRevision: 12,
  recommendationHeadRevision: 5,
  model: {
    modelId: "123e4567-e89b-42d3-a456-426614174000",
    trainingRunId: "223e4567-e89b-42d3-a456-426614174000",
    analyticsGenerationId: "28",
    analyticsHeadRevision: 12,
    inputManifestSha256: "a".repeat(64),
    trainingConfigurationVersionId: "31",
    trainingConfigurationKey: "recommendation.training.default",
    trainingConfigurationVersion: 3,
    trainingConfigurationSchema: "ascendany.training.recommendation.v2",
    trainingConfigurationSha256: "b".repeat(64),
    knowledgeCatalogVersionId: "41",
    knowledgeCatalogKey: "recommendation.knowledge.default",
    knowledgeCatalogVersion: 2,
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
    sourceRating: 1810,
    evidence: { trainInteractionCount: 8, validationInteractionCount: 2, distinctProblemCount: 6, passedProblemCount: 3 },
    knowledgeMastery: [
      { knowledgePointId: "arrays", label: "数组", description: "数组基础与索引。", prerequisiteIds: [], mastery: 0.42, trainInteractionCount: 4 },
      { knowledgePointId: "graphs", label: "图论", description: "图遍历。", prerequisiteIds: ["arrays"], mastery: 0.31, trainInteractionCount: 4 },
    ],
    learningPath: [
      { order: 1, knowledgePointId: "arrays", label: "数组", description: "数组基础与索引。", prerequisiteIds: [], mastery: 0.42, targetMastery: 0.8, reasonCode: "prerequisite", recommendedProblems: [{ problemKey: "pintia:501:fact", sourceProblemKey: "pintia:501", platform: "pintia", problemId: "501", title: "数组练习", sourceProblemSets: [{ problemSetId: "1001", sourceUrl: "https://pintia.cn/problem-sets/1001" }, { problemSetId: "1001", sourceUrl: "https://pintia.cn/problem-sets/1001/problems/type/7" }], predictedSuccessProbability: 0.67, recommendationScore: 0.2, rankingEvidence: { knowledgeGap: 0.38, successDistance: 0.03, stepKnowledgeWeight: 1 } }] },
      { order: 2, knowledgePointId: "graphs", label: "图论", description: "图遍历。", prerequisiteIds: ["arrays"], mastery: 0.31, targetMastery: 0.8, reasonCode: "knowledge_gap", recommendedProblems: [{ problemKey: "pintia:502:fact", sourceProblemKey: "pintia:502", platform: "pintia", problemId: "502", title: "图遍历练习", sourceProblemSets: [{ problemSetId: "1002", sourceUrl: "https://pintia.cn/problem-sets/1002" }], predictedSuccessProbability: 0.61, recommendationScore: 0.3, rankingEvidence: { knowledgeGap: 0.49, successDistance: 0.09, stepKnowledgeWeight: 1 } }] },
    ],
  },
} satisfies SelfRecommendation;

describe("web student insight panels", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useSession.mockReturnValue({ session: { marker: "web-session" } });
    mocks.loadSelfAchievements.mockResolvedValue(achievements);
    mocks.loadSelfRecommendation.mockResolvedValue(recommendation);
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders every active rule, tier, targets, summary provenance, and fresh recommendation", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { container } = render(<><AchievementPanel /><RecommendationPanel /></>);

    await screen.findByText("初次登场");
    await screen.findByText("个性化学习建议");
    const content = container.textContent ?? "";
    expect(content).toContain("登峰造极");
    expect(content).toContain("未解锁");
    expect(content).toContain("黄金");
    expect(content).toContain("当前进度1810");
    expect(content).toContain("分析 headr12");
    expect(content).toContain("规则集v4");
    expect(content).toContain("规则 headr9");
    expect(content).toContain("数组 · 42%");
    expect(content).toContain("图论 · 31%");
    expect(content).toContain("题目 ID 501");
    expect(content).toContain("题目 ID 502");
    expect(content).toContain("预计通过 61%");
    const duplicatedSetLinks = screen.getAllByRole("link", { name: "打开题目集 1001" });
    expect(duplicatedSetLinks).toHaveLength(2);
    expect(duplicatedSetLinks.map((link) => link.getAttribute("href"))).toEqual([
      "https://pintia.cn/problem-sets/1001",
      "https://pintia.cn/problem-sets/1001/problems/type/7",
    ]);
    for (const link of duplicatedSetLinks) {
      expect(link.getAttribute("target")).toBe("_blank");
      expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    }
    const graphSetLink = screen.getByRole("link", { name: "打开题目集 1002" });
    expect(graphSetLink.getAttribute("href")).toBe("https://pintia.cn/problem-sets/1002");
    expect(graphSetLink.getAttribute("href")).not.toContain("/problems/502");
    expect(consoleError).not.toHaveBeenCalled();
    expect(content).toContain("recommendation.training.default v3");
    expect(content).toContain("recommendation.knowledge.default v2");
    expect(mocks.loadSelfAchievements).toHaveBeenCalledWith({ marker: "web-session" });
    expect(mocks.loadSelfRecommendation).toHaveBeenCalledWith({ marker: "web-session" });
  });

  it.each([
    ["not_generated", "等待首轮分析"],
    ["no_observations", "暂无考试观测"],
  ] as const)("keeps all rules visible for %s", async (state, label) => {
    mocks.loadSelfAchievements.mockResolvedValue({ ...achievements, state });
    render(<AchievementPanel />);

    await screen.findByText(label);
    expect(screen.getByText("初次登场")).toBeTruthy();
    expect(screen.getByText("登峰造极")).toBeTruthy();
    await waitFor(() => expect(mocks.loadSelfAchievements).toHaveBeenCalledTimes(1));
  });
});
