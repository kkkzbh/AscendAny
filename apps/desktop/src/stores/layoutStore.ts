import { create } from "zustand";
import { persist } from "zustand/middleware";

export type RightPanelTab = "ability" | "history";

interface LayoutState {
  isLeftSidebarCollapsed: boolean;
  leftSidebarRatio: number;
  isMetricsPanelVisible: boolean;
  activeRightPanelTab: RightPanelTab;
  splitRatio: number;
  activeFullscreenView: "none" | "achievements";
  resetForAccount: () => void;
  toggleLeftSidebar: () => void;
  setLeftSidebarCollapsed: (collapsed: boolean) => void;
  setLeftSidebarRatio: (ratio: number) => void;
  toggleMetricsPanel: () => void;
  setActiveRightPanelTab: (tab: RightPanelTab) => void;
  setSplitRatio: (ratio: number) => void;
  setActiveFullscreenView: (view: "none" | "achievements") => void;
  closeFullscreenView: () => void;
}

export const DEFAULT_LEFT_SIDEBAR_RATIO = 0.22;
export const MIN_LEFT_SIDEBAR_RATIO = 0.17;
export const MAX_LEFT_SIDEBAR_RATIO = 0.32;

const DEFAULT_SPLIT_RATIO = 0.55;
const MIN_SPLIT_RATIO = 0.3;
const MAX_SPLIT_RATIO = 1 - MIN_SPLIT_RATIO;

function normalizeLeftSidebarRatio(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_LEFT_SIDEBAR_RATIO;
  }
  return Math.max(MIN_LEFT_SIDEBAR_RATIO, Math.min(MAX_LEFT_SIDEBAR_RATIO, value));
}

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
      leftSidebarRatio: DEFAULT_LEFT_SIDEBAR_RATIO,
      isMetricsPanelVisible: true,
      activeRightPanelTab: "ability",
      splitRatio: DEFAULT_SPLIT_RATIO,
      activeFullscreenView: "none",
      resetForAccount: () =>
        set({
          isLeftSidebarCollapsed: false,
          leftSidebarRatio: DEFAULT_LEFT_SIDEBAR_RATIO,
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
      setLeftSidebarRatio: (ratio) =>
        set({
          leftSidebarRatio: normalizeLeftSidebarRatio(ratio),
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
        leftSidebarRatio: state.leftSidebarRatio,
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
          leftSidebarRatio: normalizeLeftSidebarRatio(persisted.leftSidebarRatio),
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
