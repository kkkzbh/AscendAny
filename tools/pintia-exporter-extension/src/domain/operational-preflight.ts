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
  utf8ByteLength,
} from "./limits";
import { nullableDecimal, nullableSafeInteger, requiredSafeInteger } from "./numeric";
import type {
  ExportBudgetLedger,
  JsonObject,
  PintiaSnapshotV2,
  SnapshotSource,
} from "./types";

export const SNAPSHOT_TYPED_MAX_DEPTH = 6;

export interface OperationalPreflightStats {
  nodes: number;
  stringBytes: number;
  maximumDepth: number;
}

export function emptyExportBudget(): ExportBudgetLedger {
  return { nodes: 0, stringBytes: 0, maximumDepth: 0 };
}

export function consumeExportBudget(
  current: ExportBudgetLedger,
  value: unknown,
  field: string,
): ExportBudgetLedger {
  const addition = scanOperationalJson(value);
  const nodes = current.nodes + addition.nodes;
  const stringBytes = current.stringBytes + addition.stringBytes;
  if (!Number.isSafeInteger(nodes) || nodes > MAX_TOTAL_NODES) {
    throw new Error(`${field} exceeds the cumulative JSON node limit ${MAX_TOTAL_NODES}.`);
  }
  if (!Number.isSafeInteger(stringBytes) || stringBytes > MAX_TOTAL_STRING_BYTES) {
    throw new Error(`${field} exceeds the cumulative string byte limit ${MAX_TOTAL_STRING_BYTES}.`);
  }
  return {
    nodes,
    stringBytes,
    maximumDepth: Math.max(current.maximumDepth, addition.maximumDepth),
  };
}

