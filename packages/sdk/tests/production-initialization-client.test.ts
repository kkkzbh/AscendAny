import { createHash } from "node:crypto";
import { describe, expect, it } from "vitest";
import type {
  CatalogPublicationAuthorizationResult,
  CreateConfigurationVersionResult,
  CatalogPublicationIntent,
  ExamSummary,
  ImportJob,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
} from "../src";
import {
  agentPromptConfiguration,
  agentModelConfiguration,
  agentReplyAcceptanceContent,
  assertAgentConfigurationResult,
  assertCatalogPublicationAuthorization,
  assertCanonicalDeploymentTransition,
  assertCatalogCoverage,
  buildAgentAcceptanceReceipt,
  buildAgentAutoAnalysisAcceptancePayload,
  parseAgentSSEAcceptance,
  planAgentConfiguration,
  readAllCursorPages,
  selectSnapshotImportBinding,
  strictlyUTF8BytewiseOrdered,
} from "../../../tools/v2-production-initialization-client";

describe("production Agent configuration and SSE acceptance", () => {
  it("pins both frontend-context prompts to analytics and local notes mutation tools", () => {
    const prompt = agentPromptConfiguration();
    expect(prompt).toMatchObject({
      key: "agent.prompt.default",
      kind: "prompt",
      schemaId: "ascendany.prompt.chat.v1",
      credentialRef: null,
      document: { enabledTools: ["analytics.get_self", "update_notes"] },
    });
    expect(prompt.document.systemPrompt).toEqual(expect.stringContaining(
      "ascendany.agent.frontend-context.v1",
    ));
    expect(prompt.document.systemPrompt).toEqual(expect.stringContaining(
      "ascendany.agent.auto-analysis.frontend-context.v1",
    ));
    expect(prompt.document.systemPrompt).toEqual(expect.stringContaining("context.roleSystemPrompt"));
    expect(prompt.document.systemPrompt).toEqual(expect.stringContaining("latestExamId"));
    expect(prompt.document.systemPrompt).toEqual(expect.stringContaining("notesLocked"));
    expect(prompt.document.systemPrompt).toEqual(expect.stringContaining("analytics.get_self"));
    expect(prompt.document.systemPrompt).toEqual(expect.stringContaining("update_notes"));
    expect(createHash("sha256").update(JSON.stringify(prompt.document)).digest("hex")).toBe(
      "1e7fc27df0bedfb43126579204833750e36877940d921cbb01afeb116d9d59f2",
    );
  });

  it("pins the model document and emits the exact closed acceptance receipt", () => {
    const model = agentModelConfiguration({
      modelEndpoint: "https://models.example/v1/chat/completions",
      model: "reasoner-v1",
      modelCredentialRef: "models.primary",
    });
    expect(model).toEqual({
      key: "agent.model.default",
      kind: "model_connection",
      schemaId: "ascendany.model_connection.openai_compatible.v1",
      document: {
        endpoint: "https://models.example/v1/chat/completions",
        maxCompletionTokens: 4096,
        model: "reasoner-v1",
        timeoutMilliseconds: 120000,
      },
      credentialRef: "models.primary",
    });

    const promptDocumentSha256 = createHash("sha256")
      .update(JSON.stringify(agentPromptConfiguration().document))
      .digest("hex");
    const modelDocumentSha256 = createHash("sha256")
      .update(JSON.stringify(model.document))
      .digest("hex");
    const acceptance = {
      runId: "00000000-0000-4000-8000-000000000205",
      threadId: "00000000-0000-4000-8000-000000000206",
      inputMessageId: "00000000-0000-4000-8000-000000000207",
      outputMessageId: "00000000-0000-4000-8000-000000000208",
      created: true,
      replySha256: "a".repeat(64),
      eventCount: 1,
      terminalDoneCount: 1 as const,
    };
    const receipt = buildAgentAcceptanceReceipt({
      acceptedAt: "2026-07-18T01:02:04.000Z",
      administratorAccountId: "00000000-0000-4000-8000-000000000201",
      acceptanceStudentAccountId: "00000000-0000-4000-8000-000000000202",
      acceptanceStudentUsername: "acceptance_student",
      acceptanceStudentNumber: "20260001",
      targetApplicationVersion: "0.2.0",
      targetApplicationCommit: "e".repeat(40),
      targetApplicationBuildTime: "2026-07-18T01:00:00Z",
      providerCredentialSha256: "f".repeat(64),
      promptConfiguration: {
        key: "agent.prompt.default",
        configurationId: "00000000-0000-4000-8000-000000000203",
        headRevision: 1,
        versionId: "1",
        versionNumber: 1,
        schemaId: "ascendany.prompt.chat.v1",
        documentSha256: promptDocumentSha256,
        credentialRef: null,
        state: "created",
      },
      modelConfiguration: {
        key: "agent.model.default",
        configurationId: "00000000-0000-4000-8000-000000000204",
        headRevision: 1,
        versionId: "2",
        versionNumber: 1,
        schemaId: "ascendany.model_connection.openai_compatible.v1",
        documentSha256: modelDocumentSha256,
        credentialRef: "models.primary",
        state: "created",
      },
      modelProbe: {
        configurationKey: "agent.model.default",
        configurationHeadRevision: 1,
        configurationVersion: 1,
        configurationSha256: modelDocumentSha256,
        authority: "models.example:443",
        model: "reasoner-v1",
        checkedAt: "2026-07-18T01:02:03Z",
        latencyMilliseconds: 25,
      },
      replyAcceptance: acceptance,
      autoAnalysisAcceptance: {
        ...acceptance,
        runId: "00000000-0000-4000-8000-000000000209",
        threadId: "00000000-0000-4000-8000-000000000210",
        inputMessageId: "00000000-0000-4000-8000-000000000211",
        outputMessageId: "00000000-0000-4000-8000-000000000212",
        created: false,
        replySha256: "b".repeat(64),
      },
    });

    expect(Object.keys(receipt).sort()).toEqual([
      "acceptanceStudentAccountId",
      "acceptanceStudentNumber",
      "acceptanceStudentUsername",
      "acceptedAt",
      "administratorAccountId",
      "autoAnalysisAcceptance",
      "modelConfiguration",
      "modelProbe",
      "promptConfiguration",
      "providerCredentialSha256",
      "replyAcceptance",
      "schema",
      "targetApplicationBuildTime",
      "targetApplicationCommit",
      "targetApplicationVersion",
    ]);
    expect(receipt.schema).toBe("ascendany.production-agent-acceptance-receipt.v1");
    expect(receipt.modelProbe).toMatchObject({
      configurationKey: receipt.modelConfiguration.key,
      configurationHeadRevision: receipt.modelConfiguration.headRevision,
      configurationVersion: receipt.modelConfiguration.versionNumber,
      configurationSha256: receipt.modelConfiguration.documentSha256,
    });
  });

  it("accepts exact initial and forward Agent configuration transitions", () => {
    const spec = agentPromptConfiguration();
    const serialized = JSON.stringify(spec.document);
    const documentSha256 = createHash("sha256").update(serialized).digest("hex");
    const result: CreateConfigurationVersionResult = {
      idempotent: false,
      item: {
        id: "00000000-0000-4000-8000-000000000201",
        key: spec.key,
        kind: spec.kind,
        headRevision: 1,
        activeVersion: {
          id: "1",
          number: 1,
          schemaId: spec.schemaId,
          document: spec.document,
          documentSha256,
          credentialRef: null,
          createdByAccountId: "00000000-0000-4000-8000-000000000202",
          createdBySessionId: "00000000-0000-4000-8000-000000000203",
          createdAt: "2026-07-18T01:02:03Z",
        },
        createdAt: "2026-07-18T01:02:03Z",
        updatedAt: "2026-07-18T01:02:03Z",
      },
    };
    expect(assertAgentConfigurationResult(spec, result)).toMatchObject({
      key: spec.key,
      headRevision: 1,
      versionNumber: 1,
      documentSha256,
      state: "created",
    });
    const advanced = {
      ...result,
      item: {
        ...result.item,
        headRevision: 2,
        activeVersion: { ...result.item.activeVersion!, id: "2", number: 2 },
      },
    };
    expect(assertAgentConfigurationResult(spec, advanced, 1)).toMatchObject({
      headRevision: 2,
      versionNumber: 2,
      state: "advanced",
    });
    expect(() => assertAgentConfigurationResult(spec, advanced, 0)).toThrow("head transition");
    expect(() => assertAgentConfigurationResult(spec, { ...result, idempotent: true })).toThrow(
      "head transition",
    );

	const oldDocument = { enabledTools: ["analytics.get_self"], systemPrompt: "old prompt" };
	const differing = {
		...result.item,
		activeVersion: {
			...result.item.activeVersion!,
			document: oldDocument,
			documentSha256: createHash("sha256").update(JSON.stringify(oldDocument)).digest("hex"),
		},
	};
	expect(planAgentConfiguration(spec, differing)).toEqual({ action: "publish", expectedHeadRevision: 1 });
	expect(planAgentConfiguration(spec, result.item)).toMatchObject({ action: "matched", provenance: { state: "matched" } });
	expect(planAgentConfiguration(spec, null)).toEqual({ action: "publish", expectedHeadRevision: 0 });
  });

  it("requires exactly one durable terminal done event and records its immutable identity", () => {
    const receipt = parseAgentSSEAcceptance([
      "event: meta",
      'data: {"type":"meta","summary":"prior context","provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"}',
      "",
      "event: tool_activity_start",
      'data: {"type":"tool_activity_start","activityId":"call-1","label":"analytics.get_self","status":"running"}',
      "",
      "event: tool_activity_done",
      'data: {"type":"tool_activity_done","activityId":"call-1","label":"analytics.get_self","status":"done"}',
      "",
      "event: delta",
      'data: {"type":"delta","text":"Accepted answer."}',
      "",
      "event: done",
      'data: {"type":"done","reply":"Accepted answer.","summary":"bound","runId":"00000000-0000-4000-8000-000000000205","threadId":"00000000-0000-4000-8000-000000000206","inputMessageId":"00000000-0000-4000-8000-000000000207","outputMessageId":"00000000-0000-4000-8000-000000000208","created":true,"provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"}',
      "",
      "",
    ].join("\n"));
    expect(receipt).toEqual({
      runId: "00000000-0000-4000-8000-000000000205",
      threadId: "00000000-0000-4000-8000-000000000206",
      inputMessageId: "00000000-0000-4000-8000-000000000207",
      outputMessageId: "00000000-0000-4000-8000-000000000208",
      created: true,
      replySha256: createHash("sha256").update("Accepted answer.").digest("hex"),
      eventCount: 5,
      terminalDoneCount: 1,
    });
    expect(receipt).not.toHaveProperty("reply");
  });

  it("rejects incomplete or inconsistent Agent provider metadata", () => {
    const identity = '"runId":"00000000-0000-4000-8000-000000000205","threadId":"00000000-0000-4000-8000-000000000206","inputMessageId":"00000000-0000-4000-8000-000000000207","outputMessageId":"00000000-0000-4000-8000-000000000208","created":true';
    expect(() => parseAgentSSEAcceptance(
      `event: meta\ndata: {"type":"meta","provider":"openai_compatible","model":"deepseek-chat","requestMode":"chat_completions"}\n\nevent: done\ndata: {"type":"done","reply":"Accepted.",${identity},"provider":"openai_compatible","model":"other","requestMode":"chat_completions"}\n\n`,
    )).toThrow("invalid durable identity or reply");
    expect(() => parseAgentSSEAcceptance(
      `event: done\ndata: {"type":"done","reply":"Accepted.",${identity},"provider":"openai_compatible"}\n\n`,
    )).toThrow("invalid durable identity or reply");
  });

  it("requires an ordered notes mutation when production acceptance requests it", () => {
    const initial = "# initial";
    const updated = "# initial\n\nProgress is improving.";
    const receipt = parseAgentSSEAcceptance([
      "event: tool_activity_start",
      'data: {"type":"tool_activity_start","activityId":"call-notes","label":"更新学习笔记","status":"running"}',
      "",
      "event: notes_update",
      `data: ${JSON.stringify({ type: "notes_update", mode: "replace", previous: initial, next: updated, patch: null })}`,
      "",
      "event: tool_activity_done",
      'data: {"type":"tool_activity_done","activityId":"call-notes","label":"更新学习笔记","status":"done"}',
      "",
      "event: done",
      `data: ${JSON.stringify({ type: "done", reply: "Updated.", updatedNotes: updated, runId: "00000000-0000-4000-8000-000000000205", threadId: "00000000-0000-4000-8000-000000000206", inputMessageId: "00000000-0000-4000-8000-000000000207", outputMessageId: "00000000-0000-4000-8000-000000000208", created: true })}`,
      "",
      "",
    ].join("\n"), initial);
    expect(receipt.eventCount).toBe(4);
    expect(() => parseAgentSSEAcceptance([
      "event: done",
      'data: {"type":"done","reply":"Skipped.","runId":"00000000-0000-4000-8000-000000000205","threadId":"00000000-0000-4000-8000-000000000206","inputMessageId":"00000000-0000-4000-8000-000000000207","outputMessageId":"00000000-0000-4000-8000-000000000208","created":true}',
      "",
      "",
    ].join("\n"), initial)).toThrow("exactly one durable");
  });

  it("retains created=false for an immutable automatic-analysis replay", () => {
    expect(parseAgentSSEAcceptance(
      'event: done\ndata: {"type":"done","reply":"Replay.","runId":"00000000-0000-4000-8000-000000000205","threadId":"00000000-0000-4000-8000-000000000206","inputMessageId":"00000000-0000-4000-8000-000000000207","outputMessageId":"00000000-0000-4000-8000-000000000208","created":false}\n\n',
    )).toMatchObject({ created: false, runId: "00000000-0000-4000-8000-000000000205" });
  });

  it("rejects error, empty, duplicated, and unterminated Agent streams", () => {
    expect(() => parseAgentSSEAcceptance(
      'event: error\ndata: {"type":"error","code":"failed","message":"failed"}\n\n',
    )).toThrow("error event");
    expect(() => parseAgentSSEAcceptance(
      'event: done\ndata: {"type":"done","reply":"   ","runId":"00000000-0000-4000-8000-000000000205","threadId":"00000000-0000-4000-8000-000000000206","inputMessageId":"00000000-0000-4000-8000-000000000207","outputMessageId":"00000000-0000-4000-8000-000000000208","created":true}\n\n',
    )).toThrow("invalid durable identity or reply");
    expect(() => parseAgentSSEAcceptance(
      'event: done\ndata: {"type":"done","reply":"one","runId":"00000000-0000-4000-8000-000000000205","threadId":"00000000-0000-4000-8000-000000000206","inputMessageId":"00000000-0000-4000-8000-000000000207","outputMessageId":"00000000-0000-4000-8000-000000000208","created":true}\n\nevent: done\ndata: {"type":"done","reply":"two","runId":"00000000-0000-4000-8000-000000000215","threadId":"00000000-0000-4000-8000-000000000216","inputMessageId":"00000000-0000-4000-8000-000000000217","outputMessageId":"00000000-0000-4000-8000-000000000218","created":true}\n\n',
    )).toThrow("event/data contract");
    expect(() => parseAgentSSEAcceptance(
      'event: delta\ndata: {"type":"delta","text":"partial"}\n\n',
    )).toThrow("exactly one durable nonempty terminal done");
    expect(() => parseAgentSSEAcceptance([
      "event: tool_activity_start",
      'data: {"type":"tool_activity_start","activityId":"call-1","label":"analytics.get_self","status":"running"}',
      "",
      "event: done",
      'data: {"type":"done","reply":"unfinished tool","runId":"00000000-0000-4000-8000-000000000205","threadId":"00000000-0000-4000-8000-000000000206","inputMessageId":"00000000-0000-4000-8000-000000000207","outputMessageId":"00000000-0000-4000-8000-000000000208","created":true}',
      "",
      "",
    ].join("\n"))).toThrow("invalid durable identity or reply");
  });

  it("builds one closed target-bound reply acceptance marker", () => {
    expect(JSON.parse(agentReplyAcceptanceContent({
      version: "0.2.1",
      commit: "e".repeat(40),
      buildTime: "2026-07-18T01:00:00Z",
    }))).toEqual({
      schema: "ascendany.production-agent-reply-acceptance.v1",
      instruction: "Read my current learning data, update my current notes with a concise progress summary by calling update_notes, and briefly explain my learning progress.",
      targetApplicationBuildTime: "2026-07-18T01:00:00Z",
      targetApplicationCommit: "e".repeat(40),
      targetApplicationVersion: "0.2.1",
    });
  });

  it("builds the frozen minimal auto-analysis identity from current analytics", () => {
    const values = {
      knowledge: 0.5,
      accuracy: 0.6,
      quality: 0.7,
      flexibility: 0.8,
      proficiency: 0.9,
    };
    const first = {
      examId: "00000000-0000-4000-8000-000000000211",
      snapshotId: "00000000-0000-4000-8000-000000000212",
      title: "First exam",
      eventTime: "2026-07-17T01:00:00Z",
    };
    const latest = {
      examId: "00000000-0000-4000-8000-000000000213",
      snapshotId: "00000000-0000-4000-8000-000000000214",
      title: "Latest exam",
      eventTime: "2026-07-18T01:00:00Z",
    };
    const analytics = {
      state: "ready" as const,
      headRevision: 3,
      referenceTime: "2026-07-18T02:00:00Z",
      rating: 1510,
      current: values,
      examHistory: [
        { ...first, values },
        { ...latest, values },
      ],
      ratingHistory: [
        { ...first, rank: 2, oldRating: 1500, delta: 5, newRating: 1505, seed: 1500, performance: 1510 },
        { ...latest, rank: 1, oldRating: 1505, delta: 5, newRating: 1510, seed: 1505, performance: 1520 },
      ],
    };

    expect(buildAgentAutoAnalysisAcceptancePayload(analytics)).toEqual({
      roleId: "xiaoD",
      latestExamId: latest.examId,
    });
    expect(() => buildAgentAutoAnalysisAcceptancePayload({
      state: "no_observations",
      headRevision: 3,
    })).toThrow("requires aligned student exam analytics");
  });
});

