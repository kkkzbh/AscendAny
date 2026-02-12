import { create } from "zustand";
import type { StudentMetrics, RatingInfo } from "@/types/metrics";
import { MOCK_METRICS, MOCK_RATING } from "@/lib/mock";

interface MetricsState {
  metrics: StudentMetrics | null;
  rating: RatingInfo | null;
  loading: boolean;
  loadMockData: () => void;
  setMetrics: (m: StudentMetrics) => void;
  setRating: (r: RatingInfo) => void;
}

export const useMetricsStore = create<MetricsState>()((set) => ({
  metrics: null,
  rating: null,
  loading: false,

  loadMockData: () =>
    set({
      metrics: MOCK_METRICS,
      rating: MOCK_RATING,
      loading: false,
    }),

  setMetrics: (m) => set({ metrics: m }),
  setRating: (r) => set({ rating: r }),
}));
