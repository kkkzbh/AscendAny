import { describe, expect, it } from "vitest";
import {
  archiveAgentNote,
  consumeEnrollmentClaim,
  createAgentNote,
  createConfigurationVersion,
  createClient,
  createPintiaImport,
  getCurrentAccount,
  getAgentNote,
  getConfiguration,
  getExam,
  getImportJob,
  getSelfStudentAnalytics,
  getStudentLeaderboard,
  issueEnrollmentClaim,
  listAuditEvents,
  listAccountSessions,
  listAgentNotes,
  listConfigurations,
  listConfigurationVersions,
  listExams,
  listImportJobs,
  listManagedAccounts,
  listManagedStudents,
  loginAccount,
  logoutSession,
  refreshSession,
  replaceAgentNote,
  restoreAgentNote,
  revokeAccountSession,
  revokeEnrollmentClaim,
  setManagedAccountState,
  submitAuthenticatedFeedback,
  updateAccountProfile,
} from "../src";

const JOB_ID = "123e4567-e89b-42d3-a456-426614174000";
const API_ERROR = {
  code: "not_found",
  message: "Import job was not found.",
  requestId: "123e4567-e89b-12d3-a456-426614174001",
} as const;

const AUTH_SESSION = {
  accessToken: "header.payload.signature",
  expiresAt: "2026-07-10T00:15:00.000Z",
  csrfToken: "A".repeat(43),
  account: {
    id: "123e4567-e89b-42d3-a456-426614174010",
    username: "student_1",
    displayName: "Student",
    studentNumber: "20260001",
    role: "student" as const,
    authRevision: 1,
  },
};

function importJob() {
  return {
    id: JOB_ID,
    artifactSha256: "a".repeat(64),
    status: "queued" as const,
    stage: "queued",
    createdAt: "2026-07-10T00:00:00.000Z",
    updatedAt: "2026-07-10T00:00:00.000Z",
    examId: null,
    snapshotId: null,
    error: null,
  };
}

