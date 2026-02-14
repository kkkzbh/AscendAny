import { useCallback } from "react";
import { MessageList } from "./MessageList";
import { ChatInput } from "./ChatInput";
import { useChatStore } from "@/stores/chatStore";
import { useAutoAnalysis } from "@/hooks/useAutoAnalysis";

export function ChatPanel() {
  const addMessage = useChatStore((s) => s.addMessage);

  const handleAutoAnalysis = useCallback(
    (reply: string) => {
      addMessage("assistant", reply);
    },
    [addMessage],
  );

  useAutoAnalysis(handleAutoAnalysis);

  return (
    <section className="flex h-full w-full flex-col">
      <MessageList />
      <ChatInput />
    </section>
  );
}
