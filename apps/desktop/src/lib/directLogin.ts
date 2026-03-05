const TRUE_VALUES = new Set(["1", "true", "yes", "on"]);

function readParam(params: URLSearchParams, keys: string[]): string {
  for (const key of keys) {
    const value = params.get(key);
    if (value === null) {
      continue;
    }
    const trimmed = value.trim();
    if (trimmed) {
      return trimmed;
    }
  }
  return "";
}

function readBooleanParam(
  params: URLSearchParams,
  key: string,
  fallback: boolean,
): boolean {
  const value = params.get(key);
  if (value === null) {
    return fallback;
  }
  return TRUE_VALUES.has(value.trim().toLowerCase());
}

const DIRECT_LOGIN_PARAM_KEYS = [
  "username",
  "user",
  "aa_username",
  "password",
  "pass",
  "aa_password",
  "deviceId",
  "aa_device_id",
  "autoLogin",
  "rememberPassword",
] as const;

export interface DirectLoginParams {
  username: string;
  password: string;
  deviceId?: string;
  autoLogin: boolean;
  rememberPassword: boolean;
}

export function isDirectLoginEnabled(envValue: string | undefined): boolean {
  if (!envValue) {
    return false;
  }
  return TRUE_VALUES.has(envValue.trim().toLowerCase());
}

export function extractDirectLoginParamsFromUrl(
  url: URL,
): DirectLoginParams | null {
  const username = readParam(url.searchParams, [
    "username",
    "user",
    "aa_username",
  ]);
  const password = readParam(url.searchParams, [
    "password",
    "pass",
    "aa_password",
  ]);
  if (!username || !password) {
    return null;
  }

  const deviceId = readParam(url.searchParams, ["deviceId", "aa_device_id"]);
  return {
    username,
    password,
    autoLogin: readBooleanParam(url.searchParams, "autoLogin", true),
    rememberPassword: readBooleanParam(
      url.searchParams,
      "rememberPassword",
      false,
    ),
    deviceId: deviceId || undefined,
  };
}

export function scrubDirectLoginParams(url: URL): string {
  const next = new URL(url.toString());
  for (const key of DIRECT_LOGIN_PARAM_KEYS) {
    next.searchParams.delete(key);
  }
  const search = next.searchParams.toString();
  return `${next.pathname}${search ? `?${search}` : ""}${next.hash}`;
}
