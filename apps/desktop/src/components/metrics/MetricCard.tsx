import type { MetricName } from "@/types/metrics";
import { METRIC_LABELS, METRIC_COLORS } from "@/types/metrics";

interface MetricCardProps {
  name: MetricName;
  value: number;
}

export function MetricCard({ name, value }: MetricCardProps) {
  const label = METRIC_LABELS[name];
  const color = METRIC_COLORS[name];

  return (
    <div className="transition-all-smooth flex items-center gap-2.5 rounded-lg px-3 py-2 hover:bg-[var(--surface-hover)]">
      <div
        className="h-2 w-2 shrink-0 rounded-full"
        style={{ backgroundColor: color }}
      />
      <span className="w-8 text-xs text-[var(--text-secondary)]">{label}</span>
      <span className="w-7 text-right text-xs font-bold tabular-nums text-[var(--text-primary)]">
        {value}
      </span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-black/[0.04]">
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{
            width: `${value}%`,
            backgroundColor: color,
          }}
        />
      </div>
    </div>
  );
}
