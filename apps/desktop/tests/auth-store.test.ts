import { beforeEach, describe, expect, it, vi } from "vitest";

const {
  fetchAuthMe,
  fetchAuthPolicy,
  postBootstrapLocalPassword,
  postLogin,
  postLogout,
  postRefresh,
  postRegister,
  postSsoExchange,
  putAuthProfile,
} = vi.hoisted(() => ({
  fetchAuthMe: vi.fn(),
  fetchAuthPolicy: vi.fn(),
  postBootstrapLocalPassword: vi.fn(),
  postLogin: vi.fn(),
  postLogout: vi.fn(),
  postRefresh: vi.fn(),
  postRegister: vi.fn(),
  postSsoExchange: vi.fn(),
  putAuthProfile: vi.fn(),
}));

const { bindCurrentLocalProfile } = vi.hoisted(() => ({
  bindCurrentLocalProfile: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/lib/api", () => ({
  fetchAuthMe,
  fetchAuthPolicy,
  getApiErrorMessage: (error: unknown, fallback: string) =>
    error instanceof Error ? error.message : fallback,
  postBootstrapLocalPassword,
  postLogin,
  postLogout,
  postRefresh,
  postRegister,
  postSsoExchange,
  putAuthProfile,
}));

vi.mock("@/stores/localStateHydration", () => ({
  bindCurrentLocalProfile,
}));

import { useAuthStore } from "@/stores/authStore";

describe("authStore logout", () => {
  const credentialDelete = vi.fn().mockResolvedValue(true);

  beforeEach(() => {
    localStorage.clear();
    postLogout.mockReset();
    bindCurrentLocalProfile.mockClear();
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
        provisionSource: "local",
        localPasswordEnabled: true,
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
    bindCurrentLocalProfile.mockClear();

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
      provisionSource: "local",
      localPasswordEnabled: true,
    });

    const hasHydratedSpy = vi.spyOn(useAuthStore.persist, "hasHydrated").mockReturnValue(false);
    const rehydrateImpl = useAuthStore.persist.rehydrate.bind(useAuthStore.persist);
    const rehydrateSpy = vi
      .spyOn(useAuthStore.persist, "rehydrate")
      .mockImplementation(() => rehydrateImpl());

    await useAuthStore.getState().bootstrap();

    expect(rehydrateSpy).toHaveBeenCalledTimes(1);
    expect(fetchAuthMe).toHaveBeenCalledWith("persisted-access-token");
    expect(bindCurrentLocalProfile).toHaveBeenCalledWith({
      accountId: "acc-1",
      username: "alice",
      displayName: "Alice",
    });
    expect(useAuthStore.getState().status).toBe("authenticated");
    expect(useAuthStore.getState().lastUsername).toBe("alice");

    hasHydratedSpy.mockRestore();
    rehydrateSpy.mockRestore();
  });

  it("falls back to localStorage when auth session IPC read fails", async () => {
    localStorage.setItem(
      "ascendany_auth_session",
      JSON.stringify({
        state: {
          account: null,
          accessToken: "local-access-token",
          refreshToken: "local-refresh-token",
          autoLogin: true,
          rememberPassword: false,
          lastUsername: "alice",
        },
        version: 0,
      }),
    );

    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      authSessionGet: vi.fn().mockRejectedValue(new Error("ipc_unavailable")),
      authSessionSet: vi.fn().mockResolvedValue(true),
      authSessionDelete: vi.fn().mockResolvedValue(true),
    };

    fetchAuthPolicy.mockResolvedValue({});
    fetchAuthMe.mockResolvedValue({
      accountId: "acc-1",
      username: "alice",
      displayName: "Alice",
      studentId: "20230001",
      ptaNickname: "alice_pta",
      provisionSource: "local",
      localPasswordEnabled: true,
    });

    await useAuthStore.persist.rehydrate();
    await useAuthStore.getState().bootstrap();

    expect(fetchAuthMe).toHaveBeenCalledWith("local-access-token");
    expect(useAuthStore.getState().status).toBe("authenticated");
  });

  it("keeps local profile data untouched when auth bootstrap fails", async () => {
    useAuthStore.setState({
      accessToken: "expired-access-token",
      refreshToken: "expired-refresh-token",
      autoLogin: true,
      initialized: false,
      status: "booting",
    });
    fetchAuthPolicy.mockResolvedValue({});
    fetchAuthMe.mockRejectedValue(new Error("expired"));
    postRefresh.mockRejectedValue(new Error("expired"));

    await useAuthStore.getState().bootstrap();

    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(bindCurrentLocalProfile).not.toHaveBeenCalled();
  });
});
