import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { PeerComparisonCard } from "@/components/metrics/PeerComparisonCard";
import {
  createEmptyPeerComparison,
  type PeerComparison,
} from "@/types/metrics";

describe("PeerComparisonCard", () => {
  it("renders fallback for missing data", () => {
    render(<PeerComparisonCard data={null} />);
    expect(screen.getByText("暂无可比较数据")).toBeTruthy();
  });

  it("supports switching between percentile and previous modes", () => {
    const data: PeerComparison = {
      ...createEmptyPeerComparison(),
      available: true,
      defaultMode: "percentile_band",
      percentileBand: {
        ...createEmptyPeerComparison().percentileBand,
        bandLabel: "Top 30%",
        myPercentile: 78.0,
        gapVsBandMedian: {
          ...createEmptyPeerComparison().percentileBand.gapVsBandMedian,
          score: 12,
        },
      },
      previousRanker: {
        ...createEmptyPeerComparison().previousRanker,
        available: true,
        scoreGap: 8,
        solvedGap: 1,
        metricGapVsPrevious: {
          ...createEmptyPeerComparison().previousRanker.metricGapVsPrevious,
          knowledge: 3,
        },
      },
    };

    render(<PeerComparisonCard data={data} />);

    expect(screen.getByText("Top 30% · 分位 78")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "前一名差距" }));
    expect(screen.getByText("与前一名差距：+8 分，+1 题")).toBeTruthy();
  });
});
