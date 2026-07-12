import { describe, expect, it } from "vitest";
import {
  MAX_CASE_RESULTS_PER_SUBMISSION,
  MAX_CODE_BYTES,
  MAX_JSON_DEPTH,
  MAX_PARTICIPANTS,
  MAX_PROBLEMS,
  MAX_PROBLEM_RESULTS_PER_RANKING,
  MAX_SNAPSHOT_BYTES,
  MAX_STRING_BYTES,
  MAX_SUBMISSIONS,
  MAX_TOTAL_NODES,
  MAX_TOTAL_STRING_BYTES,
} from "../src/domain/limits";
import { buildSnapshot } from "../src/domain/normalize";
import {
  scanOperationalJson,
  SNAPSHOT_TYPED_MAX_DEPTH,
  validateSnapshotOperationalLimits,
  validateSerializedSnapshotBytes,
  validateSourceOperationalLimits,
} from "../src/domain/operational-preflight";
import type { JsonObject, PintiaSnapshotV2, SnapshotSource } from "../src/domain/types";
import fixture from "./fixtures/sanitized-source-shape.json";

const EXPORTED_AT = "2026-02-03T04:05:06.000Z";

function source(): SnapshotSource {
  return structuredClone(fixture) as unknown as SnapshotSource;
}

function repeatedReferences<T>(value: T, length: number): T[] {
  return Array.from({ length }, () => value);
}

