import { act, renderHook, waitFor } from "@testing-library/react";
import type {
  Account,
  BrowserSession,
  BrowserSessionListener,
  BrowserSessionSnapshot,
} from "@ascendany/sdk";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { SessionProvider } from "../src/session/SessionProvider";
import { useSession } from "../src/session/context";

const account: Account = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  username: "student-1",
  displayName: "学生一",
  studentNumber: "20260001",
  role: "student",
  authRevision: 1,
};

function fakeBrowserSession(): BrowserSession {
  let snapshot: BrowserSessionSnapshot = { status: "anonymous" };
  const listeners = new Set<BrowserSessionListener>();
  const emit = (next: BrowserSessionSnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener(next);
  };
  return {
    client: {},
    snapshot: vi.fn(() => snapshot),
    subscribe: vi.fn((listener: BrowserSessionListener) => {
      listeners.add(listener);
      listener(snapshot);
      return () => listeners.delete(listener);
    }),
    bootstrap: vi.fn(async () => snapshot),
    login: vi.fn(async () => {
      emit({
        status: "authenticated",
        account,
        expiresAt: "2026-07-11T05:00:00Z",
      });
      return account;
    }),
    consumeEnrollment: vi.fn(async () => {
      emit({
        status: "authenticated",
        account,
        expiresAt: "2026-07-11T05:00:00Z",
      });
      return account;
    }),
    logout: vi.fn(async () => emit({ status: "anonymous" })),
  } as unknown as BrowserSession;
}

function wrapperFor(session: BrowserSession) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <SessionProvider session={session}>{children}</SessionProvider>;
  };
}

describe("desktop SessionProvider", () => {
  it("keeps a bootstrap failure visible until explicitly cleared", async () => {
    const session = fakeBrowserSession();
    vi.mocked(session.bootstrap).mockRejectedValueOnce(
      new Error("安全会话恢复失败"),
    );
    const { result } = renderHook(() => useSession(), {
      wrapper: wrapperFor(session),
    });

    await waitFor(() => expect(result.current.status).toBe("anonymous"));
    expect(result.current.error).toBe("安全会话恢复失败");

    act(() => result.current.clearError());
    expect(result.current.error).toBeNull();
  });

  it("delegates login, enrollment claim, and logout to BrowserSession", async () => {
    const session = fakeBrowserSession();
    const { result } = renderHook(() => useSession(), {
      wrapper: wrapperFor(session),
    });
    await waitFor(() => expect(result.current.status).toBe("anonymous"));

    await act(async () => {
      await result.current.login("student-1", "correct horse battery staple");
    });
    expect(session.login).toHaveBeenCalledWith({
      username: "student-1",
      password: "correct horse battery staple",
    });
    expect(result.current.account).toEqual(account);

    await act(async () => {
      await result.current.logout();
    });
    expect(result.current.status).toBe("anonymous");

    await act(async () => {
      await result.current.consumeEnrollment("claim-secret", "new secure password");
    });
    expect(session.consumeEnrollment).toHaveBeenCalledWith({
      token: "claim-secret",
      password: "new secure password",
    });
  });
});
