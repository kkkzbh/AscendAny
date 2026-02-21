import { useMemo, useState } from "react";
import type { PeerComparison, PeerMetricGap } from "@/types/metrics";

interface PeerComparisonCardProps {
  data: PeerComparison | null;
}

type ComparisonMode = "percentile_band" | "previous_ranker";

const METRIC_KEYS: Array<keyof PeerMetricGap> = [
  "score",
  "solved",
  "knowledge",
  "accuracy",
  "quality",
  "flexibility",
  "proficiency",
];

const METRIC_LABELS: Record<keyof PeerMetricGap, string> = {
  score: "总分",
  solved: "解题",
  knowledge: "知识",
  accuracy: "准确",
  quality: "质量",
  flexibility: "灵活",
  proficiency: "熟练",
};

function formatGap(value: number | null): string {
  if (value === null) return "暂无可比较数据";
  if (value > 0) return `+${value}`;
  return String(value);
}

function GapList({ gap }: { gap: PeerMetricGap }) {
  return (
    <div className="growth-gap-grid">
      {METRIC_KEYS.map((key) => (
        <div key={key} className="growth-gap-row">
          <span>{METRIC_LABELS[key]}</span>
          <span className="tabular-nums">{formatGap(gap[key])}</span>
        </div>
      ))}
    </div>
  );
}

export function PeerComparisonCard({ data }: PeerComparisonCardProps) {
  const initialMode = (data?.defaultMode ?? "percentile_band") as ComparisonMode;
  const [mode, setMode] = useState<ComparisonMode>(initialMode);

  const effectiveMode = useMemo<ComparisonMode>(() => {
    if (mode === "previous_ranker" && !data?.previousRanker.available) {
      return "percentile_band";
    }
    return mode;
  }, [data?.previousRanker.available, mode]);

  if (!data || !data.available) {
    return (
      <section className="metric-section growth-card rounded-xl p-3.5">
        <h3 className="growth-card-title">同层对比</h3>
        <p className="growth-card-empty">暂无可比较数据</p>
      </section>
    );
  }

  return (
    <section className="metric-section growth-card rounded-xl p-3.5">
      <div className="growth-card-head">
        <h3 className="growth-card-title">同层对比</h3>
        <div className="growth-toggle-group" role="tablist" aria-label="comparison mode">
          <button
            type="button"
            className={`growth-toggle-btn ${effectiveMode === "percentile_band" ? "active" : ""}`}
            onClick={() => setMode("percentile_band")}
          >
            分位区间
          </button>
          <button
            type="button"
            className={`growth-toggle-btn ${effectiveMode === "previous_ranker" ? "active" : ""}`}
            onClick={() => setMode("previous_ranker")}
            disabled={!data.previousRanker.available}
          >
            前一名差距
          </button>
        </div>
      </div>

      {effectiveMode === "percentile_band" ? (
        <div className="mt-1">
          <p className="growth-card-summary">
            {data.percentileBand.bandLabel}
            {typeof data.percentileBand.myPercentile === "number"
              ? ` · 分位 ${data.percentileBand.myPercentile}`
              : ""}
          </p>
          <GapList gap={data.percentileBand.gapVsBandMedian} />
        </div>
      ) : (
        <div className="mt-1">
          <p className="growth-card-summary">
            与前一名差距：
            {formatGap(data.previousRanker.scoreGap)} 分，
            {formatGap(data.previousRanker.solvedGap)} 题
          </p>
          <GapList gap={data.previousRanker.metricGapVsPrevious} />
        </div>
      )}
    </section>
  );
}
