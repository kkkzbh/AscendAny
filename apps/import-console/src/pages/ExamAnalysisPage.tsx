import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import {
  generateExamAnalysis,
  getExamAnalysisDetail,
  listExamAnalysisExams,
  type ExamAnalysisExamDetail,
  type ExamAnalysisExamItem,
  type ExamAnalysisStudentItem,
} from "../api/examAnalysis";
import { AdminHeader } from "../components/AdminHeader";
import type { AccountInfo } from "../hooks/useAuth";
import { useSSEStream } from "../hooks/useSSEStream";

type AnalysisStatus = "all" | "success" | "failed" | "missing";
type SortKey = "rank" | "studentId" | "ratingDelta" | "knowledge";

interface Props {
  account: AccountInfo | null;
  onLogout: () => void;
}

const EXAM_TYPE_LABELS: Record<string, string> = {
  datastructure: "数据结构月测",
  pta_icpc: "PTA ICPC",
  pta_ioi: "PTA IOI",
};

function formatDateTime(value: string | null): string {
  if (!value) return "未记录";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatMetric(value: number | null): string {
  if (value === null || Number.isNaN(value)) return "-";
  return Number.isInteger(value) ? String(value) : value.toFixed(1);
}

function formatRank(value: number | null): string {
  if (value === null || Number.isNaN(value)) return "-";
  return `#${value}`;
}

function formatDelta(value: number | null): string {
  if (value === null || Number.isNaN(value)) return "-";
  return `${value > 0 ? "+" : ""}${value}`;
}

function statusLabel(status: string): string {
  if (status === "success") return "成功";
  if (status === "failed") return "失败";
  if (status === "missing") return "缺失";
  return status;
}

function statusClassName(status: string): string {
  if (status === "success") return "status-pill status-pill--success";
  if (status === "failed") return "status-pill status-pill--error";
  if (status === "missing") return "status-pill status-pill--warning";
  return "status-pill status-pill--neutral";
}

function examTypeLabel(examType: string): string {
  return EXAM_TYPE_LABELS[examType] ?? examType;
}

export function stripMarkdown(value: string): string {
  return value
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\!\[[^\]]*\]\([^)]+\)/g, " ")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/[*_>#-]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function buildExcerpt(row: ExamAnalysisStudentItem): string {
  const content = stripMarkdown(row.analysisReply);
  if (content) {
    return content.length > 96 ? `${content.slice(0, 96)}...` : content;
  }
  if (row.errorMessage) {
    return `失败: ${row.errorMessage}`;
  }
  return "暂无分析结果";
}

function compareNullableNumber(a: number | null, b: number | null, fallback: number): number {
  const left = a ?? fallback;
  const right = b ?? fallback;
  if (left === right) return 0;
  return left > right ? 1 : -1;
}

function sortItems(items: ExamAnalysisStudentItem[], sortKey: SortKey): ExamAnalysisStudentItem[] {
  return [...items].sort((left, right) => {
    if (sortKey === "studentId") {
      return (left.studentId ?? "").localeCompare(right.studentId ?? "", "zh-CN");
    }
    if (sortKey === "ratingDelta") {
      return compareNullableNumber(right.ratingDelta, left.ratingDelta, Number.NEGATIVE_INFINITY);
    }
    if (sortKey === "knowledge") {
      return compareNullableNumber(right.knowledge, left.knowledge, Number.NEGATIVE_INFINITY);
    }
    const byRank = compareNullableNumber(left.rank, right.rank, Number.MAX_SAFE_INTEGER);
    if (byRank !== 0) return byRank;
    return (left.studentId ?? "").localeCompare(right.studentId ?? "", "zh-CN");
  });
}

export function filterAndSortExamAnalysisRows(
  items: ExamAnalysisStudentItem[],
  search: string,
  status: AnalysisStatus,
  sortKey: SortKey,
): ExamAnalysisStudentItem[] {
  const normalizedSearch = search.trim().toLowerCase();
  return sortItems(
    items.filter((item) => {
      if (status !== "all" && item.analysisStatus !== status) {
        return false;
      }
      if (!normalizedSearch) {
        return true;
      }
      return [
        item.studentId ?? "",
        item.studentName ?? "",
        item.studentEntityId,
        item.analysisReply,
        item.errorMessage ?? "",
      ]
        .join(" ")
        .toLowerCase()
        .includes(normalizedSearch);
    }),
    sortKey,
  );
}

