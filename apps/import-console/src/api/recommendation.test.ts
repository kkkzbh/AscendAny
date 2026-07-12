import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ApiError,
  ConfigurationItem,
  CreateRecommendationTrainingConfigurationVersionRequest,
  QueueRecommendationTrainingRunRequest,
  QueueRecommendationTrainingRunResult,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
  RecommendationTrainingEventPage,
  RecommendationTrainingRunDetail,
} from "@ascendany/sdk";
import {
  loadRecommendationConfiguration,
  loadRecommendationReviewContext,
  loadRecommendationTrainingEvents,
  loadRecommendationTrainingRun,
  publishRecommendationConfiguration,
  queueRecommendationTraining,
  RecommendationWorkflowError,
} from "./recommendation";

const sdk = vi.hoisted(() => ({
  createConfigurationVersion: vi.fn(),
  getConfiguration: vi.fn(),
  getRecommendationReviewContext: vi.fn(),
  getRecommendationTrainingRun: vi.fn(),
  listRecommendationTrainingRunEvents: vi.fn(),
  queueRecommendationTrainingRun: vi.fn(),
}));

const transport = vi.hoisted(() => ({
  ensureAuthenticated: vi.fn(),
  client: { kind: "browser-session-client" },
}));

vi.mock("@ascendany/sdk", () => sdk);
vi.mock("./v2Client", () => ({
  browserSession: { ensureAuthenticated: transport.ensureAuthenticated },
  v2Client: transport.client,
}));

const runId = "11111111-1111-4111-8111-111111111115";
const accountId = "22222222-2222-4222-8222-222222222222";
const sessionId = "33333333-3333-4333-8333-333333333333";

const configurationItem: ConfigurationItem = {
  id: "11111111-1111-4111-8111-111111111111",
  key: "recommendation.training.default",
  kind: "training",
  headRevision: 1,
  activeVersion: {
    id: "17",
    number: 1,
    schemaId: "ascendany.training.recommendation.v2",
    document: { knowledgeCatalogVersionId: "16" },
    documentSha256: "a".repeat(64),
    credentialRef: null,
    createdByAccountId: accountId,
    createdBySessionId: sessionId,
    createdAt: "2026-07-12T01:00:00Z",
  },
  createdAt: "2026-07-12T01:00:00Z",
  updatedAt: "2026-07-12T01:00:00Z",
};

const review: RecommendationReviewContext = {
  analyticsGenerationId: "9",
  analyticsHeadRevision: 4,
  inputManifestSha256: "b".repeat(64),
  problems: [{
    problemKey: `pintia:7:${"c".repeat(64)}`,
    sourceProblemKey: "pintia:7",
    platform: "pintia",
    problemId: "7",
    problemFactSha256: "c".repeat(64),
    title: "A + B",
    sourceProblemSets: [{ problemSetId: "2039", sourceUrl: "https://pintia.cn/problem-sets/2039" }],
  }],
};

const queueResult: QueueRecommendationTrainingRunResult = {
  created: true,
  trainingRun: {
    id: runId,
    sourceAnalyticsGenerationId: "9",
    sourceAnalyticsHeadRevision: 4,
    trainingConfigurationVersionId: "17",
    knowledgeCatalogVersionId: "16",
    trainingConfigurationKey: configurationItem.key,
    bundleProtocol: "ascendany.recommendation.training-bundle.v2",
    inputManifestSha256: "b".repeat(64),
    inputArtifactSha256: "d".repeat(64),
    inputArtifactSizeBytes: 2048,
    status: "queued",
    attemptCount: 0,
    createdAt: "2026-07-12T01:01:00Z",
    startedAt: null,
    finishedAt: null,
  },
};

const runDetail: RecommendationTrainingRunDetail = {
  ...queueResult.trainingRun,
  status: "running",
  attemptCount: 1,
  startedAt: "2026-07-12T01:01:01Z",
  failure: null,
};

const eventPage: RecommendationTrainingEventPage = {
  runId,
  items: [{
    sequence: 1,
    type: "queued",
    payload: {
      artifactSha256: "d".repeat(64),
      configurationVersionId: "17",
      knowledgeCatalogVersionId: "16",
      sourceAnalyticsGenerationId: "9",
      sourceAnalyticsHeadRevision: 4,
    },
    createdAt: "2026-07-12T01:01:00Z",
  }],
  nextAfterSequence: null,
};

const catalogDocument: RecommendationKnowledgeCatalogV1 = {
  taxonomyId: "recommendation.catalog.default",
  knowledgePoints: [{
    id: "fundamentals",
    label: "Fundamentals",
    description: "Reviewed Pintia problem fundamentals",
    prerequisiteIds: [],
  }],
  problemAssignments: [{
    platform: "pintia",
    problemId: "7",
    problemFactSha256: "c".repeat(64),
    knowledge: [{ knowledgePointId: "fundamentals", weight: 1 }],
  }],
};

