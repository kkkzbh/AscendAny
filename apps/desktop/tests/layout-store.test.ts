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

  it("persists split ratio and metrics panel visibility", async () => {
    useLayoutStore.getState().setSplitRatio(0.37);
    useLayoutStore.getState().toggleMetricsPanel();

    useLayoutStore.getState().resetForAccount();
    await useLayoutStore.persist.rehydrate();

    const state = useLayoutStore.getState();
    expect(state.splitRatio).toBe(0.37);
    expect(state.isMetricsPanelVisible).toBe(false);
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
        },
        version: 0,
      }),
    );

    await useLayoutStore.persist.rehydrate();
    const state = useLayoutStore.getState();
    expect(state.splitRatio).toBe(0.55);
    expect(state.isMetricsPanelVisible).toBe(true);
  });
});
