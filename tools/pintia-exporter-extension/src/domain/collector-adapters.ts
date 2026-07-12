import { parseGetProblemSetResponse } from "./problem-set-metadata";
import type {
  JsonObject,
  ProblemCollection,
  RankingCollection,
  SubmissionCollection,
  SubmissionIndexes,
} from "./types";

export function collectionObject(value: unknown, field: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${field} collector returned an invalid object.`);
  }
  return value as JsonObject;
}

function objectIndex(value: unknown, field: string): Record<string, JsonObject> {
  const object = collectionObject(value, field);
  return Object.fromEntries(
    Object.entries(object).map(([key, item]) => [key, collectionObject(item, `${field}.${key}`)]),
  );
}

function collection(value: unknown, field: string): {
  items: JsonObject[];
  sourceReportedCount: unknown;
  observedCount: unknown;
  paginationExhausted: true;
  raw: JsonObject;
} {
  const object = collectionObject(value, field);
  if (!Array.isArray(object.items) || object.paginationExhausted !== true) {
    throw new Error(`${field} collector did not prove pagination exhaustion.`);
  }
  return {
    items: object.items.map((item, index) => collectionObject(item, `${field}.items[${index}]`)),
    sourceReportedCount: object.sourceReportedCount,
    observedCount: object.observedCount,
    paginationExhausted: true,
    raw: object,
  };
}

export function adaptProblemCollection(value: unknown, expectedProblemSetId: string): ProblemCollection {
  const object = collection(value, "problems");
  const metadata = parseGetProblemSetResponse(object.raw.metadataResponse, expectedProblemSetId);
  return {
    items: object.items,
    sourceReportedCount: object.sourceReportedCount as number | null,
    observedCount: object.observedCount as number,
    paginationExhausted: true,
    metadata,
  };
}

export function adaptRankingCollection(value: unknown): RankingCollection {
  const object = collection(value, "rankings");
  return {
    items: object.items,
    sourceReportedCount: object.sourceReportedCount as number | null,
    observedCount: object.observedCount as number,
    paginationExhausted: true,
    studentUserById: objectIndex(object.raw.studentUserById, "rankings.studentUserById"),
    userById: objectIndex(object.raw.userById, "rankings.userById"),
    userGroupById: objectIndex(object.raw.userGroupById, "rankings.userGroupById"),
  };
}

export function adaptSubmissionCollection(value: unknown): SubmissionCollection {
  const object = collection(value, "submissions");
  const rawIndexes = collectionObject(object.raw.indexes, "submissions.indexes");
  const indexes: SubmissionIndexes = {
    examMemberByUserId: objectIndex(rawIndexes.examMemberByUserId, "submissions.examMemberByUserId"),
    studentUserById: objectIndex(rawIndexes.studentUserById, "submissions.studentUserById"),
    userById: objectIndex(rawIndexes.userById, "submissions.userById"),
  };
  return {
    items: object.items,
    sourceReportedCount: object.sourceReportedCount as number | null,
    observedCount: object.observedCount as number,
    paginationExhausted: true,
    indexes,
  };
}
