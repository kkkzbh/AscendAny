import { useCallback, useEffect, useRef, useState, type DragEvent } from "react";
import { getImportHistory, uploadPintiaSnapshot, type ImportJob } from "../api/import";
import { EmptyState, PageHeader, StatusBadge } from "../components/ui";
import { useImportJobStream, type ImportLogEntry } from "../hooks/useImportJobStream";

function formatTime(value: string | null): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function shortID(value: string): string {
  return value.slice(0, 8);
}

export function ImportPage() {
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadPct, setUploadPct] = useState(0);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [recentJobs, setRecentJobs] = useState<ImportJob[]>([]);
  const [history, setHistory] = useState<ImportJob[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const logEndRef = useRef<HTMLDivElement>(null);
  const stream = useImportJobStream();
  const isStreaming = stream.status === "connecting" || stream.status === "streaming";
  const busy = uploading || isStreaming;

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [stream.logs]);

  const loadHistory = useCallback(async (cursor?: string) => {
    setHistoryLoading(true);
    setHistoryError(null);
    try {
      const response = await getImportHistory(30, cursor);
      setHistory((current) => cursor ? [...current, ...response.items] : response.items);
      setNextCursor(response.nextCursor);
    } catch (error) {
      setHistoryError(error instanceof Error ? error.message : "导入历史加载失败");
    } finally {
      setHistoryLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadHistory();
  }, [loadHistory]);

  useEffect(() => {
    if (stream.status === "done" || stream.status === "error") {
      void loadHistory();
    }
  }, [loadHistory, stream.status]);

  const doUpload = async (file: File) => {
    setUploadError(null);
    setUploading(true);
    setUploadPct(0);
    try {
      const job = await uploadPintiaSnapshot(file, setUploadPct);
      setRecentJobs((current) => [job, ...current.filter((item) => item.id !== job.id)].slice(0, 5));
      stream.connect(job.id);
      await loadHistory();
    } catch (error) {
      setUploadError(error instanceof Error ? error.message : "上传失败");
    } finally {
      setUploading(false);
      setUploadPct(0);
    }
  };

  const selectSingleJSON = async (files: File[]) => {
    if (busy) return;
    const jsonFiles = files.filter((file) => file.name.toLowerCase().endsWith(".json"));
    if (jsonFiles.length !== 1 || files.length !== 1) {
      setUploadError("每次请选择一个浏览器插件导出的 Pintia JSON 快照");
      return;
    }
    const file = jsonFiles[0];
    if (file) await doUpload(file);
  };

  const handleDrop = useCallback(async (event: DragEvent) => {
    event.preventDefault();
    event.stopPropagation();
    setDragOver(false);
    await selectSingleJSON(Array.from(event.dataTransfer.files));
  }, [busy]);

  const handleFileSelect = useCallback(async () => {
    const files = fileInputRef.current?.files;
    if (!files?.length) return;
    await selectSingleJSON(Array.from(files));
    if (fileInputRef.current) fileInputRef.current.value = "";
  }, [busy]);

  return (
    <div className="page page-import">
      <PageHeader
        title="Pintia 数据导入"
        description="上传浏览器插件导出的 snapshot v2 JSON；快照持久化后会立即进入 Go 导入队列。"
        actions={
          <button className="button" type="button" onClick={() => void loadHistory()} disabled={historyLoading}>
            {historyLoading ? "刷新中" : "刷新历史"}
          </button>
        }
      />

      <div className="import-workspace">
        <section className="panel upload-panel">
          <div className="panel-title">上传快照</div>
          <div
            className={`drop-target${dragOver ? " is-active" : ""}`}
            onClick={() => !busy && fileInputRef.current?.click()}
            onDragOver={(event) => {
              event.preventDefault();
              if (!busy) setDragOver(true);
            }}
            onDragLeave={(event) => {
              event.preventDefault();
              setDragOver(false);
            }}
            onDrop={handleDrop}
            role="button"
            tabIndex={0}
            aria-disabled={busy}
          >
            <input ref={fileInputRef} type="file" accept="application/json,.json" hidden onChange={handleFileSelect} />
            {uploading ? (
              <>
                <strong>正在上传并持久化 {uploadPct}%</strong>
                <div className="mini-progress">
                  <span style={{ width: `${uploadPct}%` }} />
                </div>
              </>
            ) : (
              <>
                <strong>{isStreaming ? "当前任务执行中" : "拖入 Pintia snapshot v2 JSON"}</strong>
                <span>{isStreaming ? "任务结束后可上传下一份快照" : "每次上传一份浏览器插件生成的完整 JSON 快照"}</span>
              </>
            )}
          </div>

          {uploadError ? <div className="notice notice-error">{uploadError}</div> : null}

          <div className="queue-list">
            <div className="queue-head">
              <span>本次会话任务</span>
              {recentJobs.length ? (
                <button className="button button-ghost" type="button" onClick={() => setRecentJobs([])}>
                  清空
                </button>
              ) : null}
            </div>
            {recentJobs.length ? recentJobs.map((job) => (
              <div className="queue-item" key={job.id}>
                <div>
                  <strong title={job.id}>{shortID(job.id)}</strong>
                  <span>{job.stage} · {job.artifactSha256.slice(0, 12)}</span>
                </div>
                <StatusBadge status={job.status} />
              </div>
            )) : <EmptyState>上传成功后，任务会立即显示在这里。</EmptyState>}
          </div>
        </section>

        <section className="panel task-panel">
          <div className="panel-title">当前任务</div>
          {stream.progress ? (
            <div className="progress-strip">
              <div className="progress-track">
                <span style={{ width: `${(stream.progress.current / stream.progress.total) * 100}%` }} />
              </div>
              <span>{stream.progress.current} / {stream.progress.total} · {stream.progress.phase}</span>
            </div>
          ) : null}

          {stream.result ? (
            <div className="summary-grid">
              <div className="metric-tile"><span>状态</span><strong>{stream.result.status}</strong></div>
              <div className="metric-tile"><span>阶段</span><strong>{stream.result.stage}</strong></div>
              <div className="metric-tile"><span>考试</span><strong>{stream.result.examId ? shortID(stream.result.examId) : "-"}</strong></div>
              <div className="metric-tile"><span>快照</span><strong>{stream.result.snapshotId ? shortID(stream.result.snapshotId) : "-"}</strong></div>
            </div>
          ) : (
            <div className="task-empty">
              <strong>{isStreaming ? "正在执行导入任务" : "等待快照上传"}</strong>
              <span>{isStreaming ? "事件流可断点续传，详细状态显示在底部终端。" : "快照上传成功后自动入队，无需再次启动。"}</span>
            </div>
          )}
          {stream.errorMessage ? <div className="notice notice-error">{stream.errorMessage}</div> : null}
        </section>

        <section className="panel history-panel">
          <div className="panel-title">导入历史</div>
          {historyError ? <div className="notice notice-error">{historyError}</div> : null}
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>任务</th>
                  <th>状态</th>
                  <th>阶段</th>
                  <th>创建时间</th>
                  <th>错误</th>
                </tr>
              </thead>
              <tbody>
                {history.map((job) => (
                  <tr key={job.id}>
                    <td title={job.id}>{shortID(job.id)}</td>
                    <td><StatusBadge status={job.status} /></td>
                    <td>{job.stage}</td>
                    <td>{formatTime(job.createdAt)}</td>
                    <td>{job.error?.message ?? "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {nextCursor ? (
            <button className="button button-ghost" type="button" disabled={historyLoading} onClick={() => void loadHistory(nextCursor)}>
              {historyLoading ? "加载中" : "加载更多"}
            </button>
          ) : null}
        </section>
      </div>

      <section className="panel log-panel import-terminal" aria-label="实时日志终端">
        <div className="log-title">
          <span>实时日志</span>
          {stream.logs.length ? (
            <button className="button button-ghost" type="button" onClick={stream.clearLogs}>清除</button>
          ) : null}
        </div>
        <div className="log-lines">
          {stream.logs.length
            ? stream.logs.map((log, index) => <LogLine key={`${log.timestamp}-${index}`} log={log} />)
            : <EmptyState>任务日志会在快照上传后显示。</EmptyState>}
          <div ref={logEndRef} />
        </div>
      </section>
    </div>
  );
}

function LogLine({ log }: { log: ImportLogEntry }) {
  const time = log.timestamp ? new Date(log.timestamp).toLocaleTimeString("zh-CN") : "";
  return (
    <div className={`log-line log-${log.level}`}>
      <span>{time}</span>
      <p>{log.message}</p>
    </div>
  );
}
