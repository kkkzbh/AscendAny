import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SettingsWorkspace } from "@/components/settings/SettingsDialog";
import { useSettingsStore } from "@/stores/settingsStore";

const SETTINGS_CSS = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

function getCssRule(selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`).exec(SETTINGS_CSS);
  return match?.[1] ?? "";
}

describe("SettingsWorkspace layout", () => {
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

  it("renders settings as an in-window workspace and returns to the app", () => {
    render(<SettingsWorkspace />);

    const workspace = document.querySelector(".settings-workspace");
    const sidebar = document.querySelector(".settings-sidebar");
    const scrollContainer = document.querySelector(".settings-content");
    const innerContainer = document.querySelector(".settings-content-inner");
    const returnButton = screen.getByRole("button", { name: "返回应用" });

    expect(workspace).toBeTruthy();
    expect(sidebar).toBeTruthy();
    expect(scrollContainer).toBeTruthy();
    expect(innerContainer).toBeTruthy();
    expect(scrollContainer?.contains(innerContainer)).toBe(true);
    expect(document.querySelector(".settings-dialog")).toBeNull();
    expect(document.querySelector(".fixed.inset-0")).toBeNull();

    expect(getCssRule(".settings-workspace")).toContain(
      "grid-template-columns: calc(100vw * var(--student-default-left-sidebar-ratio, 0.22)) minmax(0, 1fr)",
    );
    expect(getCssRule(".settings-content")).toContain("overflow-y: auto");
    expect(getCssRule(".settings-content-inner")).toContain("width: min(720px, calc(100% - 48px))");
    expect(screen.queryByRole("separator", { name: "调整左侧栏宽度" })).toBeNull();

    fireEvent.click(returnButton);
    expect(useSettingsStore.getState().isOpen).toBe(false);
  });

  it("switches between settings tabs in the left sidebar", () => {
    render(<SettingsWorkspace />);

    expect(screen.getByText("通用设置")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "角色" }));
    expect(screen.getByText("角色设置")).toBeTruthy();
    expect(screen.queryByText("通用设置")).toBeNull();
  });
});
