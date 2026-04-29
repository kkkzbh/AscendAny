import { create } from "zustand";

export type RightPanelTab = "ability" | "history";

export interface LayoutSnapshot {
  isLeftSidebarCollapsed: boolean;
  leftSidebarRatio: number;
  isMetricsPanelVisible: boolean;
  activeRightPanelTab: RightPanelTab;
  splitRatio: number;
  activeFullscreenView: "none" | "achievements";
}

interface LayoutState extends LayoutSnapshot {
  resetForAccount: () => void;
  hydrateFromLocalState: (layout: unknown) => void;
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

function normalizeLayoutSnapshot(value: unknown, fallback: LayoutSnapshot): LayoutSnapshot {
  const persisted = (value ?? {}) as Partial<LayoutSnapshot>;
  return {
    isLeftSidebarCollapsed:
      typeof persisted.isLeftSidebarCollapsed === "boolean"
        ? persisted.isLeftSidebarCollapsed
        : fallback.isLeftSidebarCollapsed,
    leftSidebarRatio: normalizeLeftSidebarRatio(persisted.leftSidebarRatio),
    isMetricsPanelVisible:
      typeof persisted.isMetricsPanelVisible === "boolean"
        ? persisted.isMetricsPanelVisible
        : fallback.isMetricsPanelVisible,
    activeRightPanelTab:
      persisted.activeRightPanelTab === "ability" || persisted.activeRightPanelTab === "history"
        ? persisted.activeRightPanelTab
        : fallback.activeRightPanelTab,
    splitRatio: normalizeSplitRatio(persisted.splitRatio),
    activeFullscreenView:
      persisted.activeFullscreenView === "achievements" ? "achievements" : fallback.activeFullscreenView,
  };
}

function pickLayoutSnapshot(state: LayoutState): LayoutSnapshot {
  return {
    isLeftSidebarCollapsed: state.isLeftSidebarCollapsed,
    leftSidebarRatio: state.leftSidebarRatio,
    isMetricsPanelVisible: state.isMetricsPanelVisible,
    activeRightPanelTab: state.activeRightPanelTab,
    splitRatio: state.splitRatio,
    activeFullscreenView: state.activeFullscreenView,
  };
}

function persistLayoutSnapshot(snapshot: LayoutSnapshot): void {
  const api = typeof window === "undefined" ? undefined : window.electronAPI;
  if (!api?.localStateSaveLayout) {
    return;
  }
  void api.localStateSaveLayout(snapshot).catch(() => {
    // Local UI state remains authoritative until the next successful save.
  });
}

export const useLayoutStore = create<LayoutState>()(
  (set, get) => ({
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
      hydrateFromLocalState: (layout) =>
        set((state) => ({
          ...normalizeLayoutSnapshot(layout, pickLayoutSnapshot(state)),
        })),
      toggleLeftSidebar: () => {
        set((state) => ({
          isLeftSidebarCollapsed: !state.isLeftSidebarCollapsed,
        }));
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
      setLeftSidebarCollapsed: (collapsed) => {
        set({
          isLeftSidebarCollapsed: collapsed,
        });
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
      setLeftSidebarRatio: (ratio) => {
        set({
          leftSidebarRatio: normalizeLeftSidebarRatio(ratio),
        });
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
      toggleMetricsPanel: () => {
        set((state) => ({
          isMetricsPanelVisible: !state.isMetricsPanelVisible,
        }));
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
      setActiveRightPanelTab: (tab) => {
        set({
          activeRightPanelTab: tab,
        });
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
      setSplitRatio: (ratio) => {
        set({
          splitRatio: normalizeSplitRatio(ratio),
        });
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
      setActiveFullscreenView: (view) => {
        set({
          activeFullscreenView: view,
        });
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
      closeFullscreenView: () => {
        set({
          activeFullscreenView: "none",
        });
        persistLayoutSnapshot(pickLayoutSnapshot(get()));
      },
    }),
);
