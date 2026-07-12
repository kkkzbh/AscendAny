import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { Account, BrowserSession, BrowserSessionSnapshot } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";

export type SessionStatus = "booting" | "anonymous" | "authenticated";

interface SessionContextValue {
  session: BrowserSession;
  status: SessionStatus;
  account: Account | null;
  error: string | null;
  login: (username: string, password: string) => Promise<void>;
  consumeEnrollment: (token: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  clearError: () => void;
  replaceAccount: (account: Account) => void;
}

const SessionContext = createContext<SessionContextValue | null>(null);

function snapshotAccount(snapshot: BrowserSessionSnapshot): Account | null {
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
  const [account, setAccount] = useState<Account | null>(() => snapshotAccount(initialSnapshot));
  const [ready, setReady] = useState(initialSnapshot.status === "authenticated");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    const unsubscribe = session.subscribe((snapshot) => {
      if (active) setAccount(snapshotAccount(snapshot));
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
    clearError: () => setError(null),
    replaceAccount,
  }), [account, consumeEnrollment, error, login, logout, ready, replaceAccount, session]);

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (value === null) throw new Error("SessionProvider is required.");
  return value;
}
