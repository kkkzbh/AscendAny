import { describe, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import fixture from "./fixtures/sanitized-source-shape.json";
import schema from "../../../contracts/pintia/ascendany.pintia.snapshot.v2.schema.json";
import { validateAuthoritativeSnapshotSchema } from "../src/domain/authoritative-schema";
import { buildSnapshot, sha256Utf8, validateSnapshot } from "../src/domain/normalize";
import {
  SCHEMA_SHA256,
  SNAPSHOT_SCHEMA,
  type PintiaSnapshotV2,
  type SnapshotSource,
} from "../src/domain/types";

const EXPORTED_AT = "2026-02-03T04:05:06.000Z";

function source(): SnapshotSource {
  return structuredClone(fixture) as unknown as SnapshotSource;
}

function allKeys(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.flatMap(allKeys);
  }
  if (typeof value !== "object" || value === null) {
    return [];
  }
  return Object.entries(value).flatMap(([key, child]) => [key, ...allKeys(child)]);
}

function reverseObject(value: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(value).reverse());
}

describe("Pintia snapshot v2 normalization", () => {
  it("emits the exact v2 top-level and closed normalized shapes", async () => {
    const snapshot = await buildSnapshot(source(), EXPORTED_AT);

    expect(Object.keys(snapshot)).toEqual([
      "schema",
      "schemaSha256",
      "exporter",
      "exam",
      "problems",
      "participants",
      "submissions",
      "completeness",
    ]);
    expect(snapshot.schema).toBe(SNAPSHOT_SCHEMA);
    expect(snapshot.schemaSha256).toBe(SCHEMA_SHA256);
    expect(snapshot.exam).toMatchObject({
      problemSetId: "9000000000000000001",
      title: "Sanitized Programming Practice",
      startsAt: "2026-02-01T01:00:00.000Z",
      endsAt: "2026-02-03T03:00:00.000Z",
    });
    expect(Object.keys(snapshot.problems[0] ?? {})).toEqual([
      "problemSetProblemId",
      "problemId",
      "label",
      "title",
      "type",
      "maxScore",
      "contentHtml",
      "timeLimitMs",
      "memoryLimitBytes",
    ]);
    expect(Object.keys(snapshot.submissions[0] ?? {})).toEqual([
      "submissionId",
      "problemSetProblemId",
      "userId",
      "submittedAt",
      "language",
      "compiler",
      "verdict",
      "score",
      "timeMs",
      "memoryBytes",
      "code",
      "codeSha256",
      "compileLog",
      "caseResults",
    ]);
    expect(allKeys(snapshot)).not.toContain("raw");
    expect(allKeys(snapshot)).not.toContain("rawIndexes");
  });

  it("satisfies the authoritative draft-2020-12 JSON Schema", async () => {
    const validator = new Ajv2020({ allErrors: true, strict: true });
    addFormats(validator);
    const validate = validator.compile(schema);
    const snapshot = await buildSnapshot(source(), EXPORTED_AT);

    expect(validate(snapshot), JSON.stringify(validate.errors)).toBe(true);
  });

  it("embeds the SHA-256 of the exact authoritative schema bytes", async () => {
    const bytes = await readFile(new URL(
      "../../../contracts/pintia/ascendany.pintia.snapshot.v2.schema.json",
      import.meta.url,
    ));

    expect(createHash("sha256").update(bytes).digest("hex")).toBe(SCHEMA_SHA256);
  });

  it("uses the exact ranking and programming-submission user union", async () => {
    const snapshot = await buildSnapshot(source(), EXPORTED_AT);

    expect(snapshot.participants.map((participant) => participant.userId)).toEqual(["user-alpha", "user-beta"]);
    expect(snapshot.participants.find((participant) => participant.userId === "user-alpha")?.ranking).not.toBeNull();
    expect(snapshot.participants.find((participant) => participant.userId === "user-beta")?.ranking).toBeNull();
    expect(snapshot.submissions.map((submission) => submission.userId)).toEqual(["user-alpha", "user-beta"]);
    expect(snapshot.participants.some((participant) => participant.userId === "user-filtered")).toBe(false);
    expect(snapshot.participants.find((participant) => participant.userId === "user-alpha")?.groupName).toBe(
      "Synthetic Group",
    );
  });

  it("merges canonically equal cross-collector identities independent of object-key order", async () => {
    const input = source();
    const ranking = input.rankings.items[0] as Record<string, unknown>;
    const rankingMember = ranking.user as Record<string, unknown>;
    const rankingStudent = input.rankings.studentUserById["student-alpha"] as Record<string, unknown>;
    const rankingUser = input.rankings.userById["user-alpha"] as Record<string, unknown>;
    input.submissions.indexes.examMemberByUserId["user-alpha"] = reverseObject(rankingMember);
    input.submissions.indexes.studentUserById["student-alpha"] = reverseObject(rankingStudent);
    input.submissions.indexes.userById["user-alpha"] = reverseObject(rankingUser);

    const snapshot = await buildSnapshot(input, EXPORTED_AT);
    expect(snapshot.participants.find((participant) => participant.userId === "user-alpha")).toMatchObject({
      studentNumber: "SYNTHETIC-001",
      displayName: "Learner Alpha",
      groupName: "Synthetic Group",
    });
  });

  it.each([
    ["examMemberByUserId.user-alpha", (input: SnapshotSource) => {
      const ranking = input.rankings.items[0] as Record<string, unknown>;
      (ranking.user as Record<string, unknown>).userGroupId = "group-conflict";
    }],
    ["studentUserById.student-alpha", (input: SnapshotSource) => {
      (input.rankings.studentUserById["student-alpha"] as Record<string, unknown>).name = "Conflicting Name";
    }],
    ["userById.user-alpha", (input: SnapshotSource) => {
      (input.rankings.userById["user-alpha"] as Record<string, unknown>).nickname = "Conflicting Nickname";
    }],
  ])("rejects conflicting cross-collector identity %s", async (identity, mutate) => {
    const input = source();
    mutate(input);

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      `${identity} conflicts between ranking and submission collectors`,
    );
  });

  it("preserves explicitly missing exam time bounds", async () => {
    const input = source();
    input.problems.metadata.startsAt = null;
    input.problems.metadata.endsAt = null;

    expect((await buildSnapshot(input, EXPORTED_AT)).exam).toMatchObject({
      startsAt: null,
      endsAt: null,
    });
  });

  it("rejects a participant group reference missing from the collected group index", async () => {
    const input = source();
    delete input.rankings.userGroupById["group-synthetic"];

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      "references missing user group group-synthetic",
    );
  });

  it("normalizes Pintia's ungrouped member sentinel to a null group name", async () => {
    const input = source();
    const ranking = input.rankings.items[0] as Record<string, unknown>;
    (ranking.user as Record<string, unknown>).userGroupId = "0";
    (input.submissions.indexes.examMemberByUserId["user-alpha"] as Record<string, unknown>).userGroupId = "0";

    const snapshot = await buildSnapshot(input, EXPORTED_AT);

    expect(snapshot.participants.find((participant) => participant.userId === "user-alpha")?.groupName).toBeNull();
  });

  it("rejects a lookalike ungrouped member sentinel", async () => {
    const input = source();
    const ranking = input.rankings.items[0] as Record<string, unknown>;
    (ranking.user as Record<string, unknown>).userGroupId = "00";
    (input.submissions.indexes.examMemberByUserId["user-alpha"] as Record<string, unknown>).userGroupId = "00";

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      "references missing user group 00",
    );
  });

  it("rejects a numeric ungrouped member sentinel", async () => {
    const input = source();
    const ranking = input.rankings.items[0] as Record<string, unknown>;
    (ranking.user as Record<string, unknown>).userGroupId = 0;
    (input.submissions.indexes.examMemberByUserId["user-alpha"] as Record<string, unknown>).userGroupId = 0;

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      "participant user-alpha.userGroupId must be a non-empty string.",
    );
  });

  it("maps source counts, programming filtering, hashes, case results, and units", async () => {
    const snapshot = await buildSnapshot(source(), EXPORTED_AT);
    const alpha = snapshot.submissions.find((submission) => submission.submissionId === "submission-alpha");

    expect(snapshot.completeness.problems).toEqual({
      sourceReportedCount: 2,
      observedCount: 2,
      exportedCount: 2,
      paginationExhausted: true,
    });
    expect(snapshot.completeness.submissions).toEqual({
      sourceReportedCount: 3,
      observedCount: 3,
      exportedCount: 2,
      paginationExhausted: true,
    });
    expect(alpha?.codeSha256).toBe(await sha256Utf8("SANITIZED_CODE_PLACEHOLDER_ALPHA"));
    expect(alpha?.timeMs).toBe(125);
    expect(alpha?.memoryBytes).toBe(1_048_576);
    expect(alpha?.caseResults).toEqual([
      {
        caseId: "case-1",
        verdict: "ACCEPTED",
        score: 40,
        timeMs: 125,
        memoryBytes: 1_048_576,
        message: null,
      },
    ]);
    expect(snapshot.problems[0]?.memoryLimitBytes).toBe(67_108_864);
  });

  it("maps Pintia's elapsed ranking time and derives pass state from the problem maximum", async () => {
    const input = source();
    const ranking = input.rankings.items[0] as Record<string, unknown>;
    const scoreByProblem = ranking.problemScoreByProblemSetProblemId as Record<string, Record<string, unknown>>;
    const result = scoreByProblem["problem-set-problem-alpha"];
    if (result === undefined) {
      throw new Error("Fixture must contain the alpha ranking result.");
    }
    result.score = 0;
    result.acceptTime = 5;

    const snapshot = await buildSnapshot(input, EXPORTED_AT);
    expect(snapshot.participants[0]?.ranking?.problemResults[0]).toMatchObject({
      passed: false,
      acceptTimeSeconds: 5,
    });

    result.score = 39;
    expect((await buildSnapshot(input, EXPORTED_AT)).participants[0]?.ranking?.problemResults[0]).toMatchObject({
      passed: false,
      acceptTimeSeconds: 5,
    });

    result.score = 40;
    expect((await buildSnapshot(input, EXPORTED_AT)).participants[0]?.ranking?.problemResults[0]).toMatchObject({
      passed: true,
      acceptTimeSeconds: 5,
    });

    (input.problems.items[0] as Record<string, unknown>).score = null;
    expect((await buildSnapshot(input, EXPORTED_AT)).participants[0]?.ranking?.problemResults[0]).toMatchObject({
      passed: null,
      acceptTimeSeconds: 5,
    });
  });

  it("rejects non-integer Pintia ranking times", async () => {
    const input = source();
    const ranking = input.rankings.items[0] as Record<string, unknown>;
    const scoreByProblem = ranking.problemScoreByProblemSetProblemId as Record<string, Record<string, unknown>>;
    const result = scoreByProblem["problem-set-problem-alpha"];
    if (result === undefined) {
      throw new Error("Fixture must contain the alpha ranking result.");
    }
    result.acceptTime = "2026-01-02T03:04:05.000Z";

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      "must be a safe integer greater than or equal to 0",
    );
  });

  it("rejects duplicate source submission ids", async () => {
    const input = source();
    input.submissions.items.push(structuredClone(input.submissions.items[0] as Record<string, unknown>));
    input.submissions.observedCount += 1;
    input.submissions.sourceReportedCount = input.submissions.observedCount;

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow("Duplicate source submission id");
  });

  it("rejects an empty programming-problem collection", async () => {
    const input = source();
    input.problems.items = [];
    input.problems.observedCount = 0;
    input.problems.sourceReportedCount = 0;

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow("At least one programming problem");
  });

  it("rejects metadata from a different problem set", async () => {
    const input = source();
    input.problems.metadata.problemSetId = "different-problem-set";

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      "metadata does not match the exported problem set",
    );
  });

  it.each([
    "https://pintia.cn/problem-sets/9000000000000000002/problems",
    "https://pintia.cn/problem-sets/90000000000000000010/problems",
    "https://pintia.cn/archive/problem-sets/9000000000000000001/problems",
    "https://user@pintia.cn/problem-sets/9000000000000000001/problems",
  ])("rejects an inconsistent exam source URL: %s", async (sourceUrl) => {
    const input = source();
    input.sourceUrl = sourceUrl;

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      "exam.sourceUrl must identify the exported Pintia problem set",
    );
  });

  it("rejects programming submissions whose problem page was incomplete", async () => {
    const input = source();
    const first = input.submissions.items[0] as Record<string, unknown>;
    first.problemSetProblemId = "missing-programming-problem";

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow("unexported problem");
  });

  it("rejects source-reported count mismatches", async () => {
    const input = source();
    input.rankings.sourceReportedCount = 2;

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow("sourceReportedCount");
  });

  it("rejects missing or extra code-detail ids", async () => {
    const input = source();
    delete input.submissionDetailsById["submission-beta"];

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow("submission detail id set");
  });

  it("rejects an empty program even when a detail record exists", async () => {
    const input = source();
    const detail = input.submissionDetailsById["submission-alpha"];
    if (detail === undefined) {
      throw new Error("Fixture must contain submission-alpha detail.");
    }
    detail.code = "";

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow("empty program");
  });

  it("accepts whitespace-only programming code and hashes its exact bytes", async () => {
    const input = source();
    const detail = input.submissionDetailsById["submission-alpha"];
    if (detail === undefined) {
      throw new Error("Fixture must contain submission-alpha detail.");
    }
    detail.code = " \n\t";

    const snapshot = await buildSnapshot(input, EXPORTED_AT);
    const submission = snapshot.submissions.find((item) => item.submissionId === "submission-alpha");
    expect(submission).toMatchObject({
      code: " \n\t",
      codeSha256: await sha256Utf8(" \n\t"),
    });
  });

  it.each([
    ["exam.title", (input: SnapshotSource) => { input.problems.metadata.title = " \t "; }],
    ["problems[0].title", (input: SnapshotSource) => {
      (input.problems.items[0] as Record<string, unknown>).title = " \n ";
    }],
    ["participant user-alpha.studentNumber", (input: SnapshotSource) => {
      (input.rankings.studentUserById["student-alpha"] as Record<string, unknown>).studentNumber = " \t ";
      (input.submissions.indexes.studentUserById["student-alpha"] as Record<string, unknown>).studentNumber = " \t ";
    }],
    ["submissions[0].status", (input: SnapshotSource) => {
      (input.submissions.items[0] as Record<string, unknown>).status = " \n ";
    }],
  ])("rejects source %s without a non-whitespace character", async (field, mutate) => {
    const input = source();
    mutate(input);

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(field);
  });

  it("revalidates code hashes before download", async () => {
    const snapshot = await buildSnapshot(source(), EXPORTED_AT);
    const first = snapshot.submissions[0];
    if (first === undefined) {
      throw new Error("Fixture must contain a submission.");
    }
    first.codeSha256 = "0".repeat(64);

    await expect(validateSnapshot(snapshot)).rejects.toThrow("invalid codeSha256");
  });

  it("revalidates non-empty code even when the empty-string hash matches", async () => {
    const snapshot = await buildSnapshot(source(), EXPORTED_AT);
    const first = snapshot.submissions[0];
    if (first === undefined) {
      throw new Error("Fixture must contain a submission.");
    }
    first.code = "";
    first.codeSha256 = await sha256Utf8("");

    await expect(validateSnapshot(snapshot)).rejects.toThrow("empty program");
  });

  it.each([
    ["exam.title", (snapshot: PintiaSnapshotV2) => { snapshot.exam.title = " \t "; }],
    ["problems[0].title", (snapshot: PintiaSnapshotV2) => {
      const problem = snapshot.problems[0];
      if (problem === undefined) {
        throw new Error("Fixture must contain a problem.");
      }
      problem.title = " \n ";
    }],
    ["participant user-alpha.studentNumber", (snapshot: PintiaSnapshotV2) => {
      const participant = snapshot.participants.find((item) => item.userId === "user-alpha");
      if (participant === undefined) {
        throw new Error("Fixture must contain user-alpha.");
      }
      participant.studentNumber = " \t ";
    }],
    ["submission submission-alpha.verdict", (snapshot: PintiaSnapshotV2) => {
      const submission = snapshot.submissions.find((item) => item.submissionId === "submission-alpha");
      if (submission === undefined) {
        throw new Error("Fixture must contain submission-alpha.");
      }
      submission.verdict = " \n ";
    }],
  ])("revalidates final snapshot %s as non-whitespace", async (field, mutate) => {
    const snapshot = await buildSnapshot(source(), EXPORTED_AT);
    mutate(snapshot);

    await expect(validateSnapshot(snapshot)).rejects.toThrow(field);
  });

  it("rejects every nullable Decimal path outside zero or [1e-100, 1e100]", async () => {
    const mutations: Array<[string, (input: SnapshotSource, value: number) => void]> = [
      ["problem score", (input, value) => { (input.problems.items[0] as Record<string, unknown>).score = value; }],
      ["ranking total", (input, value) => { (input.rankings.items[0] as Record<string, unknown>).totalScore = value; }],
      ["ranking problem score", (input, value) => {
        const ranking = input.rankings.items[0] as Record<string, unknown>;
        const results = ranking.problemScoreByProblemSetProblemId as Record<string, Record<string, unknown>>;
        (results["problem-set-problem-alpha"] as Record<string, unknown>).score = value;
      }],
      ["submission score", (input, value) => { (input.submissions.items[0] as Record<string, unknown>).score = value; }],
      ["case score", (input, value) => {
        const detail = input.submissionDetailsById["submission-alpha"] as unknown as Record<string, unknown>;
        const results = detail.testcaseJudgeResults as Record<string, Record<string, unknown>>;
        (results["case-1"] as Record<string, unknown>).testcaseScore = value;
      }],
    ];
    for (const [name, mutate] of mutations) {
      for (const invalid of [1e101, 1e-101]) {
        const input = source();
        mutate(input, invalid);
        await expect(buildSnapshot(input, EXPORTED_AT), `${name} ${invalid}`).rejects.toThrow(
          "must be null, zero, or a finite number between",
        );
      }
    }
  });

  it("uses the generated authoritative validator for all final Decimal paths", async () => {
    const valid = await buildSnapshot(source(), EXPORTED_AT);
    const mutations: Array<[string, (snapshot: PintiaSnapshotV2, value: number) => void]> = [
      ["exam", (snapshot, value) => { snapshot.exam.totalScore = value; }],
      ["problem", (snapshot, value) => { (snapshot.problems[0] as NonNullable<typeof snapshot.problems[0]>).maxScore = value; }],
      ["ranking total", (snapshot, value) => {
        (snapshot.participants[0] as NonNullable<typeof snapshot.participants[0]>).ranking!.totalScore = value;
      }],
      ["ranking result", (snapshot, value) => {
        (snapshot.participants[0] as NonNullable<typeof snapshot.participants[0]>).ranking!.problemResults[0]!.score = value;
      }],
      ["submission", (snapshot, value) => { (snapshot.submissions[0] as NonNullable<typeof snapshot.submissions[0]>).score = value; }],
      ["case", (snapshot, value) => { (snapshot.submissions[0] as NonNullable<typeof snapshot.submissions[0]>).caseResults[0]!.score = value; }],
    ];
    for (const [, mutate] of mutations) {
      for (const allowed of [0, 1e-100, 1e100]) {
        const snapshot = structuredClone(valid);
        mutate(snapshot, allowed);
        expect(() => validateAuthoritativeSnapshotSchema(snapshot)).not.toThrow();
      }
      for (const rejected of [1e-101, 1e101]) {
        const snapshot = structuredClone(valid);
        mutate(snapshot, rejected);
        expect(() => validateAuthoritativeSnapshotSchema(snapshot)).toThrow(
          "authoritative Pintia v2 JSON Schema",
        );
      }
    }
  });

  it("rejects a finite derived exam total above 1e100", async () => {
    const input = source();
    (input.problems.items[0] as Record<string, unknown>).score = 6e99;
    (input.problems.items[1] as Record<string, unknown>).score = 6e99;

    await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(
      "exam.totalScore exceeds the maximum decimal",
    );
  });

  it("rejects unsafe direct and rounded integer paths", async () => {
    const unsafe = Number.MAX_SAFE_INTEGER + 1;
    const mutations: Array<(input: SnapshotSource) => void> = [
      (input) => { (input.rankings.items[0] as Record<string, unknown>).rank = unsafe; },
      (input) => { (input.rankings.items[0] as Record<string, unknown>).solvingTime = unsafe; },
      (input) => {
        const ranking = input.rankings.items[0] as Record<string, unknown>;
        const result = (ranking.problemScoreByProblemSetProblemId as Record<string, Record<string, unknown>>)["problem-set-problem-alpha"]!;
        result.acceptTime = unsafe;
      },
      (input) => {
        const ranking = input.rankings.items[0] as Record<string, unknown>;
        const result = (ranking.problemScoreByProblemSetProblemId as Record<string, Record<string, unknown>>)["problem-set-problem-alpha"]!;
        result.validSubmitCount = unsafe;
      },
      (input) => {
        const problem = input.problems.items[0] as Record<string, unknown>;
        const programming = (problem.problemConfig as Record<string, Record<string, unknown>>).programmingProblemConfig!;
        programming.timeLimit = unsafe;
      },
      (input) => {
        const problem = input.problems.items[0] as Record<string, unknown>;
        const programming = (problem.problemConfig as Record<string, Record<string, unknown>>).programmingProblemConfig!;
        programming.memoryLimit = Number.MAX_SAFE_INTEGER / 1024 + 1;
      },
      (input) => { (input.submissions.items[0] as Record<string, unknown>).time = Number.MAX_SAFE_INTEGER / 1000 + 1; },
      (input) => { (input.submissions.items[0] as Record<string, unknown>).memory = unsafe; },
      (input) => {
        const detail = input.submissionDetailsById["submission-alpha"] as unknown as Record<string, unknown>;
        const result = (detail.testcaseJudgeResults as Record<string, Record<string, unknown>>)["case-1"]!;
        result.time = Number.MAX_SAFE_INTEGER / 1000 + 1;
      },
      (input) => {
        const detail = input.submissionDetailsById["submission-alpha"] as unknown as Record<string, unknown>;
        const result = (detail.testcaseJudgeResults as Record<string, Record<string, unknown>>)["case-1"]!;
        result.memory = unsafe;
      },
    ];
    for (const mutate of mutations) {
      const input = source();
      mutate(input);
      await expect(buildSnapshot(input, EXPORTED_AT)).rejects.toThrow(/safe (integer|non-negative integer)/);
    }
  });

  it("hashes normalized submissions sequentially", async () => {
    const originalDigest = crypto.subtle.digest.bind(crypto.subtle);
    let active = 0;
    let maximumActive = 0;
    const digest = vi.spyOn(crypto.subtle, "digest").mockImplementation(async (...arguments_) => {
      active += 1;
      maximumActive = Math.max(maximumActive, active);
      await Promise.resolve();
      try {
        return await originalDigest(...arguments_);
      } finally {
        active -= 1;
      }
    });
    try {
      await buildSnapshot(source(), EXPORTED_AT);
    } finally {
      digest.mockRestore();
    }
    expect(maximumActive).toBe(1);
  });
});
