export const SNAPSHOT_SCHEMA = "ascendany.pintia.snapshot.v2" as const;
export const EXPORTER_NAME = "ascendany-pintia-exporter" as const;
export const EXPORTER_VERSION = "2.2.3" as const;
export const EXPORT_TASK_FORMAT_VERSION = 4 as const;
export const EXPORT_COORDINATION_JOURNAL_ID = "global" as const;
export const SCHEMA_SHA256 = "85b8277dc4485019499ff3bcceb1715ea73f58197ebdff9487c9a5fb8f3ccdfa" as const;

export type Nullable<T> = T | null;
export type JsonObject = Record<string, unknown>;

export interface CollectionCompleteness {
  sourceReportedCount: number | null;
  observedCount: number;
  exportedCount: number;
  paginationExhausted: true;
}

export interface PintiaSnapshotV2 {
  schema: typeof SNAPSHOT_SCHEMA;
  schemaSha256: string;
  exporter: {
    name: typeof EXPORTER_NAME;
    version: string;
    exportedAt: string;
  };
  exam: {
    platform: "pintia";
    problemSetId: string;
    title: string;
    sourceUrl: string;
    startsAt: string | null;
    endsAt: string | null;
    totalScore: number | null;
  };
  problems: PintiaProblem[];
  participants: PintiaParticipant[];
  submissions: PintiaSubmission[];
  completeness: {
    problems: CollectionCompleteness;
    rankings: CollectionCompleteness;
    submissions: CollectionCompleteness;
    participants: {
      exportedCount: number;
    };
  };
}

export interface PintiaProblem {
  problemSetProblemId: string;
  problemId: string;
  label: string | null;
  title: string;
  type: "PROGRAMMING";
  maxScore: number | null;
  contentHtml: string | null;
  timeLimitMs: number | null;
  memoryLimitBytes: number | null;
}

export interface PintiaParticipant {
  userId: string;
  studentUserId: string | null;
  studentNumber: string | null;
  /** Exact nullable PTA user.nickname captured for this participant. */
  displayName: string | null;
  groupName: string | null;
  ranking: PintiaRanking | null;
}

export interface PintiaRanking {
  rank: number;
  totalScore: number | null;
  timeUsedSeconds: number | null;
  problemResults: PintiaRankingProblemResult[];
}

export interface PintiaRankingProblemResult {
  problemSetProblemId: string;
  score: number | null;
  passed: boolean | null;
  validSubmissionCount: number | null;
  acceptTimeSeconds: number;
}

export interface PintiaSubmission {
  submissionId: string;
  problemSetProblemId: string;
  userId: string;
  submittedAt: string;
  language: string | null;
  compiler: string | null;
  verdict: string;
  score: number | null;
  timeMs: number | null;
  memoryBytes: number | null;
  code: string;
  codeSha256: string;
  compileLog: string | null;
  caseResults: PintiaCaseResult[];
}

export interface PintiaCaseResult {
  caseId: string;
  verdict: string | null;
  score: number | null;
  timeMs: number | null;
  memoryBytes: number | null;
  message: string | null;
}

export interface ExhaustiveCollection<T> {
  items: T[];
  sourceReportedCount: number | null;
  observedCount: number;
  paginationExhausted: true;
}

export interface ProblemCollection extends ExhaustiveCollection<JsonObject> {
  metadata: ProblemSetMetadataSource;
}

export interface ProblemSetMetadataSource {
  problemSetId: string;
  title: string;
  startsAt: string | null;
  endsAt: string | null;
}

export interface RankingCollection extends ExhaustiveCollection<JsonObject> {
  studentUserById: Record<string, JsonObject>;
  userById: Record<string, JsonObject>;
  userGroupById: Record<string, JsonObject>;
}

export interface SubmissionIndexes {
  examMemberByUserId: Record<string, JsonObject>;
  studentUserById: Record<string, JsonObject>;
  userById: Record<string, JsonObject>;
}

export interface SubmissionCollection extends ExhaustiveCollection<JsonObject> {
  indexes: SubmissionIndexes;
}

export interface SubmissionDetailSource {
  submissionId: string;
  code: string;
  compileLog: string | null;
  testcaseJudgeResults: JsonObject;
}

export type CollectorName = "problems" | "rankings" | "submissions" | "submission-details";

export interface CollectorRequest {
  type: "ASCENDANY_COLLECT_PINTIA_ROUTE_V2";
  problemSetId: string;
  collector: CollectorName;
  submissionIds?: string[];
  limits: {
    maxProblems: number;
    maxParticipants: number;
    maxProblemResultsPerRanking: number;
    maxSubmissions: number;
    maxCaseResultsPerSubmission: number;
    maxDetailBatchSize: number;
    maxCodeBytes: number;
    maxStringBytes: number;
    maxTotalStringBytes: number;
    maxTotalNodes: number;
    maxJsonDepth: number;
    apiCallTimeoutMs: number;
    collectionTimeoutMs: number;
    detailBatchTimeoutMs: number;
    detailBatchConcurrency: number;
  };
}

