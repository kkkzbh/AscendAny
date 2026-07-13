import { createHash, randomBytes } from "node:crypto";
import { constants as fsConstants } from "node:fs";
import {
  lstat,
  mkdir,
  open,
  readdir,
  realpath,
  rename,
  rm,
} from "node:fs/promises";
import { basename, dirname, isAbsolute, join } from "node:path";
import { pathToFileURL } from "node:url";
import {
  authorizeKnowledgeCatalogPublication,
  consumeEnrollmentClaim,
  createClient,
  createPintiaImport,
  getCapabilities,
  getConfiguration,
  getExamAnalysisGeneration,
  getImportJob,
  getLiveness,
  getReadiness,
  getRecommendationReviewContext,
  getSelfRecommendation,
  getSelfStudentAnalytics,
  getStudentLeaderboard,
  getVersion,
  issueEnrollmentClaim,
  listAuditEvents,
  listExams,
  listImportJobs,
  listManagedStudents,
  loginAccount,
  revokeEnrollmentClaim,
  type AuditEvent,
  type AuthorizedCatalogPublicationRequest,
  type AuthSession,
  type CatalogPublicationAuthorizationResult,
  type CatalogPublicationIntent,
  type ExamAnalysisGeneration,
  type ExamSummary,
  type ImportJob,
  type ManagedStudent,
  type RecommendationKnowledgeCatalogV1,
  type RecommendationReviewContext,
  type Version,
} from "../packages/sdk/src/index";

const requestSchema = "ascendany.knowledge_catalog.publication-request.v1";
const publicationReceiptSchema = "ascendany.knowledge_catalog.publication-receipt.v1";
const prepareReceiptSchema = "ascendany.production-initialization.prepare-receipt.v1";
const verifyReceiptSchema = "ascendany.production-initialization.verify-receipt.v1";
const catalogKey = "recommendation.catalog.active";
const catalogSchema = "ascendany.knowledge_catalog.recommendation.v1";
const requestCredentialName = "catalog_publication_request";
const accessTokenCredentialName = "admin_access_token";
const snapshotMediaType = "application/vnd.ascendany.pintia.snapshot.v2+json";
const maximumSnapshotBytes = 64 * 1024 * 1024;
const maximumCatalogBytes = 256 * 1024;
const maximumPageItems = 100;
const paginationDelayMilliseconds = 600;
const minimumPublicationAccessValidityMilliseconds = 5 * 60 * 1000;
const sha256Pattern = /^[0-9a-f]{64}$/u;
const commitPattern = /^[0-9a-f]{40}$/u;
const semanticVersionPattern = /^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/u;
const usernamePattern = /^[a-z0-9_]{3,32}$/u;
const canonicalUUIDv4Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const canonicalPositiveInt64Pattern = /^[1-9][0-9]{0,18}$/u;
const canonicalRFC3339NanoUTCPattern = /^(?:[0-9]{4})-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]{1,9})?Z$/u;
const terminalImportStatuses = new Set<ImportJob["status"]>([
  "succeeded",
  "failed",
  "superseded",
]);
const terminalAnalyticsStatuses = new Set<ExamAnalysisGeneration["status"]>([
  "succeeded",
  "failed",
  "superseded",
]);

const catalogPublicationIntentKeys = [
  "expectedAnalyticsGenerationId",
  "expectedAnalyticsHeadRevision",
  "expectedConfigurationHeadRevision",
  "expectedCurrentModelArtifactSha256",
  "expectedCurrentModelHeadRevision",
  "expectedInputManifestSha256",
  "schema",
  "targetApplicationBuildTime",
  "targetApplicationCommit",
  "targetApplicationVersion",
  "targetCatalogSha256",
  "targetModelArtifactSha256",
] as const;

const authorizedCatalogPublicationRequestKeys = [
  "authorizationId",
  ...catalogPublicationIntentKeys,
].sort();

const catalogPublicationAuthorizationResultKeys = [
  "authorizationId",
  "expiresAt",
  "publicationRequest",
] as const;

type Phase = "prepare" | "verify";
type ModelPurpose = "production" | "acceptance_test";
export type DeploymentKind = "initial" | "forward";

export type DeploymentTransitionExpectation = {
  deploymentKind: DeploymentKind;
  expectedModelHeadRevision: number;
  expectedCurrentModelHeadRevision: number;
  expectedModelSHA256: string;
  expectedCurrentModelSHA256: string;
  expectedCatalogHeadRevision: number;
  expectedCurrentCatalogHeadRevision: number;
  expectedCatalogSHA256: string;
  expectedCurrentCatalogSHA256: string | null;
};

type CursorPage<T> = {
  items: T[];
  nextCursor: string | null;
};

type SnapshotImportBinding = {
  importJob: ImportJob;
  exam: ExamSummary;
};

type SnapshotIdentity = {
  schema: "ascendany.pintia.snapshot.v2";
  exam: {
    platform: "pintia";
    problemSetId: string;
    title: string;
  };
  participants: Array<{
    studentNumber: string | null;
  }>;
};

type ApplicationIdentity = {
  version: string;
  commit: string;
  buildTime: string;
};

type CommonInputs = {
  deploymentKind: DeploymentKind;
  baseUrl: string;
  origin: string;
  snapshotPath: string;
  adminPasswordPath: string;
  targetApplication: ApplicationIdentity;
  currentApplication: ApplicationIdentity;
  expectedModelPurpose: ModelPurpose;
  expectedModelSHA256: string;
  expectedModelHeadRevision: number;
  expectedCurrentModelSHA256: string;
  expectedCurrentModelHeadRevision: number;
  expectedCurrentCatalogHeadRevision: number;
  expectedCatalogHeadRevision: number;
  expectedCurrentCatalogSHA256: string | null;
  catalogPath: string;
  expectedCatalogSHA256: string;
  acceptanceStudentNumber: string;
};

type LoadedInputs = CommonInputs & {
  snapshotBytes: Buffer;
  snapshotSHA256: string;
  snapshot: SnapshotIdentity;
  studentNumbers: string[];
  adminPassword: string;
  catalog: RecommendationKnowledgeCatalogV1;
  knowledgePointIDs: string[];
};

type CatalogPublicationReceipt = {
  schema: typeof publicationReceiptSchema;
  authorizationId: string;
  knowledgeCatalogPublicationId: string;
  targetModelReleaseId: string;
  catalogSha256: string;
  modelArtifactSha256: string;
  modelId: string;
  configurationKey: typeof catalogKey;
  configurationId: string;
  expectedConfigurationHeadRevision: number;
  configurationHeadRevision: number;
  configurationVersionId: string;
  configurationVersionNumber: number;
  analyticsGenerationId: string;
  analyticsHeadRevision: number;
  inputManifestSha256: string;
  currentModelHeadRevision: number;
  currentModelArtifactSha256: string;
  targetApplicationVersion: string;
  targetApplicationCommit: string;
  targetApplicationBuildTime: string;
  publishedByAccountId: string;
  publishedBySessionId: string;
  publishedAt: string;
  auditEventId: string;
  configurationMutated: boolean;
};

const catalogPublicationReceiptKeys = [
  "analyticsGenerationId",
  "analyticsHeadRevision",
  "auditEventId",
  "authorizationId",
  "catalogSha256",
  "configurationHeadRevision",
  "configurationId",
  "configurationKey",
  "configurationMutated",
  "configurationVersionId",
  "configurationVersionNumber",
  "currentModelArtifactSha256",
  "currentModelHeadRevision",
  "expectedConfigurationHeadRevision",
  "inputManifestSha256",
  "knowledgeCatalogPublicationId",
  "modelArtifactSha256",
  "modelId",
  "publishedAt",
  "publishedByAccountId",
  "publishedBySessionId",
  "schema",
  "targetApplicationBuildTime",
  "targetApplicationCommit",
  "targetApplicationVersion",
  "targetModelReleaseId",
] as const;

const catalogPublicationAuditPayloadKeys = [
  "analyticsGenerationId",
  "analyticsHeadRevision",
  "authorizationId",
  "configurationId",
  "configurationMutated",
  "credentialRef",
  "currentModelArtifactSha256",
  "currentModelHeadRevision",
  "documentSha256",
  "expectedConfigurationHeadRevision",
  "headRevision",
  "inputManifestSha256",
  "key",
  "kind",
  "schemaId",
  "targetApplicationBuildTime",
  "targetApplicationCommit",
  "targetApplicationVersion",
  "targetCatalogSha256",
  "targetModelArtifactSha256",
  "targetModelId",
  "targetModelReleaseId",
  "versionNumber",
] as const;

function requiredEnvironment(name: string): string {
  const value = process.env[name];
  if (value === undefined || value.length === 0 || value.trim() !== value) {
    throw new Error(`${name} must contain one non-empty unpadded value`);
  }
  return value;
}

