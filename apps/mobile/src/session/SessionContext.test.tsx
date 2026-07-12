import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { Account, BrowserSession, BrowserSessionListener, BrowserSessionSnapshot } from "@ascendany/sdk";
import { SessionProvider, useSession } from "./SessionContext";

const account: Account = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  username: "student-1",
  displayName: "学生一",
  studentNumber: "20260001",
  role: "student",
  authRevision: 1,
};

function fakeBrowserSession() {
  let snapshot: BrowserSessionSnapshot = { status: "anonymous" };
  const listeners = new Set<BrowserSessionListener>();
  const emit = (next: BrowserSessionSnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener(next);
  };
  const session = {
    client: {},
    snapshot: vi.fn(() => snapshot),
    subscribe: vi.fn((listener: BrowserSessionListener) => {
      listeners.add(listener);
      listener(snapshot);
      return () => listeners.delete(listener);
    }),
    bootstrap: vi.fn(async () => snapshot),
    login: vi.fn(async () => {
      emit({ status: "authenticated", account, expiresAt: "2026-07-11T05:00:00Z" });
      return account;
    }),
    consumeEnrollment: vi.fn(async () => {
      emit({ status: "authenticated", account, expiresAt: "2026-07-11T05:00:00Z" });
      return account;
    }),
    logout: vi.fn(async () => emit({ status: "anonymous" })),
  };
  return { session: session as unknown as BrowserSession };
}

describe("SessionProvider", () => {
  it("bootstraps and delegates login/logout to BrowserSession", async () => {
    const fake = fakeBrowserSession();
    const wrapper = ({ children }: { children: ReactNode }) => <SessionProvider session={fake.session}>{children}</SessionProvider>;
    const { result } = renderHook(() => useSession(), { wrapper });

    expect(result.current.status).toBe("booting");
    await waitFor(() => expect(result.current.status).toBe("anonymous"));
    await act(async () => result.current.login("student-1", "correct horse battery staple"));
    expect(fake.session.login).toHaveBeenCalledWith({ username: "student-1", password: "correct horse battery staple" });
    expect(result.current.status).toBe("authenticated");
    expect(result.current.account).toEqual(account);

    await act(async () => result.current.logout());
    expect(fake.session.logout).toHaveBeenCalledTimes(1);
    expect(result.current.status).toBe("anonymous");
  });

  it("consumes an enrollment claim without storing the credential in state", async () => {
    const fake = fakeBrowserSession();
    const wrapper = ({ children }: { children: ReactNode }) => <SessionProvider session={fake.session}>{children}</SessionProvider>;
    const { result } = renderHook(() => useSession(), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("anonymous"));

    await act(async () => result.current.consumeEnrollment("claim-secret", "new secure password"));

    expect(fake.session.consumeEnrollment).toHaveBeenCalledWith({ token: "claim-secret", password: "new secure password" });
    expect(result.current.status).toBe("authenticated");
    expect(result.current.account).toEqual(account);
  });
});
