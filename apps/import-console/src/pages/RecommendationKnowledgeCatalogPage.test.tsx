import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ApiError,
  ConfigurationItem,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
} from "@ascendany/sdk";
import { RecommendationCatalogApiError } from "../api/recommendation";
import { RecommendationKnowledgeCatalogPage } from "./RecommendationKnowledgeCatalogPage";

const api = vi.hoisted(() => ({
  loadRecommendationKnowledgeCatalog: vi.fn(),
  loadRecommendationReviewContext: vi.fn(),
  publishRecommendationKnowledgeCatalog: vi.fn(),
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

function catalogItem(
  key = "recommendation.catalog.active",
  headRevision = 1,
): ConfigurationItem {
  return {
    id: "11111111-1111-4111-8111-111111111111",
    key,
    kind: "knowledge_catalog",
    headRevision,
    activeVersion: {
      id: "16",
      number: headRevision,
      schemaId: "ascendany.knowledge_catalog.recommendation.v1",
      document: catalogDocument,
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
    api.publishRecommendationKnowledgeCatalog.mockResolvedValue({ item: catalogItem(), idempotent: false });
  });

  it("requires reviewed knowledge semantics before publishing the immutable catalog", async () => {
    render(<RecommendationKnowledgeCatalogPage />);

    expect(await screen.findByText(problemKey)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "2039" })).toHaveAttribute("href", "https://pintia.cn/problem-sets/2039");
    expect(screen.getByRole("link", { name: "2040" })).toHaveAttribute("href", "https://pintia.cn/problem-sets/2040/problems/type/7");
    const editor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" });
    await waitFor(() => expect(editor.value).not.toBe(""));
    expect(JSON.parse(editor.value)).toEqual({
      taxonomyId: "recommendation.catalog.active",
      knowledgePoints: [],
      problemAssignments: [{
        platform: "pintia",
        problemId: "7",
        problemFactSha256: "c".repeat(64),
        knowledge: [],
      }],
    });
    const publish = screen.getByRole("button", { name: "发布 catalog v1" });
    expect(publish).toBeDisabled();
    expect(screen.getByText(/不包含推测的知识语义/)).toBeInTheDocument();

    fireEvent.change(editor, { target: { value: JSON.stringify(catalogDocument, null, 2) } });
    await waitFor(() => expect(publish).toBeEnabled());
    fireEvent.click(publish);

    await waitFor(() => expect(api.publishRecommendationKnowledgeCatalog).toHaveBeenCalledWith({
      key: "recommendation.catalog.active",
      kind: "knowledge_catalog",
      expectedHeadRevision: 0,
	  expectedAnalyticsGenerationId: "9",
	  expectedAnalyticsHeadRevision: 4,
	  expectedInputManifestSha256: "b".repeat(64),
      schemaId: "ascendany.knowledge_catalog.recommendation.v1",
      credentialRef: null,
      document: catalogDocument,
    }));
    expect(await screen.findByText(/Active catalog version ID: 16/)).toBeInTheDocument();
    expect(screen.getByText(/a{64}/)).toBeInTheDocument();
  });

  it("preserves an edited catalog draft when review provenance is reloaded", async () => {
    render(<RecommendationKnowledgeCatalogPage />);
    const editor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" });
    await waitFor(() => expect(editor.value).not.toBe(""));
    const editedDocument = JSON.stringify(catalogDocument, null, 2);
    fireEvent.change(editor, { target: { value: editedDocument } });
    api.loadRecommendationReviewContext.mockResolvedValueOnce({
      ...review,
      problems: review.problems.map((problem) => ({ ...problem })),
    });

    fireEvent.click(screen.getByRole("button", { name: "重新加载 review" }));
    await waitFor(() => expect(api.loadRecommendationReviewContext).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByRole("button", { name: "重新加载 review" })).toBeEnabled());

    expect(editor.value).toBe(editedDocument);
  });

  it("loads and advances the fixed catalog singleton with CAS", async () => {
    api.loadRecommendationKnowledgeCatalog.mockResolvedValue(catalogItem("recommendation.catalog.active", 3));
    render(<RecommendationKnowledgeCatalogPage />);
    const panel = await screen.findByRole("region", { name: "Knowledge catalog editor" });
    await waitFor(() => expect(within(panel).getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" }).value).not.toBe(""));

    expect(within(panel).getByText("recommendation.catalog.active")).toBeInTheDocument();
    fireEvent.click(within(panel).getByRole("button", { name: "读取 active version" }));
    expect(await within(panel).findByText("CAS r3")).toBeInTheDocument();
    fireEvent.click(within(panel).getByRole("button", { name: "发布 catalog v1" }));

    await waitFor(() => expect(api.publishRecommendationKnowledgeCatalog).toHaveBeenCalledWith(expect.objectContaining({
      key: "recommendation.catalog.active",
      kind: "knowledge_catalog",
      expectedHeadRevision: 3,
    })));
  });

  it("shows semantic coverage details and the affected review problem", async () => {
    const validation: ApiError = {
      code: "recommendation_preflight_failed",
      message: "Recommendation catalog coverage failed.",
      requestId: "55555555-5555-4555-8555-555555555555",
      details: { issueCode: "knowledge_catalog_assignment_missing", problemKeys: [problemKey] },
    };
    api.publishRecommendationKnowledgeCatalog.mockRejectedValue(new RecommendationCatalogApiError(422, validation));
    render(<RecommendationKnowledgeCatalogPage />);
    const editor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" });
    await waitFor(() => expect(editor.value).not.toBe(""));
    fireEvent.change(editor, { target: { value: JSON.stringify(catalogDocument, null, 2) } });
    await waitFor(() => expect(screen.getByRole("button", { name: "发布 catalog v1" })).toBeEnabled());

    fireEvent.click(screen.getByRole("button", { name: "发布 catalog v1" }));

    expect(await screen.findByText("422 semantic / coverage validation")).toBeInTheDocument();
    const affected = screen.getByRole("region", { name: "Coverage affected problems" });
    expect(within(affected).getByText(/A \+ B/)).toBeInTheDocument();
  });

  it("rejects an invalid local document before publish", async () => {
    render(<RecommendationKnowledgeCatalogPage />);
    const editor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" });
    await waitFor(() => expect(editor.value).not.toBe(""));
    fireEvent.change(editor, { target: { value: "{}" } });

    expect(screen.getByRole("button", { name: "发布 catalog v1" })).toBeDisabled();
    expect(await screen.findByText(/字段必须严格为/)).toBeInTheDocument();
    expect(api.publishRecommendationKnowledgeCatalog).not.toHaveBeenCalled();
  });

  it("blocks a valid catalog whose assignments do not exactly cover review facts", async () => {
	render(<RecommendationKnowledgeCatalogPage />);
	const editor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" });
	await waitFor(() => expect(editor.value).not.toBe(""));
	fireEvent.change(editor, { target: { value: JSON.stringify({ ...catalogDocument, problemAssignments: [] }, null, 2) } });

	const publish = screen.getByRole("button", { name: "发布 catalog v1" });
	await waitFor(() => expect(publish).toBeDisabled());
	expect(screen.getByText(/不能缺失或包含悬空 identity/)).toBeInTheDocument();
	expect(api.publishRecommendationKnowledgeCatalog).not.toHaveBeenCalled();
  });
});