function positiveIntegerEnvironment(name: string): number {
  const raw = requiredEnvironment(name);
  if (!/^[1-9][0-9]*$/u.test(raw)) {
    throw new Error(`${name} must be one canonical positive decimal integer`);
  }
  const value = Number(raw);
  if (!Number.isSafeInteger(value)) {
    throw new Error(`${name} exceeds the JavaScript safe-integer boundary`);
  }
  return value;
}

function nonNegativeIntegerEnvironment(name: string): number {
  const raw = requiredEnvironment(name);
  if (!/^(?:0|[1-9][0-9]*)$/u.test(raw)) {
    throw new Error(`${name} must be one canonical non-negative decimal integer`);
  }
  const value = Number(raw);
  if (!Number.isSafeInteger(value)) {
    throw new Error(`${name} exceeds the JavaScript safe-integer boundary`);
  }
  return value;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(
  value: Record<string, unknown>,
  expectedKeys: readonly string[],
): boolean {
  const actualKeys = Object.keys(value).sort();
  return actualKeys.length === expectedKeys.length
    && actualKeys.every((key, index) => key === expectedKeys[index]);
}

function isSafeNonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function isSafePositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function isCanonicalPositiveInt64(value: unknown): value is string {
  return typeof value === "string"
    && canonicalPositiveInt64Pattern.test(value)
    && BigInt(value) <= 9_223_372_036_854_775_807n;
}

function isCanonicalRFC3339NanoUTC(value: unknown): value is string {
  if (typeof value !== "string" || !canonicalRFC3339NanoUTCPattern.test(value)) {
    return false;
  }
  const parsed = new Date(value);
  return Number.isFinite(parsed.valueOf())
    && parsed.getUTCFullYear() === Number(value.slice(0, 4))
    && parsed.getUTCMonth() + 1 === Number(value.slice(5, 7))
    && parsed.getUTCDate() === Number(value.slice(8, 10))
    && parsed.getUTCHours() === Number(value.slice(11, 13))
    && parsed.getUTCMinutes() === Number(value.slice(14, 16))
    && parsed.getUTCSeconds() === Number(value.slice(17, 19));
}

function validateApplicationIdentity(identity: ApplicationIdentity, label: string): void {
  if (
    !semanticVersionPattern.test(identity.version)
    || !commitPattern.test(identity.commit)
    || !isCanonicalRFC3339NanoUTC(identity.buildTime)
  ) {
    throw new Error(`${label} must contain canonical SemVer, commit, and UTC build-time values`);
  }
}

function apiErrorCode(error: unknown): string {
  if (isObject(error) && typeof error.code === "string") {
    return error.code;
  }
  return "unknown_error";
}

function assertAPIResult<T>(
  label: string,
  result: { data?: T; error?: unknown; response?: Response },
): T {
  if (result.data === undefined) {
    const status = result.response?.status ?? 0;
    throw new Error(`${label} failed with HTTP ${status} (${apiErrorCode(result.error)})`);
  }
  return result.data;
}

export function assertCatalogPublicationAuthorization(
  intent: CatalogPublicationIntent,
  accessTokenExpiresAt: string,
  value: CatalogPublicationAuthorizationResult,
  nowMilliseconds = Date.now(),
): AuthorizedCatalogPublicationRequest {
  if (
    !isObject(value)
    || !hasExactKeys(value, catalogPublicationAuthorizationResultKeys)
    || typeof value.authorizationId !== "string"
    || !canonicalUUIDv4Pattern.test(value.authorizationId)
    || typeof value.expiresAt !== "string"
    || !isCanonicalRFC3339NanoUTC(value.expiresAt)
    || value.expiresAt !== accessTokenExpiresAt
    || Date.parse(value.expiresAt) - nowMilliseconds < minimumPublicationAccessValidityMilliseconds
    || !isObject(value.publicationRequest)
    || !hasExactKeys(value.publicationRequest, authorizedCatalogPublicationRequestKeys)
    || value.publicationRequest.authorizationId !== value.authorizationId
  ) {
    throw new Error("catalog publication authorization violates its access-token-bound response contract");
  }
  const returnedIntent: Record<string, unknown> = { ...value.publicationRequest };
  delete returnedIntent.authorizationId;
  if (
    !hasExactKeys(returnedIntent, catalogPublicationIntentKeys)
    || canonicalJSON(returnedIntent) !== canonicalJSON(intent)
  ) {
    throw new Error("catalog publication authorization differs from the requested release intent");
  }
  return value.publicationRequest;
}

function canonicalJSONString(value: string): string {
  if (value.includes("\0")) {
    throw new Error("canonical JSON strings must not contain NUL");
  }
  for (let index = 0; index < value.length; index += 1) {
    const unit = value.charCodeAt(index);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = value.charCodeAt(index + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) {
        throw new Error("canonical JSON strings must not contain an unpaired surrogate");
      }
      index += 1;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) {
      throw new Error("canonical JSON strings must not contain an unpaired surrogate");
    }
  }
  return JSON.stringify(value)
    .replaceAll("<", "\\u003c")
    .replaceAll(">", "\\u003e")
    .replaceAll("&", "\\u0026")
    .replaceAll("\u2028", "\\u2028")
    .replaceAll("\u2029", "\\u2029");
}

