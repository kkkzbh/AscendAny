import { useEffect, useRef } from "react";
import { useChatStore } from "@/stores/chatStore";
import { MessageBubble } from "./MessageBubble";
import { AssistantWorkingCard } from "./AssistantWorkingCard";

export function MessageList() {
  const messages = useChatStore((s) => s.session.messages);
  const isAiWorking = useChatStore((s) => s.isAiWorking);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length, isAiWorking]);

  if (messages.length === 0 && !isAiWorking) {
    return (
      <div className="message-list-shell animate-fade-in flex h-full flex-col items-center justify-center gap-4 text-[var(--text-muted)]">
        <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-[var(--surface-soft)] ring-1 ring-[var(--border-subtle)]">
          <svg
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="var(--accent-600)"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
          </svg>
        </div>
        <div className="space-y-1 text-center">
          <p className="text-sm font-semibold text-[var(--text-strong)]">
            开始对话
          </p>
          <p className="text-xs text-[var(--text-soft)]">
            问我考试表现、能力变化和下次训练重点
          </p>
        </div>
      </div>
    );
  }

  const contentClassName = messages.length === 0
    ? "flex min-h-full flex-col items-center justify-center"
    : "space-y-1";

  return (
    <div className="message-list-shell flex-1 overflow-y-auto">
      <div className={contentClassName}>
        {messages.map((msg) => (
          <MessageBubble key={msg.id} message={msg} />
        ))}
        {isAiWorking ? <AssistantWorkingCard /> : null}
      </div>
      <div ref={bottomRef} />
    </div>
  );
}
