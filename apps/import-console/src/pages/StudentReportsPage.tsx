import { useCallback, useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { getAdminStudentExamReports, listAdminStudents, type AdminStudentExamReport, type AdminStudentSummary } from "../api/admin";
import { generateExamAnalysis } from "../api/examAnalysis";
import { EmptyState, PageHeader, StatusBadge } from "../components/ui";

function formatDate(value: string | null): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
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

function formatDelta(value: number | null): string {
  if (value === null) return "-";
  return `${value > 0 ? "+" : ""}${value}`;
}

export function StudentReportsPage() {
  const [students, setStudents] = useState<AdminStudentSummary[]>([]);
  const [search, setSearch] = useState("");
  const [selectedStudentId, setSelectedStudentId] = useState("");
  const [reports, setReports] = useState<AdminStudentExamReport[]>([]);
  const [selectedExamId, setSelectedExamId] = useState("");
  const [loadingStudents, setLoadingStudents] = useState(false);
  const [loadingReports, setLoadingReports] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [generatingExamId, setGeneratingExamId] = useState<string | null>(null);

  const selectedStudent = useMemo(
    () => students.find((student) => student.studentEntityId === selectedStudentId) ?? null,
    [selectedStudentId, students],
  );
  const selectedReport = useMemo(
    () => reports.find((report) => report.examId === selectedExamId) ?? reports[0] ?? null,
    [reports, selectedExamId],
  );

  const loadStudents = useCallback(async (keyword: string) => {
    setLoadingStudents(true);
    setError(null);
    try {
      const response = await listAdminStudents(keyword);
      setStudents(response.items);
      setSelectedStudentId((current) => {
        if (current && response.items.some((item) => item.studentEntityId === current)) {
          return current;
        }
        return response.items[0]?.studentEntityId ?? "";
      });
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "学生列表加载失败");
    } finally {
      setLoadingStudents(false);
    }
  }, []);

  const loadReports = useCallback(async (studentEntityId: string) => {
    if (!studentEntityId) {
      setReports([]);
      setSelectedExamId("");
      return;
    }
    setLoadingReports(true);
    setError(null);
    try {
      const response = await getAdminStudentExamReports(studentEntityId);
      setReports(response.items);
      setSelectedExamId((current) => {
        if (current && response.items.some((item) => item.examId === current)) {
          return current;
        }
        return response.items[0]?.examId ?? "";
      });
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "考试报告加载失败");
    } finally {
      setLoadingReports(false);
    }
  }, []);

  useEffect(() => {
    void loadStudents("");
  }, [loadStudents]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadStudents(search);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [loadStudents, search]);

  useEffect(() => {
    void loadReports(selectedStudentId);
  }, [loadReports, selectedStudentId]);

  const generateSelectedExam = async (force: boolean) => {
    if (!selectedReport) return;
    setGeneratingExamId(selectedReport.examId);
    setError(null);
    try {
      await generateExamAnalysis(selectedReport.examId, { force });
      window.alert("分析任务已启动，请稍后刷新查看结果。");
    } catch (generateError) {
      setError(generateError instanceof Error ? generateError.message : "启动生成失败");
    } finally {
      setGeneratingExamId(null);
    }
  };

  return (
    <div className="page page-students">
      <PageHeader
        title="学生报告"
        description="按学生查看每次考试的指标、rating 变化与 Markdown 分析报告。"
        actions={
          <button className="button" type="button" onClick={() => void loadReports(selectedStudentId)} disabled={!selectedStudentId || loadingReports}>
            刷新报告
          </button>
        }
      />
      {error ? <div className="notice notice-error">{error}</div> : null}

      <div className="student-report-layout">
        <aside className="panel student-list-panel">
          <div className="panel-title">学生</div>
          <input
            aria-label="搜索学生"
            className="search-input"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="学号、姓名、账号"
          />
          <div className="student-list">
            {students.map((student) => (
              <button
                className={`student-row${student.studentEntityId === selectedStudentId ? " is-selected" : ""}`}
                key={student.studentEntityId}
                type="button"
                onClick={() => setSelectedStudentId(student.studentEntityId)}
              >
                <strong>{student.studentName || student.studentId || `实体 ${student.studentEntityId}`}</strong>
                <span>{student.studentId || "无学号"} · rating {student.rating}</span>
                <small>报告 {Math.round(student.reportCompletionRate * 100)}% · {student.examCount} 场</small>
              </button>
            ))}
            {!students.length && !loadingStudents ? <EmptyState>没有匹配的学生。</EmptyState> : null}
          </div>
        </aside>

        <section className="panel exam-list-panel">
          <div className="panel-title">
            {selectedStudent ? `${selectedStudent.studentName || selectedStudent.studentId || "学生"} 的考试` : "考试记录"}
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>考试</th>
                  <th>排名</th>
                  <th>分数</th>
                  <th>Rating</th>
                  <th>分析</th>
                </tr>
              </thead>
              <tbody>
                {reports.map((report) => (
                  <tr
                    className={report.examId === selectedReport?.examId ? "is-selected-row" : ""}
                    key={report.examId}
                    onClick={() => setSelectedExamId(report.examId)}
                  >
                    <td>
                      <strong>{report.examName}</strong>
                      <span className="muted-block">{formatDate(report.examDate)}</span>
                    </td>
                    <td>{report.rank ? `#${report.rank}` : "-"}</td>
                    <td>{formatMetric(report.totalScore)}</td>
                    <td>{formatDelta(report.ratingDelta)}</td>
                    <td><StatusBadge status={report.analysisStatus} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!reports.length && !loadingReports ? <EmptyState>该学生暂无考试记录。</EmptyState> : null}
          </div>
        </section>

        <aside className="panel report-panel">
          <div className="report-toolbar">
            <div>
              <div className="panel-title">{selectedReport?.examName ?? "报告详情"}</div>
              {selectedReport ? <span>{formatDate(selectedReport.examDate)}</span> : null}
            </div>
            <div className="report-actions">
              <button className="button" type="button" disabled={!selectedReport || generatingExamId !== null} onClick={() => void generateSelectedExam(false)}>
                {generatingExamId ? "启动中" : "生成"}
              </button>
              <button className="button" type="button" disabled={!selectedReport || generatingExamId !== null} onClick={() => void generateSelectedExam(true)}>
                重生成
              </button>
            </div>
          </div>

          {selectedReport ? (
            <>
              <div className="metric-strip">
                <span>K {formatMetric(selectedReport.knowledge)}</span>
                <span>A {formatMetric(selectedReport.accuracy)}</span>
                <span>Q {formatMetric(selectedReport.quality)}</span>
                <span>F {formatMetric(selectedReport.flexibility)}</span>
                <span>P {formatMetric(selectedReport.proficiency)}</span>
              </div>
              <div className="markdown-report">
                {selectedReport.analysisStatus === "failed" ? (
                  <div className="notice notice-error">{selectedReport.errorMessage || "分析生成失败"}</div>
                ) : selectedReport.analysisReply.trim() ? (
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{selectedReport.analysisReply}</ReactMarkdown>
                ) : (
                  <EmptyState>未生成分析报告。</EmptyState>
                )}
              </div>
            </>
          ) : (
            <EmptyState>选择学生和考试后查看报告。</EmptyState>
          )}
        </aside>
      </div>
    </div>
  );
}
