import { useCallback, useEffect, useState } from "react";
import type { SelfStudentAnalytics, StudentMetricValues } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadSelfAnalytics } from "../api/operations";
import { useSession } from "../session/SessionContext";
import { AchievementPanel } from "./AchievementPanel";
import { RecommendationPanel } from "./RecommendationPanel";

const metricKeys = ["knowledge", "accuracy", "quality", "flexibility", "proficiency"] as const;

const metricLabels: Record<(typeof metricKeys)[number], string> = {
  knowledge: "知识掌握",
  accuracy: "答题准确",
  quality: "代码质量",
  flexibility: "思维灵活",
  proficiency: "熟练程度",
};

function metricText(value: number | null): string {
  return value === null ? "—" : value.toFixed(1);
}

function formatEventTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function MetricGrid({ values }: { values: StudentMetricValues }) {
  return (
    <div className="metric-grid">
      {metricKeys.map((key) => (
        <article className="metric-card" key={key}>
          <span>{metricLabels[key]}</span>
          <strong>{metricText(values[key])}</strong>
        </article>
      ))}
    </div>
  );
}

export function AnalyticsView() {
  const { session } = useSession();
  const [analytics, setAnalytics] = useState<SelfStudentAnalytics | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setAnalytics(await loadSelfAnalytics(session));
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoading(false);
    }
  }, [session]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) return <LoadingPanel label="正在读取能力画像…" />;
  if (error !== null) return <ErrorPanel message={error} onRetry={load} />;
  if (analytics === null) return null;
  if (analytics.state === "not_generated") {
    return <div className="view-stack"><StatePanel title="分析尚未发布" detail="管理员完成首轮分析生成后，这里会显示能力画像。" /><AchievementPanel /><RecommendationPanel /></div>;
  }
  if (analytics.state === "no_observations") {
    return <div className="view-stack"><StatePanel title="暂无个人观测" detail={`已发布分析版本 ${analytics.headRevision}，当前账号尚无可计算的考试记录。`} /><AchievementPanel /><RecommendationPanel /></div>;
  }

  const examHistory = analytics.examHistory.slice(-8).reverse();
  const ratingByExam = new Map(analytics.ratingHistory.map((item) => [item.examId, item]));

  return (
    <div className="view-stack">
      <section className="rating-hero">
        <div>
          <span className="eyebrow">CURRENT RATING</span>
          <strong>{analytics.rating}</strong>
          <p>发布版本 {analytics.headRevision} · {formatEventTime(analytics.referenceTime)}</p>
        </div>
        <div className="rating-orbit" aria-hidden="true"><span /></div>
      </section>

      <section className="panel-card">
        <div className="section-heading">
          <div><span className="eyebrow">CAPABILITY</span><h2>五维能力</h2></div>
          <button className="text-button" type="button" onClick={() => void load()}>刷新</button>
        </div>
        <MetricGrid values={analytics.current} />
      </section>

      <section className="panel-card">
        <div className="section-heading"><div><span className="eyebrow">HISTORY</span><h2>最近考试</h2></div></div>
        {examHistory.length === 0 ? <p className="empty-copy">暂无考试历史。</p> : (
          <div className="history-list">
            {examHistory.map((exam) => {
              const rating = ratingByExam.get(exam.examId);
              return (
                <article className="history-item" key={exam.examId}>
                  <div><strong>{exam.title}</strong><span>{formatEventTime(exam.eventTime)}</span></div>
                  <div className="history-rating">
                    <strong>{rating?.newRating ?? "—"}</strong>
                    {rating ? <span className={rating.delta >= 0 ? "positive" : "negative"}>{rating.delta >= 0 ? "+" : ""}{rating.delta}</span> : null}
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </section>
      <AchievementPanel />
      <RecommendationPanel />
    </div>
  );
}

function LoadingPanel({ label }: { label: string }) {
  return <section className="state-panel" role="status"><span className="loading-dot" /> <p>{label}</p></section>;
}

function ErrorPanel({ message, onRetry }: { message: string; onRetry: () => Promise<void> }) {
  return <section className="state-panel error-state"><h2>读取失败</h2><p>{message}</p><button className="secondary-button" type="button" onClick={() => void onRetry()}>重试</button></section>;
}

function StatePanel({ title, detail }: { title: string; detail: string }) {
  return <section className="state-panel"><div className="state-symbol">◇</div><h2>{title}</h2><p>{detail}</p></section>;
}
