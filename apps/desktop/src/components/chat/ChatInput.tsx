import { useState, useRef, useCallback } from "react";
import { useChatStore } from "@/stores/chatStore";

export function ChatInput() {
  const [text, setText] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const addMessage = useChatStore((s) => s.addMessage);
  const clearContext = useChatStore((s) => s.clearContext);

  const handleSend = useCallback(() => {
    const trimmed = text.trim();
    if (!trimmed) return;

    addMessage("user", trimmed);
    setText("");

    // Mock assistant reply after short delay
    setTimeout(() => {
      addMessage(
        "assistant",
        "这是一条模拟回复。后端 API 接入后，这里将显示 AI 助手的真实回答。",
      );
    }, 600);

    // Reset textarea height
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }
  }, [text, addMessage]);

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
    <div className="shrink-0 border-t border-[var(--border)] px-4 pb-3 pt-2">
      <div className="flex items-end gap-1.5 rounded-xl bg-white/50 px-2 py-1.5 shadow-sm ring-1 ring-black/[0.04] focus-within:ring-[var(--accent)]/20">
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            handleInput();
          }}
          onKeyDown={handleKeyDown}
          placeholder="输入消息..."
          rows={1}
          className="max-h-[120px] min-h-[32px] flex-1 resize-none bg-transparent px-1.5 py-1 text-[13px] text-[var(--text-primary)] outline-none placeholder:text-[var(--text-muted)]"
        />
        <div className="flex shrink-0 items-center gap-0.5 pb-0.5">
          <button
            onClick={clearContext}
            className="transition-all-smooth flex h-7 w-7 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-hover)] hover:text-[var(--text-secondary)]"
            title="清空上下文"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
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
            className="transition-all-smooth flex h-7 w-7 items-center justify-center rounded-md bg-[var(--accent)] text-white hover:bg-[var(--accent-light)] disabled:opacity-25 disabled:hover:bg-[var(--accent)]"
            title="发送"
          >
            <svg
              width="14"
              height="14"
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
    </div>
  );
}
