import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ApiError,
  ConfigurationItem,
  CreateRecommendationKnowledgeCatalogVersionRequest,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
} from "@ascendany/sdk";
import {
  loadRecommendationKnowledgeCatalog,
  loadRecommendationReviewContext,
  publishRecommendationKnowledgeCatalog,
  RecommendationCatalogApiError,
} from "./recommendation";

const sdk = vi.hoisted(() => ({
  createConfigurationVersion: vi.fn(),
  getConfiguration: vi.fn(),
  getRecommendationReviewContext: vi.fn(),
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

const accountId = "22222222-2222-4222-8222-222222222222";
const sessionId = "33333333-3333-4333-8333-333333333333";

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
    knowledge: [{ knowledgePointId: "fundamentals", weight: "1" }],
  }],
};

const configurationItem: ConfigurationItem = {
  id: "11111111-1111-4111-8111-111111111111",
  key: "recommendation.catalog.active",
  kind: "knowledge_catalog",
  headRevision: 1,
  activeVersion: {
    id: "16",
    number: 1,
    schemaId: "ascendany.knowledge_catalog.recommendation.v1",
    document: catalogDocument,
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

const catalogRequest: CreateRecommendationKnowledgeCatalogVersionRequest = {
  key: configurationItem.key,
  kind: "knowledge_catalog",
  expectedHeadRevision: 0,
  expectedAnalyticsGenerationId: review.analyticsGenerationId,
  expectedAnalyticsHeadRevision: review.analyticsHeadRevision,
  expectedInputManifestSha256: review.inputManifestSha256,
  schemaId: "ascendany.knowledge_catalog.recommendation.v1",
  document: catalogDocument,
  credentialRef: null,
};

function success<T>(data: T, status = 200) {
  return { data, error: undefined, response: new Response(null, { status }) };
}

describe("recommendation knowledge catalog API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    transport.ensureAuthenticated.mockResolvedValue(undefined);
    sdk.getRecommendationReviewContext.mockResolvedValue(success(review));
    sdk.getConfiguration.mockResolvedValue(success(configurationItem));
    sdk.createConfigurationVersion.mockResolvedValue(success({ item: configurationItem, idempotent: false }, 201));
  });

  it("uses only generated review, configuration, and catalog publish operations", async () => {
    await expect(loadRecommendationReviewContext()).resolves.toEqual(review);
    await expect(loadRecommendationKnowledgeCatalog(configurationItem.key)).resolves.toEqual(configurationItem);
    await expect(publishRecommendationKnowledgeCatalog(catalogRequest)).resolves.toMatchObject({ item: configurationItem });

    expect(sdk.getRecommendationReviewContext).toHaveBeenCalledWith({ client: transport.client });
    expect(sdk.getConfiguration).toHaveBeenCalledWith({ client: transport.client, path: { key: configurationItem.key } });
    expect(sdk.createConfigurationVersion).toHaveBeenCalledWith({ client: transport.client, body: catalogRequest });
    expect(transport.ensureAuthenticated).toHaveBeenCalledTimes(3);
  });

  it("preserves HTTP 409 CAS conflict details", async () => {
    const error: ApiError = {
      code: "configuration_head_conflict",
      message: "Configuration head changed.",
      requestId: "44444444-4444-4444-8444-444444444444",
      details: { expectedHeadRevision: 0, currentHeadRevision: 1 },
    };
    sdk.createConfigurationVersion.mockResolvedValue({
      data: undefined,
      error,
      response: new Response(null, { status: 409 }),
    });

    await expect(publishRecommendationKnowledgeCatalog(catalogRequest)).rejects.toEqual(
      expect.objectContaining<Partial<RecommendationCatalogApiError>>({ status: 409, apiError: error }),
    );
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

    await expect(publishRecommendationKnowledgeCatalog(catalogRequest)).rejects.toEqual(
      expect.objectContaining<Partial<RecommendationCatalogApiError>>({ status: 422, apiError: error }),
    );
  });

  it("rejects a malformed HTTP error envelope", async () => {
    sdk.createConfigurationVersion.mockResolvedValue({
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
      await publishRecommendationKnowledgeCatalog(catalogRequest);
    } catch (error) {
      captured = error;
    }

    expect(captured).toBeInstanceOf(Error);
    expect(captured).not.toBeInstanceOf(RecommendationCatalogApiError);
    expect((captured as Error).message).toBe("Recommendation catalog request failed without a valid API error.");
  });
});
