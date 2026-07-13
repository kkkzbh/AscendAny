import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ApiError, ConfigurationItem, RecommendationReviewContext } from "@ascendany/sdk";
import {
  loadRecommendationKnowledgeCatalog,
  loadRecommendationReviewContext,
  RecommendationCatalogApiError,
} from "./recommendation";

const sdk = vi.hoisted(() => ({
  getConfiguration: vi.fn(),
  getRecommendationReviewContext: vi.fn(),
}));
const transport = vi.hoisted(() => ({
  client: { marker: "generated-client" },
  ensureAuthenticated: vi.fn(),
}));

vi.mock("@ascendany/sdk", async (importOriginal) => ({
  ...await importOriginal<typeof import("@ascendany/sdk")>(),
  ...sdk,
}));

vi.mock("./v2Client", () => ({
  v2Client: transport.client,
  browserSession: { ensureAuthenticated: transport.ensureAuthenticated },
}));

const review: RecommendationReviewContext = {
  analyticsGenerationId: "9",
  analyticsHeadRevision: 4,
  inputManifestSha256: "b".repeat(64),
  problems: [],
};

const configurationItem: ConfigurationItem = {
  id: "11111111-1111-4111-8111-111111111111",
  key: "recommendation.catalog.active",
  kind: "knowledge_catalog",
  headRevision: 1,
  activeVersion: null,
  createdAt: "2026-07-12T01:00:00Z",
  updatedAt: "2026-07-12T01:00:00Z",
};

function success<T>(data: T, status = 200) {
  return { data, error: undefined, response: new Response(null, { status }) };
}

describe("recommendation knowledge catalog read API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    transport.ensureAuthenticated.mockResolvedValue(undefined);
    sdk.getRecommendationReviewContext.mockResolvedValue(success(review));
    sdk.getConfiguration.mockResolvedValue(success(configurationItem));
  });

  it("uses only generated review and configuration read operations", async () => {
    await expect(loadRecommendationReviewContext()).resolves.toEqual(review);
    await expect(loadRecommendationKnowledgeCatalog(configurationItem.key)).resolves.toEqual(configurationItem);

    expect(sdk.getRecommendationReviewContext).toHaveBeenCalledWith({ client: transport.client });
    expect(sdk.getConfiguration).toHaveBeenCalledWith({
      client: transport.client,
      path: { key: configurationItem.key },
    });
    expect(transport.ensureAuthenticated).toHaveBeenCalledTimes(2);
  });

  it("preserves a strict API error envelope", async () => {
    const error: ApiError = {
      code: "configuration_not_found",
      message: "Configuration was not found.",
      requestId: "44444444-4444-4444-8444-444444444444",
      details: { key: configurationItem.key },
    };
    sdk.getConfiguration.mockResolvedValue({
      data: undefined,
      error,
      response: new Response(null, { status: 404 }),
    });

    await expect(loadRecommendationKnowledgeCatalog(configurationItem.key)).rejects.toEqual(
      expect.objectContaining<Partial<RecommendationCatalogApiError>>({ status: 404, apiError: error }),
    );
  });

  it("rejects a malformed API error envelope", async () => {
    sdk.getConfiguration.mockResolvedValue({
      data: undefined,
      error: {
        code: "configuration_not_found",
        message: "Configuration was not found.",
        requestId: "not-a-canonical-uuid",
        details: [],
      },
      response: new Response(null, { status: 404 }),
    });

    await expect(loadRecommendationKnowledgeCatalog(configurationItem.key)).rejects.toThrow(
      "Recommendation catalog request failed without a valid API error.",
    );
  });
});
