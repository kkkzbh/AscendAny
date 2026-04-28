import type { Role } from "@/types/chat";
import type { LeaderboardEntry } from "@/types/leaderboard";
import {
  createEmptyMilestoneStreak,
  createEmptyPeerComparison,
  createEmptyPostExamSupport,
  createEmptyProgressExplanation,
  type MetricDeltaInfo,
  type MetricMissingValues,
  type MilestoneStreak,
  type PeerComparison,
  type PostExamSupport,
  type ProgressExplanation,
  type RatingInfo,
  type StudentDashboardData,
  type StudentIdentity,
  type StudentMetrics,
} from "@/types/metrics";
import type {
  AuthAccount,
  AuthPolicy,
  AuthProfile,
  AuthTokens,
} from "@/types/auth";
import type { ProviderType } from "@/types/settings";
import type {
  AchievementIdentity,
  AchievementItem,
  AchievementSummary,
  StudentAchievementsData,
} from "@/types/achievements";

const DEFAULT_API_BASE_URL = __ASCENDANY_WEB_BUILD__
  ? ""
  : "http://127.0.0.1:8000";

type RuntimeLocation = {
  origin?: string;
  protocol?: string;
};

type RuntimeWindow = {
  location?: RuntimeLocation;
  electronAPI?: Window["electronAPI"];
};

export function resolveApiBaseUrl(
  runtimeWindow: RuntimeWindow | undefined =
    typeof window === "undefined" ? undefined : window,
): string {
  const configuredBaseUrl = import.meta.env.VITE_API_BASE_URL?.trim();
  if (configuredBaseUrl) {
    return configuredBaseUrl;
  }

  const runtimeOrigin = runtimeWindow?.location?.origin?.trim() ?? "";
  const runtimeProtocol = runtimeWindow?.location?.protocol ?? "";
  const isWebOrigin = runtimeProtocol === "http:" || runtimeProtocol === "https:";

  if (!runtimeWindow?.electronAPI && isWebOrigin && runtimeOrigin) {
    return runtimeOrigin;
  }

  return DEFAULT_API_BASE_URL;
}

const API_BASE_URL = resolveApiBaseUrl();

type QueryValue = string | number | boolean | null | undefined;

interface ApiErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
  };
  detail?: string;
}

type StudentDashboardPayload = Partial<StudentDashboardData> & {
  metrics?: Partial<StudentMetrics>;
  metricMissing?: Partial<MetricMissingValues>;
  metricDelta?: Partial<MetricDeltaInfo> & {
    values?: Partial<MetricDeltaInfo["values"]>;
  };
  rating?: Partial<RatingInfo> & {
    history?: Array<Partial<RatingInfo["history"][number]>>;
  };
  identity?: Partial<StudentIdentity>;
  progressExplanation?: Partial<ProgressExplanation>;
  milestoneStreak?: Partial<MilestoneStreak>;
  peerComparison?: Partial<PeerComparison>;
  postExamSupport?: Partial<PostExamSupport>;
};

type StudentAchievementsPayload = Partial<StudentAchievementsData> & {
  identity?: Partial<AchievementIdentity>;
  summary?: Partial<AchievementSummary>;
  items?: Array<Partial<AchievementItem>>;
};

interface LeaderboardEntryPayload {
  studentId?: string;
  grade?: string;
  username?: string;
  rating?: number;
  knowledge?: number;
  accuracy?: number;
  quality?: number;
  flexibility?: number;
  proficiency?: number;
}

interface StudentLeaderboardPayload {
  items?: LeaderboardEntryPayload[];
}

export interface ChatMessagePayload {
  role: Role;
  content: string;
}

export interface ChatReplyRequestPayload {
  studentId?: string;
  ptaNickname?: string;
  messages: ChatMessagePayload[];
  summary: string;
  roleId?: string;
  roleName?: string;
  roleSystemPrompt?: string;
}

export interface AutoAnalysisRequestPayload {
  studentId?: string;
  ptaNickname?: string;
  roleId?: string;
  roleName?: string;
  roleSystemPrompt?: string;
  latestExamId?: string;
}

export interface AutoAnalysisResponsePayload {
  reply: string;
  provider: ProviderType;
  model?: string;
  requestMode?: string;
}