const publicationIntent: CatalogPublicationIntent = {
  schema: "ascendany.knowledge_catalog.publication-request.v1",
  expectedConfigurationHeadRevision: 0,
  expectedAnalyticsGenerationId: "17",
  expectedAnalyticsHeadRevision: 9,
  expectedInputManifestSha256: "a".repeat(64),
  expectedCurrentModelHeadRevision: 1,
  expectedCurrentModelArtifactSha256: "b".repeat(64),
  targetCatalogSha256: "c".repeat(64),
  targetModelArtifactSha256: "d".repeat(64),
  targetApplicationVersion: "0.2.0",
  targetApplicationCommit: "e".repeat(40),
  targetApplicationBuildTime: "2026-07-14T04:00:00Z",
};

function publicationAuthorization(
  overrides: Partial<CatalogPublicationAuthorizationResult> = {},
): CatalogPublicationAuthorizationResult {
  const authorizationId = "00000000-0000-4000-8000-000000000100";
  return {
    authorizationId,
    expiresAt: "2026-07-14T05:15:00Z",
    publicationRequest: { authorizationId, ...publicationIntent },
    ...overrides,
  };
}

describe("production initialization catalog publication authorization", () => {
  it("accepts the exact intent and access-token expiry", () => {
    expect(assertCatalogPublicationAuthorization(
      publicationIntent,
      "2026-07-14T05:15:00Z",
      publicationAuthorization(),
      Date.parse("2026-07-14T05:00:00Z"),
    )).toEqual(publicationAuthorization().publicationRequest);
  });

  it("rejects changed intent, identity, expiry, and insufficient lifetime", () => {
    expect(() => assertCatalogPublicationAuthorization(
      publicationIntent,
      "2026-07-14T05:15:00Z",
      publicationAuthorization({
        publicationRequest: {
          ...publicationAuthorization().publicationRequest,
          expectedAnalyticsHeadRevision: 10,
        },
      }),
      Date.parse("2026-07-14T05:00:00Z"),
    )).toThrow("differs from the requested release intent");

    expect(() => assertCatalogPublicationAuthorization(
      publicationIntent,
      "2026-07-14T05:15:01Z",
      publicationAuthorization(),
      Date.parse("2026-07-14T05:00:00Z"),
    )).toThrow("access-token-bound response contract");

    expect(() => assertCatalogPublicationAuthorization(
      publicationIntent,
      "2026-07-14T05:15:00Z",
      publicationAuthorization({
        publicationRequest: {
          ...publicationAuthorization().publicationRequest,
          authorizationId: "00000000-0000-4000-8000-000000000101",
        },
      }),
      Date.parse("2026-07-14T05:00:00Z"),
    )).toThrow("access-token-bound response contract");

    expect(() => assertCatalogPublicationAuthorization(
      publicationIntent,
      "2026-07-14T05:15:00Z",
      publicationAuthorization(),
      Date.parse("2026-07-14T05:11:00.001Z"),
    )).toThrow("access-token-bound response contract");
  });
});

