import { describe, expect, it } from "vitest";

import { toOutboundChatMessage } from "@/components/chat/ChatInput";
import type { ChatMessage } from "@/types/chat";

describe("ChatInput outbound message serialization", () => {
  it("sends assistant reasoning but excludes transient UI fields", () => {
    const message: ChatMessage = {
      id: "msg_1",
      role: "assistant",
      content: "最终回答",
      reasoningContent: "DeepSeek 后续请求需要的思考内容",
      timestamp: 1710000000000,
      streaming: false,
      reasoningStreaming: false,
    };

    const payload = toOutboundChatMessage(message);

    expect(payload).toEqual({
      role: "assistant",
      content: "最终回答",
      reasoningContent: "DeepSeek 后续请求需要的思考内容",
    });
    expect(payload).not.toHaveProperty("streaming");
    expect(payload).not.toHaveProperty("reasoningStreaming");
  });
});