describe("Go-aligned operational preflight", () => {
  it.each([
    ["problems", MAX_PROBLEMS],
    ["rankings", MAX_PARTICIPANTS],
    ["submissions", MAX_SUBMISSIONS],
  ] as const)("accepts %s at its limit and rejects limit + 1", (collection, maximum) => {
    const input = source();
    if (collection === "problems") {
      input.problems.items = repeatedReferences({}, maximum);
    } else if (collection === "rankings") {
      input.rankings.items = repeatedReferences({ problemScoreByProblemSetProblemId: {} }, maximum);
    } else {
      input.submissions.items = repeatedReferences({}, maximum);
    }
    expect(() => validateSourceOperationalLimits(input)).not.toThrow();

    if (collection === "problems") {
      input.problems.items.push(input.problems.items[0] as JsonObject);
    } else if (collection === "rankings") {
      input.rankings.items.push(input.rankings.items[0] as JsonObject);
    } else {
      input.submissions.items.push(input.submissions.items[0] as JsonObject);
    }
    expect(() => validateSourceOperationalLimits(input)).toThrow(`maximum is ${maximum}`);
  });

  it("enforces the source code byte cap at the exact boundary", () => {
    const input = source();
    const detail = input.submissionDetailsById["submission-alpha"]!;
    detail.code = "x".repeat(MAX_CODE_BYTES);
    expect(() => validateSourceOperationalLimits(input)).not.toThrow();

    detail.code += "x";
    expect(() => validateSourceOperationalLimits(input)).toThrow(
      `code exceeds ${MAX_CODE_BYTES} UTF-8 bytes`,
    );
  });

  it("rejects ranking results and testcase results at limit + 1 before normalization", () => {
    const input = source();
    const ranking = input.rankings.items[0] as Record<string, unknown>;
    ranking.problemScoreByProblemSetProblemId = Object.fromEntries(
      Array.from({ length: MAX_PROBLEM_RESULTS_PER_RANKING }, (_, index) => [`problem-${index}`, {}]),
    );
    const detail = input.submissionDetailsById["submission-alpha"]!;
    detail.testcaseJudgeResults = Object.fromEntries(
      Array.from({ length: MAX_CASE_RESULTS_PER_SUBMISSION }, (_, index) => [`case-${index}`, {}]),
    );
    expect(() => validateSourceOperationalLimits(input)).not.toThrow();

    (ranking.problemScoreByProblemSetProblemId as Record<string, unknown>)["problem-over"] = {};
    expect(() => validateSourceOperationalLimits(input)).toThrow(
      `maximum is ${MAX_PROBLEM_RESULTS_PER_RANKING}`,
    );
    delete (ranking.problemScoreByProblemSetProblemId as Record<string, unknown>)["problem-over"];
    detail.testcaseJudgeResults["case-over"] = {};
    expect(() => validateSourceOperationalLimits(input)).toThrow(
      `maximum is ${MAX_CASE_RESULTS_PER_SUBMISSION}`,
    );
  });

  it("enforces per-string and aggregate decoded UTF-8 byte limits at exact boundaries", () => {
    expect(scanOperationalJson("x".repeat(MAX_STRING_BYTES)).stringBytes).toBe(MAX_STRING_BYTES);
    expect(() => scanOperationalJson("x".repeat(MAX_STRING_BYTES + 1))).toThrow(
      `maximum is ${MAX_STRING_BYTES}`,
    );

    const base = Math.floor(MAX_TOTAL_STRING_BYTES / 5);
    const strings = Array.from({ length: 5 }, (_, index) =>
      "x".repeat(index === 4 ? MAX_TOTAL_STRING_BYTES - base * 4 : base));
    expect(scanOperationalJson(strings).stringBytes).toBe(MAX_TOTAL_STRING_BYTES);
    strings[4] += "x";
    expect(() => scanOperationalJson(strings)).toThrow(
      `total string byte limit ${MAX_TOTAL_STRING_BYTES}`,
    );
  });

  it("enforces the exact total node count", () => {
    const exact = new Array<null>(MAX_TOTAL_NODES - 1).fill(null);
    expect(scanOperationalJson(exact).nodes).toBe(MAX_TOTAL_NODES);
    exact.push(null);
    expect(() => scanOperationalJson(exact)).toThrow(`total JSON node limit ${MAX_TOTAL_NODES}`);
  }, 30_000);

  it("enforces the serialized snapshot byte cap at the exact boundary", () => {
    const exact = "x".repeat(MAX_SNAPSHOT_BYTES);
    expect(validateSerializedSnapshotBytes(exact)).toBe(MAX_SNAPSHOT_BYTES);
    expect(() => validateSerializedSnapshotBytes(`${exact}x`)).toThrow(
      `server limit is ${MAX_SNAPSHOT_BYTES}`,
    );
  }, 30_000);

  it("enforces generic depth 32 and proves the closed typed snapshot depth is 6", async () => {
    let nested: unknown = null;
    for (let depth = 0; depth < MAX_JSON_DEPTH; depth += 1) {
      nested = [nested];
    }
    expect(scanOperationalJson(nested).maximumDepth).toBe(MAX_JSON_DEPTH);
    expect(() => scanOperationalJson([nested])).toThrow(`operational JSON depth ${MAX_JSON_DEPTH}`);

    const snapshot = await buildSnapshot(source(), EXPORTED_AT);
    expect(validateSnapshotOperationalLimits(snapshot).maximumDepth).toBe(SNAPSHOT_TYPED_MAX_DEPTH);
  });

  it("rejects final ranking and case arrays above their importer caps", async () => {
    const valid = await buildSnapshot(source(), EXPORTED_AT);
    const rankingSnapshot = structuredClone(valid);
    const ranking = rankingSnapshot.participants[0]?.ranking;
    if (ranking === null || ranking === undefined || ranking.problemResults[0] === undefined) {
      throw new Error("Fixture ranking is missing.");
    }
    ranking.problemResults = repeatedReferences(
      ranking.problemResults[0],
      MAX_PROBLEM_RESULTS_PER_RANKING + 1,
    );
    expect(() => validateSnapshotOperationalLimits(rankingSnapshot)).toThrow(
      `maximum is ${MAX_PROBLEM_RESULTS_PER_RANKING}`,
    );

    const caseSnapshot: PintiaSnapshotV2 = structuredClone(valid);
    const submission = caseSnapshot.submissions[0];
    if (submission === undefined || submission.caseResults[0] === undefined) {
      throw new Error("Fixture case result is missing.");
    }
    submission.caseResults = repeatedReferences(
      submission.caseResults[0],
      MAX_CASE_RESULTS_PER_SUBMISSION + 1,
    );
    expect(() => validateSnapshotOperationalLimits(caseSnapshot)).toThrow(
      `maximum is ${MAX_CASE_RESULTS_PER_SUBMISSION}`,
    );
  });
});
