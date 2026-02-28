import { useEffect, useMemo, useRef, useState } from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import type { RatingPoint } from "@/types/metrics";

const CHART_HEIGHT = 176;
const MIN_CHART_WIDTH = 320;
const MIN_SIDE_PADDING = 16;
const POINT_STEP = 62;
const Y_AXIS_PADDING = 24;

export interface RatingTrendPoint {
  examId: string;
  examName: string;
  date: string;
  shortDate: string;
  rating: number;
  delta: number;
}

interface RatingHistoryLineChartProps {
  history: RatingPoint[];
}

interface TooltipPayloadItem {
  payload: RatingTrendPoint;
}

interface RatingTrendTooltipProps {
  active?: boolean;
  payload?: TooltipPayloadItem[];
}

function formatShortDate(input: string): string {
  const text = input.trim();
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text);
  if (!match) {
    return text;
  }
  return `${match[2]}-${match[3]}`;
}

export function buildRatingTrendPoints(history: RatingPoint[]): RatingTrendPoint[] {
  return [...history].reverse().map((point) => ({
    examId: point.examId,
    examName: point.examName,
    date: point.date,
    shortDate: formatShortDate(point.date),
    rating: point.newRating,
    delta: point.delta,
  }));
}

export function computeTrendChartWidth(
  pointCount: number,
  viewportWidth: number,
): number {
  const pointsWidth =
    pointCount <= 1 ? MIN_CHART_WIDTH : pointCount * POINT_STEP;
  const viewportDriven = Math.max(0, viewportWidth - MIN_SIDE_PADDING * 2);
  return Math.max(MIN_CHART_WIDTH, pointsWidth, viewportDriven + MIN_SIDE_PADDING * 2);
}

function RatingTrendTooltip({ active, payload }: RatingTrendTooltipProps) {
  if (!active || !payload || payload.length === 0) {
    return null;
  }

  const first = payload[0];
  if (!first?.payload) {
    return null;
  }
  const point = first.payload;
  const deltaPrefix = point.delta >= 0 ? "+" : "";

  return (
    <div className="rating-trend-tooltip">
      <div className="rating-trend-tooltip-title">{point.examName}</div>
      <div className="rating-trend-tooltip-row">{point.date}</div>
      <div className="rating-trend-tooltip-row">
        Rating: {point.rating}
      </div>
      <div className="rating-trend-tooltip-row">
        变化: {deltaPrefix}
        {point.delta}
      </div>
    </div>
  );
}

export function RatingHistoryLineChart({ history }: RatingHistoryLineChartProps) {
  const [viewportWidth, setViewportWidth] = useState(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const trend = useMemo(() => buildRatingTrendPoints(history), [history]);

  useEffect(() => {
    const element = scrollRef.current;
    if (!element) {
      return;
    }

    const update = () => {
      setViewportWidth(element.clientWidth);
    };

    update();

    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", update);
      return () => {
        window.removeEventListener("resize", update);
      };
    }

    const observer = new ResizeObserver(() => update());
    observer.observe(element);
    return () => {
      observer.disconnect();
    };
  }, []);

  if (trend.length === 0) {
    return (
      <section className="rating-trend-section" aria-label="rating-trend-empty">
        <h4 className="rating-trend-title">Rating 趋势</h4>
        <p className="rating-trend-empty">暂无 rating 历史数据</p>
      </section>
    );
  }

  const chartWidth = computeTrendChartWidth(trend.length, viewportWidth);
  const values = trend.map((item) => item.rating);
  const minValue = Math.min(...values);
  const maxValue = Math.max(...values);
  const yDomainMin = Math.floor((minValue - Y_AXIS_PADDING) / 10) * 10;
  const yDomainMax = Math.ceil((maxValue + Y_AXIS_PADDING) / 10) * 10;

  return (
    <section className="rating-trend-section" aria-label="rating-trend">
      <h4 className="rating-trend-title">Rating 趋势</h4>
      <div ref={scrollRef} className="rating-trend-scroll">
        <div className="rating-trend-chart" style={{ width: `${chartWidth}px` }}>
          <LineChart
            width={chartWidth}
            height={CHART_HEIGHT}
            data={trend}
            margin={{
              top: 10,
              right: MIN_SIDE_PADDING,
              bottom: 6,
              left: MIN_SIDE_PADDING,
            }}
          >
            <CartesianGrid
              stroke="var(--border)"
              strokeDasharray="3 3"
              strokeOpacity={0.65}
              vertical={false}
            />
            <XAxis
              dataKey="shortDate"
              tickLine={false}
              axisLine={false}
              minTickGap={22}
              interval="preserveStartEnd"
              tick={{
                fill: "var(--text-soft)",
                fontSize: 10,
              }}
            />
            <YAxis
              width={44}
              tickLine={false}
              axisLine={false}
              domain={[yDomainMin, yDomainMax]}
              tick={{
                fill: "var(--text-soft)",
                fontSize: 10,
              }}
            />
            <Tooltip
              cursor={{
                stroke: "var(--accent-500)",
                strokeOpacity: 0.25,
              }}
              content={<RatingTrendTooltip />}
            />
            <Line
              type="monotone"
              dataKey="rating"
              stroke="var(--accent-600)"
              strokeWidth={2}
              dot={{
                r: 2.2,
                strokeWidth: 1.2,
                stroke: "var(--surface-raised)",
                fill: "var(--accent-500)",
              }}
              activeDot={{
                r: 4,
                strokeWidth: 2,
                stroke: "var(--surface-raised)",
                fill: "var(--accent-600)",
              }}
            />
          </LineChart>
        </div>
      </div>
    </section>
  );
}
