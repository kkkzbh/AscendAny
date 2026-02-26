import { beforeEach, describe, expect, it, vi } from "vitest";

const {
  fetchAuthMe,
  fetchAuthPolicy,
  postLogin,
  postLogout,
  postRefresh,
  postRegister,
  putAuthProfile,
} = vi.hoisted(() => ({
  fetchAuthMe: vi.fn(),
  fetchAuthPolicy: vi.fn(),
  postLogin: vi.fn(),
  postLogout: vi.fn(),
  postRefresh: vi.fn(),
  postRegister: vi.fn(),
  putAuthProfile: vi.fn(),
}));

const { cleanupLegacyAnonymousStorage, switchAccountNamespace } = vi.hoisted(() => ({
  cleanupLegacyAnonymousStorage: vi.fn(),
  switchAccountNamespace: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/lib/api", () => ({
  fetchAuthMe,
  fetchAuthPolicy,
  getApiErrorMessage: (error: unknown, fallback: string) =>
    error instanceof Error ? error.message : fallback,
  postLogin,
  postLogout,
  postRefresh,
  postRegister,
  putAuthProfile,
}));

vi.mock("@/stores/accountNamespace", () => ({
  cleanupLegacyAnonymousStorage,
  switchAccountNamespace,
}));

import { useAuthStore } from "@/stores/authStore";

describe("authStore logout", () => {
  const credentialDelete = vi.fn().mockResolvedValue(true);

  beforeEach(() => {
    localStorage.clear();
    postLogout.mockReset();
    switchAccountNamespace.mockClear();
    cleanupLegacyAnonymousStorage.mockClear();
    credentialDelete.mockClear();

    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      credentialDelete,
    };

    useAuthStore.setState({
      status: "authenticated",
      account: {
        accountId: "acc-1",
        username: "alice",
        displayName: "Alice",
        studentId: "20230001",
        ptaNickname: "alice_pta",
      },
      accessToken: "access-token",
      refreshToken: "refresh-token",
      rememberPassword: false,
      autoLogin: true,
      lastUsername: "alice",
      initialized: true,
      error: null,
    });
  });

  it("deletes local credential on logout when remember password is off", async () => {
    postLogout.mockResolvedValue(undefined);

    await useAuthStore.getState().logout();

    expect(postLogout).toHaveBeenCalledWith(
      { refreshToken: "refresh-token" },
      "access-token",
    );
    expect(credentialDelete).toHaveBeenCalledWith("alice");
  });

  it("keeps local credential on logout when remember password is on", async () => {
    postLogout.mockResolvedValue(undefined);
    useAuthStore.setState({ rememberPassword: true });

    await useAuthStore.getState().logout();

    expect(postLogout).toHaveBeenCalledWith(
      { refreshToken: "refresh-token" },
      "access-token",
    );
    expect(credentialDelete).not.toHaveBeenCalled();
  });
});
