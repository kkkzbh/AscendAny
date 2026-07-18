import { useCallback, useEffect, useRef } from "react";
import { useChatStore } from "@/stores/chatStore";
import {
  toOutboundChatMessage,
  useChatStreamSender,
} from "@/hooks/useChatStreamSender";

export { toOutboundChatMessage };

export interface ChatInputProps {
  showClearButton?: boolean;
  sendVariant?: "icon" | "pill";
  sendLabel?: string;
}

export function ChatInput({
  showClearButton = false,
  sendVariant = "icon",
  sendLabel = "发送",
}: ChatInputProps = {}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const { isBlocked, sendManual } = useChatStreamSender();

  const text = useChatStore((s) =>
    s.activeSessionId
      ? s.sessions.find((session) => session.id === s.activeSessionId)?.draft ?? ""
      : s.newSessionDraft,
  );
  const setText = useChatStore((s) => s.setCurrentDraft);
  const clearContext = useChatStore((s) => s.clearContext);

  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  }, [text]);

  const handleSend = useCallback(async () => {
    const trimmed = text.trim();
    if (!trimmed || isBlocked) return;
    setText("");

    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }

    await sendManual(trimmed);
  }, [
    text,
    isBlocked,
    sendManual,
    setText,
  ]);

  const handleClear = useCallback(() => {
    if (isBlocked) return;
    clearContext();
  }, [clearContext, isBlocked]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleInput = () => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  };

  const sendButtonClassName = sendVariant === "pill"
    ? "send-button flex h-10 min-w-[146px] items-center justify-center rounded-full px-8 text-[15px] font-semibold tracking-[0.02em] text-white shadow-[0_6px_14px_rgba(43,83,255,0.18)] disabled:opacity-40 disabled:shadow-none"
    : "send-button flex h-8 w-8 items-center justify-center rounded-lg text-white disabled:opacity-30 disabled:shadow-none";

  return (
    <div className="chat-input-wrap shrink-0">
      <div className="mb-3 h-px bg-gradient-to-r from-transparent via-[var(--border)] to-transparent" />

      <div className="input-shell chat-input-shell flex items-end gap-2 rounded-[18px]">
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            handleInput();
          }}
          onKeyDown={handleKeyDown}
          aria-label="消息输入"
          rows={1}
          className="chat-textarea max-h-[120px] min-h-[40px] flex-1 resize-none bg-transparent text-[13px] leading-relaxed text-[var(--text-strong)] outline-none placeholder:text-[var(--text-soft)]"
        />
        <div className="flex shrink-0 items-center gap-1.5 pb-0.5">
          {showClearButton && (
            <button
              onClick={handleClear}
              disabled={isBlocked}
              className="ui-icon-button"
              title="清空上下文"
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
              </svg>
            </button>
          )}
          <button
            onClick={handleSend}
            disabled={!text.trim() || isBlocked}
            className={sendButtonClassName}
            title={isBlocked ? "助手处理中" : sendLabel}
          >
            {sendVariant === "pill" ? (
              <span>{sendLabel}</span>
            ) : (
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <line x1="22" y1="2" x2="11" y2="13" />
                <polygon points="22 2 15 22 11 13 2 9 22 2" />
              </svg>
            )}
          </button>
        </div>
      </div>

    </div>
  );
}
