import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import Ajv2020 from "ajv/dist/2020.js";
import standaloneCode from "ajv/dist/standalone/index.js";
import addFormats from "ajv-formats";

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = resolve(packageRoot, "../..");
const schemaPath = resolve(repositoryRoot, "contracts/pintia/ascendany.pintia.snapshot.v2.schema.json");
const outputDirectory = resolve(packageRoot, ".generated");
const outputPath = resolve(outputDirectory, "authoritative-schema-validator.mjs");
const declarationPath = resolve(outputDirectory, "authoritative-schema-validator.d.mts");
const metadataPath = resolve(outputDirectory, "authoritative-schema-validator.meta.json");

export async function generateAuthoritativeSchemaValidator() {
  const schemaBytes = await readFile(schemaPath);
  const schema = JSON.parse(schemaBytes.toString("utf8"));
  const ajv = new Ajv2020({
    allErrors: true,
    strict: true,
    code: { source: true, esm: true, optimize: true },
  });
  addFormats(ajv);
  const validate = ajv.compile(schema);
  const generated = `${standaloneCode(ajv, validate)}\n`;
  if (/\bnew Function\b|\beval\s*\(/.test(generated)) {
    throw new Error("Generated Pintia validator contains runtime code generation forbidden by extension CSP.");
  }
  const digest = createHash("sha256").update(schemaBytes).digest("hex");
  const generatedDigest = createHash("sha256").update(generated).digest("hex");
  const declaration = `export interface StandaloneValidationError {
  instancePath: string;
  schemaPath: string;
  keyword: string;
  params: Record<string, unknown>;
  message?: string;
}
export interface StandaloneValidator {
  (value: unknown): boolean;
  errors?: StandaloneValidationError[] | null;
}
declare const validate: StandaloneValidator;
export { validate };
export default validate;
`;
  const metadata = `${JSON.stringify({ schemaSha256: digest, generatedSha256: generatedDigest }, null, 2)}\n`;
  await mkdir(outputDirectory, { recursive: true });
  await Promise.all([
    writeFile(outputPath, generated, "utf8"),
    writeFile(declarationPath, declaration, "utf8"),
    writeFile(metadataPath, metadata, "utf8"),
  ]);
  return { schemaSha256: digest, generatedSha256: generatedDigest };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  await generateAuthoritativeSchemaValidator();
}
