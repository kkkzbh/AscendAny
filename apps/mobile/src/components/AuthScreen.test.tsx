import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthScreen } from "./AuthScreen";

const session = vi.hoisted(() => ({
  login: vi.fn(),
  consumeEnrollment: vi.fn(),
  clearError: vi.fn(),
  error: null as string | null,
}));

vi.mock("../session/SessionContext", () => ({
  useSession: () => ({
    login: session.login,
    consumeEnrollment: session.consumeEnrollment,
    clearError: session.clearError,
    error: session.error,
  }),
}));

describe("AuthScreen", () => {
  beforeEach(() => {
    session.login.mockReset();
    session.consumeEnrollment.mockReset();
    session.clearError.mockReset();
    session.login.mockResolvedValue(undefined);
    session.consumeEnrollment.mockResolvedValue(undefined);
    session.error = null;
  });

  afterEach(cleanup);

  it("submits local account credentials without trimming the password", async () => {
    render(<AuthScreen />);
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: " student-1 " } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: " password with spaces " } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));

    await waitFor(() => expect(session.login).toHaveBeenCalledWith("student-1", " password with spaces "));
  });

  it("submits an explicitly entered one-time enrollment claim", async () => {
    render(<AuthScreen />);
    fireEvent.click(screen.getByRole("tab", { name: "首次激活" }));
    fireEvent.change(screen.getByLabelText("一次性激活凭证"), { target: { value: " claim-secret " } });
    fireEvent.change(screen.getByLabelText("设置密码"), { target: { value: "new secure password" } });
    fireEvent.click(screen.getByRole("button", { name: "激活并登录" }));

    await waitFor(() => expect(session.consumeEnrollment).toHaveBeenCalledWith("claim-secret", "new secure password"));
  });
});
