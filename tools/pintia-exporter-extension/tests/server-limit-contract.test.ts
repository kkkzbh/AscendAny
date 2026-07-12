import { readFile } from "node:fs/promises";
import { describe, expect, it } from "vitest";
import {
  DETAIL_REQUEST_SPACING_MS,
  DOWNLOAD_CLEANUP_TIMEOUT_MS,
  DOWNLOAD_TERMINAL_TIMEOUT_MS,
  DOWNLOAD_UNSAFE_TIMEOUT_MS,
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
  WHOLE_EXPORT_TIMEOUT_MS,
} from "../src/domain/limits";

function parseEnvironment(source: string): ReadonlyMap<string, number> {
  const entries = source.split(/\r?\n/).flatMap((line): Array<[string, number]> => {
    const match = /^([A-Z0-9_]+)=([0-9]+)$/.exec(line);
    return match === null ? [] : [[match[1] as string, Number(match[2])]];
  });
  return new Map(entries);
}

describe("production Go importer limit contract", () => {
  it("keeps two maximum-size 90 ms detail pacing passes within half the export deadline", () => {
    const twoPassPacingMilliseconds = 2 * MAX_SUBMISSIONS * DETAIL_REQUEST_SPACING_MS;

    expect(MAX_SUBMISSIONS).toBe(20_000);
    expect(DETAIL_REQUEST_SPACING_MS).toBe(90);
    expect(twoPassPacingMilliseconds).toBe(60 * 60_000);
    expect(twoPassPacingMilliseconds).toBeLessThanOrEqual(WHOLE_EXPORT_TIMEOUT_MS / 2);
  });

  it("keeps the persistent download lease through terminal wait and bounded cleanup", () => {
    expect(DOWNLOAD_UNSAFE_TIMEOUT_MS).toBe(
      DOWNLOAD_TERMINAL_TIMEOUT_MS + DOWNLOAD_CLEANUP_TIMEOUT_MS,
    );
  });

  it("matches every deployed Pintia preflight cap", async () => {
    const configuration = parseEnvironment(await readFile(
      new URL("../../../deploy/v2/config/ascendanyd.env.example", import.meta.url),
      "utf8",
    ));
    const expected = new Map<string, number>([
      ["ASCENDANY_ARTIFACT_MAX_BYTES", MAX_SNAPSHOT_BYTES],
      ["ASCENDANY_PINTIA_MAX_TOTAL_NODES", MAX_TOTAL_NODES],
      ["ASCENDANY_PINTIA_MAX_TOTAL_STRING_BYTES", MAX_TOTAL_STRING_BYTES],
      ["ASCENDANY_PINTIA_MAX_JSON_DEPTH", MAX_JSON_DEPTH],
      ["ASCENDANY_PINTIA_MAX_STRING_BYTES", MAX_STRING_BYTES],
      ["ASCENDANY_PINTIA_MAX_PROBLEMS", MAX_PROBLEMS],
      ["ASCENDANY_PINTIA_MAX_PARTICIPANTS", MAX_PARTICIPANTS],
      ["ASCENDANY_PINTIA_MAX_PROBLEM_RESULTS_PER_RANKING", MAX_PROBLEM_RESULTS_PER_RANKING],
      ["ASCENDANY_PINTIA_MAX_SUBMISSIONS", MAX_SUBMISSIONS],
      ["ASCENDANY_PINTIA_MAX_CASE_RESULTS_PER_SUBMISSION", MAX_CASE_RESULTS_PER_SUBMISSION],
      ["ASCENDANY_PINTIA_MAX_CODE_BYTES", MAX_CODE_BYTES],
    ]);

    for (const [name, value] of expected) {
      expect(configuration.get(name), name).toBe(value);
    }
  });

  it("proves the online worker maps artifact bytes and every Pintia cap into the Go validator", async () => {
    const runtime = await readFile(
      new URL("../../../backend/internal/runtimeapp/runtime.go", import.meta.url),
      "utf8",
    );
    expect(runtime).toContain("MaxTotalBytes:               configuration.Artifact.MaxBytes");
    for (const field of [
      "MaxTotalNodes",
      "MaxTotalStringBytes",
      "MaxJSONDepth",
      "MaxStringBytes",
      "MaxProblems",
      "MaxParticipants",
      "MaxProblemResultsPerRanking",
      "MaxSubmissions",
      "MaxCaseResultsPerSubmission",
      "MaxCodeBytes",
    ]) {
      expect(runtime).toContain(`${field}:`);
      expect(runtime).toContain(`configuration.Pintia.${field}`);
    }
  });
});
