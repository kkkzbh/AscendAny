import {
  createConfigurationVersion,
  getConfiguration,
  getRecommendationReviewContext,
  type ApiError,
  type ConfigurationItem,
  type CreateConfigurationVersionResult,
  type CreateRecommendationKnowledgeCatalogVersionRequest,
  type RecommendationReviewContext,
} from "@ascendany/sdk";
import { browserSession, v2Client } from "./v2Client";

type GeneratedResult<T> = {
  data: T | undefined;
  error: unknown;
  response?: Response;
};

const CANONICAL_UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const API_ERROR_KEYS = new Set(["code", "message", "requestId", "details"]);

function plainRecord(value: unknown): value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null;
}

function apiError(value: unknown): value is ApiError {
  if (!plainRecord(value) || Object.keys(value).some((key) => !API_ERROR_KEYS.has(key))) return false;
  if (typeof value.code !== "string" || value.code.length === 0) return false;
  if (typeof value.message !== "string" || value.message.length === 0) return false;
  if (typeof value.requestId !== "string" || !CANONICAL_UUID_V4.test(value.requestId)) return false;
  return value.details === undefined || plainRecord(value.details);
}

export class RecommendationCatalogApiError extends Error {
  readonly status: number | undefined;
  readonly apiError: ApiError;

  constructor(status: number | undefined, apiError: ApiError) {
    super(apiError.message);
    this.name = "RecommendationCatalogApiError";
    this.status = status;
    this.apiError = apiError;
  }
}

async function authenticated<T>(operation: () => Promise<GeneratedResult<T>>): Promise<T> {
  await browserSession.ensureAuthenticated();
  const result = await operation();
  if (result.data !== undefined) return result.data;
  if (apiError(result.error)) throw new RecommendationCatalogApiError(result.response?.status, result.error);
  if (result.error instanceof Error) throw result.error;
  if (typeof result.error === "string" && result.error.length > 0) throw new Error(result.error);
  throw new Error("Recommendation catalog request failed without a valid API error.");
}

export function loadRecommendationReviewContext(): Promise<RecommendationReviewContext> {
  return authenticated<RecommendationReviewContext>(() => getRecommendationReviewContext({ client: v2Client }));
}

export function loadRecommendationKnowledgeCatalog(key: string): Promise<ConfigurationItem> {
  return authenticated<ConfigurationItem>(() => getConfiguration({
    client: v2Client,
    path: { key },
  }));
}

export function publishRecommendationKnowledgeCatalog(
  body: CreateRecommendationKnowledgeCatalogVersionRequest,
): Promise<CreateConfigurationVersionResult> {
  return authenticated<CreateConfigurationVersionResult>(() => createConfigurationVersion({ client: v2Client, body }));
}
