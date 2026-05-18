import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

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
      accessToken: "token_1",
    });
  });

  afterEach(() => {
    cleanup();
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

  it("renders assistant streaming text as markdown before completion", () => {
    const message: ChatMessage = {
      id: "msg_streaming_markdown",
      role: "assistant",
      content: "**重点**\n\n- 数组\n\n```ts\nconst score = 1\n```",
      blocks: [
        {
          kind: "text",
          text: "**重点**\n\n- 数组\n\n```ts\nconst score = 1\n```",
        },
      ],
      timestamp: 1710000000000,
      streaming: true,
      reasoningStreaming: false,
    };

    const { container } = render(<MessageBubble message={message} />);

    expect(container.querySelector(".chat-markdown-streaming")).toBeTruthy();
    expect(container.querySelector(".streaming-message-text")).toBeNull();
    expect(container.querySelector('[data-streamdown="strong"]')?.textContent).toBe("重点");
    expect(container.querySelector("li")?.textContent).toContain("数组");
    expect(container.querySelector("code")?.textContent).toContain("const score = 1");
  });

  it("does not render an empty streamdown caret while waiting for text", () => {
    const message: ChatMessage = {
      id: "msg_waiting_for_text",
      role: "assistant",
      content: "",
      timestamp: 1710000000000,
      streaming: true,
      reasoningStreaming: false,
      blocks: [
        { kind: "tool", activity: { id: "tool_1", label: "查看考试数据", status: "running" } },
      ],
    };

    const { container } = render(<MessageBubble message={message} />);

    expect(screen.getByText("正在查看考试数据...")).toBeTruthy();
    expect(container.querySelector(".chat-markdown-streaming")).toBeNull();
  });

  it("removes streaming affordances after the assistant message completes", () => {
    const message: ChatMessage = {
      id: "msg_done_markdown",
      role: "assistant",
      content: "**完成**",
      blocks: [{ kind: "text", text: "**完成**" }],
      timestamp: 1710000000000,
      streaming: false,
      reasoningStreaming: false,
    };

    const { container } = render(<MessageBubble message={message} />);

    expect(container.querySelector(".chat-markdown-streaming")).toBeNull();
    expect(container.querySelector('[data-streamdown="strong"]')?.textContent).toBe("完成");
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

  it("preserves text and tool block order", () => {
    const message: ChatMessage = {
      id: "msg_blocks",
      role: "assistant",
      content: "先看结论后看建议",
      timestamp: 1710000000000,
      streaming: false,
      reasoningStreaming: false,
      blocks: [
        { kind: "text", text: "先看**结论**" },
        { kind: "tool", activity: { id: "tool_1", label: "查看考试数据", status: "done" } },
        { kind: "text", text: "后看`建议`" },
      ],
    };

    const { container } = render(<MessageBubble message={message} />);

    const body = container.querySelector(".assistant-message-body");
    const textBlocks = body?.querySelectorAll(".assistant-message-text");
    const tool = body?.querySelector(".assistant-tool-activities");
    const children = Array.from(body?.children ?? []);

    expect(textBlocks?.[0]?.textContent).toContain("先看结论");
    expect(tool?.textContent).toContain("查看考试数据");
    expect(textBlocks?.[1]?.textContent).toContain("后看建议");
    expect(children.indexOf(textBlocks?.[0] as Element)).toBeLessThan(
      children.indexOf(tool as Element),
    );
    expect(children.indexOf(tool as Element)).toBeLessThan(
      children.indexOf(textBlocks?.[1] as Element),
    );
  });
});
