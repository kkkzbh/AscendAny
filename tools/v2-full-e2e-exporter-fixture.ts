import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import {
  buildSnapshot,
  validateSnapshot,
} from "./pintia-exporter-extension/src/domain/normalize.ts";
import type { SnapshotSource } from "./pintia-exporter-extension/src/domain/types.ts";

function paths(argv: string[]): { source: string; output: string } {
  if (
    argv.length !== 4
    || argv[0] !== "--source"
    || argv[2] !== "--output"
    || !path.isAbsolute(argv[1] ?? "")
    || !path.isAbsolute(argv[3] ?? "")
  ) {
    throw new Error(
      "usage: v2-full-e2e-exporter-fixture --source /absolute/source.json --output /absolute/snapshot.json",
    );
  }
  const source = path.normalize(argv[1]);
  const output = path.normalize(argv[3]);
  if (
    source !== argv[1]
    || output !== argv[3]
    || source === path.parse(source).root
    || output === path.parse(output).root
    || source === output
  ) {
    throw new Error("source and output must be distinct canonical absolute file paths");
  }
  return { source, output };
}

const io = paths(process.argv.slice(2));
const source = JSON.parse(await readFile(io.source, "utf8")) as SnapshotSource;
const snapshot = await buildSnapshot(source, "2026-02-03T04:05:06.000Z");
await validateSnapshot(snapshot);
const bytes = JSON.stringify(snapshot);
await writeFile(io.output, bytes, { encoding: "utf8", flag: "wx", mode: 0o600 });

process.stdout.write(`${JSON.stringify({
  schema: "ascendany.full-e2e.exporter-fixture.v1",
  snapshotSchema: snapshot.schema,
  problemCount: snapshot.problems.length,
  participantCount: snapshot.participants.length,
  submissionCount: snapshot.submissions.length,
})}\n`);
