import { mkdtemp, readdir, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createClient } from "@hey-api/openapi-ts";
import { generateRuntimeContracts } from "./runtime-contracts-generator.mjs";

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const committedDirectory = resolve(packageRoot, "src/generated");
const temporaryRoot = await mkdtemp(join(tmpdir(), "ascendany-sdk-generated-"));
const generatedDirectory = resolve(temporaryRoot, "generated");

async function files(root, directory = root) {
  const entries = await readdir(directory, { withFileTypes: true });
  const paths = [];
  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      paths.push(...await files(root, path));
    } else if (entry.isFile()) {
      paths.push(relative(root, path));
    }
  }
  return paths;
}

try {
  const { default: configPromise } = await import("../openapi-ts.config.ts");
  const config = await configPromise;
  if (
    Array.isArray(config) ||
    typeof config.input !== "string" ||
    typeof config.output !== "object" ||
    config.output === null
  ) {
    throw new Error("SDK generation consistency requires one path-based OpenAPI config.");
  }
  await createClient({
    ...config,
    input: resolve(packageRoot, config.input),
    output: {
      ...config.output,
      path: generatedDirectory,
    },
  });
  await generateRuntimeContracts(generatedDirectory);

  const committedFiles = await files(committedDirectory);
  const generatedFiles = await files(generatedDirectory);
  const allFiles = [...new Set([...committedFiles, ...generatedFiles])].sort();
  const differences = [];

  for (const path of allFiles) {
    if (!committedFiles.includes(path)) {
      differences.push(`missing committed file: ${path}`);
      continue;
    }
    if (!generatedFiles.includes(path)) {
      differences.push(`obsolete committed file: ${path}`);
      continue;
    }
    const [committed, generated] = await Promise.all([
      readFile(join(committedDirectory, path)),
      readFile(join(generatedDirectory, path)),
    ]);
    if (!committed.equals(generated)) {
      differences.push(`content differs: ${path}`);
    }
  }

  if (differences.length > 0) {
    throw new Error(
      `Generated SDK is stale. Run pnpm --filter @ascendany/sdk generate.\n${differences.join("\n")}`,
    );
  }

  process.stdout.write(`Generated SDK matches ${generatedFiles.length} files.\n`);
} finally {
  await rm(temporaryRoot, { recursive: true, force: true });
}
