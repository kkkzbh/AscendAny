import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  buildRatingTrendPoints,
  computeTrendChartWidth,
  RatingHistoryLineChart,
} from "@/components/metrics/RatingHistoryLineChart";
import type { RatingPoint } from "@/types/metrics";

const SAMPLE_HISTORY: RatingPoint[] = [
  {
    examId: "3",
    examName: "第3场",
    date: "2026-02-20",
    oldRating: 980,
    delta: 12,
    newRating: 992,
  },
  {
    examId: "2",
    examName: "第2场",
    date: "2026-02-13",
    oldRating: 962,
    delta: 18,
    newRating: 980,
  },
  {
    examId: "1",
    examName: "第1场",
    date: "2026-02-06",
    oldRating: 800,
    delta: 162,
    newRating: 962,
  },
];

describe("RatingHistoryLineChart helpers", () => {
  it("converts latest-first history into chronological trend points", () => {
    const trend = buildRatingTrendPoints(SAMPLE_HISTORY);
    expect(trend.map((item) => item.examId)).toEqual(["1", "2", "3"]);
    expect(trend.map((item) => item.shortDate)).toEqual([
      "02-06",
      "02-13",
      "02-20",
    ]);
  });

  it("computes chart width by point count and viewport width", () => {
    expect(computeTrendChartWidth(0, 0)).toBe(320);
    expect(computeTrendChartWidth(4, 360)).toBe(360);
    expect(computeTrendChartWidth(12, 360)).toBe(744);
  });
});

describe("RatingHistoryLineChart", () => {
  it("renders empty state when there is no history", () => {
    render(<RatingHistoryLineChart history={[]} />);
    expect(screen.getByText("暂无 rating 历史数据")).toBeTruthy();
  });
});
