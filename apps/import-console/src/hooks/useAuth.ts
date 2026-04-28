import { useCallback, useEffect, useState } from "react";
import {
  apiFetch,
  clearTokens,
  consumeTokenHandoff,
  getStoredToken,
  storeTokens,
} from "../api/client";

export interface AccountInfo {
  accountId: string;
  username: string;
  isAdmin: boolean;
  studentId?: string | null;
  ptaNickname?: string | null;
}

interface AuthTokensResponse {
  accessToken: string;
  refreshToken: string;
  account: AccountInfo;
}

interface AuthState {
  token: string | null;
  account: AccountInfo | null;
  loading: boolean;
  error: string | null;
  login: (username: string, password: string) => Promise<void>;
  logout: () => void;
}

export function useAuth(): AuthState {
  const [token, setToken] = useState<string | null>(() => consumeTokenHandoff() ?? getStoredToken());
  const [account, setAccount] = useState<AccountInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // On mount, if we have a token, fetch current user info
  useEffect(() => {
    if (!token) {
      setAccount(null);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const me = await apiFetch<{ account: AccountInfo }>("/api/v1/auth/me");
        if (!cancelled) setAccount(me.account);
      } catch {
        if (!cancelled) {
          clearTokens();
          setToken(null);
          setAccount(null);
        }
      }
    })();
    return () => { cancelled = true; };
  }, [token]);

  const login = useCallback(async (username: string, password: string) => {
    setLoading(true);
    setError(null);
    try {
      const data = await apiFetch<AuthTokensResponse>("/api/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ username, password }),
      });
      storeTokens(data.accessToken, data.refreshToken);
      setToken(data.accessToken);
      setAccount(data.account);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "登录失败";
      setError(msg);
      throw err;
    } finally {
      setLoading(false);
    }
  }, []);

  const logout = useCallback(() => {
    clearTokens();
    setToken(null);
    setAccount(null);
  }, []);

  return { token, account, loading, error, login, logout };
}
