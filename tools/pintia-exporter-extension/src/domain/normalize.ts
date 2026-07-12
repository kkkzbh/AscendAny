import {
  EXPORTER_NAME,
  EXPORTER_VERSION,
  SCHEMA_SHA256,
  SNAPSHOT_SCHEMA,
  type ExhaustiveCollection,
  type JsonObject,
  type PintiaCaseResult,
  type PintiaParticipant,
  type PintiaProblem,
  type PintiaRanking,
  type PintiaRankingProblemResult,
  type PintiaSnapshotV2,
  type PintiaSubmission,
  type RankingCollection,
  type SnapshotSource,
  type SubmissionIndexes,
} from "./types";
import { validateAuthoritativeSnapshotSchema } from "./authoritative-schema";
import {
  nullableDecimal,
  nullableFiniteNonnegative,
  nullableSafeInteger,
  requiredSafeInteger,
  roundedSafeNonnegativeInteger,
  sumDecimals,
} from "./numeric";
import {
  validateSnapshotOperationalLimits,
  validateSourceOperationalLimits,
} from "./operational-preflight";
import { normalizedUTCDateTime } from "./timestamp";

const PINTIA_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/;

export async function sha256Utf8(value: string, signal?: AbortSignal): Promise<string> {
  signal?.throwIfAborted();
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  signal?.throwIfAborted();
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

function record(value: unknown, field: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${field} must be an object.`);
  }
  return value as JsonObject;
}

function requiredString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${field} must be a non-empty string.`);
  }
  return value;
}

function requiredNonWhitespaceString(value: unknown, field: string): string {
  const text = requiredString(value, field);
  if (text.trim().length === 0) {
    throw new Error(`${field} must contain a non-whitespace character.`);
  }
  return text;
}

function nullableString(value: unknown, field: string): string | null {
  if (value === undefined || value === null || value === "") {
    return null;
  }
  if (typeof value !== "string") {
    throw new Error(`${field} must be a string when present.`);
  }
  return value;
}

function nullableNonWhitespaceString(value: unknown, field: string): string | null {
  const text = nullableString(value, field);
  if (text !== null && text.trim().length === 0) {
    throw new Error(`${field} must be null or contain a non-whitespace character.`);
  }
  return text;
}

function pintiaId(value: unknown, field: string): string {
  const id = requiredString(value, field);
  if (id.length > 256 || !PINTIA_ID.test(id)) {
    throw new Error(`${field} is not a valid Pintia id.`);
  }
  return id;
}

function nullablePintiaId(value: unknown, field: string): string | null {
  if (value === undefined || value === null || value === "") {
    return null;
  }
  return pintiaId(value, field);
}

function nullableUserGroupId(value: unknown, field: string): string | null {
  const id = nullablePintiaId(value, field);
  // Pintia exam-member payloads use the literal ID "0" for a member that is
  // not assigned to a problem-set user group. It is a sentinel, so no
  // userGroupById entry exists for it.
  return id === "0" ? null : id;
}

function utcDateTime(value: unknown, field: string): string {
  return normalizedUTCDateTime(value, field);
}

function nullableUTCDateTime(value: unknown, field: string): string | null {
  return value === null ? null : utcDateTime(value, field);
}

function expectUnique(values: Iterable<string>, field: string): void {
  const seen = new Set<string>();
  for (const value of values) {
    if (seen.has(value)) {
      throw new Error(`Duplicate ${field}: ${value}.`);
    }
    seen.add(value);
  }
}

function assertCollection<T>(collection: ExhaustiveCollection<T>, field: string): void {
  requiredSafeInteger(collection.observedCount, `${field}.observedCount`, 0);
  nullableSafeInteger(collection.sourceReportedCount, `${field}.sourceReportedCount`);
  if (collection.paginationExhausted !== true) {
    throw new Error(`${field} pagination is not exhausted.`);
  }
  if (collection.observedCount !== collection.items.length) {
    throw new Error(`${field} observedCount does not match collected items.`);
  }
  if (
    collection.sourceReportedCount !== null &&
    collection.sourceReportedCount !== collection.observedCount
  ) {
    throw new Error(`${field} sourceReportedCount does not match the exhausted collection.`);
  }
}