export interface LatestExamImportedAtPayload {
  latestExamImportedAt: string | null;
}

export interface ChatReplyResponsePayload {
  reply: string;
  summary: string;
  provider: ProviderType;
  model?: string;
  requestMode?: string;
}

export type ChatStreamEvent =
  | { type: "meta"; provider?: string; model?: string; requestMode?: string; summary?: string }
  | { type: "delta"; text: string }
  | { type: "tool_start" }
  | { type: "tool_done" }
  | { type: "done"; reply: string; summary?: string; provider?: string; model?: string; requestMode?: string }
  | { type: "error"; code?: string; message: string };

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
  const url = base
    ? new URL(`${base}${normalizedPath}`)
    : new URL(normalizedPath, window.location.origin);

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

export function normalizeDashboardData(
  payload: StudentDashboardPayload,
): StudentDashboardData {
  const progress = {
    ...createEmptyProgressExplanation(),
    ...(payload.progressExplanation ?? {}),
    keyImprovements: Array.isArray(payload.progressExplanation?.keyImprovements)
      ? payload.progressExplanation.keyImprovements
      : [],
    keySetbacks: Array.isArray(payload.progressExplanation?.keySetbacks)
      ? payload.progressExplanation.keySetbacks
      : [],
  };

  const milestone = {
    ...createEmptyMilestoneStreak(),
    ...(payload.milestoneStreak ?? {}),
    newMilestones: Array.isArray(payload.milestoneStreak?.newMilestones)
      ? payload.milestoneStreak.newMilestones
      : [],
    recentMilestones: Array.isArray(payload.milestoneStreak?.recentMilestones)
      ? payload.milestoneStreak.recentMilestones
      : [],
    nextTargets: Array.isArray(payload.milestoneStreak?.nextTargets)
      ? payload.milestoneStreak.nextTargets
      : [],
  };

  const peerFallback = createEmptyPeerComparison();
  const peer = {
    ...peerFallback,
    ...(payload.peerComparison ?? {}),
    percentileBand: {
      ...peerFallback.percentileBand,
      ...(payload.peerComparison?.percentileBand ?? {}),
      gapVsBandMedian: {
        ...peerFallback.percentileBand.gapVsBandMedian,
        ...(payload.peerComparison?.percentileBand?.gapVsBandMedian ?? {}),
      },
    },
    previousRanker: {
      ...peerFallback.previousRanker,
      ...(payload.peerComparison?.previousRanker ?? {}),
      metricGapVsPrevious: {
        ...peerFallback.previousRanker.metricGapVsPrevious,
        ...(payload.peerComparison?.previousRanker?.metricGapVsPrevious ?? {}),
      },
    },
  };

  const support = {
    ...createEmptyPostExamSupport(),
    ...(payload.postExamSupport ?? {}),
    actionPlan: Array.isArray(payload.postExamSupport?.actionPlan)
      ? payload.postExamSupport.actionPlan
      : [],
  };

  return {
    metrics: {
      knowledge: Number(payload.metrics?.knowledge ?? 0),
      accuracy: Number(payload.metrics?.accuracy ?? 0),
      quality: Number(payload.metrics?.quality ?? 0),
      flexibility: Number(payload.metrics?.flexibility ?? 0),
      proficiency: Number(payload.metrics?.proficiency ?? 0),
    },
    metricMissing: {
      knowledge: Boolean(payload.metricMissing?.knowledge ?? false),
      accuracy: Boolean(payload.metricMissing?.accuracy ?? false),
      quality: Boolean(payload.metricMissing?.quality ?? false),
      flexibility: Boolean(payload.metricMissing?.flexibility ?? false),
      proficiency: Boolean(payload.metricMissing?.proficiency ?? false),
    },
    rating: {
      current: Number(payload.rating?.current ?? 0),
      lastDelta:
        typeof payload.rating?.lastDelta === "number"
          ? payload.rating.lastDelta
          : null,
      history: Array.isArray(payload.rating?.history)
        ? payload.rating.history
            .filter((item) => Boolean(item?.examId))
            .map((item) => ({
              examId: String(item.examId),
              examName: String(item.examName ?? item.examId),
              date: String(item.date ?? ""),
              oldRating: Number(item.oldRating ?? 0),
              delta: Number(item.delta ?? 0),
              newRating: Number(item.newRating ?? 0),
            }))
        : [],
    },
    metricDelta: {
      latestExamId:
        typeof payload.metricDelta?.latestExamId === "string"
          ? payload.metricDelta.latestExamId
          : null,
      latestExamName:
        typeof payload.metricDelta?.latestExamName === "string"
          ? payload.metricDelta.latestExamName
          : null,
      latestExamDate:
        typeof payload.metricDelta?.latestExamDate === "string"
          ? payload.metricDelta.latestExamDate
          : null,
      baseline:
        payload.metricDelta?.baseline === "previous_exam"
          ? "previous_exam"
          : "zero",
      values: {
        knowledge: Number(payload.metricDelta?.values?.knowledge ?? 0),
        accuracy: Number(payload.metricDelta?.values?.accuracy ?? 0),
        quality: Number(payload.metricDelta?.values?.quality ?? 0),
        flexibility: Number(payload.metricDelta?.values?.flexibility ?? 0),
        proficiency: Number(payload.metricDelta?.values?.proficiency ?? 0),
      },
    },
    identity: {
      studentId: String(payload.identity?.studentId ?? ""),
      ptaNickname:
        typeof payload.identity?.ptaNickname === "string"
          ? payload.identity.ptaNickname
          : null,
      noSubmissionRecords: Boolean(payload.identity?.noSubmissionRecords ?? false),
    },
    progressExplanation: progress,
    milestoneStreak: milestone,
    peerComparison: peer,
    postExamSupport: support,
  };
}

