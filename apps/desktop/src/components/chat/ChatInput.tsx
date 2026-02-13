import { useState, useRef, useCallback, useEffect } from "react";
import { useChatStore } from "@/stores/chatStore";

export function ChatInput() {
  const [text, setText] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const timerRef = useRef<number[]>([]);
  const addMessage = useChatStore((s) => s.addMessage);
  const clearContext = useChatStore((s) => s.clearContext);

  useEffect(() => {
    return () => {
      timerRef.current.forEach((timer) => window.clearTimeout(timer));
      timerRef.current = [];
    };
  }, []);

  const handleSend = useCallback(() => {
    const trimmed = text.trim();
    if (!trimmed) return;

    addMessage("user", trimmed);
    setText("");

    const timer = window.setTimeout(() => {
      addMessage(
        "assistant",
        "这是一条模拟回复。后端 API 接入后，这里将显示 AI 助手的真实回答。",
      );
      timerRef.current = timerRef.current.filter((id) => id !== timer);
    }, 600);
    timerRef.current.push(timer);

    // Reset textarea height
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }
  }, [text, addMessage]);

  const handleClear = useCallback(() => {
    timerRef.current.forEach((timer) => window.clearTimeout(timer));
    timerRef.current = [];
    clearContext();
  }, [clearContext]);

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
          placeholder="输入消息，按 Enter 发送..."
          rows={1}
          className="chat-textarea max-h-[120px] min-h-[40px] flex-1 resize-none bg-transparent text-[13px] leading-relaxed text-[var(--text-strong)] outline-none placeholder:text-[var(--text-soft)]"
        />
        <div className="flex shrink-0 items-center gap-1.5 pb-0.5">
          <button
            onClick={handleClear}
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
          <button
            onClick={handleSend}
            disabled={!text.trim()}
            className="send-button flex h-8 w-8 items-center justify-center rounded-lg text-white disabled:opacity-30 disabled:shadow-none"
            title="发送"
          >
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
          </button>
        </div>
      </div>

      <p className="chat-input-hint mt-1.5 text-[10px] text-[var(--text-soft)]">
        Shift + Enter 换行
      </p>
    </div>
  );
}
