import { create } from "zustand";
import { createJSONStorage, persist, type StateStorage } from "zustand/middleware";
import {
  fetchAuthMe,
  fetchAuthPolicy,
  getApiErrorMessage,
  postLogin,
  postLogout,
  postRefresh,
  postRegister,
  putAuthProfile,
} from "@/lib/api";
import type { AuthAccount, AuthPolicy } from "@/types/auth";
import {
  cleanupLegacyAnonymousStorage,
  switchAccountNamespace,
} from "@/stores/accountNamespace";

interface LoginInput {
  username: string;
  password: string;
  autoLogin: boolean;
  rememberPassword: boolean;
  deviceId?: string;
}

interface RegisterInput {
  username: string;
  password: string;
  studentId: string;
  ptaNickname: string;
  phone?: string;
  email?: string;
  autoLogin: boolean;
  rememberPassword: boolean;
  deviceId?: string;
}

interface AuthState {
  status: "booting" | "anonymous" | "authenticated";
  account: AuthAccount | null;
  accessToken: string | null;
  refreshToken: string | null;
  policy: AuthPolicy | null;
  autoLogin: boolean;
  rememberPassword: boolean;
  lastUsername: string;
  initialized: boolean;
  error: string | null;
  profileSaving: boolean;
  bootstrap: () => Promise<void>;
  refreshPolicy: () => Promise<void>;
  login: (input: LoginInput) => Promise<void>;
  register: (input: RegisterInput) => Promise<void>;
  logout: () => Promise<void>;
  updateProfile: (payload: {
    displayName?: string | null;
    studentId?: string | null;
    ptaNickname?: string | null;
  }) => Promise<void>;
  clearError: () => void;
}

type AuthPersistedState = Pick<
  AuthState,
  "account" | "accessToken" | "refreshToken" | "autoLogin" | "rememberPassword" | "lastUsername"
>;

function normalizeOptional(value?: string | null): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

const memoryAuthSessionStorage = new Map<string, string>();

const memoryStateStorage: StateStorage = {
  getItem: (name) => memoryAuthSessionStorage.get(name) ?? null,
  setItem: (name, value) => {
    memoryAuthSessionStorage.set(name, value);
  },
  removeItem: (name) => {
    memoryAuthSessionStorage.delete(name);
  },
};

function resolveAuthSessionStateStorage(): StateStorage {
  if (typeof window === "undefined") {
    return memoryStateStorage;
  }

  const api = window.electronAPI;
  if (api?.authSessionGet && api?.authSessionSet && api?.authSessionDelete) {
    return {
      getItem: (name) => api.authSessionGet!(name),
      setItem: (name, value) => api.authSessionSet!(name, value),
      removeItem: (name) => api.authSessionDelete!(name),
    };
  }

  try {
    return window.localStorage;
  } catch {
    return memoryStateStorage;
  }
}