export function ExamAnalysisPage({ account, onLogout }: Props) {
  const [examList, setExamList] = useState<ExamAnalysisExamItem[]>([]);
  const [examSearch, setExamSearch] = useState("");
  const [selectedExamId, setSelectedExamId] = useState("");
  const [detail, setDetail] = useState<ExamAnalysisExamDetail | null>(null);
  const [selectedStudentId, setSelectedStudentId] = useState("");
  const [studentSearch, setStudentSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<AnalysisStatus>("all");
  const [sortKey, setSortKey] = useState<SortKey>("rank");
  const [loadingExams, setLoadingExams] = useState(false);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [taskBusy, setTaskBusy] = useState(false);
  const handledStreamStatusRef = useRef<string>("idle");
  const logEndRef = useRef<HTMLDivElement>(null);
  const stream = useSSEStream();

  const loadExamList = useCallback(async () => {
    setLoadingExams(true);
    try {
      const response = await listExamAnalysisExams();
      setExamList(response.items);
      setSelectedExamId((current) => {
        if (current && response.items.some((item) => item.examId === current)) {
          return current;
        }
        return response.items[0]?.examId ?? "";
      });
      setError(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "考试列表加载失败");
    } finally {
      setLoadingExams(false);
    }
  }, []);

  const loadExamDetail = useCallback(async (examId: string) => {
    if (!examId) {
      setDetail(null);
      setSelectedStudentId("");
      return;
    }
    setLoadingDetail(true);
    try {
      const response = await getExamAnalysisDetail(examId);
      setDetail(response);
      setSelectedStudentId((current) => {
        if (current && response.items.some((item) => item.studentEntityId === current)) {
          return current;
        }
        return response.items[0]?.studentEntityId ?? "";
      });
      setError(null);
    } catch (loadError) {
      setDetail(null);
      setSelectedStudentId("");
      setError(loadError instanceof Error ? loadError.message : "考试详情加载失败");
    } finally {
      setLoadingDetail(false);
    }
  }, []);

  useEffect(() => {
    void loadExamList();
  }, [loadExamList]);

  useEffect(() => {
    if (!selectedExamId) {
      setDetail(null);
      return;
    }
    void loadExamDetail(selectedExamId);
  }, [loadExamDetail, selectedExamId]);

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [stream.logs]);

  useEffect(() => {
    if (handledStreamStatusRef.current === stream.status) {
      return;
    }
    handledStreamStatusRef.current = stream.status;
    if (stream.status === "done") {
      setTaskBusy(false);
      void loadExamList();
      if (selectedExamId) {
        void loadExamDetail(selectedExamId);
      }
    } else if (stream.status === "error") {
      setTaskBusy(false);
    }
  }, [loadExamDetail, loadExamList, selectedExamId, stream.status]);

  const filteredExamList = useMemo(() => {
    const normalizedSearch = examSearch.trim().toLowerCase();
    if (!normalizedSearch) {
      return examList;
    }
    return examList.filter((exam) =>
      [exam.examName, exam.examType, exam.examId]
        .join(" ")
        .toLowerCase()
        .includes(normalizedSearch),
    );
  }, [examList, examSearch]);

  const filteredRows = useMemo(
    () => filterAndSortExamAnalysisRows(detail?.items ?? [], studentSearch, statusFilter, sortKey),
    [detail?.items, sortKey, statusFilter, studentSearch],
  );

  const selectedStudent = useMemo(() => {
    if (filteredRows.length === 0) return null;
    return filteredRows.find((item) => item.studentEntityId === selectedStudentId) ?? filteredRows[0];
  }, [filteredRows, selectedStudentId]);

  const isStreaming = taskBusy || stream.status === "connecting" || stream.status === "streaming";

  const startGeneration = useCallback(
    async (force: boolean) => {
      if (!selectedExamId || isStreaming) {
        return;
      }
      setTaskBusy(true);
      handledStreamStatusRef.current = "idle";
      setError(null);
      stream.clearLogs();
      try {
        const response = await generateExamAnalysis(selectedExamId, { force });
        stream.connect(response.runId, "/api/v1/exam-analysis/runs/{run_id}/stream");
      } catch (runError) {
        setTaskBusy(false);
        setError(runError instanceof Error ? runError.message : "考试分析任务启动失败");
      }
    },
    [isStreaming, selectedExamId, stream],
  );

  return (
    <div className="console-page">
      <AdminHeader account={account} title="AscendAny 考试分析" onLogout={onLogout} />

      <div className="exam-analysis-page">
        <aside className="exam-analysis-sidebar">
          <div className="panel-header">
            <h2>考试列表</h2>
            <span className="badge badge-total">{examList.length}</span>
          </div>
          <div className="exam-analysis-sidebar-search">
            <input
              type="text"
              placeholder="搜索考试名或类型"
              aria-label="搜索考试"
              value={examSearch}
              onChange={(event) => setExamSearch(event.target.value)}
            />
          </div>
          <div className="exam-analysis-sidebar-list">
            {loadingExams ? <div className="empty-state">加载考试列表中...</div> : null}
            {!loadingExams && filteredExamList.length === 0 ? <div className="empty-state">没有匹配的考试</div> : null}
            {!loadingExams
              ? filteredExamList.map((exam) => (
                  <button
                    key={exam.examId}
                    type="button"
                    className={`exam-analysis-exam-card${exam.examId === selectedExamId ? " is-active" : ""}`}
                    onClick={() => setSelectedExamId(exam.examId)}
                  >
                    <div className="exam-analysis-exam-card-head">
                      <strong>{exam.examName}</strong>
                      <span className="badge badge-total">{exam.examType}</span>
                    </div>
                    <div className="exam-analysis-exam-card-meta">
                      <span>{examTypeLabel(exam.examType)}</span>
                      <span>{formatDateTime(exam.examDate)}</span>
                    </div>
                    <div className="exam-analysis-exam-card-stats">
                      <span className="status-pill status-pill--neutral">人数 {exam.participantCount}</span>
                      <span className="status-pill status-pill--success">已生成 {exam.generatedCount}</span>
                      <span className="status-pill status-pill--warning">缺失 {exam.missingCount}</span>
                      <span className="status-pill status-pill--error">失败 {exam.failedCount}</span>
                    </div>
                  </button>
                ))
              : null}
          </div>
        </aside>

        <main className="exam-analysis-main">
          {error ? <div className="alert alert-error">{error}</div> : null}

          {!selectedExamId && !loadingExams ? (
            <div className="empty-state empty-state-large">当前没有可查看的考试。</div>
          ) : null}

          {selectedExamId && detail ? (
            <>
              <section className="exam-analysis-summary">
                <div>
                  <h2>{detail.examName}</h2>
                  <p>
                    {examTypeLabel(detail.examType)} · {formatDateTime(detail.examDate)} · 考试 ID {detail.examId}
                  </p>
                </div>
                <div className="exam-analysis-actions">
                  <span className="status-pill status-pill--neutral">总人数 {detail.participantCount}</span>
                  <span className="status-pill status-pill--success">已生成 {detail.generatedCount}</span>
                  <span className="status-pill status-pill--warning">缺失 {detail.missingCount}</span>
                  <span className="status-pill status-pill--error">失败 {detail.failedCount}</span>
	                  <button
	                    type="button"
	                    className="btn btn-secondary"
	                    onClick={() => void startGeneration(false)}
	                    disabled={isStreaming || loadingDetail}
	                  >
	                    {isStreaming ? "生成中..." : "生成本场分析"}
	                  </button>
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={() => void startGeneration(true)}
                    disabled={isStreaming || loadingDetail}
                  >
                    强制重算
                  </button>
                </div>
              </section>

	              <section className="exam-analysis-toolbar">
	                <input
	                  type="text"
	                  placeholder="筛选当前考试中的学生或分析摘要"
	                  aria-label="搜索学生"
	                  value={studentSearch}
	                  onChange={(event) => setStudentSearch(event.target.value)}
                />
                <select
                  className="upload-select"
                  aria-label="状态筛选"
                  value={statusFilter}
                  onChange={(event) => setStatusFilter(event.target.value as AnalysisStatus)}
                >
                  <option value="all">全部状态</option>
                  <option value="success">成功</option>
                  <option value="missing">缺失</option>
                  <option value="failed">失败</option>
                </select>
                <select
                  className="upload-select"
                  aria-label="排序方式"
                  value={sortKey}
                  onChange={(event) => setSortKey(event.target.value as SortKey)}
                >
                  <option value="rank">按排名</option>
                  <option value="studentId">按学号</option>
                  <option value="ratingDelta">按 rating delta</option>
                  <option value="knowledge">按知识维度</option>
                </select>
              </section>

              <section className="exam-analysis-content">
                <div className="exam-analysis-table-card">
                  <div className="exam-analysis-table-wrap">
                    <table className="exam-analysis-table">
                      <thead>
                        <tr>
                          <th>学号</th>
                          <th>姓名</th>
                          <th>排名</th>
                          <th>总分</th>
                          <th>过题</th>
                          <th>rating delta</th>
                          <th>知识</th>
                          <th>准确</th>
                          <th>质量</th>
                          <th>灵活</th>
                          <th>熟练</th>
                          <th>状态</th>
                          <th>分析摘要</th>
                        </tr>
                      </thead>
                      <tbody>
                        {loadingDetail ? (
                          <tr>
                            <td className="table-empty-cell" colSpan={13}>加载考试详情中...</td>
                          </tr>
                        ) : null}
                        {!loadingDetail && filteredRows.length === 0 ? (
                          <tr>
                            <td className="table-empty-cell" colSpan={13}>当前筛选条件下没有学生记录。</td>
                          </tr>
                        ) : null}
                        {!loadingDetail
                          ? filteredRows.map((row) => {
                              const isActive = row.studentEntityId === selectedStudent?.studentEntityId;
                              const deltaClassName =
                                row.ratingDelta === null
                                  ? ""
                                  : row.ratingDelta >= 0
                                    ? "metric-positive"
                                    : "metric-negative";
                              return (
                                <tr
                                  key={row.studentEntityId}
                                  className={isActive ? "is-active" : ""}
                                  onClick={() => setSelectedStudentId(row.studentEntityId)}
                                >
                                  <td>{row.studentId ?? "-"}</td>
                                  <td>{row.studentName ?? "-"}</td>
                                  <td>{formatRank(row.rank)}</td>
                                  <td>{formatMetric(row.totalScore)}</td>
                                  <td>{formatMetric(row.solvedCount)}</td>
                                  <td className={deltaClassName}>{formatDelta(row.ratingDelta)}</td>
                                  <td>{formatMetric(row.knowledge)}</td>
                                  <td>{formatMetric(row.accuracy)}</td>
                                  <td>{formatMetric(row.quality)}</td>
                                  <td>{formatMetric(row.flexibility)}</td>
                                  <td>{formatMetric(row.proficiency)}</td>
                                  <td>
                                    <span className={statusClassName(row.analysisStatus)}>
                                      {statusLabel(row.analysisStatus)}
                                    </span>
                                  </td>
                                  <td className="analysis-excerpt-cell">{buildExcerpt(row)}</td>
                                </tr>
                              );
                            })
                          : null}
                      </tbody>
                    </table>
                  </div>
                </div>

                <aside className="exam-analysis-detail-card">
                  {selectedStudent ? (
                    <>
                      <div className="exam-analysis-detail-head">
                        <div>
                          <h3>{selectedStudent.studentName ?? selectedStudent.studentId ?? selectedStudent.studentEntityId}</h3>
                          <p>学号 {selectedStudent.studentId ?? "-"} · 实体 {selectedStudent.studentEntityId}</p>
                        </div>
                        <span className={statusClassName(selectedStudent.analysisStatus)}>
                          {statusLabel(selectedStudent.analysisStatus)}
                        </span>
                      </div>
                      <div className="exam-analysis-detail-grid">
                        <span>排名 {formatRank(selectedStudent.rank)}</span>
                        <span>总分 {formatMetric(selectedStudent.totalScore)}</span>
                        <span>过题 {formatMetric(selectedStudent.solvedCount)}</span>
                        <span
                          className={
                            selectedStudent.ratingDelta !== null && selectedStudent.ratingDelta < 0
                              ? "metric-negative"
                              : "metric-positive"
                          }
                        >
                          delta {formatDelta(selectedStudent.ratingDelta)}
                        </span>
                      </div>
                      <div className="exam-analysis-detail-meta">
                        <span>生成时间 {formatDateTime(selectedStudent.generatedAt)}</span>
                        {selectedStudent.errorMessage ? <span className="detail-error-text">错误: {selectedStudent.errorMessage}</span> : null}
                      </div>
	                      <div className="exam-analysis-detail-body">
	                        <div className="exam-analysis-markdown">
	                          {selectedStudent.analysisReply ? (
	                            <ReactMarkdown remarkPlugins={[remarkGfm]}>
	                              {selectedStudent.analysisReply}
	                            </ReactMarkdown>
	                          ) : (
	                            <div className="empty-state">当前没有可展示的分析结果</div>
	                          )}
	                        </div>
	                      </div>
	                    </>
	                  ) : (
                    <div className="empty-state">请选择一名学生查看完整分析。</div>
                  )}
                </aside>
              </section>

              <section className="exam-analysis-stream-card">
                <div className="panel-header">
                  <h2>生成进度</h2>
                  <span className="badge badge-total">{stream.status}</span>
                </div>

                {stream.progress ? (
                  <div className="exam-analysis-progress">
                    <div className="progress-bar-container">
                      <div
                        className="progress-bar-fill"
                        style={{
                          width: stream.progress.total > 0
                            ? `${(stream.progress.current / stream.progress.total) * 100}%`
                            : "0%",
                        }}
                      />
                    </div>
                    <span className="progress-text">
                      {stream.progress.current} / {stream.progress.total}
                      {stream.progress.phase ? ` · ${stream.progress.phase}` : ""}
                    </span>
                  </div>
                ) : null}

                {stream.status === "done" && stream.result ? (
                  <div className="exam-analysis-run-summary" aria-label="分析生成结果">
                    {"participants" in stream.result ? <span>参与 {String(stream.result.participants)}</span> : null}
                    {"generated" in stream.result ? <span>成功 {String(stream.result.generated)}</span> : null}
                    {"skipped" in stream.result ? <span>跳过 {String(stream.result.skipped)}</span> : null}
                    {"failed" in stream.result ? <span>失败 {String(stream.result.failed)}</span> : null}
                  </div>
                ) : null}

                {stream.errorMessage ? (
                  <div className="alert alert-error" style={{ marginTop: "12px" }}>
                    {stream.errorMessage}
                  </div>
                ) : null}

                <div className="log-console exam-analysis-log-terminal">
                  <div className="log-header">
                    <span>实时日志</span>
                    {stream.logs.length > 0 ? (
                      <button type="button" className="btn btn-ghost btn-xs" onClick={stream.clearLogs}>
                        清除
                      </button>
                    ) : null}
                  </div>
                  <div className="log-body">
                    {stream.logs.length === 0 ? <div className="log-empty">启动批量生成后，进度和日志会在这里持续刷新。</div> : null}
                    {stream.logs.map((log, index) => (
                      <div key={`${log.timestamp}-${index}`} className={`log-line log-${log.level}`}>
                        <span className="log-time">{new Date(log.timestamp).toLocaleTimeString("zh-CN")}</span>
                        <span className="log-msg">{log.message}</span>
                      </div>
                    ))}
                    <div ref={logEndRef} />
                  </div>
                </div>
              </section>
            </>
          ) : null}
        </main>
      </div>
    </div>
	);
}

export const filterAndSortRows = filterAndSortExamAnalysisRows;
