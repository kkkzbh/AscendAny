import { useMemo } from "react";
import {
  Area,
  AreaChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import type { KnowledgeNodeRecentDay } from "@/types/path";

interface MiniSeriesChartProps {
  data: KnowledgeNodeRecentDay[];
}

interface ChartPoint {
  date: string;
  attempted: number;
  correct: number;
  short: string;
}

export function MiniSeriesChart({ data }: MiniSeriesChartProps) {
  const points = useMemo<ChartPoint[]>(
    () =>
      (data ?? []).map((day) => ({
        date: day.date,
        attempted: day.attempted,
        correct: day.correct,
        short: day.date.slice(5),
      })),
    [data],
  );

  if (points.length === 0) {
    return (
      <div className="path-mini-chart-empty">
        最近 7 天暂无该知识点的练习记录
      </div>
    );
  }

  return (
    <div className="path-mini-chart">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart
          data={points}
          margin={{ top: 6, right: 8, bottom: 0, left: -28 }}
        >
          <defs>
            <linearGradient id="path-mini-attempted" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--star-current)" stopOpacity={0.55} />
              <stop offset="100%" stopColor="var(--star-current)" stopOpacity={0} />
            </linearGradient>
            <linearGradient id="path-mini-correct" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--star-mastered)" stopOpacity={0.55} />
              <stop offset="100%" stopColor="var(--star-mastered)" stopOpacity={0} />
            </linearGradient>
          </defs>
          <XAxis
            dataKey="short"
            stroke="var(--text-soft)"
            tick={{ fontSize: 10 }}
            tickLine={false}
            axisLine={false}
          />
          <YAxis
            stroke="var(--text-soft)"
            tick={{ fontSize: 10 }}
            tickLine={false}
            axisLine={false}
            width={28}
            allowDecimals={false}
          />
          <Tooltip
            contentStyle={{
              background: "var(--surface-raised)",
              border: "1px solid var(--border-subtle)",
              borderRadius: 10,
              fontSize: 12,
              padding: "6px 10px",
            }}
            labelStyle={{ color: "var(--text-strong)", fontWeight: 600 }}
            formatter={(value: number, name: string) => {
              const display = name === "attempted" ? "尝试" : "正确";
              return [value, display];
            }}
          />
          <Area
            type="monotone"
            dataKey="attempted"
            stroke="var(--star-current)"
            strokeWidth={2}
            fill="url(#path-mini-attempted)"
            isAnimationActive={false}
          />
          <Area
            type="monotone"
            dataKey="correct"
            stroke="var(--star-mastered)"
            strokeWidth={2}
            fill="url(#path-mini-correct)"
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
