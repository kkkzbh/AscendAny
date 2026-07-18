import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { UpdateFlowDialog } from "@/components/updater/UpdateFlowDialog";

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
    currentVersion: "1.0.0",
    latestVersion: null,
    progressPercent: null,
    lastCheckedAt: null,
    message: null,
    ...state,
  };
}

describe("UpdateFlowDialog", () => {
  it("shows confirm dialog for available update and allows skipping", async () => {
    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      updaterGetState: vi.fn().mockResolvedValue(
        mockUpdaterState({
          status: "available",
          latestVersion: "1.1.0",
        }),
      ),
      updaterStartDownload: vi.fn().mockResolvedValue({
        success: true,
        message: "已开始下载更新包。",
      } satisfies TestUpdateActionResult),
      updaterOnStateChanged: vi.fn().mockImplementation(() => () => {}),
    };

    render(<UpdateFlowDialog />);

    await waitFor(() => {
      expect(screen.getByText("发现新版本")).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: "稍后更新" }));
    await waitFor(() => {
      expect(screen.queryByText("发现新版本")).toBeNull();
    });
  });

  it("starts download after confirmation and shows progress", async () => {
    let listener: ((state: TestUpdateStateSnapshot) => void) | null = null;
    const updaterStartDownload = vi.fn().mockImplementation(async () => {
      listener?.(
        mockUpdaterState({
          status: "downloading",
          latestVersion: "1.1.0",
          progressPercent: 0,
          message: "正在下载更新包...",
        }),
      );
      return {
        success: true,
        message: "已开始下载更新包。",
      } satisfies TestUpdateActionResult;
    });

    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      updaterGetState: vi.fn().mockResolvedValue(
        mockUpdaterState({
          status: "available",
          latestVersion: "1.1.0",
        }),
      ),
      updaterStartDownload,
      updaterOnStateChanged: vi.fn().mockImplementation((next) => {
        listener = next as (state: TestUpdateStateSnapshot) => void;
        return () => {
          listener = null;
        };
      }),
    };

    render(<UpdateFlowDialog />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "立即更新" })).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: "立即更新" }));

    await waitFor(() => {
      expect(updaterStartDownload).toHaveBeenCalledTimes(1);
      expect(screen.getByText("正在更新客户端")).toBeTruthy();
    });

    listener?.(
      mockUpdaterState({
        status: "downloading",
        latestVersion: "1.1.0",
        progressPercent: 42.37,
        message: "正在下载更新包...",
      }),
    );

    await waitFor(() => {
      expect(screen.getByText("42.37%")).toBeTruthy();
    });
  });
});
