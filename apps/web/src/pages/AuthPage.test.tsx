import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthPage } from "./AuthPage";

const session = vi.hoisted(() => ({
  login: vi.fn(),
  consumeEnrollment: vi.fn(),
  clearError: vi.fn(),
  error: null as string | null,
}));

vi.mock("../session/context", () => ({
  useSession: () => session,
}));

describe("AuthPage", () => {
  beforeEach(() => {
    session.login.mockReset();
    session.consumeEnrollment.mockReset();
    session.clearError.mockReset();
    session.login.mockResolvedValue(undefined);
    session.consumeEnrollment.mockResolvedValue(undefined);
    session.error = null;
  });

  afterEach(cleanup);

  it("submits a local login without changing password whitespace", async () => {
    render(
      <MemoryRouter
        future={{
          v7_relativeSplatPath: true,
          v7_startTransition: true,
        }}
      >
        <AuthPage mode="login" />
      </MemoryRouter>,
    );
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

  it("submits only the explicitly entered enrollment claim", async () => {
    window.history.replaceState(
      {},
      "",
      "/claim?token=must-not-be-consumed&password=must-not-be-read",
    );
    render(
      <MemoryRouter
        initialEntries={["/claim"]}
        future={{
          v7_relativeSplatPath: true,
          v7_startTransition: true,
        }}
      >
        <AuthPage mode="claim" />
      </MemoryRouter>,
    );
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

  it("rejects a password outside the v2 byte-length contract", () => {
    render(
      <MemoryRouter
        future={{
          v7_relativeSplatPath: true,
          v7_startTransition: true,
        }}
      >
        <AuthPage mode="login" />
      </MemoryRouter>,
    );
    fireEvent.change(screen.getByLabelText("用户名"), {
      target: { value: "student-1" },
    });
    fireEvent.change(screen.getByLabelText("密码"), {
      target: { value: "too-short" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));

    expect(screen.getByRole("alert")).toHaveTextContent("12 至 128");
    expect(session.login).not.toHaveBeenCalled();
  });
});