const snapshotSHA256 = "a".repeat(64);
const factSHA256 = "b".repeat(64);
const importJobID = "00000000-0000-4000-8000-000000000001";
const examID = "00000000-0000-4000-8000-000000000002";
const snapshotID = "00000000-0000-4000-8000-000000000003";

describe("production initialization model publication transition", () => {
  const modelSHA256 = "d".repeat(64);

  it("requires initial catalog publication to authorize H1 to H2", () => {
    expect(() => assertCanonicalDeploymentTransition({
      deploymentKind: "initial",
      expectedModelHeadRevision: 2,
      expectedCurrentModelHeadRevision: 1,
      expectedModelSHA256: modelSHA256,
      expectedCurrentModelSHA256: modelSHA256,
      expectedCatalogHeadRevision: 1,
      expectedCurrentCatalogHeadRevision: 0,
      expectedCatalogSHA256: "e".repeat(64),
      expectedCurrentCatalogSHA256: null,
    })).not.toThrow();
  });

  it("rejects initial verification without the publication-authorized activation", () => {
    expect(() => assertCanonicalDeploymentTransition({
      deploymentKind: "initial",
      expectedModelHeadRevision: 1,
      expectedCurrentModelHeadRevision: 1,
      expectedModelSHA256: modelSHA256,
      expectedCurrentModelSHA256: modelSHA256,
      expectedCatalogHeadRevision: 1,
      expectedCurrentCatalogHeadRevision: 0,
      expectedCatalogSHA256: "e".repeat(64),
      expectedCurrentCatalogSHA256: null,
    })).toThrow("publication-authorized model-head transition");
  });
});

