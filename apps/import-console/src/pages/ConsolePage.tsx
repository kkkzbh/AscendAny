import { useCallback, useEffect, useRef, useState } from "react";
import type { AccountInfo } from "../hooks/useAuth";
import { useSSEStream, type LogEntry } from "../hooks/useSSEStream";
import {
  discoverExams,
  startImportRun,
  startLinkActors,
  getIngestHistory,
  type DiscoverExamItem,
  type DiscoverResponse,
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

function examIcon(type: string): string {
  return EXAM_TYPE_LABELS[type]?.icon ?? "📄";
}

/* ── Props ────────────────────────────────────────────── */

interface Props {
  account: AccountInfo | null;
  onLogout: () => void;
}

/* ── Tab type ─────────────────────────────────────────── */

type Tab = "console" | "history";

/* ── Main Component ───────────────────────────────────── */

export function ConsolePage({ account, onLogout }: Props) {
  // ── State ──
  const [discover, setDiscover] = useState<DiscoverResponse | null>(null);
  const [discoverLoading, setDiscoverLoading] = useState(false);
  const [discoverError, setDiscoverError] = useState<string | null>(null);
  const [selectedTypes, setSelectedTypes] = useState<Set<string>>(new Set());
  const [expandedTypes, setExpandedTypes] = useState<Set<string>>(new Set());
  const [dryRun, setDryRun] = useState(false);
  const [force, setForce] = useState(false);
  const [activeTab, setActiveTab] = useState<Tab>("console");
  const [history, setHistory] = useState<IngestHistoryItem[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [helpOpen, setHelpOpen] = useState(() => {
    try { return !localStorage.getItem("ascendany-help-seen"); } catch { return true; }
  });
  const [taskBusy, setTaskBusy] = useState(false);

  const stream = useSSEStream();
  const logEndRef = useRef<HTMLDivElement>(null);

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

  // ── Discover ──
  const handleDiscover = useCallback(async () => {
    setDiscoverLoading(true);
    setDiscoverError(null);
    try {
      const res = await discoverExams();
      setDiscover(res);
      // Auto-expand all types
      setExpandedTypes(new Set(res.examTypes));
      // Auto-select types with changed exams
      const changedTypes = new Set(
        res.exams.filter((e) => e.hasChanged).map((e) => e.examType),
      );
      setSelectedTypes(changedTypes);
    } catch (err) {
      setDiscoverError(err instanceof Error ? err.message : "扫描失败");
    } finally {
      setDiscoverLoading(false);
    }
  }, []);

  // Auto-discover on mount
  useEffect(() => { handleDiscover(); }, [handleDiscover]);

  // ── Import ──
  const handleImport = useCallback(async () => {
    setTaskBusy(true);
    stream.clearLogs();
    try {
      const examTypes = selectedTypes.size > 0 ? Array.from(selectedTypes) : null;
      const res = await startImportRun({ examTypes, dryRun, force });
      stream.connect(res.runId, "/api/v1/import/run/{run_id}/stream");
    } catch (err) {
      stream.clearLogs();
      setTaskBusy(false);
      alert(err instanceof Error ? err.message : "启动导入失败");
    }
  }, [selectedTypes, dryRun, force, stream]);

  // ── Link Actors ──
  const handleLinkActors = useCallback(async () => {
    setTaskBusy(true);
    stream.clearLogs();
    try {
      const examTypes = selectedTypes.size > 0 ? Array.from(selectedTypes) : null;
      const res = await startLinkActors({ examTypes, dryRun });
      stream.connect(res.runId, "/api/v1/import/link-actors/{run_id}/stream");
    } catch (err) {
      stream.clearLogs();
      setTaskBusy(false);
      alert(err instanceof Error ? err.message : "启动关联失败");
    }
  }, [selectedTypes, dryRun, stream]);

  // Reset busy when stream done/error
  useEffect(() => {
    if (stream.status === "done" || stream.status === "error") {
      setTaskBusy(false);
    }
  }, [stream.status]);

  // ── History ──
  const loadHistory = useCallback(async () => {
    setHistoryLoading(true);
    try {
      const res = await getIngestHistory(30);
      setHistory(res.items);
    } catch {
      // silent
    } finally {
      setHistoryLoading(false);
    }
  }, []);

  useEffect(() => {
    if (activeTab === "history") loadHistory();
  }, [activeTab, loadHistory]);

  // ── Toggle helpers ──
  const toggleType = (type: string) => {
    setSelectedTypes((prev) => {
      const next = new Set(prev);
      if (next.has(type)) next.delete(type); else next.add(type);
      return next;
    });
  };

  const toggleExpand = (type: string) => {
    setExpandedTypes((prev) => {
      const next = new Set(prev);
      if (next.has(type)) next.delete(type); else next.add(type);
      return next;
    });
  };

  // Group exams by type
  const examsByType: Record<string, DiscoverExamItem[]> = {};
  if (discover) {
    for (const exam of discover.exams) {
      (examsByType[exam.examType] ??= []).push(exam);
    }
  }

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
        {/* ── Left Panel: Discover ── */}
        <aside className="panel-discover">
          <div className="panel-header">
            <h2>📂 考试发现</h2>
            <button
              className="btn btn-sm btn-primary"
              onClick={handleDiscover}
              disabled={discoverLoading}
            >
              {discoverLoading ? "扫描中..." : "🔄 刷新扫描"}
            </button>
          </div>

          {discoverError && (
            <div className="alert alert-error">{discoverError}</div>
          )}

          {discover && (
            <div className="discover-summary">
              共 <strong>{discover.totalCount}</strong> 场考试，
              <strong className="text-changed">{discover.changedCount}</strong> 场有变更
            </div>
          )}

          <div className="exam-type-list">
            {(discover?.examTypes ?? []).map((type) => {
              const exams = examsByType[type] ?? [];
              const changedCount = exams.filter((e) => e.hasChanged).length;
              const isExpanded = expandedTypes.has(type);
              const isSelected = selectedTypes.has(type);

              return (
                <div key={type} className="exam-type-group">
                  <div className="exam-type-header">
                    <label className="exam-type-checkbox">
                      <input
                        type="checkbox"
                        checked={isSelected}
                        onChange={() => toggleType(type)}
                      />
                      <span className="exam-type-icon">{examIcon(type)}</span>
                      <span className="exam-type-label">{examLabel(type)}</span>
                    </label>
                    <span className="exam-type-stats">
                      {changedCount > 0 && (
                        <span className="badge badge-changed">{changedCount} 变更</span>
                      )}
                      <span className="badge badge-total">{exams.length} 场</span>
                    </span>
                    <button
                      className="btn btn-icon"
                      onClick={() => toggleExpand(type)}
                      title={isExpanded ? "折叠" : "展开"}
                    >
                      {isExpanded ? "▾" : "▸"}
                    </button>
                  </div>

                  {isExpanded && (
                    <div className="exam-list">
                      {exams.map((exam) => (
                        <div
                          key={exam.sourcePath}
                          className={`exam-item ${exam.hasChanged ? "exam-changed" : "exam-synced"}`}
                        >
                          <span className="exam-status-dot">{exam.hasChanged ? "🟡" : "🟢"}</span>
                          <span className="exam-path" title={exam.sourcePath}>
                            {exam.sourcePath.split("/").pop()}
                          </span>
                          <span className="exam-file-count">{exam.fileCount} 文件</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>

          {!discoverLoading && discover?.totalCount === 0 && (
            <div className="empty-message">未发现任何考试数据</div>
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
                <button
                  className="btn btn-secondary"
                  onClick={handleLinkActors}
                  disabled={taskBusy || isStreaming}
                >
                  🔗 关联 Actor
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
                      </>
                    )}
                    {"matched" in stream.result && (
                      <>
                        <div className="result-item result-success">
                          <span className="result-label">匹配</span>
                          <span className="result-value">{String(stream.result.matched)}</span>
                        </div>
                        <div className="result-item">
                          <span className="result-label">更新</span>
                          <span className="result-value">{String(stream.result.updated)}</span>
                        </div>
                        <div className="result-item result-warn">
                          <span className="result-label">模糊</span>
                          <span className="result-value">{String(stream.result.ambiguous)}</span>
                        </div>
                        <div className="result-item result-error">
                          <span className="result-label">未匹配</span>
                          <span className="result-value">{String(stream.result.remainingUnmatched)}</span>
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
                      点击「开始增量导入」或「关联 Actor」开始操作，日志将在此处实时显示...
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
