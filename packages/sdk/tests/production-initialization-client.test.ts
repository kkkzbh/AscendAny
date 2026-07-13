import { describe, expect, it } from "vitest";
import type {
  CatalogPublicationAuthorizationResult,
  CatalogPublicationIntent,
  ExamSummary,
  ImportJob,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
} from "../src";
import {
  assertCatalogPublicationAuthorization,
  assertCanonicalDeploymentTransition,
  assertCatalogCoverage,
  readAllCursorPages,
  selectSnapshotImportBinding,
  strictlyUTF8BytewiseOrdered,
} from "../../../tools/v2-production-initialization-client";

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
