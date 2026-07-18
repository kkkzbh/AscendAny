import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { useSettingsStore } from "@/stores/settingsStore";

type TestUpdateStatus =
  | "idle"
  | "checking"
  | "available"
  | "downloading"
  | "downloaded"
  | "up_to_date"
  | "error"
  | "disabled";

interface TestUpdateStateSnapshot {
  status: TestUpdateStatus;
  currentVersion: string;
  latestVersion: string | null;
  progressPercent: number | null;
  lastCheckedAt: string | null;
  message: string | null;
}

interface TestUpdateActionResult {
  success: boolean;
  message: string;
}

function mockUpdaterState(state: Partial<TestUpdateStateSnapshot>): TestUpdateStateSnapshot {
  return {
    status: "idle",
    currentVersion: "0.1.0",
    latestVersion: null,
    progressPercent: null,
    lastCheckedAt: null,
    message: null,
    ...state,
  };
}

describe("SettingsDialog update section", () => {
  beforeEach(() => {
    localStorage.clear();
    useSettingsStore.getState().resetForAccount();
    useSettingsStore.getState().openSettings();
  });

  it("shows update status and triggers manual check", async () => {
    const updaterCheckNow = vi.fn().mockResolvedValue({
      success: true,
      message: "已发起检查更新。",
    } satisfies TestUpdateActionResult);
    const updaterQuitAndInstall = vi.fn().mockResolvedValue({
      success: true,
      message: "正在重启并安装更新。",
    } satisfies TestUpdateActionResult);
    const updaterOnStateChanged = vi.fn().mockImplementation((_listener: (state: TestUpdateStateSnapshot) => void) => {
      return () => {};
    });

    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      updaterGetState: vi.fn().mockResolvedValue(
        mockUpdaterState({
          status: "downloaded",
          currentVersion: "1.0.0",
          latestVersion: "1.1.0",
          progressPercent: 100,
        }),
      ),
      updaterCheckNow,
      updaterQuitAndInstall,
      updaterOnStateChanged,
    };

    render(<SettingsDialog />);

    await waitFor(() => {
      expect(screen.getByText("当前版本：1.0.0")).toBeTruthy();
    });
    expect(screen.getByRole("button", { name: "重启并更新" })).toBeTruthy();

    fireEvent.click(screen.getAllByRole("button", { name: "检查更新" })[0]!);
    await waitFor(() => {
      expect(updaterCheckNow).toHaveBeenCalledTimes(1);
    });
  });

  it("disables manual check button while downloading", async () => {
    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "win32",
      updaterGetState: vi.fn().mockResolvedValue(
        mockUpdaterState({
          status: "downloading",
          currentVersion: "1.0.0",
          latestVersion: "1.1.0",
          progressPercent: 20.5,
        }),
      ),
      updaterCheckNow: vi.fn().mockResolvedValue({
        success: true,
        message: "",
      } satisfies TestUpdateActionResult),
      updaterQuitAndInstall: vi.fn().mockResolvedValue({
        success: true,
        message: "",
      } satisfies TestUpdateActionResult),
      updaterOnStateChanged: vi.fn().mockImplementation((_listener: (state: TestUpdateStateSnapshot) => void) => {
        return () => {};
      }),
    };

    render(<SettingsDialog />);

    await waitFor(() => {
      expect(screen.getByText("状态：下载中")).toBeTruthy();
    });
    const checkButtons = screen.getAllByRole("button", { name: "检查更新" });
    expect(checkButtons.some((button) => button.hasAttribute("disabled"))).toBe(true);
  });

  it("shows download button when update is available", async () => {
    const updaterStartDownload = vi.fn().mockResolvedValue({
      success: true,
      message: "已开始下载更新包。",
    } satisfies TestUpdateActionResult);

    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      updaterGetState: vi.fn().mockResolvedValue(
        mockUpdaterState({
          status: "available",
          currentVersion: "1.0.0",
          latestVersion: "1.1.0",
        }),
      ),
      updaterCheckNow: vi.fn().mockResolvedValue({
        success: true,
        message: "",
      } satisfies TestUpdateActionResult),
      updaterStartDownload,
      updaterQuitAndInstall: vi.fn().mockResolvedValue({
        success: true,
        message: "",
      } satisfies TestUpdateActionResult),
      updaterOnStateChanged: vi.fn().mockImplementation((_listener: (state: TestUpdateStateSnapshot) => void) => {
        return () => {};
      }),
    };

    render(<SettingsDialog />);

    await waitFor(() => {
      expect(screen.getByText("状态：发现新版本")).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: "立即更新" }));

    await waitFor(() => {
      expect(updaterStartDownload).toHaveBeenCalledTimes(1);
    });
  });
});
