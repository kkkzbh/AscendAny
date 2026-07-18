import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSettingsStore } from "@/stores/settingsStore";
import { DEFAULT_ZOOM_PERCENT } from "@/types/settings";

describe("settingsStore zoom", () => {
  const localStateSaveSettings = vi.fn().mockResolvedValue(true);

  beforeEach(() => {
    localStateSaveSettings.mockClear();
    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      localStateSaveSettings,
    };
    useSettingsStore.getState().resetForAccount();
  });

  it("normalizes zoom value into configured range and step", () => {
    const { setZoomPercent } = useSettingsStore.getState();

    setZoomPercent(83);
    expect(useSettingsStore.getState().zoomPercent).toBe(85);

    setZoomPercent(79);
    expect(useSettingsStore.getState().zoomPercent).toBe(80);

    setZoomPercent(200);
    expect(useSettingsStore.getState().zoomPercent).toBe(130);

    setZoomPercent(Number.NaN);
    expect(useSettingsStore.getState().zoomPercent).toBe(DEFAULT_ZOOM_PERCENT);
  });

  it("hydrates normalized values from local state snapshot", () => {
    useSettingsStore.getState().hydrateFromLocalState({
      zoomPercent: 118,
    });
    expect(useSettingsStore.getState().zoomPercent).toBe(120);

    useSettingsStore.getState().hydrateFromLocalState({
      zoomPercent: "bad-data",
    });
    expect(useSettingsStore.getState().zoomPercent).toBe(DEFAULT_ZOOM_PERCENT);
  });

  it("persists settings changes through desktop local state IPC", () => {
    useSettingsStore.getState().setZoomPercent(118);
    expect(localStateSaveSettings).toHaveBeenCalled();
    expect(localStateSaveSettings.mock.calls.at(-1)?.[0].zoomPercent).toBe(120);
  });

  it("allows custom role id as active role", () => {
    useSettingsStore.getState().setActiveRole("custom_role_test");
    expect(useSettingsStore.getState().activeRole).toBe("custom_role_test");
  });
});
