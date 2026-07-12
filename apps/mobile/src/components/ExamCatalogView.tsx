import { useCallback, useEffect, useState } from "react";
import type { ExamDetail, ExamSummary } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadExam, loadExams } from "../api/operations";
import { useSession } from "../session/SessionContext";
import { ExamAnalysisGenerationPanel } from "./ExamAnalysisGenerationPanel";

export function ExamCatalogView() {
  const { session } = useSession();
  const [items, setItems] = useState<ExamSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [selected, setSelected] = useState<ExamDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadFirstPage = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const page = await loadExams(session);
      setItems(page.items);
      setCursor(page.nextCursor);
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoading(false);
    }
  }, [session]);

  useEffect(() => { void loadFirstPage(); }, [loadFirstPage]);

  const openExam = async (examId: string) => {
    setLoading(true);
    setError(null);
    try {
      setSelected(await loadExam(session, examId));
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoading(false);
    }
  };

  const loadMore = async () => {
    if (cursor === null || loadingMore) return;
    setLoadingMore(true);
    setError(null);
    try {
      const page = await loadExams(session, 20, cursor);
      setItems((current) => [...current, ...page.items]);
      setCursor(page.nextCursor);
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoadingMore(false);
    }
  };

  if (loading) return <section className="state-panel" role="status"><span className="loading-dot" /><p>正在读取考试题目集…</p></section>;
  if (error !== null && items.length === 0) return <section className="state-panel error-state"><h2>考试读取失败</h2><p>{error}</p><button className="secondary-button" type="button" onClick={() => void loadFirstPage()}>重试</button></section>;
  if (selected !== null) return <ExamDetailView exam={selected} session={session} onBack={() => setSelected(null)} />;

  return (
    <section className="panel-card">
      <div className="section-heading"><div><span className="eyebrow">EXAMS</span><h2>考试题目集</h2><p>当前活动快照</p></div><button className="text-button" type="button" onClick={() => void loadFirstPage()}>刷新</button></div>
      {items.length === 0 ? <p className="empty-copy">尚未导入考试。</p> : (
        <div className="mobile-exam-list">
          {items.map((exam) => (
            <button className="mobile-exam-card" type="button" key={exam.id} onClick={() => void openExam(exam.id)}>
              <span>快照 {exam.snapshotSequence}</span>
              <strong>{exam.title}</strong>
              <small>{exam.problemCount} 题 · {exam.participantCount} 人 · {exam.submissionCount} 次提交</small>
            </button>
          ))}
        </div>
      )}
      {error !== null ? <p className="inline-error" role="alert">{error}</p> : null}
      {cursor !== null ? <button className="secondary-button mobile-load-more" type="button" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? "正在加载…" : "加载更多"}</button> : null}
    </section>
  );
}

function ExamDetailView({ exam, session, onBack }: { exam: ExamDetail; session: ReturnType<typeof useSession>["session"]; onBack: () => void }) {
  return (
    <div className="view-stack">
      <section className="panel-card mobile-exam-detail">
        <button className="text-button" type="button" onClick={onBack}>← 返回考试列表</button>
        <span className="eyebrow">PINTIA · SNAPSHOT {exam.snapshotSequence}</span>
        <h2>{exam.title}</h2>
        <p>{exam.problemCount} 题 · {exam.participantCount} 人 · {exam.submissionCount} 次提交</p>
        <a className="secondary-button" href={exam.sourceUrl} target="_blank" rel="noreferrer">打开 Pintia 来源</a>
      </section>
      <ExamAnalysisGenerationPanel examId={exam.id} session={session} />
      <section className="panel-card">
        <div className="section-heading"><div><span className="eyebrow">PROBLEMS</span><h2>题目统计</h2></div></div>
        <div className="mobile-problem-list">
          {exam.problems.map((problem) => (
            <article key={problem.id}>
              <div><strong>{problem.label ?? problem.problemId} · {problem.title}</strong><span>{problem.maxScore ?? "—"} 分</span></div>
              <small>{problem.submissionCount} 提交 · {problem.submittingParticipantCount} 人尝试 · {problem.passedParticipantCount} 人通过</small>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