export interface RateLimitedCollectorFailure {
  kind: "rate_limited";
  status: 429;
  message: string;
}

export interface HttpCollectorFailure {
  kind: "http";
  status: number;
  message: string;
}

export interface InternalCollectorFailure {
  kind: "collector";
  message: string;
}

export type CollectorFailure =
  | RateLimitedCollectorFailure
  | HttpCollectorFailure
  | InternalCollectorFailure;

export type CollectorResponse =
  | {
    ok: true;
    collector: CollectorName;
    result: unknown;
  }
  | {
    ok: false;
    collector: CollectorName;
    failure: CollectorFailure;
  };

export type ExportCoordinatorState = "active" | "recovering" | "completed" | "failed";

export interface ExportCollectorOperationJournal {
  operationId: string;
  collector: CollectorName;
  startedAtMs: number;
  absoluteDeadlineAtMs: number;
  unsafeUntilMs: number;
  state: "running" | "settled";
  error: string | null;
}

export interface ExportNavigationOperationJournal {
  operationId: string;
  tabId: number;
  targetUrl: string;
  recoveryUrl: string;
  startedAtMs: number;
  unsafeUntilMs: number;
  state: "running" | "settled";
  error: string | null;
}

export interface ExportBlobResourceJournal {
  requestId: string;
  fileName: string;
  expectedBytes: number;
  unsafeUntilMs: number;
  state: "writing" | "creating" | "live" | "revoking";
}

export interface ExportDownloadResourceJournal {
  identity: string;
  filename: string;
  downloadId: number | null;
  unsafeUntilMs: number;
  state: "starting" | "in_progress" | "cancelling" | "complete" | "interrupted";
}

export interface ExportResourceJournal {
  blob: ExportBlobResourceJournal | null;
  download: ExportDownloadResourceJournal | null;
}

export interface ExportRecoveryJournal {
  recoveryId: string;
  claimedAtMs: number;
  unsafeUntilMs: number;
}

export interface ExportCoordinationJournal {
  id: typeof EXPORT_COORDINATION_JOURNAL_ID;
  formatVersion: typeof EXPORT_TASK_FORMAT_VERSION;
  generation: string;
  taskId: string;
  problemSetId: string;
  state: ExportCoordinatorState;
  acquiredAtMs: number;
  updatedAtMs: number;
  navigation: ExportNavigationOperationJournal | null;
  collector: ExportCollectorOperationJournal | null;
  resources: ExportResourceJournal;
  recovery: ExportRecoveryJournal | null;
  finalError: string | null;
}

export interface SnapshotSource {
  problemSetId: string;
  sourceUrl: string;
  problems: ProblemCollection;
  rankings: RankingCollection;
  submissions: SubmissionCollection;
  submissionDetailsById: Record<string, SubmissionDetailSource>;
}

export type ExportStage =
  | "starting"
  | "problems"
  | "rankings"
  | "submissions"
  | "submission-details"
  | "restoring"
  | "validating"
  | "downloading"
  | "failed";

export interface ExportProgress {
  phase: ExportStage;
  totalSubmissions: number;
  completedDetails: number;
  pendingDetails: number;
  detailPass: number;
  percent: number;
  queueConcurrency?: number;
  requestSpacingMs?: number;
  lastCheckpointCompleted?: number;
  lastCheckpointAtMs?: number;
}

export interface ExportBudgetLedger {
  nodes: number;
  stringBytes: number;
  maximumDepth: number;
}

export interface ExportFailure {
  submissionId: string;
  error: string;
  attempts: number;
}

export interface ExportTask {
  schema: typeof SNAPSHOT_SCHEMA;
  taskFormatVersion: typeof EXPORT_TASK_FORMAT_VERSION;
  taskId: string;
  generation: string;
  problemSetId: string;
  tabId: number;
  origin: "https://pintia.cn";
  sourceUrl: string;
  originalUrl: string;
  status: "running" | "failed";
  stage: ExportStage;
  error: string | null;
  createdAt: string;
  updatedAt: string;
  captureAttempt: number;
  progress: ExportProgress;
  logs: string[];
  failures: ExportFailure[];
  budget: ExportBudgetLedger;
  parts: {
    problems: ProblemCollection | null;
    rankings: RankingCollection | null;
    submissions: {
      collection: SubmissionCollection | null;
      programmingItems: JsonObject[];
      submissionDetailsById: Record<string, SubmissionDetailSource>;
    };
  };
}
