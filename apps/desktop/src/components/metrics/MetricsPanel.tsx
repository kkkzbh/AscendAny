import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useLayoutStore, type RightPanelTab } from "@/stores/layoutStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { RadarChart } from "./RadarChart";
import { RatingDisplay } from "./RatingDisplay";
import { MetricCard } from "./MetricCard";
import { RatingHistoryLineChart } from "./RatingHistoryLineChart";
import { NotesWorkspace } from "@/components/notes/NotesWorkspace";
import { PathPanel } from "@/components/path/PathPanel";
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
  const [activeHistoryExamId, setActiveHistoryExamId] = useState<string | null>(null);
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
    { id: "path", label: "地图" },
    { id: "notes", label: "笔记" },
  ];
  const activeIndex = tabs.findIndex((tab) => tab.id === activeTab);

  // 测量当前选中 tab 的位置和宽度，驱动滑块（indicator）的 transform / width 过渡
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const [indicator, setIndicator] = useState({ left: 0, width: 0, ready: false });
  useLayoutEffect(() => {
    const el = tabRefs.current[activeIndex];
    if (el) {
      setIndicator({ left: el.offsetLeft, width: el.offsetWidth, ready: true });
    }
  }, [activeIndex]);

  // 跟踪 tab 切换方向，决定内容区域从哪一侧滑入
  const prevIndexRef = useRef(activeIndex);
  const [slideDirection, setSlideDirection] = useState<"forward" | "backward">("forward");
  useEffect(() => {
    if (activeIndex !== prevIndexRef.current) {
      setSlideDirection(activeIndex > prevIndexRef.current ? "forward" : "backward");
      prevIndexRef.current = activeIndex;
    }
  }, [activeIndex]);

  let content: ReactNode;
  if (activeTab === "notes") {
    content = <NotesWorkspace />;
  } else if (activeTab === "path") {
    content = <PathPanel />;
  } else if (loading && !metrics && !rating) {
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
            <RatingHistoryLineChart
              history={historyItems}
              activeExamId={activeHistoryExamId}
            />
            <div className="rating-history-list space-y-1">
              {historyItems.map((point) => (
                <div
                  key={point.examId}
                  className="rating-history-row flex items-center justify-between text-xs transition-colors duration-150 hover:bg-[var(--surface-soft)]"
                  onMouseEnter={() => setActiveHistoryExamId(point.examId)}
                  onMouseLeave={() => setActiveHistoryExamId(null)}
                  onFocus={() => setActiveHistoryExamId(point.examId)}
                  onBlur={() => setActiveHistoryExamId(null)}
                  tabIndex={0}
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
        <span
          className="student-right-tab-indicator"
          style={{
            transform: `translateX(${indicator.left}px)`,
            width: `${indicator.width}px`,
            opacity: indicator.ready ? 1 : 0,
          }}
          aria-hidden
        />
        {tabs.map((tab, index) => (
          <button
            key={tab.id}
            ref={(el) => {
              tabRefs.current[index] = el;
            }}
            type="button"
            className={`student-right-tab ${activeTab === tab.id ? "is-active" : ""}`}
            onClick={() => setActiveTab(tab.id)}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div className="metrics-sidebar-scroll student-right-scroll">
        <div
          key={activeTab}
          className={`student-right-pane student-right-pane--${slideDirection}`}
        >
          {content}
        </div>
      </div>
    </section>
  );
}