async function applySession(params: {
  account: AuthAccount;
  accessToken: string;
  refreshToken: string;
  autoLogin: boolean;
  rememberPassword: boolean;
  lastUsername: string;
}): Promise<void> {
  await switchAccountNamespace(params.account.accountId);
  useAuthStore.setState({
    status: "authenticated",
    account: params.account,
    accessToken: params.accessToken,
    refreshToken: params.refreshToken,
    autoLogin: params.autoLogin,
    rememberPassword: params.rememberPassword,
    lastUsername: params.lastUsername,
    initialized: true,
    error: null,
  });
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      status: "booting",
      account: null,
      accessToken: null,
      refreshToken: null,
      policy: null,
      autoLogin: true,
      rememberPassword: false,
      lastUsername: "",
      initialized: false,
      error: null,
      profileSaving: false,

      bootstrap: async () => {
        if (!useAuthStore.persist.hasHydrated()) {
          await useAuthStore.persist.rehydrate();
        }

        if (get().initialized) {
          return;
        }

        cleanupLegacyAnonymousStorage();
        await get().refreshPolicy();

        const accessToken = get().accessToken;
        const refreshToken = get().refreshToken;
        const autoLogin = get().autoLogin;

        if (accessToken) {
          try {
            const account = await fetchAuthMe(accessToken);
            await switchAccountNamespace(account.accountId);
            set({
              status: "authenticated",
              account,
              initialized: true,
              error: null,
            });
            return;
          } catch {
            // Try refresh fallback below.
          }
        }

        if (autoLogin && refreshToken) {
          try {
            const refreshed = await postRefresh({ refreshToken });
            await applySession({
              account: refreshed.account,
              accessToken: refreshed.accessToken,
              refreshToken: refreshed.refreshToken,
              autoLogin,
              rememberPassword: get().rememberPassword,
              lastUsername: refreshed.account.username,
            });
            return;
          } catch {
            // Continue to anonymous fallback.
          }
        }

        await switchAccountNamespace(null);
        set({
          status: "anonymous",
          account: null,
          accessToken: null,
          refreshToken: null,
          initialized: true,
          error: null,
        });
      },

      refreshPolicy: async () => {
        try {
          const policy = await fetchAuthPolicy();
          set({ policy });
        } catch {
          // Keep policy null on boot failures.
        }
      },

      login: async (input) => {
        const username = input.username.trim();
        set({ error: null });

        try {
          const tokens = await postLogin({
            username,
            password: input.password,
            deviceId: normalizeOptional(input.deviceId),
          });
          await applySession({
            account: tokens.account,
            accessToken: tokens.accessToken,
            refreshToken: tokens.refreshToken,
            autoLogin: input.autoLogin,
            rememberPassword: input.rememberPassword,
            lastUsername: username,
          });
        } catch (error) {
          set({
            status: "anonymous",
            account: null,
            accessToken: null,
            refreshToken: null,
            autoLogin: input.autoLogin,
            rememberPassword: input.rememberPassword,
            lastUsername: username,
            error: getApiErrorMessage(error, "登录失败，请稍后重试。"),
            initialized: true,
          });
          throw error;
        }
      },

      register: async (input) => {
        const username = input.username.trim();
        set({ error: null });

        try {
          const tokens = await postRegister({
            username,
            password: input.password,
            studentId: input.studentId.trim(),
            ptaNickname: input.ptaNickname.trim(),
            phone: normalizeOptional(input.phone),
            email: normalizeOptional(input.email),
            deviceId: normalizeOptional(input.deviceId),
          });
          await applySession({
            account: tokens.account,
            accessToken: tokens.accessToken,
            refreshToken: tokens.refreshToken,
            autoLogin: input.autoLogin,
            rememberPassword: input.rememberPassword,
            lastUsername: username,
          });
        } catch (error) {
          set({
            status: "anonymous",
            account: null,
            accessToken: null,
            refreshToken: null,
            autoLogin: input.autoLogin,
            rememberPassword: input.rememberPassword,
            lastUsername: username,
            error: getApiErrorMessage(error, "注册失败，请稍后重试。"),
            initialized: true,
          });
          throw error;
        }
      },

      logout: async () => {
        const accessToken = get().accessToken;
        const refreshToken = get().refreshToken;
        const account = get().account;
        const rememberPassword = get().rememberPassword;
        if (accessToken) {
          try {
            await postLogout({ refreshToken: refreshToken ?? undefined }, accessToken);
          } catch {
            // Ignore logout network failures on client cleanup path.
          }
        }
        if (!rememberPassword && account?.username) {
          const api = window.electronAPI;
          if (api?.credentialDelete) {
            try {
              await api.credentialDelete(account.username);
            } catch {
              // Ignore local credential cleanup failures.
            }
          }
        }

        await switchAccountNamespace(null);
        set({
          status: "anonymous",
          account: null,
          accessToken: null,
          refreshToken: null,
          error: null,
          initialized: true,
        });
      },

      updateProfile: async (payload) => {
        const accessToken = get().accessToken;
        const account = get().account;
        if (!accessToken || !account) {
          throw new Error("not_authenticated");
        }

        set({ profileSaving: true, error: null });
        try {
          const profile = await putAuthProfile(
            {
              displayName: payload.displayName,
              studentId: payload.studentId,
              ptaNickname: payload.ptaNickname,
            },
            accessToken,
          );
          set({
            account: {
              ...account,
              displayName: profile.displayName ?? account.displayName,
              studentId: profile.studentId,
              ptaNickname: profile.ptaNickname,
            },
            profileSaving: false,
            error: null,
          });
        } catch (error) {
          set({
            profileSaving: false,
            error: getApiErrorMessage(error, "保存资料失败，请稍后重试。"),
          });
          throw error;
        }
      },

      clearError: () => set({ error: null }),
    }),
    {
      name: "ascendany_auth_session",
      storage: createJSONStorage<AuthPersistedState>(() => resolveAuthSessionStateStorage()),
      partialize: (state) => ({
        account: state.account,
        accessToken: state.autoLogin ? state.accessToken : null,
        refreshToken: state.autoLogin ? state.refreshToken : null,
        autoLogin: state.autoLogin,
        rememberPassword: state.rememberPassword,
        lastUsername: state.lastUsername,
      }),
    },
  ),
);
