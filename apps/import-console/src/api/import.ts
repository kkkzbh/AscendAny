/**
 * Import-specific API functions.
 */

import { apiFetch, getStoredToken, apiUrl } from "./client";

// ── Types ────────────────────────────────────────────────

export const EXAM_TYPES = [
  { value: "pintia", label: "Pintia JSON" },
  { value: "datastructure", label: "数据结构" },
  { value: "pta_icpc", label: "PTA ICPC" },
  { value: "pta_ioi", label: "PTA IOI" },
] as const;

export type ExamTypeValue = (typeof EXAM_TYPES)[number]["value"];

export interface UploadResponse {
  examType: string;
  examName: string;
  sourcePath: string;
  fileCount: number;
  message: string;
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

export interface SingleImportRunRequest {
  examType: string;
  sourcePath: string;
  dryRun?: boolean;
  force?: boolean;
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

/**
 * Upload a Pintia JSON file or a legacy .zip file with the given exam type.
 * Uses raw fetch (FormData) since apiFetch auto-sets Content-Type.
 */
export async function uploadExamZip(
  file: File,
  examType: string,
  onProgress?: (pct: number) => void,
): Promise<UploadResponse> {
  const token = getStoredToken();
  const url = apiUrl("/api/v1/import/upload");

  return new Promise<UploadResponse>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url);
    if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);

    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable && onProgress) {
        onProgress(Math.round((e.loaded / e.total) * 100));
      }
    });

    xhr.addEventListener("load", () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText));
        } catch {
          reject(new Error("无法解析响应"));
        }
      } else {
        let msg = `HTTP ${xhr.status}`;
        try {
          const body = JSON.parse(xhr.responseText);
          msg = body?.detail ?? body?.error?.message ?? msg;
        } catch {
          // ignore
        }
        reject(new Error(msg));
      }
    });

    xhr.addEventListener("error", () => reject(new Error("网络错误")));
    xhr.addEventListener("abort", () => reject(new Error("上传已取消")));

    const form = new FormData();
    form.append("file", file);
    if (!file.name.toLowerCase().endsWith(".json")) {
      form.append("examType", examType);
    }
    xhr.send(form);
  });
}

export function startImportRun(req: ImportRunRequest): Promise<ImportRunResponse> {
  return apiFetch("/api/v1/import/run", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export function startSingleImportRun(
  req: SingleImportRunRequest,
): Promise<ImportRunResponse> {
  return apiFetch("/api/v1/import/run-single", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export function getIngestHistory(limit = 20): Promise<IngestHistoryResponse> {
  return apiFetch(`/api/v1/import/history?limit=${limit}`);
}
