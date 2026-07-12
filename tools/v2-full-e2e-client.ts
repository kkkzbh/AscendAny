import { readFile } from "node:fs/promises";
import {
  consumeEnrollmentClaim,
  createClient,
  createPintiaImport,
  getCapabilities,
  getExamAnalysisGeneration,
  getImportJob,
  getLiveness,
  getReadiness,
  getSelfStudentAnalytics,
  getStudentLeaderboard,
  getVersion,
  issueEnrollmentClaim,
  listAuditEvents,
  listExams,
  listManagedStudents,
  loginAccount,
  type AuthSession,
  type ExamAnalysisGeneration,
  type ImportJob,
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
    await new Promise((resolve) => setTimeout(resolve, 200));
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
    await new Promise((resolve) => setTimeout(resolve, 200));
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

  const snapshotBytes = await readFile(snapshotPath);
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
  const [studentNumber] = studentNumbers;

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

  const managedStudents = assertAPIResult(
    "list managed students",
    await listManagedStudents({ client: adminClient, query: { limit: 100 } }),
  );
  const managedStudent = managedStudents.items.find((candidate) => (
    candidate.studentNumber === studentNumber
  ));
  if (managedStudent === undefined || managedStudent.account !== null) {
    throw new Error("imported student identity is unavailable for enrollment");
  }

  const issued = assertAPIResult(
    "issue enrollment",
    await issueEnrollmentClaim({
      client: adminClient,
      body: {
        username: "e2estudent01",
        displayName: "E2E Student",
        studentNumber,
        expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
      },
    }),
  );
  const studentPassword = "e2e-student-password-2026";
  const studentSession = assertAPIResult<AuthSession>(
    "consume enrollment",
    await consumeEnrollmentClaim({
      client: publicClient,
      body: { token: issued.token, password: studentPassword },
    }),
  );
  if (
    studentSession.account.role !== "student"
    || studentSession.account.studentNumber !== studentNumber
  ) {
    throw new Error("enrollment did not create the expected student binding");
  }
  const replayClaim = await consumeEnrollmentClaim({
    client: publicClient,
    body: { token: issued.token, password: studentPassword },
  });
  if (replayClaim.data !== undefined || replayClaim.response?.status !== 401) {
    throw new Error("consumed enrollment token was accepted more than once");
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
    enrollmentSingleUse: true,
    studentAnalyticsState: analytics.state,
    leaderboardState: leaderboard.state,
    examCount: exams.items.length,
    auditEventCount: audit.items.length,
  })}\n`);
}

await main();
