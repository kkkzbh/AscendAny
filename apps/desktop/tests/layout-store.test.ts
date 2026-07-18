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
    useLayoutStore.getState().setRightPanelRatio(0.37);
    useLayoutStore.getState().toggleMetricsPanel();
    useLayoutStore.getState().setActiveRightPanelTab("history");
    useLayoutStore.getState().setActiveFullscreenView("achievements");

    const saved = localStateSaveLayout.mock.calls.at(-1)?.[0];
    expect(saved.isLeftSidebarCollapsed).toBe(true);
    expect(saved.leftSidebarRatio).toBe(0.24);
    expect(saved.rightPanelRatio).toBe(0.37);
    expect(saved.isMetricsPanelVisible).toBe(false);
    expect(saved.activeRightPanelTab).toBe("history");
    expect(saved.activeFullscreenView).toBe("achievements");
  });

  it("normalizes right panel ratio from runtime updates and local state snapshots", () => {
    useLayoutStore.getState().setLeftSidebarRatio(0.99);
    useLayoutStore.getState().setRightPanelRatio(0.99);
    expect(useLayoutStore.getState().leftSidebarRatio).toBe(0.32);
    expect(useLayoutStore.getState().rightPanelRatio).toBe(0.5);

    useLayoutStore.getState().setLeftSidebarRatio(0.01);
    useLayoutStore.getState().setRightPanelRatio(0.01);
    expect(useLayoutStore.getState().leftSidebarRatio).toBe(0.17);
    expect(useLayoutStore.getState().rightPanelRatio).toBe(0.32);

    useLayoutStore.getState().hydrateFromLocalState({
      leftSidebarRatio: "bad-data",
      rightPanelRatio: "bad-data",
      isMetricsPanelVisible: "bad-data",
      isLeftSidebarCollapsed: "bad-data",
      activeRightPanelTab: "bad-data",
    });
    const state = useLayoutStore.getState();
    expect(state.leftSidebarRatio).toBe(0.22);
    expect(state.rightPanelRatio).toBe(0.36);
    expect(state.isMetricsPanelVisible).toBe(true);
    expect(state.isLeftSidebarCollapsed).toBe(false);
    expect(state.activeRightPanelTab).toBe("ability");
  });

  it("hydrates legacy split ratio as the right panel ratio", () => {
    useLayoutStore.getState().hydrateFromLocalState({
      splitRatio: 0.42,
    });

    expect(useLayoutStore.getState().rightPanelRatio).toBe(0.42);
  });

  it("can open and close fullscreen achievement view", () => {
    useLayoutStore.getState().setActiveFullscreenView("achievements");
    expect(useLayoutStore.getState().activeFullscreenView).toBe("achievements");

    useLayoutStore.getState().closeFullscreenView();
    expect(useLayoutStore.getState().activeFullscreenView).toBe("none");
  });
});
