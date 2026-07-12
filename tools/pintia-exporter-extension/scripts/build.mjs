import { createHash } from "node:crypto";
import { cp, mkdir, readFile, rm } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { generateAuthoritativeSchemaValidator } from "./generate-schema-validator.mjs";

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = resolve(packageRoot, "../..");
const outputDirectory = resolve(packageRoot, "dist");
const schemaPath = resolve(repositoryRoot, "contracts/pintia/ascendany.pintia.snapshot.v2.schema.json");
const expectedSchemaSha256 = "85b8277dc4485019499ff3bcceb1715ea73f58197ebdff9487c9a5fb8f3ccdfa";

const packageMetadata = JSON.parse(await readFile(resolve(packageRoot, "package.json"), "utf8"));
const manifest = JSON.parse(await readFile(resolve(packageRoot, "src/static/manifest.json"), "utf8"));
const domainTypes = await readFile(resolve(packageRoot, "src/domain/types.ts"), "utf8");
const domainVersion = domainTypes.match(/EXPORTER_VERSION = "([0-9]+[.][0-9]+[.][0-9]+)"/)?.[1];
const domainSchemaSha256 = domainTypes.match(
  /^export const SCHEMA_SHA256 = "([0-9a-f]{64})" as const;$/m,
)?.[1];
if (
  typeof packageMetadata.version !== "string" ||
  manifest.version !== packageMetadata.version ||
  domainVersion !== packageMetadata.version
) {
  throw new Error("Exporter version differs between package.json, manifest.json, and domain/types.ts");
}

const generatedValidator = await generateAuthoritativeSchemaValidator();
if (generatedValidator.schemaSha256 !== expectedSchemaSha256) {
  throw new Error(
    `Generated Pintia validator schema digest mismatch: ${generatedValidator.schemaSha256}`,
  );
}

const schemaBytes = await readFile(schemaPath);
const actualSchemaSha256 = createHash("sha256").update(schemaBytes).digest("hex");
if (
  domainSchemaSha256 !== expectedSchemaSha256 ||
  actualSchemaSha256 !== expectedSchemaSha256
) {
  throw new Error(
    "Pintia v2 schema digest mismatch: " +
      `expected ${expectedSchemaSha256}, ` +
      `domain ${domainSchemaSha256 ?? "missing"}, ` +
      `actual ${actualSchemaSha256}`,
  );
}

await rm(outputDirectory, { recursive: true, force: true });
await mkdir(outputDirectory, { recursive: true });

await build({
  absWorkingDir: packageRoot,
  entryPoints: {
    background: "src/background.ts",
    offscreen: "src/offscreen.ts",
    popup: "src/popup.ts",
    progress: "src/progress.ts",
  },
  outdir: outputDirectory,
  bundle: true,
  format: "iife",
  platform: "browser",
  target: "chrome120",
  charset: "utf8",
  legalComments: "none",
  sourcemap: false,
  minify: false,
  treeShaking: true,
  logLevel: "info",
});

for (const bundleName of ["background.js", "offscreen.js", "popup.js", "progress.js"]) {
  const bundle = await readFile(resolve(outputDirectory, bundleName), "utf8");
  if (/\bnew Function\b|\beval\s*\(/.test(bundle)) {
    throw new Error(`${bundleName} contains runtime code generation forbidden by extension CSP.`);
  }
}

for (const name of ["manifest.json", "offscreen.html", "popup.html", "progress.html", "popup.css", "progress.css"]) {
  await cp(resolve(packageRoot, "src/static", name), resolve(outputDirectory, name));
}
