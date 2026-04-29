import { beforeEach, describe, expect, it, vi } from "vitest";
import { useLayoutStore } from "@/stores/layoutStore";

describe("layoutStore", () => {
  const localStateSaveLayout = vi.fn().mockResolvedValue(true);

  beforeEach(() => {
    localStateSaveLayout.mockClear();
    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      localStateSaveLayout,
    };
    useLayoutStore.getState().resetForAccount();
  });

  it("persists student layout state through desktop local state IPC", () => {
    useLayoutStore.getState().toggleLeftSidebar();
    useLayoutStore.getState().setLeftSidebarRatio(0.24);
    useLayoutStore.getState().setSplitRatio(0.37);
    useLayoutStore.getState().toggleMetricsPanel();
    useLayoutStore.getState().setActiveRightPanelTab("history");
    useLayoutStore.getState().setActiveFullscreenView("achievements");

    const saved = localStateSaveLayout.mock.calls.at(-1)?.[0];
    expect(saved.isLeftSidebarCollapsed).toBe(true);
    expect(saved.leftSidebarRatio).toBe(0.24);
    expect(saved.splitRatio).toBe(0.37);
    expect(saved.isMetricsPanelVisible).toBe(false);
    expect(saved.activeRightPanelTab).toBe("history");
    expect(saved.activeFullscreenView).toBe("achievements");
  });

  it("normalizes split ratio from runtime updates and local state snapshots", () => {
    useLayoutStore.getState().setLeftSidebarRatio(0.99);
    useLayoutStore.getState().setSplitRatio(0.99);
    expect(useLayoutStore.getState().leftSidebarRatio).toBe(0.32);
    expect(useLayoutStore.getState().splitRatio).toBe(0.7);

    useLayoutStore.getState().setLeftSidebarRatio(0.01);
    expect(useLayoutStore.getState().leftSidebarRatio).toBe(0.17);

    useLayoutStore.getState().hydrateFromLocalState({
      leftSidebarRatio: "bad-data",
      splitRatio: "bad-data",
      isMetricsPanelVisible: "bad-data",
      isLeftSidebarCollapsed: "bad-data",
      activeRightPanelTab: "bad-data",
    });
    const state = useLayoutStore.getState();
    expect(state.leftSidebarRatio).toBe(0.22);
    expect(state.splitRatio).toBe(0.55);
    expect(state.isMetricsPanelVisible).toBe(true);
    expect(state.isLeftSidebarCollapsed).toBe(false);
    expect(state.activeRightPanelTab).toBe("ability");
  });

  it("can open and close fullscreen achievement view", () => {
    useLayoutStore.getState().setActiveFullscreenView("achievements");
    expect(useLayoutStore.getState().activeFullscreenView).toBe("achievements");

    useLayoutStore.getState().closeFullscreenView();
    expect(useLayoutStore.getState().activeFullscreenView).toBe("none");
  });
});