function importJob(overrides: Partial<ImportJob> = {}): ImportJob {
  return {
    id: importJobID,
    artifactSha256: snapshotSHA256,
    status: "succeeded",
    stage: "completed",
    createdAt: "2026-07-13T00:00:00Z",
    updatedAt: "2026-07-13T00:00:01Z",
    examId: examID,
    snapshotId: snapshotID,
    error: null,
    ...overrides,
  };
}

function exam(overrides: Partial<ExamSummary> = {}): ExamSummary {
  return {
    id: examID,
    snapshotId: snapshotID,
    platform: "pintia",
    problemSetId: "acceptance-set",
    title: "Acceptance exam",
    sourceUrl: "https://pintia.cn/problem-sets/acceptance-set",
    startsAt: null,
    endsAt: null,
    totalScore: "100",
    problemCount: 1,
    participantCount: 1,
    rankingCount: 1,
    submissionCount: 1,
    snapshotSequence: 1,
    headRevision: 1,
    exporterVersion: "2.0.0",
    exportedAt: "2026-07-13T00:00:00Z",
    updatedAt: "2026-07-13T00:00:01Z",
    ...overrides,
  };
}

describe("production initialization pagination", () => {
  it("collects every page with exact cursor progress", async () => {
    const cursors: Array<string | undefined> = [];
    const items = await readAllCursorPages(
      "test page",
      async (cursor) => {
        cursors.push(cursor);
        return cursor === undefined
          ? { items: [{ id: "first" }], nextCursor: "first" }
          : { items: [{ id: "second" }], nextCursor: null };
      },
      (item) => item.id,
      (identity) => identity.length > 0,
      (cursor) => cursor.length > 0,
      (item) => item.id,
      0,
    );

    expect(items.map((item) => item.id)).toEqual(["first", "second"]);
    expect(cursors).toEqual([undefined, "first"]);
  });

  it("rejects duplicate items, cursor cycles, empty progress, and wrong terminal-item cursors", async () => {
    await expect(readAllCursorPages(
      "duplicate page",
      async (cursor) => cursor === undefined
        ? { items: [{ id: "same" }], nextCursor: "same" }
        : { items: [{ id: "same" }], nextCursor: null },
      (item) => item.id,
      () => true,
      () => true,
      (item) => item.id,
      0,
    )).rejects.toThrow("duplicate item identity");

    let cyclePage = 0;
    await expect(readAllCursorPages(
      "cycle page",
      async () => {
        cyclePage += 1;
        return {
          items: [{ id: `item-${cyclePage}` }],
          nextCursor: cyclePage === 1 ? "cursor-a" : cyclePage === 2 ? "cursor-b" : "cursor-a",
        };
      },
      (item) => item.id,
      () => true,
      () => true,
      null,
      0,
    )).rejects.toThrow("non-progressing cursor");

    await expect(readAllCursorPages(
      "empty page",
      async (cursor) => cursor === undefined
        ? { items: [{ id: "first" }], nextCursor: "first" }
        : { items: [], nextCursor: null },
      (item) => item.id,
      () => true,
      () => true,
      (item) => item.id,
      0,
    )).rejects.toThrow("empty-progress page");

    await expect(readAllCursorPages(
      "wrong cursor page",
      async () => ({ items: [{ id: "first" }], nextCursor: "different" }),
      (item) => item.id,
      () => true,
      () => true,
      (item) => item.id,
      0,
    )).rejects.toThrow("non-progressing cursor");
  });

  it("uses the server's UTF-8 C ordering for Unicode managed-student identities", () => {
    expect(strictlyUTF8BytewiseOrdered(["\uE000", "\u{10000}"])).toBe(true);
    expect(strictlyUTF8BytewiseOrdered(["\u{10000}", "\uE000"])).toBe(false);
  });
});

