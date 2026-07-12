import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type {
  Account,
  BrowserSession,
  BrowserSessionSnapshot,
} from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import {
  SessionContext,
  type SessionContextValue,
} from "./context";

function accountFromSnapshot(snapshot: BrowserSessionSnapshot): Account | null {
  return snapshot.status === "authenticated" ? snapshot.account : null;
}

export function SessionProvider({
  session,
  children,
}: {
  session: BrowserSession;
  children: ReactNode;
}) {
  const initialSnapshot = session.snapshot();
  const [account, setAccount] = useState<Account | null>(
    accountFromSnapshot(initialSnapshot),
  );
  const [ready, setReady] = useState(initialSnapshot.status === "authenticated");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    const unsubscribe = session.subscribe((snapshot) => {
      if (active) setAccount(accountFromSnapshot(snapshot));
    });

    if (session.snapshot().status === "authenticated") {
      setReady(true);
    } else {
      void session.bootstrap()
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
  }, [session]);

  const login = useCallback(async (username: string, password: string) => {
    setError(null);
    try {
      await session.login({ username, password });
    } catch (loginError) {
      const message = apiFailureMessage(loginError);
      setError(message);
      throw new Error(message);
    }
  }, [session]);

  const consumeEnrollment = useCallback(async (token: string, password: string) => {
    setError(null);
    try {
      await session.consumeEnrollment({ token, password });
    } catch (claimError) {
      const message = apiFailureMessage(claimError);
      setError(message);
      throw new Error(message);
    }
  }, [session]);

  const logout = useCallback(async () => {
    setError(null);
    try {
      await session.logout();
    } catch (logoutError) {
      setError(apiFailureMessage(logoutError));
    }
  }, [session]);

  const clearError = useCallback(() => {
    setError(null);
  }, []);

  const replaceAccount = useCallback((nextAccount: Account) => {
    setAccount(nextAccount);
  }, []);

  const value = useMemo<SessionContextValue>(() => ({
    session,
    status: ready ? (account === null ? "anonymous" : "authenticated") : "booting",
    account,
    error,
    login,
    consumeEnrollment,
    logout,
    clearError,
    replaceAccount,
  }), [
    account,
    clearError,
    consumeEnrollment,
    error,
    login,
    logout,
    ready,
    replaceAccount,
    session,
  ]);

  return (
    <SessionContext.Provider value={value}>
      {children}
    </SessionContext.Provider>
  );
}
