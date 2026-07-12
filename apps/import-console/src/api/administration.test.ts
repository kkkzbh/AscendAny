import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  AuditEventPage,
  IssuedEnrollment,
  ManagedAccount,
  ManagedAccountPage,
  ManagedStudentPage,
} from "@ascendany/sdk";
import {
  changeManagedAccountState,
  getAuditEvents,
  getManagedAccounts,
  getManagedStudents,
  issueManagedEnrollmentClaim,
  revokeManagedEnrollmentClaim,
} from "./administration";

const sdk = vi.hoisted(() => ({
  issueEnrollmentClaim: vi.fn(),
  listAuditEvents: vi.fn(),
  listManagedAccounts: vi.fn(),
  listManagedStudents: vi.fn(),
  revokeEnrollmentClaim: vi.fn(),
  setManagedAccountState: vi.fn(),
}));

const transport = vi.hoisted(() => ({
  ensureAuthenticated: vi.fn(),
  client: { kind: "browser-session-client" },
}));

vi.mock("@ascendany/sdk", () => sdk);
vi.mock("./v2Client", () => ({
  browserSession: { ensureAuthenticated: transport.ensureAuthenticated },
  v2Client: transport.client,
  apiFailureMessage: (error: unknown) => error instanceof Error ? error.message : "请求失败",
}));

const account: ManagedAccount = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  username: "student_1",
  displayName: "Student",
  studentNumber: "20260001",
  role: "student",
  authRevision: 1,
  disabledAt: null,
  createdAt: "2026-07-11T00:00:00Z",
  updatedAt: "2026-07-11T00:00:00Z",
  activeSessionCount: 1,
};

const issuedEnrollment: IssuedEnrollment = {
  grant: {
    id: "223e4567-e89b-42d3-a456-426614174000",
    username: "student_1",
    displayName: "Student",
    studentNumber: "20260001",
    issuerAccountId: "323e4567-e89b-42d3-a456-426614174000",
    issuedAt: "2026-07-11T01:00:00.000Z",
    expiresAt: "2026-07-12T01:00:00.000Z",
  },
  token: "enrollment-token",
};

describe("v2 administration API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    transport.ensureAuthenticated.mockResolvedValue(undefined);
    sdk.listManagedAccounts.mockResolvedValue({ data: { items: [account], nextCursor: null } satisfies ManagedAccountPage });
    sdk.listManagedStudents.mockResolvedValue({ data: { items: [], nextCursor: null } satisfies ManagedStudentPage });
    sdk.listAuditEvents.mockResolvedValue({ data: { items: [], nextCursor: null } satisfies AuditEventPage });
    sdk.setManagedAccountState.mockResolvedValue({ data: account });
    sdk.issueEnrollmentClaim.mockResolvedValue({ data: issuedEnrollment });
    sdk.revokeEnrollmentClaim.mockResolvedValue({ data: undefined });
  });

  it("refreshes one BrowserSession before every generated read", async () => {
    await getManagedAccounts(10, account.id);
    await getManagedStudents(20, "student-cursor");
    await getAuditEvents(30, "9");

    expect(transport.ensureAuthenticated).toHaveBeenCalledTimes(3);
    expect(sdk.listManagedAccounts).toHaveBeenCalledWith({
      client: transport.client,
      query: { limit: 10, cursor: account.id },
      throwOnError: true,
    });
    expect(sdk.listManagedStudents).toHaveBeenCalledWith({
      client: transport.client,
      query: { limit: 20, cursor: "student-cursor" },
      throwOnError: true,
    });
    expect(sdk.listAuditEvents).toHaveBeenCalledWith({
      client: transport.client,
      query: { limit: 30, cursor: "9" },
      throwOnError: true,
    });
  });

  it("uses the generated idempotent account-state mutation", async () => {
    await expect(changeManagedAccountState(account.id, true)).resolves.toEqual(account);
    expect(sdk.setManagedAccountState).toHaveBeenCalledWith({
      client: transport.client,
      path: { accountId: account.id },
      body: { disabled: true },
      throwOnError: true,
    });
    expect(transport.ensureAuthenticated.mock.invocationCallOrder[0]).toBeLessThan(
      sdk.setManagedAccountState.mock.invocationCallOrder[0] ?? 0,
    );
  });

  it("converts the explicit lifetime to expiresAt for the generated issue operation", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-11T01:00:00.000Z"));

    await expect(issueManagedEnrollmentClaim({
      username: "student_1",
      displayName: "Student",
      studentNumber: "20260001",
      expiresInSeconds: 86_400,
    })).resolves.toEqual(issuedEnrollment);

    expect(sdk.issueEnrollmentClaim).toHaveBeenCalledWith({
      client: transport.client,
      body: {
        username: "student_1",
        displayName: "Student",
        studentNumber: "20260001",
        expiresAt: "2026-07-12T01:00:00.000Z",
      },
      throwOnError: true,
    });
    expect(transport.ensureAuthenticated.mock.invocationCallOrder[0]).toBeLessThan(
      sdk.issueEnrollmentClaim.mock.invocationCallOrder[0] ?? 0,
    );
    now.mockRestore();
  });

  it("uses the generated revoke operation and preserves its public conflict", async () => {
    await expect(revokeManagedEnrollmentClaim(issuedEnrollment.grant.id)).resolves.toBeUndefined();
    expect(sdk.revokeEnrollmentClaim).toHaveBeenCalledWith({
      client: transport.client,
      path: { grantId: issuedEnrollment.grant.id },
      throwOnError: true,
    });

    sdk.revokeEnrollmentClaim.mockRejectedValueOnce(new Error("凭据已过期、已消费或已撤销（409）"));
    await expect(revokeManagedEnrollmentClaim(issuedEnrollment.grant.id)).rejects.toThrow(
      "凭据已过期、已消费或已撤销（409）",
    );
  });
});
