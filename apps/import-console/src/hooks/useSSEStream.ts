import { useCallback, useEffect, useRef, useState } from "react";
import { apiUrl, getStoredToken } from "../api/client";

export interface LogEntry {
  level: "info" | "success" | "warning" | "error";
  message: string;
  timestamp: string;
}

export interface ProgressInfo {
  current: number;
  total: number;
  examType?: string | null;
  sourcePath?: string | null;
  phase?: string | null;
}

export type StreamStatus = "idle" | "connecting" | "streaming" | "done" | "error";

export interface UseSSEStreamReturn {
  logs: LogEntry[];
  progress: ProgressInfo | null;
  status: StreamStatus;
  result: Record<string, unknown> | null;
  errorMessage: string | null;
  connect: (runId: string, streamPath: string) => void;
  disconnect: () => void;
  clearLogs: () => void;
}

/**
 * Custom hook to consume SSE event streams from import/link-actors tasks.
 *
 * Uses fetch + ReadableStream instead of EventSource to support
 * the Authorization header.
 */
export function useSSEStream(): UseSSEStreamReturn {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [progress, setProgress] = useState<ProgressInfo | null>(null);
  const [status, setStatus] = useState<StreamStatus>("idle");
  const [result, setResult] = useState<Record<string, unknown> | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const disconnect = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
  }, []);

  const clearLogs = useCallback(() => {
    setLogs([]);
    setProgress(null);
    setResult(null);
    setErrorMessage(null);
    setStatus("idle");
  }, []);

  const connect = useCallback((runId: string, streamPath: string) => {
    // Abort any previous connection
    disconnect();

    const controller = new AbortController();
    abortRef.current = controller;

    setStatus("connecting");
    setErrorMessage(null);

    const url = apiUrl(streamPath.replace("{run_id}", runId));
    const token = getStoredToken();

    (async () => {
      try {
        const response = await fetch(url, {
          headers: {
            Authorization: token ? `Bearer ${token}` : "",
            Accept: "text/event-stream",
          },
          signal: controller.signal,
        });

        if (!response.ok) {
          setStatus("error");
          setErrorMessage(`HTTP ${response.status}: ${response.statusText}`);
          return;
        }

        setStatus("streaming");

        const reader = response.body?.getReader();
        if (!reader) {
          setStatus("error");
          setErrorMessage("No readable stream available");
          return;
        }

        const decoder = new TextDecoder();
        let buffer = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop() ?? "";

          let currentEventType = "";
          let currentData = "";

          for (const line of lines) {
            if (line.startsWith("event: ")) {
              currentEventType = line.slice(7).trim();
            } else if (line.startsWith("data: ")) {
              currentData = line.slice(6);

              if (currentEventType && currentData) {
                try {
                  const parsed = JSON.parse(currentData);
                  handleEvent(currentEventType, parsed);
                } catch {
                  // ignore malformed JSON
                }
                currentEventType = "";
                currentData = "";
              }
            } else if (line === "") {
              // empty line = end of event block
              if (currentEventType && currentData) {
                try {
                  const parsed = JSON.parse(currentData);
                  handleEvent(currentEventType, parsed);
                } catch {
                  // ignore
                }
              }
              currentEventType = "";
              currentData = "";
            }
          }
        }

        // Stream ended naturally
        setStatus((prev) => (prev === "streaming" ? "done" : prev));
      } catch (err) {
        if ((err as Error).name === "AbortError") return;
        setStatus("error");
        setErrorMessage(err instanceof Error ? err.message : "Stream failed");
      }
    })();

    function handleEvent(eventType: string, data: Record<string, unknown>) {
      const ts =
        (data.timestamp as string) ?? new Date().toISOString();

      switch (eventType) {
        case "log":
          setLogs((prev) => [
            ...prev,
            {
              level: (data.level as LogEntry["level"]) ?? "info",
              message: (data.message as string) ?? "",
              timestamp: ts,
            },
          ]);
          break;

        case "progress":
          setProgress({
            current: (data.current as number) ?? 0,
            total: (data.total as number) ?? 0,
            examType: data.examType as string | undefined,
            sourcePath: data.sourcePath as string | undefined,
            phase: data.phase as string | undefined,
          });
          break;

        case "done":
          setResult(data);
          setStatus("done");
          setLogs((prev) => [
            ...prev,
            { level: "success", message: "✅ 任务完成", timestamp: ts },
          ]);
          break;

        case "error":
          setErrorMessage((data.message as string) ?? "Unknown error");
          setStatus("error");
          setLogs((prev) => [
            ...prev,
            {
              level: "error",
              message: `❌ ${(data.message as string) ?? "Unknown error"}`,
              timestamp: ts,
            },
          ]);
          break;

        case "heartbeat":
          // ignore
          break;

        default:
          break;
      }
    }
  }, [disconnect]);

  // Cleanup on unmount
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
