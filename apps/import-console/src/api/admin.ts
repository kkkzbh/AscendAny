import { apiFetch } from "./client";

export type AdminModelProviderId = "siliconflow" | "openai" | "copilot" | "deepseek";
export type AdminModelRequestMode = "chat_completions" | "responses";
export type AdminModelListSource = "dynamic" | "static";

export interface AdminModelOption {
  modelId: string;
  label: string;
  requestMode: AdminModelRequestMode;
  deprecated: boolean;
  disabled: boolean;
  disabledReason: string | null;
}

export interface AdminModelProviderConfig {
  id: AdminModelProviderId;
  title: string;
  provider: string;
  strategyId: string;
  adapter: string;
  baseUrl: string;
  model: string;
  transportModel: string;
  apiKeyEnv: string;
  apiKeyConfigured: boolean;
  active: boolean;
  requestMode: AdminModelRequestMode;
  modelOptions: AdminModelOption[];
  description: string;
  modelHint: string;
}

export interface AdminModelConfigResponse {
  configPath: string;
  envFilePath: string;
  activeProvider: AdminModelProviderId;
  providers: AdminModelProviderConfig[];
  activeRuntime: {
    mode: string;
    baseUrl: string;
    model: string;
    apiKeyEnv: string;
  };
}

export interface AdminModelConfigPatch {
  activeProvider?: AdminModelProviderId;
  provider?: {
    id: AdminModelProviderId;
    adapter?: string;
    baseUrl?: string;
    model?: string;
    apiKeyEnv?: string;
    requestMode?: AdminModelRequestMode;
    apiKey?: string;
  };
}

export interface AdminModelConnectionTestResponse {
  ok: boolean;
  status: string;
  message: string;
  provider: string;
  model: string;
  elapsedMs: number;
}

export interface AdminDeepSeekModelsResponse {
  models: AdminModelOption[];
  source: AdminModelListSource;
  error: string | null;
}

export interface AdminPreprocessConfig {
  practiceRoot: string;
  encodings: string[];
  fingerprintRoles: string[];
  timezone: string;
  metrics: {
    winsorLow: number;
    winsorHigh: number;
    flexibilityModeDefault: string;
    includedProblemKinds: string[];
    randomExamMissingDrawnSetPolicy: string;
    randomExamSlotSourcePriority: string[];
  };
  mapping: {
    primaryKeys: string[];
    actorSources: string[];
    strictMode: boolean;
    autoBindOnIngest: boolean;
    claimIdentitySource: string;
  };
  fusionHalfLifeDays: {
    knowledge: number;
    accuracy: number;
    quality: number;
    flexibility: number;
    proficiency: number;
  };
  rating: {
    initialRating: number;
    maxBinarySearchRating: number;
    minBinarySearchRating: number;
    binarySearchSteps: number;
  };
  warmup: {
    enabled: boolean;
    apiBaseUrl: string | null;
    tokenEnv: string;
    timeoutSeconds: number;
    roleId: string;
  };
}

export interface AdminConfigResponse {
  preprocessConfigPath: string;
  preprocess: AdminPreprocessConfig;
  restartRequiredKeys: string[];
}

export type AdminConfigPatch = Partial<{
  preprocess: Partial<{
    practiceRoot: string;
    encodings: string[];
    fingerprintRoles: string[];
    timezone: string;
    metrics: Partial<AdminPreprocessConfig["metrics"]>;
    mapping: Partial<AdminPreprocessConfig["mapping"]>;
    fusionHalfLifeDays: Partial<AdminPreprocessConfig["fusionHalfLifeDays"]>;
    rating: Partial<AdminPreprocessConfig["rating"]>;
    warmup: Partial<AdminPreprocessConfig["warmup"]>;
  }>;
}>;

export interface AdminStudentSummary {
  studentEntityId: string;
  studentId: string | null;
  studentName: string | null;
  grade: string | null;
  username: string | null;
  rating: number;
  knowledge: number | null;
  accuracy: number | null;
  quality: number | null;
  flexibility: number | null;
  proficiency: number | null;
  latestExamAt: string | null;
  examCount: number;
  generatedReports: number;
  failedReports: number;
  missingReports: number;
  reportCompletionRate: number;
}

