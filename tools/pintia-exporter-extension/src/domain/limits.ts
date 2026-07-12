export const MAX_SNAPSHOT_BYTES = 128 * 1024 * 1024;
export const MAX_TOTAL_NODES = 2_000_000;
export const MAX_TOTAL_STRING_BYTES = 32 * 1024 * 1024;
export const MAX_JSON_DEPTH = 32;
export const MAX_STRING_BYTES = 8 * 1024 * 1024;
export const MAX_PROBLEMS = 1_000;
export const MAX_PARTICIPANTS = 20_000;
export const MAX_PROBLEM_RESULTS_PER_RANKING = 1_000;
export const MAX_SUBMISSIONS = 20_000;
export const MAX_CASE_RESULTS_PER_SUBMISSION = 1_000;
export const MAX_CODE_BYTES = 1 * 1024 * 1024;
export const MIN_POSITIVE_DECIMAL = 1e-100;
export const MAX_DECIMAL = 1e100;

export const API_CALL_TIMEOUT_MS = 20_000;
export const COLLECTION_TIMEOUT_MS = 10 * 60_000;
export const DETAIL_BATCH_TIMEOUT_MS = 2 * 60_000;
export const EXECUTE_SCRIPT_GRACE_MS = 5_000;
export const NAVIGATION_UNSAFE_TIMEOUT_MS = 65_000;
export const WHOLE_EXPORT_TIMEOUT_MS = 2 * 60 * 60_000;
// A timed-out export keeps its lease long enough for the longest MAIN-world
// collector to reach its own deterministic deadline before another export can
// inject into the same Pintia session.
export const EXPORT_CANCELLATION_GRACE_MS = COLLECTION_TIMEOUT_MS + EXECUTE_SCRIPT_GRACE_MS;
export const DOWNLOAD_TERMINAL_TIMEOUT_MS = 5 * 60_000;
export const DOWNLOAD_CANCEL_TIMEOUT_MS = 5_000;
export const DOWNLOAD_CLEANUP_TIMEOUT_MS = 30_000;
export const DOWNLOAD_UNSAFE_TIMEOUT_MS = DOWNLOAD_TERMINAL_TIMEOUT_MS + DOWNLOAD_CLEANUP_TIMEOUT_MS;
export const CHECKPOINT_OPERATION_TIMEOUT_MS = 30_000;
export const SNAPSHOT_BLOB_CREATE_TIMEOUT_MS = 10 * 60_000;
export const EXPORT_RECOVERY_TIMEOUT_MS = 10 * 60_000;
export const MAX_DETAIL_BATCH_SIZE = 32;
export const DETAIL_BATCH_SIZE = 8;
export const DETAIL_BATCH_CONCURRENCY = 8;
export const DETAIL_REQUEST_SPACING_MS = 90;

export const EXPORT_LIMITS = Object.freeze({
  maxProblems: MAX_PROBLEMS,
  maxParticipants: MAX_PARTICIPANTS,
  maxProblemResultsPerRanking: MAX_PROBLEM_RESULTS_PER_RANKING,
  maxSubmissions: MAX_SUBMISSIONS,
  maxCaseResultsPerSubmission: MAX_CASE_RESULTS_PER_SUBMISSION,
  maxDetailBatchSize: MAX_DETAIL_BATCH_SIZE,
  maxCodeBytes: MAX_CODE_BYTES,
  maxStringBytes: MAX_STRING_BYTES,
  maxTotalStringBytes: MAX_TOTAL_STRING_BYTES,
  maxTotalNodes: MAX_TOTAL_NODES,
  maxJsonDepth: MAX_JSON_DEPTH,
  apiCallTimeoutMs: API_CALL_TIMEOUT_MS,
  collectionTimeoutMs: COLLECTION_TIMEOUT_MS,
  detailBatchTimeoutMs: DETAIL_BATCH_TIMEOUT_MS,
  detailBatchConcurrency: DETAIL_BATCH_CONCURRENCY,
});

export function utf8ByteLength(value: string): number {
  let bytes = 0;
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);
    if (codeUnit <= 0x7f) {
      bytes += 1;
      continue;
    }
    if (codeUnit <= 0x7ff) {
      bytes += 2;
      continue;
    }
    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const next = value.charCodeAt(index + 1);
      if (next >= 0xdc00 && next <= 0xdfff) {
        bytes += 4;
        index += 1;
        continue;
      }
    }
    bytes += 3;
  }
  return bytes;
}
