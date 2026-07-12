import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ApiError,
  ConfigurationItem,
  QueueRecommendationTrainingRunResult,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
  RecommendationTrainingEventPage,
  RecommendationTrainingRunDetail,
} from "@ascendany/sdk";
import { RecommendationWorkflowError } from "../api/recommendation";
import { RecommendationTrainingPage } from "./RecommendationTrainingPage";

const api = vi.hoisted(() => ({
  loadRecommendationConfiguration: vi.fn(),
  loadRecommendationReviewContext: vi.fn(),
  loadRecommendationTrainingEvents: vi.fn(),
  loadRecommendationTrainingRun: vi.fn(),
  publishRecommendationConfiguration: vi.fn(),
  queueRecommendationTraining: vi.fn(),
}));

vi.mock("../api/recommendation", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/recommendation")>(),
  ...api,
}));

const runId = "11111111-1111-4111-8111-111111111115";
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

function configurationItem(
  kind: "knowledge_catalog" | "training",
  key: string,
  id: string,
  document: Record<string, unknown>,
  headRevision = 1,
): ConfigurationItem {
  return {
    id: kind === "knowledge_catalog"
      ? "11111111-1111-4111-8111-111111111111"
      : "11111111-1111-4111-8111-111111111112",
    key,
    kind,
    headRevision,
    activeVersion: {
      id,
      number: headRevision,
      schemaId: kind === "knowledge_catalog"
        ? "ascendany.knowledge_catalog.recommendation.v1"
        : "ascendany.training.recommendation.v2",
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

const trainingDocument = {
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

const catalogItem = configurationItem(
  "knowledge_catalog",
  "recommendation.catalog.default",
  "16",
  catalogDocument,
);
const trainingItem = configurationItem(
  "training",
  "recommendation.training.default",
  "17",
  trainingDocument,
);

function queueResult(created = true): QueueRecommendationTrainingRunResult {
  return {
    created,
    trainingRun: {
      id: runId,
      sourceAnalyticsGenerationId: "9",
      sourceAnalyticsHeadRevision: 4,
      trainingConfigurationVersionId: "17",
      knowledgeCatalogVersionId: "16",
      trainingConfigurationKey: trainingItem.key,
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
}

function runDetail(
  status: RecommendationTrainingRunDetail["status"],
  failure: RecommendationTrainingRunDetail["failure"] = null,
): RecommendationTrainingRunDetail {
  return {
    ...queueResult().trainingRun,
    status,
    attemptCount: status === "queued" ? 0 : 1,
    startedAt: status === "queued" ? null : "2026-07-12T01:01:01Z",
    finishedAt: status === "running" || status === "queued" ? null : "2026-07-12T01:01:02Z",
    failure,
  };
}

const noEvents: RecommendationTrainingEventPage = {
  runId,
  items: [],
  nextAfterSequence: null,
};

function deferred<Value>() {
  let resolve!: (value: Value) => void;
  const promise = new Promise<Value>((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
}

describe("RecommendationTrainingPage", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    api.loadRecommendationReviewContext.mockResolvedValue(review);
    api.loadRecommendationTrainingRun.mockResolvedValue(runDetail("succeeded"));
    api.loadRecommendationTrainingEvents.mockResolvedValue(noEvents);
    api.publishRecommendationConfiguration
      .mockResolvedValueOnce({ item: catalogItem, idempotent: false })
      .mockResolvedValueOnce({ item: trainingItem, idempotent: false });
    api.queueRecommendationTraining.mockResolvedValue(queueResult());
  });

  async function publishFirstRunConfigurations() {
    await screen.findByText(problemKey);
    await waitFor(() => expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" }).value).not.toBe(""));
    fireEvent.click(await screen.findByRole("button", { name: "发布 catalog v1" }));
    await waitFor(() => expect(api.publishRecommendationConfiguration).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "发布 training v2" }));
    await waitFor(() => expect(api.publishRecommendationConfiguration).toHaveBeenCalledTimes(2));
  }

  it("completes the first-run review, catalog, training configuration, and fenced queue", async () => {
    render(<RecommendationTrainingPage />);

    expect(await screen.findByText(problemKey)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "2039" })).toHaveAttribute("href", "https://pintia.cn/problem-sets/2039");
    expect(screen.getByRole("link", { name: "2040" })).toHaveAttribute("href", "https://pintia.cn/problem-sets/2040/problems/type/7");

    await publishFirstRunConfigurations();

    expect(api.publishRecommendationConfiguration.mock.calls[0]?.[0]).toEqual(expect.objectContaining({
      key: "recommendation.catalog.default",
      kind: "knowledge_catalog",
      expectedHeadRevision: 0,
      schemaId: "ascendany.knowledge_catalog.recommendation.v1",
      credentialRef: null,
      document: expect.objectContaining({
        problemAssignments: [{
          platform: "pintia",
          problemId: "7",
          problemFactSha256: "c".repeat(64),
          knowledge: [{ knowledgePointId: "fundamentals", weight: 1 }],
        }],
      }),
    }));
    expect(api.publishRecommendationConfiguration.mock.calls[1]?.[0]).toEqual(expect.objectContaining({
      key: "recommendation.training.default",
      kind: "training",
      expectedHeadRevision: 0,
      schemaId: "ascendany.training.recommendation.v2",
      credentialRef: null,
      document: expect.objectContaining({
        knowledgeCatalogVersionId: "16",
        batchSize: 32,
        minTrainInteractions: 32,
      }),
    }));

    fireEvent.click(screen.getByRole("button", { name: "按已 review provenance 排队" }));
    await waitFor(() => expect(api.queueRecommendationTraining).toHaveBeenCalledWith({
      trainingConfigurationKey: "recommendation.training.default",
      expectedAnalyticsGenerationId: "9",
      expectedAnalyticsHeadRevision: 4,
    }));
    expect(await screen.findByText("已创建新的 durable 训练任务。")).toBeInTheDocument();
  });

  it("preserves an edited catalog draft when the same provenance is reloaded", async () => {
    render(<RecommendationTrainingPage />);
    const editor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" });
    await waitFor(() => expect(editor.value).not.toBe(""));
    const editedDocument = editor.value.replace("Fundamentals", "Reviewed fundamentals");
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

  it("resets CAS ownership when an operator changes a loaded configuration key", async () => {
    const existingCatalog = configurationItem(
      "knowledge_catalog",
      "recommendation.catalog.existing",
      "18",
      catalogDocument,
      3,
    );
    api.loadRecommendationConfiguration.mockResolvedValue(existingCatalog);
    render(<RecommendationTrainingPage />);
    const panel = await screen.findByRole("region", { name: "Knowledge catalog editor" });
    await waitFor(() => expect(within(panel).getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" }).value).not.toBe(""));

    fireEvent.click(within(panel).getByRole("button", { name: "读取 active version" }));
    expect(await within(panel).findByText("CAS r3")).toBeInTheDocument();
    fireEvent.change(within(panel).getByRole("textbox", { name: "Configuration key" }), {
      target: { value: "recommendation.catalog.new" },
    });
    expect(within(panel).getByText("CAS r0")).toBeInTheDocument();
    fireEvent.click(within(panel).getByRole("button", { name: "发布 catalog v1" }));

    await waitFor(() => expect(api.publishRecommendationConfiguration).toHaveBeenCalledWith(expect.objectContaining({
      key: "recommendation.catalog.new",
      kind: "knowledge_catalog",
      expectedHeadRevision: 0,
    })));
  });

  it("resets training CAS ownership when a loaded training key changes", async () => {
    const existingTraining = configurationItem(
      "training",
      "recommendation.training.existing",
      "19",
      trainingDocument,
      3,
    );
    api.loadRecommendationConfiguration.mockResolvedValue(existingTraining);
    render(<RecommendationTrainingPage />);
    await screen.findByText(problemKey);
    const catalogPanel = screen.getByRole("region", { name: "Knowledge catalog editor" });
    await waitFor(() => expect(within(catalogPanel).getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" }).value).not.toBe(""));
    fireEvent.click(within(catalogPanel).getByRole("button", { name: "发布 catalog v1" }));
    await waitFor(() => expect(api.publishRecommendationConfiguration).toHaveBeenCalledTimes(1));

    const trainingPanel = screen.getByRole("region", { name: "Recommendation training configuration editor" });
    fireEvent.click(within(trainingPanel).getByRole("button", { name: "读取 active version" }));
    expect(await within(trainingPanel).findByText("CAS r3")).toBeInTheDocument();
    fireEvent.change(within(trainingPanel).getByRole("textbox", { name: "Configuration key" }), {
      target: { value: "recommendation.training.new" },
    });
    expect(within(trainingPanel).getByText("CAS r0")).toBeInTheDocument();
    api.publishRecommendationConfiguration.mockResolvedValue({ item: trainingItem, idempotent: false });
    fireEvent.click(within(trainingPanel).getByRole("button", { name: "发布 training v2" }));

    await waitFor(() => expect(api.publishRecommendationConfiguration).toHaveBeenLastCalledWith(expect.objectContaining({
      key: "recommendation.training.new",
      kind: "training",
      expectedHeadRevision: 0,
    })));
  });

  it("shows 409 drift and requires an explicit review reload before another queue", async () => {
    const drift: ApiError = {
      code: "recommendation_analytics_head_conflict",
      message: "Analytics head changed.",
      requestId: "44444444-4444-4444-8444-444444444444",
      details: { currentAnalyticsGenerationId: "10", currentAnalyticsHeadRevision: 5 },
    };
    api.queueRecommendationTraining.mockRejectedValue(new RecommendationWorkflowError(409, drift));
    render(<RecommendationTrainingPage />);
    await publishFirstRunConfigurations();

    fireEvent.click(screen.getByRole("button", { name: "按已 review provenance 排队" }));
    expect(await screen.findByText("409 analytics drift")).toBeInTheDocument();
    expect(screen.getByText(/必须重新加载 recommendation review/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "按已 review provenance 排队" })).toBeDisabled();

    const refreshed = { ...review, analyticsGenerationId: "10", analyticsHeadRevision: 5 };
    api.loadRecommendationReviewContext.mockResolvedValue(refreshed);
    fireEvent.click(screen.getByRole("button", { name: "重新加载 review" }));
    await waitFor(() => expect(screen.getByText("generation=10 / head=5")).toBeInTheDocument());
  });

  it("keeps a new queue drift visible when an older run poll completes", async () => {
    const poll = deferred<RecommendationTrainingRunDetail>();
    const drift: ApiError = {
      code: "recommendation_analytics_head_conflict",
      message: "Analytics head changed.",
      requestId: "44444444-4444-4444-8444-444444444444",
      details: { currentAnalyticsGenerationId: "10", currentAnalyticsHeadRevision: 5 },
    };
    api.loadRecommendationTrainingRun.mockReset();
    api.loadRecommendationTrainingRun.mockReturnValueOnce(poll.promise);
    api.loadRecommendationTrainingRun.mockResolvedValue(runDetail("succeeded"));
    api.queueRecommendationTraining
      .mockResolvedValueOnce(queueResult())
      .mockRejectedValueOnce(new RecommendationWorkflowError(409, drift));
    render(<RecommendationTrainingPage />);
    await publishFirstRunConfigurations();

    const queueButton = screen.getByRole("button", { name: "按已 review provenance 排队" });
    fireEvent.click(queueButton);
    expect(await screen.findByText("已创建新的 durable 训练任务。")).toBeInTheDocument();
    await waitFor(() => expect(api.loadRecommendationTrainingRun).toHaveBeenCalledTimes(1));
    fireEvent.click(queueButton);
    expect(await screen.findByText("409 analytics drift")).toBeInTheDocument();
    expect(screen.queryByText("已创建新的 durable 训练任务。")).not.toBeInTheDocument();

    await act(async () => {
      poll.resolve(runDetail("succeeded"));
      await poll.promise;
    });
    await waitFor(() => expect(screen.getAllByText("succeeded").length).toBeGreaterThan(0));
    expect(screen.getByText("409 analytics drift")).toBeInTheDocument();
  });

  it("prevents an in-flight poll from an older run from overwriting a newly queued run", async () => {
    const nextRunId = "11111111-1111-4111-8111-111111111116";
    const oldPoll = deferred<RecommendationTrainingRunDetail>();
    const nextQueue = {
      ...queueResult(),
      trainingRun: { ...queueResult().trainingRun, id: nextRunId },
    } satisfies QueueRecommendationTrainingRunResult;
    const nextDetail = { ...runDetail("succeeded"), id: nextRunId };
    api.loadRecommendationTrainingRun.mockReset();
    api.loadRecommendationTrainingRun
      .mockReturnValueOnce(oldPoll.promise)
      .mockResolvedValueOnce(nextDetail);
    api.loadRecommendationTrainingEvents.mockResolvedValue({ ...noEvents, runId: nextRunId });
    api.queueRecommendationTraining
      .mockResolvedValueOnce(queueResult())
      .mockResolvedValueOnce(nextQueue);
    render(<RecommendationTrainingPage />);
    await publishFirstRunConfigurations();

    const queueButton = screen.getByRole("button", { name: "按已 review provenance 排队" });
    fireEvent.click(queueButton);
    await waitFor(() => expect(api.loadRecommendationTrainingRun).toHaveBeenCalledWith(runId));
    fireEvent.click(queueButton);
    await waitFor(() => expect(api.loadRecommendationTrainingRun).toHaveBeenCalledWith(nextRunId));
    expect(await screen.findByText(nextRunId)).toBeInTheDocument();

    await act(async () => {
      oldPoll.resolve(runDetail("failed", {
        code: "old_run_failure",
        message: "Training ended with a recorded failure. See ordered events for its safe operational context.",
      }));
      await oldPoll.promise;
    });

    expect(screen.getByText(nextRunId)).toBeInTheDocument();
    expect(screen.queryByText("Safe terminal failure: old_run_failure")).not.toBeInTheDocument();
    expect(api.loadRecommendationTrainingEvents).not.toHaveBeenCalledWith(runId, expect.any(Number));
  });

  it("blocks a malformed typed document before calling the publish API", async () => {
    render(<RecommendationTrainingPage />);
    await screen.findByText(problemKey);
    fireEvent.change(screen.getByRole("textbox", { name: "Knowledge catalog document" }), {
      target: { value: `{"taxonomyId":"recommendation.catalog.default","knowledgePoints":[]}` },
    });
    fireEvent.click(screen.getByRole("button", { name: "发布 catalog v1" }));

    expect(await screen.findByText(/字段必须严格为/)).toBeInTheDocument();
    expect(screen.getByText("draft invalid")).toBeInTheDocument();
    expect(api.publishRecommendationConfiguration).not.toHaveBeenCalled();
  });

  it("shows the server-owned configuration_document_invalid 422", async () => {
    const validation: ApiError = {
      code: "configuration_document_invalid",
      message: "Configuration document violates its semantic schema.",
      requestId: "55555555-5555-4555-8555-555555555555",
    };
    api.publishRecommendationConfiguration.mockReset();
    api.publishRecommendationConfiguration.mockRejectedValue(new RecommendationWorkflowError(422, validation));
    render(<RecommendationTrainingPage />);
    await screen.findByText(problemKey);
    await waitFor(() => expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" }).value).not.toBe(""));
    fireEvent.click(screen.getByRole("button", { name: "发布 catalog v1" }));

    expect(await screen.findByText("422 semantic / coverage validation")).toBeInTheDocument();
    expect(screen.getByText("Configuration document violates its semantic schema.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Coverage affected problems" })).not.toBeInTheDocument();
  });

  it("shows queue-owned 422 coverage details and maps affected problem identities", async () => {
    const validation: ApiError = {
      code: "recommendation_preflight_failed",
      message: "Catalog coverage is incomplete.",
      requestId: "55555555-5555-4555-8555-555555555555",
      details: { issueCode: "knowledge_catalog_assignment_missing", problemKeys: [problemKey] },
    };
    api.queueRecommendationTraining.mockRejectedValue(new RecommendationWorkflowError(422, validation));
    render(<RecommendationTrainingPage />);
    await screen.findByText(problemKey);
    const catalogEditor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Knowledge catalog document" });
    await waitFor(() => expect(catalogEditor.value).not.toBe(""));
    const wrongFactCatalog = JSON.parse(catalogEditor.value) as RecommendationKnowledgeCatalogV1;
    wrongFactCatalog.problemAssignments[0]!.problemFactSha256 = "f".repeat(64);
    fireEvent.change(catalogEditor, { target: { value: JSON.stringify(wrongFactCatalog, null, 2) } });
    expect(await screen.findByText("fact changed")).toBeInTheDocument();
    await publishFirstRunConfigurations();
    fireEvent.click(screen.getByRole("button", { name: "按已 review provenance 排队" }));
    expect(await screen.findByText("422 semantic / coverage validation")).toBeInTheDocument();
    expect(screen.getByText(/knowledge_catalog_assignment_missing/)).toBeInTheDocument();
    const affected = screen.getByRole("region", { name: "Coverage affected problems" });
    expect(within(affected).getByText(/A \+ B/)).toBeInTheDocument();
    expect(within(affected).getByText(problemKey)).toBeInTheDocument();
  });

  it("renders ordered durable event progression and safe terminal failure for a recovered run", async () => {
    const firstEvents: RecommendationTrainingEventPage = {
      runId,
      items: [
        {
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
        },
        {
          sequence: 2,
          type: "claimed",
          payload: { attemptCount: 1, leaseOwner: "trainer-1" },
          createdAt: "2026-07-12T01:01:01Z",
        },
      ],
      nextAfterSequence: 2,
    };
    const terminalEvents: RecommendationTrainingEventPage = {
      runId,
      items: [{
        sequence: 3,
        type: "failed",
        payload: { attemptCount: 1, code: "trainer_contract_rejected" },
        createdAt: "2026-07-12T01:01:02Z",
      }],
      nextAfterSequence: null,
    };
    api.queueRecommendationTraining.mockResolvedValue({
      ...queueResult(false),
      trainingRun: {
        ...queueResult(false).trainingRun,
        status: "failed",
        attemptCount: 1,
        startedAt: "2026-07-12T01:01:01Z",
        finishedAt: "2026-07-12T01:01:02Z",
      },
    });
    api.loadRecommendationTrainingRun
      .mockRejectedValueOnce(new Error("poll transport unavailable"))
      .mockResolvedValue(runDetail("failed", {
        code: "trainer_contract_rejected",
        message: "Training ended with a recorded failure. See ordered events for its safe operational context.",
      }));
    api.loadRecommendationTrainingEvents
      .mockResolvedValueOnce(firstEvents)
      .mockResolvedValueOnce(terminalEvents);
    render(<RecommendationTrainingPage />);
    await publishFirstRunConfigurations();

    fireEvent.click(screen.getByRole("button", { name: "按已 review provenance 排队" }));
    expect(await screen.findByText("created=false：已恢复现有训练任务并继续追踪。")).toBeInTheDocument();
    expect(await screen.findByText("poll transport unavailable")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "从 review 重建模板" }));
    expect(screen.getByRole("button", { name: "继续追踪 durable run" })).toBeInTheDocument();
    expect(screen.getByText("poll transport unavailable")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "继续追踪 durable run" }));
    expect(await screen.findByText("Safe terminal failure: trainer_contract_rejected")).toBeInTheDocument();
    expect(screen.getByText("Training ended with a recorded failure. See ordered events for its safe operational context.")).toBeInTheDocument();
    const timeline = screen.getByRole("list", { name: "Ordered durable training events" });
    const items = within(timeline).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items.map((item) => item.textContent)).toEqual([
      expect.stringContaining("#1queued"),
      expect.stringContaining("#2claimed"),
      expect.stringContaining("#3failed"),
    ]);
    expect(api.loadRecommendationTrainingEvents).toHaveBeenCalledWith(runId, 0);
    expect(api.loadRecommendationTrainingEvents).toHaveBeenCalledWith(runId, 2);
    expect(api.loadRecommendationTrainingRun.mock.invocationCallOrder[1]).toBeLessThan(
      api.loadRecommendationTrainingEvents.mock.invocationCallOrder[0] ?? 0,
    );
  });
});
