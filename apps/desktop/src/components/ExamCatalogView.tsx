import { useCallback, useEffect, useState } from "react";
import type { BrowserSession, ExamDetail, ExamSummary } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadExam, loadExams } from "../api/operations";
import { ExamAnalysisGenerationPanel } from "./ExamAnalysisGenerationPanel";

export function ExamCatalogView({ session }: { session: BrowserSession }) {
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
    <section className="panel-card desktop-exam-catalog">
      <header className="section-heading"><div><span className="eyebrow">EXAMS</span><h2>考试题目集</h2><p>读取当前活动快照及其分析 generation。</p></div><button className="text-button" type="button" onClick={() => void loadFirstPage()}>刷新</button></header>
      {items.length === 0 ? <p className="empty-copy">尚未导入考试。</p> : (
        <div className="desktop-exam-grid">
          {items.map((exam) => (
            <button type="button" key={exam.id} onClick={() => void openExam(exam.id)}>
              <span>SNAPSHOT {exam.snapshotSequence}</span>
              <strong>{exam.title}</strong>
              <small>{exam.problemCount} 题 · {exam.participantCount} 人 · {exam.submissionCount} 次提交</small>
            </button>
          ))}
        </div>
      )}
      {error === null ? null : <p className="inline-error" role="alert">{error}</p>}
      {cursor === null ? null : <button className="secondary-button desktop-exam-load-more" type="button" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? "正在加载…" : "加载更多"}</button>}
    </section>
  );
}

function ExamDetailView({
  exam,
  session,
  onBack,
}: {
  exam: ExamDetail;
  session: BrowserSession;
  onBack: () => void;
}) {
  return (
    <div className="view-stack desktop-exam-detail">
      <section className="panel-card desktop-exam-hero">
        <button className="text-button" type="button" onClick={onBack}>← 返回考试列表</button>
        <span className="eyebrow">PINTIA · SNAPSHOT {exam.snapshotSequence}</span>
        <h2>{exam.title}</h2>
        <p>{exam.problemCount} 题 · {exam.participantCount} 人 · {exam.rankingCount} 条排名 · {exam.submissionCount} 次提交</p>
        <a className="secondary-button" href={exam.sourceUrl} target="_blank" rel="noreferrer">打开 Pintia 来源</a>
      </section>
      <ExamAnalysisGenerationPanel examId={exam.id} session={session} />
      <section className="panel-card">
        <header className="section-heading"><div><span className="eyebrow">PROBLEMS</span><h2>题目统计</h2></div></header>
        <div className="desktop-exam-problems">
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