export function normalizeAchievementsData(
  payload: StudentAchievementsPayload,
): StudentAchievementsData {
  const items = Array.isArray(payload.items)
    ? payload.items.map((item) => ({
        code: String(item.code ?? ""),
        title: String(item.title ?? ""),
        description: String(item.description ?? ""),
        tier: Number(item.tier ?? 0),
        progress: Number(item.progress ?? 0),
        bronzeTarget: Number(item.bronzeTarget ?? 0),
        silverTarget: Number(item.silverTarget ?? 0),
        goldTarget: Number(item.goldTarget ?? 0),
        sortOrder: Number(item.sortOrder ?? 0),
      }))
    : [];

  return {
    identity: {
      studentId: String(payload.identity?.studentId ?? ""),
      ptaNickname:
        typeof payload.identity?.ptaNickname === "string"
          ? payload.identity.ptaNickname
          : null,
      noSubmissionRecords: Boolean(payload.identity?.noSubmissionRecords ?? false),
    },
    summary: {
      total: Number(payload.summary?.total ?? items.length),
      locked: Number(payload.summary?.locked ?? 0),
      bronze: Number(payload.summary?.bronze ?? 0),
      silver: Number(payload.summary?.silver ?? 0),
      gold: Number(payload.summary?.gold ?? 0),
    },
    items,
  };
}

export async function fetchStudentDashboard(params: {
  studentId?: string;
  ptaNickname?: string;
  authToken?: string;
}): Promise<StudentDashboardData> {
  const payload = await requestJson<StudentDashboardPayload>(
    "/api/v1/students/dashboard",
    {
      query: {
        studentId: params.studentId,
        ptaNickname: params.ptaNickname,
      },
      authToken: params.authToken,
    },
  );
  return normalizeDashboardData(payload);
}

export async function fetchStudentAchievements(params: {
  studentId?: string;
  ptaNickname?: string;
  authToken?: string;
}): Promise<StudentAchievementsData> {
  const query = {
    studentId: params.studentId,
    ptaNickname: params.ptaNickname,
  };
  try {
    const payload = await requestJson<StudentAchievementsPayload>(
      "/api/v1/students/achievements",
      {
        query,
        authToken: params.authToken,
      },
    );
    return normalizeAchievementsData(payload);
  } catch (error) {
    // Some runtime environments may fail cross-origin preflight when Authorization
    // header is present; retry once without auth header when network error appears.
    if (
      params.authToken &&
      error instanceof Error &&
      /failed to fetch|networkerror/i.test(error.message)
    ) {
      const payload = await requestJson<StudentAchievementsPayload>(
        "/api/v1/students/achievements",
        { query },
      );
      return normalizeAchievementsData(payload);
    }
    throw error;
  }
}