const trainingDocument: CreateRecommendationTrainingConfigurationVersionRequest["document"] = {
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

function success<T>(data: T, status = 200) {
  return { data, error: undefined, response: new Response(null, { status }) };
}

describe("recommendation workflow API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    transport.ensureAuthenticated.mockResolvedValue(undefined);
    sdk.getRecommendationReviewContext.mockResolvedValue(success(review));
    sdk.getConfiguration.mockResolvedValue(success(configurationItem));
    sdk.createConfigurationVersion.mockResolvedValue(success({ item: configurationItem, idempotent: false }, 201));
    sdk.queueRecommendationTrainingRun.mockResolvedValue(success(queueResult, 202));
    sdk.getRecommendationTrainingRun.mockResolvedValue(success(runDetail));
    sdk.listRecommendationTrainingRunEvents.mockResolvedValue(success(eventPage));
  });

  it("uses only generated operations and sends the exact reviewed provenance", async () => {
    const configurationRequest: CreateRecommendationTrainingConfigurationVersionRequest = {
      key: configurationItem.key,
      kind: "training",
      expectedHeadRevision: 0,
      schemaId: "ascendany.training.recommendation.v2",
      document: trainingDocument,
      credentialRef: null,
    };
    const queueRequest: QueueRecommendationTrainingRunRequest = {
      trainingConfigurationKey: configurationItem.key,
      expectedAnalyticsGenerationId: review.analyticsGenerationId,
      expectedAnalyticsHeadRevision: review.analyticsHeadRevision,
    };

    await expect(loadRecommendationReviewContext()).resolves.toEqual(review);
    await expect(loadRecommendationConfiguration(configurationItem.key)).resolves.toEqual(configurationItem);
    await expect(publishRecommendationConfiguration(configurationRequest)).resolves.toMatchObject({ item: configurationItem });
    await expect(queueRecommendationTraining(queueRequest)).resolves.toEqual(queueResult);
    await expect(loadRecommendationTrainingRun(runId)).resolves.toEqual(runDetail);
    await expect(loadRecommendationTrainingEvents(runId, 7, 50)).resolves.toEqual(eventPage);

    expect(sdk.getRecommendationReviewContext).toHaveBeenCalledWith({ client: transport.client });
    expect(sdk.getConfiguration).toHaveBeenCalledWith({ client: transport.client, path: { key: configurationItem.key } });
    expect(sdk.createConfigurationVersion).toHaveBeenCalledWith({ client: transport.client, body: configurationRequest });
    expect(sdk.queueRecommendationTrainingRun).toHaveBeenCalledWith({ client: transport.client, body: queueRequest });
    expect(sdk.getRecommendationTrainingRun).toHaveBeenCalledWith({ client: transport.client, path: { runId } });
    expect(sdk.listRecommendationTrainingRunEvents).toHaveBeenCalledWith({
      client: transport.client,
      path: { runId },
      query: { afterSequence: 7, limit: 50 },
    });
    expect(transport.ensureAuthenticated).toHaveBeenCalledTimes(6);
  });

  it("preserves HTTP 409 analytics drift details", async () => {
    const error: ApiError = {
      code: "recommendation_analytics_head_conflict",
      message: "Analytics head changed.",
      requestId: "44444444-4444-4444-8444-444444444444",
      details: { expectedAnalyticsGenerationId: "9", currentAnalyticsGenerationId: "10" },
    };
    sdk.queueRecommendationTrainingRun.mockResolvedValue({
      data: undefined,
      error,
      response: new Response(null, { status: 409 }),
    });

    await expect(queueRecommendationTraining({
      trainingConfigurationKey: configurationItem.key,
      expectedAnalyticsGenerationId: "9",
      expectedAnalyticsHeadRevision: 4,
    })).rejects.toEqual(expect.objectContaining<Partial<RecommendationWorkflowError>>({
      status: 409,
      apiError: error,
    }));
  });

  it("preserves HTTP 422 semantic and coverage details", async () => {
    const error: ApiError = {
      code: "recommendation_preflight_failed",
      message: "Recommendation preflight failed.",
      requestId: "55555555-5555-4555-8555-555555555555",
      details: { issueCode: "knowledge_catalog_assignment_missing", problemKeys: [review.problems[0]!.problemKey] },
    };
    sdk.createConfigurationVersion.mockResolvedValue({
      data: undefined,
      error,
      response: new Response(null, { status: 422 }),
    });

    await expect(publishRecommendationConfiguration({
      key: "recommendation.catalog.default",
      kind: "knowledge_catalog",
      expectedHeadRevision: 0,
      schemaId: "ascendany.knowledge_catalog.recommendation.v1",
      document: catalogDocument,
      credentialRef: null,
    })).rejects.toEqual(expect.objectContaining<Partial<RecommendationWorkflowError>>({
      status: 422,
      apiError: error,
    }));
  });

  it("rejects a malformed HTTP error envelope without classifying it as a workflow validation", async () => {
    sdk.queueRecommendationTrainingRun.mockResolvedValue({
      data: undefined,
      error: {
        code: "recommendation_preflight_failed",
        message: "Recommendation preflight failed.",
        requestId: "not-a-canonical-uuid",
        details: [],
      },
      response: new Response(null, { status: 422 }),
    });

    let captured: unknown;
    try {
      await queueRecommendationTraining({
        trainingConfigurationKey: configurationItem.key,
        expectedAnalyticsGenerationId: "9",
        expectedAnalyticsHeadRevision: 4,
      });
    } catch (error) {
      captured = error;
    }

    expect(captured).toBeInstanceOf(Error);
    expect(captured).not.toBeInstanceOf(RecommendationWorkflowError);
    expect((captured as Error).message).toBe("Recommendation request failed without a valid API error.");
  });
});
