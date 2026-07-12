import { BrowserSession, BrowserSessionError, type ApiError } from "@ascendany/sdk";

export function resolveAPIOrigin(
  configuredOrigin: string | undefined,
  pageOrigin: string,
): string {
  return configuredOrigin === undefined || configuredOrigin === ""
    ? pageOrigin
    : configuredOrigin;
}

export function createWebSession(): BrowserSession {
  return new BrowserSession({
    apiOrigin: resolveAPIOrigin(
      import.meta.env.VITE_API_BASE_URL,
      window.location.origin,
    ),
    storage: window.localStorage,
  });
}

function isAPIError(value: unknown): value is ApiError {
  if (typeof value !== "object" || value === null) return false;
  const candidate = value as Partial<ApiError>;
  return (
    typeof candidate.code === "string"
    && typeof candidate.message === "string"
    && typeof candidate.requestId === "string"
  );
}

export function apiFailureMessage(error: unknown): string {
  if (error instanceof BrowserSessionError) {
    return isAPIError(error.apiError) ? error.apiError.message : error.message;
  }
  if (isAPIError(error)) return error.message;
  if (error instanceof Error && error.message.length > 0) return error.message;
  return "请求失败";
}
