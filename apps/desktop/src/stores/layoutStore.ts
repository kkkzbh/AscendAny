import { create } from "zustand";

interface LayoutState {
  isMetricsPanelVisible: boolean;
  toggleMetricsPanel: () => void;
}

export const useLayoutStore = create<LayoutState>()((set) => ({
  isMetricsPanelVisible: true,
  toggleMetricsPanel: () =>
    set((state) => ({
      isMetricsPanelVisible: !state.isMetricsPanelVisible,
    })),
}));
