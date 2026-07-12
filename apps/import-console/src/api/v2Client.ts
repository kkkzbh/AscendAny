import { BrowserSession, BrowserSessionError } from "@ascendany/sdk";

const apiOrigin = import.meta.env.VITE_API_BASE_URL || window.location.origin;

export const browserSession = new BrowserSession({
  apiOrigin,
  storage: window.localStorage,
});

export const v2Client = browserSession.client;

function publicMessage(value: unknown): string | null {
  if (typeof value !== "object" || value === null) return null;
  const candidate = value as { message?: unknown; error?: unknown };
  if (typeof candidate.message === "string" && candidate.message.length > 0) {
    return candidate.message;
  }
  return publicMessage(candidate.error);
}

export function apiFailureMessage(error: unknown): string {
  if (error instanceof BrowserSessionError) {
    return publicMessage(error.apiError) ?? error.message;
  }
  if (error instanceof Error && error.message) return error.message;
  return publicMessage(error) ?? "请求失败";
}
