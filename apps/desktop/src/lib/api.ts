import type { ChatBlock, Role } from "@/types/chat";
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
import type {
  KnowledgeNodeDetail,
  KnowledgeNodeProblem,
  KnowledgeNodeRecentDay,
  KnowledgeNodeStats,
  LearningPathSnapshot,
  LearningPathStatusItem,
  LearningPathStatusSnapshot,
} from "@/types/path";

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
  reasoningContent?: string;
}

export interface ChatReplyRequestPayload {
  studentId?: string;
  ptaNickname?: string;
  messages: ChatMessagePayload[];
  summary: string;
  roleId?: string;
  roleName?: string;
  roleSystemPrompt?: string;
  notes?: string;
  notesTitle?: string;
  notesLocked?: boolean;
}

export interface AutoAnalysisRequestPayload {
  studentId?: string;
  ptaNickname?: string;
  roleId?: string;
  roleName?: string;
  roleSystemPrompt?: string;
  latestExamId?: string;
  notes?: string;
  notesTitle?: string;
  notesLocked?: boolean;
}

export interface AutoAnalysisResponsePayload {
  reply: string;
  provider: ProviderType;
  model?: string;
  requestMode?: string;
  updatedNotes?: string | null;
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
  updatedNotes?: string | null;
}

export type ChatStreamEvent =
  | { type: "meta"; provider?: string; model?: string; requestMode?: string; summary?: string }
  | { type: "delta"; text: string }
  | { type: "reasoning_delta"; text: string }
  | {
      type: "tool_activity_start" | "tool_activity_done" | "tool_activity_error";
      activityId: string;
      label: string;
      status: "running" | "done" | "error";
    }
  | {
      type: "notes_update";
      mode: "patch" | "replace";
      previous: string;
      next: string;
      patch: string | null;
    }
  | {
      type: "path_update";
      mode: "patch" | "replace";
      previous: LearningPathSnapshot | null;
      next: LearningPathSnapshot;
    }
  | { type: "node_focus"; point: string }
  | { type: "node_status"; point: string; mastery: number }
  | { type: "block_append"; block: ChatBlock }
  | {
      type: "done";
      reply: string;
      summary?: string;
      provider?: string;
      model?: string;
      requestMode?: string;
      updatedNotes?: string | null;
    }
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
    } else if (type === "reasoning_delta") {
      onEvent({ type: "reasoning_delta", text: String(parsed.text ?? "") });
    } else if (type === "done") {
      onEvent({
        type: "done",
        reply: String(parsed.reply ?? ""),
        summary: typeof parsed.summary === "string" ? parsed.summary : undefined,
        provider: typeof parsed.provider === "string" ? parsed.provider : undefined,
        model: typeof parsed.model === "string" ? parsed.model : undefined,
        requestMode: typeof parsed.requestMode === "string" ? parsed.requestMode : undefined,
        updatedNotes:
          typeof parsed.updatedNotes === "string" ? parsed.updatedNotes : undefined,
      });
    } else if (type === "error") {
      throw new ApiError(
        String(parsed.message ?? "流式请求失败"),
        response.status,
        typeof parsed.code === "string" ? parsed.code : undefined,
      );
    } else if (
      type === "tool_activity_start" ||
      type === "tool_activity_done" ||
      type === "tool_activity_error"
    ) {
      const activityId = typeof parsed.activityId === "string" ? parsed.activityId.trim() : "";
      const label = typeof parsed.label === "string" ? parsed.label.trim() : "";
      const status =
        parsed.status === "running" || parsed.status === "done" || parsed.status === "error"
          ? parsed.status
          : type === "tool_activity_start"
            ? "running"
            : type === "tool_activity_error"
              ? "error"
              : "done";
      if (activityId && label) {
        onEvent({ type, activityId, label, status });
      }
    } else if (type === "notes_update") {
      const mode = parsed.mode === "patch" || parsed.mode === "replace" ? parsed.mode : null;
      if (
        mode
        && typeof parsed.previous === "string"
        && typeof parsed.next === "string"
      ) {
        onEvent({
          type: "notes_update",
          mode,
          previous: parsed.previous,
          next: parsed.next,
          patch: typeof parsed.patch === "string" ? parsed.patch : null,
        });
      }
    } else if (type === "path_update") {
      const mode = parsed.mode === "patch" || parsed.mode === "replace" ? parsed.mode : "replace";
      const next = parsed.next as LearningPathPayload | undefined;
      if (next && typeof next === "object") {
        onEvent({
          type: "path_update",
          mode,
          previous:
            parsed.previous && typeof parsed.previous === "object"
              ? normalizeLearningPath(parsed.previous as LearningPathPayload)
              : null,
          next: normalizeLearningPath(next),
        });
      }
    } else if (type === "node_focus") {
      const point = typeof parsed.point === "string" ? parsed.point.trim() : "";
      if (point) {
        onEvent({ type: "node_focus", point });
      }
    } else if (type === "node_status") {
      const point = typeof parsed.point === "string" ? parsed.point.trim() : "";
      const mastery =
        typeof parsed.mastery === "number" && Number.isFinite(parsed.mastery)
          ? parsed.mastery
          : null;
      if (point && mastery !== null) {
        onEvent({ type: "node_status", point, mastery });
      }
    } else if (type === "block_append") {
      const block = parseStreamBlock(parsed.block);
      if (block) {
        onEvent({ type: "block_append", block });
      }
    } else if (type === "tool_start" || type === "tool_done") {
      return;
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

function parseStreamBlock(value: unknown): ChatBlock | null {
  if (!value || typeof value !== "object") return null;
  const kind = (value as { kind?: unknown }).kind;
  if (kind === "text") {
    const text = (value as { text?: unknown }).text;
    return typeof text === "string" && text.length > 0
      ? { kind: "text", text }
      : null;
  }
  if (kind === "problem") {
    const problemRaw = (value as { problem?: unknown }).problem;
    if (!problemRaw || typeof problemRaw !== "object") return null;
    const problem = problemRaw as Record<string, unknown>;
    const problemId =
      typeof problem.problemId === "string" ? problem.problemId.trim() : "";
    if (!problemId) return null;
    const knowledgePoints = Array.isArray(problem.knowledgePoints)
      ? problem.knowledgePoints
          .filter((point): point is string => typeof point === "string")
          .map((point) => point.trim())
          .filter(Boolean)
      : [];
    return {
      kind: "problem",
      problem: {
        problemId,
        title: typeof problem.title === "string" ? problem.title : null,
        difficulty:
          typeof problem.difficulty === "number" ? problem.difficulty : null,
        knowledgePoints,
        reason: typeof problem.reason === "string" ? problem.reason : null,
      },
    };
  }
  if (kind === "choice") {
    const question = (value as { question?: unknown }).question;
    const optionsRaw = (value as { options?: unknown }).options;
    if (typeof question !== "string" || !question.trim()) return null;
    if (!Array.isArray(optionsRaw)) return null;
    const options: { id: string; label: string }[] = [];
    for (const option of optionsRaw) {
      if (!option || typeof option !== "object") continue;
      const candidate = option as Record<string, unknown>;
      const id = typeof candidate.id === "string" ? candidate.id.trim() : "";
      const label =
        typeof candidate.label === "string" ? candidate.label.trim() : "";
      if (id && label) options.push({ id, label });
    }
    if (options.length === 0) return null;
    const answerIdxRaw = (value as { answerIdx?: unknown }).answerIdx;
    const explanationRaw = (value as { explanation?: unknown }).explanation;
    return {
      kind: "choice",
      question: question.trim(),
      options,
      answerIdx:
        typeof answerIdxRaw === "number" &&
        answerIdxRaw >= 0 &&
        answerIdxRaw < options.length
          ? answerIdxRaw
          : undefined,
      explanation:
        typeof explanationRaw === "string" && explanationRaw.trim()
          ? explanationRaw.trim()
          : undefined,
    };
  }
  if (kind === "math_steps") {
    const stepsRaw = (value as { steps?: unknown }).steps;
    if (!Array.isArray(stepsRaw)) return null;
    const steps: Array<{ title?: string; tex: string; note?: string }> = [];
    for (const step of stepsRaw) {
      if (!step || typeof step !== "object") continue;
      const candidate = step as Record<string, unknown>;
      const tex = typeof candidate.tex === "string" ? candidate.tex.trim() : "";
      if (!tex) continue;
      const result: { title?: string; tex: string; note?: string } = { tex };
      if (typeof candidate.title === "string" && candidate.title.trim()) {
        result.title = candidate.title.trim();
      }
      if (typeof candidate.note === "string" && candidate.note.trim()) {
        result.note = candidate.note.trim();
      }
      steps.push(result);
    }
    return steps.length > 0 ? { kind: "math_steps", steps } : null;
  }
  if (kind === "code") {
    const code = (value as { code?: unknown }).code;
    if (typeof code !== "string" || !code) return null;
    const langRaw = (value as { lang?: unknown }).lang;
    const lang = typeof langRaw === "string" ? langRaw.trim() : "";
    return { kind: "code", lang: lang || "text", code };
  }
  if (kind === "node_ref") {
    const point = (value as { point?: unknown }).point;
    if (typeof point !== "string" || !point.trim()) return null;
    const labelRaw = (value as { label?: unknown }).label;
    return {
      kind: "node_ref",
      point: point.trim(),
      label:
        typeof labelRaw === "string" && labelRaw.trim()
          ? labelRaw.trim()
          : undefined,
    };
  }
  if (kind === "callout") {
    const tone = (value as { tone?: unknown }).tone;
    const markdown = (value as { markdown?: unknown }).markdown;
    if (typeof markdown !== "string" || !markdown.trim()) return null;
    const safeTone = tone === "warn" ? "warn" : tone === "tip" ? "tip" : "info";
    return { kind: "callout", tone: safeTone, markdown: markdown.trim() };
  }
  return null;
}

interface LearningPathPayload {
  studentEntityId?: number;
  studentEntityIds?: number[];
  modelRunId?: number | null;
  generatedAt?: string | null;
  targets?: string[];
  path?: string[];
  explanations?: Record<string, unknown>;
}

interface LearningPathStatusItemPayload {
  point?: string;
  mastery?: number;
  attempted?: number;
  correct?: number;
  lastTriedAt?: string | null;
}

interface LearningPathStatusPayload {
  studentEntityId?: number;
  studentEntityIds?: number[];
  items?: LearningPathStatusItemPayload[];
}

interface KnowledgeNodeRecentDayPayload {
  date?: string;
  attempted?: number;
  correct?: number;
}

interface KnowledgeNodeStatsPayload {
  attempted?: number;
  correct?: number;
  accuracy?: number;
  lastTriedAt?: string | null;
  recentSeries?: KnowledgeNodeRecentDayPayload[];
}

interface KnowledgeNodeProblemPayload {
  problemId?: string;
  title?: string | null;
  difficulty?: number | null;
  knowledgePoints?: string[];
  score?: number | null;
  reason?: string | null;
}

interface KnowledgeNodeDetailPayload {
  point?: string;
  level?: string | null;
  parents?: string[];
  children?: string[];
  prerequisites?: string[];
  successors?: string[];
  description?: string | null;
  mastery?: number;
  stats?: KnowledgeNodeStatsPayload;
  problems?: KnowledgeNodeProblemPayload[];
}

function normalizeLearningPath(payload: LearningPathPayload): LearningPathSnapshot {
  return {
    studentEntityId: Number(payload.studentEntityId ?? 0),
    studentEntityIds: Array.isArray(payload.studentEntityIds)
      ? payload.studentEntityIds.map((value) => Number(value))
      : [],
    modelRunId:
      typeof payload.modelRunId === "number" ? payload.modelRunId : null,
    generatedAt:
      typeof payload.generatedAt === "string" ? payload.generatedAt : null,
    targets: Array.isArray(payload.targets)
      ? payload.targets.map((value) => String(value))
      : [],
    path: Array.isArray(payload.path)
      ? payload.path.map((value) => String(value))
      : [],
    explanations:
      payload.explanations && typeof payload.explanations === "object"
        ? (payload.explanations as Record<string, unknown>)
        : {},
  };
}

function normalizeLearningPathStatus(
  payload: LearningPathStatusPayload,
): LearningPathStatusSnapshot {
  const items: LearningPathStatusItem[] = Array.isArray(payload.items)
    ? payload.items
        .map((item) => ({
          point: String(item.point ?? "").trim(),
          mastery: Number(item.mastery ?? 0),
          attempted: Number(item.attempted ?? 0),
          correct: Number(item.correct ?? 0),
          lastTriedAt:
            typeof item.lastTriedAt === "string" ? item.lastTriedAt : null,
        }))
        .filter((item) => item.point.length > 0)
    : [];
  return {
    studentEntityId: Number(payload.studentEntityId ?? 0),
    studentEntityIds: Array.isArray(payload.studentEntityIds)
      ? payload.studentEntityIds.map((value) => Number(value))
      : [],
    items,
  };
}

function normalizeKnowledgeNodeStats(
  payload: KnowledgeNodeStatsPayload | undefined,
): KnowledgeNodeStats {
  const recent: KnowledgeNodeRecentDay[] = Array.isArray(payload?.recentSeries)
    ? payload.recentSeries.map((day) => ({
        date: String(day?.date ?? ""),
        attempted: Number(day?.attempted ?? 0),
        correct: Number(day?.correct ?? 0),
      }))
    : [];
  return {
    attempted: Number(payload?.attempted ?? 0),
    correct: Number(payload?.correct ?? 0),
    accuracy: Number(payload?.accuracy ?? 0),
    lastTriedAt:
      typeof payload?.lastTriedAt === "string" ? payload.lastTriedAt : null,
    recentSeries: recent,
  };
}

function normalizeKnowledgeNodeDetail(
  payload: KnowledgeNodeDetailPayload,
): KnowledgeNodeDetail {
  const problems: KnowledgeNodeProblem[] = Array.isArray(payload.problems)
    ? payload.problems
        .map((problem) => ({
          problemId: String(problem?.problemId ?? "").trim(),
          title:
            typeof problem?.title === "string" && problem.title.trim()
              ? problem.title
              : null,
          difficulty:
            typeof problem?.difficulty === "number" ? problem.difficulty : null,
          knowledgePoints: Array.isArray(problem?.knowledgePoints)
            ? problem.knowledgePoints.map((value) => String(value))
            : [],
          score: typeof problem?.score === "number" ? problem.score : null,
          reason:
            typeof problem?.reason === "string" && problem.reason.trim()
              ? problem.reason
              : null,
        }))
        .filter((problem) => problem.problemId.length > 0)
    : [];
  return {
    point: String(payload.point ?? "").trim(),
    level: typeof payload.level === "string" ? payload.level : null,
    parents: Array.isArray(payload.parents)
      ? payload.parents.map((value) => String(value))
      : [],
    children: Array.isArray(payload.children)
      ? payload.children.map((value) => String(value))
      : [],
    prerequisites: Array.isArray(payload.prerequisites)
      ? payload.prerequisites.map((value) => String(value))
      : [],
    successors: Array.isArray(payload.successors)
      ? payload.successors.map((value) => String(value))
      : [],
    description:
      typeof payload.description === "string" && payload.description.trim()
        ? payload.description
        : null,
    mastery: Number(payload.mastery ?? 0),
    stats: normalizeKnowledgeNodeStats(payload.stats),
    problems,
  };
}

export async function fetchLearningPath(
  authToken?: string,
): Promise<LearningPathSnapshot> {
  const payload = await requestJson<LearningPathPayload>(
    "/api/v1/recommendations/path/me",
    { authToken },
  );
  return normalizeLearningPath(payload);
}

export async function fetchLearningPathStatus(
  authToken?: string,
): Promise<LearningPathStatusSnapshot> {
  const payload = await requestJson<LearningPathStatusPayload>(
    "/api/v1/recommendations/path/me/status",
    { authToken },
  );
  return normalizeLearningPathStatus(payload);
}

export async function fetchKnowledgeNodeDetail(
  point: string,
  options?: { topK?: number; authToken?: string },
): Promise<KnowledgeNodeDetail> {
  const payload = await requestJson<KnowledgeNodeDetailPayload>(
    `/api/v1/recommendations/knowledge/${encodeURIComponent(point)}`,
    {
      query: options?.topK ? { topK: options.topK } : undefined,
      authToken: options?.authToken,
    },
  );
  return normalizeKnowledgeNodeDetail(payload);
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
