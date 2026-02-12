import type { ChatMessage } from "@/types/chat";

interface MessageBubbleProps {
  message: ChatMessage;
}

export function MessageBubble({ message }: MessageBubbleProps) {
  const isUser = message.role === "user";
  const isSystem = message.role === "system";

  if (isSystem) {
    return (
      <div className="flex justify-center py-2">
        <span className="rounded-full bg-[var(--accent-soft)] px-3 py-1 text-[11px] text-[var(--text-muted)]">
          {message.content}
        </span>
      </div>
    );
  }

  return (
    <div
      className={`flex w-full py-1 ${isUser ? "justify-end" : "justify-start"}`}
    >
      <div
        className={`max-w-[75%] rounded-2xl px-3.5 py-2.5 text-[13px] leading-relaxed ${
          isUser
            ? "rounded-br-sm bg-[var(--accent)] text-white"
            : "rounded-bl-sm bg-white/70 text-[var(--text-primary)] shadow-sm"
        }`}
      >
        <p className="whitespace-pre-wrap break-words">{message.content}</p>
        <time
          className={`mt-1 block text-right text-[10px] leading-none ${
            isUser ? "text-white/50" : "text-[var(--text-muted)]"
          }`}
        >
          {new Date(message.timestamp).toLocaleTimeString("zh-CN", {
            hour: "2-digit",
            minute: "2-digit",
          })}
        </time>
      </div>
    </div>
  );
}
