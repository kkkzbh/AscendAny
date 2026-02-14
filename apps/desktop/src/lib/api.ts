import type { Role } from "@/types/chat";
import type { StudentDashboardData } from "@/types/metrics";
import type {
  AuthAccount,
  AuthPolicy,
  AuthProfile,
  AuthTokens,
} from "@/types/auth";
import type { ProviderType } from "@/types/settings";

const DEFAULT_API_BASE_URL = "http://127.0.0.1:8000";
const API_BASE_URL =
  import.meta.env.VITE_API_BASE_URL?.trim() || DEFAULT_API_BASE_URL;

type QueryValue = string | number | boolean | null | undefined;

interface ApiErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
  };
  detail?: string;
}

export interface ChatMessagePayload {
  role: Role;
  content: string;
}

export interface ClientProviderConfigPayload {
  baseUrl: string;
  model: string;
  apiKey: string;
  mode: "openai_compatible" | "anthropic";
}

export interface ChatReplyRequestPayload {
  studentId?: string;
  ptaNickname?: string;
  messages: ChatMessagePayload[];
  summary: string;
  providerType: ProviderType;
  providerConfig?: ClientProviderConfigPayload;
  roleId?: string;
}

export interface AutoAnalysisRequestPayload {
  studentId?: string;
  ptaNickname?: string;
  providerType: ProviderType;
  providerConfig?: ClientProviderConfigPayload;
  roleId?: string;
}

export interface AutoAnalysisResponsePayload {
  reply: string;
  provider: ProviderType;
}

export interface LatestExamImportedAtPayload {
  latestExamImportedAt: string | null;
}

export interface ChatReplyResponsePayload {
  reply: string;
  summary: string;
  provider: ProviderType;
}

export interface ModelProviderOptionPayload {
  type: string;
  label: string;
  usesServerConfig: boolean;
  enabled: boolean;
}

export interface ModelProvidersResponsePayload {
  defaultProvider: string;
  serverDefaultTarget: string;
  serverDefaultTargetLabel?: string;
  serverDefaultModel?: string;
  providers: ModelProviderOptionPayload[];
}

export class ApiError extends Error {
  status: number;
  code?: string;

  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

function buildUrl(path: string, query?: Record<string, QueryValue>): string {
  const base = API_BASE_URL.replace(/\/+$/, "");
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  const url = new URL(`${base}${normalizedPath}`);

  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value === null || value === undefined) continue;
      const normalizedValue = String(value).trim();
      if (!normalizedValue) continue;
      url.searchParams.set(key, normalizedValue);
    }
  }

  return url.toString();
}

function extractError(payload: unknown): { code?: string; message?: string } {
  if (!payload || typeof payload !== "object") {
    return {};
  }
  const data = payload as ApiErrorEnvelope;
  if (data.error) {
    return {
      code: data.error.code,
      message: data.error.message,
    };
  }
  if (typeof data.detail === "string") {
    return { message: data.detail };
  }
  return {};
}

async function requestJson<T>(
  path: string,
  options?: {
    method?: "GET" | "POST" | "PUT";
    query?: Record<string, QueryValue>;
    body?: unknown;
    authToken?: string;
  },
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (options?.authToken?.trim()) {
    headers.Authorization = `Bearer ${options.authToken.trim()}`;
  }

  const response = await fetch(buildUrl(path, options?.query), {
    method: options?.method ?? "GET",
    headers,
    body: options?.body !== undefined ? JSON.stringify(options.body) : undefined,
  });

  const contentType = response.headers.get("content-type") ?? "";
  const isJson = contentType.includes("application/json");

  if (!response.ok) {
    if (isJson) {
      const payload = (await response.json()) as unknown;
      const { code, message } = extractError(payload);
      throw new ApiError(
        message ?? `请求失败（${response.status}）`,
        response.status,
        code,
      );
    }

    const text = (await response.text()).trim();
    throw new ApiError(
      text || `请求失败（${response.status}）`,
      response.status,
    );
  }

  if (!isJson) {
    throw new ApiError("后端返回了非 JSON 响应", response.status);
  }

  return (await response.json()) as T;
}

export async function fetchStudentDashboard(params: {
  studentId?: string;
  ptaNickname?: string;
  authToken?: string;
}): Promise<StudentDashboardData> {
  return requestJson<StudentDashboardData>("/api/v1/students/dashboard", {
    query: {
      studentId: params.studentId,
      ptaNickname: params.ptaNickname,
    },
    authToken: params.authToken,
  });
}

export async function postChatReply(
  payload: ChatReplyRequestPayload,
  authToken?: string,
): Promise<ChatReplyResponsePayload> {
  return requestJson<ChatReplyResponsePayload>("/api/v1/chat/reply", {
    method: "POST",
    body: payload,
    authToken,
  });
}

