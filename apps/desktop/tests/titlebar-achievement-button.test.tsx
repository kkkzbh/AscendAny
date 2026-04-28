import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TitleBar } from "@/components/layout/TitleBar";
import { useAuthStore } from "@/stores/authStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useLeaderboardStore } from "@/stores/leaderboardStore";
import { useSettingsStore } from "@/stores/settingsStore";

const STUDENT_CSS = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

function getCssRule(selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`).exec(STUDENT_CSS);
  return match?.[1] ?? "";
}

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    fetchStudentsLeaderboard: vi.fn(async () => []),
  };
});

describe("TitleBar student controls", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  beforeEach(() => {
    useLayoutStore.getState().resetForAccount();
    useSettingsStore.getState().resetForAccount();
    useLeaderboardStore.getState().closeLeaderboard();
    useAuthStore.setState({
      status: "authenticated",
      account: {
        accountId: "student-1",
        username: "student",
        displayName: "student",
        studentId: "2023001",
        ptaNickname: "student",
        provisionSource: "local",
        localPasswordEnabled: false,
      },
      accessToken: null,
      refreshToken: null,
      initialized: true,
      rememberPassword: true,
    });
    window.electronAPI = {
      platform: "linux",
      openFeedbackWindow: vi.fn(async () => true),
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
    };
  });

  it("exposes the compact student titlebar actions", () => {
    render(<TitleBar />);

    expect(screen.getByRole("button", { name: "打开排行榜" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "打开成就页面" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "切换到暗色主题" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "打开反馈窗口" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "退出登录" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "展开左侧栏" })).toBeNull();
    expect(screen.getByRole("button", { name: "折叠右侧栏" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "关闭" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "最小化" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "最大化" })).toBeTruthy();
    expect(
      Array.from(document.querySelectorAll(".student-titlebar-traffic")).map((button) =>
        button.getAttribute("aria-label"),
      ),
    ).toEqual(["最小化", "最大化", "关闭"]);

    expect(screen.queryByRole("button", { name: /上传|导入/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /刷新/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /清空/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "设置" })).toBeNull();
    expect(screen.queryByRole("button", { name: "打开设置" })).toBeNull();
    expect(screen.queryByText(/DeepSeek|小D|2023 秋学期第 1 次月测/)).toBeNull();

    expect(getCssRule(".student-titlebar")).not.toContain("border-bottom");
  });

  it("toggles the right panel", () => {
    render(<TitleBar />);

    fireEvent.click(screen.getByRole("button", { name: "折叠右侧栏" }));
    expect(useLayoutStore.getState().isMetricsPanelVisible).toBe(false);
  });

  it("shows the left sidebar restore action only when the sidebar is collapsed", () => {
    useLayoutStore.getState().setLeftSidebarCollapsed(true);
    render(<TitleBar />);

    const restoreButton = screen.getByRole("button", { name: "展开左侧栏" });
    expect(restoreButton.classList.contains("student-titlebar-button")).toBe(true);

    fireEvent.click(restoreButton);
    expect(useLayoutStore.getState().isLeftSidebarCollapsed).toBe(false);
  });

  it("opens achievements and leaderboard from the titlebar", () => {
    render(<TitleBar />);

    fireEvent.click(screen.getByRole("button", { name: "打开成就页面" }));
    expect(useLayoutStore.getState().activeFullscreenView).toBe("achievements");

    expect(useLeaderboardStore.getState().isOpen).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "打开排行榜" }));
    expect(useLeaderboardStore.getState().isOpen).toBe(true);
  });

  it("keeps theme and feedback actions functional", () => {
    render(<TitleBar />);

    fireEvent.click(screen.getByRole("button", { name: "切换到暗色主题" }));
    expect(useSettingsStore.getState().theme).toBe("dark");
    expect(screen.getByRole("button", { name: "切换到亮色主题" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "打开反馈窗口" }));
    expect(window.electronAPI?.openFeedbackWindow).toHaveBeenCalledTimes(1);
  });
});
