import { useCallback, useEffect, useState } from "react";
import type { Account, BrowserSessionSnapshot } from "@ascendany/sdk";
import { apiFailureMessage, browserSession } from "../api/v2Client";

export type AuthStatus = "initializing" | "anonymous" | "authenticated";

interface AuthState {
  status: AuthStatus;
  account: Account | null;
  error: string | null;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

function accountFromSnapshot(snapshot: BrowserSessionSnapshot): Account | null {
  return snapshot.status === "authenticated" ? snapshot.account : null;
}

export function useAuth(): AuthState {
  const [snapshot, setSnapshot] = useState<BrowserSessionSnapshot>(() => browserSession.snapshot());
  const [ready, setReady] = useState(snapshot.status === "authenticated");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    const unsubscribe = browserSession.subscribe((nextSnapshot) => {
      if (active) setSnapshot(nextSnapshot);
    });

    if (browserSession.snapshot().status === "authenticated") {
      setReady(true);
    } else {
      void browserSession.bootstrap()
        .catch((bootstrapError: unknown) => {
          if (active) setError(apiFailureMessage(bootstrapError));
        })
        .finally(() => {
          if (active) setReady(true);
        });
    }

    return () => {
      active = false;
      unsubscribe();
    };
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    setError(null);
    try {
      await browserSession.login({ username, password });
    } catch (loginError) {
      const message = apiFailureMessage(loginError);
      setError(message);
      throw new Error(message);
    }
  }, []);

  const logout = useCallback(async () => {
    setError(null);
    try {
      await browserSession.logout();
    } catch (logoutError) {
      setError(apiFailureMessage(logoutError));
    }
  }, []);

  return {
    status: ready ? snapshot.status : "initializing",
    account: accountFromSnapshot(snapshot),
    error,
    login,
    logout,
  };
}
