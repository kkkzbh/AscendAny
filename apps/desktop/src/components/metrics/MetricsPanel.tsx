import { useEffect, useState } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { RadarChart } from "./RadarChart";
import { RatingDisplay } from "./RatingDisplay";
import { MetricCard } from "./MetricCard";
import { RatingHistoryLineChart } from "./RatingHistoryLineChart";
import type { MetricName } from "@/types/metrics";

const METRIC_ORDER: MetricName[] = [
  "knowledge",
  "accuracy",
  "quality",
  "flexibility",
  "proficiency",
];

export function MetricsPanel() {
  const { metrics, metricMissing, rating, metricDelta, loading, error, loadDashboard } =
    useMetricsStore();
  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const [historyOpen, setHistoryOpen] = useState(true);
  const studentId = account?.studentId ?? undefined;
  const ptaNickname = account?.ptaNickname ?? undefined;

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadDashboard({ studentId, ptaNickname, authToken: accessToken ?? undefined });
    }, 280);

    return () => {
      window.clearTimeout(timer);
    };
  }, [studentId, ptaNickname, accessToken, loadDashboard]);

  if (loading && !metrics && !rating) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-[var(--text-soft)]">
        加载中...
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex h-full items-center justify-center px-6 text-center text-sm text-[var(--rating-negative)]">
        {error}
      </div>
    );
  }

  if (!metrics || !rating) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-[var(--text-soft)]">
        暂无数据
      </div>
    );
  }

  const historyItems = rating.history;

  return (
    <section className="metrics-sidebar-scroll h-full w-full overflow-y-auto">
      <div className="metrics-sidebar-content w-full p-4 pt-4">
        <div className="metric-section w-full overflow-visible">
          <div className="shrink-0 px-1 pt-2">
            <RatingDisplay rating={rating} />
          </div>

          <div className="shrink-0 px-3.5 pb-2">
            <RadarChart metrics={metrics} />
          </div>

          <div className="metric-bars shrink-0 border-t border-[var(--border-subtle)]">
            {METRIC_ORDER.map((name) => (
              <MetricCard
                key={name}
                name={name}
                value={metrics[name]}
                delta={metricDelta?.values[name] ?? 0}
                isMissing={metricMissing?.[name] ?? false}
              />
            ))}
          </div>

          <div className="border-t border-[var(--border-subtle)]" />

          <div className="rating-history-section">
            <button
              type="button"
              onClick={() => setHistoryOpen((open) => !open)}
              className="flex w-full items-center justify-between px-2 pb-1 text-left"
            >
              <h3
                className="rating-history-title text-[11px] font-semibold tracking-[0.12em] text-[var(--text-soft)] uppercase"
                style={{ paddingLeft: 12 }}
              >
                Rating 历史
              </h3>
              <div className="flex items-center gap-1 text-[10px] font-medium text-[var(--text-soft)]">
                <span>{historyItems.length}</span>
                <svg
                  width="12"
                  height="12"
                  viewBox="0 0 24 24"
                  className={`transition-transform duration-200 ${historyOpen ? "rotate-180" : ""}`}
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <polyline points="6 9 12 15 18 9" />
                </svg>
              </div>
            </button>

            {historyOpen && (
              <>
                <div className="rating-history-list rating-history-list--inner-scroll space-y-1 overflow-y-auto pr-1">
                  {historyItems.map((point) => (
                    <div
                      key={point.examId}
                      className="rating-history-row flex items-center justify-between text-xs transition-colors duration-150 hover:bg-[var(--surface-soft)]"
                    >
                      <div className="flex flex-col">
                        <span className="font-medium text-[var(--text-strong)]">
                          {point.examName}
                        </span>
                        <span className="text-[10px] text-[var(--text-soft)]">
                          {point.date}
                        </span>
                      </div>
                      <div className="flex items-center gap-1.5">
                        <span className="tabular-nums font-medium text-[var(--text-strong)]">
                          {point.newRating}
                        </span>
                        <span
                          className="min-w-[32px] px-1 py-0.5 text-center text-[10px] font-semibold tabular-nums"
                          style={{
                            color:
                              point.delta >= 0
                                ? "var(--rating-positive)"
                                : "var(--rating-negative)",
                            backgroundColor:
                              point.delta >= 0
                                ? "var(--rating-positive-soft)"
                                : "var(--rating-negative-soft)",
                          }}
                        >
                          {point.delta >= 0 ? "+" : ""}
                          {point.delta}
                        </span>
                      </div>
                    </div>
                  ))}
                </div>

                <RatingHistoryLineChart history={historyItems} />
              </>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}
