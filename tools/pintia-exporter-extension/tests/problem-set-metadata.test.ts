import { describe, expect, it } from "vitest";
import { parseGetProblemSetResponse } from "../src/domain/problem-set-metadata";
import responseFixture from "./fixtures/sanitized-get-problem-set-response.json";

describe("Pintia GetProblemSet metadata contract", () => {
  it("extracts the authoritative closed metadata fields", () => {
    expect(parseGetProblemSetResponse(responseFixture, "9000000000000000001")).toEqual({
      problemSetId: "9000000000000000001",
      title: "SANITIZED_PROBLEM_SET",
      startsAt: "2030-01-01T01:00:00.000Z",
      endsAt: "2030-01-02T01:00:00.000Z",
    });
  });

  it.each([
    [null, null],
    [null, "2030-01-02T01:00:00.000Z"],
    ["2030-01-01T01:00:00.000Z", null],
  ])("preserves explicitly nullable problem-set bounds (%s, %s)", (startAt, endAt) => {
    expect(parseGetProblemSetResponse({
      problemSet: {
        id: "expected",
        name: "Exam",
        startAt,
        endAt,
      },
    }, "expected")).toMatchObject({ startsAt: startAt, endsAt: endAt });
  });

  it.each([
    [{}, "problemSet"],
    [{ problemSet: { id: "other", name: "Exam", startAt: "2026-01-01T00:00:00Z", endAt: "2026-01-02T00:00:00Z" } }, "different problem set id"],
    [{ problemSet: { id: "expected", name: "", startAt: "2026-01-01T00:00:00Z", endAt: "2026-01-02T00:00:00Z" } }, "name"],
    [{ problemSet: { id: "expected", name: "Exam", startAt: "invalid", endAt: "2026-01-02T00:00:00Z" } }, "startAt"],
    [{ problemSet: { id: "expected", name: "Exam", startAt: "2026-02-30T00:00:00Z", endAt: "2026-03-03T00:00:00Z" } }, "calendar timestamp"],
    [{ problemSet: { id: "expected", name: "Exam", startAt: "2026-01-01T00:00:00", endAt: "2026-01-02T00:00:00Z" } }, "RFC 3339"],
    [{ problemSet: { id: "expected", name: "Exam", startAt: "July 11, 2026", endAt: "2026-01-02T00:00:00Z" } }, "RFC 3339"],
    [{ problemSet: { id: "expected", name: "Exam", startAt: "2026-01-01T00:00:60Z", endAt: "2026-01-02T00:00:00Z" } }, "calendar timestamp"],
    [{ problemSet: { id: "expected", name: "Exam", startAt: "2026-01-03T00:00:00Z", endAt: "2026-01-02T00:00:00Z" } }, "must not be after"],
  ])("rejects incomplete or inconsistent response %j", (response, message) => {
    expect(() => parseGetProblemSetResponse(response, "expected")).toThrow(message);
  });
});
