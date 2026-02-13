import type { Role } from "@/types/chat";
import type { StudentDashboardData } from "@/types/metrics";
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
    method?: "GET" | "POST";
    query?: Record<string, QueryValue>;
    body?: unknown;
  },
): Promise<T> {
  const response = await fetch(buildUrl(path, options?.query), {
    method: options?.method ?? "GET",
    headers: {
      "Content-Type": "application/json",
    },
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
}): Promise<StudentDashboardData> {
  return requestJson<StudentDashboardData>("/api/v1/students/dashboard", {
    query: {
      studentId: params.studentId,
      ptaNickname: params.ptaNickname,
    },
  });
}

export async function postChatReply(
  payload: ChatReplyRequestPayload,
): Promise<ChatReplyResponsePayload> {
  return requestJson<ChatReplyResponsePayload>("/api/v1/chat/reply", {
    method: "POST",
    body: payload,
  });
}

export async function fetchModelProviders(): Promise<ModelProvidersResponsePayload> {
  return requestJson<ModelProvidersResponsePayload>("/api/v1/model/providers");
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
