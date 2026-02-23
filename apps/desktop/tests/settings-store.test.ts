import { beforeEach, describe, expect, it } from "vitest";

import { useSettingsStore } from "@/stores/settingsStore";
import { DEFAULT_ZOOM_PERCENT } from "@/types/settings";

describe("settingsStore zoom", () => {
  beforeEach(() => {
    localStorage.clear();
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

  it("keeps hydration compatible with legacy persisted values", async () => {
    localStorage.setItem(
      "ascendany_settings_guest",
      JSON.stringify({
        state: {
          zoomPercent: 118,
        },
        version: 0,
      }),
    );

    await useSettingsStore.persist.rehydrate();
    expect(useSettingsStore.getState().zoomPercent).toBe(120);

    localStorage.setItem(
      "ascendany_settings_guest",
      JSON.stringify({
        state: {
          zoomPercent: "bad-data",
        },
        version: 0,
      }),
    );

    await useSettingsStore.persist.rehydrate();
    expect(useSettingsStore.getState().zoomPercent).toBe(DEFAULT_ZOOM_PERCENT);
  });

  it("forces anthropic and deepseek enabled after server sync", () => {
    useSettingsStore.getState().syncProviderOptions({
      defaultProvider: "server_default",
      serverDefaultTarget: "openai",
      serverDefaultTargetLabel: "OpenAI",
      serverDefaultModel: "gpt-4o",
      providers: [
        {
          type: "anthropic",
          label: "Anthropic",
          usesServerConfig: false,
          enabled: false,
        },
        {
          type: "deepseek",
          label: "DeepSeek",
          usesServerConfig: false,
          enabled: false,
        },
      ],
    });

    const state = useSettingsStore.getState();
    expect(state.providers.anthropic.enabled).toBe(true);
    expect(state.providers.deepseek.enabled).toBe(true);
  });

  it("allows custom role id as active role", () => {
    useSettingsStore.getState().setActiveRole("custom_role_test");
    expect(useSettingsStore.getState().activeRole).toBe("custom_role_test");
  });
});
