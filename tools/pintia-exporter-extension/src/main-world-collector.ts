import type {
  CollectorFailure,
  CollectorRequest,
  CollectorResponse,
} from "./domain/types";

// Chrome serializes this function and executes it in Pintia's MAIN world. Keep every
// runtime dependency inside the function so the injected collector has no page-visible
// request channel or persistent page-global state.
export async function collectPintiaRouteInMainWorld(request: CollectorRequest): Promise<CollectorResponse> {
  type ApiName =
    | "GetProblemSet"
    | "ListProblemSetProblems"
    | "GetCommonRankings"
    | "ListUserGroupsForProblemSet"
    | "ListSubmissions"
    | "GetSubmission";
  type ApiFunction = (
    parameters: Record<string, unknown>,
    options: { message: false },
  ) => Promise<unknown>;
  interface WebpackRequire {
    (id: number): Record<string, unknown>;
    m?: Record<string, (...arguments_: unknown[]) => unknown>;
  }

  const PAGE_ORIGIN = "https://pintia.cn";
  const PROBLEM_PAGE_SIZE = 200;
  const RANKING_PAGE_SIZE = 200;
  const SUBMISSION_PAGE_SIZE = 200;
  const MAX_FAILURE_MESSAGE_LENGTH = 512;
  let apiCallTimeoutMs = 0;
  let requestDeadlineAt = 0;
  let maximumStringBytes = 0;
  let maximumTotalStringBytes = 0;
  let maximumTotalNodes = 0;
  let maximumJsonDepth = 0;
  let maximumCodeBytes = 0;
  let maximumCaseResults = 0;
  let consumedStringBytes = 0;
  let consumedNodes = 0;

  function boundedFailureMessage(error: unknown): string {
    let rawMessage: unknown;
    try {
      rawMessage = error instanceof Error
        ? error.message
        : typeof error === "string"
          ? error
          : undefined;
    } catch {
      rawMessage = undefined;
    }
    const normalized = typeof rawMessage === "string"
      ? rawMessage.replace(/[\u0000-\u001f\u007f]+/g, " ").trim()
      : "";
    const message = normalized.length === 0 ? "Pintia collector failed." : normalized;
    return message.slice(0, MAX_FAILURE_MESSAGE_LENGTH);
  }

  function rejectedHttpStatus(error: unknown): number | null {
    try {
      if (typeof error !== "object" || error === null || Array.isArray(error)) {
        return null;
      }
      const response = (error as Record<string, unknown>).response;
      if (typeof response !== "object" || response === null || Array.isArray(response)) {
        return null;
      }
      const status = (response as Record<string, unknown>).status;
      return typeof status === "number" && Number.isSafeInteger(status) && status >= 100 && status <= 599
        ? status
        : null;
    } catch {
      return null;
    }
  }

  function collectorFailure(error: unknown): CollectorFailure {
    const status = rejectedHttpStatus(error);
    if (status === 429) {
      return {
        kind: "rate_limited",
        status,
        message: "Pintia API request was rate limited (HTTP 429).",
      };
    }
    if (status !== null) {
      return {
        kind: "http",
        status,
        message: `Pintia API request failed with HTTP ${status}.`,
      };
    }
    return { kind: "collector", message: boundedFailureMessage(error) };
  }

  function object(value: unknown, field: string): Record<string, unknown> {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      throw new Error(`${field} must be an object.`);
    }
    return value as Record<string, unknown>;
  }

  function objectArray(value: unknown, field: string): Array<Record<string, unknown>> {
    if (!Array.isArray(value)) {
      throw new Error(`${field} must be an array.`);
    }
    return value.map((item, index) => object(item, `${field}[${index}]`));
  }

  function id(value: unknown, field: string): string {
    if (typeof value !== "string" || value.length === 0) {
      throw new Error(`${field} must be a non-empty string.`);
    }
    return value;
  }

  function requiredBoolean(value: unknown, field: string): boolean {
    if (typeof value !== "boolean") {
      throw new Error(`${field} must be boolean.`);
    }
    return value;
  }

  function positiveLimit(value: unknown, field: string): number {
    if (typeof value !== "number" || !Number.isSafeInteger(value) || value <= 0) {
      throw new Error(`${field} must be a positive safe integer.`);
    }
    return value;
  }

  function responseCount(response: Record<string, unknown>): number | null {
    if (response.total === undefined || response.total === null) {
      return null;
    }
    if (typeof response.total !== "number" || !Number.isSafeInteger(response.total) || response.total < 0) {
      throw new Error("Pintia response total must be a non-negative integer.");
    }
    return response.total;
  }

  function responseHasNext(response: Record<string, unknown>): boolean | null {
    if (response.hasNext === undefined || response.hasNext === null) {
      return null;
    }
    if (typeof response.hasNext !== "boolean") {
      throw new Error("Pintia response hasNext must be boolean when present.");
    }
    return response.hasNext;
  }

  function canonicalJson(value: unknown): string {
    if (Array.isArray(value)) {
      return `[${value.map(canonicalJson).join(",")}]`;
    }
    if (typeof value === "object" && value !== null) {
      return `{${Object.entries(value as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, child]) => `${JSON.stringify(key)}:${canonicalJson(child)}`)
        .join(",")}}`;
    }
    return JSON.stringify(value);
  }

  function utf8Bytes(value: string): number {
    let bytes = 0;
    for (let index = 0; index < value.length; index += 1) {
      const first = value.charCodeAt(index);
      if (first <= 0x7f) {
        bytes += 1;
      } else if (first <= 0x7ff) {
        bytes += 2;
      } else if (first >= 0xd800 && first <= 0xdbff) {
        const second = value.charCodeAt(index + 1);
        if (second >= 0xdc00 && second <= 0xdfff) {
          bytes += 4;
          index += 1;
        } else {
          bytes += 3;
        }
      } else {
        bytes += 3;
      }
    }
    return bytes;
  }

  function consumeProjectedBudget(value: unknown, field: string): void {
    const stack: Array<{ value: unknown; depth: number; field: string }> = [
      { value, depth: 0, field },
    ];
    while (stack.length > 0) {
      const current = stack.pop() as { value: unknown; depth: number; field: string };
      consumedNodes += 1;
      if (consumedNodes > maximumTotalNodes) {
        throw new Error(`${current.field} exceeds the collector node budget ${maximumTotalNodes}.`);
      }
      if (typeof current.value === "string") {
        const bytes = utf8Bytes(current.value);
        if (bytes > maximumStringBytes) {
          throw new Error(`${current.field} exceeds the per-string byte budget ${maximumStringBytes}.`);
        }
        consumedStringBytes += bytes;
        if (consumedStringBytes > maximumTotalStringBytes) {
          throw new Error(`${current.field} exceeds the collector string byte budget ${maximumTotalStringBytes}.`);
        }
        continue;
      }
      if (Array.isArray(current.value)) {
        const depth = current.depth + 1;
        if (depth > maximumJsonDepth) {
          throw new Error(`${current.field} exceeds the collector JSON depth ${maximumJsonDepth}.`);
        }
        for (let index = current.value.length - 1; index >= 0; index -= 1) {
          stack.push({ value: current.value[index], depth, field: `${current.field}[${index}]` });
        }
        continue;
      }
      if (typeof current.value === "object" && current.value !== null) {
        const depth = current.depth + 1;
        if (depth > maximumJsonDepth) {
          throw new Error(`${current.field} exceeds the collector JSON depth ${maximumJsonDepth}.`);
        }
        for (const [key, child] of Object.entries(current.value as Record<string, unknown>)) {
          consumedNodes += 1;
          if (consumedNodes > maximumTotalNodes) {
            throw new Error(`${current.field}.${key} exceeds the collector node budget ${maximumTotalNodes}.`);
          }
          const keyBytes = utf8Bytes(key);
          if (keyBytes > maximumStringBytes) {
            throw new Error(`${current.field}.${key} exceeds the per-string byte budget ${maximumStringBytes}.`);
          }
          consumedStringBytes += keyBytes;
          if (consumedStringBytes > maximumTotalStringBytes) {
            throw new Error(`${current.field}.${key} exceeds the collector string byte budget ${maximumTotalStringBytes}.`);
          }
          stack.push({ value: child, depth, field: `${current.field}.${key}` });
        }
      }
    }
  }

  function jsonScalar(value: unknown, field: string): string | number | boolean | null | undefined {
    if (value === undefined || value === null || typeof value === "string" || typeof value === "boolean") {
      return value;
    }
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
    throw new Error(`${field} must be a finite JSON scalar when present.`);
  }

  function projected(
    source: Record<string, unknown>,
    fields: string[],
    field: string,
  ): Record<string, unknown> {
    const result: Record<string, unknown> = {};
    for (const key of fields) {
      if (source[key] !== undefined) {
        result[key] = jsonScalar(source[key], `${field}.${key}`);
      }
    }
    return result;
  }

  function projectProblem(value: unknown, field: string): Record<string, unknown> {
    const source = object(value, field);
    const problemConfig = object(source.problemConfig, `${field}.problemConfig`);
    const programming = object(
      problemConfig.programmingProblemConfig,
      `${field}.problemConfig.programmingProblemConfig`,
    );
    return {
      ...projected(source, ["id", "problemId", "label", "title", "type", "score", "content"], field),
      problemConfig: {
        programmingProblemConfig: projected(
          programming,
          ["timeLimit", "memoryLimit"],
          `${field}.problemConfig.programmingProblemConfig`,
        ),
      },
    };
  }

  function projectProblemSetMetadata(value: unknown): Record<string, unknown> {
    const response = object(value, "GetProblemSet response");
    const problemSet = object(response.problemSet, "GetProblemSet response.problemSet");
    return {
      problemSet: projected(
        problemSet,
        ["id", "name", "startAt", "endAt"],
        "GetProblemSet response.problemSet",
      ),
    };
  }

  function projectExamMember(value: unknown, field: string): Record<string, unknown> {
    return projected(object(value, field), ["userId", "studentUserId", "userGroupId"], field);
  }

  function projectStudentUser(value: unknown, field: string): Record<string, unknown> {
    return projected(object(value, field), ["studentNumber", "name"], field);
  }

  function projectUser(value: unknown, field: string): Record<string, unknown> {
    return projected(object(value, field), ["nickname"], field);
  }

  function projectUserGroup(value: unknown, field: string): Record<string, unknown> {
    return projected(object(value, field), ["name"], field);
  }

  function projectRanking(value: unknown, field: string): Record<string, unknown> {
    const source = object(value, field);
    const rawResults = object(
      source.problemScoreByProblemSetProblemId,
      `${field}.problemScoreByProblemSetProblemId`,
    );
    const results: Record<string, unknown> = {};
    for (const [problemId, rawResult] of Object.entries(rawResults)) {
      results[problemId] = projected(
        object(rawResult, `${field}.problemScoreByProblemSetProblemId.${problemId}`),
        ["score", "acceptTime", "validSubmitCount"],
        `${field}.problemScoreByProblemSetProblemId.${problemId}`,
      );
    }
    return {
      ...projected(source, ["rank", "totalScore", "solvingTime"], field),
      user: projectExamMember(source.user, `${field}.user`),
      problemScoreByProblemSetProblemId: results,
    };
  }

  function projectSubmission(value: unknown, field: string): Record<string, unknown> {
    return projected(
      object(value, field),
      [
        "id",
        "problemType",
        "problemSetProblemId",
        "userId",
        "submitAt",
        "compiler",
        "time",
        "memory",
        "status",
        "score",
      ],
      field,
    );
  }

  function projectIndex(
    value: unknown,
    field: string,
    projector: (value: unknown, field: string) => Record<string, unknown>,
  ): Record<string, Record<string, unknown>> {
    const source = object(value ?? {}, field);
    return Object.fromEntries(
      Object.entries(source).map(([key, item]) => [key, projector(item, `${field}.${key}`)]),
    );
  }

  function projectSubmissionDetail(responseValue: unknown, submissionId: string): Record<string, unknown> {
    const root = object(responseValue, `GetSubmission ${submissionId} response`);
    const submission = object(root.submission, `GetSubmission ${submissionId}.submission`);
    const details = objectArray(
      submission.submissionDetails,
      `GetSubmission ${submissionId}.submissionDetails`,
    );
    const programs: string[] = [];
    for (const detail of details) {
      if (detail.programmingSubmissionDetail === undefined) {
        continue;
      }
      const programming = object(
        detail.programmingSubmissionDetail,
        `GetSubmission ${submissionId}.programmingSubmissionDetail`,
      );
      if (typeof programming.program === "string" && programming.program.length > 0) {
        programs.push(programming.program);
      }
    }
    if (programs.length !== 1) {
      throw new Error(`GetSubmission ${submissionId} returned ${programs.length} programming code entries.`);
    }
    const code = programs[0] as string;
    if (utf8Bytes(code) > maximumCodeBytes) {
      throw new Error(`GetSubmission ${submissionId} program exceeds ${maximumCodeBytes} UTF-8 bytes.`);
    }
    let compileLog: string | null = null;
    let testcaseJudgeResults: Record<string, unknown> = {};
    if (submission.judgeResponseContents !== undefined && submission.judgeResponseContents !== null) {
      const contents = objectArray(
        submission.judgeResponseContents,
        `GetSubmission ${submissionId}.judgeResponseContents`,
      ).filter((content) => content.programmingJudgeResponseContent !== undefined);
      if (contents.length > 1) {
        throw new Error(`GetSubmission ${submissionId} returned multiple programming judge contents.`);
      }
      const content = contents[0];
      if (content !== undefined) {
        const programming = object(
          content.programmingJudgeResponseContent,
          `GetSubmission ${submissionId}.programmingJudgeResponseContent`,
        );
        if (programming.compilationResult !== undefined && programming.compilationResult !== null) {
          const compilation = object(
            programming.compilationResult,
            `GetSubmission ${submissionId}.compilationResult`,
          );
          if (compilation.log !== undefined && compilation.log !== null) {
            if (typeof compilation.log !== "string") {
              throw new Error(`GetSubmission ${submissionId}.compilationResult.log must be a string.`);
            }
            compileLog = compilation.log.length === 0 ? null : compilation.log;
          }
        }
        if (programming.testcaseJudgeResults !== undefined && programming.testcaseJudgeResults !== null) {
          const rawCases = object(
            programming.testcaseJudgeResults,
            `GetSubmission ${submissionId}.testcaseJudgeResults`,
          );
          if (Object.keys(rawCases).length > maximumCaseResults) {
            throw new Error(`GetSubmission ${submissionId} exceeds ${maximumCaseResults} testcase results.`);
          }
          testcaseJudgeResults = Object.fromEntries(Object.entries(rawCases).map(([caseId, result]) => [
            caseId,
            projected(
              object(result, `GetSubmission ${submissionId}.testcaseJudgeResults.${caseId}`),
              ["result", "testcaseScore", "time", "memory", "error", "checkerOutput"],
              `GetSubmission ${submissionId}.testcaseJudgeResults.${caseId}`,
            ),
          ]));
        }
      }
    }
    return { submissionId, code, compileLog, testcaseJudgeResults };
  }

  function remainingRequestMilliseconds(label: string): number {
    const remaining = requestDeadlineAt - Date.now();
    if (remaining <= 0) {
      throw new Error(`${label} exceeded the collector deadline.`);
    }
    return remaining;
  }

  function withTimeout<T>(promise: Promise<T>, milliseconds: number, label: string): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const timeout = window.setTimeout(() => reject(new Error(`${label} timed out.`)), milliseconds);
      promise.then(
        (value) => {
          window.clearTimeout(timeout);
          resolve(value);
        },
        (error: unknown) => {
          window.clearTimeout(timeout);
          reject(error);
        },
      );
    });
  }

  function mergeIndex(
    target: Record<string, Record<string, unknown>>,
    source: Record<string, Record<string, unknown>>,
    field: string,
    maximumEntries: number,
  ): void {
    for (const [key, value] of Object.entries(source)) {
      const current = target[key];
      if (current !== undefined && canonicalJson(current) !== canonicalJson(value)) {
        throw new Error(`${field}.${key} changed between pagination pages.`);
      }
      if (current === undefined) {
        consumeProjectedBudget({ [key]: value }, `${field}.${key}`);
      }
      target[key] = value;
    }
    if (Object.keys(target).length > maximumEntries) {
      throw new Error(`${field} exceeds the exporter limit of ${maximumEntries}.`);
    }
  }

  async function collectNumberedPages(
    pageSize: number,
    maximumItems: number,
    fetchPage: (page: number, limit: number) => Promise<{
      items: Array<Record<string, unknown>>;
      sourceReportedCount: number | null;
      hasNext: boolean | null;
    }>,
    identity: (item: Record<string, unknown>) => string,
  ): Promise<Record<string, unknown>> {
    const items: Array<Record<string, unknown>> = [];
    const identities = new Set<string>();
    let sourceReportedCount: number | null = null;
    const maximumPages = Math.ceil(maximumItems / pageSize) + 1;

    for (let page = 0; page < maximumPages; page += 1) {
      const result = await fetchPage(page, pageSize);
      if (result.items.length > pageSize) {
        throw new Error("Numbered pagination page exceeded its requested limit.");
      }
      if (result.sourceReportedCount !== null) {
        if (
          result.sourceReportedCount > maximumItems ||
          (sourceReportedCount !== null && sourceReportedCount !== result.sourceReportedCount)
        ) {
          throw new Error("Source-reported pagination count is invalid, over limit, or changed between pages.");
        }
        sourceReportedCount = result.sourceReportedCount;
      }
      for (const item of result.items) {
        consumeProjectedBudget(item, `numbered page ${page} item`);
        const itemId = identity(item);
        if (identities.has(itemId)) {
          throw new Error(`Numbered pagination repeated item ${itemId}.`);
        }
        identities.add(itemId);
        items.push(item);
        if (items.length > maximumItems) {
          throw new Error(`Numbered pagination exceeds the exporter limit of ${maximumItems}.`);
        }
      }
      if (sourceReportedCount !== null && items.length > sourceReportedCount) {
        throw new Error("Numbered pagination exceeded the source-reported count.");
      }
      if (result.hasNext === true && sourceReportedCount !== null && items.length === sourceReportedCount) {
        throw new Error("Numbered pagination claims another page after reaching the source-reported count.");
      }
      const exhausted = result.hasNext === false ||
        (sourceReportedCount !== null && items.length === sourceReportedCount) ||
        (result.hasNext === null && sourceReportedCount === null && result.items.length < pageSize);
      if (exhausted) {
        if (sourceReportedCount !== null && items.length !== sourceReportedCount) {
          throw new Error("Numbered pagination ended before the source-reported count was collected.");
        }
        consumeProjectedBudget(
          { sourceReportedCount, observedCount: items.length, paginationExhausted: true },
          "numbered pagination metadata",
        );
        return { items, sourceReportedCount, observedCount: items.length, paginationExhausted: true };
      }
      if (result.items.length === 0) {
        throw new Error("Numbered pagination did not make progress before exhaustion.");
      }
    }
    throw new Error(`Numbered pagination cannot prove exhaustion within the exporter limit of ${maximumItems}.`);
  }

  async function collectBeforeCursorPages(
    pageSize: number,
    maximumItems: number,
    fetchPage: (before: string | undefined, limit: number) => Promise<{
      items: Array<Record<string, unknown>>;
      hasBefore: boolean;
    }>,
    identity: (item: Record<string, unknown>) => string,
  ): Promise<Record<string, unknown>> {
    const items: Array<Record<string, unknown>> = [];
    const identities = new Set<string>();
    let before: string | undefined;
    const maximumPages = Math.ceil(maximumItems / pageSize) + 1;

    for (let page = 0; page < maximumPages; page += 1) {
      const result = await fetchPage(before, pageSize);
      if (result.items.length > pageSize) {
        throw new Error("Before-cursor pagination page exceeded its requested limit.");
      }
      for (const item of result.items) {
        consumeProjectedBudget(item, `before-cursor page ${page} item`);
        const itemId = identity(item);
        if (identities.has(itemId)) {
          throw new Error(`Before-cursor pagination repeated item ${itemId}.`);
        }
        identities.add(itemId);
        items.push(item);
        if (items.length > maximumItems) {
          throw new Error(`Before-cursor pagination exceeds the exporter limit of ${maximumItems}.`);
        }
      }
      if (!result.hasBefore) {
        consumeProjectedBudget(
          { sourceReportedCount: null, observedCount: items.length, paginationExhausted: true },
          "before-cursor pagination metadata",
        );
        return { items, sourceReportedCount: null, observedCount: items.length, paginationExhausted: true };
      }
      if (result.items.length === 0) {
        throw new Error("Before-cursor pagination did not make progress before exhaustion.");
      }
      const nextBefore = identity(result.items[result.items.length - 1] as Record<string, unknown>);
      if (nextBefore === before) {
        throw new Error("Before-cursor pagination did not advance its cursor.");
      }
      before = nextBefore;
    }
    throw new Error(`Before-cursor pagination cannot prove exhaustion within the exporter limit of ${maximumItems}.`);
  }

  let webpackRequire: WebpackRequire | undefined;
  const apiCache = new Map<ApiName, ApiFunction>();

  function escapeRegExp(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }

  function captureWebpackRequire(): WebpackRequire {
    if (webpackRequire !== undefined) {
      return webpackRequire;
    }
    const page = window as typeof window & { webpackChunkbig_front?: unknown };
    const chunk = page.webpackChunkbig_front;
    if (!Array.isArray(chunk) || typeof chunk.push !== "function") {
      throw new Error("Pintia Webpack runtime is unavailable on this route.");
    }
    let captured: WebpackRequire | undefined;
    chunk.push([
      [`ascendany-v2-${crypto.randomUUID()}`],
      {},
      (required: WebpackRequire) => {
        captured = required;
      },
    ]);
    if (captured === undefined) {
      throw new Error("Pintia Webpack require capture failed.");
    }
    webpackRequire = captured;
    return captured;
  }

  function resolveApi(apiName: ApiName): ApiFunction {
    const cached = apiCache.get(apiName);
    if (cached !== undefined) {
      return cached;
    }
    const required = captureWebpackRequire();
    const apiPattern = `name:\"${apiName}\"`;
    const matches: ApiFunction[] = [];
    const modules = Object.keys(required.m ?? {}).map((rawId) => {
      const numericId = Number(rawId);
      if (!Number.isSafeInteger(numericId) || numericId < 0) {
        throw new Error(`Pintia Webpack module id is invalid: ${rawId}.`);
      }
      return { rawId, numericId };
    }).sort((left, right) => left.numericId - right.numericId);
    for (const { rawId, numericId } of modules) {
      const factory = required.m?.[rawId];
      const source = factory === undefined ? "" : Function.prototype.toString.call(factory);
      if (!source.includes(apiPattern)) {
        continue;
      }
      const localMatch = source.match(new RegExp(
        `(?:const|let|var|,)\\s*([A-Za-z_$][\\w$]*)=\\(0,[^)]*\\.createAPI\\)\\(\\{[^}]*name:\"${escapeRegExp(apiName)}\"`,
      ));
      if (localMatch?.[1] === undefined) {
        continue;
      }
      const exportMatch = source.match(new RegExp(
        `([A-Za-z_$][\\w$]*)\\s*:\\s*\\(\\)\\s*=>\\s*${escapeRegExp(localMatch[1])}\\b`,
      ));
      if (exportMatch?.[1] === undefined) {
        continue;
      }
      const candidate = required(numericId)[exportMatch[1]];
      if (typeof candidate === "function") {
        matches.push(candidate as ApiFunction);
      }
    }
    if (matches.length !== 1) {
      throw new Error(`Pintia API ${apiName} resolved ${matches.length} functions; expected exactly one.`);
    }
    const resolved = matches[0] as ApiFunction;
    apiCache.set(apiName, resolved);
    return resolved;
  }

  function callApi(apiName: ApiName, parameters: Record<string, unknown>): Promise<unknown> {
    const timeout = Math.min(apiCallTimeoutMs, remainingRequestMilliseconds(apiName));
    return withTimeout(resolveApi(apiName)(parameters, { message: false }), timeout, apiName);
  }

  async function collectProblems(problemSetId: string, maximumProblems: number): Promise<unknown> {
    const metadataResponse = projectProblemSetMetadata(await callApi("GetProblemSet", { problemSetId }));
    consumeProjectedBudget(metadataResponse, "GetProblemSet projected metadata");
    const collection = await collectNumberedPages(
      PROBLEM_PAGE_SIZE,
      maximumProblems,
      async (page, limit) => {
        const response = object(
          await callApi("ListProblemSetProblems", { problemSetId, problemType: "PROGRAMMING", page, limit }),
          "ListProblemSetProblems response",
        );
        return {
          items: objectArray(response.problemSetProblems, "problemSetProblems").map(
            (problem, index) => projectProblem(problem, `problemSetProblems[${index}]`),
          ),
          sourceReportedCount: responseCount(response),
          hasNext: responseHasNext(response),
        };
      },
      (problem) => id(problem.id, "problem.id"),
    );
    return { ...collection, metadataResponse };
  }

  async function collectRankings(
    problemSetId: string,
    maximumParticipants: number,
    maximumProblemResults: number,
  ): Promise<unknown> {
    const studentUserById: Record<string, Record<string, unknown>> = {};
    const userById: Record<string, Record<string, unknown>> = {};
    const userGroupById: Record<string, Record<string, unknown>> = {};
    const userGroupsResponse = object(
      await callApi("ListUserGroupsForProblemSet", { problemSetId }),
      "ListUserGroupsForProblemSet response",
    );
    mergeIndex(
      userGroupById,
      projectIndex(userGroupsResponse.userGroupById, "userGroupById", projectUserGroup),
      "userGroupById",
      maximumParticipants,
    );
    const collection = await collectNumberedPages(
      RANKING_PAGE_SIZE,
      maximumParticipants,
      async (page, limit) => {
        const response = object(
          await callApi("GetCommonRankings", { problemSetId, page, limit, filter: {} }),
          "GetCommonRankings response",
        );
        mergeIndex(
          studentUserById,
          projectIndex(response.studentUserById, "studentUserById", projectStudentUser),
          "studentUserById",
          maximumParticipants,
        );
        mergeIndex(
          userById,
          projectIndex(response.userById, "userById", projectUser),
          "userById",
          maximumParticipants,
        );
        const items = objectArray(response.commonRankings, "commonRankings").map(
          (ranking, index) => projectRanking(ranking, `commonRankings[${index}]`),
        );
        items.forEach((ranking, index) => {
          const results = object(
            ranking.problemScoreByProblemSetProblemId,
            `commonRankings[${index}].problemScoreByProblemSetProblemId`,
          );
          if (Object.keys(results).length > maximumProblemResults) {
            throw new Error(`Ranking problem results exceed the exporter limit of ${maximumProblemResults}.`);
          }
        });
        return {
          items,
          sourceReportedCount: responseCount(response),
          hasNext: responseHasNext(response),
        };
      },
      (ranking) => id(object(ranking.user, "ranking.user").userId, "ranking.user.userId"),
    );
    return { ...collection, studentUserById, userById, userGroupById };
  }

  async function collectSubmissions(
    problemSetId: string,
    maximumSubmissions: number,
    maximumParticipants: number,
  ): Promise<unknown> {
    const indexes = {
      examMemberByUserId: {} as Record<string, Record<string, unknown>>,
      studentUserById: {} as Record<string, Record<string, unknown>>,
      userById: {} as Record<string, Record<string, unknown>>,
    };
    const collection = await collectBeforeCursorPages(
      SUBMISSION_PAGE_SIZE,
      maximumSubmissions,
      async (before, limit) => {
        const parameters: Record<string, unknown> = { problemSetId, limit, filter: {} };
        if (before !== undefined) {
          parameters.before = before;
        }
        const response = object(await callApi("ListSubmissions", parameters), "ListSubmissions response");
        mergeIndex(
          indexes.examMemberByUserId,
          projectIndex(response.examMemberByUserId, "examMemberByUserId", projectExamMember),
          "examMemberByUserId",
          maximumParticipants,
        );
        mergeIndex(
          indexes.studentUserById,
          projectIndex(response.studentUserById, "studentUserById", projectStudentUser),
          "studentUserById",
          maximumParticipants,
        );
        mergeIndex(
          indexes.userById,
          projectIndex(response.userById, "userById", projectUser),
          "userById",
          maximumParticipants,
        );
        return {
          items: objectArray(response.submissions, "submissions").map(
            (submission, index) => projectSubmission(submission, `submissions[${index}]`),
          ),
          hasBefore: requiredBoolean(response.hasBefore, "ListSubmissions.hasBefore"),
        };
      },
      (submission) => id(submission.id, "submission.id"),
    );
    return { ...collection, indexes };
  }

  async function collectSubmissionDetails(
    submissionIds: string[],
    maximumBatchSize: number,
    concurrency: number,
  ): Promise<unknown> {
    if (submissionIds.length === 0 || submissionIds.length > maximumBatchSize) {
      throw new Error(`Submission detail batch must contain 1 to ${maximumBatchSize} ids.`);
    }
    const seen = new Set<string>();
    submissionIds.forEach((submissionId, index) => {
      id(submissionId, `submissionIds[${index}]`);
      if (seen.has(submissionId)) {
        throw new Error(`Submission detail batch repeated id ${submissionId}.`);
      }
      seen.add(submissionId);
    });
    const results = new Array<Record<string, unknown>>(submissionIds.length);
    let nextIndex = 0;
    let failure: unknown;
    const worker = async (): Promise<void> => {
      while (failure === undefined) {
        const index = nextIndex;
        nextIndex += 1;
        const submissionId = submissionIds[index];
        if (submissionId === undefined) {
          return;
        }
        try {
          if (failure !== undefined) {
            return;
          }
          const detail = projectSubmissionDetail(
            await callApi("GetSubmission", { submissionId }),
            submissionId,
          );
          consumeProjectedBudget(detail, `submission detail ${submissionId}`);
          results[index] = { submissionId, detail };
        } catch (error: unknown) {
          if (failure === undefined) {
            failure = error;
          }
          return;
        }
      }
    };
    await Promise.all(Array.from(
      { length: Math.min(concurrency, submissionIds.length) },
      () => worker(),
    ));
    if (failure !== undefined) {
      throw failure;
    }
    return { items: results };
  }

  try {
    if (location.origin !== PAGE_ORIGIN) {
      throw new Error("AscendAny collector executed on an unexpected origin.");
    }
    if (request.type !== "ASCENDANY_COLLECT_PINTIA_ROUTE_V2") {
      throw new Error("Collector request type is invalid.");
    }
    if (typeof request.problemSetId !== "string" || request.problemSetId.length === 0) {
      throw new Error("Collector problemSetId must be a non-empty string.");
    }
    const maximumProblems = positiveLimit(request.limits.maxProblems, "limits.maxProblems");
    const maximumParticipants = positiveLimit(request.limits.maxParticipants, "limits.maxParticipants");
    const maximumProblemResults = positiveLimit(
      request.limits.maxProblemResultsPerRanking,
      "limits.maxProblemResultsPerRanking",
    );
    const maximumSubmissions = positiveLimit(request.limits.maxSubmissions, "limits.maxSubmissions");
    maximumCaseResults = positiveLimit(
      request.limits.maxCaseResultsPerSubmission,
      "limits.maxCaseResultsPerSubmission",
    );
    const maximumDetailBatchSize = positiveLimit(
      request.limits.maxDetailBatchSize,
      "limits.maxDetailBatchSize",
    );
    maximumCodeBytes = positiveLimit(request.limits.maxCodeBytes, "limits.maxCodeBytes");
    maximumStringBytes = positiveLimit(request.limits.maxStringBytes, "limits.maxStringBytes");
    maximumTotalStringBytes = positiveLimit(
      request.limits.maxTotalStringBytes,
      "limits.maxTotalStringBytes",
    );
    maximumTotalNodes = positiveLimit(request.limits.maxTotalNodes, "limits.maxTotalNodes");
    maximumJsonDepth = positiveLimit(request.limits.maxJsonDepth, "limits.maxJsonDepth");
    apiCallTimeoutMs = positiveLimit(request.limits.apiCallTimeoutMs, "limits.apiCallTimeoutMs");
    const collectionTimeoutMs = positiveLimit(
      request.limits.collectionTimeoutMs,
      "limits.collectionTimeoutMs",
    );
    const detailBatchTimeoutMs = positiveLimit(
      request.limits.detailBatchTimeoutMs,
      "limits.detailBatchTimeoutMs",
    );
    const detailBatchConcurrency = positiveLimit(
      request.limits.detailBatchConcurrency,
      "limits.detailBatchConcurrency",
    );
    requestDeadlineAt = Date.now() + (
      request.collector === "submission-details" ? detailBatchTimeoutMs : collectionTimeoutMs
    );
    let result: unknown;
    if (request.collector === "problems") {
      result = await collectProblems(request.problemSetId, maximumProblems);
    } else if (request.collector === "rankings") {
      result = await collectRankings(request.problemSetId, maximumParticipants, maximumProblemResults);
    } else if (request.collector === "submissions") {
      result = await collectSubmissions(request.problemSetId, maximumSubmissions, maximumParticipants);
    } else if (request.collector === "submission-details") {
      if (!Array.isArray(request.submissionIds)) {
        throw new Error("submission-details collector requires submissionIds.");
      }
      result = await collectSubmissionDetails(
        request.submissionIds,
        maximumDetailBatchSize,
        detailBatchConcurrency,
      );
    } else {
      throw new Error("Collector name is invalid.");
    }
    return { ok: true, collector: request.collector, result };
  } catch (error: unknown) {
    return {
      ok: false,
      collector: request.collector,
      failure: collectorFailure(error),
    };
  }
}