export interface AdminStudentExamReport {
  examId: string;
  examName: string;
  examType: string;
  examDate: string | null;
  rank: number | null;
  totalScore: number | null;
  solvedCount: number | null;
  ratingDelta: number | null;
  oldRating: number | null;
  newRating: number | null;
  knowledge: number | null;
  accuracy: number | null;
  quality: number | null;
  flexibility: number | null;
  proficiency: number | null;
  analysisStatus: string;
  analysisReply: string;
  generatedAt: string | null;
  errorMessage: string | null;
}

export interface AdminStudentListResponse {
  items: AdminStudentSummary[];
  total: number;
}

export interface AdminStudentExamReportsResponse {
  student: AdminStudentSummary | null;
  items: AdminStudentExamReport[];
}

export interface AdminAccountSummary {
  accountId: string;
  username: string;
  displayName: string;
  isActive: boolean;
  isAdmin: boolean;
  provisionSource: string;
  studentId: string | null;
  ptaNickname: string | null;
  createdAt: string | null;
  updatedAt: string | null;
  lastLoginAt: string | null;
}

export interface AdminAccountsResponse {
  items: AdminAccountSummary[];
  total: number;
}

export interface AdminAuditLogItem {
  id: string;
  kind: string;
  status: string;
  title: string;
  detail: string;
  actor: string | null;
  createdAt: string;
  payload: Record<string, unknown>;
}

export interface AdminAuditLogResponse {
  items: AdminAuditLogItem[];
  total: number;
}

export function getAdminConfig(): Promise<AdminConfigResponse> {
  return apiFetch("/api/v1/admin/config");
}

export function patchAdminConfig(payload: AdminConfigPatch): Promise<AdminConfigResponse> {
  return apiFetch("/api/v1/admin/config", {
    method: "PATCH",
    body: JSON.stringify(payload),
  });
}

export function getAdminModelConfig(): Promise<AdminModelConfigResponse> {
  return apiFetch("/api/v1/admin/model-config");
}

export function patchAdminModelConfig(payload: AdminModelConfigPatch): Promise<AdminModelConfigResponse> {
  return apiFetch("/api/v1/admin/model-config", {
    method: "PATCH",
    body: JSON.stringify(payload),
  });
}

export function testAdminModelConnection(payload: {
  providerId: AdminModelProviderId;
  adapter?: string;
  baseUrl: string;
  model: string;
  apiKeyEnv: string;
  requestMode?: AdminModelRequestMode;
  apiKey?: string;
}): Promise<AdminModelConnectionTestResponse> {
  return apiFetch("/api/v1/admin/model-config/test", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function listAdminDeepSeekModels(payload: {
  providerId?: AdminModelProviderId;
  adapter?: string;
  baseUrl: string;
  model?: string;
  apiKeyEnv: string;
  requestMode?: AdminModelRequestMode;
  apiKey?: string;
}): Promise<AdminDeepSeekModelsResponse> {
  const providerId = payload.providerId ?? "deepseek";
  return apiFetch(`/api/v1/admin/model-config/${providerId}/models`, {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function listAdminStudents(search: string, limit = 200): Promise<AdminStudentListResponse> {
  const params = new URLSearchParams();
  if (search.trim()) params.set("search", search.trim());
  params.set("limit", String(limit));
  return apiFetch(`/api/v1/admin/students?${params.toString()}`);
}

export function getAdminStudentExamReports(
  studentEntityId: string,
): Promise<AdminStudentExamReportsResponse> {
  return apiFetch(`/api/v1/admin/students/${encodeURIComponent(studentEntityId)}/exam-reports`);
}

export function listAdminAccounts(limit = 200): Promise<AdminAccountsResponse> {
  return apiFetch(`/api/v1/admin/accounts?limit=${limit}`);
}

export function listAdminAuditLog(limit = 100): Promise<AdminAuditLogResponse> {
  return apiFetch(`/api/v1/admin/audit-log?limit=${limit}`);
}