function normalizeLeaderboardEntry(
  payload: LeaderboardEntryPayload,
): LeaderboardEntry | null {
  const studentId = String(payload.studentId ?? "").trim();
  if (!studentId || !/^\d{4,}$/.test(studentId)) {
    return null;
  }

  const username = String(payload.username ?? "").trim();
  if (!username || username.toLowerCase().startsWith("test_")) {
    return null;
  }

  const gradeInput = String(payload.grade ?? "").trim();
  const grade = gradeInput || studentId.slice(0, 4);
  if (!grade || grade.length < 4) {
    return null;
  }

  return {
    studentId,
    grade: grade.slice(0, 4),
    username,
    rating: Number(payload.rating ?? 0),
    knowledge: Number(payload.knowledge ?? 0),
    accuracy: Number(payload.accuracy ?? 0),
    quality: Number(payload.quality ?? 0),
    flexibility: Number(payload.flexibility ?? 0),
    proficiency: Number(payload.proficiency ?? 0),
  };
}

export async function fetchStudentsLeaderboard(
  authToken?: string,
): Promise<LeaderboardEntry[]> {
  const payload = await requestJson<StudentLeaderboardPayload>(
    "/api/v1/students/leaderboard",
    {
      authToken,
    },
  );
  if (!Array.isArray(payload.items)) {
    return [];
  }
  return payload.items
    .map((item) => normalizeLeaderboardEntry(item))
    .filter((item): item is LeaderboardEntry => item !== null);
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

async function streamJsonEvents(
  path: string,
  payload: unknown,
  authToken: string | undefined,
  onEvent: (event: ChatStreamEvent) => void,
): Promise<void> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    Accept: "text/event-stream",
  };
  if (authToken?.trim()) {
    headers.Authorization = `Bearer ${authToken.trim()}`;
  }
  const response = await fetch(buildUrl(path), {
    method: "POST",
    headers,
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    const contentType = response.headers.get("content-type") ?? "";
    if (contentType.includes("application/json")) {
      const body = (await response.json()) as unknown;
      const { code, message } = extractError(body);
      if (response.status === 404 && path.endsWith("/stream")) {
        throw new ApiError(
          "后端缺少流式聊天接口，请重启 API 服务后重试。",
          response.status,
          "STREAM_ENDPOINT_NOT_FOUND",
        );
      }
      throw new ApiError(message ?? `请求失败（${response.status}）`, response.status, code);
    }
    const text = (await response.text()).trim();
    if (response.status === 404 && path.endsWith("/stream")) {
      throw new ApiError(
        "后端缺少流式聊天接口，请重启 API 服务后重试。",
        response.status,
        "STREAM_ENDPOINT_NOT_FOUND",
      );
    }
    throw new ApiError(text || `请求失败（${response.status}）`, response.status);
  }
  if (!response.body) {
    throw new ApiError("后端没有返回可读取的流", response.status);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const dispatchBlock = (block: string) => {
    const lines = block.split(/\r?\n/);
    let eventType = "message";
    const dataLines: string[] = [];
    for (const line of lines) {
      if (line.startsWith("event:")) {
        eventType = line.slice(6).trim();
      } else if (line.startsWith("data:")) {
        dataLines.push(line.slice(5).trimStart());
      }
    }
    if (!dataLines.length) return;
    const raw = dataLines.join("\n");
    let parsed: Partial<ChatStreamEvent> & Record<string, unknown>;
    try {
      parsed = JSON.parse(raw) as Partial<ChatStreamEvent> & Record<string, unknown>;
    } catch {
      // Ignore malformed keepalive/event blocks.
      return;
    }
    const type = typeof parsed.type === "string" ? parsed.type : eventType;
    if (type === "delta") {
      onEvent({ type: "delta", text: String(parsed.text ?? "") });
    } else if (type === "done") {
      onEvent({ type: "done", reply: String(parsed.reply ?? ""), summary: typeof parsed.summary === "string" ? parsed.summary : undefined, provider: typeof parsed.provider === "string" ? parsed.provider : undefined, model: typeof parsed.model === "string" ? parsed.model : undefined, requestMode: typeof parsed.requestMode === "string" ? parsed.requestMode : undefined });
    } else if (type === "error") {
      throw new ApiError(
        String(parsed.message ?? "流式请求失败"),
        response.status,
        typeof parsed.code === "string" ? parsed.code : undefined,
      );
    } else if (type === "tool_start" || type === "tool_done") {
      onEvent({ type });
    } else {
      onEvent({ type: "meta", provider: typeof parsed.provider === "string" ? parsed.provider : undefined, model: typeof parsed.model === "string" ? parsed.model : undefined, requestMode: typeof parsed.requestMode === "string" ? parsed.requestMode : undefined, summary: typeof parsed.summary === "string" ? parsed.summary : undefined });
    }
  };

  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let splitIndex = buffer.search(/\r?\n\r?\n/);
    while (splitIndex >= 0) {
      const block = buffer.slice(0, splitIndex);
      const match = buffer.slice(splitIndex).match(/^\r?\n\r?\n/);
      buffer = buffer.slice(splitIndex + (match?.[0].length ?? 2));
      dispatchBlock(block);
      splitIndex = buffer.search(/\r?\n\r?\n/);
    }
  }
  buffer += decoder.decode();
  if (buffer.trim()) {
    dispatchBlock(buffer);
  }
}