function normalizeProblem(source: JsonObject, index: number): PintiaProblem {
  const type = requiredString(source.type, `problems[${index}].type`).toUpperCase();
  if (type !== "PROGRAMMING") {
    throw new Error(`problems[${index}] is not a PROGRAMMING problem.`);
  }
  const problemConfig = record(source.problemConfig, `problems[${index}].problemConfig`);
  const programming = record(
    problemConfig.programmingProblemConfig,
    `problems[${index}].problemConfig.programmingProblemConfig`,
  );
  const timeLimit = nullableFiniteNonnegative(programming.timeLimit, `problems[${index}].timeLimit`);
  const memoryLimitKiB = nullableFiniteNonnegative(programming.memoryLimit, `problems[${index}].memoryLimit`);

  return {
    problemSetProblemId: pintiaId(source.id, `problems[${index}].id`),
    problemId: pintiaId(source.problemId, `problems[${index}].problemId`),
    label: nullableString(source.label, `problems[${index}].label`),
    title: requiredNonWhitespaceString(source.title, `problems[${index}].title`),
    type: "PROGRAMMING",
    maxScore: nullableDecimal(source.score, `problems[${index}].score`),
    contentHtml: nullableString(source.content, `problems[${index}].content`),
    timeLimitMs: roundedSafeNonnegativeInteger(timeLimit, 1, `problems[${index}].timeLimitMs`),
    memoryLimitBytes: roundedSafeNonnegativeInteger(
      memoryLimitKiB,
      1024,
      `problems[${index}].memoryLimitBytes`,
    ),
  };
}

function rankingUserId(source: JsonObject, index: number): string {
  const user = record(source.user, `rankings[${index}].user`);
  return pintiaId(user.userId, `rankings[${index}].user.userId`);
}

function normalizeRankingProblemResults(
  source: JsonObject,
  index: number,
  maxScoreByProblemId: ReadonlyMap<string, number | null>,
): PintiaRankingProblemResult[] {
  const scoreByProblem = record(
    source.problemScoreByProblemSetProblemId,
    `rankings[${index}].problemScoreByProblemSetProblemId`,
  );
  return Object.entries(scoreByProblem)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([rawProblemId, rawResult]) => {
      const problemSetProblemId = pintiaId(rawProblemId, `rankings[${index}].problem id`);
      if (!maxScoreByProblemId.has(problemSetProblemId)) {
        throw new Error(`Ranking references unknown problem ${problemSetProblemId}.`);
      }
      const result = record(rawResult, `rankings[${index}].problemResults.${problemSetProblemId}`);
      const score = nullableDecimal(
        result.score,
        `rankings[${index}].problemResults.${problemSetProblemId}.score`,
      );
      const acceptTimeSeconds = requiredSafeInteger(
        result.acceptTime,
        `rankings[${index}].problemResults.${problemSetProblemId}.acceptTime`,
        0,
      );
      const maxScore = maxScoreByProblemId.get(problemSetProblemId) ?? null;
      return {
        problemSetProblemId,
        score,
        passed: score === null || maxScore === null ? null : score >= maxScore,
        validSubmissionCount: nullableSafeInteger(
          result.validSubmitCount,
          `rankings[${index}].problemResults.${problemSetProblemId}.validSubmitCount`,
        ),
        acceptTimeSeconds,
      };
    });
}

function normalizeRanking(
  source: JsonObject,
  index: number,
  maxScoreByProblemId: ReadonlyMap<string, number | null>,
): PintiaRanking {
  return {
    rank: requiredSafeInteger(source.rank, `rankings[${index}].rank`, 1),
    totalScore: nullableDecimal(source.totalScore, `rankings[${index}].totalScore`),
    timeUsedSeconds: nullableSafeInteger(source.solvingTime, `rankings[${index}].solvingTime`),
    problemResults: normalizeRankingProblemResults(source, index, maxScoreByProblemId),
  };
}

function normalizeCaseResults(value: JsonObject, submissionId: string): PintiaCaseResult[] {
  return Object.entries(value)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([rawCaseId, rawResult]) => {
      const caseId = pintiaId(rawCaseId, `submissions.${submissionId}.caseId`);
      const result = record(rawResult, `submissions.${submissionId}.caseResults.${caseId}`);
      const seconds = nullableFiniteNonnegative(
        result.time,
        `submissions.${submissionId}.caseResults.${caseId}.time`,
      );
      const memory = nullableFiniteNonnegative(
        result.memory,
        `submissions.${submissionId}.caseResults.${caseId}.memory`,
      );
      const error = nullableString(result.error, `submissions.${submissionId}.caseResults.${caseId}.error`);
      const checkerOutput = nullableString(
        result.checkerOutput,
        `submissions.${submissionId}.caseResults.${caseId}.checkerOutput`,
      );
      return {
        caseId,
        verdict: nullableString(result.result, `submissions.${submissionId}.caseResults.${caseId}.result`),
        score: nullableDecimal(
          result.testcaseScore,
          `submissions.${submissionId}.caseResults.${caseId}.testcaseScore`,
        ),
        timeMs: roundedSafeNonnegativeInteger(
          seconds,
          1000,
          `submissions.${submissionId}.caseResults.${caseId}.timeMs`,
        ),
        memoryBytes: roundedSafeNonnegativeInteger(
          memory,
          1,
          `submissions.${submissionId}.caseResults.${caseId}.memoryBytes`,
        ),
        message: error ?? checkerOutput,
      };
    });
}

