import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";

const { exchangeSsoToken } = vi.hoisted(() => ({
  exchangeSsoToken: vi.fn(),
}));

vi.mock("@/stores/authStore", () => ({
  useAuthStore: (selector: (state: { exchangeSsoToken: typeof exchangeSsoToken }) => unknown) =>
    selector({ exchangeSsoToken }),
}));

import { SsoExchangeScreen } from "@/components/auth/SsoExchangeScreen";

describe("SsoExchangeScreen", () => {
  beforeEach(() => {
    exchangeSsoToken.mockReset();
    window.history.replaceState(null, "", "/");
  });

  it("exchanges hash token and clears the callback hash", async () => {
    exchangeSsoToken.mockResolvedValue(undefined);
    window.history.replaceState(null, "", "/#/sso?token=test-jwt-token");

    render(<SsoExchangeScreen />);

    await waitFor(() => {
      expect(exchangeSsoToken).toHaveBeenCalledWith("test-jwt-token");
    });
    await waitFor(() => {
      expect(window.location.hash).toBe("");
    });
  });

  it("shows api error details when exchange fails", async () => {
    exchangeSsoToken.mockRejectedValue(
      new ApiError("票据已过期", 401, "AUTH_SSO_TOKEN_EXPIRED"),
    );
    window.history.replaceState(null, "", "/#/sso?token=expired-jwt-token");

    render(<SsoExchangeScreen />);

    await waitFor(() => {
      expect(screen.getByText("SSO 登录失败")).toBeTruthy();
    });
    expect(screen.getByText("票据已过期（AUTH_SSO_TOKEN_EXPIRED）")).toBeTruthy();
    expect(screen.getByRole("button", { name: "返回外部系统" })).toBeTruthy();
  });
});
