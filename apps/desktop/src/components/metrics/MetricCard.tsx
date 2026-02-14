import type { MetricName } from "@/types/metrics";
import { METRIC_LABELS, METRIC_COLORS } from "@/types/metrics";

interface MetricCardProps {
  name: MetricName;
  value: number;
  delta: number;
  isMissing?: boolean;
}

function clampPercent(value: number): number {
  return Math.max(0, Math.min(100, value));
}

function hexToRgba(hex: string, alpha: number): string {
  const normalized = hex.replace("#", "").trim();
  if (normalized.length !== 6) {
    return `rgba(2, 132, 199, ${alpha})`;
  }
  const r = Number.parseInt(normalized.slice(0, 2), 16);
  const g = Number.parseInt(normalized.slice(2, 4), 16);
  const b = Number.parseInt(normalized.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

export function MetricCard({ name, value, delta, isMissing = false }: MetricCardProps) {
  const label = METRIC_LABELS[name];
  const color = METRIC_COLORS[name];
  const roundedValue = clampPercent(Math.round(value));
  const roundedDelta = Math.round(delta);
  const previousValue = clampPercent(roundedValue - roundedDelta);
  const solidWidth = roundedDelta < 0 ? roundedValue : previousValue;
  const positiveSegmentWidth =
    roundedDelta > 0 ? Math.max(0, roundedValue - previousValue) : 0;
  const negativeSegmentWidth =
    roundedDelta < 0 ? Math.max(0, previousValue - roundedValue) : 0;
  const deltaText = isMissing
    ? "缺失"
    : roundedDelta > 0
      ? `+${roundedDelta}`
      : `${roundedDelta}`;
  const deltaStyle =
    isMissing
      ? {
          color: "var(--text-soft)",
          borderColor: "transparent",
          backgroundColor: "var(--surface-soft)",
        }
      : roundedDelta > 0
      ? {
          color: "var(--rating-positive)",
          borderColor: "transparent",
          backgroundColor: "var(--rating-positive-soft)",
        }
      : roundedDelta < 0
        ? {
            color: "var(--rating-negative)",
            borderColor: "transparent",
            backgroundColor: "var(--rating-negative-soft)",
          }
        : {
            color: "var(--metric-delta-neutral-text)",
            borderColor: "transparent",
            backgroundColor: "var(--metric-delta-neutral-bg)",
          };

  return (
    <div className="metric-row flex items-center gap-3 rounded-lg transition-colors duration-150 hover:bg-[var(--surface-soft)]">
      <div
        className="h-2 w-2 shrink-0 rounded-full"
        style={{ backgroundColor: color }}
      />
      <span className="w-8 text-xs text-[var(--text-muted)]">{label}</span>
      <span className="w-7 text-right text-xs font-bold tabular-nums text-[var(--text-strong)]">
        {isMissing ? "N/A" : roundedValue}
      </span>
      <div className="relative h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--surface-soft)]">
        {!isMissing && solidWidth > 0 && (
          <div
            className="absolute inset-y-0 left-0 rounded-full transition-all duration-500"
            style={{
              width: `${solidWidth}%`,
              backgroundColor: color,
            }}
          />
        )}
        {!isMissing && positiveSegmentWidth > 0 && (
          <div
            className="absolute inset-y-0 rounded-full transition-all duration-500"
            style={{
              left: `${previousValue}%`,
              width: `${positiveSegmentWidth}%`,
              backgroundColor: hexToRgba(color, 0.26),
            }}
          />
        )}
        {!isMissing && negativeSegmentWidth > 0 && (
          <div
            className="absolute inset-y-0 rounded-full border transition-all duration-500"
            style={{
              left: `${roundedValue}%`,
              width: `${negativeSegmentWidth}%`,
              borderColor: hexToRgba(color, 0.42),
              backgroundColor: "transparent",
            }}
          />
        )}
      </div>
      <span
        className="metric-delta-chip min-w-[40px] shrink-0 rounded px-1.5 py-0.5 text-center text-[10px] font-semibold tabular-nums"
        style={deltaStyle}
      >
        {deltaText}
      </span>
    </div>
  );
}
