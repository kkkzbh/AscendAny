import { createContext, useContext } from "react";
import type { Account, BrowserSession } from "@ascendany/sdk";

export type SessionStatus = "booting" | "anonymous" | "authenticated";

export interface SessionContextValue {
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

export const SessionContext = createContext<SessionContextValue | null>(null);

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (value === null) throw new Error("SessionProvider is required.");
  return value;
}
