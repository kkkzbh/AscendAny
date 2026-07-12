import { afterEach, describe, expect, it, vi } from "vitest";
import { adaptProblemCollection, adaptRankingCollection, adaptSubmissionCollection } from "../src/domain/collector-adapters";
import {
  API_CALL_TIMEOUT_MS,
  DETAIL_BATCH_SIZE,
  DETAIL_REQUEST_SPACING_MS,
  EXPORT_LIMITS,
} from "../src/domain/limits";
import { buildSnapshot } from "../src/domain/normalize";
import type {
  CollectorName,
  CollectorRequest,
  CollectorResponse,
  JsonObject,
  SubmissionDetailSource,
} from "../src/domain/types";
import { collectPintiaRouteInMainWorld } from "../src/main-world-collector";
import getProblemSetResponse from "./fixtures/sanitized-get-problem-set-response.json";
import rankingHttpResponse from "./fixtures/sanitized-get-common-rankings-http-response.json";
import getSubmissionResponse from "./fixtures/synthetic-get-submission-response.json";
import listProblemsResponse from "./fixtures/synthetic-list-problem-set-problems-response.json";
import listSubmissionsResponse from "./fixtures/synthetic-list-submissions-response.json";
import listUserGroupsResponse from "./fixtures/synthetic-list-user-groups-for-problem-set-response.json";

const PROBLEM_SET_ID = "9000000000000000001";

type PageApi = (parameters: Record<string, unknown>, options: { message: false }) => Promise<unknown>;

function normalizedRankingApiResponse(): Record<string, unknown> {
  const studentUserById: Record<string, JsonObject> = {};
  const userById: Record<string, JsonObject> = {};
  const commonRankings = rankingHttpResponse.commonRankings.commonRankings.map((rawRanking) => {
    const student = structuredClone(rawRanking.user.studentUser);
    const user = structuredClone(rawRanking.user.user);
    studentUserById[student.id] = student;
    userById[user.id] = user;
    return {
      ...structuredClone(rawRanking),
      user: {
        examId: rawRanking.user.examId,
        studentUserId: student.id,
        userId: user.id,
        userGroupId: rawRanking.user.userGroupId,
      },
    };
  });
  return {
    total: rankingHttpResponse.total,
    commonRankings,
    studentUserById,
    userById,
  };
}

function serializedCollector(): typeof collectPintiaRouteInMainWorld {
  const factory = Function(
    `return (${collectPintiaRouteInMainWorld.toString()});`,
  )() as unknown;
  if (typeof factory !== "function") {
    throw new Error("Collector did not serialize to a standalone function.");
  }
  return factory as typeof collectPintiaRouteInMainWorld;
}

