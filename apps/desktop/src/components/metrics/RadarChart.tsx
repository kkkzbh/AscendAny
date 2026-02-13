import {
  Radar,
  RadarChart as RechartsRadar,
  PolarGrid,
  PolarAngleAxis,
  ResponsiveContainer,
  Tooltip,
} from "recharts";
import type { StudentMetrics } from "@/types/metrics";
import { METRIC_LABELS, type MetricName } from "@/types/metrics";

interface RadarChartProps {
  metrics: StudentMetrics;
}

const METRIC_KEYS: MetricName[] = [
  "knowledge",
  "accuracy",
  "quality",
  "flexibility",
  "proficiency",
];

export function RadarChart({ metrics }: RadarChartProps) {
  const data = METRIC_KEYS.map((key) => ({
    metric: METRIC_LABELS[key],
    value: Math.round(metrics[key]),
    fullMark: 100,
  }));

  return (
    <div className="h-[220px] w-full">
      <ResponsiveContainer width="100%" height="100%">
        <RechartsRadar cx="50%" cy="50%" outerRadius="70%" data={data}>
          <PolarGrid
            stroke="var(--border)"
            strokeDasharray="3 3"
            strokeOpacity={0.7}
          />
          <PolarAngleAxis
            dataKey="metric"
            tick={{ fill: "var(--text-muted)", fontSize: 11, fontWeight: 500 }}
          />
          <Tooltip
            contentStyle={{
              background: "var(--tooltip-background)",
              backdropFilter: "blur(12px)",
              WebkitBackdropFilter: "blur(12px)",
              border: "1px solid var(--tooltip-border)",
              borderRadius: "8px",
              fontSize: "12px",
              boxShadow: "var(--tooltip-shadow)",
              color: "var(--text-strong)",
              padding: "6px 10px",
            }}
            formatter={(value: number) => [`${value}`, "得分"]}
          />
          <Radar
            name="能力"
            dataKey="value"
            stroke="var(--accent-600)"
            fill="var(--accent-500)"
            fillOpacity={0.16}
            strokeWidth={1.8}
          />
        </RechartsRadar>
      </ResponsiveContainer>
    </div>
  );
}
