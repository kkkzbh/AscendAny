import { useEffect } from "react";
import { useMetricsStore } from "@/stores/metricsStore";
import { RadarChart } from "./RadarChart";
import { RatingDisplay } from "./RatingDisplay";
import { MetricCard } from "./MetricCard";
import type { MetricName } from "@/types/metrics";

const METRIC_ORDER: MetricName[] = [
  "knowledge",
  "accuracy",
  "quality",
  "flexibility",
  "proficiency",
];

export function MetricsPanel() {
  const { metrics, rating, loadMockData } = useMetricsStore();

  useEffect(() => {
    loadMockData();
  }, [loadMockData]);

  if (!metrics || !rating) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-[var(--text-muted)]">
        加载中...
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col gap-2.5 overflow-y-auto p-3">
      {/* Rating */}
      <RatingDisplay rating={rating} />

      {/* Radar Chart */}
      <div className="rounded-xl bg-white/40 p-3 shadow-sm ring-1 ring-black/[0.04]">
        <h3 className="mb-0.5 text-[11px] font-semibold tracking-wider text-[var(--text-muted)] uppercase">
          能力雷达
        </h3>
        <RadarChart metrics={metrics} />
      </div>

      {/* Individual metric bars */}
      <div className="rounded-xl bg-white/40 py-1.5 shadow-sm ring-1 ring-black/[0.04]">
        {METRIC_ORDER.map((name) => (
          <MetricCard key={name} name={name} value={metrics[name]} />
        ))}
      </div>

      {/* Rating history */}
      <div className="rounded-xl bg-white/40 p-3 shadow-sm ring-1 ring-black/[0.04]">
        <h3 className="mb-1.5 text-[11px] font-semibold tracking-wider text-[var(--text-muted)] uppercase">
          Rating 历史
        </h3>
        <div className="space-y-0.5">
          {rating.history
            .slice()
            .reverse()
            .map((point) => (
              <div
                key={point.examId}
                className="transition-all-smooth flex items-center justify-between rounded-lg px-2 py-1.5 text-xs hover:bg-[var(--surface-hover)]"
              >
                <div className="flex flex-col">
                  <span className="font-medium text-[var(--text-primary)]">
                    {point.examName}
                  </span>
                  <span className="text-[10px] text-[var(--text-muted)]">
                    {point.date}
                  </span>
                </div>
                <div className="flex items-center gap-1.5">
                  <span className="tabular-nums font-medium text-[var(--text-primary)]">
                    {point.newRating}
                  </span>
                  <span
                    className="min-w-[32px] rounded px-1 py-0.5 text-center text-[10px] font-semibold tabular-nums"
                    style={{
                      color:
                        point.delta >= 0
                          ? "var(--rating-positive)"
                          : "var(--rating-negative)",
                      backgroundColor:
                        point.delta >= 0
                          ? "rgba(16, 185, 129, 0.08)"
                          : "rgba(244, 63, 94, 0.08)",
                    }}
                  >
                    {point.delta >= 0 ? "+" : ""}
                    {point.delta}
                  </span>
                </div>
              </div>
            ))}
        </div>
      </div>
    </div>
  );
}