export function selectProgrammingSubmissionItems(
  items: JsonObject[],
  problemIds: ReadonlySet<string>,
): JsonObject[] {
  expectUnique(
    items.map((item, index) => pintiaId(item.id, `submissions[${index}].id`)),
    "source submission id",
  );

  return items.filter((item, index) => {
    const problemType = requiredString(item.problemType, `submissions[${index}].problemType`).toUpperCase();
    const problemId = pintiaId(item.problemSetProblemId, `submissions[${index}].problemSetProblemId`);
    if (problemType === "PROGRAMMING" && !problemIds.has(problemId)) {
      throw new Error(`Programming submission references unexported problem ${problemId}.`);
    }
    return problemType === "PROGRAMMING";
  });
}

async function normalizeSubmission(
  source: JsonObject,
  index: number,
  detailsById: SnapshotSource["submissionDetailsById"],
  signal?: AbortSignal,
): Promise<PintiaSubmission> {
  signal?.throwIfAborted();
  const submissionId = pintiaId(source.id, `submissions[${index}].id`);
  const detail = detailsById[submissionId];
  if (detail === undefined || detail.submissionId !== submissionId) {
    throw new Error(`Submission ${submissionId} has no matching code detail.`);
  }
  if (typeof detail.code !== "string" || detail.code.length === 0) {
    throw new Error(`Submission ${submissionId} has an empty program.`);
  }
  const compiler = nullableString(source.compiler, `submissions[${index}].compiler`);
  const seconds = nullableFiniteNonnegative(source.time, `submissions[${index}].time`);
  const memory = nullableFiniteNonnegative(source.memory, `submissions[${index}].memory`);
  return {
    submissionId,
    problemSetProblemId: pintiaId(
      source.problemSetProblemId,
      `submissions[${index}].problemSetProblemId`,
    ),
    userId: pintiaId(source.userId, `submissions[${index}].userId`),
    submittedAt: utcDateTime(source.submitAt, `submissions[${index}].submitAt`),
    language: compiler,
    compiler,
    verdict: requiredNonWhitespaceString(source.status, `submissions[${index}].status`),
    score: nullableDecimal(source.score, `submissions[${index}].score`),
    timeMs: roundedSafeNonnegativeInteger(seconds, 1000, `submissions[${index}].timeMs`),
    memoryBytes: roundedSafeNonnegativeInteger(memory, 1, `submissions[${index}].memoryBytes`),
    code: detail.code,
    codeSha256: await sha256Utf8(detail.code, signal),
    compileLog: detail.compileLog,
    caseResults: normalizeCaseResults(detail.testcaseJudgeResults, submissionId),
  };
}