function canonicalJSON(value: unknown): string {
  if (value === null) {
    return "null";
  }
  if (typeof value === "string") {
    return canonicalJSONString(value);
  }
  if (typeof value === "boolean") {
    return value ? "true" : "false";
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new Error("canonical JSON numbers must be finite");
    }
    const encoded = JSON.stringify(Object.is(value, -0) ? 0 : value);
    const exponentIndex = encoded.search(/[eE]/u);
    if (exponentIndex < 0) {
      return encoded;
    }
    const coefficient = encoded.slice(0, exponentIndex);
    const exponent = Number.parseInt(encoded.slice(exponentIndex + 1), 10);
    const negative = coefficient.startsWith("-");
    const unsigned = negative ? coefficient.slice(1) : coefficient;
    const decimalIndex = unsigned.indexOf(".");
    const digits = unsigned.replace(".", "");
    const expandedIndex = (decimalIndex < 0 ? unsigned.length : decimalIndex) + exponent;
    const magnitude = expandedIndex <= 0
      ? `0.${"0".repeat(-expandedIndex)}${digits}`
      : expandedIndex >= digits.length
        ? `${digits}${"0".repeat(expandedIndex - digits.length)}`
        : `${digits.slice(0, expandedIndex)}.${digits.slice(expandedIndex)}`;
    return negative ? `-${magnitude}` : magnitude;
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJSON(item)).join(",")}]`;
  }
  if (isObject(value)) {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${canonicalJSONString(key)}:${canonicalJSON(value[key])}`)
      .join(",")}}`;
  }
  throw new Error("canonical JSON contains an unsupported value");
}

function sha256(bytes: Uint8Array): string {
  return createHash("sha256").update(bytes).digest("hex");
}

function strictlyOrdered(values: readonly string[]): boolean {
  return values.length > 0 && values.every((value, index) => (
    value.length > 0 && (index === 0 || values[index - 1]! < value)
  ));
}

export function strictlyUTF8BytewiseOrdered(values: readonly string[]): boolean {
  if (values.length === 0) return false;
  try {
    return values.every((value, index) => {
      canonicalJSONString(value);
      return value.length > 0
        && (index === 0 || Buffer.compare(
          Buffer.from(values[index - 1]!, "utf8"),
          Buffer.from(value, "utf8"),
        ) < 0);
    });
  } catch {
    return false;
  }
}

async function readStableFile(
  path: string,
  maximumBytes: number,
  label: string,
  privateCredential = false,
  expectedMode?: number,
): Promise<Buffer> {
  if (!isAbsolute(path) || path.includes("\0")) {
    throw new Error(`${label} path must be absolute and contain no NUL`);
  }
  if (await realpath(path) !== path) {
    throw new Error(`${label} path must already be canonical and contain no symlink ancestry`);
  }
  const before = await lstat(path);
  const effectiveUserID = process.geteuid?.();
  if (
    !before.isFile()
    || before.nlink !== 1
    || before.size < 1
    || before.size > maximumBytes
    || (before.mode & 0o022) !== 0
    || (privateCredential && (before.mode & 0o077) !== 0)
    || (expectedMode !== undefined && (before.mode & 0o777) !== expectedMode)
    || (effectiveUserID !== undefined && effectiveUserID !== 0 && before.uid !== effectiveUserID)
  ) {
    throw new Error(`${label} must be one owned, single-link, bounded regular file${privateCredential ? " with owner-only access" : " without group/world write access"}${expectedMode === undefined ? "" : ` and exact mode 0${expectedMode.toString(8)}`}`);
  }
  const handle = await open(path, fsConstants.O_RDONLY | fsConstants.O_NOFOLLOW);
  try {
    const opened = await handle.stat();
    if (
      !opened.isFile()
      || opened.dev !== before.dev
      || opened.ino !== before.ino
      || opened.size !== before.size
      || opened.mtimeMs !== before.mtimeMs
      || opened.ctimeMs !== before.ctimeMs
    ) {
      throw new Error(`${label} changed before reading`);
    }
    const bytes = await handle.readFile();
    const after = await handle.stat();
    if (
      bytes.length !== before.size
      || after.dev !== opened.dev
      || after.ino !== opened.ino
      || after.size !== opened.size
      || after.mtimeMs !== opened.mtimeMs
      || after.ctimeMs !== opened.ctimeMs
    ) {
      throw new Error(`${label} changed while reading`);
    }
    return bytes;
  } finally {
    await handle.close();
  }
}

async function readCatalogPublicationReceipt(
  inputs: LoadedInputs,
): Promise<CatalogPublicationReceipt> {
  const path = requiredEnvironment(
    "ASCENDANY_INITIALIZATION_CATALOG_PUBLICATION_RECEIPT_PATH",
  );
  const receiptFilename = basename(path);
  const bytes = await readStableFile(path, 16 * 1024, "catalog publication receipt", false, 0o640);
  const serialized = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  let parsed: unknown;
  try {
    parsed = JSON.parse(serialized) as unknown;
  } catch {
    throw new Error("catalog publication receipt must contain one JSON object");
  }
  if (
    !isObject(parsed)
    || !hasExactKeys(parsed, catalogPublicationReceiptKeys)
    || canonicalJSON(parsed) !== serialized
  ) {
    throw new Error("catalog publication receipt must contain the exact canonical schema");
  }
  if (
    parsed.schema !== publicationReceiptSchema
    || typeof parsed.authorizationId !== "string"
    || !canonicalUUIDv4Pattern.test(parsed.authorizationId)
    || !isCanonicalPositiveInt64(parsed.knowledgeCatalogPublicationId)
    || !isCanonicalPositiveInt64(parsed.targetModelReleaseId)
    || !sha256Pattern.test(typeof parsed.catalogSha256 === "string" ? parsed.catalogSha256 : "")
    || !sha256Pattern.test(typeof parsed.modelArtifactSha256 === "string" ? parsed.modelArtifactSha256 : "")
    || typeof parsed.modelId !== "string"
    || !canonicalUUIDv4Pattern.test(parsed.modelId)
    || parsed.configurationKey !== catalogKey
    || typeof parsed.configurationId !== "string"
    || !canonicalUUIDv4Pattern.test(parsed.configurationId)
    || !isSafeNonNegativeInteger(parsed.expectedConfigurationHeadRevision)
    || !isSafePositiveInteger(parsed.configurationHeadRevision)
    || !isCanonicalPositiveInt64(parsed.configurationVersionId)
    || !isSafePositiveInteger(parsed.configurationVersionNumber)
    || !isCanonicalPositiveInt64(parsed.analyticsGenerationId)
    || !isSafePositiveInteger(parsed.analyticsHeadRevision)
    || !sha256Pattern.test(typeof parsed.inputManifestSha256 === "string" ? parsed.inputManifestSha256 : "")
    || !isSafePositiveInteger(parsed.currentModelHeadRevision)
    || !sha256Pattern.test(typeof parsed.currentModelArtifactSha256 === "string" ? parsed.currentModelArtifactSha256 : "")
    || typeof parsed.targetApplicationVersion !== "string"
    || !semanticVersionPattern.test(parsed.targetApplicationVersion)
    || typeof parsed.targetApplicationCommit !== "string"
    || !commitPattern.test(parsed.targetApplicationCommit)
    || !isCanonicalRFC3339NanoUTC(parsed.targetApplicationBuildTime)
    || typeof parsed.publishedByAccountId !== "string"
    || !canonicalUUIDv4Pattern.test(parsed.publishedByAccountId)
    || typeof parsed.publishedBySessionId !== "string"
    || !canonicalUUIDv4Pattern.test(parsed.publishedBySessionId)
    || !isCanonicalRFC3339NanoUTC(parsed.publishedAt)
    || !isCanonicalPositiveInt64(parsed.auditEventId)
    || typeof parsed.configurationMutated !== "boolean"
  ) {
    throw new Error("catalog publication receipt contains an invalid field value");
  }
  const receipt = parsed as CatalogPublicationReceipt;
  const expectedConfigurationMutated = inputs.deploymentKind === "initial"
    || inputs.expectedCatalogSHA256 !== inputs.expectedCurrentCatalogSHA256;
  if (
    receiptFilename !== `${receipt.knowledgeCatalogPublicationId}.json`
    || receipt.catalogSha256 !== inputs.expectedCatalogSHA256
    || receipt.modelArtifactSha256 !== inputs.expectedModelSHA256
    || receipt.expectedConfigurationHeadRevision !== inputs.expectedCurrentCatalogHeadRevision
    || receipt.configurationHeadRevision !== inputs.expectedCatalogHeadRevision
    || receipt.configurationVersionNumber !== inputs.expectedCatalogHeadRevision
    || receipt.currentModelHeadRevision !== inputs.expectedCurrentModelHeadRevision
    || receipt.currentModelArtifactSha256 !== inputs.expectedCurrentModelSHA256
    || receipt.targetApplicationVersion !== inputs.targetApplication.version
    || receipt.targetApplicationCommit !== inputs.targetApplication.commit
    || receipt.targetApplicationBuildTime !== inputs.targetApplication.buildTime
    || receipt.configurationMutated !== expectedConfigurationMutated
    || receipt.configurationHeadRevision !== receipt.expectedConfigurationHeadRevision
      + (receipt.configurationMutated ? 1 : 0)
  ) {
    throw new Error("catalog publication receipt differs from the selected release transition");
  }
  return receipt;
}

async function validateOutputTarget(target: string, label: string): Promise<void> {
  if (!isAbsolute(target) || target.includes("\0") || basename(target) !== basename(target).trim()) {
    throw new Error(`${label} must be one absolute normalized directory path`);
  }
  const parent = dirname(target);
  if (await realpath(parent) !== parent) {
    throw new Error(`${label} parent must already be canonical and contain no symlink ancestry`);
  }
  const parentInfo = await lstat(parent);
  const effectiveUserID = process.geteuid?.();
  if (
    !parentInfo.isDirectory()
    || (parentInfo.mode & 0o777) !== 0o700
    || (effectiveUserID !== undefined && parentInfo.uid !== effectiveUserID)
  ) {
    throw new Error(`${label} parent must be an owned mode-0700 real directory`);
  }
  try {
    await lstat(target);
  } catch (error) {
    if (isObject(error) && error.code === "ENOENT") {
      return;
    }
    throw error;
  }
  throw new Error(`${label} already exists`);
}

async function syncDirectory(path: string): Promise<void> {
  const handle = await open(path, fsConstants.O_RDONLY | fsConstants.O_DIRECTORY);
  try {
    await handle.sync();
  } finally {
    await handle.close();
  }
}

async function writeCredentialFile(path: string, bytes: Uint8Array): Promise<void> {
  const handle = await open(
    path,
    fsConstants.O_WRONLY | fsConstants.O_CREAT | fsConstants.O_EXCL | fsConstants.O_NOFOLLOW,
    0o600,
  );
  try {
    await handle.writeFile(bytes);
    await handle.sync();
    const info = await handle.stat();
    if (!info.isFile() || info.nlink !== 1 || (info.mode & 0o777) !== 0o600 || info.size !== bytes.length) {
      throw new Error(`credential output has invalid metadata: ${basename(path)}`);
    }
  } finally {
    await handle.close();
  }
}

async function writeCredentialDirectory(
  target: string,
  files: ReadonlyArray<readonly [string, Uint8Array]>,
  label: string,
): Promise<void> {
  await validateOutputTarget(target, label);
  const parent = dirname(target);
  const staging = join(parent, `.${basename(target)}.staging-${randomBytes(16).toString("hex")}`);
  await mkdir(staging, { mode: 0o700 });
  try {
    for (const [name, bytes] of files) {
      if (!/^[a-z0-9_]+$/u.test(name) || bytes.length < 1) {
        throw new Error(`${label} contains an invalid credential entry`);
      }
      await writeCredentialFile(join(staging, name), bytes);
    }
    await syncDirectory(staging);
    await rename(staging, target);
    await syncDirectory(parent);
  } catch (error) {
    await rm(staging, { recursive: true, force: true });
    throw error;
  }
}

async function readAcceptanceCredentials(target: string): Promise<{
  username: string;
  password: string;
  studentNumber: string;
}> {
  if (!isAbsolute(target) || target.includes("\0") || await realpath(target) !== target) {
    throw new Error("acceptance credential directory must be one canonical absolute real path");
  }
  const info = await lstat(target);
  const effectiveUserID = process.geteuid?.();
  if (
    !info.isDirectory()
    || (info.mode & 0o777) !== 0o700
    || (effectiveUserID !== undefined && info.uid !== effectiveUserID)
  ) {
    throw new Error("acceptance credential directory must be an owned mode-0700 real directory");
  }
  const entries = await readdir(target, { withFileTypes: true });
  const names = entries.map((entry) => entry.name).sort();
  const expectedNames = ["password", "student_number", "username"];
  if (
    names.length !== expectedNames.length
    || names.some((name, index) => name !== expectedNames[index])
    || entries.some((entry) => !entry.isFile())
  ) {
    throw new Error("acceptance credential directory entry set is noncanonical");
  }
  const [usernameBytes, passwordBytes, studentNumberBytes] = await Promise.all([
    readStableFile(join(target, "username"), 32, "acceptance username", true),
    readStableFile(join(target, "password"), 128, "acceptance password", true),
    readStableFile(join(target, "student_number"), 64, "acceptance student number", true),
  ]);
  const decoder = new TextDecoder("utf-8", { fatal: true });
  const username = decoder.decode(usernameBytes);
  const password = decoder.decode(passwordBytes);
  const studentNumber = decoder.decode(studentNumberBytes);
  if (
    !usernamePattern.test(username)
    || password.length < 12
    || password.length > 128
    || password.trim() !== password
    || studentNumber.length < 1
    || studentNumber.trim() !== studentNumber
  ) {
    throw new Error("acceptance credential serialization is invalid");
  }
  return { username, password, studentNumber };
}

async function pathExists(path: string): Promise<boolean> {
  try {
    await lstat(path);
    return true;
  } catch (error) {
    if (isObject(error) && error.code === "ENOENT") return false;
    throw error;
  }
}

function parseCanonicalCatalog(
  bytes: Buffer,
  expectedSHA256: string,
): RecommendationKnowledgeCatalogV1 {
  if (sha256(bytes) !== expectedSHA256) {
    throw new Error("knowledge catalog bytes differ from the independently supplied SHA-256");
  }
  const parsed = JSON.parse(bytes.toString("utf8")) as unknown;
  if (!isObject(parsed) || Buffer.from(canonicalJSON(parsed), "utf8").compare(bytes) !== 0) {
    throw new Error("knowledge catalog must be one canonical JSON object");
  }
  const catalog = parsed as RecommendationKnowledgeCatalogV1;
  if (!Array.isArray(catalog.knowledgePoints) || !Array.isArray(catalog.problemAssignments)) {
    throw new Error("knowledge catalog arrays are missing");
  }
  const knowledgePointIDs = catalog.knowledgePoints.map((point) => point.id);
  if (!strictlyOrdered(knowledgePointIDs)) {
    throw new Error("knowledge catalog points must be nonempty and strictly ordered");
  }
  return catalog;
}

function parseSnapshot(bytes: Buffer): { snapshot: SnapshotIdentity; studentNumbers: string[] } {
  const parsed = JSON.parse(bytes.toString("utf8")) as unknown;
  if (
    !isObject(parsed)
    || parsed.schema !== "ascendany.pintia.snapshot.v2"
    || !isObject(parsed.exam)
    || parsed.exam.platform !== "pintia"
    || typeof parsed.exam.problemSetId !== "string"
    || typeof parsed.exam.title !== "string"
    || !Array.isArray(parsed.participants)
  ) {
    throw new Error("Pintia snapshot identity shape is invalid");
  }
  const snapshot = parsed as SnapshotIdentity;
  const studentNumbers = snapshot.participants
    .map((participant) => participant.studentNumber)
    .filter((value): value is string => typeof value === "string")
    .sort();
  if (studentNumbers.length === 0 || new Set(studentNumbers).size !== studentNumbers.length) {
    throw new Error("Pintia snapshot must contain unique enrollable student identities");
  }
  return { snapshot, studentNumbers };
}

function authenticatedClient(baseUrl: string, origin: string, accessToken?: string) {
  return createClient({
    baseUrl,
    auth: accessToken,
    headers: {
      Origin: origin,
      "CF-Connecting-IP": "203.0.113.19",
    },
  });
}

export async function readAllCursorPages<T>(
  label: string,
  readPage: (cursor: string | undefined) => Promise<CursorPage<T>>,
  itemIdentity: (item: T) => string,
  validItemIdentity: (identity: string) => boolean,
  validCursor: (cursor: string) => boolean,
  cursorForLastItem: ((item: T) => string) | null,
  delayMilliseconds = paginationDelayMilliseconds,
): Promise<T[]> {
  const collected: T[] = [];
  const seenItemIdentities = new Set<string>();
  const seenCursors = new Set<string>();
  let cursor: string | undefined;

  for (;;) {
    const page = await readPage(cursor);
    if (
      !Array.isArray(page.items)
      || page.items.length > maximumPageItems
      || (cursor !== undefined && page.items.length === 0)
    ) {
      throw new Error(`${label} returned an oversized or empty-progress page`);
    }
    for (const item of page.items) {
      const identity = itemIdentity(item);
      if (!validItemIdentity(identity) || seenItemIdentities.has(identity)) {
        throw new Error(`${label} returned an invalid or duplicate item identity`);
      }
      seenItemIdentities.add(identity);
      collected.push(item);
    }

    const nextCursor: unknown = page.nextCursor;
    if (nextCursor === null) {
      return collected;
    }
    if (
      typeof nextCursor !== "string"
      || !validCursor(nextCursor)
      || page.items.length === 0
      || seenCursors.has(nextCursor)
      || (cursorForLastItem !== null
        && nextCursor !== cursorForLastItem(page.items.at(-1)!))
    ) {
      throw new Error(`${label} returned an invalid or non-progressing cursor`);
    }
    seenCursors.add(nextCursor);
    cursor = nextCursor;
    if (delayMilliseconds > 0) {
      await new Promise((resolve) => setTimeout(resolve, delayMilliseconds));
    }
  }
}

async function readAllImportJobs(
  client: ReturnType<typeof createClient>,
): Promise<ImportJob[]> {
  return readAllCursorPages(
    "list import jobs",
    async (cursor) => assertAPIResult(
      "list import jobs",
      await listImportJobs({
        client,
        query: { limit: maximumPageItems, ...(cursor === undefined ? {} : { cursor }) },
      }),
    ),
    (job) => job.id,
    (identity) => canonicalUUIDv4Pattern.test(identity),
    (cursor) => canonicalUUIDv4Pattern.test(cursor),
    (job) => job.id,
  );
}

async function readAllExams(
  client: ReturnType<typeof createClient>,
): Promise<ExamSummary[]> {
  return readAllCursorPages(
    "list exams",
    async (cursor) => assertAPIResult(
      "list exams",
      await listExams({
        client,
        query: { limit: maximumPageItems, ...(cursor === undefined ? {} : { cursor }) },
      }),
    ),
    (exam) => exam.id,
    (identity) => canonicalUUIDv4Pattern.test(identity),
    (cursor) => canonicalUUIDv4Pattern.test(cursor),
    (exam) => exam.id,
  );
}

async function readSnapshotImportBinding(
  client: ReturnType<typeof createClient>,
  inputs: LoadedInputs,
): Promise<SnapshotImportBinding> {
  const importJobs = await readAllImportJobs(client);
  const exams = await readAllExams(client);
  return selectSnapshotImportBinding(
    importJobs,
    exams,
    inputs.deploymentKind,
    inputs.snapshotSHA256,
    inputs.snapshot.exam.platform,
    inputs.snapshot.exam.problemSetId,
  );
}

export function selectSnapshotImportBinding(
  importJobs: readonly ImportJob[],
  exams: readonly ExamSummary[],
  deploymentKind: DeploymentKind,
  snapshotSHA256: string,
  platform: SnapshotIdentity["exam"]["platform"],
  problemSetID: string,
): SnapshotImportBinding {
  const succeededSnapshotJobs = importJobs.filter((job) => (
    job.status === "succeeded" && job.artifactSha256 === snapshotSHA256
  ));
  const activeExams = exams.filter((exam) => (
    exam.platform === platform && exam.problemSetId === problemSetID
  ));
  if (succeededSnapshotJobs.length !== 1 || activeExams.length !== 1) {
    throw new Error("supplied snapshot does not have one exact succeeded import and active exam binding");
  }
  const importJob = succeededSnapshotJobs[0]!;
  const exam = activeExams[0]!;
  if (
    importJob.examId === null
    || importJob.snapshotId === null
    || importJob.examId !== exam.id
    || importJob.snapshotId !== exam.snapshotId
  ) {
    throw new Error("supplied snapshot import identifiers differ from the current active exam");
  }
  if (
    deploymentKind === "initial"
    && (
      importJobs.length !== 1
      || exams.length !== 1
      || exam.snapshotSequence !== 1
    )
  ) {
    throw new Error("initial deployment must expose exactly one import and one sequence-1 active exam snapshot");
  }
  return { importJob, exam };
}

async function waitForImport(
  client: ReturnType<typeof createClient>,
  jobID: string,
): Promise<ImportJob> {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    const job = assertAPIResult(
      "read import job",
      await getImportJob({ client, path: { jobId: jobID } }),
    );
    if (terminalImportStatuses.has(job.status)) {
      return job;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_100));
  }
  throw new Error("import job did not reach a terminal state before the deadline");
}

async function waitForAnalytics(
  client: ReturnType<typeof createClient>,
  examID: string,
): Promise<ExamAnalysisGeneration> {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    const generation = assertAPIResult(
      "read exam analytics generation",
      await getExamAnalysisGeneration({ client, path: { examId: examID } }),
    );
    if (terminalAnalyticsStatuses.has(generation.status)) {
      return generation;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_100));
  }
  throw new Error("analytics generation did not reach a terminal state before the deadline");
}

export function assertCatalogCoverage(
  catalog: RecommendationKnowledgeCatalogV1,
  review: RecommendationReviewContext,
): void {
  const assignmentKeys = catalog.problemAssignments.map((assignment) => {
    if (assignment.platform !== "pintia") {
      throw new Error("knowledge catalog contains a non-Pintia problem assignment");
    }
    const sourceProblemKey = `pintia:problem:${Buffer.byteLength(assignment.problemId, "utf8")}:${assignment.problemId}`;
    return `${sourceProblemKey}:${assignment.problemFactSha256}`;
  });
  const assignments = new Set(assignmentKeys);
  const reviewKeys = review.problems.map((problem) => problem.problemKey);
  if (
    assignments.size !== catalog.problemAssignments.length
    || assignments.size !== review.problems.length
    || !strictlyOrdered(reviewKeys)
  ) {
    throw new Error("knowledge catalog assignment set differs from the analytics review set");
  }
  for (const problem of review.problems) {
    const sourceProblemKey = `pintia:problem:${Buffer.byteLength(problem.problemId, "utf8")}:${problem.problemId}`;
    if (
      problem.platform !== "pintia"
      || problem.sourceProblemKey !== sourceProblemKey
      || problem.problemKey !== `${sourceProblemKey}:${problem.problemFactSha256}`
      || !assignments.has(problem.problemKey)
    ) {
      throw new Error("knowledge catalog contains a stale or dangling problem assignment");
    }
  }
}

export function assertCanonicalDeploymentTransition(
  expectation: DeploymentTransitionExpectation,
): void {
  if (expectation.deploymentKind === "initial") {
    if (
      expectation.expectedCatalogHeadRevision !== 1
      || expectation.expectedCurrentCatalogHeadRevision !== 0
      || expectation.expectedModelHeadRevision !== expectation.expectedCurrentModelHeadRevision + 1
      || expectation.expectedCurrentModelSHA256 !== expectation.expectedModelSHA256
      || expectation.expectedCurrentCatalogSHA256 !== null
    ) {
      throw new Error("initial deployment requires catalog head 1 and one publication-authorized model-head transition");
    }
    return;
  }
  if (
    expectation.expectedCurrentCatalogSHA256 === null
    || expectation.expectedCurrentCatalogHeadRevision < 1
    || expectation.expectedCatalogHeadRevision !== expectation.expectedCurrentCatalogHeadRevision
      + (expectation.expectedCatalogSHA256 === expectation.expectedCurrentCatalogSHA256 ? 0 : 1)
    || expectation.expectedModelHeadRevision !== expectation.expectedCurrentModelHeadRevision + 1
  ) {
    throw new Error("forward deployment catalog/model transition is noncanonical");
  }
}

async function loadInputs(): Promise<LoadedInputs> {
  const deploymentKind = requiredEnvironment("ASCENDANY_INITIALIZATION_DEPLOYMENT_KIND");
  if (deploymentKind !== "initial" && deploymentKind !== "forward") {
    throw new Error("ASCENDANY_INITIALIZATION_DEPLOYMENT_KIND must be initial or forward");
  }
  const baseUrl = requiredEnvironment("ASCENDANY_INITIALIZATION_BASE_URL");
  const origin = requiredEnvironment("ASCENDANY_INITIALIZATION_ORIGIN");
  for (const [name, raw, requireOrigin] of [
    ["ASCENDANY_INITIALIZATION_BASE_URL", baseUrl, false],
    ["ASCENDANY_INITIALIZATION_ORIGIN", origin, true],
  ] as const) {
    const parsed = new URL(raw);
    if (
      (parsed.protocol !== "http:" && parsed.protocol !== "https:")
      || parsed.username !== ""
      || parsed.password !== ""
      || parsed.search !== ""
      || parsed.hash !== ""
      || parsed.pathname !== "/"
      || (parsed.protocol === "http:" && !["127.0.0.1", "[::1]"].includes(parsed.hostname))
    ) {
      throw new Error(`${name} must be one canonical HTTPS URL${requireOrigin ? " origin" : ""} or a loopback HTTP URL`);
    }
  }
  const snapshotPath = requiredEnvironment("ASCENDANY_INITIALIZATION_SNAPSHOT_PATH");
  const adminPasswordPath = requiredEnvironment("ASCENDANY_INITIALIZATION_ADMIN_PASSWORD_FILE");
  const targetApplication: ApplicationIdentity = {
    version: requiredEnvironment("ASCENDANY_INITIALIZATION_TARGET_APPLICATION_VERSION"),
    commit: requiredEnvironment("ASCENDANY_INITIALIZATION_TARGET_APPLICATION_COMMIT"),
    buildTime: requiredEnvironment("ASCENDANY_INITIALIZATION_TARGET_APPLICATION_BUILD_TIME"),
  };
  const currentApplication: ApplicationIdentity = deploymentKind === "forward"
    ? {
        version: requiredEnvironment("ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_VERSION"),
        commit: requiredEnvironment("ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_COMMIT"),
        buildTime: requiredEnvironment("ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_BUILD_TIME"),
      }
    : { ...targetApplication };
  const expectedModelPurpose = requiredEnvironment("ASCENDANY_INITIALIZATION_EXPECTED_MODEL_PURPOSE");
  const expectedModelSHA256 = requiredEnvironment("ASCENDANY_INITIALIZATION_EXPECTED_MODEL_SHA256");
  const expectedModelHeadRevision = positiveIntegerEnvironment(
    "ASCENDANY_INITIALIZATION_EXPECTED_MODEL_HEAD_REVISION",
  );
  const expectedCurrentModelSHA256 = requiredEnvironment(
    "ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_SHA256",
  );
  const expectedCurrentModelHeadRevision = positiveIntegerEnvironment(
    "ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_HEAD_REVISION",
  );
  const expectedCatalogHeadRevision = positiveIntegerEnvironment(
    "ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_HEAD_REVISION",
  );
  const expectedCurrentCatalogHeadRevision = nonNegativeIntegerEnvironment(
    "ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_HEAD_REVISION",
  );
  const expectedCurrentCatalogSHA256 = deploymentKind === "forward"
    ? requiredEnvironment("ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_SHA256")
    : null;
  const catalogPath = requiredEnvironment("ASCENDANY_INITIALIZATION_KNOWLEDGE_CATALOG_PATH");
  const expectedCatalogSHA256 = requiredEnvironment("ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_SHA256");
  const acceptanceStudentNumber = requiredEnvironment(
    "ASCENDANY_INITIALIZATION_ACCEPTANCE_STUDENT_NUMBER",
  );
  validateApplicationIdentity(targetApplication, "target application identity");
  validateApplicationIdentity(currentApplication, "current application identity");
  if (expectedModelPurpose !== "production" && expectedModelPurpose !== "acceptance_test") {
    throw new Error("ASCENDANY_INITIALIZATION_EXPECTED_MODEL_PURPOSE is invalid");
  }
  if (
    !sha256Pattern.test(expectedModelSHA256)
    || !sha256Pattern.test(expectedCurrentModelSHA256)
    || !sha256Pattern.test(expectedCatalogSHA256)
    || (expectedCurrentCatalogSHA256 !== null && !sha256Pattern.test(expectedCurrentCatalogSHA256))
  ) {
    throw new Error("model and catalog SHA-256 values must be lowercase hexadecimal");
  }
  assertCanonicalDeploymentTransition({
    deploymentKind,
    expectedModelHeadRevision,
    expectedCurrentModelHeadRevision,
    expectedModelSHA256,
    expectedCurrentModelSHA256,
    expectedCatalogHeadRevision,
    expectedCurrentCatalogHeadRevision,
    expectedCatalogSHA256,
    expectedCurrentCatalogSHA256,
  });
  const [snapshotBytes, adminPasswordBytes, catalogBytes] = await Promise.all([
    readStableFile(snapshotPath, maximumSnapshotBytes, "Pintia snapshot"),
    readStableFile(adminPasswordPath, 128, "administrator password", true),
    readStableFile(catalogPath, maximumCatalogBytes, "knowledge catalog"),
  ]);
  const adminPassword = new TextDecoder("utf-8", { fatal: true }).decode(adminPasswordBytes);
  if (adminPassword.length < 12 || adminPassword.length > 128 || adminPassword.trim() !== adminPassword) {
    throw new Error("administrator password credential serialization is invalid");
  }
  const { snapshot, studentNumbers } = parseSnapshot(snapshotBytes);
  if (!studentNumbers.includes(acceptanceStudentNumber)) {
    throw new Error("explicit acceptance student number is absent from the supplied Pintia snapshot");
  }
  const catalog = parseCanonicalCatalog(catalogBytes, expectedCatalogSHA256);
  return {
    deploymentKind,
    baseUrl,
    origin,
    snapshotPath,
    adminPasswordPath,
    targetApplication,
    currentApplication,
    expectedModelPurpose,
    expectedModelSHA256,
    expectedModelHeadRevision,
    expectedCurrentModelSHA256,
    expectedCurrentModelHeadRevision,
    expectedCurrentCatalogHeadRevision,
    expectedCatalogHeadRevision,
    expectedCurrentCatalogSHA256,
    catalogPath,
    expectedCatalogSHA256,
    acceptanceStudentNumber,
    snapshotBytes,
    snapshotSHA256: sha256(snapshotBytes),
    snapshot,
    studentNumbers,
    adminPassword,
    catalog,
    knowledgePointIDs: catalog.knowledgePoints.map((point) => point.id),
  };
}

async function assertRuntime(
  client: ReturnType<typeof createClient>,
  expectedApplication: ApplicationIdentity,
): Promise<Version> {
  const liveness = assertAPIResult("liveness", await getLiveness({ client }));
  const readiness = assertAPIResult("readiness", await getReadiness({ client }));
  const version = assertAPIResult("version", await getVersion({ client }));
  const capabilities = assertAPIResult("capabilities", await getCapabilities({ client }));
  if (liveness.status !== "alive" || readiness.status !== "ready") {
    throw new Error("server health contract is not ready");
  }
  if (
    version.version !== expectedApplication.version
    || version.commit !== expectedApplication.commit
    || version.buildTime !== expectedApplication.buildTime
  ) {
    throw new Error("running server provenance differs from the reviewed release");
  }
  if (
    !capabilities.writesEnabled
    || capabilities.pintiaSnapshotSchema !== "ascendany.pintia.snapshot.v2"
    || capabilities.maxSubmissions !== 20_000
  ) {
    throw new Error("running server capabilities differ from the initialization contract");
  }
  return version;
}

async function loginAdministrator(
  inputs: LoadedInputs,
  client: ReturnType<typeof createClient>,
): Promise<AuthSession> {
  const session = assertAPIResult<AuthSession>(
    "administrator login",
    await loginAccount({
      client,
      body: { username: "admin", password: inputs.adminPassword },
    }),
  );
  if (session.account.role !== "admin") {
    throw new Error("bootstrap account did not authenticate as administrator");
  }
  return session;
}

async function prepare(inputs: LoadedInputs): Promise<Record<string, unknown>> {
  const outputDirectory = requiredEnvironment(
    "ASCENDANY_INITIALIZATION_CATALOG_CREDENTIAL_DIRECTORY_OUTPUT",
  );
  await validateOutputTarget(outputDirectory, "catalog credential output directory");
  const publicClient = authenticatedClient(inputs.baseUrl, inputs.origin);
  await assertRuntime(publicClient, inputs.currentApplication);
  const adminSession = await loginAdministrator(inputs, publicClient);
  if (Date.parse(adminSession.expiresAt) - Date.now() < minimumPublicationAccessValidityMilliseconds) {
    throw new Error("administrator access token has insufficient remaining lifetime for publication");
  }
  const adminClient = authenticatedClient(inputs.baseUrl, inputs.origin, adminSession.accessToken);
  const catalogRead = await getConfiguration({ client: adminClient, path: { key: catalogKey } });
  const requestHeadRevision = inputs.expectedCurrentCatalogHeadRevision;
  let targetCatalogAlreadyActive = false;
  if (catalogRead.data !== undefined) {
    const current = catalogRead.data;
    if (
      current.key !== catalogKey
      || current.kind !== "knowledge_catalog"
      || current.activeVersion === null
      || current.activeVersion.schemaId !== catalogSchema
    ) {
      throw new Error("current knowledge catalog has an invalid singleton shape");
    }
    if (
      current.headRevision === inputs.expectedCatalogHeadRevision
      && current.activeVersion.number === inputs.expectedCatalogHeadRevision
      && current.activeVersion.documentSha256 === inputs.expectedCatalogSHA256
    ) {
      targetCatalogAlreadyActive = true;
    } else if (
      inputs.deploymentKind === "forward"
      && current.headRevision === requestHeadRevision
      && current.activeVersion.number === requestHeadRevision
      && current.activeVersion.documentSha256 === inputs.expectedCurrentCatalogSHA256
    ) {
      targetCatalogAlreadyActive = false;
    } else {
      throw new Error("current knowledge catalog does not match the selected deployment transition");
    }
  } else if (
    catalogRead.response?.status !== 404
    || inputs.deploymentKind !== "initial"
    || requestHeadRevision !== 0
  ) {
    throw new Error("knowledge catalog state does not match the selected deployment transition");
  }

  let createdImportJobID: string | null = null;
  let generation: ExamAnalysisGeneration | null = null;
  if (inputs.deploymentKind === "initial") {
    const createdImport = assertAPIResult(
      "create Pintia import",
      await createPintiaImport({
        client: adminClient,
        body: new Blob([inputs.snapshotBytes], { type: snapshotMediaType }),
      }),
    );
    if (createdImport.artifactSha256 !== inputs.snapshotSHA256) {
      throw new Error("import job artifact digest differs from the supplied snapshot bytes");
    }
    const completedImport = await waitForImport(adminClient, createdImport.id);
    if (
      completedImport.status !== "succeeded"
      || completedImport.examId === null
      || completedImport.snapshotId === null
    ) {
      throw new Error(`Pintia import terminated as ${completedImport.status}`);
    }
    createdImportJobID = completedImport.id;
  }
  const snapshotBinding = await readSnapshotImportBinding(adminClient, inputs);
  const importJob = snapshotBinding.importJob;
  if (createdImportJobID !== null && importJob.id !== createdImportJobID) {
    throw new Error("initial import response differs from the fully paginated import state");
  }
  if (inputs.deploymentKind === "initial") {
    generation = await waitForAnalytics(adminClient, snapshotBinding.exam.id);
    if (generation.status !== "succeeded") {
      throw new Error(`analytics generation terminated as ${generation.status}`);
    }
  }
  const review = assertAPIResult(
    "recommendation review context",
    await getRecommendationReviewContext({ client: adminClient }),
  );
  if (
    (generation !== null && review.analyticsGenerationId !== generation.generationId)
    || review.analyticsHeadRevision < 1
    || !sha256Pattern.test(review.inputManifestSha256)
  ) {
    throw new Error("recommendation review provenance differs from the current analytics head");
  }
  assertCatalogCoverage(inputs.catalog, review);

  const publicationIntent: CatalogPublicationIntent = {
    schema: requestSchema,
    expectedConfigurationHeadRevision: requestHeadRevision,
    expectedAnalyticsGenerationId: review.analyticsGenerationId,
    expectedAnalyticsHeadRevision: review.analyticsHeadRevision,
    expectedInputManifestSha256: review.inputManifestSha256,
    expectedCurrentModelHeadRevision: inputs.expectedCurrentModelHeadRevision,
    expectedCurrentModelArtifactSha256: inputs.expectedCurrentModelSHA256,
    targetCatalogSha256: inputs.expectedCatalogSHA256,
    targetModelArtifactSha256: inputs.expectedModelSHA256,
    targetApplicationVersion: inputs.targetApplication.version,
    targetApplicationCommit: inputs.targetApplication.commit,
    targetApplicationBuildTime: inputs.targetApplication.buildTime,
  };
  if (Date.parse(adminSession.expiresAt) - Date.now() < minimumPublicationAccessValidityMilliseconds) {
    throw new Error("administrator access token has insufficient remaining lifetime for publication authorization");
  }
  const publicationAuthorization = assertAPIResult(
    "authorize knowledge catalog publication",
    await authorizeKnowledgeCatalogPublication({
      client: adminClient,
      body: {
        publicationIntent,
        document: inputs.catalog,
      },
    }),
  );
  const publicationRequest = assertCatalogPublicationAuthorization(
    publicationIntent,
    adminSession.expiresAt,
    publicationAuthorization,
  );
  await writeCredentialDirectory(
    outputDirectory,
    [
      [requestCredentialName, Buffer.from(canonicalJSON(publicationRequest), "utf8")],
      [accessTokenCredentialName, Buffer.from(adminSession.accessToken, "ascii")],
    ],
    "catalog credential output directory",
  );
  return {
    schema: prepareReceiptSchema,
    deploymentKind: inputs.deploymentKind,
    releaseVerified: true,
    snapshotSha256: inputs.snapshotSHA256,
    problemSetId: inputs.snapshot.exam.problemSetId,
    importJobId: importJob.id,
    importStatus: importJob.status,
    examId: snapshotBinding.exam.id,
    snapshotId: snapshotBinding.exam.snapshotId,
    snapshotSequence: snapshotBinding.exam.snapshotSequence,
    analyticsGenerationId: review.analyticsGenerationId,
    analyticsHeadRevision: review.analyticsHeadRevision,
    inputManifestSha256: review.inputManifestSha256,
    catalogSha256: inputs.expectedCatalogSHA256,
    catalogProblemAssignmentCount: inputs.catalog.problemAssignments.length,
    catalogCoverageVerified: true,
    catalogPublicationAuthorizationId: publicationRequest.authorizationId,
    expectedConfigurationHeadRevision: requestHeadRevision,
    targetCatalogHeadRevision: inputs.expectedCatalogHeadRevision,
    targetCatalogAlreadyActive,
    currentModelHeadRevision: inputs.expectedCurrentModelHeadRevision,
    currentModelArtifactSha256: inputs.expectedCurrentModelSHA256,
    targetModelHeadRevision: inputs.expectedModelHeadRevision,
    targetModelArtifactSha256: inputs.expectedModelSHA256,
    targetApplication: inputs.targetApplication,
    currentApplication: inputs.currentApplication,
    administratorAccountId: adminSession.account.id,
    accessTokenExpiresAt: adminSession.expiresAt,
    credentialDirectory: outputDirectory,
  };
}

async function readAllManagedStudents(
  client: ReturnType<typeof createClient>,
): Promise<ManagedStudent[]> {
  const students = await readAllCursorPages(
    "list managed students",
    async (cursor) => assertAPIResult(
      "list managed students",
      await listManagedStudents({
        client,
        query: { limit: maximumPageItems, ...(cursor === undefined ? {} : { cursor }) },
      }),
    ),
    (student) => student.studentNumber,
    (studentNumber) => (
      studentNumber.length > 0
      && studentNumber.trim() === studentNumber
      && !studentNumber.includes("\0")
    ),
    (cursor) => /^[A-Za-z0-9_-]{1,128}$/u.test(cursor),
    null,
  );
  if (!strictlyUTF8BytewiseOrdered(students.map((student) => student.studentNumber))) {
    throw new Error("list managed students returned a non-progressing global order");
  }
  return students;
}

async function readAllAuditEvents(
  client: ReturnType<typeof createClient>,
): Promise<AuditEvent[]> {
  const events = await readAllCursorPages(
    "list audit events",
    async (cursor) => assertAPIResult(
      "list audit events",
      await listAuditEvents({
        client,
        query: { limit: maximumPageItems, ...(cursor === undefined ? {} : { cursor }) },
      }),
    ),
    (event) => event.id,
    isCanonicalPositiveInt64,
    isCanonicalPositiveInt64,
    (event) => event.id,
  );
  if (events.some((event, index) => (
    index > 0 && BigInt(events[index - 1]!.id) <= BigInt(event.id)
  ))) {
    throw new Error("list audit events returned a non-progressing global order");
  }
  return events;
}

async function verify(inputs: LoadedInputs): Promise<Record<string, unknown>> {
  const studentUsername = requiredEnvironment("ASCENDANY_INITIALIZATION_STUDENT_USERNAME");
  const credentialDirectory = requiredEnvironment(
    "ASCENDANY_INITIALIZATION_STUDENT_CREDENTIAL_DIRECTORY",
  );
  if (!usernamePattern.test(studentUsername) || studentUsername === "admin") {
    throw new Error("acceptance student username must match [a-z0-9_]{3,32} and differ from admin");
  }
  const publicationReceipt = await readCatalogPublicationReceipt(inputs);
  const publicClient = authenticatedClient(inputs.baseUrl, inputs.origin);
  const version = await assertRuntime(publicClient, inputs.targetApplication);
  const adminSession = await loginAdministrator(inputs, publicClient);
  if (publicationReceipt.publishedByAccountId !== adminSession.account.id) {
    throw new Error("catalog publication receipt actor differs from the authenticated release administrator");
  }
  const adminClient = authenticatedClient(inputs.baseUrl, inputs.origin, adminSession.accessToken);

  const configurationItem = assertAPIResult(
    "read active knowledge catalog",
    await getConfiguration({ client: adminClient, path: { key: catalogKey } }),
  );
  if (
    configurationItem.key !== catalogKey
    || configurationItem.kind !== "knowledge_catalog"
    || configurationItem.headRevision !== inputs.expectedCatalogHeadRevision
    || configurationItem.activeVersion?.number !== inputs.expectedCatalogHeadRevision
    || configurationItem.activeVersion.schemaId !== catalogSchema
    || configurationItem.activeVersion.documentSha256 !== inputs.expectedCatalogSHA256
    || configurationItem.activeVersion.credentialRef !== null
    || configurationItem.id !== publicationReceipt.configurationId
    || configurationItem.activeVersion.id !== publicationReceipt.configurationVersionId
    || configurationItem.activeVersion.number !== publicationReceipt.configurationVersionNumber
  ) {
    throw new Error("active knowledge catalog differs from the isolated publication contract");
  }
  if (
    publicationReceipt.configurationMutated
    && (
      configurationItem.activeVersion.createdByAccountId !== publicationReceipt.publishedByAccountId
      || configurationItem.activeVersion.createdBySessionId !== publicationReceipt.publishedBySessionId
      || configurationItem.activeVersion.createdAt !== publicationReceipt.publishedAt
    )
  ) {
    throw new Error("mutated knowledge catalog version differs from its publication actor and timestamp");
  }
  const review = assertAPIResult(
    "recommendation review context",
    await getRecommendationReviewContext({ client: adminClient }),
  );
  if (
    review.analyticsGenerationId !== publicationReceipt.analyticsGenerationId
    || review.analyticsHeadRevision !== publicationReceipt.analyticsHeadRevision
    || review.inputManifestSha256 !== publicationReceipt.inputManifestSha256
  ) {
    throw new Error("current analytics provenance differs from the catalog publication receipt");
  }
  assertCatalogCoverage(inputs.catalog, review);
  const snapshotBinding = await readSnapshotImportBinding(adminClient, inputs);

  const students = await readAllManagedStudents(adminClient);
  const studentNumber = inputs.acceptanceStudentNumber;
  const managedStudent = students.find((candidate) => candidate.studentNumber === studentNumber);
  if (managedStudent === undefined) {
    throw new Error("explicit acceptance student is absent from the current managed-student set");
  }
  const credentialDirectoryExists = await pathExists(credentialDirectory);
  let studentSession: AuthSession;
  let enrollmentSingleUse = false;
  if (managedStudent.account === null) {
    if (inputs.deploymentKind === "forward") {
      throw new Error("forward acceptance requires the persistent acceptance account created during initial deployment");
    }
    let studentPassword: string;
    if (credentialDirectoryExists) {
      const stored = await readAcceptanceCredentials(credentialDirectory);
      if (stored.username !== studentUsername || stored.studentNumber !== studentNumber) {
        throw new Error("existing acceptance credentials differ from the explicit acceptance identity");
      }
      studentPassword = stored.password;
    } else {
      await validateOutputTarget(credentialDirectory, "student credential directory");
      studentPassword = randomBytes(32).toString("base64url");
      await writeCredentialDirectory(
        credentialDirectory,
        [
          ["username", Buffer.from(studentUsername, "ascii")],
          ["password", Buffer.from(studentPassword, "ascii")],
          ["student_number", Buffer.from(studentNumber, "utf8")],
        ],
        "student credential directory",
      );
    }
    const issued = assertAPIResult(
      "issue acceptance enrollment",
      await issueEnrollmentClaim({
        client: adminClient,
        body: {
          username: studentUsername,
          displayName: "AscendAny Release Acceptance",
          studentNumber,
          expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
        },
      }),
    );
    try {
      studentSession = assertAPIResult<AuthSession>(
        "consume acceptance enrollment",
        await consumeEnrollmentClaim({
          client: publicClient,
          body: { token: issued.token, password: studentPassword },
        }),
      );
    } catch (error) {
      const revoked = await revokeEnrollmentClaim({
        client: adminClient,
        path: { grantId: issued.grant.id },
      });
      if (revoked.response?.status !== 204) {
        throw new AggregateError([error, revoked.error], "enrollment consumption failed and grant revocation failed");
      }
      throw error;
    }
    const replay = await consumeEnrollmentClaim({
      client: publicClient,
      body: { token: issued.token, password: studentPassword },
    });
    if (replay.data !== undefined || replay.response?.status !== 401) {
      throw new Error("consumed acceptance enrollment token was accepted more than once");
    }
    enrollmentSingleUse = true;
  } else {
    const stored = await readAcceptanceCredentials(credentialDirectory);
    if (
      stored.username !== studentUsername
      || stored.studentNumber !== studentNumber
      || managedStudent.account.username !== studentUsername
      || managedStudent.account.disabledAt !== null
    ) {
      throw new Error("persistent acceptance account differs from the explicit credential binding");
    }
    studentSession = assertAPIResult<AuthSession>(
      "persistent acceptance student login",
      await loginAccount({
        client: publicClient,
        body: { username: stored.username, password: stored.password },
      }),
    );
  }
  if (
    studentSession.account.role !== "student"
    || studentSession.account.username !== studentUsername
    || studentSession.account.studentNumber !== studentNumber
  ) {
    throw new Error("acceptance enrollment created an unexpected student binding");
  }
  const studentClient = authenticatedClient(inputs.baseUrl, inputs.origin, studentSession.accessToken);
  const analytics = assertAPIResult(
    "acceptance student analytics",
    await getSelfStudentAnalytics({ client: studentClient }),
  );
  const leaderboard = assertAPIResult(
    "acceptance student leaderboard",
    await getStudentLeaderboard({ client: studentClient, query: { limit: 100 } }),
  );
  const recommendation = assertAPIResult(
    "acceptance student recommendation",
    await getSelfRecommendation({ client: studentClient }),
  );
  if (
    analytics.state !== "ready"
    || analytics.headRevision !== review.analyticsHeadRevision
    || leaderboard.state !== "ready"
    || leaderboard.headRevision !== review.analyticsHeadRevision
    || leaderboard.population < 1
    || leaderboard.items.length !== Math.min(leaderboard.population, maximumPageItems)
  ) {
    throw new Error("published student analytics or leaderboard head differs from the reviewed analytics release");
  }
  if (
    recommendation.state !== "fresh"
    || recommendation.result.schema !== "ascendany.recommendation.inference-result.v1"
    || !sha256Pattern.test(recommendation.result.sha256)
    || recommendation.model.purpose !== inputs.expectedModelPurpose
    || recommendation.model.modelId !== publicationReceipt.modelId
    || recommendation.model.artifactSha256 !== inputs.expectedModelSHA256
    || recommendation.model.knowledgeCatalogSha256 !== inputs.expectedCatalogSHA256
    || recommendation.model.modelSchema !== "ascendany.recommendation.inference-model.v1"
    || recommendation.model.inferenceContract !== "ascendany.recommendation.inference.v1"
    || recommendation.modelHeadRevision !== inputs.expectedModelHeadRevision
    || recommendation.model.modelHeadRevision !== inputs.expectedModelHeadRevision
    || recommendation.model.applicationVersion !== inputs.targetApplication.version
    || recommendation.model.applicationCommit !== inputs.targetApplication.commit
    || recommendation.model.applicationBuildTime !== inputs.targetApplication.buildTime
    || recommendation.model.applicationBuildTime !== version.buildTime
  ) {
    throw new Error("online inference provenance differs from the reviewed model/catalog release");
  }
  const masteryIDs = recommendation.result.knowledgeMastery.map((item) => item.knowledgePointId);
  if (
    !strictlyOrdered(masteryIDs)
    || masteryIDs.length !== inputs.knowledgePointIDs.length
    || masteryIDs.some((value, index) => value !== inputs.knowledgePointIDs[index])
  ) {
    throw new Error("online inference knowledge mastery is empty, unordered, or incomplete");
  }
  if (
    recommendation.result.status === "ready"
    && (
      recommendation.result.learningPath.length === 0
      || recommendation.result.learningPath.some((step, index) => (
        step.order !== index + 1 || step.recommendedProblems.length === 0
      ))
    )
  ) {
    throw new Error("ready online inference learning path is empty or unordered");
  }

  const auditEvents = await readAllAuditEvents(adminClient);
  const matchingAuditEvents = auditEvents.filter((event) => (
    event.id === publicationReceipt.auditEventId
  ));
  const catalogAudit = matchingAuditEvents.length === 1 ? matchingAuditEvents[0] : undefined;
  const expectedAuditType = publicationReceipt.configurationMutated
    ? "admin.configuration_version_created"
    : "admin.knowledge_catalog_release_bound";
  const expectedAuditPayload: Record<string, unknown> = {
    authorizationId: publicationReceipt.authorizationId,
    configurationId: publicationReceipt.configurationId,
    key: catalogKey,
    kind: "knowledge_catalog",
    versionNumber: publicationReceipt.configurationVersionNumber,
    schemaId: catalogSchema,
    documentSha256: publicationReceipt.catalogSha256,
    headRevision: publicationReceipt.configurationHeadRevision,
    credentialRef: null,
    expectedConfigurationHeadRevision: publicationReceipt.expectedConfigurationHeadRevision,
    configurationMutated: publicationReceipt.configurationMutated,
    analyticsGenerationId: publicationReceipt.analyticsGenerationId,
    analyticsHeadRevision: publicationReceipt.analyticsHeadRevision,
    inputManifestSha256: publicationReceipt.inputManifestSha256,
    currentModelHeadRevision: publicationReceipt.currentModelHeadRevision,
    currentModelArtifactSha256: publicationReceipt.currentModelArtifactSha256,
    targetApplicationVersion: publicationReceipt.targetApplicationVersion,
    targetApplicationCommit: publicationReceipt.targetApplicationCommit,
    targetApplicationBuildTime: publicationReceipt.targetApplicationBuildTime,
    targetCatalogSha256: publicationReceipt.catalogSha256,
    targetModelId: publicationReceipt.modelId,
    targetModelArtifactSha256: publicationReceipt.modelArtifactSha256,
    targetModelReleaseId: publicationReceipt.targetModelReleaseId,
  };
  if (
    catalogAudit === undefined
    || catalogAudit.type !== expectedAuditType
    || catalogAudit.actorAccountId !== publicationReceipt.publishedByAccountId
    || catalogAudit.actorSessionId !== publicationReceipt.publishedBySessionId
    || catalogAudit.occurredAt !== publicationReceipt.publishedAt
    || !hasExactKeys(catalogAudit.payload, catalogPublicationAuditPayloadKeys)
    || canonicalJSON(catalogAudit.payload) !== canonicalJSON(expectedAuditPayload)
  ) {
    throw new Error("catalog publication audit event differs from the immutable receipt");
  }

  return {
    schema: verifyReceiptSchema,
    deploymentKind: inputs.deploymentKind,
    releaseVerified: true,
    problemSetId: inputs.snapshot.exam.problemSetId,
    snapshotSha256: inputs.snapshotSHA256,
    importJobId: snapshotBinding.importJob.id,
    importStatus: snapshotBinding.importJob.status,
    examId: snapshotBinding.exam.id,
    snapshotId: snapshotBinding.exam.snapshotId,
    snapshotSequence: snapshotBinding.exam.snapshotSequence,
    analyticsGenerationId: review.analyticsGenerationId,
    analyticsHeadRevision: review.analyticsHeadRevision,
    catalogConfigurationId: configurationItem.id,
    catalogHeadRevision: configurationItem.headRevision,
    catalogSha256: inputs.expectedCatalogSHA256,
    catalogPublicationReceiptPath: requiredEnvironment(
      "ASCENDANY_INITIALIZATION_CATALOG_PUBLICATION_RECEIPT_PATH",
    ),
    catalogPublicationConfigurationMutated: publicationReceipt.configurationMutated,
    catalogPublicationAuthorizationId: publicationReceipt.authorizationId,
    knowledgeCatalogPublicationId: publicationReceipt.knowledgeCatalogPublicationId,
    catalogPublicationModelReleaseId: publicationReceipt.targetModelReleaseId,
    catalogPublicationModelId: publicationReceipt.modelId,
    targetApplication: inputs.targetApplication,
    catalogAuditEventId: catalogAudit.id,
    acceptanceStudentAccountId: studentSession.account.id,
    acceptanceStudentUsername: studentUsername,
    acceptanceStudentNumber: studentNumber,
    studentCredentialDirectory: credentialDirectory,
    studentAnalyticsState: analytics.state,
    studentAnalyticsHeadRevision: analytics.headRevision,
    leaderboardState: leaderboard.state,
    leaderboardHeadRevision: leaderboard.headRevision,
    leaderboardPopulation: leaderboard.population,
    leaderboardVisibleCount: leaderboard.items.length,
    enrollmentSingleUse,
    recommendationState: recommendation.state,
    recommendationResultStatus: recommendation.result.status,
    recommendationResultSha256: recommendation.result.sha256,
    recommendationKnowledgePointCount: masteryIDs.length,
    recommendationModel: recommendation.model,
  };
}

async function main(): Promise<void> {
  const phase = process.argv[2] as Phase | undefined;
  if (process.argv.length !== 3 || (phase !== "prepare" && phase !== "verify")) {
    throw new Error("usage: v2-production-initialization-client prepare|verify");
  }
  const inputs = await loadInputs();
  const receipt = phase === "prepare" ? await prepare(inputs) : await verify(inputs);
  process.stdout.write(`${canonicalJSON(receipt)}\n`);
}

const invokedPath = process.argv[1];
if (invokedPath !== undefined && import.meta.url === pathToFileURL(invokedPath).href) {
  await main();
}
