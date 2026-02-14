import type { MetricName } from "@/types/metrics";
import { METRIC_LABELS, METRIC_COLORS } from "@/types/metrics";

interface MetricCardProps {
  name: MetricName;
  value: number;
  delta: number;
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

export function MetricCard({ name, value, delta }: MetricCardProps) {
  const label = METRIC_LABELS[name];
  const color = METRIC_COLORS[name];
  const roundedValue = Math.round(value);
  const roundedDelta = Math.round(delta);
  const deltaText = roundedDelta > 0 ? `+${roundedDelta}` : `${roundedDelta}`;
  const deltaStyle =
    roundedDelta > 0
      ? {
          color,
          borderColor: "transparent",
          backgroundColor: hexToRgba(color, 0.14),
        }
      : roundedDelta < 0
        ? {
            color,
            borderColor: color,
            backgroundColor: "transparent",
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
        {roundedValue}
      </span>
      <div className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--surface-soft)]">
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{
            width: `${roundedValue}%`,
            backgroundColor: color,
          }}
        />
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