function canonicalJson(value: unknown, field: string): string {
  if (value === null) {
    return "null";
  }
  if (typeof value === "string" || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new Error(`${field} must contain only finite JSON numbers.`);
    }
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item, index) => canonicalJson(item, `${field}[${index}]`)).join(",")}]`;
  }
  if (typeof value === "object") {
    return `{${Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, child]) => `${JSON.stringify(key)}:${canonicalJson(child, `${field}.${key}`)}`)
      .join(",")}}`;
  }
  throw new Error(`${field} must contain only JSON values.`);
}

function mergeIdentityIndex(
  rankingIndex: Readonly<Record<string, JsonObject>>,
  submissionIndex: Readonly<Record<string, JsonObject>>,
  field: string,
): Record<string, JsonObject> {
  const merged: Record<string, JsonObject> = {};
  for (const [identity, value] of Object.entries(rankingIndex)) {
    merged[identity] = value;
  }
  for (const [identity, value] of Object.entries(submissionIndex)) {
    const existing = merged[identity];
    if (
      existing !== undefined &&
      canonicalJson(existing, `${field}.${identity}`) !== canonicalJson(value, `${field}.${identity}`)
    ) {
      throw new Error(`${field}.${identity} conflicts between ranking and submission collectors.`);
    }
    merged[identity] = value;
  }
  return merged;
}

function mergeIndexes(
  rankings: RankingCollection,
  submissions: SubmissionIndexes,
): SubmissionIndexes {
  const rankingExamMemberByUserId: Record<string, JsonObject> = {};
  rankings.items.forEach((ranking, index) => {
    const user = record(ranking.user, `rankings[${index}].user`);
    const userId = rankingUserId(ranking, index);
    const existing = rankingExamMemberByUserId[userId];
    if (existing !== undefined) {
      throw new Error(`Duplicate ranking exam member identity: ${userId}.`);
    }
    rankingExamMemberByUserId[userId] = user;
  });
  return {
    examMemberByUserId: mergeIdentityIndex(
      rankingExamMemberByUserId,
      submissions.examMemberByUserId,
      "examMemberByUserId",
    ),
    studentUserById: mergeIdentityIndex(
      rankings.studentUserById,
      submissions.studentUserById,
      "studentUserById",
    ),
    userById: mergeIdentityIndex(rankings.userById, submissions.userById, "userById"),
  };
}

function participantAttributes(
  userId: string,
  indexes: SubmissionIndexes,
  userGroupById: RankingCollection["userGroupById"],
): Omit<PintiaParticipant, "userId" | "ranking"> {
  const member = indexes.examMemberByUserId[userId];
  const studentUserId = member === undefined
    ? null
    : nullablePintiaId(member.studentUserId, `participant ${userId}.studentUserId`);
  const student = studentUserId === null ? undefined : indexes.studentUserById[studentUserId];
  const user = indexes.userById[userId];
  const userGroupId = member === undefined
    ? null
    : nullableUserGroupId(member.userGroupId, `participant ${userId}.userGroupId`);
  const userGroup = userGroupId === null ? undefined : userGroupById[userGroupId];
  if (userGroupId !== null && userGroup === undefined) {
    throw new Error(`Participant ${userId} references missing user group ${userGroupId}.`);
  }
  return {
    studentUserId,
    studentNumber: student === undefined
      ? null
      : nullableNonWhitespaceString(student.studentNumber, `participant ${userId}.studentNumber`),
    displayName: student === undefined
      ? user === undefined
        ? null
        : nullableString(user.nickname, `participant ${userId}.nickname`)
      : nullableString(student.name, `participant ${userId}.name`),
    groupName: userGroup === undefined
      ? null
      : requiredString(userGroup.name, `participant ${userId}.groupName`),
  };
}

function exactSet(left: ReadonlySet<string>, right: ReadonlySet<string>, field: string): void {
  if (left.size !== right.size || [...left].some((value) => !right.has(value))) {
    throw new Error(`${field} set does not match the required union.`);
  }
}

export interface BuildSnapshotOptions {
  exporterVersion?: string;
  schemaSha256?: string;
  signal?: AbortSignal;
}

export async function buildSnapshot(
  source: SnapshotSource,
  exportedAt: string,
  options: BuildSnapshotOptions = {},
): Promise<PintiaSnapshotV2> {
  const exporterVersion = options.exporterVersion ?? EXPORTER_VERSION;
  const schemaSha256 = options.schemaSha256 ?? SCHEMA_SHA256;
  options.signal?.throwIfAborted();
  validateSourceOperationalLimits(source);
  assertCollection(source.problems, "problems");
  assertCollection(source.rankings, "rankings");
  assertCollection(source.submissions, "submissions");

  const problemSetId = pintiaId(source.problemSetId, "exam.problemSetId");
  const metadataProblemSetId = pintiaId(source.problems.metadata.problemSetId, "problems.metadata.problemSetId");
  if (metadataProblemSetId !== problemSetId) {
    throw new Error("GetProblemSet metadata does not match the exported problem set.");
  }
  const sourceUrl = new URL(source.sourceUrl);
  const sourcePathMatch = /^\/problem-sets\/([^/]+)(?:\/|$)/.exec(sourceUrl.pathname);
  let sourceProblemSetId: string | null = null;
  if (sourcePathMatch?.[1] !== undefined) {
    try {
      sourceProblemSetId = decodeURIComponent(sourcePathMatch[1]);
    } catch {
      sourceProblemSetId = null;
    }
  }
  if (
    sourceUrl.protocol !== "https:" ||
    sourceUrl.hostname !== "pintia.cn" ||
    sourceUrl.port !== "" ||
    sourceUrl.username !== "" ||
    sourceUrl.password !== "" ||
    sourceProblemSetId !== problemSetId
  ) {
    throw new Error("exam.sourceUrl must identify the exported Pintia problem set.");
  }

  const problems = source.problems.items.map(normalizeProblem);
  if (problems.length === 0) {
    throw new Error("At least one programming problem is required.");
  }
  expectUnique(problems.map((problem) => problem.problemSetProblemId), "problemSetProblemId");
  expectUnique(problems.map((problem) => problem.problemId), "problemId");
  const problemIds = new Set(problems.map((problem) => problem.problemSetProblemId));
  const maxScoreByProblemId = new Map(
    problems.map((problem) => [problem.problemSetProblemId, problem.maxScore] as const),
  );

  expectUnique(
    source.rankings.items.map((ranking, index) => rankingUserId(ranking, index)),
    "ranking userId",
  );
  const rankingByUserId = new Map<string, PintiaRanking>();
  source.rankings.items.forEach((ranking, index) => {
    const userId = rankingUserId(ranking, index);
    rankingByUserId.set(userId, normalizeRanking(ranking, index, maxScoreByProblemId));
  });

  const programmingItems = selectProgrammingSubmissionItems(source.submissions.items, problemIds);
  const programmingIds = new Set(
    programmingItems.map((item, index) => pintiaId(item.id, `programmingSubmissions[${index}].id`)),
  );
  const detailIds = new Set(Object.keys(source.submissionDetailsById));
  exactSet(detailIds, programmingIds, "submission detail id");
  const submissions: PintiaSubmission[] = [];
  for (let index = 0; index < programmingItems.length; index += 1) {
    options.signal?.throwIfAborted();
    submissions.push(await normalizeSubmission(
      programmingItems[index] as JsonObject,
      index,
      source.submissionDetailsById,
      options.signal,
    ));
  }
  submissions.sort((left, right) => left.submissionId.localeCompare(right.submissionId));

  for (const submission of submissions) {
    if (!problemIds.has(submission.problemSetProblemId)) {
      throw new Error(`Submission ${submission.submissionId} references an unknown problem.`);
    }
  }

  const requiredParticipantIds = new Set(rankingByUserId.keys());
  submissions.forEach((submission) => requiredParticipantIds.add(submission.userId));
  const indexes = mergeIndexes(source.rankings, source.submissions.indexes);
  const participants = [...requiredParticipantIds]
    .sort((left, right) => left.localeCompare(right))
    .map((userId): PintiaParticipant => ({
      userId,
      ...participantAttributes(
        userId,
        indexes,
        source.rankings.userGroupById,
      ),
      ranking: rankingByUserId.get(userId) ?? null,
    }));
  exactSet(new Set(participants.map((participant) => participant.userId)), requiredParticipantIds, "participant");

  const totalScore = sumDecimals(problems.map((problem) => problem.maxScore), "exam.totalScore");

  const snapshot: PintiaSnapshotV2 = {
    schema: SNAPSHOT_SCHEMA,
    schemaSha256,
    exporter: {
      name: EXPORTER_NAME,
      version: exporterVersion,
      exportedAt: utcDateTime(exportedAt, "exporter.exportedAt"),
    },
    exam: {
      platform: "pintia",
      problemSetId,
      title: requiredNonWhitespaceString(source.problems.metadata.title, "exam.title"),
      sourceUrl: sourceUrl.href,
      startsAt: nullableUTCDateTime(source.problems.metadata.startsAt, "exam.startsAt"),
      endsAt: nullableUTCDateTime(source.problems.metadata.endsAt, "exam.endsAt"),
      totalScore,
    },
    problems: problems.sort((left, right) =>
      left.problemSetProblemId.localeCompare(right.problemSetProblemId)),
    participants,
    submissions,
    completeness: {
      problems: {
        sourceReportedCount: source.problems.sourceReportedCount,
        observedCount: source.problems.observedCount,
        exportedCount: problems.length,
        paginationExhausted: true,
      },
      rankings: {
        sourceReportedCount: source.rankings.sourceReportedCount,
        observedCount: source.rankings.observedCount,
        exportedCount: source.rankings.items.length,
        paginationExhausted: true,
      },
      submissions: {
        sourceReportedCount: source.submissions.sourceReportedCount,
        observedCount: source.submissions.observedCount,
        exportedCount: submissions.length,
        paginationExhausted: true,
      },
      participants: {
        exportedCount: participants.length,
      },
    },
  };

  await validateSnapshot(snapshot, options.signal);
  return snapshot;
}

export async function validateSnapshot(snapshot: PintiaSnapshotV2, signal?: AbortSignal): Promise<void> {
  signal?.throwIfAborted();
  validateAuthoritativeSnapshotSchema(snapshot);
  validateSnapshotOperationalLimits(snapshot);
  if (snapshot.schema !== SNAPSHOT_SCHEMA || snapshot.schemaSha256 !== SCHEMA_SHA256) {
    throw new Error("Snapshot schema identity is invalid.");
  }
  if (snapshot.problems.length === 0) {
    throw new Error("Snapshot must contain at least one programming problem.");
  }
  requiredNonWhitespaceString(snapshot.exam.title, "exam.title");
  snapshot.problems.forEach((problem, index) => {
    requiredNonWhitespaceString(problem.title, `problems[${index}].title`);
  });
  expectUnique(snapshot.problems.map((problem) => problem.problemSetProblemId), "problemSetProblemId");
  expectUnique(snapshot.problems.map((problem) => problem.problemId), "problemId");
  expectUnique(snapshot.participants.map((participant) => participant.userId), "participant userId");
  expectUnique(snapshot.submissions.map((submission) => submission.submissionId), "submissionId");

  const problemIds = new Set(snapshot.problems.map((problem) => problem.problemSetProblemId));
  const participantIds = new Set(snapshot.participants.map((participant) => participant.userId));
  const requiredParticipantIds = new Set<string>();
  for (const participant of snapshot.participants) {
    if (participant.studentNumber !== null) {
      nullableNonWhitespaceString(participant.studentNumber, `participant ${participant.userId}.studentNumber`);
    }
    if (participant.ranking !== null) {
      requiredParticipantIds.add(participant.userId);
      expectUnique(
        participant.ranking.problemResults.map((result) => result.problemSetProblemId),
        `ranking problem reference for ${participant.userId}`,
      );
      participant.ranking.problemResults.forEach((result) => {
        if (!problemIds.has(result.problemSetProblemId)) {
          throw new Error(`Participant ${participant.userId} ranks an unknown problem.`);
        }
      });
    }
  }
  for (const submission of snapshot.submissions) {
    signal?.throwIfAborted();
    requiredNonWhitespaceString(submission.verdict, `submission ${submission.submissionId}.verdict`);
    requiredParticipantIds.add(submission.userId);
    if (!problemIds.has(submission.problemSetProblemId) || !participantIds.has(submission.userId)) {
      throw new Error(`Submission ${submission.submissionId} has a dangling reference.`);
    }
    expectUnique(submission.caseResults.map((result) => result.caseId), `caseId for ${submission.submissionId}`);
    if (submission.code.length === 0) {
      throw new Error(`Submission ${submission.submissionId} has an empty program.`);
    }
    if ((await sha256Utf8(submission.code, signal)) !== submission.codeSha256) {
      throw new Error(`Submission ${submission.submissionId} has an invalid codeSha256.`);
    }
  }
  exactSet(participantIds, requiredParticipantIds, "participant");

  const completenessChecks: Array<[string, CollectionCompletenessLike, number]> = [
    ["problems", snapshot.completeness.problems, snapshot.problems.length],
    [
      "rankings",
      snapshot.completeness.rankings,
      snapshot.participants.filter((participant) => participant.ranking !== null).length,
    ],
    ["submissions", snapshot.completeness.submissions, snapshot.submissions.length],
  ];
  for (const [name, completeness, exportedLength] of completenessChecks) {
    if (
      completeness.paginationExhausted !== true ||
      completeness.exportedCount !== exportedLength ||
      completeness.observedCount < completeness.exportedCount ||
      (completeness.sourceReportedCount !== null &&
        completeness.sourceReportedCount !== completeness.observedCount)
    ) {
      throw new Error(`${name} completeness is invalid.`);
    }
  }
  if (snapshot.completeness.participants.exportedCount !== snapshot.participants.length) {
    throw new Error("participants completeness is invalid.");
  }
}

interface CollectionCompletenessLike {
  sourceReportedCount: number | null;
  observedCount: number;
  exportedCount: number;
  paginationExhausted: true;
}
