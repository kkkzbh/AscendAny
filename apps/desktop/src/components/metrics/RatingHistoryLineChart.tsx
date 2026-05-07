import { useEffect, useMemo, useRef, useState } from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceDot,
  ReferenceLine,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import type { RatingPoint } from "@/types/metrics";

const CHART_HEIGHT = 176;
const MIN_CHART_WIDTH = 320;
const CHART_RIGHT_MARGIN = 14;
const Y_AXIS_WIDTH = 36;
const X_AXIS_MIN_TICK_GAP = 12;
const X_AXIS_PADDING = { left: 6, right: 30 };
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
  activeExamId?: string | null;
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
  _pointCount: number,
  viewportWidth: number,
): number {
  return Math.max(MIN_CHART_WIDTH, viewportWidth);
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

function RatingTrendPointDetails({ point }: { point: RatingTrendPoint }) {
  const deltaPrefix = point.delta >= 0 ? "+" : "";

  return (
    <div className="rating-trend-highlight-tooltip" role="status" aria-label="考试详情">
      <div className="rating-trend-tooltip-title">{point.examName}</div>
      <div className="rating-trend-tooltip-row">{point.date}</div>
      <div className="rating-trend-tooltip-row">Rating: {point.rating}</div>
      <div className="rating-trend-tooltip-row">
        变化: {deltaPrefix}
        {point.delta}
      </div>
    </div>
  );
}

export function RatingHistoryLineChart({
  history,
  activeExamId = null,
}: RatingHistoryLineChartProps) {
  const [viewportWidth, setViewportWidth] = useState(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const trend = useMemo(() => buildRatingTrendPoints(history), [history]);
  const activePoint = useMemo(
    () => trend.find((item) => item.examId === activeExamId) ?? null,
    [activeExamId, trend],
  );

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
              right: CHART_RIGHT_MARGIN,
              bottom: 6,
              left: 0,
            }}
          >
            <CartesianGrid
              stroke="var(--border)"
              strokeDasharray="3 3"
              strokeOpacity={0.65}
              vertical={false}
            />
            <XAxis
              dataKey="examId"
              tickLine={false}
              axisLine={false}
              minTickGap={X_AXIS_MIN_TICK_GAP}
              interval="preserveStartEnd"
              padding={X_AXIS_PADDING}
              tickFormatter={(examId) =>
                trend.find((item) => item.examId === examId)?.shortDate ?? String(examId)
              }
              tick={{
                fill: "var(--text-soft)",
                fontSize: 10,
              }}
            />
            <YAxis
              width={Y_AXIS_WIDTH}
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
            {activePoint ? (
              <>
                <ReferenceLine
                  x={activePoint.examId}
                  stroke="var(--accent-500)"
                  strokeOpacity={0.26}
                  strokeDasharray="3 3"
                  ifOverflow="visible"
                />
                <ReferenceDot
                  x={activePoint.examId}
                  y={activePoint.rating}
                  r={6}
                  isFront
                  fill="var(--accent-600)"
                  stroke="var(--surface-raised)"
                  strokeWidth={2}
                  ifOverflow="visible"
                />
              </>
            ) : null}
            <Line
              type="monotone"
              dataKey="rating"
              stroke="var(--accent-600)"
              strokeWidth={2}
              dot={{
                r: 4,
                strokeWidth: 1.6,
                stroke: "var(--surface-raised)",
                fill: "var(--accent-500)",
              }}
              activeDot={{
                r: 6,
                strokeWidth: 2,
                stroke: "var(--surface-raised)",
                fill: "var(--accent-600)",
              }}
            />
          </LineChart>
          {activePoint ? <RatingTrendPointDetails point={activePoint} /> : null}
        </div>
      </div>
    </section>
  );
}
