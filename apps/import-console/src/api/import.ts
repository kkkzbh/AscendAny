/**
 * Import-specific API functions.
 */

import { apiFetch } from "./client";

// ── Types ────────────────────────────────────────────────

export interface DiscoverFileItem {
  fileRole: string;
  relativePath: string;
  sha256: string;
}

export interface DiscoverExamItem {
  examType: string;
  sourcePath: string;
  fingerprint: string;
  fileCount: number;
  hasChanged: boolean;
  files: DiscoverFileItem[];
}

export interface DiscoverResponse {
  examTypes: string[];
  exams: DiscoverExamItem[];
  totalCount: number;
  changedCount: number;
}

export interface ImportRunRequest {
  examTypes?: string[] | null;
  limit?: number | null;
  dryRun?: boolean;
  force?: boolean;
}

export interface ImportRunResponse {
  runId: string;
  message: string;
}

export interface LinkActorsRequest {
  examTypes?: string[] | null;
  limit?: number | null;
  dryRun?: boolean;
}

export interface LinkActorsResponse {
  runId: string;
  message: string;
}

export interface IngestHistoryItem {
  ingestRunId: number;
  status: string;
  startedAt: string | null;
  finishedAt: string | null;
  scanned: number | null;
  toProcess: number | null;
  succeeded: number | null;
  failed: number | null;
  errors: string[];
}

export interface IngestHistoryResponse {
  items: IngestHistoryItem[];
  total: number;
}

// ── API calls ────────────────────────────────────────────

export function discoverExams(examTypes?: string[]): Promise<DiscoverResponse> {
  const params = new URLSearchParams();
  if (examTypes?.length) {
    for (const t of examTypes) params.append("examType", t);
  }
  const qs = params.toString();
  return apiFetch(`/api/v1/import/discover${qs ? `?${qs}` : ""}`);
}

export function startImportRun(req: ImportRunRequest): Promise<ImportRunResponse> {
  return apiFetch("/api/v1/import/run", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export function startLinkActors(req: LinkActorsRequest): Promise<LinkActorsResponse> {
  return apiFetch("/api/v1/import/link-actors", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export function getIngestHistory(limit = 20): Promise<IngestHistoryResponse> {
  return apiFetch(`/api/v1/import/history?limit=${limit}`);
}
