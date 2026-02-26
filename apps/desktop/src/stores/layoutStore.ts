import { create } from "zustand";
import { persist } from "zustand/middleware";

interface LayoutState {
  isMetricsPanelVisible: boolean;
  splitRatio: number;
  resetForAccount: () => void;
  toggleMetricsPanel: () => void;
  setSplitRatio: (ratio: number) => void;
}

const DEFAULT_SPLIT_RATIO = 0.55;
const MIN_SPLIT_RATIO = 0.3;
const MAX_SPLIT_RATIO = 1 - MIN_SPLIT_RATIO;

function normalizeSplitRatio(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_SPLIT_RATIO;
  }
  return Math.max(MIN_SPLIT_RATIO, Math.min(MAX_SPLIT_RATIO, value));
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      isMetricsPanelVisible: true,
      splitRatio: DEFAULT_SPLIT_RATIO,
      resetForAccount: () =>
        set({
          isMetricsPanelVisible: true,
          splitRatio: DEFAULT_SPLIT_RATIO,
        }),
      toggleMetricsPanel: () =>
        set((state) => ({
          isMetricsPanelVisible: !state.isMetricsPanelVisible,
        })),
      setSplitRatio: (ratio) =>
        set({
          splitRatio: normalizeSplitRatio(ratio),
        }),
    }),
    {
      name: "ascendany_layout_guest",
      partialize: (state) => ({
        isMetricsPanelVisible: state.isMetricsPanelVisible,
        splitRatio: state.splitRatio,
      }),
      merge: (persistedState, currentState) => {
        const persisted = (persistedState ?? {}) as Partial<LayoutState>;
        return {
          ...currentState,
          isMetricsPanelVisible:
            typeof persisted.isMetricsPanelVisible === "boolean"
              ? persisted.isMetricsPanelVisible
              : currentState.isMetricsPanelVisible,
          splitRatio: normalizeSplitRatio(persisted.splitRatio),
        };
      },
    },
  ),
);
