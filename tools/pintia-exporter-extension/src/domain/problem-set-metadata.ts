import type { JsonObject, ProblemSetMetadataSource } from "./types";
import { normalizedUTCDateTime } from "./timestamp";

function object(value: unknown, field: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${field} must be an object.`);
  }
  return value as JsonObject;
}

function requiredString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new Error(`${field} must be a non-empty string.`);
  }
  return value;
}

function nullableUTCDateTime(value: unknown, field: string): string | null {
  return value === null ? null : normalizedUTCDateTime(value, field);
}

export function parseGetProblemSetResponse(
  value: unknown,
  expectedProblemSetId: string,
): ProblemSetMetadataSource {
  const response = object(value, "GetProblemSet response");
  const problemSet = object(response.problemSet, "GetProblemSet response.problemSet");
  const problemSetId = requiredString(problemSet.id, "GetProblemSet problemSet.id");
  if (problemSetId !== expectedProblemSetId) {
    throw new Error("GetProblemSet returned a different problem set id.");
  }
  const startsAt = nullableUTCDateTime(problemSet.startAt, "GetProblemSet problemSet.startAt");
  const endsAt = nullableUTCDateTime(problemSet.endAt, "GetProblemSet problemSet.endAt");
  if (startsAt !== null && endsAt !== null && Date.parse(startsAt) > Date.parse(endsAt)) {
    throw new Error("GetProblemSet problemSet.startAt must not be after endAt.");
  }
  return {
    problemSetId,
    title: requiredString(problemSet.name, "GetProblemSet problemSet.name"),
    startsAt,
    endsAt,
  };
}
