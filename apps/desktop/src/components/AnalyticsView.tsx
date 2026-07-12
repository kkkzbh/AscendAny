import { useCallback, useEffect, useMemo, useState } from "react";
import type {
  SelfStudentAnalytics,
  StudentMetricValues,
  StudentRatingHistoryPoint,
} from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadSelfAnalytics } from "../api/operations";
import { AchievementPanel } from "./AchievementPanel";
import { RecommendationPanel } from "./RecommendationPanel";
import { useSession } from "../session/context";

const metricKeys = [
  "knowledge",
  "accuracy",
  "quality",
  "flexibility",
  "proficiency",
] as const;

const metricLabels: Record<(typeof metricKeys)[number], string> = {
  knowledge: "知识掌握",
  accuracy: "答题准确",
  quality: "代码质量",
  flexibility: "思维灵活",
  proficiency: "熟练程度",
};

function formatMetric(value: number | null): string {
  return value === null ? "—" : value.toFixed(1);
}

function formatTime(value: string): string {
  return new Date(value).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
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

  if (loading) return <LoadingState label="正在读取能力画像…" />;
  if (error !== null) {
    return <ErrorState title="能力画像读取失败" message={error} onRetry={load} />;
  }
  if (analytics === null) return null;
  if (analytics.state === "not_generated") {
    return (
      <div className="view-stack">
        <EmptyState title="分析尚未发布" detail="管理员发布首轮分析后，这里会显示能力画像。" />
        <AchievementPanel />
        <RecommendationPanel />
      </div>
    );
  }
  if (analytics.state === "no_observations") {
    return (
      <div className="view-stack">
        <EmptyState title="暂无个人观测" detail={"已发布分析版本 " + analytics.headRevision + "，当前账号尚无可计算的考试记录。"} />
        <AchievementPanel />
        <RecommendationPanel />
      </div>
    );
  }

  const historyStart = Math.max(0, analytics.examHistory.length - 10);
  const recentExams = analytics.examHistory.slice(historyStart).reverse();
  const recentRatings = analytics.ratingHistory.slice(historyStart).reverse();

  return (
    <div className="view-stack">
      <section className="rating-hero">
        <div className="rating-summary">
          <span className="eyebrow">CURRENT RATING</span>
          <strong>{analytics.rating}</strong>
          <p>
            发布版本 {analytics.headRevision}
            <span aria-hidden="true"> · </span>
            {formatTime(analytics.referenceTime)}
          </p>
        </div>
        <RatingTrend history={analytics.ratingHistory} />
      </section>

      <section className="panel-card">
        <header className="section-heading">
          <div>
            <span className="eyebrow">CAPABILITY</span>
            <h2>五维能力</h2>
            <p>所有指标来自同一发布版本。</p>
          </div>
          <button className="text-button" type="button" onClick={() => void load()}>
            刷新数据
          </button>
        </header>
        <MetricGrid values={analytics.current} />
      </section>

      <section className="panel-card">
        <header className="section-heading">
          <div>
            <span className="eyebrow">EXAM HISTORY</span>
            <h2>最近考试</h2>
            <p>最多显示最近 10 场考试。</p>
          </div>
        </header>
        {recentExams.length === 0 ? (
          <p className="empty-copy">暂无考试历史。</p>
        ) : (
          <div className="history-list">
            {recentExams.map((exam, index) => {
              const rating = recentRatings[index];
              if (rating === undefined || rating.examId !== exam.examId) {
                throw new Error("Analytics history alignment contract was violated.");
              }
              return (
                <article className="history-item" key={exam.examId}>
                  <div className="history-main">
                    <strong>{exam.title}</strong>
                    <span>{formatTime(exam.eventTime)}</span>
                  </div>
                  <div className="history-metrics">
                    <span>准确 {formatMetric(exam.values.accuracy)}</span>
                    <span>质量 {formatMetric(exam.values.quality)}</span>
                  </div>
                  <div className="history-rating">
                    <strong>{rating.newRating}</strong>
                    <span className={rating.delta >= 0 ? "positive" : "negative"}>
                      {rating.delta >= 0 ? "+" : ""}{rating.delta}
                    </span>
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

function MetricGrid({ values }: { values: StudentMetricValues }) {
  return (
    <div className="metric-grid">
      {metricKeys.map((key, index) => {
        const value = values[key];
        return (
          <article className="metric-card" key={key}>
            <span className="metric-index">0{index + 1}</span>
            <span>{metricLabels[key]}</span>
            <strong>{formatMetric(value)}</strong>
            <div className="metric-track" aria-hidden="true">
              <span
                style={{
                  width:
                    value === null
                      ? "0%"
                      : String(Math.min(100, Math.max(0, value))) + "%",
                }}
              />
            </div>
          </article>
        );
      })}
    </div>
  );
}

function RatingTrend({ history }: { history: StudentRatingHistoryPoint[] }) {
  const points = useMemo(() => {
    if (history.length === 0) return "";
    const ratings = history.map((item) => item.newRating);
    const low = Math.min(...ratings);
    const high = Math.max(...ratings);
    const range = Math.max(1, high - low);
    return ratings.map((rating, index) => {
      const x = history.length === 1 ? 50 : (index / (history.length - 1)) * 100;
      const y = 76 - ((rating - low) / range) * 56;
      return String(x) + "," + String(y);
    }).join(" ");
  }, [history]);

  return (
    <div className="rating-chart" aria-label="Rating 历史趋势">
      {points.length === 0 ? (
        <span>暂无历史趋势</span>
      ) : (
        <svg viewBox="0 0 100 96" role="img" aria-label="Rating 折线图">
          <defs>
            <linearGradient id="desktop-rating-line" x1="0" x2="1">
              <stop offset="0" stopColor="#9a8cff" />
              <stop offset="1" stopColor="#35d0ba" />
            </linearGradient>
          </defs>
          <line x1="0" y1="76" x2="100" y2="76" className="chart-axis" />
          <polyline
            points={points}
            fill="none"
            stroke="url(#desktop-rating-line)"
            strokeWidth="3"
            vectorEffect="non-scaling-stroke"
          />
        </svg>
      )}
    </div>
  );
}

function LoadingState({ label }: { label: string }) {
  return (
    <section className="state-panel" role="status">
      <span className="loading-dot" />
      <p>{label}</p>
    </section>
  );
}

function ErrorState({
  title,
  message,
  onRetry,
}: {
  title: string;
  message: string;
  onRetry: () => Promise<void>;
}) {
  return (
    <section className="state-panel error-state">
      <h2>{title}</h2>
      <p>{message}</p>
      <button className="secondary-button" type="button" onClick={() => void onRetry()}>
        重试
      </button>
    </section>
  );
}

function EmptyState({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="state-panel">
      <div className="state-symbol" aria-hidden="true">◇</div>
      <h2>{title}</h2>
      <p>{detail}</p>
    </section>
  );
}