describe("generated AscendAny SDK", () => {
  it("applies base URL, bearer auth, and the Pintia vendor media type", async () => {
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify(importJob()), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "test-bearer-token",
      fetch: fetchMock,
    });

    const result = await createPintiaImport({
      client,
      body: new Blob(["{\"schema\":\"ascendany.pintia.snapshot.v2\"}"], {
        type: "application/json",
      }),
    });

    expect(result.data?.id).toBe(JOB_ID);
    expect(requests).toHaveLength(1);
    const request = requests[0];
    expect(request?.url).toBe("https://ascendany.invalid/api/v2/imports/pintia");
    expect(request?.headers.get("Authorization")).toBe("Bearer test-bearer-token");
    expect(request?.headers.get("Content-Type")).toBe(
      "application/vnd.ascendany.pintia.snapshot.v2+json",
    );
    expect(await request?.text()).toBe("{\"schema\":\"ascendany.pintia.snapshot.v2\"}");
  });

  it("serializes the UUID path and returns typed API errors without throwing by default", async () => {
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify(API_ERROR), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({ baseUrl: "https://ascendany.invalid", fetch: fetchMock });

    const result = await getImportJob({ client, path: { jobId: JOB_ID } });

    expect(requests[0]?.url).toBe(`https://ascendany.invalid/api/v2/imports/${JOB_ID}`);
    expect(result.data).toBeUndefined();
    expect(result.error).toEqual(API_ERROR);
    expect(result.response?.status).toBe(404);
  });

  it("serializes durable import history cursor pagination", async () => {
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify({ items: [importJob()], nextCursor: JOB_ID }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "admin-access-token",
      fetch: fetchMock,
    });

    const result = await listImportJobs({
      client,
      query: { cursor: JOB_ID, limit: 2 },
    });

    expect(result.data?.items[0]?.id).toBe(JOB_ID);
    const request = requests[0];
    const url = new URL(request?.url ?? "https://invalid.invalid");
    expect(url.pathname).toBe("/api/v2/imports");
    expect(url.searchParams.get("cursor")).toBe(JOB_ID);
    expect(url.searchParams.get("limit")).toBe("2");
    expect(request?.headers.get("Authorization")).toBe("Bearer admin-access-token");
  });

  it("throws the parsed API error when throwOnError is explicit", async () => {
    const fetchMock: typeof fetch = async () => new Response(JSON.stringify(API_ERROR), {
      status: 404,
      headers: { "Content-Type": "application/json" },
    });
    const client = createClient({ baseUrl: "https://ascendany.invalid", fetch: fetchMock });

    await expect(getImportJob({
      client,
      path: { jobId: JOB_ID },
      throwOnError: true,
    })).rejects.toEqual(API_ERROR);
  });

  it("uses generated v2 auth operations with browser-managed refresh cookies", async () => {
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      if (request.url.endsWith("/logout")) {
        return new Response(null, { status: 204 });
      }
      const responseBody = request.url.endsWith("/me") ? AUTH_SESSION.account : AUTH_SESSION;
      return new Response(JSON.stringify(responseBody), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      credentials: "include",
      auth: (security) => security.in === "cookie" ? undefined : "test-access-token",
      fetch: fetchMock,
    });
    const loggedIn = await loginAccount({
      client,
      body: { username: "student_1", password: "long-enough-password" },
    });
    const refreshed = await refreshSession({
      client,
      headers: { "X-AscendAny-CSRF": AUTH_SESSION.csrfToken },
    });
    const current = await getCurrentAccount({ client });
    await logoutSession({
      client,
      headers: { "X-AscendAny-CSRF": AUTH_SESSION.csrfToken },
    });
    expect(loggedIn.data).toEqual(AUTH_SESSION);
    expect(refreshed.data).toEqual(AUTH_SESSION);
    expect(current.data).toEqual(AUTH_SESSION.account);
    expect(requests.map((request) => new URL(request.url).pathname)).toEqual([
      "/api/v2/auth/login",
      "/api/v2/auth/refresh",
      "/api/v2/auth/me",
      "/api/v2/auth/logout",
    ]);
    for (const request of requests) {
      expect(request.credentials).toBe("include");
      expect(request.headers.has("Cookie")).toBe(false);
    }
    expect(requests[0] && JSON.parse(await requests[0].text())).toEqual({
      username: "student_1",
      password: "long-enough-password",
    });
    expect(requests[1]?.headers.get("X-AscendAny-CSRF")).toBe(AUTH_SESSION.csrfToken);
    expect(requests[2]?.headers.get("Authorization")).toBe("Bearer test-access-token");
    expect(requests[3]?.headers.get("Authorization")).toBe("Bearer test-access-token");
  });

  it("uses generated enrollment issue, consume, and revoke contracts", async () => {
    const requests: Request[] = [];
    const grantId = "123e4567-e89b-42d3-a456-426614174020";
    const enrollmentToken = "A".repeat(43);
    const issued = {
      grant: {
        id: grantId,
        username: "student_20",
        displayName: "Student Twenty",
        studentNumber: "20260020",
        issuerAccountId: "123e4567-e89b-42d3-a456-426614174021",
        issuedAt: "2026-07-11T03:04:05Z",
        expiresAt: "2026-07-17T03:04:05Z",
      },
      token: enrollmentToken,
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      if (request.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      const body = request.url.endsWith("/consume") ? AUTH_SESSION : issued;
      return new Response(JSON.stringify(body), {
        status: request.url.endsWith("/consume") ? 200 : 201,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      credentials: "include",
      auth: "admin-access-token",
      fetch: fetchMock,
    });

    const issueResult = await issueEnrollmentClaim({
      client,
      body: {
        username: "student_20",
        displayName: "Student Twenty",
        studentNumber: "20260020",
        expiresAt: "2026-07-17T03:04:05Z",
      },
    });
    const claimResult = await consumeEnrollmentClaim({
      client,
      body: { token: enrollmentToken, password: "long-enough-password" },
    });
    const revokeResult = await revokeEnrollmentClaim({ client, path: { grantId } });

    expect(issueResult.data).toEqual(issued);
    expect(claimResult.data).toEqual(AUTH_SESSION);
    expect(revokeResult.response?.status).toBe(204);
    expect(requests.map((request) => [request.method, new URL(request.url).pathname])).toEqual([
      ["POST", "/api/v2/admin/enrollment-claims"],
      ["POST", "/api/v2/auth/enrollment-claims/consume"],
      ["DELETE", `/api/v2/admin/enrollment-claims/${grantId}`],
    ]);
    expect(requests[0]?.headers.get("Authorization")).toBe("Bearer admin-access-token");
    expect(requests[1]?.headers.has("Authorization")).toBe(false);
    expect(requests[2]?.headers.get("Authorization")).toBe("Bearer admin-access-token");
    expect(requests.every((request) => request.credentials === "include")).toBe(true);
    expect(requests[0] && JSON.parse(await requests[0].text())).toEqual({
      username: "student_20",
      displayName: "Student Twenty",
      studentNumber: "20260020",
      expiresAt: "2026-07-17T03:04:05Z",
    });
    expect(requests[1] && JSON.parse(await requests[1].text())).toEqual({
      token: enrollmentToken,
      password: "long-enough-password",
    });
    expect(await requests[2]?.text()).toBe("");
  });

  it("uses generated account profile and session-management contracts", async () => {
    const requests: Request[] = [];
    const sessionId = "123e4567-e89b-42d3-a456-426614174081";
    const account = AUTH_SESSION.account;
    const sessions = {
      items: [
        {
          id: sessionId,
          createdAt: "2026-07-10T00:00:00Z",
          expiresAt: "2026-07-17T00:00:00Z",
          lastSeenAt: "2026-07-11T00:00:00Z",
          revokedAt: null,
          revocationReason: null,
          current: true,
          active: true,
        },
      ],
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      if (request.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      const body = request.method === "PATCH" ? account : sessions;
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "account-access-token",
      fetch: fetchMock,
    });

    const updated = await updateAccountProfile({
      client,
      body: { displayName: "Student" },
    });
    const listed = await listAccountSessions({ client });
    const revoked = await revokeAccountSession({ client, path: { sessionId } });

    expect(updated.data).toEqual(account);
    expect(listed.data).toEqual(sessions);
    expect(revoked.response?.status).toBe(204);
    expect(requests.map((request) => [request.method, new URL(request.url).pathname])).toEqual([
      ["PATCH", "/api/v2/account/profile"],
      ["GET", "/api/v2/account/sessions"],
      ["DELETE", `/api/v2/account/sessions/${sessionId}`],
    ]);
    expect(requests.every(
      (request) => request.headers.get("Authorization") === "Bearer account-access-token",
    )).toBe(true);
    expect(requests[0] && JSON.parse(await requests[0].text())).toEqual({
      displayName: "Student",
    });
    expect(await requests[1]?.text()).toBe("");
    expect(await requests[2]?.text()).toBe("");
  });

  it("serializes the self analytics history limit and returns the state union", async () => {
    const requests: Request[] = [];
    const responseBody = {
      state: "no_observations" as const,
      headRevision: 4,
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify(responseBody), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const result = await getSelfStudentAnalytics({ client, query: { limit: 100 } });

    expect(result.data).toEqual(responseBody);
    expect(requests[0]?.url).toBe(
      "https://ascendany.invalid/api/v2/students/me/analytics?limit=100",
    );
    expect(requests[0]?.headers.get("Authorization")).toBe("Bearer student-access-token");
  });

  it("serializes the student leaderboard limit and returns its state union", async () => {
    const requests: Request[] = [];
    const responseBody = {
      state: "ready" as const,
      headRevision: 7,
      population: 1,
      items: [
        {
          rank: 1,
          studentNumber: "20260001",
          displayName: "Student",
          rating: 1512,
          metrics: {
            knowledge: 90,
            accuracy: 88,
            quality: null,
            flexibility: 75,
            proficiency: 82,
          },
        },
      ],
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify(responseBody), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const result = await getStudentLeaderboard({ client, query: { limit: 200 } });

    expect(result.data).toEqual(responseBody);
    expect(requests[0]?.url).toBe(
      "https://ascendany.invalid/api/v2/students/leaderboard?limit=200",
    );
    expect(requests[0]?.headers.get("Authorization")).toBe("Bearer student-access-token");
  });

  it("serializes exam catalog pagination and exact detail IDs", async () => {
    const requests: Request[] = [];
    const examId = "123e4567-e89b-42d3-a456-426614174091";
    const summary = {
      id: examId,
      snapshotId: "123e4567-e89b-42d3-a456-426614174092",
      platform: "pintia" as const,
      problemSetId: "2039341868571590656",
      title: "Training",
      sourceUrl: "https://pintia.cn/problem-sets/2039341868571590656",
      startsAt: null,
      endsAt: null,
      totalScore: "300.0",
      problemCount: 1,
      participantCount: 35,
      rankingCount: 35,
      submissionCount: 624,
      snapshotSequence: 1,
      headRevision: 1,
      exporterVersion: "2.0.5",
      exportedAt: "2026-07-10T06:52:38Z",
      updatedAt: "2026-07-10T06:53:00Z",
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      const body = request.url.includes(`/exams/${examId}`)
        ? {
            ...summary,
            problems: [{
              id: "problem-set-problem-1",
              problemId: "problem-1",
              label: "7-1",
              title: "A+B",
              maxScore: "20.0",
              timeLimitMs: 400,
              memoryLimitBytes: 67108864,
              submissionCount: 40,
              submittingParticipantCount: 20,
              passedParticipantCount: 15,
            }],
          }
        : { items: [summary], nextCursor: examId };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "catalog-access-token",
      fetch: fetchMock,
    });

    const page = await listExams({ client, query: { cursor: examId, limit: 20 } });
    const detail = await getExam({ client, path: { examId } });

    expect(page.data?.items[0]?.id).toBe(examId);
    expect(detail.data?.problems[0]?.passedParticipantCount).toBe(15);
    const pageURL = new URL(requests[0]?.url ?? "https://invalid.invalid");
    expect(pageURL.pathname).toBe("/api/v2/exams");
    expect(pageURL.searchParams.get("cursor")).toBe(examId);
    expect(pageURL.searchParams.get("limit")).toBe("20");
    expect(requests[1]?.url).toBe(`https://ascendany.invalid/api/v2/exams/${examId}`);
    expect(requests.every(
      (request) => request.headers.get("Authorization") === "Bearer catalog-access-token",
    )).toBe(true);
  });

  it("uses generated administration account, student, and audit contracts", async () => {
    const requests: Request[] = [];
    const accountId = "123e4567-e89b-42d3-a456-426614174095";
    const managedAccount = {
      id: accountId,
      username: "student_95",
      displayName: "Student Ninety Five",
      studentNumber: "20260095",
      role: "student" as const,
      authRevision: 2,
      disabledAt: "2026-07-11T08:00:00Z",
      createdAt: "2026-07-10T08:00:00Z",
      updatedAt: "2026-07-11T08:00:00Z",
      activeSessionCount: 0,
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      const path = new URL(request.url).pathname;
      let body: unknown;
      if (request.method === "PATCH") {
        body = managedAccount;
      } else if (path.endsWith("/students")) {
        body = {
          items: [{
            studentNumber: "20260095",
            pintiaUserId: "pintia-user-95",
            sourceDisplayName: "Student Ninety Five",
            account: {
              id: accountId,
              username: "student_95",
              displayName: "Student Ninety Five",
              disabledAt: managedAccount.disabledAt,
            },
            rating: 1510,
          }],
          nextCursor: null,
        };
      } else if (path.endsWith("/audit-events")) {
        body = {
          items: [{
            id: "9",
            actorAccountId: "123e4567-e89b-42d3-a456-426614174096",
            actorSessionId: "123e4567-e89b-42d3-a456-426614174097",
            type: "admin.account_disabled",
            occurredAt: "2026-07-11T08:00:00Z",
            payload: { targetAccountId: accountId, disabled: true },
          }],
          nextCursor: null,
        };
      } else {
        body = { items: [managedAccount], nextCursor: accountId };
      }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "admin-access-token",
      fetch: fetchMock,
    });

    const accounts = await listManagedAccounts({
      client,
      query: { cursor: accountId, limit: 10 },
    });
    const students = await listManagedStudents({ client, query: { limit: 20 } });
    const audit = await listAuditEvents({ client, query: { cursor: "10", limit: 30 } });
    const changed = await setManagedAccountState({
      client,
      path: { accountId },
      body: { disabled: true },
    });

    expect(accounts.data?.items[0]?.id).toBe(accountId);
    expect(students.data?.items[0]?.rating).toBe(1510);
    expect(audit.data?.items[0]?.type).toBe("admin.account_disabled");
    expect(changed.data?.disabledAt).toBe(managedAccount.disabledAt);
    expect(requests.map((request) => [request.method, new URL(request.url).pathname])).toEqual([
      ["GET", "/api/v2/admin/accounts"],
      ["GET", "/api/v2/admin/students"],
      ["GET", "/api/v2/admin/audit-events"],
      ["PATCH", `/api/v2/admin/accounts/${accountId}/state`],
    ]);
    expect(requests.every(
      (request) => request.headers.get("Authorization") === "Bearer admin-access-token",
    )).toBe(true);
    expect(requests[3] && JSON.parse(await requests[3].text())).toEqual({ disabled: true });
  });

  it("uses generated immutable configuration management contracts", async () => {
    const requests: Request[] = [];
    const key = "feedback.delivery.github";
    const version = {
      number: 3,
      schemaId: "ascendany.feedback_delivery.github.v1",
      document: { provider: "github", repository: "owner/repository" },
      documentSha256: "b".repeat(64),
      credentialRef: "feedback.delivery.github_token",
      createdByAccountId: "123e4567-e89b-42d3-a456-426614174110",
      createdBySessionId: "123e4567-e89b-42d3-a456-426614174111",
      createdAt: "2026-07-11T09:00:00Z",
    };
    const item = {
      id: "123e4567-e89b-42d3-a456-426614174112",
      key,
      kind: "feedback_delivery" as const,
      headRevision: 3,
      activeVersion: version,
      createdAt: "2026-07-11T08:00:00Z",
      updatedAt: "2026-07-11T09:00:00Z",
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      const path = new URL(request.url).pathname;
      if (request.method === "POST") {
        return new Response(JSON.stringify({ item, idempotent: false }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      const body = path === "/api/v2/admin/configurations"
        ? { items: [item], nextCursor: key }
        : path.endsWith("/versions")
          ? {
              key,
              kind: "feedback_delivery",
              headRevision: 3,
              items: [version],
              nextBeforeNumber: 3,
            }
          : item;
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "admin-access-token",
      fetch: fetchMock,
    });

    const page = await listConfigurations({
      client,
      query: { kind: "feedback_delivery", afterKey: key, limit: 10 },
    });
    const created = await createConfigurationVersion({
      client,
      body: {
        key,
        kind: "feedback_delivery",
        expectedHeadRevision: 2,
        schemaId: version.schemaId,
        document: version.document,
        credentialRef: version.credentialRef,
      },
    });
    const current = await getConfiguration({ client, path: { key } });
    const versions = await listConfigurationVersions({
      client,
      path: { key },
      query: { beforeNumber: 4, limit: 20 },
    });

    expect(page.data?.items[0]?.kind).toBe("feedback_delivery");
    expect(created.data?.idempotent).toBe(false);
    expect(current.data?.activeVersion?.documentSha256).toBe("b".repeat(64));
    expect(versions.data?.nextBeforeNumber).toBe(3);
    expect(requests.map((request) => [request.method, new URL(request.url).pathname])).toEqual([
      ["GET", "/api/v2/admin/configurations"],
      ["POST", "/api/v2/admin/configurations/versions"],
      ["GET", `/api/v2/admin/configurations/${key}`],
      ["GET", `/api/v2/admin/configurations/${key}/versions`],
    ]);
    const listURL = new URL(requests[0]?.url ?? "https://invalid.invalid");
    expect(listURL.searchParams.get("kind")).toBe("feedback_delivery");
    expect(listURL.searchParams.get("afterKey")).toBe(key);
    const versionsURL = new URL(requests[3]?.url ?? "https://invalid.invalid");
    expect(versionsURL.searchParams.get("beforeNumber")).toBe("4");
    expect(requests.every(
      (request) => request.headers.get("Authorization") === "Bearer admin-access-token",
    )).toBe(true);
    expect(requests[1]?.headers.get("Content-Type")).toBe("application/json");
    expect(requests[1] && JSON.parse(await requests[1].text())).toEqual({
      key,
      kind: "feedback_delivery",
      expectedHeadRevision: 2,
      schemaId: version.schemaId,
      document: version.document,
      credentialRef: version.credentialRef,
    });
  });

  it("uses the generated authenticated feedback idempotency contract", async () => {
    const requests: Request[] = [];
    const feedbackId = "123e4567-e89b-42d3-a456-426614174120";
    const deliveryJobId = "123e4567-e89b-42d3-a456-426614174121";
    const clientRequestId = "123e4567-e89b-42d3-a456-426614174122";
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify({
        submission: {
          id: feedbackId,
          deliveryJobId,
          createdAt: "2026-07-11T10:00:00Z",
        },
        created: true,
      }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const result = await submitAuthenticatedFeedback({
      client,
      body: {
        clientRequestId,
        title: "Import feedback",
        content: "The snapshot import completed successfully.",
        platform: null,
        appVersion: "2.0.5",
      },
    });

    expect(result.data?.submission.id).toBe(feedbackId);
    expect(result.data?.created).toBe(true);
    expect(requests[0]?.method).toBe("POST");
    expect(requests[0]?.url).toBe("https://ascendany.invalid/api/v2/feedback");
    expect(requests[0]?.headers.get("Authorization")).toBe("Bearer student-access-token");
    expect(requests[0]?.headers.get("Content-Type")).toBe("application/json");
    expect(requests[0] && JSON.parse(await requests[0].text())).toEqual({
      clientRequestId,
      title: "Import feedback",
      content: "The snapshot import completed successfully.",
      platform: null,
      appVersion: "2.0.5",
    });
  });

  it("uses the generated student-owned Agent Notes REST contract", async () => {
    const requests: Request[] = [];
    const noteId = "123e4567-e89b-42d3-a456-426614174130";
    const mutationId = "123e4567-e89b-42d3-a456-426614174131";
    const cursor = "YWdlbnQtbm90ZS52MQAyMDI2LTA3LTExVDEwOjAwOjAwWgAxMjNlNDU2Ny1lODliLTQyZDMtYTQ1Ni00MjY2MTQxNzQxMzA";
    const note = {
      id: noteId,
      headRevision: 1,
      state: "active" as const,
      title: "Training plan",
      contentSha256: "c".repeat(64),
      currentMutationId: mutationId,
      currentOperation: "create" as const,
      currentRevisionCreatedAt: "2026-07-11T10:00:00Z",
      createdAt: "2026-07-11T10:00:00Z",
      updatedAt: "2026-07-11T10:00:00Z",
      content: "Study trees",
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      const url = new URL(request.url);
      if (request.method === "GET" && url.pathname.endsWith("/notes")) {
        const { content: _content, ...summary } = note;
        return new Response(JSON.stringify({ items: [summary], nextCursor: cursor }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (request.method === "GET") {
        return new Response(JSON.stringify(note), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({ note, idempotent: false }), {
        status: request.method === "POST" && url.pathname.endsWith("/notes") ? 201 : 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const page = await listAgentNotes({ client, query: { cursor, limit: 10 } });
    const created = await createAgentNote({
      client,
      body: { mutationId, expectedHeadRevision: 0, title: note.title, content: note.content },
    });
    const current = await getAgentNote({ client, path: { noteId } });
    const replaced = await replaceAgentNote({
      client,
      path: { noteId },
      body: { mutationId, expectedHeadRevision: 1, title: "Revised plan", content: "Study graphs" },
    });
    const archived = await archiveAgentNote({
      client,
      path: { noteId },
      body: { mutationId, expectedHeadRevision: 2 },
    });
    const restored = await restoreAgentNote({
      client,
      path: { noteId },
      body: { mutationId, expectedHeadRevision: 3 },
    });

    expect(page.data?.nextCursor).toBe(cursor);
    expect(created.data?.note.id).toBe(noteId);
    expect(current.data?.content).toBe("Study trees");
    expect(replaced.data?.idempotent).toBe(false);
    expect(archived.data?.note.id).toBe(noteId);
    expect(restored.data?.note.id).toBe(noteId);
    expect(requests.map((request) => [request.method, new URL(request.url).pathname])).toEqual([
      ["GET", "/api/v2/students/me/notes"],
      ["POST", "/api/v2/students/me/notes"],
      ["GET", `/api/v2/students/me/notes/${noteId}`],
      ["PUT", `/api/v2/students/me/notes/${noteId}/document`],
      ["POST", `/api/v2/students/me/notes/${noteId}/archive`],
      ["POST", `/api/v2/students/me/notes/${noteId}/restore`],
    ]);
    expect(requests.every(
      (request) => request.headers.get("Authorization") === "Bearer student-access-token",
    )).toBe(true);
    const listURL = new URL(requests[0]?.url ?? "https://invalid.invalid");
    expect(listURL.searchParams.get("cursor")).toBe(cursor);
    expect(listURL.searchParams.get("limit")).toBe("10");
    expect(requests[1] && JSON.parse(await requests[1].text())).toEqual({
      mutationId,
      expectedHeadRevision: 0,
      title: note.title,
      content: note.content,
    });
  });
});
