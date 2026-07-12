import {
  issueEnrollmentClaim,
  listAuditEvents,
  listManagedAccounts,
  listManagedStudents,
  revokeEnrollmentClaim,
  setManagedAccountState,
  type AuditEventPage,
  type EnrollmentGrantId,
  type EnrollmentIssueRequest,
  type IssuedEnrollment,
  type ManagedAccount,
  type ManagedAccountPage,
  type ManagedStudentPage,
} from "@ascendany/sdk";
import { apiFailureMessage, browserSession, v2Client } from "./v2Client";

export type { AuditEvent, AuditEventPage, ManagedAccount, ManagedAccountPage, ManagedStudent, ManagedStudentPage } from "@ascendany/sdk";

export type IssueManagedEnrollmentClaimInput = Omit<EnrollmentIssueRequest, "expiresAt"> & {
  expiresInSeconds: number;
};

async function authenticated<T>(operation: () => Promise<T>): Promise<T> {
  try {
    await browserSession.ensureAuthenticated();
    return await operation();
  } catch (error) {
    throw new Error(apiFailureMessage(error));
  }
}

export function getManagedAccounts(limit = 30, cursor?: string): Promise<ManagedAccountPage> {
  return authenticated(async () => {
    const result = await listManagedAccounts({
      client: v2Client,
      query: { limit, ...(cursor ? { cursor } : {}) },
      throwOnError: true,
    });
    return result.data;
  });
}

export function getManagedStudents(limit = 30, cursor?: string): Promise<ManagedStudentPage> {
  return authenticated(async () => {
    const result = await listManagedStudents({
      client: v2Client,
      query: { limit, ...(cursor ? { cursor } : {}) },
      throwOnError: true,
    });
    return result.data;
  });
}

export function getAuditEvents(limit = 30, cursor?: string): Promise<AuditEventPage> {
  return authenticated(async () => {
    const result = await listAuditEvents({
      client: v2Client,
      query: { limit, ...(cursor ? { cursor } : {}) },
      throwOnError: true,
    });
    return result.data;
  });
}

export function changeManagedAccountState(accountId: string, disabled: boolean): Promise<ManagedAccount> {
  return authenticated(async () => {
    const result = await setManagedAccountState({
      client: v2Client,
      path: { accountId },
      body: { disabled },
      throwOnError: true,
    });
    return result.data;
  });
}

export function issueManagedEnrollmentClaim(
  input: IssueManagedEnrollmentClaimInput,
): Promise<IssuedEnrollment> {
  return authenticated(async () => {
    const expiresAt = new Date(Date.now() + input.expiresInSeconds * 1_000).toISOString();
    const result = await issueEnrollmentClaim({
      client: v2Client,
      body: {
        username: input.username,
        displayName: input.displayName,
        studentNumber: input.studentNumber,
        expiresAt,
      },
      throwOnError: true,
    });
    return result.data;
  });
}

export function revokeManagedEnrollmentClaim(grantId: EnrollmentGrantId): Promise<void> {
  return authenticated(async () => {
    await revokeEnrollmentClaim({
      client: v2Client,
      path: { grantId },
      throwOnError: true,
    });
  });
}
