import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { submitFeedback } from "@/lib/api";
import { FeedbackSettingsPage } from "@/components/settings/FeedbackSettingsPage";
import { useAuthStore } from "@/stores/authStore";

vi.mock("@/lib/api", () => ({
  submitFeedback: vi.fn(),
  getApiErrorMessage: (error: unknown, fallback: string) =>
    error instanceof Error && error.message.trim() ? error.message : fallback,
}));

const submitFeedbackMock = vi.mocked(submitFeedback);

describe("FeedbackSettingsPage", () => {
  beforeEach(() => {
    submitFeedbackMock.mockReset();
    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
    };
    useAuthStore.setState({
      status: "authenticated",
      account: {
        accountId: "42",
        username: "alice",
        displayName: "Alice",
        studentId: "20230001",
        ptaNickname: "pta-alice",
        provisionSource: "local",
        localPasswordEnabled: true,
      },
      accessToken: "access-token",
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("submits feedback through the API and clears the form on success", async () => {
    submitFeedbackMock.mockResolvedValue({
      success: true,
      message: "反馈已发送，感谢你的反馈。",
    });
    render(<FeedbackSettingsPage />);

    const titleInput = screen.getByLabelText("标题");
    const contentInput = screen.getByLabelText("详细描述");
    fireEvent.change(titleInput, { target: { value: "截图异常" } });
    fireEvent.change(contentInput, { target: { value: "设置页截图上传区异常。" } });
    fireEvent.click(screen.getByRole("button", { name: "发送反馈" }));

    await waitFor(() => {
      expect(submitFeedbackMock).toHaveBeenCalledTimes(1);
    });
    expect(submitFeedbackMock).toHaveBeenCalledWith(
      expect.objectContaining({
        title: "截图异常",
        content: "设置页截图上传区异常。",
        images: [],
        platform: "linux",
      }),
      "access-token",
    );
    await waitFor(() => {
      expect((titleInput as HTMLInputElement).value).toBe("");
      expect((contentInput as HTMLTextAreaElement).value).toBe("");
    });
    expect(screen.getByText("反馈已发送，感谢你的反馈。")).toBeTruthy();
  });

  it("shows API failures without clearing the form", async () => {
    submitFeedbackMock.mockRejectedValue(new Error("反馈发送失败，请稍后重试。"));
    render(<FeedbackSettingsPage />);

    const titleInput = screen.getByLabelText("标题");
    const contentInput = screen.getByLabelText("详细描述");
    fireEvent.change(titleInput, { target: { value: "截图异常" } });
    fireEvent.change(contentInput, { target: { value: "设置页截图上传区异常。" } });
    fireEvent.click(screen.getByRole("button", { name: "发送反馈" }));

    await waitFor(() => {
      expect(screen.getByText("反馈发送失败，请稍后重试。")).toBeTruthy();
    });
    expect((titleInput as HTMLInputElement).value).toBe("截图异常");
    expect((contentInput as HTMLTextAreaElement).value).toBe("设置页截图上传区异常。");
  });
});