export function streamChatReply(
  payload: ChatReplyRequestPayload,
  authToken: string | undefined,
  onEvent: (event: ChatStreamEvent) => void,
): Promise<void> {
  return streamJsonEvents("/api/v1/chat/reply/stream", payload, authToken, onEvent);
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
  passwordMode?: "plain" | "stored_value";
  deviceId?: string;
}

export interface SSOExchangePayload {
  token: string;
}

export interface RefreshPayload {
  refreshToken: string;
  deviceId?: string;
}

export interface LogoutPayload {
  refreshToken?: string;
}

export interface LocalPasswordBootstrapPayload {
  newPassword: string;
}

interface AuthAccountPayload {
  accountId: string;
  username: string;
  displayName?: string | null;
  isAdmin?: boolean;
  studentId?: string | null;
  ptaNickname?: string | null;
  provisionSource?: "local" | "external_sso";
  localPasswordEnabled?: boolean;
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
  displayName?: string | null;
  studentId?: string | null;
  ptaNickname?: string | null;
}

function normalizeAccount(payload: AuthAccountPayload): AuthAccount {
  return {
    accountId: payload.accountId,
    username: payload.username,
    displayName: payload.displayName ?? payload.username,
    studentId: payload.studentId ?? null,
    ptaNickname: payload.ptaNickname ?? null,
    provisionSource: payload.provisionSource ?? "local",
    localPasswordEnabled: payload.localPasswordEnabled ?? true,
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
    displayName: payload.displayName ?? null,
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

export async function postSsoExchange(
  payload: SSOExchangePayload,
): Promise<AuthTokens> {
  const response = await requestJson<AuthTokensPayload>(
    "/api/v1/auth/sso/exchange",
    {
      method: "POST",
      body: payload,
    },
  );
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
  payload: Partial<AuthProfile>,
  authToken: string,
): Promise<AuthProfile> {
  const response = await requestJson<AuthProfilePayload>("/api/v1/auth/profile", {
    method: "PUT",
    body: {
      displayName: payload.displayName ?? null,
      studentId: payload.studentId ?? null,
      ptaNickname: payload.ptaNickname ?? null,
    },
    authToken,
  });
  return normalizeProfile(response);
}

export async function postBootstrapLocalPassword(
  payload: LocalPasswordBootstrapPayload,
  authToken: string,
): Promise<{ ok: boolean }> {
  return requestJson<{ ok: boolean }>("/api/v1/auth/local-password/bootstrap", {
    method: "POST",
    body: payload,
    authToken,
  });
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

export function streamAutoAnalysis(
  payload: AutoAnalysisRequestPayload,
  authToken: string | undefined,
  onEvent: (event: ChatStreamEvent) => void,
): Promise<void> {
  return streamJsonEvents("/api/v1/chat/auto-analysis/stream", payload, authToken, onEvent);
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
