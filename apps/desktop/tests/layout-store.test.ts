import { beforeEach, describe, expect, it } from "vitest";
import { useLayoutStore } from "@/stores/layoutStore";

describe("layoutStore", () => {
  beforeEach(async () => {
    localStorage.clear();
    useLayoutStore.persist.setOptions({
      name: "ascendany_layout_guest",
    });
    useLayoutStore.getState().resetForAccount();
    await useLayoutStore.persist.rehydrate();
  });

  it("persists student layout state", async () => {
    useLayoutStore.getState().toggleLeftSidebar();
    useLayoutStore.getState().setSplitRatio(0.37);
    useLayoutStore.getState().toggleMetricsPanel();
    useLayoutStore.getState().setActiveRightPanelTab("history");
    useLayoutStore.getState().setActiveFullscreenView("achievements");

    await useLayoutStore.persist.rehydrate();
    const persistedRaw = localStorage.getItem("ascendany_layout_guest");
    const persisted = persistedRaw ? JSON.parse(persistedRaw) : {};

    expect(persisted?.state?.isLeftSidebarCollapsed).toBe(true);
    expect(persisted?.state?.splitRatio).toBe(0.37);
    expect(persisted?.state?.isMetricsPanelVisible).toBe(false);
    expect(persisted?.state?.activeRightPanelTab).toBe("history");
    expect(persisted?.state?.activeFullscreenView).toBeUndefined();
  });

  it("normalizes split ratio from runtime updates and persisted snapshots", async () => {
    useLayoutStore.getState().setSplitRatio(0.99);
    expect(useLayoutStore.getState().splitRatio).toBe(0.7);

    localStorage.setItem(
      "ascendany_layout_guest",
      JSON.stringify({
        state: {
          splitRatio: "bad-data",
          isMetricsPanelVisible: "bad-data",
          isLeftSidebarCollapsed: "bad-data",
          activeRightPanelTab: "bad-data",
        },
        version: 0,
      }),
    );

    await useLayoutStore.persist.rehydrate();
    const state = useLayoutStore.getState();
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