function installPageRuntime(
  overrides: Partial<Record<string, PageApi>> = {},
): Map<string, Array<Record<string, unknown>>> {
  const calls = new Map<string, Array<Record<string, unknown>>>();
  const api = (name: string, result: unknown): PageApi => async (parameters, options) => {
    expect(options).toEqual({ message: false });
    calls.set(name, [...(calls.get(name) ?? []), structuredClone(parameters)]);
    return structuredClone(result);
  };
  const exports = {
    getProblemSet: overrides.GetProblemSet ?? api("GetProblemSet", getProblemSetResponse),
    listProblems: overrides.ListProblemSetProblems ?? api("ListProblemSetProblems", listProblemsResponse),
    getRankings: overrides.GetCommonRankings ?? api("GetCommonRankings", normalizedRankingApiResponse()),
    listGroups: overrides.ListUserGroupsForProblemSet ?? api("ListUserGroupsForProblemSet", listUserGroupsResponse),
    listSubmissions: overrides.ListSubmissions ?? api("ListSubmissions", listSubmissionsResponse),
    getSubmission: overrides.GetSubmission ?? api("GetSubmission", getSubmissionResponse),
  };
  const moduleFactory = Function(`return function(){
    const a=(0,x.createAPI)({name:"GetProblemSet"}),b=(0,x.createAPI)({name:"ListProblemSetProblems"}),c=(0,x.createAPI)({name:"GetCommonRankings"}),d=(0,x.createAPI)({name:"ListUserGroupsForProblemSet"}),e=(0,x.createAPI)({name:"ListSubmissions"}),f=(0,x.createAPI)({name:"GetSubmission"});
    return {getProblemSet:()=>a,listProblems:()=>b,getRankings:()=>c,listGroups:()=>d,listSubmissions:()=>e,getSubmission:()=>f};
  }`)() as (...arguments_: unknown[]) => unknown;
  const required = ((moduleId: number): Record<string, unknown> => {
    if (moduleId !== 42) {
      throw new Error(`Unexpected synthetic module ${moduleId}.`);
    }
    return exports;
  }) as ((moduleId: number) => Record<string, unknown>) & {
    m: Record<string, (...arguments_: unknown[]) => unknown>;
  };
  required.m = { "42": moduleFactory };

  const chunk: unknown[] = [];
  chunk.push = ((payload: unknown): number => {
    if (!Array.isArray(payload) || typeof payload[2] !== "function") {
      throw new Error("Synthetic Webpack runtime received an invalid capture chunk.");
    }
    (payload[2] as (runtime: typeof required) => void)(required);
    return 1;
  }) as typeof chunk.push;
  vi.stubGlobal("location", { origin: "https://pintia.cn" });
  vi.stubGlobal("window", {
    webpackChunkbig_front: chunk,
    setTimeout: globalThis.setTimeout.bind(globalThis),
    clearTimeout: globalThis.clearTimeout.bind(globalThis),
  });
  return calls;
}

function request(
  collector: CollectorName,
  submissionIds?: string[],
  limitOverrides: Partial<CollectorRequest["limits"]> = {},
): CollectorRequest {
  return {
    type: "ASCENDANY_COLLECT_PINTIA_ROUTE_V2",
    problemSetId: PROBLEM_SET_ID,
    collector,
    limits: { ...EXPORT_LIMITS, ...limitOverrides },
    ...(submissionIds === undefined ? {} : { submissionIds }),
  };
}

