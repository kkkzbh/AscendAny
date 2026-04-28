/**
 * Base API client with JWT auth support.
 */

const API_BASE =
  (import.meta.env.VITE_API_BASE_URL ?? "").replace(/\/+$/, "") || "";

const TOKEN_KEY = "ascendany-import-access-token";
const REFRESH_KEY = "ascendany-import-refresh-token";

const TOKEN_HANDOFF_ACCESS_PARAM = "aa_access_token";
const TOKEN_HANDOFF_REFRESH_PARAM = "aa_refresh_token";

export function getStoredToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function getStoredRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_KEY);
}

export function storeTokens(access: string, refresh: string): void {
  localStorage.setItem(TOKEN_KEY, access);
  localStorage.setItem(REFRESH_KEY, refresh);
}

export function clearTokens(): void {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(REFRESH_KEY);
}

export function consumeTokenHandoff(): string | null {
  if (
    typeof window === "undefined" ||
    (!import.meta.env.DEV && import.meta.env.VITE_TOKEN_HANDOFF !== "true")
  ) {
    return null;
  }

  const url = new URL(window.location.href);
  const access = url.searchParams.get(TOKEN_HANDOFF_ACCESS_PARAM);
  const refresh = url.searchParams.get(TOKEN_HANDOFF_REFRESH_PARAM);
  if (!access || !refresh) return null;

  storeTokens(access, refresh);
  url.searchParams.delete(TOKEN_HANDOFF_ACCESS_PARAM);
  url.searchParams.delete(TOKEN_HANDOFF_REFRESH_PARAM);
  window.history.replaceState(
    window.history.state,
    document.title,
    `${url.pathname}${url.search}${url.hash}`,
  );
  return access;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

let _refreshPromise: Promise<string | null> | null = null;

async function tryRefreshToken(): Promise<string | null> {
  const refreshToken = getStoredRefreshToken();
  if (!refreshToken) return null;

  if (_refreshPromise) return _refreshPromise;

  _refreshPromise = (async () => {
    try {
      const res = await fetch(`${API_BASE}/api/v1/auth/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refreshToken }),
      });
      if (!res.ok) {
        clearTokens();
        return null;
      }
      const data = await res.json();
      storeTokens(data.accessToken, data.refreshToken);
      return data.accessToken as string;
    } catch {
      clearTokens();
      return null;
    } finally {
      _refreshPromise = null;
    }
  })();

  return _refreshPromise;
}

export async function apiFetch<T = unknown>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const token = getStoredToken();
  const headers = new Headers(options.headers);
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  if (!headers.has("Content-Type") && options.body && typeof options.body === "string") {
    headers.set("Content-Type", "application/json");
  }

  let res = await fetch(`${API_BASE}${path}`, { ...options, headers });

  // Auto-refresh on 401
  if (res.status === 401 && token) {
    const newToken = await tryRefreshToken();
    if (newToken) {
      headers.set("Authorization", `Bearer ${newToken}`);
      res = await fetch(`${API_BASE}${path}`, { ...options, headers });
    }
  }

  if (!res.ok) {
    let code = "UNKNOWN";
    let message = `HTTP ${res.status}`;
    try {
      const body = await res.json();
      code = body?.error?.code ?? code;
      message = body?.error?.message ?? message;
    } catch {
      // ignore parse errors
    }
    throw new ApiError(res.status, code, message);
  }

  return res.json() as Promise<T>;
}

export function apiUrl(path: string): string {
  return `${API_BASE}${path}`;
}
