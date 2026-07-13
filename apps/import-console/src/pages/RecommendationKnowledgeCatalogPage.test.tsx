import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ConfigurationItem,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
} from "@ascendany/sdk";
import { RecommendationKnowledgeCatalogPage } from "./RecommendationKnowledgeCatalogPage";

const api = vi.hoisted(() => ({
  loadRecommendationKnowledgeCatalog: vi.fn(),
  loadRecommendationReviewContext: vi.fn(),
}));

vi.mock("../api/recommendation", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/recommendation")>(),
  ...api,
}));

const problemKey = `pintia:7:${"c".repeat(64)}`;

const review: RecommendationReviewContext = {
  analyticsGenerationId: "9",
  analyticsHeadRevision: 4,
  inputManifestSha256: "b".repeat(64),
  problems: [{
    problemKey,
    sourceProblemKey: "pintia:7",
    platform: "pintia",
    problemId: "7",
    problemFactSha256: "c".repeat(64),
    title: "A + B",
    sourceProblemSets: [
      { problemSetId: "2039", sourceUrl: "https://pintia.cn/problem-sets/2039" },
      { problemSetId: "2040", sourceUrl: "https://pintia.cn/problem-sets/2040/problems/type/7" },
    ],
  }],
};

function catalogDocument(problemFactSha256 = "c".repeat(64)): RecommendationKnowledgeCatalogV1 {
  return {
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
      problemFactSha256,
      knowledge: [{ knowledgePointId: "fundamentals", weight: "1" }],
    }],
  };
}

function catalogItem(
  document: RecommendationKnowledgeCatalogV1 = catalogDocument(),
  schemaId = "ascendany.knowledge_catalog.recommendation.v1",
): ConfigurationItem {
  return {
    id: "11111111-1111-4111-8111-111111111111",
    key: "recommendation.catalog.active",
    kind: "knowledge_catalog",
    headRevision: 1,
    activeVersion: {
      id: "16",
      number: 1,
      schemaId,
      document,
      documentSha256: "a".repeat(64),
      credentialRef: null,
      createdByAccountId: "22222222-2222-4222-8222-222222222222",
      createdBySessionId: "33333333-3333-4333-8333-333333333333",
      createdAt: "2026-07-12T01:00:00Z",
    },
    createdAt: "2026-07-12T01:00:00Z",
    updatedAt: "2026-07-12T01:00:00Z",
  };
}

describe("RecommendationKnowledgeCatalogPage", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.loadRecommendationReviewContext.mockResolvedValue(review);
    api.loadRecommendationKnowledgeCatalog.mockResolvedValue(catalogItem());
  });

  it("renders release-owned catalog provenance and exact analytics coverage read-only", async () => {
    render(<RecommendationKnowledgeCatalogPage />);

    const catalog = await screen.findByRole("region", { name: "Active knowledge catalog" });
    expect(within(catalog).getByText("head r1")).toBeInTheDocument();
    expect(within(catalog).getByText("16")).toBeInTheDocument();
    expect(within(catalog).getByText("a".repeat(64))).toBeInTheDocument();
    expect(within(catalog).getByText("recommendation.catalog.default")).toBeInTheDocument();
    expect(await screen.findByText(problemKey)).toBeInTheDocument();
    expect(screen.getByText("covered")).toBeInTheDocument();
    expect(screen.getByText(/精确覆盖当前 analytics/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "2039" })).toHaveAttribute("href", "https://pintia.cn/problem-sets/2039");
    expect(screen.getByRole("link", { name: "2040" })).toHaveAttribute("href", "https://pintia.cn/problem-sets/2040/problems/type/7");
    expect(screen.queryByRole("button", { name: /发布 catalog/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /Knowledge catalog document/ })).not.toBeInTheDocument();
    expect(api.loadRecommendationKnowledgeCatalog).toHaveBeenCalledWith("recommendation.catalog.active");
  });

  it("reports an exact fact mismatch without exposing a runtime mutation path", async () => {
    api.loadRecommendationKnowledgeCatalog.mockResolvedValue(catalogItem(catalogDocument("d".repeat(64))));
    render(<RecommendationKnowledgeCatalogPage />);

    expect(await screen.findByText("fact changed")).toBeInTheDocument();
    expect(screen.getByText(/与当前 analytics problem identity\/fact 集合不一致/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /发布 catalog/ })).not.toBeInTheDocument();
  });

  it("fails closed when the active configuration has the wrong schema", async () => {
    api.loadRecommendationKnowledgeCatalog.mockResolvedValue(catalogItem(
      catalogDocument(),
      "ascendany.knowledge_catalog.recommendation.v2",
    ));
    render(<RecommendationKnowledgeCatalogPage />);

    expect(await screen.findByRole("alert")).toHaveTextContent(/active version schema 必须是/);
    expect(screen.getByText(/尚未读取到经过严格校验/)).toBeInTheDocument();
  });

  it("reloads review and active catalog as one operator inspection", async () => {
    render(<RecommendationKnowledgeCatalogPage />);
    await screen.findByRole("region", { name: "Active knowledge catalog" });

    fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
    await waitFor(() => expect(api.loadRecommendationReviewContext).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(api.loadRecommendationKnowledgeCatalog).toHaveBeenCalledTimes(2));
  });
});