function collectorResult(response: CollectorResponse): unknown {
  expect(response.ok, response.ok ? undefined : response.failure.message).toBe(true);
  if (!response.ok) {
    throw new Error(response.failure.message);
  }
  return response.result;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("serialized Pintia MAIN-world collector", () => {
  it("resolves current named APIs, adapts their shapes, and builds a strict snapshot", async () => {
    const calls = installPageRuntime();
    const collect = serializedCollector();

    const problems = adaptProblemCollection(
      collectorResult(await collect(request("problems"))),
      PROBLEM_SET_ID,
    );
    const rankings = adaptRankingCollection(
      collectorResult(await collect(request("rankings"))),
    );
    const submissions = adaptSubmissionCollection(
      collectorResult(await collect(request("submissions"))),
    );
    const detailBatch = collectorResult(
      await collect(request("submission-details", ["submission-alpha"])),
    ) as { items: Array<{ submissionId: string; detail: SubmissionDetailSource }> };
    expect(detailBatch.items.map((item) => item.submissionId)).toEqual(["submission-alpha"]);
    const detail = detailBatch.items[0]?.detail as SubmissionDetailSource;
    const snapshot = await buildSnapshot({
      problemSetId: PROBLEM_SET_ID,
      sourceUrl: `https://pintia.cn/problem-sets/${PROBLEM_SET_ID}/overview`,
      problems,
      rankings,
      submissions,
      submissionDetailsById: { "submission-alpha": detail },
    }, "2030-01-01T02:00:00.000Z");

    expect(snapshot.exam.title).toBe("SANITIZED_PROBLEM_SET");
    expect(snapshot.problems).toHaveLength(1);
    expect(snapshot.participants).toEqual([
      expect.objectContaining({
        userId: "user-alpha",
        studentUserId: "student-alpha",
        groupName: "Synthetic Group",
      }),
    ]);
    expect(snapshot.submissions[0]).toMatchObject({
      submissionId: "submission-alpha",
      code: "SANITIZED_CODE_PLACEHOLDER",
      verdict: "ACCEPTED",
    });
    expect(calls.get("GetProblemSet")).toEqual([{ problemSetId: PROBLEM_SET_ID }]);
    expect(calls.get("ListProblemSetProblems")).toEqual([{
      problemSetId: PROBLEM_SET_ID,
      problemType: "PROGRAMMING",
      page: 0,
      limit: 200,
    }]);
    expect(calls.get("GetCommonRankings")).toEqual([{
      problemSetId: PROBLEM_SET_ID,
      page: 0,
      limit: 200,
      filter: {},
    }]);
    expect(calls.get("ListSubmissions")).toEqual([{
      problemSetId: PROBLEM_SET_ID,
      limit: 200,
      filter: {},
    }]);
    expect(calls.get("GetSubmission")).toEqual([{ submissionId: "submission-alpha" }]);
  });

  it("drops an oversized unused API field before the MAIN-world result crosses the extension boundary", async () => {
    const response = structuredClone(listProblemsResponse) as Record<string, unknown>;
    const problems = response.problemSetProblems as Array<Record<string, unknown>>;
    (problems[0] as Record<string, unknown>).unusedPayload = "x".repeat(129);
    installPageRuntime({ ListProblemSetProblems: async () => response });

    const collected = collectorResult(await serializedCollector()(request(
      "problems",
      undefined,
      { maxStringBytes: 128 },
    ))) as Record<string, unknown>;

    expect(JSON.stringify(collected)).not.toContain("unusedPayload");
  });

  it.each(["compileLog", "caseOutput"] as const)(
    "rejects an oversized projected detail %s inside MAIN world",
    async (field) => {
      const response = structuredClone(getSubmissionResponse) as Record<string, unknown>;
      const submission = response.submission as Record<string, unknown>;
      const contents = submission.judgeResponseContents as Array<Record<string, unknown>>;
      const programming = contents[0]?.programmingJudgeResponseContent as Record<string, unknown>;
      if (field === "compileLog") {
        const compilation = programming.compilationResult as Record<string, unknown>;
        compilation.log = "x".repeat(129);
      } else {
        const cases = programming.testcaseJudgeResults as Record<string, Record<string, unknown>>;
        (cases["case-1"] as Record<string, unknown>).checkerOutput = "x".repeat(129);
      }
      installPageRuntime({ GetSubmission: async () => response });

      await expect(serializedCollector()(request(
        "submission-details",
        ["submission-alpha"],
        { maxStringBytes: 128 },
      ))).resolves.toMatchObject({
        ok: false,
        failure: {
          kind: "collector",
          message: expect.stringContaining("per-string byte budget 128"),
        },
      });
    },
  );

  it("enforces the cumulative projected budget across pagination pages", async () => {
    const template = (listProblemsResponse.problemSetProblems[0] ?? {}) as Record<string, unknown>;
    installPageRuntime({
      ListProblemSetProblems: async (parameters) => {
        const page = parameters.page as number;
        return {
          problemSetProblems: [{
            ...structuredClone(template),
            id: `problem-set-problem-${page}`,
            problemId: `problem-${page}`,
            title: `Problem ${page}`,
            content: "x".repeat(200),
          }],
          total: 2,
          hasNext: page === 0,
        };
      },
    });

    await expect(serializedCollector()(request("problems", undefined, {
      maxStringBytes: 256,
      maxTotalStringBytes: 500,
    }))).resolves.toMatchObject({
      ok: false,
      failure: {
        kind: "collector",
        message: expect.stringContaining("collector string byte budget 500"),
      },
    });
  });

  it("fails closed before resolving Webpack APIs on another origin", async () => {
    installPageRuntime();
    vi.stubGlobal("location", { origin: "https://example.invalid" });

    const response = await serializedCollector()(request("problems"));

    expect(response).toMatchObject({
      ok: false,
      collector: "problems",
      failure: {
        kind: "collector",
        message: "AscendAny collector executed on an unexpected origin.",
      },
    });
  });

  it.each([
    ["GetProblemSet", "problems", undefined],
    ["ListProblemSetProblems", "problems", undefined],
    ["ListUserGroupsForProblemSet", "rankings", undefined],
    ["GetCommonRankings", "rankings", undefined],
    ["ListSubmissions", "submissions", undefined],
    ["GetSubmission", "submission-details", ["submission-alpha"]],
  ] as const)("terminates a never-settling %s API at its per-call deadline", async (
    apiName,
    collector,
    submissionIds,
  ) => {
    vi.useFakeTimers();
    installPageRuntime({ [apiName]: async () => new Promise<never>(() => undefined) });

    const responsePromise = serializedCollector()(request(
      collector,
      submissionIds === undefined ? undefined : [...submissionIds],
    ));
    await vi.advanceTimersByTimeAsync(API_CALL_TIMEOUT_MS);

    await expect(responsePromise).resolves.toMatchObject({
      ok: false,
      collector,
      failure: {
        kind: "collector",
        message: `${apiName} timed out.`,
      },
    });
  });

  it("classifies an Axios-like HTTP 429 rejection without exposing its untrusted message", async () => {
    const rejected = Object.assign(new Error("untrusted server detail must stay in MAIN world"), {
      response: { status: 429 },
    });
    installPageRuntime({ GetSubmission: async () => { throw rejected; } });

    const response = await serializedCollector()(request(
      "submission-details",
      ["submission-alpha"],
    ));

    expect(response).toEqual({
      ok: false,
      collector: "submission-details",
      failure: {
        kind: "rate_limited",
        status: 429,
        message: "Pintia API request was rate limited (HTTP 429).",
      },
    });
    expect(JSON.stringify(response)).not.toContain("untrusted server detail");
  });

  it("classifies other structured HTTP failures separately from collector failures", async () => {
    installPageRuntime({
      GetSubmission: async () => {
        throw { response: { status: 503 }, body: "untrusted body" };
      },
    });

    await expect(serializedCollector()(request(
      "submission-details",
      ["submission-alpha"],
    ))).resolves.toEqual({
      ok: false,
      collector: "submission-details",
      failure: {
        kind: "http",
        status: 503,
        message: "Pintia API request failed with HTTP 503.",
      },
    });
  });

  it("bounds and sanitizes an ordinary rejected error message", async () => {
    installPageRuntime({
      GetSubmission: async () => {
        throw new Error(`ordinary\ncollector\u0000failure ${"x".repeat(1_000)}`);
      },
    });

    const response = await serializedCollector()(request(
      "submission-details",
      ["submission-alpha"],
    ));

    expect(response).toMatchObject({
      ok: false,
      collector: "submission-details",
      failure: {
        kind: "collector",
        message: expect.stringMatching(/^ordinary collector failure /),
      },
    });
    if (response.ok) {
      throw new Error("Synthetic ordinary failure unexpectedly succeeded.");
    }
    expect(response.failure.message).toHaveLength(512);
    expect(response.failure.message).not.toMatch(/[\u0000-\u001f\u007f]/);
  });

  it("collects a maximum-size detail batch with bounded concurrency and exact ordering", async () => {
    vi.useFakeTimers();
    const submissionIds = Array.from({ length: EXPORT_LIMITS.maxDetailBatchSize }, (_, index) =>
      `submission-${index.toString().padStart(2, "0")}`);
    const calls: string[] = [];
    let active = 0;
    let maximumActive = 0;
    installPageRuntime({
      GetSubmission: async (parameters) => {
        const submissionId = parameters.submissionId;
        if (typeof submissionId !== "string") {
          throw new Error("Synthetic GetSubmission request is missing submissionId.");
        }
        calls.push(submissionId);
        active += 1;
        maximumActive = Math.max(maximumActive, active);
        await new Promise<void>((resolve) => setTimeout(resolve, 1_000));
        active -= 1;
        return structuredClone(getSubmissionResponse);
      },
    });

    const responsePromise = serializedCollector()(request("submission-details", submissionIds));
    await vi.runAllTimersAsync();
    const result = collectorResult(await responsePromise) as {
      items: Array<{ submissionId: string; detail: SubmissionDetailSource }>;
    };

    expect(calls).toEqual(submissionIds);
    expect(maximumActive).toBe(EXPORT_LIMITS.detailBatchConcurrency);
    expect(result.items.map((item) => item.submissionId)).toEqual(submissionIds);
    expect(result.items.map((item) => item.detail.submissionId)).toEqual(submissionIds);
  });

  it("starts a production-sized detail batch without a MAIN-world pacing timer", async () => {
    vi.useFakeTimers();
    const timeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const submissionIds = Array.from(
      { length: DETAIL_BATCH_SIZE },
      (_, index) => `submission-${index}`,
    );
    const calls: string[] = [];
    installPageRuntime({
      GetSubmission: async (parameters) => {
        calls.push(String(parameters.submissionId));
        return structuredClone(getSubmissionResponse);
      },
    });

    const response = serializedCollector()(request("submission-details", submissionIds));
    await vi.runAllTimersAsync();
    expect(collectorResult(await response)).toMatchObject({
      items: submissionIds.map((submissionId) => ({ submissionId })),
    });

    const timerDurations = timeoutSpy.mock.calls.flatMap((call) =>
      typeof call[1] === "number" ? [call[1]] : []);
    expect(calls).toEqual(submissionIds);
    expect(timerDurations).not.toContain(DETAIL_REQUEST_SPACING_MS);
    expect(timerDurations.every((duration) =>
      duration === API_CALL_TIMEOUT_MS || duration === EXPORT_LIMITS.detailBatchTimeoutMs)).toBe(true);
  });

  it("rejects oversized and duplicate detail batches before calling GetSubmission", async () => {
    const calls: string[] = [];
    installPageRuntime({
      GetSubmission: async (parameters) => {
        calls.push(String(parameters.submissionId));
        return {};
      },
    });
    const collect = serializedCollector();
    const oversized = Array.from(
      { length: EXPORT_LIMITS.maxDetailBatchSize + 1 },
      (_, index) => `submission-${index}`,
    );

    await expect(collect(request("submission-details", oversized))).resolves.toMatchObject({
      ok: false,
      failure: {
        kind: "collector",
        message: `Submission detail batch must contain 1 to ${EXPORT_LIMITS.maxDetailBatchSize} ids.`,
      },
    });
    await expect(collect(request("submission-details", ["duplicate", "duplicate"]))).resolves.toMatchObject({
      ok: false,
      failure: {
        kind: "collector",
        message: "Submission detail batch repeated id duplicate.",
      },
    });
    expect(calls).toEqual([]);
  });

  it("fails a detail batch atomically and starts no work beyond bounded concurrency", async () => {
    vi.useFakeTimers();
    const calls: string[] = [];
    installPageRuntime({
      GetSubmission: async (parameters) => {
        const submissionId = String(parameters.submissionId);
        calls.push(submissionId);
        if (submissionId === "submission-0") {
          throw new Error("synthetic detail failure");
        }
        return { marker: submissionId };
      },
    });
    const response = serializedCollector()(request(
      "submission-details",
      Array.from({ length: 16 }, (_, index) => `submission-${index}`),
    ));

    await vi.runAllTimersAsync();
    await expect(response).resolves.toMatchObject({
      ok: false,
      collector: "submission-details",
      failure: {
        kind: "collector",
        message: "synthetic detail failure",
      },
    });
    expect(calls).toEqual(Array.from({ length: 8 }, (_, index) => `submission-${index}`));
  });

  it.each([
    ["problems", undefined],
    ["submission-details", ["submission-alpha"]],
  ] as const)("enforces the whole %s collector deadline", async (collector, submissionIds) => {
    vi.useFakeTimers();
    installPageRuntime({
      GetProblemSet: async () => new Promise<never>(() => undefined),
      GetSubmission: async () => new Promise<never>(() => undefined),
    });
    const response = serializedCollector()(request(
      collector,
      submissionIds === undefined ? undefined : [...submissionIds],
      {
        apiCallTimeoutMs: 1_000,
        collectionTimeoutMs: 100,
        detailBatchTimeoutMs: 100,
      },
    ));

    await vi.advanceTimersByTimeAsync(100);
    await expect(response).resolves.toMatchObject({ ok: false, collector });
  });
});
