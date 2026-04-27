import { useEffect, type ReactNode } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useLayoutStore, type RightPanelTab } from "@/stores/layoutStore";
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
  const activeTab = useLayoutStore((s) => s.activeRightPanelTab);
  const setActiveTab = useLayoutStore((s) => s.setActiveRightPanelTab);
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

  const historyItems = rating?.history ?? [];

  const tabs: Array<{ id: RightPanelTab; label: string }> = [
    { id: "ability", label: "能力" },
    { id: "history", label: "历史" },
  ];

  let content: ReactNode;
  if (loading && !metrics && !rating) {
    content = (
      <div className="student-right-empty">
        加载中...
      </div>
    );
  } else if (error) {
    content = (
      <div className="student-right-empty text-[var(--rating-negative)]">
        {error}
      </div>
    );
  } else if (!metrics || !rating) {
    content = (
      <div className="student-right-empty">
        暂无数据
      </div>
    );
  } else if (activeTab === "history") {
    content = (
      <div className="student-right-content">
        {historyItems.length > 0 ? (
          <>
            <RatingHistoryLineChart history={historyItems} />
            <div className="rating-history-list space-y-1">
              {historyItems.map((point) => (
                <div
                  key={point.examId}
                  className="rating-history-row flex items-center justify-between text-xs transition-colors duration-150 hover:bg-[var(--surface-soft)]"
                >
                  <div className="flex min-w-0 flex-col">
                    <span className="truncate font-medium text-[var(--text-strong)]">
                      {point.examName}
                    </span>
                    <span className="text-[10px] text-[var(--text-soft)]">
                      {point.date}
                    </span>
                  </div>
                  <div className="flex shrink-0 items-center gap-1.5">
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
          </>
        ) : (
          <div className="student-right-empty">暂无历史</div>
        )}
      </div>
    );
  } else {
    content = (
      <div className="student-right-content">
        <RatingDisplay rating={rating} />
        <RadarChart metrics={metrics} />
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
      </div>
    );
  }

  return (
    <section className="student-right-panel">
      <div className="student-right-tabs">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            type="button"
            className={`student-right-tab ${activeTab === tab.id ? "is-active" : ""}`}
            onClick={() => setActiveTab(tab.id)}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div className="metrics-sidebar-scroll student-right-scroll">
        {content}
      </div>
    </section>
  );
}