function object(value: unknown, field: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${field} must be an object.`);
  }
  return value as JsonObject;
}

function enforceLength(length: number, maximum: number, field: string): void {
  if (!Number.isSafeInteger(length) || length < 0 || length > maximum) {
    throw new Error(`${field} contains ${length} items; maximum is ${maximum}.`);
  }
}

function enforceObjectEntries(value: object, maximum: number, field: string): void {
  enforceLength(Object.keys(value).length, maximum, field);
}

export function validateSourceOperationalLimits(source: SnapshotSource): void {
  enforceLength(source.problems.items.length, MAX_PROBLEMS, "problems");
  enforceLength(source.rankings.items.length, MAX_PARTICIPANTS, "rankings");
  enforceLength(source.submissions.items.length, MAX_SUBMISSIONS, "submissions");
  enforceObjectEntries(source.rankings.studentUserById, MAX_PARTICIPANTS, "rankings.studentUserById");
  enforceObjectEntries(source.rankings.userById, MAX_PARTICIPANTS, "rankings.userById");
  enforceObjectEntries(source.rankings.userGroupById, MAX_PARTICIPANTS, "rankings.userGroupById");
  enforceObjectEntries(
    source.submissions.indexes.examMemberByUserId,
    MAX_PARTICIPANTS,
    "submissions.examMemberByUserId",
  );
  enforceObjectEntries(
    source.submissions.indexes.studentUserById,
    MAX_PARTICIPANTS,
    "submissions.studentUserById",
  );
  enforceObjectEntries(source.submissions.indexes.userById, MAX_PARTICIPANTS, "submissions.userById");
  enforceObjectEntries(source.submissionDetailsById, MAX_SUBMISSIONS, "submissionDetailsById");
  source.rankings.items.forEach((ranking, index) => {
    const results = object(
      ranking.problemScoreByProblemSetProblemId,
      `rankings[${index}].problemScoreByProblemSetProblemId`,
    );
    enforceLength(
      Object.keys(results).length,
      MAX_PROBLEM_RESULTS_PER_RANKING,
      `rankings[${index}].problemResults`,
    );
  });
  for (const [submissionId, detail] of Object.entries(source.submissionDetailsById)) {
    if (utf8ByteLength(detail.code) > MAX_CODE_BYTES) {
      throw new Error(`Submission ${submissionId} code exceeds ${MAX_CODE_BYTES} UTF-8 bytes.`);
    }
    enforceLength(
      Object.keys(detail.testcaseJudgeResults).length,
      MAX_CASE_RESULTS_PER_SUBMISSION,
      `submission ${submissionId} caseResults`,
    );
  }
  scanOperationalJson(source);
}

export function validateSerializedSnapshotBytes(json: string): number {
  const bytes = utf8ByteLength(json);
  if (bytes > MAX_SNAPSHOT_BYTES) {
    throw new Error(`Pintia snapshot is ${bytes} UTF-8 bytes; server limit is ${MAX_SNAPSHOT_BYTES}.`);
  }
  return bytes;
}

function validateSnapshotNumbers(snapshot: PintiaSnapshotV2): void {
  nullableDecimal(snapshot.exam.totalScore, "exam.totalScore");
  snapshot.problems.forEach((problem, index) => {
    nullableDecimal(problem.maxScore, `problems[${index}].maxScore`);
    nullableSafeInteger(problem.timeLimitMs, `problems[${index}].timeLimitMs`);
    nullableSafeInteger(problem.memoryLimitBytes, `problems[${index}].memoryLimitBytes`);
  });
  snapshot.participants.forEach((participant, participantIndex) => {
    const ranking = participant.ranking;
    if (ranking === null) {
      return;
    }
    requiredSafeInteger(ranking.rank, `participants[${participantIndex}].ranking.rank`, 1);
    nullableDecimal(ranking.totalScore, `participants[${participantIndex}].ranking.totalScore`);
    nullableSafeInteger(
      ranking.timeUsedSeconds,
      `participants[${participantIndex}].ranking.timeUsedSeconds`,
    );
    ranking.problemResults.forEach((result, resultIndex) => {
      nullableDecimal(
        result.score,
        `participants[${participantIndex}].ranking.problemResults[${resultIndex}].score`,
      );
      nullableSafeInteger(
        result.validSubmissionCount,
        `participants[${participantIndex}].ranking.problemResults[${resultIndex}].validSubmissionCount`,
      );
      requiredSafeInteger(
        result.acceptTimeSeconds,
        `participants[${participantIndex}].ranking.problemResults[${resultIndex}].acceptTimeSeconds`,
        0,
      );
    });
  });
  snapshot.submissions.forEach((submission, submissionIndex) => {
    nullableDecimal(submission.score, `submissions[${submissionIndex}].score`);
    nullableSafeInteger(submission.timeMs, `submissions[${submissionIndex}].timeMs`);
    nullableSafeInteger(submission.memoryBytes, `submissions[${submissionIndex}].memoryBytes`);
    submission.caseResults.forEach((result, resultIndex) => {
      nullableDecimal(result.score, `submissions[${submissionIndex}].caseResults[${resultIndex}].score`);
      nullableSafeInteger(
        result.timeMs,
        `submissions[${submissionIndex}].caseResults[${resultIndex}].timeMs`,
      );
      nullableSafeInteger(
        result.memoryBytes,
        `submissions[${submissionIndex}].caseResults[${resultIndex}].memoryBytes`,
      );
    });
  });
  nullableSafeInteger(snapshot.completeness.problems.sourceReportedCount, "completeness.problems.sourceReportedCount");
  requiredSafeInteger(snapshot.completeness.problems.observedCount, "completeness.problems.observedCount", 0);
  requiredSafeInteger(snapshot.completeness.problems.exportedCount, "completeness.problems.exportedCount", 0);
  nullableSafeInteger(snapshot.completeness.rankings.sourceReportedCount, "completeness.rankings.sourceReportedCount");
  requiredSafeInteger(snapshot.completeness.rankings.observedCount, "completeness.rankings.observedCount", 0);
  requiredSafeInteger(snapshot.completeness.rankings.exportedCount, "completeness.rankings.exportedCount", 0);
  nullableSafeInteger(snapshot.completeness.submissions.sourceReportedCount, "completeness.submissions.sourceReportedCount");
  requiredSafeInteger(snapshot.completeness.submissions.observedCount, "completeness.submissions.observedCount", 0);
  requiredSafeInteger(snapshot.completeness.submissions.exportedCount, "completeness.submissions.exportedCount", 0);
  requiredSafeInteger(snapshot.completeness.participants.exportedCount, "completeness.participants.exportedCount", 0);
}

export function validateSnapshotOperationalLimits(snapshot: PintiaSnapshotV2): OperationalPreflightStats {
  enforceLength(snapshot.problems.length, MAX_PROBLEMS, "problems");
  enforceLength(snapshot.participants.length, MAX_PARTICIPANTS, "participants");
  enforceLength(snapshot.submissions.length, MAX_SUBMISSIONS, "submissions");
  snapshot.participants.forEach((participant, index) => {
    if (participant.ranking !== null) {
      enforceLength(
        participant.ranking.problemResults.length,
        MAX_PROBLEM_RESULTS_PER_RANKING,
        `participants[${index}].ranking.problemResults`,
      );
    }
  });
  snapshot.submissions.forEach((submission, index) => {
    enforceLength(
      submission.caseResults.length,
      MAX_CASE_RESULTS_PER_SUBMISSION,
      `submissions[${index}].caseResults`,
    );
    if (utf8ByteLength(submission.code) > MAX_CODE_BYTES) {
      throw new Error(`submissions[${index}].code exceeds ${MAX_CODE_BYTES} UTF-8 bytes.`);
    }
  });
  validateSnapshotNumbers(snapshot);

  return scanOperationalJson(snapshot, SNAPSHOT_TYPED_MAX_DEPTH);
}

export function scanOperationalJson(
  value: unknown,
  maximumAllowedDepth = MAX_JSON_DEPTH,
): OperationalPreflightStats {
  if (
    !Number.isSafeInteger(maximumAllowedDepth) ||
    maximumAllowedDepth <= 0 ||
    maximumAllowedDepth > MAX_JSON_DEPTH
  ) {
    throw new Error(`Operational JSON depth must be between 1 and ${MAX_JSON_DEPTH}.`);
  }
  let nodes = 0;
  let stringBytes = 0;
  let maximumDepth = 0;
  const stack: Array<{ value: unknown; depth: number; path: string }> = [
    { value, depth: 0, path: "$" },
  ];
  const addNode = (path: string): void => {
    nodes += 1;
    if (nodes > MAX_TOTAL_NODES) {
      throw new Error(`${path} exceeds the total JSON node limit ${MAX_TOTAL_NODES}.`);
    }
  };
  const addString = (value: string, path: string): void => {
    const bytes = utf8ByteLength(value);
    if (bytes > MAX_STRING_BYTES) {
      throw new Error(`${path} contains ${bytes} UTF-8 bytes; maximum is ${MAX_STRING_BYTES}.`);
    }
    stringBytes += bytes;
    if (stringBytes > MAX_TOTAL_STRING_BYTES) {
      throw new Error(`${path} exceeds the total string byte limit ${MAX_TOTAL_STRING_BYTES}.`);
    }
  };

  while (stack.length > 0) {
    const current = stack.pop() as { value: unknown; depth: number; path: string };
    addNode(current.path);
    if (typeof current.value === "string") {
      addString(current.value, current.path);
      continue;
    }
    if (Array.isArray(current.value)) {
      const depth = current.depth + 1;
      maximumDepth = Math.max(maximumDepth, depth);
      if (depth > maximumAllowedDepth) {
        throw new Error(`${current.path} exceeds the operational JSON depth ${maximumAllowedDepth}.`);
      }
      for (let index = current.value.length - 1; index >= 0; index -= 1) {
        stack.push({ value: current.value[index], depth, path: `${current.path}[${index}]` });
      }
      continue;
    }
    if (typeof current.value === "object" && current.value !== null) {
      const depth = current.depth + 1;
      maximumDepth = Math.max(maximumDepth, depth);
      if (depth > maximumAllowedDepth) {
        throw new Error(`${current.path} exceeds the operational JSON depth ${maximumAllowedDepth}.`);
      }
      const entries = Object.entries(current.value as Record<string, unknown>);
      for (let index = entries.length - 1; index >= 0; index -= 1) {
        const [key, value] = entries[index] as [string, unknown];
        const path = `${current.path}.${key}`;
        addNode(path);
        addString(key, path);
        stack.push({ value, depth, path });
      }
    }
  }
  return { nodes, stringBytes, maximumDepth };
}