export async function fetchModelProviders(): Promise<ModelProvidersResponsePayload> {
  return requestJson<ModelProvidersResponsePayload>("/api/v1/model/providers");
}

export type SignupPolicy =
  | "username_password_only"
  | "require_phone_or_email"
  | "require_phone_and_email";

export interface AuthPolicyPayload {
  signupPolicy: SignupPolicy;
  requirePhone: boolean;
  requireEmail: boolean;
}

export interface RegisterPayload {
  username: string;
  password: string;
  studentId: string;
  ptaNickname: string;
  phone?: string;
  email?: string;
  deviceId?: string;
}

export interface LoginPayload {
  username: string;
  password: string;
  deviceId?: string;
}

export interface RefreshPayload {
  refreshToken: string;
  deviceId?: string;
}

export interface LogoutPayload {
  refreshToken?: string;
}

interface AuthAccountPayload {
  accountId: string;
  username: string;
  studentId?: string | null;
  ptaNickname?: string | null;
}

interface AuthTokensPayload {
  accessToken: string;
  accessTokenExpiresAt: string;
  refreshToken: string;
  refreshTokenExpiresAt: string;
  account: AuthAccountPayload;
}

interface AuthMePayload {
  account: AuthAccountPayload;
}

interface AuthProfilePayload {
  studentId?: string | null;
  ptaNickname?: string | null;
}

function normalizeAccount(payload: AuthAccountPayload): AuthAccount {
  return {
    accountId: payload.accountId,
    username: payload.username,
    studentId: payload.studentId ?? null,
    ptaNickname: payload.ptaNickname ?? null,
  };
}

function normalizeTokens(payload: AuthTokensPayload): AuthTokens {
  return {
    accessToken: payload.accessToken,
    accessTokenExpiresAt: payload.accessTokenExpiresAt,
    refreshToken: payload.refreshToken,
    refreshTokenExpiresAt: payload.refreshTokenExpiresAt,
    account: normalizeAccount(payload.account),
  };
}

function normalizeProfile(payload: AuthProfilePayload): AuthProfile {
  return {
    studentId: payload.studentId ?? null,
    ptaNickname: payload.ptaNickname ?? null,
  };
}

export async function fetchAuthPolicy(): Promise<AuthPolicy> {
  const payload = await requestJson<AuthPolicyPayload>("/api/v1/auth/policy");
  return {
    signupPolicy: payload.signupPolicy,
    requirePhone: payload.requirePhone,
    requireEmail: payload.requireEmail,
  };
}

export async function postRegister(payload: RegisterPayload): Promise<AuthTokens> {
  const response = await requestJson<AuthTokensPayload>("/api/v1/auth/register", {
    method: "POST",
    body: payload,
  });
  return normalizeTokens(response);
}

export async function postLogin(payload: LoginPayload): Promise<AuthTokens> {
  const response = await requestJson<AuthTokensPayload>("/api/v1/auth/login", {
    method: "POST",
    body: payload,
  });
  return normalizeTokens(response);
}

export async function postRefresh(payload: RefreshPayload): Promise<AuthTokens> {
  const response = await requestJson<AuthTokensPayload>("/api/v1/auth/refresh", {
    method: "POST",
    body: payload,
  });
  return normalizeTokens(response);
}

export async function postLogout(
  payload: LogoutPayload,
  authToken: string,
): Promise<void> {
  await requestJson<{ ok: boolean }>("/api/v1/auth/logout", {
    method: "POST",
    body: payload,
    authToken,
  });
}

export async function fetchAuthMe(authToken: string): Promise<AuthAccount> {
  const response = await requestJson<AuthMePayload>("/api/v1/auth/me", {
    authToken,
  });
  return normalizeAccount(response.account);
}

export async function putAuthProfile(
  payload: AuthProfile,
  authToken: string,
): Promise<AuthProfile> {
  const response = await requestJson<AuthProfilePayload>("/api/v1/auth/profile", {
    method: "PUT",
    body: {
      studentId: payload.studentId ?? null,
      ptaNickname: payload.ptaNickname ?? null,
    },
    authToken,
  });
  return normalizeProfile(response);
}

export async function postAutoAnalysis(
  payload: AutoAnalysisRequestPayload,
  authToken?: string,
): Promise<AutoAnalysisResponsePayload> {
  return requestJson<AutoAnalysisResponsePayload>("/api/v1/chat/auto-analysis", {
    method: "POST",
    body: payload,
    authToken,
  });
}

export async function fetchLatestExamImportedAt(): Promise<LatestExamImportedAtPayload> {
  return requestJson<LatestExamImportedAtPayload>(
    "/api/v1/meta/latest_exam_imported_at",
  );
}

export function getApiErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return fallback;
}
