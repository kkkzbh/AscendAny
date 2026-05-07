import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ChoiceBlock } from "@/components/chat/blocks/ChoiceBlock";
import { useChatStore } from "@/stores/chatStore";
import type { ChatBlock } from "@/types/chat";

const streamChatReplyMock = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    streamChatReply: streamChatReplyMock,
    getApiErrorMessage: (error: unknown, fallback: string) =>
      error instanceof Error ? error.message : fallback,
  };
});

const choiceBlock: Extract<ChatBlock, { kind: "choice" }> = {
  kind: "choice",
  question: "下一步练什么？",
  options: [
    { id: "A", label: "数组" },
    { id: "B", label: "链表" },
  ],
  explanation: "根据薄弱点选择。",
};

function ActiveChoiceBlock({
  messageId,
  blockIndex,
}: {
  messageId: string;
  blockIndex: number;
}) {
  const message = useChatStore((s) =>
    s.getActiveSession()?.messages.find((item) => item.id === messageId),
  );
  const block = message?.blocks?.[blockIndex];
  if (!block || block.kind !== "choice") return null;
  return (
    <ChoiceBlock
      messageId={messageId}
      blockIndex={blockIndex}
      question={block.question}
      options={block.options}
      answerIdx={block.answerIdx}
      explanation={block.explanation}
    />
  );
}

function seedChoice(block: Extract<ChatBlock, { kind: "choice" }> = choiceBlock) {
  const store = useChatStore.getState();
  store.addMessage("assistant", "", { roleId: "xiaoD" });
  const session = store.getActiveSession();
  const messageId = session!.messages[0]!.id;
  store.appendMessageBlock(messageId, block);
  return { messageId, blockIndex: 0 };
}

describe("ChoiceBlock implicit Agent turn", () => {
  beforeEach(() => {
    streamChatReplyMock.mockReset();
    streamChatReplyMock.mockImplementation(async (_payload, _token, onEvent) => {
      onEvent({ type: "delta", text: "收到，你选了数组。" });
      onEvent({
        type: "block_append",
        block: { kind: "callout", tone: "tip", markdown: "继续看数组基础。" },
      });
      onEvent({ type: "done", reply: "收到，你选了数组。", summary: "" });
    });
    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      localStateSaveChat: vi.fn().mockResolvedValue(true),
    };
    useChatStore.getState().resetForAccount();
  });

  it("locks the selected option and sends a hidden choice answer to the Agent", async () => {
    const { messageId, blockIndex } = seedChoice();
    render(<ActiveChoiceBlock messageId={messageId} blockIndex={blockIndex} />);

    fireEvent.click(screen.getByRole("radio", { name: /数组/ }));

    await waitFor(() => expect(streamChatReplyMock).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("radio", { name: /数组/ }).getAttribute("aria-checked"))
      .toBe("true");

    const payload = streamChatReplyMock.mock.calls[0]![0];
    expect(payload.messages).toHaveLength(1);
    expect(payload.messages[0]).toMatchObject({ role: "user" });
    expect(payload.messages[0].content).toContain("题目：下一步练什么？");
    expect(payload.messages[0].content).toContain("A. 数组");
    expect(payload.messages[0].content).toContain("B. 链表");
    expect(payload.messages[0].content).toContain("用户选择：A. 数组");
    expect(payload.messages[0].content).toContain("原解析：根据薄弱点选择。");

    await waitFor(() => {
      const session = useChatStore.getState().getActiveSession();
      expect(session?.messages.filter((message) => message.role === "user")).toHaveLength(0);
      expect(
        session?.messages.some(
          (message) =>
            message.role === "assistant" &&
            message.content.includes("收到，你选了数组。") &&
            message.blocks?.some((block) => block.kind === "callout"),
        ),
      ).toBe(true);
    });
  });

  it("does not resend an already answered choice", async () => {
    const { messageId, blockIndex } = seedChoice({
      ...choiceBlock,
      answerIdx: 0,
    });
    render(<ActiveChoiceBlock messageId={messageId} blockIndex={blockIndex} />);

    fireEvent.click(screen.getByRole("radio", { name: /数组/ }));

    expect(streamChatReplyMock).not.toHaveBeenCalled();
  });

  it("disables unanswered options while another Agent turn is active", () => {
    const { messageId, blockIndex } = seedChoice();
    const taskId = useChatStore.getState().startAiWork("manual");
    render(<ActiveChoiceBlock messageId={messageId} blockIndex={blockIndex} />);

    const option = screen.getByRole("radio", { name: /数组/ });
    expect((option as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(option);

    expect(streamChatReplyMock).not.toHaveBeenCalled();
    useChatStore.getState().finishAiWork(taskId);
  });
});
