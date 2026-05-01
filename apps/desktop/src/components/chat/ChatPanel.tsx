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
  const createAssistantDraft = useChatStore((s) => s.createAssistantDraft);
  const appendMessageContent = useChatStore((s) => s.appendMessageContent);
  const appendMessageReasoning = useChatStore((s) => s.appendMessageReasoning);
  const upsertMessageToolActivity = useChatStore((s) => s.upsertMessageToolActivity);
  const finalizeMessageReasoning = useChatStore((s) => s.finalizeMessageReasoning);
  const finalizeMessage = useChatStore((s) => s.finalizeMessage);
  const removeMessage = useChatStore((s) => s.removeMessage);
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
    onStreamStart: createAssistantDraft,
    onStreamDelta: appendMessageContent,
    onStreamReasoning: appendMessageReasoning,
    onStreamReasoningDone: finalizeMessageReasoning,
    onStreamToolActivity: upsertMessageToolActivity,
    onStreamDone: (messageId) => finalizeMessage(messageId),
    onStreamEmpty: removeMessage,
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