describe("production initialization snapshot provenance", () => {
  it("accepts unrelated forward exams while selecting the exact snapshot binding", () => {
    const unrelatedExam = exam({
      id: "00000000-0000-4000-8000-000000000010",
      snapshotId: "00000000-0000-4000-8000-000000000011",
      problemSetId: "another-set",
      snapshotSequence: 7,
    });

    expect(selectSnapshotImportBinding(
      [importJob()],
      [unrelatedExam, exam({ snapshotSequence: 3 })],
      "forward",
      snapshotSHA256,
      "pintia",
      "acceptance-set",
    )).toEqual({ importJob: importJob(), exam: exam({ snapshotSequence: 3 }) });
  });

  it("rejects duplicate succeeded jobs, mismatched identifiers, and non-fresh initial exams", () => {
    expect(() => selectSnapshotImportBinding(
      [importJob(), importJob({ id: "00000000-0000-4000-8000-000000000012" })],
      [exam()],
      "forward",
      snapshotSHA256,
      "pintia",
      "acceptance-set",
    )).toThrow("one exact succeeded import");

    expect(() => selectSnapshotImportBinding(
      [importJob({ snapshotId: "00000000-0000-4000-8000-000000000013" })],
      [exam()],
      "forward",
      snapshotSHA256,
      "pintia",
      "acceptance-set",
    )).toThrow("identifiers differ");

    expect(() => selectSnapshotImportBinding(
      [importJob()],
      [exam({ snapshotSequence: 2 })],
      "initial",
      snapshotSHA256,
      "pintia",
      "acceptance-set",
    )).toThrow("sequence-1");

    expect(() => selectSnapshotImportBinding(
      [importJob(), importJob({
        id: "00000000-0000-4000-8000-000000000014",
        artifactSha256: "e".repeat(64),
        status: "failed",
        examId: null,
        snapshotId: null,
      })],
      [exam()],
      "initial",
      snapshotSHA256,
      "pintia",
      "acceptance-set",
    )).toThrow("exactly one import");
  });
});

