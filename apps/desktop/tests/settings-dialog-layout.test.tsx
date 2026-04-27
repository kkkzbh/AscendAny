import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { useSettingsStore } from "@/stores/settingsStore";

const SETTINGS_CSS = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

function getCssRule(selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`).exec(SETTINGS_CSS);
  return match?.[1] ?? "";
}

describe("SettingsDialog layout", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  beforeEach(() => {
    localStorage.clear();
    useSettingsStore.getState().resetForAccount();
    useSettingsStore.getState().openSettings();
    window.electronAPI = {
      platform: "linux",
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      updaterGetState: vi.fn().mockResolvedValue({
        status: "idle",
        currentVersion: "0.1.0",
        latestVersion: null,
        progressPercent: null,
        lastCheckedAt: null,
        message: null,
      }),
      updaterOnStateChanged: vi.fn().mockImplementation(() => () => {}),
    };
  });

  it("keeps the settings scrollbar at the outer edge and preserves the close action", () => {
    render(<SettingsDialog />);

    const scrollContainer = document.querySelector(".settings-content");
    const innerContainer = document.querySelector(".settings-content-inner");
    const controls = document.querySelector(".settings-dialog-controls");
    const closeButton = screen.getByRole("button", { name: "关闭设置" });

    expect(scrollContainer).toBeTruthy();
    expect(innerContainer).toBeTruthy();
    expect(controls).toBeTruthy();
    expect(scrollContainer?.contains(innerContainer)).toBe(true);
    expect(controls?.contains(closeButton)).toBe(true);
    expect(scrollContainer?.classList.contains("overflow-y-auto")).toBe(true);

    expect(getCssRule(".settings-content")).toContain("padding: 0 !important");
    expect(getCssRule(".settings-content-inner")).toContain("padding: 20px 18px 16px 18px");
    expect(getCssRule(".settings-dialog-controls")).toContain("flex: 0 0 34px");

    fireEvent.click(closeButton);
    expect(useSettingsStore.getState().isOpen).toBe(false);
  });
});
