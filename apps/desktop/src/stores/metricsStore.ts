import { create } from "zustand";
import {
  fetchStudentDashboard,
  getApiErrorMessage,
} from "@/lib/api";
import type {
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
  rating: RatingInfo | null;
  identity: StudentIdentity | null;
  loading: boolean;
  error: string | null;
  loadDashboard: (query: DashboardQuery) => Promise<void>;
}

export const useMetricsStore = create<MetricsState>()((set) => ({
  metrics: null,
  rating: null,
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
        rating: response.rating,
        identity: response.identity,
        loading: false,
        error: null,
      });
    } catch (error) {
      set({
        metrics: null,
        rating: null,
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
