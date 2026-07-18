import { create } from "zustand";
import {
  fetchStudentDashboard,
  getApiErrorMessage,
} from "@/lib/api";
import {
  createEmptyMilestoneStreak,
  createEmptyPeerComparison,
  createEmptyPostExamSupport,
  createEmptyProgressExplanation,
} from "@/types/metrics";
import type {
  MilestoneStreak,
  MetricDeltaInfo,
  MetricMissingValues,
  PeerComparison,
  PostExamSupport,
  ProgressExplanation,
  StudentIdentity,
  StudentMetrics,
  RatingInfo,
} from "@/types/metrics";

interface DashboardQuery {
  studentId?: string;
  ptaNickname?: string;
  authToken?: string;
}

function roundMetric(value: number): number {
  return Math.round(value);
}

function normalizeMetrics(metrics: StudentMetrics): StudentMetrics {
  return {
    knowledge: roundMetric(metrics.knowledge),
    accuracy: roundMetric(metrics.accuracy),
    quality: roundMetric(metrics.quality),
    flexibility: roundMetric(metrics.flexibility),
    proficiency: roundMetric(metrics.proficiency),
  };
}

function normalize(value?: string): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

interface MetricsState {
  metrics: StudentMetrics | null;
  metricMissing: MetricMissingValues | null;
  rating: RatingInfo | null;
  metricDelta: MetricDeltaInfo | null;
  progressExplanation: ProgressExplanation | null;
  milestoneStreak: MilestoneStreak | null;
  peerComparison: PeerComparison | null;
  postExamSupport: PostExamSupport | null;
  identity: StudentIdentity | null;
  loading: boolean;
  error: string | null;
  loadDashboard: (query: DashboardQuery) => Promise<void>;
}

export const useMetricsStore = create<MetricsState>()((set) => ({
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

  loadDashboard: async (query) => {
    const studentId = normalize(query.studentId);
    const ptaNickname = normalize(query.ptaNickname);

    set((state) => ({
      ...state,
      loading: true,
      error: null,
    }));

    try {
      const response = await fetchStudentDashboard({
        studentId,
        ptaNickname,
        authToken: query.authToken,
      });
      set({
        metrics: normalizeMetrics(response.metrics),
        metricMissing: response.metricMissing,
        rating: response.rating,
        metricDelta: response.metricDelta,
        progressExplanation:
          response.progressExplanation ?? createEmptyProgressExplanation(),
        milestoneStreak:
          response.milestoneStreak ?? createEmptyMilestoneStreak(),
        peerComparison: response.peerComparison ?? createEmptyPeerComparison(),
        postExamSupport: response.postExamSupport ?? createEmptyPostExamSupport(),
        identity: response.identity,
        loading: false,
        error: null,
      });
    } catch (error) {
      set({
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
        error: getApiErrorMessage(
          error,
          "加载失败，请检查后端服务是否可用。",
        ),
      });
    }
  },
}));
