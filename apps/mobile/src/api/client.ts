import { BrowserSession, BrowserSessionError } from "@ascendany/sdk";
import { Capacitor } from "@capacitor/core";

export function resolveAPIOrigin(
  configuredOrigin: string | undefined,
  pageOrigin: string,
  nativePlatform: boolean,
): string {
  if (configuredOrigin !== undefined && configuredOrigin !== "") {
    return configuredOrigin;
  }
  if (nativePlatform) {
    throw new TypeError("VITE_API_BASE_URL is required for a native Mobile build.");
  }
  return pageOrigin;
}

export function createMobileSession(): BrowserSession {
  return new BrowserSession({
    apiOrigin: resolveAPIOrigin(
      import.meta.env.VITE_API_BASE_URL,
      window.location.origin,
      Capacitor.isNativePlatform(),
    ),
    storage: window.localStorage,
  });
}

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
  if (error instanceof Error && error.message.length > 0) return error.message;
  return publicMessage(error) ?? "请求失败";
}
