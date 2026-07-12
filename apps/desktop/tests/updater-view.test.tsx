import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { UpdaterView } from "../src/components/UpdaterView";

function setElectronAPI(api: ElectronAPI | undefined): void {
  Object.defineProperty(window, "electronAPI", {
    configurable: true,
    writable: true,
    value: api,
  });
}

describe("desktop updater surface", () => {
  afterEach(() => {
    cleanup();
    setElectronAPI(undefined);
  });

  it("states that updater capability is unavailable in browser preview", () => {
    setElectronAPI(undefined);
    render(<UpdaterView />);

    expect(screen.getByText("当前环境没有客户端更新能力")).toBeTruthy();
  });

  it("delegates checks to the Electron updater bridge", async () => {
    const checkNow = vi.fn().mockResolvedValue({
      success: true,
      message: "检查已开始",
    });
    const api: ElectronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      updaterGetState: vi.fn().mockResolvedValue({
        status: "idle",
        currentVersion: "0.1.0",
        latestVersion: null,
        progressPercent: null,
        lastCheckedAt: null,
        message: null,
      }),
      updaterCheckNow: checkNow,
      updaterOnStateChanged: vi.fn(() => vi.fn()),
    };
    setElectronAPI(api);
    render(<UpdaterView />);

    const button = await screen.findByRole("button", { name: "检查更新" });
    fireEvent.click(button);

    await waitFor(() => expect(checkNow).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByText("检查已开始")).toBeTruthy());
  });
});
