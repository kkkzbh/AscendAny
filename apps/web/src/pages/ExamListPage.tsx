import { useCallback, useEffect, useState } from "react";
import type { ExamSummary } from "@ascendany/sdk";
import { Link } from "react-router-dom";
import { apiFailureMessage } from "../api/client";
import { loadExams } from "../api/operations";
import { useSession } from "../session/context";

function formatTime(value: string | null): string {
  if (value === null) return "时间未公开";
  return new Date(value).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function ExamListPage() {
  const { session } = useSession();
  const [items, setItems] = useState<ExamSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
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

  useEffect(() => {
    void loadFirstPage();
  }, [loadFirstPage]);

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

  if (loading) {
    return <section className="state-panel" role="status"><span className="loading-dot" /><p>正在读取考试题目集…</p></section>;
  }
  if (error !== null && items.length === 0) {
    return <section className="state-panel error-state"><h2>考试列表读取失败</h2><p>{error}</p><button className="secondary-button" type="button" onClick={() => void loadFirstPage()}>重试</button></section>;
  }

  return (
    <section className="panel-card">
      <header className="section-heading">
        <div><span className="eyebrow">EXAMS</span><h2>已导入考试</h2><p>每项展示当前活动快照。</p></div>
        <button className="text-button" type="button" onClick={() => void loadFirstPage()}>刷新数据</button>
      </header>
      {items.length === 0 ? <p className="empty-copy">尚未导入考试。</p> : (
        <div className="exam-grid">
          {items.map((exam) => <ExamCard exam={exam} key={exam.id} />)}
        </div>
      )}
      {error !== null ? <p className="inline-error" role="alert">{error}</p> : null}
      {cursor !== null ? <button className="secondary-button load-more-button" type="button" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? "正在加载…" : "加载更多"}</button> : null}
    </section>
  );
}

function ExamCard({ exam }: { exam: ExamSummary }) {
  return (
    <Link className="exam-card" to={`/exams/${exam.id}`}>
      <span className="exam-sequence">快照 {exam.snapshotSequence}</span>
      <h3>{exam.title}</h3>
      <p>{formatTime(exam.startsAt)} — {formatTime(exam.endsAt)}</p>
      <dl>
        <div><dt>题目</dt><dd>{exam.problemCount}</dd></div>
        <div><dt>参与者</dt><dd>{exam.participantCount}</dd></div>
        <div><dt>提交</dt><dd>{exam.submissionCount}</dd></div>
      </dl>
      <span className="exam-link">查看详情 →</span>
    </Link>
  );
}
