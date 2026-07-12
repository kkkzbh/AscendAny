import { useCallback, useEffect, useRef, useState } from "react";
import { streamImportEvents, type ImportEvent, type ImportJob } from "@ascendany/sdk";
import { readImportJob } from "../api/import";
import { apiFailureMessage, browserSession, v2Client } from "../api/v2Client";

export interface ImportLogEntry {
  level: "info" | "success" | "warning" | "error";
  message: string;
  timestamp: string;
}

export interface ImportProgress {
  current: number;
  total: number;
  phase: string;
}

export type ImportStreamStatus = "idle" | "connecting" | "streaming" | "done" | "error";

export interface ImportJobStream {
  logs: ImportLogEntry[];
  progress: ImportProgress | null;
  status: ImportStreamStatus;
  result: ImportJob | null;
  errorMessage: string | null;
  connect: (jobId: string) => void;
  disconnect: () => void;
  clearLogs: () => void;
}

const stageProgress: Record<string, number> = {
  received: 1,
  claimed: 2,
  reclaimed: 2,
  validation_completed: 3,
  snapshot_imported: 4,
  completed: 5,
  failed: 5,
  superseded: 5,
};

const eventMessages: Record<string, string> = {
  received: "快照已持久化并进入导入队列",
  claimed: "Worker 已领取任务并开始验证",
  reclaimed: "任务租约已恢复并继续执行",
  validation_completed: "快照验证完成，开始写入数据",
  snapshot_imported: "考试快照已写入，正在生成分析结果",
  retry_scheduled: "任务已安排重试",
  completed: "导入与分析已完成",
  failed: "导入失败",
  superseded: "相同考试快照已存在，本任务已结束",
};

function terminalStatus(status: ImportJob["status"]): boolean {
  return status === "succeeded" || status === "failed" || status === "superseded";
}

function terminalEvent(type: string): boolean {
  return type === "completed" || type === "failed" || type === "superseded";
}

function logForEvent(event: ImportEvent): ImportLogEntry {
  const level: ImportLogEntry["level"] = event.type === "failed"
    ? "error"
    : event.type === "retry_scheduled"
      ? "warning"
      : terminalEvent(event.type)
        ? "success"
        : "info";
  return {
    level,
    message: eventMessages[event.type] ?? `任务事件：${event.type}`,
    timestamp: event.occurredAt,
  };
}

export function useImportJobStream(): ImportJobStream {
  const [logs, setLogs] = useState<ImportLogEntry[]>([]);
  const [progress, setProgress] = useState<ImportProgress | null>(null);
  const [status, setStatus] = useState<ImportStreamStatus>("idle");
  const [result, setResult] = useState<ImportJob | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const disconnect = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
  }, []);

  const clearLogs = useCallback(() => {
    setLogs([]);
  }, []);

  const connect = useCallback((jobId: string) => {
    disconnect();
    const controller = new AbortController();
    abortRef.current = controller;
    setLogs([]);
    setProgress(null);
    setResult(null);
    setErrorMessage(null);
    setStatus("connecting");

    void (async () => {
      let after = 0;
      try {
        while (!controller.signal.aborted) {
          await browserSession.ensureAuthenticated();
          let streamFailure: unknown;
          const eventStream = await streamImportEvents({
            client: v2Client,
            path: { jobId },
            headers: after > 0 ? { "Last-Event-ID": String(after) } : undefined,
            signal: controller.signal,
            sseMaxRetryAttempts: 1,
            onSseError: (error) => {
              streamFailure = error;
            },
          });
          setStatus("streaming");
          let sawTerminalEvent = false;
          for await (const event of eventStream.stream) {
            if (controller.signal.aborted) return;
            after = Math.max(after, event.sequence);
            setLogs((current) => [...current, logForEvent(event)]);
            const currentProgress = stageProgress[event.type];
            if (currentProgress !== undefined) {
              setProgress({ current: currentProgress, total: 5, phase: event.type });
            }
            if (terminalEvent(event.type)) {
              sawTerminalEvent = true;
            }
          }
          if (controller.signal.aborted) return;
          if (streamFailure !== undefined) throw streamFailure;

          const job = await readImportJob(jobId);
          if (terminalStatus(job.status) || sawTerminalEvent) {
            setResult(job);
            if (job.status === "failed") {
              const message = job.error?.message ?? "导入失败";
              setErrorMessage(message);
              setStatus("error");
            } else {
              setStatus("done");
            }
            return;
          }
          const resumeAfter = after;
          setLogs((current) => [...current, {
            level: "info",
            message: `事件流连接已到期，正在从序号 ${resumeAfter} 恢复`,
            timestamp: new Date().toISOString(),
          }]);
          setStatus("connecting");
        }
      } catch (error) {
        if (controller.signal.aborted) return;
        const message = apiFailureMessage(error);
        setErrorMessage(message);
        setStatus("error");
        setLogs((current) => [...current, {
          level: "error",
          message,
          timestamp: new Date().toISOString(),
        }]);
      }
    })();
  }, [disconnect]);

  useEffect(() => () => disconnect(), [disconnect]);

  return {
    logs,
    progress,
    status,
    result,
    errorMessage,
    connect,
    disconnect,
    clearLogs,
  };
}
