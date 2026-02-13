import type { ChatMessage } from "@/types/chat";

interface MessageBubbleProps {
  message: ChatMessage;
}

export function MessageBubble({ message }: MessageBubbleProps) {
  const isUser = message.role === "user";
  const isSystem = message.role === "system";

  const timeStr = new Date(message.timestamp).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
  });

  if (isSystem) {
    return (
      <div className="message-row flex justify-center py-3">
        <span className="rounded-full bg-[var(--surface-soft)] px-3.5 py-1 text-[11px] font-medium text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]">
          {message.content}
        </span>
      </div>
    );
  }

  return (
    <div
      className={`message-row flex w-full items-start gap-2.5 py-1.5 ${isUser ? "justify-end" : "justify-start"}`}
    >
      {!isUser && (
        <div className="mt-1 flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-[var(--accent-700)] to-[var(--accent-500)] shadow-[0_8px_20px_rgba(3,105,161,0.24)]">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12 2L2 7l10 5 10-5-10-5z" />
            <path d="M2 17l10 5 10-5" />
            <path d="M2 12l10 5 10-5" />
          </svg>
        </div>
      )}

      <div className="flex max-w-[72%] flex-col gap-1">
        <div
          className={`message-bubble rounded-[18px] text-[13px] leading-relaxed ${
            isUser
              ? "bg-gradient-to-br from-[var(--accent-600)] to-[var(--accent-500)] text-white shadow-[0_10px_26px_rgba(3,105,161,0.24)]"
              : "bg-[var(--surface-raised)] text-[var(--text-strong)] ring-1 ring-[var(--border-subtle)]"
          }`}
        >
          <p className="whitespace-pre-wrap break-words leading-6">{message.content}</p>
        </div>
        <time
          className={`px-1 text-[10px] leading-none ${
            isUser ? "text-right text-[var(--text-soft)]" : "text-left text-[var(--text-soft)]"
          }`}
        >
          {timeStr}
        </time>
      </div>

      {isUser && (
        <div className="mt-1 flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[var(--surface-raised)] text-[var(--text-soft)] ring-1 ring-[var(--border-subtle)]">
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M20 21a8 8 0 0 0-16 0" />
            <circle cx="12" cy="7" r="4" />
          </svg>
        </div>
      )}
    </div>
  );
}
