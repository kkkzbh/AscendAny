import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import {
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
  listAuditEvents,
  listExams,
  listManagedStudents,
  loginAccount,
  type AuthSession,
  type ExamAnalysisGeneration,
  type ImportJob,
  type RecommendationKnowledgeCatalogV1,
} from "../packages/sdk/src/index.ts";

type SnapshotIdentity = {
  schema: "ascendany.pintia.snapshot.v2";
  exporter: {
    exportedAt: string;
  };
  exam: {
    problemSetId: string;
    title: string;
  };
  participants: Array<{
    studentNumber: string | null;
  }>;
};

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

function requiredEnvironment(name: string): string {
  const value = process.env[name];
  if (value === undefined || value.length === 0 || value.trim() !== value) {
    throw new Error(`${name} must contain one non-empty unpadded value`);
  }
  return value;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function apiErrorCode(error: unknown): string {
  if (isObject(error) && typeof error.code === "string") {
    return error.code;
  }
  return "unknown_error";
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
    const exponentIndex = encoded.search(/[eE]/);
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

function parseCanonicalCatalog(bytes: Buffer, expectedSHA256: string): RecommendationKnowledgeCatalogV1 {
  const actualSHA256 = createHash("sha256").update(bytes).digest("hex");
  if (actualSHA256 !== expectedSHA256) {
    throw new Error("knowledge catalog bytes differ from the independently supplied SHA-256");
  }
  const parsed = JSON.parse(bytes.toString("utf8")) as unknown;
  if (!isObject(parsed)) {
    throw new Error("knowledge catalog root must be an object");
  }
  if (!Buffer.from(canonicalJSON(parsed), "utf8").equals(bytes)) {
    throw new Error("knowledge catalog bytes must already use the server canonical JSON encoding");
  }
  return parsed as RecommendationKnowledgeCatalogV1;
}

function strictlyOrdered(values: readonly string[]): boolean {
  return values.length > 0 && values.every((value, index) => (
    value.length > 0 && (index === 0 || values[index - 1] < value)
  ));
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

function authenticatedClient(
  baseUrl: string,
  origin: string,
  accessToken?: string,
) {
  return createClient({
    baseUrl,
    auth: accessToken,
    headers: {
      Origin: origin,
      "CF-Connecting-IP": "203.0.113.19",
    },
  });
}

async function main(): Promise<void> {
  const baseUrl = requiredEnvironment("ASCENDANY_E2E_BASE_URL");
  const origin = requiredEnvironment("ASCENDANY_E2E_ORIGIN");
  const snapshotPath = requiredEnvironment("ASCENDANY_E2E_SNAPSHOT_PATH");
  const adminPasswordPath = requiredEnvironment("ASCENDANY_E2E_ADMIN_PASSWORD_FILE");
  const expectedCommit = requiredEnvironment("ASCENDANY_E2E_EXPECTED_COMMIT");
  const expectedVersion = requiredEnvironment("ASCENDANY_E2E_EXPECTED_VERSION");
  const expectedModelSHA256 = requiredEnvironment("ASCENDANY_E2E_EXPECTED_MODEL_SHA256");
  const expectedModelPurpose = requiredEnvironment("ASCENDANY_E2E_EXPECTED_MODEL_PURPOSE");
  if (expectedModelPurpose !== "acceptance_test") {
    throw new Error("full E2E requires the acceptance_test model purpose");
  }
  const catalogPath = requiredEnvironment("ASCENDANY_E2E_KNOWLEDGE_CATALOG_PATH");
  const expectedCatalogSHA256 = requiredEnvironment("ASCENDANY_E2E_EXPECTED_CATALOG_SHA256");
  const studentCredentialDirectory = requiredEnvironment("ASCENDANY_E2E_STUDENT_CREDENTIAL_DIRECTORY");

  const snapshotBytes = await readFile(snapshotPath);
  const catalogDocument = parseCanonicalCatalog(
    await readFile(catalogPath),
    expectedCatalogSHA256,
  );
  const catalogKnowledgePointIDs = catalogDocument.knowledgePoints.map((point) => point.id);
  if (!strictlyOrdered(catalogKnowledgePointIDs)) {
    throw new Error("knowledge catalog points must be nonempty and strictly ordered");
  }
  const snapshot = JSON.parse(snapshotBytes.toString("utf8")) as unknown;
  if (
    !isObject(snapshot)
    || snapshot.schema !== "ascendany.pintia.snapshot.v2"
    || !isObject(snapshot.exporter)
    || typeof snapshot.exporter.exportedAt !== "string"
    || !isObject(snapshot.exam)
    || typeof snapshot.exam.problemSetId !== "string"
    || typeof snapshot.exam.title !== "string"
    || !Array.isArray(snapshot.participants)
  ) {
    throw new Error("committed E2E snapshot has an invalid identity shape");
  }
  const identity = snapshot as SnapshotIdentity;
  const studentNumbers = identity.participants
    .map((participant) => participant.studentNumber)
    .filter((value): value is string => typeof value === "string")
    .sort();
  if (studentNumbers.length === 0 || new Set(studentNumbers).size !== studentNumbers.length) {
    throw new Error("exporter E2E snapshot must contain unique enrollable student identities");
  }
  const [studentUsername, studentPassword, studentNumber] = await Promise.all([
    readFile(join(studentCredentialDirectory, "username"), "utf8"),
    readFile(join(studentCredentialDirectory, "password"), "utf8"),
    readFile(join(studentCredentialDirectory, "student_number"), "utf8"),
  ]);
  if (
    !/^[a-z0-9_]{3,32}$/u.test(studentUsername)
    || studentPassword.length < 12
    || studentPassword.trim() !== studentPassword
    || !studentNumbers.includes(studentNumber)
  ) {
    throw new Error("acceptance student credentials differ from the exporter fixture identity set");
  }

  const publicClient = authenticatedClient(baseUrl, origin);
  const liveness = assertAPIResult("liveness", await getLiveness({ client: publicClient }));
  const readiness = assertAPIResult("readiness", await getReadiness({ client: publicClient }));
  const version = assertAPIResult("version", await getVersion({ client: publicClient }));
  const capabilities = assertAPIResult(
    "capabilities",
    await getCapabilities({ client: publicClient }),
  );
  if (liveness.status !== "alive" || readiness.status !== "ready") {
    throw new Error("server health contract is not ready");
  }
  if (version.commit !== expectedCommit || version.version !== expectedVersion) {
    throw new Error("running server provenance differs from the release manifest");
  }
  if (
    !capabilities.writesEnabled
    || capabilities.pintiaSnapshotSchema !== "ascendany.pintia.snapshot.v2"
    || capabilities.maxSubmissions !== 20_000
  ) {
    throw new Error("running server capabilities differ from the v2 E2E contract");
  }

  const adminPassword = (await readFile(adminPasswordPath, "utf8")).trim();
  const adminSession = assertAPIResult<AuthSession>(
    "administrator login",
    await loginAccount({
      client: publicClient,
      body: { username: "admin", password: adminPassword },
    }),
  );
  if (adminSession.account.role !== "admin") {
    throw new Error("bootstrap account did not authenticate as administrator");
  }
  const adminClient = authenticatedClient(baseUrl, origin, adminSession.accessToken);

  const upload = new Blob([snapshotBytes], {
    type: "application/vnd.ascendany.pintia.snapshot.v2+json",
  });
  const firstImport = assertAPIResult(
    "create Pintia import",
    await createPintiaImport({ client: adminClient, body: upload }),
  );
  const replayImport = assertAPIResult(
    "replay Pintia import",
    await createPintiaImport({
      client: adminClient,
      body: new Blob([snapshotBytes], {
        type: "application/vnd.ascendany.pintia.snapshot.v2+json",
      }),
    }),
  );
  if (
    replayImport.id !== firstImport.id
    || replayImport.artifactSha256 !== firstImport.artifactSha256
  ) {
    throw new Error("byte-identical import did not converge on one durable job");
  }
  const importJob = await waitForImport(adminClient, firstImport.id);
  if (importJob.status !== "succeeded" || importJob.examId === null || importJob.snapshotId === null) {
    throw new Error(`Pintia import terminated as ${importJob.status}`);
  }

  const domainDuplicate = assertAPIResult(
    "create typed-domain duplicate import",
    await createPintiaImport({
      client: adminClient,
      body: new Blob([JSON.stringify(snapshot, null, 2)], {
        type: "application/vnd.ascendany.pintia.snapshot.v2+json",
      }),
    }),
  );
  if (
    domainDuplicate.id === firstImport.id
    || domainDuplicate.artifactSha256 === firstImport.artifactSha256
  ) {
    throw new Error("byte-distinct typed-domain duplicate reused the byte-idempotent job");
  }
  const duplicateJob = await waitForImport(adminClient, domainDuplicate.id);
  if (duplicateJob.status !== "superseded") {
    throw new Error(`typed-domain duplicate terminated as ${duplicateJob.status}`);
  }

  const nextSnapshot = structuredClone(identity);
  nextSnapshot.exam.title = `${nextSnapshot.exam.title} · Revision 2`;
  nextSnapshot.exporter.exportedAt = new Date(
    Date.parse(nextSnapshot.exporter.exportedAt) + 1_000,
  ).toISOString();
  const nextImport = assertAPIResult(
    "create new logical-exam snapshot",
    await createPintiaImport({
      client: adminClient,
      body: new Blob([JSON.stringify(nextSnapshot)], {
        type: "application/vnd.ascendany.pintia.snapshot.v2+json",
      }),
    }),
  );
  const nextJob = await waitForImport(adminClient, nextImport.id);
  if (
    nextJob.status !== "succeeded"
    || nextJob.examId !== importJob.examId
    || nextJob.snapshotId === null
    || nextJob.snapshotId === importJob.snapshotId
  ) {
    throw new Error("new typed domain content did not publish one new immutable snapshot");
  }

  const exams = assertAPIResult("list exams", await listExams({ client: adminClient }));
  const exam = exams.items.find((candidate) => (
    candidate.problemSetId === identity.exam.problemSetId
  ));
  if (
    exam === undefined
    || exam.id !== importJob.examId
    || exam.snapshotId !== nextJob.snapshotId
    || exam.snapshotSequence !== 2
    || exams.items.length !== 1
  ) {
    throw new Error("imported logical exam is missing or noncanonical");
  }
  const generation = await waitForAnalytics(adminClient, exam.id);
  if (generation.status !== "succeeded") {
    throw new Error(`analytics generation terminated as ${generation.status}`);
  }

  const reviewContext = assertAPIResult(
    "recommendation review context",
    await getRecommendationReviewContext({ client: adminClient }),
  );
  const assignmentByIdentity = new Map(catalogDocument.problemAssignments.map((assignment) => [
    `${assignment.problemId}\0${assignment.problemFactSha256}`,
    assignment,
  ]));
  const reviewProblemKeys = reviewContext.problems.map((problem) => problem.problemKey);
  if (
    reviewContext.analyticsGenerationId !== generation.generationId
    || reviewContext.analyticsHeadRevision < 1
    || !/^[0-9a-f]{64}$/.test(reviewContext.inputManifestSha256)
    || reviewContext.problems.length !== assignmentByIdentity.size
    || !strictlyOrdered(reviewProblemKeys)
  ) {
    throw new Error("recommendation review context provenance is noncanonical");
  }
  for (const problem of reviewContext.problems) {
    const sourceProblemKey = `pintia:problem:${Buffer.byteLength(problem.problemId, "utf8")}:${problem.problemId}`;
    if (
      problem.platform !== "pintia"
      || problem.sourceProblemKey !== sourceProblemKey
      || problem.problemKey !== `${sourceProblemKey}:${problem.problemFactSha256}`
      || !assignmentByIdentity.has(`${problem.problemId}\0${problem.problemFactSha256}`)
      || problem.sourceProblemSets.length < 1
      || !problem.sourceProblemSets.some((source) => source.problemSetId === identity.exam.problemSetId)
    ) {
      throw new Error("recommendation review problem identity differs from the published catalog assignment");
    }
  }

  const catalogPublication = assertAPIResult(
    "read stopped-runtime recommendation knowledge catalog",
    await getConfiguration({
      client: adminClient,
      path: { key: "recommendation.catalog.active" },
    }),
  );
  if (
    catalogPublication.kind !== "knowledge_catalog"
    || catalogPublication.headRevision !== 1
    || catalogPublication.activeVersion?.number !== 1
    || catalogPublication.activeVersion.schemaId
      !== "ascendany.knowledge_catalog.recommendation.v1"
    || catalogPublication.activeVersion.documentSha256 !== expectedCatalogSHA256
  ) {
    throw new Error("stopped-runtime knowledge catalog publication is noncanonical");
  }

  const managedStudents = assertAPIResult(
    "list managed students",
    await listManagedStudents({ client: adminClient, query: { limit: 100 } }),
  );
  const managedStudent = managedStudents.items.find((candidate) => (
    candidate.studentNumber === studentNumber
  ));
  if (
    managedStudent === undefined
    || managedStudent.account === null
    || managedStudent.account.username !== studentUsername
    || managedStudent.account.disabledAt !== null
  ) {
    throw new Error("persistent acceptance student binding is unavailable");
  }

  const studentSession = assertAPIResult<AuthSession>(
    "persistent acceptance student login",
    await loginAccount({
      client: publicClient,
      body: { username: studentUsername, password: studentPassword },
    }),
  );
  if (
    studentSession.account.role !== "student"
    || studentSession.account.studentNumber !== studentNumber
  ) {
    throw new Error("enrollment did not create the expected student binding");
  }
  const studentClient = authenticatedClient(baseUrl, origin, studentSession.accessToken);
  const analytics = assertAPIResult(
    "student analytics",
    await getSelfStudentAnalytics({ client: studentClient }),
  );
  const leaderboard = assertAPIResult(
    "student leaderboard",
    await getStudentLeaderboard({ client: studentClient, query: { limit: 100 } }),
  );
  if (analytics.state !== "ready" || leaderboard.state !== "ready") {
    throw new Error("published analytics are unavailable to the enrolled student");
  }
  if (!leaderboard.items.some((item) => item.studentNumber === studentNumber)) {
    throw new Error("enrolled student is missing from the published leaderboard");
  }

  const recommendation = assertAPIResult(
    "student recommendation",
    await getSelfRecommendation({ client: studentClient }),
  );
  if (
    recommendation.state !== "fresh"
    || recommendation.result.schema !== "ascendany.recommendation.inference-result.v1"
    || !/^[0-9a-f]{64}$/.test(recommendation.result.sha256)
    || recommendation.model.purpose !== expectedModelPurpose
    || recommendation.model.artifactSha256 !== expectedModelSHA256
    || recommendation.model.knowledgeCatalogSha256 !== expectedCatalogSHA256
    || recommendation.model.modelSchema !== "ascendany.recommendation.inference-model.v1"
    || recommendation.model.inferenceContract !== "ascendany.recommendation.inference.v1"
    || recommendation.model.modelHeadRevision !== recommendation.modelHeadRevision
    || recommendation.model.applicationVersion !== expectedVersion
    || recommendation.model.applicationCommit !== expectedCommit
    || recommendation.model.applicationBuildTime !== version.buildTime
  ) {
    throw new Error("fresh recommendation model/catalog provenance differs from the release contract");
  }
  const masteryIDs = recommendation.result.knowledgeMastery.map((item) => item.knowledgePointId);
  if (
    !strictlyOrdered(masteryIDs)
    || masteryIDs.length !== catalogKnowledgePointIDs.length
    || masteryIDs.some((value, index) => value !== catalogKnowledgePointIDs[index])
  ) {
    throw new Error("recommendation knowledge-mastery output is empty or noncanonical");
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
    throw new Error("ready recommendation learning path is empty or unordered");
  }

  const audit = assertAPIResult(
    "audit events",
    await listAuditEvents({ client: adminClient, query: { limit: 100 } }),
  );
  if (audit.items.length < 3) {
    throw new Error("business flow did not produce the expected durable audit trail");
  }

  process.stdout.write(`${JSON.stringify({
    schema: "ascendany.full-e2e.client.v1",
    releaseVerified: true,
    importStatus: importJob.status,
    importReplayConverged: true,
    typedDomainDuplicateStatus: duplicateJob.status,
    newSnapshotStatus: nextJob.status,
    snapshotSequence: exam.snapshotSequence,
    analyticsStatus: generation.status,
    recommendationReviewContextVerified: true,
    acceptanceAccountReused: true,
    studentAnalyticsState: analytics.state,
    leaderboardState: leaderboard.state,
    knowledgeCatalogVerified: true,
    knowledgeCatalogSha256: expectedCatalogSHA256,
    recommendationState: recommendation.state,
    recommendationResultSchema: recommendation.result.schema,
    recommendationResultStatus: recommendation.result.status,
    recommendationResultSha256: recommendation.result.sha256,
    recommendationOutputCount: recommendation.result.knowledgeMastery.length,
    recommendationOrdered: true,
    model: recommendation.model,
    examCount: exams.items.length,
    auditEventCount: audit.items.length,
  })}\n`);
}

await main();
