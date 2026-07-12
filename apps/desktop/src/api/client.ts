import {
  BrowserSession,
  BrowserSessionError,
  type ApiError,
} from "@ascendany/sdk";

export function resolveDesktopAPIOrigin(
  configuredOrigin: string | undefined,
  pageOrigin: string,
  pageProtocol: string,
): string {
  if (configuredOrigin !== undefined && configuredOrigin !== "") {
    return configuredOrigin;
  }
  if (pageProtocol === "http:" || pageProtocol === "https:") {
    return pageOrigin;
  }
  throw new TypeError(
    "VITE_API_BASE_URL is required for the packaged AscendAny Desktop.",
  );
}

export function createDesktopSession(): BrowserSession {
  return new BrowserSession({
    apiOrigin: resolveDesktopAPIOrigin(
      import.meta.env.VITE_API_BASE_URL,
      window.location.origin,
      window.location.protocol,
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
