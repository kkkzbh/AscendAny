import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";

describe("SettingsDialog local password bootstrap", () => {
  const bootstrapLocalPassword = vi.fn().mockResolvedValue(undefined);

  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    localStorage.clear();
    bootstrapLocalPassword.mockClear();
    useSettingsStore.getState().resetForAccount();
    useSettingsStore.getState().openSettings();
    useAuthStore.setState({
      status: "authenticated",
      account: {
        accountId: "acc-1",
        username: "alice",
        displayName: "Alice",
        studentId: "20230001",
        ptaNickname: "alice_pta",
        provisionSource: "external_sso",
        localPasswordEnabled: false,
      },
      accessToken: "access-token",
      refreshToken: "refresh-token",
      autoLogin: true,
      rememberPassword: false,
      lastUsername: "alice",
      initialized: true,
      error: null,
      profileSaving: false,
      bootstrapLocalPassword,
      updateProfile: vi.fn().mockResolvedValue(undefined),
      clearError: vi.fn(),
    });

    window.electronAPI = {
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
      updaterOnStateChanged: vi.fn().mockImplementation(() => () => {}),
    };
  });

  it("shows local password setup for external sso accounts", async () => {
    render(<SettingsDialog />);

    await waitFor(() => {
      expect(screen.getAllByText("客户端登录密码").length).toBeGreaterThan(0);
    });
    expect(screen.getByText("当前账号来自外部单点登录。设置本地密码后，可直接在桌面客户端继续登录该账号。")).toBeTruthy();
  });

  it("submits and shows success after enabling local password", async () => {
    render(<SettingsDialog />);

    await waitFor(() => {
      expect(screen.getAllByText("客户端登录密码").length).toBeGreaterThan(0);
    });

    const passwordInputs = screen.getAllByPlaceholderText(/至少 8 位|再次输入密码/);
    fireEvent.change(passwordInputs[0]!, { target: { value: "password_123" } });
    fireEvent.change(passwordInputs[1]!, { target: { value: "password_123" } });
    fireEvent.click(screen.getByRole("button", { name: "设置本地密码" }));

    await waitFor(() => {
      expect(bootstrapLocalPassword).toHaveBeenCalledWith("password_123");
    });
    await waitFor(() => {
      expect(screen.getByText("已启用本地密码，可用于桌面客户端登录。")).toBeTruthy();
    });
  });
});
