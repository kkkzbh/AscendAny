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
      setOpaqueSidebarBackground: vi.fn().mockResolvedValue(true),
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

  it("toggles only the sidebar opacity setting without using window-wide styling", () => {
    render(<SettingsWorkspace />);

    const toggle = screen.getByRole("switch", { name: "使用不透明左侧栏背景" });
    expect(screen.getByText("使用不透明左侧栏背景")).toBeTruthy();
    expect(toggle.getAttribute("aria-checked")).toBe("true");

    fireEvent.click(toggle);

    expect(useSettingsStore.getState().useOpaqueSidebarBackground).toBe(false);
    expect(window.electronAPI?.setOpaqueSidebarBackground).toHaveBeenCalledWith(false);
  });

  it("keeps transparent styling scoped to the left sidebars", () => {
    expect(SETTINGS_CSS).not.toContain("data-opaque-window");
    expect(SETTINGS_CSS).not.toContain("#26264f");
    expect(SETTINGS_CSS).not.toContain("#143447");
    const studentSidebarRule = getCssRule(".student-sidebar");
    const settingsSidebarRule = getCssRule(".settings-sidebar");
    const darkRootRule = getCssRule(':root[data-theme="dark"]');
    const darkStudentAppRule = getCssRule(':root[data-theme="dark"] .student-app');
    const darkStudentSidebarRule = getCssRule(':root[data-theme="dark"] .student-sidebar');
    const darkTransparentStudentSidebarRule = getCssRule(':root[data-theme="dark"][data-opaque-sidebar="false"] .student-sidebar');
    const transparentStudentSidebarRule = getCssRule(':root[data-opaque-sidebar="false"] .student-sidebar');
    const transparentSettingsSidebarRule = getCssRule(':root[data-opaque-sidebar="false"] .settings-sidebar');

    expect(darkRootRule).toContain("--surface-base: #151617");
    expect(darkRootRule).toContain("--surface-raised: rgba(32, 33, 35, 0.96)");
    expect(darkStudentAppRule).toContain("--student-surface: #191a1b");
    expect(darkStudentAppRule).toContain("--student-surface-raised: #202123");
    expect(studentSidebarRule).toContain("linear-gradient(155deg, #ded7ff, #d6edf6)");
    expect(studentSidebarRule).toContain("#eef7fb");
    expect(settingsSidebarRule).toContain("linear-gradient(155deg, #ded7ff, #d6edf6)");
    expect(settingsSidebarRule).toContain("#eef7fb");
    expect(darkStudentSidebarRule).toContain("linear-gradient(155deg, #1f2021, #18191a)");
    expect(darkStudentSidebarRule).toContain("#141516");
    expect(transparentStudentSidebarRule).toContain("background: rgba(248, 250, 252, 0.26)");
    expect(transparentSettingsSidebarRule).toContain("background: rgba(248, 250, 252, 0.26)");
    expect(darkTransparentStudentSidebarRule).toContain("background: rgba(20, 21, 22, 0.42)");
    expect(getCssRule("body")).not.toContain("var(--body-background)");
    expect(getCssRule(".student-main")).toContain("background: var(--student-surface)");
    expect(getCssRule(".settings-main")).toContain("background: var(--student-surface)");
  });
});
