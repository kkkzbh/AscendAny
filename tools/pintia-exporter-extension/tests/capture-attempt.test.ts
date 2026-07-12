import { describe, expect, it } from "vitest";
import {
  assertCaptureStable,
  assertSubmissionDetailsStable,
  CaptureDriftError,
} from "../src/domain/capture-attempt";
import type { ProblemCollection, SubmissionDetailSource } from "../src/domain/types";

function problemCollection(): ProblemCollection {
  return {
    items: [
      { id: "problem-b", title: "B" },
      { id: "problem-a", title: "A" },
    ],
    sourceReportedCount: 2,
    observedCount: 2,
    paginationExhausted: true,
    metadata: {
      problemSetId: "problem-set",
      title: "Exam",
      startsAt: null,
      endsAt: null,
    },
  };
}

function detail(code = "return 0;", verdict = "ACCEPTED"): SubmissionDetailSource {
  return {
    submissionId: "submission-a",
    code,
    compileLog: null,
    testcaseJudgeResults: {
      "case-b": { result: verdict, time: 0.2 },
      "case-a": { result: verdict, time: 0.1 },
    },
  };
}

describe("full-attempt capture equivalence", () => {
  it("ignores collection and object-key ordering while comparing typed content", () => {
    const initial = problemCollection();
    const final = structuredClone(initial);
    final.items.reverse();
    final.items = final.items.map((item) => ({ title: item.title, id: item.id }));

    expect(() => assertCaptureStable("problems", initial, final)).not.toThrow();
    expect(() => assertSubmissionDetailsStable(
      { "submission-a": detail() },
      {
        "submission-a": {
          ...detail(),
          testcaseJudgeResults: {
            "case-a": { time: 0.1, result: "ACCEPTED" },
            "case-b": { time: 0.2, result: "ACCEPTED" },
          },
        },
      },
    )).not.toThrow();
  });

  it("rejects a changed collection so the entire attempt must be discarded", () => {
    const initial = problemCollection();
    const final = structuredClone(initial);
    const first = final.items[0];
    if (first === undefined) {
      throw new Error("Synthetic problem fixture is empty.");
    }
    first.title = "Changed during capture";

    expect(() => assertCaptureStable("problems", initial, final)).toThrowError(
      expect.objectContaining<Partial<CaptureDriftError>>({
        name: "CaptureDriftError",
        collection: "problems",
      }),
    );
  });

  it.each([
    ["code", detail("return 1;")],
    ["rejudge result", detail("return 0;", "WRONG_ANSWER")],
  ])("rejects submission-detail %s drift between the two complete passes", (_label, changed) => {
    expect(() => assertSubmissionDetailsStable(
      { "submission-a": detail() },
      { "submission-a": changed },
    )).toThrowError(
      expect.objectContaining<Partial<CaptureDriftError>>({
        name: "CaptureDriftError",
        collection: "submission-details",
      }),
    );
  });
});
