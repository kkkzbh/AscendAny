(function () {
  const READY_EVENT = "ASCENDANY_PINTIA_BRIDGE_READY";
  const REQUEST_EVENT = "ASCENDANY_PINTIA_EXPORT_REQUEST";
  const RESPONSE_EVENT = "ASCENDANY_PINTIA_EXPORT_RESPONSE";
  const EXPORTER_VERSION = "0.1.0";

  function emitReady() {
    window.dispatchEvent(new CustomEvent(READY_EVENT));
  }

  function captureWebpackRequire() {
    if (typeof window.__ASCENDANY_PINTIA_REQUIRE__ === "function") {
      return window.__ASCENDANY_PINTIA_REQUIRE__;
    }

    const chunk = window.webpackChunkbig_front;
    if (!Array.isArray(chunk) || typeof chunk.push !== "function") {
      throw new Error("Pintia Webpack runtime was not found. Please wait until the page finishes loading.");
    }

    const marker = `ascendany-pintia-${Date.now()}`;
    chunk.push([
      [marker],
      {},
      function (require) {
        window.__ASCENDANY_PINTIA_REQUIRE__ = require;
      }
    ]);

    if (typeof window.__ASCENDANY_PINTIA_REQUIRE__ !== "function") {
      throw new Error("Failed to capture Pintia Webpack require.");
    }
    return window.__ASCENDANY_PINTIA_REQUIRE__;
  }

  function moduleSource(require, moduleId) {
    const factory = require.m && require.m[moduleId];
    return factory ? Function.prototype.toString.call(factory) : "";
  }

  function findModule(require, predicates) {
    const modules = require.m || {};
    for (const id of Object.keys(modules)) {
      const source = moduleSource(require, id);
      if (!predicates.every((predicate) => source.includes(predicate))) {
        continue;
      }
      try {
        return { id, exports: require(Number(id)), source };
      } catch (_error) {
        return null;
      }
    }
    return null;
  }

  function firstFunction(exportsObject, keys) {
    for (const key of keys) {
      if (exportsObject && typeof exportsObject[key] === "function") {
        return exportsObject[key];
      }
    }
    return null;
  }

  function escapeRegExp(value) {
    return String(value).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }

  function findApiFunction(require, apiName) {
    const modules = require.m || {};
    const apiNamePattern = `name:"${apiName}"`;
    for (const id of Object.keys(modules)) {
      const source = moduleSource(require, id);
      if (!source.includes(apiNamePattern)) {
        continue;
      }

      const localMatch = source.match(
        new RegExp(`(?:const|let|var|,)\\s*([A-Za-z_$][\\w$]*)=\\(0,[^)]*\\.createAPI\\)\\(\\{[^}]*name:"${escapeRegExp(apiName)}"`)
      );
      if (!localMatch) {
        continue;
      }

      const localName = localMatch[1];
      const exportMatch = source.match(new RegExp(`([A-Za-z_$][\\w$]*)\\s*:\\s*\\(\\)\\s*=>\\s*${escapeRegExp(localName)}\\b`));
      if (!exportMatch) {
        continue;
      }

      try {
        const exportsObject = require(Number(id));
        const fn = exportsObject[exportMatch[1]];
        if (typeof fn === "function") {
          return fn;
        }
      } catch (_error) {
        return null;
      }
    }
    return null;
  }

  function getApi(require) {
    const api = {};

    api.paperSummary = findApiFunction(require, "GetProblemSetPaperSummary");
    api.previewProblems = findApiFunction(require, "GetPreviewProblemsByType");
    api.problemSetProblems = findApiFunction(require, "ListProblemSetProblems");
    api.commonRankings = findApiFunction(require, "GetCommonRankings");
    api.getSubmission = findApiFunction(require, "GetSubmission");
    api.listSubmissions = findApiFunction(require, "ListSubmissions");

    if (!api.paperSummary || !api.previewProblems || !api.problemSetProblems) {
      try {
      const problems = require(29872);
        api.paperSummary = api.paperSummary || firstFunction(problems, ["$8"]);
        api.previewProblems = api.previewProblems || firstFunction(problems, ["Wu"]);
        api.problemSetProblems = api.problemSetProblems || firstFunction(problems, ["zf"]);
      } catch (_error) {
      const found = findModule(require, ["problem-set-paper-summary", "preview/problems"]);
      if (found) {
          api.paperSummary = api.paperSummary || firstFunction(found.exports, Object.keys(found.exports));
          api.previewProblems = api.previewProblems || firstFunction(found.exports, Object.keys(found.exports));
        }
      }
    }

    if (!api.commonRankings) {
      try {
      const rankings = require(21640);
        api.commonRankings = firstFunction(rankings, ["Ol"]);
      } catch (_error) {
      const found = findModule(require, ["CommonRankings"]);
      if (found) {
        api.commonRankings = firstFunction(found.exports, Object.keys(found.exports));
        }
      }
    }

    if (!api.getSubmission || !api.listSubmissions) {
      try {
      const submissions = require(34514);
        api.getSubmission = api.getSubmission || firstFunction(submissions, ["D5"]);
        api.listSubmissions = api.listSubmissions || firstFunction(submissions, ["pU"]);
      } catch (_error) {
      const found = findModule(require, ["GetSubmission", "ListSubmissions"]);
      if (found) {
          api.getSubmission = api.getSubmission || firstFunction(found.exports, Object.keys(found.exports));
          api.listSubmissions = api.listSubmissions || firstFunction(found.exports, Object.keys(found.exports));
        }
      }
    }

    return api;
  }

  function normalizeProblemSetId(input) {
    const value = String(input || "");
    if (/^\d+$/.test(value)) {
      return value;
    }
    const match = location.href.match(/\/problem-sets\/(\d+)/);
    return match ? match[1] : null;
  }

  function normalizeList(value, keys) {
    if (Array.isArray(value)) {
      return value;
    }
    for (const key of keys) {
      if (value && Array.isArray(value[key])) {
        return value[key];
      }
    }
    return [];
  }

  async function tryCall(fn, variants) {
    if (typeof fn !== "function") {
      return null;
    }
    let lastError = null;
    for (const params of variants) {
      try {
        return await fn(params, { message: false });
      } catch (error) {
        lastError = error;
      }
    }
    if (lastError) {
      throw lastError;
    }
    return null;
  }

  async function sha256Text(value) {
    const bytes = new TextEncoder().encode(String(value || ""));
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    return Array.from(new Uint8Array(digest))
      .map((byte) => byte.toString(16).padStart(2, "0"))
      .join("");
  }

  function delay(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  async function mapLimit(items, limit, worker) {
    const results = new Array(items.length);
    let index = 0;

    async function run() {
      while (index < items.length) {
        const current = index;
        index += 1;
        results[current] = await worker(items[current], current);
      }
    }

    await Promise.all(Array.from({ length: Math.min(limit, items.length) }, () => run()));
    return results;
  }

  function withTimeout(promise, ms, label) {
    return Promise.race([
      promise,
      new Promise((_, reject) => {
        setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms);
      })
    ]);
  }

  async function collectProblemSummary(api, problemSetId) {
    const response = await tryCall(api.paperSummary, [
      { problemSetId },
      { problem_set_id: problemSetId }
    ]);
    return response || {};
  }

  async function collectProblems(api, problemSetId) {
    const candidates = [];
    if (api.problemSetProblems) {
      candidates.push(
        tryCall(api.problemSetProblems, [
          { problemSetId, page: 0, limit: 500 },
          { problemSetId, problemType: "PROGRAMMING", page: 0, limit: 500 }
        ])
      );
    }
    if (api.previewProblems) {
      candidates.push(
        tryCall(api.previewProblems, [
          { problemSetId, problemType: "PROGRAMMING", page: 0, limit: 500 },
          { problemSetId, problem_type: "PROGRAMMING", page: 0, limit: 500 }
        ])
      );
    }

    for (const candidate of candidates) {
      const result = await candidate.catch(() => null);
      const problems = normalizeList(result, ["problemSetProblems", "problems", "items"]);
      if (problems.length > 0) {
        return problems;
      }
    }
    return [];
  }

  async function collectRankings(api, problemSetId) {
    return (
      (await tryCall(api.commonRankings, [
        { problemSetId, page: 0, limit: 1000, filter: {} },
        { problemSetId, page: 0, limit: 1000 }
      ]).catch(() => null)) || {}
    );
  }

  async function collectSubmissionList(api, problemSetId) {
    if (typeof api.listSubmissions !== "function") {
      throw new Error("Pintia submission list API was not resolved on the submissions route.");
    }

    const submissions = [];
    const indexes = {
      problemSetProblemById: {},
      examMemberByUserId: {},
      userById: {},
      studentUserById: {},
      showDetailBySubmissionId: {}
    };
    let before;

    for (let page = 0; page < 1000; page += 1) {
      const response = await api.listSubmissions(
        {
          problemSetId,
          limit: 200,
          before,
          filter: {}
        },
        { message: false }
      );
      const pageItems = normalizeList(response, ["submissions", "items"]);
      submissions.push(...pageItems);

      for (const key of Object.keys(indexes)) {
        Object.assign(indexes[key], response && response[key] ? response[key] : {});
      }

      if (!response || !response.hasBefore || pageItems.length === 0) {
        break;
      }
      before = pageItems[pageItems.length - 1].id;
    }

    return { submissions, indexes };
  }

  function extractCode(detail) {
    const submission = detail && detail.submission ? detail.submission : detail;
    const details = submission && Array.isArray(submission.submissionDetails) ? submission.submissionDetails : [];
    for (const item of details) {
      const programming = item && item.programmingSubmissionDetail;
      if (programming && typeof programming.program === "string") {
        return programming.program;
      }
    }
    return "";
  }

  function extractJudgeArtifacts(detail) {
    const submission = detail && detail.submission ? detail.submission : detail;
    const contents =
      submission && Array.isArray(submission.judgeResponseContents) ? submission.judgeResponseContents : [];
    for (const item of contents) {
      const programming = item && item.programmingJudgeResponseContent;
      if (!programming) {
        continue;
      }
      return {
        compileLog:
          programming.compilationResult && typeof programming.compilationResult.log === "string"
            ? programming.compilationResult.log
            : "",
        caseResults: programming.testcaseJudgeResults || {}
      };
    }
    return { compileLog: "", caseResults: {} };
  }

  function resolveStudentForSubmission(detail, listItem, indexes) {
    if (detail && detail.studentUser) {
      return detail.studentUser;
    }
    const examMember = indexes.examMemberByUserId && indexes.examMemberByUserId[listItem.userId];
    if (!examMember) {
      return null;
    }
    return indexes.studentUserById ? indexes.studentUserById[examMember.studentUserId] || null : null;
  }

  async function getSubmissionDetail(api, submissionId) {
    let lastError = null;
    for (let attempt = 0; attempt < 2; attempt += 1) {
      try {
        return await withTimeout(
          api.getSubmission({ submissionId }, { message: false }),
          20_000,
          `GetSubmission ${submissionId}`
        );
      } catch (error) {
        lastError = error;
      }
    }
    throw lastError || new Error(`Failed to get submission ${submissionId}`);
  }

  async function collectSubmissionDetails(api, submissionList, indexes, concurrency, requestDelayMs) {
    if (typeof api.getSubmission !== "function") {
      throw new Error("Pintia submission detail API was not resolved on the submissions route.");
    }

    const actualConcurrency = concurrency || 4;
    const actualDelay = requestDelayMs || 800;
    return mapLimit(submissionList, actualConcurrency, async (submission, index) => {
      if (actualDelay > 0) {
        await delay((index % actualConcurrency) * Math.min(500, actualDelay));
      }
      const detail = await getSubmissionDetail(api, submission.id);
      const code = extractCode(detail);
      const judgeArtifacts = extractJudgeArtifacts(detail);
      const student = resolveStudentForSubmission(detail, submission, indexes) || {};
      const row = {
        submissionId: submission.id,
        problemSetProblemId: submission.problemSetProblemId,
        userId: submission.userId,
        studentNo: student.studentNumber || null,
        name: student.name || null,
        submittedAt: submission.submitAt,
        language: submission.compiler,
        status: submission.status,
        score: submission.score,
        timeMs: typeof submission.time === "number" ? Math.round(submission.time * 1000) : null,
        memoryKb: typeof submission.memory === "number" ? Math.round(submission.memory / 1024) : null,
        compiler: submission.compiler,
        code,
        codeSha256: code ? await sha256Text(code) : "",
        caseResults: judgeArtifacts.caseResults,
        compileLog: judgeArtifacts.compileLog,
        raw: {
          listItem: submission
        }
      };
      if (actualDelay > 0) {
        await delay(actualDelay);
      }
      return row;
    });
  }

  async function collectProblemRoute(problemSetId, api) {
    if (!api.paperSummary && !api.problemSetProblems && !api.previewProblems) {
      throw new Error("Pintia problem APIs were not resolved on the problems route.");
    }
    return {
      summary: api.paperSummary ? await collectProblemSummary(api, problemSetId) : {},
      problems: await collectProblems(api, problemSetId)
    };
  }

  async function collectRankingRoute(problemSetId, api) {
    if (!api.commonRankings) {
      throw new Error("Pintia ranking API was not resolved on the rankings route.");
    }
    return {
      rankingResponse: await collectRankings(api, problemSetId)
    };
  }

  async function collectSubmissionRoute(problemSetId, api) {
    const { submissions, indexes } = await collectSubmissionList(api, problemSetId);
    return {
      submissionList: submissions,
      submissionIndexes: indexes
    };
  }

  async function collectSubmissionDetailsRoute(api, payload) {
    const submissions = Array.isArray(payload.submissions) ? payload.submissions : [];
    const indexes = payload.indexes || {};
    return {
      submissionDetails: await collectSubmissionDetails(
        api,
        submissions,
        indexes,
        payload.concurrency || 4,
        payload.requestDelayMs || 800
      )
    };
  }

  async function collectRoute(collector, problemSetIdInput, payload) {
    const problemSetId = normalizeProblemSetId(problemSetIdInput);
    if (!problemSetId) {
      throw new Error("Cannot resolve Pintia problemSetId from current page.");
    }

    const require = captureWebpackRequire();
    const api = getApi(require);
    if (collector === "problems") {
      return collectProblemRoute(problemSetId, api);
    }
    if (collector === "rankings") {
      return collectRankingRoute(problemSetId, api);
    }
    if (collector === "submissions") {
      return collectSubmissionRoute(problemSetId, api);
    }
    if (collector === "submission-details") {
      return collectSubmissionDetailsRoute(api, payload || {});
    }
    throw new Error(`Unknown Pintia collector: ${collector}`);
  }

  window.addEventListener("message", async (event) => {
    if (event.source !== window) {
      return;
    }
    const data = event.data || {};
    if (data.type !== REQUEST_EVENT) {
      return;
    }

    try {
      const payload = await collectRoute(data.collector, data.problemSetId, data.payload);
      window.postMessage(
        {
          type: RESPONSE_EVENT,
          requestId: data.requestId,
          payload: { ok: true, collector: data.collector, result: payload, exporterVersion: EXPORTER_VERSION }
        },
        "*"
      );
    } catch (error) {
      window.postMessage(
        {
          type: RESPONSE_EVENT,
          requestId: data.requestId,
          payload: { ok: false, collector: data.collector, error: error && error.message ? error.message : String(error) }
        },
        "*"
      );
    }
  });

  emitReady();
})();
