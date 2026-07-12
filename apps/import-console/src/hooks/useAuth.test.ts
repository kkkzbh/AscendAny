import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Account, BrowserSessionListener, BrowserSessionSnapshot } from "@ascendany/sdk";
import { useAuth } from "./useAuth";

const admin: Account = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  username: "admin",
  displayName: "管理员",
  studentNumber: null,
  role: "admin",
  authRevision: 1,
};

const session = vi.hoisted(() => {
  let snapshot: BrowserSessionSnapshot = { status: "anonymous" };
  const listeners = new Set<BrowserSessionListener>();
  const emit = (next: BrowserSessionSnapshot) => {
    snapshot = next;
    for (const listener of listeners) listener(next);
  };
  return {
    reset() {
      snapshot = { status: "anonymous" };
      listeners.clear();
    },
    snapshot: vi.fn(() => snapshot),
    subscribe: vi.fn((listener: BrowserSessionListener) => {
      listeners.add(listener);
      listener(snapshot);
      return () => listeners.delete(listener);
    }),
    bootstrap: vi.fn(async () => snapshot),
    login: vi.fn(async () => {
      emit({ status: "authenticated", account: admin, expiresAt: "2026-07-11T05:00:00Z" });
      return admin;
    }),
    logout: vi.fn(async () => {
      emit({ status: "anonymous" });
    }),
    emit,
  };
});

vi.mock("../api/v2Client", () => ({
  browserSession: session,
  apiFailureMessage: (error: unknown) => error instanceof Error ? error.message : "会话失败",
}));

describe("useAuth", () => {
  beforeEach(() => {
    session.reset();
    session.snapshot.mockClear();
    session.subscribe.mockClear();
    session.bootstrap.mockReset();
    session.bootstrap.mockImplementation(async () => session.snapshot());
    session.login.mockClear();
    session.logout.mockClear();
  });

  it("bootstraps the BrowserSession before exposing an anonymous state", async () => {
    const { result } = renderHook(() => useAuth());

    expect(result.current.status).toBe("initializing");
    await waitFor(() => expect(result.current.status).toBe("anonymous"));
    expect(session.bootstrap).toHaveBeenCalledTimes(1);
  });

  it("uses BrowserSession login and logout snapshots as the only auth state", async () => {
    const { result } = renderHook(() => useAuth());
    await waitFor(() => expect(result.current.status).toBe("anonymous"));

    await act(async () => result.current.login("admin", "correct horse battery staple"));
    expect(session.login).toHaveBeenCalledWith({
      username: "admin",
      password: "correct horse battery staple",
    });
    expect(result.current.status).toBe("authenticated");
    expect(result.current.account).toEqual(admin);

    await act(async () => result.current.logout());
    expect(session.logout).toHaveBeenCalledTimes(1);
    expect(result.current.status).toBe("anonymous");
    expect(result.current.account).toBeNull();
  });

  it("surfaces a bootstrap failure after session cleanup", async () => {
    session.bootstrap.mockRejectedValueOnce(new Error("会话恢复失败"));
    const { result } = renderHook(() => useAuth());

    await waitFor(() => expect(result.current.status).toBe("anonymous"));
    expect(result.current.error).toBe("会话恢复失败");
  });
});