describe("production initialization catalog coverage", () => {
  const sourceProblemKey = "pintia:problem:2:p1";
  const problemKey = `${sourceProblemKey}:${factSHA256}`;
  const catalog: RecommendationKnowledgeCatalogV1 = {
    taxonomyId: "recommendation.taxonomy.test.v1",
    knowledgePoints: [],
    problemAssignments: [{
      platform: "pintia",
      problemId: "p1",
      problemFactSha256: factSHA256,
      knowledge: [],
    }],
  };
  const review: RecommendationReviewContext = {
    analyticsGenerationId: "1",
    analyticsHeadRevision: 1,
    inputManifestSha256: "c".repeat(64),
    problems: [{
      problemKey,
      sourceProblemKey,
      platform: "pintia",
      problemId: "p1",
      problemFactSha256: factSHA256,
      title: "Problem 1",
      sourceProblemSets: [{
        problemSetId: "global-source-set",
        sourceUrl: "https://pintia.cn/problem-sets/global-source-set",
      }],
    }],
  };

  it("accepts exact global assignment coverage independent of the acceptance problem set", () => {
    expect(() => assertCatalogCoverage(catalog, review)).not.toThrow();
  });

  it("rejects missing and dangling assignments", () => {
    expect(() => assertCatalogCoverage({ ...catalog, problemAssignments: [] }, review)).toThrow(
      "assignment set differs",
    );
    expect(() => assertCatalogCoverage({
      ...catalog,
      problemAssignments: [
        ...catalog.problemAssignments,
        {
          platform: "pintia",
          problemId: "p2",
          problemFactSha256: "d".repeat(64),
          knowledge: [],
        },
      ],
    }, review)).toThrow("assignment set differs");
  });

  it("rejects a non-Pintia runtime assignment platform", () => {
    expect(() => assertCatalogCoverage({
      ...catalog,
      problemAssignments: [{
        ...catalog.problemAssignments[0]!,
        platform: "other" as "pintia",
      }],
    }, review)).toThrow("non-Pintia");
  });
});
