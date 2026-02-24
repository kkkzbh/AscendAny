import { useCallback } from "react";
import { MessageList } from "./MessageList";
import { ChatInput, type ChatInputProps } from "./ChatInput";
import { useChatStore } from "@/stores/chatStore";
import { useAutoAnalysis } from "@/hooks/useAutoAnalysis";

interface ChatPanelProps extends ChatInputProps {}

export function ChatPanel({
  showClearButton,
  sendVariant,
  sendLabel,
}: ChatPanelProps = {}) {
  const addMessage = useChatStore((s) => s.addMessage);
  const startAiWork = useChatStore((s) => s.startAiWork);
  const finishAiWork = useChatStore((s) => s.finishAiWork);

  const handleAutoAnalysis = useCallback(
    (reply: string, roleId: string) => {
      addMessage("assistant", reply, { roleId });
    },
    [addMessage],
  );

  const handleAutoWorkStart = useCallback(
    () => startAiWork("auto"),
    [startAiWork],
  );

  const handleAutoWorkEnd = useCallback(
    (taskId: string | undefined) => {
      if (!taskId) return;
      finishAiWork(taskId);
    },
    [finishAiWork],
  );

  useAutoAnalysis({
    onReply: handleAutoAnalysis,
    onWorkStart: handleAutoWorkStart,
    onWorkEnd: handleAutoWorkEnd,
  });

  return (
    <section className="flex h-full w-full flex-col">
      <MessageList />
      <ChatInput
        showClearButton={showClearButton}
        sendVariant={sendVariant}
        sendLabel={sendLabel}
      />
    </section>
  );
}
