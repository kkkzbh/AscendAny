import {
  createPintiaImport,
  getImportJob,
  listImportJobs,
  type ImportJob,
  type ImportJobPage,
} from "@ascendany/sdk";
import { apiFailureMessage, browserSession, v2Client } from "./v2Client";

export type { ImportJob, ImportJobPage } from "@ascendany/sdk";

export async function uploadPintiaSnapshot(
  file: File,
  onProgress?: (percent: number) => void,
): Promise<ImportJob> {
  if (!file.name.toLowerCase().endsWith(".json")) {
    throw new Error("仅接受浏览器插件导出的 Pintia JSON 快照");
  }
  onProgress?.(0);
  try {
    await browserSession.ensureAuthenticated();
    const result = await createPintiaImport({
      client: v2Client,
      body: file,
      throwOnError: true,
    });
    onProgress?.(100);
    return result.data;
  } catch (error) {
    throw new Error(apiFailureMessage(error));
  }
}

export async function getImportHistory(limit = 20, cursor?: string): Promise<ImportJobPage> {
  try {
    await browserSession.ensureAuthenticated();
    const result = await listImportJobs({
      client: v2Client,
      query: { limit, ...(cursor ? { cursor } : {}) },
      throwOnError: true,
    });
    return result.data;
  } catch (error) {
    throw new Error(apiFailureMessage(error));
  }
}

export async function readImportJob(jobId: string): Promise<ImportJob> {
  try {
    await browserSession.ensureAuthenticated();
    const result = await getImportJob({
      client: v2Client,
      path: { jobId },
      throwOnError: true,
    });
    return result.data;
  } catch (error) {
    throw new Error(apiFailureMessage(error));
  }
}
