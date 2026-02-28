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

describe("authStore bootstrap", () => {
  beforeEach(() => {
    localStorage.clear();
    fetchAuthMe.mockReset();
    fetchAuthPolicy.mockReset();
    postRefresh.mockReset();
    switchAccountNamespace.mockClear();
    cleanupLegacyAnonymousStorage.mockClear();

    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
    };

    useAuthStore.setState({
      status: "booting",
      account: null,
      accessToken: null,
      refreshToken: null,
      rememberPassword: false,
      autoLogin: true,
      lastUsername: "",
      initialized: false,
      error: null,
    });
  });

  it("rehydrates persisted session before bootstrap auth check", async () => {
    localStorage.setItem(
      "ascendany_auth_session",
      JSON.stringify({
        state: {
          account: null,
          accessToken: "persisted-access-token",
          refreshToken: "persisted-refresh-token",
          autoLogin: true,
          rememberPassword: false,
          lastUsername: "alice",
        },
        version: 0,
      }),
    );

    fetchAuthPolicy.mockResolvedValue({});
    fetchAuthMe.mockResolvedValue({
      accountId: "acc-1",
      username: "alice",
      displayName: "Alice",
      studentId: "20230001",
      ptaNickname: "alice_pta",
    });

    const hasHydratedSpy = vi.spyOn(useAuthStore.persist, "hasHydrated").mockReturnValue(false);
    const rehydrateImpl = useAuthStore.persist.rehydrate.bind(useAuthStore.persist);
    const rehydrateSpy = vi
      .spyOn(useAuthStore.persist, "rehydrate")
      .mockImplementation(() => rehydrateImpl());

    await useAuthStore.getState().bootstrap();

    expect(rehydrateSpy).toHaveBeenCalledTimes(1);
    expect(fetchAuthMe).toHaveBeenCalledWith("persisted-access-token");
    expect(useAuthStore.getState().status).toBe("authenticated");
    expect(useAuthStore.getState().lastUsername).toBe("alice");

    hasHydratedSpy.mockRestore();
    rehydrateSpy.mockRestore();
  });
});
