import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { MessageBubble } from "@/components/chat/MessageBubble";
import { useAuthStore } from "@/stores/authStore";
import type { ChatMessage } from "@/types/chat";

describe("MessageBubble reasoning panel", () => {
  beforeEach(() => {
    useAuthStore.setState({
      account: {
        accountId: "account_1",
        username: "alice",
        displayName: "Alice",
        studentId: "20230001",
        ptaNickname: "alice_pta",
        provisionSource: "local",
        localPasswordEnabled: true,
      },
    });
  });

  it("renders a plain completed reasoning status and expands it on click", () => {
    const message: ChatMessage = {
      id: "msg_1",
      role: "assistant",
      content: "最终回答",
      reasoningContent: "先查看考试榜单，再比较五维指标。",
      timestamp: 1710000000000,
      reasoningStartedAt: 1710000000000,
      reasoningEndedAt: 1710000123000,
      streaming: false,
      reasoningStreaming: false,
    };

    render(<MessageBubble message={message} />);

    const toggle = screen.getByRole("button", { name: "思考结束(123s)" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    const body = screen.getByText("先查看考试榜单，再比较五维指标。")
      .closest(".assistant-reasoning-body");
    expect(body?.getAttribute("aria-hidden")).toBe("true");

    fireEvent.click(toggle);

    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(body?.getAttribute("aria-hidden")).toBe("false");
    expect(screen.getByText("先查看考试榜单，再比较五维指标。")).toBeTruthy();
    expect(screen.getByText("最终回答")).toBeTruthy();
  });

  it("labels an active reasoning stream as thinking", () => {
    const message: ChatMessage = {
      id: "msg_2",
      role: "assistant",
      content: "",
      reasoningContent: "正在整理",
      timestamp: 1710000000000,
      streaming: true,
      reasoningStreaming: true,
    };

    render(<MessageBubble message={message} />);

    expect(screen.getByRole("button", { name: /思考中/ })).toBeTruthy();
  });

  it("renders non-clickable public tool activity summaries", () => {
    const message: ChatMessage = {
      id: "msg_3",
      role: "assistant",
      content: "最终回答",
      timestamp: 1710000000000,
      streaming: false,
      reasoningStreaming: false,
      toolActivities: [
        { id: "call_1", label: "查看学习画像", status: "done" },
        { id: "call_2", label: "查看考试数据", status: "running" },
        { id: "call_3", label: "核对提交记录", status: "error" },
      ],
    };

    render(<MessageBubble message={message} />);

    expect(screen.getByText("查看学习画像")).toBeTruthy();
    expect(screen.getByText("正在查看考试数据...")).toBeTruthy();
    expect(screen.getByText("核对提交记录失败")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /查看学习画像/ })).toBeNull();
    expect(screen.queryByText(/get_student_learning_profile/)).toBeNull();
  });
});
