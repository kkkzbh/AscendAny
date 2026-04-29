import { beforeEach, describe, expect, it, vi } from "vitest";
import { useSettingsStore } from "@/stores/settingsStore";

describe("settingsStore theme mode", () => {
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

  it("supports switching between light and dark", () => {
    useSettingsStore.getState().setTheme("dark");
    expect(useSettingsStore.getState().theme).toBe("dark");
  });

  it("toggles back to light from dark", () => {
    useSettingsStore.setState({ theme: "dark" });
    useSettingsStore.getState().toggleTheme();
    expect(useSettingsStore.getState().theme).toBe("light");
  });

  it("stores opaque sidebar background preference", () => {
    useSettingsStore.getState().setOpaqueSidebarBackground(false);
    expect(useSettingsStore.getState().useOpaqueSidebarBackground).toBe(false);
  });

  it("hydrates opaque sidebar background preference from local state", () => {
    useSettingsStore.getState().hydrateFromLocalState({
      useOpaqueSidebarBackground: false,
    });
    expect(useSettingsStore.getState().useOpaqueSidebarBackground).toBe(false);
  });
});
