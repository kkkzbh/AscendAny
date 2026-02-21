import { beforeEach, describe, expect, it, vi } from "vitest";

const { fetchStudentDashboard } = vi.hoisted(() => ({
  fetchStudentDashboard: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  fetchStudentDashboard,
  getApiErrorMessage: (error: unknown, fallback: string) =>
    error instanceof Error ? error.message : fallback,
}));

import { useMetricsStore } from "@/stores/metricsStore";

const BASE_DASHBOARD = {
  metrics: {
    knowledge: 70,
    accuracy: 65,
    quality: 60,
    flexibility: 58,
    proficiency: 72,
  },
  metricMissing: {
    knowledge: false,
    accuracy: false,
    quality: false,
    flexibility: false,
    proficiency: false,
  },
  rating: {
    current: 1012,
    lastDelta: 7,
    history: [],
  },
  metricDelta: {
    latestExamId: "11",
    latestExamName: "Exam 11",
    latestExamDate: "2026-02-11",
    baseline: "previous_exam" as const,
    values: {
      knowledge: 3,
      accuracy: -1,
      quality: 2,
      flexibility: 0,
      proficiency: 4,
    },
  },
  identity: {
    studentId: "20230001",
    ptaNickname: "Alice",
    noSubmissionRecords: false,
  },
};

describe("metricsStore", () => {
  beforeEach(() => {
    fetchStudentDashboard.mockReset();
    useMetricsStore.setState({
      metrics: null,
      metricMissing: null,
      rating: null,
      metricDelta: null,
      progressExplanation: null,
      milestoneStreak: null,
      peerComparison: null,
      postExamSupport: null,
      identity: null,
      loading: false,
      error: null,
    });
  });

  it("fills newly added growth fields when old payload misses them", async () => {
    fetchStudentDashboard.mockResolvedValue(BASE_DASHBOARD);

    await useMetricsStore.getState().loadDashboard({ studentId: "20230001" });

    const state = useMetricsStore.getState();
    expect(state.progressExplanation?.available).toBe(false);
    expect(state.milestoneStreak?.newMilestones).toEqual([]);
    expect(state.peerComparison?.defaultMode).toBe("percentile_band");
    expect(state.postExamSupport?.mode).toBe("steady");
  });
});
