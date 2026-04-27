import { create } from "zustand";
import { persist } from "zustand/middleware";

export type RightPanelTab = "ability" | "history";

interface LayoutState {
  isLeftSidebarCollapsed: boolean;
  isMetricsPanelVisible: boolean;
  activeRightPanelTab: RightPanelTab;
  splitRatio: number;
  activeFullscreenView: "none" | "achievements";
  resetForAccount: () => void;
  toggleLeftSidebar: () => void;
  setLeftSidebarCollapsed: (collapsed: boolean) => void;
  toggleMetricsPanel: () => void;
  setActiveRightPanelTab: (tab: RightPanelTab) => void;
  setSplitRatio: (ratio: number) => void;
  setActiveFullscreenView: (view: "none" | "achievements") => void;
  closeFullscreenView: () => void;
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
      isLeftSidebarCollapsed: false,
      isMetricsPanelVisible: true,
      activeRightPanelTab: "ability",
      splitRatio: DEFAULT_SPLIT_RATIO,
      activeFullscreenView: "none",
      resetForAccount: () =>
        set({
          isLeftSidebarCollapsed: false,
          isMetricsPanelVisible: true,
          activeRightPanelTab: "ability",
          splitRatio: DEFAULT_SPLIT_RATIO,
          activeFullscreenView: "none",
        }),
      toggleLeftSidebar: () =>
        set((state) => ({
          isLeftSidebarCollapsed: !state.isLeftSidebarCollapsed,
        })),
      setLeftSidebarCollapsed: (collapsed) =>
        set({
          isLeftSidebarCollapsed: collapsed,
        }),
      toggleMetricsPanel: () =>
        set((state) => ({
          isMetricsPanelVisible: !state.isMetricsPanelVisible,
        })),
      setActiveRightPanelTab: (tab) =>
        set({
          activeRightPanelTab: tab,
        }),
      setSplitRatio: (ratio) =>
        set({
          splitRatio: normalizeSplitRatio(ratio),
        }),
      setActiveFullscreenView: (view) =>
        set({
          activeFullscreenView: view,
        }),
      closeFullscreenView: () =>
        set({
          activeFullscreenView: "none",
        }),
    }),
    {
      name: "ascendany_layout_guest",
      partialize: (state) => ({
        isLeftSidebarCollapsed: state.isLeftSidebarCollapsed,
        isMetricsPanelVisible: state.isMetricsPanelVisible,
        activeRightPanelTab: state.activeRightPanelTab,
        splitRatio: state.splitRatio,
      }),
      merge: (persistedState, currentState) => {
        const persisted = (persistedState ?? {}) as Partial<LayoutState>;
        return {
          ...currentState,
          isLeftSidebarCollapsed:
            typeof persisted.isLeftSidebarCollapsed === "boolean"
              ? persisted.isLeftSidebarCollapsed
              : currentState.isLeftSidebarCollapsed,
          isMetricsPanelVisible:
            typeof persisted.isMetricsPanelVisible === "boolean"
              ? persisted.isMetricsPanelVisible
              : currentState.isMetricsPanelVisible,
          activeRightPanelTab:
            persisted.activeRightPanelTab === "ability" ||
            persisted.activeRightPanelTab === "history"
              ? persisted.activeRightPanelTab
              : currentState.activeRightPanelTab,
          splitRatio: normalizeSplitRatio(persisted.splitRatio),
        };
      },
    },
  ),
);
