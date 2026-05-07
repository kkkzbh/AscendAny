import { useCallback, useEffect, useRef, useState, type DragEvent } from "react";
import { EXAM_TYPES, getIngestHistory, startImportRun, uploadExamZip, type IngestHistoryItem } from "../api/import";
import { EmptyState, PageHeader, StatusBadge } from "../components/ui";
import { useSSEStream, type LogEntry } from "../hooks/useSSEStream";

interface UploadedExam {
  examType: string;
  examName: string;
  sourcePath: string;
  fileCount: number;
}

const EXAM_TYPE_LABELS: Record<string, string> = {
  pintia: "Pintia",
  datastructure: "数据结构",
  pta_icpc: "PTA ICPC",
  pta_ioi: "PTA IOI",
};

function examLabel(type: string): string {
  return EXAM_TYPE_LABELS[type] ?? type;
}

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

export function ImportPage() {
  const [selectedType, setSelectedType] = useState<string>(EXAM_TYPES[0]?.value ?? "pintia");
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadPct, setUploadPct] = useState(0);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [uploadedExams, setUploadedExams] = useState<UploadedExam[]>([]);
  const [dryRun, setDryRun] = useState(false);
  const [force, setForce] = useState(false);
  const [history, setHistory] = useState<IngestHistoryItem[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [taskBusy, setTaskBusy] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const logEndRef = useRef<HTMLDivElement>(null);
  const stream = useSSEStream();

  const isStreaming = stream.status === "connecting" || stream.status === "streaming";

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [stream.logs]);

  const loadHistory = useCallback(async () => {
    setHistoryLoading(true);
    setHistoryError(null);
    try {
      const response = await getIngestHistory(30);
      setHistory(response.items);
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
      setTaskBusy(false);
      void loadHistory();
    }
  }, [loadHistory, stream.status]);

  const doUpload = async (files: File[]) => {
    setUploadError(null);
    setUploading(true);
    setUploadPct(0);
    const results: UploadedExam[] = [];
    try {
      for (const file of files) {
        const response = await uploadExamZip(file, selectedType, setUploadPct);
        results.push({
          examType: response.examType,
          examName: response.examName,
          sourcePath: response.sourcePath,
          fileCount: response.fileCount,
        });
      }
      setUploadedExams((current) => [...current, ...results]);
    } catch (error) {
      setUploadError(error instanceof Error ? error.message : "上传失败");
    } finally {
      setUploading(false);
      setUploadPct(0);
    }
  };

  const handleDrop = useCallback(
    async (event: DragEvent) => {
      event.preventDefault();
      event.stopPropagation();
      setDragOver(false);
      const files = Array.from(event.dataTransfer.files).filter((file) =>
        file.name.toLowerCase().endsWith(".json") || file.name.toLowerCase().endsWith(".zip"),
      );
      if (!files.length) {
        setUploadError("请拖入 Pintia .json 或旧格式 .zip 文件");
        return;
      }
      await doUpload(files);
    },
    [selectedType],
  );

  const handleFileSelect = useCallback(async () => {
    const files = fileInputRef.current?.files;
    if (!files?.length) return;
    await doUpload(Array.from(files));
    if (fileInputRef.current) fileInputRef.current.value = "";
  }, [selectedType]);

  const handleImport = useCallback(async () => {
    setTaskBusy(true);
    stream.clearLogs();
    try {
      const examTypes = uploadedExams.length
        ? [...new Set(uploadedExams.map((item) => item.examType))]
        : [selectedType];
      const response = await startImportRun({ examTypes, dryRun, force });
      stream.connect(response.runId, "/api/v1/import/run/{run_id}/stream");
    } catch (error) {
      setTaskBusy(false);
      stream.clearLogs();
      window.alert(error instanceof Error ? error.message : "启动导入失败");
    }
  }, [dryRun, force, selectedType, stream, uploadedExams]);

  return (
    <div className="page page-import">
      <PageHeader
        title="数据导入"
        description="上传考试数据，启动增量导入，并跟踪实时任务日志。"
        actions={
          <button className="button" type="button" onClick={loadHistory} disabled={historyLoading}>
            {historyLoading ? "刷新中" : "刷新历史"}
          </button>
        }
      />

      <div className="import-workspace">
        <section className="panel upload-panel">
          <div className="panel-title">上传队列</div>
          <label className="field">
            <span className="field-label">旧 ZIP 类型</span>
            <select value={selectedType} onChange={(event) => setSelectedType(event.target.value)} disabled={uploading}>
              {EXAM_TYPES.map((item) => (
                <option key={item.value} value={item.value}>
                  {item.label}
                </option>
              ))}
            </select>
          </label>

          <div
            className={`drop-target${dragOver ? " is-active" : ""}`}
            onClick={() => !uploading && fileInputRef.current?.click()}
            onDragOver={(event) => {
              event.preventDefault();
              setDragOver(true);
            }}
            onDragLeave={(event) => {
              event.preventDefault();
              setDragOver(false);
            }}
            onDrop={handleDrop}
            role="button"
            tabIndex={0}
          >
            <input ref={fileInputRef} type="file" accept=".json,.zip" multiple hidden onChange={handleFileSelect} />
            {uploading ? (
              <>
                <strong>上传中 {uploadPct}%</strong>
                <div className="mini-progress">
                  <span style={{ width: `${uploadPct}%` }} />
                </div>
              </>
            ) : (
              <>
                <strong>拖入 Pintia JSON</strong>
                <span>也支持选择旧格式 ZIP 包</span>
              </>
            )}
          </div>

          {uploadError ? <div className="notice notice-error">{uploadError}</div> : null}

          <div className="queue-list">
            <div className="queue-head">
              <span>已上传 {uploadedExams.length}</span>
              {uploadedExams.length ? (
                <button className="button button-ghost" type="button" onClick={() => setUploadedExams([])}>
                  清空
                </button>
              ) : null}
            </div>
            {uploadedExams.length ? (
              uploadedExams.map((item, index) => (
                <div className="queue-item" key={`${item.sourcePath}-${index}`}>
                  <div>
                    <strong>{item.examName}</strong>
                    <span>{examLabel(item.examType)} · {item.fileCount} 文件</span>
                  </div>
                  <button
                    className="button button-ghost"
                    type="button"
                    onClick={() => setUploadedExams((current) => current.filter((_, itemIndex) => itemIndex !== index))}
                  >
                    移除
                  </button>
                </div>
              ))
            ) : (
              <EmptyState>没有待处理上传。未上传时默认扫描 Pintia JSON 增量数据。</EmptyState>
            )}
          </div>
        </section>

        <section className="panel task-panel">
          <div className="task-toolbar">
            <button className="button button-primary" type="button" disabled={taskBusy || isStreaming} onClick={handleImport}>
              {isStreaming ? "导入中" : "开始增量导入"}
            </button>
            <label className="check-control">
              <input type="checkbox" checked={dryRun} disabled={isStreaming} onChange={(event) => setDryRun(event.target.checked)} />
              Dry Run
            </label>
            <label className="check-control">
              <input type="checkbox" checked={force} disabled={isStreaming} onChange={(event) => setForce(event.target.checked)} />
              Force
            </label>
          </div>

          {stream.progress ? (
            <div className="progress-strip">
              <div className="progress-track">
                <span
                  style={{
                    width: `${stream.progress.total > 0 ? (stream.progress.current / stream.progress.total) * 100 : 0}%`,
                  }}
                />
              </div>
              <span>
                {stream.progress.current} / {stream.progress.total}
                {stream.progress.examType ? ` · ${examLabel(stream.progress.examType)}` : ""}
              </span>
            </div>
          ) : null}

          {stream.status === "done" && stream.result ? (
            <div className="summary-grid">
              {["scanned", "skipped", "succeeded", "failed", "submissionsBound", "submissionsPendingClaim"].map((key) => (
                <div className="metric-tile" key={key}>
                  <span>{key}</span>
                  <strong>{String(stream.result?.[key] ?? 0)}</strong>
                </div>
              ))}
            </div>
          ) : (
            <div className="task-empty">
              <strong>{isStreaming ? "正在执行导入任务" : "等待导入任务"}</strong>
              <span>
                {isStreaming
                  ? "进度会显示在上方，详细输出在底部终端。"
                  : "上传 Pintia JSON 或设置导入选项后启动任务。实时日志会在底部终端显示。"}
              </span>
            </div>
          )}
        </section>

        <section className="panel history-panel">
          <div className="panel-title">导入历史</div>
          {historyError ? <div className="notice notice-error">{historyError}</div> : null}
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>状态</th>
                  <th>开始</th>
                  <th>成功</th>
                  <th>失败</th>
                </tr>
              </thead>
              <tbody>
                {history.map((item) => (
                  <tr key={item.ingestRunId}>
                    <td>{item.ingestRunId}</td>
                    <td><StatusBadge status={item.status} /></td>
                    <td>{formatTime(item.startedAt)}</td>
                    <td>{item.succeeded ?? "-"}</td>
                    <td>{item.failed ?? "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>

      <section className="panel log-panel import-terminal" aria-label="实时日志终端">
        <div className="log-title">
          <span>实时日志</span>
          {stream.logs.length ? (
            <button className="button button-ghost" type="button" onClick={stream.clearLogs}>
              清除
            </button>
          ) : null}
        </div>
        <div className="log-lines">
          {stream.logs.length ? stream.logs.map((log, index) => <LogLine key={`${log.timestamp}-${index}`} log={log} />) : <EmptyState>任务日志会在导入开始后显示。</EmptyState>}
          <div ref={logEndRef} />
        </div>
      </section>
    </div>
  );
}

function LogLine({ log }: { log: LogEntry }) {
  const time = log.timestamp ? new Date(log.timestamp).toLocaleTimeString("zh-CN") : "";
  return (
    <div className={`log-line log-${log.level}`}>
      <span>{time}</span>
      <p>{log.message}</p>
    </div>
  );
}
