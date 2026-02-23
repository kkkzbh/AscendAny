import { beforeEach, describe, expect, it } from "vitest";
import { useSettingsStore } from "@/stores/settingsStore";

describe("settingsStore theme mode", () => {
  beforeEach(() => {
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

  it("stores opaque window background preference", () => {
    useSettingsStore.getState().setOpaqueWindowBackground(false);
    expect(useSettingsStore.getState().useOpaqueWindowBackground).toBe(false);
  });
});
