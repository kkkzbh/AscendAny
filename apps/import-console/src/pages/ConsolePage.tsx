import { useCallback, useEffect, useRef, useState, type DragEvent } from "react";
import type { AccountInfo } from "../hooks/useAuth";
import { useSSEStream, type LogEntry } from "../hooks/useSSEStream";
import {
  EXAM_TYPES,
  uploadExamZip,
  startImportRun,
  getIngestHistory,
  type UploadResponse,
  type IngestHistoryItem,
} from "../api/import";
import { HelpDrawer } from "../components/HelpDrawer";

/* ── Exam type display mapping ────────────────────────── */

const EXAM_TYPE_LABELS: Record<string, { label: string; icon: string }> = {
  datastructure: { label: "数据结构月测", icon: "📚" },
  pta_icpc: { label: "PTA ICPC 题目集", icon: "🏆" },
  pta_ioi: { label: "PTA IOI 题目集", icon: "📝" },
};

function examLabel(type: string): string {
  return EXAM_TYPE_LABELS[type]?.label ?? type;
}

/* ── Props ────────────────────────────────────────────── */

interface Props {
  account: AccountInfo | null;
  onLogout: () => void;
}

/* ── Tab type ─────────────────────────────────────────── */

type Tab = "console" | "history";

/* ── Upload state ─────────────────────────────────────── */

interface UploadedExam {
  examType: string;
  examName: string;
  sourcePath: string;
  fileCount: number;
}

/* ── Main Component ───────────────────────────────────── */

