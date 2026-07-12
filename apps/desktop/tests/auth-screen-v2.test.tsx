import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthScreen } from "../src/components/AuthScreen";

const session = vi.hoisted(() => ({
  login: vi.fn(),
  consumeEnrollment: vi.fn(),
  clearError: vi.fn(),
  error: null as string | null,
}));

vi.mock("../src/session/context", () => ({
  useSession: () => session,
}));

describe("desktop v2 AuthScreen", () => {
  beforeEach(() => {
    session.login.mockReset().mockResolvedValue(undefined);
    session.consumeEnrollment.mockReset().mockResolvedValue(undefined);
    session.clearError.mockReset();
    session.error = null;
    window.history.replaceState({}, "", "/");
  });

  afterEach(cleanup);

  it("trims the username while preserving password whitespace", async () => {
    render(<AuthScreen />);
    fireEvent.change(screen.getByLabelText("用户名"), {
      target: { value: " student-1 " },
    });
    fireEvent.change(screen.getByLabelText("密码"), {
      target: { value: " password with spaces " },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));

    await waitFor(() => {
      expect(session.login).toHaveBeenCalledWith(
        "student-1",
        " password with spaces ",
      );
    });
  });

  it("consumes only the explicitly entered enrollment claim", async () => {
    window.history.replaceState(
      {},
      "",
      "/?token=query-secret#access_token=legacy-secret&refresh_token=legacy-refresh",
    );
    render(<AuthScreen />);
    expect(session.consumeEnrollment).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("tab", { name: "首次激活" }));
    fireEvent.change(screen.getByLabelText("一次性激活凭证"), {
      target: { value: " claim-secret " },
    });
    fireEvent.change(screen.getByLabelText("设置密码"), {
      target: { value: "new secure password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "激活并登录" }));

    await waitFor(() => {
      expect(session.consumeEnrollment).toHaveBeenCalledWith(
        "claim-secret",
        "new secure password",
      );
    });
  });

  it("enforces the v2 UTF-8 password byte contract", () => {
    render(<AuthScreen />);
    fireEvent.change(screen.getByLabelText("用户名"), {
      target: { value: "student-1" },
    });
    fireEvent.change(screen.getByLabelText("密码"), {
      target: { value: "too-short" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));

    expect(screen.getByRole("alert").textContent).toContain("12 至 128");
    expect(session.login).not.toHaveBeenCalled();
  });
});
