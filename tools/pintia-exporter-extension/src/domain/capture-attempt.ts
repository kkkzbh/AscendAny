import type {
  JsonObject,
  ProblemCollection,
  RankingCollection,
  SubmissionCollection,
  SubmissionDetailSource,
} from "./types";

export type CaptureCollectionName = "problems" | "rankings" | "submissions" | "submission-details";

export class CaptureDriftError extends Error {
  constructor(readonly collection: CaptureCollectionName) {
    super(`Pintia ${collection} changed during the capture attempt.`);
    this.name = "CaptureDriftError";
  }
}

function object(value: unknown, field: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${field} must be an object.`);
  }
  return value as JsonObject;
}

function requiredId(value: unknown, field: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${field} must be a non-empty string.`);
  }
  return value;
}

export function canonicalCaptureJson(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map(canonicalCaptureJson).join(",")}]`;
  }
  if (typeof value === "object" && value !== null) {
    return `{${Object.entries(value as JsonObject)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, child]) => `${JSON.stringify(key)}:${canonicalCaptureJson(child)}`)
      .join(",")}}`;
  }
  return JSON.stringify(value);
}

function itemId(collection: CaptureCollectionName, item: JsonObject, index: number): string {
  if (collection === "rankings") {
    return requiredId(object(item.user, `rankings[${index}].user`).userId, `rankings[${index}].user.userId`);
  }
  return requiredId(item.id, `${collection}[${index}].id`);
}

export function captureCollectionCanonical(
  collection: CaptureCollectionName,
  value: { items: JsonObject[] },
): string {
  const items = value.items
    .map((item, index) => ({ id: itemId(collection, item, index), item }))
    .sort((left, right) => left.id.localeCompare(right.id));
  for (let index = 1; index < items.length; index += 1) {
    if (items[index - 1]?.id === items[index]?.id) {
      throw new Error(`${collection} contains duplicate identity ${items[index]?.id ?? "unknown"}.`);
    }
  }
  return canonicalCaptureJson({ ...value, items: items.map(({ item }) => item) });
}

export function assertCaptureStable(
  collection: "problems",
  initial: ProblemCollection,
  final: ProblemCollection,
): void;
export function assertCaptureStable(
  collection: "rankings",
  initial: RankingCollection,
  final: RankingCollection,
): void;
export function assertCaptureStable(
  collection: "submissions",
  initial: SubmissionCollection,
  final: SubmissionCollection,
): void;
export function assertCaptureStable(
  collection: CaptureCollectionName,
  initial: ProblemCollection | RankingCollection | SubmissionCollection,
  final: ProblemCollection | RankingCollection | SubmissionCollection,
): void {
  if (
    captureCollectionCanonical(collection, initial) !==
    captureCollectionCanonical(collection, final)
  ) {
    throw new CaptureDriftError(collection);
  }
}

export function assertSubmissionDetailsStable(
  initial: Record<string, SubmissionDetailSource>,
  final: Record<string, SubmissionDetailSource>,
): void {
  if (canonicalCaptureJson(initial) !== canonicalCaptureJson(final)) {
    throw new CaptureDriftError("submission-details");
  }
}