export function ConsolePage({ account, onLogout }: Props) {
  // ── Upload state ──
  const [selectedType, setSelectedType] = useState<string>(EXAM_TYPES[0].value);
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadPct, setUploadPct] = useState(0);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [uploadedExams, setUploadedExams] = useState<UploadedExam[]>([]);

  // ── Task state ──
  const [dryRun, setDryRun] = useState(false);
  const [force, setForce] = useState(false);
  const [activeTab, setActiveTab] = useState<Tab>("console");
  const [history, setHistory] = useState<IngestHistoryItem[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [helpOpen, setHelpOpen] = useState(() => {
    try { return !localStorage.getItem("ascendany-help-seen"); } catch { return true; }
  });
  const [taskBusy, setTaskBusy] = useState(false);

  const stream = useSSEStream();
  const logEndRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Auto-scroll log
  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [stream.logs]);

  // Mark help as seen
  useEffect(() => {
    if (!helpOpen) {
      try { localStorage.setItem("ascendany-help-seen", "1"); } catch { /* noop */ }
    }
  }, [helpOpen]);

  // ── Drag & Drop handlers ──
  const handleDragOver = useCallback((e: DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(true);
  }, []);

  const handleDragLeave = useCallback((e: DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);
  }, []);

  const handleDrop = useCallback(async (e: DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);

    const files = Array.from(e.dataTransfer.files).filter(
      (f) => f.name.toLowerCase().endsWith(".zip"),
    );
    if (files.length === 0) {
      setUploadError("请拖入 .zip 文件");
      return;
    }

    await doUpload(files);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedType]);

  const handleFileSelect = useCallback(async () => {
    const files = fileInputRef.current?.files;
    if (!files || files.length === 0) return;
    await doUpload(Array.from(files));
    if (fileInputRef.current) fileInputRef.current.value = "";
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedType]);

  const doUpload = async (files: File[]) => {
    setUploadError(null);
    setUploading(true);
    setUploadPct(0);

    const results: UploadedExam[] = [];
    try {
      for (const file of files) {
        setUploadPct(0);
        const res: UploadResponse = await uploadExamZip(file, selectedType, (pct) => setUploadPct(pct));
        results.push({
          examType: res.examType,
          examName: res.examName,
          sourcePath: res.sourcePath,
          fileCount: res.fileCount,
        });
      }
      setUploadedExams((prev) => [...prev, ...results]);
    } catch (err) {
      setUploadError(err instanceof Error ? err.message : "上传失败");
    } finally {
      setUploading(false);
      setUploadPct(0);
    }
  };

  // ── Remove uploaded exam from list ──
  const removeUploaded = (idx: number) => {
    setUploadedExams((prev) => prev.filter((_, i) => i !== idx));
  };

  const clearUploaded = () => setUploadedExams([]);

  // ── Import ──
  const handleImport = useCallback(async () => {
    setTaskBusy(true);
    stream.clearLogs();
    try {
      // Use types from uploaded exams if any, or selected type
      const types = uploadedExams.length > 0
        ? [...new Set(uploadedExams.map((e) => e.examType))]
        : [selectedType];
      const res = await startImportRun({ examTypes: types, dryRun, force });
      stream.connect(res.runId, "/api/v1/import/run/{run_id}/stream");
    } catch (err) {
      stream.clearLogs();
      setTaskBusy(false);
      alert(err instanceof Error ? err.message : "启动导入失败");
    }
  }, [uploadedExams, selectedType, dryRun, force, stream]);

  // Reset busy when stream done/error
  useEffect(() => {
    if (stream.status === "done" || stream.status === "error") {
      setTaskBusy(false);
    }
  }, [stream.status]);

  // ── History ──
  const loadHistory = useCallback(async () => {
    setHistoryLoading(true);
    setHistoryError(null);
    try {
      const res = await getIngestHistory(30);
      setHistory(res.items);
    } catch (err) {
      setHistoryError(err instanceof Error ? err.message : "历史记录加载失败");
    } finally {
      setHistoryLoading(false);
    }
  }, []);

  useEffect(() => {
    if (activeTab === "history") loadHistory();
  }, [activeTab, loadHistory]);

  const isStreaming = stream.status === "connecting" || stream.status === "streaming";

  return (
    <div className="console-page">
      {/* ── Top Bar ── */}
      <header className="topbar">
        <div className="topbar-left">
          <span className="topbar-logo">🔧</span>
          <h1>AscendAny 数据导入控制台</h1>
        </div>
        <div className="topbar-right">
          <button className="btn btn-ghost" onClick={() => setHelpOpen(true)} title="帮助">
            ❓ 帮助
          </button>
          <span className="topbar-user">👤 {account?.username ?? "admin"}</span>
          <button className="btn btn-ghost" onClick={onLogout}>退出</button>
        </div>
      </header>

      <div className="console-body">
        {/* ── Left Panel: Upload ── */}
        <aside className="panel-upload">
          <div className="panel-header">
            <h2>📤 上传考试数据</h2>
          </div>

          {/* Exam type selector */}
          <div className="upload-type-selector">
            <label className="upload-label">考试类型</label>
            <select
              className="upload-select"
              value={selectedType}
              onChange={(e) => setSelectedType(e.target.value)}
              disabled={uploading}
            >
              {EXAM_TYPES.map((t) => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
          </div>

          {/* Drop zone */}
          <div
            className={`drop-zone ${dragOver ? "drop-zone-active" : ""} ${uploading ? "drop-zone-uploading" : ""}`}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            onClick={() => !uploading && fileInputRef.current?.click()}
          >
            <input
              ref={fileInputRef}
              type="file"
              accept=".zip"
              multiple
              style={{ display: "none" }}
              onChange={handleFileSelect}
            />
            {uploading ? (
              <div className="drop-zone-uploading-content">
                <div className="upload-spinner" />
                <span>上传中 {uploadPct}%</span>
                <div className="upload-progress-bar">
                  <div className="upload-progress-fill" style={{ width: `${uploadPct}%` }} />
                </div>
              </div>
            ) : (
              <>
                <span className="drop-zone-icon">📁</span>
                <span className="drop-zone-text">拖入 .zip 文件</span>
                <span className="drop-zone-hint">或点击选择文件</span>
              </>
            )}
          </div>

          {uploadError && (
            <div className="alert alert-error" style={{ margin: "0 16px" }}>
              {uploadError}
            </div>
          )}

          {/* Uploaded exams list */}
          {uploadedExams.length > 0 && (
            <div className="uploaded-list">
              <div className="uploaded-list-header">
                <span>已上传 ({uploadedExams.length})</span>
                <button className="btn btn-ghost btn-xs" onClick={clearUploaded}>清空</button>
              </div>
              {uploadedExams.map((exam, idx) => (
                <div key={idx} className="uploaded-item">
                  <span className="uploaded-item-icon">
                    {EXAM_TYPE_LABELS[exam.examType]?.icon ?? "📄"}
                  </span>
                  <div className="uploaded-item-info">
                    <span className="uploaded-item-name">{exam.examName}</span>
                    <span className="uploaded-item-meta">
                      {examLabel(exam.examType)} · {exam.fileCount} 文件
                    </span>
                  </div>
                  <button
                    className="btn btn-icon btn-xs"
                    onClick={() => removeUploaded(idx)}
                    title="移除"
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
          )}
        </aside>

        {/* ── Right Panel: Actions & Log ── */}
        <main className="panel-main">
          {/* Tab bar */}
          <div className="tab-bar">
            <button
              className={`tab ${activeTab === "console" ? "tab-active" : ""}`}
              onClick={() => setActiveTab("console")}
            >
              🖥️ 控制台
            </button>
            <button
              className={`tab ${activeTab === "history" ? "tab-active" : ""}`}
              onClick={() => setActiveTab("history")}
            >
              📋 历史记录
            </button>
          </div>

          {activeTab === "console" && (
            <div className="console-tab">
              {/* Action bar */}
              <div className="action-bar">
                <button
                  className="btn btn-primary btn-lg"
                  onClick={handleImport}
                  disabled={taskBusy || isStreaming}
                >
                  {isStreaming ? "⏳ 导入中..." : "🚀 开始增量导入"}
                </button>
                <div className="action-options">
                  <label className="option-toggle" title="仅扫描不写入数据库，预览将要导入的内容">
                    <input
                      type="checkbox"
                      checked={dryRun}
                      onChange={(e) => setDryRun(e.target.checked)}
                      disabled={isStreaming}
                    />
                    <span>Dry Run</span>
                  </label>
                  <label className="option-toggle" title="忽略指纹比较，强制重新处理所有考试">
                    <input
                      type="checkbox"
                      checked={force}
                      onChange={(e) => setForce(e.target.checked)}
                      disabled={isStreaming}
                    />
                    <span>Force</span>
                  </label>
                </div>
              </div>

              {/* Progress bar */}
              {stream.progress && (
                <div className="progress-section">
                  <div className="progress-bar-container">
                    <div
                      className="progress-bar-fill"
                      style={{
                        width: `${stream.progress.total > 0 ? (stream.progress.current / stream.progress.total) * 100 : 0}%`,
                      }}
                    />
                  </div>
                  <span className="progress-text">
                    {stream.progress.current} / {stream.progress.total}
                    {stream.progress.examType && ` — ${examLabel(stream.progress.examType)}`}
                  </span>
                </div>
              )}

              {/* Result summary */}
              {stream.status === "done" && stream.result && (
                <div className="result-summary">
                  <h3>✅ 任务完成</h3>
                  <div className="result-grid">
                    {"scanned" in stream.result && (
                      <>
                        <div className="result-item">
                          <span className="result-label">扫描</span>
                          <span className="result-value">{String(stream.result.scanned)}</span>
                        </div>
                        <div className="result-item">
                          <span className="result-label">跳过</span>
                          <span className="result-value">{String(stream.result.skipped)}</span>
                        </div>
                        <div className="result-item result-success">
                          <span className="result-label">成功</span>
                          <span className="result-value">{String(stream.result.succeeded)}</span>
                        </div>
                        <div className="result-item result-error">
                          <span className="result-label">失败</span>
                          <span className="result-value">{String(stream.result.failed)}</span>
                        </div>
                        <div className="result-item result-success">
                          <span className="result-label">提交已绑定</span>
                          <span className="result-value">
                            {String(stream.result.submissionsBound ?? 0)}
                          </span>
                        </div>
                        <div className="result-item result-warn">
                          <span className="result-label">待认领提交</span>
                          <span className="result-value">
                            {String(stream.result.submissionsPendingClaim ?? 0)}
                          </span>
                        </div>
                        <div className="result-item result-error">
                          <span className="result-label">昵称冲突</span>
                          <span className="result-value">
                            {String(stream.result.nicknameConflicts ?? 0)}
                          </span>
                        </div>
                      </>
                    )}
                  </div>
                </div>
              )}

              {/* Log console */}
              <div className="log-console">
                <div className="log-header">
                  <span>📟 实时日志</span>
                  {stream.logs.length > 0 && (
                    <button className="btn btn-ghost btn-xs" onClick={stream.clearLogs}>
                      清除
                    </button>
                  )}
                </div>
                <div className="log-body">
                  {stream.logs.length === 0 && (
                    <div className="log-empty">
                      上传 .zip 后点击「开始增量导入」，日志将在此处实时显示...
                    </div>
                  )}
                  {stream.logs.map((log, i) => (
                    <LogLine key={i} log={log} />
                  ))}
                  <div ref={logEndRef} />
                </div>
              </div>
            </div>
          )}

          {activeTab === "history" && (
            <div className="history-tab">
              <div className="history-header">
                <h3>📋 导入历史记录</h3>
                <button
                  className="btn btn-sm btn-secondary"
                  onClick={loadHistory}
                  disabled={historyLoading}
                >
                  {historyLoading ? "加载中..." : "刷新"}
                </button>
              </div>
              {historyError && (
                <div className="alert alert-error" style={{ marginBottom: "12px" }}>
                  历史记录加载失败：{historyError}
                </div>
              )}
              {history.length === 0 && !historyLoading && (
                <div className="empty-message">暂无导入记录</div>
              )}
              <div className="history-table-wrapper">
                <table className="history-table">
                  <thead>
                    <tr>
                      <th>ID</th>
                      <th>状态</th>
                      <th>开始时间</th>
                      <th>完成时间</th>
                      <th>扫描</th>
                      <th>处理</th>
                      <th>成功</th>
                      <th>失败</th>
                    </tr>
                  </thead>
                  <tbody>
                    {history.map((item) => (
                      <tr key={item.ingestRunId}>
                        <td>{item.ingestRunId}</td>
                        <td>
                          <span className={`status-badge status-${item.status}`}>
                            {item.status}
                          </span>
                        </td>
                        <td>{formatTime(item.startedAt)}</td>
                        <td>{formatTime(item.finishedAt)}</td>
                        <td>{item.scanned ?? "-"}</td>
                        <td>{item.toProcess ?? "-"}</td>
                        <td className="text-success">{item.succeeded ?? "-"}</td>
                        <td className="text-error">{item.failed ?? "-"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </main>
      </div>

      {/* Help Drawer */}
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}

/* ── Sub-components ───────────────────────────────────── */

function LogLine({ log }: { log: LogEntry }) {
  const time = log.timestamp
    ? new Date(log.timestamp).toLocaleTimeString("zh-CN")
    : "";
  return (
    <div className={`log-line log-${log.level}`}>
      <span className="log-time">{time}</span>
      <span className="log-msg">{log.message}</span>
    </div>
  );
}

function formatTime(iso: string | null): string {
  if (!iso) return "-";
  try {
    return new Date(iso).toLocaleString("zh-CN", {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}
