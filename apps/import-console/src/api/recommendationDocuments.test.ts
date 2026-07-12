import { describe, expect, it } from "vitest";
import type {
  RecommendationKnowledgeCatalogV1,
  RecommendationTrainingConfigurationV2,
} from "@ascendany/sdk";
import {
  parseRecommendationKnowledgeCatalogV1,
  parseRecommendationTrainingConfigurationV2,
} from "./recommendationDocuments";

function catalog(label: string): RecommendationKnowledgeCatalogV1 {
  return {
    taxonomyId: "recommendation.catalog.default",
    knowledgePoints: [{
      id: "fundamentals",
      label,
      description: "Reviewed fundamentals",
      prerequisiteIds: [],
    }],
    problemAssignments: [{
      platform: "pintia",
      problemId: "7",
      problemFactSha256: "a".repeat(64),
      knowledge: [{ knowledgePointId: "fundamentals", weight: 1 }],
    }],
  };
}

function training(): RecommendationTrainingConfigurationV2 {
  return {
    algorithm: "knowledge_mirt_v1",
    knowledgeCatalogVersionId: "16",
    accelerator: "cuda",
    seed: 2026,
    epochs: 100,
    patience: 10,
    batchSize: 32,
    learningRate: 0.01,
    weightDecay: 0.001,
    minTrainInteractions: 32,
    minActorInteractions: 2,
    minProblemInteractions: 1,
    validation: { minActors: 2, minInteractions: 2, minRelativeLogLossImprovement: 0 },
    pathPolicy: {
      targetMastery: 0.8,
      maxKnowledgeTargets: 3,
      minSteps: 2,
      maxSteps: 4,
      problemsPerStep: 2,
      targetSuccessProbability: 0.7,
    },
    rankingWeights: { knowledgeGap: 1, successDistance: 1 },
  };
}

describe("recommendation configuration document boundaries", () => {
  it("measures canonical text limits in UTF-8 bytes", () => {
    const atLimit = catalog("中".repeat(85));
    expect(parseRecommendationKnowledgeCatalogV1(JSON.stringify(atLimit))).toEqual(atLimit);

    const overLimit = catalog("中".repeat(86));
    expect(() => parseRecommendationKnowledgeCatalogV1(JSON.stringify(overLimit)))
      .toThrow(/1\.\.256 UTF-8 bytes/);
  });

  it("accepts the generated first-run training template contract bounds", () => {
    const value = training();
    expect(parseRecommendationTrainingConfigurationV2(JSON.stringify(value))).toEqual(value);
  });

  it("rejects batchSize above minTrainInteractions before publish", () => {
    const value = { ...training(), batchSize: 33 };
    expect(() => parseRecommendationTrainingConfigurationV2(JSON.stringify(value)))
      .toThrow("batchSize 禁止大于 minTrainInteractions。");
  });
});
